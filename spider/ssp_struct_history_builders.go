package spider

import (
	"math"
	"nofx/market"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SSP + Struct + History processing helpers
// ----------------------------------------
//
// This package provides lightweight, production-friendly utilities to build:
//
// 1) struct_ctx:
//    - For 1m / 5m / 15m bars
//    - Outputs: {"trend": "UP|DOWN|RANGE", "last_swings": [...]}
//    - Detects local pivot highs/lows and labels HH/HL/LH/LL
//    - Does NOT truncate bars internally (assumes upstream already trimmed).
//
// 2) history_ctx (LLM-friendly):
//    - Compresses raw SSP snapshots & signal logs
//    - Keeps only the last SSP snapshot + last N signals for Agent inputs
//    - Leaves the full logs for local storage or analytics.
//
// Designed to align with Agent1 pipeline:
// - Upstream: compute SSP & C-signals per tick/frame
// - This package: compress & shape struct_ctx/history_ctx for LLM
// - Downstream: execution/risk/trailing handled elsewhere.

//
// ---------------------------
// Data structures
// ---------------------------
//

//type KlineBar struct {
//	// Minimal bar structure required for swing labelling.
//	Ts     time.Time `json:"ts"`
//	Open   float64   `json:"open"`
//	High   float64   `json:"high"`
//	Low    float64   `json:"low"`
//	Close  float64   `json:"close"`
//	Volume float64   `json:"volume,omitempty"`
//}

type SSPSnapshot struct {
	// A lightweight SSP snapshot for history compression.
	TsMS int64     `json:"ts_ms"`
	P    float64   `json:"P"`
	SSP  []float64 `json:"SSP"`
}

type SignalRecord struct {
	// A minimal signal record used for history compression.
	TsMS      int64    `json:"ts_ms"`
	Symbol    string   `json:"symbol"`
	Decision  string   `json:"decision"` // "LONG"|"SHORT"|"WAIT"
	Mode      string   `json:"mode"`     // "RANGE_REVERT"|"BREAKOUT_FOLLOW"|"NONE"
	AnchorSSP *float64 `json:"anchor_ssp,omitempty"`
	TestLevel *float64 `json:"test_level,omitempty"`
	Summary   string   `json:"summary,omitempty"`
}

//
// ---------------------------
// SSP utilities
// ---------------------------
//

// ToFloatSlice is a best-effort converter similar to the Python _to_float_list.
// It accepts a slice of arbitrary values (numbers or strings) and returns []float64.
func ToFloatSlice(values []interface{}) []float64 {
	out := make([]float64, 0, len(values))
	for _, v := range values {
		if f, ok := toFloat64(v); ok {
			out = append(out, f)
		}
	}
	return out
}

// SSPSortedUnique deduplicates + sorts SSP prices.
func SSPSortedUnique(ssp []float64) []float64 {
	if len(ssp) == 0 {
		return nil
	}
	m := make(map[float64]struct{}, len(ssp))
	for _, v := range ssp {
		m[v] = struct{}{}
	}
	out := make([]float64, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Float64s(out)
	return out
}

type AnchorView struct {
	S0         *float64 `json:"S0,omitempty"`
	R0         *float64 `json:"R0,omitempty"`
	AnchorSSP  *float64 `json:"anchor_ssp,omitempty"`
	DistAnchor *float64 `json:"dist_anchor,omitempty"`
}

// ComputeS0R0Anchor computes nearest support (S0), nearest resistance (R0),
// anchor_ssp (nearest SSP level to price) and dist_anchor (absolute distance).
func ComputeS0R0Anchor(P float64, ssp []float64) AnchorView {
	list := SSPSortedUnique(ssp)
	if len(list) == 0 {
		return AnchorView{}
	}

	var (
		haveBelow bool
		s0        float64
		haveAbove bool
		r0        float64
	)

	for _, lvl := range list {
		if lvl < P {
			if !haveBelow || lvl > s0 {
				haveBelow = true
				s0 = lvl
			}
		}
		if lvl > P {
			if !haveAbove || lvl < r0 {
				haveAbove = true
				r0 = lvl
			}
		}
	}

	anchor := list[0]
	minDist := math.Abs(anchor - P)
	for i := 1; i < len(list); i++ {
		d := math.Abs(list[i] - P)
		if d < minDist {
			minDist = d
			anchor = list[i]
		}
	}

	var res AnchorView
	if haveBelow {
		res.S0 = floatPtr(s0)
	}
	if haveAbove {
		res.R0 = floatPtr(r0)
	}
	res.AnchorSSP = floatPtr(anchor)
	res.DistAnchor = floatPtr(minDist)
	return res
}

//
// ---------------------------
// Struct (HH/HL/LH/LL) builder
// ---------------------------
//

type SwingPoint struct {
	TsMS  int64   `json:"ts"`
	Price float64 `json:"price"`
	Label string  `json:"label"` // "HH", "LH", "HL", "LL"
}

type StructResult struct {
	Trend      string       `json:"trend"`       // "UP"|"DOWN"|"RANGE"
	LastSwings []SwingPoint `json:"last_swings"` // chronological
}

// BuildStructForBars builds a simple struct entry for one timeframe.
//
// Conventions:
//   - 'trend' is derived from the most recent `trendLookbackSwings` pivot prices.
//   - 'last_swings' is a chronological list of pivot points with labels:
//     HH / LH for highs, HL / LL for lows.
//
// Notes:
// - No internal bar truncation.
// - Requires at least 3 valid bars to detect local pivots.
func BuildStructForBars(
	bars []market.InputKline,
	maxSwings int,
	trendLookbackSwings int,
) StructResult {
	valid := make([]market.InputKline, 0, len(bars))
	for _, b := range bars {
		if !(b.TS == 0) {
			valid = append(valid, b)
		}
	}

	sort.Slice(valid, func(i, j int) bool {
		return valid[i].TS < valid[j].TS
	})

	if len(valid) < 3 {
		return StructResult{
			Trend:      "RANGE",
			LastSwings: nil,
		}
	}

	swings := make([]SwingPoint, 0, len(valid))
	var (
		lastHighPrice float64
		lastLowPrice  float64
		haveHigh      bool
		haveLow       bool
	)

	for i := 1; i < len(valid)-1; i++ {
		prevBar := valid[i-1]
		bar := valid[i]
		nextBar := valid[i+1]

		tsMS := bar.TS

		high := bar.H
		low := bar.L

		// Local high
		if high >= prevBar.H && high >= nextBar.H &&
			(high > prevBar.H || high > nextBar.H) {

			label := "LH"
			if !haveHigh || high > lastHighPrice {
				label = "HH"
			}
			swings = append(swings, SwingPoint{
				TsMS:  tsMS,
				Price: high,
				Label: label,
			})
			lastHighPrice = high
			haveHigh = true
		}

		// Local low
		if low <= prevBar.L && low <= nextBar.L &&
			(low < prevBar.L || low < nextBar.L) {

			labelLow := "LL"
			if !haveLow || low > lastLowPrice {
				labelLow = "HL"
			}
			swings = append(swings, SwingPoint{
				TsMS:  tsMS,
				Price: low,
				Label: labelLow,
			})
			lastLowPrice = low
			haveLow = true
		}
	}

	// Ensure chronological order
	sort.Slice(swings, func(i, j int) bool {
		return swings[i].TsMS < swings[j].TsMS
	})

	if maxSwings > 0 && len(swings) > maxSwings {
		swings = swings[len(swings)-maxSwings:]
	}

	// Trend detection
	trend := "RANGE"
	if len(swings) > 0 {
		k := trendLookbackSwings
		if k < 2 {
			k = 2
		}
		if k > len(swings) {
			k = len(swings)
		}
		if k >= 2 {
			recent := swings[len(swings)-k:]
			firstPrice := recent[0].Price
			lastPrice := recent[len(recent)-1].Price
			if lastPrice > firstPrice {
				trend = "UP"
			} else if lastPrice < firstPrice {
				trend = "DOWN"
			}
		}
	}

	return StructResult{
		Trend:      trend,
		LastSwings: swings,
	}
}

type StructCtx struct {
	Struct1m  StructResult `json:"struct_1m"`
	Struct5m  StructResult `json:"struct_5m"`
	Struct15m StructResult `json:"struct_15m"`
}

// BuildStructCtx builds struct_ctx for 1m/5m/15m.
//
// Assumes upstream already limits bar counts, e.g.:
// - 15m: 5 bars
// - 5m : 10 bars
// - 1m : 15 bars
func BuildStructCtx(
	bars1m []market.InputKline,
	bars5m []market.InputKline,
	bars15m []market.InputKline,
	maxSwings int,
	trendLookback1m int,
	trendLookback5m int,
	trendLookback15m int,
) StructCtx {
	return StructCtx{
		Struct1m: BuildStructForBars(
			bars1m, maxSwings, trendLookback1m,
		),
		Struct5m: BuildStructForBars(
			bars5m, maxSwings, trendLookback5m,
		),
		Struct15m: BuildStructForBars(
			bars15m, maxSwings, trendLookback15m,
		),
	}
}

//
// ---------------------------
// History compression for Agent
// ---------------------------
//

type HistoryCtx struct {
	LastSSPSnapshot *SSPSnapshot   `json:"last_ssp_snapshot,omitempty"`
	RecentSignals   []SignalRecord `json:"recent_signals"`
}

// BuildHistoryCtxForAgent compresses raw history into LLM-friendly history_ctx.
//
// Keeps only:
// - last_ssp_snapshot
// - recent_signals (last N)
func BuildHistoryCtxForAgent(
	sspSnapshots []SSPSnapshot,
	signalLog []SignalRecord,
	keepRecentSignals int,
) HistoryCtx {
	var lastSSP *SSPSnapshot
	if len(sspSnapshots) > 0 {
		last := sspSnapshots[len(sspSnapshots)-1]
		lastSSP = &last
	}

	n := keepRecentSignals
	if n < 0 {
		n = 0
	}

	var recent []SignalRecord
	if len(signalLog) > 0 && n > 0 {
		if n > len(signalLog) {
			n = len(signalLog)
		}
		recent = append([]SignalRecord(nil), signalLog[len(signalLog)-n:]...)
	} else {
		recent = []SignalRecord{}
	}

	return HistoryCtx{
		LastSSPSnapshot: lastSSP,
		RecentSignals:   recent,
	}
}

type PrevSignalCtx struct {
	TsMS      int64    `json:"ts_ms"`
	Symbol    string   `json:"symbol"`
	Decision  string   `json:"decision"`
	Mode      string   `json:"mode"`
	AnchorSSP *float64 `json:"anchor_ssp,omitempty"`
	TestLevel *float64 `json:"test_level,omitempty"`
}

// BuildPrevSignalCtx builds a minimal prev_signal_ctx for the next Agent call.
func BuildPrevSignalCtx(
	lastSignal *SignalRecord,
	lastSSPSnapshot *SSPSnapshot,
) *PrevSignalCtx {
	if lastSignal == nil {
		return nil
	}

	ctx := &PrevSignalCtx{
		TsMS:      lastSignal.TsMS,
		Symbol:    lastSignal.Symbol,
		Decision:  lastSignal.Decision,
		Mode:      lastSignal.Mode,
		AnchorSSP: lastSignal.AnchorSSP,
		TestLevel: lastSignal.TestLevel,
	}

	if (ctx.AnchorSSP == nil || ctx.TestLevel == nil) && lastSSPSnapshot != nil {
		view := ComputeS0R0Anchor(lastSSPSnapshot.P, lastSSPSnapshot.SSP)
		if ctx.AnchorSSP == nil {
			ctx.AnchorSSP = view.AnchorSSP
		}
		if ctx.TestLevel == nil {
			// Mirror Python: default test_level to anchor_ssp as well.
			ctx.TestLevel = view.AnchorSSP
		}
	}

	return ctx
}

//
// ---------------------------
// Example adapters (optional)
// ---------------------------
//

// AdaptRawBar adapts a raw dict-like map into KlineBar.
//
// Accepts flexible time keys:
//   - "ts" / "time" / "timestamp" (ms or ISO string)
//func AdaptRawBar(d map[string]interface{}) KlineBar {
//	var ts time.Time
//
//	// time key
//	var tsVal interface{}
//	if v, ok := d["ts"]; ok {
//		tsVal = v
//	} else if v, ok := d["time"]; ok {
//		tsVal = v
//	} else if v, ok := d["timestamp"]; ok {
//		tsVal = v
//	}
//
//	switch v := tsVal.(type) {
//	case time.Time:
//		ts = v
//	case int64:
//		ts = unixMilliToTime(v)
//	case int:
//		ts = unixMilliToTime(int64(v))
//	case float64:
//		ts = unixMilliToTime(int64(v))
//	case string:
//		ts = parseTimeFlexible(v)
//	default:
//		ts = time.Unix(0, 0).UTC()
//	}
//
//	return KlineBar{
//		Ts:     ts,
//		Open:   mustFloat(d["open"]),
//		High:   mustFloat(d["high"]),
//		Low:    mustFloat(d["low"]),
//		Close:  mustFloat(d["close"]),
//		Volume: mustFloat(d["volume"]),
//	}
//}

// AdaptBars is a bulk adapter for bars.
//func AdaptBars(raw []map[string]interface{}) []KlineBar {
//	out := make([]KlineBar, 0, len(raw))
//	for _, m := range raw {
//		out = append(out, AdaptRawBar(m))
//	}
//	return out
//}

// MakeSSPSnapshot is a convenience creator for SSPSnapshot with best-effort timestamp.
// If tsMS <= 0, it uses current UTC time in milliseconds.
func MakeSSPSnapshot(P float64, SSP []float64, tsMS int64) SSPSnapshot {
	if tsMS <= 0 {
		tsMS = time.Now().UTC().UnixNano() / int64(time.Millisecond)
	}
	out := SSPSnapshot{
		TsMS: tsMS,
		P:    P,
		SSP:  make([]float64, len(SSP)),
	}
	copy(out.SSP, SSP)
	return out
}

//
// ---------------------------
// Internal helpers
// ---------------------------
//

func floatPtr(v float64) *float64 {
	return &v
}

func toFloat64(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case int32:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint64:
		return float64(t), true
	case uint32:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func mustFloat(v interface{}) float64 {
	f, ok := toFloat64(v)
	if !ok {
		return 0
	}
	return f
}

func unixMilliToTime(ms int64) time.Time {
	sec := ms / 1000
	nsec := (ms % 1000) * int64(time.Millisecond)
	return time.Unix(sec, nsec).UTC()
}

func parseTimeFlexible(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Unix(0, 0).UTC()
	}

	// Try RFC3339 / ISO-8601 with Z
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}

	// Try without timezone, e.g. "2006-01-02T15:04:05"
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t
	}

	return time.Unix(0, 0).UTC()
}

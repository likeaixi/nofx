package spider

// Package sspbox implements the same Super-Box + drift enrichment logic as your Python code.
//
// Notes:
// - This implementation is dependency-free (no external decimal lib).
// - It’s designed to work well with encoding/json decoded data.
//   Recommended: json.Decoder.UseNumber() to preserve numeric fidelity.
// - Prices are assumed to be non-negative (BTCUSDT-like). Floor for negatives is still handled correctly.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------
// Robust numeric helpers
// ---------------------------

// toDecimalString converts common JSON-ish number types into a string we can parse with big.Rat.
// Supported inputs: json.Number, float64, float32, int*, uint*, string, fmt.Stringer, others via fmt.Sprint.
func toDecimalString(x any) (string, error) {
	switch v := x.(type) {
	case nil:
		return "", errors.New("nil numeric")
	case json.Number:
		return v.String(), nil
	case float64:
		// avoid scientific form when possible
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 64), nil
	case int:
		return strconv.FormatInt(int64(v), 10), nil
	case int8:
		return strconv.FormatInt(int64(v), 10), nil
	case int16:
		return strconv.FormatInt(int64(v), 10), nil
	case int32:
		return strconv.FormatInt(int64(v), 10), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return "", errors.New("empty string numeric")
		}
		return s, nil
	default:
		// Last resort: fmt.Sprint
		s := strings.TrimSpace(fmt.Sprint(v))
		if s == "" {
			return "", errors.New("empty fmt numeric")
		}
		return s, nil
	}
}

func parseRat(x any) (*big.Rat, error) {
	s, err := toDecimalString(x)
	if err != nil {
		return nil, err
	}

	// big.Rat.SetString supports:
	// - integers: "123"
	// - rationals: "3/4"
	// - decimals: "123.45"
	r := new(big.Rat)
	if _, ok := r.SetString(s); ok {
		return r, nil
	}

	// Some inputs might be like "85830.65000000" -> ok.
	// If not ok, try to sanitize commas etc.
	s2 := strings.ReplaceAll(s, ",", "")
	if _, ok := r.SetString(s2); ok {
		return r, nil
	}

	return nil, fmt.Errorf("cannot parse numeric: %q", s)
}

// floorRatToInt64 returns floor(r) as int64, correct for negative values too.
func floorRatToInt64(r *big.Rat) int64 {
	// q = trunc(num/den) toward zero, then adjust if negative and has remainder
	num := new(big.Int).Set(r.Num())
	den := new(big.Int).Set(r.Denom())

	q := new(big.Int)
	rem := new(big.Int)
	q.QuoRem(num, den, rem)

	if r.Sign() < 0 && rem.Sign() != 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q.Int64()
}

func floorPrice(sspRaw any) (int, error) {
	r, err := parseRat(sspRaw)
	if err != nil {
		return 0, err
	}
	return int(floorRatToInt64(r)), nil
}

func parsePrice(P any) (int, error) {
	// same behavior: floor to integer price level
	r, err := parseRat(P)
	if err != nil {
		return 0, err
	}
	return int(floorRatToInt64(r)), nil
}

// ---------------------------
// Core structures
// ---------------------------

type Band struct {
	Low              int     `json:"low"`
	High             int     `json:"high"`
	Center           float64 `json:"center"`
	Width            int     `json:"width"`
	DensityHintPrice *int    `json:"density_hint_price,omitempty"`
	DensityHintCount *int    `json:"density_hint_count,omitempty"`
}

type SuperBox struct {
	BoxLow   int `json:"box_low"`
	BoxHigh  int `json:"box_high"`
	BoxWidth int `json:"box_width"`
	EdgeLow  int `json:"edge_low"`
	EdgeHigh int `json:"edge_high"`

	PosBox *float64 `json:"pos_box,omitempty"` // nil if OUT
	Zone   string   `json:"zone"`              // BATTLE / EDGE_LOW / EDGE_HIGH / OUT_UP / OUT_DOWN

	S0    *int   `json:"s0,omitempty"`
	R0    *int   `json:"r0,omitempty"`
	Bands []Band `json:"bands"`
}

// ---------------------------
// Bands / Super-Box builder
// ---------------------------

func buildDensityMap(sspList []float64) (map[int]int, error) {
	dm := make(map[int]int, len(sspList))
	for _, raw := range sspList {
		p, err := floorPrice(raw)
		if err != nil {
			return nil, err
		}
		dm[p]++
	}
	return dm, nil
}

func buildSSPPricesSorted(sspList []float64) ([]int, error) {
	set := make(map[int]struct{}, len(sspList))
	for _, x := range sspList {
		p, err := floorPrice(x)
		if err != nil {
			return nil, err
		}
		set[p] = struct{}{}
	}
	prices := make([]int, 0, len(set))
	for p := range set {
		prices = append(prices, p)
	}
	sort.Ints(prices)
	return prices, nil
}

// DenseSegment: adjacency gap < 120 continues, else break.
// Returns list of (seg_low, seg_high).
type seg struct{ low, high int }

func buildDenseSegments(pricesSorted []int) []seg {
	if len(pricesSorted) == 0 {
		return nil
	}
	segs := make([]seg, 0, 8)
	curLow := pricesSorted[0]
	curHigh := pricesSorted[0]
	for _, p := range pricesSorted[1:] {
		if (p - curHigh) < 120 {
			curHigh = p
		} else {
			segs = append(segs, seg{low: curLow, high: curHigh})
			curLow, curHigh = p, p
		}
	}
	segs = append(segs, seg{low: curLow, high: curHigh})
	return segs
}

// Band merge rule:
// - seg_gap = next.low - cur.high
// - seg_gap > 300 => split (new band)
// - seg_gap <= 300 => merge into same band
type bandRange struct{ low, high int }

func mergeSegmentsToBands(segs []seg) []bandRange {
	if len(segs) == 0 {
		return nil
	}
	out := make([]bandRange, 0, len(segs))
	curLow, curHigh := segs[0].low, segs[0].high
	for _, s := range segs[1:] {
		segGap := s.low - curHigh
		if segGap > 300 {
			out = append(out, bandRange{low: curLow, high: curHigh})
			curLow, curHigh = s.low, s.high
		} else {
			if s.high > curHigh {
				curHigh = s.high
			}
		}
	}
	out = append(out, bandRange{low: curLow, high: curHigh})
	return out
}

func buildBands(sspPricesSorted []int, densityMap map[int]int) []Band {
	segs := buildDenseSegments(sspPricesSorted)
	bandRanges := mergeSegmentsToBands(segs)

	// deterministic iteration over density keys (for tie-breaking)
	dKeys := make([]int, 0, len(densityMap))
	for p := range densityMap {
		dKeys = append(dKeys, p)
	}
	sort.Ints(dKeys)

	bands := make([]Band, 0, len(bandRanges))
	for _, br := range bandRanges {
		low, high := br.low, br.high
		width := high - low
		center := float64(low+high) / 2.0

		var hintP *int
		var hintC *int
		bestC := math.MinInt

		for _, p := range dKeys {
			if p < low || p > high {
				continue
			}
			c := densityMap[p]
			// max count; if tie, choose smaller price (due to sorted order)
			if c > bestC {
				bestC = c
				pp := p
				cc := c
				hintP = &pp
				hintC = &cc
			}
		}

		bands = append(bands, Band{
			Low:              low,
			High:             high,
			Center:           center,
			Width:            width,
			DensityHintPrice: hintP,
			DensityHintCount: hintC,
		})
	}
	return bands
}

// Pick B_near: containing band preferred; else minimal distance to band center.
func pickNearestBand(bands []Band, pInt int) (int, error) {
	if len(bands) == 0 {
		return 0, errors.New("no bands to pick from")
	}

	containing := make([]int, 0, 2)
	for i, b := range bands {
		if b.Low <= pInt && pInt <= b.High {
			containing = append(containing, i)
		}
	}

	if len(containing) > 0 {
		sort.Slice(containing, func(i, j int) bool {
			return bands[containing[i]].Width < bands[containing[j]].Width
		})
		return containing[0], nil
	}

	bestIdx := 0
	bestDist := math.Abs(float64(pInt) - bands[0].Center)
	for i := 1; i < len(bands); i++ {
		d := math.Abs(float64(pInt) - bands[i].Center)
		if d < bestDist {
			bestDist = d
			bestIdx = i
		}
	}
	return bestIdx, nil
}

// Super-Box expansion:
// - Start from idx_near
// - Expand down/up while gap_band <= 900
// - gap_band = next.low - cur.high
func expandSuperBox(bands []Band, idxNear int) []int {
	included := map[int]struct{}{idxNear: {}}

	// down
	cur := idxNear
	for cur-1 >= 0 {
		prev := cur - 1
		gapBand := bands[cur].Low - bands[prev].High
		if gapBand > 900 {
			break
		}
		included[prev] = struct{}{}
		cur = prev
	}

	// up
	cur = idxNear
	for cur+1 < len(bands) {
		nxt := cur + 1
		gapBand := bands[nxt].Low - bands[cur].High
		if gapBand > 900 {
			break
		}
		included[nxt] = struct{}{}
		cur = nxt
	}

	idxs := make([]int, 0, len(included))
	for i := range included {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	return idxs
}

// S0: nearest below; R0: nearest above.
func findS0R0(sspPricesSorted []int, pInt int) (s0 *int, r0 *int) {
	for _, p := range sspPricesSorted {
		if p <= pInt {
			pp := p
			s0 = &pp
		} else if p > pInt && r0 == nil {
			pp := p
			r0 = &pp
			break
		}
	}
	return s0, r0
}

func BuildSuperBox(P any, SSP []float64) (SuperBox, error) {
	if len(SSP) == 0 {
		return SuperBox{}, errors.New("SSP is empty")
	}

	pInt, err := parsePrice(P)
	if err != nil {
		return SuperBox{}, err
	}

	densityMap, err := buildDensityMap(SSP)
	if err != nil {
		return SuperBox{}, err
	}

	sspPricesSorted, err := buildSSPPricesSorted(SSP)
	if err != nil {
		return SuperBox{}, err
	}

	bands := buildBands(sspPricesSorted, densityMap)

	// fallback: treat whole SSP range as a single box
	if len(bands) == 0 {
		low := sspPricesSorted[0]
		high := sspPricesSorted[len(sspPricesSorted)-1]
		boxLow, boxHigh := low, high
		boxWidth := boxHigh - boxLow
		s0, r0 := findS0R0(sspPricesSorted, pInt)

		if pInt < boxLow {
			return SuperBox{
				BoxLow: boxLow, BoxHigh: boxHigh, BoxWidth: boxWidth,
				EdgeLow: boxLow, EdgeHigh: boxHigh,
				PosBox: nil, Zone: "OUT_DOWN",
				S0: s0, R0: r0,
				Bands: nil,
			}, nil
		}
		if pInt > boxHigh {
			return SuperBox{
				BoxLow: boxLow, BoxHigh: boxHigh, BoxWidth: boxWidth,
				EdgeLow: boxLow, EdgeHigh: boxHigh,
				PosBox: nil, Zone: "OUT_UP",
				S0: s0, R0: r0,
				Bands: nil,
			}, nil
		}

		pos := float64(pInt-boxLow) / float64(maxInt(boxWidth, 1))
		zone := "BATTLE"
		if pos < 0.35 {
			zone = "EDGE_LOW"
		} else if pos > 0.65 {
			zone = "EDGE_HIGH"
		}
		posPtr := new(float64)
		*posPtr = pos

		return SuperBox{
			BoxLow: boxLow, BoxHigh: boxHigh, BoxWidth: boxWidth,
			EdgeLow: boxLow, EdgeHigh: boxHigh,
			PosBox: posPtr, Zone: zone,
			S0: s0, R0: r0,
			Bands: nil,
		}, nil
	}

	idxNear, err := pickNearestBand(bands, pInt)
	if err != nil {
		return SuperBox{}, err
	}
	idxs := expandSuperBox(bands, idxNear)

	boxLow := bands[idxs[0]].Low
	boxHigh := bands[idxs[0]].High
	for _, i := range idxs[1:] {
		if bands[i].Low < boxLow {
			boxLow = bands[i].Low
		}
		if bands[i].High > boxHigh {
			boxHigh = bands[i].High
		}
	}
	boxWidth := boxHigh - boxLow

	s0, r0 := findS0R0(sspPricesSorted, pInt)

	var zone string
	var posPtr *float64

	if pInt < boxLow {
		zone = "OUT_DOWN"
		posPtr = nil
	} else if pInt > boxHigh {
		zone = "OUT_UP"
		posPtr = nil
	} else {
		pos := float64(pInt-boxLow) / float64(maxInt(boxWidth, 1))
		zone = "BATTLE"
		if pos < 0.35 {
			zone = "EDGE_LOW"
		} else if pos > 0.65 {
			zone = "EDGE_HIGH"
		}
		posPtr = new(float64)
		*posPtr = pos
	}

	includedBands := make([]Band, 0, len(idxs))
	for _, i := range idxs {
		includedBands = append(includedBands, bands[i])
	}

	return SuperBox{
		BoxLow: boxLow, BoxHigh: boxHigh, BoxWidth: boxWidth,
		EdgeLow: boxLow, EdgeHigh: boxHigh,
		PosBox: posPtr, Zone: zone,
		S0: s0, R0: r0,
		Bands: includedBands,
	}, nil
}

// ---------------------------
// Drift (ssp_box_drift) builder
// ---------------------------

func toInt64Default(x any, def int64) int64 {
	if x == nil {
		return def
	}
	// fast path for ints
	switch v := x.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return int64(f)
		}
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return i
		}
	}
	// fallback via decimal parsing
	r, err := parseRat(x)
	if err != nil {
		return def
	}
	return floorRatToInt64(r)
}

func asSliceAny(x any) ([]float64, bool) {
	if x == nil {
		return nil, false
	}
	switch v := x.(type) {
	case []float64:
		return v, true
	default:
		return nil, false
	}
}

func asMapAny(x any) (map[string]any, bool) {
	if x == nil {
		return nil, false
	}
	switch v := x.(type) {
	case map[string]interface{}:
		return v, true
	default:
		return nil, false
	}
}

func ComputeSSPBoxDrift(P any, SSPHist []map[string]any) string {
	// Use last 2 snapshots from SSP_hist:
	// - compute super-box using CURRENT P against each snapshot's SSP
	// - if both box_low and box_high shift >= +120 => UP_BOX
	// - if both shift <= -120 => DOWN_BOX
	// - else FLAT_BOX
	if len(SSPHist) < 2 {
		return ""
	}

	// sort by T if present
	sort.Slice(SSPHist, func(i, j int) bool {
		ti := toInt64Default(SSPHist[i]["T"], 0)
		tj := toInt64Default(SSPHist[j]["T"], 0)
		return ti < tj
	})

	a := SSPHist[len(SSPHist)-2]
	b := SSPHist[len(SSPHist)-1]

	sspAAny, okA := asSliceAny(a["SSP"])
	sspBAny, okB := asSliceAny(b["SSP"])
	if !okA || !okB || len(sspAAny) == 0 || len(sspBAny) == 0 {
		return ""
	}

	boxA, errA := BuildSuperBox(P, sspAAny)
	boxB, errB := BuildSuperBox(P, sspBAny)
	if errA != nil || errB != nil {
		return ""
	}

	dLow := boxB.BoxLow - boxA.BoxLow
	dHigh := boxB.BoxHigh - boxA.BoxHigh

	var out string
	if dLow >= 120 && dHigh >= 120 {
		out = "UP_BOX"
		return out
	}
	if dLow <= -120 && dHigh <= -120 {
		out = "DOWN_BOX"
		return out
	}
	out = "FLAT_BOX"
	return out
}

// ---------------------------
// Enrichment entry
// ---------------------------

func EnrichSnapshot(snapshot map[string]any, addDebugFields bool) (map[string]any, error) {
	var market map[string]any
	if m, ok := asMapAny(snapshot["market"]); ok {
		market = m
	} else {
		// small compatibility: accept top-level P/SSP/C1/C3/C5
		market = snapshot
	}

	P, okP := market["P"]
	SSPVal, okSSP := market["SSP"]
	if !okP || !okSSP {
		return nil, errors.New("input must contain market.P and market.SSP (or top-level P/SSP)")
	}

	sspList, ok := asSliceAny(SSPVal)
	if !ok || len(sspList) == 0 {
		return nil, errors.New("market.SSP must be a non-empty array")
	}

	box, err := BuildSuperBox(P, sspList)
	if err != nil {
		return nil, err
	}

	// SSP_hist optional
	var drift string
	if histVal, ok := market["SSP_hist"]; ok {
		if histSlice, ok2 := asSliceAny(histVal); ok2 {
			hist := make([]map[string]any, 0, len(histSlice))
			for _, item := range histSlice {
				if mm, ok3 := asMapAny(item); ok3 {
					hist = append(hist, mm)
				}
			}
			drift = ComputeSSPBoxDrift(P, hist)
		}
	}

	// write back only optional fields (won't break prompt even if ignored)
	market["ssp_zone"] = box.Zone
	if drift != "" {
		market["ssp_box_drift"] = drift
	}

	if addDebugFields {
		market["ssp_box_low"] = box.BoxLow
		market["ssp_box_high"] = box.BoxHigh
		if box.PosBox == nil {
			market["ssp_pos_box"] = nil
		} else {
			// match python: round(pos, 4)
			market["ssp_pos_box"] = math.Round(*box.PosBox*1e4) / 1e4
		}
		if box.S0 != nil {
			market["ssp_S0"] = *box.S0
		} else {
			market["ssp_S0"] = nil
		}
		if box.R0 != nil {
			market["ssp_R0"] = *box.R0
		} else {
			market["ssp_R0"] = nil
		}

		bandsOut := make([]map[string]any, 0, len(box.Bands))
		for _, b := range box.Bands {
			m := map[string]any{
				"low":    b.Low,
				"high":   b.High,
				"width":  b.Width,
				"center": math.Round(b.Center*100) / 100, // round(,2)
			}
			if b.DensityHintPrice != nil {
				m["density_hint_price"] = *b.DensityHintPrice
			} else {
				m["density_hint_price"] = nil
			}
			if b.DensityHintCount != nil {
				m["density_hint_count"] = *b.DensityHintCount
			} else {
				m["density_hint_count"] = nil
			}
			bandsOut = append(bandsOut, m)
		}
		market["ssp_bands"] = bandsOut
	}

	// if original snapshot had "market", keep structure, else leave as is
	if _, ok := snapshot["market"]; ok {
		snapshot["market"] = market
	}
	return snapshot, nil
}

// ---------------------------
// Optional helpers: JSON decode/encode convenience
// ---------------------------

// DecodeJSON decodes a JSON blob into map[string]any with UseNumber enabled (recommended).
func DecodeJSON(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// EncodeJSON pretty-prints a value as JSON.
func EncodeJSON(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

package strategy

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SSPResponse 对应 Python 的 SSPResponse Pydantic 模型
// 协议示例:
//
//	{"P":"83951.82000000","SSP":[83950,83950,84050,...],"T":1763805060000}
type SSPResponse struct {
	P   string  `json:"P"`   // current BTC price as string
	SSP []int64 `json:"SSP"` // spider-web price levels
	T   int64   `json:"T"`   // timestamp in milliseconds
}

// SSPResult 对应 Python 的 SSPResult Pydantic 模型
// 注意：原来 Optional[Decimal] 的字段，这里统一用 0 表示无
type SSPResult struct {
	Price     float64   `json:"price"`
	SSP       []int64   `json:"ssp"`
	Timestamp time.Time `json:"timestamp"`

	KeySupport          float64       `json:"key_support"`    // 0 = None
	KeyResistance       float64       `json:"key_resistance"` // 0 = None
	SupportPower        float64       `json:"support_power"`
	ResistPower         float64       `json:"resist_power"`
	Bias                float64       `json:"bias"`
	SupportStrengthNear float64       `json:"support_strength_near"`
	ResistStrengthNear  float64       `json:"resist_strength_near"`
	SupBandLow          float64       `json:"sup_band_low"`  // 0 = None
	SupBandHigh         float64       `json:"sup_band_high"` // 0 = None
	ResBandLow          float64       `json:"res_band_low"`  // 0 = None
	ResBandHigh         float64       `json:"res_band_high"` // 0 = None
	BiasNear            float64       `json:"bias_near"`
	LevelsDetail        []LevelDetail `json:"levels_detail"`
}

// NewSSPResult 等价于 Python SSPResult.from_response(cls, resp)
func NewSSPResult(resp SSPResponse) SSPResult {
	// Convert price = Decimal(resp.P or "0") → float64
	price := 0.0
	if resp.P != "" {
		if v, err := strconv.ParseFloat(resp.P, 64); err == nil {
			price = v
		}
	}

	// Raw levels (with duplicates)
	rawLevels := resp.SSP

	// unique_levels = sorted(set(int(x) for x in raw_levels))
	uniqueSet := make(map[int64]struct{})
	for _, x := range rawLevels {
		uniqueSet[x] = struct{}{}
	}
	uniqueLevels := make([]int64, 0, len(uniqueSet))
	for v := range uniqueSet {
		uniqueLevels = append(uniqueLevels, v)
	}
	sort.Slice(uniqueLevels, func(i, j int) bool { return uniqueLevels[i] < uniqueLevels[j] })

	// Convert T (milliseconds) to aware UTC datetime
	// Python: ts = datetime.fromtimestamp(ts_ms / 1000, tz=timezone.utc)
	ts := time.UnixMilli(resp.T).UTC()

	// Spider profile
	profile := ComputeSpiderProfile(price, rawLevels)

	result := SSPResult{
		Price:     price,
		SSP:       uniqueLevels,
		Timestamp: ts,

		KeySupport:          profile.KeySupport,
		KeyResistance:       profile.KeyResistance,
		SupportPower:        profile.SupportPower,
		ResistPower:         profile.ResistPower,
		Bias:                profile.Bias,
		SupportStrengthNear: profile.SupportStrengthNear,
		ResistStrengthNear:  profile.ResistStrengthNear,
		SupBandLow:          profile.SupBandLow,
		SupBandHigh:         profile.SupBandHigh,
		ResBandLow:          profile.ResBandLow,
		ResBandHigh:         profile.ResBandHigh,
		BiasNear:            profile.BiasNear,
		LevelsDetail:        profile.LevelsDetail,
	}

	return result
}

// ---------- 辅助格式化 ----------

func show2(x float64) string { return fmt.Sprintf("%.2f", x) }
func show3(x float64) string { return fmt.Sprintf("%.3f", x) }

func opt2(x float64) string { // 0 代表 None → 打印 “—”
	if x == 0 {
		return "—"
	}
	return show2(x)
}

func intsPreview(xs []int64, n int) string {
	if len(xs) == 0 {
		return "[]"
	}
	if len(xs) <= n {
		return fmt.Sprint(xs)
	}
	return fmt.Sprintf("%v…(+%d)", xs[:n], len(xs)-n)
}

func summarizeLevels(levels []LevelDetail, side string, top int) (count int, preview string) {
	var arr []LevelDetail
	for _, lv := range levels {
		if strings.EqualFold(lv.Side, side) {
			arr = append(arr, lv)
		}
	}
	count = len(arr)
	if count == 0 {
		return 0, "[]"
	}
	// 强度降序 -> 次数降序 -> 价格升序
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].NormStrength != arr[j].NormStrength {
			return arr[i].NormStrength > arr[j].NormStrength
		}
		if arr[i].Count != arr[j].Count {
			return arr[i].Count > arr[j].Count
		}
		return arr[i].Price < arr[j].Price
	})
	if len(arr) > top {
		arr = arr[:top]
	}
	parts := make([]string, 0, len(arr))
	for _, ld := range arr {
		parts = append(parts, fmt.Sprintf("%.2f(str=%.3f,x%d)", ld.Price, ld.NormStrength, ld.Count))
	}
	return count, "[" + strings.Join(parts, ", ") + "]"
}

// ---------- Stringer 实现 ----------

func (r SSPResult) String() string {
	const (
		maxSSPPreview = 12
		labelW        = 11
		topLevels     = 5
	)
	var b strings.Builder

	// 标题
	fmt.Fprintf(&b, "SSPResult @ %s\n", r.Timestamp.UTC().Format(time.RFC3339))

	// 核心指标
	fmt.Fprintf(&b, "  %-*s %s  |  Bias=%s  (near %s)\n",
		labelW, "Price:", show2(r.Price), show3(r.Bias), show3(r.BiasNear),
	)

	// 支撑/压力摘要（0 → “—”）
	fmt.Fprintf(&b, "  %-*s key=%s  power=%s  near=%s  band=[%s, %s]\n",
		labelW, "Support:",
		opt2(r.KeySupport), show3(r.SupportPower), show3(r.SupportStrengthNear),
		opt2(r.SupBandLow), opt2(r.SupBandHigh),
	)
	fmt.Fprintf(&b, "  %-*s key=%s  power=%s  near=%s  band=[%s, %s]\n",
		labelW, "Resist:",
		opt2(r.KeyResistance), show3(r.ResistPower), show3(r.ResistStrengthNear),
		opt2(r.ResBandLow), opt2(r.ResBandHigh),
	)

	// 蜘蛛丝预览 + Level 统计
	fmt.Fprintf(&b, "  %-*s %s (total=%d)\n", labelW, "SSP:", intsPreview(r.SSP, maxSSPPreview), len(r.SSP))

	sCnt, sTop := summarizeLevels(r.LevelsDetail, "support", topLevels)
	rCnt, rTop := summarizeLevels(r.LevelsDetail, "resistance", topLevels)
	fmt.Fprintf(&b, "  %-*s total=%d (support=%d, resistance=%d)\n", labelW, "Levels:", len(r.LevelsDetail), sCnt, rCnt)
	fmt.Fprintf(&b, "    • top support   : %s\n", sTop)
	fmt.Fprintf(&b, "    • top resistance: %s\n", rTop)

	return b.String()
}

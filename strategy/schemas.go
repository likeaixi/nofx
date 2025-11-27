package strategy

import (
	"fmt"
	"github.com/shopspring/decimal"
	"sort"
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
//
// 比 SSPResponse 多出来：
// - Price: Decimal 价格
// - SSP: 去重 + 升序后的 levels
// - Timestamp: UTC 时间
// - 以及 SpiderProfile 的各项分析字段
type SSPResult struct {
	Price     decimal.Decimal `json:"price"`
	SSP       []int64         `json:"ssp"`
	Timestamp time.Time       `json:"timestamp"`

	KeySupport          *decimal.Decimal `json:"key_support,omitempty"`
	KeyResistance       *decimal.Decimal `json:"key_resistance,omitempty"`
	SupportPower        decimal.Decimal  `json:"support_power"`
	ResistPower         decimal.Decimal  `json:"resist_power"`
	Bias                decimal.Decimal  `json:"bias"`
	SupportStrengthNear decimal.Decimal  `json:"support_strength_near"`
	ResistStrengthNear  decimal.Decimal  `json:"resist_strength_near"`
	SupBandLow          *decimal.Decimal `json:"sup_band_low,omitempty"`
	SupBandHigh         *decimal.Decimal `json:"sup_band_high,omitempty"`
	ResBandLow          *decimal.Decimal `json:"res_band_low,omitempty"`
	ResBandHigh         *decimal.Decimal `json:"res_band_high,omitempty"`
	BiasNear            decimal.Decimal  `json:"bias_near"`

	LevelsDetail []LevelDetail `json:"levels_detail"`
}

// NewSSPResult 等价于 Python SSPResult.from_response(cls, resp)
func NewSSPResult(resp SSPResponse) SSPResult {
	zeroDec := decimal.NewFromInt(0)

	// Convert price = Decimal(resp.P or "0")
	price, err := decimal.NewFromString(resp.P)
	if err != nil {
		price = zeroDec
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
	// ts_ms = int(resp.T or 0); ts = datetime.fromtimestamp(ts_ms / 1000, tz=timezone.utc)
	var ts time.Time
	// 在 Python 里 0 也会得到 1970-01-01，而不是 now()，这里保持一样语义
	ts = time.UnixMilli(resp.T).UTC()

	// Spider profile
	// profile = compute_spider_profile(price=price, raw_levels=raw_levels)
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

func decPtr(p *decimal.Decimal) string {
	if p == nil {
		return "—"
	}
	return p.String()
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
	// 强度降序，其次次数降序，其次价格升序
	sort.Slice(arr, func(i, j int) bool {
		if !arr[i].NormStrength.Equal(arr[j].NormStrength) {
			return arr[i].NormStrength.GreaterThan(arr[j].NormStrength)
		}
		if arr[i].Count != arr[j].Count {
			return arr[i].Count > arr[j].Count
		}
		return arr[i].Price.LessThan(arr[j].Price)
	})
	if len(arr) > top {
		arr = arr[:top]
	}
	// 简洁预览：price(str=0.812,x5)
	var parts []string
	for _, ld := range arr {
		parts = append(parts,
			fmt.Sprintf("%s(str=%s,x%d)",
				ld.Price.String(),              // 或 StringFixed(0/2)
				ld.NormStrength.StringFixed(3), // 强度保留3位
				ld.Count,
			),
		)
	}
	return count, "[" + strings.Join(parts, ", ") + "]"
}

// ---------- Stringer 实现 ----------

func (r SSPResult) String() string {
	const (
		maxSSPPreview = 12
		topLevels     = 5
	)
	var b strings.Builder

	// 标题行
	fmt.Fprintf(&b, "SSPResult @ %s\n", r.Timestamp.UTC().Format(time.RFC3339))

	// 顶部核心指标
	fmt.Fprintf(&b, "  Price: %s | Bias: %s (near %s)\n",
		r.Price.String(), r.Bias.String(), r.BiasNear.String(),
	)

	// 支撑/压力概览
	fmt.Fprintf(&b, "  Support: key=%s  power=%s  near=%s  band=[%s, %s]\n",
		decPtr(r.KeySupport), r.SupportPower.String(), r.SupportStrengthNear.String(),
		decPtr(r.SupBandLow), decPtr(r.SupBandHigh),
	)
	fmt.Fprintf(&b, "  Resist : key=%s  power=%s  near=%s  band=[%s, %s]\n",
		decPtr(r.KeyResistance), r.ResistPower.String(), r.ResistStrengthNear.String(),
		decPtr(r.ResBandLow), decPtr(r.ResBandHigh),
	)

	// SSP 列表预览
	fmt.Fprintf(&b, "  SSP levels (%d): %s\n", len(r.SSP), intsPreview(r.SSP, maxSSPPreview))

	// LevelsDetail 分侧汇总与 Top-N
	sCnt, sTop := summarizeLevels(r.LevelsDetail, "support", topLevels)
	rCnt, rTop := summarizeLevels(r.LevelsDetail, "resistance", topLevels)

	fmt.Fprintf(&b, "  LevelsDetail: total=%d (support=%d, resistance=%d)\n",
		len(r.LevelsDetail), sCnt, rCnt,
	)
	fmt.Fprintf(&b, "    • top support   : %s\n", sTop)
	fmt.Fprintf(&b, "    • top resistance: %s\n", rTop)

	return b.String()
}

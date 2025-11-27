package strategy

import (
	"github.com/shopspring/decimal"
	"sort"
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

package strategy

import (
	"math"
	"sort"

	"github.com/shopspring/decimal"
)

// LevelDetail 对应 Python levels_detail 里的每一项 dict：
//
//	{
//	  "price": Decimal,
//	  "side": "support" | "resistance",
//	  "count": int,
//	  "norm_strength": Decimal
//	}
type LevelDetail struct {
	Price        decimal.Decimal `json:"price"`
	Side         string          `json:"side"` // "support" or "resistance"
	Count        int             `json:"count"`
	NormStrength decimal.Decimal `json:"norm_strength"` // 归一化强度（相对该侧 max）
}

// SpiderProfile 对应 compute_spider_profile 返回的 dict
type SpiderProfile struct {
	KeySupport          *decimal.Decimal `json:"key_support,omitempty"`
	KeyResistance       *decimal.Decimal `json:"key_resistance,omitempty"`
	SupportPower        decimal.Decimal  `json:"support_power"`
	ResistPower         decimal.Decimal  `json:"resist_power"`
	Bias                decimal.Decimal  `json:"bias"`
	SupportStrengthNear decimal.Decimal  `json:"support_strength_near"`
	ResistStrengthNear  decimal.Decimal  `json:"resist_strength_near"`
	LevelsDetail        []LevelDetail    `json:"levels_detail"`

	SupBandLow  *decimal.Decimal `json:"sup_band_low,omitempty"`
	SupBandHigh *decimal.Decimal `json:"sup_band_high,omitempty"`
	ResBandLow  *decimal.Decimal `json:"res_band_low,omitempty"`
	ResBandHigh *decimal.Decimal `json:"res_band_high,omitempty"`
	BiasNear    decimal.Decimal  `json:"bias_near"`
}

// ComputeSpiderProfile 等价于 Python 的 compute_spider_profile
func ComputeSpiderProfile(price decimal.Decimal, rawLevels []int64) SpiderProfile {
	zero := decimal.NewFromInt(0)

	var (
		keySupport          *decimal.Decimal
		keyResistance       *decimal.Decimal
		supportPower        = zero
		resistPower         = zero
		bias                = zero
		supportStrengthNear = zero
		resistStrengthNear  = zero
		levelsDetail        []LevelDetail
		supBandLow          *decimal.Decimal
		supBandHigh         *decimal.Decimal
		resBandLow          *decimal.Decimal
		resBandHigh         *decimal.Decimal
		biasNear            = zero
	)

	// if price <= 0 or not raw_levels: return defaults
	if price.Cmp(zero) <= 0 || len(rawLevels) == 0 {
		return SpiderProfile{
			KeySupport:          keySupport,
			KeyResistance:       keyResistance,
			SupportPower:        supportPower,
			ResistPower:         resistPower,
			Bias:                bias,
			SupportStrengthNear: supportStrengthNear,
			ResistStrengthNear:  resistStrengthNear,
			LevelsDetail:        levelsDetail,
			SupBandLow:          supBandLow,
			SupBandHigh:         supBandHigh,
			ResBandLow:          resBandLow,
			ResBandHigh:         resBandHigh,
			BiasNear:            biasNear,
		}
	}

	// counts = Counter(int(x) for x in raw_levels)
	counts := make(map[int64]int)
	for _, x := range rawLevels {
		counts[int64(x)]++
	}

	// level_values = sorted(counts.keys())
	levelValues := make([]int64, 0, len(counts))
	for k := range counts {
		levelValues = append(levelValues, k)
	}
	sort.Slice(levelValues, func(i, j int) bool { return levelValues[i] < levelValues[j] })

	// Parameters for cluster/distance (完全对齐 Python)
	const (
		W               = 300.0  // cluster width in USD
		lambdaBps       = 50.0   // distance decay in basis points
		NEAR_WINDOW     = 2000.0 // bias_near window
		BAND_COVER_FRAC = 0.9    // core band cover fraction
	)

	strengths := make(map[int64]float64)

	priceF, err := price.Float64()
	if err != nil {
		priceF = 0.0
	}

	// Precompute cluster_weight + distance-based strength
	for _, L := range levelValues {
		// cluster_weight(L)
		cw := 0.0
		for x, cnt := range counts {
			xf := float64(x)
			Lf := float64(L)
			cw += float64(cnt) * math.Exp(-math.Abs(xf-Lf)/W)
		}

		// distance in bps from price
		proximity := 0.0
		if priceF != 0.0 {
			dBps := math.Abs(float64(L)-priceF) / priceF * 10000.0
			proximity = math.Exp(-dBps / lambdaBps)
		}

		strengths[L] = cw * proximity
	}

	// Split support / resistance by price
	var supportLevels, resistLevels []int64
	for _, L := range levelValues {
		LDec := decimal.NewFromInt(L)
		if LDec.Cmp(price) <= 0 {
			supportLevels = append(supportLevels, L)
		} else {
			resistLevels = append(resistLevels, L)
		}
	}

	// max_support / max_resist
	maxSupport := 0.0
	for _, L := range supportLevels {
		if s := strengths[L]; s > maxSupport {
			maxSupport = s
		}
	}
	maxResist := 0.0
	for _, L := range resistLevels {
		if s := strengths[L]; s > maxResist {
			maxResist = s
		}
	}

	// Helper: _pick_key(levels)
	pickKey := func(levels []int64) (int64, bool) {
		bestStrength := -1.0
		bestDist := math.Inf(1)
		var bestL int64
		for _, L := range levels {
			s := strengths[L]
			d := math.Abs(float64(L) - priceF)
			if s > bestStrength || (s == bestStrength && d < bestDist) {
				bestStrength = s
				bestDist = d
				bestL = L
			}
		}
		if bestStrength < 0 {
			return 0, false
		}
		return bestL, true
	}

	ksInt, hasKS := pickKey(supportLevels)
	krInt, hasKR := pickKey(resistLevels)

	if hasKS {
		v := decimal.NewFromInt(ksInt)
		keySupport = &v
		s := strengths[ksInt]
		supportPower = decimal.NewFromFloat(s)
	}
	if hasKR {
		v := decimal.NewFromInt(krInt)
		keyResistance = &v
		s := strengths[krInt]
		resistPower = decimal.NewFromFloat(s)
	}

	// bias = (sp - rp) / (sp + rp)
	{
		sp, err1 := supportPower.Float64()
		rp, err2 := resistPower.Float64()
		if err1 == nil && err2 == nil && sp+rp > 0 {
			bias = decimal.NewFromFloat((sp - rp) / (sp + rp))
		}
	}

	// Build levels_detail with normalized strength per side
	details := make([]LevelDetail, 0, len(levelValues))
	for _, L := range levelValues {
		sVal := strengths[L]
		LDec := decimal.NewFromInt(L)

		side := "support"
		if LDec.Cmp(price) > 0 {
			side = "resistance"
		}

		var norm float64
		if side == "support" {
			if maxSupport > 0 {
				norm = sVal / maxSupport
			} else {
				norm = 0
			}
		} else {
			if maxResist > 0 {
				norm = sVal / maxResist
			} else {
				norm = 0
			}
		}

		details = append(details, LevelDetail{
			Price:        LDec,
			Side:         side,
			Count:        counts[L],
			NormStrength: decimal.NewFromFloat(norm),
		})
	}
	levelsDetail = details

	// support_strength_near / resist_strength_near
	if hasKS && maxSupport > 0 {
		val := strengths[ksInt] / maxSupport
		supportStrengthNear = decimal.NewFromFloat(val)
	}
	if hasKR && maxResist > 0 {
		val := strengths[krInt] / maxResist
		resistStrengthNear = decimal.NewFromFloat(val)
	}

	// -------- Extended: bias_near --------
	// Near-window mass-based bias (NEAR_WINDOW = 2000.0)
	massSup := 0.0
	massRes := 0.0

	for _, L := range supportLevels {
		dDown := priceF - float64(L)
		if dDown <= NEAR_WINDOW {
			massSup += strengths[L]
		}
	}

	for _, L := range resistLevels {
		dUp := float64(L) - priceF
		if dUp <= NEAR_WINDOW {
			massRes += strengths[L]
		}
	}

	// denom = mass_sup + mass_res + 1e-9
	denom := massSup + massRes + 1e-9
	if denom != 0 {
		biasNear = decimal.NewFromFloat((massSup - massRes) / denom)
	}

	// -------- Extended: core bands sup_band / res_band --------
	// Core bands: accumulate from nearest to price until covering BAND_COVER_FRAC of mass

	// support band
	totalSupMass := 0.0
	for _, L := range supportLevels {
		totalSupMass += strengths[L]
	}
	if totalSupMass > 0 && len(supportLevels) > 0 {
		supSorted := make([]int64, len(supportLevels))
		copy(supSorted, supportLevels)
		sort.Slice(supSorted, func(i, j int) bool {
			return math.Abs(float64(supSorted[i])-priceF) < math.Abs(float64(supSorted[j])-priceF)
		})

		acc := 0.0
		var selectedSup []int64
		for _, L := range supSorted {
			acc += strengths[L]
			selectedSup = append(selectedSup, L)
			if acc >= BAND_COVER_FRAC*totalSupMass {
				break
			}
		}
		if len(selectedSup) > 0 {
			minL, maxL := selectedSup[0], selectedSup[0]
			for _, L := range selectedSup[1:] {
				if L < minL {
					minL = L
				}
				if L > maxL {
					maxL = L
				}
			}
			low := decimal.NewFromInt(minL)
			high := decimal.NewFromInt(maxL)
			supBandLow = &low
			supBandHigh = &high
		}
	}

	// resistance band
	totalResMass := 0.0
	for _, L := range resistLevels {
		totalResMass += strengths[L]
	}
	if totalResMass > 0 && len(resistLevels) > 0 {
		resSorted := make([]int64, len(resistLevels))
		copy(resSorted, resistLevels)
		sort.Slice(resSorted, func(i, j int) bool {
			return math.Abs(float64(resSorted[i])-priceF) < math.Abs(float64(resSorted[j])-priceF)
		})

		acc := 0.0
		var selectedRes []int64
		for _, L := range resSorted {
			acc += strengths[L]
			selectedRes = append(selectedRes, L)
			if acc >= BAND_COVER_FRAC*totalResMass {
				break
			}
		}
		if len(selectedRes) > 0 {
			minL, maxL := selectedRes[0], selectedRes[0]
			for _, L := range selectedRes[1:] {
				if L < minL {
					minL = L
				}
				if L > maxL {
					maxL = L
				}
			}
			low := decimal.NewFromInt(minL)
			high := decimal.NewFromInt(maxL)
			resBandLow = &low
			resBandHigh = &high
		}
	}

	return SpiderProfile{
		KeySupport:          keySupport,
		KeyResistance:       keyResistance,
		SupportPower:        supportPower,
		ResistPower:         resistPower,
		Bias:                bias,
		SupportStrengthNear: supportStrengthNear,
		ResistStrengthNear:  resistStrengthNear,
		LevelsDetail:        levelsDetail,
		SupBandLow:          supBandLow,
		SupBandHigh:         supBandHigh,
		ResBandLow:          resBandLow,
		ResBandHigh:         resBandHigh,
		BiasNear:            biasNear,
	}
}

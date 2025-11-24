package strategy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"nofx/decision"
	"nofx/market"
	"nofx/pool"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

var (
	//apiKey    = getenv("BINANCE_API_KEY", "YOUR_API_KEY")
	//apiSecret = getenv("BINANCE_API_SECRET", "YOUR_API_SECRET")
	//
	//symbol = getenv("SYMBOL", "BTCUSDT")

	// 轮询
	pollInterval = 2 * time.Second

	// 蜘蛛丝（2s 刷新）
	spiderURL = "http://47.245.93.48:28088/ssp/signal?accessToken=65962631-158e-401b-a87c-a40650f82446"

	// C3/C5（返回 {"c3":"up|down|neutral","c5":"..."}）
	c35URL = "https://api.hyperaitrade.com/api/signal/latest"

	// 触碰 / 穿越价带
	priceBandUSD = d("100")

	// 入场过滤：最近一对 L/S gap < 500U → 成对剔除（仅无持仓找入场时应用）
	pairMinGapUSD = d("500")

	// 开仓参数
	leverage             = 50
	accountEquityUSDT    = d("1000") // 备注
	positionNotionalUSDT = d("200")  // 每次开仓名义 200U

	STOP_LOSS_PCT   = d("0.05")
	TAKE_PROFIT_PCT = d("0.25")

	positionPercent = d("0.2")

	// 可选硬止损（0 关闭）
	slPctHard = d("0.05") // 5%

	// 仅考虑当前价 ± 这个范围内的蜘蛛丝来找入场
	entryNearRangeUSD = d("2000")

	//// LIMIT 挂单生命周期（秒）
	//orderLifetimeSec = 60 * time.Second

	// 交易 JSONL 日志文件
	tradeLogFile = "spider_trades_log.jsonl"
)

// SpiderDataProvider exposes the market inputs required by SpiderStrategy.
type SpiderDataProvider interface {
	// FetchLevels returns the latest short/long spider levels.
	FetchLevels(ctx *decision.Context, symbol string) (shorts []decimal.Decimal, longs []decimal.Decimal, err error)
	// Price returns the latest mark or index price for the symbol.
	Price(ctx *decision.Context, symbol string) (decimal.Decimal, error)
}

// SpiderStrategy transforms spider signals into structured Decision outputs.
type SpiderStrategy struct {
	BaseStrategy

	data SpiderDataProvider

	symbol        string
	entryDistance decimal.Decimal
	positionSize  decimal.Decimal
	leverage      int
	confidence    int
}

// NewSpiderStrategy prepares a strategy wired with its dependencies.
func NewSpiderStrategy() *SpiderStrategy {
	return &SpiderStrategy{
		BaseStrategy: NewBaseStrategy("spider"),
		//data:          data,
		entryDistance: decimal.NewFromInt(200),
		positionSize:  decimal.NewFromInt(100),
		leverage:      5,
		confidence:    60,
	}
}

// Configure reads the generic StrategyConfig into strongly typed members.
func (s *SpiderStrategy) Configure(_ *decision.Context, cfg StrategyConfig) error {
	if cfg.Symbol == "" {
		return errors.New("spider strategy requires a symbol")
	}
	s.symbol = cfg.Symbol

	if cfg.Params != nil {
		if v, ok := cfg.Params["entry_distance"]; ok {
			s.entryDistance = mustDecimal(v, s.entryDistance)
		}
		if v, ok := cfg.Params["position_size_usd"]; ok {
			s.positionSize = mustDecimal(v, s.positionSize)
		} else if v, ok := cfg.Params["position_size"]; ok {
			s.positionSize = mustDecimal(v, s.positionSize)
		}
		if v, ok := cfg.Params["leverage"]; ok {
			s.leverage = mustInt(v, s.leverage)
		}
		if v, ok := cfg.Params["confidence"]; ok {
			s.confidence = mustInt(v, s.confidence)
		}
	}
	//logger.WithFields(logrus.Fields{
	//	"strategy":       s.Name(),
	//	"symbol":         s.symbol,
	//	"entry_distance": s.entryDistance.String(),
	//	"position_size":  s.positionSize.String(),
	//	"leverage":       s.leverage,
	//	"confidence":     s.confidence,
	//}).Info("spider strategy configured")
	return nil
}

// Decide evaluates spider levels and emits trading decisions.
func (s *SpiderStrategy) Decide(ctx *decision.Context) ([]decision.Decision, error) {
	if s.data == nil {
		return nil, errors.New("spider strategy missing data provider")
	}
	if s.symbol == "" {
		return nil, errors.New("spider strategy not configured")
	}

	shorts, longs, err := s.data.FetchLevels(ctx, s.symbol)
	if err != nil {
		//logger.WithFields(logrus.Fields{
		//	"strategy": s.Name(),
		//	"symbol":   s.symbol,
		//}).WithError(err).Error("spider fetch levels failed")
		return nil, fmt.Errorf("fetch spider levels: %w", err)
	}
	//logger.WithFields(logrus.Fields{
	//	"strategy":     s.Name(),
	//	"symbol":       s.symbol,
	//	"short_levels": len(shorts),
	//	"long_levels":  len(longs),
	//}).Debug("spider fetched levels")
	if len(shorts) == 0 && len(longs) == 0 {
		//logger.WithFields(logrus.Fields{
		//	"strategy": s.Name(),
		//	"symbol":   s.symbol,
		//}).Debug("spider levels empty, skip decision")
		return nil, nil
	}

	price, err := s.data.Price(ctx, s.symbol)
	if err != nil {
		//logger.WithFields(logrus.Fields{
		//	"strategy": s.Name(),
		//	"symbol":   s.symbol,
		//}).WithError(err).Error("spider fetch price failed")
		return nil, fmt.Errorf("fetch price: %w", err)
	}

	direction, level := s.pickEntry(price, shorts, longs)
	if level == nil || direction == "" {
		//logger.WithFields(logrus.Fields{
		//	"strategy": s.Name(),
		//	"symbol":   s.symbol,
		//	"price":    price.String(),
		//}).Debug("spider no eligible level near price")
		return nil, nil
	}

	action, ok := actionFromDirection(direction)
	if !ok {
		return nil, nil
	}

	distance := absDecimal(price.Sub(*level))
	sizeUSD := decimalToFloat(s.positionSize)
	if sizeUSD <= 0 {
		sizeUSD = decimalToFloat(price) // fallback to notional approx
	}

	dec := decision.Decision{
		Symbol:          s.symbol,
		Action:          action,
		Leverage:        s.leverage,
		PositionSizeUSD: sizeUSD,
		Confidence:      s.confidence,
		Reasoning: fmt.Sprintf(
			"Spider %s near level %s (price=%s, distance=%s)",
			direction, level.String(), price.String(), distance.String(),
		),
	}

	//logger.WithFields(logrus.Fields{
	//	"strategy":          s.Name(),
	//	"symbol":            s.symbol,
	//	"action":            action,
	//	"level":             level.String(),
	//	"distance":          distance.String(),
	//	"price":             price.String(),
	//	"position_size_usd": sizeUSD,
	//	"leverage":          s.leverage,
	//	"confidence":        s.confidence,
	//}).Info("spider decision generated")

	return []decision.Decision{dec}, nil
}

type tradeDirection string

// "open_long", "open_short", "close_long", "close_short", "update_stop_loss", "update_take_profit", "partial_close", "hold", "wait"
const (
	directionLong  tradeDirection = "LONG"
	directionShort tradeDirection = "SHORT"
)

func (s *SpiderStrategy) pickEntry(price decimal.Decimal, shorts, longs []decimal.Decimal) (tradeDirection, *decimal.Decimal) {
	bestDist := decimal.Zero
	var bestLevel *decimal.Decimal
	var bestDirection tradeDirection

	update := func(direction tradeDirection, level decimal.Decimal) {
		dist := absDecimal(price.Sub(level))
		if dist.GreaterThan(s.entryDistance) {
			return
		}
		if bestLevel == nil || dist.LessThan(bestDist) {
			lv := level
			bestLevel = &lv
			bestDirection = direction
			bestDist = dist
		}
	}

	for _, lv := range longs {
		update(directionLong, lv)
	}
	for _, lv := range shorts {
		update(directionShort, lv)
	}
	return bestDirection, bestLevel
}

func actionFromDirection(direction tradeDirection) (string, bool) {
	switch direction {
	case directionLong:
		return "open_long", true
	case directionShort:
		return "open_short", true
	default:
		return "", false
	}
}

func absDecimal(v decimal.Decimal) decimal.Decimal {
	if v.IsNegative() {
		return v.Neg()
	}
	return v
}

func mustDecimal(v any, fallback decimal.Decimal) decimal.Decimal {
	switch val := v.(type) {
	case decimal.Decimal:
		return val
	case string:
		if parsed, err := decimal.NewFromString(val); err == nil {
			return parsed
		}
	case float64:
		return decimal.NewFromFloat(val)
	case float32:
		return decimal.NewFromFloat(float64(val))
	case int:
		return decimal.NewFromInt(int64(val))
	case int64:
		return decimal.NewFromInt(val)
	}
	return fallback
}

func mustInt(v any, fallback int) int {
	switch val := v.(type) {
	case int:
		return val
	case int32:
		return int(val)
	case int64:
		return int(val)
	case float64:
		return int(val)
	case float32:
		return int(val)
	case string:
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return fallback
}

func decimalToFloat(v decimal.Decimal) float64 {
	f, _ := v.Float64()
	return f
}

// 重写AI决策的方法
func (s *SpiderStrategy) GetFullDecision(ctx *decision.Context) (*decision.FullDecision, error) {
	log.Printf("Decision start")
	// 1. 为所有币种获取市场数据
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("获取市场数据失败: %w", err)
	}

	// 2. 构建 System Prompt（固定规则）和 User Prompt（动态数据）
	//systemPrompt := buildSystemPromptWithCustom(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, customPrompt, overrideBase, templateName)
	//userPrompt := buildUserPrompt(ctx)

	// 3. 调用AI API（使用 system + user prompt）
	aiCallStart := time.Now().UnixMicro()
	//aiResponse, err := mcpClient.CallWithMessages(systemPrompt, userPrompt)
	//aiCallDuration := time.Since(aiCallStart)
	//if err != nil {
	//	return nil, fmt.Errorf("调用AI API失败: %w", err)
	//}

	fullDecision := &decision.FullDecision{
		SystemPrompt:        "",
		UserPrompt:          "",
		CoTTrace:            "",
		Decisions:           nil,
		Timestamp:           time.Time{},
		AIRequestDurationMs: 0,
	}

	fullDecision.SystemPrompt = "Spider strategy"

	// 计算所有币种的决策
	decisions := make([]decision.Decision, 0, len(ctx.CandidateCoins))
	for _, coin := range ctx.CandidateCoins {
		dec := decision.Decision{Symbol: coin.Symbol}

		var p = decision.PositionInfo{}

		for _, pos := range ctx.Positions {
			if coin.Symbol == pos.Symbol {
				p = pos
				break
			}
		}

		ssp, pri := fetchSpiderRaw()
		act, c1, c3, c5 := fetchCombo()

		dec.Symbol = coin.Symbol

		fullDecision.SystemPrompt += "\nSymbol: " + dec.Symbol
		fullDecision.SystemPrompt += "\nAI signal: " + act
		fullDecision.SystemPrompt += "\nPrice: " + pri.String()
		fullDecision.SystemPrompt += "\nSSP: " + fmt.Sprintf("%v", ssp)
		fullDecision.SystemPrompt += "\nC1: " + fmt.Sprintf("%v", c1)
		fullDecision.SystemPrompt += "\nC3: " + fmt.Sprintf("%v", c3)
		fullDecision.SystemPrompt += "\nC5: " + fmt.Sprintf("%v", c5)

		// 1) 有持仓先做退出逻辑
		if p.Symbol != "" {
			dec = checkExitConditions(p, ctx.Klines[coin.Symbol])
			decisions = append(decisions, dec)
			log.Printf("[ENTRY] 有持仓，先判断是否退出，symbol: %s，action: %s", p.Symbol, dec.Reasoning)
			continue
		}

		// 2) 无持仓，找入场
		marketData, ok1 := ctx.MarketDataMap[coin.Symbol]
		klines, ok2 := ctx.Klines[coin.Symbol]
		if !ok1 || !ok2 {
			log.Printf("[ENTRY] 没有市场数据或者K线，wait")
			dec.Action = "wait"
			dec.Reasoning = "没有K线数据"
			decisions = append(decisions, dec)
			continue
		}

		price := decimal.NewFromFloat(marketData.CurrentPrice)
		closed := klines
		//start := time.Now()

		if closed == nil || len(closed) < 4 {
			log.Printf("[ENTRY] 获取K线失败:")
			//sleepUntil(start, pollInterval)

			dec.Action = "wait"
			dec.Reasoning = "获取K线失败"
			decisions = append(decisions, dec)
			continue
		}

		if act == "NEUTRAL" {
			log.Printf("[ENTRY] 信号方向为NEUTRAL，wait")
			dec.Action = "wait"
			dec.Reasoning = "AI信号方向为NEUTRAL"
			decisions = append(decisions, dec)
			continue
		}

		if len(ssp) == 0 {
			log.Printf("[ENTRY] 蜘蛛丝为空，wait")
			//sleepUntil(start, pollInterval)
			dec.Action = "wait"
			dec.Reasoning = "蜘蛛丝为空"
			decisions = append(decisions, dec)
			continue
		}

		//shorts, longs := filterPairsForEntry(shortsRaw, longsRaw, pairMinGapUSD)
		//if len(shorts) == 0 && len(longs) == 0 {
		//	log.Printf("[ENTRY] 没有可用的蜘蛛丝，wait")
		//	//sleepUntil(start, pollInterval)
		//	dec.Action = "wait"
		//	decisions = append(decisions, dec)
		//	continue
		//}

		allEntry := ssp
		var candidates []decimal.Decimal
		for _, lv := range allEntry {
			if absDec(lv.Sub(price)).LessThanOrEqual(entryNearRangeUSD) {
				candidates = append(candidates, lv)
			}
		}
		if len(candidates) == 0 {
			log.Printf("[ENTRY] 没有满足条件的蜘蛛丝价格，wait")
			//sleepUntil(start, pollInterval)
			dec.Action = "wait"
			dec.Reasoning = "没有满足条件的蜘蛛丝价格"
			decisions = append(decisions, dec)
			continue
		}

		var dcs []map[string]any
		for _, lv := range candidates {
			if m := decideOnLevel(lv, closed, act, priceBandUSD); m != nil {
				dcs = append(dcs, m)
			}
		}
		log.Println("满足条件的点位", dcs)
		if len(dcs) == 0 {
			//sleepUntil(start, pollInterval)
			log.Printf("[ENTRY] 没有满足条件的反转点位，wait")
			dec.Action = "wait"
			dec.Reasoning = "没有满足条件的反转点位"
			decisions = append(decisions, dec)
			continue
		}

		bestIdx := 0
		bestDist := absDec(price.Sub(dcs[0]["level"].(decimal.Decimal)))
		for i := 1; i < len(dcs); i++ {
			d := absDec(price.Sub(dcs[i]["level"].(decimal.Decimal)))
			if d.LessThan(bestDist) {
				bestDist = d
				bestIdx = i
			}
		}
		best := dcs[bestIdx]

		var action string
		var sl, tp decimal.Decimal
		log.Println("Best side", best["side"])
		if best["side"] == "LONG" {
			action = "open_long"

			sl = price.Mul(d("1").Sub(STOP_LOSS_PCT))
			tp = price.Mul(d("1").Add(TAKE_PROFIT_PCT))

			dec.StopLoss, _ = sl.Float64()
			dec.TakeProfit, _ = tp.Float64()
		}

		if best["side"] == "SHORT" {
			action = "open_short"

			sl = price.Mul(d("1").Add(STOP_LOSS_PCT))
			tp = price.Mul(d("1").Sub(TAKE_PROFIT_PCT))

			dec.StopLoss, _ = sl.Float64()
			dec.TakeProfit, _ = tp.Float64()
		}

		dec.Action = action
		dec.Level, _ = (best["level"]).(decimal.Decimal).Float64()
		dec.Reasoning = best["reason"].(string)

		accountEquityUSDT := decimal.NewFromFloat(ctx.Account.TotalEquity)
		l := decimal.NewFromInt(int64(leverage))

		dec.PositionSizeUSD, _ = accountEquityUSDT.Mul(positionPercent).Mul(l).Float64()
		dec.Leverage = leverage

		log.Printf("[ENTRY] symbol: %s，action: %s", dec.Symbol, dec.Reasoning)
		decisions = append(decisions, dec)

		logTradeEvent("OPEN_SIGNAL", map[string]any{
			"symbol":       dec.Symbol,
			"side":         best["side"],
			"ref_level":    best["level"],
			"reason":       best["reason"],
			"price":        price,
			"c1":           c1,
			"c3":           c3,
			"c5":           c5,
			"combo_action": act,
			"ssp":          ssp,
			"tp":           dec.TakeProfit,
			"sl":           dec.StopLoss,
		})
	}

	fullDecision.Decisions = decisions

	for _, d := range decisions {
		fullDecision.CoTTrace += d.Reasoning + "\n"
	}

	// 3) 验证决策
	if err := decision.ValidateDecisions(decisions, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage); err != nil {
		return fullDecision, fmt.Errorf("决策验证失败: %w", err)
	}

	// 无论是否有错误，都要保存 SystemPrompt 和 UserPrompt（用于调试和决策未执行后的问题定位）
	if fullDecision != nil {
		aiCallEnd := time.Now().UnixMicro()
		fullDecision.Timestamp = time.Now()
		//fullDecision.SystemPrompt = "Spider strategy" // 保存系统prompt
		fullDecision.UserPrompt = fullDecision.SystemPrompt // 保存输入prompt
		fullDecision.AIRequestDurationMs = (aiCallEnd - aiCallStart) / 1000
	}

	log.Printf("Decision end")
	return fullDecision, nil
}

// fetchMarketDataForContext 为上下文中的所有币种获取市场数据和OI数据
func fetchMarketDataForContext(ctx *decision.Context) error {
	ctx.MarketDataMap = make(map[string]*market.Data)
	ctx.OITopDataMap = make(map[string]*decision.OITopData)

	// 收集所有需要获取数据的币种
	symbolSet := make(map[string]bool)

	// 1. 优先获取持仓币种的数据（这是必须的）
	for _, pos := range ctx.Positions {
		symbolSet[pos.Symbol] = true
	}

	// 2. 候选币种数量根据账户状态动态调整
	maxCandidates := calculateMaxCandidates(ctx)
	for i, coin := range ctx.CandidateCoins {
		if i >= maxCandidates {
			break
		}
		symbolSet[coin.Symbol] = true
	}

	// 并发获取市场数据
	// 持仓币种集合（用于判断是否跳过OI检查）
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	for symbol := range symbolSet {
		data, err := market.Get(symbol)
		if err != nil {
			// 单个币种失败不影响整体，只记录错误
			continue
		}

		// ⚠️ 流动性过滤：持仓价值低于阈值的币种不做（多空都不做）
		// 持仓价值 = 持仓量 × 当前价格
		// 但现有持仓必须保留（需要决策是否平仓）
		// 💡 OI 門檻配置：用戶可根據風險偏好調整
		const minOIThresholdMillions = 15.0 // 可調整：15M(保守) / 10M(平衡) / 8M(寬鬆) / 5M(激進)

		isExistingPosition := positionSymbols[symbol]
		if !isExistingPosition && data.OpenInterest != nil && data.CurrentPrice > 0 {
			// 计算持仓价值（USD）= 持仓量 × 当前价格
			oiValue := data.OpenInterest.Latest * data.CurrentPrice
			oiValueInMillions := oiValue / 1_000_000 // 转换为百万美元单位
			if oiValueInMillions < minOIThresholdMillions {
				log.Printf("⚠️  %s 持仓价值过低(%.2fM USD < %.1fM)，跳过此币种 [持仓量:%.0f × 价格:%.4f]",
					symbol, oiValueInMillions, minOIThresholdMillions, data.OpenInterest.Latest, data.CurrentPrice)
				continue
			}
		}

		ctx.MarketDataMap[symbol] = data
	}

	// 加载OI Top数据（不影响主流程）
	oiPositions, err := pool.GetOITopPositions()
	if err == nil {
		for _, pos := range oiPositions {
			// 标准化符号匹配
			symbol := pos.Symbol
			ctx.OITopDataMap[symbol] = &decision.OITopData{
				Rank:              pos.Rank,
				OIDeltaPercent:    pos.OIDeltaPercent,
				OIDeltaValue:      pos.OIDeltaValue,
				PriceDeltaPercent: pos.PriceDeltaPercent,
				NetLong:           pos.NetLong,
				NetShort:          pos.NetShort,
			}
		}
	}

	return nil
}

// calculateMaxCandidates 根据账户状态计算需要分析的候选币种数量
func calculateMaxCandidates(ctx *decision.Context) int {
	// ⚠️ 重要：限制候选币种数量，避免 Prompt 过大
	// 根据持仓数量动态调整：持仓越少，可以分析更多候选币
	const (
		maxCandidatesWhenEmpty    = 30 // 无持仓时最多分析30个候选币
		maxCandidatesWhenHolding1 = 25 // 持仓1个时最多分析25个候选币
		maxCandidatesWhenHolding2 = 20 // 持仓2个时最多分析20个候选币
		maxCandidatesWhenHolding3 = 15 // 持仓3个时最多分析15个候选币（避免 Prompt 过大）
	)

	positionCount := len(ctx.Positions)
	var maxCandidates int

	switch positionCount {
	case 0:
		maxCandidates = maxCandidatesWhenEmpty
	case 1:
		maxCandidates = maxCandidatesWhenHolding1
	case 2:
		maxCandidates = maxCandidatesWhenHolding2
	default: // 3+ 持仓
		maxCandidates = maxCandidatesWhenHolding3
	}

	// 返回实际候选币数量和上限中的较小值
	return min(len(ctx.CandidateCoins), maxCandidates)
}

func checkExitConditions(pos decision.PositionInfo, klines []decision.Kline) decision.Decision {
	var dec = decision.Decision{}

	var price = decimal.NewFromFloat(pos.MarkPrice)

	closed := klines

	action, c1, c3, c5 := fetchCombo()
	c1 = strings.ToUpper(c1)
	c3 = strings.ToUpper(c3)
	c5 = strings.ToUpper(c5)
	ssp, _ := fetchSpiderRaw()
	allRaw := ssp

	fmt.Println("Position side", pos.Side)
	side := strings.ToUpper(pos.Side)
	entryLevel := decimal.NewFromFloat(pos.EntryLevel)
	entryPrice := decimal.NewFromFloat(pos.EntryPrice)

	dec.Symbol = pos.Symbol
	dec.Action = "hold"
	dec.Reasoning = "没有退出信号"

	dec.Level = pos.EntryLevel
	// 1) entry_level 的反向信号
	if d := decideOnLevel(entryLevel, closed, action, priceBandUSD); d != nil {
		if side == "LONG" && d["side"].(string) == "SHORT" {
			//closePosition(b, "reverse_signal_on_entry_level")
			//return
			dec.Action = "close_long"
			dec.Reasoning = "reverse_signal_on_entry_level"
		}
		if side == "SHORT" && d["side"].(string) == "LONG" {
			//closePosition(b, "reverse_signal_on_entry_level")
			//return
			dec.Action = "close_short"
			dec.Reasoning = "reverse_signal_on_entry_level"
		}
	}
	// 2) 下一根蜘蛛丝止盈
	if len(allRaw) > 0 {
		if side == "LONG" {
			if tp := nextLevelAbove(entryLevel, allRaw); tp != nil && price.GreaterThanOrEqual(tp.Sub(priceBandUSD)) {
				//closePosition(b, fmt.Sprintf("hit_tp_level_%s", tp.String()))
				//return
				dec.Action = "close_long"
				dec.Reasoning = fmt.Sprintf("hit_tp_level_%s", tp.String())
			}
		} else {
			if tp := prevLevelBelow(entryLevel, allRaw); tp != nil && price.LessThanOrEqual(tp.Add(priceBandUSD)) {
				//closePosition(b, fmt.Sprintf("hit_tp_level_%s", tp.String()))
				//return
				dec.Action = "close_short"
				dec.Reasoning = fmt.Sprintf("hit_tp_level_%s", tp.String())
			}
		}
	}
	// 3) 硬止损兜底
	if slPctHard.GreaterThan(decimal.Zero) {
		if side == "LONG" {
			sl := entryPrice.Mul(d("1").Sub(slPctHard))
			if price.LessThanOrEqual(sl) {
				//closePosition(b, "hard_stop_loss")
				//return
				dec.Action = "close_long"
				dec.Reasoning = "hard_stop_loss"
			}
		} else {
			sl := entryPrice.Mul(d("1").Add(slPctHard))
			if price.GreaterThanOrEqual(sl) {
				//closePosition(b, "hard_stop_loss")
				//return
				dec.Action = "close_short"
				dec.Reasoning = "hard_stop_loss"
			}
		}
	}

	return dec
}

// ================ 蜘蛛丝获取与过滤 ================

type spiderResp struct {
	P   string
	SSP []json.Number
	T   json.Number
}

func fetchSpiderRaw() ([]decimal.Decimal, decimal.Decimal) {
	req, _ := http.NewRequest(http.MethodGet, spiderURL, nil)
	cli := &http.Client{Timeout: 2 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		log.Println("[SPIDER] fetch error:", err)
		return nil, decimal.NewFromInt(0)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("[SPIDER] non-2xx: %d %s", resp.StatusCode, string(b))
		return nil, decimal.NewFromInt(0)
	}
	var data spiderResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Println("[SPIDER] json error:", err)
		return nil, decimal.NewFromInt(0)
	}
	p := d(data.P)
	ssp := toDecimals(data.SSP)
	log.Println("[SPIDER] RAW ssp=", ssp, "price=", p)
	return ssp, p
}

func toDecimals(ns []json.Number) []decimal.Decimal {
	out := make([]decimal.Decimal, 0, len(ns))
	for _, n := range ns {
		if v, err := decimal.NewFromString(n.String()); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// 仅用于【无持仓找入场】的过滤：配对 gap<minGap 的 L/S 双向剔除
func filterPairsForEntry(shorts, longs []decimal.Decimal, minGap decimal.Decimal) ([]decimal.Decimal, []decimal.Decimal) {
	shorts = dedupSort(shorts)
	longs = dedupSort(longs)
	i, j := 0, 0
	dropS := map[string]struct{}{}
	dropL := map[string]struct{}{}
	for i < len(shorts) && j < len(longs) {
		s, l := shorts[i], longs[j]
		diff := absDec(s.Sub(l))
		if diff.LessThan(minGap) {
			dropS[s.String()] = struct{}{}
			dropL[l.String()] = struct{}{}
			if s.LessThan(l) {
				i++
			} else if l.LessThan(s) {
				j++
			} else {
				i++
				j++
			}
		} else {
			if s.LessThan(l) {
				i++
			} else {
				j++
			}
		}
	}
	fs := make([]decimal.Decimal, 0, len(shorts))
	for _, x := range shorts {
		if _, ok := dropS[x.String()]; !ok {
			fs = append(fs, x)
		}
	}
	fl := make([]decimal.Decimal, 0, len(longs))
	for _, x := range longs {
		if _, ok := dropL[x.String()]; !ok {
			fl = append(fl, x)
		}
	}
	if len(dropS) > 0 || len(dropL) > 0 {
		log.Printf("[SPIDER] ENTRY-FILTER dropped pairs gap<%s", minGap)
	}
	return fs, fl
}

func collectAllLevels(shorts, longs []decimal.Decimal) []decimal.Decimal {
	all := append(append([]decimal.Decimal{}, shorts...), longs...)
	return dedupSort(all)
}

func dedupSort(xs []decimal.Decimal) []decimal.Decimal {
	if len(xs) == 0 {
		return xs
	}
	m := make(map[string]struct{}, len(xs))
	out := make([]decimal.Decimal, 0, len(xs))
	for _, v := range xs {
		k := v.String()
		if _, ok := m[k]; !ok {
			m[k] = struct{}{}
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LessThan(out[j]) })
	return out
}

// ================ 决策（反转点位） ================

func decideOnLevel(level decimal.Decimal, closed []decision.Kline, comboAction string, band decimal.Decimal) map[string]any {
	if len(closed) < 4 {
		return nil
	}
	prev := closed[len(closed)-4]
	last3 := closed[len(closed)-3:]

	// 最近三根是否触碰 band
	touched := false
	for _, k := range last3 {
		if k.Low.LessThanOrEqual(level.Add(band)) && k.High.GreaterThanOrEqual(level.Sub(band)) {
			touched = true
			break
		}
	}
	if !touched {
		return nil
	}

	fromBelow := prev.Close.LessThan(level.Sub(band))
	fromAbove := prev.Close.GreaterThan(level.Add(band))

	standAbove := true
	for _, k := range last3 {
		if k.Close.LessThanOrEqual(level) {
			standAbove = false
			break
		}
	}

	standBelow := true
	for _, k := range last3 {
		if k.Close.GreaterThanOrEqual(level) {
			standBelow = false
			break
		}
	}

	lastClose := last3[len(last3)-1].Close

	// 用组合动作作为方向约束
	wantLong := comboAction == "LONG"
	wantShort := comboAction == "SHORT"

	if fromBelow {
		if standAbove && wantLong {
			return map[string]any{"side": "LONG", "level": level, "reason": "break_up"}
		}
		if !standAbove && lastClose.LessThan(level) && wantShort {
			return map[string]any{"side": "SHORT", "level": level, "reason": "fail_break_up"}
		}
		return nil
	}
	if fromAbove {
		if standBelow && wantShort {
			return map[string]any{"side": "SHORT", "level": level, "reason": "reject_down"}
		}
		if !standBelow && lastClose.GreaterThan(level) && wantLong {
			return map[string]any{"side": "LONG", "level": level, "reason": "fail_break_down"}
		}
		return nil
	}
	return nil
}

func nextLevelAbove(level decimal.Decimal, all []decimal.Decimal) *decimal.Decimal {
	for _, x := range all {
		if x.GreaterThan(level) {
			v := x
			return &v
		}
	}
	return nil
}
func prevLevelBelow(level decimal.Decimal, all []decimal.Decimal) *decimal.Decimal {
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].LessThan(level) {
			v := all[i]
			return &v
		}
	}
	return nil
}

// ================ C3/C5 ================

type c35APIResp struct {
	Data struct {
		Symbol   string `json:"symbol"`
		Interval string `json:"interval"`
		Side     string `json:"side"`
		Agents   struct {
			C1 string `json:"C1"`
			C3 string `json:"C3"`
			C5 string `json:"C5"`
		} `json:"agents"`
		RuleDecision struct {
			Hour  int `json:"hour"`
			Combo struct {
				C1 string `json:"c1"`
				C3 string `json:"c3"`
				C5 string `json:"c5"`
			} `json:"combo"`
			Action         string  `json:"action"`
			Matched        bool    `json:"matched"`
			RuleAccuracy   float64 `json:"rule_accuracy"`
			HourlyAccuracy float64 `json:"hourly_accuracy"`
		} `json:"rule_decision"`
		Timestamp string `json:"timestamp"`
		Status    string `json:"status"`
	} `json:"data"`
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// 统一为 U/D/N 字母
func toLetter(s string) string {
	t := strings.TrimSpace(strings.ToUpper(s))
	switch t {
	case "UP", "LONG", "BUY", "BULL":
		return "U"
	case "DOWN", "SHORT", "SELL", "BEAR":
		return "D"
	default:
		return "N"
	}
}

// 根据 C1 C3 C5 的 U/D/N 组合，返回 LONG/SHORT/NEUTRAL
// 规则：
// UDD→SHORT, DUU→LONG, NDD→SHORT, NUU→LONG,
// UND→SHORT, DND→SHORT, UNU→LONG, DNU→LONG
func actionFromCombo(c1, c3, c5 string) string {
	key := toLetter(c1) + toLetter(c3) + toLetter(c5)
	switch key {
	case "UDD", "NDD", "UND", "DND":
		return "SHORT"
	case "DUU", "NUU", "UNU", "DNU":
		return "LONG"
	default:
		return "NEUTRAL"
	}
}

// 从 hyperaitrade 接口获取组合，并给出动作
func fetchCombo() (action, c1, c3, c5 string) {
	if strings.TrimSpace(c35URL) == "" {
		return "NEUTRAL", "N", "N", "N"
	}

	req, _ := http.NewRequest(http.MethodGet, c35URL, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "spider-go/1.0")
	cli := &http.Client{Timeout: 2 * time.Second}

	resp, err := cli.Do(req)
	if err != nil {
		fmt.Println("[C35] fetch error:", err)
		return "NEUTRAL", "N", "N", "N"
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Printf("[C35] non-2xx: %d %s", resp.StatusCode, string(b))
		return "NEUTRAL", "N", "N", "N"
	}

	var payload c35APIResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		fmt.Println("[C35] json error:", err)
		return "NEUTRAL", "N", "N", "N"
	}
	if payload.Code != 0 {
		fmt.Println("[C35] code!=0:", payload.Code, payload.Msg)
		return "NEUTRAL", "N", "N", "N"
	}

	// 仅 matched==true 才使用
	if !payload.Data.RuleDecision.Matched {
		return "NEUTRAL", "N", "N", "N"
	}

	// 优先 rule_decision.combo（lowercase up/down/neutral）
	c1 = payload.Data.RuleDecision.Combo.C1
	c3 = payload.Data.RuleDecision.Combo.C3
	c5 = payload.Data.RuleDecision.Combo.C5

	// 若为空，回退 agents（可能是 LONG/SHORT/NEUTRAL）
	if strings.TrimSpace(c1) == "" {
		c1 = payload.Data.Agents.C1
	}
	if strings.TrimSpace(c3) == "" {
		c3 = payload.Data.Agents.C3
	}
	if strings.TrimSpace(c5) == "" {
		c5 = payload.Data.Agents.C5
	}

	action = actionFromCombo(c1, c3, c5)

	logTradeEvent("C35_FETCH", map[string]any{
		"symbol":          payload.Data.Symbol,
		"interval":        payload.Data.Interval,
		"matched":         payload.Data.RuleDecision.Matched,
		"combo_c1":        toLetter(c1),
		"combo_c3":        toLetter(c3),
		"combo_c5":        toLetter(c5),
		"action":          action,
		"rule_accuracy":   payload.Data.RuleDecision.RuleAccuracy,
		"hourly_accuracy": payload.Data.RuleDecision.HourlyAccuracy,
	})

	return action, toLetter(c1), toLetter(c3), toLetter(c5)
}

// ================ 日志 ================

func logTradeEvent(event string, payload map[string]any) {
	rec := map[string]any{"ts": time.Now().UTC().Format(time.RFC3339), "event": event}
	for k, v := range payload {
		rec[k] = v
	}
	b, _ := json.Marshal(rec)
	log.Println("[TRADE]", string(b))
	f, err := os.OpenFile(tradeLogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		defer f.Close()
		f.Write(append(b, '\n'))
	}
}

// ================ 工具 ================

func d(s string) decimal.Decimal { v, _ := decimal.NewFromString(s); return v }
func getenv(k, def string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	return v
}
func absDec(v decimal.Decimal) decimal.Decimal {
	if v.IsNegative() {
		return v.Neg()
	}
	return v
}

// ================ 辅助 ================

func mustDec(s string) decimal.Decimal { v, _ := decimal.NewFromString(s); return v }
func sleepUntil(start time.Time, period time.Duration) {
	if d := period - time.Since(start); d > 0 {
		time.Sleep(d)
	}
}

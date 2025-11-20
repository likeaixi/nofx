// spider_c3c5_break_reject.go (execution-layer enhanced)
// 同步最新 Python 版本：
// 1) 反转点位四情形（break_up / fail_break_up / reject_down / fail_break_down）
// 2) 入场只看“当前价±ENTRY_NEAR_RANGE_USD”的蜘蛛丝
// 3) 入场时对 |L-S|<PAIR_MIN_GAP_USD 的配对做成对剔除（仅找入场时）
// 4) 止盈：下一根蜘蛛丝（LONG: >= next(level)-band；SHORT: <= prev(level)+band）
// 5) 可选硬止损 SL_PCT_HARD（兜底）
// 6) **执行层增强**：
//    - LIMIT 挂单生命周期（超时撤单）；
//    - 成交后用真实持仓同步 currentPosition；
//    - 启动时强制 one-way + isolated；
//    - 所有事件 JSONL 日志 spider_trades_log.jsonl。
//
// 依赖：
//   go get github.com/shopspring/decimal
//   go get github.com/adshao/go-binance/v2

package strategy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	futures "github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

// ================== 配置 ==================

var (
	apiKey    = getenv("BINANCE_API_KEY", "YOUR_API_KEY")
	apiSecret = getenv("BINANCE_API_SECRET", "YOUR_API_SECRET")

	symbol = getenv("SYMBOL", "BTCUSDT")

	// 蜘蛛丝（2s 刷新）
	spiderURL = getenv("SPIDER_URL", "http://47.245.93.48:28088/ssp/signal?accessToken=65962631-158e-401b-a87c-a40650f82446")

	// C3/C5（返回 {"c3":"up|down|neutral","c5":"..."}）
	c35URL = getenv("C35_URL", "")

	// 轮询
	pollInterval = 2 * time.Second

	// 触碰 / 穿越价带
	priceBandUSD = d("100")

	// 入场过滤：最近一对 L/S gap < 500U → 成对剔除（仅无持仓找入场时应用）
	pairMinGapUSD = d("500")

	// 开仓参数
	leverage             = 50
	accountEquityUSDT    = d("1000") // 备注
	positionNotionalUSDT = d("200")  // 每次开仓名义 200U

	// 可选硬止损（0 关闭）
	slPctHard = d("0.05") // 5%

	// 仅考虑当前价 ± 这个范围内的蜘蛛丝来找入场
	entryNearRangeUSD = d("2000")

	// LIMIT 挂单生命周期（秒）
	orderLifetimeSec = 60 * time.Second

	// 交易 JSONL 日志文件
	tradeLogFile = getenv("TRADE_LOG_FILE", "spider_trades_log.jsonl")
)

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

// ================ 日志 ================

func logTradeEvent(event string, payload map[string]any) {
	rec := map[string]any{"ts": time.Now().UTC().Format(time.RFC3339), "event": event}
	for k, v := range payload {
		rec[k] = v
	}
	b, _ := json.Marshal(rec)
	fmt.Println("[TRADE]", string(b))
	f, err := os.OpenFile(tradeLogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		defer f.Close()
		f.Write(append(b, '\n'))
	}
}

// ================ Binance 客户端 ================

type Binance struct{ C *futures.Client }

func NewBinance() *Binance {
	c := futures.NewClient(apiKey, apiSecret)
	// 测试网：c.BaseURL = "https://testnet.binancefuture.com"
	return &Binance{C: c}
}

// 读取 symbol 精度：quantityPrecision、pricePrecision、LOT_SIZE.minQty
func (b *Binance) SymbolPrecisions(ctx context.Context, sym string) (qtyPrecision, pricePrecision int, minQty decimal.Decimal, err error) {
	ei, err := b.C.NewExchangeInfoService().Do(ctx)
	if err != nil {
		return
	}
	for _, s := range ei.Symbols {
		if s.Symbol == sym {
			qtyPrecision = s.QuantityPrecision
			pricePrecision = s.PricePrecision
			minQty = d("0.001")
			for _, f := range s.Filters {
				if f["filterType"] == "LOT_SIZE" {
					if v, ok := f["minQty"].(string); ok {
						minQty, _ = decimal.NewFromString(v)
					}
				}
			}
			return
		}
	}
	err = fmt.Errorf("symbol %s not found", sym)
	return
}

func (b *Binance) ChangeLeverage(ctx context.Context, sym string, lev int) {
	if _, err := b.C.NewChangeLeverageService().Symbol(sym).Leverage(lev).Do(ctx); err != nil {
		fmt.Println("[BINANCE] change_leverage error:", err)
	}
}

// 强制 one-way + isolated
func (b *Binance) EnsureOneWayIsolated(ctx context.Context, sym string) {
	// position mode: ONE-WAY
	if pm, err := b.C.NewGetPositionModeService().Do(ctx); err == nil {
		dual := false
		if pm != nil {
			dual = pm.DualSidePosition
		}
		if dual {
			if err := b.C.NewChangePositionModeService().DualSide(false).Do(ctx); err != nil {
				fmt.Println("[INIT] change position mode error:", err)
			} else {
				fmt.Println("[INIT] switched to ONE-WAY")
			}
		}
	} else {
		fmt.Println("[INIT] get position mode error:", err)
	}

	// margin type: ISOLATED
	if err := b.C.NewChangeMarginTypeService().Symbol(sym).MarginType(futures.MarginTypeIsolated).Do(ctx); err != nil {
		// 已经是 ISOLATED 可能报错，忽略
		fmt.Println("[INIT] change margin type warn:", err)
	} else {
		fmt.Println("[INIT] margin ISOLATED")
	}
}

// 最新价（公共 REST）
func (b *Binance) LastPrice(sym string) (decimal.Decimal, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/ticker/price?symbol=%s", sym)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "spider-go/1.0")
	cli := &http.Client{Timeout: 4 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return decimal.Zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		bts, _ := io.ReadAll(resp.Body)
		return decimal.Zero, fmt.Errorf("price non-2xx: %d %s", resp.StatusCode, string(bts))
	}
	var tr struct{ Symbol, Price string }
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return decimal.Zero, err
	}
	p, err := decimal.NewFromString(tr.Price)
	if err != nil {
		return decimal.Zero, err
	}
	return p, nil
}

// 1m K线，返回“已收盘”的数据（剔除最后一根）
func (b *Binance) Klines1mClosed(sym string, limit int) ([]Kline, error) {
	raw, err := b.C.NewKlinesService().Symbol(sym).Interval("1m").Limit(limit + 1).Do(context.Background())
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("no klines")
	}
	raw = raw[:len(raw)-1]
	out := make([]Kline, 0, len(raw))
	for _, k := range raw {
		out = append(out, Kline{
			Open:  mustDec(k.Open),
			High:  mustDec(k.High),
			Low:   mustDec(k.Low),
			Close: mustDec(k.Close),
		})
	}
	return out, nil
}

// 当前仓位（PositionRisk）
func (b *Binance) Position(sym string) (side *string, qty decimal.Decimal, entry *decimal.Decimal, err error) {
	risks, err := b.C.NewGetPositionRiskService().Symbol(sym).Do(context.Background())
	if err != nil {
		return
	}
	if len(risks) == 0 {
		return nil, decimal.Zero, nil, nil
	}
	r := risks[0]
	amt := mustDec(r.PositionAmt)
	ep := mustDec(r.EntryPrice)
	if amt.GreaterThan(decimal.Zero) {
		v := "LONG"
		return &v, amt, &ep, nil
	}
	if amt.LessThan(decimal.Zero) {
		v := "SHORT"
		return &v, amt.Neg(), &ep, nil
	}
	return nil, decimal.Zero, nil, nil
}

// 从交易所同步真实持仓（one-way）
func (b *Binance) SyncPosition(sym string) (map[string]any, error) {
	risks, err := b.C.NewGetPositionRiskService().Symbol(sym).Do(context.Background())
	if err != nil {
		return nil, err
	}
	if len(risks) == 0 {
		return nil, nil
	}
	r := risks[0]
	amt := mustDec(r.PositionAmt)
	if amt.Equal(decimal.Zero) {
		return nil, nil
	}
	side := "LONG"
	if amt.IsNegative() {
		side = "SHORT"
	}
	if amt.IsNegative() {
		amt = amt.Neg()
	}
	ep := mustDec(r.EntryPrice)
	return map[string]any{
		"side":        side,
		"entry_price": ep,
		"qty":         amt,
	}, nil
}

// 订单查询/撤单
func (b *Binance) GetOrder(sym string, orderID int64) (*futures.Order, error) {
	return b.C.NewGetOrderService().Symbol(sym).OrderID(orderID).Do(context.Background())
}
func (b *Binance) CancelOrder(sym string, orderID int64) (*futures.CancelOrderResponse, error) {
	return b.C.NewCancelOrderService().Symbol(sym).OrderID(orderID).Do(context.Background())
}

// 限价入场（GTC）
func (b *Binance) PlaceLimit(sym, side string, qty, price decimal.Decimal) (*futures.CreateOrderResponse, error) {
	orderSide := futures.SideTypeBuy
	if strings.EqualFold(side, "SELL") {
		orderSide = futures.SideTypeSell
	}
	return b.C.NewCreateOrderService().
		Symbol(sym).
		Side(orderSide).
		Type(futures.OrderTypeLimit).
		TimeInForce(futures.TimeInForceTypeGTC).
		Quantity(qty.String()).
		Price(price.String()).
		Do(context.Background())
}

// 市价 reduceOnly 平仓
func (b *Binance) CloseReduceOnly(sym, posSide string, qty decimal.Decimal) (*futures.CreateOrderResponse, error) {
	side := futures.SideTypeSell
	if strings.EqualFold(posSide, "SHORT") {
		side = futures.SideTypeBuy
	}
	return b.C.NewCreateOrderService().
		Symbol(sym).
		Side(side).
		Type(futures.OrderTypeMarket).
		Quantity(qty.String()).
		ReduceOnly(true).
		Do(context.Background())
}

// ================ 结构与全局状态 ================

type Kline struct{ Open, High, Low, Close decimal.Decimal }

type positionState struct {
	side        string
	entryPrice  decimal.Decimal
	qty         decimal.Decimal
	entryLevel  decimal.Decimal
	entryReason string
	orderID     int64
}

type pendingOrderState struct {
	orderID     int64
	orderSide   string // BUY/SELL
	direction   string // LONG/SHORT
	refLevel    decimal.Decimal
	entryReason string
	createdAt   time.Time
	c3, c5      string
}

var currentPosition *positionState
var pendingOrder *pendingOrderState

// 最近一次“原始蜘蛛丝”（不过滤）缓存
var lastShortsRaw, lastLongsRaw []decimal.Decimal

// ================ 蜘蛛丝获取与过滤 ================

type spiderResp struct{ SHORT, LONG []json.Number }

func fetchSpiderRaw() ([]decimal.Decimal, []decimal.Decimal) {
	req, _ := http.NewRequest(http.MethodGet, spiderURL, nil)
	cli := &http.Client{Timeout: 2 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		fmt.Println("[SPIDER] fetch error:", err)
		return fallbackSpiderRaw()
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Printf("[SPIDER] non-2xx: %d %s", resp.StatusCode, string(b))
		return fallbackSpiderRaw()
	}
	var data spiderResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		fmt.Println("[SPIDER] json error:", err)
		return fallbackSpiderRaw()
	}
	shorts := toDecimals(data.SHORT)
	longs := toDecimals(data.LONG)
	shorts = dedupSort(shorts)
	longs = dedupSort(longs)
	lastShortsRaw, lastLongsRaw = shorts, longs
	fmt.Println("[SPIDER] RAW SHORT=", shorts, "LONG=", longs)
	return shorts, longs
}

func fallbackSpiderRaw() ([]decimal.Decimal, []decimal.Decimal) {
	if len(lastShortsRaw) > 0 || len(lastLongsRaw) > 0 {
		fmt.Println("[SPIDER] use last valid raw spider levels")
		return lastShortsRaw, lastLongsRaw
	}
	return nil, nil
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
		fmt.Printf("[SPIDER] ENTRY-FILTER dropped pairs gap<%s", minGap)
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

// ================ C3/C5 ================

func fetchC3C5() (string, string) {
	if strings.TrimSpace(c35URL) == "" {
		return "NEUTRAL", "NEUTRAL"
	}
	req, _ := http.NewRequest(http.MethodGet, c35URL, nil)
	cli := &http.Client{Timeout: time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		fmt.Println("[C35] fetch error:", err)
		return "NEUTRAL", "NEUTRAL"
	}
	defer resp.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		fmt.Println("[C35] json error:", err)
		return "NEUTRAL", "NEUTRAL"
	}
	c3 := strings.ToUpper(fmt.Sprint(data["c3"]))
	c5 := strings.ToUpper(fmt.Sprint(data["c5"]))
	if c3 != "UP" && c3 != "DOWN" {
		c3 = "NEUTRAL"
	}
	if c5 != "UP" && c5 != "DOWN" {
		c5 = "NEUTRAL"
	}
	return c3, c5
}

// ================ 决策（反转点位） ================

func decideOnLevel(level decimal.Decimal, closed []Kline, c3, c5 string, band decimal.Decimal) map[string]any {
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

	c3Up, c5Up := c3 == "UP", c5 == "UP"
	c3Dn, c5Dn := c3 == "DOWN", c5 == "DOWN"

	if fromBelow {
		if standAbove && c3Up && c5Up {
			return map[string]any{"side": "LONG", "level": level, "reason": "break_up"}
		}
		if !standAbove && lastClose.LessThan(level) && c3Dn && c5Dn {
			return map[string]any{"side": "SHORT", "level": level, "reason": "fail_break_up"}
		}
		return nil
	}
	if fromAbove {
		if standBelow && c3Dn && c5Dn {
			return map[string]any{"side": "SHORT", "level": level, "reason": "reject_down"}
		}
		if !standBelow && lastClose.GreaterThan(level) && c3Up && c5Up {
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

// ================ 下单 / 平仓 / 生命周期 ================

func floorToPrecision(x decimal.Decimal, precision int) decimal.Decimal {
	if precision < 0 {
		return x
	}
	step := decimal.New(1, int32(-precision)) // 10^-precision
	return x.Div(step).Floor().Mul(step)
}

func placeEntryOrder(b *Binance, qtyPrecision, pricePrecision int, minQty decimal.Decimal, direction string, refLevel decimal.Decimal, reason, c3, c5 string) {
	if currentPosition != nil {
		fmt.Println("[OPEN] 已有持仓，忽略新信号")
		return
	}
	if pendingOrder != nil {
		fmt.Println("[OPEN] 已有挂单，忽略新信号")
		return
	}

	// 杠杆/逐仓在启动已设；再确保一次杠杆
	b.ChangeLeverage(context.Background(), symbol, leverage)

	price, err := b.LastPrice(symbol)
	if err != nil {
		fmt.Println("[OPEN] 获取价格失败:", err)
		return
	}

	qty := positionNotionalUSDT.Div(price)
	qty = floorToPrecision(qty, qtyPrecision)
	if qty.LessThan(minQty) {
		fmt.Printf("[OPEN] qty too small after rounding (qty=%s < minQty=%s)", qty, minQty)
		return
	}

	limitPrice := floorToPrecision(price, pricePrecision)
	orderSide := "BUY"
	if direction == "SHORT" {
		orderSide = "SELL"
	}

	ord, err := b.PlaceLimit(symbol, orderSide, qty, limitPrice)
	if err != nil {
		fmt.Println("[OPEN] 下单失败:", err)
		return
	}

	logTradeEvent("OPEN_ORDER_PLACED", map[string]any{
		"symbol": symbol, "direction": direction, "order_side": orderSide,
		"price": limitPrice, "qty": qty, "ref_level": refLevel,
		"entry_reason": reason, "order_id": ord.OrderID, "c3": c3, "c5": c5,
	})

	pendingOrder = &pendingOrderState{
		orderID:     ord.OrderID,
		orderSide:   orderSide,
		direction:   direction,
		refLevel:    refLevel,
		entryReason: reason,
		createdAt:   time.Now(),
		c3:          c3, c5: c5,
	}
}

func handlePendingOrder(b *Binance) {
	if pendingOrder == nil {
		return
	}
	ord, err := b.GetOrder(symbol, pendingOrder.orderID)
	if err != nil {
		fmt.Println("[PENDING] get order error:", err)
		return
	}

	status := ord.Status
	executed := mustDec(ord.ExecutedQuantity)

	// A) 成交/部分成交 + 有持仓 → 同步 currentPosition
	if (status == futures.OrderStatusTypeFilled || status == futures.OrderStatusTypePartiallyFilled) && executed.GreaterThan(decimal.Zero) {
		pos, err := b.SyncPosition(symbol)
		if err != nil {
			fmt.Println("[SYNC] position error:", err)
		}
		if pos != nil {
			currentPosition = &positionState{
				side:        pos["side"].(string),
				entryPrice:  pos["entry_price"].(decimal.Decimal),
				qty:         pos["qty"].(decimal.Decimal),
				entryLevel:  pendingOrder.refLevel,
				entryReason: pendingOrder.entryReason,
				orderID:     pendingOrder.orderID,
			}
			logTradeEvent("ORDER_FILLED", map[string]any{
				"symbol": symbol, "order_id": ord.OrderID, "status": status,
				"side": currentPosition.side, "entry_price": currentPosition.entryPrice,
				"qty": currentPosition.qty, "ref_level": currentPosition.entryLevel,
				"entry_reason": currentPosition.entryReason,
			})
			pendingOrder = nil
			return
		}
		logTradeEvent("ORDER_FILLED_NO_POSITION", map[string]any{"symbol": symbol, "order_id": ord.OrderID, "status": status})
		pendingOrder = nil
		return
	}

	// B) 超时未完全成交 -> 撤单 + 同步可能的部分仓位
	if (status == futures.OrderStatusTypeNew || status == futures.OrderStatusTypePartiallyFilled) && time.Since(pendingOrder.createdAt) > orderLifetimeSec {
		if _, err := b.CancelOrder(symbol, pendingOrder.orderID); err != nil {
			fmt.Println("[PENDING] cancel error:", err)
		}
		logTradeEvent("ORDER_CANCELED_TIMEOUT", map[string]any{
			"symbol": symbol, "order_id": ord.OrderID, "status": status, "executedQty": executed,
		})
		// 撤单后同步真实持仓
		if pos, err := b.SyncPosition(symbol); err == nil && pos != nil {
			currentPosition = &positionState{
				side:        pos["side"].(string),
				entryPrice:  pos["entry_price"].(decimal.Decimal),
				qty:         pos["qty"].(decimal.Decimal),
				entryLevel:  pendingOrder.refLevel,
				entryReason: pendingOrder.entryReason,
				orderID:     pendingOrder.orderID,
			}
			logTradeEvent("PARTIAL_POSITION_AFTER_TIMEOUT", map[string]any{
				"symbol": symbol, "side": currentPosition.side, "entry_price": currentPosition.entryPrice, "qty": currentPosition.qty, "order_id": currentPosition.orderID,
			})
		}
		pendingOrder = nil
		return
	}

	// C) 订单被取消/拒绝/过期 -> 清空
	if status == futures.OrderStatusTypeCanceled || status == futures.OrderStatusTypeRejected || status == futures.OrderStatusTypeExpired {
		logTradeEvent("ORDER_CANCELED", map[string]any{"symbol": symbol, "order_id": ord.OrderID, "status": status})
		pendingOrder = nil
		return
	}
}

func closePosition(b *Binance, reason string) {
	if currentPosition == nil {
		return
	}
	side := currentPosition.side
	qty := currentPosition.qty
	if _, err := b.CloseReduceOnly(symbol, side, qty); err != nil {
		fmt.Println("[CLOSE] 失败:", err)
		return
	}
	logTradeEvent("CLOSE_POSITION", map[string]any{"symbol": symbol, "side": side, "qty": qty, "reason": reason})
	currentPosition = nil
}

func checkExitConditions(b *Binance) {
	if currentPosition == nil {
		return
	}
	price, err := b.LastPrice(symbol)
	if err != nil {
		fmt.Println("[EXIT] 获取价格失败:", err)
		return
	}
	closed, err := b.Klines1mClosed(symbol, 10)
	if err != nil {
		fmt.Println("[EXIT] 获取K线失败:", err)
		return
	}
	c3, c5 := fetchC3C5()
	c3 = strings.ToUpper(c3)
	c5 = strings.ToUpper(c5)
	shortsRaw, longsRaw := fetchSpiderRaw()
	allRaw := collectAllLevels(shortsRaw, longsRaw)

	side := currentPosition.side
	entryLevel := currentPosition.entryLevel
	entryPrice := currentPosition.entryPrice

	// 1) entry_level 的反向信号
	if d := decideOnLevel(entryLevel, closed, c3, c5, priceBandUSD); d != nil {
		if side == "LONG" && d["side"].(string) == "SHORT" {
			closePosition(b, "reverse_signal_on_entry_level")
			return
		}
		if side == "SHORT" && d["side"].(string) == "LONG" {
			closePosition(b, "reverse_signal_on_entry_level")
			return
		}
	}
	// 2) 下一根蜘蛛丝止盈
	if len(allRaw) > 0 {
		if side == "LONG" {
			if tp := nextLevelAbove(entryLevel, allRaw); tp != nil && price.GreaterThanOrEqual(tp.Sub(priceBandUSD)) {
				closePosition(b, fmt.Sprintf("hit_tp_level_%s", tp.String()))
				return
			}
		} else {
			if tp := prevLevelBelow(entryLevel, allRaw); tp != nil && price.LessThanOrEqual(tp.Add(priceBandUSD)) {
				closePosition(b, fmt.Sprintf("hit_tp_level_%s", tp.String()))
				return
			}
		}
	}
	// 3) 硬止损兜底
	if slPctHard.GreaterThan(decimal.Zero) {
		if side == "LONG" {
			sl := entryPrice.Mul(d("1").Sub(slPctHard))
			if price.LessThanOrEqual(sl) {
				closePosition(b, "hard_stop_loss")
				return
			}
		} else {
			sl := entryPrice.Mul(d("1").Add(slPctHard))
			if price.GreaterThanOrEqual(sl) {
				closePosition(b, "hard_stop_loss")
				return
			}
		}
	}
}

// ================ 主循环 ================

func main() {
	ctx := context.Background()
	b := NewBinance()

	// 精度 & one-way + isolated & 杠杆
	qtyPrecision, pricePrecision, minQty, err := b.SymbolPrecisions(ctx, symbol)
	if err != nil {
		fmt.Println("[INIT] 读取精度失败:", err)
		return
	}
	fmt.Printf("[INIT] %s qty_precision=%d price_precision=%d minQty=%s equity=%s", symbol, qtyPrecision, pricePrecision, minQty, accountEquityUSDT)
	b.EnsureOneWayIsolated(ctx, symbol)
	b.ChangeLeverage(ctx, symbol, leverage)

	fmt.Println("[RUN] main loop started")
	for {
		start := time.Now()

		// 0) 管理挂单生命周期
		handlePendingOrder(b)

		// 1) 有持仓先做退出逻辑
		checkExitConditions(b)
		if currentPosition != nil || pendingOrder != nil {
			sleepUntil(start, pollInterval)
			continue
		}

		// 2) 无持仓 + 无挂单：找入场
		price, err := b.LastPrice(symbol)
		if err != nil {
			fmt.Println("[ENTRY] 获取价格失败:", err)
			sleepUntil(start, pollInterval)
			continue
		}
		closed, err := b.Klines1mClosed(symbol, 10)
		if err != nil || len(closed) < 4 {
			if err != nil {
				fmt.Println("[ENTRY] 获取K线失败:", err)
			}
			sleepUntil(start, pollInterval)
			continue
		}
		c3, c5 := fetchC3C5()
		c3 = strings.ToUpper(c3)
		c5 = strings.ToUpper(c5)
		if !((c3 == "UP" && c5 == "UP") || (c3 == "DOWN" && c5 == "DOWN")) {
			sleepUntil(start, pollInterval)
			continue
		}

		shortsRaw, longsRaw := fetchSpiderRaw()
		if len(shortsRaw) == 0 || len(longsRaw) == 0 {
			fmt.Println("[ENTRY] only one side spider, pause")
			sleepUntil(start, pollInterval)
			continue
		}

		shorts, longs := filterPairsForEntry(shortsRaw, longsRaw, pairMinGapUSD)
		if len(shorts) == 0 && len(longs) == 0 {
			fmt.Println("[ENTRY] after filter, no valid spider")
			sleepUntil(start, pollInterval)
			continue
		}

		allEntry := collectAllLevels(shorts, longs)
		var candidates []decimal.Decimal
		for _, lv := range allEntry {
			if absDec(lv.Sub(price)).LessThanOrEqual(entryNearRangeUSD) {
				candidates = append(candidates, lv)
			}
		}
		if len(candidates) == 0 {
			sleepUntil(start, pollInterval)
			continue
		}

		var decisions []map[string]any
		for _, lv := range candidates {
			if m := decideOnLevel(lv, closed, c3, c5, priceBandUSD); m != nil {
				decisions = append(decisions, m)
			}
		}
		if len(decisions) == 0 {
			sleepUntil(start, pollInterval)
			continue
		}

		bestIdx := 0
		bestDist := absDec(price.Sub(decisions[0]["level"].(decimal.Decimal)))
		for i := 1; i < len(decisions); i++ {
			d := absDec(price.Sub(decisions[i]["level"].(decimal.Decimal)))
			if d.LessThan(bestDist) {
				bestDist = d
				bestIdx = i
			}
		}
		best := decisions[bestIdx]
		logTradeEvent("OPEN_SIGNAL", map[string]any{
			"symbol":       symbol,
			"direction":    best["side"],
			"ref_level":    best["level"],
			"reason":       best["reason"],
			"price":        price,
			"c3":           c3,
			"c5":           c5,
			"shorts_entry": shorts,
			"longs_entry":  longs,
		})

		placeEntryOrder(b, qtyPrecision, pricePrecision, minQty, best["side"].(string), best["level"].(decimal.Decimal), best["reason"].(string), c3, c5)

		sleepUntil(start, pollInterval)
	}
}

// ================ 辅助 ================

func mustDec(s string) decimal.Decimal { v, _ := decimal.NewFromString(s); return v }
func sleepUntil(start time.Time, period time.Duration) {
	if d := period - time.Since(start); d > 0 {
		time.Sleep(d)
	}
}

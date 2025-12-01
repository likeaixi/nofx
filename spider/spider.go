package spider

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

var (

	// 蜘蛛丝（2s 刷新）
	spiderURL = "http://47.245.93.48:28088/ssp/signal?accessToken=65962631-158e-401b-a87c-a40650f82446"

	// C3/C5（返回 {"c3":"up|down|neutral","c5":"..."}）
	c35URL = "https://api.hyperaitrade.com/api/signal/latest"
)

// ================ 蜘蛛丝获取与过滤 ================

//type SSPResponse struct {
//	P   json.Number   `json:"P"`
//	SSP []json.Number `json:"SSP"`
//	T   json.Number   `json:"T"`
//}

func FetchSpiderRaw() (SSPResponse, error) {
	data := SSPResponse{}

	req, _ := http.NewRequest(http.MethodGet, spiderURL, nil)
	cli := &http.Client{Timeout: 2 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		log.Println("[SPIDER] fetch error:", err)
		return data, fmt.Errorf("[SPIDER] fetch error %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("[SPIDER] non-2xx: %d %s", resp.StatusCode, string(b))
		return data, fmt.Errorf("[SPIDER] non-2xx: %d %s", resp.StatusCode, string(b))
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Println("[SPIDER] json error:", err)
		return data, fmt.Errorf("[SPIDER] json error %v", err)
	}
	log.Printf("[SPIDER] RAW ssp= %v", data)
	return data, nil
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
func FetchCombo() (action, c1, c3, c5 string) {
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
}

// ================ 工具 ================

func d(s string) decimal.Decimal { v, _ := decimal.NewFromString(s); return v }

func absDec(v decimal.Decimal) decimal.Decimal {
	if v.IsNegative() {
		return v.Neg()
	}
	return v
}

// Package binancecopy reads the public, delayed order history of the fixed lead portfolio.
package binancecopy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"nofx/trader/execution"
)

const PortfolioID = "5075281354358777856"

type Client struct {
	BaseURL      string
	HTTP         *http.Client
	PageSize     int
	LeadID       string
	historyPacer *historyPacer
}

func NewForPortfolio(id string) *Client { c := New(); c.LeadID = id; return c }
func (c *Client) portfolioID() string {
	if c.LeadID == "" {
		return PortfolioID
	}
	return c.LeadID
}

func New() *Client {
	return &Client{BaseURL: "https://www.binance.com/bapi/futures/v1/friendly/future/copy-trade/lead-portfolio", HTTP: &http.Client{Timeout: 10 * time.Second}, PageSize: 50, LeadID: PortfolioID, historyPacer: publicHistoryPacer}
}

type Event struct {
	Symbol, BaseAsset, QuoteAsset, Side, PositionSide string
	ExecutedQty, AvgPrice                             decimal.Decimal
	OrderTime, OrderUpdateTime                        int64
}

func (e Event) Key() string {
	return fmt.Sprintf("%s/%d/%s/%s", e.Symbol, e.OrderTime, e.Side, e.PositionSide)
}
func (e Event) Fingerprint() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%s/%s/%s/%s", e.Key(), e.OrderUpdateTime, e.ExecutedQty, e.AvgPrice, e.BaseAsset, e.QuoteAsset))))
}
func (e Event) Opening() bool {
	return e.Side == "BUY" && e.PositionSide == "LONG" || e.Side == "SELL" && e.PositionSide == "SHORT"
}

type Snapshot struct {
	Head   string
	Events []Event
}

func (c *Client) request(ctx context.Context, method, path string, body []byte, out any) error {
	if c.historyPacer != nil && (path == "/order-history" || path == "/position-history") {
		release, err := c.historyPacer.acquire(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("clienttype", "web")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://www.binance.com/en/copy-trading/lead-details/"+c.portfolioID())
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("lead data HTTP %d", res.StatusCode)
	}
	var envelope struct {
		Success bool
		Code    string
		Data    json.RawMessage
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return err
	}
	if json.Unmarshal(b, &envelope) != nil || !envelope.Success || envelope.Code != "000000" || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("invalid lead data response from %s (code=%s)", path, envelope.Code)
	}
	return json.Unmarshal(envelope.Data, out)
}
func (c *Client) Balance(ctx context.Context) (decimal.Decimal, error) {
	var d struct{ LeadPortfolioId, FuturesType, InitInvestAsset, Status, MarginBalance string }
	if err := c.request(ctx, "GET", "/detail?portfolioId="+c.portfolioID(), nil, &d); err != nil {
		return decimal.Zero, err
	}
	if d.LeadPortfolioId != c.portfolioID() || d.FuturesType != "UM" || d.InitInvestAsset != "USDT" || d.Status != "ACTIVE" || !execution.Positive(execution.Decimal(d.MarginBalance)) {
		return decimal.Zero, errors.New("active lead USDT margin balance unavailable")
	}
	return decimal.NewFromString(d.MarginBalance)
}
func (c *Client) page(ctx context.Context, n int) ([]Event, error) {
	payload, _ := json.Marshal(map[string]any{"portfolioId": c.portfolioID(), "pageNumber": n, "pageSize": c.PageSize})
	// Validate raw numbers before converting. In particular, never accept a large exponent.
	var page struct {
		List []struct {
			Symbol, BaseAsset, QuoteAsset, Side, PositionSide string
			ExecutedQty, AvgPrice                             json.Number
			OrderTime, OrderUpdateTime                        int64
		}
	}
	if err := c.request(ctx, "POST", "/order-history", payload, &page); err != nil {
		return nil, err
	}
	if len(page.List) == 0 || len(page.List) > c.PageSize {
		return nil, errors.New("lead history empty or page boundary invalid")
	}
	events := make([]Event, 0, len(page.List))
	for _, r := range page.List {
		if r.Symbol == "" || r.BaseAsset == "" || r.QuoteAsset == "" || r.OrderTime <= 0 || r.OrderUpdateTime < r.OrderTime || !execution.Positive(execution.Decimal(r.ExecutedQty)) || !execution.Positive(execution.Decimal(r.AvgPrice)) {
			return nil, errors.New("invalid lead fill")
		}
		q, _ := decimal.NewFromString(string(r.ExecutedQty))
		p, _ := decimal.NewFromString(string(r.AvgPrice))
		events = append(events, Event{r.Symbol, r.BaseAsset, r.QuoteAsset, r.Side, r.PositionSide, q, p, r.OrderTime, r.OrderUpdateTime})
	}
	return events, nil
}

// Snapshot requires the prior page head to remain visible before exposing any
// events. A page shift or repeated ambiguous identity blocks the whole batch.
func (c *Client) Snapshot(ctx context.Context, anchor string) (Snapshot, error) {
	var out Snapshot
	seen := map[string]bool{}
	var first []Event
	last := int64(1<<63 - 1)
	for n := 1; n <= 12; n++ {
		page, err := c.page(ctx, n)
		if err != nil {
			return Snapshot{}, err
		}
		if n == 1 {
			first = page
			out.Head = page[0].Fingerprint()
		}
		found := anchor == ""
		for _, e := range page {
			if e.OrderUpdateTime > last {
				return Snapshot{}, errors.New("unordered lead history")
			}
			last = e.OrderUpdateTime
			if seen[e.Key()] {
				return Snapshot{}, errors.New("ambiguous lead identity or shifted pagination")
			}
			seen[e.Key()] = true
			out.Events = append(out.Events, e)
			if e.Fingerprint() == anchor {
				found = true
			}
		}
		if found {
			if n > 1 {
				again, err := c.page(ctx, 1)
				if err != nil {
					return Snapshot{}, err
				}
				if len(again) != len(first) {
					return Snapshot{}, errors.New("lead page shifted")
				}
				for i := range again {
					if again[i].Fingerprint() != first[i].Fingerprint() {
						return Snapshot{}, errors.New("lead page shifted")
					}
				}
			}
			sort.SliceStable(out.Events, func(i, j int) bool { return out.Events[i].OrderUpdateTime < out.Events[j].OrderUpdateTime })
			return out, nil
		}
		if len(page) < c.PageSize {
			break
		}
	}
	return Snapshot{}, errors.New("gap in lead history; previous anchor unavailable")
}

// PositionBefore reconstructs the pre-fill lead quantity from a verified flat
// boundary. The public position list can hide open positions; absence from
// that list is never evidence that a symbol was flat.
func (c *Client) PositionBefore(ctx context.Context, e Event) (decimal.Decimal, error) {
	var closed int64
	for n := 1; n <= 12 && closed == 0; n++ {
		var data struct {
			List []struct {
				Symbol, Type, Side, Status string
				Opened, Closed             int64
			}
		}
		payload, _ := json.Marshal(map[string]any{"portfolioId": c.portfolioID(), "pageNumber": n, "pageSize": c.PageSize})
		if err := c.request(ctx, "POST", "/position-history", payload, &data); err != nil {
			return decimal.Zero, err
		}
		if data.List == nil {
			return decimal.Zero, errors.New("lead position history unavailable")
		}
		for _, p := range data.List {
			if p.Symbol == e.Symbol && strings.EqualFold(p.Side, e.PositionSide) && p.Type == "UM" && p.Status == "All Closed" && p.Opened > 0 && p.Closed > p.Opened && p.Closed < e.OrderTime && p.Closed > closed {
				closed = p.Closed
			}
		}
		if len(data.List) < c.PageSize {
			break
		}
	}
	if closed == 0 {
		return decimal.Zero, errors.New("no verified flat boundary for lead position")
	}
	var rows, first []Event
	seen := map[string]bool{}
	last := int64(1<<63 - 1)
	crossed, found := false, false
	for n := 1; n <= 24 && !crossed; n++ {
		page, err := c.page(ctx, n)
		if err != nil {
			return decimal.Zero, err
		}
		if n == 1 {
			first = page
		}
		for _, p := range page {
			if p.OrderUpdateTime > last || seen[p.Key()] {
				return decimal.Zero, errors.New("ambiguous lead history during position reconstruction")
			}
			last = p.OrderUpdateTime
			seen[p.Key()] = true
			if p.Fingerprint() == e.Fingerprint() {
				found = true
			}
			if p.OrderUpdateTime < closed {
				crossed = true
			}
			if p.Symbol != e.Symbol || p.PositionSide != e.PositionSide || p.Fingerprint() == e.Fingerprint() {
				continue
			}
			if p.OrderUpdateTime == e.OrderUpdateTime || (p.OrderUpdateTime == closed && p.Opening()) {
				return decimal.Zero, errors.New("ambiguous lead fills at position boundary")
			}
			if p.OrderUpdateTime > closed && p.OrderUpdateTime < e.OrderUpdateTime {
				rows = append(rows, p)
			}
		}
		if len(page) < c.PageSize {
			break
		}
	}
	if !found || !crossed {
		return decimal.Zero, errors.New("lead fills do not cover verified flat boundary")
	}
	again, err := c.page(ctx, 1)
	if err != nil {
		return decimal.Zero, err
	}
	if len(again) != len(first) {
		return decimal.Zero, errors.New("lead history moved during reconstruction")
	}
	for i := range first {
		if again[i].Fingerprint() != first[i].Fingerprint() {
			return decimal.Zero, errors.New("lead history moved during reconstruction")
		}
	}
	var net decimal.Decimal
	for i := len(rows) - 1; i >= 0; i-- {
		p := rows[i]
		if p.Side != "BUY" && p.Side != "SELL" {
			return decimal.Zero, errors.New("unknown side in lead history")
		}
		if p.Opening() {
			net = net.Add(p.ExecutedQty)
		} else {
			net = net.Sub(p.ExecutedQty)
		}
		if net.IsNegative() {
			return decimal.Zero, errors.New("incomplete historical lead position")
		}
	}
	return net, nil
}

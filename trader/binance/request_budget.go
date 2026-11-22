package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"nofx/logger"
	"nofx/trader/execution"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Shared within this process and host: API keys do not get separate IP budgets.
// The initial conservative ceiling is replaced by exchangeInfo's minute limit.
var sharedRequestBudgets = &budgetRegistry{items: map[string]*requestBudget{}}

type budgetRegistry struct {
	mu    sync.Mutex
	items map[string]*requestBudget
}

func (r *budgetRegistry) get(host string) *requestBudget {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items == nil {
		r.items = map[string]*requestBudget{}
	}
	b := r.items[host]
	if b == nil {
		b = &requestBudget{limit: 1200, now: time.Now}
		r.items[host] = b
	}
	return b
}

type requestBudget struct {
	mu                    sync.Mutex
	limit, used, inflight int
	minute, reportMinute  int64
	until, refreshed      time.Time
	refreshing            bool
	now                   func() time.Time
}

func (b *requestBudget) reserve(cost int, urgent bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if now.Before(b.until) {
		return &execution.RetryableError{Message: "Binance request cooldown active", Delay: b.until.Sub(now)}
	}
	minute := now.Unix() / 60
	if minute != b.minute {
		b.minute = minute
		b.used = b.inflight
	}
	ceiling := b.limit
	if !urgent {
		ceiling = ceiling * 80 / 100
	}
	if b.used+cost > ceiling && !(urgent && cost == 0) {
		return &execution.RetryableError{Message: "Binance shared request budget exhausted", Delay: time.Unix((minute+1)*60, 0).Sub(now)}
	}
	b.used += cost
	b.inflight += cost
	return nil
}
func retryDelay(h http.Header, now time.Time) time.Duration {
	if n, e := strconv.Atoi(h.Get("Retry-After")); e == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(h.Get("Retry-After")); e == nil && t.After(now) {
		return t.Sub(now)
	}
	return 5 * time.Second
}
func (b *requestBudget) observe(h http.Header, status int, started time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	// Carry unfinished reservations across local minute boundaries. A delayed
	// response may have been charged in either window; conservatively apply its
	// high watermark rather than releasing capacity based on local send time.
	if b.minute != now.Unix()/60 {
		b.minute = now.Unix() / 60
		b.used = b.inflight
	}
	if used, e := strconv.Atoi(h.Get("X-MBX-USED-WEIGHT-1M")); e == nil && used+b.inflight > b.used {
		b.used = used + b.inflight
	}
	if raw := h.Get("X-MBX-USED-WEIGHT-1M"); raw != "" && b.reportMinute != now.Unix()/60 {
		if used, e := strconv.Atoi(raw); e == nil {
			b.reportMinute = now.Unix() / 60
			logger.Infof("Binance request weight: used_1m=%d local_estimate=%d limit_1m=%d", used, b.used, b.limit)
		}
	}
	if status == 429 || status == 418 {
		until := now.Add(retryDelay(h, now))
		if until.After(b.until) {
			b.until = until
		}
	}
}
func (b *requestBudget) finish(cost int) { b.mu.Lock(); b.inflight -= cost; b.mu.Unlock() }
func requestWeight(r *http.Request) int {
	switch r.URL.Path {
	case "/fapi/v1/order":
		if r.Method == http.MethodPost {
			return 0
		}
		return 1
	case "/fapi/v1/algoOrder":
		if r.Method == http.MethodPost {
			return 0
		}
		return 1
	case "/fapi/v1/time", "/fapi/v1/exchangeInfo", "/fapi/v1/leverage":
		return 1
	case "/fapi/v2/account", "/fapi/v3/account", "/fapi/v2/positionRisk", "/fapi/v3/positionRisk":
		return 5
	case "/fapi/v1/commissionRate":
		return 20
	case "/fapi/v1/positionSide/dual", "/fapi/v1/multiAssetsMargin":
		if r.Method == http.MethodGet {
			return 30
		}
		return 1
	case "/fapi/v1/openOrders", "/fapi/v1/openAlgoOrders":
		if r.URL.Query().Get("symbol") != "" {
			return 1
		}
		return 40
	default:
		return 50 // Conservative for endpoints not yet classified.
	}
}

type binanceHTTPError struct {
	status, code int
	delay        time.Duration
	local        string
}

func (e *binanceHTTPError) Error() string {
	if e.local != "" {
		return "Binance transport failure (" + e.local + ")"
	}
	return fmt.Sprintf("Binance HTTP %d (code %d)", e.status, e.code)
}

type budgetTransport struct {
	base    http.RoundTripper
	budgets *budgetRegistry
}

func (t *budgetTransport) refresh(ctx context.Context, req *http.Request, b *requestBudget) {
	b.mu.Lock()
	if b.refreshing || b.now().Sub(b.refreshed) < 10*time.Minute {
		b.mu.Unlock()
		return
	}
	b.refreshing = true
	b.refreshed = b.now()
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.refreshing = false; b.mu.Unlock() }()
	// Public metadata only; never copy signed query or API-key headers.
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	u := *req.URL
	u.Path = "/fapi/v1/exchangeInfo"
	u.RawQuery = ""
	meta, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err := b.reserve(1, false); err != nil {
		return
	}
	started := b.now()
	res, err := t.base.RoundTrip(meta)
	b.finish(1)
	if err != nil {
		return
	}
	defer res.Body.Close()
	b.observe(res.Header, res.StatusCode, started)
	if res.StatusCode != 200 {
		return
	}
	var payload struct {
		RateLimits []struct {
			Type        string `json:"rateLimitType"`
			Interval    string `json:"interval"`
			IntervalNum int    `json:"intervalNum"`
			Limit       int    `json:"limit"`
		} `json:"rateLimits"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&payload) != nil {
		return
	}
	for _, l := range payload.RateLimits {
		if l.Type == "REQUEST_WEIGHT" && l.Interval == "MINUTE" && l.IntervalNum == 1 && l.Limit > 0 {
			b.mu.Lock()
			b.limit = l.Limit
			b.mu.Unlock()
			logger.Infof("Binance request budget: minute_limit=%d ordinary_share=80%%", l.Limit)
			return
		}
	}
}
func (t *budgetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	b := t.budgets.get(req.URL.Host)
	started := b.now()
	t.refresh(req.Context(), req, b)
	if req.Context().Err() != nil {
		return safeTransportFailure(req, req.Context().Err()), nil
	}
	// Do not queue signed requests; their timestamp would expire in a queue.
	cost := requestWeight(req)
	if err := b.reserve(cost, execution.ProtectivePriority(req.Context())); err != nil {
		var retry *execution.RetryableError
		delay := 5 * time.Second
		if errors.As(err, &retry) {
			delay = retry.Delay
		}
		return safeBudgetResponse(req, 429, -1003, delay), nil
	}
	res, err := t.base.RoundTrip(req)
	b.finish(cost)
	if err != nil {
		return safeTransportFailure(req, err), nil
	}
	b.observe(res.Header, res.StatusCode, started)
	if res.StatusCode >= 400 {
		defer res.Body.Close()
		var payload struct {
			Code int `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&payload)
		delay := retryDelay(res.Header, b.now())
		used, _ := strconv.Atoi(res.Header.Get("X-MBX-USED-WEIGHT-1M"))
		logger.Warnf("Binance request failed: method=%s path=%s status=%d code=%d used_weight_1m=%d retry_after_seconds=%.0f", req.Method, req.URL.Path, res.StatusCode, payload.Code, used, delay.Seconds())
		return safeBudgetResponse(req, res.StatusCode, payload.Code, delay), nil
	}
	return res, nil
}
func protectBinanceHTTP(c *http.Client) *http.Client {
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	copy := *c
	base := copy.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*budgetTransport); !ok {
		copy.Transport = &budgetTransport{base, sharedRequestBudgets}
	}
	return &copy
}

// Return sanitized HTTP responses rather than transport errors: http.Client
// wraps transport errors with a URL that may contain a signature.
func safeBudgetResponse(req *http.Request, status, code int, delay time.Duration, local ...string) *http.Response {
	msg := fmt.Sprintf("NOFX_HTTP status=%d retry_ms=%d", status, delay.Milliseconds())
	if len(local) > 0 {
		msg += " local=" + local[0]
	}
	switch code {
	case -4046:
		msg += " No need to change margin type"
	case -4059:
		msg += " No need to change position side"
	}
	body, _ := json.Marshal(struct {
		Code    int    `json:"code"`
		Message string `json:"msg"`
	}{code, msg})
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	h.Set("Retry-After", strconv.FormatInt(int64(delay/time.Second), 10))
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: h, Body: io.NopCloser(strings.NewReader(string(body))), ContentLength: int64(len(body)), Request: req}
}
func safeHTTPMetadata(message string, code int64) *binanceHTTPError {
	if !strings.HasPrefix(message, "NOFX_HTTP ") {
		return nil
	}
	var status int
	var millis int64
	if n, _ := fmt.Sscanf(message, "NOFX_HTTP status=%d retry_ms=%d", &status, &millis); n != 2 || status < 400 || status > 599 || millis < 0 {
		return nil
	}
	meta := &binanceHTTPError{status: status, code: int(code), delay: time.Duration(millis) * time.Millisecond}
	for _, kind := range []string{"canceled", "temporary", "permanent"} {
		if strings.Contains(message, " local="+kind) {
			meta.local = kind
			break
		}
	}
	return meta
}

func safeTransportFailure(req *http.Request, err error) *http.Response {
	if errors.Is(err, context.Canceled) {
		return safeBudgetResponse(req, 400, -1000, 0, "canceled")
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &network) && (network.Timeout() || network.Temporary())) {
		return safeBudgetResponse(req, 503, -1001, 5*time.Second, "temporary")
	}
	return safeBudgetResponse(req, 400, -1000, 0, "permanent")
}

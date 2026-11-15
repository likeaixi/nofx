package market

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func oiRankingTestServers(t *testing.T) (*atomic.Int32, *atomic.Int32) {
	t.Helper()
	var tickerCalls, histCalls atomic.Int32

	ticker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tickerCalls.Add(1)
		// Real Binance payloads serialize numbers as strings.
		_, _ = w.Write([]byte(`[
			{"symbol":"BTCUSDT","quoteVolume":"5000000000"},
			{"symbol":"ETHUSDT","quoteVolume":"3000000000"},
			{"symbol":"SOLUSDT","quoteVolume":"1000000000"},
			{"symbol":"DOGEUSDT","quoteVolume":"500000000"},
			{"symbol":"BTCUSDT_250627","quoteVolume":"9000000000"},
			{"symbol":"1000PEPEUSDT","quoteVolume":"400000000"},
			{"symbol":"ADAUSDT","quoteVolume":"300000000"}
		]`))
	}))
	t.Cleanup(ticker.Close)

	hist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		histCalls.Add(1)
		symbol := r.URL.Query().Get("symbol")
		w.Header().Set("Content-Type", "application/json")
		switch symbol {
		case "BTCUSDT": // strong increase
			_, _ = w.Write([]byte(`[{"sumOpenInterest":"100"},{"sumOpenInterest":"150"}]`))
		case "ETHUSDT": // mild increase
			_, _ = w.Write([]byte(`[{"sumOpenInterest":"200"},{"sumOpenInterest":"210"}]`))
		case "SOLUSDT": // decrease
			_, _ = w.Write([]byte(`[{"sumOpenInterest":"300"},{"sumOpenInterest":"250"}]`))
		case "DOGEUSDT": // single point — must be skipped
			_, _ = w.Write([]byte(`[{"sumOpenInterest":"50"}]`))
		case "1000PEPEUSDT": // strongest increase
			_, _ = w.Write([]byte(`[{"sumOpenInterest":"10"},{"sumOpenInterest":"80"}]`))
		default: // ADAUSDT and anything unknown
			_, _ = w.Write([]byte(`[{"sumOpenInterest":"1"},{"sumOpenInterest":"1"}]`))
		}
	}))
	t.Cleanup(hist.Close)

	exchangeInfo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbols":[
			{"symbol":"BTCUSDT","underlyingType":"COIN"},
			{"symbol":"ETHUSDT","underlyingType":"COIN"},
			{"symbol":"SOLUSDT","underlyingType":"COIN"},
			{"symbol":"DOGEUSDT","underlyingType":"COIN"},
			{"symbol":"1000PEPEUSDT","underlyingType":"COIN"},
			{"symbol":"ADAUSDT","underlyingType":"COIN"},
			{"symbol":"XAGUSDT","underlyingType":"COMMODITY"}
		]}`))
	}))
	t.Cleanup(exchangeInfo.Close)

	previousTick, previousHist, previousInfo := oiRankingTickerURL, oiRankingHistURL, oiRankingExchangeInfoURL
	oiRankingTickerURL, oiRankingHistURL, oiRankingExchangeInfoURL = ticker.URL, hist.URL, exchangeInfo.URL
	t.Cleanup(func() {
		oiRankingTickerURL, oiRankingHistURL, oiRankingExchangeInfoURL = previousTick, previousHist, previousInfo
	})

	resetOIRankingCacheForTest()
	t.Cleanup(resetOIRankingCacheForTest)
	return &tickerCalls, &histCalls
}

func TestBinanceOIRankingExcludesTradFiUnderlyings(t *testing.T) {
	_, _ = oiRankingTestServers(t)

	// Re-point the ticker at a board where silver (COMMODITY) tops the volume.
	silver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"symbol":"XAGUSDT","quoteVolume":"9000000000"},
			{"symbol":"BTCUSDT","quoteVolume":"5000000000"}
		]`))
	}))
	t.Cleanup(silver.Close)
	oiRankingTickerURL = silver.URL
	resetOIRankingCacheForTest()

	got, err := GetBinanceOIRanking("top", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "BTCUSDT" {
		t.Fatalf("tradFi underlyings must be excluded, got %v", got)
	}
}

func TestBinanceOIRankingTopOrdersByIncrease(t *testing.T) {
	_, _ = oiRankingTestServers(t)

	got, err := GetBinanceOIRanking("top", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1000PEPEUSDT", "BTCUSDT", "ETHUSDT"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rank %d: got %s, want %s (full: %v)", i, got[i], want[i], got)
		}
	}
	if strings.Contains(strings.Join(got, ","), "BTCUSDT_250627") {
		t.Fatal("delivery contract leaked into ranking")
	}
	if strings.Contains(strings.Join(got, ","), "DOGEUSDT") {
		t.Fatal("single-point symbol must be skipped")
	}
}

func TestBinanceOIRankingLowOrdersByDecrease(t *testing.T) {
	_, _ = oiRankingTestServers(t)

	got, err := GetBinanceOIRanking("low", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "SOLUSDT" {
		t.Fatalf("low ranking = %v, want [SOLUSDT]", got)
	}
}

func TestBinanceOIRankingCacheReusesFetch(t *testing.T) {
	tickerCalls, histCalls := oiRankingTestServers(t)

	if _, err := GetBinanceOIRanking("top", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := GetBinanceOIRanking("low", 5); err != nil {
		t.Fatal(err)
	}
	if c := tickerCalls.Load(); c != 1 {
		t.Fatalf("ticker fetched %d times, want 1 (cached)", c)
	}
	if c := histCalls.Load(); c > 12 {
		t.Fatalf("history fetched %d times, suspicious", c)
	}
}

func TestBinanceOIRankingEmptyUniverse(t *testing.T) {
	tickerCalls, _ := oiRankingTestServers(t)
	_ = tickerCalls
	// fresh empty servers
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(empty.Close)
	oiRankingTickerURL = empty.URL
	resetOIRankingCacheForTest()

	got, err := GetBinanceOIRanking("top", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty ranking, got %v", got)
	}
}

package market

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Binance-native open-interest ranking over public (unauthenticated)
// endpoints. This replaces the removed claw402/nofxos paid OI ranking that
// used to feed the oi_top / oi_low strategy coin sources: the trader's own
// venue data needs no wallet and no third-party subscription.

var (
	oiRankingTickerURL       = "https://fapi.binance.com/fapi/v1/ticker/24hr"
	oiRankingHistURL         = "https://fapi.binance.com/futures/data/openInterestHist"
	oiRankingExchangeInfoURL = "https://fapi.binance.com/fapi/v1/exchangeInfo"
	oiRankingClient          = &http.Client{Timeout: 15 * time.Second}
)

const (
	// oiRankingUniverseSize caps how many top-by-volume USDT perps get their
	// OI history polled per ranking refresh.
	oiRankingUniverseSize = 40
	oiRankingCacheTTL     = 60 * time.Second
	oiRankingConcurrency  = 8
)

// OIRankedSymbol is one universe entry with its 1h open-interest change.
type OIRankedSymbol struct {
	Symbol  string  // raw pair, e.g. BTCUSDT
	OIDelta float64 // open-interest change over the last 1h (contracts)
}

var oiRankingCache struct {
	mu        sync.Mutex
	fetchedAt time.Time
	rows      []OIRankedSymbol
	err       error
}

func resetOIRankingCacheForTest() {
	oiRankingCache.mu.Lock()
	defer oiRankingCache.mu.Unlock()
	oiRankingCache.fetchedAt = time.Time{}
	oiRankingCache.rows = nil
	oiRankingCache.err = nil
	coinPerpCache.mu.Lock()
	defer coinPerpCache.mu.Unlock()
	coinPerpCache.fetchedAt = time.Time{}
	coinPerpCache.symbols = nil
}

// coinPerpCache remembers which USDT pairs are crypto (underlyingType COIN)
// per Binance exchangeInfo. Small payload subset, refreshed rarely.
var coinPerpCache struct {
	mu        sync.Mutex
	fetchedAt time.Time
	symbols   map[string]bool
}

const coinPerpCacheTTL = 6 * time.Hour

// binanceCoinPerpSymbols returns the COIN-underlying symbol set, or nil when
// exchangeInfo is unavailable (callers then keep the suffix-only filter).
func binanceCoinPerpSymbols() map[string]bool {
	coinPerpCache.mu.Lock()
	if coinPerpCache.symbols != nil && time.Since(coinPerpCache.fetchedAt) < coinPerpCacheTTL {
		cached := coinPerpCache.symbols
		coinPerpCache.mu.Unlock()
		return cached
	}
	coinPerpCache.mu.Unlock()

	resp, err := oiRankingClient.Get(oiRankingExchangeInfoURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var info struct {
		Symbols []struct {
			Symbol         string `json:"symbol"`
			UnderlyingType string `json:"underlyingType"`
		} `json:"symbols"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&info); err != nil {
		return nil
	}
	coins := make(map[string]bool, len(info.Symbols))
	for _, s := range info.Symbols {
		if s.UnderlyingType == "COIN" {
			coins[s.Symbol] = true
		}
	}

	coinPerpCache.mu.Lock()
	coinPerpCache.fetchedAt = time.Now()
	coinPerpCache.symbols = coins
	coinPerpCache.mu.Unlock()
	return coins
}

// GetBinanceOIRanking returns USDT perpetuals ranked by 1h open-interest
// change. mode "top" = largest increase first; "low" = largest decrease
// first. Results are cached for a minute and shared by both modes.
func GetBinanceOIRanking(mode string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}

	oiRankingCache.mu.Lock()
	if oiRankingCache.err == nil && time.Since(oiRankingCache.fetchedAt) < oiRankingCacheTTL {
		rows := oiRankingCache.rows
		oiRankingCache.mu.Unlock()
		return rankedSymbols(rows, mode, limit), nil
	}
	oiRankingCache.mu.Unlock()

	rows, err := fetchBinanceOIRanking()
	oiRankingCache.mu.Lock()
	oiRankingCache.fetchedAt = time.Now()
	oiRankingCache.rows, oiRankingCache.err = rows, err
	oiRankingCache.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return rankedSymbols(rows, mode, limit), nil
}

func rankedSymbols(rows []OIRankedSymbol, mode string, limit int) []string {
	sorted := make([]OIRankedSymbol, len(rows))
	copy(sorted, rows)
	if mode == "low" {
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].OIDelta < sorted[j].OIDelta })
	} else {
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].OIDelta > sorted[j].OIDelta })
	}
	out := make([]string, 0, limit)
	for _, row := range sorted {
		if len(out) >= limit {
			break
		}
		out = append(out, row.Symbol)
	}
	return out
}

func fetchBinanceOIRanking() ([]OIRankedSymbol, error) {
	resp, err := oiRankingClient.Get(oiRankingTickerURL)
	if err != nil {
		return nil, fmt.Errorf("oi ranking universe unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oi ranking universe unavailable: HTTP %d", resp.StatusCode)
	}
	var tickers []struct {
		Symbol      string      `json:"symbol"`
		QuoteVolume json.Number `json:"quoteVolume"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&tickers); err != nil {
		return nil, fmt.Errorf("oi ranking universe parse failed: %w", err)
	}

	// USDT perps only; "_" marks delivery contracts we cannot trade spot-perp.
	type universeEntry struct {
		symbol string
		volume float64
	}
	entries := make([]universeEntry, 0, len(tickers))
	for _, tk := range tickers {
		v, _ := tk.QuoteVolume.Float64()
		entries = append(entries, universeEntry{symbol: tk.Symbol, volume: v})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].volume > entries[j].volume })
	// Crypto perps only: Binance lists commodity (XAU/XAG), equity and index
	// TradFi perps with USDT quotes too — those need a separate trading
	// agreement and do not belong in a crypto OI ranking. Filter falls back
	// to the suffix rule when exchangeInfo is unavailable.
	coinPerps := binanceCoinPerpSymbols()
	universe := make([]string, 0, oiRankingUniverseSize)
	for _, entry := range entries {
		if len(universe) >= oiRankingUniverseSize {
			break
		}
		if strings.HasSuffix(entry.symbol, "USDT") && !strings.Contains(entry.symbol, "_") {
			if coinPerps != nil && !coinPerps[entry.symbol] {
				continue
			}
			universe = append(universe, entry.symbol)
		}
	}
	if len(universe) == 0 {
		return nil, nil
	}

	sem := make(chan struct{}, oiRankingConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	rows := make([]OIRankedSymbol, 0, len(universe))
	for _, symbol := range universe {
		wg.Add(1)
		go func(symbol string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			delta, ok := fetchBinanceOIDelta(symbol)
			if !ok {
				return
			}
			mu.Lock()
			rows = append(rows, OIRankedSymbol{Symbol: symbol, OIDelta: delta})
			mu.Unlock()
		}(symbol)
	}
	wg.Wait()
	return rows, nil
}

// fetchBinanceOIDelta returns the open-interest change over the last hour.
// ok=false means the pair has no usable history and is skipped.
func fetchBinanceOIDelta(symbol string) (float64, bool) {
	url := fmt.Sprintf("%s?symbol=%s&period=1h&limit=2", oiRankingHistURL, symbol)
	resp, err := oiRankingClient.Get(url)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	var points []struct {
		SumOpenInterest string `json:"sumOpenInterest"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&points); err != nil {
		return 0, false
	}
	if len(points) < 2 {
		return 0, false
	}
	latest, errL := parseOIFloat(points[len(points)-1].SumOpenInterest)
	earlier, errE := parseOIFloat(points[0].SumOpenInterest)
	if errL != nil || errE != nil {
		return 0, false
	}
	return latest - earlier, true
}

func parseOIFloat(raw string) (float64, error) {
	var v float64
	if _, err := fmt.Sscanf(raw, "%g", &v); err != nil {
		return 0, err
	}
	return v, nil
}

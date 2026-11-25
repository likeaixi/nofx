package store

import (
	"encoding/json"
	"testing"
)

func TestCopyConfigValidationAndRoundTrip(t *testing.T) {
	for _, raw := range []string{"http://www.binance.com/en/copy-trading/lead-details/123", "https://evil.test/en/copy-trading/lead-details/123", "https://www.binance.com.evil.test/en/copy-trading/lead-details/123", "https://www.binance.com/en/copy-trading/lead-details/nope", "https://user@www.binance.com/en/copy-trading/lead-details/123"} {
		if _, err := ParseCopySourceURL(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	id, err := ParseCopySourceURL("https://www.binance.com/zh-CN/copy-trading/lead-details/123?timeRange=30D")
	if err != nil || id != "123" {
		t.Fatalf("id %s err %v", id, err)
	}
	for _, lev := range []int{0, -1, 21} {
		c := StrategyConfig{StrategyType: AllinCrypto, CopyConfig: &CopyStrategyConfig{SourceURL: "https://www.binance.com/en/copy-trading/lead-details/123", Leverage: lev}}
		if c.ValidateCopyConfig() == nil {
			t.Fatalf("accepted leverage %d", lev)
		}
	}
	c := StrategyConfig{StrategyType: AllinCrypto, CopyConfig: &CopyStrategyConfig{SourceURL: "https://www.binance.com/en/copy-trading/lead-details/123", Leverage: 5}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got StrategyConfig
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.CopyConfig == nil || got.CopyConfig.Leverage != 5 || got.CopyConfig.SourceURL != c.CopyConfig.SourceURL {
		t.Fatalf("lost config: %s", b)
	}
	if (&StrategyConfig{StrategyType: AllinCrypto}).ValidateCopyConfig() != nil {
		t.Fatal("legacy rejected")
	}
}

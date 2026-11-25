package store

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

const DefaultCopyPortfolioID = "5075281354358777856"
const DefaultCopySourceURL = "https://www.binance.com/en/copy-trading/lead-details/" + DefaultCopyPortfolioID

type CopyStrategyConfig struct {
	SourceURL string `json:"source_url"`
	Leverage  int    `json:"leverage"`
}

var copySourcePath = regexp.MustCompile(`^/(?:[a-zA-Z]{2}(?:-[a-zA-Z]{2})?/)?copy-trading/lead-details/([0-9]{1,30})/?$`)

// Parse rather than fetch the URL. All public reads use the fixed Binance API origin.
func ParseCopySourceURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || (u.Host != "www.binance.com" && u.Host != "binance.com") || u.User != nil {
		return "", errors.New("请输入币安带单主页 HTTPS 网址 / Binance lead profile URL required")
	}
	match := copySourcePath.FindStringSubmatch(u.Path)
	if len(match) != 2 {
		return "", errors.New("网址必须包含 copy-trading/lead-details/组合ID / Invalid lead profile path")
	}
	return match[1], nil
}
func (c StrategyConfig) CopySettings() CopyStrategyConfig {
	if c.CopyConfig == nil {
		return CopyStrategyConfig{DefaultCopySourceURL, AllinCryptoLeverage}
	}
	return *c.CopyConfig
}
func (c StrategyConfig) ValidateCopyConfig() error {
	if c.StrategyType != AllinCrypto {
		if c.CopyConfig != nil {
			return errors.New("copy_config requires allincrypto strategy")
		}
		return nil
	}
	cfg := c.CopySettings()
	if _, err := ParseCopySourceURL(cfg.SourceURL); err != nil {
		return err
	}
	if cfg.Leverage < 1 || cfg.Leverage > 20 {
		return errors.New("跟单杠杆须为 1–20 倍 / Copy leverage must be 1–20")
	}
	return nil
}

package binance

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
)

func (b *FuturesTrader) NativeAccountIdentity(ctx context.Context) (string, error) {
	rows, err := b.client.NewGetBalanceService().Do(ctx)
	if err != nil {
		return "", binanceReadError("verify native account identity", err)
	}
	alias := ""
	for _, row := range rows {
		if row.AccountAlias == "" {
			return "", errors.New("Binance native account alias missing")
		}
		if alias != "" && alias != row.AccountAlias {
			return "", errors.New("Binance account aliases disagree")
		}
		alias = row.AccountAlias
	}
	if alias == "" {
		return "", errors.New("Binance native account identity unavailable")
	}
	return fmt.Sprintf("binance:native:%x", sha256.Sum256([]byte(alias))), nil
}

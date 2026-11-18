package okx

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
)

func (t *OKXTrader) NativeAccountIdentity(ctx context.Context) (string, error) {
	var rows []struct {
		UID string `json:"uid"`
	}
	if err := t.readExecution(ctx, okxAccountConfigPath, &rows); err != nil {
		return "", err
	}
	if len(rows) != 1 || rows[0].UID == "" {
		return "", errors.New("OKX native account identity unavailable")
	}
	return fmt.Sprintf("okx:native:%x", sha256.Sum256([]byte(rows[0].UID))), nil
}

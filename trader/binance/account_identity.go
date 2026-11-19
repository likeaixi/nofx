package binance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2/common"
)

// NativeAccountIdentity deliberately pins balance V2: V3 accountAlias values
// have been observed to collide across independent Binance accounts.
func (b *FuturesTrader) NativeAccountIdentity(ctx context.Context) (string, error) {
	identity, err := b.nativeAccountIdentityV2(ctx)
	if err != nil {
		return "", binanceReadError("verify native account identity", err)
	}
	return identity, nil
}

func (b *FuturesTrader) nativeAccountIdentityV2(ctx context.Context) (string, error) {
	keyType := b.client.KeyType
	if keyType == "" {
		keyType = common.KeyTypeHmac
	}
	sign, err := common.SignFunc(keyType)
	if err != nil {
		return "", err
	}
	query := url.Values{"timestamp": {strconv.FormatInt(time.Now().UnixMilli()-b.client.TimeOffset, 10)}}
	signature, err := sign(b.client.SecretKey, query.Encode())
	if err != nil {
		return "", err
	}
	query.Set("signature", *signature)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(b.client.BaseURL, "/")+"/fapi/v2/balance?"+query.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-MBX-APIKEY", b.client.APIKey)
	client := http.DefaultClient
	if b.client.HTTPClient != nil {
		client = b.client.HTTPClient
	}
	// Never mutate the shared SDK client or forward credentials on redirects.
	identityClient := *client
	identityClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := identityClient.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return "", errors.New("Binance identity response unreadable")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var apiErr common.APIError
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != 0 {
			return "", &apiErr
		}
		return "", errors.New("Binance identity request rejected")
	}
	// Decode only the identity field; balances and future response fields do not
	// participate in account identity or prevent its validation.
	var rows []struct {
		AccountAlias string `json:"accountAlias"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return "", err
	}
	alias := ""
	for _, row := range rows {
		if strings.TrimSpace(row.AccountAlias) == "" {
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
	return fmt.Sprintf("binance:native:v2:%x", sha256.Sum256([]byte(alias))), nil
}

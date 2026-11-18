package store

import "encoding/json"

const AllinCrypto = "allincrypto"

func (s *Strategy) IsAllinCrypto() bool {
	if s == nil {
		return false
	}
	var tag struct {
		Type string `json:"strategy_type"`
	}
	return json.Unmarshal([]byte(s.Config), &tag) == nil && tag.Type == AllinCrypto
}
func SupportsAllinCrypto(exchange string) bool { return exchange == "binance" || exchange == "okx" }

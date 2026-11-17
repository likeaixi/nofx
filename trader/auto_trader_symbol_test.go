package trader

import (
	"errors"
	"math"
	"strings"
	"testing"

	"nofx/kernel"
	"nofx/store"
)

// The embedded interface deliberately has no implementation: reaching any
// exchange mutation before the preflight completes must fail the test.
type symbolPreflightTrader struct {
	Trader
	price            float64
	err              error
	symbol           string
	positionsReached bool
}

func (t *symbolPreflightTrader) GetMarketPrice(symbol string) (float64, error) {
	t.symbol = symbol
	return t.price, t.err
}

func (t *symbolPreflightTrader) GetPositions() ([]map[string]interface{}, error) {
	t.positionsReached = true
	return nil, errors.New("positions sentinel")
}

func TestOpeningChecksTargetExchangeBeforeExecution(t *testing.T) {
	for _, action := range []string{"open_long", "open_short"} {
		for _, tc := range []struct {
			name    string
			symbol  string
			price   float64
			err     error
			allowed bool
		}{
			{"unlisted CL", "xyz:CL", 0, errors.New("<APIError> code=-1121, msg=Invalid symbol."), false},
			{"unlisted SAMSUNG", "xyz:SAMSUNG", 0, errors.New("Invalid symbol"), false},
			{"outage", "BTCUSDT", 0, errors.New("timeout"), false},
			{"zero", "BTCUSDT", 0, nil, false},
			{"negative", "BTCUSDT", -1, nil, false},
			{"nan", "BTCUSDT", math.NaN(), nil, false},
			{"infinite", "BTCUSDT", math.Inf(1), nil, false},
			{"listed", "BTCUSDT", 100, nil, true},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				exchange := &symbolPreflightTrader{price: tc.price, err: tc.err}
				at := &AutoTrader{trader: exchange, exchange: "binance"}
				err := at.executeDecisionWithRecord(&kernel.Decision{Action: action, Symbol: tc.symbol}, &store.DecisionAction{})
				if exchange.symbol != tc.symbol {
					t.Fatalf("target exchange was not queried for %s", tc.symbol)
				}
				if exchange.positionsReached != tc.allowed {
					t.Fatalf("execution continued=%v, want %v", exchange.positionsReached, tc.allowed)
				}
				if !tc.allowed && (err == nil || !strings.Contains(err.Error(), "binance") || !strings.Contains(err.Error(), tc.symbol) || !strings.Contains(err.Error(), "skipped")) {
					t.Fatalf("decision must explain skipped symbol and exchange: %v", err)
				}
			})
		}
	}
}

func TestUnsupportedOpeningIsRecordedAsSkipped(t *testing.T) {
	for _, unsupported := range []bool{true, false} {
		errText := "timeout"
		if unsupported {
			errText = "<APIError> code=-1121, msg=Invalid symbol."
		}
		at := &AutoTrader{exchange: "binance", trader: &symbolPreflightTrader{err: errors.New(errText)}}
		err := at.validateOpeningMarket("xyz:CL")
		action := store.DecisionAction{Action: "open_long", Symbol: "xyz:CL"}
		record := &store.DecisionRecord{Success: true}
		applyDecisionExecutionError(record, &action, err)
		record.Decisions = []store.DecisionAction{action}
		summarizeDecisionExecution(record)
		if action.Skipped != unsupported || record.Success != unsupported {
			t.Fatalf("incorrect outcome: %+v / %+v", action, record)
		}
		if unsupported {
			if action.Success || action.Error != "" || action.SkipReason == "" || record.ErrorMessage != "" {
				t.Fatalf("skip must not imply fill or failure: %+v", record)
			}
			if strings.Contains(strings.Join(record.ExecutionLog, " "), "APIError") || strings.Contains(strings.Join(record.ExecutionLog, " "), "failed") {
				t.Fatalf("raw failure leaked: %v", record.ExecutionLog)
			}
		} else if !strings.Contains(action.Error, "timeout") {
			t.Fatalf("query failure hidden: %+v", action)
		}
	}
}

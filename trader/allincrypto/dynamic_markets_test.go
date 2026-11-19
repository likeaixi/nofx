package allincrypto

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/shopspring/decimal"
	"strings"
	"testing"
	"time"

	"nofx/planning/core"
	"nofx/store"
	"nofx/trader/execution"
)

type dynamicVenue struct {
	*venueFixture
	name, base   string
	marketErr    error
	accountReads int
}

func (v *dynamicVenue) Venue() string { return v.name }
func (v *dynamicVenue) AccountSnapshot(ctx context.Context, symbol string) (execution.Account, error) {
	v.accountReads++
	return v.venueFixture.AccountSnapshot(ctx, symbol)
}
func (v *dynamicVenue) SnapshotKind(ctx context.Context, venue, symbol, kind string) (core.Snapshot, error) {
	if v.marketErr != nil {
		return core.Snapshot{}, v.marketErr
	}
	s, err := v.venueFixture.SnapshotKind(ctx, venue, symbol, kind)
	s.BaseAsset = v.base
	return s, err
}
func TestDynamicMarketsVerifyExactSourceUnits(t *testing.T) {
	for _, tc := range []struct{ name, venue, source, target, want string }{
		{"stock perpetual", "binance", "MU", "MU", "MUUSDT"},
		{"scaled Binance", "binance", "1000PEPE", "1000PEPE", "1000PEPEUSDT"},
		{"scaled OKX exact", "okx", "1000PEPE", "1000PEPE", "1000PEPE-USDT-SWAP"},
		{"scaled OKX mismatch", "okx", "1000PEPE", "PEPE", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, v, src, add := setup(t)
			dv := &dynamicVenue{venueFixture: v, name: tc.venue, base: tc.target}
			r.venue = dv
			r.markets = dv
			ctx := context.Background()
			if err := r.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			add("BUY", "10")
			e := &src.events[len(src.events)-1]
			e.BaseAsset = tc.source
			e.Symbol = tc.source + "USDT"
			if err := r.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(v.orders) != 0 || dv.accountReads != 0 {
					t.Fatal("unverified units reached account/order", v.orders, dv.accountReads)
				}
				return
			}
			if len(v.orders) != 2 || v.orders[0].Symbol != tc.want {
				t.Fatalf("expected protected entry %s: %+v", tc.want, v.orders)
			}
		})
	}
}
func TestMissingTargetPrecedesSourceProofAndRemainsInHeartbeat(t *testing.T) {
	r, v, src, add := setup(t)
	dv := &dynamicVenue{venueFixture: v, name: "okx", base: "MU", marketErr: errors.New("target instrument MU-USDT-SWAP not found")}
	r.venue = dv
	r.markets = dv
	ctx := context.Background()
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	add("BUY", "10")
	e := &src.events[len(src.events)-1]
	e.BaseAsset = "MU"
	e.Symbol = "MUUSDT"
	src.proofFails = true
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var row store.DecisionRecordDB
	r.db.Order("id desc").First(&row)
	for _, want := range []string{"MUUSDT", "LONG", "okx", "not found", "跳过"} {
		if !strings.Contains(row.ExecutionLog, want) {
			t.Fatalf("missing %s: %s", want, row.ExecutionLog)
		}
	}
	var actions []store.DecisionAction
	if err := json.Unmarshal([]byte(row.Decisions), &actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Symbol == "" || !actions[0].Skipped || dv.accountReads != 0 {
		t.Fatal(actions, dv.accountReads)
	}
	for range 6 {
		v.now = v.now.Add(15 * time.Minute)
		if err := r.RecordRuntimeStatus(nil, false); err != nil {
			t.Fatal(err)
		}
	}
	row = store.DecisionRecordDB{}
	r.db.Order("id desc").First(&row)
	if !strings.Contains(row.ExecutionLog, "MUUSDT") || !strings.Contains(row.ExecutionLog, "not found") || !strings.Contains(row.ExecutionLog, "跳过") {
		t.Fatal("lost skip outcome", row.ExecutionLog)
	}
	dv.marketErr = nil
	src.proofFails = false
	if err := r.Poll(ctx); err != nil || len(v.orders) != 0 {
		t.Fatal("historical skipped event replayed", err, v.orders)
	}
}
func TestMarketTransportErrorIsNotReportedAsUnsupported(t *testing.T) {
	r, v, src, add := setup(t)
	dv := &dynamicVenue{venueFixture: v, name: "binance", base: "MU", marketErr: errors.New("metadata HTTP 429")}
	r.venue = dv
	r.markets = dv
	_ = r.Poll(context.Background())
	add("BUY", "10")
	e := &src.events[len(src.events)-1]
	e.BaseAsset = "MU"
	e.Symbol = "MUUSDT"
	if err := r.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	var row store.DecisionRecordDB
	r.db.Order("id desc").First(&row)
	if !strings.Contains(row.ExecutionLog, "429") || strings.Contains(row.ExecutionLog, "unsupported") {
		t.Fatal(row.ExecutionLog)
	}
}

type failedBalanceSource struct {
	Source
	cause error
}

func (s failedBalanceSource) Balance(context.Context) (decimal.Decimal, error) {
	return decimal.Zero, s.cause
}

type failedCollateralVenue struct {
	*venueFixture
	cause error
}

func (v failedCollateralVenue) CopyCollateral(context.Context) (map[string]string, string, error) {
	return nil, "", v.cause
}
func TestPlanPreservesBalanceAndEquityFailureCauses(t *testing.T) {
	for _, kind := range []string{"source balance", "target equity"} {
		t.Run(kind, func(t *testing.T) {
			r, v, src, _ := setup(t)
			cause := errors.New("upstream HTTP 429 retry later")
			if kind == "source balance" {
				r.source = failedBalanceSource{r.source, cause}
			} else {
				r.venue = failedCollateralVenue{v, cause}
			}
			s, err := r.load()
			if err != nil {
				t.Fatal(err)
			}
			p := &pending{Event: src.events[len(src.events)-1], Side: "long"}
			err = r.plan(context.Background(), s, p)
			if !errors.Is(err, cause) {
				t.Fatalf("lost %s cause: %v", kind, err)
			}
		})
	}
}

func TestHeartbeatRestoresMetadataFromLegacySkippedSignal(t *testing.T) {
	r, v, src, _ := setup(t)
	s, err := r.load()
	if err != nil {
		t.Fatal(err)
	}
	e := src.events[0]
	e.Symbol = "MUUSDT"
	e.BaseAsset = "MU"
	e.PositionSide = "LONG"
	e.Side = "BUY"
	detail, _ := json.Marshal(&pending{Event: e, Side: "long"})
	recorded := v.now.Add(-24 * time.Hour).UTC()
	legacy := &store.DecisionRecord{TraderID: r.id, Timestamp: recorded, Success: false, DecisionJSON: string(detail), ExecutionLog: []string{"no verified underlying asset mapping"}, Decisions: []store.DecisionAction{{Action: "allincrypto", Symbol: "", Skipped: true, Success: false, SkipReason: "no verified underlying asset mapping"}}}
	if err := r.save(s, nil, legacy); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordRuntimeStatus(nil, false); err != nil {
		t.Fatal(err)
	}
	var row store.DecisionRecordDB
	r.db.Order("id desc").First(&row)
	for _, want := range []string{"MUUSDT", "LONG", "BUY", "okx", "no verified underlying asset mapping", "跳过", recorded.Format(time.RFC3339), "轮询检查通过"} {
		if !strings.Contains(row.ExecutionLog, want) {
			t.Fatalf("missing historical metadata %s: %s", want, row.ExecutionLog)
		}
	}
	if len(v.orders) != 0 {
		t.Fatal("heartbeat replayed historical entry")
	}
}

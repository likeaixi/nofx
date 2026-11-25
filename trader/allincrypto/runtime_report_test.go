package allincrypto

import (
	"errors"
	"nofx/store"
	"strings"
	"testing"
	"time"
)

func TestRuntimeReportCadenceSurvivesRestartWithoutOrders(t *testing.T) {
	r, v, src, _ := setup(t)
	if err := r.RecordRuntimeStatus(nil, false); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(r.db, r.id, r.account, src, v, v)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = r.now
	v.now = v.now.Add(14 * time.Minute)
	if err := restarted.RecordRuntimeStatus(nil, false); err != nil {
		t.Fatal(err)
	}
	var count int64
	r.db.Model(&store.DecisionRecordDB{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate status before 15 minutes: %d", count)
	}
	v.now = v.now.Add(time.Minute)
	if err := restarted.RecordRuntimeStatus(nil, false); err != nil {
		t.Fatal(err)
	}
	var rows []store.DecisionRecordDB
	r.db.Order("id").Find(&rows)
	if len(rows) != 2 || !rows[1].Success || rows[1].CycleNumber != 2 {
		t.Fatalf("missing periodic status: %+v", rows)
	}
	if !strings.Contains(rows[1].ExecutionLog, "15 分钟") {
		t.Fatal(rows[1].ExecutionLog)
	}
	if len(v.orders) != 0 {
		t.Fatal("status reporting submitted an order")
	}
}

func TestRuntimeReportShowsFailureRecoveryAndDraining(t *testing.T) {
	r, v, _, _ := setup(t)
	if err := r.RecordRuntimeStatus(errors.New("lead data HTTP 429"), false); err != nil {
		t.Fatal(err)
	}
	var row store.DecisionRecordDB
	r.db.Order("id desc").First(&row)
	if row.Success || !strings.Contains(row.ErrorMessage, "429") {
		t.Fatalf("failure hidden: %+v", row)
	}
	v.now = v.now.Add(15 * time.Minute)
	if err := r.RecordRuntimeStatus(nil, false); err != nil {
		t.Fatal(err)
	}
	row = store.DecisionRecordDB{}
	r.db.Order("id desc").First(&row)
	if !row.Success || row.ErrorMessage != "" {
		t.Fatalf("recovery not shown: %+v", row)
	}
	v.now = v.now.Add(15 * time.Minute)
	if err := r.RecordRuntimeStatus(nil, true); err != nil {
		t.Fatal(err)
	}
	row = store.DecisionRecordDB{}
	r.db.Order("id desc").First(&row)
	if !strings.Contains(row.ExecutionLog, "停止中") {
		t.Fatal(row.ExecutionLog)
	}
}

func TestRuntimeReportUsesGenericCopyTitle(t *testing.T) {
	r, _, _, _ := setup(t)
	r.StrategyName = "自定义跟单"
	r.SourceID = "123"
	if err := r.RecordRuntimeStatus(nil, false); err != nil {
		t.Fatal(err)
	}
	var row store.DecisionRecordDB
	if err := r.db.Order("id desc").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(row.ExecutionLog, "策略名称：自定义跟单") || !strings.Contains(row.ExecutionLog, "跟单来源：123") {
		t.Fatal("missing configured identity", row.ExecutionLog)
	}
	if !strings.Contains(row.ExecutionLog, "跟单 · 15 分钟运行日志（固定规则）") || strings.Contains(row.ExecutionLog, "AllinCrypto ·") {
		t.Fatal("hardcoded source title", row.ExecutionLog)
	}
}

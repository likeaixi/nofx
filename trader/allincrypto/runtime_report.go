package allincrypto

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nofx/store"
)

// RecordRuntimeStatus uses the existing decision feed, independently of trade
// events. The checkpoint and log share a transaction so restarts do not create
// duplicate heartbeats or suppress a report whose write failed.
// Call after a source poll, or after a drain cycle with stopping=true.
func (r *Runner) RecordRuntimeStatus(checkErr error, stopping bool) error {
	s, err := r.load()
	if err != nil {
		return err
	}
	now := r.now().UTC()
	if !s.LastRuntimeReportAt.IsZero() && now.Sub(s.LastRuntimeReportAt) < 15*time.Minute {
		return nil
	}
	if checkErr == nil && s.SourceFault != "" {
		checkErr = errors.New(s.SourceFault)
	}
	waiting := len(s.Waiting)
	if s.Pending != nil {
		waiting++
	}
	status := "检查正常，按固定规则继续跟踪带单信号"
	if waiting > 0 {
		status = "订单对账处理中，等待确认后继续跟单"
	}
	if stopping {
		status = "停止中，仅核对已有订单和止损，不接收新信号"
	}
	if checkErr != nil {
		status = "检查异常，暂不执行新跟单，等待后续检查恢复"
	}
	logs := []string{
		"AllinCrypto · 15 分钟运行决策日志（固定规则）",
		"本次检查：" + now.Format(time.RFC3339),
		"运行状态：" + status,
		fmt.Sprintf("已记录跟单持仓：%d；待处理订单流程：%d", len(s.Positions), waiting),
		"检查频率：带单信号每 1 分钟；订单和止损每 5 秒。交易事件仍实时单独记录。",
	}
	message := ""
	if checkErr != nil {
		message = checkErr.Error()
		logs = append(logs, "异常原因："+message)
	}
	detail, _ := json.Marshal(map[string]any{"type": "allincrypto_runtime_status", "checked_at": now, "stopping": stopping, "tracked_positions": len(s.Positions), "pending_workflows": waiting, "error": message})
	rec := &store.DecisionRecord{
		TraderID: r.id, Timestamp: now, Success: checkErr == nil, ErrorMessage: message,
		DecisionJSON: string(detail), ExecutionLog: logs,
		Decisions: []store.DecisionAction{{Action: "wait", Symbol: "", Reasoning: status, Success: checkErr == nil, Error: message, Timestamp: now}},
	}
	s.LastRuntimeReportAt = now
	return r.save(s, nil, rec)
}

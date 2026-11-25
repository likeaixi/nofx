package allincrypto

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	status := "轮询检查通过；跟单执行结果以最近信号记录为准"
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
		"跟单来源：" + r.sourceID(),
		"跟单周期：" + s.PeriodID,
		"运行状态：" + status,
		fmt.Sprintf("固定规则：%d 倍杠杆；实际成交均价反向 %d%% 止损", r.leverage(), store.AllinCryptoStopPercent),
		fmt.Sprintf("已记录跟单持仓：%d；待处理订单流程：%d", len(s.Positions), waiting),
		"检查频率：带单信号每 1 分钟；订单和止损每 5 秒。交易事件仍实时单独记录。",
	}
	if s.LedgerBaseline > 0 {
		logs = append(logs, "源仓位零基线（用户确认）："+time.UnixMilli(s.LedgerBaseline).UTC().Format(time.RFC3339))
		keys := make([]string, 0, len(s.Lead))
		for key := range s.Lead {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			logs = append(logs, fmt.Sprintf("源账本仓位：%s = %s（非机器人实际持仓）", key, s.Lead[key]))
		}
	}
	// Keep the latest actual signal outcome visible even after many heartbeats.
	var recent store.DecisionRecordDB
	query := r.db.Where("trader_id = ? AND decisions LIKE ?", r.id, `%"action":"allincrypto"%`)
	if s.PeriodDecisionFloor > 0 {
		query = query.Where("id > ?", s.PeriodDecisionFloor)
	}
	q := query.Order("id desc").Limit(1).Find(&recent)
	if q.Error != nil {
		return q.Error
	}
	if q.RowsAffected > 0 {
		var actions []store.DecisionAction
		var eventLogs []string
		if err := json.Unmarshal([]byte(recent.Decisions), &actions); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(recent.ExecutionLog), &eventLogs); err != nil {
			return err
		}
		outcome := "已记录"
		if len(actions) > 0 && actions[0].Skipped {
			outcome = "跳过或未完成"
		}
		logs = append(logs, "最近信号结果（"+recent.Timestamp.UTC().Format(time.RFC3339)+"）："+outcome)
		hasSignalMetadata := false
		for _, line := range eventLogs {
			if strings.HasPrefix(line, "源信号：") {
				hasSignalMetadata = true
				break
			}
		}
		if !hasSignalMetadata {
			// Older skipped records omitted action.Symbol and all signal context
			// from their visible logs, but retained the source event in the detail.
			var historical pending
			if json.Unmarshal([]byte(recent.DecisionJSON), &historical) == nil && historical.Event.Symbol != "" {
				target := historical.Symbol
				if target == "" {
					target = "未记录（未核实映射）"
				}
				logs = append(logs, fmt.Sprintf("源信号：%s %s %s；目标交易所（当前配置）：%s；历史目标合约：%s", historical.Event.Symbol, historical.Event.PositionSide, historical.Event.Side, r.venue.Venue(), target))
			}
		}
		logs = append(logs, eventLogs...)
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

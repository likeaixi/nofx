package spider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 快照结构体（同一个 symbol 只有一条）
type SymbolPositionSnapshot struct {
	Symbol     string  `json:"symbol"`
	Side       string  `json:"side"`        // "LONG"/"SHORT"
	StopLoss   float64 `json:"stop_loss"`   // 当前止损
	TakeProfit float64 `json:"take_profit"` // 当前止盈
	EntryTime  int64   `json:"entry_time"`  // 毫秒时间戳
	SavedAt    int64   `json:"saved_at"`    // 最近一次写入时间

	// HistoryTags：标签字符串列表，例如:
	//  "ARMED_WATCH ...", "EXTENSION_ACTIVE ..."
	HistoryTags []string `json:"history_tags"`

	// HistorySSP：每个元素是一个 map:
	//   {"T": <int时间戳>, "SSP": []float64{...}}
	// 约定：T 为时间戳（建议毫秒），表示这组 SSP 的时间
	HistorySSP []map[string]any `json:"history_ssp"`
}

// baseDir 例如 "data/spider_snapshots"
func symbolFilePath(baseDir, symbol string) string {
	symbol = strings.ToUpper(symbol)
	return filepath.Join(baseDir, symbol+".json")
}

// 内部通用写入函数：原子写
func saveSymbolSnapshot(baseDir string, record *SymbolPositionSnapshot) error {
	path := symbolFilePath(baseDir, record.Symbol)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// SaveSnapshotOnOpen 开仓时保存快照：
// - 如果已有 snapshot 文件，则在原记录上更新字段后保存
// - 如果没有文件，则创建一条新的记录
//
// tag：单条指令字符串，如：
//
//	"+ADD ARMED_WATCH watch_dir=cand_dir watch_level=key_level ttl=2"
//	"-DEL ARMED_WATCH ..."
//	"~DEDUP ARMED_WATCH ..."
//
// 只有以 "+ADD" 开头的 tag 才会被保存到 HistoryTags，且保存时会去掉前缀，只保留：
//
//	"ARMED_WATCH watch_dir=cand_dir watch_level=key_level ttl=2"
//
// historySSP：初始 HistorySSP，例如：
//
//	[]map[string]any{
//	    {"T": 1700000000000, "SSP": []float64{89100, 89600, ...}},
//	}
//
// 函数内部会保证 HistorySSP 最多只保留 2 条（长度>2 时只保留最新的 2 条）。
func SaveSnapshotOnOpen(
	baseDir string,
	symbol string,
	side string,
	stopLoss float64,
	takeProfit float64,
	entryTimeMillis int64,
	tag string,
	historySSP []map[string]any,
) error {
	symbol = strings.ToUpper(symbol)
	path := symbolFilePath(baseDir, symbol)

	// 1. 尝试读取已有文件（如果没有就用零值 record）
	var record SymbolPositionSnapshot

	data, err := os.ReadFile(path)
	if err == nil {
		// 有文件，尝试反序列化；失败也无所谓，下面会覆盖关键字段
		_ = json.Unmarshal(data, &record)
	} else if !errors.Is(err, os.ErrNotExist) {
		// 其它读文件错误需要返回
		return err
	}

	// 2. 处理 tag -> HistoryTags（只保留 +ADD，且去掉前缀）
	historyTags := make([]string, 0, 1)
	tag = strings.TrimSpace(tag)
	if tag != "" && strings.HasPrefix(tag, "+ADD") {
		parts := strings.Fields(tag)
		if len(parts) >= 2 {
			payload := strings.Join(parts[1:], " ")
			if payload != "" {
				historyTags = append(historyTags, payload)
			}
		}
	}

	// 3. 压缩 HistorySSP，最多只保留 2 条
	if len(historySSP) > 2 {
		historySSP = historySSP[len(historySSP)-2:]
	}

	// 4. 在 record 上更新/覆盖字段
	record.Symbol = symbol
	record.Side = strings.ToUpper(side)
	record.StopLoss = stopLoss
	record.TakeProfit = takeProfit
	record.EntryTime = entryTimeMillis
	record.HistoryTags = historyTags
	record.HistorySSP = historySSP
	record.SavedAt = time.Now().UnixMilli()

	// 5. 保存（如果原来没有文件，会自动创建；有则覆盖）
	return saveSymbolSnapshot(baseDir, &record)
}

// LoadSnapshotForSymbol 读取某个 symbol 当前仓位的快照
// 如果文件不存在，返回 (nil, nil)
func LoadSnapshotForSymbol(baseDir, symbol string) (*SymbolPositionSnapshot, error) {
	symbol = strings.ToUpper(symbol)
	path := symbolFilePath(baseDir, symbol)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var record SymbolPositionSnapshot
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// DeleteSnapshotOnClose 平仓后直接删除该 symbol 的快照文件
func DeleteSnapshotOnClose(baseDir, symbol string) error {
	symbol = strings.ToUpper(symbol)
	path := symbolFilePath(baseDir, symbol)

	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// -----------------
// 止损 / 止盈更新
// -----------------

func UpdateStopLossForSymbol(baseDir, symbol string, newStopLoss float64) error {
	symbol = strings.ToUpper(symbol)
	path := symbolFilePath(baseDir, symbol)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no snapshot file for symbol %s", symbol)
	}
	if err != nil {
		return err
	}

	var record SymbolPositionSnapshot
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}

	record.StopLoss = newStopLoss
	record.SavedAt = time.Now().UnixMilli()

	return saveSymbolSnapshot(baseDir, &record)
}

func UpdateTakeProfitForSymbol(baseDir, symbol string, newTakeProfit float64) error {
	symbol = strings.ToUpper(symbol)
	path := symbolFilePath(baseDir, symbol)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no snapshot file for symbol %s", symbol)
	}
	if err != nil {
		return err
	}

	var record SymbolPositionSnapshot
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}

	record.TakeProfit = newTakeProfit
	record.SavedAt = time.Now().UnixMilli()

	return saveSymbolSnapshot(baseDir, &record)
}

// -----------------
// HistorySSP 维护
// -----------------

// AppendHistorySSPForSymbol 追加一条 HistorySSP 记录：
// - 先读取原来的 HistorySSP
// - push 新的 {"T": ts, "SSP": newSSP} 进去
// - 总长度 > 2 时，只保留最新的 2 条
//
// ts: 时间戳（建议毫秒）
// newSSP: []float64，例如 []float64{89100, 89600, ...}
func AppendHistorySSPForSymbol(baseDir, symbol string, ts int64, newSSP []float64) error {
	symbol = strings.ToUpper(symbol)
	path := symbolFilePath(baseDir, symbol)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no snapshot file for symbol %s", symbol)
	}
	if err != nil {
		return err
	}

	var record SymbolPositionSnapshot
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}

	hist := record.HistorySSP
	if hist == nil {
		hist = make([]map[string]any, 0, 2)
	}

	entry := map[string]any{
		"T":   ts,     // int64 时间戳
		"SSP": newSSP, // []float64
	}
	hist = append(hist, entry)

	if len(hist) > 2 {
		hist = hist[len(hist)-2:]
	}

	record.HistorySSP = hist
	record.SavedAt = time.Now().UnixMilli()

	return saveSymbolSnapshot(baseDir, &record)
}

// -----------------
// HistoryTags 维护（原 HistoryView）
// -----------------

// UpdateHistoryTagsForSymbol 按指令更新 HistoryTags：
// 指令格式示例：
//
//	+ADD ARMED_WATCH watch_dir=cand_dir watch_level=key_level ttl=2
//	-DEL ARMED_WATCH watch_dir=cand_dir watch_level=key_level ttl=2
//	~DEDUP ARMED_WATCH watch_dir=cand_dir watch_level=key_level ttl=2
//
// 规则：
//   - HistoryTags 里每个元素是一个字符串："ARMED_WATCH ..." 或 "EXTENSION_ACTIVE ..."
//   - 类型只有两种：ARMED_WATCH / EXTENSION_ACTIVE
//   - +ADD: 先删掉同类型所有旧值，再追加新的 payload（去掉指令前缀）
//   - -DEL: 删掉该类型所有记录
//   - ~DEDUP: 同样删掉该类型（按你的描述“删除某一类型”）
func UpdateHistoryTagsForSymbol(baseDir, symbol string, command string) error {
	symbol = strings.ToUpper(symbol)
	path := symbolFilePath(baseDir, symbol)

	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("empty history_tags command")
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no snapshot file for symbol %s", symbol)
	}
	if err != nil {
		return err
	}

	var record SymbolPositionSnapshot
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}

	parts := strings.Fields(command)
	if len(parts) < 2 {
		return fmt.Errorf("invalid history_tags command: %s", command)
	}

	cmdToken := parts[0]  // "+ADD" / "-DEL" / "~DEDUP"
	typeToken := parts[1] // "ARMED_WATCH" / "EXTENSION_ACTIVE"
	typeToken = strings.ToUpper(typeToken)

	// 要保存的 payload：去掉前缀，只保留 "ARMED_WATCH ..." 或 "EXTENSION_ACTIVE ..."
	payload := strings.Join(parts[1:], " ")

	tags := record.HistoryTags
	if tags == nil {
		tags = make([]string, 0, 2)
	}

	// 抽取一条记录里的类型（第一个 token）
	extractType := func(s string) string {
		s = strings.TrimSpace(s)
		if s == "" {
			return ""
		}
		p := strings.Fields(s)
		if len(p) == 0 {
			return ""
		}
		return strings.ToUpper(p[0])
	}

	// 先删掉同类型的记录
	filtered := make([]string, 0, len(tags))
	for _, item := range tags {
		if extractType(item) == typeToken {
			continue
		}
		filtered = append(filtered, item)
	}

	switch {
	case strings.HasPrefix(cmdToken, "+ADD"):
		if payload != "" {
			filtered = append(filtered, payload)
		}
	case strings.HasPrefix(cmdToken, "-DEL"):
		// 已删同类型，不再追加
	case strings.HasPrefix(cmdToken, "~DEDUP"):
		// 语义等价于 -DEL：已删同类型，不再追加
	default:
		return fmt.Errorf("unknown history_tags command token: %s", cmdToken)
	}

	record.HistoryTags = filtered
	record.SavedAt = time.Now().UnixMilli()

	return saveSymbolSnapshot(baseDir, &record)
}

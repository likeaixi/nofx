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

// 可选的附加信息（方便排查 / 画图），你可以删掉不需要的字段
type SymbolPositionSnapshot struct {
	Symbol     string  `json:"symbol"`
	Side       string  `json:"side"` // "LONG"/"SHORT"
	StopLoss   float64 `json:"stop_loss"`
	TakeProfit float64 `json:"take_profit"`
	EntryTime  int64   `json:"entry_time"` // 毫秒时间戳
	SavedAt    int64   `json:"saved_at"`   // 写入时间
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

// SaveSnapshotOnOpen 开仓时保存蜘蛛丝快照（同 symbol 直接覆盖）
func SaveSnapshotOnOpen(baseDir, symbol, side string, stopLoss float64, takeProfit float64, entryTimeMillis int64) error {
	symbol = strings.ToUpper(symbol)

	record := &SymbolPositionSnapshot{
		Symbol:     symbol,
		Side:       strings.ToUpper(side),
		StopLoss:   stopLoss,
		TakeProfit: takeProfit,
		EntryTime:  entryTimeMillis,
		SavedAt:    time.Now().UnixMilli(),
	}

	return saveSymbolSnapshot(baseDir, record)
}

// LoadSnapshotForSymbol 读取某个 symbol 当前仓位的蜘蛛丝快照
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
		// 本来就没有，忽略
		return nil
	}
	return err
}

// UpdateStopLossForSymbol 更新某个 symbol 当前仓位的止损价
func UpdateStopLossForSymbol(baseDir, symbol string, newStopLoss float64) error {
	symbol = strings.ToUpper(symbol)
	path := symbolFilePath(baseDir, symbol)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// 没有快照文件：你可以改成创建新文件，这里先返回错误更安全
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

// UpdateTakeProfitForSymbol 更新某个 symbol 当前仓位的止损价
func UpdateTakeProfitForSymbol(baseDir, symbol string, newTakeProfit float64) error {
	symbol = strings.ToUpper(symbol)
	path := symbolFilePath(baseDir, symbol)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// 没有快照文件：你可以改成创建新文件，这里先返回错误更安全
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

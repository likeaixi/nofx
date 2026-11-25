package store

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
)

// PublicCopy omits account-specific provider credentials and private data
// connections, including URLs/headers which may embed credentials.
func (c *StrategyConfig) PublicCopy() *StrategyConfig {
	out := *c
	out.Indicators.NofxOSAPIKey = ""
	out.Indicators.ExternalDataSources = nil
	return &out
}

func (s *StrategyStore) UpdatePublication(userID, id string, public, visible bool) error {
	if visible && !public {
		return errors.New("publish the strategy before making its parameters visible")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var row Strategy
		if err := tx.Where("id = ? AND user_id = ? AND is_default = ?", id, userID, false).First(&row).Error; err != nil {
			return err
		}
		cfg, err := row.ParseConfig()
		if err != nil {
			return err
		}
		cfg.PublishConfig = &PublishStrategyConfig{IsPublic: public, ConfigVisible: visible}
		body, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		return tx.Model(&Strategy{}).Where("id = ? AND user_id = ?", id, userID).Updates(map[string]any{"is_public": public, "config_visible": visible, "config": string(body)}).Error
	})
}

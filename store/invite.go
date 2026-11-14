package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

var ErrInviteUnavailable = errors.New("invite code unavailable")

// InviteStore invite-code storage
type InviteStore struct{ db *gorm.DB }

// InviteCode single-use registration invite
type InviteCode struct {
	Code      string     `gorm:"primaryKey;size:16" json:"code"`
	CreatedBy string     `gorm:"not null" json:"created_by"`
	UsedBy    *string    `json:"used_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	Revoked   bool       `gorm:"not null;default:false" json:"revoked"`
}

func (InviteCode) TableName() string { return "invite_codes" }

func NewInviteStore(db *gorm.DB) *InviteStore { return &InviteStore{db: db} }

func (s *InviteStore) initTables() error {
	return s.db.AutoMigrate(&InviteCode{})
}

func (s *InviteStore) Create(code, createdBy string) error {
	return s.db.Create(&InviteCode{Code: code, CreatedBy: createdBy}).Error
}

func (s *InviteStore) List() ([]InviteCode, error) {
	var out []InviteCode
	err := s.db.Order("created_at DESC").Find(&out).Error
	return out, err
}

// GetValid returns the invite only when unused and not revoked.
func (s *InviteStore) GetValid(code string) (*InviteCode, error) {
	var inv InviteCode
	err := s.db.Where("code = ? AND used_by IS NULL AND revoked = ?", code, false).First(&inv).Error
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// MarkUsed atomically claims an invite for one user; exactly one concurrent caller wins.
func (s *InviteStore) MarkUsed(code, userID string) error {
	now := time.Now().UTC()
	res := s.db.Model(&InviteCode{}).
		Where("code = ? AND used_by IS NULL AND revoked = ?", code, false).
		Updates(map[string]interface{}{"used_by": userID, "used_at": &now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrInviteUnavailable
	}
	return nil
}

// Revoke voids an unused invite.
func (s *InviteStore) Revoke(code string) error {
	res := s.db.Model(&InviteCode{}).
		Where("code = ? AND used_by IS NULL AND revoked = ?", code, false).
		Update("revoked", true)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrInviteUnavailable
	}
	return nil
}

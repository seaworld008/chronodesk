package services

import (
	"context"
	"fmt"
	"time"

	"gongdan-system/internal/models"
	"gorm.io/gorm"
)

// TrustedDeviceService 管理可信设备
// 提供查询和撤销等操作，供用户自助管理已记住的设备。
type TrustedDeviceService struct {
	db *gorm.DB
}

// NewTrustedDeviceService 创建可信设备服务
func NewTrustedDeviceService(db *gorm.DB) *TrustedDeviceService {
	return &TrustedDeviceService{db: db}
}

// ListTrustedDevices 返回用户的可信设备列表（按最近使用排序）。
func (s *TrustedDeviceService) ListTrustedDevices(ctx context.Context, userID uint) ([]*models.OTPTrustedDevice, error) {
	var devices []*models.OTPTrustedDevice
	err := s.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("revoked ASC, expires_at DESC").
		Find(&devices).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list trusted devices: %w", err)
	}
	return devices, nil
}

// RevokeTrustedDevice 撤销指定的可信设备访问权。
func (s *TrustedDeviceService) RevokeTrustedDevice(ctx context.Context, userID, deviceID uint) error {
	updates := map[string]interface{}{
		"revoked":    true,
		"expires_at": time.Now(),
	}
	result := s.db.WithContext(ctx).
		Model(&models.OTPTrustedDevice{}).
		Where("id = ? AND user_id = ?", deviceID, userID).
		Updates(updates)
	if err := result.Error; err != nil {
		return fmt.Errorf("failed to revoke trusted device: %w", err)
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// RevokeAllTrustedDevices 撤销用户的所有可信设备。
func (s *TrustedDeviceService) RevokeAllTrustedDevices(ctx context.Context, userID uint) error {
	updates := map[string]interface{}{
		"revoked":    true,
		"expires_at": time.Now(),
	}
	if err := s.db.WithContext(ctx).
		Model(&models.OTPTrustedDevice{}).
		Where("user_id = ? AND revoked = ?", userID, false).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to revoke all trusted devices: %w", err)
	}
	return nil
}

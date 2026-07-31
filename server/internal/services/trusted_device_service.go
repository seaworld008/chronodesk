package services

import (
	"context"
	"fmt"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
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

// ListTrustedDevicePage returns one bounded page for the authenticated owner.
// The user predicate is applied to both COUNT and page queries.
func (s *TrustedDeviceService) ListTrustedDevicePage(
	ctx context.Context,
	userID uint,
	request DirectoryPageRequest,
) (*DirectoryPage[*models.OTPTrustedDevice], error) {
	if userID == 0 {
		return nil, ErrDirectoryListQuery
	}
	sortFields := map[string]struct{}{
		"created_at":   {},
		"updated_at":   {},
		"last_used_at": {},
		"expires_at":   {},
		"revoked":      {},
		"device_name":  {},
	}
	if err := validateDirectoryPageRequest(request, sortFields); err != nil {
		return nil, err
	}
	query := s.db.WithContext(ctx).
		Model(&models.OTPTrustedDevice{}).
		Where("user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count trusted devices: %w", err)
	}
	devices := make([]*models.OTPTrustedDevice, 0, request.PageSize)
	err := query.
		Order(trustedDeviceDirectoryOrder(request)).
		Offset(directoryPageOffset(request)).
		Limit(request.PageSize).
		Find(&devices).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list trusted devices: %w", err)
	}
	return &DirectoryPage[*models.OTPTrustedDevice]{
		Items:      devices,
		Total:      total,
		Page:       request.Page,
		PageSize:   request.PageSize,
		TotalPages: directoryTotalPages(total, request.PageSize),
	}, nil
}

func trustedDeviceDirectoryOrder(request DirectoryPageRequest) string {
	if request.SortBy == "revoked" && request.SortOrder == "asc" {
		return "revoked ASC, expires_at DESC, id DESC"
	}
	direction := "ASC"
	if request.SortOrder == "desc" {
		direction = "DESC"
	}
	column := map[string]string{
		"created_at":   "created_at",
		"updated_at":   "updated_at",
		"last_used_at": "last_used_at",
		"expires_at":   "expires_at",
		"revoked":      "revoked",
		"device_name":  "device_name",
	}[request.SortBy]
	return column + " " + direction + ", id " + direction
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

package services

import (
	"context"
	"fmt"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gongdan-system/internal/models"
	"gorm.io/gorm"
)

// UserService 用户服务
type UserService struct {
	db *gorm.DB
}

// NewUserService 创建用户服务
func NewUserService(db *gorm.DB) *UserService {
	return &UserService{
		db: db,
	}
}

// GetUserProfile 获取用户详细信息
func (s *UserService) GetUserProfile(ctx context.Context, userID uint) (*models.User, error) {
	var user models.User
	err := s.db.WithContext(ctx).
		Preload("Manager").
		First(&user, userID).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get user profile: %w", err)
	}

	return &user, nil
}

// UpdateUserProfile 更新用户个人资料
func (s *UserService) UpdateUserProfile(ctx context.Context, userID uint, req *models.UserUpdateRequest) error {
	user := &models.User{}
	if err := s.db.WithContext(ctx).First(user, userID).Error; err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	// 构建更新数据
	updates := make(map[string]interface{})

	if req.Email != nil {
		// 检查邮箱是否被其他用户使用
		var count int64
		s.db.Model(&models.User{}).Where("email = ? AND id != ?", *req.Email, userID).Count(&count)
		if count > 0 {
			return fmt.Errorf("email already exists")
		}
		updates["email"] = *req.Email
		updates["email_verified"] = false // 邮箱变更需要重新验证
	}

	if req.Phone != nil {
		updates["phone"] = *req.Phone
		updates["phone_verified"] = false // 手机变更需要重新验证
	}

	if req.FirstName != nil {
		updates["first_name"] = *req.FirstName
	}

	if req.LastName != nil {
		updates["last_name"] = *req.LastName
	}

	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}

	if req.Avatar != nil {
		updates["avatar"] = *req.Avatar
	}

	if req.Timezone != nil {
		updates["timezone"] = *req.Timezone
	}

	if req.Language != nil {
		updates["language"] = *req.Language
	}

	if req.Department != nil {
		updates["department"] = *req.Department
	}

	if req.JobTitle != nil {
		updates["job_title"] = *req.JobTitle
	}

	if req.ManagerID != nil {
		updates["manager_id"] = *req.ManagerID
	}

	// 执行更新
	if len(updates) > 0 {
		if err := s.db.WithContext(ctx).Model(user).Updates(updates).Error; err != nil {
			return fmt.Errorf("failed to update user profile: %w", err)
		}
	}

	return nil
}

// ChangePassword 修改用户密码
func (s *UserService) ChangePassword(ctx context.Context, userID uint, currentPassword, newPassword string) error {
	user := &models.User{}
	if err := s.db.WithContext(ctx).First(user, userID).Error; err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	// 验证当前密码
	if !s.verifyPassword(user.PasswordHash, currentPassword) {
		return fmt.Errorf("invalid current password")
	}

	// 加密新密码
	hashedPassword, err := s.hashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("failed to hash new password: %w", err)
	}

	// 更新密码
	if err := s.db.WithContext(ctx).Model(user).Update("password_hash", hashedPassword).Error; err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	// 记录密码修改历史（可选）
	s.recordPasswordChange(ctx, userID)

	return nil
}

// GetLoginHistory 获取用户登录历史
func (s *UserService) GetLoginHistory(ctx context.Context, userID uint, req *models.LoginHistoryRequest) ([]*models.LoginHistoryResponse, int64, error) {
	query := s.db.WithContext(ctx).Model(&models.LoginHistory{})

	// 过滤条件
	query = query.Where("user_id = ?", userID)

	if req.Status != nil {
		query = query.Where("login_status = ?", *req.Status)
	}

	if req.StartDate != nil {
		query = query.Where("login_time >= ?", *req.StartDate)
	}

	if req.EndDate != nil {
		query = query.Where("login_time <= ?", *req.EndDate)
	}

	if req.IPAddress != "" {
		query = query.Where("ip_address = ?", req.IPAddress)
	}

	if req.DeviceType != "" {
		query = query.Where("device_type = ?", req.DeviceType)
	}

	if req.LoginMethod != "" {
		query = query.Where("login_method = ?", req.LoginMethod)
	}

	if req.SessionID != "" {
		query = query.Where("session_id = ?", req.SessionID)
	}

	if req.IsActive != nil {
		query = query.Where("is_active = ?", *req.IsActive)
	}

	// 统计总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count login history: %w", err)
	}

	// 排序
	orderBy := "login_time"
	if req.OrderBy != "" {
		orderBy = req.OrderBy
	}

	order := "DESC"
	if req.Order != "" {
		order = strings.ToUpper(req.Order)
	}

	query = query.Order(fmt.Sprintf("%s %s", orderBy, order))

	// 分页
	page := 1
	pageSize := 20
	if req.Page > 0 {
		page = req.Page
	}
	if req.PageSize > 0 {
		pageSize = req.PageSize
	}

	offset := (page - 1) * pageSize
	query = query.Offset(offset).Limit(pageSize)

	// 查询数据
	var histories []models.LoginHistory
	if err := query.Find(&histories).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to get login history: %w", err)
	}

	// 转换为响应格式
	responses := make([]*models.LoginHistoryResponse, len(histories))
	for i, history := range histories {
		responses[i] = history.ToResponse()
	}

	return responses, total, nil
}

// RecordLogin 记录用户登录
func (s *UserService) RecordLogin(ctx context.Context, userID uint, req *RecordLoginRequest) error {
	user := &models.User{}
	if err := s.db.WithContext(ctx).First(user, userID).Error; err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	// 解析设备信息
	deviceInfo := parseUserAgent(req.UserAgent)

	loginHistory := &models.LoginHistory{
		UserID:      userID,
		Username:    user.Username,
		Email:       user.Email,
		IPAddress:   req.IPAddress,
		UserAgent:   req.UserAgent,
		LoginTime:   time.Now(),
		SessionID:   req.SessionID,
		LoginStatus: models.LoginStatusSuccess,
		LoginMethod: req.LoginMethod,

		// 设备信息
		DeviceType:      deviceInfo.DeviceType,
		OperatingSystem: deviceInfo.OperatingSystem,
		Browser:         deviceInfo.Browser,

		// 地理位置信息（如果有）
		Country:  req.Country,
		Region:   req.Region,
		City:     req.City,
		Timezone: req.Timezone,

		IsActive: true,
	}

	if err := s.db.WithContext(ctx).Create(loginHistory).Error; err != nil {
		return fmt.Errorf("failed to record login: %w", err)
	}

	// 更新用户最后登录信息
	now := time.Now()
	updates := map[string]interface{}{
		"last_login_at": &now,
		"last_login_ip": req.IPAddress,
	}

	if err := s.db.WithContext(ctx).Model(user).Updates(updates).Error; err != nil {
		// 记录日志但不返回错误
		fmt.Printf("Warning: failed to update user last login info: %v\n", err)
	}

	return nil
}

// RecordLogout 记录用户退出登录
func (s *UserService) RecordLogout(ctx context.Context, userID uint, sessionID string) error {
	now := time.Now()

	// 查找活跃的登录会话
	var loginHistory models.LoginHistory
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND session_id = ? AND is_active = ?", userID, sessionID, true).
		First(&loginHistory).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 会话未找到，可能已经过期或清理
			return nil
		}
		return fmt.Errorf("failed to find login session: %w", err)
	}

	// 计算会话持续时间
	sessionDuration := int64(now.Sub(loginHistory.LoginTime).Seconds())

	// 更新登录记录
	updates := map[string]interface{}{
		"logout_time":      &now,
		"session_duration": sessionDuration,
		"is_active":        false,
	}

	if err := s.db.WithContext(ctx).Model(&loginHistory).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update logout time: %w", err)
	}

	return nil
}

// GetUserStats 获取用户统计信息
func (s *UserService) GetUserStats(ctx context.Context, userID uint) (*models.UserProfileStats, error) {
	user := &models.User{}
	if err := s.db.WithContext(ctx).First(user, userID).Error; err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}

	stats := &models.UserProfileStats{
		TwoFactorEnabled: user.TwoFactorEnabled,
		EmailVerified:    user.EmailVerified,
		PhoneVerified:    user.PhoneVerified,
	}

	// 计算账户年龄
	stats.AccountAge = int(time.Since(user.CreatedAt).Hours() / 24)

	// 工单统计
	stats.TicketsCreated = user.TicketsCreated
	stats.TicketsAssigned = user.TicketsAssigned
	stats.TicketsResolved = user.TicketsResolved

	// 获取关闭的工单数量
	var closedCount int64
	s.db.WithContext(ctx).Model(&models.Ticket{}).
		Where("created_by_id = ? AND status = ?", userID, "closed").
		Count(&closedCount)
	stats.TicketsClosed = int(closedCount)

	// 评论统计
	var commentTotal int64
	s.db.WithContext(ctx).Model(&models.TicketComment{}).
		Where("user_id = ?", userID).
		Count(&commentTotal)
	stats.CommentsTotal = int(commentTotal)

	// 本周评论数量
	weekStart := time.Now().AddDate(0, 0, -7)
	var commentWeek int64
	s.db.WithContext(ctx).Model(&models.TicketComment{}).
		Where("user_id = ? AND created_at >= ?", userID, weekStart).
		Count(&commentWeek)
	stats.CommentsThisWeek = int(commentWeek)

	// 登录统计
	var loginTotal int64
	s.db.WithContext(ctx).Model(&models.LoginHistory{}).
		Where("user_id = ? AND login_status = ?", userID, models.LoginStatusSuccess).
		Count(&loginTotal)
	stats.LoginTotal = int(loginTotal)

	// 本周登录次数
	var loginWeek int64
	s.db.WithContext(ctx).Model(&models.LoginHistory{}).
		Where("user_id = ? AND login_status = ? AND login_time >= ?",
			userID, models.LoginStatusSuccess, weekStart).
		Count(&loginWeek)
	stats.LoginThisWeek = int(loginWeek)

	// 最后登录时间
	stats.LastLoginTime = user.LastLoginAt

	// 不同设备数量统计
	var deviceCount int64
	s.db.WithContext(ctx).Model(&models.LoginHistory{}).
		Where("user_id = ?", userID).
		Distinct("device_type", "operating_system").
		Count(&deviceCount)
	stats.LoginDevices = int(deviceCount)

	// 计算安全评分
	stats.SecurityScore = s.calculateSecurityScore(user, stats)

	return stats, nil
}

// UploadAvatar 上传用户头像
func (s *UserService) UploadAvatar(ctx context.Context, userID uint, file multipart.File, header *multipart.FileHeader) (string, error) {
	// 验证文件类型
	allowedTypes := []string{".jpg", ".jpeg", ".png", ".gif"}
	ext := strings.ToLower(filepath.Ext(header.Filename))

	isAllowed := false
	for _, allowedType := range allowedTypes {
		if ext == allowedType {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		return "", fmt.Errorf("unsupported file type: %s", ext)
	}

	// 验证文件大小（2MB限制）
	if header.Size > 2*1024*1024 {
		return "", fmt.Errorf("file too large: maximum 2MB allowed")
	}

	// 生成唯一文件名
	filename := fmt.Sprintf("avatar_%d_%d%s", userID, time.Now().Unix(), ext)

	// TODO: 实现文件保存逻辑（本地存储或云存储）
	// 这里暂时返回模拟的URL
	avatarURL := fmt.Sprintf("/uploads/avatars/%s", filename)

	// 更新用户头像URL
	if err := s.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ?", userID).
		Update("avatar", avatarURL).Error; err != nil {
		return "", fmt.Errorf("failed to update avatar: %w", err)
	}

	return avatarURL, nil
}

// recordPasswordChange 记录密码修改（内部方法）
func (s *UserService) recordPasswordChange(ctx context.Context, userID uint) {
	// 可以在这里记录密码修改历史
	// 例如：更新密码修改时间、记录安全日志等
	now := time.Now()
	s.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ?", userID).
		Update("password_reset_at", &now)
}

// calculateSecurityScore 计算安全评分
func (s *UserService) calculateSecurityScore(user *models.User, stats *models.UserProfileStats) int {
	score := 0

	// 基础分数
	score += 20

	// 邮箱验证
	if user.EmailVerified {
		score += 20
	}

	// 手机验证
	if user.PhoneVerified {
		score += 15
	}

	// 两步验证
	if user.TwoFactorEnabled {
		score += 25
	}

	// 密码强度（简单判断）
	if user.PasswordHash != "" {
		score += 10
	}

	// 账户活跃度
	if stats.LoginTotal > 5 {
		score += 10
	}

	return score
}

// parseUserAgent 解析User-Agent字符串
func parseUserAgent(userAgent string) *DeviceInfo {
	deviceInfo := &DeviceInfo{
		DeviceType:      "desktop",
		OperatingSystem: "Unknown",
		Browser:         "Unknown",
	}

	userAgent = strings.ToLower(userAgent)

	// 检测设备类型
	if strings.Contains(userAgent, "mobile") || strings.Contains(userAgent, "android") || strings.Contains(userAgent, "iphone") {
		deviceInfo.DeviceType = "mobile"
	} else if strings.Contains(userAgent, "tablet") || strings.Contains(userAgent, "ipad") {
		deviceInfo.DeviceType = "tablet"
	}

	// 检测操作系统
	if strings.Contains(userAgent, "windows") {
		deviceInfo.OperatingSystem = "Windows"
	} else if strings.Contains(userAgent, "macintosh") || strings.Contains(userAgent, "mac os") {
		deviceInfo.OperatingSystem = "macOS"
	} else if strings.Contains(userAgent, "linux") {
		deviceInfo.OperatingSystem = "Linux"
	} else if strings.Contains(userAgent, "android") {
		deviceInfo.OperatingSystem = "Android"
	} else if strings.Contains(userAgent, "ios") || strings.Contains(userAgent, "iphone") || strings.Contains(userAgent, "ipad") {
		deviceInfo.OperatingSystem = "iOS"
	}

	// 检测浏览器
	if strings.Contains(userAgent, "chrome") {
		deviceInfo.Browser = "Chrome"
	} else if strings.Contains(userAgent, "firefox") {
		deviceInfo.Browser = "Firefox"
	} else if strings.Contains(userAgent, "safari") && !strings.Contains(userAgent, "chrome") {
		deviceInfo.Browser = "Safari"
	} else if strings.Contains(userAgent, "edge") {
		deviceInfo.Browser = "Edge"
	} else if strings.Contains(userAgent, "opera") {
		deviceInfo.Browser = "Opera"
	}

	return deviceInfo
}

// 请求和响应结构体

// RecordLoginRequest 记录登录请求
type RecordLoginRequest struct {
	IPAddress   string `json:"ip_address"`
	UserAgent   string `json:"user_agent"`
	SessionID   string `json:"session_id"`
	LoginMethod string `json:"login_method"`

	// 地理位置信息（可选）
	Country  string `json:"country,omitempty"`
	Region   string `json:"region,omitempty"`
	City     string `json:"city,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// DeviceInfo 设备信息
type DeviceInfo struct {
	DeviceType      string `json:"device_type"`
	OperatingSystem string `json:"operating_system"`
	Browser         string `json:"browser"`
}

// 密码处理方法

// hashPassword 加密密码
func (s *UserService) hashPassword(password string) (string, error) {
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedBytes), nil
}

// verifyPassword 验证密码
func (s *UserService) verifyPassword(hashedPassword, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

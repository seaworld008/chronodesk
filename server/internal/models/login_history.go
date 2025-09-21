package models

import (
	"time"
)

// LoginHistory 登录历史记录
type LoginHistory struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	// 用户信息
	UserID   uint   `json:"user_id" gorm:"index;not null"`
	User     User   `json:"user,omitempty" gorm:"foreignKey:UserID"`
	Username string `json:"username" gorm:"size:50;not null"`
	Email    string `json:"email" gorm:"size:100;not null"`

	// 登录信息
	IPAddress      string     `json:"ip_address" gorm:"size:45;not null"`
	UserAgent      string     `json:"user_agent" gorm:"type:text"`
	LoginTime      time.Time  `json:"login_time" gorm:"not null;index"`
	LogoutTime     *time.Time `json:"logout_time,omitempty" gorm:"index"`
	LastActivityAt *time.Time `json:"last_activity_at,omitempty" gorm:"index"`
	SessionID      string     `json:"session_id" gorm:"size:255;index"`

	// 登录状态
	LoginStatus   LoginStatus `json:"login_status" gorm:"size:20;not null;default:'success'"`
	LoginMethod   string      `json:"login_method" gorm:"size:20;default:'password'"` // password, otp, sso
	FailureReason string      `json:"failure_reason,omitempty" gorm:"size:255"`

	// 地理位置信息（可选）
	Country  string `json:"country,omitempty" gorm:"size:50"`
	Region   string `json:"region,omitempty" gorm:"size:50"`
	City     string `json:"city,omitempty" gorm:"size:50"`
	Timezone string `json:"timezone,omitempty" gorm:"size:50"`

	// 设备信息
	DeviceType      string `json:"device_type,omitempty" gorm:"size:50"`       // desktop, mobile, tablet
	OperatingSystem string `json:"operating_system,omitempty" gorm:"size:100"` // Windows, macOS, Linux, etc.
	Browser         string `json:"browser,omitempty" gorm:"size:100"`          // Chrome, Firefox, Safari, etc.

	// 会话信息
	SessionDuration *int64 `json:"session_duration,omitempty"`    // 会话持续时间（秒）
	IsActive        bool   `json:"is_active" gorm:"default:true"` // 会话是否仍然活跃
}

// LoginStatus 登录状态枚举
type LoginStatus string

const (
	LoginStatusSuccess   LoginStatus = "success"   // 登录成功
	LoginStatusFailed    LoginStatus = "failed"    // 登录失败
	LoginStatusBlocked   LoginStatus = "blocked"   // 被阻止登录
	LoginStatusSuspended LoginStatus = "suspended" // 账户被暂停
	LoginStatusExpired   LoginStatus = "expired"   // 会话过期
)

// TableName 指定表名
func (LoginHistory) TableName() string {
	return "login_histories"
}

// GetSessionDurationString 获取会话持续时间的字符串表示
func (lh *LoginHistory) GetSessionDurationString() string {
	if lh.SessionDuration == nil {
		return "未知"
	}

	duration := time.Duration(*lh.SessionDuration) * time.Second
	if duration < time.Minute {
		return "小于1分钟"
	} else if duration < time.Hour {
		return duration.Round(time.Minute).String()
	} else {
		return duration.Round(time.Hour).String()
	}
}

// IsCurrentSession 检查是否为当前会话
func (lh *LoginHistory) IsCurrentSession() bool {
	return lh.IsActive && lh.LogoutTime == nil
}

// GetDeviceInfo 获取设备信息摘要
func (lh *LoginHistory) GetDeviceInfo() string {
	if lh.DeviceType != "" && lh.OperatingSystem != "" {
		return lh.DeviceType + " - " + lh.OperatingSystem
	} else if lh.Browser != "" {
		return lh.Browser
	} else if lh.UserAgent != "" {
		// 简化的 User-Agent 解析
		return lh.UserAgent[:min(50, len(lh.UserAgent))]
	}
	return "未知设备"
}

// GetLocationInfo 获取位置信息摘要
func (lh *LoginHistory) GetLocationInfo() string {
	parts := []string{}
	if lh.Country != "" {
		parts = append(parts, lh.Country)
	}
	if lh.Region != "" {
		parts = append(parts, lh.Region)
	}
	if lh.City != "" {
		parts = append(parts, lh.City)
	}

	if len(parts) > 0 {
		return joinStrings(parts, ", ")
	}
	return "未知位置"
}

// LoginHistoryRequest 登录历史查询请求
type LoginHistoryRequest struct {
	UserID      *uint        `json:"user_id" query:"user_id"`
	Status      *LoginStatus `json:"status" query:"status"`
	StartDate   *time.Time   `json:"start_date" query:"start_date"`
	EndDate     *time.Time   `json:"end_date" query:"end_date"`
	IPAddress   string       `json:"ip_address" query:"ip_address"`
	DeviceType  string       `json:"device_type" query:"device_type"`
	LoginMethod string       `json:"login_method" query:"login_method"`
	SessionID   string       `json:"session_id" query:"session_id"`
	IsActive    *bool        `json:"is_active" query:"is_active"`
	Page        int          `json:"page" query:"page" validate:"min=1"`
	PageSize    int          `json:"page_size" query:"page_size" validate:"min=1,max=100"`
	OrderBy     string       `json:"order_by" query:"order_by"` // login_time, created_at
	Order       string       `json:"order" query:"order"`       // asc, desc
}

// LoginHistoryResponse 登录历史响应
type LoginHistoryResponse struct {
	ID         uint       `json:"id"`
	UserID     uint       `json:"user_id"`
	Username   string     `json:"username"`
	Email      string     `json:"email"`
	IPAddress  string     `json:"ip_address"`
	LoginTime  time.Time  `json:"login_time"`
	LogoutTime *time.Time `json:"logout_time"`
	SessionID  string     `json:"session_id"`

	LoginStatus   LoginStatus `json:"login_status"`
	LoginMethod   string      `json:"login_method"`
	FailureReason string      `json:"failure_reason,omitempty"`

	Location         string `json:"location"`         // 格式化的位置信息
	DeviceInfo       string `json:"device_info"`      // 格式化的设备信息
	SessionDuration  string `json:"session_duration"` // 格式化的会话时长
	IsCurrentSession bool   `json:"is_current_session"`
	IsActive         bool   `json:"is_active"`
}

// ToResponse 转换为响应格式
func (lh *LoginHistory) ToResponse() *LoginHistoryResponse {
	return &LoginHistoryResponse{
		ID:         lh.ID,
		UserID:     lh.UserID,
		Username:   lh.Username,
		Email:      lh.Email,
		IPAddress:  lh.IPAddress,
		LoginTime:  lh.LoginTime,
		LogoutTime: lh.LogoutTime,
		SessionID:  lh.SessionID,

		LoginStatus:   lh.LoginStatus,
		LoginMethod:   lh.LoginMethod,
		FailureReason: lh.FailureReason,

		Location:         lh.GetLocationInfo(),
		DeviceInfo:       lh.GetDeviceInfo(),
		SessionDuration:  lh.GetSessionDurationString(),
		IsCurrentSession: lh.IsCurrentSession(),
		IsActive:         lh.IsActive,
	}
}

// UserProfileStats 用户个人统计
type UserProfileStats struct {
	// 工单统计
	TicketsCreated  int `json:"tickets_created"`
	TicketsAssigned int `json:"tickets_assigned"`
	TicketsResolved int `json:"tickets_resolved"`
	TicketsClosed   int `json:"tickets_closed"`

	// 评论统计
	CommentsTotal    int `json:"comments_total"`
	CommentsThisWeek int `json:"comments_this_week"`

	// 登录统计
	LoginTotal    int        `json:"login_total"`
	LoginThisWeek int        `json:"login_this_week"`
	LastLoginTime *time.Time `json:"last_login_time"`
	LoginDevices  int        `json:"login_devices"` // 不同设备数量

	// 账户信息
	AccountAge       int  `json:"account_age"`    // 账户年龄（天）
	SecurityScore    int  `json:"security_score"` // 安全评分 0-100
	TwoFactorEnabled bool `json:"two_factor_enabled"`
	EmailVerified    bool `json:"email_verified"`
	PhoneVerified    bool `json:"phone_verified"`
}

// 辅助函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func joinStrings(parts []string, separator string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += separator + parts[i]
	}
	return result
}

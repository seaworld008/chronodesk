package models

import (
	"time"

	"gorm.io/gorm"
)

// PlatformRole expresses platform-wide governance duties only. It never
// grants access to project-owned data or project business capabilities.
type PlatformRole string

const (
	PlatformRolePlatformAdmin     PlatformRole = "platform_admin"
	PlatformRoleSecurityAuditor   PlatformRole = "security_auditor"
	PlatformRoleEmergencyOperator PlatformRole = "emergency_operator"
	PlatformRoleMember            PlatformRole = "member"
)

// IsValid reports whether the role belongs to the closed platform-role set.
func (r PlatformRole) IsValid() bool {
	switch r {
	case PlatformRolePlatformAdmin,
		PlatformRoleSecurityAuditor,
		PlatformRoleEmergencyOperator,
		PlatformRoleMember:
		return true
	default:
		return false
	}
}

// UserStatus 用户状态枚举
type UserStatus string

const (
	UserStatusActive    UserStatus = "active"    // 激活
	UserStatusInactive  UserStatus = "inactive"  // 未激活
	UserStatusSuspended UserStatus = "suspended" // 暂停
	UserStatusDeleted   UserStatus = "deleted"   // 删除
)

// User 用户模型
type User struct {
	ID        uint           `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time      `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	// 基本信息
	Username     string `json:"username" gorm:"uniqueIndex;size:50;not null" validate:"required,min=3,max=50"`
	Email        string `json:"email" gorm:"uniqueIndex;size:100;not null" validate:"required,email"`
	Phone        string `json:"phone" gorm:"size:20" validate:"omitempty,e164"`
	PasswordHash string `json:"-" gorm:"size:255;not null"`

	// 个人信息
	FirstName   string `json:"first_name" gorm:"size:50" validate:"omitempty,max=50"`
	LastName    string `json:"last_name" gorm:"size:50" validate:"omitempty,max=50"`
	DisplayName string `json:"display_name" gorm:"size:100" validate:"omitempty,max=100"`
	Avatar      string `json:"avatar" gorm:"size:255"`
	Timezone    string `json:"timezone" gorm:"size:50;default:'Asia/Shanghai'"`
	Language    string `json:"language" gorm:"size:10;default:'zh-CN'"`

	// 平台职责和账号状态。项目职责只来自 ProjectMembership。
	PlatformRole PlatformRole `json:"platform_role" gorm:"column:platform_role;size:30;not null;default:'member';index;check:chk_users_platform_role,platform_role IN ('platform_admin','security_auditor','emergency_operator','member')" validate:"required,oneof=platform_admin security_auditor emergency_operator member"`
	Status       UserStatus   `json:"status" gorm:"size:20;not null;default:'inactive';index" validate:"required,oneof=active inactive suspended deleted"`
	Permissions  string       `json:"permissions" gorm:"type:text"` // JSON格式存储权限列表

	// 认证相关
	EmailVerified    bool       `json:"email_verified" gorm:"default:false"`
	EmailVerifiedAt  *time.Time `json:"email_verified_at,omitempty"`
	PhoneVerified    bool       `json:"phone_verified" gorm:"default:false"`
	PhoneVerifiedAt  *time.Time `json:"phone_verified_at,omitempty"`
	TwoFactorEnabled bool       `json:"two_factor_enabled" gorm:"default:false"`
	TwoFactorSecret  string     `json:"-" gorm:"size:255"` // TOTP密钥
	BackupCodes      string     `json:"-" gorm:"type:text"`

	// 登录相关
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
	LastLoginIP        string     `json:"last_login_ip" gorm:"size:45"`
	LoginAttempts      int        `json:"login_attempts" gorm:"default:0"`
	LockedUntil        *time.Time `json:"locked_until,omitempty"`
	PasswordResetToken string     `json:"-" gorm:"size:255"`
	PasswordResetAt    *time.Time `json:"password_reset_at,omitempty"`
	// WelcomeEmailDeliveredAt is the durable idempotency receipt for the
	// authentication email Outbox. It is intentionally not exposed through
	// human or machine user contracts.
	WelcomeEmailDeliveredAt *time.Time `json:"-"`

	// 业务相关
	Department string `json:"department" gorm:"size:100"`
	JobTitle   string `json:"job_title" gorm:"size:100"`
	ManagerID  *uint  `json:"manager_id,omitempty" gorm:"index"`
	Manager    *User  `json:"manager,omitempty" gorm:"foreignKey:ManagerID"`

	// 统计信息
	TicketsCreated  int `json:"tickets_created" gorm:"default:0"`
	TicketsAssigned int `json:"tickets_assigned" gorm:"default:0"`
	TicketsResolved int `json:"tickets_resolved" gorm:"default:0"`

	// 关联关系
	CreatedTickets  []Ticket        `json:"created_tickets,omitempty" gorm:"foreignKey:CreatedByID"`
	AssignedTickets []Ticket        `json:"assigned_tickets,omitempty" gorm:"foreignKey:AssignedToID"`
	Comments        []TicketComment `json:"comments,omitempty" gorm:"foreignKey:UserID"`
}

// TableName 指定表名
func (User) TableName() string {
	return "users"
}

// GetFullName 获取完整姓名
func (u *User) GetFullName() string {
	if u.FirstName != "" && u.LastName != "" {
		return u.FirstName + " " + u.LastName
	}
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

// IsActive 检查用户是否激活
func (u *User) IsActive() bool {
	return u.Status == UserStatusActive
}

// IsLocked 检查用户是否被锁定
func (u *User) IsLocked() bool {
	return u.LockedUntil != nil && u.LockedUntil.After(time.Now())
}

// CanLogin 检查用户是否可以登录
func (u *User) CanLogin() bool {
	return u.IsActive() && !u.IsLocked()
}

// UserCreateRequest 用户创建请求
type UserCreateRequest struct {
	Username     string       `json:"username" binding:"required,min=3,max=50"`
	Email        string       `json:"email" binding:"required,email"`
	Phone        string       `json:"phone" binding:"omitempty,e164"`
	Password     string       `json:"password" binding:"required,min=8,max=128"`
	FirstName    string       `json:"first_name" binding:"omitempty,max=50"`
	LastName     string       `json:"last_name" binding:"omitempty,max=50"`
	DisplayName  string       `json:"display_name" binding:"omitempty,max=100"`
	PlatformRole PlatformRole `json:"platform_role" binding:"required,oneof=platform_admin security_auditor emergency_operator member"`
	Department   string       `json:"department" binding:"omitempty,max=100"`
	JobTitle     string       `json:"job_title" binding:"omitempty,max=100"`
	ManagerID    *uint        `json:"manager_id" binding:"omitempty,gt=0"`
}

// UserUpdateRequest 用户更新请求
type UserUpdateRequest struct {
	Email         *string       `json:"email" binding:"omitempty,email"`
	Phone         *string       `json:"phone"`
	FirstName     *string       `json:"first_name" binding:"omitempty,max=50"`
	LastName      *string       `json:"last_name" binding:"omitempty,max=50"`
	DisplayName   *string       `json:"display_name" binding:"omitempty,max=100"`
	Avatar        *string       `json:"avatar"`
	Timezone      *string       `json:"timezone" binding:"omitempty,max=50"`
	Language      *string       `json:"language" binding:"omitempty,max=10"`
	PlatformRole  *PlatformRole `json:"platform_role" binding:"omitempty,oneof=platform_admin security_auditor emergency_operator member"`
	Status        *UserStatus   `json:"status" binding:"omitempty,oneof=active inactive suspended deleted"`
	EmailVerified *bool         `json:"email_verified"`
	Department    *string       `json:"department" binding:"omitempty,max=100"`
	JobTitle      *string       `json:"job_title" binding:"omitempty,max=100"`
	ManagerID     *uint         `json:"manager_id" binding:"omitempty,gt=0"`
}

// UserResponse 用户响应
type UserResponse struct {
	ID               uint         `json:"id"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	Username         string       `json:"username"`
	Email            string       `json:"email"`
	Phone            string       `json:"phone"`
	FirstName        string       `json:"first_name"`
	LastName         string       `json:"last_name"`
	DisplayName      string       `json:"display_name"`
	Avatar           string       `json:"avatar"`
	Timezone         string       `json:"timezone"`
	Language         string       `json:"language"`
	PlatformRole     PlatformRole `json:"platform_role"`
	Status           UserStatus   `json:"status"`
	EmailVerified    bool         `json:"email_verified"`
	PhoneVerified    bool         `json:"phone_verified"`
	TwoFactorEnabled bool         `json:"two_factor_enabled"`
	LastLoginAt      *time.Time   `json:"last_login_at"`
	Department       string       `json:"department"`
	JobTitle         string       `json:"job_title"`
	ManagerID        *uint        `json:"manager_id"`
	TicketsCreated   int          `json:"tickets_created"`
	TicketsAssigned  int          `json:"tickets_assigned"`
	TicketsResolved  int          `json:"tickets_resolved"`
}

// UserSummary is the only human identity shape embedded in tickets,
// comments, histories, notifications, and categories. Authentication,
// contact, verification, login, and account-control fields belong only to
// dedicated user-management/profile endpoints.
type UserSummary struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
}

func (u *User) ToSummary() *UserSummary {
	if u == nil {
		return nil
	}
	return &UserSummary{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.GetFullName(),
		Avatar:      u.Avatar,
	}
}

// ToResponse 转换为响应格式
func (u *User) ToResponse() *UserResponse {
	return &UserResponse{
		ID:               u.ID,
		CreatedAt:        u.CreatedAt,
		UpdatedAt:        u.UpdatedAt,
		Username:         u.Username,
		Email:            u.Email,
		Phone:            u.Phone,
		FirstName:        u.FirstName,
		LastName:         u.LastName,
		DisplayName:      u.DisplayName,
		Avatar:           u.Avatar,
		Timezone:         u.Timezone,
		Language:         u.Language,
		PlatformRole:     u.PlatformRole,
		Status:           u.Status,
		EmailVerified:    u.EmailVerified,
		PhoneVerified:    u.PhoneVerified,
		TwoFactorEnabled: u.TwoFactorEnabled,
		LastLoginAt:      u.LastLoginAt,
		Department:       u.Department,
		JobTitle:         u.JobTitle,
		ManagerID:        u.ManagerID,
		TicketsCreated:   u.TicketsCreated,
		TicketsAssigned:  u.TicketsAssigned,
		TicketsResolved:  u.TicketsResolved,
	}
}

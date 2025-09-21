package models

import (
	"time"
)

// UserRole 用户角色枚举
type UserRole string

const (
	RoleAdmin      UserRole = "admin"      // 管理员
	RoleAgent      UserRole = "agent"      // 客服代理
	RoleCustomer   UserRole = "customer"   // 客户
	RoleSupervisor UserRole = "supervisor" // 主管
)

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
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

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

	// 角色和权限
	Role        UserRole   `json:"role" gorm:"size:20;not null;default:'customer'" validate:"required,oneof=admin agent customer supervisor"`
	Status      UserStatus `json:"status" gorm:"size:20;not null;default:'inactive'" validate:"required,oneof=active inactive suspended deleted"`
	Permissions string     `json:"permissions" gorm:"type:text"` // JSON格式存储权限列表

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
	OTPCodes        []OTPCode       `json:"-" gorm:"foreignKey:UserID"`
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

// HasRole 检查用户是否具有指定角色
func (u *User) HasRole(role UserRole) bool {
	return u.Role == role
}

// IsAdmin 检查是否为管理员
func (u *User) IsAdmin() bool {
	return u.Role == RoleAdmin
}

// IsAgent 检查是否为客服代理
func (u *User) IsAgent() bool {
	return u.Role == RoleAgent || u.Role == RoleSupervisor
}

// IsCustomer 检查是否为客户
func (u *User) IsCustomer() bool {
	return u.Role == RoleCustomer
}

// IsSupervisor 检查是否为主管
func (u *User) IsSupervisor() bool {
	return u.Role == RoleSupervisor
}

// UserCreateRequest 用户创建请求
type UserCreateRequest struct {
	Username    string   `json:"username" validate:"required,min=3,max=50"`
	Email       string   `json:"email" validate:"required,email"`
	Phone       string   `json:"phone" validate:"omitempty,e164"`
	Password    string   `json:"password" validate:"required,min=8,max=128"`
	FirstName   string   `json:"first_name" validate:"omitempty,max=50"`
	LastName    string   `json:"last_name" validate:"omitempty,max=50"`
	DisplayName string   `json:"display_name" validate:"omitempty,max=100"`
	Role        UserRole `json:"role" validate:"required,oneof=admin agent customer supervisor"`
	Department  string   `json:"department" validate:"omitempty,max=100"`
	JobTitle    string   `json:"job_title" validate:"omitempty,max=100"`
	ManagerID   *uint    `json:"manager_id"`
}

// UserUpdateRequest 用户更新请求
type UserUpdateRequest struct {
	Email       *string     `json:"email" validate:"omitempty,email"`
	Phone       *string     `json:"phone" validate:"omitempty,e164"`
	FirstName   *string     `json:"first_name" validate:"omitempty,max=50"`
	LastName    *string     `json:"last_name" validate:"omitempty,max=50"`
	DisplayName *string     `json:"display_name" validate:"omitempty,max=100"`
	Avatar      *string     `json:"avatar"`
	Timezone    *string     `json:"timezone" validate:"omitempty,max=50"`
	Language    *string     `json:"language" validate:"omitempty,max=10"`
	Role        *UserRole   `json:"role" validate:"omitempty,oneof=admin agent customer supervisor"`
	Status      *UserStatus `json:"status" validate:"omitempty,oneof=active inactive suspended deleted"`
	Department  *string     `json:"department" validate:"omitempty,max=100"`
	JobTitle    *string     `json:"job_title" validate:"omitempty,max=100"`
	ManagerID   *uint       `json:"manager_id"`
}

// UserResponse 用户响应
type UserResponse struct {
	ID               uint       `json:"id"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Username         string     `json:"username"`
	Email            string     `json:"email"`
	Phone            string     `json:"phone"`
	FirstName        string     `json:"first_name"`
	LastName         string     `json:"last_name"`
	DisplayName      string     `json:"display_name"`
	Avatar           string     `json:"avatar"`
	Timezone         string     `json:"timezone"`
	Language         string     `json:"language"`
	Role             UserRole   `json:"role"`
	Status           UserStatus `json:"status"`
	EmailVerified    bool       `json:"email_verified"`
	PhoneVerified    bool       `json:"phone_verified"`
	TwoFactorEnabled bool       `json:"two_factor_enabled"`
	LastLoginAt      *time.Time `json:"last_login_at"`
	Department       string     `json:"department"`
	JobTitle         string     `json:"job_title"`
	ManagerID        *uint      `json:"manager_id"`
	TicketsCreated   int        `json:"tickets_created"`
	TicketsAssigned  int        `json:"tickets_assigned"`
	TicketsResolved  int        `json:"tickets_resolved"`
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
		Role:             u.Role,
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

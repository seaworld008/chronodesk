package models

import (
	"time"
)

// UserProfile 用户详细资料
type UserProfile struct {
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	// 关联用户
	UserID uint  `json:"user_id" gorm:"uniqueIndex;not null"`
	User   *User `json:"user,omitempty" gorm:"foreignKey:UserID"`

	// 个人信息
	Avatar      string `json:"avatar" gorm:"size:500"`
	Bio         string `json:"bio" gorm:"type:text"`
	Phone       string `json:"phone" gorm:"size:20"`
	MobilePhone string `json:"mobile_phone" gorm:"size:20"`
	Address     string `json:"address" gorm:"size:500"`
	City        string `json:"city" gorm:"size:100"`
	State       string `json:"state" gorm:"size:100"`
	Country     string `json:"country" gorm:"size:100"`
	PostalCode  string `json:"postal_code" gorm:"size:20"`

	// 工作信息
	EmployeeID   string `json:"employee_id" gorm:"size:50"`
	Manager      string `json:"manager" gorm:"size:100"`
	OfficePhone  string `json:"office_phone" gorm:"size:20"`
	OfficeNumber string `json:"office_number" gorm:"size:50"`

	// 偏好设置
	Language        string `json:"language" gorm:"size:10;default:'zh-CN'"`
	Timezone        string `json:"timezone" gorm:"size:50;default:'Asia/Shanghai'"`
	DateFormat      string `json:"date_format" gorm:"size:20;default:'YYYY-MM-DD'"`
	TimeFormat      string `json:"time_format" gorm:"size:20;default:'24h'"`
	FirstDayOfWeek  int    `json:"first_day_of_week" gorm:"default:1"` // 1=Monday, 0=Sunday
	ReceiveNewsletter bool `json:"receive_newsletter" gorm:"default:true"`

	// 社交媒体
	LinkedIn  string `json:"linkedin" gorm:"size:200"`
	Twitter   string `json:"twitter" gorm:"size:200"`
	Facebook  string `json:"facebook" gorm:"size:200"`
	Instagram string `json:"instagram" gorm:"size:200"`
	Website   string `json:"website" gorm:"size:200"`

	// 其他信息
	Skills   string `json:"skills" gorm:"type:text"`   // JSON array
	Metadata string `json:"metadata" gorm:"type:text"` // JSON object
}

// TableName 指定表名
func (UserProfile) TableName() string {
	return "user_profiles"
}
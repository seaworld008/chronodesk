package models

import (
	"time"
)

// AttachmentType 附件类型
type AttachmentType string

const (
	AttachmentTypeImage    AttachmentType = "image"
	AttachmentTypeDocument AttachmentType = "document"
	AttachmentTypeVideo    AttachmentType = "video"
	AttachmentTypeAudio    AttachmentType = "audio"
	AttachmentTypeArchive  AttachmentType = "archive"
	AttachmentTypeOther    AttachmentType = "other"
)

// TicketAttachment 工单附件
type TicketAttachment struct {
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	// 关联信息
	TicketID uint    `json:"ticket_id" gorm:"not null;index"`
	Ticket   *Ticket `json:"ticket,omitempty" gorm:"foreignKey:TicketID"`
	
	CommentID *uint           `json:"comment_id,omitempty" gorm:"index"`
	Comment   *TicketComment  `json:"comment,omitempty" gorm:"foreignKey:CommentID"`
	
	UploadedBy uint  `json:"uploaded_by" gorm:"not null;index"`
	Uploader   *User `json:"uploader,omitempty" gorm:"foreignKey:UploadedBy"`

	// 文件信息
	FileName    string         `json:"file_name" gorm:"size:255;not null"`
	OriginalName string        `json:"original_name" gorm:"size:255;not null"`
	FileSize    int64          `json:"file_size" gorm:"not null"`
	MimeType    string         `json:"mime_type" gorm:"size:100"`
	FileType    AttachmentType `json:"file_type" gorm:"size:20;default:'other'"`
	Extension   string         `json:"extension" gorm:"size:10"`
	
	// 存储信息
	StoragePath string `json:"storage_path" gorm:"size:500;not null"`
	StorageType string `json:"storage_type" gorm:"size:20;default:'local'"` // local, s3, gcs, azure
	StorageUrl  string `json:"storage_url" gorm:"size:500"`
	ThumbnailUrl string `json:"thumbnail_url" gorm:"size:500"`
	
	// 访问控制
	IsPublic     bool   `json:"is_public" gorm:"default:false"`
	AccessToken  string `json:"access_token" gorm:"size:255;index"`
	DownloadCount int    `json:"download_count" gorm:"default:0"`
	
	// 安全信息
	Hash        string `json:"hash" gorm:"size:255"` // SHA256 hash
	VirusScan   string `json:"virus_scan" gorm:"size:20;default:'pending'"` // pending, clean, infected, error
	ScanDetails string `json:"scan_details" gorm:"type:text"`
	ScannedAt   *time.Time `json:"scanned_at,omitempty"`
	
	// 元数据
	Description string `json:"description" gorm:"type:text"`
	Metadata    string `json:"metadata" gorm:"type:text"` // JSON object
	
	// 图片特有信息
	Width  int `json:"width" gorm:"default:0"`
	Height int `json:"height" gorm:"default:0"`
	
	// 文档特有信息
	PageCount int `json:"page_count" gorm:"default:0"`
}

// TableName 指定表名
func (TicketAttachment) TableName() string {
	return "ticket_attachments"
}
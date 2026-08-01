package models

import (
	"time"

	"gorm.io/gorm"
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

type VirusScanStatus string

const (
	VirusScanPending  VirusScanStatus = "pending"
	VirusScanClean    VirusScanStatus = "clean"
	VirusScanInfected VirusScanStatus = "infected"
	VirusScanError    VirusScanStatus = "error"
)

// TicketAttachment 工单附件
type TicketAttachment struct {
	ID        uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"index"`

	// 关联信息
	OrganizationID uint    `json:"organization_id" gorm:"not null;index"`
	ProjectID      uint    `json:"project_id" gorm:"not null;index"`
	TicketID       uint    `json:"ticket_id" gorm:"not null;index"`
	Ticket         *Ticket `json:"ticket,omitempty" gorm:"foreignKey:TicketID"`

	CommentID *uint          `json:"comment_id,omitempty" gorm:"index"`
	Comment   *TicketComment `json:"comment,omitempty" gorm:"foreignKey:CommentID"`

	UploadedBy         *uint             `json:"uploaded_by,omitempty" gorm:"index"`
	Uploader           *User             `json:"uploader,omitempty" gorm:"foreignKey:UploadedBy"`
	ActorType          ActorType         `json:"actor_type" gorm:"size:32;not null;default:'human';index"`
	ActorID            string            `json:"actor_id" gorm:"size:128;index"`
	ServicePrincipalID *string           `json:"service_principal_id,omitempty" gorm:"size:36;index"`
	ServicePrincipal   *ServicePrincipal `json:"service_principal,omitempty" gorm:"foreignKey:ServicePrincipalID"`

	// 文件信息
	FileName     string         `json:"file_name" gorm:"size:255;not null"`
	OriginalName string         `json:"original_name" gorm:"size:255;not null"`
	FileSize     int64          `json:"file_size" gorm:"not null"`
	MimeType     string         `json:"mime_type" gorm:"size:100"`
	FileType     AttachmentType `json:"file_type" gorm:"size:20;default:'other'"`
	Extension    string         `json:"extension" gorm:"size:20"`

	// 存储信息
	StoragePath      string `json:"storage_path" gorm:"size:500;not null"`
	StorageType      string `json:"storage_type" gorm:"size:20;default:'local'"` // local, s3, gcs, azure
	StorageStoreID   string `json:"-" gorm:"size:63;not null;default:'';index"`
	StorageVersionID string `json:"-" gorm:"size:1024;not null;default:''"`
	StorageUrl       string `json:"storage_url" gorm:"size:500"`
	ThumbnailUrl     string `json:"thumbnail_url" gorm:"size:500"`

	// 访问控制
	IsPublic      bool   `json:"is_public" gorm:"default:false"`
	AccessToken   string `json:"-" gorm:"size:255;index"`
	DownloadCount int    `json:"download_count" gorm:"default:0"`

	// 安全信息
	Hash        string          `json:"hash" gorm:"size:64;index"` // SHA256 hash; legacy rows may be empty
	VirusScan   VirusScanStatus `json:"virus_scan" gorm:"size:20;not null;default:'pending';index"`
	ScanDetails string          `json:"scan_details" gorm:"type:text"`
	ScannedAt   *time.Time      `json:"scanned_at,omitempty"`

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

func (attachment *TicketAttachment) BeforeCreate(tx *gorm.DB) error {
	return inheritTicketProjectScope(
		tx,
		attachment.TicketID,
		&attachment.OrganizationID,
		&attachment.ProjectID,
	)
}

func (a *TicketAttachment) Actor() ActorRef {
	return ActorRef{Type: a.ActorType, ID: a.ActorID}
}

// TicketAttachmentResponse intentionally omits storage paths, provider URLs,
// access tokens and raw metadata from API responses.
type TicketAttachmentResponse struct {
	ID                 uint            `json:"id"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	TicketID           uint            `json:"ticket_id"`
	OrganizationID     uint            `json:"organization_id"`
	ProjectID          uint            `json:"project_id"`
	CommentID          *uint           `json:"comment_id,omitempty"`
	UploadedBy         *uint           `json:"uploaded_by,omitempty"`
	ActorType          ActorType       `json:"actor_type"`
	ActorID            string          `json:"actor_id"`
	ServicePrincipalID *string         `json:"service_principal_id,omitempty"`
	FileName           string          `json:"file_name"`
	OriginalName       string          `json:"original_name"`
	FileSize           int64           `json:"file_size"`
	MimeType           string          `json:"mime_type"`
	FileType           AttachmentType  `json:"file_type"`
	Extension          string          `json:"extension"`
	IsPublic           bool            `json:"is_public"`
	DownloadCount      int             `json:"download_count"`
	Hash               string          `json:"hash"`
	VirusScan          VirusScanStatus `json:"virus_scan"`
	ScanDetails        string          `json:"scan_details,omitempty"`
	ScannedAt          *time.Time      `json:"scanned_at,omitempty"`
	Description        string          `json:"description,omitempty"`
	Width              int             `json:"width,omitempty"`
	Height             int             `json:"height,omitempty"`
	PageCount          int             `json:"page_count,omitempty"`
}

func (a *TicketAttachment) ToResponse() *TicketAttachmentResponse {
	if a == nil {
		return nil
	}
	return &TicketAttachmentResponse{
		ID:                 a.ID,
		CreatedAt:          a.CreatedAt,
		UpdatedAt:          a.UpdatedAt,
		TicketID:           a.TicketID,
		OrganizationID:     a.OrganizationID,
		ProjectID:          a.ProjectID,
		CommentID:          a.CommentID,
		UploadedBy:         a.UploadedBy,
		ActorType:          a.ActorType,
		ActorID:            a.ActorID,
		ServicePrincipalID: a.ServicePrincipalID,
		FileName:           a.FileName,
		OriginalName:       a.OriginalName,
		FileSize:           a.FileSize,
		MimeType:           a.MimeType,
		FileType:           a.FileType,
		Extension:          a.Extension,
		IsPublic:           a.IsPublic,
		DownloadCount:      a.DownloadCount,
		Hash:               a.Hash,
		VirusScan:          a.VirusScan,
		ScanDetails:        a.ScanDetails,
		ScannedAt:          a.ScannedAt,
		Description:        a.Description,
		Width:              a.Width,
		Height:             a.Height,
		PageCount:          a.PageCount,
	}
}

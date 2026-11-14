package model

import "time"

const (
	ContentKindText = "text"
	ContentKindFile = "file"

	UploadKindRealtime = "realtime"
	UploadKindHistory  = "history"
)

type Account struct {
	ID            uint `gorm:"primaryKey"`
	Name          string
	RetentionDays int
	FileMaxBytes  int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type AdminUser struct {
	ID           uint   `gorm:"primaryKey"`
	AccountID    uint   `gorm:"index"`
	Username     string `gorm:"uniqueIndex"`
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Device struct {
	ID                     uint   `gorm:"primaryKey"`
	AccountID              uint   `gorm:"index;uniqueIndex:idx_account_uuid"`
	DeviceUUID             string `gorm:"index:idx_account_uuid,unique"`
	Nickname               string
	AppName                string
	AppVersion             string
	DeviceTokenHash        string `gorm:"index"`
	SendRealtimeEnabled    bool
	ReceiveRealtimeEnabled bool
	Deleted                bool `gorm:"not null;default:false;index"`
	LastSeenAt             *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type ClipboardItem struct {
	ID              uint   `gorm:"primaryKey;index:idx_recent_text,priority:4,sort:desc"`
	AccountID       uint   `gorm:"index;index:idx_recent_text,priority:1"`
	SourceDeviceID  uint   `gorm:"index"`
	ContentKind     string `gorm:"size:32;index:idx_recent_text,priority:2"`
	UploadKind      string `gorm:"size:32"`
	MimeType        string `gorm:"size:255"`
	TextContent     string `gorm:"type:text"`
	BlobPath        string `gorm:"size:1024"`
	ByteSize        int64
	CharCount       int
	ClientCreatedAt time.Time  `gorm:"index;index:idx_recent_text,priority:3,sort:desc"`
	ReceivedAt      time.Time  `gorm:"index"`
	ExpiresAt       *time.Time `gorm:"index"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (Device) TableName() string {
	return "devices"
}

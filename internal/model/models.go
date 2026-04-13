package model

import "time"

const (
	ContentKindText  = "text"
	ContentKindImage = "image"

	UploadKindRealtime = "realtime"
	UploadKindHistory  = "history"
)

type Account struct {
	ID                    uint `gorm:"primaryKey"`
	Name                  string
	RealtimeFanoutEnabled bool
	RetentionDays         int
	ImageMaxBytes         int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type AdminUser struct {
	ID           uint   `gorm:"primaryKey"`
	AccountID    uint   `gorm:"index"`
	Username     string `gorm:"uniqueIndex"`
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type EnrollmentToken struct {
	ID         uint   `gorm:"primaryKey"`
	AccountID  uint   `gorm:"index"`
	CodeHash   string `gorm:"uniqueIndex"`
	CodePrefix string
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	MaxUses    int
	UsedCount  int
	LastUsedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Device struct {
	ID                     uint   `gorm:"primaryKey"`
	AccountID              uint   `gorm:"index;uniqueIndex:idx_account_uuid"`
	DeviceUUID             string `gorm:"index:idx_account_uuid,unique"`
	Nickname               string
	OSName                 string
	OSVersion              string
	Platform               string
	AppVersion             string
	DeviceTokenHash        string `gorm:"index"`
	SendRealtimeEnabled    bool
	ReceiveRealtimeEnabled bool
	Disabled               bool
	LastSeenAt             *time.Time
	LastAckedCursor        uint
	CapabilitiesJSON       string `gorm:"type:text"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type ClipboardItem struct {
	ID              uint   `gorm:"primaryKey"`
	AccountID       uint   `gorm:"index"`
	SourceDeviceID  uint   `gorm:"index;uniqueIndex:idx_source_client_item"`
	ClientItemID    string `gorm:"size:191;uniqueIndex:idx_source_client_item"`
	ContentKind     string `gorm:"size:32"`
	UploadKind      string `gorm:"size:32"`
	MimeType        string `gorm:"size:255"`
	TextContent     string `gorm:"type:text"`
	BlobPath        string `gorm:"size:1024"`
	ByteSize        int64
	CharCount       int
	ClientCreatedAt time.Time  `gorm:"index"`
	ReceivedAt      time.Time  `gorm:"index"`
	ExpiresAt       *time.Time `gorm:"index"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (Device) TableName() string {
	return "devices"
}

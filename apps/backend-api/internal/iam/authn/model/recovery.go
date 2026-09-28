package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type PasswordRecoveryToken struct {
	TokenHMAC   string     `gorm:"column:token_hmac;type:text;primaryKey"`
	PrincipalID guid.ID    `gorm:"column:principal_id;not null"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt   time.Time  `gorm:"column:expires_at;not null"`
	ConsumedAt  *time.Time `gorm:"column:consumed_at"`
}

func (PasswordRecoveryToken) TableName() string { return "iam_password_recovery_tokens" }

type NotificationOutbox struct {
	PrincipalID       *guid.ID   `gorm:"column:principal_id"`
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	Kind              string     `gorm:"column:kind;type:text;not null"`
	Recipient         string     `gorm:"column:recipient;type:text;not null"`
	PayloadCiphertext string     `gorm:"column:payload_ciphertext;type:text;not null"`
	Status            string     `gorm:"column:status;type:text;not null"`
	Attempts          int        `gorm:"column:attempts;not null"`
	AvailableAt       time.Time  `gorm:"column:available_at;not null"`
	LockedAt          *time.Time `gorm:"column:locked_at"`
	SentAt            *time.Time `gorm:"column:sent_at"`
	LastError         string     `gorm:"column:last_error;type:text;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;not null"`
}

func (NotificationOutbox) TableName() string { return "iam_notification_outbox" }

type EmailVerificationToken struct {
	TokenHMAC   string     `gorm:"column:token_hmac;type:text;primaryKey"`
	PrincipalID guid.ID    `gorm:"column:principal_id;not null"`
	Email       string     `gorm:"column:email;type:text;not null"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	ExpiresAt   time.Time  `gorm:"column:expires_at;not null"`
	ConsumedAt  *time.Time `gorm:"column:consumed_at"`
	Code        string     `gorm:"column:code;type:text;not null"`
	Attempts    int        `gorm:"column:attempts;not null"`
}

func (EmailVerificationToken) TableName() string { return "iam_email_verification_tokens" }

package audit

import (
	"time"

	"7link.com/platform/pkg/module"
)

// Config configures IAM domain audit anchoring and archiving. It is distinct from the
// application operation-audit configuration used by internal/infra/audit.
type Config struct {
	module.Config
	Anchor  AnchorConfig  `mapstructure:"anchor" group:"anchor"`
	Archive ArchiveConfig `mapstructure:"archive" group:"archive"`
}

// ArchiveConfig uses the anchor S3 endpoint, credentials and object-lock bucket;
// archive objects have a separate prefix and do not require anchor.Enabled.
type ArchiveConfig struct {
	Enabled  bool          `mapstructure:"enabled" description:"execute durable audit archives using the anchor S3 configuration; never delete source events" default:"false"`
	Interval time.Duration `mapstructure:"interval" description:"interval between archive sweeps; at least one second" default:"1m"`
	Timeout  time.Duration `mapstructure:"timeout" description:"maximum duration per archive attempt; one second to one hour" default:"4m"`
}

type AnchorConfig struct {
	Enabled       bool   `mapstructure:"enabled" description:"anchor verified audit heads into an external S3-compatible object lock store" default:"false"`
	Endpoint      string `mapstructure:"endpoint" description:"S3-compatible endpoint, e.g. http://127.0.0.1:9000" default:""`
	Bucket        string `mapstructure:"bucket" description:"bucket holding audit anchors; created with object locking when it does not exist" default:"audit-anchors"`
	Region        string `mapstructure:"region" description:"signing region; empty is fine for MinIO" default:""`
	AccessKey     string `mapstructure:"access_key" description:"S3 access key" default:""`
	SecretKey     string `mapstructure:"secret_key" description:"S3 secret key" default:"" secret:"true"`
	RetentionDays int    `mapstructure:"retention_days" description:"object lock retention in days (COMPLIANCE mode)" default:"365"`
	SigningKey    string `mapstructure:"signing_key" description:"base64-encoded 32-byte Ed25519 seed signing anchored audit roots" default:"" secret:"true"`
}

func (c AnchorConfig) valid() bool {
	return c.Endpoint != "" && c.Bucket != "" && c.AccessKey != "" && c.SecretKey != "" && c.RetentionDays > 0 && c.RetentionDays <= maxRetentionDays
}

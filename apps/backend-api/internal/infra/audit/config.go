package audit

import (
	"strings"
	"time"
)

const (
	overflowDrop  = "drop"
	overflowBlock = "block"
)

// Config controls the operation audit module. The structured-log sink is always
// active; the database sink is gated by Database.Enabled and requires the
// operation_logs migration.
type Config struct {
	// Enabled is the master switch. When false, automatic HTTP/gRPC capture and
	// explicit Record calls are no-ops.
	Enabled bool `mapstructure:"enabled" description:"enable operation audit logging" default:"true"`
	// Database controls the gorm-backed sink. Events always reach the structured
	// log sink even when the database sink is disabled.
	Database DatabaseConfig `mapstructure:"database" group:"database"`
	// RecordReads records read-only operations (GET/List/Query). Reads are
	// skipped by default to avoid flooding the audit log.
	RecordReads bool `mapstructure:"record_reads" description:"record read-only operations" default:"false"`
	// RecordStreams records WebSocket and Server-Sent Event sessions. Sessions
	// are logged once as a terminal summary, never per message.
	RecordStreams bool `mapstructure:"record_streams" description:"record WebSocket and SSE sessions" default:"true"`
	// CaptureBodies opts into redacted JSON request and error-response bodies.
	// It is disabled by default so enabling operation metadata does not
	// automatically expand the application's sensitive-data footprint.
	CaptureBodies bool `mapstructure:"capture_bodies" description:"capture redacted JSON request and error response bodies" default:"false"`
	// MaxBodyBytes caps the captured request/response body size per direction.
	MaxBodyBytes int `mapstructure:"max_body_bytes" description:"maximum captured request or response body size in bytes" default:"8192"`
	// SensitiveFields adds JSON field names whose values are redacted in bodies.
	SensitiveFields []string `mapstructure:"sensitive_fields" description:"additional JSON field names to redact" default:"[]"`
	// SkipRoutes extends the built-in path-prefix skip list.
	SkipRoutes []string `mapstructure:"skip_routes" description:"additional request path prefixes excluded from auditing" default:"[]"`

	// QueueSize is the capacity of the in-process event queue.
	QueueSize int `mapstructure:"queue_size" description:"audit event queue capacity" default:"4096"`
	// BatchSize is the number of events written per database batch.
	BatchSize int `mapstructure:"batch_size" description:"events per database write batch" default:"200"`
	// FlushInterval is the maximum time between database flushes when the batch
	// size is not yet reached.
	FlushInterval time.Duration `mapstructure:"flush_interval" description:"maximum time between database flushes" default:"1s"`
	// FlushTimeout bounds a single flush (used during shutdown drain).
	FlushTimeout time.Duration `mapstructure:"flush_timeout" description:"maximum time for one database flush" default:"5s"`
	// Overflow selects the queue-full behavior: drop (default) or block.
	Overflow string `mapstructure:"overflow" description:"queue overflow behavior: drop | block" default:"drop" validate:"^(drop|block)$"`
}

// DatabaseConfig controls the gorm-backed audit sink.
type DatabaseConfig struct {
	// Enabled writes events to the operation_logs table.
	Enabled bool `mapstructure:"enabled" description:"persist audit events to the operation_logs table" default:"false"`
}

func defaultConfig() Config {
	return Config{
		Enabled:         true,
		Database:        DatabaseConfig{Enabled: false},
		RecordReads:     false,
		RecordStreams:   true,
		CaptureBodies:   false,
		MaxBodyBytes:    8192,
		QueueSize:       4096,
		BatchSize:       200,
		FlushInterval:   time.Second,
		FlushTimeout:    5 * time.Second,
		Overflow:        overflowDrop,
		SensitiveFields: []string{},
		SkipRoutes:      []string{},
	}
}

// normalizeConfig clamps invalid configuration to safe defaults. It is applied
// by the module constructor so directly-constructed Config values cannot
// produce a zero-size queue, a non-positive batch, or an unknown overflow mode.
func normalizeConfig(config Config) Config {
	if !config.Enabled {
		return config
	}
	base := defaultConfig()
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = base.MaxBodyBytes
	}
	if config.QueueSize <= 0 {
		config.QueueSize = base.QueueSize
	}
	if config.BatchSize <= 0 {
		config.BatchSize = base.BatchSize
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = base.FlushInterval
	}
	if config.FlushTimeout <= 0 {
		config.FlushTimeout = base.FlushTimeout
	}
	switch normalized := strings.ToLower(strings.TrimSpace(config.Overflow)); normalized {
	case overflowDrop, overflowBlock:
		// Write the canonical form back: downstream comparisons are exact, so
		// leaving " BLOCK " in place would silently select the drop path.
		config.Overflow = normalized
	default:
		config.Overflow = base.Overflow
	}
	return config
}

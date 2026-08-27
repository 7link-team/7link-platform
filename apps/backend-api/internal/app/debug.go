package app

import "time"

// applyDebugDefaults keeps normal startup tolerant of a missing database while
// making --debug immediately usable for modules that require one.
func applyDebugDefaults(config *AppConfig) {
	if config == nil || !config.Debug {
		return
	}
	if len(config.Database) == 0 {
		config.Database = map[string]Database{
			"default": {
				Type:            "sqlite",
				Dsn:             "sqlite.db",
				Writable:        true,
				Readable:        true,
				MaxOpenConns:    25,
				MaxIdleConns:    5,
				ConnMaxLifetime: 30 * time.Minute,
				ConnMaxIdleTime: 5 * time.Minute,
			},
		}
	}
}

package healthi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	CheckCompleted     = "health_check_completed"
	ReadinessSucceeded = "readiness_check_succeeded"
	ReadinessFailed    = "readiness_check_failed"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error {
	return platformi18n.RegisterFS(locales, "locales")
}

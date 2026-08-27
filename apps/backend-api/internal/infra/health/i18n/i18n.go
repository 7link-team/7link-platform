package healthi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace          = "health"
	CheckCompleted     = Namespace + ".check_completed"
	ReadinessSucceeded = Namespace + ".readiness_succeeded"
	ReadinessFailed    = Namespace + ".readiness_failed"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}

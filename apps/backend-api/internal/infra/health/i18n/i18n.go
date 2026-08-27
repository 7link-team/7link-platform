package healthi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const CheckSucceeded = "health_check_succeeded"

//go:embed locales/*.json
var locales embed.FS

func Register() error {
	return platformi18n.RegisterFS(locales, "locales")
}

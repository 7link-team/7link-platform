package templatesi18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const Namespace = "templates"

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

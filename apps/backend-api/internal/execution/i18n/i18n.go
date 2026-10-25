package executioni18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

//go:embed locales/*.json
var locales embed.FS

const Namespace = "execution"

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

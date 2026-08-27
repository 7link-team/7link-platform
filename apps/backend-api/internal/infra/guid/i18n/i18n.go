package guidi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace = "guid"
	NotReady  = Namespace + ".not_ready"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}

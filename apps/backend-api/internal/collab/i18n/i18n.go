package collabi18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const (
	NotEditable = "collab.message_not_editable"
	Namespace   = "collab"
	Forbidden   = Namespace + ".forbidden"
	NotFound    = Namespace + ".not_found"
	Invalid     = Namespace + ".invalid"
	Conflict    = Namespace + ".conflict"
	Unavailable = Namespace + ".unavailable"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

package planningi18n

import (
	p "7link.com/platform/pkg/i18n"
	"embed"
)

const Namespace = "planning"
const (
	Forbidden   = Namespace + ".forbidden"
	NotFound    = Namespace + ".not_found"
	Invalid     = Namespace + ".invalid"
	Conflict    = Namespace + ".conflict"
	Unavailable = Namespace + ".unavailable"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return p.RegisterFS(Namespace, locales, "locales") }

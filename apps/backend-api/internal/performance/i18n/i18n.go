package performancei18n

import (
	pi "7link.com/platform/pkg/i18n"
	"embed"
)

const Namespace = "performance"
const Invalid = Namespace + ".invalid"
const Forbidden = Namespace + ".forbidden"
const Unavailable = Namespace + ".unavailable"
const Conflict = Namespace + ".conflict"
const NotFound = Namespace + ".not_found"
const Failed = Namespace + ".failed"

//go:embed locales/*.json
var locales embed.FS

func Register() error { return pi.RegisterFS(Namespace, locales, "locales") }

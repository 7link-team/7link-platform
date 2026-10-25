package agentsi18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const (
	Namespace              = "agents"
	Forbidden              = Namespace + ".forbidden"
	NotFound               = Namespace + ".not_found"
	Invalid                = Namespace + ".invalid"
	Conflict               = Namespace + ".conflict"
	Unavailable            = Namespace + ".unavailable"
	RetirementNameMismatch = Namespace + ".retirement_name_mismatch"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

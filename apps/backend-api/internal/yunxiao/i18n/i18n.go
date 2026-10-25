package yi18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const (
	StateInUse            = "yunxiao.state_in_use"
	Namespace             = "yunxiao"
	Unavailable           = Namespace + ".unavailable"
	Invalid               = Namespace + ".invalid"
	Forbidden             = Namespace + ".forbidden"
	IngressSignature      = Namespace + ".ingress_signature"
	IngressExhausted      = Namespace + ".ingress_exhausted"
	NotFound              = Namespace + ".not_found"
	Conflict              = Namespace + ".conflict"
	TransitionBlocked     = Namespace + ".transition_blocked"
	TransitionUnavailable = Namespace + ".transition_unavailable"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

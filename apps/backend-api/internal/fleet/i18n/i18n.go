package fleeti18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const (
	Namespace   = "fleet"
	Forbidden   = Namespace + ".forbidden"
	NotFound    = Namespace + ".not_found"
	Invalid     = Namespace + ".invalid"
	Conflict    = Namespace + ".conflict"
	Unavailable = Namespace + ".unavailable"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

const TerminalDisabled = Namespace + ".terminal_disabled"
const TerminalClosed = Namespace + ".terminal_closed"
const TerminalReauthentication = Namespace + ".terminal_reauthentication"
const TunnelQuota = Namespace + ".tunnel_quota"

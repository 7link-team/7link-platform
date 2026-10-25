package clienti18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const (
	BundleStale     = Namespace + ".bundle_stale"
	BundleFailed    = Namespace + ".bundle_failed"
	Namespace       = "clientwork"
	DispatchExpired = Namespace + ".dispatch_expired"
	ShareEnded      = Namespace + ".share_ended"
	OpenException   = Namespace + ".open_exception"
	Invalid         = Namespace + ".invalid"
	NotFound        = Namespace + ".not_found"
	Conflict        = Namespace + ".conflict"
	Forbidden       = Namespace + ".forbidden"
	Unavailable     = Namespace + ".unavailable"
	Internal        = Namespace + ".internal"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

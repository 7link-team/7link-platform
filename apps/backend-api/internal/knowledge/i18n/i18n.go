package knowledgei18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const (
	Namespace        = "knowledge"
	Forbidden        = Namespace + ".forbidden"
	NotFound         = Namespace + ".not_found"
	Invalid          = Namespace + ".invalid"
	Conflict         = Namespace + ".conflict"
	Unavailable      = Namespace + ".unavailable"
	MemoryQuota      = Namespace + ".memory_quota"
	MemorySecret     = Namespace + ".memory_secret"
	MemoryProvenance = Namespace + ".memory_provenance"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

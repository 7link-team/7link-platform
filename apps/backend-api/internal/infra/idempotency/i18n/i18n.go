package idempotencyi18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const (
	Namespace  = "idempotency"
	InvalidKey = Namespace + ".invalid_key"
	KeyReused  = Namespace + ".key_reused"
	InProgress = Namespace + ".in_progress"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

package governancei18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const EscapeNotFound = Namespace + ".escape_not_found"
const EscapeDecided = Namespace + ".escape_decided"

const Namespace = "business-governance"
const SpawnInvalid = Namespace + ".spawn_invalid"
const SpawnQuota = Namespace + ".spawn_quota"
const SpawnBudget = Namespace + ".spawn_budget"
const SpawnActive = Namespace + ".spawn_active"

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

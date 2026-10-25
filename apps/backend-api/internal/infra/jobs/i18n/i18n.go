package jobsi18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const (
	Namespace         = "jobs"
	VersionRequired   = Namespace + ".version_required"
	VersionConflict   = Namespace + ".version_conflict"
	InvalidTransition = Namespace + ".invalid_transition"
	ScheduleActive    = Namespace + ".schedule_active"
)

//go:embed locales/*.json
var locales embed.FS

var fieldMessages = platformi18n.NewModuleI18nFS(platformi18n.Base, locales, "locales")

func Translations(key string) map[string]string { return fieldMessages.Translations(key) }

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

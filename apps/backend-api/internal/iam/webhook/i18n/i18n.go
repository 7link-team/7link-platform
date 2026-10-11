// Package webhooki18n publishes webhook's locale bundle.
package webhooki18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const Namespace = "webhook"

const (
	InvalidRequest      = Namespace + ".invalid_request"
	SubscriptionMissing = Namespace + ".subscription_missing"
	SubscriptionExists  = Namespace + ".subscription_exists"
	Unavailable         = Namespace + ".unavailable"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

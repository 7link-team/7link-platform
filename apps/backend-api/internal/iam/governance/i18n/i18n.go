// Package governancei18n publishes governance's locale bundle.
package governancei18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace = "governance"

	RequestNotFound           = Namespace + ".access_request_not_found"
	RoleNotFound              = Namespace + ".access_request_role_not_found"
	RequestExpired            = Namespace + ".access_request_expired"
	RequesterInactive         = Namespace + ".access_request_requester_inactive"
	AlreadyGranted            = Namespace + ".access_already_granted"
	RequestStateConflict      = Namespace + ".access_request_state_conflict"
	SelfApproval              = Namespace + ".access_request_self_approval"
	RequesterOnly             = Namespace + ".access_request_requester_only"
	InvalidRequest            = Namespace + ".invalid_access_request"
	HumanPrincipalRequired    = Namespace + ".human_principal_required"
	Unavailable               = Namespace + ".governance_unavailable"
	ReviewNotFound            = Namespace + ".access_review_not_found"
	ReviewItemNotFound        = Namespace + ".access_review_item_not_found"
	ReviewExpired             = Namespace + ".access_review_expired"
	ReviewEmpty               = Namespace + ".access_review_empty"
	ReviewStateConflict       = Namespace + ".access_review_state_conflict"
	ReviewIncomplete          = Namespace + ".access_review_incomplete"
	ReviewSelfDecision        = Namespace + ".access_review_self_decision"
	ReviewLastAdministrator   = Namespace + ".access_review_last_administrator"
	InvalidReview             = Namespace + ".invalid_access_review"
	ReviewDerivedNotRevocable = Namespace + ".access_review_derived_not_revocable"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

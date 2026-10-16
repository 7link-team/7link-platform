package deliveryi18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const (
	Namespace               = "delivery"
	ProjectCodeTaken        = Namespace + ".project_code_taken"
	ProjectNameTaken        = Namespace + ".project_name_taken"
	ParentNotClient         = Namespace + ".parent_not_client"
	ProvisioningUnavailable = Namespace + ".provisioning_unavailable"
	ContactNotFound         = Namespace + ".contact_not_found"
	IntakeNotFound          = Namespace + ".intake_not_found"
	MemberRoleMismatch      = Namespace + ".member_role_mismatch"
	ProjectLimitReached     = Namespace + ".project_limit_reached"
	HumanOnly               = Namespace + ".human_only"

	ClientChoice      = Namespace + ".client_choice"
	PrimaryContact    = Namespace + ".primary_contact"
	PrimaryAccept     = Namespace + ".primary_accept"
	RepositoryURL     = Namespace + ".repository_url"
	RepositoryName    = Namespace + ".repository_name"
	PrimaryRepository = Namespace + ".primary_repository"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

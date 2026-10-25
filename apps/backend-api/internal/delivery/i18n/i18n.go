package deliveryi18n

import (
	platformi18n "7link.com/platform/pkg/i18n"
	"embed"
)

const (
	AttentionClosed            = Namespace + ".attention_session_closed"
	RequirementSuiteMissing    = Namespace + ".requirement_suite_missing"
	ResultEvidenceForbidden    = Namespace + ".result_evidence_forbidden"
	ResultState                = Namespace + ".result_state"
	ResultIndependent          = Namespace + ".result_independent"
	ResultCoverage             = Namespace + ".result_coverage"
	DependencyLimit            = Namespace + ".dependency_limit"
	DependencyProject          = Namespace + ".dependency_cross_project"
	DependencyCycle            = Namespace + ".dependency_cycle"
	WorkVersionConflict        = Namespace + ".work_version_conflict"
	WorkPreparationState       = Namespace + ".work_preparation_state"
	Namespace                  = "delivery"
	ProjectCodeTaken           = Namespace + ".project_code_taken"
	ProjectNameTaken           = Namespace + ".project_name_taken"
	ParentNotClient            = Namespace + ".parent_not_client"
	ProvisioningUnavailable    = Namespace + ".provisioning_unavailable"
	ContactNotFound            = Namespace + ".contact_not_found"
	IntakeNotFound             = Namespace + ".intake_not_found"
	MemberRoleMismatch         = Namespace + ".member_role_mismatch"
	ProjectLimitReached        = Namespace + ".project_limit_reached"
	HumanOnly                  = Namespace + ".human_only"
	RequirementVersionConflict = Namespace + ".requirement_version_conflict"
	RequirementFrozen          = Namespace + ".requirement_frozen"
	RequirementStage           = Namespace + ".requirement_stage"
	RequirementLimit           = Namespace + ".requirement_limit"
	CriterionLimit             = Namespace + ".criterion_limit"
	WorkProjectReadOnly        = Namespace + ".work_project_read_only"
	ConstitutionTooLarge       = Namespace + ".constitution_too_large"
	ConstitutionState          = Namespace + ".constitution_state"
	ConstitutionVersion        = Namespace + ".constitution_version"
	ConstitutionHumanOnly      = Namespace + ".constitution_human_only"
	AssignmentState            = Namespace + ".assignment_state"
	AssignmentHeld             = Namespace + ".assignment_held"
	AssignmentHolder           = Namespace + ".assignment_holder"
	AssignmentReadiness        = Namespace + ".assignment_readiness"
	StageIllegal               = Namespace + ".stage_transition_illegal"
	StageDelivered             = Namespace + ".stage_delivered_requires_acceptance"
	StageClose                 = Namespace + ".stage_close_open_work"
	StageCriteria              = Namespace + ".stage_criteria_unmet"
	StageReason                = Namespace + ".stage_reason_required"

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

const VerificationPending = Namespace + ".verification_pending"

const (
	ModeSame          = Namespace + ".mode_same"
	ModeIllegal       = Namespace + ".mode_transition_illegal"
	ModePreconditions = Namespace + ".mode_preconditions_unmet"
)

const DefectDeploymentMismatch = Namespace + ".deployment_evidence_mismatch"
const DefectReopenSourceNotTerminal = Namespace + ".defect_reopen_source_not_terminal"

package governance

import (
	"context"
	"database/sql/driver"
	"strings"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/policyx"
	"gorm.io/gorm"
)

// roleAuthority is the S4 portion of the authority evaluation. Carrier matches
// are evidence for S6, never a direct role grant. This does not authorize an
// action by itself: S0-S3 and the nondelegable/delegation/policy gates still apply.
type roleAuthority struct {
	source       string
	carriers     []authz.DecisionMatch
	customSource string
}

// roleAuthoritySource resolves stable business identities from the tenant's
// seed ledger, never role display names. db is the same read handle used for
// the IAM explanation (the caller transaction for a business write).
func (s *seedService) roleAuthoritySource(ctx context.Context, db *gorm.DB, tenant guid.ID, permission string, explanation authz.Explanation) (roleAuthority, error) {
	if tenant <= 0 || permission == "" {
		return roleAuthority{}, ErrInvalidArgument
	}
	if !explanation.Allowed || explanation.Reason == "explicit_deny" || explanation.Reason == "administrator_scope_denied" {
		return roleAuthority{}, nil
	}
	if s == nil || s.names.RoleSeeds.IsZero() || db == nil {
		return roleAuthority{}, ErrUnavailable
	}
	ids := make([]driver.Valuer, 0, len(explanation.Matches))
	seen := make(map[guid.ID]bool)
	for _, match := range explanation.Matches {
		if match.Effect != "allow" || (match.PermissionCode != permission && match.PermissionCode != "tenant_administer") {
			continue
		}
		id, err := guid.Parse(match.RoleID)
		if err != nil {
			return roleAuthority{}, ErrInvalidArgument
		}
		if id > 0 && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	keys := make(map[guid.ID]string, len(ids))
	if len(ids) > 0 {
		table := query.BindRoleSeeds(db, s.names)
		rows, err := table.WithContext(ctx).Where(table.TenantID.Eq(tenant), table.RoleID.In(ids...)).Find()
		if err != nil {
			return roleAuthority{}, err
		}
		known := make(map[string]bool)
		for _, entry := range defaultRoleManifest() {
			known[entry.Key] = true
		}
		for _, row := range rows {
			if row.SeedConflict {
				continue
			}
			if !known[row.RoleKey] {
				return roleAuthority{}, ErrInvalidArgument
			}
			// managed=false stops upgrades; it does not erase the role's identity.
			keys[row.RoleID] = row.RoleKey
		}
	}
	result := roleAuthority{}
	bestRank := 6
	var bestID guid.ID
	var customID guid.ID
	choose := func(key string, id guid.ID) {
		if strings.HasPrefix(key, "custom.") && (customID == 0 || id < customID) {
			customID = id
			result.customSource = "ROLE:" + key
		}
		rank := roleAuthorityRank(key)
		if result.source == "" || rank < bestRank || (rank == bestRank && id < bestID) {
			result.source = "ROLE:" + key
			bestRank = rank
			bestID = id
		}
	}
	// IAM also reports administrator authority for an organization account view,
	// where no ordinary role match is present. Its decision reason is authoritative.
	if explanation.Reason == "administrator_grant" && len(explanation.Matches) == 0 {
		choose("tenant_administrator", 0)
	}
	for _, match := range explanation.Matches {
		if match.Effect != "allow" || (match.PermissionCode != permission && match.PermissionCode != "tenant_administer") {
			continue
		}
		id, _ := guid.Parse(match.RoleID) // validated above; zero denotes non-role matches
		if id <= 0 {
			continue
		}
		key := keys[id]
		if strings.HasPrefix(key, "delegated_") {
			result.carriers = append(result.carriers, match)
			continue
		}
		if key == "" {
			if match.PermissionCode == "tenant_administer" {
				key = "tenant_administrator"
			} else {
				key = "custom." + id.String()
			}
		}
		choose(key, id)
	}
	return result, nil
}

func roleAuthorityRank(key string) int {
	switch key {
	case "team_owner":
		return 0
	case "delivery_lead":
		return 1
	case "tenant_administrator":
		return 2
	case "engineer":
		return 3
	default:
		if strings.HasPrefix(key, "agent_") {
			return 4
		}
		return 5
	}
}

// AuthorityStep is an evaluated stage in the domain authority decision.
type AuthorityStep struct {
	Step    string         `json:"step"`
	Outcome string         `json:"outcome"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// AuthorityDecision retains both the IAM revision and the domain denial reason.
// An empty source never proves authority, even when IAM Allowed was true.
type AuthorityDecision struct {
	Allowed         bool            `json:"allowed"`
	AuthoritySource string          `json:"authority_source,omitempty"`
	DelegationID    guid.ID         `json:"delegation_id,omitempty"`
	ReasonCode      string          `json:"reason_code,omitempty"`
	PolicyRevision  int64           `json:"policy_revision"`
	Steps           []AuthorityStep `json:"steps"`
}

// ProjectCreationAuthority exposes only the implemented, nondelegable creation
// decision. actorKind must be resolved by the server, never from request JSON.
// With tx it fences authorization until the caller commits or rolls back.
type ProjectCreationAuthority func(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, actorKind string) (AuthorityDecision, error)

// ProjectStageAuthority evaluates a direct role grant for an existing project.
// Actor identity/lifecycle and the phase-specific guards remain with its owner.
type ProjectStageAuthority func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (AuthorityDecision, error)

func (m *Module) projectStageAuthority(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID, actorKind string) (AuthorityDecision, error) {
	return m.Check(ctx, tx, AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: principal, ActorKind: actorKind, Code: "project_stage_change", Resource: ResourceRef{Kind: "project", ID: project}})
}

func (m *Module) checkProjectStage(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error) {
	result := AuthorityDecision{Steps: []AuthorityStep{{Step: "S0", Outcome: "PASS"}}}
	deny := func(step, reason string) (AuthorityDecision, error) {
		result.Steps = append(result.Steps, AuthorityStep{Step: step, Outcome: "DENY"})
		result.ReasonCode = reason
		return result, nil
	}
	if req.ProjectID <= 0 || req.Resource.Kind != "project" || req.Resource.ID != req.ProjectID || req.Spawn != nil || req.Facts != nil {
		return deny("S0", "INVALID_REQUEST")
	}
	if req.ActorKind != "HUMAN" && req.ActorKind != "AGENT" {
		return deny("S2", "ACTOR_NOT_ELIGIBLE")
	}
	if req.Environment == "PRODUCTION" || m.explain == nil {
		return AuthorityDecision{}, ErrUnavailable
	}
	if _, ok := m.seed.registry.Find(req.Code); !ok {
		return AuthorityDecision{}, ErrUnavailable
	}
	explanation, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: req.TenantID, EntityID: req.ProjectID, PrincipalID: req.PrincipalID, Permission: req.Code})
	if err != nil {
		return AuthorityDecision{}, err
	}
	result.PolicyRevision = explanation.Revision
	if explanation.Reason == "inactive_membership" || explanation.Reason == "inactive_tenant" {
		return deny("S1", "PRINCIPAL_INACTIVE")
	}
	result.Steps = append(result.Steps, AuthorityStep{Step: "S1", Outcome: "PASS"})
	if explanation.Reason == "explicit_deny" || explanation.Reason == "administrator_scope_denied" {
		return deny("S4", "EXPLICIT_DENY")
	}
	if !explanation.Allowed {
		return deny("S4", "NO_ROLE_GRANT")
	}
	read := tx
	if read == nil {
		if m.runtime == nil || m.runtime.Database() == nil {
			return AuthorityDecision{}, ErrUnavailable
		}
		read = m.runtime.Database().Write()
	}
	source, err := m.seed.roleAuthoritySource(ctx, read, req.TenantID, req.Code, explanation)
	if err != nil {
		return AuthorityDecision{}, err
	}
	if req.ActorKind == "AGENT" {
		source.source = source.customSource
	}
	if source.source == "" {
		return deny("S5", "NON_DELEGABLE")
	}
	result.Allowed, result.AuthoritySource = true, source.source
	result.Steps = append(result.Steps, AuthorityStep{Step: "S4", Outcome: "MATCH"})
	return result, nil
}

func (m *Module) projectCreationAuthority(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, actorKind string) (AuthorityDecision, error) {
	return m.Check(ctx, tx, AuthorityRequest{TenantID: tenant, PrincipalID: principal, ActorKind: actorKind, Code: "project_create"})
}

// checkProjectCreate is the nondelegable project_create branch of the domain
// checker. Its caller supplies a server-resolved actor kind; HTTP input must
// never choose it. No seeding, delegation use, audit or business mutation occurs
// here. The full action dispatcher and eligibility listing are separate work.
func (m *Module) checkProjectCreate(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, actorKind string) (AuthorityDecision, error) {
	result := AuthorityDecision{Steps: make([]AuthorityStep, 0, 6)}
	step := func(name, outcome string) {
		result.Steps = append(result.Steps, AuthorityStep{Step: name, Outcome: outcome})
	}
	deny := func(name, reason string) (AuthorityDecision, error) {
		step(name, "DENY")
		result.ReasonCode = reason
		return result, nil
	}
	if tenant <= 0 || principal <= 0 || (actorKind != "HUMAN" && actorKind != "AGENT" && actorKind != "MACHINE") {
		return deny("S0", "INVALID_REQUEST")
	}
	if m == nil || m.seed == nil || m.seed.registry == nil || m.explain == nil {
		return AuthorityDecision{}, ErrUnavailable
	}
	if _, ok := m.seed.registry.Find("project_manage"); !ok {
		return AuthorityDecision{}, ErrUnavailable
	}
	step("S0", "PASS")
	explanation, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, PrincipalID: principal, Permission: "project_manage"})
	if err != nil {
		return AuthorityDecision{}, err
	}
	result.PolicyRevision = explanation.Revision
	if explanation.Reason == "inactive_membership" || explanation.Reason == "inactive_tenant" {
		return deny("S1", "PRINCIPAL_INACTIVE")
	}
	step("S1", "PASS")
	// Neither an IAM role nor a delegation can turn a non-human into a project
	// creator. No Agent-directory lookup can make this branch permit creation.
	if RulesForAction("project_create", actorKind).HumanOnly && actorKind != "HUMAN" {
		return deny("S2", "NON_DELEGABLE")
	}
	step("S2", "PASS")
	step("S3", "SKIP") // Creating a project has no existing work-item participants.
	if explanation.Reason == "explicit_deny" || explanation.Reason == "administrator_scope_denied" {
		return deny("S4", "EXPLICIT_DENY")
	}
	if !explanation.Allowed {
		step("S4", "NO_MATCH")
		return deny("S5", "NON_DELEGABLE")
	}
	read := tx
	if read == nil {
		if m.runtime == nil || m.runtime.Database() == nil {
			return AuthorityDecision{}, ErrUnavailable
		}
		read = m.runtime.Database().Write()
	}
	source, err := m.seed.roleAuthoritySource(ctx, read, tenant, "project_manage", explanation)
	if err != nil {
		return AuthorityDecision{}, err
	}
	if source.source == "" {
		step("S4", "NO_MATCH")
		return deny("S5", "NON_DELEGABLE")
	}
	result.Allowed = true
	result.AuthoritySource = source.source
	step("S4", "MATCH")
	return result, nil
}

// ResourceRef identifies a business-owned object without exposing its storage.
type ResourceRef struct {
	Kind string
	ID   guid.ID
}

// AuthorityRequest carries server-resolved facts, never caller-supplied claims.
// SYSTEM actors have PrincipalID zero and identify a registered SystemJob.
type AuthorityRequest struct {
	TenantID, ProjectID guid.ID
	ActorKind           string
	PrincipalID         guid.ID
	SystemJob           string
	Code                string
	Resource            ResourceRef
	Environment         string
	Facts               *WorkItemFacts
	Spawn               *SpawnRequestFacts
}

// WorkItemFacts is the read-only decision context defined by spec08 §3.5.
// CostMicros excludes verification executions; zero verification records cannot
// be represented as RequiredVerificationsPassed=true.
type WorkItemFacts struct {
	ClaimID                      guid.ID
	Type, Risk                   string
	CostMicros                   int64
	QualityReworkCount           int
	SelfProduced                 bool
	AssignmentRole               string
	AssigneePrincipalID          guid.ID
	DispatchedByActor            bool
	RequiredVerificationsPassed  bool
	RequireHumanDecision, Paused bool
	IntakeConvertMode            string
	TriageCardAuthorID           string
}

// SpawnRequestFacts describes the proposed Agent, not an already-reserved quota.
// The fleet module must reserve quota transactionally after authority succeeds.
type SpawnRequestFacts struct {
	SupervisorAgentID  guid.ID   `json:"supervisor_agent_id"`
	ProjectIDs         []guid.ID `json:"project_ids" maxItems:"200"`
	Role               string    `json:"role" enum:"EXECUTOR,ASSISTANT"`
	MachineID          guid.ID   `json:"machine_id"`
	Runtime            string    `json:"runtime" enum:"CODEX,CLAUDE_CODE"`
	Model              string    `json:"model" minLength:"1" maxLength:"160"`
	Effort             string    `json:"effort" maxLength:"20"`
	EstimatedCapMicros int64     `json:"estimated_cap_micros" minimum:"1"`
}

// Check dispatches implemented domain authority branches using the frozen
// spec08 request contract. Unsupported registered actions return ErrUnavailable,
// never an implicit grant; the complete checker capability is not published
// until its remaining branches and ListEligible are implemented.
func (m *Module) Check(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error) {
	invalid := func(reason string) (AuthorityDecision, error) {
		return AuthorityDecision{ReasonCode: reason, Steps: []AuthorityStep{{Step: "S0", Outcome: "DENY"}}}, nil
	}
	if req.TenantID <= 0 || req.Code == "" || (req.Environment != "" && req.Environment != "DEFAULT" && req.Environment != "PRODUCTION") {
		return invalid("INVALID_REQUEST")
	}
	switch req.ActorKind {
	case "HUMAN", "AGENT", "MACHINE":
		if req.PrincipalID <= 0 || req.SystemJob != "" {
			return invalid("INVALID_REQUEST")
		}
	case "SYSTEM":
		if req.PrincipalID != 0 || req.SystemJob == "" {
			return invalid("INVALID_REQUEST")
		}
	default:
		return invalid("INVALID_REQUEST")
	}
	if m == nil || m.seed == nil || m.seed.registry == nil {
		return AuthorityDecision{}, ErrUnavailable
	}
	if req.Code == "production_access" || req.Environment == "PRODUCTION" {
		decision, err := m.checkProductionAuthority(ctx, tx, req)
		if err != nil || !decision.Allowed || req.Code == "production_access" {
			return decision, err
		}
		// The independent action still needs its ordinary authority and SoD gates.
		req.Environment = "DEFAULT"
	}
	if req.Code == "agent_spawn" {
		return m.checkSpawnAuthority(ctx, tx, req)
	}
	if req.ActorKind == "AGENT" && RulesForAction(req.Code, "AGENT").Delegable {
		if _, known := m.seed.registry.Find(req.Code); !known {
			return invalid("INVALID_REQUEST")
		}
		return m.checkDelegationAuthority(ctx, tx, req)
	}
	if req.Code == "code_merge" {
		return m.codeMergeAuthority(ctx, tx, req)
	}
	if req.Code == "work_item_assign" {
		return m.dispatchAuthority(ctx, tx, req)
	}
	if req.Code == "project_stage_change" {
		return m.checkProjectStage(ctx, tx, req)
	}
	if req.Code == "decision_force_valid" || req.Code == "scope_change" || req.Code == "decision_approve" || req.Code == "decision_request_changes" || req.Code == "decision_pause" || req.Code == "delivery_send" {
		return m.checkDeliveryAuthority(ctx, tx, req)
	}
	if req.Code != "project_create" {
		if _, known := m.seed.registry.Find(req.Code); !known {
			return invalid("INVALID_REQUEST")
		}
		return AuthorityDecision{}, ErrUnavailable
	}
	if req.ProjectID != 0 || req.Resource.ID != 0 || (req.Resource.Kind != "" && req.Resource.Kind != "project") || req.Spawn != nil {
		return invalid("INVALID_REQUEST")
	}
	if req.ActorKind == "SYSTEM" {
		// project_create is NonDelegable and cannot be registered in SystemActions.
		return AuthorityDecision{ReasonCode: "SYSTEM_NOT_REGISTERED", Steps: []AuthorityStep{{Step: "S0", Outcome: "PASS"}, {Step: "S1", Outcome: "DENY"}}}, nil
	}
	// H1 creates a HYBRID project in its default environment. These are server
	// facts; a context for another resource must not influence creation grants.
	ctx = policyx.WithResourceContext(ctx, policyx.ResourceContext{Type: "project", Owner: req.PrincipalID.String(), Attrs: map[string]string{"environment": "DEFAULT", "project_mode": "HYBRID"}})
	return m.checkProjectCreate(ctx, tx, req.TenantID, req.PrincipalID, req.ActorKind)
}

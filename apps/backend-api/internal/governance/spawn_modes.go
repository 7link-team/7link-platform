package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm/clause"
	"slices"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// bindAgentSpawn installs policy/usage callbacks without a reverse Fleet
// dependency. Fleet remains owner of identity and lifecycle mutations.
func (m *Module) bindAgentSpawn(s *module.Services) error {
	c, err := module.Resolve[fleet.AgentSpawnConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(fleet.AgentSpawnPolicy{FenceOn: func(ctx context.Context, tx *gorm.DB, tenant, supervisor guid.ID) error {
		return spawnFleetError(m.agentSpawnFenceOn(ctx, tx, tenant, supervisor))
	}, AuthorizeOn: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, in fleet.AgentSpawnInput, replay bool) (fleet.AgentSpawnGrant, error) {
		v, err := m.agentSpawnAuthorizeOn(ctx, tx, tenant, in, replay)
		return v, spawnFleetError(err)
	}, ReserveOn: func(ctx context.Context, tx *gorm.DB, tenant, child guid.ID, in fleet.AgentSpawnInput) error {
		return spawnFleetError(m.reserveSpawnOn(ctx, tx, tenant, child, agentSpawnRequest(ctx, tenant, in)))
	}, RetireOn: func(ctx context.Context, tx *gorm.DB, tenant, parent, child, grant guid.ID, release bool) error {
		return spawnFleetError(m.agentSpawnRetireOn(ctx, tx, tenant, parent, child, grant, release))
	}})
}

func spawnFleetError(err error) error {
	switch {
	case errors.Is(err, ErrForbidden):
		return fleet.ErrMachineForbidden
	case errors.Is(err, ErrInvalidArgument), errors.Is(err, ErrSpawnScope):
		return fleet.ErrInvalidArgument
	case errors.Is(err, ErrSpawnBudget), errors.Is(err, ErrSpawnQuota), errors.Is(err, ErrVersion):
		return fleet.ErrVersionConflict
	case errors.Is(err, ErrUnavailable):
		return fleet.ErrUnavailable
	default:
		return err
	}
}

// Project fences precede Fleet's machine and grant locks. Runtime inventory
// writers take machine then runtime, so model SHARE reads must follow machine.
func (m *Module) agentSpawnFenceOn(ctx context.Context, tx *gorm.DB, tenant, supervisor guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || supervisor <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m.seed == nil || m.delegations.projects.InspectOn == nil {
		return ErrUnavailable
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	g, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.GranteeAgentID.Eq(supervisor), q.GranteePrincipalID.Eq(authn.PrincipalIDFromContext(ctx)), q.State.Eq("ACTIVE"), q.StartsAt.Lte(m.seed.now()), q.EndsAt.Gt(m.seed.now())).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	var scope SpawnGrantScope
	if json.Unmarshal([]byte(g.ScopeJSON), &scope) != nil || !validSpawnGrantScope(scope) {
		return ErrUnavailable
	}
	ids := slices.Clone(scope.ProjectIDs)
	slices.Sort(ids)
	for _, id := range ids {
		if _, err = m.delegations.projects.InspectOn(ctx, tx, tenant, id); err != nil {
			return err
		}
	}
	return nil
}

func agentSpawnRequest(ctx context.Context, tenant guid.ID, in fleet.AgentSpawnInput) AuthorityRequest {
	role := ""
	if len(in.Registration.Roles) == 1 {
		role = in.Registration.Roles[0]
	}
	return AuthorityRequest{TenantID: tenant, PrincipalID: authn.PrincipalIDFromContext(ctx), ActorKind: "AGENT", Code: "agent_spawn", Spawn: &SpawnRequestFacts{SupervisorAgentID: in.SupervisorAgentID, ProjectIDs: in.ProjectIDs, Role: role, MachineID: in.Registration.MachineID, Runtime: in.Registration.Runtime, Model: in.Registration.Model, Effort: in.Effort, EstimatedCapMicros: in.EstimatedCapMicros}}
}

func (m *Module) agentSpawnAuthorizeOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in fleet.AgentSpawnInput, replay bool) (fleet.AgentSpawnGrant, error) {
	var out fleet.AgentSpawnGrant
	if tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 || len(in.Registration.Roles) != 1 || len(in.ProjectIDs) == 0 {
		return out, ErrInvalidArgument
	}
	a, err := m.checkSpawnAuthorityUsage(ctx, tx, agentSpawnRequest(ctx, tenant, in), replay)
	if err != nil {
		return out, err
	}
	if !a.Allowed {
		return out, ErrForbidden
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	g, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.GranteeAgentID.Eq(in.SupervisorAgentID), q.GranteePrincipalID.Eq(authn.PrincipalIDFromContext(ctx)), q.State.Eq("ACTIVE"), q.StartsAt.Lte(m.seed.now()), q.EndsAt.Gt(m.seed.now())).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrForbidden
	}
	if err != nil {
		return out, err
	}
	var scope SpawnGrantScope
	if json.Unmarshal([]byte(g.ScopeJSON), &scope) != nil || !validSpawnGrantScope(scope) {
		return out, ErrUnavailable
	}
	mode, err := m.spawnScopeOn(ctx, tx, tenant, scope)
	if err != nil {
		return out, err
	}
	if mode != "AUTO" {
		return out, ErrForbidden
	}
	// The existing IAM principal is never allowed to carry more authority than
	// its supervisor. Check the whole registered tenant catalog, including any
	// extra roles already held by the proposed account.
	for _, project := range in.ProjectIDs {
		state, err := m.delegations.projects.InspectOn(ctx, tx, tenant, project)
		if err != nil {
			return out, err
		}
		if state.Mode != "AUTONOMOUS" || state.Stage == "CLOSED" || state.Stage == "CANCELLED" { //nolint:misspell // Frozen project state.
			return out, ErrForbidden
		}
		for _, action := range m.seed.registry.All() {
			if action.Scope != "tenant" {
				continue
			}
			child, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: in.Registration.PrincipalID, Permission: action.Code})
			if err != nil {
				return out, err
			}
			if !child.Allowed {
				continue
			}
			parent, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: authn.PrincipalIDFromContext(ctx), Permission: action.Code})
			if err != nil {
				return out, err
			}
			if !parent.Allowed {
				return out, ErrForbidden
			}
		}
	}
	if !replay {
		remaining := scope.BudgetCapMicros - g.SpentMicros
		members := query.BindSpawnGrantMembers(tx, m.seed.names)
		rows, err := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.GrantID.Eq(g.ID), members.State.Eq("ACTIVE")).Find()
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			unspent := max(int64(0), row.PerAgentCapMicros-row.SpentMicros)
			if unspent > remaining {
				return out, ErrSpawnBudget
			}
			remaining -= unspent
		}
		if remaining < scope.PerAgentCapMicros {
			return out, ErrSpawnBudget
		}
	}
	return fleet.AgentSpawnGrant{GrantID: g.ID, OwnerPrincipalID: g.GrantedBy, ExpiresAt: m.seed.now().Add(time.Duration(scope.AgentTTLHours) * time.Hour)}, nil
}

func (m *Module) agentSpawnRetireOn(ctx context.Context, tx *gorm.DB, tenant, parent, child, grant guid.ID, release bool) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || parent <= 0 || child <= 0 || grant <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m.spawnDirectory.AgentOn == nil || m.principals.LockActiveRow == nil || m.delegations.members == nil || m.explain == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	_, active, err := m.principals.LockActiveRow(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	active, err = m.delegations.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	supervisor, found, err := m.spawnDirectory.AgentOn(ctx, tx, tenant, parent)
	if err != nil {
		return err
	}
	if !found || supervisor.PrincipalID != actor || supervisor.State != "ACTIVE" || !slices.Contains(supervisor.Roles, "LEAD") || (supervisor.Origin == "AGENT_SPAWNED" && supervisor.AdoptedAt == nil) {
		return ErrForbidden
	}
	x, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, PrincipalID: actor, Permission: "agent_spawn"})
	if err != nil {
		return err
	}
	if x.Reason == "explicit_deny" || x.Reason == "administrator_scope_denied" || x.Reason == "inactive_tenant" || x.Reason == "inactive_membership" {
		return ErrForbidden
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	g, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(grant), q.GranteeAgentID.Eq(parent), q.GranteePrincipalID.Eq(actor), q.State.Eq("ACTIVE"), q.StartsAt.Lte(m.seed.now()), q.EndsAt.Gt(m.seed.now())).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	members := query.BindSpawnGrantMembers(tx, m.seed.names)
	member, err := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.GrantID.Eq(g.ID), members.AgentID.Eq(child)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if member.State != "ACTIVE" && !(member.State == "RELEASED" && member.ReleaseReason != nil && *member.ReleaseReason == "RETIRED") {
		return ErrForbidden
	}
	if release {
		return m.releaseSpawnOn(ctx, tx, tenant, child, "RETIRED")
	}
	return nil
}

// AndonAuthority evaluates ordinary current IAM grants. Pulling an Andon is an
// Agent's direct safety action, not a delegated approval. Clearing is Human only.
type AndonAuthority func(context.Context, *gorm.DB, AuthorityRequest) (AuthorityDecision, error)

func (m *Module) provideSpawnModes(s *module.Services) error {
	if err := module.Provide[ProjectModeGovernance](s, ProjectModeGovernance{PrincipalsOn: m.modeDelegationPrincipalsOn, AuthorityOn: m.projectModeAuthorityOn, PreconditionsOn: m.modeGovernancePreconditionsOn, RevokeOn: m.revokeModeDelegationsOn, RevokeSpawnOn: m.revokeModeSpawnGrantsOn, AgentRoleOn: m.modeAgentRoleOn}); err != nil {
		return err
	}
	for _, kind := range []string{"ENSEMBLE_APPROVAL", "ENSEMBLE_SELECTION"} {
		if err := m.inbox.Register(InboxSource{Kind: kind, Read: func(ctx context.Context, req InboxRequest) ([]InboxItem, error) {
			if req.TenantID <= 0 || req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID <= 0 || req.PrincipalID != authn.PrincipalIDFromContext(ctx) {
				return nil, ErrForbidden
			}
			if req.Kind != kind || req.ProjectID < 0 || req.Limit < 1 || req.Limit > 101 {
				return nil, ErrInvalidArgument
			}
			// Resolution occurs at read time, after the services registry is sealed.
			source, err := module.Resolve[fleet.EnsembleDecisionFacts](s)
			if err != nil || source.PendingOn == nil || source.EligibleOn == nil {
				return nil, ErrUnavailable
			}
			db, err := m.writer()
			if err != nil {
				return nil, err
			}
			out := []InboxItem{}
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				rows, err := source.PendingOn(ctx, tx, req.TenantID, req.ProjectID)
				if err != nil {
					return err
				}
				for _, r := range rows {
					if r.Kind != kind {
						continue
					}
					ok, err := source.EligibleOn(ctx, tx, req.TenantID, r.ID, req.PrincipalID)
					if err != nil {
						return err
					}
					if !ok {
						continue
					}
					if kind == "ENSEMBLE_SELECTION" {
						lead, err := m.projectLeadOn(ctx, tx, req.TenantID, r.ProjectID, req.PrincipalID)
						if err != nil {
							return err
						}
						if !lead {
							continue
						}
					}
					item := InboxItem{InboxKey: InboxKey{Kind: kind, ID: r.ID, Priority: "NORMAL", CreatedAt: r.CreatedAt}, ProjectID: r.ProjectID, Title: kind, ObjectType: "work_item", ObjectID: r.WorkItemID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
					if kind == "ENSEMBLE_SELECTION" {
						item.Priority = "HIGH"
					}
					if req.After == nil || CompareInboxKeys(item.InboxKey, *req.After) > 0 {
						out = append(out, item)
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			slices.SortFunc(out, func(a, b InboxItem) int { return CompareInboxKeys(a.InboxKey, b.InboxKey) })
			if len(out) > req.Limit {
				out = out[:req.Limit]
			}
			return out, nil
		}}); err != nil {
			return err
		}
	}
	return module.Provide[AndonAuthority](s, m.andonAuthorityOn)
}

func (m *Module) andonAuthorityOn(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error) {
	var out AuthorityDecision
	if tx == nil || tx.Statement == nil || req.TenantID <= 0 || req.ProjectID <= 0 || req.PrincipalID <= 0 || req.Resource.ID <= 0 || (req.Resource.Kind != "work_item" && req.Resource.Kind != "project") || (req.Code != "andon_pull" && req.Code != "andon_clear") || req.Spawn != nil || req.SystemJob != "" {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	if (req.ActorKind != "HUMAN" && req.ActorKind != "AGENT") || (req.Code == "andon_clear" && req.ActorKind != "HUMAN") {
		out.ReasonCode = "ACTOR_NOT_ELIGIBLE"
		return out, nil
	}
	if req.Environment != "" && req.Environment != "DEFAULT" {
		return out, ErrUnavailable
	}
	if m.seed == nil || m.seed.registry == nil || m.explain == nil {
		return out, ErrUnavailable
	}
	if _, known := m.seed.registry.Find(req.Code); !known {
		return out, ErrUnavailable
	}
	x, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: req.TenantID, EntityID: req.ProjectID, PrincipalID: req.PrincipalID, Permission: req.Code})
	if err != nil {
		return out, err
	}
	out.PolicyRevision = x.Revision
	if !x.Allowed || x.Reason == "explicit_deny" || x.Reason == "administrator_scope_denied" || x.Reason == "inactive_membership" || x.Reason == "inactive_tenant" {
		out.ReasonCode = "NO_CURRENT_GRANT"
		return out, nil
	}
	source, err := m.seed.roleAuthoritySource(ctx, tx, req.TenantID, req.Code, x)
	if err != nil {
		return out, err
	}
	if source.source == "" {
		out.ReasonCode = "NO_DIRECT_AUTHORITY"
		return out, nil
	}
	out.Allowed, out.AuthoritySource = true, source.source
	return out, nil
}

// ProjectModeGovernance keeps governance-owned facts and revocations inside the
// mode command's transaction. PrincipalsOn discovers a lock set, never authority.
type ProjectModeGovernance struct {
	PrincipalsOn    func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	AuthorityOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (string, error)
	AgentRoleOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error)
	PreconditionsOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (bool, []string, error)
	RevokeOn        func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) ([]guid.ID, error)
	RevokeSpawnOn   func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) ([]guid.ID, error)
}

func (m *Module) modeDelegationPrincipalsOn(ctx context.Context, db *gorm.DB, tenant, project guid.ID) ([]guid.ID, error) {
	if m == nil || m.seed == nil || db == nil {
		return nil, ErrUnavailable
	}
	q := query.BindDelegations(db, m.seed.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("ACTIVE")).Order(q.GranteePrincipalID).Find()
	if err != nil {
		return nil, err
	}
	ids := []guid.ID{}
	for _, r := range rows {
		ids = append(ids, r.GranteePrincipalID)
	}
	grants, err := m.modeScopedSpawnGrantsOn(ctx, db, tenant, project)
	if err != nil {
		return nil, err
	}
	for _, r := range grants {
		ids = append(ids, r.GranteePrincipalID)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (m *Module) projectModeAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) (string, error) {
	if permission != "project_mode_change" && permission != "project_takeover" {
		return "", ErrInvalidArgument
	}
	if err := m.delegationAuthorityOn(ctx, tx, tenant, project, permission); err != nil {
		return "", err
	}
	x, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: authn.PrincipalIDFromContext(ctx), Permission: permission})
	if err != nil {
		return "", err
	}
	a, err := m.seed.roleAuthoritySource(ctx, tx, tenant, permission, x)
	if err != nil {
		return "", err
	}
	if a.source == "" {
		return "", ErrForbidden
	}
	return a.source, nil
}

// Read permission carries the concrete project role identity without requiring
// delegated approval to be usable before the project enters autonomous mode.
func (m *Module) modeAgentRoleOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID, role string) (bool, error) {
	if role != "agent_executor" && role != "agent_reviewer" {
		return false, ErrInvalidArgument
	}
	if m == nil || m.explain == nil || m.delegations.agents.ByPrincipalOn == nil || m.delegations.members == nil {
		return false, ErrUnavailable
	}
	member, err := m.delegations.members.IsMemberActiveOn(ctx, tx, tenant, principal)
	if err != nil || !member {
		return false, err
	}
	agent, found, err := m.delegations.agents.ByPrincipalOn(ctx, tx, tenant, principal)
	if err != nil || !found {
		return false, err
	}
	if agent.ID <= 0 || agent.TenantID != tenant || agent.PrincipalID != principal || agent.State != "ACTIVE" {
		return false, nil
	}
	roleID, err := m.roleIDOn(ctx, tx, tenant, role)
	if err != nil {
		return false, err
	}
	x, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: principal, Permission: "work_item_read"})
	if err != nil || !x.Allowed {
		return false, err
	}
	for _, match := range x.Matches {
		if match.RoleID == roleID.String() && match.Effect == "allow" && match.SourceType == "entity_binding" && match.ScopeType == "entity" && match.ScopeID == project.String() && !match.Inherited {
			return true, nil
		}
	}
	return false, nil
}

func (m *Module) modeGovernancePreconditionsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, locked []guid.ID) (bool, []string, error) {
	if m == nil || m.seed == nil || tx == nil {
		return false, nil, ErrUnavailable
	}
	q := query.BindDelegations(tx, m.seed.names)
	now := m.seed.now()
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("ACTIVE"), q.ActivatedAt.IsNotNull(), q.StartsAt.Lte(now), q.EndsAt.Gte(now.Add(24*time.Hour))).Order(q.ID).Find()
	if err != nil {
		return false, nil, err
	}
	ready := false
	for _, r := range rows {
		if !slices.Contains(locked, r.GranteePrincipalID) {
			return false, nil, ErrVersion
		}
		var c DelegationConditions
		if json.Unmarshal([]byte(r.ConditionsJSON), &c) != nil {
			return false, nil, ErrUnavailable
		}
		if !slices.Contains(c.Actions, "decision_approve") {
			continue
		}
		ok, err := m.modeAgentRoleOn(ctx, tx, tenant, project, r.GranteePrincipalID, "agent_reviewer")
		if err != nil {
			return false, nil, err
		}
		if ok {
			ready = true
		}
	}
	rules, err := m.readEffectiveRulesOn(ctx, tx.Clauses(clause.Locking{Strength: "SHARE"}), tenant, project)
	if err != nil {
		return false, nil, err
	}
	missing := []string{}
	for _, kind := range []string{"BUDGET", "VERIFICATION_INCONCLUSIVE", "CLIENT_REJECTION", "REWORK_LIMIT"} {
		if !slices.ContainsFunc(rules, func(r EscalationRuleView) bool { return r.Kind == kind && r.Enabled }) {
			missing = append(missing, kind)
		}
	}
	return ready, missing, nil
}

func (m *Module) revokeModeDelegationsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission, reason string, locked []guid.ID) ([]guid.ID, error) {
	source, err := m.projectModeAuthorityOn(ctx, tx, tenant, project, permission)
	if err != nil {
		return nil, err
	}
	q := query.BindDelegations(tx, m.seed.names)
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("ACTIVE")).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	ids := []guid.ID{}
	endReason := "MODE_CHANGED"
	if permission == "project_takeover" {
		endReason = "TAKEOVER"
	}
	for _, r := range rows {
		if !slices.Contains(locked, r.GranteePrincipalID) {
			return nil, ErrVersion
		}
		if err := m.endDelegationRowOn(ctx, tx, r, "REVOKED", endReason, reason, source); err != nil {
			return nil, err
		}
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// Team-wide grants remain intact. A scoped grant covering several projects is
// revoked as a whole when any covered project is taken over (spec08 BR-08-57).
func (m *Module) modeScopedSpawnGrantsOn(ctx context.Context, db *gorm.DB, tenant, project guid.ID) ([]*model.SpawnGrant, error) {
	if m == nil || m.seed == nil || db == nil {
		return nil, ErrUnavailable
	}
	q := query.BindSpawnGrants(db, m.seed.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("ACTIVE", "EXHAUSTED")).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	out := []*model.SpawnGrant{}
	for _, r := range rows {
		var scope SpawnGrantScope
		if json.Unmarshal([]byte(r.ScopeJSON), &scope) != nil || !validSpawnGrantScope(scope) {
			return nil, ErrUnavailable
		}
		if slices.Contains(scope.ProjectIDs, project) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *Module) revokeModeSpawnGrantsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission, reason string, locked []guid.ID) ([]guid.ID, error) {
	source, err := m.projectModeAuthorityOn(ctx, tx, tenant, project, permission)
	if err != nil {
		return nil, err
	}
	rows, err := m.modeScopedSpawnGrantsOn(ctx, tx, tenant, project)
	if err != nil {
		return nil, err
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	ids := []guid.ID{}
	for _, r := range rows {
		if !slices.Contains(locked, r.GranteePrincipalID) {
			return nil, ErrVersion
		}
		current, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(r.ID), q.Version.Eq(r.Version)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVersion
		}
		if err != nil {
			return nil, err
		}
		if err = m.endSpawnGrantOn(ctx, tx, current, "REVOKED", "TAKEOVER", reason, authn.PrincipalIDFromContext(ctx), source); err != nil {
			return nil, err
		}
		ids = append(ids, r.ID)
	}
	return ids, nil
}

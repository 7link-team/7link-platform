package governance

import (
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
)

// checkDelegationAuthority evaluates persisted domain records and current IAM
// carriers together. Work decisions require a persisted escalation receipt for
// the exact claim; no caller flag can invent it. Intake needs no claim receipt.
func (m *Module) checkDelegationAuthority(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error) {
	result := AuthorityDecision{Steps: []AuthorityStep{{Step: "S0", Outcome: "PASS"}}}
	deny := func(step, reason string) (AuthorityDecision, error) {
		result.ReasonCode = reason
		result.Steps = append(result.Steps, AuthorityStep{Step: step, Outcome: "DENY"})
		return result, nil
	}
	kind := "work_item"
	if req.Code == "intake_convert" {
		kind = "intake"
	}
	if req.ActorKind != "AGENT" || !RulesForAction(req.Code, "AGENT").Delegable || req.ProjectID <= 0 || req.Resource.ID <= 0 || req.Resource.Kind != kind || req.Facts == nil || req.Spawn != nil {
		return deny("S0", "INVALID_REQUEST")
	}
	if req.Environment == "PRODUCTION" {
		return AuthorityDecision{}, ErrUnavailable
	}
	if m.explain == nil || m.delegations.agents.ByPrincipalOn == nil || m.delegations.members == nil || m.principals.LockActiveRow == nil || m.delegations.projects.InspectOn == nil {
		return AuthorityDecision{}, ErrUnavailable
	}
	if tx == nil {
		db, e := m.writer()
		if e != nil {
			return AuthorityDecision{}, e
		}
		var out AuthorityDecision
		e = db.WithContext(ctx).Transaction(func(work *gorm.DB) error { var e error; out, e = m.checkDelegationAuthority(ctx, work, req); return e })
		return out, e
	}
	if tx.Statement == nil {
		return AuthorityDecision{}, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return AuthorityDecision{}, ErrInvalidArgument
	}
	if _, active, e := m.principals.LockActiveRow(ctx, tx, req.PrincipalID); e != nil {
		return AuthorityDecision{}, e
	} else if !active {
		return deny("S1", "PRINCIPAL_INACTIVE")
	}
	if active, e := m.delegations.members.IsMemberActiveOn(ctx, tx, req.TenantID, req.PrincipalID); e != nil {
		return AuthorityDecision{}, e
	} else if !active {
		return deny("S1", "PRINCIPAL_INACTIVE")
	}
	agent, found, e := m.delegations.agents.ByPrincipalOn(ctx, tx, req.TenantID, req.PrincipalID)
	if e != nil {
		return AuthorityDecision{}, e
	}
	if !found || agent.State != "ACTIVE" || agent.TenantID != req.TenantID || agent.PrincipalID != req.PrincipalID || (agent.Origin == "AGENT_SPAWNED" && agent.AdoptedAt == nil) {
		return deny("S1", "PRINCIPAL_INACTIVE")
	}
	if req.Code != "intake_convert" && !slices.Contains(agent.Roles, "REVIEWER") {
		return deny("S2", "ACTOR_NOT_ELIGIBLE")
	}
	if req.Code == "intake_convert" && !slices.Contains(agent.Roles, "COORDINATOR") && !slices.Contains(agent.Roles, "REVIEWER") {
		return deny("S2", "ACTOR_NOT_ELIGIBLE")
	}
	if req.Facts.SelfProduced || (req.Code == "intake_convert" && req.Facts.TriageCardAuthorID == req.PrincipalID.String()) {
		return deny("S3", "SELF_REVIEW")
	}
	if req.Code == "intake_convert" && req.Facts.TriageCardAuthorID == "" {
		return deny("S0", "INVALID_REQUEST")
	}
	x, e := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: req.TenantID, EntityID: req.ProjectID, PrincipalID: req.PrincipalID, Permission: req.Code})
	if e != nil {
		return AuthorityDecision{}, e
	}
	result.PolicyRevision = x.Revision
	if x.Reason == "explicit_deny" || x.Reason == "administrator_scope_denied" {
		return deny("S4", "EXPLICIT_DENY")
	}
	if !x.Allowed {
		return deny("S4", "NO_ROLE_GRANT")
	}
	source, e := m.seed.roleAuthoritySource(ctx, tx, req.TenantID, req.Code, x)
	if e != nil {
		return AuthorityDecision{}, e
	}
	p, e := m.delegations.projects.InspectOn(ctx, tx, req.TenantID, req.ProjectID)
	if e != nil {
		return AuthorityDecision{}, e
	}
	q := query.BindDelegations(tx, m.seed.names)
	now := m.seed.now()
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where(q.TenantID.Eq(req.TenantID), q.ProjectID.Eq(req.ProjectID), q.GranteePrincipalID.Eq(req.PrincipalID), q.GranteeAgentID.Eq(agent.ID), q.State.Eq("ACTIVE"), q.ActivatedAt.IsNotNull(), q.StartsAt.Lte(now), q.EndsAt.Gt(now)).Order(q.EndsAt, q.ID).Find()
	if e != nil {
		return AuthorityDecision{}, e
	}
	reason := "NO_DELEGATION"
	for _, row := range rows {
		var c DelegationConditions
		var bindings []DelegationBinding
		if json.Unmarshal([]byte(row.ConditionsJSON), &c) != nil || json.Unmarshal([]byte(row.IAMBindings), &bindings) != nil {
			return AuthorityDecision{}, ErrUnavailable
		}
		if !slices.Contains(c.Actions, req.Code) || !delegationCarrierMatches(source.carriers, bindings, req) {
			continue
		}
		guards := DelegationGuards{ProjectAllowed: p.DelegatedAllowed, Autonomous: p.Mode == "AUTONOMOUS"}
		if req.Code != "intake_convert" {
			guards.ClaimEvaluated, e = m.claimEvaluatedOn(ctx, tx, req)
			if e != nil {
				return AuthorityDecision{}, e
			}
		}
		incidents := query.BindEscalationIncidents(tx, m.seed.names)
		open, e := incidents.WithContext(ctx).Where(incidents.TenantID.Eq(req.TenantID), incidents.ProjectID.Eq(req.ProjectID), incidents.State.In("OPEN", "ACKNOWLEDGED")).Find()
		if e != nil {
			return AuthorityDecision{}, e
		}
		rules, e := m.readEffectiveRulesOn(ctx, tx, req.TenantID, req.ProjectID)
		if e != nil {
			return AuthorityDecision{}, e
		}
		for _, incident := range open {
			if incident.SubjectKind != "PROJECT" && (incident.SubjectKind != "WORK_ITEM" || incident.SubjectID != req.Resource.ID) {
				continue
			}
			for _, rule := range rules {
				if rule.Kind != incident.Kind {
					continue
				}
				actions := rule.ActionsHybrid
				if guards.Autonomous {
					actions = rule.ActionsAutonomous
				}
				if slices.Contains(actions, "HOLD_DELEGATIONS") {
					guards.Hold = true
				}
			}
		}
		current := delegationConditionReason(c, req.Code, req.Facts, guards)
		if current != "" {
			reason = current
			continue
		}
		result.Allowed = true
		result.DelegationID = row.ID
		result.AuthoritySource = "DELEGATION:" + row.ID.String()
		result.Steps = append(result.Steps, AuthorityStep{Step: "S6", Outcome: "MATCH"})
		return result, nil
	}
	return deny("S6", reason)
}

func delegationCarrierMatches(matches []authz.DecisionMatch, bindings []DelegationBinding, req AuthorityRequest) bool {
	for _, b := range bindings {
		if b.RoleKey != "delegated_"+req.Code {
			continue
		}
		for _, match := range matches {
			if match.RoleID == b.RoleID.String() && match.PermissionCode == req.Code && match.Effect == "allow" && match.SourceType == "entity_binding" && match.ScopeType == "entity" && match.ScopeID == req.ProjectID.String() {
				return true
			}
		}
	}
	return false
}

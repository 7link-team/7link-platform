package governance

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// DelegationNotificationFact identifies the actual grantor of a persisted end.
// It carries no authority and exposes no delegation conditions or free text.
type DelegationNotificationFact struct {
	ProjectID, GrantorID guid.ID
	State, EndReason     string
	EndsAt               time.Time
}

type DelegationNotificationFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationNotificationFact, bool, error)

func (m *Module) delegationNotificationFacts(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (DelegationNotificationFact, bool, error) {
	var out DelegationNotificationFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalidArgument
	}
	q := query.BindDelegations(tx, m.seed.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out = DelegationNotificationFact{ProjectID: r.ProjectID, GrantorID: r.GrantorPrincipalID, State: r.State, EndsAt: r.EndsAt}
	if r.EndReason != nil {
		out.EndReason = *r.EndReason
	}
	return out, true, nil
}

// IncidentNotificationFacts exposes routing state only, never incident facts or notes.
type IncidentNotificationFact struct {
	ProjectID guid.ID
	State     string
	CreatedAt time.Time
}
type IncidentNotificationFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) (IncidentNotificationFact, bool, error)

func (m *Module) incidentNotificationFacts(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (IncidentNotificationFact, bool, error) {
	var out IncidentNotificationFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalidArgument
	}
	q := query.BindEscalationIncidents(tx, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return IncidentNotificationFact{ProjectID: row.ProjectID, State: row.State, CreatedAt: row.CreatedAt}, true, nil
}

// SpawnNotificationFact exposes persisted routing/cost facts without proposal text.
type SpawnNotificationFact struct {
	GrantID, GrantorID guid.ID
	State              string
	Cap, Spent         int64
	EndsAt             time.Time
	ProjectIDs         []guid.ID
}
type SpawnNotificationFacts func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (SpawnNotificationFact, bool, error)

func (m *Module) spawnNotificationFacts(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string) (SpawnNotificationFact, bool, error) {
	var out SpawnNotificationFact
	if tx == nil || tenant <= 0 || id <= 0 || (kind != "grant" && kind != "request") {
		return out, false, ErrInvalidArgument
	}
	grantID := id
	var requestState string
	if kind == "request" {
		q := query.BindSpawnRequests(tx, m.seed.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.State.In("REQUESTED", "UNAVAILABLE"), q.ExpiresAt.Gt(m.seed.now())).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		var payload agentSpawnPayload
		if json.Unmarshal([]byte(r.Payload), &payload) != nil {
			return out, false, ErrUnavailable
		}
		grantID = payload.GrantID
		out.ProjectIDs = payload.Facts.ProjectIDs
		requestState = r.State
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(grantID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	var scope SpawnGrantScope
	if json.Unmarshal([]byte(r.ScopeJSON), &scope) != nil || !validSpawnGrantScope(scope) {
		return out, false, ErrUnavailable
	}
	out.GrantID, out.GrantorID, out.State, out.Cap, out.Spent, out.EndsAt = r.ID, r.GrantedBy, r.State, scope.BudgetCapMicros, r.SpentMicros, r.EndsAt
	if kind == "request" {
		if r.State != "ACTIVE" || m.seed.now().Before(r.StartsAt) || !m.seed.now().Before(r.EndsAt) || r.SpentMicros >= scope.BudgetCapMicros || scope.ApprovalMode != "REQUIRE_APPROVAL" {
			return out, false, nil
		}
		if m.spawnDirectory.AgentOn == nil {
			return out, false, ErrUnavailable
		}
		a, found, err := m.spawnDirectory.AgentOn(ctx, tx, tenant, r.GranteeAgentID)
		if err != nil || !found || a.State != "ACTIVE" || a.PrincipalID != r.GranteePrincipalID {
			return out, false, err
		}
		out.State = requestState
	}
	return out, true, nil
}

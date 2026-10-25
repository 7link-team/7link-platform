package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

var (
	ErrFullAutoForbidden        = errors.New("execution: FULL_AUTO_FORBIDDEN")
	ErrReauthenticationRequired = errors.New("execution: REAUTH_REQUIRED")
)

// FullAutoGrant is immutable evidence of a Human's approval of this exact plan
// and attempt. A runner cannot create this evidence through its receipt API.
type FullAutoGrant struct {
	ID            guid.ID   `json:"id"`
	ActorID       guid.ID   `json:"actor_id"`
	ExecutionID   guid.ID   `json:"execution_id"`
	ProjectID     guid.ID   `json:"project_id"`
	PlanHash      string    `json:"plan_hash"`
	DispatchEpoch int64     `json:"dispatch_epoch"`
	Purpose       string    `json:"purpose"`
	Reason        string    `json:"reason"`
	GrantedAt     time.Time `json:"granted_at"`
}

type ExecutionProjectPolicy struct {
	AllowFullAuto bool `json:"allow_full_auto"`
	Version       int  `json:"version"`
}

func (m *Module) fullAutoPolicyOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (ExecutionProjectPolicy, error) {
	q := query.BindProjectPolicies(tx, m.names)
	p, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ExecutionProjectPolicy{AllowFullAuto: true}, nil
	}
	if err != nil {
		return ExecutionProjectPolicy{}, err
	}
	return ExecutionProjectPolicy{AllowFullAuto: p.AllowFullAuto, Version: p.Version}, nil
}

func (m *Module) fullAutoPrincipalLocks(ctx context.Context, tx *gorm.DB, row *model.Execution) error {
	if m.machines.ByIDOn == nil {
		return ErrUnavailable
	}
	machine, err := m.machines.ByIDOn(ctx, tx, row.TenantID, row.MachineID)
	if err != nil {
		return err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	return m.workPrincipalLocks(ctx, tx, link(row, actor), actor, row.InitiatorID, machine.PrincipalID)
}

func (m *Module) fullAutoExecutionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, permission string) (*model.Execution, error) {
	if err := transaction(tx, tenant, id); err != nil {
		return nil, err
	}
	snap, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return nil, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if err = m.fullAutoPrincipalLocks(ctx, tx, snap); err != nil {
		return nil, err
	}
	if err = m.humanAuthority(ctx, tx, tenant, snap.ProjectID, permission); err != nil {
		return nil, err
	}
	if m.work.AuthorizeOn == nil {
		return nil, ErrUnavailable
	}
	if err = m.work.AuthorizeOn(ctx, tx, link(snap, actor), "RECEIPT"); err != nil {
		return nil, err
	}
	return m.rowOn(ctx, tx, tenant, id, true)
}

// PutFullAutoPolicyOn runs under the current project's lock acquired by the
// work adapter. Version zero denotes the documented default allow=true policy.
func (m *Module) PutFullAutoPolicyOn(ctx context.Context, tx *gorm.DB, tenant, executionID guid.ID, expected int, allow bool, reason string) (ExecutionProjectPolicy, error) {
	if !bounded(strings.TrimSpace(reason), 2000) || expected < 0 {
		return ExecutionProjectPolicy{}, ErrInvalidArgument
	}
	row, err := m.fullAutoExecutionOn(ctx, tx, tenant, executionID, "project_manage")
	if err != nil {
		return ExecutionProjectPolicy{}, err
	}
	prior, err := m.fullAutoPolicyOn(ctx, tx, tenant, row.ProjectID)
	if err != nil {
		return ExecutionProjectPolicy{}, err
	}
	if prior.Version != expected {
		return ExecutionProjectPolicy{}, ErrConflict
	}
	q := query.BindProjectPolicies(tx, m.names)
	now := time.Now().UTC().Truncate(time.Millisecond)
	if expected == 0 {
		err = q.WithContext(ctx).Create(&model.ProjectExecutionPolicy{TenantID: tenant, ProjectID: row.ProjectID, AllowFullAuto: allow, Version: 1, ActorID: authn.PrincipalIDFromContext(ctx), Reason: reason, UpdatedAt: now})
	} else {
		info, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(row.ProjectID), q.Version.Eq(expected)).Updates(map[string]any{"allow_full_auto": allow, "version": expected + 1, "actor_id": authn.PrincipalIDFromContext(ctx), "reason": reason, "updated_at": now})
		err = e
		if err == nil && info.RowsAffected != 1 {
			err = ErrConflict
		}
	}
	if err != nil {
		return ExecutionProjectPolicy{}, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "HUMAN", "permission:project_manage", map[string]any{"policy": "allow_full_auto", "allow_full_auto": allow, "policy_version": expected + 1, "reason": reason}); err != nil {
		return ExecutionProjectPolicy{}, err
	}
	return ExecutionProjectPolicy{AllowFullAuto: allow, Version: expected + 1}, nil
}

func (m *Module) recordedFullAutoGrantOn(ctx context.Context, tx *gorm.DB, row *model.Execution) (*FullAutoGrant, error) {
	q := query.BindEvidence(tx, m.names)
	e, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ExecutionID.Eq(row.ID), q.Kind.Eq("FULL_AUTO_GRANT"), q.Title.Eq("FULL_AUTO:"+row.PlanHash+":"+strconv.FormatInt(row.DispatchEpoch, 10))).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrApprovalUnavailable
	}
	if err != nil {
		return nil, err
	}
	var grant FullAutoGrant
	if json.Unmarshal([]byte(e.Content), &grant) != nil || hash([]byte(e.Content)) != e.ContentHash || grant.ID != e.ID || grant.ActorID <= 0 || grant.ExecutionID != row.ID || grant.ProjectID != row.ProjectID || grant.PlanHash != row.PlanHash || grant.DispatchEpoch != row.DispatchEpoch || grant.Purpose != "execution_full_auto" {
		return nil, ErrApprovalUnavailable
	}
	return &grant, nil
}

func (m *Module) GrantFullAutoOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, cookie, proof, reason string) (FullAutoGrant, error) {
	if !bounded(strings.TrimSpace(reason), 2000) {
		return FullAutoGrant{}, ErrInvalidArgument
	}
	if err := transaction(tx, tenant, id); err != nil {
		return FullAutoGrant{}, err
	}
	snap, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return FullAutoGrant{}, err
	}
	if err = m.fullAutoPrincipalLocks(ctx, tx, snap); err != nil {
		return FullAutoGrant{}, err
	}
	prior, priorErr := m.recordedFullAutoGrantOn(ctx, tx, snap)
	if priorErr != nil && !errors.Is(priorErr, ErrApprovalUnavailable) {
		return FullAutoGrant{}, priorErr
	}
	if prior == nil {
		if m.reauth == nil || proof == "" {
			return FullAutoGrant{}, ErrReauthenticationRequired
		}
		// Principal locks precede business locks. Canonical consumption enforces
		// browser binding, exact purpose/target, <=5m freshness and single use.
		if err := m.reauth(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, proof, "execution_full_auto", "execution:"+id.String()); err != nil {
			return FullAutoGrant{}, ErrReauthenticationRequired
		}
	}

	row, err := m.fullAutoExecutionOn(ctx, tx, tenant, id, "execution_start")
	if err != nil {
		return FullAutoGrant{}, err
	}
	v, err := view(row)
	if err != nil {
		return FullAutoGrant{}, err
	}
	if v.Plan.AuthorizationMode != "FULL_AUTO" {
		return FullAutoGrant{}, ErrConflict
	}
	p, err := m.fullAutoPolicyOn(ctx, tx, tenant, row.ProjectID)
	if err != nil {
		return FullAutoGrant{}, err
	}
	if !p.AllowFullAuto {
		return FullAutoGrant{}, ErrFullAutoForbidden
	}
	if prior != nil {
		if prior.ActorID != authn.PrincipalIDFromContext(ctx) || prior.Reason != reason {
			return FullAutoGrant{}, ErrConflict
		}
		return *prior, nil
	}
	if row.State != "CREATED" || row.LeaseExpiresAt != nil || row.CancelRequestedAt != nil {
		return FullAutoGrant{}, ErrConflict
	}
	key, err := m.nextID()
	if err != nil {
		return FullAutoGrant{}, err
	}
	g := FullAutoGrant{ID: key, ActorID: authn.PrincipalIDFromContext(ctx), ExecutionID: id, ProjectID: row.ProjectID, PlanHash: row.PlanHash, DispatchEpoch: row.DispatchEpoch, Purpose: "execution_full_auto", Reason: reason, GrantedAt: time.Now().UTC().Truncate(time.Millisecond)}
	b, _ := json.Marshal(g)
	if err = query.BindEvidence(tx, m.names).WithContext(ctx).Create(&model.Evidence{ID: key, TenantID: tenant, ProjectID: row.ProjectID, ExecutionID: id, Kind: "FULL_AUTO_GRANT", Title: "FULL_AUTO:" + row.PlanHash + ":" + strconv.FormatInt(row.DispatchEpoch, 10), Content: string(b), ContentHash: hash(b), CreatedAt: g.GrantedAt}); err != nil {
		return FullAutoGrant{}, err
	}
	q := query.BindExecutions(tx, m.names)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"state_reason": "", "version": row.Version + 1, "updated_at": g.GrantedAt}); err != nil {
		return FullAutoGrant{}, err
	}
	row.Version++
	if err = m.eventOn(ctx, tx, row, "execution.full_auto_enabled", "HUMAN", "permission:execution_start", map[string]any{"grant_id": key.String(), "plan_hash": row.PlanHash, "dispatch_epoch": row.DispatchEpoch, "purpose": g.Purpose, "mode": "FULL_AUTO", "reason": reason}); err != nil {
		return FullAutoGrant{}, err
	}
	return g, nil
}

// Recheck policy at dispatch; a recorded approval does not override a later
// project ban. The evidence is attempt-bound, so recovery needs a fresh grant.
func (m *Module) authorizeFullAutoDispatchOn(ctx context.Context, tx *gorm.DB, row *model.Execution) (*FullAutoGrant, error) {
	p, err := m.fullAutoPolicyOn(ctx, tx, row.TenantID, row.ProjectID)
	if err != nil {
		return nil, err
	}
	if !p.AllowFullAuto {
		return nil, ErrFullAutoForbidden
	}
	grant, err := m.recordedFullAutoGrantOn(ctx, tx, row)
	if err != nil {
		return nil, err
	}
	if m.principals.ActiveHumanByID == nil || m.policy == nil {
		return nil, ErrUnavailable
	}
	_, active, err := m.principals.ActiveHumanByID(ctx, tx, grant.ActorID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrForbidden
	}
	c, err := m.policy(ctx, tx, row.TenantID, row.ProjectID, "execution_start", grant.ActorID)
	if err != nil {
		return nil, err
	}
	if !c.AllowsEntityRows(row.ProjectID) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, row.ProjectID.String()) && !slices.Contains(c.OwnerIDs, grant.ActorID.String())) {
		return nil, ErrForbidden
	}
	return grant, nil
}

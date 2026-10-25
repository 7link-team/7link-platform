package governance

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrVersion = errors.New("governance: version conflict")
var ErrForbidden = errors.New("governance: forbidden")
var ErrNotFound = errors.New("governance: not found")

func fmtVersion(v int) string { return strconv.Itoa(v) }

type SettingsValue struct {
	DelegationMaxDays              int    `json:"delegation_max_days" minimum:"1" maximum:"30"`
	DelegationDefaultDays          int    `json:"delegation_default_days" minimum:"1" maximum:"30"`
	DelegationMaxCostCapMicros     int64  `json:"delegation_max_cost_cap_micros" minimum:"1"`
	DelegationDefaultCostCapMicros int64  `json:"delegation_default_cost_cap_micros" minimum:"1"`
	SODHumanSelfApproval           string `json:"sod_human_self_approval" enum:"DENY,ALLOW_WITH_REASON"`
	DefaultBudgetNotifyPct         int    `json:"default_budget_notify_pct" minimum:"50" maximum:"95"`
	ProductionAccessMaxHours       int    `json:"production_access_max_hours" minimum:"1" maximum:"24"`
	InboxReviewRouting             string `json:"inbox_review_routing" enum:"REVIEWER_ELSE_ELIGIBLE,ALL_ELIGIBLE"`
}

type SettingsView struct {
	SettingsValue
	Version int `json:"version"`
}

// SettingsReader reads actual persisted settings on the consumer transaction.
// Defaults are explicit and do not create a record as a side effect of reading.
type SettingsReader func(context.Context, *gorm.DB, guid.ID) (SettingsView, error)

func defaultSettings() SettingsView {
	return SettingsView{SettingsValue: SettingsValue{DelegationMaxDays: 7, DelegationDefaultDays: 3, DelegationMaxCostCapMicros: 50000000, DelegationDefaultCostCapMicros: 5000000, SODHumanSelfApproval: "DENY", DefaultBudgetNotifyPct: 80, ProductionAccessMaxHours: 8, InboxReviewRouting: "REVIEWER_ELSE_ELIGIBLE"}}
}

func validSettings(v SettingsValue) bool {
	return v.DelegationMaxDays >= 1 && v.DelegationMaxDays <= 30 && v.DelegationDefaultDays >= 1 && v.DelegationDefaultDays <= v.DelegationMaxDays && v.DelegationMaxCostCapMicros >= 1 && v.DelegationDefaultCostCapMicros >= 1 && v.DelegationDefaultCostCapMicros <= v.DelegationMaxCostCapMicros && (v.SODHumanSelfApproval == "DENY" || v.SODHumanSelfApproval == "ALLOW_WITH_REASON") && v.DefaultBudgetNotifyPct >= 50 && v.DefaultBudgetNotifyPct <= 95 && v.ProductionAccessMaxHours >= 1 && v.ProductionAccessMaxHours <= 24 && (v.InboxReviewRouting == "REVIEWER_ELSE_ELIGIBLE" || v.InboxReviewRouting == "ALL_ELIGIBLE")
}

func (m *Module) writer() (*gorm.DB, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, ErrUnavailable
	}
	return m.runtime.Database().Write(), nil
}

func (m *Module) readSettingsOn(ctx context.Context, db *gorm.DB, tenant guid.ID) (SettingsView, error) {
	if m == nil || m.seed == nil || db == nil {
		return SettingsView{}, ErrUnavailable
	}
	if tenant <= 0 {
		return SettingsView{}, ErrInvalidArgument
	}
	q := query.BindSettings(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaultSettings(), nil
	}
	if err != nil {
		return SettingsView{}, err
	}
	var value SettingsValue
	if json.Unmarshal([]byte(row.ValueJSON), &value) != nil || !validSettings(value) {
		return SettingsView{}, ErrUnavailable
	}
	return SettingsView{SettingsValue: value, Version: row.Version}, nil
}

// authorizeHumanOn rechecks the current Human and tenant grant under the
// principal lock in the same transaction as every governance mutation.
func (m *Module) authorizeHumanOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, permission string) error {
	if m == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.explain == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if tx == nil || tx.Statement == nil {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return err
	}
	_, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	explanation, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, PrincipalID: actor, Permission: permission})
	if err != nil {
		return err
	}
	if !explanation.Allowed || explanation.Reason == "explicit_deny" || explanation.Reason == "administrator_scope_denied" {
		return ErrForbidden
	}
	return nil
}

func (m *Module) governanceEventOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int, kind, target string, detail map[string]any) error {
	if m == nil || m.seed == nil || m.seed.audit == nil || m.publisher == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if err := m.seed.audit(ctx, tx, audit.DomainEvent{TenantID: tenant, PrincipalID: authn.ActorIDFromContext(ctx), EventType: kind, TargetType: target, TargetID: id, Detail: detail}); err != nil {
		return err
	}
	_, err := m.publisher.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: kind, Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "ROLE"}, Subject: events.Subject{Kind: target, ID: id}, SubjectVersion: int64(version), Data: detail})
	return err
}

func (m *Module) putSettingsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, expected int, value SettingsValue, cookie, proof string) (SettingsView, error) {
	if m == nil || m.seed == nil || m.seed.ids == nil {
		return SettingsView{}, ErrUnavailable
	}
	if !validSettings(value) || expected < 0 {
		return SettingsView{}, ErrInvalidArgument
	}
	if err := m.authorizeHumanOn(ctx, tx, tenant, "policy_manage"); err != nil {
		return SettingsView{}, err
	}
	q := query.BindSettings(tx, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return SettingsView{}, err
	}
	old := defaultSettings()
	if row != nil {
		old, err = m.readSettingsOn(ctx, tx, tenant)
		if err != nil {
			return SettingsView{}, err
		}
	}
	if old.Version != expected {
		return SettingsView{}, ErrVersion
	}
	if value.SODHumanSelfApproval == "ALLOW_WITH_REASON" && old.SODHumanSelfApproval != "ALLOW_WITH_REASON" {
		if m.reauth == nil {
			return SettingsView{}, ErrUnavailable
		}
		if err := m.reauth(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, proof, "governance_sod_relax", tenant.String()+":governance-settings:"+fmtVersion(expected)); err != nil {
			return SettingsView{}, ErrForbidden
		}
	}
	if old.SettingsValue == value {
		return old, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return SettingsView{}, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if row == nil {
		id, err := m.seed.ids.Next()
		if err != nil {
			return SettingsView{}, err
		}
		if id <= 0 {
			return SettingsView{}, ErrUnavailable
		}
		row = &model.Settings{ID: guid.ID(id), TenantID: tenant, ValueJSON: string(encoded), Version: 1, CreatedAt: now, UpdatedAt: now}
		if err = q.WithContext(ctx).Create(row); err != nil {
			return SettingsView{}, err
		}
	} else {
		result, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.Version.Eq(expected)).Updates(map[string]any{"value_json": string(encoded), "version": expected + 1, "updated_at": now})
		if err != nil {
			return SettingsView{}, err
		}
		if result.RowsAffected != 1 {
			return SettingsView{}, ErrVersion
		}
		row.Version = expected + 1
	}
	if err := m.governanceEventOn(ctx, tx, tenant, 0, row.ID, row.Version, "governance.settings_updated", "governance_settings", map[string]any{"version": row.Version, "before": old.SettingsValue, "after": value}); err != nil {
		return SettingsView{}, err
	}
	return SettingsView{SettingsValue: value, Version: row.Version}, nil
}

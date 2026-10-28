package governance

import (
	governancei18n "7link.com/platform/internal/governance/i18n"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type moduleGovernanceEvents struct{}

func (moduleGovernanceEvents) AddTypes(_ string, specs ...events.TypeSpec) error {
	for _, spec := range specs {
		base := governancei18n.Namespace + ".activity_event_" + strings.ReplaceAll(spec.Name, ".", "_")
		if spec.DisplayNameKey != base+"_name" || spec.DescriptionKey != base+"_description" {
			return fmt.Errorf("event presentation does not describe %s", spec.Name)
		}
	}
	return nil
}
func (moduleGovernanceEvents) AddSubscribers(string, ...events.Subscriber) error { return nil }
func (moduleGovernanceEvents) Publish(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
	return 1, nil
}

type governanceTestPublisher struct{ Fail bool }

func (p *governanceTestPublisher) Publish(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	if p.Fail {
		return 0, errors.New("outbox unavailable")
	}
	data, err := json.Marshal(e)
	if err != nil {
		return 0, err
	}
	return 1, tx.WithContext(ctx).Exec("INSERT INTO event_probe(detail) VALUES(?)", string(data)).Error
}

func settingsHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *governanceTestPublisher) {
	t.Helper()
	s, db, rt := seedHarnessRuntime(t)
	require.NoError(t, db.Exec("CREATE TABLE event_probe(detail TEXT)").Error)
	s.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		return tx.WithContext(ctx).Exec("INSERT INTO audit_probe(detail) VALUES(?)", string(data)).Error
	}
	pub := &governanceTestPublisher{}
	m := &Module{runtime: rt, seed: s, publisher: pub, principals: identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }, ActiveHumanByID: func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, true, nil
	}}, explain: func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true}, nil
	}, reauth: func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 2, SubjectType: authn.SubjectTypePrincipal})
	return m, db, ctx, pub
}

func settingsCommand(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, version int, value SettingsValue) (SettingsView, error) {
	t.Helper()
	var out SettingsView
	err := db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.putSettingsOn(ctx, tx, 1, version, value, "cookie", "proof")
		return e
	})
	return out, err
}

func TestSettingsDefaultsCASRevocationAndTransactionalAuditOutbox(t *testing.T) {
	m, db, ctx, pub := settingsHarness(t)
	initial, err := m.readSettingsOn(ctx, db, 1)
	require.NoError(t, err)
	require.Equal(t, defaultSettings(), initial)
	value := initial.SettingsValue
	value.DefaultBudgetNotifyPct = 85
	created, err := settingsCommand(t, m, db, ctx, 0, value)
	require.NoError(t, err)
	require.Equal(t, 1, created.Version)
	stored, err := m.readSettingsOn(ctx, db, 1)
	require.NoError(t, err)
	require.Equal(t, created, stored)
	_, err = settingsCommand(t, m, db, ctx, 0, value)
	require.ErrorIs(t, err, ErrVersion)
	replayed, err := settingsCommand(t, m, db, ctx, 1, value)
	require.NoError(t, err)
	require.Equal(t, created, replayed)
	var auditCount, eventCount int64
	require.NoError(t, db.Table("audit_probe").Count(&auditCount).Error)
	require.NoError(t, db.Table("event_probe").Count(&eventCount).Error)
	require.EqualValues(t, 1, auditCount)
	require.Equal(t, auditCount, eventCount)
	value.DefaultBudgetNotifyPct = 90
	pub.Fail = true
	_, err = settingsCommand(t, m, db, ctx, 1, value)
	require.Error(t, err)
	stored, err = m.readSettingsOn(ctx, db, 1)
	require.NoError(t, err)
	require.Equal(t, created, stored)
	pub.Fail = false
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: false, Reason: "explicit_deny"}, nil
	}
	_, err = settingsCommand(t, m, db, ctx, 1, value)
	require.ErrorIs(t, err, ErrForbidden)
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true}, nil
	}
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	_, err = settingsCommand(t, m, db, ctx, 1, value)
	require.ErrorIs(t, err, ErrForbidden)
}

func TestSettingsSODRelaxationPurposeAndVersionBound(t *testing.T) {
	m, db, ctx, _ := settingsHarness(t)
	value := defaultSettings().SettingsValue
	value.SODHumanSelfApproval = "ALLOW_WITH_REASON"
	m.reauth = func(_ context.Context, tx *gorm.DB, id guid.ID, cookie, proof, purpose, target string) error {
		require.NotNil(t, tx)
		require.EqualValues(t, 2, id)
		require.Equal(t, "cookie", cookie)
		require.Equal(t, "proof", proof)
		require.Equal(t, "governance_sod_relax", purpose)
		require.Equal(t, "1:governance-settings:0", target)
		return errors.New("expired or wrong target")
	}
	_, err := settingsCommand(t, m, db, ctx, 0, value)
	require.ErrorIs(t, err, ErrForbidden)
	m.reauth = nil
	_, err = settingsCommand(t, m, db, ctx, 0, value)
	require.ErrorIs(t, err, ErrUnavailable)
	m.reauth = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	saved, err := settingsCommand(t, m, db, ctx, 0, value)
	require.NoError(t, err)
	require.Equal(t, "ALLOW_WITH_REASON", saved.SODHumanSelfApproval)
}

func TestSettingsValidationAndUnavailable(t *testing.T) {
	base := defaultSettings().SettingsValue
	for name, change := range map[string]func(*SettingsValue){"max days": func(v *SettingsValue) { v.DelegationMaxDays = 31 }, "default days": func(v *SettingsValue) { v.DelegationDefaultDays = 8 }, "max cost": func(v *SettingsValue) { v.DelegationMaxCostCapMicros = 0 }, "default cost": func(v *SettingsValue) { v.DelegationDefaultCostCapMicros = 50000001 }, "SOD": func(v *SettingsValue) { v.SODHumanSelfApproval = "ALLOW" }, "budget": func(v *SettingsValue) { v.DefaultBudgetNotifyPct = 100 }, "hours": func(v *SettingsValue) { v.ProductionAccessMaxHours = 25 }, "routing": func(v *SettingsValue) { v.InboxReviewRouting = "AUTO" }} {
		t.Run(name, func(t *testing.T) { value := base; change(&value); require.False(t, validSettings(value)) })
	}
	m, db, ctx, _ := settingsHarness(t)
	_, err := settingsCommand(t, m, db, ctx, -1, base)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.readSettingsOn(ctx, db, 0)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.readSettingsOn(ctx, nil, 1)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = (*Module)(nil).writer()
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = (*Module)(nil).putSettingsOn(ctx, nil, 1, 0, base, "", "")
	require.ErrorIs(t, err, ErrUnavailable)
	err = m.authorizeHumanOn(ctx, db, 1, "policy_manage")
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = settingsCommand(t, m, db, t.Context(), 0, base)
	require.ErrorIs(t, err, ErrForbidden)
	q := query.BindSettings(db, m.seed.names)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putSettingsOn(ctx, tx, 1, 0, SettingsValue{}, "", "")
		require.ErrorIs(t, e, ErrInvalidArgument)
		return nil
	}))
	value := base
	value.DefaultBudgetNotifyPct = 90
	_, err = settingsCommand(t, m, db, ctx, 0, value)
	require.NoError(t, err)
	_, err = q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(1))).Update(q.ValueJSON, "{}")
	require.NoError(t, err)
	_, err = m.readSettingsOn(ctx, db, 1)
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, db.Exec("DROP TABLE g_governance_settings").Error)
	_, err = m.readSettingsOn(ctx, db, 1)
	require.Error(t, err)
}

func TestSettingsSourceFailuresPreservePreviousPolicyAndAudit(t *testing.T) {
	for _, scenario := range []string{"read", "identifier", "zero_identifier", "insert", "update", "audit", "lock", "human", "explanation", "stepup_missing"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := settingsHarness(t)
			fault := errors.New("policy source failed")
			want := fault
			value := defaultSettings().SettingsValue
			value.DefaultBudgetNotifyPct = 85
			version := 0
			if scenario == "update" {
				_, err := settingsCommand(t, m, db, ctx, 0, value)
				require.NoError(t, err)
				version = 1
				value.DefaultBudgetNotifyPct = 86
			}
			before := seedCount(t, db, m.seed.names.Settings.String())
			events := seedCount(t, db, "event_probe")
			audits := seedCount(t, db, "audit_probe")
			switch scenario {
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:settings", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.Settings.String() {
						tx.AddError(fault)
					}
				}))
			case "identifier":
				m.seed.ids = seedTestIDs{err: fault}
			case "zero_identifier":
				m.seed.ids = seedTestIDs{}
				want = ErrUnavailable
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:settings", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.Settings.String() {
						tx.AddError(fault)
					}
				}))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:settings", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.Settings.String() {
						tx.AddError(fault)
					}
				}))
			case "audit":
				m.seed.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return fault }
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "explanation":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, fault
				}
			case "stepup_missing":
				m.reauth = nil
				value.SODHumanSelfApproval = "ALLOW_WITH_REASON"
				want = ErrUnavailable
			}
			_, err := settingsCommand(t, m, db, ctx, version, value)
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Query().Remove("test:settings"))
			require.NoError(t, db.Callback().Create().Remove("test:settings"))
			require.NoError(t, db.Callback().Update().Remove("test:settings"))
			require.Equal(t, before, seedCount(t, db, m.seed.names.Settings.String()))
			require.Equal(t, events, seedCount(t, db, "event_probe"))
			require.Equal(t, audits, seedCount(t, db, "audit_probe"))
			saved, err := m.readSettingsOn(ctx, db, 1)
			require.NoError(t, err)
			require.Equal(t, version, saved.Version)
		})
	}
}

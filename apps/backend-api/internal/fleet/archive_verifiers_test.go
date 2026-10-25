package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestVerifierToolsUseCurrentAuthorityAndObservedVersionFingerprint(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, true, nil
	}
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		session, e = m.OpenSessionOn(ctx, tx, 7, 1, "verifier-tools")
		return e
	}))
	record := func(version string) {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return m.RecordInventoryOn(ctx, tx, 7, 1, session.ID, session.ConnEpoch, Inventory{Tools: []ToolObservation{{Name: "browser", Version: version, ExecutablePath: "/observed/browser"}, {Name: "node", Version: "22.0", ExecutablePath: "/observed/node"}}})
		}))
	}
	read := func(names []string) (string, error) {
		var pin string
		e := db.Transaction(func(tx *gorm.DB) error { var e error; pin, e = m.verifierToolsOn(ctx, tx, 7, 1, names); return e })
		return pin, e
	}
	record("141.0")
	pin, e := read([]string{"browser", "node"})
	require.NoError(t, e)
	require.Len(t, pin, 64)
	reordered, e := read([]string{"node", "browser"})
	require.NoError(t, e)
	require.Equal(t, pin, reordered)
	record("142.0")
	next, e := read([]string{"browser", "node"})
	require.NoError(t, e)
	require.NotEqual(t, pin, next)
	_, e = read([]string{"missing"})
	require.ErrorIs(t, e, ErrUnavailable)
	_, e = read([]string{"browser", "browser"})
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = m.verifierToolsOn(ctx, db, 7, 1, []string{"browser"})
	require.ErrorIs(t, e, ErrInvalidArgument)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = read([]string{"browser"})
	require.ErrorIs(t, e, ErrMachineForbidden)
}

func TestEvalAgentConfigurationIsSourceOwnedAndPromptIsPrivate(t *testing.T) {
	m, db, ctx, _ := machineHarness(t)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, true, nil
	}
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Table(m.names.AgentRoles.String()).Where("tenant_id=? AND agent_id=?", 7, 1).Update("revoked_at", time.Now().UTC()).Error)
	require.NoError(t, db.Table(m.names.AgentRoles.String()).Create(map[string]any{"tenant_id": 7, "agent_id": 1, "role": "ACCEPTOR", "iam_role_key": "agent_acceptor", "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()}).Error)
	require.NoError(t, db.Table(m.names.Agents.String()).Where("tenant_id=? AND id=?", 7, 1).Update("state", "ACTIVE").Error)
	read := func() (VerifierAgentConfiguration, error) {
		var out VerifierAgentConfiguration
		e := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.verifierAgentOn(ctx, tx, 7, 1, "ACCEPTOR", "model")
			return e
		})
		return out, e
	}
	first, e := read()
	require.NoError(t, e)
	require.Equal(t, "Private prompt", first.Instructions)
	require.Len(t, first.SHA256, 64)
	raw, e := json.Marshal(first)
	require.NoError(t, e)
	require.NotContains(t, string(raw), "Private prompt")
	require.NoError(t, db.Table(m.names.Agents.String()).Where("tenant_id=? AND id=?", 7, 1).Update("system_prompt", "Changed private instructions").Error)
	next, e := read()
	require.NoError(t, e)
	require.NotEqual(t, first.SHA256, next.SHA256)
	require.NoError(t, db.Table(m.names.AgentRoles.String()).Where("tenant_id=? AND agent_id=? AND role=?", 7, 1, "ACCEPTOR").Update("revoked_at", time.Now().UTC()).Error)
	_, e = read()
	require.ErrorIs(t, e, ErrUnavailable)
}

func TestProjectArchiveFactsReadActualTerminalReleaseAndPendingEnsembleDecisions(t *testing.T) {
	m, db, ctx, terminal, _ := terminalFixture(t)
	now := time.Now().UTC()
	plans := []*model.EnsemblePlan{{ID: 601, TenantID: 7, ProjectID: 9, WorkItemID: 91, State: "DRAFT", Reason: "compare", ComparisonJSON: "{}", CreatedBy: 11, Version: 1, CreatedAt: now, UpdatedAt: now, ApprovalRequestedAt: &now}, {ID: 602, TenantID: 7, ProjectID: 9, WorkItemID: 92, State: "COMPARING", Reason: "compare", ComparisonJSON: "{}", CreatedBy: 11, Version: 2, CreatedAt: now, UpdatedAt: now}}
	require.NoError(t, query.BindEnsemblePlans(db, m.names).WithContext(ctx).Create(plans...))
	read := func() []ProjectArchiveFact {
		var facts []ProjectArchiveFact
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; facts, e = m.projectArchiveFactsOn(ctx, tx, 7, 9); return e }))
		return facts
	}
	facts := read()
	require.Len(t, facts, 3)
	require.Equal(t, "TERMINAL", facts[0].Kind)
	require.False(t, facts[0].Ready)
	require.Equal(t, "ENSEMBLE_DECISION", facts[1].Kind)
	require.Equal(t, 2, facts[2].Version)
	_, e := m.CloseTerminal(ctx, 7, terminal.ID, "USER")
	require.NoError(t, e)
	facts = read()
	require.True(t, facts[0].Ready)
	for _, table := range []string{m.names.TerminalSessions.String(), m.names.EnsemblePlans.String()} {
		injected := errors.New("archive source unavailable")
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("archive_source_failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(injected)
			}
		}))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			facts, e := m.projectArchiveFactsOn(ctx, tx, 7, 9)
			require.ErrorIs(t, e, injected)
			require.Nil(t, facts)
			return nil
		}))
		require.NoError(t, db.Callback().Query().Remove("archive_source_failure"))
	}
	_, e = m.projectArchiveFactsOn(ctx, db, 7, 9)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = m.projectArchiveFactsOn(ctx, nil, 7, 9)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestVerifierAgentRejectsMissingCurrentPrincipalAndStorageAuthority(t *testing.T) {
	for _, fault := range []string{"principal", "accounts-missing", "accounts-error", "accounts-disabled", "query", "policy"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _ := machineHarness(t)
			m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, true, nil
			}
			m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			injected := errors.New("current verifier source unavailable")
			switch fault {
			case "principal":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "accounts-missing":
				m.accounts = nil
			case "accounts-error":
				m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, injected }
			case "accounts-disabled":
				m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("verifier_source_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Agents.String() {
						tx.AddError(injected)
					}
				}))
			case "policy":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				fact, e := m.verifierAgentOn(ctx, tx, 7, 1, "ACCEPTOR", "model")
				require.Empty(t, fact.SHA256)
				require.Empty(t, fact.Instructions)
				return e
			}))
		})
	}
}

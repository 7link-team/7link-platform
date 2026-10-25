package knowledge

import (
	"7link.com/platform/internal/knowledge/query"
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type activeMembers struct{}

func (activeMembers) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}
func (activeMembers) IsMemberActiveOn(_ context.Context, _ *gorm.DB, tenant, actor guid.ID) (bool, error) {
	return tenant == 1 && actor != 9, nil
}
func knowledgeContext(t *testing.T, actor guid.ID) context.Context {
	t.Helper()
	return authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: actor, TenantID: 1, SubjectType: authn.SubjectTypePrincipal})
}
func knowledgeHarness(t *testing.T) (*Module, *gorm.DB, *module.Services) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "knowledge.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("k_"), appenv.WithDatabase(router))
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}})
	m.archiveState.read = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil }
	db := router.Write()
	s := module.NewServices()
	ids, err := guid.New(1)
	require.NoError(t, err)
	require.NoError(t, module.Provide[guid.Source](s, ids))
	require.NoError(t, module.Provide[identity.PrincipalStateStore](s, identity.PrincipalStateStore{LockActiveRow: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id, LoginName: id.String()}, id > 0 && id != 8, nil
	}, HumanByLogin: func(_ context.Context, _ *gorm.DB, login string) (identity.Principal, bool, error) {
		id, e := strconv.ParseInt(login, 10, 64)
		return identity.Principal{ID: guid.ID(id)}, e == nil && id != 7, nil
	}}))
	require.NoError(t, module.Provide[core.ActiveMemberChecker](s, activeMembers{}))
	require.NoError(t, module.Provide[core.EntityConstraintOn](s, core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	})))
	require.NoError(t, module.Provide[core.TenantConstraintOn](s, core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	})))
	require.NoError(t, module.Provide[fleet.AgentDirectory](s, fleet.AgentDirectory{ByPrincipalOn: func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 70, TenantID: tenant, PrincipalID: principal, OwnerPrincipalID: 2, State: "ACTIVE"}, principal == 7, nil
	}}))
	require.NoError(t, db.Exec("CREATE TABLE audit_probe (event_type TEXT NOT NULL)").Error)
	require.NoError(t, module.Provide[audit.Appender](s, func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		require.NotEmpty(t, event.Detail["authority_source"])
		return tx.WithContext(ctx).Exec("INSERT INTO audit_probe VALUES (?)", event.EventType).Error
	}))
	locks := dlock.NewModule(rt)
	require.NoError(t, locks.ProvideServices(s))
	publisher := events.NewModule(rt)
	require.NoError(t, publisher.ProvideServices(s))
	replay := idempotency.NewModule(rt)
	require.NoError(t, replay.ProvideServices(s))
	require.NoError(t, module.Provide[jobs.Queue](s, &knowledgeQueue{}))
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, registry.Contribute([]authz.Action{
		{Resource: "agent", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read agent skill mounts"},
		{Resource: "agent", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "manage agent skill mounts"},
	}, nil))
	require.NoError(t, module.Provide[*authz.Registrar](s, authz.NewDeclarationOnlyRegistrar(registry)))
	require.NoError(t, s.Seal())
	require.NoError(t, m.InjectServices(s))
	require.NoError(t, publisher.InjectServices(s))
	require.NoError(t, replay.InjectServices(s))
	require.NoError(t, publisher.Initialize(t.Context()))
	require.NoError(t, platformi18n.NewModule(rt, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, m.Initialize(t.Context()))
	require.NoError(t, replay.Initialize(t.Context()))
	raw, err := db.DB()
	require.NoError(t, err)
	for _, source := range []goosex.Source{m.MigrationSource(), locks.MigrationSource(), publisher.MigrationSource(), replay.MigrationSource()} {
		source.Prefix = "k_"
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	}
	return m, db, s
}
func writeKnowledge(t *testing.T, m *Module, db *gorm.DB, in EntryInput) EntryView {
	t.Helper()
	var v EntryView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.writeOn(knowledgeContext(t, 2), tx, 1, in); return e }))
	return v
}
func TestRuleMemoriesRequireHumanApprovalAndCurrentVersion(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	in := EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: ptr(3)}, Kind: "RULE", Title: "Use verified evidence", Body: "Always record the evidence source.", Provenance: "Human-authored project rule"}
	v := writeKnowledge(t, m, db, in)
	require.Equal(t, "PENDING_REVIEW", v.State)
	query := knowledgeListInput{Scope: "PROJECT", ProjectID: "3", State: "ACTIVE", Limit: 50}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listOn(knowledgeContext(t, 2), tx, 1, query)
		require.Empty(t, page.Items)
		return e
	}))
	query.State = "PENDING_REVIEW"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listOn(knowledgeContext(t, 2), tx, 1, query)
		require.Len(t, page.Items, 1)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.reviewOn(knowledgeContext(t, 7), tx, 1, v.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Reviewed by agent"})
		return e
	}), ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.reviewOn(knowledgeContext(t, 3), tx, 1, v.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Reviewed and approved by an independent human"})
		return e
	}))
	require.Equal(t, "ACTIVE", v.State)
	require.Equal(t, 2, v.Version)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.reviewOn(knowledgeContext(t, 2), tx, 1, v.ID, ReviewInput{Version: 1, Action: "REVOKED", Reason: "Revocation reason"})
		return e
	}), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.reviewOn(knowledgeContext(t, 2), tx, 1, v.ID, ReviewInput{Version: 2, Action: "REVOKED", Reason: "Replaced by updated evidence"})
		return e
	}))
	require.Equal(t, "REVOKED", v.State)
	query.State = "ACTIVE"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listOn(knowledgeContext(t, 2), tx, 1, query)
		require.Empty(t, page.Items)
		return e
	}))
	for _, table := range []string{"k_knowledge_reviews", "k_domain_events", "audit_probe"} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.EqualValues(t, 3, n)
	}
}
func ptr(id guid.ID) *guid.ID { return &id }
func TestRuleWriterCannotReviewOwnRule(t *testing.T) {
	for _, action := range []string{"APPROVED", "REJECTED"} {
		t.Run(action, func(t *testing.T) {
			m, db, _ := knowledgeHarness(t)
			entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Independent review", Body: "A rule requires a different reviewer.", Provenance: "Human-authored proposal"})
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.reviewOn(knowledgeContext(t, 2), tx, 1, entry.ID, ReviewInput{Version: 1, Action: action, Reason: "The author must not review this rule"})
				return err
			}), ErrForbidden)
			var stored struct {
				State   string
				Version int
			}
			require.NoError(t, db.Table(m.names.Entries.String()).Where("id = ?", entry.ID).Take(&stored).Error)
			require.Equal(t, "PENDING_REVIEW", stored.State)
			require.Equal(t, 1, stored.Version)
			for _, table := range []string{m.names.Reviews.String(), "k_domain_events", "audit_probe"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.EqualValues(t, 1, count, "a denied review must not write any business fact")
			}
		})
	}
}
func TestKnowledgeScopesAndAgentPrivacy(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Team", Body: "Team knowledge", Provenance: "Human notes"})
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: ptr(3)}, Kind: "DOCUMENT", Title: "Project", Body: "Project knowledge", Provenance: "Human notes"})
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "AGENT", AgentPrincipalID: ptr(7)}, Kind: "EXPERIENCE", Title: "Private", Body: "Agent experience", Provenance: "Human notes"})
	m.tenantPolicy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission != "agent_workspace_read"}, nil
	}
	in := knowledgeListInput{Scope: "AGENT", AgentPrincipalID: "7", State: "ACTIVE", Limit: 1}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listOn(knowledgeContext(t, 2), tx, 1, in)
		require.Len(t, page.Items, 1)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.listOn(knowledgeContext(t, 4), tx, 1, in); return e }), ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listOn(knowledgeContext(t, 7), tx, 1, in)
		require.Len(t, page.Items, 1)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.listOn(knowledgeContext(t, 2), tx, 2, in); return e }), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.writeOn(knowledgeContext(t, 7), tx, 1, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Unauthorized", Body: "No team writes", Provenance: "Agent claim"})
		return e
	}), ErrForbidden)
}
func TestKnowledgeTransactionRollsBackEveryFactOnOutboxFailure(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:knowledge-outbox", func(tx *gorm.DB) {
		if tx.Statement.Table == "k_domain_events" {
			tx.AddError(errors.New("injected outbox failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove("test:knowledge-outbox") })
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.writeOn(knowledgeContext(t, 2), tx, 1, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Rule", Body: "Uncommitted memory", Provenance: "Human notes"})
		return e
	}))
	for _, table := range []string{"k_knowledge_entries", "k_knowledge_reviews", "audit_probe", "k_domain_events"} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.Zero(t, n)
	}
}

func TestKnowledgeCurrentIdentityAndPolicyFailuresNeverWrite(t *testing.T) {
	boom := errors.New("identity provider unavailable")
	for _, tc := range []struct {
		name   string
		change func(*Module)
		want   error
	}{
		{"missing principal capability", func(m *Module) { m.principals.LockActiveRow = nil }, ErrUnavailable},
		{"principal lookup failure", func(m *Module) {
			m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, boom
			}
		}, boom},
		{"inactive identity", func(m *Module) {
			m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, nil
			}
		}, ErrForbidden},
		{"human lookup failure", func(m *Module) {
			m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
				return identity.Principal{}, false, boom
			}
		}, boom},
		{"tenant policy failure", func(m *Module) {
			m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, boom
			}
		}, boom},
		{"scope policy revoked", func(m *Module) {
			m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, nil
			}
		}, ErrForbidden},
		{"audit capability unavailable", func(m *Module) { m.audit = nil }, ErrUnavailable},
		{"outbox capability unavailable", func(m *Module) { m.events = nil }, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, db, _ := knowledgeHarness(t)
			tc.change(m)
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.writeOn(knowledgeContext(t, 2), tx, 1, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Guide", Body: "Protected", Provenance: "Human notes"})
				return err
			})
			require.ErrorIs(t, err, tc.want)
			for _, table := range []string{"k_knowledge_entries", "k_knowledge_reviews", "audit_probe", "k_domain_events"} {
				var n int64
				require.NoError(t, db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

func TestKnowledgeInvalidScopeAndReviewTransitions(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	for _, scope := range []Scope{{Scope: "UNKNOWN"}, {Scope: "TEAM", ProjectID: ptr(3)}, {Scope: "PROJECT"}, {Scope: "AGENT"}, {Scope: "AGENT", AgentPrincipalID: ptr(0)}} {
		require.False(t, validScope(scope))
	}
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Guide", Body: "Contents", Provenance: "Human source"})
	for _, review := range []ReviewInput{{Version: 0, Action: "APPROVED", Reason: "Reviewed"}, {Version: 1, Action: "UNKNOWN", Reason: "Reviewed"}, {Version: 1, Action: "REVOKED", Reason: "no"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.reviewOn(knowledgeContext(t, 2), tx, 1, entry.ID, review)
			return err
		}), ErrInvalid)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 2), tx, 1, entry.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Documents cannot be rule-approved"})
		return err
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 2), tx, 1, 999, ReviewInput{Version: 1, Action: "REVOKED", Reason: "Missing entry"})
		return err
	}), ErrNotFound)
}

func TestKnowledgeAgentIdentityFailuresAndSelfOnlyWrites(t *testing.T) {
	boom := errors.New("fleet identity read failed")
	for _, tc := range []struct {
		name   string
		lookup func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error)
		actor  guid.ID
		want   error
	}{
		{"missing directory", nil, 7, ErrUnavailable},
		{"directory failure", func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
			return fleet.AgentIdentity{}, false, boom
		}, 7, boom},
		{"unregistered service account", func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
			return fleet.AgentIdentity{}, false, nil
		}, 7, ErrForbidden},
		{"inactive agent", func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
			return fleet.AgentIdentity{PrincipalID: 7, State: "RETIRED"}, true, nil
		}, 7, ErrForbidden},
		{"missing target", func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
			return fleet.AgentIdentity{}, false, nil
		}, 2, ErrNotFound},
		{"target lookup failure", func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
			return fleet.AgentIdentity{}, false, boom
		}, 2, boom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, db, _ := knowledgeHarness(t)
			m.agents.ByPrincipalOn = tc.lookup
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.writeOn(knowledgeContext(t, tc.actor), tx, 1, EntryInput{Scope: Scope{Scope: "AGENT", AgentPrincipalID: ptr(7)}, Kind: "EXPERIENCE", Title: "Private", Body: "Notes", Provenance: "Author claim"})
				return err
			})
			require.ErrorIs(t, err, tc.want)
			var n int64
			require.NoError(t, db.Table("k_knowledge_entries").Count(&n).Error)
			require.Zero(t, n)
		})
	}
	m, db, _ := knowledgeHarness(t)
	m.memory.evidence = MemoryExecutionEvidence{VerifyOn: func(_ context.Context, _ *gorm.DB, tenant, agent guid.ID, project *guid.ID, execution guid.ID, _ time.Time) (string, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 7, agent)
		require.EqualValues(t, 90, execution)
		return "", nil
	}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, err := m.writeOn(knowledgeContext(t, 7), tx, 1, EntryInput{Scope: Scope{Scope: "AGENT", AgentPrincipalID: ptr(7)}, Kind: "EXPERIENCE", Title: "Private", Body: "Verified execution notes", Provenance: "Execution output", SourceExecutionID: ptr(90)})
		require.Equal(t, "ACTIVE", v.State)
		return err
	}))
}

type knowledgeIDs func() (int64, error)

func (f knowledgeIDs) Next() (int64, error) { return f() }

func TestKnowledgeIdentifierFailureCannotLeaveUnauditedEntry(t *testing.T) {
	boom := errors.New("identifier source unavailable")
	for _, failAt := range []int{1, 2} {
		for _, zero := range []bool{false, true} {
			m, db, _ := knowledgeHarness(t)
			calls := 0
			m.ids = knowledgeIDs(func() (int64, error) {
				calls++
				if calls == failAt {
					if zero {
						return 0, nil
					}
					return 0, boom
				}
				return int64(100 + calls), nil
			})
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.writeOn(knowledgeContext(t, 2), tx, 1, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Guide", Body: "Content", Provenance: "Human notes"})
				return err
			})
			if zero {
				require.ErrorIs(t, err, ErrUnavailable)
			} else {
				require.ErrorIs(t, err, boom)
			}
			for _, table := range []string{"k_knowledge_entries", "k_knowledge_reviews", "audit_probe", "k_domain_events"} {
				var n int64
				require.NoError(t, db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		}
	}
}

func TestKnowledgeEntryWriteAndReviewStorageFailuresKeepIndependentApprovalAtomic(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	in := memoryInput()
	in.Kind = "RULE"
	proposed := writeKnowledge(t, m, db, in)
	failure, rollback := errors.New("knowledge entry persistence unavailable"), errors.New("rollback entry probe")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("entry-storage-failure", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("entry-storage-failure", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("entry-storage-failure", probe))
	exercise := func(run func(*gorm.DB) error) {
		apply := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				if err := run(tx); err != nil {
					return err
				}
				return rollback
			})
		}
		calls, failAt = 0, 0
		require.ErrorIs(t, apply(), rollback)
		total := calls
		require.Positive(t, total)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, apply(), failure, "entry persistence stage %d", i)
		}
		calls, failAt = 0, 0
	}
	exercise(func(tx *gorm.DB) error {
		_, err := m.writeOn(knowledgeContext(t, 2), tx, 1, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Canonical policy", Body: "Retain immutable source evidence.", Provenance: "Human policy"})
		return err
	})
	exercise(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, proposed.ID, ReviewInput{Version: proposed.Version, Action: "APPROVED", Reason: "Independent approval"})
		return err
	})
	require.NoError(t, db.Callback().Query().Remove("entry-storage-failure"))
	require.NoError(t, db.Callback().Create().Remove("entry-storage-failure"))
	require.NoError(t, db.Callback().Update().Remove("entry-storage-failure"))
	var count int64
	require.NoError(t, db.Table(m.names.Entries.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var state string
	require.NoError(t, db.Table(m.names.Entries.String()).Select("state").Where("id = ?", proposed.ID).Scan(&state).Error)
	require.Equal(t, "PENDING_REVIEW", state)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, proposed.ID, ReviewInput{Version: proposed.Version, Action: "APPROVED", Reason: "Independent approval"})
		require.Equal(t, "ACTIVE", out.State)
		return err
	}))
}

func TestKnowledgeWritesAndReviewsRejectCurrentPrincipalServiceFailures(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	pending := memoryInput()
	pending.Kind = "RULE"
	pending.Title = "Pending authority review"
	entry := writeKnowledge(t, m, db, pending)
	failure := errors.New("current principal authority unavailable")
	rollback := errors.New("rollback principal diagnostic")
	run := func(review bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var err error
			if review {
				_, err = m.reviewOn(knowledgeContext(t, 3), tx, 1, entry.ID, ReviewInput{Version: entry.Version, Action: "APPROVED", Reason: "Current independent reviewer authority"})
			} else {
				input := memoryInput()
				input.Title = "New principal fenced memory"
				_, err = m.writeOn(knowledgeContext(t, 2), tx, 1, input)
			}
			if err != nil {
				return err
			}
			return rollback
		})
	}
	original := m.principals
	calls, failAt := 0, 0
	inactive := false
	m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		calls++
		if calls == failAt {
			if inactive {
				return identity.Principal{ID: id}, false, nil
			}
			return identity.Principal{}, false, failure
		}
		return original.LockActiveRow(ctx, tx, id)
	}
	for _, review := range []bool{false, true} {
		calls, failAt = 0, 0
		require.ErrorIs(t, run(review), rollback)
		total := calls
		require.Greater(t, total, 1)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, run(review), failure, "principal stage %d", i)
		}
	}
	calls, failAt = 0, 1
	inactive = true
	require.ErrorIs(t, run(false), ErrForbidden)
	m.principals = original
	calls, failAt = 0, 0
	m.principals.HumanByLogin = func(ctx context.Context, tx *gorm.DB, login string) (identity.Principal, bool, error) {
		calls++
		if calls == failAt {
			return identity.Principal{}, false, failure
		}
		return original.HumanByLogin(ctx, tx, login)
	}
	for _, review := range []bool{false, true} {
		calls, failAt = 0, 0
		require.ErrorIs(t, run(review), rollback)
		total := calls
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, run(review), failure, "human stage %d", i)
		}
	}
	m.principals = original
	q := query.BindEntries(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "PENDING_REVIEW", row.State)
	require.Equal(t, entry.Version, row.Version)
}

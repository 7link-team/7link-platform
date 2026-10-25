package templates

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCopyTemplateProvenanceMandatoryGatesAndReplay(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Nodes[1].Mandatory = true
	source := publishRuntime(t, m, db, ctx, "source", d)
	baseline, err := m.getHTTP(ctx, &getInput{ID: source.ID.String()})
	require.NoError(t, err)
	source = baseline.Body.Data
	in := &draftInput{IdempotencyKey: "copy-once"}
	in.Body.BasedOnVersionID, in.Body.Key, in.Body.Name = source.ID, "team-copy", "Team workflow"
	first, err := m.draftHTTP(ctx, in)
	require.NoError(t, err)
	copied := first.Body.Data
	require.Equal(t, "DRAFT", copied.State)
	require.Equal(t, "TEAM", copied.Origin)
	require.Equal(t, "Team workflow", copied.Name)
	require.Equal(t, source.ID, *copied.BasedOnVersionID)
	require.Equal(t, source.Hash, copied.BasedOnSHA256)
	require.NotEqual(t, source.Definition.Workflow.WorkflowID, copied.Definition.Workflow.WorkflowID)
	require.Equal(t, source.Definition.Workflow.Nodes, copied.Definition.Workflow.Nodes)
	replayed, err := m.draftHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, copied, replayed.Body.Data)
	in.IdempotencyKey = "duplicate-copy"
	_, err = m.draftHTTP(ctx, in)
	assertStatus(t, err, 409)
	for _, change := range []string{"unmark", "replace", "remove"} {
		t.Run(change, func(t *testing.T) {
			node := copied.Definition.Workflow.Nodes[1]
			op := TemplatePatchOperation{Kind: "update_node", ID: node.ID}
			switch change {
			case "unmark":
				node.Mandatory = false
			case "replace":
				node.Gate = "CLIENT"
			case "remove":
				op.Kind = "remove_node"
			}
			op.Node = &node
			patch := TemplateWorkflowPatch{BaseVersion: 1, BaseHash: copied.Hash, Reason: "Attempt inherited gate edit", Operations: []TemplatePatchOperation{op}}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.patchOn(ctx, tx, copied.ID, patch); return e })
			if change == "unmark" {
				require.ErrorIs(t, err, ErrMandatoryGate)
			} else {
				require.Error(t, err)
			}
		})
	}
	// A harmless label edit is still permitted, and the independent workflow publishes.
	node := copied.Definition.Workflow.Nodes[0]
	node.Name = "Team build"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.patchOn(ctx, tx, copied.ID, TemplateWorkflowPatch{BaseVersion: 1, BaseHash: copied.Hash, Reason: "Team wording", Operations: []TemplatePatchOperation{{Kind: "update_node", ID: node.ID, Node: &node}}})
		if e != nil {
			return e
		}
		_, e = m.publishOn(ctx, tx, v.ID, v.Hash)
		return e
	}))
	stored, err := m.getHTTP(ctx, &getInput{ID: source.ID.String()})
	require.NoError(t, err)
	require.Equal(t, source, stored.Body.Data)
	// A later semver through the compatible raw-draft endpoint cannot shed the
	// copied template family's immutable ancestry or mandatory gate protections.
	next := cloneDefinition(copied.Definition)
	next.Semver = "1.1.0"
	next.Workflow.Version = 2
	next.Workflow.Nodes[1].Mandatory = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.draftOn(ctx, tx, copied.Key, next); return e }), ErrMandatoryGate)
	next.Workflow.Nodes[1].Mandatory = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.draftOn(ctx, tx, copied.Key, next)
		require.NotNil(t, v.BasedOnVersionID)
		require.Equal(t, source.ID, *v.BasedOnVersionID)
		require.Equal(t, source.Hash, v.BasedOnSHA256)
		return e
	}))
	// An immutable source may later retire; replay still checks visibility but
	// does not rerun the original creation precondition.
	q := query.BindVersions(db, m.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(source.ID)).Update(q.State, "RETIRED")
	require.NoError(t, err)
	in.IdempotencyKey = "copy-once"
	replayed, err = m.draftHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, copied, replayed.Body.Data)
	// Replay must recheck current source visibility and active Human authority.
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	in.IdempotencyKey = "copy-once"
	_, err = m.draftHTTP(ctx, in)
	assertStatus(t, err, 403)
}

func TestCopyTemplateTenantIsolationFirstPartyAndRollback(t *testing.T) {
	for _, fault := range []string{"foreign", "draft", "inactive", "audit", "outbox", "name", "key"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			source := publishRuntime(t, m, db, ctx, "source", testDefinition())
			q := query.BindVersions(db, m.names)
			name, key := "Team copy", "copy"
			switch fault {
			case "foreign":
				_, err := q.WithContext(ctx).Where(q.ID.Eq(source.ID)).Update(q.TenantID, guid.ID(2))
				require.NoError(t, err)
			case "draft":
				_, err := q.WithContext(ctx).Where(q.ID.Eq(source.ID)).Update(q.State, "DRAFT")
				require.NoError(t, err)
			case "inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failed") }
			case "outbox":
				m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, errors.New("publish failed") })
			case "name":
				name = " "
			case "key":
				key = " "
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.copyOn(ctx, tx, source.ID, key, name); return err }))
			count, err := q.WithContext(ctx).Count()
			require.NoError(t, err)
			require.EqualValues(t, 1, count)
		})
	}
	m, db, ctx := templateHarness(t)
	source := publishRuntime(t, m, db, ctx, "first-party", testDefinition())
	q := query.BindVersions(db, m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(source.ID)).Updates(map[string]any{"tenant_id": 0, "origin": "FIRST_PARTY"})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.copyOn(ctx, tx, source.ID, "custom", "Custom")
		require.Equal(t, "TEAM", v.Origin)
		return e
	}))
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, err = m.getHTTP(other, &getInput{ID: source.ID.String()})
	require.NoError(t, err, "published first-party sources are readable across tenants")
	local, err := q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(1)), q.Key.Eq("custom")).Take()
	require.NoError(t, err)
	_, err = m.getHTTP(other, &getInput{ID: local.ID.String()})
	assertStatus(t, err, 404)
}

func TestCopyTemplateNameUsesUnicodeCharacterLimit(t *testing.T) {
	m, db, ctx := templateHarness(t)
	source := publishRuntime(t, m, db, ctx, "source", testDefinition())
	name := strings.Repeat("名", 200)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.copyOn(ctx, tx, source.ID, "unicode", name)
		require.Equal(t, name, v.Name)
		return e
	}))
	for _, invalid := range []string{strings.Repeat("名", 201), string([]byte{0xff})} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.copyOn(ctx, tx, source.ID, "invalid", invalid); return e }), ErrInvalid)
	}
}

func TestDeprecationRequiresHigherPublishedVersionAndPreservesPin(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	old := publishRuntime(t, m, db, ctx, "versions", d)
	var pin TemplateInstanceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; pin, e = m.instantiateOn(ctx, tx, old.ID, 99, 11); return e }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.deprecateOn(ctx, tx, old.ID); return e }), ErrNoNewerVersion)
	d.Semver = "1.10.0"
	d.Workflow.Version = 2
	var newer TemplateVersionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; newer, e = m.draftOn(ctx, tx, "versions", d); return e }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.deprecateOn(ctx, tx, old.ID); return e }), ErrNoNewerVersion)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishOn(ctx, tx, newer.ID, newer.Hash); return e }))
	in := &deprecationInput{VersionID: old.ID.String(), IdempotencyKey: "deprecate-once"}
	v, err := m.deprecateHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "DEPRECATED", v.Body.Data.State)
	require.Equal(t, old.Hash, v.Body.Data.Hash)
	require.Equal(t, old.Definition, v.Body.Data.Definition)
	replay, err := m.deprecateHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, v.Body.Data, replay.Body.Data)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, old.ID, 100, 11); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		current, e := m.instanceOn(ctx, tx, 99)
		require.Equal(t, "DEPRECATED", current.TemplateState)
		require.Equal(t, pin.TemplateVersionID, current.TemplateVersionID)
		require.Equal(t, pin.Hash, current.Hash)
		require.Equal(t, pin.Workflow, current.Workflow)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		current, e := m.advanceOn(ctx, tx, 99)
		require.Equal(t, pin.Hash, current.Hash)
		require.Equal(t, pin.TemplateVersionID, current.TemplateVersionID)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		children, e := m.childrenOn(ctx, tx)
		require.Contains(t, children, TemplateWorkflowRef{old.Definition.Workflow.WorkflowID, old.Definition.Workflow.Version})
		return e
	}))
	require.True(t, higherSemver("1.10.0", "1.9.0"))
	require.False(t, higherSemver("1.0.0", "1.0.0"))
	require.False(t, higherSemver("2.0.0", "3.0.0"))
}

func TestDeprecationRollbackAndCurrentHumanAuthorization(t *testing.T) {
	for _, fault := range []string{"audit", "event", "forbidden", "inactive"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			d := testDefinition()
			old := publishRuntime(t, m, db, ctx, "versions", d)
			d.Semver = "2.0.0"
			d.Workflow.Version = 2
			publishRuntime(t, m, db, ctx, "versions", d)
			switch fault {
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit") }
			case "event":
				m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, errors.New("event") })
			case "forbidden":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.deprecateOn(ctx, tx, old.ID); return e }))
			q := query.BindVersions(db, m.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(old.ID)).Take()
			require.NoError(t, err)
			require.Equal(t, "PUBLISHED", row.State)
			require.Equal(t, old.Hash, row.SHA256)
		})
	}
}

func TestCopyStorageAndInheritedSourceFailuresAreAtomic(t *testing.T) {
	for _, failure := range []string{"update", "copied-event", "source-read"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			source := publishRuntime(t, m, db, ctx, "copy-source", testDefinition())
			injected := errors.New("copy persistence unavailable")
			if failure == "update" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("copy-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Versions.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Update().Remove("copy-failure")
			}
			if failure == "source-read" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("copy-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Versions.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Query().Remove("copy-failure")
			}
			if failure == "copied-event" {
				original := m.audit
				m.audit = func(c context.Context, tx *gorm.DB, event audit.DomainEvent) error {
					if event.EventType == "template.copied" {
						return injected
					}
					return original(c, tx, event)
				}
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.copyOn(ctx, tx, source.ID, "copy-dest", "Human copy"); return e }), injected)
			if failure == "source-read" {
				require.NoError(t, db.Callback().Query().Remove("copy-failure"))
			}
			q := query.BindVersions(db, m.names)
			n, e := q.WithContext(ctx).Count()
			require.NoError(t, e)
			require.EqualValues(t, 1, n)
		})
	}
	m, db, ctx := templateHarness(t)
	draft := draftTemplate(t, m, db, ctx, "deprecate-draft")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.deprecateOn(ctx, tx, draft.ID); return e }), ErrConflict)
	_, e := m.deprecateHTTP(ctx, &deprecationInput{VersionID: "bad"})
	assertStatus(t, e, 422)
	require.False(t, higherSemver("invalid", "1.0.0"))
	require.False(t, higherSemver("2.0.0", "invalid"))
}

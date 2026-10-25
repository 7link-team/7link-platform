package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testIDs struct {
	n   int64
	err error
}

func (i *testIDs) Next() (int64, error) { i.n++; return i.n, i.err }

type testMembers bool

func (m testMembers) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return bool(m), nil
}
func (m testMembers) IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
	return bool(m), nil
}

type testPublisher struct{ err error }

func (p testPublisher) Publish(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	if p.err != nil {
		return 0, p.err
	}
	return 1, tx.Exec("INSERT INTO test_effects(kind) VALUES (?)", e.Type).Error
}
func configHarness(t *testing.T) (*Module, *gorm.DB, context.Context) {
	t.Helper()
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	router, e := gormx.NewDatasourceRouter(ctx, false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "integration.sqlite"), Readable: true, Writable: true}})
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix("yx_"))
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}, EncryptionKey: base64.StdEncoding.EncodeToString(make([]byte, 32))})
	db := router.Write()
	raw, e := db.DB()
	require.NoError(t, e)
	src := m.MigrationSource()
	src.Prefix = "yx_"
	require.NoError(t, goosex.Migrate(ctx, raw, src, "sqlite"))
	require.NoError(t, db.Exec("CREATE TABLE test_effects(kind TEXT NOT NULL)").Error)
	m.ids = &testIDs{n: 100}
	m.now = func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }
	m.principals = identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }, ActiveHumanByID: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}}
	m.members = testMembers(true)
	m.policy = func(_ context.Context, _ *gorm.DB, tenant guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, "integration_manage", permission)
		require.Equal(t, guid.ID(11), actor)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.projects = delivery.ProjectScope{AuthorizeOn: func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string) error {
		if tenant != 1 || project != 99 || permission != "project_manage" {
			return ErrForbidden
		}
		return nil
	}}
	m.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		return tx.Exec("INSERT INTO test_effects(kind) VALUES (?)", e.EventType).Error
	}
	m.events = testPublisher{}
	m.proof = func(_ context.Context, _ *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		if actor != 11 || cookie != "session" || proof != "proof" || purpose != "integration_credentials" || !strings.HasPrefix(target, "tenant:1:integration:") {
			return ErrForbidden
		}
		return nil
	}
	store := idempotency.NewModule(rt)
	src = store.MigrationSource()
	src.Prefix = "yx_"
	require.NoError(t, goosex.Migrate(ctx, raw, src, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, store.InjectServices(services))
	m.idem = store
	require.NoError(t, platformi18n.NewModule(rt, platformi18n.Base).Initialize(ctx))
	return m, db, ctx
}
func connectionBody() ConnectionInput {
	return ConnectionInput{Name: "Development", OrganizationID: "org-1", APIEndpoint: "https://example.invalid", Token: "private-token-1234", WebhookSecret: "private-hook", ReauthProof: "proof"}
}
func mappingBody() MappingInput {
	return MappingInput{ExternalProjectID: "external-1", ExternalProjectName: "Remote project", ProjectID: 99, SyncMode: "BIDIRECTIONAL", FieldMasters: map[string]string{"state": "MYRMIDON", "assignee": "YUNXIAO"}}
}
func createConnection(t *testing.T, m *Module, db *gorm.DB, ctx context.Context) ConnectionView {
	t.Helper()
	var v ConnectionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.saveConnectionOn(ctx, tx, 1, 0, 0, connectionBody(), "session")
		return e
	}))
	return v
}
func TestConnectionSealingVersioningAndRedaction(t *testing.T) {
	m, db, ctx := configHarness(t)
	v := createConnection(t, m, db, ctx)
	require.Equal(t, "PENDING_CHECK", v.State)
	require.Equal(t, "UNAVAILABLE", v.Availability)
	require.Equal(t, "1234", v.TokenLast4)
	require.Empty(t, v.TokenScopes)
	q := query.BindConnections(db, m.names)
	row, e := q.WithContext(ctx).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, e)
	require.NotContains(t, row.TokenSealed, "private-token")
	ring, e := m.keys()
	require.NoError(t, e)
	plain, e := ring.Open("token", []byte("1:"+v.ID.String()), row.TokenSealed)
	require.NoError(t, e)
	require.Equal(t, connectionBody().Token, string(plain))
	_, e = ring.Open("token", []byte("2:"+v.ID.String()), row.TokenSealed)
	require.Error(t, e)
	encoded, e := json.Marshal(v)
	require.NoError(t, e)
	require.NotContains(t, string(encoded), "private")
	require.NotContains(t, string(encoded), "sealed")
	in := connectionBody()
	in.Token = ""
	in.WebhookSecret = ""
	in.Disabled = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		next, e := m.saveConnectionOn(ctx, tx, 1, v.ID, 1, in, "session")
		require.Equal(t, 2, next.Version)
		require.Equal(t, "DISABLED", next.State)
		return e
	}))
	row2, e := q.WithContext(ctx).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, e)
	require.Equal(t, row.TokenSealed, row2.TokenSealed)
	e = db.Transaction(func(tx *gorm.DB) error { _, e := m.saveConnectionOn(ctx, tx, 1, v.ID, 1, in, "session"); return e })
	require.ErrorIs(t, e, ErrConflict)
	e = db.Transaction(func(tx *gorm.DB) error { _, e := m.saveConnectionOn(ctx, tx, 1, 999, 1, in, "session"); return e })
	require.ErrorIs(t, e, ErrNotFound)
}
func TestMappingPersistenceScopeMasterAndCAS(t *testing.T) {
	m, db, ctx := configHarness(t)
	c := createConnection(t, m, db, ctx)
	var v MappingView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.saveMappingOn(ctx, tx, 1, c.ID, 0, 0, mappingBody())
		return e
	}))
	require.Equal(t, mappingBody().FieldMasters, v.FieldMasters)
	in := mappingBody()
	in.Paused = true
	in.SyncMode = "IMPORT_ONLY"
	in.FieldMasters = nil
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		next, e := m.saveMappingOn(ctx, tx, 1, c.ID, v.ID, 1, in)
		require.Equal(t, 2, next.Version)
		require.True(t, next.Paused)
		return e
	}))
	for _, mut := range []struct {
		connection, id guid.ID
		version        int
		body           MappingInput
		want           error
	}{{c.ID, v.ID, 1, in, ErrConflict}, {c.ID, 999, 1, in, ErrNotFound}, {999, v.ID, 2, in, ErrInvalid}, {999, 0, 0, in, ErrNotFound}, {c.ID, 0, 0, func() MappingInput { x := in; x.TrustedPipelines = []string{"unverified"}; return x }(), ErrUnavailable}, {c.ID, 0, 0, func() MappingInput { x := in; x.ProjectID = 98; return x }(), ErrForbidden}} {
		e := db.Transaction(func(tx *gorm.DB) error {
			_, e := m.saveMappingOn(ctx, tx, 1, mut.connection, mut.id, mut.version, mut.body)
			return e
		})
		require.ErrorIs(t, e, mut.want)
	}
	var count int64
	require.NoError(t, db.Table(m.names.ProjectMappings.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
func TestCommandsRollBackConfigurationAuditAndOutbox(t *testing.T) {
	for _, which := range []string{"audit", "outbox", "ids", "key", "proof", "insert", "mapping-outbox"} {
		t.Run(which, func(t *testing.T) {
			m, db, ctx := configHarness(t)
			var conn ConnectionView
			if which == "mapping-outbox" {
				conn = createConnection(t, m, db, ctx)
			}
			var before int64
			require.NoError(t, db.Table("test_effects").Count(&before).Error)
			failure := errors.New("injected")
			switch which {
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox", "mapping-outbox":
				m.events = testPublisher{failure}
			case "ids":
				m.ids = &testIDs{err: failure}
			case "key":
				m.config.EncryptionKey = ""
			case "proof":
				m.proof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return failure }
			case "insert":
				require.NoError(t, db.Exec("DROP TABLE yx_integration_connections").Error)
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				if which == "mapping-outbox" {
					_, e := m.saveMappingOn(ctx, tx, 1, conn.ID, 0, 0, mappingBody())
					return e
				}
				_, e := m.saveConnectionOn(ctx, tx, 1, 0, 0, connectionBody(), "session")
				return e
			})
			require.Error(t, e)
			var after int64
			require.NoError(t, db.Table("test_effects").Count(&after).Error)
			require.Equal(t, before, after)
			if which != "insert" {
				require.NoError(t, db.Table(m.names.Connections.String()).Count(&after).Error)
				if which == "mapping-outbox" {
					require.EqualValues(t, 1, after)
					require.NoError(t, db.Table(m.names.ProjectMappings.String()).Count(&after).Error)
				}
				require.Zero(t, after)
			}
		})
	}
}
func TestAuthorityRejectsInactiveNonHumanCrossTenantAndNarrowGrant(t *testing.T) {
	for _, which := range []string{"tenant", "actor", "nil", "nontransaction", "lock", "human", "human-query", "member", "policy", "policy-error", "missing"} {
		t.Run(which, func(t *testing.T) {
			m, db, ctx := configHarness(t)
			switch which {
			case "tenant":
				ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11})
			case "actor":
				ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1})
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrForbidden }
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "human-query":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrForbidden
				}
			case "member":
				m.members = testMembers(false)
			case "policy":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "policy-error":
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrForbidden
				}
			case "missing":
				m.policy = nil
			}
			if which == "nil" {
				require.Error(t, m.authorityOn(ctx, nil, 1))
				return
			}
			if which == "nontransaction" {
				require.Error(t, m.authorityOn(ctx, db, 1))
				return
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.authorityOn(ctx, tx, 1) }))
		})
	}
}
func TestConfigurationValidationAndSingleMasterOutcomes(t *testing.T) {
	for _, mut := range []func(*ConnectionInput){func(i *ConnectionInput) { i.Name = " " }, func(i *ConnectionInput) { i.OrganizationID = "" }, func(i *ConnectionInput) { i.Token = strings.Repeat("x", 8001) }, func(i *ConnectionInput) { i.APIEndpoint = "http://example.invalid" }, func(i *ConnectionInput) { i.APIEndpoint = "https://user:pass@example.invalid" }, func(i *ConnectionInput) { i.APIEndpoint = "https://example.invalid?token=secret" }, func(i *ConnectionInput) { i.APIEndpoint = "https://example.invalid/#fragment" }, func(i *ConnectionInput) { i.APIEndpoint = "https://%" }} {
		in := connectionBody()
		mut(&in)
		require.ErrorIs(t, validateConnection(in), ErrInvalid)
	}
	require.NoError(t, validateConnection(ConnectionInput{Name: "local", OrganizationID: "org"}))
	require.False(t, textValid("\x00", 2))
	require.False(t, textValid(string([]byte{255}), 2))
	for _, mut := range []func(*MappingInput){func(i *MappingInput) { i.ProjectID = 0 }, func(i *MappingInput) { i.SyncMode = "other" }, func(i *MappingInput) { i.FieldMasters = map[string]string{"secret": "YUNXIAO"} }, func(i *MappingInput) { i.FieldMasters = map[string]string{"state": "other"} }, func(i *MappingInput) { i.CodeupRepositories = []string{"same", "same"} }, func(i *MappingInput) { i.TrustedPipelines = []string{""} }} {
		in := mappingBody()
		mut(&in)
		require.ErrorIs(t, validateMapping(in), ErrInvalid)
	}
	for _, direction := range []string{"IN", "OUT"} {
		for _, master := range []string{"", "MYRMIDON", "YUNXIAO"} {
			for _, both := range []bool{false, true} {
				out, e := SyncOutcome(map[string]string{"state": master}, "state", direction, both, both)
				require.NoError(t, e)
				owner := master
				if owner == "" {
					owner = "MYRMIDON"
				}
				want := "ELIGIBLE"
				if direction == "IN" && owner != "YUNXIAO" || direction == "OUT" && owner != "MYRMIDON" {
					want = "SKIPPED_NOT_MASTER"
				} else if both {
					want = "CONFLICT_MASTER_WINS"
				}
				require.Equal(t, want, out)
			}
		}
	}
	for _, args := range [][3]string{{"state", "BAD", ""}, {"bad", "IN", ""}, {"state", "IN", "bad"}} {
		_, e := SyncOutcome(map[string]string{args[0]: args[2]}, args[0], args[1], false, false)
		require.Error(t, e)
	}
}
func TestConnectionSecretErrorsAndDependencies(t *testing.T) {
	m, db, ctx := configHarness(t)
	for _, mut := range []func(*Module){func(m *Module) { m.proof = nil }, func(m *Module) { m.now = nil }, func(m *Module) { m.ids = nil }, func(m *Module) { m.audit = nil }, func(m *Module) { m.events = nil }} {
		candidate := *m
		mut(&candidate)
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := candidate.saveConnectionOn(ctx, tx, 1, 0, 0, connectionBody(), "session")
			return e
		}))
	}
	_, e := (&Module{}).nextID()
	require.Error(t, e)
	m.ids = &testIDs{n: -1}
	_, e = m.nextID()
	require.Error(t, e)
	for _, key := range []string{"", "bad"} {
		m.config.EncryptionKey = key
		_, e = m.keys()
		require.Error(t, e)
	}
	m.config.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	m.config.EncryptionKeyFile = "missing-file"
	_, e = m.keys()
	require.Error(t, e)
}

func TestShortCredentialsNeverEchoTheWholeSecret(t *testing.T) {
	m, db, ctx := configHarness(t)
	for _, secret := range []string{"a", "abcd", "\u4ee4\u724c"} {
		in := connectionBody()
		in.Token = secret
		var view ConnectionView
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			view, e = m.saveConnectionOn(ctx, tx, 1, 0, 0, in, "session")
			return e
		}))
		require.Equal(t, "****", view.TokenLast4)
		require.NotEqual(t, secret, view.TokenLast4)
	}
}

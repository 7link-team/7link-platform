package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func requireStatus(t *testing.T, e error, want int) {
	t.Helper()
	var status huma.StatusError
	require.ErrorAs(t, e, &status)
	require.Equal(t, want, status.GetStatus())
}
func TestHTTPCommandsRealReplayIsolationAndVersion(t *testing.T) {
	m, db, ctx := configHarness(t)
	in := &createConnectionInput{TenantInput: TenantInput{"1"}, WriteHeaders: WriteHeaders{IdempotencyKey: "once", Cookie: "session"}, Body: connectionBody()}
	first, e := m.createConnectionHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 201, first.Status)
	require.Equal(t, `"1"`, first.ETag)
	replay, e := m.createConnectionHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	c := first.Body.Data
	in.Body.Name = "changed"
	_, e = m.createConnectionHTTP(ctx, in)
	requireStatus(t, e, 422)
	update := &updateConnectionInput{ConnectionPath: ConnectionPath{TenantInput: TenantInput{"1"}, ConnectionID: c.ID.String()}, WriteHeaders: WriteHeaders{IdempotencyKey: "update", Cookie: "session", IfMatch: `"1"`}, Body: connectionBody()}
	update.Body.Disabled = true
	v, e := m.updateConnectionHTTP(ctx, update)
	require.NoError(t, e)
	require.Equal(t, `"2"`, v.ETag)
	again, e := m.updateConnectionHTTP(ctx, update)
	require.NoError(t, e)
	require.Equal(t, "true", again.Replayed)
	update.IdempotencyKey = "stale"
	_, e = m.updateConnectionHTTP(ctx, update)
	requireStatus(t, e, 409)
	mapping := &createMappingInput{ConnectionPath: ConnectionPath{TenantInput: TenantInput{"1"}, ConnectionID: c.ID.String()}, WriteHeaders: WriteHeaders{IdempotencyKey: "map"}, Body: mappingBody()}
	mapped, e := m.createMappingHTTP(ctx, mapping)
	require.NoError(t, e)
	require.Equal(t, 201, mapped.Status)
	repeated, e := m.createMappingHTTP(ctx, mapping)
	require.NoError(t, e)
	require.Equal(t, mapped.Body.Data, repeated.Body.Data)
	mr := mapped.Body.Data
	change := &updateMappingInput{MappingPath: MappingPath{ConnectionPath: mapping.ConnectionPath, MappingID: mr.ID.String()}, WriteHeaders: WriteHeaders{IdempotencyKey: "map-change", IfMatch: `"1"`}, Body: mappingBody()}
	change.Body.Paused = true
	updated, e := m.updateMappingHTTP(ctx, change)
	require.NoError(t, e)
	require.Equal(t, `"2"`, updated.ETag)
	_, e = m.listConnectionsHTTP(ctx, &ListInput{TenantInput: TenantInput{"2"}, Limit: 50})
	requireStatus(t, e, 403)
	// Current policy is rechecked before returning an old idempotency receipt.
	m.members = testMembers(false)
	in.Body = connectionBody()
	_, e = m.createConnectionHTTP(ctx, in)
	requireStatus(t, e, 403)
	var n int64
	require.NoError(t, db.Table(m.names.Connections.String()).Count(&n).Error)
	require.EqualValues(t, 1, n)
}
func TestHTTPListsStableCursorsAndUnavailableNeverCreateRemoteFacts(t *testing.T) {
	m, db, ctx := configHarness(t)
	c := createConnection(t, m, db, ctx)
	createConnection(t, m, db, ctx)
	page, e := m.listConnectionsHTTP(ctx, &ListInput{TenantInput: TenantInput{"1"}, Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.True(t, page.Body.Data.HasMore)
	next, e := m.listConnectionsHTTP(ctx, &ListInput{TenantInput: TenantInput{"1"}, Limit: 1, Cursor: page.Body.Data.NextCursor})
	require.NoError(t, e)
	require.Len(t, next.Body.Data.Items, 1)
	require.False(t, next.Body.Data.HasMore)
	require.NotEqual(t, page.Body.Data.Items[0].ID, next.Body.Data.Items[0].ID)
	var mapping MappingView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		mapping, e = m.saveMappingOn(ctx, tx, 1, c.ID, 0, 0, mappingBody())
		return e
	}))
	// Seed another authorized mapping to verify continuation uses stable keys.
	in := mappingBody()
	in.ExternalProjectID = "external-2"
	m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }
	in.ProjectID = 100
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveMappingOn(ctx, tx, 1, c.ID, 0, 0, in); return e }))
	maps, e := m.listMappingsHTTP(ctx, &mappingListInput{ConnectionPath: ConnectionPath{TenantInput: TenantInput{"1"}, ConnectionID: c.ID.String()}, Limit: 1})
	require.NoError(t, e)
	require.True(t, maps.Body.Data.HasMore)
	more, e := m.listMappingsHTTP(ctx, &mappingListInput{ConnectionPath: ConnectionPath{TenantInput: TenantInput{"1"}, ConnectionID: c.ID.String()}, Limit: 1, Cursor: maps.Body.Data.NextCursor})
	require.NoError(t, e)
	require.False(t, more.Body.Data.HasMore)
	require.Len(t, more.Body.Data.Items, 1)
	for _, id := range []string{c.ID.String(), "999"} {
		_, e = m.remoteConnectionHTTP(ctx, &ConnectionPath{TenantInput: TenantInput{"1"}, ConnectionID: id})
		if id == "999" {
			requireStatus(t, e, 404)
		} else {
			requireStatus(t, e, 503)
		}
	}
	path := MappingPath{ConnectionPath: ConnectionPath{TenantInput: TenantInput{"1"}, ConnectionID: c.ID.String()}, MappingID: mapping.ID.String()}
	_, e = m.remoteMappingHTTP(ctx, &path)
	requireStatus(t, e, 503)
	path.ConnectionID = "999"
	_, e = m.remoteMappingHTTP(ctx, &path)
	requireStatus(t, e, 404)
	for _, name := range []string{m.names.ImportJobs.String(), m.names.ExternalRefs.String(), m.names.InboundEvents.String(), m.names.SyncLogs.String(), m.names.StateMappings.String()} {
		var count int64
		require.NoError(t, db.Table(name).Count(&count).Error)
		require.Zero(t, count)
	}
	row, e := query.BindConnections(db, m.names).WithContext(ctx).First()
	require.NoError(t, e)
	require.Equal(t, "PENDING_CHECK", row.State)
	require.Nil(t, row.LastCheckedAt)
}
func TestHTTPInputAndDatasourceFailures(t *testing.T) {
	m, _, ctx := configHarness(t)
	for _, s := range []string{"", "1", "W/\"1\"", "\"0\"", "\"x\""} {
		_, e := versionHeader(s)
		require.Error(t, e)
	}
	_, e := versionHeader(`"2"`)
	require.NoError(t, e)
	for _, s := range []string{"%", pageCursor(2, 0, 1), pageCursor(1, 2, 1), "MTowOng"} {
		_, e := parseCursor(s, 1, 0)
		require.Error(t, e)
	}
	_, e = parseCursor(pageCursor(1, 0, 3), 1, 0)
	require.NoError(t, e)
	for _, cause := range []error{ErrInvalid, idempotency.ErrInvalidKey, idempotency.ErrKeyReused, ErrConflict, gorm.ErrDuplicatedKey, idempotency.ErrInProgress, ErrNotFound, delivery.ErrProjectNotFound, ErrForbidden, delivery.ErrWorkForbidden, ErrUnavailable, idempotency.ErrUnavailable, errors.New("internal")} {
		require.Error(t, httpError(ctx, cause))
	}
	requireStatus(t, httpError(ctx, httpError(ctx, ErrForbidden)), 403)
	for _, tenant := range []string{"bad", "1"} {
		bare := NewModule(nil, Config{})
		_, e := bare.createConnectionHTTP(ctx, &createConnectionInput{TenantInput: TenantInput{tenant}})
		require.Error(t, e)
		_, e = bare.listConnectionsHTTP(ctx, &ListInput{TenantInput: TenantInput{tenant}, Limit: 1})
		require.Error(t, e)
		_, e = bare.listMappingsHTTP(ctx, &mappingListInput{ConnectionPath: ConnectionPath{TenantInput: TenantInput{tenant}, ConnectionID: "1"}, Limit: 1})
		require.Error(t, e)
		_, e = bare.remoteConnectionHTTP(ctx, &ConnectionPath{TenantInput: TenantInput{tenant}, ConnectionID: "1"})
		require.Error(t, e)
		_, e = bare.remoteMappingHTTP(ctx, &MappingPath{ConnectionPath: ConnectionPath{TenantInput: TenantInput{tenant}, ConnectionID: "1"}, MappingID: "1"})
		require.Error(t, e)
	}
	for _, limit := range []int{0, 101} {
		_, e := m.listConnectionsHTTP(ctx, &ListInput{TenantInput: TenantInput{"1"}, Limit: limit})
		requireStatus(t, e, 422)
		_, e = m.listMappingsHTTP(ctx, &mappingListInput{ConnectionPath: ConnectionPath{TenantInput: TenantInput{"1"}, ConnectionID: "1"}, Limit: limit})
		requireStatus(t, e, 422)
	}
	_, e = m.createConnectionHTTP(ctx, &createConnectionInput{TenantInput: TenantInput{"1"}, WriteHeaders: WriteHeaders{IdempotencyKey: "invalid key"}})
	requireStatus(t, e, 422)
}
func TestRegisterHTTPContainsGuardedContracts(t *testing.T) {
	m, _, ctx := configHarness(t)
	m.registrar = authz.NewRegistrar(authz.DefaultRegistry(), testAuth{}, testAuth{}, testAuth{})
	require.NoError(t, m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "integration", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "worklog", Verb: "submit", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "worklog", Verb: "approve", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "workflow", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "intake", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "intake", Verb: "submit", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "intake_channel", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true},
	}, nil))
	_, api := humatest.New(t)
	require.NoError(t, m.RegisterHTTP(ctx, api))
	require.NotNil(t, api.OpenAPI().Paths["/tenants/{tenant_id}/integration-connections"].Options)
	for _, path := range []string{
		"/tenants/{tenant_id}/projects/{project_id}/worklogs",
		"/tenants/{tenant_id}/projects/{project_id}/worklogs/{worklog_id}",
		"/tenants/{tenant_id}/projects/{project_id}/worklogs/{worklog_id}/state",
		"/tenants/{tenant_id}/projects/{project_id}/workflow-configs/{work_item_type}",
		"/tenants/{tenant_id}/projects/{project_id}/intake-channels/API/credential",
		"/tenants/{tenant_id}/projects/{project_id}/intake-channels/API/submissions",
	} {
		require.NotNil(t, api.OpenAPI().Paths[path], path)
		require.NotNil(t, api.OpenAPI().Paths[path].Options, path)
	}
	require.Error(t, (&Module{}).RegisterHTTP(ctx, api))
}

func TestProofFailuresMapToForbiddenOrUnavailable(t *testing.T) {
	m, _, ctx := configHarness(t)
	for _, fault := range []struct {
		err    error
		status int
	}{{authn.ErrInvalidCredentials, 403}, {authn.ErrInvalidSession, 403}, {authn.ErrSessionNotFound, 403}, {authn.ErrUnavailable, 503}} {
		m.proof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return fault.err }
		_, e := m.createConnectionHTTP(ctx, &createConnectionInput{TenantInput: TenantInput{"1"}, WriteHeaders: WriteHeaders{IdempotencyKey: "proof-errors", Cookie: "session"}, Body: connectionBody()})
		requireStatus(t, e, fault.status)
	}
}

func TestIntegrationHTTPRejectsMalformedScopeAndMissingCurrentResources(t *testing.T) {
	m, _, ctx := configHarness(t)
	for _, path := range []ConnectionPath{{TenantInput: TenantInput{TenantID: "0"}, ConnectionID: "1"}, {TenantInput: TenantInput{TenantID: "1"}, ConnectionID: "0"}} {
		_, e := m.updateConnectionHTTP(ctx, &updateConnectionInput{ConnectionPath: path})
		require.Error(t, e)
		_, e = m.createMappingHTTP(ctx, &createMappingInput{ConnectionPath: path})
		require.Error(t, e)
		_, e = m.remoteConnectionHTTP(ctx, &path)
		require.Error(t, e)
	}
	path := ConnectionPath{TenantInput: TenantInput{TenantID: "1"}, ConnectionID: "999"}
	_, e := m.updateConnectionHTTP(ctx, &updateConnectionInput{ConnectionPath: path, WriteHeaders: WriteHeaders{IfMatch: "invalid"}})
	require.Error(t, e)
	for _, p := range []MappingPath{{ConnectionPath: ConnectionPath{TenantInput: TenantInput{TenantID: "0"}, ConnectionID: "1"}, MappingID: "1"}, {ConnectionPath: ConnectionPath{TenantInput: TenantInput{TenantID: "1"}, ConnectionID: "0"}, MappingID: "1"}, {ConnectionPath: path, MappingID: "0"}} {
		_, e = m.updateMappingHTTP(ctx, &updateMappingInput{MappingPath: p})
		require.Error(t, e)
		_, e = m.remoteMappingHTTP(ctx, &p)
		require.Error(t, e)
	}
	p := MappingPath{ConnectionPath: path, MappingID: "999"}
	_, e = m.updateMappingHTTP(ctx, &updateMappingInput{MappingPath: p, WriteHeaders: WriteHeaders{IfMatch: "invalid"}})
	require.Error(t, e)
	_, e = m.listMappingsHTTP(ctx, &mappingListInput{ConnectionPath: path, Limit: 1})
	require.Error(t, e)
	_, e = m.listMappingsHTTP(ctx, &mappingListInput{ConnectionPath: path, Limit: 1, Cursor: "malformed"})
	require.Error(t, e)
	m.members = testMembers(false)
	_, e = m.listMappingsHTTP(ctx, &mappingListInput{ConnectionPath: path, Limit: 1})
	require.Error(t, e)
	_, e = m.remoteConnectionHTTP(ctx, &path)
	require.Error(t, e)
	_, e = m.remoteMappingHTTP(ctx, &p)
	require.Error(t, e)
	for _, domainErr := range []error{ErrVersionLocked, ErrIterationClosed} {
		requireStatus(t, httpError(ctx, domainErr), 409)
	}
}

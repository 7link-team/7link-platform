package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestArchiveHTTPRegisteredFlowAndRequiredHeaders(t *testing.T) {
	m, _, ctx := archiveHTTPFixture(t)
	registry, err := authz.NewRegistry(authz.Action{Resource: "project", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true})
	require.NoError(t, err)
	guard := resultHTTPGuard{ctx: ctx}
	m.registrar = authz.NewRegistrar(registry, guard, guard, guard)
	m.entityOwner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{ScopeType: "tenant", TenantID: 1, EntityID: 99}, true, nil
	}
	_, api := humatest.New(t)
	require.NoError(t, m.registerProjectArchiveHTTP(api))
	preflight := api.Get("/entities/99/project/archive-preflight")
	require.Equal(t, 200, preflight.Code, preflight.Body.String())
	var receipt struct {
		Data projectArchivePreflight `json:"data"`
	}
	require.NoError(t, json.Unmarshal(preflight.Body.Bytes(), &receipt))
	require.True(t, receipt.Data.Ready)
	body := map[string]any{"reason": "Preserve completed project history", "preflight_hash": receipt.Data.Hash}
	path := "/entities/99/project/archive"
	require.Equal(t, 422, api.Post(path, body).Code)
	require.Equal(t, 422, api.Post(path, "If-Match: "+fmtETag(receipt.Data.Version), body).Code)
	archived := api.Post(path, "If-Match: "+fmtETag(receipt.Data.Version), "Idempotency-Key: registered-archive", body)
	require.Equal(t, 200, archived.Code, archived.Body.String())
	var project struct {
		Data ProjectView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(archived.Body.Bytes(), &project))
	require.NotNil(t, project.Data.ArchivedAt)
	replayed := api.Post(path, "If-Match: "+fmtETag(receipt.Data.Version), "Idempotency-Key: registered-archive", body)
	require.Equal(t, 200, replayed.Code, replayed.Body.String())
	require.Equal(t, "true", replayed.Header().Get("Idempotent-Replayed"))
	restored := api.Post("/entities/99/project/restore", "If-Match: "+fmtETag(project.Data.Version), "Idempotency-Key: registered-restore", map[string]any{"reason": "Explicitly restore historical project"})
	require.Equal(t, 200, restored.Code, restored.Body.String())
	require.NoError(t, json.Unmarshal(restored.Body.Bytes(), &project))
	require.Nil(t, project.Data.ArchivedAt)
}

func archiveHTTPFixture(t *testing.T) (*Module, *gorm.DB, context.Context) {
	t.Helper()
	m, db, ctx := constitutionHarness(t)
	m.archiveSources = projectArchiveSources{}
	for _, owner := range []string{"execution", "fleet", "governance", "jobs", "planning", "agents", "clientwork", "knowledge"} {
		m.archiveSources[owner] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) {
			return []ProjectArchiveResource{}, nil
		}
	}
	return m, db, ctx
}
func TestArchiveHTTPPreflightArchiveRestoreAndReplayCurrentAuthority(t *testing.T) {
	m, db, ctx := archiveHTTPFixture(t)
	checked, err := m.projectArchivePreflightHTTP(ctx, &stageReadinessInput{EntityID: "99"})
	require.NoError(t, err)
	require.True(t, checked.Body.Data.Ready)
	in := &projectArchiveInput{EntityID: "99", IfMatch: fmtETag(checked.Body.Data.Version), IdempotencyKey: "archive-project-once", Body: projectArchiveBody{Reason: "Preserve the project history", PreflightHash: checked.Body.Data.Hash}}
	first, err := m.archiveProjectHTTP(ctx, in)
	require.NoError(t, err)
	require.NotNil(t, first.Body.Data.ArchivedAt)
	replayed, err := m.archiveProjectHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, first.Body.Data.Version, replayed.Body.Data.Version)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.projectHistoryAuthorityOn(ctx, tx, 1, 99, 11, "project_read") }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return (clientWork{m: m}).LockProjectOn(ctx, tx, 1, 99, "project_read") }))
	policy := m.workPolicy
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.archiveProjectHTTP(ctx, in)
	assertWorkStatus(t, err, 403)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.projectHistoryAuthorityOn(ctx, tx, 1, 99, 11, "project_read") }), ErrWorkForbidden)
	m.workPolicy = policy
	restored, err := m.projectArchiveCommandHTTP(ctx, "99", fmtETag(first.Body.Data.Version), "restore-project-once", true, projectArchiveBody{Reason: "Explicitly reopen project access"})
	require.NoError(t, err)
	require.Nil(t, restored.Body.Data.ArchivedAt)
	require.Equal(t, first.Body.Data.Stage, restored.Body.Data.Stage)
	_, err = m.projectArchiveCommandHTTP(ctx, "99", fmtETag(first.Body.Data.Version), "restore-project-once", true, projectArchiveBody{Reason: "Different reason cannot reuse a key"})
	assertWorkStatus(t, err, 422)
}
func TestArchiveHTTPFailureRollsBackProjectAndRejectsStaleReceipts(t *testing.T) {
	m, db, ctx := archiveHTTPFixture(t)
	checked, err := m.projectArchivePreflightHTTP(ctx, &stageReadinessInput{EntityID: "99"})
	require.NoError(t, err)
	in := &projectArchiveInput{EntityID: "99", IfMatch: fmtETag(checked.Body.Data.Version), IdempotencyKey: "archive-project-failure", Body: projectArchiveBody{Reason: "Preserve project history", PreflightHash: checked.Body.Data.Hash}}
	auditFn := m.service.audit
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit write failed") }
	_, err = m.archiveProjectHTTP(ctx, in)
	assertWorkStatus(t, err, 500)
	p := query.BindProjects(db, m.service.names)
	row, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
	require.NoError(t, err)
	require.Nil(t, row.ArchivedAt)
	require.Equal(t, checked.Body.Data.Version, row.Version)
	m.service.audit = auditFn
	in.IdempotencyKey = "archive-project-stale"
	m.archiveSources["execution"] = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ProjectArchiveResource, error) {
		return []ProjectArchiveResource{{Kind: "EXECUTION", ID: 500, State: "RUNNING", Version: 1}}, nil
	}
	_, err = m.archiveProjectHTTP(ctx, in)
	assertWorkStatus(t, err, 409)
	for _, id := range []string{"0", "invalid"} {
		_, err = m.projectArchivePreflightHTTP(ctx, &stageReadinessInput{EntityID: id})
		assertWorkStatus(t, err, 422)
	}
	_, err = m.projectArchiveCommandHTTP(ctx, "99", "bad", "key", false, in.Body)
	assertWorkStatus(t, err, 422)
	_, err = m.projectArchiveCommandHTTP(ctx, "99", `"1"`, "", false, in.Body)
	assertWorkStatus(t, err, 422)
}

package delivery

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectHTTPReadsPreserveScopeETagAndFailureStatus(t *testing.T) {
	m, db, ctx, _ := workHTTPHarness(t)
	m.service.entities.GetOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (core.Entity, error) {
		return core.Entity{ID: id, TenantID: tenant, Type: "project", Name: "Current name"}, nil
	}
	got, err := m.getProjectHTTP(ctx, &projectGetInput{EntityID: "99"})
	require.NoError(t, err)
	require.Equal(t, `"1"`, got.ETag)
	require.Equal(t, "Current name", got.Body.Data.Name)
	_, err = m.getProjectHTTP(ctx, &projectGetInput{EntityID: "100"})
	assertWorkStatus(t, err, 404)
	_, err = m.getProjectHTTP(ctx, &projectGetInput{EntityID: "0"})
	assertWorkStatus(t, err, 422)
	m.projectAccess = core.PublishedAccessTableNames("d_")
	require.NoError(t, db.Exec("CREATE TABLE d_iam_entities (tenant_id INTEGER,id INTEGER,name TEXT,type TEXT)").Error)
	require.NoError(t, db.Exec("INSERT INTO d_iam_entities VALUES (1,99,'Current name','project')").Error)
	m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	page, err := m.listProjectsHTTP(ctx, &projectListHTTPInput{Limit: 50})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Body.Meta.Page.Total)
	require.Len(t, page.Body.Data, 1)
	_, err = m.listProjectsHTTP(ctx, &projectListHTTPInput{Limit: 50, RawQuery: url.Values{"name": {"eq:invalid-operator"}}})
	assertWorkStatus(t, err, 422)
	m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, ErrProjectForbidden
	}
	_, err = m.listProjectsHTTP(ctx, &projectListHTTPInput{Limit: 50})
	assertWorkStatus(t, err, 403)
	_, err = m.createProjectHTTP(ctx, &projectCreateInput{TenantID: "1", IdempotencyKey: "invalid key"})
	assertWorkStatus(t, err, 400)
	m.idempotency = nil
	_, err = m.createProjectHTTP(ctx, &projectCreateInput{TenantID: "1", IdempotencyKey: "valid-key"})
	assertWorkStatus(t, err, 503)
	m.runtime = nil
	_, err = m.getProjectHTTP(ctx, &projectGetInput{EntityID: "99"})
	assertWorkStatus(t, err, 503)
	_, err = m.listProjectsHTTP(ctx, &projectListHTTPInput{Limit: 50})
	assertWorkStatus(t, err, 503)
	_, err = m.createProjectHTTP(ctx, &projectCreateInput{TenantID: "1"})
	assertWorkStatus(t, err, 503)
	_, err = m.createProjectHTTP(ctx, &projectCreateInput{TenantID: "invalid"})
	assertWorkStatus(t, err, 422)
}

func TestProjectHTTPAtomicReplayAndCurrentAuthorization(t *testing.T) {
	for _, scenario := range []string{"success", "denied", "nonhuman", "channel_failure", "event_failure", "code_taken"} {
		for _, keyed := range []bool{false, true} {
			t.Run(scenario+map[bool]string{false: "/unkeyed", true: "/keyed"}[keyed], func(t *testing.T) {
				m, db, ctx, _ := workHTTPHarness(t)
				m.sources = intake.IntakeReader{
					GetOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
						return intake.IntakeRef{}, false, nil
					},
					ContactsOn: func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
						return nil, nil
					},
				}
				m.roles = governance.SeededRoles{
					EnsureTenant: func(context.Context, guid.ID) error { return nil },
					RoleIDOn:     func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 6, nil },
				}
				m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{ID: id}, scenario != "nonhuman", nil
				}
				m.createAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: scenario != "denied", AuthoritySource: "permission:project_manage"}, nil
				}
				m.service.entities.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) { return core.Entity{}, nil }
				m.service.entities.CreateOn = func(_ context.Context, tx *gorm.DB, e core.Entity) (core.Entity, error) {
					e.ID = 100
					return e, tx.Exec("INSERT INTO provision_probe VALUES ('entity')").Error
				}
				m.service.roles.PutOn = func(_ context.Context, tx *gorm.DB, _, _, _, _ guid.ID, _ core.BindingEffect, _ time.Time) (core.EntityRoleBinding, bool, error) {
					return core.EntityRoleBinding{}, true, tx.Exec("INSERT INTO provision_probe VALUES ('binding')").Error
				}
				failure := errors.New("private dependency error")
				m.service.channels.CreateDefaultOn = func(_ context.Context, tx *gorm.DB, _ collab.DefaultChannelInput) (guid.ID, error) {
					if scenario == "channel_failure" {
						return 0, failure
					}
					return 8, tx.Exec("INSERT INTO provision_probe VALUES ('channel')").Error
				}
				if scenario == "event_failure" {
					m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
				}
				api, body := projectContractAPI(t)
				require.Equal(t, 200, api.Post("/projects", strings.NewReader(`{"name":"HTTP project","code":"HTTP"}`)).Code)
				if scenario == "code_taken" {
					require.NoError(t, db.Table(m.service.names.Projects.String()).Select("code").Where("project_id=99").Scan(&body.Code).Error)
				}
				in := &projectCreateInput{TenantID: "1", Body: *body}
				if keyed {
					in.IdempotencyKey = "project-http-once"
				}
				first, err := m.createProjectHTTP(ctx, in)
				if scenario != "success" {
					status := 500
					if scenario == "denied" || scenario == "nonhuman" {
						status = 403
					}
					if scenario == "code_taken" {
						status = 409
					}
					assertWorkStatus(t, err, status)
					if keyed && scenario == "code_taken" {
						_, err = m.createProjectHTTP(ctx, in)
						assertWorkStatus(t, err, 409)
					}
					var count int64
					require.NoError(t, db.Table("provision_probe").Count(&count).Error)
					require.Zero(t, count, "no partial entity/binding/channel may survive")
					require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id = 100").Count(&count).Error)
					require.Zero(t, count)
					return
				}
				require.NoError(t, err)
				require.Equal(t, 201, first.Status)
				require.EqualValues(t, 100, first.Body.Data.Project.ProjectID)
				if keyed {
					replayed, err := m.createProjectHTTP(ctx, in)
					require.NoError(t, err)
					require.Equal(t, "true", replayed.Replayed)
					require.Equal(t, first.Body, replayed.Body)
					in.Body.Code = "OTHER"
					_, err = m.createProjectHTTP(ctx, in)
					assertWorkStatus(t, err, 422)
				}
				var count int64
				require.NoError(t, db.Table("provision_probe").Count(&count).Error)
				require.EqualValues(t, 3, count)
			})
		}
	}
}

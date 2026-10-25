package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestDependencyHTTPReplayPagesAndIsolation(t *testing.T) {
	m, _, ctx, work, predecessor := dependencyHarness(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	in := &dependencyCreateInput{EntityID: "99", WorkItemID: work.ID.String(), DependsOnID: predecessor.ID.String()}
	first, e := m.createDependencyHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 201, first.Status)
	replay, e := m.createDependencyHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 200, replay.Status)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	page, e := m.listDependenciesHTTP(ctx, &dependencyListInput{EntityID: "99", WorkItemID: work.ID.String(), Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Body.Data, 1)
	page, e = m.listDependenciesHTTP(ctx, &dependencyListInput{EntityID: "99", WorkItemID: predecessor.ID.String(), Direction: "downstream", Limit: 50})
	require.NoError(t, e)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, work.ID, page.Body.Data[0].ID)
	page, e = m.listDependenciesHTTP(ctx, &dependencyListInput{EntityID: "99", WorkItemID: work.ID.String(), Offset: 1, Limit: 1})
	require.NoError(t, e)
	require.Empty(t, page.Body.Data)
	_, e = m.listDependenciesHTTP(authn.WithClaims(ctx, &authn.Claims{TenantID: 2, EntityID: 99, PrincipalID: 11}), &dependencyListInput{EntityID: "99", WorkItemID: work.ID.String(), Limit: 50})
	require.Error(t, e)
	_, e = m.deleteDependencyHTTP(ctx, in)
	require.NoError(t, e)
	_, e = m.deleteDependencyHTTP(ctx, in)
	require.NoError(t, e)
}

func TestDependencyHTTPNaturalIdempotencyAndCurrentAuthority(t *testing.T) {
	m, db, ctx, work, predecessor := dependencyHarness(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, api := humatest.New(t)
	path := dependenciesPath + "/{depends_on_id}"
	huma.Register(api, huma.Operation{OperationID: "dependency-put-contract", Method: "PUT", Path: path, DefaultStatus: 201}, func(_ context.Context, in *dependencyCreateInput) (*deliveryCommandOutput[dependencyReceipt], error) {
		return m.createDependencyHTTP(ctx, in)
	})
	huma.Register(api, huma.Operation{OperationID: "dependency-delete-contract", Method: "DELETE", Path: path, DefaultStatus: 204}, func(_ context.Context, in *dependencyDeleteInput) (*struct{}, error) {
		return m.deleteDependencyHTTP(ctx, in)
	})
	url := "/entities/99/work-items/" + work.ID.String() + "/dependencies/" + predecessor.ID.String()
	require.Equal(t, 201, api.Put(url).Code)
	require.Equal(t, 200, api.Put(url).Code)
	current, e := m.readWork(ctx, db, 1, 99, work.ID, 11)
	require.NoError(t, e)
	require.Equal(t, work.Version+1, current.Version)
	m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "work_item_read"}, nil
	}
	require.Equal(t, 404, api.Put(url).Code)
	require.Equal(t, 404, api.Delete(url).Code)
	count, e := query.BindDependencies(db, m.service.names).WithContext(ctx).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, count)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.Equal(t, 204, api.Delete(url).Code)
	require.Equal(t, 204, api.Delete(url).Code)
	current, e = m.readWork(ctx, db, 1, 99, work.ID, 11)
	require.NoError(t, e)
	require.Equal(t, work.Version+2, current.Version)
}

func TestDependencyHTTPCycleIncludesPath(t *testing.T) {
	m, _, ctx, work, predecessor := dependencyHarness(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, err := m.createDependencyHTTP(ctx, &dependencyCreateInput{EntityID: "99", WorkItemID: work.ID.String(), DependsOnID: predecessor.ID.String()})
	require.NoError(t, err)
	_, err = m.createDependencyHTTP(ctx, &dependencyCreateInput{EntityID: "99", WorkItemID: predecessor.ID.String(), DependsOnID: work.ID.String()})
	require.Error(t, err)
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 422, status.GetStatus())
	body, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Cycle []string `json:"cycle"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	require.Equal(t, 120422, envelope.Code)
	require.Len(t, envelope.Data.Cycle, 3)
	require.Equal(t, envelope.Data.Cycle[0], envelope.Data.Cycle[2])
}

func TestDependencyHTTPCycleDoesNotDiscloseUnreadableWork(t *testing.T) {
	m, db, ctx, work, predecessor := dependencyHarness(t)
	_, _, _, body := workHTTPHarness(t)
	var hidden workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		hidden, err = m.createWorkOn(ctx, tx, 1, 99, 11, body)
		return err
	}))
	w := query.BindWorkItems(db, m.service.names)
	_, err := w.WithContext(ctx).Where(w.ID.Eq(hidden.ID)).Update(w.CreatedBy, guid.ID(22))
	require.NoError(t, err)
	q := query.BindDependencies(db, m.service.names)
	require.NoError(t, q.WithContext(ctx).Create(
		&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: predecessor.ID, DependsOnID: hidden.ID},
		&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: hidden.ID, DependsOnID: work.ID},
	))
	m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		if permission == "work_item_read" {
			return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
		}
		return authz.DataConstraint{AllowAll: true}, nil
	}
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, err = m.createDependencyHTTP(ctx, &dependencyCreateInput{EntityID: "99", WorkItemID: work.ID.String(), DependsOnID: predecessor.ID.String()})
	require.Error(t, err)
	encoded, marshalErr := json.Marshal(err)
	require.NoError(t, marshalErr)
	var envelope struct {
		Code int `json:"code"`
		Data any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(encoded, &envelope))
	require.Equal(t, 120422, envelope.Code)
	require.Nil(t, envelope.Data)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count, "rejected cyclic relation must not persist")
}

func TestDependencyHTTPCrossProjectOnlyDisclosedWhenReadable(t *testing.T) {
	for _, scenario := range []string{"visible", "hidden", "other_tenant"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work, predecessor := dependencyHarness(t)
			p := query.BindProjects(db, m.service.names)
			foreignProject, err := p.WithContext(ctx).First()
			require.NoError(t, err)
			foreignProject.ProjectID, foreignProject.Code = 100, "FOREIGN"
			if scenario == "other_tenant" {
				foreignProject.TenantID = 2
			}
			require.NoError(t, p.WithContext(ctx).Create(foreignProject))
			w := query.BindWorkItems(db, m.service.names)
			foreignWork, err := w.WithContext(ctx).Where(w.ID.Eq(predecessor.ID)).First()
			require.NoError(t, err)
			foreignWork.ID += 1000
			foreignWork.ProjectID, foreignWork.TenantID = foreignProject.ProjectID, foreignProject.TenantID
			require.NoError(t, w.WithContext(ctx).Create(foreignWork))
			m.workPolicy = func(_ context.Context, _ *gorm.DB, _, project guid.ID, _ string, _ guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: scenario != "hidden" || project == 99}, nil
			}
			ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
			_, err = m.createDependencyHTTP(ctx, &dependencyCreateInput{EntityID: "99", WorkItemID: work.ID.String(), DependsOnID: foreignWork.ID.String()})
			require.Error(t, err)
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			if scenario == "visible" {
				require.Equal(t, 422, status.GetStatus())
				body, marshalErr := json.Marshal(err)
				require.NoError(t, marshalErr)
				var envelope struct {
					Code int `json:"code"`
				}
				require.NoError(t, json.Unmarshal(body, &envelope))
				require.Equal(t, 120451, envelope.Code)
			} else {
				require.Equal(t, 404, status.GetStatus())
			}
			count, err := query.BindDependencies(db, m.service.names).WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestDependencyHTTPValidationAndUnavailable(t *testing.T) {
	m := &Module{}
	ctx := t.Context()
	for _, in := range []*dependencyListInput{{EntityID: "bad", WorkItemID: "1", Limit: 50}, {EntityID: "1", WorkItemID: "1", Limit: 201}, {EntityID: "1", WorkItemID: "1", Limit: 50, Direction: "unknown"}, {EntityID: "1", WorkItemID: "1", Limit: 50, Offset: -1}} {
		_, err := m.listDependenciesHTTP(ctx, in)
		assertWorkStatus(t, err, 422)
	}
	_, err := m.listDependenciesHTTP(ctx, &dependencyListInput{EntityID: "1", WorkItemID: "1", Limit: 50})
	assertWorkStatus(t, err, 503)
	for _, bad := range []dependencyCreateInput{{EntityID: "bad", WorkItemID: "1", DependsOnID: "2"}, {EntityID: "1", WorkItemID: "bad", DependsOnID: "2"}, {EntityID: "1", WorkItemID: "1", DependsOnID: "bad"}} {
		_, err := m.createDependencyHTTP(ctx, &bad)
		assertWorkStatus(t, err, 422)
	}
	_, err = m.createDependencyHTTP(ctx, &dependencyCreateInput{EntityID: "1", WorkItemID: "1", DependsOnID: "2"})
	assertWorkStatus(t, err, 503)
	m, _, ctx, _, _ = dependencyHarness(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11})
	_, err = m.createDependencyHTTP(ctx, &dependencyCreateInput{EntityID: "99", WorkItemID: "999", DependsOnID: "2"})
	assertWorkStatus(t, err, 404)
}

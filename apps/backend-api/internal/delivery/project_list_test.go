package delivery

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
)

func TestProjectListFiltersBeforeCountingAndPaging(t *testing.T) {
	s, db, draft := projectHarness(t)
	names := core.PublishedAccessTableNames("d_")
	require.NoError(t, db.Exec("CREATE TABLE d_iam_entities (tenant_id INTEGER NOT NULL,id INTEGER NOT NULL,name TEXT NOT NULL,type TEXT NOT NULL,PRIMARY KEY(tenant_id,id))").Error)
	require.NoError(t, db.Exec("INSERT INTO d_iam_entities VALUES (1,98,'Customer','client')").Error)
	for _, row := range []struct {
		id, tenant, owner guid.ID
		name, health      string
		archived          bool
	}{
		{101, 1, 11, "Alpha", "GREEN", false}, {102, 1, 12, "Beta", "AMBER", false}, {103, 1, 11, "Gamma", "RED", false}, {104, 1, 11, "Archived", "NONE", true}, {201, 2, 11, "Foreign", "GREEN", false},
	} {
		p := draft.project
		p.ProjectID, p.TenantID, p.CreatedBy = row.id, row.tenant, &row.owner
		p.Code = "P" + row.id.String()
		p.Health = row.health
		if row.id == 101 {
			client := guid.ID(98)
			p.ClientEntityID = &client
		}
		if row.archived {
			now := time.Now().UTC()
			p.ArchivedAt = &now
			p.Stage = "CLOSED"
			p.ClosedAt = &now
		}
		require.NoError(t, query.BindProjects(db, s.names).WithContext(t.Context()).Create(&p))
		require.NoError(t, db.Exec("INSERT INTO d_iam_entities VALUES (?,?,?,'project')", row.tenant, row.id, row.name).Error)
	}
	all := authz.DataConstraint{AllowAll: true}
	caller := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11})
	m := &Module{service: s, projectAccess: names, listConstraint: func(_ context.Context, tenant guid.ID, permission string, principal guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, "project_read", permission)
		require.Equal(t, guid.ID(11), principal)
		return authz.DataConstraint{ResourceIDs: []string{"102"}}, nil
	}}
	scoped, err := m.listProjectRecords(caller, db, projectListInput{Limit: 100})
	require.NoError(t, err)
	require.Equal(t, int64(1), scoped.Total)
	require.Equal(t, "Beta", scoped.Records[0].Name)
	_, err = m.listProjectRecords(t.Context(), db, projectListInput{Limit: 100})
	require.ErrorIs(t, err, ErrProjectForbidden)
	injected := errors.New("policy unavailable")
	m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, injected
	}
	_, err = m.listProjectRecords(caller, db, projectListInput{Limit: 100})
	require.ErrorIs(t, err, injected)

	list := func(c authz.DataConstraint, raw string, offset, limit int) projectRecordPage {
		t.Helper()
		values, err := url.ParseQuery(raw)
		require.NoError(t, err)
		page, err := s.listRecords(t.Context(), db, names, 1, c, projectListInput{RawQuery: values, Sort: "name", Offset: offset, Limit: limit})
		require.NoError(t, err)
		return page
	}
	page := list(all, "", 0, 1)
	require.Equal(t, int64(3), page.Total)
	require.Len(t, page.Records, 1)
	require.Equal(t, "Alpha", page.Records[0].Name)
	require.Equal(t, &projectClientView{ID: 98, Name: "Customer"}, page.Records[0].Client)
	require.Equal(t, "Beta", list(all, "", 1, 1).Records[0].Name)
	require.Equal(t, int64(1), list(all, "archived=eq:true", 0, 100).Total)
	require.Equal(t, int64(1), list(all, "health=in:AMBER,RED&name=like:amm", 0, 100).Total)
	require.Equal(t, int64(1), list(all, "client_entity_id=eq:98", 0, 100).Total)
	require.Equal(t, int64(1), list(all, "code=eq:P102&stage=in:INTAKE,ACTIVE&team_mode=eq:HYBRID", 0, 100).Total)
	narrowed := authz.DataConstraint{OwnerIDs: []string{"11"}, ResourceIDs: []string{"102"}, DeniedIDs: []string{"103"}}
	page = list(narrowed, "", 0, 100)
	require.Equal(t, int64(2), page.Total)
	require.Equal(t, []string{"Alpha", "Beta"}, []string{page.Records[0].Name, page.Records[1].Name})
	require.Equal(t, int64(2), list(authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"102"}}, "", 0, 100).Total)
	require.Empty(t, list(authz.DataConstraint{}, "", 0, 100).Records)
	require.Empty(t, list(authz.DataConstraint{DepartmentIDs: []string{"7"}}, "", 0, 100).Records)
	require.Empty(t, list(authz.DataConstraint{ResourceIDs: []string{"201"}}, "", 0, 100).Records)
	require.Equal(t, int64(3), list(all, "", 100, 100).Total)
	for _, raw := range []string{"stage=eq:INVALID", "health=eq:YELLOW", "name=eq:Alpha", "unknown=eq:1", "archived=true", "archived=eq:true&archived=eq:false", "client_entity_id=eq:0", "lead_agent_id=eq:abc"} {
		values, err := url.ParseQuery(raw)
		require.NoError(t, err)
		_, err = s.listRecords(t.Context(), db, names, 1, all, projectListInput{RawQuery: values, Limit: 10})
		require.Error(t, err, raw)
	}
	_, err = s.listRecords(t.Context(), db, names, 1, all, projectListInput{Limit: 101})
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = s.listRecords(t.Context(), db, names, 1, authz.DataConstraint{DeniedIDs: []string{"bad"}}, projectListInput{Limit: 10})
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = s.listRecords(t.Context(), db, names, 1, all, projectListInput{Limit: 10, Sort: "name;drop table"})
	require.Error(t, err)
}

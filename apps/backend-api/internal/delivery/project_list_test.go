package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
)

func TestProjectListRealActivityIsTenantScopedAndDoesNotMultiply(t *testing.T) {
	s, db, draft := projectHarness(t)
	names := core.PublishedAccessTableNames("d_")
	require.NoError(t, db.Exec("CREATE TABLE d_iam_entities (tenant_id INTEGER NOT NULL,id INTEGER NOT NULL,name TEXT NOT NULL,type TEXT NOT NULL,PRIMARY KEY(tenant_id,id))").Error)
	now := time.Now().UTC()
	for _, scope := range [][2]guid.ID{{1, 101}, {1, 102}, {1, 103}, {2, 101}} {
		p := draft.project
		p.TenantID, p.ProjectID, p.Code = scope[0], scope[1], "P"+scope[1].String()
		require.NoError(t, query.BindProjects(db, s.names).WithContext(t.Context()).Create(&p))
		require.NoError(t, db.Exec("INSERT INTO d_iam_entities VALUES (?,?,?,'project')", scope[0], scope[1], p.Code).Error)
		require.NoError(t, db.Exec("INSERT INTO d_delivery_requirements (tenant_id,id,project_id,number,title,created_at,updated_at) VALUES (?,?,?,1,'Requirement',?,?)", scope[0], scope[1], scope[1], now, now).Error)
		for i, state := range []string{"BLOCKED", "NEEDS_HUMAN_REVIEW", "NEEDS_HUMAN_REVIEW", "ACCEPTED", "CANCELLED", "SKIPPED"} { //nolint:misspell // Frozen state.
			var archived *time.Time
			if state == "CANCELLED" { //nolint:misspell // Frozen state.
				archived = &now
			}
			require.NoError(t, db.Exec("INSERT INTO d_delivery_work_items (tenant_id,id,project_id,requirement_id,number,title,type,state,is_container,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at,archived_at) VALUES (?,?,?,?,?,'Task','TASK',?,?,0,11,?,?,?,?)", scope[0], scope[1]*10+guid.ID(i), scope[1], scope[1], i+1, state, i == 2, now, now, now, archived).Error)
		}
		for i, state := range []string{"SENT", "SENT", "DRAFT", "READY", "ACCEPTED", "REJECTED", "PARTIALLY_ACCEPTED"} {
			var archived *time.Time
			if state == "DRAFT" {
				archived = &now
			}
			require.NoError(t, db.Exec("INSERT INTO d_delivery_deliveries (tenant_id,id,project_id,sequence_no,number,title,state,created_at,updated_at,archived_at) VALUES (?,?,?,?,?,'Delivery',?,?,?,?)", scope[0], scope[1]*10+guid.ID(i), scope[1], i+1, fmt.Sprint(i+1), state, now, now, archived).Error)
		}
	}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11})
	m := &Module{service: s, projectAccess: names, listConstraint: func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"103"}}, nil
	}}
	page, err := m.listProjectRecords(ctx, db, projectListInput{Limit: 1, Offset: 1, Sort: "name"})
	require.NoError(t, err)
	require.Equal(t, int64(2), page.Total)
	require.Len(t, page.Records, 1)
	require.Equal(t, guid.ID(102), page.Records[0].ProjectID)
	require.Equal(t, projectListCounts{OpenWorkItems: 3, NeedsReview: 2, WaitingDeliveries: 2}, page.Records[0].Counts)
	page, err = m.listProjectRecords(ctx, db, projectListInput{Limit: 100, Sort: "name"})
	require.NoError(t, err)
	require.Len(t, page.Records, 2)
	for _, view := range page.Records {
		require.Equal(t, projectListCounts{OpenWorkItems: 3, NeedsReview: 2, WaitingDeliveries: 2}, view.Counts)
	}
	// An empty page keeps its filtered total and never reads activity tables.
	require.NoError(t, db.Exec("DROP TABLE d_delivery_deliveries").Error)
	page, err = m.listProjectRecords(ctx, db, projectListInput{Limit: 1, Offset: 9})
	require.NoError(t, err)
	require.Empty(t, page.Records)
	require.Equal(t, int64(2), page.Total)
	_, err = m.listProjectRecords(ctx, db, projectListInput{Limit: 1})
	require.Error(t, err, "missing activity storage must fail rather than fabricate zero counts")
}

func TestProjectListBudgetWirePrecision(t *testing.T) {
	s, db, draft := projectHarness(t)
	names := core.PublishedAccessTableNames("d_")
	require.NoError(t, db.Exec("CREATE TABLE d_iam_entities (tenant_id INTEGER NOT NULL,id INTEGER NOT NULL,name TEXT NOT NULL,type TEXT NOT NULL,PRIMARY KEY(tenant_id,id))").Error)
	require.NoError(t, db.Exec("INSERT INTO d_iam_entities VALUES (1,101,'Exact','project')").Error)
	p := query.BindProjects(db, s.names)
	row := draft.project
	row.ProjectID, row.TenantID = 101, 1
	require.NoError(t, p.WithContext(t.Context()).Create(&row))
	for _, tc := range []struct {
		name  string
		cap   *int64
		spent int64
		json  string
	}{
		{name: "no cap", spent: 9007199254740993, json: `{"cap":null,"spent":"9007199254740993","state":"NORMAL","pct":null}`},
		{name: "zero spent", cap: new(int64(100000000)), json: `{"cap":"100000000","spent":"0","state":"NORMAL","pct":0}`},
		{name: "eighty five percent", cap: new(int64(100000000)), spent: 85000000, json: `{"cap":"100000000","spent":"85000000","state":"NORMAL","pct":85}`},
		{name: "over cap", cap: new(int64(100000000)), spent: 125000000, json: `{"cap":"100000000","spent":"125000000","state":"NORMAL","pct":125}`},
		{name: "maximum int64", cap: new(int64(math.MaxInt64)), spent: math.MaxInt64, json: `{"cap":"9223372036854775807","spent":"9223372036854775807","state":"NORMAL","pct":100}`},
		{name: "multiply overflow boundary", cap: new(int64(100)), spent: math.MaxInt64, json: `{"cap":"100","spent":"9223372036854775807","state":"NORMAL","pct":9223372036854776000}`},
		{name: "clear existing cap", spent: 9007199254740993, json: `{"cap":null,"spent":"9007199254740993","state":"NORMAL","pct":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capValue := p.BudgetCapMicros.Null()
			if tc.cap != nil {
				capValue = p.BudgetCapMicros.Value(*tc.cap)
			}
			_, err := p.WithContext(t.Context()).Where(p.ProjectID.Eq(guid.ID(101))).UpdateSimple(capValue, p.BudgetSpentMicros.Value(tc.spent))
			require.NoError(t, err)
			page, err := s.listRecords(t.Context(), db, names, 1, authz.DataConstraint{AllowAll: true}, projectListInput{Limit: 10})
			require.NoError(t, err)
			require.Len(t, page.Records, 1)
			encoded, err := json.Marshal(page.Records[0].Budget)
			require.NoError(t, err)
			require.JSONEq(t, tc.json, string(encoded))
			encoded, err = json.Marshal(page.Records[0])
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &fields))
			require.ElementsMatch(t, []string{"project_id", "code", "name", "client", "stage", "team_mode", "health", "health_reasons", "budget", "target_date", "lead_agent_id", "version", "updated_at"}, slices.Collect(maps.Keys(fields)))
			// The existing detail response deliberately retains its original numeric
			// amount contract; the list must not silently rewrite that API.
			row, err := p.WithContext(t.Context()).Where(p.ProjectID.Eq(guid.ID(101))).First()
			require.NoError(t, err)
			detail, err := projectRecordView(*row)
			require.NoError(t, err)
			encoded, err = json.Marshal(detail)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(encoded, &fields))
			require.Equal(t, json.RawMessage(strconv.FormatInt(tc.spent, 10)), fields["budget_spent_micros"]) //nolint:testifylint // JSONEq converts numbers to float64 and cannot detect int64 precision loss.
			capJSON, err := json.Marshal(tc.cap)
			require.NoError(t, err)
			require.Equal(t, json.RawMessage(capJSON), fields["budget_cap_micros"]) //nolint:testifylint // Preserve exact numeric bytes in the existing detail contract.
		})
	}
}

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

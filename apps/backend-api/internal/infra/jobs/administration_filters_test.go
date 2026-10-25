package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/query"
	"7link.com/platform/pkg/extension/authn"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminJobFiltersPreserveOrderingAndExactTimeBoundaries(t *testing.T) {
	m, db := queueHarness(t, 100)
	q := query.BindJobs(db, m.names)
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	ids := []guid.ID{}
	for i, priority := range []int{1, 5, 9} {
		job, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 7, Kind: "jobs.work", Priority: priority})
		require.NoError(t, err)
		ids = append(ids, job.JobID)
		at := base.Add(time.Duration(i) * time.Minute)
		_, err = q.WithContext(t.Context()).Where(q.ID.Eq(job.JobID)).UpdateSimple(q.CreatedAt.Value(at), q.FinishedAt.Value(at), q.RunAtMS.Value(at.UnixMilli()))
		require.NoError(t, err)
	}
	api, _ := adminAPI(t, m)
	read := func(params url.Values) adminJobPage {
		t.Helper()
		out := api.Get("/platform/jobs?"+params.Encode(), "Authorization: Bearer viewer")
		require.Equal(t, http.StatusOK, out.Code, out.Body.String())
		var body struct {
			Data adminJobPage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
		return body.Data
	}
	for _, sort := range []string{"id", "created_at", "run_at", "priority", "-id", "-created_at", "-run_at", "-priority"} {
		page := read(url.Values{"sort": {sort}, "tenant_id": {"eq:7"}, "state": {"PENDING"}, "queue": {"default"}, "kind": {"jobs.work"}})
		got := []guid.ID{}
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		want := ids
		if sort[0] == '-' {
			want = []guid.ID{ids[2], ids[1], ids[0]}
		}
		require.Equal(t, want, got, sort)
	}
	for _, field := range []string{"created_at", "finished_at"} {
		for _, tc := range []struct {
			op   string
			want []guid.ID
		}{{"gt", ids[2:]}, {"gte", ids[1:]}, {"lt", ids[:1]}, {"lte", ids[:2]}} {
			page := read(url.Values{field: {tc.op + ":" + base.Add(time.Minute).Format(time.RFC3339)}, "sort": {"id"}})
			got := []guid.ID{}
			for _, item := range page.Items {
				got = append(got, item.ID)
			}
			require.Equal(t, tc.want, got, field+":"+tc.op)
		}
	}
	for _, priority := range []string{"5", "eq:5"} {
		page := read(url.Values{"priority": {priority}})
		require.Len(t, page.Items, 1)
		require.Equal(t, ids[1], page.Items[0].ID)
	}
}

func TestAdminJobInvalidFiltersAndLostAuthorityFailClosed(t *testing.T) {
	m, db := queueHarness(t, 100)
	api, _ := adminAPI(t, m)
	for _, filter := range []string{"tenant_id=-1", "tenant_id=eq:bad", "priority=gt:2", "priority=10", "created_at=bad", "created_at=gte:bad", "finished_at=eq:2026-10-19T00:00:00Z"} {
		out := api.Get("/platform/jobs?"+filter, "Authorization: Bearer viewer")
		require.Equal(t, http.StatusBadRequest, out.Code, filter)
	}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 1})
	for _, in := range []adminJobListInput{{Sort: "unknown"}, {Limit: -1}, {Limit: 201}, {Offset: -1}} {
		_, err := m.listAdminJobs(ctx, &in)
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Equal(t, http.StatusBadRequest, status.GetStatus())
	}
	require.ErrorIs(t, m.adminOn(t.Context(), db, false), errAdminForbidden)
	failure := errors.New("current authority unavailable")
	m.admin.check = func(context.Context, *gorm.DB, string, guid.ID) (bool, error) { return false, failure }
	require.ErrorIs(t, m.adminOn(ctx, db, true), failure)
	m.admin.check = nil
	require.ErrorIs(t, m.adminOn(ctx, db, true), ErrUnavailable)
	m.admin.audit = nil
	require.ErrorIs(t, m.adminAuditOn(ctx, db, "jobs.retried", 0, "job", 1, nil), ErrUnavailable)
}

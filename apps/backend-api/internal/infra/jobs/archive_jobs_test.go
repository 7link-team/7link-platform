package jobs

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestProjectArchiveJobsScanBeyondFirstBatchAndKeepTenantBoundary(t *testing.T) {
	m, db := queueHarness(t, 500)
	for i := 0; i < 201; i++ {
		_, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 1, Kind: "jobs.work", Payload: map[string]any{"project_id": "99"}})
		require.NoError(t, err)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.projectArchiveJobsOn(t.Context(), tx, 1, 99)
		require.Len(t, out, 201)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.projectArchiveJobsOn(t.Context(), tx, 2, 99)
		require.Empty(t, out)
		return e
	}))
	q := query.BindJobs(db, m.names)
	_, err := q.WithContext(t.Context()).Where(q.TenantID.Eq(guid.ID(1))).UpdateSimple(q.State.Value("SUCCEEDED"))
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.projectArchiveJobsOn(t.Context(), tx, 1, 99)
		require.Empty(t, out)
		return e
	}))
	_, err = m.projectArchiveJobsOn(t.Context(), db, 1, 99)
	require.ErrorIs(t, err, ErrInvalidRequest)
}

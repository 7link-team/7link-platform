package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPlanningProjectEnumerationUsesCurrentAuthorityAndPropagatesFailures(t *testing.T) {
	m, db, ctx, _ := workHTTPHarness(t)
	q := query.BindProjects(db, m.service.names)
	base, err := q.WithContext(ctx).First()
	require.NoError(t, err)
	for _, id := range []guid.ID{100, 101, 102} {
		row := *base
		row.ProjectID, row.Code = id, "P"+id.String()
		if id == 102 {
			now := time.Now().UTC()
			row.ArchivedAt = &now
			row.Stage = "CLOSED"
			row.ClosedAt = &now
		}
		require.NoError(t, q.WithContext(ctx).Create(&row))
	}
	m.workPolicy = func(_ context.Context, _ *gorm.DB, _, project guid.ID, _ string, _ guid.ID) (authz.DataConstraint, error) {
		if project == 100 {
			return authz.DataConstraint{}, ErrWorkForbidden
		}
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var ids []guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; ids, err = m.planningProjectsOn(ctx, tx, 1); return err }))
	require.Equal(t, []guid.ID{99, 101}, ids, "no reports are needed to enumerate real projects; denied and archived projects stay hidden")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.planningProjectsOn(ctx, tx, 2); return err }), ErrWorkForbidden)
	failure := errors.New("current authorization unavailable")
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	}
	err = db.Transaction(func(tx *gorm.DB) error { _, err := m.planningProjectsOn(ctx, tx, 1); return err })
	require.ErrorIs(t, err, failure, "a policy outage must not become a successful empty project list")
	require.NoError(t, db.Exec("ALTER TABLE "+m.service.names.Projects.String()+" RENAME TO unavailable_projects").Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.planningProjectsOn(ctx, tx, 1); return err }))
}

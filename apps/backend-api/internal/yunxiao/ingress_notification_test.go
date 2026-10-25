package yunxiao

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/yunxiao/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIngressFailureFactsRequirePersistedFinalAttemptAndRollback(t *testing.T) {
	m, db, ctx, c, _ := ingressFixture(t)
	r, err := m.receiveIngress(ctx, c.ID, "CODEUP", "terminal-notice", connectionBody().WebhookSecret, codeupTestBody())
	require.NoError(t, err)
	q := query.BindInboundEvents(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	cause := errors.New("provider unavailable")
	job := jobs.Job{TenantID: 1, Attempt: 4, MaxAttempts: 5}
	require.ErrorIs(t, m.ingressJobFailure(ctx, job, row, cause), cause)
	_, exhausted, err := m.ingressFailureOn(ctx, db, 1, r.ID)
	require.NoError(t, err)
	require.False(t, exhausted)
	job.Attempt = 5
	oldAudit := m.audit
	m.audit = func(_ context.Context, _ *gorm.DB, _ audit.DomainEvent) error { return cause }
	require.Error(t, m.ingressJobFailure(ctx, job, row, cause))
	_, exhausted, err = m.ingressFailureOn(ctx, db, 1, r.ID)
	require.NoError(t, err)
	require.False(t, exhausted, "failed audit must roll back exhaustion and its event")
	m.audit = oldAudit
	require.ErrorIs(t, m.ingressJobFailure(ctx, job, row, cause), cause)
	connection, exhausted, err := m.ingressFailureOn(ctx, db, 1, r.ID)
	require.NoError(t, err)
	require.True(t, exhausted)
	require.Equal(t, c.ID, connection)
	before, err := q.WithContext(ctx).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.ErrorIs(t, m.ingressJobFailure(ctx, job, row, cause), cause)
	after, err := q.WithContext(ctx).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.Equal(t, before.Version, after.Version, "duplicate final delivery must not produce another failure event")
	_, exhausted, err = m.ingressFailureOn(ctx, db, 2, r.ID)
	require.NoError(t, err)
	require.False(t, exhausted)
}

package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func workHarness(t *testing.T) (*Module, *gorm.DB, context.Context) {
	m, db, ctx := configHarness(t)
	m.projects = delivery.ProjectScope{AuthorizeOn: func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, _ string) error {
		if tenant != 1 || project != 99 {
			return ErrForbidden
		}
		return nil
	}}
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	return m, db, ctx
}
func workInput() WorklogInput {
	return WorklogInput{Category: "DEVELOPMENT", WorkDate: "2026-10-20", Minutes: 60, Note: "Investigated regression"}
}
func TestWorklogDailyLimitOwnershipApprovalAndRollback(t *testing.T) {
	m, db, ctx := workHarness(t)
	var v WorklogView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		v, err = m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, workInput())
		return err
	}))
	require.Equal(t, "DRAFT", v.State)
	require.Nil(t, v.ApprovedBy)
	require.Nil(t, v.ApprovedAt)
	require.Equal(t, m.now(), v.CreatedAt)
	require.Equal(t, m.now(), v.UpdatedAt)
	in := workInput()
	in.Minutes = 1400
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, in); return e }), ErrInvalid)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		v, err = m.worklogStateOn(ctx, tx, 1, 99, v.ID, 1, "SUBMITTED", "")
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.worklogStateOn(ctx, tx, 1, 99, v.ID, 2, "APPROVED", ""); return e }), ErrForbidden)
	reviewer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		v, err = m.worklogStateOn(reviewer, tx, 1, 99, v.ID, 2, "APPROVED", "")
		return err
	}))
	require.Equal(t, guid.ID(12), *v.ApprovedBy)
	require.Equal(t, m.now(), *v.ApprovedAt)
	created := v.CreatedAt
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveWorklogOn(ctx, tx, 1, 99, v.ID, 3, workInput()); return e }), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.worklogStateOn(reviewer, tx, 1, 99, v.ID, 3, "SUBMITTED", "")
		return e
	}), ErrInvalid)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		v, err = m.worklogStateOn(reviewer, tx, 1, 99, v.ID, 3, "SUBMITTED", "Correction required")
		return err
	}))
	require.Equal(t, 4, v.Version)
	require.Nil(t, v.ApprovedBy, "withdrawal must not retain a current Human approval")
	require.Nil(t, v.ApprovedAt)
	require.Equal(t, created, v.CreatedAt)
	m.events = testPublisher{err: errors.New("outbox unavailable")}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, workInput()); return e }))
	q := query.BindWorklogs(db, m.names)
	n, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Equal(t, int64(1), n)
}
func TestRuntimeFactsCeilingIdempotencyImmutableAndUnknownCost(t *testing.T) {
	m, db, ctx := workHarness(t)
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	fact := RuntimeFact{ExecutionID: 200, ProjectID: 99, WorkItemID: 77, PrincipalID: 23, StartedAt: start, FinishedAt: start.Add(12*time.Minute + 30*time.Second)}
	m.runtimeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (RuntimeFact, bool, error) {
		return fact, true, nil
	}
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return m.consumeRuntime(ctx, tx, events.Envelope{TenantID: 1, ProjectID: 99, Subject: events.Subject{ID: 200}, Data: []byte(`{"state":"SUCCEEDED"}`)})
		}))
	}
	q := query.BindWorklogs(db, m.names)
	rows, e := q.WithContext(ctx).Find()
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Equal(t, int64(13), rows[0].Minutes)
	require.Nil(t, rows[0].CostMicros)
	require.Equal(t, "APPROVED", rows[0].State)
	runtimeView := worklogView(rows[0])
	require.Nil(t, runtimeView.ApprovedBy, "source-backed runtime is not a Human confirmation")
	require.Nil(t, runtimeView.ApprovedAt)
	require.Equal(t, &fact.ExecutionID, runtimeView.ExecutionID)
	require.Equal(t, rows[0].CreatedAt, runtimeView.CreatedAt)
	require.NotNil(t, rows[0].WorkItemID)
	require.EqualValues(t, 77, *rows[0].WorkItemID)
	summary, err := m.summaryWorklogsHTTP(ctx, &WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"})
	require.NoError(t, err)
	require.Equal(t, int64(13), summary.Body.Data.ApprovedMinutes)
	require.Equal(t, 1, summary.Body.Data.UnknownCostCount)
	require.Zero(t, summary.Body.Data.KnownCostMicros)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.worklogStateOn(ctx, tx, 1, 99, rows[0].ID, 1, "REJECTED", "bad")
		return e
	}), ErrConflict)
	require.ErrorIs(t, m.recordRuntimeOn(ctx, db, 1, fact), ErrInvalid)
	fact.FinishedAt = start
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.recordRuntimeOn(ctx, tx, 1, fact) }), ErrInvalid)
}
func TestRuntimeConnectorRejectsNilAndDuplicateBindings(t *testing.T) {
	m := NewModule(nil, Config{})
	s := module.NewServices()
	require.NoError(t, m.ProvideServices(s))
	require.NoError(t, s.Seal())
	c, e := module.Resolve[RuntimeConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, c.BindExecution(nil), ErrInvalid)
	read := RuntimeReader(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (RuntimeFact, bool, error) {
		return RuntimeFact{}, false, nil
	})
	require.NoError(t, c.BindExecution(read))
	require.ErrorIs(t, c.BindExecution(read), ErrInvalid)
}

func TestWorklogReturnRequiresIndependentReviewerReasonAndPersistsHistory(t *testing.T) {
	m, db, ctx := workHarness(t)
	var row WorklogView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		row, e = m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, workInput())
		return e
	}))
	reviewer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.saveWorklogOn(reviewer, tx, 1, 99, row.ID, 1, workInput())
		return e
	}), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.worklogStateOn(reviewer, tx, 1, 99, row.ID, 1, "SUBMITTED", "")
		return e
	}), ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		row, e = m.worklogStateOn(ctx, tx, 1, 99, row.ID, 1, "SUBMITTED", "")
		return e
	}))
	for _, tc := range []struct {
		c      context.Context
		reason string
		want   error
	}{{ctx, "explain", ErrForbidden}, {reviewer, "", ErrInvalid}, {reviewer, strings.Repeat("x", 501), ErrInvalid}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.worklogStateOn(tc.c, tx, 1, 99, row.ID, 2, "REJECTED", tc.reason)
			return e
		}), tc.want)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		row, e = m.worklogStateOn(reviewer, tx, 1, 99, row.ID, 2, "REJECTED", "Need evidence")
		return e
	}))
	require.Equal(t, "REJECTED", row.State)
	require.Equal(t, 3, row.Version)
	require.Nil(t, row.ApprovedBy)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.worklogStateOn(ctx, tx, 1, 99, row.ID, 3, "APPROVED", ""); return e }), ErrConflict)
	bad := workInput()
	bad.CalibrationReason = strings.Repeat("x", 501)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, bad); return e }), ErrInvalid)
	valid := workInput()
	valid.CalibrationReason = "Initial participation basis"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, valid); return e }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.worklogStateOn(ctx, tx, 1, 99, 99999, 1, "SUBMITTED", ""); return e }), ErrNotFound)
}

func TestWorklogRemovedWorkBindingStillRequiresCurrentOriginalVisibility(t *testing.T) {
	for _, mode := range []string{"missing", "denied", "visible"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			work := guid.ID(77)
			in := workInput()
			in.WorkItemID = &work
			m.effortWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
			var row WorklogView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; row, e = m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, in); return e }))
			switch mode {
			case "missing":
				m.effortWorkOn = nil
			case "denied":
				m.effortWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrForbidden }
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.saveWorklogOn(ctx, tx, 1, 99, row.ID, 1, workInput()); return e })
			if mode == "visible" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.worklogStateOn(ctx, tx, 1, 99, row.ID, 1, "SUBMITTED", "")
					return e
				}))
			}
			stored, e := query.BindWorklogs(db, m.names).WithContext(ctx).First()
			require.NoError(t, e)
			if mode == "visible" {
				require.Nil(t, stored.WorkItemID)
			} else {
				require.Equal(t, &work, stored.WorkItemID)
				require.Equal(t, 1, stored.Version)
			}
		})
	}
}

func TestWorklogReadAndRevisionFailuresRollbackSaveAndState(t *testing.T) {
	for _, mode := range []string{"save_read", "state_read", "revision", "save_id", "state_update", "state_outbox"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			var row WorklogView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				row, e = m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, workInput())
				return e
			}))
			switch mode {
			case "save_read", "state_read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("worklog_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Worklogs.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("worklog_storage")
			case "revision":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("worklog_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.WorklogRevisions.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Create().Remove("worklog_storage")
			case "save_id":
				m.ids = &testIDs{err: ErrUnavailable}
			case "state_update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("worklog_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Worklogs.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Update().Remove("worklog_storage")
			case "state_outbox":
				m.events = testPublisher{err: ErrUnavailable}
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				if mode == "save_read" || mode == "revision" {
					_, e := m.saveWorklogOn(ctx, tx, 1, 99, row.ID, 1, workInput())
					return e
				}
				if mode == "save_id" {
					_, e := m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, workInput())
					return e
				}
				_, e := m.worklogStateOn(ctx, tx, 1, 99, row.ID, 1, "SUBMITTED", "")
				return e
			}))
			if mode == "save_read" || mode == "state_read" {
				require.NoError(t, db.Callback().Query().Remove("worklog_storage"))
			}
			stored, e := query.BindWorklogs(db, m.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, 1, stored.Version)
			require.Equal(t, "DRAFT", stored.State)
		})
	}
}

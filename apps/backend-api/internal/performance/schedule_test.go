package performance

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/performance/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestScheduledGenerationRejectsUnpersistedScheduleAndInvalidTransaction(t *testing.T) {
	m, db, _, scheme, window := reportFixture(t)
	system := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1})
	due := DueReport{SchemeID: scheme.SchemeID, SubjectID: 21, Period: window}
	_, err := m.generateScheduledOn(system, nil, 1, due, window.CutoffAt)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = m.generateScheduledOn(system, db, 1, due, window.CutoffAt)
	require.ErrorIs(t, err, ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.generateScheduledOn(system, tx, 1, due, window.CutoffAt)
		return err
	}), ErrInvalid)
	due.SchemeID++
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.generateScheduledOn(system, tx, 1, due, window.CutoffAt)
		return err
	}), ErrNotFound)
	due.SchemeID = scheme.SchemeID
	failure := errors.New("scheduled principal lock failed")
	m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.generateScheduledOn(system, tx, 1, due, window.CutoffAt)
		return err
	}), failure)
	var count int64
	require.NoError(t, db.Table(m.names.Reports.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestScheduleJobGeneratesDuePeriodOnceAsSystemAndRollsBackFailures(t *testing.T) {
	m, db, ctx, scheme, _ := reportFixture(t)
	now := time.Now().UTC()
	window, err := periodWindow("WEEK", "UTC", now)
	require.NoError(t, err)
	if window.CutoffAt.After(now) {
		window, err = periodWindow("WEEK", "UTC", now.AddDate(0, 0, -7))
		require.NoError(t, err)
	}
	d := scheme.Definition
	d.Schedule = &GenerationSchedule{Timezone: "UTC", Cadences: []string{"WEEK"}}
	configured, err := m.reviseHTTP(ctx, &reviseInput{SchemeID: scheme.SchemeID.String(), IdempotencyKey: "job-schedule", Body: schemeBody{BaseVersion: 1, Definition: d, Reason: "Enable due report job"}})
	require.NoError(t, err)
	q := query.BindRevisions(db, m.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(configured.Body.Data.ID)).UpdateSimple(q.CreatedAt.Value(window.StartAt.Add(time.Hour)))
	require.NoError(t, err)
	failure := errors.New("evidence backend unavailable")
	reader := m.evidence.ReadOn
	m.evidence.ReadOn = func(context.Context, *gorm.DB, EvidenceRequest) (EvidenceSet, error) { return EvidenceSet{}, failure }
	require.ErrorIs(t, m.runSchedule(t.Context(), jobs.Job{TenantID: 1}), failure)
	var count int64
	require.NoError(t, db.Table(m.names.Reports.String()).Count(&count).Error)
	require.Zero(t, count)
	m.evidence.ReadOn = reader
	for range 2 {
		require.NoError(t, m.runSchedule(ctx, jobs.Job{TenantID: 1}))
	}
	require.NoError(t, db.Table(m.names.Reports.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
	system := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		r := query.BindReports(tx, m.names)
		row, err := r.WithContext(system).First()
		require.NoError(t, err)
		_, view, err := m.reportOn(system, tx, row.ID, 0)
		require.NoError(t, err)
		require.Zero(t, view.CreatedBy)
		require.Equal(t, window, view.Snapshot.Period)
		require.Equal(t, "PENDING", view.Snapshot.Status)
		require.Nil(t, view.Snapshot.TotalScoreBPS)
		return nil
	}))
	require.ErrorIs(t, m.runSchedule(t.Context(), jobs.Job{}), ErrInvalid)
	require.ErrorIs(t, (&Module{}).runSchedule(t.Context(), jobs.Job{TenantID: 1}), ErrUnavailable)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("schedule_read_failure", func(tx *gorm.DB) { tx.AddError(failure) }))
	require.ErrorIs(t, m.runSchedule(t.Context(), jobs.Job{TenantID: 1}), failure)
	require.NoError(t, db.Callback().Query().Remove("schedule_read_failure"))
}

func TestScheduleStatusHTTPReturnsBacklogAndMapsStorageFailure(t *testing.T) {
	m, db, ctx, _, _ := reportFixture(t)
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "performance", Verb: "manage", Scope: authz.ActionScopeTenant, Summary: "Manage assessments"}}, nil))
	m.registrar = authz.NewDeclarationOnlyRegistrar(registry)
	_, api := humatest.New(t)
	api.UseMiddleware(func(c huma.Context, next func(huma.Context)) {
		next(huma.WithContext(c, authn.WithClaims(c.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11})))
	})
	require.NoError(t, m.RegisterHTTP(ctx, api))
	response := api.Get("/tenants/1/performance-schedule-status")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"pending":[]`)
	require.Contains(t, response.Body.String(), `"has_more":false`)
	failure := errors.New("private scheduler query details")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("schedule_http_failure", func(tx *gorm.DB) { tx.AddError(failure) }))
	response = api.Get("/tenants/1/performance-schedule-status")
	require.Equal(t, 500, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), failure.Error())
	require.NoError(t, db.Callback().Query().Remove("schedule_http_failure"))
}

func TestScheduleBackfillIsVisibleIdempotentAndUsesSystemAuthority(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	d := scheme.Definition
	d.Schedule = &GenerationSchedule{Timezone: window.Timezone, Cadences: []string{"WEEK", "MONTH", "QUARTER", "YEAR"}}
	configured, err := m.reviseHTTP(ctx, &reviseInput{SchemeID: scheme.SchemeID.String(), IdempotencyKey: "scheduled-scheme", Body: schemeBody{BaseVersion: 1, Definition: d, Reason: "Enable weekly and longer reporting periods"}})
	require.NoError(t, err)
	q := query.BindRevisions(db, m.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(configured.Body.Data.ID)).UpdateSimple(q.CreatedAt.Value(window.StartAt.Add(time.Hour)))
	require.NoError(t, err)
	system := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1})
	var due ScheduleStatus
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		due, e = m.dueReportsOn(system, tx, window.CutoffAt, 100)
		return e
	}))
	require.Len(t, due.Pending, 1)
	require.Equal(t, window, due.Pending[0].Period)
	var generated ReportView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		generated, e = m.generateScheduledOn(system, tx, 1, due.Pending[0], window.CutoffAt)
		return e
	}))
	require.Zero(t, generated.CreatedBy, "scheduled generation must not impersonate the scheme owner")
	require.Equal(t, window.CutoffAt, generated.Snapshot.Period.CutoffAt)
	require.Equal(t, "PENDING", generated.Snapshot.Status)
	require.Nil(t, generated.Snapshot.TotalScoreBPS, "professional review remains unassessed")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		replayed, e := m.generateScheduledOn(system, tx, 1, due.Pending[0], window.CutoffAt)
		require.Equal(t, generated, replayed)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		status, e := m.dueReportsOn(system, tx, window.CutoffAt, 100)
		require.Empty(t, status.Pending)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.generateScheduledOn(ctx, tx, 1, due.Pending[0], window.CutoffAt)
		return e
	}), ErrForbidden)
	// A rule-only revision retains missed schedule activation, without altering
	// the already-generated immutable report or expanding its original cutoff.
	d.Template.Summary = "Updated wording"
	revised, err := m.reviseHTTP(ctx, &reviseInput{SchemeID: scheme.SchemeID.String(), IdempotencyKey: "scheduled-wording", Body: schemeBody{BaseVersion: 2, Definition: d, Reason: "Clarify report wording"}})
	require.NoError(t, err)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(revised.Body.Data.ID)).UpdateSimple(q.CreatedAt.Value(window.EndAt.AddDate(0, 0, 8)))
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		status, e := m.dueReportsOn(system, tx, window.CutoffAt.AddDate(0, 0, 14), 1)
		require.Len(t, status.Pending, 1)
		require.True(t, status.HasMore)
		require.Equal(t, window.EndAt, status.Pending[0].Period.StartAt)
		return e
	}))
	read, err := m.getReportHTTP(ctx, &reportReadInput{ReportID: generated.ReportID.String(), Version: 1})
	require.NoError(t, err)
	require.Equal(t, generated, read.Body.Data)
}

func TestGenerationScheduleValidationAndDeclarations(t *testing.T) {
	for _, config := range []GenerationSchedule{{}, {Timezone: "Local", Cadences: []string{"WEEK"}}, {Timezone: "UTC", Cadences: []string{"DAY"}}, {Timezone: "UTC", Cadences: []string{"WEEK", "WEEK"}}} {
		require.ErrorIs(t, config.validate(), ErrInvalid)
	}
	require.NoError(t, (GenerationSchedule{Timezone: "Asia/Kuala_Lumpur", Cadences: []string{"WEEK", "MONTH", "QUARTER", "YEAR"}}).validate())
	m := &Module{}
	require.Len(t, m.JobHandlers(), 1)
	require.Len(t, m.JobSchedules(), 1)
	require.Equal(t, "* * * * *", m.JobSchedules()[0].Cron)
	_, err := m.generateScheduledOn(context.Background(), nil, guid.ID(1), DueReport{}, time.Now())
	require.ErrorIs(t, err, ErrForbidden)
}

func TestScheduleActivationTracksSubjectsAndKindButNotCadenceOrder(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	d := scheme.Definition
	d.Schedule = &GenerationSchedule{Timezone: window.Timezone, Cadences: []string{"WEEK", "MONTH"}}
	q := query.BindRevisions(db, m.names)
	revise := func(version int, key string, at time.Time) RevisionView {
		t.Helper()
		out, err := m.reviseHTTP(ctx, &reviseInput{SchemeID: scheme.SchemeID.String(), IdempotencyKey: key, Body: schemeBody{BaseVersion: version, Definition: d, Reason: "Verify subject schedule activation"}})
		require.NoError(t, err)
		_, err = q.WithContext(ctx).Where(q.ID.Eq(out.Body.Data.ID)).UpdateSimple(q.CreatedAt.Value(at))
		require.NoError(t, err)
		current := out.Body.Data
		current.CreatedAt = at
		return current
	}
	firstStart := window.StartAt.Add(time.Hour)
	revise(1, "activation-first", firstStart)
	// Reordering the same cadence set must preserve the original subject's
	// missed window, while a newly selected subject starts at this revision.
	d.Schedule.Cadences = []string{"MONTH", "WEEK"}
	d.SubjectIDs = []guid.ID{21, 22}
	secondStart := window.EndAt.Add(time.Hour)
	current := revise(2, "activation-added-subject", secondStart)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		start, err := m.scheduleActivatedOn(ctx, tx, current, 21)
		require.NoError(t, err)
		require.Equal(t, firstStart, start)
		start, err = m.scheduleActivatedOn(ctx, tx, current, 22)
		require.NoError(t, err)
		require.Equal(t, secondStart, start)
		status, err := m.dueReportsOn(ctx, tx, window.CutoffAt, 100)
		require.NoError(t, err)
		require.Len(t, status.Pending, 1)
		require.Equal(t, guid.ID(21), status.Pending[0].SubjectID)
		require.Equal(t, window, status.Pending[0].Period)
		return nil
	}))
	d.SubjectKind = "AGENT"
	thirdStart := window.EndAt.AddDate(0, 0, 7).Add(time.Hour)
	current = revise(3, "activation-kind-change", thirdStart)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, subject := range d.SubjectIDs {
			start, err := m.scheduleActivatedOn(ctx, tx, current, subject)
			require.NoError(t, err)
			require.Equal(t, thirdStart, start, "a different subject kind must not inherit earlier activation")
		}
		status, err := m.dueReportsOn(ctx, tx, window.CutoffAt, 100)
		require.NoError(t, err)
		require.Empty(t, status.Pending)
		return nil
	}))
}

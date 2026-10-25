package yunxiao

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEffortHTTPPreconditionsCalibrationAndLegacyObservation(t *testing.T) {
	m, db, _, row, input := effortHarness(t)
	a := testAuth{}
	m.registrar = authz.NewRegistrar(authz.DefaultRegistry(), a, a, a)
	require.NoError(t, m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "worklog", Verb: "submit", Scope: authz.ActionScopeTenant, DataScoped: true},
		{Resource: "workflow", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true},
	}, nil))
	_, api := humatest.New(t)
	require.NoError(t, m.registerWorkHTTP(api))
	path := "/tenants/1/projects/99/worklogs/" + row.ID.String()
	require.Equal(t, http.StatusUnprocessableEntity, api.Put(path+"/confirmation", map[string]any{}).Code)
	require.Equal(t, http.StatusUnprocessableEntity, api.Put(path+"/confirmation", "Idempotency-Key: missing-version", map[string]any{}).Code)
	confirmed := api.Put(path+"/confirmation", "Idempotency-Key: http-effort-confirm", `If-Match: "1"`, map[string]any{})
	require.Equal(t, http.StatusOK, confirmed.Code, confirmed.Body.String())
	require.Contains(t, confirmed.Body.String(), `"confirmed_by":"11"`)
	require.Contains(t, confirmed.Body.String(), `"approved_by":null`)
	body := EffortCalibration{WorklogInput: input, Reason: "Actual participation corrected"}
	body.Minutes = 40
	require.Equal(t, http.StatusUnprocessableEntity, api.Put(path+"/calibration", "Idempotency-Key: missing-context", `If-Match: "2"`, map[string]any{"category": "TEST", "work_date": "2026-10-20", "minutes": 40, "note": "", "reason": "Actual participation"}).Code)
	calibrated := api.Put(path+"/calibration", "Idempotency-Key: http-effort-calibrate", `If-Match: "2"`, body)
	require.Equal(t, http.StatusOK, calibrated.Code, calibrated.Body.String())
	require.Contains(t, calibrated.Body.String(), `"confirmed_at":null`)
	history := api.Get(path + "/history?limit=1")
	require.Equal(t, http.StatusOK, history.Code, history.Body.String())
	require.Contains(t, history.Body.String(), `"has_more":true`)
	require.Equal(t, http.StatusUnprocessableEntity, api.Get(path+"/history?limit=101").Code)
	// A historical record read remains observational and must not fabricate an actor/time.
	require.NoError(t, db.Table(m.names.WorklogRevisions.String()).Where("worklog_id = ?", row.ID).Delete(map[string]any{}).Error)
	require.NoError(t, db.Table(m.names.Worklogs.String()).Where("id = ?", row.ID).Updates(map[string]any{"timezone": nil, "confirmed_by": nil, "confirmed_at": nil}).Error)
	legacy := api.Get(path + "/history?limit=1")
	require.Equal(t, http.StatusOK, legacy.Code, legacy.Body.String())
	require.Contains(t, legacy.Body.String(), `"actor_id":null`)
	require.Contains(t, legacy.Body.String(), `"recorded_at":null`)
	require.Contains(t, legacy.Body.String(), `"timezone":null`)
	var count int64
	require.NoError(t, db.Table(m.names.WorklogRevisions.String()).Count(&count).Error)
	require.Zero(t, count)
}

type effortProjectHTTPAuth struct{ testAuth }

func (effortProjectHTTPAuth) Authenticate(ctx context.Context, authorization, cookie string) (*authn.Claims, error) {
	scope, ok := authn.PersonalTokenScopeFromContext(ctx)
	if !ok || (scope.Permission != "worklog_submit" && scope.Permission != "work_item_read") || authorization == "Bearer limited" {
		return nil, authn.ErrInvalidToken
	}
	return testAuth{}.Authenticate(ctx, authorization, cookie)
}
func (effortProjectHTTPAuth) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return false, nil
}
func (effortProjectHTTPAuth) Constraint(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{}, nil
}
func TestEffortHTTPHonorsProjectGrantsAndTokenScopeWithoutTenantWideGrant(t *testing.T) {
	m, _, _, row, input := effortHarness(t)
	a := effortProjectHTTPAuth{}
	m.registrar = authz.NewRegistrar(authz.DefaultRegistry(), a, a, a)
	require.NoError(t, m.registrar.Registry().Contribute([]authz.Action{{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true}, {Resource: "worklog", Verb: "submit", Scope: authz.ActionScopeTenant, DataScoped: true}, {Resource: "workflow", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	_, api := humatest.New(t)
	require.NoError(t, m.registerWorkHTTP(api))
	base := "/tenants/1/projects/99/worklogs"
	path := base + "/" + row.ID.String()
	require.Equal(t, http.StatusOK, api.Get(path+"/history").Code)
	require.Equal(t, http.StatusUnauthorized, api.Get(path+"/history", "Authorization: Bearer limited").Code)
	confirm := func() int {
		return api.Put(path+"/confirmation", "Idempotency-Key: project-confirm", `If-Match: "1"`, map[string]any{}).Code
	}
	require.Equal(t, http.StatusOK, confirm())
	calibrate := api.Put(path+"/calibration", "Idempotency-Key: project-calibrate", `If-Match: "2"`, EffortCalibration{WorklogInput: input, Reason: "Project scoped correction"})
	require.Equal(t, http.StatusOK, calibrate.Code, calibrate.Body.String())
	created := api.Post(base, "Idempotency-Key: project-context-create", input)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	submitted := api.Put(path+"/state", "Idempotency-Key: project-context-submit", `If-Match: "3"`, map[string]any{"state": "SUBMITTED", "reason": ""})
	require.Equal(t, http.StatusOK, submitted.Code, submitted.Body.String())
	m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
	require.Equal(t, http.StatusForbidden, api.Get(path+"/history").Code)
	require.Equal(t, http.StatusForbidden, confirm(), "current project revocation applies before replay")
}

func TestEffortRuntimeEnvelopeCannotSubstituteSourceScope(t *testing.T) {
	for _, scenario := range []string{"missing", "execution", "project", "forged_cost", "source_error"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			start := m.now()
			m.runtimeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (RuntimeFact, bool, error) {
				fact := RuntimeFact{ExecutionID: 200, ProjectID: 99, WorkItemID: 77, PrincipalID: 23, StartedAt: start, FinishedAt: start.Add(time.Second)}
				switch scenario {
				case "missing":
					return RuntimeFact{}, false, nil
				case "execution":
					fact.ExecutionID = 201
				case "project":
					fact.ProjectID = 100
				case "source_error":
					return RuntimeFact{}, false, ErrUnavailable
				}
				return fact, true, nil
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return m.consumeRuntime(ctx, tx, events.Envelope{TenantID: 1, ProjectID: 99, Subject: events.Subject{ID: 200}, Data: []byte(`{"state":"SUCCEEDED","minutes":999,"cost_micros":999,"principal_id":"11"}`)})
			})
			if scenario != "forged_cost" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			q := query.BindWorklogs(db, m.names)
			rows, e := q.WithContext(ctx).Find()
			require.NoError(t, e)
			require.Len(t, rows, 1)
			require.EqualValues(t, 1, rows[0].Minutes)
			require.Nil(t, rows[0].CostMicros)
			require.Equal(t, guid.ID(23), rows[0].PrincipalID)
			require.Nil(t, rows[0].ConfirmedBy)
		})
	}
}

func effortHarness(t *testing.T) (*Module, *gorm.DB, context.Context, WorklogView, WorklogInput) {
	t.Helper()
	m, db, ctx := workHarness(t)
	m.effortWorkOn = func(_ context.Context, _ *gorm.DB, tenant, project, id guid.ID) error {
		if tenant != 1 || project != 99 || id != 77 {
			return ErrForbidden
		}
		return nil
	}
	input := workInput()
	work := guid.ID(77)
	zone := "Asia/Kuala_Lumpur"
	input.WorkItemID = &work
	input.Timezone = &zone
	var row WorklogView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; row, e = m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, input); return e }))
	return m, db, ctx, row, input
}
func TestEffortConfirmationCalibrationHistoryAndSeparatedSummary(t *testing.T) {
	m, db, ctx, row, input := effortHarness(t)
	require.Nil(t, row.ConfirmedAt)
	require.Nil(t, row.ApprovedAt)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; row, e = m.confirmEffortOn(ctx, tx, 1, 99, row.ID, 1); return e }))
	require.Equal(t, "DRAFT", row.State)
	require.Nil(t, row.ApprovedBy)
	require.NotNil(t, row.ConfirmedAt)
	require.Equal(t, row.PrincipalID, *row.ConfirmedBy)
	path := WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}
	summary, e := m.summaryWorklogsHTTP(ctx, &path)
	require.NoError(t, e)
	require.EqualValues(t, 60, summary.Body.Data.HumanConfirmedMinutes)
	require.Zero(t, summary.Body.Data.ApprovedMinutes)
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	fact := RuntimeFact{ExecutionID: 100, ProjectID: 99, WorkItemID: 77, PrincipalID: 21, StartedAt: start, FinishedAt: start.Add(time.Minute)}
	for i := 0; i < 2; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.recordRuntimeOn(ctx, tx, 1, fact) }))
	}
	input.Minutes = 45
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = m.saveWorklogOn(ctx, tx, 1, 99, row.ID, row.Version, input, "Corrected against task activity")
		return err
	}))
	require.Nil(t, row.ConfirmedAt)
	require.Nil(t, row.ConfirmedBy)
	require.Nil(t, row.ApprovedBy)
	summary, e = m.summaryWorklogsHTTP(ctx, &path)
	require.NoError(t, e)
	require.Zero(t, summary.Body.Data.HumanConfirmedMinutes)
	require.EqualValues(t, 45, summary.Body.Data.HumanUnconfirmedMinutes)
	require.EqualValues(t, 1, summary.Body.Data.AgentWallMinutes)
	history, e := m.effortHistoryHTTP(ctx, &effortHistoryInput{EffortPath: EffortPath{WorkProjectPath: path, WorklogID: row.ID.String()}, Limit: 2})
	require.NoError(t, e)
	require.True(t, history.Body.Data.HasMore)
	require.Len(t, history.Body.Data.Items, 2)
	for _, cursor := range []string{pageCursor(2, row.ID, 1), pageCursor(1, row.ID+1, 1), "not-a-cursor"} {
		_, err := m.effortHistoryHTTP(ctx, &effortHistoryInput{EffortPath: EffortPath{WorkProjectPath: path, WorklogID: row.ID.String()}, Cursor: cursor, Limit: 2})
		require.Error(t, err, "history cursors are bound to their tenant and worklog")
	}
	require.Nil(t, history.Body.Data.Items[0].Snapshot.ConfirmedAt)
	require.NotNil(t, history.Body.Data.Items[1].Snapshot.ConfirmedAt)
	require.EqualValues(t, 60, history.Body.Data.Items[1].Snapshot.Minutes)
	next, e := m.effortHistoryHTTP(ctx, &effortHistoryInput{EffortPath: EffortPath{WorkProjectPath: path, WorklogID: row.ID.String()}, Cursor: history.Body.Data.NextCursor, Limit: 2})
	require.NoError(t, e)
	require.False(t, next.Body.Data.HasMore)
	require.Len(t, next.Body.Data.Items, 1)
	require.Equal(t, "Corrected against task activity", next.Body.Data.Items[0].Reason)
	require.EqualValues(t, 45, next.Body.Data.Items[0].Snapshot.Minutes)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.confirmEffortOn(ctx, tx, 1, 99, row.ID, 1); return err }), ErrConflict)
}
func TestEffortConfirmationReplayAndCurrentFences(t *testing.T) {
	m, _, ctx, row, _ := effortHarness(t)
	input := &effortConfirmationInput{EffortPath: EffortPath{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WorklogID: row.ID.String()}, EffortWriteHeaders: EffortWriteHeaders{IdempotencyKey: "confirm-effort", IfMatch: `"1"`}}
	first, e := m.confirmEffortHTTP(ctx, input)
	require.NoError(t, e)
	replay, e := m.confirmEffortHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	m.effortWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrForbidden }
	_, e = m.confirmEffortHTTP(ctx, input)
	require.Error(t, e, "current work scope must gate replay")
}

func TestEffortCanonicalReplayRechecksTaskAndSelectedRole(t *testing.T) {
	m, _, ctx, row, input := effortHarness(t)
	path := WorkProjectPath{TenantInput: TenantInput{"1"}, ProjectID: "99"}
	create := &worklogCreateInput{WorkProjectPath: path, WriteHeaders: WriteHeaders{IdempotencyKey: "context-create-replay"}, Body: input}
	_, err := m.createWorklogHTTP(ctx, create)
	require.NoError(t, err)
	submit := &worklogStateInput{WorklogPath: WorklogPath{WorkProjectPath: path, WorklogID: row.ID.String()}, WriteHeaders: WriteHeaders{IdempotencyKey: "context-submit-replay", IfMatch: `"1"`}}
	submit.Body.State = "SUBMITTED"
	_, err = m.stateWorklogHTTP(ctx, submit)
	require.NoError(t, err)
	read := m.effortWorkOn
	m.effortWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrForbidden }
	_, err = m.createWorklogHTTP(ctx, create)
	require.Error(t, err)
	_, err = m.stateWorklogHTTP(ctx, submit)
	require.Error(t, err)
	m.effortWorkOn = read
	m.projects = delivery.ProjectScope{AuthorizeOn: func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string) error {
		if permission == "worklog_submit" {
			return ErrForbidden
		}
		return nil
	}}
	_, err = m.stateWorklogHTTP(ctx, submit)
	require.Error(t, err, "read access alone cannot replay a now-revoked own submit grant")
	m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }}
	reviewer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	approve := *submit
	approve.IdempotencyKey = "context-approve-replay"
	approve.IfMatch = `"2"`
	approve.Body.State = "APPROVED"
	_, err = m.stateWorklogHTTP(reviewer, &approve)
	require.NoError(t, err)
	m.projects = delivery.ProjectScope{AuthorizeOn: func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string) error {
		if permission == "worklog_approve" {
			return ErrForbidden
		}
		return nil
	}}
	_, err = m.stateWorklogHTTP(reviewer, &approve)
	require.Error(t, err, "approval replay needs current supervisor authority")
}
func TestEffortFailureRollbackAndIdentity(t *testing.T) {
	for _, scenario := range []string{"audit", "event", "inactive", "other_human", "hidden_work", "archived_project", "legacy_timezone", "telemetry"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, row, _ := effortHarness(t)
			switch scenario {
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			case "event":
				m.events = testPublisher{err: errors.New("outbox unavailable")}
			case "inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "other_human":
				ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
			case "hidden_work":
				m.effortWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrForbidden }
			case "archived_project":
				m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
			case "legacy_timezone":
				require.NoError(t, db.Table(m.names.Worklogs.String()).Where("id = ?", row.ID).Update("timezone", nil).Error)
			case "telemetry":
				require.NoError(t, db.Table(m.names.Worklogs.String()).Where("id = ?", row.ID).Update("actor_kind", "AGENT").Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.confirmEffortOn(ctx, tx, 1, 99, row.ID, row.Version); return e })
			require.Error(t, err)
			q := query.BindWorklogs(db, m.names)
			stored, e := q.WithContext(ctx).Where(q.ID.Eq(row.ID)).Take()
			require.NoError(t, e)
			require.Nil(t, stored.ConfirmedAt)
			require.Equal(t, row.Version, stored.Version)
			revisions := query.BindWorklogRevisions(db, m.names)
			count, e := revisions.WithContext(ctx).Where(revisions.WorklogID.Eq(row.ID)).Count()
			require.NoError(t, e)
			require.EqualValues(t, 1, count, "confirmation/audit/outbox failure cannot append a revision")
		})
	}
}

func TestEffortRequiresExplicitPortableTimezone(t *testing.T) {
	for _, zone := range []string{"", " ", "Local", "Unknown/Timezone"} {
		t.Run(zone, func(t *testing.T) {
			m, db, ctx, row, input := effortHarness(t)
			input.Timezone = &zone
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveWorklogOn(ctx, tx, 1, 99, 0, 0, input); return e }), ErrInvalid)
			require.NoError(t, db.Table(m.names.Worklogs.String()).Where("id = ?", row.ID).Update("timezone", zone).Error)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.confirmEffortOn(ctx, tx, 1, 99, row.ID, row.Version); return e }), ErrInvalid)
		})
	}
}

func TestEffortHistoryRechecksFormerTaskScope(t *testing.T) {
	m, db, ctx, row, _ := effortHarness(t)
	require.NoError(t, db.Table(m.names.Worklogs.String()).Where("id = ?", row.ID).Update("work_item_id", 78).Error)
	m.effortWorkOn = func(_ context.Context, _ *gorm.DB, _, _, work guid.ID) error {
		if work != 78 {
			return ErrForbidden
		}
		return nil
	}
	_, err := m.effortHistoryHTTP(ctx, &effortHistoryInput{EffortPath: EffortPath{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{"1"}, ProjectID: "99"}, WorklogID: row.ID.String()}, Limit: 25})
	require.Error(t, err, "a visible current task cannot expose a hidden former task's immutable snapshot")
}

func TestEffortCalibrationKeepsFormerSupervisorApprovalButRequiresNewSelfConfirmation(t *testing.T) {
	m, db, ctx, row, input := effortHarness(t)
	change := func(apply func(*gorm.DB) (WorklogView, error)) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; row, e = apply(tx); return e }))
	}
	change(func(tx *gorm.DB) (WorklogView, error) { return m.confirmEffortOn(ctx, tx, 1, 99, row.ID, row.Version) })
	change(func(tx *gorm.DB) (WorklogView, error) {
		return m.worklogStateOn(ctx, tx, 1, 99, row.ID, row.Version, "SUBMITTED", "")
	})
	reviewer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	change(func(tx *gorm.DB) (WorklogView, error) {
		return m.worklogStateOn(reviewer, tx, 1, 99, row.ID, row.Version, "APPROVED", "")
	})
	approvedVersion := row.Version
	require.NotNil(t, row.ConfirmedAt)
	require.Equal(t, guid.ID(12), *row.ApprovedBy)
	input.Minutes = 35
	change(func(tx *gorm.DB) (WorklogView, error) {
		return m.saveWorklogOn(ctx, tx, 1, 99, row.ID, row.Version, input, "Reconciled own actual participation")
	})
	require.Equal(t, "DRAFT", row.State)
	require.Nil(t, row.ConfirmedAt)
	require.Nil(t, row.ApprovedAt)
	history, e := m.effortHistoryHTTP(ctx, &effortHistoryInput{EffortPath: EffortPath{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{"1"}, ProjectID: "99"}, WorklogID: row.ID.String()}, Limit: 100})
	require.NoError(t, e)
	var former *WorklogView
	for _, revision := range history.Body.Data.Items {
		if revision.Version == approvedVersion {
			snapshot := revision.Snapshot
			former = &snapshot
		}
	}
	require.NotNil(t, former)
	require.EqualValues(t, 60, former.Minutes)
	require.Equal(t, guid.ID(11), *former.ConfirmedBy)
	require.Equal(t, guid.ID(12), *former.ApprovedBy)
	change(func(tx *gorm.DB) (WorklogView, error) { return m.confirmEffortOn(ctx, tx, 1, 99, row.ID, row.Version) })
	require.Equal(t, "DRAFT", row.State)
	require.NotNil(t, row.ConfirmedAt)
	require.Nil(t, row.ApprovedBy)
}

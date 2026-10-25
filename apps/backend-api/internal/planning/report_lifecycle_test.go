package planning

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	eventmodel "7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReportReopenPreservesContentAndRequiresCurrentHumanAuthority(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	r := reportDraft(t, h)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		r, err = h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, PlanningReportInput{Uncompleted: "Pending work", Retrospective: "Actual facts", NextPlan: []PlanningPlanItem{linkedItem(g)}, DeclaredConfidence: "YELLOW"})
		return err
	}))
	for _, action := range []string{"SUBMIT", "CONFIRM"} {
		actor := guid.ID(2)
		if action == "CONFIRM" {
			actor = 3
		}
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			r, err = h.m.decideReportOn(claims(t, actor), tx, 1, r.ID, PlanningDecisionInput{Action: action, Version: r.Version, ExpectedContentHash: r.ContentHash})
			return err
		}))
	}
	in := PlanningDecisionInput{Action: "REOPEN", Version: r.Version, ExpectedContentHash: r.ContentHash, Reason: "Correct the retrospective"}
	read := func() model.Report {
		var row model.Report
		require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id = ?", r.ID).First(&row).Error)
		return row
	}
	before := read()
	for _, actor := range []guid.ID{2, 3, 4, 7} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			view, err := h.m.readOn(claims(t, actor), tx, 1, r.ID, "report")
			if actor == 7 {
				require.ErrorIs(t, err, ErrForbidden)
				return nil
			}
			require.NoError(t, err)
			require.Equal(t, actor == 3, view.CanReopen)
			return err
		}))
	}
	counts := func() [2]int64 {
		var out [2]int64
		require.NoError(t, h.db.Table("audit_probe").Count(&out[0]).Error)
		require.NoError(t, h.db.Table("p_domain_events").Count(&out[1]).Error)
		return out
	}
	beforeEffects := counts()
	decide := func(actor guid.ID, body PlanningDecisionInput) error {
		return h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.decideReportOn(claims(t, actor), tx, 1, r.ID, body); return err })
	}
	for _, actor := range []guid.ID{2, 4, 6, 7, 8, 9} {
		require.ErrorIs(t, decide(actor, in), ErrForbidden, "actor %v", actor)
		require.Equal(t, before, read())
	}
	bad := in
	bad.Reason = "bad"
	require.ErrorIs(t, decide(3, bad), ErrInvalid)
	bad = in
	bad.Version--
	require.ErrorIs(t, decide(3, bad), ErrConflict)
	bad = in
	bad.ExpectedContentHash = "incorrect"
	require.ErrorIs(t, decide(3, bad), ErrConflict)
	require.Equal(t, before, read())
	failure := errors.New("reopen sink unavailable")
	appender := h.m.audit
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, decide(3, in), failure)
	h.m.audit = appender
	require.Equal(t, before, read())
	publisher := h.m.events
	h.m.events = failingPlanningPublisher{}
	require.Error(t, decide(3, in))
	h.m.events = publisher
	require.Equal(t, before, read())
	require.Equal(t, beforeEffects, counts())
	require.NoError(t, decide(3, in))
	after := read()
	afterEffects := counts()
	require.Equal(t, [2]int64{beforeEffects[0] + 1, beforeEffects[1] + 1}, afterEffects)
	var event eventmodel.EventRow
	require.NoError(t, h.db.Table("p_domain_events").Where("subject_id = ?", r.ID).Order("id DESC").First(&event).Error)
	require.Equal(t, "HUMAN", event.ActorKind)
	require.Equal(t, "IAM:report_confirm", event.AuthoritySource)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(event.Data), &detail))
	require.Equal(t, "REOPEN", detail["action"])
	require.Equal(t, in.Reason, detail["reason"])
	var auditCount int64
	require.NoError(t, h.db.Table("audit_probe").Where("event_type = ?", "planning.report_reopened").Count(&auditCount).Error)
	require.EqualValues(t, 1, auditCount)
	require.Equal(t, "RETURNED", after.State)
	require.Equal(t, before.Version+1, after.Version)
	require.Nil(t, after.ConfirmedBy)
	require.Nil(t, after.ConfirmedAt)
	require.Equal(t, before.Content, after.Content)
	require.Equal(t, before.ContentHash, after.ContentHash)
	require.Equal(t, before.Facts, after.Facts)
	var submissions int64
	require.NoError(t, h.db.Table(h.m.names.ReportSubmissions.String()).Where("report_id = ?", r.ID).Count(&submissions).Error)
	require.EqualValues(t, 1, submissions)
	in.Version = after.Version
	require.ErrorIs(t, decide(3, in), ErrConflict)
	// The team owner can correct a report confirmed by a previous supervisor.
	previous := guid.ID(4)
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id = ?", r.ID).Updates(map[string]any{"state": "CONFIRMED", "confirmed_by": previous, "confirmed_at": before.ConfirmedAt}).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		view, err := h.m.readOn(claims(t, 4), tx, 1, r.ID, "report")
		require.True(t, view.CanReopen, "recorded confirmer retains correction authority even after the current superior changes")
		return err
	}))
	owner := guid.ID(3)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{TeamOwnerID: &owner})
		return err
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		view, err := h.m.readOn(claims(t, 3), tx, 1, r.ID, "report")
		require.True(t, view.CanReopen, "current owner affordance is computed from current domain authority")
		return err
	}))
	command := func() (*PlanningCommandOutput, error) {
		return h.m.command(claims(t, 3), 1, "owner-report-reopen", "report.decide", "/reports/"+r.ID.String()+"/decisions", in, func(tx *gorm.DB) (PlanningRecord, error) {
			return h.m.decideReportOn(claims(t, 3), tx, 1, r.ID, in)
		})
	}
	first, err := command()
	require.NoError(t, err)
	require.Equal(t, &previous, first.Body.Data.ReopenedFromConfirmedBy)
	replay, err := command()
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	newOwner := guid.ID(5)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.patchSettingsOn(claims(t, 3), tx, 1, 2, PlanningSettingsPatch{TeamOwnerID: &newOwner})
		return err
	}))
	_, err = command()
	require.Error(t, err, "revoked owner relationship cannot replay merely because generic report_confirm remains")
}

func TestSubmittedFactsRequireAuthorReconciliationBeforeConfirmation(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	r := reportDraft(t, h)
	body := PlanningReportInput{Completed: []PlanningCompletion{{WorkItemID: 100}}, Uncompleted: "Pending work", Retrospective: "Actual facts", NextPlan: []PlanningPlanItem{linkedItem(g)}, DeclaredConfidence: "YELLOW"}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, body)
		return e
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		row, _, err := h.m.reportOn(claims(t, 2), tx, 1, r.ID, "report_read", false)
		if err != nil {
			return err
		}
		check, err := h.m.checkReport(claims(t, 2), tx, 1, row)
		require.NotEqual(t, "BLOCKED", check.Result, "stale-check fixture must start with a genuinely submittable report: %v", check.Issues)
		return err
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = h.m.decideReportOn(claims(t, 2), tx, 1, r.ID, PlanningDecisionInput{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "SUBMIT"})
		return e
	}))
	event := events.Envelope{EventID: 9001, TenantID: 1, ProjectID: 3, Subject: events.Subject{ID: 100, Kind: "work_item"}, EventType: "work_item.state_changed", OccurredAt: time.Now().UTC().Add(time.Second)}
	invalidate := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error { return h.m.recheckReportsOn(context.Background(), tx, event) })
	}
	publisher := h.m.events
	h.m.events = failingPlanningPublisher{}
	require.Error(t, invalidate(), "outbox failure rolls back stale flags and audit")
	h.m.events = publisher
	require.NoError(t, invalidate())
	q := query.BindReports(h.db, h.m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "SUBMITTED", row.State)
	require.True(t, row.NeedsRecheck)
	require.Empty(t, row.LastCheckHash)
	version := row.Version
	require.NoError(t, invalidate())
	row, e = q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, e)
	require.Equal(t, version, row.Version)
	decide := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.decideReportOn(claims(t, 3), tx, 1, r.ID, PlanningDecisionInput{Version: version, ExpectedContentHash: r.ContentHash, Action: "CONFIRM"})
			return e
		})
	}
	require.ErrorIs(t, decide(), ErrStaleCheck)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.recordReportCheckOn(claims(t, 3), tx, 1, r.ID); return e }))
	require.ErrorIs(t, decide(), ErrStaleCheck, "reader cannot clear the author's stale check")
	h.facts.Work[0].State = "IN_PROGRESS"
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		check, e := h.m.recordReportCheckOn(claims(t, 2), tx, 1, r.ID)
		require.Equal(t, "BLOCKED", check.Result)
		return e
	}))
	require.ErrorIs(t, decide(), ErrStaleCheck, "blocked recheck cannot clear stale facts")
	h.facts.Work[0].State = "ACCEPTED"
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.recordReportCheckOn(claims(t, 2), tx, 1, r.ID); return e }))
	require.NoError(t, decide())
}

func TestReportConfirmerUsesCurrentRelationshipAndActiveHuman(t *testing.T) {
	h := setup(t)
	r := &model.Report{TenantID: 1, ProjectID: 3, PrincipalID: 2}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		id, e := h.m.reportConfirmerOn(t.Context(), tx, r)
		require.NoError(t, e)
		require.Equal(t, guid.ID(3), id)
		h.m.reportSuperior = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 7, nil }
		id, e = h.m.reportConfirmerOn(t.Context(), tx, r)
		require.NoError(t, e)
		require.Zero(t, id, "Agent cannot act as Human confirmer")
		r.PrincipalID = 7
		id, e = h.m.reportConfirmerOn(t.Context(), tx, r)
		require.NoError(t, e)
		require.Equal(t, guid.ID(2), id, "Agent reports go to current Human owner")
		return nil
	}))
}

func TestReportSourceInvalidationHonorsEventOrderAndChildDependencies(t *testing.T) {
	for _, mode := range []string{"child", "echo", "self", "older", "agent", "bad_event", "bad_content", "bad_facts", "read", "write"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			r := reportDraft(t, h)
			content := `{"children":[{"id":"950"}]}`
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Updates(map[string]any{"state": "READY", "content": content, "facts": "{}", "updated_at": h.m.now()}).Error)
			event := events.Envelope{EventID: 990, TenantID: 1, ProjectID: 3, Subject: events.Subject{ID: 950}, EventType: "planning.changed", Data: json.RawMessage(`{"kind":"report","action":"EDIT"}`), OccurredAt: h.m.now().Add(time.Second)}
			switch mode {
			case "echo":
				event.Data = json.RawMessage(`{"kind":"report","action":"RECHECK"}`)
			case "self":
				event.Subject.ID = r.ID
			case "older":
				event.OccurredAt = h.m.now().Add(-time.Second)
			case "agent":
				event.EventType = "agent.retired"
				event.ProjectID = 0
				event.Subject.ID = 2
				event.Data = nil
			case "bad_event":
				event.Data = json.RawMessage(`{`)
			case "bad_content":
				require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Update("content", "[").Error)
			case "bad_facts":
				event.EventType = "work_item.changed"
				event.Data = nil
				require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Update("facts", "[").Error)
			case "read":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("report_invalidation_read", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Reports.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "write":
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("report_invalidation_write", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Reports.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.recheckReportsOn(t.Context(), tx, event) })
			require.NoError(t, h.db.Callback().Query().Remove("report_invalidation_read"))
			require.NoError(t, h.db.Callback().Update().Remove("report_invalidation_write"))
			if mode == "bad_event" || mode == "bad_content" || mode == "bad_facts" || mode == "read" || mode == "write" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			var row model.Report
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&row).Error)
			affected := mode == "child" || mode == "agent"
			require.Equal(t, affected, row.NeedsRecheck)
			if affected {
				require.Equal(t, r.Version+1, row.Version)
			} else {
				require.Equal(t, r.Version, row.Version)
			}
		})
	}
	h := setup(t)
	require.ErrorIs(t, h.m.recheckReportsOn(t.Context(), nil, events.Envelope{}), ErrInvalid)
}

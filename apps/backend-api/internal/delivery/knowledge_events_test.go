package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestKnowledgeRequirementRendererReadsImmutableBaselineAndExactProject(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		created, err := m.createRequirementOn(ctx, tx, 1, 99, 11, body)
		if err != nil {
			return err
		}
		id = created.ID
		_, err = m.baselineRequirementOn(ctx, tx, 1, 99, id, 11, 1)
		return err
	}))
	payload, err := json.Marshal(map[string]int{"version_no": 1})
	require.NoError(t, err)
	event := events.Envelope{EventID: 800, EventType: "requirement.versioned", TenantID: 1, ProjectID: 99, Subject: events.Subject{Kind: "requirement", ID: id}, Data: payload}
	// Mutable head edits must never affect a historical version projection.
	r := query.BindRequirements(db, m.service.names)
	_, err = r.WithContext(ctx).Where(r.ID.Eq(id)).Update(r.Description, "Wrong mutable head content")
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		docs, e := m.knowledgeEventDocumentsOn(ctx, tx, event)
		require.NoError(t, e)
		require.Len(t, docs, 1)
		require.Contains(t, docs[0].Content, "Business objective")
		require.NotContains(t, docs[0].Content, "Wrong mutable head content")
		require.Contains(t, docs[0].Content, "Acceptance")
		require.Equal(t, id, docs[0].OriginID)
		return e
	}))
	event.ProjectID = 98
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.knowledgeEventDocumentsOn(ctx, tx, event); return e }))
}

func TestKnowledgeEventRenderingRejectsForgedPinsAndPropagatesStorageFailures(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	at := time.Now().UTC()
	action := model.ResultAction{TenantID: 1, ID: 900, ProjectID: 99, WorkItemID: h.work, ResultID: result.ID, Kind: "ACCEPT", Verdict: "PASSED", ActorID: 12, AuthoritySource: "ROLE:reviewer", Reason: "Verified the original pinned evidence against every criterion", CreatedAt: at}
	require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Create(&action).Error)
	payload, _ := json.Marshal(map[string]string{"reason_code": "RESULT_ACCEPTED", "result_id": result.ID.String()})
	event := events.Envelope{TenantID: 1, ProjectID: 99, EventType: "work_item.state_changed", Subject: events.Subject{Kind: "work_item", ID: h.work}, SubjectVersion: 3, Actor: events.Actor{Kind: "HUMAN", ID: 12}, OccurredAt: at, Data: payload}
	read := func(ev events.Envelope) (docs []knowledge.EventSourceDocument, err error) {
		err = h.db.Transaction(func(tx *gorm.DB) error { var e error; docs, e = h.m.knowledgeEventDocumentsOn(h.ctx, tx, ev); return e })
		return
	}
	docs, e := read(event)
	require.NoError(t, e)
	require.Len(t, docs, 2)
	require.Equal(t, "DECISION", docs[0].Kind)
	require.Contains(t, docs[0].Content, action.Reason)
	require.Equal(t, "DELIVERY", docs[1].Kind)
	injected := errors.New("knowledge source storage unavailable")
	for _, table := range []string{h.m.service.names.ResultActions.String(), h.m.service.names.WorkItems.String(), h.m.service.names.ResultVersions.String(), h.m.service.names.ResultEvidence.String()} {
		require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("event-source-failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(injected)
			}
		}))
		docs, e = read(event)
		require.ErrorIs(t, e, injected)
		require.Empty(t, docs)
		require.NoError(t, h.db.Callback().Query().Remove("event-source-failure"))
	}
	forged := event
	forged.Data = []byte(`{"reason_code":"RESULT_ACCEPTED","result_id":"999"}`)
	_, e = read(forged)
	require.ErrorIs(t, e, ErrInvalidArgument)
	forged.Data = []byte(`[]`)
	docs, e = read(forged)
	require.ErrorIs(t, e, ErrInvalidArgument)
	require.Empty(t, docs)
	forged = event
	forged.EventType = "requirement.versioned"
	forged.Data = []byte(`{"version_no":0}`)
	_, e = read(forged)
	require.ErrorIs(t, e, ErrInvalidArgument)
	forged.EventType = "requirement.scope_changed"
	forged.Data = []byte(`{"change_record_id":"0"}`)
	_, e = read(forged)
	require.ErrorIs(t, e, ErrInvalidArgument)
	forged = event
	forged.EventType = "unrelated"
	docs, e = read(forged)
	require.NoError(t, e)
	require.Empty(t, docs)
	_, e = h.m.knowledgeEventDocumentsOn(h.ctx, h.db, event)
	require.ErrorIs(t, e, ErrInvalidArgument)
	action.Kind = "RECEIPT"
	action.ID = 901
	require.NoError(t, h.db.Table(h.m.service.names.ResultActions.String()).Create(&action).Error)
	event.Data, _ = json.Marshal(map[string]string{"reason_code": "RESULT_RECEIVED", "result_id": result.ID.String()})
	docs, e = read(event)
	require.NoError(t, e)
	require.Len(t, docs, 1)
	require.Equal(t, "DELIVERY", docs[0].Kind)
}

func TestKnowledgeInjectionRequiresCurrentExactAgentAssignment(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	slot, agentID := work.ID, guid.ID(120)
	a := query.BindAssignments(db, m.service.names)
	require.NoError(t, a.WithContext(ctx).Create(&model.Assignment{TenantID: 1, ID: 900, ProjectID: 99, WorkItemID: work.ID, Role: "EXECUTOR", AssigneeKind: "AGENT", AssigneePrincipalID: 12, AgentID: &agentID, ActiveSlot: &slot, Status: "ACTIVE", LeaseTTLMS: 3600000, LeaseExpiresAt: &expiry, Version: 1, CreatedAt: now, UpdatedAt: now}))
	run := func(agent guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.verifyKnowledgeInjectionWorkOn(ctx, tx, 1, 99, work.ID, agent) })
	}
	require.NoError(t, run(12))
	require.Error(t, run(13))
	_, err := a.WithContext(ctx).Where(a.ID.Eq(guid.ID(900))).Update(a.LeaseExpiresAt, now.Add(-time.Hour))
	require.NoError(t, err)
	require.ErrorIs(t, run(12), ErrWorkForbidden)
}

func TestKnowledgeDecisionRendererUsesActualAcceptedClaimAndRejectedFeedback(t *testing.T) {
	h := newResultFixture(t)
	var captured []events.Event
	h.m.service.events = projectPublisher(func(_ context.Context, _ *gorm.DB, event events.Event) (guid.ID, error) {
		captured = append(captured, event)
		return guid.ID(len(captured)), nil
	})
	result, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(result.ID, 12)
	require.NoError(t, err)
	render := func() []knowledge.EventSourceDocument {
		t.Helper()
		event := captured[len(captured)-1]
		payload, e := json.Marshal(event.Data)
		require.NoError(t, e)
		var docs []knowledge.EventSourceDocument
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			docs, e = h.m.knowledgeEventDocumentsOn(h.ctx, tx, events.Envelope{EventID: 1000, EventType: event.Type, TenantID: event.TenantID, ProjectID: event.ProjectID, Subject: event.Subject, SubjectVersion: event.SubjectVersion, Actor: event.Actor, Data: payload, OccurredAt: time.Now().UTC()})
			return e
		}))
		return docs
	}
	docs := render()
	require.Len(t, docs, 1)
	require.Equal(t, "DELIVERY", docs[0].Kind)
	require.Contains(t, docs[0].Content, "Human result")
	require.Contains(t, docs[0].Content, "evidence.txt")
	require.NotContains(t, docs[0].Content, "exact") // File bytes are never embedded.
	result, err = h.handoff(result.ID)
	require.NoError(t, err)
	_, err = h.receive(result.ID, result.Handoffs[0].ID, 13, false)
	require.NoError(t, err)
	// The return command may append escalation events; select the canonical work
	// transition instead of assuming it is the final emitted notification.
	for i := len(captured) - 1; i >= 0; i-- {
		if captured[i].Type == "work_item.state_changed" {
			captured = captured[:i+1]
			break
		}
	}
	docs = render()
	require.Len(t, docs, 1)
	require.Equal(t, "FEEDBACK", docs[0].Kind)
	require.Contains(t, docs[0].Content, "Independent receipt decision")
	require.NotNil(t, docs[0].WorkItemID)
	require.Equal(t, h.work, *docs[0].WorkItemID)
}

func TestKnowledgeEventConnectorsPropagateBindingFailureAndAllowAbsentOptionalInjection(t *testing.T) {
	m := &Module{}
	failure := errors.New("knowledge source already bound")
	unsealed := module.NewServices()
	require.ErrorIs(t, m.bindKnowledgeEventReader(unsealed), module.ErrServicesProviding)
	for _, fail := range []bool{true, false} {
		s := module.NewServices()
		require.NoError(t, module.Provide(s, knowledge.EventSourceReaderConnector{Bind: func(reader knowledge.EventSourceReader) error {
			require.NotNil(t, reader.ReadOn)
			if fail {
				return failure
			}
			return nil
		}}))
		require.NoError(t, s.Seal())
		if fail {
			require.ErrorIs(t, m.bindKnowledgeEventReader(s), failure)
		} else {
			require.NoError(t, m.bindKnowledgeEventReader(s))
		}
	}
	s := module.NewServices()
	require.NoError(t, module.Provide(s, knowledge.EventSourceReaderConnector{Bind: func(knowledge.EventSourceReader) error { return nil }}))
	require.NoError(t, module.Provide(s, knowledge.InjectionWorkConnector{Bind: func(reader knowledge.InjectionWorkReader) error { require.NotNil(t, reader.VerifyOn); return failure }}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, m.bindKnowledgeEventReader(s), failure)
}

func TestKnowledgeScopeEventRequiresExactImmutableChangeAndValidBaseline(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	var req RequirementView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		req, e = m.createRequirementOn(ctx, tx, 1, 99, 11, body)
		if e != nil {
			return e
		}
		_, e = m.baselineRequirementOn(ctx, tx, 1, 99, req.ID, 11, 1)
		return e
	}))
	change := model.ChangeRecord{TenantID: 1, ID: 900, ProjectID: 99, RequirementID: req.ID, Kind: "SCOPE", FromVersionNo: 1, ToVersionNo: 2, Summary: "Actual changed acceptance criterion requires additional verification", Diff: "{}", AuthoritySource: "permission:scope_change", ActorID: 11, CreatedAt: time.Now().UTC()}
	require.NoError(t, db.Table(m.service.names.Changes.String()).Create(&change).Error)
	event := events.Envelope{TenantID: 1, ProjectID: 99, EventType: "requirement.scope_changed", Subject: events.Subject{Kind: "requirement", ID: req.ID}, Data: []byte(`{"change_record_id":"900"}`)}
	read := func() (docs []knowledge.EventSourceDocument, e error) {
		e = db.Transaction(func(tx *gorm.DB) error { var e error; docs, e = m.knowledgeEventDocumentsOn(ctx, tx, event); return e })
		return
	}
	docs, e := read()
	require.NoError(t, e)
	require.Len(t, docs, 1)
	require.Equal(t, change.ID, docs[0].OriginID)
	require.Contains(t, docs[0].Content, change.Summary)
	require.NoError(t, db.Table(m.service.names.Changes.String()).Where("id=?", change.ID).Update("summary", "Small edit").Error)
	docs, e = read()
	require.NoError(t, e)
	require.Empty(t, docs)
	event.Subject.ID++
	docs, e = read()
	require.ErrorIs(t, e, gorm.ErrRecordNotFound)
	require.Empty(t, docs)
	event.Subject.ID = req.ID
	event.EventType = "requirement.versioned"
	event.Data = []byte(`{"version_no":1}`)
	require.NoError(t, db.Table(m.service.names.RequirementVersions.String()).Where("requirement_id=?", req.ID).Update("criteria", "[1]").Error)
	docs, e = read()
	require.Error(t, e)
	require.Empty(t, docs)
	event.EventType = "result.feedback_added"
	event.Data = []byte(`{}`)
	docs, e = read()
	require.NoError(t, e)
	require.Empty(t, docs)
	event.TenantID = 0
	_, e = read()
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestKnowledgeInjectionRejectsMissingAgentAndCurrentWorkAuthority(t *testing.T) {
	h := newResultFixture(t)
	require.ErrorIs(t, h.m.verifyKnowledgeInjectionWorkOn(h.ctx, h.db, 1, 99, h.work, 0), ErrInvalidArgument)
	require.ErrorIs(t, h.m.verifyKnowledgeInjectionWorkOn(h.ctx, h.db, 1, 99, h.work, 12), ErrInvalidArgument)
}

func TestKnowledgeFeedbackRendererUsesCanonicalQualityRejectionDecision(t *testing.T) {
	h, result, request, _ := qualityFixture(t)
	_, e := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, result.ID, 12, 11, v)
	})
	require.NoError(t, e)
	require.Positive(t, request.SourceDecisionID)
	event := events.Envelope{TenantID: 1, ProjectID: 99, EventType: "result.feedback_added", Subject: events.Subject{Kind: "result", ID: result.ID}, OccurredAt: time.Now().UTC()}
	event.Data, _ = json.Marshal(map[string]any{"decision_id": request.SourceDecisionID})
	read := func() (docs []knowledge.EventSourceDocument, e error) {
		e = h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			docs, e = h.m.knowledgeEventDocumentsOn(h.ctx, tx, event)
			return e
		})
		return
	}
	docs, e := read()
	require.NoError(t, e)
	require.Len(t, docs, 1)
	require.Equal(t, "FEEDBACK", docs[0].Kind)
	require.Contains(t, docs[0].Content, "QUALITY_REJECTION")
	require.Equal(t, request.SourceDecisionID, docs[0].OriginID)
	failure := errors.New("frozen feedback storage unavailable")
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("knowledge-feedback-source", func(tx *gorm.DB) {
		if tx.Statement.Table == h.m.service.names.ResultFeedback.String() {
			tx.AddError(failure)
		}
	}))
	docs, e = read()
	require.ErrorIs(t, e, failure)
	require.Empty(t, docs)
	require.NoError(t, h.db.Callback().Query().Remove("knowledge-feedback-source"))
}

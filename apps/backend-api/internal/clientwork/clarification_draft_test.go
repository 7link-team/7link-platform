package clientwork

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestClarificationDraftAgentCannotSendOrExposeInternalDraftToClient(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	allowed := true
	m.questionDrafts.AuthorizeOn = func(ctx context.Context, _ *gorm.DB, tenant, project guid.ID) (string, error) {
		if !allowed || tenant != 1 || project != 3 {
			return "", ErrForbidden
		}
		if authn.PrincipalIDFromContext(ctx) == 25 {
			return "AGENT", nil
		}
		if authn.PrincipalIDFromContext(ctx) == 2 {
			return "HUMAN", nil
		}
		return "", ErrForbidden
	}
	m.questionDrafts.SourceOn = func(_ context.Context, tx *gorm.DB, tenant, project, id guid.ID, v int) error {
		var current int
		if err := tx.Table("g_intake_records").Where("tenant_id=? AND project_id=? AND id=?", tenant, project, id).Select("version").Scan(&current).Error; err != nil {
			return err
		}
		if current != v {
			return ErrConflict
		}
		return nil
	}
	in := &ClarificationDraftInput{EntityID: "3", Key: "agent-questions", Body: ClarificationDraftBody{IntakeID: 50, IntakeVersion: 1, Questions: []string{"Which export format?", "Which timezone?"}}}
	oldAudit := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err := m.createClarificationDraftHTTP(testContext(t, 25), in)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table(m.names.ClarificationDraft.String()).Count(&count).Error)
	require.Zero(t, count)
	m.audit = oldAudit
	draft, err := m.createClarificationDraftHTTP(testContext(t, 25), in)
	require.NoError(t, err)
	view, err := m.getClarificationDraftHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: draft.Body.Data.ID.String()})
	require.NoError(t, err)
	require.Equal(t, "AGENT", view.Body.Data.ActorKind)
	require.Equal(t, in.Body.Questions, view.Body.Data.Questions)
	require.NoError(t, db.Table(m.names.ClarificationRound.String()).Count(&count).Error)
	require.Zero(t, count, "draft must not send client questions")
	_, err = m.getClarificationDraftHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: draft.Body.Data.ID.String()})
	require.Error(t, err)
	send := &EditInput[ClarificationDraftSend]{EntityID: "3", ID: draft.Body.Data.ID.String(), IfMatch: `"1"`, Key: "human-send", Body: ClarificationDraftSend{RecipientID: 4}}
	_, err = m.sendClarificationDraftHTTP(testContext(t, 25), send)
	require.Error(t, err, "an Agent draft is not authority to send")
	sent, err := m.sendClarificationDraftHTTP(testContext(t, 2), send)
	require.NoError(t, err)
	client, err := m.getClarificationHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: sent.Body.Data.ID.String()})
	require.NoError(t, err)
	require.Equal(t, "Which export format?", client.Body.Data.Questions[0].Text)
	replay, err := m.sendClarificationDraftHTTP(testContext(t, 2), send)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	send.Key = "another-send"
	_, err = m.sendClarificationDraftHTTP(testContext(t, 2), send)
	require.Error(t, err)
	projects.deny = true
	send.Key = "human-send"
	_, err = m.sendClarificationDraftHTTP(testContext(t, 2), send)
	require.Error(t, err)
	allowed = false
	_, err = m.createClarificationDraftHTTP(testContext(t, 25), in)
	require.Error(t, err, "revoked draft authorization also denies replay")
}

func TestClarificationDraftRejectsInvalidAuthorityAndRollsBackStorageFailure(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	ctx := testContext(t, 2)
	in := ClarificationDraftBody{IntakeID: 50, IntakeVersion: 1, Questions: []string{"Which timezone?"}}
	failure := errors.New("draft storage unavailable")
	for _, stage := range []string{"invalid_source", "empty_question", "missing_capability", "unknown_actor", "source_revoked", "count", "create"} {
		t.Run(stage, func(t *testing.T) {
			copyModule := *m
			body := in
			copyModule.questionDrafts.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) { return "HUMAN", nil }
			copyModule.questionDrafts.SourceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error { return nil }
			want := failure
			switch stage {
			case "invalid_source":
				body.IntakeID = 0
				want = ErrInvalid
			case "empty_question":
				body.Questions = []string{" "}
				want = ErrInvalid
			case "missing_capability":
				copyModule.questionDrafts.SourceOn = nil
				want = ErrUnavailable
			case "unknown_actor":
				copyModule.questionDrafts.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) { return "SYSTEM", nil }
				want = ErrForbidden
			case "source_revoked":
				copyModule.questionDrafts.SourceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error { return failure }
			case "count":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_draft", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ClarificationDraft.String() {
						tx.AddError(failure)
					}
				}))
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("regression_draft", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ClarificationDraft.String() {
						tx.AddError(failure)
					}
				}))
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := copyModule.createClarificationDraftOn(ctx, tx, 3, body); return err })
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Query().Remove("regression_draft"))
			require.NoError(t, db.Callback().Create().Remove("regression_draft"))
			var count int64
			require.NoError(t, db.Table(m.names.ClarificationDraft.String()).Count(&count).Error)
			require.Zero(t, count, "failed draft creation never leaves a draft or a success receipt")
		})
	}
	for _, in := range []*ObjectInput{{EntityID: "bad", ID: "1"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "999"}} {
		out, err := m.getClarificationDraftHTTP(ctx, in)
		require.Error(t, err)
		require.Nil(t, out)
	}
	_, err := m.createClarificationDraftHTTP(ctx, &ClarificationDraftInput{EntityID: "bad"})
	require.Error(t, err)
	_, err = m.sendClarificationDraftHTTP(ctx, &EditInput[ClarificationDraftSend]{EntityID: "bad"})
	require.Error(t, err)
}

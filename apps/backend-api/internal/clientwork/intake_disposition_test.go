package clientwork

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestDispositionConversationInjectionRejectsIncompleteOrFailedConnectors(t *testing.T) {
	for _, stage := range []string{"unsealed", "missing_bind", "bind_failure", "missing_notification", "notification_failure"} {
		t.Run(stage, func(t *testing.T) {
			m := &Module{}
			s := module.NewServices()
			failure := errors.New("connector rejected handler")
			connector := intake.DispositionRoundConnector{Bind: func(closer intake.DispositionRoundCloser) error {
				require.NotNil(t, closer)
				if stage == "bind_failure" {
					return failure
				}
				return nil
			}, BindNotification: func(notify intake.DispositionNotification) error { require.NotNil(t, notify); return failure }}
			if stage == "missing_bind" {
				connector.Bind = nil
			}
			if stage == "missing_notification" {
				connector.BindNotification = nil
			}
			require.NoError(t, module.Provide[intake.DispositionRoundConnector](s, connector))
			if stage != "unsealed" {
				require.NoError(t, s.Seal())
			}
			err := m.injectIntakeConversation(s)
			if stage == "bind_failure" || stage == "notification_failure" {
				require.ErrorIs(t, err, failure)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestDispositionCloserRejectsStaleRoundAndCASWithoutPartialClose(t *testing.T) {
	for _, stage := range []string{"permission", "reason", "read", "update", "cas"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _, _ := clarificationHarness(t)
			ctx := testContext(t, 2)
			opened, err := m.requestClarificationHTTP(ctx, clarificationRequest())
			require.NoError(t, err)
			permission, reason := "intake_decline", "Outside scope"
			before := collaborationSnapshot(t, m, db)
			failure := errors.New("close storage unavailable")
			switch stage {
			case "permission":
				permission = "scope_change"
			case "reason":
				reason = " "
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_close", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ClarificationRound.String() {
						tx.AddError(failure)
					}
				}))
			case "update", "cas":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("regression_close", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ClarificationRound.String() {
						if stage == "cas" {
							tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "1=0"}}})
						} else {
							tx.AddError(failure)
						}
					}
				}))
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				return m.closeClarificationForDispositionOn(ctx, tx, 1, 3, 50, opened.Body.Data.ID, permission, reason)
			})
			require.Error(t, err)
			require.NoError(t, db.Callback().Query().Remove("regression_close"))
			require.NoError(t, db.Callback().Update().Remove("regression_close"))
			require.Equal(t, before, collaborationSnapshot(t, m, db))
		})
	}
}

func TestIntakeDispositionClosesExactClarificationAndRollsBackOnFailure(t *testing.T) {
	for _, action := range []string{"MERGE", "DECLINE"} {
		t.Run(action, func(t *testing.T) {
			m, db, _, _ := clarificationHarness(t)
			opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
			require.NoError(t, err)
			at := now()
			require.NoError(t, db.Table("g_intake_records").Create(map[string]any{"id": 51, "tenant_id": 1, "ref_no": 2, "channel_id": 3, "channel_kind": "SHARED_CHANNEL", "contact_id": 1, "kind": "NEW", "title": "Canonical target", "raw_content_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "received_at": at, "reply_token": "source-51", "status": "RECEIVED", "project_id": 3, "created_at": at, "updated_at": at}).Error)
			in := &EditInput[intake.SourceDisposition]{EntityID: "3", ID: "50", IfMatch: `"2"`, Key: "disposition-closes-round", Body: intake.SourceDisposition{Action: action, Reason: "Duplicate or declined scope, with a recorded Human explanation"}}
			route := "merge"
			if action == "MERGE" {
				in.Body.TargetID = 51
			} else {
				route = "decline"
				in.Body.ReasonCode = "OUT_OF_SCOPE"
			}
			originalAudit := m.audit
			m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error {
				return errors.New("closure audit unavailable")
			}
			_, err = m.disposeHTTP(testContext(t, 2), in, route)
			require.Error(t, err)
			m.audit = originalAudit
			view, err := m.getClarificationHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: opened.Body.Data.ID.String()})
			require.NoError(t, err)
			require.Equal(t, "OPEN", view.Body.Data.State)
			first, err := m.disposeHTTP(testContext(t, 2), in, route)
			require.NoError(t, err)
			require.Contains(t, []string{"MERGED", "DECLINED"}, first.Body.Data.State)
			replay, err := m.disposeHTTP(testContext(t, 2), in, route)
			require.NoError(t, err)
			require.Equal(t, "true", replay.Replayed)
			view, err = m.getClarificationHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: opened.Body.Data.ID.String()})
			require.NoError(t, err)
			require.Equal(t, "CANCELLED", view.Body.Data.State) //nolint:misspell // Frozen domain state.
			require.Equal(t, 2, view.Body.Data.Version)
			for _, question := range view.Body.Data.Questions {
				require.Equal(t, "WITHDRAWN", question.State)
			}
			require.Equal(t, in.Body.Reason, *view.Body.Data.CloseNote)
			var pending int64
			require.NoError(t, db.Table("g_intake_records").Where("id=50 AND open_round_id IS NOT NULL").Count(&pending).Error)
			require.Zero(t, pending)
		})
	}
}

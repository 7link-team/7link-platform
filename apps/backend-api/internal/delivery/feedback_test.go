package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func feedbackHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *resultFeedbackInput) {
	m, db, ctx, body := workHTTPHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	q := query.BindResultVersions(db, m.service.names)
	require.NoError(t, q.WithContext(ctx).Create(&model.ResultVersion{TenantID: 1, ID: 801, ProjectID: 99, WorkItemID: created.Body.Data.ID, Sequence: 1, Summary: "Human supplied result", ActorID: 11, BaselineVersionNo: 0, CreatedAt: time.Now().UTC()}))
	return m, db, ctx, &resultFeedbackInput{EntityID: "99", WorkItemID: created.Body.Data.ID.String(), ResultID: "801", IdempotencyKey: "feedback-one", Body: resultFeedbackBody{Kind: "COMMENT", Detail: "Please explain the boundary condition"}}
}

func TestResultFeedbackWriteFailuresRollbackAndListRejectsForeignOrHiddenResults(t *testing.T) {
	for _, scenario := range []string{"id", "write", "outbox", "unknown_result", "missing_table", "invalid_page", "hidden_work", "policy_error"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, in := feedbackHarness(t)
			work, err := guid.RequireID(in.WorkItemID)
			require.NoError(t, err)
			result := guid.ID(801)
			switch scenario {
			case "id":
				m.ids = draftIDSource(func() (int64, error) { return 0, errors.New("id unavailable") })
			case "write":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_feedback BEFORE INSERT ON d_delivery_result_feedback BEGIN SELECT RAISE(ABORT,'write failed'); END").Error)
			case "outbox":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
					return 0, errors.New("outbox unavailable")
				})
			case "unknown_result":
				result = 333
			case "missing_table":
				require.NoError(t, db.Exec("ALTER TABLE d_delivery_result_feedback RENAME TO hidden_feedback").Error)
			case "hidden_work":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
				}
			case "policy_error":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, errors.New("policy unavailable")
				}
			}
			if scenario == "id" || scenario == "write" || scenario == "outbox" {
				err = db.Transaction(func(tx *gorm.DB) error {
					_, e := m.appendResultFeedbackOn(ctx, tx, 1, 99, work, result, 11, 11, in.Body)
					return e
				})
				require.Error(t, err)
				var count int64
				require.NoError(t, db.Table("d_delivery_result_feedback").Count(&count).Error)
				require.Zero(t, count)
			} else {
				offset := 0
				if scenario == "invalid_page" {
					offset = -1
				}
				_, _, err = m.resultFeedbackOn(ctx, db, 1, 99, work, result, 11, offset, 200)
				require.Error(t, err)
			}
		})
	}
}

func TestResultFeedbackAuditFailureAndForeignResultLeaveNoRows(t *testing.T) {
	for _, scenario := range []string{"audit", "foreign_result", "missing_result", "invalid_category"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, in := feedbackHarness(t)
			switch scenario {
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			case "foreign_result":
				_, _, _, body := workHTTPHarness(t)
				other, e := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
				require.NoError(t, e)
				in.WorkItemID = other.Body.Data.ID.String()
			case "missing_result":
				in.ResultID = "802"
			case "invalid_category":
				in.Body.Kind = "FEEDBACK"
			}
			_, err := m.appendResultFeedbackHTTP(ctx, in)
			require.Error(t, err)
			var count int64
			require.NoError(t, db.Table("d_delivery_result_feedback").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
func TestResultFeedbackCommentConversionFrozenDefaultsReplayAndAtomicRollback(t *testing.T) {
	for _, failure := range []string{"", "source", "outbox", "foreign", "client", "duplicate"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx, in := feedbackHarness(t)
			work, err := guid.RequireID(in.WorkItemID)
			require.NoError(t, err)
			require.NoError(t, db.Exec("CREATE TABLE feedback_conversion_probe(target BIGINT NOT NULL)").Error)
			require.NoError(t, db.Exec("INSERT INTO feedback_conversion_probe VALUES (0)").Error)
			thread := guid.ID(901)
			m.artifactComments = ArtifactComments{GetOn: func(_ context.Context, tx *gorm.DB, tenant, id guid.ID) (ArtifactCommentSource, error) {
				source := ArtifactCommentSource{ID: id, TenantID: tenant, ProjectID: 99, WorkItemID: work, ExecutionID: 71, Visibility: "INTERNAL", FirstBody: "Frozen original comment", Anchor: ArtifactAnchorSnapshot{Kind: "CODE_LINES", Path: "source.go", StartLine: 4, EndLine: 6, VersionRef: "file-version:3", FileID: 2, VersionID: 3, SHA256: "frozen-sha", Excerpt: "original source"}}
				if failure == "foreign" {
					source.WorkItemID = 999
				}
				if failure == "client" {
					source.Visibility = "CLIENT"
				}
				if failure == "duplicate" {
					source.FeedbackID = 123
				}
				return source, nil
			}, MarkConvertedOn: func(_ context.Context, tx *gorm.DB, tenant, id guid.ID, kind string, target, actor guid.ID) error {
				require.Equal(t, "FEEDBACK", kind)
				require.Equal(t, thread, id)
				if failure == "source" {
					return errors.New("source failure")
				}
				return tx.Table("feedback_conversion_probe").Where("target = ?", 0).Update("target", target).Error
			}}
			if failure == "outbox" {
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, errors.New("outbox failure") })
			}
			in.Body = resultFeedbackBody{Kind: "FEEDBACK", Category: "QUALITY_REJECTION", CommentThreadID: &thread, Expected: "Correct output"}
			got, err := m.appendResultFeedbackHTTP(ctx, in)
			var count int64
			require.NoError(t, db.Table(m.service.names.ResultFeedback.String()).Count(&count).Error)
			var target guid.ID
			require.NoError(t, db.Table("feedback_conversion_probe").Select("target").Scan(&target).Error)
			if failure != "" {
				require.Error(t, err)
				require.Zero(t, count)
				require.Zero(t, target)
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 1, count)
			require.Equal(t, got.Body.Data.ID, target)
			require.Equal(t, "Frozen original comment", got.Body.Data.Detail)
			require.Equal(t, "source.go:4-6", got.Body.Data.Location)
			require.Equal(t, "file-version:3", got.Body.Data.AnchorSnapshot.VersionRef)
			replay, err := m.appendResultFeedbackHTTP(ctx, in)
			require.NoError(t, err)
			require.Equal(t, got.Body.Data.ID, replay.Body.Data.ID)
			listed, _, err := m.resultFeedbackOn(ctx, db, 1, 99, work, 801, 11, 0, 20)
			require.NoError(t, err)
			require.Len(t, listed, 1)
			require.Equal(t, "original source", listed[0].AnchorSnapshot.Excerpt)
		})
	}
}

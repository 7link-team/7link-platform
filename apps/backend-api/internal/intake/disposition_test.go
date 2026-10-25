package intake

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestSourceDispositionPreservesContentAndCannotReopen(t *testing.T) {
	for _, action := range []string{"MERGE", "DECLINE"} {
		t.Run(action, func(t *testing.T) {
			m, db, ctx, allowed, originalPublisher := changeSourceHarness(t)
			pub := &dispositionPublisher{briefTestPublisher: originalPublisher}
			m.brief.events = pub
			m.brief.explain = func(_ context.Context, _ *gorm.DB, in core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				require.Contains(t, []string{"intake_convert", "intake_decline", "scope_change"}, in.Permission)
				return authz.Explanation{Allowed: *allowed}, nil
			}
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": "RECEIVED", "requirement_id": nil, "raw_content": "Original immutable source"}).Error)
			var source model.Record
			require.NoError(t, db.Table(m.names.Records.String()).First(&source).Error)
			target := source
			target.ID = 11
			target.RefNo = 2
			target.ReplyToken = "target-11"
			require.NoError(t, db.Table(m.names.Records.String()).Create(&target).Error)
			in := SourceDisposition{Action: action, Reason: "Independent Human source disposition", ReasonCode: "SPAM", ClientMessage: "Client-facing explanation", NotifyClient: true}
			if action == "MERGE" {
				in.TargetID = 11
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				out, e := m.disposeSourceOn(ctx, tx, 7, 20, 10, 1, in)
				require.Equal(t, 2, out.Version)
				return e
			}))
			var stored model.Record
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").First(&stored).Error)
			require.Equal(t, source.RawContent, stored.RawContent)
			require.Equal(t, source.RawContentHash, stored.RawContentHash)
			require.NotNil(t, stored.ResolvedAt)
			require.Equal(t, "HUMAN", *stored.ResolvedActorKind)
			require.Equal(t, in.ClientMessage, *stored.ClientMessage)
			if action == "MERGE" {
				require.Equal(t, target.ID, *stored.MergedIntoIntakeID)
			} else {
				require.Equal(t, "SPAM", *stored.DeclineReasonCode)
				require.Equal(t, false, pub.events[len(pub.events)-1].Data.(map[string]any)["notify_client"])
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.disposeSourceOn(ctx, tx, 7, 20, 10, 2, in); return e }), ErrConflict)
		})
	}
}

func TestSourceDispositionGuardsAndRollback(t *testing.T) {
	for _, scenario := range []string{"denied", "missing_reason", "stale", "self", "target_terminal", "other_project", "other_tenant", "pending_round", "audit", "outbox"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, allowed, pub := changeSourceHarness(t)
			m.brief.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Allowed: *allowed}, nil
			}
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": "TRIAGED", "requirement_id": nil}).Error)
			var source model.Record
			require.NoError(t, db.Table(m.names.Records.String()).First(&source).Error)
			target := source
			target.ID = 11
			target.RefNo = 2
			target.ReplyToken = "target-11"
			require.NoError(t, db.Table(m.names.Records.String()).Create(&target).Error)
			in := SourceDisposition{Action: "MERGE", TargetID: 11, Reason: "Duplicate source with verified matching scope"}
			version := 1
			switch scenario {
			case "denied":
				*allowed = false
			case "missing_reason":
				in.Reason = " "
			case "stale":
				version = 2
			case "self":
				in.TargetID = 10
			case "target_terminal":
				require.NoError(t, db.Table(m.names.Records.String()).Where("id=11").Updates(map[string]any{"status": "DECLINED", "decline_reason_code": "OTHER", "decline_reason": "Not accepted"}).Error)
			case "other_project":
				require.NoError(t, db.Table(m.names.Records.String()).Where("id=11").Update("project_id", 21).Error)
			case "other_tenant":
				require.NoError(t, db.Table(m.names.Records.String()).Where("id=11").Update("tenant_id", 8).Error)
			case "pending_round":
				require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("open_round_id", 999).Error)
			case "audit":
				m.brief.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			case "outbox":
				pub.fail = true
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.disposeSourceOn(ctx, tx, 7, 20, 10, version, in); return e }))
			var stored model.Record
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").First(&stored).Error)
			require.Equal(t, "TRIAGED", stored.Status)
			require.Equal(t, 1, stored.Version)
			require.Nil(t, stored.MergedIntoIntakeID)
		})
	}
}

func TestSourceDispositionNotificationUsesActualTransportAndRollsBack(t *testing.T) {
	for _, outcome := range []string{"POSTED", "UNAVAILABLE", "invalid", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			m, db, ctx, _, original := changeSourceHarness(t)
			m.brief.explain = func(_ context.Context, _ *gorm.DB, in core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				require.Equal(t, "intake_decline", in.Permission)
				return authz.Explanation{Allowed: true}, nil
			}
			pub := &dispositionPublisher{briefTestPublisher: original}
			m.brief.events = pub
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": "RECEIVED"}).Error)
			m.dispositionNotification = func(_ context.Context, tx *gorm.DB, tenant, project, source guid.ID, body string, _ *int64) (DispositionDelivery, error) {
				require.Equal(t, guid.ID(7), tenant)
				require.Equal(t, guid.ID(20), project)
				require.Equal(t, guid.ID(10), source)
				require.Equal(t, "Exact client explanation", body)
				if err := tx.Exec("INSERT INTO brief_probe(kind) VALUES ('transport')").Error; err != nil {
					return DispositionDelivery{}, err
				}
				if outcome == "failed" {
					return DispositionDelivery{}, errors.New("transport offline")
				}
				notice := DispositionDelivery{State: outcome}
				if outcome == "POSTED" {
					message := guid.ID(123)
					notice.MessageID = &message
				}
				return notice, nil
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.disposeSourceOn(ctx, tx, 7, 20, 10, 1, SourceDisposition{Action: "DECLINE", ReasonCode: "OTHER", Reason: "Human decision", ClientMessage: "Exact client explanation", NotifyClient: true})
				return err
			})
			var status string
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Select("status").Scan(&status).Error)
			var n int64
			require.NoError(t, db.Table("brief_probe").Where("kind=?", "transport").Count(&n).Error)
			if outcome == "POSTED" || outcome == "UNAVAILABLE" {
				require.NoError(t, err)
				require.Equal(t, "DECLINED", status)
				require.EqualValues(t, 1, n)
				require.Equal(t, outcome, pub.events[len(pub.events)-1].Data.(map[string]any)["client_notification"])
			} else {
				require.Error(t, err)
				require.Equal(t, "RECEIVED", status)
				require.Zero(t, n)
			}
		})
	}
}

type dispositionPublisher struct {
	*briefTestPublisher
	events []events.Event
}

func (p *dispositionPublisher) Publish(ctx context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
	p.events = append(p.events, event)
	return p.briefTestPublisher.Publish(ctx, tx, event)
}

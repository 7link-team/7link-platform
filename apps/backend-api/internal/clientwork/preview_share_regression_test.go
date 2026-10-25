package clientwork

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPreviewShareRequestFailuresPreserveExistingGrant(t *testing.T) {
	for _, scenario := range []string{"authority", "recipient_id", "principal_lock", "client_lookup", "missing_preview", "public_preview", "id", "insert", "audit", "expired_audit", "expired_update"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, projects, clients := harness(t)
			preview, share := previewShareFixture(t, m, db)
			ctx := testContext(t, 2)
			q := query.BindPreviewShare(db, m.names)
			pq := query.BindPreview(db, m.names)
			_, err := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).UpdateSimple(q.State.Value("ACTIVE"), q.ExpiresAt.Value(now().Add(-time.Hour)))
			require.NoError(t, err)
			before, err := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
			require.NoError(t, err)
			in := PreviewShareBody{PreviewID: preview.ID, RecipientID: 4, TTLHours: 24, Note: "Replacement"}
			failure := errors.New("share boundary unavailable")
			switch scenario {
			case "authority":
				projects.deny = true
			case "recipient_id":
				in.RecipientID = 0
			case "principal_lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "client_lookup":
				clients.clientErr = failure
			case "missing_preview":
				in.PreviewID = 999
			case "public_preview":
				_, err = pq.WithContext(ctx).Where(pq.ID.Eq(preview.ID)).Update(pq.ClientVisible, true)
				require.NoError(t, err)
			case "id":
				m.ids = &unavailableIDs{source: m.ids, at: 1}
			case "audit", "expired_audit":
				m.audit = func(_ context.Context, _ *gorm.DB, e audit.DomainEvent) error {
					if scenario == "expired_audit" || e.EventType == "clientwork.preview_share_requested" {
						return failure
					}
					return nil
				}
			case "insert":
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_share_insert BEFORE INSERT ON g_clientwork_preview_shares BEGIN SELECT RAISE(ABORT, 'insert unavailable'); END").Error)
			case "expired_update":
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_share_update BEFORE UPDATE ON g_clientwork_preview_shares BEGIN SELECT RAISE(ABORT, 'update unavailable'); END").Error)
			}
			err = db.Transaction(func(tx *gorm.DB) error { _, e := m.requestShareOn(ctx, tx, 3, in); return e })
			require.Error(t, err)
			after, err := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
			require.NoError(t, err)
			require.Equal(t, before, after, "failed replacement must retain original grant/version/expiry")
			count, err := q.WithContext(ctx).Count()
			require.NoError(t, err)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestPreviewShareApprovalFailuresRollBackActiveTransition(t *testing.T) {
	for _, scenario := range []string{"reason", "missing", "public", "missing_pin", "binding_id", "update", "already_rejected"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _, _ := harness(t)
			preview, share := previewShareFixture(t, m, db)
			ctx := testContext(t, 6)
			q := query.BindPreviewShare(db, m.names)
			in := PreviewShareDecision{Decision: "APPROVE", Reason: "Audience checked"}
			id := share.ID
			switch scenario {
			case "reason":
				in.Reason = ""
			case "missing":
				id = 999
			case "public":
				pq := query.BindPreview(db, m.names)
				_, e := pq.WithContext(ctx).Where(pq.ID.Eq(preview.ID)).Update(pq.ClientVisible, true)
				require.NoError(t, e)
			case "missing_pin":
				m.files.PinOn = nil
			case "binding_id":
				m.ids = &unavailableIDs{source: m.ids, at: 1}
			case "update":
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_share_update BEFORE UPDATE ON g_clientwork_preview_shares BEGIN SELECT RAISE(ABORT, 'update unavailable'); END").Error)
			case "already_rejected":
				_, e := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).Update(q.State, "REJECTED")
				require.NoError(t, e)
			}
			before, e := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
			require.NoError(t, e)
			e = db.Transaction(func(tx *gorm.DB) error { _, err := m.decideShareOn(ctx, tx, 3, id, 1, in); return err })
			require.Error(t, e)
			after, e := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
			require.NoError(t, e)
			require.Equal(t, before, after)
		})
	}
}

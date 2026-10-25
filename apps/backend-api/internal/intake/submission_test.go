package intake

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOwnSubmissionsFenceOwnerTenantAndSafeProjection(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"submitted_by_principal_id": 2, "external_ref": "caller-ref", "raw_content": "private source", "decline_reason": "private reasoning", "client_message": "Public explanation", "received_at": at}).Error)
	var original model.Record
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&original).Error)
	for _, id := range []int{11, 12, 13} {
		row := original
		row.ID = guid.ID(id)
		row.ChannelID = guid.ID(id)
		row.RefNo = id
		row.ReplyToken = "source-" + row.ID.String()
		if id == 12 {
			actor := guid.ID(9)
			row.SubmittedByPrincipalID = &actor
		}
		if id == 13 {
			row.TenantID = 8
		}
		require.NoError(t, db.Table(m.names.Records.String()).Create(&row).Error)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.listOwnSubmissionsOn(ctx, tx, 7, 0, 1, SubmissionFilter{Status: "CONVERTED", ExternalRef: "caller-ref", ReceivedFrom: &at, ReceivedTo: &at})
		require.NoError(t, err)
		require.True(t, page.HasMore)
		require.Len(t, page.Items, 1)
		require.EqualValues(t, 11, page.Items[0].ID)
		next, err := m.listOwnSubmissionsOn(ctx, tx, 7, page.Items[0].ID, 1, SubmissionFilter{})
		require.NoError(t, err)
		require.False(t, next.HasMore)
		require.Len(t, next.Items, 1)
		require.EqualValues(t, 10, next.Items[0].ID)
		own, err := m.ownSubmissionOn(ctx, tx, 7, 10)
		require.NoError(t, err)
		raw, err := json.Marshal(own)
		require.NoError(t, err)
		require.Contains(t, string(raw), "Public explanation")
		require.NotContains(t, string(raw), "private")
		require.NotContains(t, string(raw), "triage")
		require.NotContains(t, string(raw), "raw_content")
		for _, id := range []int{12, 13, 99} {
			_, err := m.ownSubmissionOn(ctx, tx, 7, guid.ID(id))
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		}
		_, err = m.ownSubmissionOn(ctx, tx, 8, 13)
		require.ErrorIs(t, err, ErrForbidden)
		_, err = m.listOwnSubmissionsOn(ctx, tx, 7, 0, 1, SubmissionFilter{Status: "BOGUS"})
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
}

func TestOwnSubmissionsRequireCurrentHumanAndTransactionalStorage(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	_, err := m.listOwnSubmissionsOn(ctx, db, 7, 0, 1, SubmissionFilter{})
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.ownSubmissionOn(ctx, nil, 7, 10)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = m.ownSubmissionOn(ctx, nil, 7, 0)
	require.ErrorIs(t, err, ErrInvalidArgument)
	failure := errors.New("identity storage unavailable")
	for _, tc := range []struct {
		name  string
		store identity.PrincipalStateStore
		want  error
	}{
		{"missing_capability", identity.PrincipalStateStore{}, ErrUnavailable},
		{"lock_failed", identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return failure }, ActiveHumanByID: m.brief.principals.ActiveHumanByID}, failure},
		{"lookup_failed", identity.PrincipalStateStore{Lock: m.brief.principals.Lock, ActiveHumanByID: func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
			return identity.Principal{}, false, failure
		}}, failure},
		{"inactive_human", identity.PrincipalStateStore{Lock: m.brief.principals.Lock, ActiveHumanByID: func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
			return identity.Principal{ID: 2}, false, nil
		}}, ErrForbidden},
		{"different_human", identity.PrincipalStateStore{Lock: m.brief.principals.Lock, ActiveHumanByID: func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
			return identity.Principal{ID: 3}, true, nil
		}}, ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyModule := *m
			copyModule.brief.principals = tc.store
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				page, err := copyModule.listOwnSubmissionsOn(ctx, tx, 7, 0, 1, SubmissionFilter{})
				require.ErrorIs(t, err, tc.want)
				require.Empty(t, page.Items)
				return nil
			}))
		})
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.listOwnSubmissionsOn(authn.WithClaims(ctx, &authn.Claims{TenantID: 8, PrincipalID: 2}), tx, 7, 0, 1, SubmissionFilter{})
		require.ErrorIs(t, err, ErrForbidden)
		require.Empty(t, page.Items)
		return nil
	}))
}

func TestOwnSubmissionsReceiptOrderAndStrictKeysetIsolation(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"submitted_by_principal_id": 2, "received_at": at}).Error)
	var original model.Record
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&original).Error)
	for _, id := range []guid.ID{11, 12, 13, 14, 15} {
		row := original
		row.ID, row.ChannelID, row.RefNo, row.ReplyToken = id, id, int(id), "order-"+id.String()
		switch id {
		case 11:
			row.ReceivedAt = at.Add(-time.Hour)
		case 13:
			other := guid.ID(9)
			row.SubmittedByPrincipalID = &other
		case 14:
			row.TenantID = 8
		case 15:
			row.Status = "RECEIVED"
		}
		require.NoError(t, db.Table(m.names.Records.String()).Create(&row).Error)
	}
	for _, tc := range []struct {
		sort string
		want []guid.ID
	}{{"", []guid.ID{12, 10, 11}}, {"-received_at", []guid.ID{12, 10, 11}}, {"received_at", []guid.ID{11, 10, 12}}} {
		t.Run("sort="+tc.sort, func(t *testing.T) {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				before := guid.ID(0)
				got := []guid.ID{}
				for i := 0; i < 4; i++ {
					page, err := m.listOwnSubmissionsOn(ctx, tx, 7, before, 1, SubmissionFilter{Sort: tc.sort, Status: "CONVERTED", ProjectID: 20})
					require.NoError(t, err)
					require.Len(t, page.Items, 1)
					before = page.Items[0].ID
					got = append(got, before)
					if !page.HasMore {
						break
					}
				}
				require.Equal(t, tc.want, got, "timestamps, equal-time IDs and ownership/filter fences must all hold on every page")
				return nil
			}))
		})
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, foreign := range []guid.ID{13, 14, 99} {
			page, err := m.listOwnSubmissionsOn(ctx, tx, 7, foreign, 1, SubmissionFilter{})
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			require.Empty(t, page.Items)
		}
		_, err := m.listOwnSubmissionsOn(ctx, tx, 7, 0, 1, SubmissionFilter{Sort: "raw_content"})
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
}

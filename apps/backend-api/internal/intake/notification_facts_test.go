package intake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPendingTriageUsesQualifyingSourceAndCurrentHumanAuthority(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	allow := true
	m.brief.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: allow}, nil
	}
	at := time.Now().UTC()
	project, origin := guid.ID(20), guid.ID(22)
	for i, kind := range []string{"CHANGE", "REWORK", "NEW", "REWORK"} {
		row := model.Record{ID: guid.ID(30 + i), TenantID: 7, RefNo: 30 + i, ChannelID: 3, ChannelKind: "API", ContactID: 1, Kind: kind, Status: "RECEIVED", Title: kind, RawContentFormat: "PLAIN", ProjectID: &project, ReceivedAt: at, ReplyToken: guid.ID(30 + i).String(), CreatedAt: at, UpdatedAt: at}
		if i == 1 {
			row.OriginAcceptanceID = &origin
		}
		row.RawContentHash, row.Version = strings.Repeat("a", 64), 1
		if kind == "CHANGE" {
			original, requirement := guid.ID(10), guid.ID(100)
			row.ChangeOfIntakeID, row.TargetRequirementID = &original, &requirement
		}
		require.NoError(t, db.Table(m.names.Records.String()).Create(&row).Error)
	}
	read := func(project guid.ID) []PendingTriageFact {
		var out []PendingTriageFact
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; out, err = m.pendingTriageOn(ctx, tx, 7, project); return err }))
		return out
	}
	require.Len(t, read(20), 2, "ordinary NEW and unbound REWORK are not qualifying Human conversion facts")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		project, channel, due, active, err := m.triageReminderSourceOn(ctx, tx, 7, 30)
		require.True(t, active)
		require.EqualValues(t, 20, project)
		require.EqualValues(t, 3, channel)
		require.WithinDuration(t, at.Add(30*time.Minute), due, time.Millisecond)
		return err
	}))
	require.Empty(t, read(21))
	require.NoError(t, db.Table(m.names.Records.String()).Where("id = ?", 30).Updates(map[string]any{"status": "CONVERTED", "requirement_id": 100}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, _, active, err := m.triageReminderSourceOn(ctx, tx, 7, 30)
		require.False(t, active)
		return err
	}))
	require.Len(t, read(20), 1)
	allow = false
	require.Empty(t, read(20), "revoked current permission removes pending sources")
}

func TestNotificationSourcesNeverPublishMissingUnscopedOrFailedReads(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	for _, id := range []guid.ID{10, 999} {
		project, channel, found, err := m.notificationSourceOn(ctx, db, 7, id)
		require.NoError(t, err)
		require.Equal(t, id == 10, found)
		if found {
			require.EqualValues(t, 20, project)
			require.EqualValues(t, 3, channel)
		} else {
			require.Zero(t, project)
			require.Zero(t, channel)
		}
		_, _, found, err = m.triagedNotificationSourceOn(ctx, db, 7, id)
		require.NoError(t, err)
		require.False(t, found, "a received source without a persisted assessment cannot trigger triaged notification")
	}
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("latest_card_revision", 1).Error)
	project, channel, found, err := m.triagedNotificationSourceOn(ctx, db, 7, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 20, project)
	require.EqualValues(t, 3, channel)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("project_id", nil).Error)
	for _, read := range []NotificationSource{m.notificationSourceOn, m.triagedNotificationSourceOn} {
		_, _, found, err := read(ctx, db, 7, 10)
		require.NoError(t, err)
		require.False(t, found, "unscoped sources must not produce project notifications")
	}
	_, _, _, found, err = m.triageReminderSourceOn(ctx, db, 7, 999)
	require.NoError(t, err)
	require.False(t, found)
	_, _, _, _, err = m.triageReminderSourceOn(ctx, nil, 7, 10)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.pendingTriageOn(ctx, nil, 7, 20)
	require.ErrorIs(t, err, ErrInvalidArgument)
	failure := errors.New("notification source storage failed")
	const hook = "regression_notification_read_failure"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) { tx.AddError(failure) }))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(hook)) })
	for _, read := range []NotificationSource{m.notificationSourceOn, m.triagedNotificationSourceOn} {
		project, channel, found, err := read(ctx, db, 7, 10)
		require.ErrorIs(t, err, failure)
		require.False(t, found)
		require.Zero(t, project)
		require.Zero(t, channel)
	}
	_, _, _, found, err = m.triageReminderSourceOn(ctx, db, 7, 10)
	require.ErrorIs(t, err, failure)
	require.False(t, found)
	items, err := m.pendingTriageOn(ctx, db, 7, 20)
	require.ErrorIs(t, err, failure)
	require.Empty(t, items)
}

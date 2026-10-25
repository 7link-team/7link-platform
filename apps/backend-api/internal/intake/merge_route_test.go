package intake

import (
	"7link.com/platform/internal/intake/model"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestMergeRoutingAndClientProjectionFenceOriginalContactAndEveryHop(t *testing.T) {
	m, db, ctx, allowed, _ := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("principal_id", 2).Error)
	var first model.Record
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").First(&first).Error)
	target := first
	target.ID = 11
	target.RefNo = 2
	target.ReplyToken = "merge-target-11"
	target.Title = "Other confidential requirement"
	require.NoError(t, db.Table(m.names.Records.String()).Create(&target).Error)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"status": "MERGED", "merged_into_intake_id": 11}).Error)
	read := func() (ClientMergeProjection, error) {
		var out ClientMergeProjection
		err := db.Transaction(func(tx *gorm.DB) error { var err error; out, err = m.clientMergeOn(ctx, tx, 7, 20, 10); return err })
		return out, err
	}
	got, err := read()
	require.NoError(t, err)
	require.True(t, got.Merged)
	require.NotNil(t, got.TargetID)
	require.Equal(t, target.ID, *got.TargetID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		route, err := m.mergeRouteOn(ctx, tx, 7, 20, 10)
		require.Equal(t, target.ID, route.TargetID)
		require.Equal(t, first.RefNo, route.SourceRef)
		require.Equal(t, target.RequirementID, route.TargetRequirementID)
		return err
	}))
	// The caller is still allowed to view the source, never the other contact.
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=11").Update("contact_id", 999).Error)
	got, err = read()
	require.NoError(t, err)
	require.Nil(t, got.TargetID)
	require.Nil(t, got.TargetRef)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "target_id")
	require.NotContains(t, string(raw), target.Title)
	// A same-contact final target cannot erase an intervening foreign contact.
	final := target
	final.ID = 12
	final.RefNo = 3
	final.ReplyToken = "merge-target-12"
	require.NoError(t, db.Table(m.names.Records.String()).Create(&final).Error)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=11").Updates(map[string]any{"status": "MERGED", "merged_into_intake_id": 12}).Error)
	got, err = read()
	require.NoError(t, err)
	require.Nil(t, got.TargetID)
	// Current policy still applies to internal routing, before any work binding.
	*allowed = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.mergeRouteOn(ctx, tx, 7, 20, 10); return err }), ErrForbidden)
	*allowed = true
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("state", "BLOCKED").Error)
	_, err = read()
	require.Error(t, err)
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("state", "ACTIVE").Error)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=12").Updates(map[string]any{"status": "MERGED", "merged_into_intake_id": 10}).Error)
	_, err = read()
	require.ErrorIs(t, err, ErrConflict)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=12").Updates(map[string]any{"status": "CONVERTED", "merged_into_intake_id": nil, "project_id": 21}).Error)
	_, err = read()
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = m.clientMergeOn(ctx, db, 7, 20, 10)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

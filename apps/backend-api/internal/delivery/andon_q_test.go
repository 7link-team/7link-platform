package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAndonCanonicalEventsLinkExactPullAndRollback(t *testing.T) {
	m, db, ctx, work, _ := dependencyHarness(t)
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	m.service.events = realDeliveryPublisher(t, db)
	change := func(version int, release bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.changeAndonOn(ctx, tx, 1, 99, work.ID, 11, 11, andonBody{Version: version, Reason: "Investigate exact requirement inconsistency"}, release)
			return err
		})
	}
	require.NoError(t, change(work.Version, false))
	pulls := query.BindWorkTransitions(db, m.service.names)
	pull, err := pulls.WithContext(ctx).Where(pulls.WorkItemID.Eq(work.ID), pulls.ReasonCode.Eq("ANDON_PULLED")).First()
	require.NoError(t, err)
	require.NoError(t, change(work.Version+1, true))
	var records []struct {
		EventType string
		Data      string
	}
	require.NoError(t, db.Table("domain_events").Where("event_type IN ?", []string{"andon.pulled", "andon.cleared"}).Order("id").Find(&records).Error)
	require.Len(t, records, 2)
	for _, row := range records {
		var data map[string]any
		require.NoError(t, json.Unmarshal([]byte(row.Data), &data))
		require.Equal(t, pull.ID.String(), data["pull_id"])
		require.Equal(t, work.ID.String(), data["target_id"])
		require.Equal(t, "Investigate exact requirement inconsistency", data["reason"])
	}
	original := m.service.events
	m.service.events = projectPublisher(func(c context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
		if event.Type == "andon.pulled" {
			return 0, errors.New("canonical source unavailable")
		}
		return original.Publish(c, tx, event)
	})
	require.Error(t, change(work.Version+2, false))
	w := query.BindWorkItems(db, m.service.names)
	current, err := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.Nil(t, current.PausedAt)
	require.Equal(t, work.Version+2, current.Version)
}

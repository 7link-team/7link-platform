package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestScheduleHTTPReplayRechecksCurrentAuthority(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	now := time.Now().UTC().Truncate(time.Millisecond)
	end := now.Add(time.Hour)
	in := &scheduleInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: work.ID.String()}, Key: "schedule-once", Body: scheduleBody{Version: work.Version, Start: &now, End: &end, Timezone: "UTC", Reason: "Agreed delivery window"}}
	first, err := m.scheduleWorkHTTP(ctx, in)
	require.NoError(t, err)
	again, err := m.scheduleWorkHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data, again.Body.Data)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.scheduleWorkHTTP(ctx, in)
	require.Error(t, err)
	_ = db
}

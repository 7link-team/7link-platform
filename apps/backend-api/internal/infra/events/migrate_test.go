package events

import (
	"7link.com/platform/internal/infra/events/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMigrationDefaultsAndUnsequencedUniqueness(t *testing.T) {
	m, db := newTestPublisher(t)
	// Raw writes deliberately omit columns with declared database defaults.
	for _, id := range []int{20, 21} {
		require.NoError(t, db.Exec(`INSERT INTO ev_domain_events
 (id,tenant_id,event_type,occurred_at,actor_kind,subject_kind,subject_id,topics,data,created_at)
 VALUES (?,1,'project.created',?,'SYSTEM','project',2,'[]','{}',?)`, id, m.now(), m.now()).Error)
	}
	var rows []model.EventRow
	require.NoError(t, db.Table("ev_domain_events").Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Nil(t, row.StreamSeq)
		require.Nil(t, row.ActorID)
		require.Equal(t, 1, row.SchemaVersion)
		require.True(t, row.Durable)
		require.Equal(t, "SIGNAL", row.RealtimeMode)
		require.Zero(t, row.SubjectVersion)
		require.Empty(t, row.AuthoritySource)
		require.Empty(t, row.CorrelationID)
	}
	require.NoError(t, db.Exec("UPDATE ev_domain_events SET stream_seq=1 WHERE id=20").Error)
	require.Error(t, db.Exec("UPDATE ev_domain_events SET stream_seq=1 WHERE id=21").Error)
	require.Error(t, db.Exec("UPDATE ev_domain_events SET actor_kind='ROOT' WHERE id=21").Error)
	require.Error(t, db.Exec("UPDATE ev_domain_events SET realtime_mode='UNKNOWN' WHERE id=21").Error)
}

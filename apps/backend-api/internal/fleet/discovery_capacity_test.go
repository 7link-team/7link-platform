package fleet

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/fleet/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDiscoveryGatewayMySQLMigrationCapacityForAcceptedCatalogs(t *testing.T) {
	facts := discoveryFacts()
	in := gatewayWrite()
	facts[0].Models = nil
	in.Models = nil
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("m%04d%s", i, strings.Repeat("x", 155))
		facts[0].Models = append(facts[0].Models, id)
		in.Models = append(in.Models, GatewayModel{ID: id, Provider: "provider", Runtime: "CODEX"})
	}
	in.DefaultModel = in.Models[0].ID
	require.NoError(t, validateDiscovery("MODELS", facts))
	m, db, ctx, _, _ := enrollmentHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.validateGatewayOn(ctx, tx, 7, in) }))
	for _, tc := range []struct {
		name, column string
		payload      any
	}{
		{"00007_discovery.sql", "result_json", facts},
		{"00008_gateway.sql", "records_json", in.GatewayRecords},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(tc.payload)
			require.NoError(t, err)
			require.Greater(t, len(payload), 65535, "accepted catalogs exceed MySQL TEXT capacity")
			sql, err := migrationsFS.ReadFile("sql/mysql/" + tc.name)
			require.NoError(t, err)
			require.Regexp(t, `(?i)\b`+tc.column+`\s+MEDIUMTEXT\b`, string(sql), "MySQL must store the full accepted JSON payload")
			if tc.column == "result_json" {
				require.NotContains(t, string(sql), "DEFAULT '[]'", "MySQL TEXT defaults require an expression")
			}
		})
	}
}

func TestDiscoveryCorruptCacheAndEventFailurePreserveLastSnapshot(t *testing.T) {
	f := inspectionHarness(t)
	report := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			_, err := f.m.RecordDiscoveryOn(f.ctx, tx, 7, 1, f.session.ID, 1, "MODELS", discoveryFacts())
			return err
		})
	}
	require.NoError(t, report())
	q := query.BindInspections(f.db, f.m.names)
	prior, err := q.WithContext(f.ctx).First()
	require.NoError(t, err)
	f.publisher.fail = true
	require.Error(t, report())
	f.publisher.fail = false
	count, err := q.WithContext(f.ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "failed publication must roll back replacement and pruning")
	remaining, err := q.WithContext(f.ctx).First()
	require.NoError(t, err)
	require.Equal(t, prior.ID, remaining.ID)
	require.Equal(t, prior.ResultJSON, remaining.ResultJSON)
	_, err = q.WithContext(f.ctx).Where(q.ID.Eq(prior.ID)).Update(q.ResultJSON, "invalid-json")
	require.NoError(t, err)
	require.ErrorIs(t, report(), ErrUnavailable, "corrupt persisted cache cannot become an empty successful catalog")
	count, err = q.WithContext(f.ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

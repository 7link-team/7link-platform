package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestEscapeEvalSourceHookUsesPersistedFactAndFreezesReference(t *testing.T) {
	m, db, ctx, _, id := escapeHarness(t)
	calls := 0
	m.escapeEval = func(_ context.Context, _ *gorm.DB, fact EscapeEvalFact) (guid.ID, bool, error) {
		calls++
		require.Equal(t, id, fact.ID)
		require.Equal(t, guid.ID(99), fact.ProjectID)
		var snapshot map[string]any
		require.NoError(t, json.Unmarshal(fact.Snapshot, &snapshot))
		require.Equal(t, "REJECTED", snapshot["human_acceptance"])
		require.NotContains(t, snapshot, "expected")
		return 700, true, nil
	}
	data, _ := json.Marshal(map[string]any{"escape_key": "work:44:claim:55", "work_item_id": "44"})
	event := events.Envelope{EventID: 99, TenantID: 1, ProjectID: 99, EventType: "quality.escaped", Data: data}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeQualityEscape(ctx, tx, event) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeQualityEscape(ctx, tx, event) }))
	require.Equal(t, 2, calls)
	q := query.BindQualityEscapes(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, "700", *row.EvalCaseRef)
	m.escapeEval = func(context.Context, *gorm.DB, EscapeEvalFact) (guid.ID, bool, error) { return 701, false, nil }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeQualityEscape(ctx, tx, event) }), ErrVersion)
	row, err = q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, "700", *row.EvalCaseRef)
}

func TestProjectArchiveFactRequiresRevocationReceipt(t *testing.T) {
	m, db, ctx, _, in := delegationHarness(t)
	v, err := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, err)
	read := func() []ProjectArchiveFact {
		var out []ProjectArchiveFact
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.projectArchiveFactsOn(ctx, tx, 1, 99); return e }))
		return out
	}
	require.False(t, read()[0].Ready)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.revokeDelegationOn(ctx, tx, 1, v.ID, "Explicitly stop delegated authority")
		return e
	}))
	require.True(t, read()[0].Ready)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.projectArchiveFactsOn(ctx, tx, 2, 99)
		require.Empty(t, out)
		return e
	}))
	_, err = m.projectArchiveFactsOn(ctx, db, 1, 99)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestProjectArchivePendingSpawnRequiresActualDisposition(t *testing.T) {
	m, db, human, agent, _, in := agentSpawnFixture(t)
	var row model.SpawnRequest
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = m.createAgentSpawnRequestOn(agent, tx, 1, in)
		return err
	}))
	read := func(project int64) []ProjectArchiveFact {
		var out []ProjectArchiveFact
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.projectArchiveFactsOn(human, tx, 1, guid.ID(project))
			return err
		}))
		return out
	}
	require.Empty(t, read(100))
	require.Len(t, read(99), 1)
	require.Equal(t, "AGENT_SPAWN_APPROVAL", read(99)[0].Kind)
	require.False(t, read(99)[0].Ready)
	q := query.BindSpawnRequests(db, m.seed.names)
	for _, invalid := range []string{"invalid json", "null", "{}"} {
		_, err := q.WithContext(human).Where(q.ID.Eq(row.ID)).Update(q.Payload, invalid)
		require.NoError(t, err)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.projectArchiveFactsOn(human, tx, 1, 99); return err }), ErrUnavailable)
	}
	_, err := q.WithContext(human).Where(q.ID.Eq(row.ID)).Update(q.Payload, row.Payload)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.changeAgentSpawnRequestOn(human, tx, 1, row.ID, row.Version, "REJECT", "Explicit archive disposition")
		return err
	}))
	require.Empty(t, read(99))
}

func TestArchiveEvaluationConnectorFreezesWriterAndPropagatesSourceFailure(t *testing.T) {
	m, db, ctx, _, id := escapeHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideArchiveEvaluation(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[EscapeEvalConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalidArgument)
	fault := errors.New("evaluation case source failed")
	calls := 0
	writer := func(_ context.Context, tx *gorm.DB, fact EscapeEvalFact) (guid.ID, bool, error) {
		calls++
		require.NotNil(t, tx)
		require.Equal(t, id, fact.ID)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(fact.Snapshot, &fields))
		require.JSONEq(t, `"REJECTED"`, string(fields["human_acceptance"]))
		return 0, false, fault
	}
	require.NoError(t, connector.Bind(writer))
	require.ErrorIs(t, connector.Bind(writer), ErrInvalidArgument)
	q := query.BindQualityEscapes(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.proposeEscapeEvalOn(ctx, tx, row) }), fault)
	require.Equal(t, 1, calls)
	saved, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Nil(t, saved.EvalCaseRef)
	require.Equal(t, 1, saved.Version)
}

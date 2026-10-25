package agents

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentContextUsesOwnDurableEvidenceAndLocksExecutionPrincipalsFirst(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	planHash := strings.Repeat("a", 64)
	called := false
	original := m.principals.LockActiveRow
	m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (p identity.Principal, active bool, err error) {
		require.True(t, called, "execution must acquire its sorted principal locks before Agent SELF authorization")
		return original(ctx, tx, id)
	}
	var evidence guid.ID
	m.contextReader = func(_ context.Context, tx *gorm.DB, tenant, agent, execution guid.ID, persist PersistContext) ([]knowledge.ContextItem, error) {
		called = true
		var err error
		evidence, err = persist(execution, planHash)
		return []knowledge.ContextItem{{EntryID: 91, Label: "[K1]"}}, err
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			items, err := m.contextOn(ctx, tx, 1, 7, "88")
			require.Len(t, items, 1)
			if err != nil {
				return err
			}
			return m.verifyContextOn(ctx, tx, 1, 7, 88, evidence, planHash)
		}))
	}
	count, err := query.BindInboxEvent(db, m.names).WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.verifyContextOn(ctx, tx, 1, 7, 89, evidence, planHash) }), ErrNotFound)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.contextOn(agentContext(t, 2), tx, 1, 7, "88"); return err }), ErrForbidden)
}

func TestContextManifestCurrentSourceFailuresDoNotPublishEvidence(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	_, err := m.contextOn(ctx, db, 1, 7, "bad")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = m.contextOn(ctx, db, 1, 7, "88")
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorIs(t, m.verifyContextOn(ctx, db, 1, 7, 88, 1, "bad"), ErrForbidden)
	for _, mode := range []string{"source", "hash", "outbox"} {
		original := m.events
		fault := errors.New("context source failed")
		m.contextReader = func(_ context.Context, tx *gorm.DB, tenant, agent, execution guid.ID, persist PersistContext) ([]knowledge.ContextItem, error) {
			if mode == "source" {
				return nil, fault
			}
			hash := strings.Repeat("a", 64)
			if mode == "hash" {
				hash = "short"
			}
			_, e := persist(execution, hash)
			return nil, e
		}
		if mode == "outbox" {
			m.events = badPublisher{}
		}
		result, e := m.manifestHTTP(ctx, &ManifestInput{TeamPath: TeamPath{TenantID: "1"}, ContextInput: ContextInput{ExecutionID: "88"}})
		require.Error(t, e)
		require.Nil(t, result)
		m.events = original
		n, e := query.BindInboxEvent(db, m.names).WithContext(ctx).Count()
		require.NoError(t, e)
		require.Zero(t, n)
	}
	m.contextReader = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, PersistContext) ([]knowledge.ContextItem, error) {
		return []knowledge.ContextItem{{EntryID: 91, Label: "[K1]"}}, nil
	}
	result, err := m.manifestHTTP(ctx, &ManifestInput{TeamPath: TeamPath{TenantID: "1"}, ContextInput: ContextInput{ExecutionID: "88"}})
	require.NoError(t, err)
	require.EqualValues(t, 91, result.Body.Data.Knowledge[0].EntryID)
}

func TestContextConnectorBindsOnceAndDeliversRealManifestItems(t *testing.T) {
	m, _, _ := agentHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideContextEvidence(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[ContextConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalid)
	calls := 0
	source := ReadContext(func(_ context.Context, _ *gorm.DB, tenant, agent, execution guid.ID, _ PersistContext) ([]knowledge.ContextItem, error) {
		calls++
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 7, agent)
		require.EqualValues(t, 88, execution)
		return []knowledge.ContextItem{{EntryID: 91, Label: "[K1]"}}, nil
	})
	require.NoError(t, connector.Bind(source))
	require.ErrorIs(t, connector.Bind(source), ErrConflict)
	result, err := m.manifestHTTP(agentContext(t, 7), &ManifestInput{TeamPath: TeamPath{TenantID: "1"}, ContextInput: ContextInput{ExecutionID: "88"}})
	require.NoError(t, err)
	require.EqualValues(t, 91, result.Body.Data.Knowledge[0].EntryID)
	require.Equal(t, 1, calls)
}

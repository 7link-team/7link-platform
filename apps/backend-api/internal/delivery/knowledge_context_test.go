package delivery

import (
	"context"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkKnowledgeContextPersistsOnlyValidatedCanonicalReferences(t *testing.T) {
	m, db, ctx, work, input := readinessHarness(t)
	m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	projects := query.BindProjects(db, m.service.names)
	_, err := projects.WithContext(ctx).Where(projects.ProjectID.Eq(guid.ID(99))).Update(projects.Stage, "ACTIVE")
	require.NoError(t, err)
	m.knowledgeContext.ResolveOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, ids []guid.ID) ([]knowledge.ContextItem, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		if len(ids) != 1 || ids[0] != 9007199254740995 {
			return nil, ErrWorkForbidden
		}
		return []knowledge.ContextItem{{EntryID: ids[0]}}, nil
	}
	input.ContextRefs = []workContextRef{{Kind: "KNOWLEDGE", Ref: "9007199254740995"}}
	prepared, err := prepareTestWork(t, m, db, ctx, work.ID, work.Version, &input)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ids, err := m.workKnowledgeContextOn(ctx, tx, 1, 99, work.ID)
		require.Equal(t, []guid.ID{9007199254740995}, ids)
		return err
	}))
	input.ContextRefs[0].Ref = "90"
	_, err = prepareTestWork(t, m, db, ctx, work.ID, prepared.Version, &input)
	require.ErrorIs(t, err, ErrWorkForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ids, err := m.workKnowledgeContextOn(ctx, tx, 1, 99, work.ID)
		require.Equal(t, []guid.ID{9007199254740995}, ids)
		return err
	}))
}

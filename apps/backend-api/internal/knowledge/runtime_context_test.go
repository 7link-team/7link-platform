package knowledge

import (
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRuntimeContextFreezesAuthorizedIndexedEntriesWithoutCountingRetrieval(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	entry := readKnowledgeChunks(t, m, db, source).Items[0].EntryID
	var items []ContextItem
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		items, err = m.contextItemsOn(ctx, tx, 1, 3, []guid.ID{entry})
		return err
	}))
	require.Len(t, items, 1)
	require.Equal(t, "[K1]", items[0].Label)
	require.NotEmpty(t, items[0].Body)
	require.Len(t, items[0].ContentHash, 64)
	var receipts int64
	require.NoError(t, query.BindUsageReceipts(db, m.names).Count(&receipts).Error)
	require.Zero(t, receipts)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.contextItemsOn(ctx, tx, 1, 3, []guid.ID{entry, entry})
		return err
	}), ErrInvalid)
	tampered := append([]ContextItem{}, items...)
	tampered[0].Body = "Invented content"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.validateContextItemsOn(ctx, tx, 1, 3, tampered) }), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.invalidateSourceOn(ctx, tx, 1, source.ID, source.Version)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.validateContextItemsOn(ctx, tx, 1, 3, items) }), ErrConflict)
}

func TestSnapshotReadAuthorizationUsesCurrentScopeWithoutRuntimeValidity(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, memoryInput())
	item := ContextItem{EntryID: entry.ID, Version: entry.Version, Title: entry.Title, Body: entry.Body, ContentHash: entry.ContentHash, RetrievalLogID: 90}
	q := query.BindEntries(db, m.names)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).Update(q.State, "REVOKED")
	require.NoError(t, err)
	old := m.tenantPolicy
	revoked := false
	m.tenantPolicy = func(ctx context.Context, tx *gorm.DB, tenant guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "agent_workspace_read" || (revoked && permission == "knowledge_read") {
			return authz.DataConstraint{}, nil
		}
		return old(ctx, tx, tenant, permission, actor)
	}
	read := func(actor guid.ID, value ContextItem) error {
		return db.Transaction(func(tx *gorm.DB) error {
			return m.authorizeSnapshotOn(knowledgeContext(t, actor), tx, 1, 3, []ContextItem{value})
		})
	}
	require.NoError(t, read(2, item), "owner may read retained revoked memory without an active chunk or executor claim")
	require.ErrorIs(t, read(3, item), ErrForbidden, "execution access does not grant another Agent's private scope")
	revoked = true
	require.ErrorIs(t, read(2, item), ErrForbidden, "current knowledge permission is still required")
	revoked = false
	frozen := InjectionResult{Rules: []InjectionItem{}, Items: []InjectionItem{{ContextItem: item, Scope: "AGENT"}}}
	raw, err := json.Marshal(frozen)
	require.NoError(t, err)
	require.NoError(t, query.BindInjectionLogs(db, m.names).Create(&model.InjectionLog{ID: 90, TenantID: 1, ProjectID: 3, AgentPrincipalID: 7, ExecutionID: 91, WorkItemID: 92, ResultJSON: string(raw), CreatedAt: m.now()}).Error)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).Delete()
	require.NoError(t, err)
	require.NoError(t, read(2, item), "an exact immutable log retains the original source scope after purge")
	require.ErrorIs(t, read(3, item), ErrForbidden)
	altered := item
	altered.Body = "invented snapshot"
	require.ErrorIs(t, read(2, altered), ErrForbidden)
	missing := item
	missing.RetrievalLogID = 0
	require.Error(t, read(2, missing), "absence alone never authorizes bytes")
	revoked = true
	// Retained AGENT scope uses the project's current IAM after purge as well.
	oldProject := m.policy
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, read(2, item), ErrForbidden)
	m.policy = oldProject
}

func TestFrozenInjectionContextValidationRejectsMutationAndMissingSource(t *testing.T) {
	m, db, request := injectionHarness(t)
	draft := sourceDraft()
	draft.Title = "Ledger guide"
	draft.Content = "Ledger evidence remains authoritative."
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, draft), nil, false)
	selected := buildInjectionTest(t, m, db, request)
	require.Len(t, selected.Items, 1)
	items := []ContextItem{selected.Items[0].ContextItem}
	validate := func(v []ContextItem) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.validateContextItemsOn(knowledgeContext(t, 7), tx, 1, 99, v) })
	}
	require.NoError(t, validate(items))
	changed := append([]ContextItem{}, items...)
	changed[0].Body = "Caller substituted bytes"
	require.ErrorIs(t, validate(changed), ErrConflict)
	require.ErrorIs(t, validate(append(items, items[0])), ErrConflict)
	failure := errors.New("frozen validation unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("frozen-context-query", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	require.NoError(t, validate(items))
	total := calls
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, validate(items), failure, "context validation stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("frozen-context-query"))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.invalidateSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, source.Version)
		return e
	}))
	require.ErrorIs(t, validate(items), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.authorizeSnapshotOn(knowledgeContext(t, 2), tx, 1, 99, append(items, items[0]))
	}))
	for _, item := range []ContextItem{{}, {EntryID: items[0].EntryID, RetrievalLogID: 999}} {
		if item.EntryID == 0 {
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				return m.authorizeSnapshotOn(knowledgeContext(t, 2), tx, 1, 99, []ContextItem{item})
			}), ErrInvalid)
		}
	}
	require.ErrorIs(t, m.authorizeSnapshotOn(knowledgeContext(t, 2), db, 1, 99, items), ErrInvalid)
	require.ErrorIs(t, m.authorizeSnapshotOn(knowledgeContext(t, 2), nil, 1, 99, items), ErrInvalid)
}

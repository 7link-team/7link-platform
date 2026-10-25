package knowledge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRuleConflictPolarityAndAcknowledgement(t *testing.T) {
	require.True(t, conflictingRules("Deploy using verified builds only", "Never deploy using verified builds only"))
	require.False(t, conflictingRules("Deploy using verified builds only", "Deploy using verified builds only"))
	require.False(t, conflictingRules("Deploy using verified builds only", "Never delete customer billing records"))
	require.True(t, ruleNegative("不要跳过验证"))
	require.False(t, ruleNegative("不要禁止验证"))
	m, db, _ := knowledgeHarness(t)
	var first, proposed EntryView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.writeOn(knowledgeContext(t, 2), tx, 1, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Deploy", Body: "Deploy using verified builds only", Provenance: "Human policy"})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, first.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Independent approval"})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		proposed, err = m.writeOn(knowledgeContext(t, 2), tx, 1, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Deploy", Body: "Never deploy using verified builds only", Provenance: "Human proposal"})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.listOn(knowledgeContext(t, 3), tx, 1, knowledgeListInput{Scope: "TEAM", State: "PENDING_REVIEW", Limit: 10})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		require.Equal(t, []guid.ID{first.ID}, page.Items[0].ConflictMemoryIDs)
		return err
	}))
	in := ReviewInput{Version: 1, Action: "APPROVED", Reason: "Review contradictory policy"}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 2), tx, 1, proposed.ID, in)
		return err
	}), ErrSelfReview, "independent-review authority precedes conflict acknowledgement")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, proposed.ID, in)
		return err
	}), ErrConflictUnacknowledged)
	in.ConflictsAcknowledged = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, proposed.ID, in)
		return err
	}))
	// Only actual tenant-qualified inclusion receipts contribute affected runs;
	// repeated citations and other entries/tenants never inflate the result.
	for i, receipt := range []model.UsageReceipt{
		{TenantID: 1, EntryID: proposed.ID, ExecutionID: 51, Kind: "INCLUSION"},
		{TenantID: 1, EntryID: proposed.ID, ExecutionID: 51, Kind: "CITATION"},
		{TenantID: 1, EntryID: first.ID, ExecutionID: 52, Kind: "INCLUSION"},
		{TenantID: 2, EntryID: proposed.ID, ExecutionID: 53, Kind: "INCLUSION"},
		{TenantID: 1, EntryID: proposed.ID, ExecutionID: 54, Kind: "INCLUSION"},
	} {
		receipt.ID = guid.ID(i + 100)
		receipt.SourceID = 10
		receipt.EvidenceID = guid.ID(i + 200)
		receipt.ContentHash = "hash"
		receipt.CreatedAt = m.now()
		require.NoError(t, query.BindUsageReceipts(db, m.names).Create(&receipt).Error)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		view, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, proposed.ID, ReviewInput{Version: 2, Action: "REVOKED", Reason: "Withdraw conflicting policy"})
		require.Equal(t, []guid.ID{51, 54}, view.AffectedExecutionIDs)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		firstPage, err := m.affectedExecutionsOn(knowledgeContext(t, 3), tx, 1, proposed.ID, "", 1)
		require.NoError(t, err)
		require.Equal(t, []guid.ID{51}, firstPage.Items)
		require.True(t, firstPage.HasMore)
		secondPage, err := m.affectedExecutionsOn(knowledgeContext(t, 3), tx, 1, proposed.ID, firstPage.NextCursor, 1)
		require.NoError(t, err)
		require.Equal(t, []guid.ID{54}, secondPage.Items)
		require.False(t, secondPage.HasMore)
		_, err = m.affectedExecutionsOn(knowledgeContext(t, 2), tx, 1, proposed.ID, firstPage.NextCursor, 1)
		require.ErrorIs(t, err, ErrInvalid, "cursor cannot transfer to another reader")
		return nil
	}))
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("review-facts", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	read := func(status int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/tenants/1/knowledge/"+proposed.ID.String()+"/affected-executions?limit=1", nil).WithContext(knowledgeContext(t, 3))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
		if status == http.StatusOK {
			require.Contains(t, rec.Body.String(), `"51"`)
			require.NotContains(t, rec.Body.String(), `"53"`)
		}
	}
	read(http.StatusOK)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	read(http.StatusForbidden)
}

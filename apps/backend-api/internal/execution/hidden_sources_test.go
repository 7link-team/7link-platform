package execution

import (
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHiddenSourceFactsUseFrozenProvenanceAndScope(t *testing.T) {
	f, in := verificationFixture(t)
	q := query.BindExecutions(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(in.ExecutionID)).First()
	require.NoError(t, err)
	v, err := view(row)
	require.NoError(t, err)
	v.Plan.ExecutorPrincipalID = 21
	v.Plan.ModelFamily = "frozen-family"
	v.Plan.Model = "frozen-model"
	raw, _ := json.Marshal(v.Plan)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(row.ID)).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)})
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		source, err := f.m.hiddenSourceOn(t.Context(), tx, row.TenantID, row.ProjectID, row.WorkItemID, row.ID)
		require.NoError(t, err)
		require.Equal(t, guid.ID(21), source.PrincipalID)
		require.Equal(t, "frozen-family", source.Family)
		require.Equal(t, v.Plan.Workdir, source.RepositoryPath)
		_, err = f.m.hiddenSourceOn(t.Context(), tx, row.TenantID, row.ProjectID+1, row.WorkItemID, row.ID)
		require.ErrorIs(t, err, ErrConflict)
		return nil
	}))
}

func TestHiddenContentCannotBeInjectedIntoExecutionContextOrPublicSurfaces(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	const hidden = "PRIVATE_ACCEPTANCE_SENTINEL_steps_expected_result"
	const safeText = "Public implementation guidance"
	safe := knowledge.ContextItem{EntryID: 90, Version: 1, Label: "[K1]", Title: "Public guide", Body: safeText, ContentHash: hash([]byte(safeText))}
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", Roles: []string{"EXECUTOR"}, RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "executor-family", AuthorizationMode: "READ_ONLY"}, true, nil
	}
	f.m.knowledge.work = delivery.WorkKnowledgeContext{ReadOn: func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID) ([]guid.ID, error) {
		require.Equal(t, []guid.ID{7, 9, 3}, []guid.ID{tenant, project, work})
		return []guid.ID{90}, nil
	}}
	var resolvedActors []guid.ID
	f.m.knowledge.context = knowledge.RuntimeContext{
		AuthorizeSnapshotOn: func(ctx context.Context, _ *gorm.DB, tenant, project guid.ID, items []knowledge.ContextItem) error {
			require.Equal(t, guid.ID(11), authn.PrincipalIDFromContext(ctx))
			require.Equal(t, []guid.ID{7, 9}, []guid.ID{tenant, project})
			require.Equal(t, []knowledge.ContextItem{safe}, items)
			return nil
		},
		ResolveOn: func(ctx context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) ([]knowledge.ContextItem, error) {
			require.Equal(t, []guid.ID{90}, ids, "posted hidden references must never reach the knowledge resolver")
			resolvedActors = append(resolvedActors, authn.PrincipalIDFromContext(ctx))
			return []knowledge.ContextItem{safe}, nil
		},
		ValidateOn: func(_ context.Context, _ *gorm.DB, _, _ guid.ID, items []knowledge.ContextItem) error {
			require.Equal(t, []knowledge.ContextItem{safe}, items)
			return nil
		},
	}
	f.m.knowledge.agentEvidence.VerifyOn = func(_ context.Context, _ *gorm.DB, tenant, agent, execution, evidence guid.ID, planHash string) error {
		require.Equal(t, []guid.ID{7, 21, 700}, []guid.ID{tenant, agent, evidence})
		require.Positive(t, execution)
		require.Len(t, planHash, 64)
		return nil
	}
	f.m.knowledge.recorder = knowledge.UsageRecorder{RecordOn: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, fact knowledge.UsageFact) error {
		require.Equal(t, safe.EntryID, fact.EntryID)
		require.Equal(t, safe.ContentHash, fact.ContentHash)
		return f.m.verifyKnowledgeUsageOn(ctx, tx, tenant, fact)
	}}
	in := validCreate()
	// Neither caller-supplied plaintext nor its hidden-content hash establishes
	// authority to inject a reference document into the executor's frozen plan.
	in.Plan.Knowledge = []knowledge.ContextItem{{EntryID: 999, Version: 1, Label: "[K9]", Title: hidden, Body: hidden, ContentHash: hash([]byte(hidden))}}
	var execution ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		execution, err = f.m.CreateOn(f.human, tx, 7, in)
		return err
	}))
	require.Equal(t, []guid.ID{11, 21}, resolvedActors)
	require.Equal(t, []knowledge.ContextItem{safe}, execution.Plan.Knowledge)
	require.Equal(t, []ContextRef{{Kind: "KNOWLEDGE", Ref: 90}}, execution.Plan.ContextRefs)
	row, err := query.BindExecutions(f.db, f.m.names).WithContext(t.Context()).Where(query.BindExecutions(f.db, f.m.names).ID.Eq(execution.ID)).First()
	require.NoError(t, err)
	require.NotContains(t, row.PlanJSON, hidden)
	require.NotContains(t, row.PlanJSON, hash([]byte(hidden)))
	persisted := false
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		items, err := f.m.agentKnowledgeOn(executorContext(t.Context(), 7, 21), tx, 7, 21, execution.ID, func(id guid.ID, planHash string) (guid.ID, error) {
			require.Equal(t, execution.ID, id)
			require.Equal(t, execution.PlanHash, planHash)
			persisted = true
			return 700, tx.Exec("INSERT INTO effects(kind) VALUES (?)", "context:"+id.String()+":"+planHash).Error
		})
		require.NoError(t, err)
		require.Equal(t, []knowledge.ContextItem{safe}, items)
		return err
	}))
	require.True(t, persisted, "actual Agent assembly must persist its source-bound inclusion evidence")
	var contextEvidence int64
	require.NoError(t, f.db.Table("effects").Where("kind = ?", "context:"+execution.ID.String()+":"+execution.PlanHash).Count(&contextEvidence).Error)
	require.EqualValues(t, 1, contextEvidence)
	path := "/executions/" + execution.ID.String()
	pulled := executionRouteRequest(t, handler, http.MethodPost, "/runner/execution-pulls", f.fence, 0, true)
	leases := executionRouteData[[]ExecutionView](t, pulled, http.StatusOK)
	require.Len(t, leases, 1)
	require.Equal(t, []knowledge.ContextItem{safe}, leases[0].Plan.Knowledge)
	require.NotNil(t, leases[0].LeaseExpiresAt)
	require.NotContains(t, pulled.Body.String(), hidden)
	require.NotContains(t, pulled.Body.String(), hash([]byte(hidden)))
	for i, receipt := range []ReceiptInput{{Kind: "ACCEPTED"}, {Kind: "LOG", Content: "Actual public runtime log"}, {Kind: "EVIDENCE", EvidenceKind: "OBSERVATION", EvidenceTitle: "Public answer", Content: "Used public guide [K1]"}} {
		receipt.DispatchEpoch, receipt.Seq = 1, int64(i+1)
		executionRouteData[ReceiptView](t, executionRouteRequest(t, handler, http.MethodPost, path+"/receipts", map[string]any{"fence": f.fence, "receipt": receipt}, 0, true), http.StatusOK)
	}
	detailResponse := executionRouteRequest(t, handler, http.MethodGet, path, nil, 0, false)
	detail := executionRouteData[ExecutionDetail](t, detailResponse, http.StatusOK)
	require.Len(t, detail.Logs, 1)
	require.Equal(t, "Actual public runtime log", detail.Logs[0].Content)
	require.Len(t, detail.Evidence, 1)
	listResponse := executionRouteRequest(t, handler, http.MethodGet, "/tenants/7/executions?project_id=9", nil, 0, false)
	require.Len(t, executionRouteData[ExecutionPage](t, listResponse, http.StatusOK).Items, 1)
	for name, body := range map[string]string{"detail with logs and evidence": detailResponse.Body.String(), "execution list": listResponse.Body.String()} {
		require.Contains(t, body, safeText, name)
		require.NotContains(t, body, hidden, name)
		require.NotContains(t, body, hash([]byte(hidden)), name)
	}
	// A caller cannot bypass canonical references by changing the reference kind
	// or by relabeling a private case ID as an ordinary knowledge entry.
	for _, ref := range []ContextRef{{Kind: "HIDDEN_ACCEPTANCE", Ref: 999}, {Kind: "KNOWLEDGE", Ref: 999}} {
		bad := validCreate()
		bad.Plan.ContextRefs = []ContextRef{ref}
		require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.CreateOn(f.human, tx, 7, bad); return err }), ErrInvalidArgument)
	}
}

func TestProvideHiddenSourceRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideHiddenSource(s))
	require.ErrorIs(t, f.m.provideHiddenSource(s), module.ErrServiceDuplicate)
}

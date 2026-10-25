package composition_test

import (
	"7link.com/platform/internal/execution"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStrategyBranchComposedCurrentAssignmentAndReplay(t *testing.T) {
	f := newApprovalFixture(t)
	db := f.app.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	var source struct {
		PlanJSON  string
		ProjectID guid.ID
	}
	require.NoError(t, db.Table("execution_executions").Where("id=601").Take(&source).Error)
	var plan execution.ExecutionPlan
	require.NoError(t, json.Unmarshal([]byte(source.PlanJSON), &plan))
	plan.RepositoryID = 4
	plan.WorkdirID = 701
	plan.MaxInputTokens = 4000
	plan.MaxOutputTokens = 2000
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, db.Table("execution_executions").Where("id=601").Updates(map[string]any{"state": "SUCCEEDED", "result_state": "CONFIRMED", "plan_json": string(raw), "plan_hash": fmt.Sprintf("%x", sha256.Sum256(raw))}).Error)
	require.NoError(t, db.Table("fleet_workdirs").Create(map[string]any{"id": 701, "tenant_id": 201, "machine_id": 401, "project_id": source.ProjectID, "repository_id": 4, "path": f.repo, "path_key": "branch-workspace", "version": 1, "created_at": now}).Error)
	// A strategy may launch only when the owning work lifecycle allows a new
	// execution. It must not bypass the single current execution slot.
	require.NoError(t, db.Table("delivery_work_items").Where("id=501").Updates(map[string]any{"state": "REWORK", "current_execution_id": nil, "risk": "LOW", "autonomy_level": "LOW"}).Error)
	web, err := module.Resolve[iamauthn.WebService](f.app.Modules().Services())
	require.NoError(t, err)
	token, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 302, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	router := moduleRouter(t, f.app)
	call := func(method, path, credential, key string, body any) *httptest.ResponseRecorder {
		b, e := json.Marshal(body)
		require.NoError(t, e)
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+credential)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
			req.Header.Set("If-Match", `"1"`)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	input := execution.StrategyBranch{Reason: "Alternative approach after review", Objective: "Use an alternative implementation"}
	created := call("POST", "/executions/601/branches", token, "branch-create", input)
	require.Equal(t, 201, created.Code, created.Body.String())
	var envelope struct{ Data execution.ExecutionView }
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &envelope))
	child := envelope.Data
	require.Equal(t, guid.ID(601), *child.ParentExecutionID)
	require.Equal(t, f.base, child.Plan.BaseCommit)
	require.Equal(t, "STRATEGY", child.BranchReason)
	replay := call("POST", "/executions/601/branches", token, "branch-create", input)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	list := call("GET", "/executions/601/branches?limit=1", token, "", nil)
	require.Equal(t, 200, list.Code, list.Body.String())
	require.Contains(t, list.Body.String(), `"has_more":true`)
	// A second new command is rejected by the real work owner while the first
	// branch owns its current execution. The cached command remains safe to replay.
	blocked := call("POST", "/executions/601/branches", token, "second-active", input)
	require.Equal(t, 409, blocked.Code, blocked.Body.String())

	require.NoError(t, db.Table("delivery_work_items").Where("id=501").Update("risk", "HIGH").Error)
	highRisk := call("POST", "/executions/601/branches", token, "branch-create", input)
	require.Equal(t, 409, highRisk.Code, highRisk.Body.String())
	require.NoError(t, db.Table("delivery_work_items").Where("id=501").Update("risk", "LOW").Error)
	var audit, outbox int64
	require.NoError(t, db.Table("iam_audit_events").Where("tenant_id=201 AND event_type='execution.branch_created' AND target_id=?", child.ID).Count(&audit).Error)
	require.EqualValues(t, 1, audit)
	require.NoError(t, db.Table("domain_events").Where("tenant_id=201 AND event_type='execution.branch_created' AND subject_id=?", child.ID).Count(&outbox).Error)
	require.EqualValues(t, 1, outbox)

	require.Equal(t, 200, call("GET", "/executions/601", token, "", nil).Code)
	proof := call("GET", "/executions/601/evidence-provenance?limit=50&sort=-id&compare_execution_id="+child.ID.String(), token, "", nil)
	require.Equal(t, 200, proof.Code, proof.Body.String())
	for _, id := range []int{304, 305} {
		require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": id, "login_name": fmt.Sprintf("branch-reader-%d", id), "display_name": "Scoped branch reader", "status": "active", "created_at": now, "updated_at": now}).Error)
	}
	require.NoError(t, db.Table("iam_tenant_members").Create(map[string]any{"tenant_id": 201, "principal_id": 304, "status": "active", "created_at": now, "updated_at": now}).Error)
	require.NoError(t, db.Table("intake_contacts").Create(map[string]any{"id": 804, "tenant_id": 201, "principal_id": 304, "kind": "PERSON", "display_name": "External branch reader", "state": "ACTIVE", "version": 1, "created_at": now, "updated_at": now}).Error)
	external, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 304, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	require.Equal(t, 403, call("GET", "/executions/601/branches", external, "", nil).Code)
	require.Equal(t, 403, call("POST", "/executions/601/branches", external, "external-write", input).Code)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 202, "slug": "branch-foreign", "name": "Foreign tenant", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	bootstrap, err := module.Resolve[core.TenantBootstrapper](f.app.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 202, OwnerID: 305, DisplayName: "Foreign owner"}, now)
		return e
	}))
	foreign, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 305, "202", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	require.Equal(t, 404, call("GET", "/executions/601/branches", foreign, "", nil).Code)
	require.Equal(t, 404, call("POST", "/executions/601/branches", foreign, "foreign-write", input).Code)
	require.NoError(t, db.Table("delivery_assignments").Where("id=502").Updates(map[string]any{"status": "REVOKED", "active_slot": nil, "ended_at": now, "end_reason": "REVOKED"}).Error)
	denied := call("POST", "/executions/601/branches", token, "branch-create", input)
	require.Equal(t, 404, denied.Code, denied.Body.String())
	require.NoError(t, db.Table("iam_tenant_members").Where("tenant_id=201 AND principal_id=302").Update("status", "disabled").Error)
	require.Equal(t, 404, call("GET", "/executions/601/branches", token, "", nil).Code)
}

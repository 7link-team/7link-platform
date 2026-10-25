package composition_test

import (
	platformapp "7link.com/platform/internal/app"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	transportaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const scratchObjective = `This is a scratch permission-bridge test. Use Bash to run exactly these two commands in order, requesting permissions normally. First: python -c "from pathlib import Path; Path('hello.txt').write_bytes(bytes([104,101,108,108,111,10]))" . Second: python -c "from pathlib import Path; Path('denied.txt').write_text('forbidden')" . A Human API will allow the first and deny the second. After denial, stop without trying alternatives. Do not inspect files, use other tools, modify anything else, run other commands or commit. Report the denial.`

func TestFullAutoTransportAuditNeverRecordsStepUpProof(t *testing.T) {
	for _, status := range []int{201, 403, 422} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			sink := &capturingAuditSink{}
			rt := appenv.NewRuntime(appenv.NewIdentity("test", "test"))
			auditor := transportaudit.NewModule(rt, transportaudit.Config{Enabled: true, CaptureBodies: true, QueueSize: 32, BatchSize: 1, FlushInterval: time.Millisecond, MaxBodyBytes: 4096}, transportaudit.WithSinks(sink))
			require.NoError(t, auditor.Start(t.Context()))
			t.Cleanup(func() { require.NoError(t, auditor.Stop(context.Background())) })
			body := `{"reauth_proof":"PRIVATE-STEP-UP-PROOF","reason":"Human approves"}`
			if status == 422 {
				body = strings.TrimSuffix(body, "}")
			}
			business := (&execution.Module{}).HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if !assert.NoError(t, err) {
					return
				}
				if !assert.Equal(t, body, string(raw)) {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":0}`))
			}))
			request := httptest.NewRequest(http.MethodPut, "/executions/99/full-auto-grant", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			auditor.HTTPMiddleware(business).ServeHTTP(httptest.NewRecorder(), request)
			require.Eventually(t, func() bool { return len(sink.snapshot()) == 1 }, time.Second, time.Millisecond)
			require.NotContains(t, sink.snapshot()[0].Request.Body, "PRIVATE-STEP-UP-PROOF")
			require.Contains(t, sink.snapshot()[0].Request.Body, "confidential_resource")
		})
	}
}

type approvalFixture struct {
	app                                      *platformapp.App
	api                                      func(string, string, string, any, any) int
	humanToken, runnerToken, url, repo, base string
	engineer, runnerPrincipal                guid.ID
}

func newApprovalFixture(t *testing.T, configure ...func(*platformapp.AppConfig)) approvalFixture {
	t.Helper()
	app := bootstrapComposedAppConfigured(t, func(config *platformapp.AppConfig) {
		for _, apply := range configure {
			apply(config)
		}
	})
	db := app.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	insert := func(table string, row map[string]any) {
		t.Helper()
		require.NoError(t, db.Table(table).Create(row).Error)
	}
	insert("iam_tenants", map[string]any{"id": 201, "slug": "approval-live", "name": "Approval live", "status": "active", "version": 1, "created_at": now, "updated_at": now})
	insert("iam_principals", map[string]any{"id": 301, "login_name": "approval-human", "display_name": "Approval Human", "status": "active", "created_at": now, "updated_at": now})
	bootstrap, err := module.Resolve[core.TenantBootstrapper](app.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Approval Human"}, now)
		if e != nil {
			return e
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, now)
	}))
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 201, PrincipalID: 301})
	roles, err := module.Resolve[governance.SeededRoles](app.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, roles.EnsureTenant(actor, 201))
	web, err := module.Resolve[iamauthn.WebService](app.Modules().Services())
	require.NoError(t, err)
	humanToken, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	iam, err := module.Resolve[fleet.EnrollmentIAM](app.Modules().Services())
	require.NoError(t, err)
	account, err := iam.CreateAccount(actor, 201, "approval-runner", "Approval runner", "Live scratch test", nil)
	require.NoError(t, err)
	runnerToken, _, err := web.IssueTenantServiceAccountToken(t.Context(), account.ID.String(), "201", "", "", account.LoginName, 1)
	require.NoError(t, err)
	router := moduleRouter(t, app)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	api := func(method, path, token string, body any, out any) int {
		t.Helper()
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		req, e := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(raw))
		require.NoError(t, e)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, e := server.Client().Do(req)
		require.NoError(t, e)
		defer res.Body.Close()
		data, e := io.ReadAll(res.Body)
		require.NoError(t, e)
		if res.StatusCode >= 200 && res.StatusCode < 300 && out != nil {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(data, &envelope))
			require.NoError(t, json.Unmarshal(envelope.Data, out))
		}
		if res.StatusCode >= 400 && token != "" {
			t.Logf("API %s %s status=%d body=%s", method, path, res.StatusCode, data)
		}
		return res.StatusCode
	}
	require.Equal(t, 401, api("GET", "/tenants/201/inbox-items?kind=INTERACTION", "", nil, nil))
	var project struct {
		Project struct {
			ProjectID string `json:"project_id"`
		} `json:"project"`
	}
	require.Equal(t, 201, api("POST", "/tenants/201/projects", humanToken, map[string]any{"name": "Approval scratch", "code": "AP-LIVE"}, &project))
	require.NotEmpty(t, project.Project.ProjectID)
	// The fixture represents already active work; this test exercises approval,
	// rather than the independent project stage-transition workflow.
	require.NoError(t, db.Table("delivery_projects").Where("tenant_id=201 AND project_id=?", project.Project.ProjectID).Update("stage", "ACTIVE").Error)
	insert("iam_principals", map[string]any{"id": 302, "login_name": "approval-executor", "display_name": "Approval executor", "status": "active", "created_at": now, "updated_at": now})
	insert("iam_tenant_members", map[string]any{"tenant_id": 201, "principal_id": 302, "display_name": "Approval executor", "status": "active", "created_at": now, "updated_at": now})
	var engineer guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		engineer, e = roles.RoleIDOn(actor, tx, 201, "engineer")
		return e
	}))
	insert("iam_role_bindings", map[string]any{"tenant_id": 201, "role_id": engineer, "principal_id": 302, "scope_type": "entity", "scope_id": project.Project.ProjectID, "effect": "allow", "created_at": now})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return bootstrap.BumpRevision(t.Context(), tx, 201, now) }))

	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		hide(cmd)
		data, e := cmd.Output()
		require.NoError(t, e)
		return strings.TrimSpace(string(data))
	}
	run("init", "--quiet")
	run("-c", "user.name=Tester", "-c", "user.email=tester@localhost", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=", "commit", "--allow-empty", "-m", "baseline")
	base := run("rev-parse", "HEAD")
	insert("fleet_machines", map[string]any{"id": 401, "tenant_id": 201, "principal_id": account.ID, "name": "Approval scratch", "name_key": "approval-scratch", "state": "ONLINE", "drain_requested": false, "os": "WINDOWS", "slot_count": 1, "conn_epoch": 1, "current_session_id": 402, "last_seen_at": now, "version": 1, "created_at": now, "updated_at": now})
	insert("fleet_runner_sessions", map[string]any{"id": 402, "tenant_id": 201, "machine_id": 401, "incarnation_id": "approval-live", "conn_epoch": 1, "state": "ACTIVE", "created_at": now, "last_seen_at": now})
	insert("delivery_work_items", map[string]any{"id": 501, "tenant_id": 201, "project_id": project.Project.ProjectID, "number": 1, "title": "Approval scratch", "type": "TASK", "state": "IN_PROGRESS", "baseline_version_no": 1, "current_execution_id": 601, "accountable_principal_id": 301, "state_changed_at": now, "created_at": now, "updated_at": now, "created_by": 301})
	insert("delivery_assignments", map[string]any{"id": 502, "tenant_id": 201, "project_id": project.Project.ProjectID, "work_item_id": 501, "role": "EXECUTOR", "assignee_kind": "HUMAN", "assignee_principal_id": 302, "status": "ACTIVE", "active_slot": 501, "lease_ttl_ms": 600000, "lease_expires_at": now.Add(10 * time.Minute), "version": 1, "created_at": now, "updated_at": now, "created_by": 301})
	plan := execution.ExecutionPlan{Runtime: "CLAUDE_CODE", Model: "sonnet", ApprovalProtocol: "CLAUDE_CONTROL_V1", AuthorizationMode: "ON_REQUEST", ExecutorPrincipalID: 302, Workdir: repo, BaseCommit: base, Objective: scratchObjective}
	planBytes, err := json.Marshal(plan)
	require.NoError(t, err)
	insert("execution_executions", map[string]any{"id": 601, "tenant_id": 201, "project_id": project.Project.ProjectID, "work_item_id": 501, "assignment_id": 502, "machine_id": 401, "initiator_id": 302, "state": "RUNNING", "state_reason": "", "result_state": "NONE", "plan_json": string(planBytes), "plan_hash": fmt.Sprintf("%x", sha256.Sum256(planBytes)), "dispatch_epoch": 1, "last_seq": 1, "lease_expires_at": now.Add(10 * time.Minute), "version": 1, "created_at": now, "updated_at": now})

	return approvalFixture{app: app, api: api, humanToken: humanToken, runnerToken: runnerToken, url: server.URL, repo: repo, base: base, engineer: engineer, runnerPrincipal: account.ID}
}

// The non-admin executor retains work execution authority after only its
// interaction_request grant is revoked. The real composed IAM must deny the
// next request, even though the runner token and execution fence remain valid.
func TestApprovalComposedRequesterAuthority(t *testing.T) {
	f := newApprovalFixture(t)
	request := func(id string) int {
		return f.api("POST", "/executions/601/approval-requests", f.runnerToken, map[string]any{"fence": map[string]any{"machine_id": "401", "session_id": "402", "conn_epoch": 1}, "request": map[string]any{"dispatch_epoch": 1, "request_id": id, "tool": "Bash", "input": map[string]string{"command": "scratch-command"}}}, nil)
	}
	require.Equal(t, 200, request("authorized-request"))
	db := f.app.Runtime().Database().Write()
	bootstrap, err := module.Resolve[core.TenantBootstrapper](f.app.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Exec("DELETE FROM iam_role_permissions WHERE tenant_id=201 AND role_id=? AND permission_code='interaction_request'", f.engineer).Error; e != nil {
			return e
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, time.Now().UTC())
	}))
	require.Equal(t, 403, request("revoked-request"))
	var count int64
	require.NoError(t, db.Table("execution_approvals").Where("execution_id=601").Count(&count).Error)
	require.Equal(t, int64(1), count, "revocation must not persist another permission request")
}

// Opt-in, billable live acceptance. Build the runner's control test binary first
// and pass its absolute path in RUNNER_EXECUTION_TEST_BINARY. All API handlers,
// authorization, persisted fences, inbox reads and decisions are production.
func TestApprovalBackendClaudeLive(t *testing.T) {
	worker := os.Getenv("RUNNER_EXECUTION_TEST_BINARY")
	if worker == "" {
		t.Skip("requires RUNNER_EXECUTION_TEST_BINARY and authenticated Claude")
	}
	f := newApprovalFixture(t)
	db, api, humanToken, runnerToken, repo, base := f.app.Runtime().Database().Write(), f.api, f.humanToken, f.runnerToken, f.repo, f.base
	// Validate all real providers before incurring runtime usage. This request
	// is rolled back and contributes nothing to the live acceptance evidence.
	rollback := errors.New("rollback-only provider preflight")
	mod, ok := f.app.Modules().ByName(execution.Name)
	require.True(t, ok)
	runner := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypeServiceAccount, TenantID: 201, PrincipalID: f.runnerPrincipal})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := mod.(*execution.Module).RequestApprovalOn(runner, tx, 201, 601, execution.RunnerFence{MachineID: 401, SessionID: 402, ConnEpoch: 1}, execution.ApprovalRequest{DispatchEpoch: 1, RequestID: "provider-preflight", Tool: "Bash", Input: json.RawMessage(`{}`)})
		if err != nil {
			return err
		}
		return rollback
	}), rollback)
	private := filepath.Join(t.TempDir(), "worker.json")
	config, err := json.Marshal(map[string]string{"URL": f.url, "Token": runnerToken, "ExecutionID": "601", "MachineID": "401", "SessionID": "402", "Repository": repo, "BaseCommit": base, "Model": "sonnet", "Objective": scratchObjective})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(private, config, 0600))
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, worker, "-test.run=^TestApprovalBackendLiveWorker$", "-test.v", "-test.timeout=4m")
	hide(cmd)
	cmd.Env = append(os.Environ(), "RUNNER_APPROVAL_BACKEND_CONFIG="+private)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cancel()
		_ = cmd.Process.Kill()
	})
	seen := map[string]string{}
	allowed := `python -c "from pathlib import Path; Path('hello.txt').write_bytes(bytes([104,101,108,108,111,10]))"`
	denied := `python -c "from pathlib import Path; Path('denied.txt').write_text('forbidden')"`
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			t.Log(output.String())

			require.NoError(t, err)
			require.Contains(t, seen, "ALLOW")
			require.Contains(t, seen, "DENY_EXACT")
			var count int64
			require.NoError(t, db.Table("execution_approvals").Where("execution_id=601 AND actor_id=301 AND state IN ('ALLOW','DENY')").Count(&count).Error)
			require.GreaterOrEqual(t, count, int64(2))
			return
		case <-ctx.Done():
			t.Fatal("live backend deadline exceeded")
		case <-ticker.C:
			var page governance.InboxPage
			require.Equal(t, 200, api("GET", "/tenants/201/inbox-items?kind=INTERACTION", humanToken, nil, &page))
			for _, item := range page.Items {
				if item.ObjectID.String() != "601" {
					continue
				}
				var detail execution.ExecutionDetail
				require.Equal(t, 200, api("GET", "/executions/601", humanToken, nil, &detail))
				for _, request := range detail.Approvals {
					if request.ID != item.ID || request.State != "PENDING" {
						continue
					}
					var input struct {
						Command string `json:"command"`
					}
					require.NoError(t, json.Unmarshal(request.Input, &input))
					state := "DENY"
					if request.Tool == "Bash" && strings.TrimSpace(input.Command) == allowed {
						state = "ALLOW"
					}
					var result execution.ApprovalView
					require.Equal(t, 200, api("PUT", "/executions/601/approval-requests/"+request.ID.String()+"/decision", humanToken, map[string]string{"state": state, "reason": "Explicit Human API scratch decision"}, &result))
					require.Equal(t, state, result.State)
					require.NotNil(t, result.ActorID)
					require.Equal(t, "301", result.ActorID.String())
					seen[state] = request.ID.String()
					if state == "DENY" && request.Tool == "Bash" && strings.TrimSpace(input.Command) == denied {
						seen["DENY_EXACT"] = request.ID.String()
					}
				}
			}
		}
	}
}

func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	v := reflect.ValueOf(cmd.SysProcAttr).Elem()
	if f := v.FieldByName("HideWindow"); f.IsValid() && f.CanSet() {
		f.SetBool(true)
	}
	if f := v.FieldByName("CreationFlags"); f.IsValid() && f.CanSet() {
		f.SetUint(f.Uint() | 0x08000000)
	}
}

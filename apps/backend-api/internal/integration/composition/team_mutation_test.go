package composition_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/governance"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSavedTestReportAndAtomicFailedRunComposedAuthorityAndRollback(t *testing.T) {
	a := bootstrapComposedApp(t)
	db := a.Runtime().Database().Write()
	at := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 201, "slug": "report-regression", "name": "Report regression", "status": "active", "version": 1, "created_at": at, "updated_at": at}).Error)
	require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": 301, "login_name": "report-human", "display_name": "Human", "status": "active", "created_at": at, "updated_at": at}).Error)
	bootstrap, err := module.Resolve[core.TenantBootstrapper](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Human"}, at)
		return err
	}))
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, SubjectType: authn.SubjectTypePrincipal})
	roles, err := module.Resolve[governance.SeededRoles](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, roles.EnsureTenant(ctx, 201))
	web, err := module.Resolve[iamauthn.WebService](a.Modules().Services())
	require.NoError(t, err)
	token, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: at, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	router := moduleRouter(t, a)
	call := func(method, path, key string, body any, out any) int {
		t.Helper()
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("If-Match", `"1"`)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, r)
		if response.Code < 300 && out != nil {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.NoError(t, json.Unmarshal(envelope.Data, out))
		}
		if response.Code >= 400 {
			t.Log(response.Code, response.Body.String())
		}
		return response.Code
	}
	var project struct {
		Project delivery.ProjectView `json:"project"`
	}
	require.Equal(t, 201, call("POST", "/tenants/201/projects", "report-project", map[string]any{"name": "Testing", "code": "RPT"}, &project))
	p := project.Project.ProjectID
	creator, err := module.Resolve[delivery.TemplateInstantiation](a.Modules().Services())
	require.NoError(t, err)
	var work guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		work, err = creator.CreateWorkOn(ctx, tx, 201, p, delivery.TemplateWorkInput{Title: "Test source", Type: "TASK", Objective: "Observe behavior", AccountablePrincipalID: 301, Criteria: []delivery.TemplateCriterion{{Text: "Expected behavior", VerificationMethod: "Human observation"}}})
		return err
	}))
	base := "/entities/" + p.String()
	var testCase, plan, run, report clientwork.Receipt
	require.Equal(t, 201, call("POST", base+"/test-cases", "case", map[string]any{"title": "Frozen case", "steps": "Frozen steps", "expected": "Expected behavior", "work_item_id": work}, &testCase))
	require.Equal(t, 201, call("POST", base+"/test-plans", "plan", map[string]any{"title": "Frozen plan", "case_ids": []guid.ID{testCase.ID}}, &plan))
	body := map[string]any{"case_id": testCase.ID, "case_version": 1, "result": "FAILED", "evidence": "Actual observed failure", "defect": map[string]any{"title": "Canonical bug", "severity": "NORMAL", "priority": "MEDIUM", "reproducibility": "ALWAYS", "accountable_principal_id": "301"}}
	path := base + "/test-plans/" + plan.ID.String() + "/runs"
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_test_run_outbox BEFORE INSERT ON domain_events WHEN NEW.event_type='clientwork.test_run_recorded' BEGIN SELECT RAISE(ABORT,'outbox failure'); END`).Error)
	require.Equal(t, 500, call("POST", path, "failed-run", body, nil))
	var count int64
	require.NoError(t, db.Table("delivery_work_items").Where("project_id=? AND type='BUG'", p).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Table("clientwork_test_runs").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Exec("DROP TRIGGER reject_test_run_outbox").Error)
	require.Equal(t, 200, call("POST", path, "failed-run", body, &run))
	require.Equal(t, 200, call("POST", path, "failed-run", body, nil))
	require.NoError(t, db.Table("delivery_work_items").Where("project_id=? AND type='BUG'", p).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var result clientwork.TestRunView
	require.Equal(t, 200, call("GET", base+"/test-runs/"+run.ID.String(), "", nil, &result))
	require.NotNil(t, result.DefectID)
	var bug struct {
		Description string
		State       string
	}
	require.NoError(t, db.Table("delivery_work_items").Where("id=?", *result.DefectID).Take(&bug).Error)
	require.Equal(t, "DRAFT", bug.State)
	require.Contains(t, bug.Description, run.ID.String())
	reportBody := map[string]any{"title": "Saved report", "plan_ids": []guid.ID{plan.ID}}
	require.Equal(t, 201, call("POST", base+"/test-reports", "report", reportBody, &report))
	require.Equal(t, 201, call("POST", base+"/test-reports", "report", reportBody, nil))
	var receipt clientwork.TestReportView
	require.Equal(t, 200, call("GET", base+"/test-reports/"+report.ID.String(), "", nil, &receipt))
	require.Equal(t, "PENDING", receipt.State)
	require.Equal(t, 1, receipt.Summary.Failed)
	// Current disabled membership conceals the entity through the owner guard (404).
	require.NoError(t, db.Table("iam_tenant_members").Where("tenant_id=201 AND principal_id=301").Update("status", "disabled").Error)
	require.Equal(t, 404, call("POST", path, "failed-run", body, nil))
	require.Equal(t, 404, call("GET", base+"/test-reports/"+report.ID.String(), "", nil, nil))
}

func TestTeamBoardComposedCanonicalCreationAndCurrentIAM(t *testing.T) {
	a := bootstrapComposedApp(t)
	db := a.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 201, "slug": "team-board-test", "name": "Team board test", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	for _, id := range []int{301, 302} {
		require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": id, "login_name": fmt.Sprintf("team-human-%d", id), "display_name": "Human", "status": "active", "created_at": now, "updated_at": now}).Error)
	}
	bootstrap, e := module.Resolve[core.TenantBootstrapper](a.Modules().Services())
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Human"}, now)
		return e
	}))
	require.NoError(t, db.Table("iam_tenant_members").Create(map[string]any{"tenant_id": 201, "principal_id": 302, "status": "active", "created_at": now, "updated_at": now}).Error)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, SubjectType: authn.SubjectTypePrincipal})
	roles, e := module.Resolve[governance.SeededRoles](a.Modules().Services())
	require.NoError(t, e)
	require.NoError(t, roles.EnsureTenant(ctx, 201))
	web, e := module.Resolve[iamauthn.WebService](a.Modules().Services())
	require.NoError(t, e)
	token, _, e := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, e)
	router := moduleRouter(t, a)
	call := func(method, path, key string, body any, out any) int {
		t.Helper()
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		request := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code < 300 && out != nil {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.NoError(t, json.Unmarshal(envelope.Data, out))
		}
		if response.Code >= 400 {
			t.Logf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		return response.Code
	}
	var project struct {
		Project delivery.ProjectView `json:"project"`
	}
	require.Equal(t, 201, call(http.MethodPost, "/tenants/201/projects", "team-project", map[string]any{"name": "Human tasks", "code": "HUM-T"}, &project))
	p := project.Project.ProjectID
	require.Positive(t, p)
	// Establish the test deployment's human-only mode before any team commands.
	require.NoError(t, db.Table("delivery_projects").Where("project_id = ?", p).Update("team_mode", "HUMAN_ONLY").Error)
	definition := templates.TemplateDefinition{Domain: "TEAM_TASKS", Semver: "1.0.0", ConstitutionSeed: "# Human review", Metadata: map[string]any{}, Workflow: templates.TemplateWorkflowDef{SchemaVersion: 1, WorkflowID: "human-team", Version: 1, Name: "Human team", Parameters: map[string]any{}, EntryNodeID: "work", OutputBindings: map[string]string{}, Nodes: []templates.TemplateNode{{ID: "work", Kind: "WORK_ITEM", Name: "Human work", Objective: "Review actual evidence", Criteria: []templates.TemplateCriterion{{Text: "Evidence reviewed", VerificationMethod: "Human review"}}}, {ID: "team", Kind: "HUMAN_GATE", Name: "Peer review", Gate: "TEAM"}, {ID: "client", Kind: "HUMAN_GATE", Name: "Accept", Gate: "CLIENT"}}, Edges: []templates.TemplateEdge{{ID: "a", Source: "work", Target: "team"}, {ID: "b", Source: "team", Target: "client"}}}}
	var version templates.TemplateVersionView
	require.Equal(t, 201, call("POST", "/tenants/201/templates", "team-template", map[string]any{"key": "human-team", "definition": definition}, &version))
	require.Equal(t, 201, call("POST", "/tenants/201/templates/"+version.ID.String()+"/publications", "team-publish", map[string]any{"hash": version.Hash}, &version))
	require.Equal(t, 201, call("POST", "/tenants/201/templates/"+version.ID.String()+"/instances", "team-instance", map[string]any{"project_id": p, "accountable_principal_id": guid.ID(301)}, nil))
	owner := guid.ID(301)
	due := now.Add(time.Hour)
	content := templates.StudioContent{Title: "Adopt real human action", Text: "Trace original meeting action", Source: "https://example.invalid/minutes", OwnerID: &owner, DueAt: &due, Team: &templates.TeamTaskData{SignerID: 302, Checklist: []templates.TeamChecklistItem{{Text: "Deliver report"}}, Evidence: []templates.TeamEvidence{}, Risks: []templates.TeamRisk{}}}
	input := templates.TeamEdit{Action: "SAVE", Reason: "Human action confirmed", Content: content}
	base := "/tenants/201/projects/" + p.String() + "/team-task-board"
	var created templates.TeamTaskView
	require.Equal(t, 201, call("POST", base, "team-create", input, &created))
	require.Positive(t, created.Work.ID)
	require.Equal(t, "DRAFT", created.Work.State)
	var count int64
	require.NoError(t, db.Table("delivery_work_items").Where("id = ? AND project_id = ?", created.Work.ID, p).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var replay templates.TeamTaskView
	require.Equal(t, 201, call("POST", base, "team-create", input, &replay))
	require.Equal(t, created.Record.ID, replay.Record.ID)
	require.Equal(t, created.Work.ID, replay.Work.ID)
	require.NoError(t, db.Table("delivery_work_items").Where("id = ?", created.Work.ID).Updates(map[string]any{"state": "IN_PROGRESS", "version": 2}).Error)
	var board templates.TeamBoardPage
	require.Equal(t, 200, call("GET", base+"?limit=50&sort=id", "", nil, &board))
	require.Len(t, board.Items, 1)
	require.Equal(t, "IN_PROGRESS", board.Items[0].Work.State)
	require.Equal(t, 2, board.Items[0].Work.Version)
	require.Equal(t, 403, call("POST", base+"/"+created.Record.ID.String()+"/decisions", "self-sign", templates.TeamEdit{Version: 1, Action: "SIGN_OFF", Reason: "Cannot self-sign", Content: created.Record.Content}, nil))
	for _, table := range []string{"iam_audit_events", "domain_events"} {
		require.NoError(t, db.Table(table).Where("tenant_id = ? AND event_type = ?", 201, "template.studio_changed").Count(&count).Error)
		require.EqualValues(t, 1, count, table+": idempotent replay must not duplicate audit/outbox")
	}
	require.NoError(t, db.Table("iam_tenant_members").Where("tenant_id = 201 AND principal_id = 302").Update("status", "disabled").Error)
	input.Content.Team.WorkItemID = created.Work.ID
	input.Version = 1
	require.Equal(t, 403, call("POST", base+"/"+created.Record.ID.String()+"/decisions", "revoked-reviewer", input, nil))
	require.NoError(t, db.Table("iam_principals").Where("id = 301").Update("status", "disabled").Error)
	status := call("POST", base, "team-create", templates.TeamEdit{Action: "SAVE", Reason: "Human action confirmed", Content: content}, nil)
	require.Contains(t, []int{401, 403}, status, "replay must require current active IAM")
}

// TestWorkflowLoopComposedCostsAndEscalation exercises the production bindings,
// not replacement cost or escalation callbacks. Execution rows below are explicit
// persisted input fixtures; this test does not claim to run a remote Runner.
func TestWorkflowLoopComposedCostsAndEscalation(t *testing.T) {
	a := bootstrapComposedApp(t)
	db := a.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 201, "slug": "workflow-cost-test", "name": "Workflow costs", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": 301, "login_name": "workflow-cost-owner", "display_name": "Owner", "status": "active", "created_at": now, "updated_at": now}).Error)
	bootstrap, err := module.Resolve[core.TenantBootstrapper](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Owner"}, now)
		return err
	}))
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, SubjectType: authn.SubjectTypePrincipal})
	roles, err := module.Resolve[governance.SeededRoles](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, roles.EnsureTenant(ctx, 201))
	web, err := module.Resolve[iamauthn.WebService](a.Modules().Services())
	require.NoError(t, err)
	token, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	router := moduleRouter(t, a)
	call := func(method, path, key string, body, out any, status int) {
		t.Helper()
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		request := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, status, response.Code, response.Body.String())
		if out != nil {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.NoError(t, json.Unmarshal(envelope.Data, out))
		}
	}
	var project struct {
		Project delivery.ProjectView `json:"project"`
	}
	call("POST", "/tenants/201/projects", "workflow-project", map[string]any{"name": "Workflow costs", "code": "WF-COST"}, &project, 201)
	p := project.Project.ProjectID
	definition := templates.TemplateDefinition{Domain: "SOFTWARE", Semver: "1.0.0", ConstitutionSeed: "# Explicit Human acceptance", Metadata: map[string]any{}, Workflow: templates.TemplateWorkflowDef{SchemaVersion: 1, WorkflowID: "cost-child", Version: 1, Name: "Cost child", Parameters: map[string]any{}, EntryNodeID: "work", OutputBindings: map[string]string{}, Nodes: []templates.TemplateNode{{ID: "work", Kind: "WORK_ITEM", Name: "Review output", Objective: "Use real work facts", Criteria: []templates.TemplateCriterion{{Text: "Human reviewed", VerificationMethod: "Human review"}}}, {ID: "team", Kind: "HUMAN_GATE", Name: "Team", Gate: "TEAM"}, {ID: "client", Kind: "HUMAN_GATE", Name: "Client", Gate: "CLIENT"}}, Edges: []templates.TemplateEdge{{ID: "a", Source: "work", Target: "team"}, {ID: "b", Source: "team", Target: "client"}}}}
	publish := func(key string, d templates.TemplateDefinition) templates.TemplateVersionView {
		t.Helper()
		var v templates.TemplateVersionView
		call("POST", "/tenants/201/templates", key+"-draft", map[string]any{"key": key, "definition": d}, &v, 201)
		call("POST", "/tenants/201/templates/"+v.ID.String()+"/publications", key+"-publish", map[string]any{"hash": v.Hash}, &v, 201)
		return v
	}
	definition.Workflow.WorkflowID = "cost-leaf"
	publish("cost-leaf", definition)
	definition.Workflow.WorkflowID = "cost-child"
	definition.Workflow.Nodes[0] = templates.TemplateNode{ID: "work", Kind: "FAN_OUT", Name: "Two bounded alternatives", Items: []string{"first", "second"}, Subflow: &templates.TemplateWorkflowRef{WorkflowID: "cost-leaf", Version: 1}}
	publish("cost-child", definition)
	definition.Workflow.WorkflowID = "cost-parent"
	definition.Workflow.Nodes[0] = templates.TemplateNode{ID: "work", Kind: "LOOP", Name: "Bounded review", MaxIterations: 2, Budget: 10, EscalateTo: "accountable", Condition: json.RawMessage(`false`), Subflow: &templates.TemplateWorkflowRef{WorkflowID: "cost-child", Version: 1}}
	v := publish("cost-parent", definition)
	var instance templates.TemplateInstanceView
	instanceBody := map[string]any{"project_id": p, "accountable_principal_id": guid.ID(301)}
	instancePath := "/tenants/201/templates/" + v.ID.String() + "/instances"
	call("POST", instancePath, "workflow-instance", instanceBody, &instance, 201)
	require.Equal(t, v.Hash, instance.Hash)
	require.Equal(t, 1, instance.Runs[0].Iteration)
	require.Len(t, instance.Runs[0].ChildHash, 64)
	var works []guid.ID
	for _, r := range instance.Runs {
		if r.WorkItemID != nil {
			works = append(works, *r.WorkItemID)
		}
	}
	require.Len(t, works, 2)
	require.NotEqual(t, works[0], works[1])
	call("POST", instancePath, "workflow-instance", instanceBody, &instance, 201)
	var count int64
	require.NoError(t, db.Table("delivery_work_items").Where("project_id = ?", p).Count(&count).Error)
	require.EqualValues(t, 2, count, "replay must not materialize duplicate children")
	path := "/tenants/201/projects/" + p.String() + "/workflow/advances"
	advance := func(key string) {
		t.Helper()
		call("POST", path, key, map[string]any{}, &instance, 200)
		require.Equal(t, v.Hash, instance.Hash)
	}
	// A RUNNING label alone is not proof of a start or a price.
	for i, work := range works {
		require.NoError(t, db.Table("execution_executions").Create(map[string]any{"id": 801 + i*2, "tenant_id": 201, "project_id": p, "work_item_id": work, "assignment_id": 901 + i, "machine_id": 1, "initiator_id": 301, "state": "RUNNING", "state_reason": "", "result_state": "NONE", "plan_json": "{}", "plan_hash": strings.Repeat("a", 64), "dispatch_epoch": 1, "last_seq": 1, "version": 1, "created_at": now, "updated_at": now}).Error)
	}
	advance("workflow-no-start")
	require.Equal(t, "MISSING_LOOP_BUDGET_FACTS", instance.Runs[0].Reason)
	provider, err := module.Resolve[budget.Provider](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return provider.ReserveOn(ctx, tx, 201, p, "EXECUTION", 801, 10, true) }))
	for i := range works {
		require.NoError(t, db.Table("execution_receipts").Create(map[string]any{"id": 802 + i*2, "tenant_id": 201, "execution_id": 801 + i*2, "dispatch_epoch": 1, "seq": 1, "kind": "ACCEPTED", "content_hash": strings.Repeat("b", 64), "state": "RUNNING", "created_at": now}).Error)
	}
	advance("workflow-reserved")
	require.Equal(t, "MISSING_LOOP_BUDGET_FACTS", instance.Runs[0].Reason, "a reservation is not actual cost")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return provider.SettleOn(ctx, tx, 201, p, "EXECUTION", 801, 9, true) }))
	advance("workflow-one-settled")
	require.Equal(t, "MISSING_LOOP_BUDGET_FACTS", instance.Runs[0].Reason, "every materialized branch needs a proven settlement")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return provider.ReserveOn(ctx, tx, 201, p, "EXECUTION", 803, 2, true) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return provider.SettleOn(ctx, tx, 201, p, "EXECUTION", 803, 2, true) }))
	// A failure after the source event and governance audit must roll them all
	// back, including the command receipt, so the same key can safely retry.
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_workflow_incident BEFORE INSERT ON domain_events WHEN NEW.event_type = 'governance.incident_changed' BEGIN SELECT RAISE(ABORT, 'injected outbox failure'); END`).Error)
	call("POST", path, "workflow-over-budget", map[string]any{}, nil, 500)
	call("GET", strings.TrimSuffix(path, "/advances"), "", nil, &instance, 200)
	require.Equal(t, "WAITING", instance.Runs[0].State)
	for _, table := range []string{"iam_audit_events", "domain_events"} {
		require.NoError(t, db.Table(table).Where("tenant_id = ? AND event_type IN ?", 201, []string{"template.loop_escalated", "governance.incident_changed"}).Count(&count).Error)
		require.Zero(t, count, "failed escalation must roll back source, audit and outbox")
	}
	require.NoError(t, db.Exec("DROP TRIGGER reject_workflow_incident").Error)
	advance("workflow-over-budget")
	require.Equal(t, "ESCALATED", instance.Runs[0].State)
	require.Equal(t, "LOOP_BUDGET_EXCEEDED", instance.Runs[0].Reason)
	require.Equal(t, 1, instance.Runs[0].Iteration)
	for _, r := range instance.Runs {
		require.NotEqual(t, "ACCEPTED", r.State, "budget escalation cannot accept work")
	}
	advance("workflow-over-budget")
	advance("workflow-still-escalated")
	require.NoError(t, db.Table("delivery_work_items").Where("project_id = ?", p).Count(&count).Error)
	require.EqualValues(t, 2, count)
	for _, table := range []string{"iam_audit_events", "domain_events"} {
		require.NoError(t, db.Table(table).Where("tenant_id = ? AND event_type = ?", 201, "governance.incident_changed").Count(&count).Error)
		require.EqualValues(t, 1, count, "replay cannot duplicate the incident audit/outbox")
	}
	require.NoError(t, db.Table("domain_events").Where("tenant_id = ? AND event_type = ?", 201, "template.loop_escalated").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Table("iam_principals").Where("id = ?", 301).Update("status", "disabled").Error)
	call("POST", path, "workflow-over-budget", map[string]any{}, nil, 401)
}

func TestWorkflowArchiveComposedMigrationRollbackAndCurrentAuthority(t *testing.T) {
	a := bootstrapComposedApp(t)
	db := a.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 201, "slug": "team-board-test", "name": "Team board test", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	for _, id := range []int{301, 302} {
		require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": id, "login_name": fmt.Sprintf("team-human-%d", id), "display_name": "Human", "status": "active", "created_at": now, "updated_at": now}).Error)
	}
	bootstrap, e := module.Resolve[core.TenantBootstrapper](a.Modules().Services())
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Human"}, now)
		return e
	}))
	require.NoError(t, db.Table("iam_tenant_members").Create(map[string]any{"tenant_id": 201, "principal_id": 302, "status": "active", "created_at": now, "updated_at": now}).Error)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, SubjectType: authn.SubjectTypePrincipal})
	roles, e := module.Resolve[governance.SeededRoles](a.Modules().Services())
	require.NoError(t, e)
	require.NoError(t, roles.EnsureTenant(ctx, 201))
	web, e := module.Resolve[iamauthn.WebService](a.Modules().Services())
	require.NoError(t, e)
	token, _, e := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, e)
	router := moduleRouter(t, a)
	call := func(method, path, key string, body any, out any) int {
		t.Helper()
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		request := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code < 300 && out != nil {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.NoError(t, json.Unmarshal(envelope.Data, out))
		}
		if response.Code >= 400 {
			t.Logf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		return response.Code
	}
	var project struct {
		Project delivery.ProjectView `json:"project"`
	}
	require.Equal(t, 201, call(http.MethodPost, "/tenants/201/projects", "team-project", map[string]any{"name": "Human tasks", "code": "HUM-T"}, &project))
	p := project.Project.ProjectID
	require.Positive(t, p)
	// Establish the test deployment's human-only mode before any team commands.
	require.NoError(t, db.Table("delivery_projects").Where("project_id = ?", p).Update("team_mode", "HUMAN_ONLY").Error)

	creator, err := module.Resolve[delivery.TemplateInstantiation](a.Modules().Services())
	require.NoError(t, err)
	var works []guid.ID
	for i := 0; i < 3; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			id, err := creator.CreateWorkOn(ctx, tx, 201, p, delivery.TemplateWorkInput{Title: fmt.Sprintf("Migrate %d", i), Type: "TASK", Objective: "Preserve business state", AccountablePrincipalID: 301, Criteria: []delivery.TemplateCriterion{{Text: "Keep evidence", VerificationMethod: "Human review"}}})
			works = append(works, id)
			return err
		}))
	}
	path := "/tenants/201/projects/" + p.String() + "/workflow-configs/TASK"
	definition := map[string]any{"states": []map[string]any{{"key": "draft", "display_name": "Draft", "group": "TODO", "maps_to": "DRAFT", "archived": true}, {"key": "queued", "display_name": "Queued", "group": "TODO", "maps_to": "DRAFT"}}, "transitions": []any{}}
	var preview struct {
		Projects int    `json:"projects_affected"`
		Items    int    `json:"items_in_archived_states"`
		Impact   string `json:"runtime_impact"`
	}
	require.Equal(t, 200, call("POST", path+"/previews", "", map[string]any{"version": 0, "remove": false, "definition": definition}, &preview))
	require.Equal(t, "EVALUATED", preview.Impact)
	require.Equal(t, 1, preview.Projects)
	require.Equal(t, 3, preview.Items)
	require.Equal(t, 409, call("PUT", path, "archive-no-target", definition, nil))
	definition["states"].([]map[string]any)[0]["migrate_to"] = "queued"
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_workflow_config BEFORE INSERT ON domain_events WHEN NEW.event_type = 'workflow.config_changed' BEGIN SELECT RAISE(ABORT, 'injected configuration outbox failure'); END`).Error)
	require.Equal(t, 500, call("PUT", path, "archive-valid-target", definition, nil))
	var count int64
	require.NoError(t, db.Table("delivery_work_item_transitions").Where("work_item_id IN ? AND reason_code=?", works, "DISPLAY_STATE_MIGRATED").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Exec("DROP TRIGGER reject_workflow_config").Error)
	require.Equal(t, 200, call("PUT", path, "archive-valid-target", definition, nil))
	require.Equal(t, 200, call("PUT", path, "archive-valid-target", definition, nil))
	require.NoError(t, db.Table("delivery_work_item_transitions").Where("work_item_id IN ? AND reason_code=?", works, "DISPLAY_STATE_MIGRATED").Count(&count).Error)
	require.EqualValues(t, 3, count)
	require.NoError(t, db.Table("delivery_work_items").Where("id IN ? AND state='DRAFT' AND current_execution_id IS NULL AND current_claim_id IS NULL", works).Count(&count).Error)
	require.EqualValues(t, 3, count)
	states, err := module.Resolve[delivery.WorkflowDisplayStates](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		items, err := states.ItemsOn(ctx, tx, 201, p, "TASK")
		require.NoError(t, err)
		require.Len(t, items, 3)
		for _, item := range items {
			require.Equal(t, "queued", item.Key)
			require.Equal(t, "DRAFT", item.State)
		}
		return nil
	}))
	// A later canonical creation uses the active alias, rather than targeting the
	// archived default or inventing a business transition.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := creator.CreateWorkOn(ctx, tx, 201, p, delivery.TemplateWorkInput{Title: "New after archive", Type: "TASK", Objective: "Keep default mapping", AccountablePrincipalID: 301, Criteria: []delivery.TemplateCriterion{{Text: "Still draft", VerificationMethod: "Human review"}}})
		if err != nil {
			return err
		}
		items, err := states.ItemsOn(ctx, tx, 201, p, "TASK")
		require.NoError(t, err)
		require.Len(t, items, 4)
		for _, item := range items {
			require.Equal(t, "queued", item.Key)
		}
		return nil
	}))
	require.NoError(t, db.Table("iam_principals").Where("id=301").Update("status", "disabled").Error)
	require.Equal(t, 401, call("PUT", path, "archive-valid-target", definition, nil))
}

func TestWorkflowTransitionComposedFieldsConditionsAndReplay(t *testing.T) {
	a := bootstrapComposedApp(t)
	db := a.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 201, "slug": "team-board-test", "name": "Team board test", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	for _, id := range []int{301, 302} {
		require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": id, "login_name": fmt.Sprintf("team-human-%d", id), "display_name": "Human", "status": "active", "created_at": now, "updated_at": now}).Error)
	}
	bootstrap, e := module.Resolve[core.TenantBootstrapper](a.Modules().Services())
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Human"}, now)
		return e
	}))
	require.NoError(t, db.Table("iam_tenant_members").Create(map[string]any{"tenant_id": 201, "principal_id": 302, "status": "active", "created_at": now, "updated_at": now}).Error)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, SubjectType: authn.SubjectTypePrincipal})
	roles, e := module.Resolve[governance.SeededRoles](a.Modules().Services())
	require.NoError(t, e)
	require.NoError(t, roles.EnsureTenant(ctx, 201))
	web, e := module.Resolve[iamauthn.WebService](a.Modules().Services())
	require.NoError(t, e)
	token, _, e := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, e)
	router := moduleRouter(t, a)
	call := func(method, path, key string, body any, out any) int {
		t.Helper()
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		request := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code < 300 && out != nil {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.NoError(t, json.Unmarshal(envelope.Data, out))
		}
		if response.Code >= 400 {
			t.Logf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		return response.Code
	}
	var project struct {
		Project delivery.ProjectView `json:"project"`
	}
	require.Equal(t, 201, call(http.MethodPost, "/tenants/201/projects", "team-project", map[string]any{"name": "Human tasks", "code": "HUM-T"}, &project))
	p := project.Project.ProjectID
	require.Positive(t, p)
	// Establish the test deployment's human-only mode before any team commands.
	require.NoError(t, db.Table("delivery_projects").Where("project_id = ?", p).Update("team_mode", "HUMAN_ONLY").Error)

	creator, err := module.Resolve[delivery.TemplateInstantiation](a.Modules().Services())
	require.NoError(t, err)
	var work guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		work, err = creator.CreateWorkOn(ctx, tx, 201, p, delivery.TemplateWorkInput{Title: "Conditional workflow", Type: "TASK", Objective: "Complete original work fields atomically", AccountablePrincipalID: 301, Criteria: []delivery.TemplateCriterion{{Text: "Do not invent evidence", VerificationMethod: "Human review"}}})
		return err
	}))
	configPath := "/tenants/201/projects/" + p.String() + "/workflow-configs/TASK"
	definition := map[string]any{"states": []map[string]any{{"key": "draft", "display_name": "Draft", "group": "TODO", "maps_to": "DRAFT"}, {"key": "triaged", "display_name": "Triaged", "group": "TODO", "maps_to": "DRAFT"}}, "transitions": []map[string]any{{"from": "draft", "to": "triaged", "enabled": true, "manual_allowed": true, "conditions": []map[string]any{{"kind": "REQUIRED_FIELDS", "fields": []string{"estimate_minutes"}}}}}}
	require.Equal(t, 200, call("PUT", configPath, "configure-transitions", definition, nil))
	path := "/work-items/" + work.String() + "/transitions"
	var before struct {
		Version     int    `json:"version"`
		ActivitySeq int64  `json:"activity_seq"`
		StateKey    string `json:"state_key"`
	}
	workPath := "/entities/" + p.String() + "/work-items/" + work.String()
	require.Equal(t, 200, call("GET", workPath, "", nil, &before))
	require.Equal(t, "draft", before.StateKey)
	require.Equal(t, 409, call("POST", path, "missing-required-fields", map[string]any{"to_state_key": "triaged"}, nil))
	var after = before
	require.Equal(t, 200, call("GET", workPath, "", nil, &after))
	require.Equal(t, before, after)
	body := map[string]any{"to_state_key": "triaged", "fields": map[string]any{"estimate_minutes": 30}, "comment": "Current source reviewed"}
	var receipt struct {
		ID       guid.ID `json:"id"`
		State    string  `json:"state"`
		StateKey string  `json:"state_key"`
		Version  int     `json:"version"`
	}
	require.Equal(t, 200, call("POST", path, "complete-fields-transition", body, &receipt))
	require.Equal(t, "DRAFT", receipt.State)
	require.Equal(t, "triaged", receipt.StateKey)
	var replay = receipt
	require.Equal(t, 200, call("POST", path, "complete-fields-transition", body, &replay))
	require.Equal(t, receipt, replay)
	require.Equal(t, 200, call("GET", workPath, "", nil, &after))
	require.Equal(t, "triaged", after.StateKey)
	require.Greater(t, after.ActivitySeq, before.ActivitySeq)
	var stored struct {
		EstimateMinutes *int
		State           string
	}
	require.NoError(t, db.Table("delivery_work_items").Select("estimate_minutes,state").Where("id=?", work).Take(&stored).Error)
	require.NotNil(t, stored.EstimateMinutes)
	require.Equal(t, 30, *stored.EstimateMinutes)
	require.Equal(t, "DRAFT", stored.State)
	require.NoError(t, db.Table("iam_principals").Where("id=301").Update("status", "disabled").Error)
	require.Equal(t, 401, call("POST", path, "complete-fields-transition", body, nil))
}

package composition_test

import (
	platformapp "7link.com/platform/internal/app"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This uses the real composition, current IAM policies, governed file storage,
// Knowledge review/mounts and Fleet command queue, rather than provider stubs.
func TestAtomicAgentCreationComposedProvidersAndRollback(t *testing.T) {
	a := bootstrapComposedAppConfigured(t, func(c *platformapp.AppConfig) {
		c.Fleet.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	})
	db := a.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 201, "slug": "atomic-test", "name": "Atomic test", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": 301, "login_name": "atomic-human", "display_name": "Human", "status": "active", "created_at": now, "updated_at": now}).Error)
	bootstrap, err := module.Resolve[core.TenantBootstrapper](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Human"}, now)
		return e
	}))
	human := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, SubjectType: authn.SubjectTypePrincipal})
	roles, err := module.Resolve[governance.SeededRoles](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, roles.EnsureTenant(human, 201))
	web, err := module.Resolve[iamauthn.WebService](a.Modules().Services())
	require.NoError(t, err)
	token, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	iam, err := module.Resolve[fleet.EnrollmentIAM](a.Modules().Services())
	require.NoError(t, err)
	account, err := iam.CreateAccount(human, 201, "atomic-runner", "Runner", "", nil)
	require.NoError(t, err)
	require.NoError(t, db.Table("fleet_machines").Create(map[string]any{"id": 401, "tenant_id": 201, "principal_id": account.ID, "name": "Atomic machine", "name_key": "atomic-machine", "state": "ONLINE", "drain_requested": false, "conn_epoch": 0, "os": "LINUX", "slot_count": 1, "version": 1, "created_at": now, "updated_at": now}).Error)
	owner, ok := a.Modules().ByName(fleet.Name)
	require.True(t, ok)
	f := owner.(*fleet.Module)
	runner := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: account.ID, SubjectType: authn.SubjectTypeServiceAccount})
	var machineSession fleet.SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		s, e := f.OpenSessionOn(runner, tx, 201, 401, "atomic-runner-session")
		if e != nil {
			return e
		}
		machineSession = s
		return f.RecordInventoryOn(runner, tx, 201, 401, s.ID, s.ConnEpoch, fleet.Inventory{Runtimes: []fleet.RuntimeObservation{{Kind: "CODEX", Version: "test", ExecutablePath: "/test/codex", Models: []string{"gpt-test"}}}})
	}))
	router := moduleRouter(t, a)
	call := func(path, key string, body, out any) int {
		t.Helper()
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code < 300 && out != nil {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.NoError(t, json.Unmarshal(envelope.Data, out))
		}
		if response.Code >= 400 {
			t.Logf("%s: %d %s", path, response.Code, response.Body.String())
		}
		require.NotContains(t, response.Body.String(), "client_secret")
		return response.Code
	}
	var skill knowledge.SkillResult
	require.Equal(t, 201, call("/tenants/201/skills", "atomic-skill", map[string]string{"content": "---\nname: atomic-review\ndescription: Review actual evidence\n---\nReview the current source."}, &skill))
	require.Equal(t, 201, call("/tenants/201/skill-versions/"+skill.Version.ID.String()+"/reviews", "atomic-skill-review", map[string]string{"decision": "APPROVED", "reason": "Reviewed actual instructions"}, nil))
	in := agents.AgentCreation{MachineID: 401, Name: "Atomic executor", Handle: "atomic-executor", Runtime: "CODEX", Model: "gpt-test", Roles: []string{"EXECUTOR"}, SystemPrompt: "Follow the approved instructions", AuthorizationMode: "READ_ONLY", AvatarStyle: "PIXEL", AvatarSeed: 42, SkillIDs: []guid.ID{skill.Skill.ID}, MemoryPolicy: `{"profile":"STANDARD"}`, DefaultChannels: []string{}}
	var created fleet.RegisteredAgent
	require.Equal(t, 201, call("/tenants/201/agent-registrations", "atomic-registration", in, &created))
	require.Positive(t, created.PrincipalID)
	var replay fleet.RegisteredAgent
	require.Equal(t, 201, call("/tenants/201/agent-registrations", "atomic-registration", in, &replay))
	require.Equal(t, created, replay)
	for _, table := range []string{"iam_service_accounts", "iam_service_account_credentials", "fleet_agent_machine_commands"} {
		var count int64
		column := "principal_id"
		if table == "fleet_agent_machine_commands" {
			column = "agent_principal_id"
		}
		require.NoError(t, db.Table(table).Where(column+" = ?", created.PrincipalID).Count(&count).Error)
		require.EqualValues(t, 1, count, table)
	}
	catalog, err := module.Resolve[knowledge.SkillCatalog](a.Modules().Services())
	require.NoError(t, err)
	var snapshot []knowledge.ResolvedSkill
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		snapshot, e = catalog.ForAgentOn(human, tx, 201, created.PrincipalID)
		return e
	}))
	require.Len(t, snapshot, 1)
	require.Equal(t, skill.Version.ID, snapshot[0].VersionID)
	t.Run("Agent transition uses real workflow selection and source context", func(t *testing.T) {
		var project struct {
			Project delivery.ProjectView `json:"project"`
		}
		require.Equal(t, 201, call("/tenants/201/projects", "transition-project", map[string]any{"name": "Workflow context", "code": "WF-CTX"}, &project))
		projectID := project.Project.ProjectID
		var work struct {
			ID guid.ID `json:"id"`
		}
		require.Equal(t, 201, call("/entities/"+projectID.String()+"/work-items", "transition-work", map[string]any{
			"title": "Triage source context", "description": "", "type": "TASK", "objective": "Observe new source activity before triage", "accountable_principal_id": "301",
			"criteria": []map[string]string{{"text": "Source context was read", "verification_method": "Inspect immutable transition"}},
		}, &work))
		require.Positive(t, work.ID)
		bindings, err := module.Resolve[core.EntityRoleBindingStore](a.Modules().Services())
		require.NoError(t, err)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			role, err := roles.RoleIDOn(human, tx, 201, "delivery_lead")
			if err != nil {
				return err
			}
			_, _, err = bindings.PutOn(human, tx, 201, projectID, role, created.PrincipalID, core.BindingAllow, time.Time{})
			return err
		}))
		var tokenVersion int64
		require.NoError(t, db.Table("iam_service_accounts").Select("token_version").Where("principal_id=?", created.PrincipalID).Scan(&tokenVersion).Error)
		agentToken, _, err := web.IssueTenantServiceAccountToken(t.Context(), created.PrincipalID.String(), "201", "", "openid", "workflow-context", tokenVersion)
		require.NoError(t, err)
		send := func(method, path, key, bearer string, body any) *httptest.ResponseRecorder {
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			req := httptest.NewRequest(method, path, bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+bearer)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", key)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			return response
		}
		workflow := map[string]any{
			"states":      []map[string]string{{"key": "draft", "display_name": "Draft", "group": "TODO", "maps_to": "DRAFT"}, {"key": "triaged", "display_name": "Triaged", "group": "TODO", "maps_to": "DRAFT"}},
			"transitions": []map[string]any{{"from": "draft", "to": "triaged", "enabled": true, "manual_allowed": true, "conditions": []map[string]any{{"kind": "ACTOR_SCOPE", "actor_kinds": []string{"AGENT"}}}}},
		}
		response := send("PUT", "/tenants/201/projects/"+projectID.String()+"/workflow-configs/TASK", "transition-config", token, workflow)
		require.Equal(t, 200, response.Code, response.Body.String())
		path := "/work-items/" + work.ID.String() + "/transitions"
		body := map[string]any{"to_state_key": "triaged", "comment": "Read the current source"}
		state := func() string {
			var row map[string]any
			require.NoError(t, db.Table("delivery_work_items").Where("id=?", work.ID).Take(&row).Error)
			counts := map[string]int64{}
			for _, table := range []string{"delivery_work_item_transitions", "delivery_work_item_activities", "domain_events"} {
				var n int64
				scope := db.Table(table)
				if table == "domain_events" {
					// Background consumers may emit unrelated events; freeze this aggregate.
					scope = scope.Where("subject_id = ?", work.ID)
				} else {
					scope = scope.Where("work_item_id = ?", work.ID)
				}
				require.NoError(t, scope.Count(&n).Error)
				counts[table] = n
			}
			raw, err := json.Marshal([]any{row, counts})
			require.NoError(t, err)
			return string(raw)
		}
		before := state()
		response = send("POST", path, "transition-apply", agentToken, body)
		require.Equal(t, 422, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"code":126062,`)
		require.Equal(t, before, state())
		recorder, err := module.Resolve[delivery.WorkItemActivityRecorder](a.Modules().Services())
		require.NoError(t, err)
		var seq int64
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			seq, err = recorder.RecordOn(human, tx, 201, work.ID, delivery.WorkActivityInput{Kind: "COMMENT_ADDED", ActorKind: "HUMAN", ActorID: 301, RefKind: "work_item", RefID: work.ID, Title: "Read the changed triage context"})
			return err
		}))
		before = state()
		body["seen_seq"] = seq - 1
		response = send("POST", path, "transition-apply", agentToken, body)
		require.Equal(t, 409, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"code":126061,`)
		require.Contains(t, response.Body.String(), "inbox_events.list")
		require.Equal(t, before, state())
		body["seen_seq"] = seq
		response = send("POST", path, "transition-apply", agentToken, body)
		require.Equal(t, 200, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"state_key":"triaged"`)
		var receipt struct{ From, To, Comment string }
		require.NoError(t, db.Table("delivery_work_item_transitions").Select("from_state_key AS `from`, to_state_key AS `to`, comment").Where("work_item_id=? AND to_state_key=?", work.ID, "triaged").Take(&receipt).Error)
		require.Equal(t, "draft", receipt.From)
		require.Equal(t, "triaged", receipt.To)
		require.Equal(t, body["comment"], receipt.Comment)
		var activity struct {
			Kind, ActorKind string
			ActorID         guid.ID
			Seq             int64
		}
		require.NoError(t, db.Table("delivery_work_item_activities").Where("work_item_id=?", work.ID).Order("seq DESC").Take(&activity).Error)
		require.Equal(t, "TRANSITIONED", activity.Kind)
		require.Equal(t, "AGENT", activity.ActorKind)
		require.Equal(t, created.PrincipalID, activity.ActorID)
		require.Equal(t, seq+1, activity.Seq)
		before = state()
		replay := send("POST", path, "transition-apply", agentToken, body)
		require.Equal(t, 200, replay.Code, replay.Body.String())
		require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
		var firstData, replayData struct {
			Data json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &firstData))
		require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &replayData))
		require.JSONEq(t, string(firstData.Data), string(replayData.Data))
		require.Equal(t, before, state())
		// Current identity remains authoritative even for a completed key.
		require.NoError(t, db.Table("iam_service_accounts").Where("principal_id=?", created.PrincipalID).Update("status", "disabled").Error)
		replay = send("POST", path, "transition-apply", agentToken, body)
		require.Contains(t, []int{401, 403}, replay.Code, replay.Body.String())
		require.Equal(t, before, state())
		require.NoError(t, db.Table("iam_service_accounts").Where("principal_id=?", created.PrincipalID).Update("status", "active").Error)
	})

	// A late mount failure must also undo IAM identity, credential, role rows,
	// avatar binding, Fleet delivery and their audit/outbox records.
	baselines := map[string]int64{}
	for _, table := range []string{"iam_principals", "iam_tenant_members", "iam_service_accounts", "iam_service_account_credentials", "fleet_agents", "fleet_agent_roles", "fleet_agent_machine_commands", "knowledge_agent_skills", "file_resource_bindings"} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		baselines[table] = count
	}
	in.Name = "Rejected atomic"
	in.Handle = "rejected-atomic"
	in.SkillIDs = []guid.ID{999999}
	require.Equal(t, 422, call("/tenants/201/agent-registrations", "atomic-failing", in, nil))
	for table, before := range baselines {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Equal(t, before, count, table)
	}
	t.Run("durable retirement HTTP uses real Fleet and current IAM", func(t *testing.T) {
		profiles, e := module.Resolve[fleet.AgentProfiles](a.Modules().Services())
		require.NoError(t, e)
		var profile fleet.AgentProfile
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			profile, err = profiles.ReadOn(human, tx, 201, created.PrincipalID)
			return err
		}))
		send := func(method, path, key, version string, body any) *httptest.ResponseRecorder {
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			req := httptest.NewRequest(method, path, bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", key)
			req.Header.Set("If-Match", version)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			return response
		}
		startPath := "/tenants/201/agents/" + created.PrincipalID.String() + "/retirements"
		response := send("POST", startPath, "durable-retirement-start", fmt.Sprint(profile.Version), map[string]string{"mode": "NORMAL", "reason": "Transfer responsibility through durable protocol"})
		require.Equal(t, 201, response.Code, response.Body.String())
		var record struct {
			Data struct {
				Precheck struct {
					WorkItems  []json.RawMessage `json:"work_items"`
					Executions []json.RawMessage `json:"executions"`
					Machine    *struct {
						MachineID     guid.ID `json:"machine_id"`
						MachineState  string  `json:"machine_state"`
						ProcessStatus string  `json:"process_status"`
					} `json:"machine,omitempty"`
				} `json:"precheck"`
				ID      guid.ID `json:"id"`
				State   string  `json:"state"`
				Step    string  `json:"step"`
				Version int     `json:"version"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &record))
		require.Equal(t, "IN_PROGRESS", record.Data.State)
		require.Equal(t, "HANDOVER", record.Data.Step)
		require.NotNil(t, record.Data.Precheck.WorkItems, "Delivery source is bound and confirms an empty manifest")
		require.NotNil(t, record.Data.Precheck.Executions, "Execution source is bound and confirms no active execution")
		require.NotNil(t, record.Data.Precheck.Machine)
		require.EqualValues(t, 401, record.Data.Precheck.Machine.MachineID)
		require.Equal(t, "ONLINE", record.Data.Precheck.Machine.MachineState)
		require.Equal(t, "NONE", record.Data.Precheck.Machine.ProcessStatus)
		var precheckJSON string
		require.NoError(t, db.Table("agents_retirements").Where("id=?", record.Data.ID).Select("precheck").Scan(&precheckJSON).Error)
		frozen, e := json.Marshal(record.Data.Precheck)
		require.NoError(t, e)
		require.JSONEq(t, string(frozen), precheckJSON)
		var state string
		require.NoError(t, db.Table("fleet_agents").Where("principal_id=?", created.PrincipalID).Select("state").Scan(&state).Error)
		require.Equal(t, "RETIRING", state)
		replay := send("POST", startPath, "durable-retirement-start", fmt.Sprint(profile.Version), map[string]string{"mode": "NORMAL", "reason": "Transfer responsibility through durable protocol"})
		require.Equal(t, 201, replay.Code, replay.Body.String())
		require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			profile, err = profiles.ReadOn(human, tx, 201, created.PrincipalID)
			return err
		}))
		response = send("PATCH", "/tenants/201/agent-retirements/"+record.Data.ID.String(), "durable-retirement-cancel", fmt.Sprint(record.Data.Version), map[string]any{"state": "CANCELLED", "agent_version": profile.Version}) //nolint:misspell // Preserve the published domain or serialized contract spelling.
		require.Equal(t, 200, response.Code, response.Body.String())
		require.NoError(t, db.Table("fleet_agents").Where("principal_id=?", created.PrincipalID).Select("state").Scan(&state).Error)
		require.Equal(t, "STOPPED", state)
		var active int64
		require.NoError(t, db.Table("agents_retirements").Where("tenant_id=? AND agent_principal_id=? AND active_agent_id IS NOT NULL", 201, created.PrincipalID).Count(&active).Error)
		require.Zero(t, active)
	})
	t.Run("forced retirement HTTP uses real proof revocation and offline cleanup", func(t *testing.T) {
		input := in
		input.Name, input.Handle, input.SkillIDs = "Forced executor", "forced-executor", []guid.ID{skill.Skill.ID}
		var target fleet.RegisteredAgent
		require.Equal(t, 201, call("/tenants/201/agent-registrations", "forced-registration", input, &target))
		credentials, e := module.Resolve[iamauthn.BootstrapCredentialStore](a.Modules().Services())
		require.NoError(t, e)
		const password = "Retirement-Fixture-Only-42!"
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return credentials.Create(t.Context(), tx, 301, password, time.Now().UTC()) }))
		// Password installation advances the human token fence; refresh the outer
		// fixture token so later independent subtests keep current credentials.
		token, _, e = web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
		require.NoError(t, e)
		session, e := web.CreateSession(t.Context(), 301, time.Now().UTC(), authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
		require.NoError(t, e)
		cookie := (&http.Cookie{Name: web.SessionCookieName(), Value: session}).String()
		send := func(path, key, version string, body any, browser bool) *httptest.ResponseRecorder {
			raw, e := json.Marshal(body)
			require.NoError(t, e)
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Cookie", cookie)
			req.Header.Set("Origin", "https://app.example.test")
			req.Header.Set("Idempotency-Key", key)
			req.Header.Set("If-Match", version)
			if !browser {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			r := httptest.NewRecorder()
			router.ServeHTTP(r, req)
			return r
		}
		challenge := send("/principals/301/reauthentication/challenges", "", "", map[string]string{"purpose": "agent_forced_retire", "target": "tenant:201:agent:" + target.PrincipalID.String(), "method": "password"}, true)
		require.Equal(t, 201, challenge.Code, challenge.Body.String())
		var proof struct {
			Data iamauthn.ReauthenticationOptions `json:"data"`
		}
		require.NoError(t, json.Unmarshal(challenge.Body.Bytes(), &proof))
		verified := send("/principals/301/reauthentication/verifications", "", "", map[string]string{"proof": proof.Data.Proof, "password": password}, true)
		require.Equal(t, 201, verified.Code, verified.Body.String())
		require.NoError(t, db.Table("fleet_machines").Where("id=401").Update("state", "OFFLINE").Error)
		t.Cleanup(func() { require.NoError(t, db.Table("fleet_machines").Where("id=401").Update("state", "ONLINE").Error) })
		path := "/tenants/201/agents/" + target.PrincipalID.String() + "/retirements"
		body := map[string]string{"mode": "FORCED", "reason": "Offline machine cannot be restored", "confirm_name": input.Name, "reauth_proof": proof.Data.Proof}
		response := send(path, "forced-retirement", fmt.Sprint(target.Version), body, false)
		require.Equal(t, 201, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), proof.Data.Proof)
		var record struct {
			Data struct {
				State, Step, Mode string
				ID                guid.ID
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &record))
		require.Equal(t, "COMPLETED", record.Data.State)
		require.Equal(t, "DONE", record.Data.Step)
		require.Equal(t, "FORCED", record.Data.Mode)
		var runtime struct {
			State                              string
			MachineID, PendingCleanupMachineID *guid.ID
		}
		require.NoError(t, db.Table("fleet_agents").Where("principal_id=?", target.PrincipalID).Take(&runtime).Error)
		require.Equal(t, "RETIRED", runtime.State)
		require.Nil(t, runtime.MachineID)
		require.EqualValues(t, 401, *runtime.PendingCleanupMachineID)
		var active int64
		require.NoError(t, db.Table("iam_service_accounts").Where("principal_id=? AND status=?", target.PrincipalID, "active").Count(&active).Error)
		require.Zero(t, active)
		require.NoError(t, db.Table("fleet_agent_roles").Where("agent_id=? AND revoked_at IS NULL", target.ID).Count(&active).Error)
		require.Zero(t, active)
		require.NoError(t, db.Table("fleet_agent_machine_commands").Where("agent_principal_id=? AND kind=? AND state=?", target.PrincipalID, "CLEANUP", "QUEUED").Count(&active).Error)
		require.EqualValues(t, 1, active)
		require.NoError(t, db.Table("fleet_agent_machine_commands").Where("agent_principal_id=? AND kind=? AND (state<>? OR sealed_credential<>?)", target.PrincipalID, "CREDENTIAL", "CANCELLED", "").Count(&active).Error) //nolint:misspell // Preserve the published domain or serialized contract spelling.
		require.Zero(t, active, "revoked private deliveries cannot precede cleanup in the queue")
		body["reauth_proof"] = ""
		replay := send(path, "forced-retirement", fmt.Sprint(target.Version), body, false)
		require.Equal(t, 201, replay.Code, replay.Body.String())
		require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	})
	t.Run("retirement runtime uses composed authority and cannot bypass machine ACK", func(t *testing.T) {
		// Persist genuine source-owned carrier facts so finalization proves the
		// revocation adapters, rather than only exercising their empty path.
		entities, e := module.Resolve[core.EntityProvisioner](a.Modules().Services())
		require.NoError(t, e)
		bindings, e := module.Resolve[core.EntityRoleBindingStore](a.Modules().Services())
		require.NoError(t, e)
		var project core.Entity
		var delegatedRole guid.ID
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			project, err = entities.CreateOn(human, tx, core.Entity{TenantID: 201, Type: "project", Name: "Retirement carrier facts"})
			if err != nil {
				return err
			}
			for _, key := range []string{"agent_executor", "delegated_decision_approve"} {
				role, err := roles.RoleIDOn(human, tx, 201, key)
				if err != nil {
					return err
				}
				if key == "delegated_decision_approve" {
					delegatedRole = role
				}
				if _, _, err = bindings.PutOn(human, tx, 201, project.ID, role, created.PrincipalID, core.BindingAllow, time.Time{}); err != nil {
					return err
				}
			}
			return nil
		}))
		carrierJSON, e := json.Marshal([]governance.DelegationBinding{{RoleKey: "delegated_decision_approve", RoleID: delegatedRole}})
		require.NoError(t, e)
		require.NoError(t, db.Table("governance_delegations").Create(map[string]any{"id": 701, "tenant_id": 201, "project_id": project.ID, "grantee_principal_id": created.PrincipalID, "grantee_agent_id": created.ID, "grantor_principal_id": 301, "conditions_json": "{}", "iam_bindings": string(carrierJSON), "starts_at": now, "ends_at": now.Add(time.Hour), "reason": "Retirement source fixture", "state": "ACTIVE", "active_key": "retirement-fixture", "activated_at": now, "version": 1, "created_at": now, "updated_at": now}).Error)
		require.NoError(t, db.Table("collab_channels").Create(map[string]any{"id": 702, "tenant_id": 201, "project_id": project.ID, "kind": "CUSTOM", "name": "Retirement source", "last_seq": 0, "version": 1, "created_at": now, "updated_at": now}).Error)
		require.NoError(t, db.Table("collab_channel_members").Create(map[string]any{"channel_id": 702, "principal_id": created.PrincipalID, "tenant_id": 201, "member_kind": "AGENT", "role": "MEMBER", "source": "EXPLICIT", "last_read_seq": 0, "muted": false, "joined_at": now, "created_at": now, "updated_at": now}).Error)
		retirement, e := module.Resolve[fleet.AgentRetirementRuntime](a.Modules().Services())
		require.NoError(t, e)
		profiles, e := module.Resolve[fleet.AgentProfiles](a.Modules().Services())
		require.NoError(t, e)
		var binding fleet.AgentRetirementBinding
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			profile, err := profiles.ReadOn(human, tx, 201, created.PrincipalID)
			if err != nil {
				return err
			}
			binding, err = retirement.BeginOn(human, tx, 201, created.PrincipalID, 501, profile.Version)
			return err
		}))
		require.Equal(t, "RETIRING", binding.State)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			binding, err = retirement.CleanupOn(human, tx, 201, created.PrincipalID, 501, binding.Version)
			return err
		}))
		require.NotNil(t, binding.CleanupCommandID)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			state, err := retirement.CleanupStateOn(human, tx, 201, created.PrincipalID, 501)
			require.Equal(t, "PENDING", state)
			return err
		}))
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := retirement.CompleteOn(human, tx, 201, created.PrincipalID, 501, binding.Version)
			return err
		}))
		var status string
		require.NoError(t, db.Table("iam_service_accounts").Select("status").Where("principal_id=?", created.PrincipalID).Scan(&status).Error)
		require.Equal(t, "active", status, "cleanup request alone must not claim retirement")
		var machineTokenVersion int64
		require.NoError(t, db.Table("iam_service_accounts").Select("token_version").Where("principal_id=?", account.ID).Scan(&machineTokenVersion).Error)
		machineToken, _, e := web.IssueTenantServiceAccountToken(t.Context(), account.ID.String(), "201", "", "openid", "atomic-runner", machineTokenVersion)
		require.NoError(t, e)
		runnerCall := func(method, path string, body, out any) {
			t.Helper()
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			req := httptest.NewRequest(method, path, bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+machineToken)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			require.Equal(t, 200, response.Code, response.Body.String())
			if out != nil {
				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
				require.NoError(t, json.Unmarshal(envelope.Data, out))
			}
		}
		var deliveries []fleet.AgentMachineDelivery
		var queuedCommands int64
		require.NoError(t, db.Table("fleet_agent_machine_commands").Where("machine_id=? AND state IN ?", 401, []string{"QUEUED", "RUNNING"}).Count(&queuedCommands).Error)
		for attempt := int64(0); attempt <= queuedCommands; attempt++ {
			runnerCall("POST", "/machines/401/agent-command-pulls", map[string]any{"session_id": machineSession.ID, "conn_epoch": machineSession.ConnEpoch}, &deliveries)
			if len(deliveries) == 0 {
				continue
			}
			require.Len(t, deliveries, 1)
			if deliveries[0].ID == *binding.CleanupCommandID {
				break
			}
			// Earlier subtests share this machine's FIFO queue. Acknowledge
			// their cleanup without treating it as this Agent's acknowledgement.
			require.Equal(t, "CLEANUP", deliveries[0].Kind)
			require.NotEqual(t, created.PrincipalID, deliveries[0].AgentPrincipalID)
			runnerCall("PUT", "/machines/401/agent-commands/"+deliveries[0].ID.String()+"/result", map[string]any{"session_id": machineSession.ID, "conn_epoch": machineSession.ConnEpoch, "state": "DONE"}, nil)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				state, err := retirement.CleanupStateOn(human, tx, 201, created.PrincipalID, 501)
				require.Equal(t, "PENDING", state, "another Agent's ACK must not complete this cleanup")
				return err
			}))
			deliveries = nil
		}
		require.Len(t, deliveries, 1)
		require.Equal(t, *binding.CleanupCommandID, deliveries[0].ID)
		require.Equal(t, "CLEANUP", deliveries[0].Kind)
		runnerCall("PUT", "/machines/401/agent-commands/"+deliveries[0].ID.String()+"/result", map[string]any{"session_id": machineSession.ID, "conn_epoch": machineSession.ConnEpoch, "state": "DONE"}, nil)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			state, err := retirement.CleanupStateOn(human, tx, 201, created.PrincipalID, 501)
			require.NoError(t, err)
			require.Equal(t, "ACKED", state)
			profile, err := profiles.ReadOn(human, tx, 201, created.PrincipalID)
			if err != nil {
				return err
			}
			binding, err = retirement.CompleteOn(human, tx, 201, created.PrincipalID, 501, profile.Version)
			return err
		}))
		require.Equal(t, "RETIRED", binding.State)
		require.NoError(t, db.Table("iam_service_accounts").Select("status").Where("principal_id=?", created.PrincipalID).Scan(&status).Error)
		require.Equal(t, "disabled", status)
		var activeCredentials int64
		require.NoError(t, db.Table("iam_service_account_credentials").Where("principal_id=? AND revoked_at IS NULL", created.PrincipalID).Count(&activeCredentials).Error)
		require.Zero(t, activeCredentials)
		var remainingBindings, remainingDelegations, remainingMemberships int64
		require.NoError(t, db.Table("iam_role_bindings").Where("tenant_id=? AND principal_id=? AND scope_type=?", 201, created.PrincipalID, "entity").Count(&remainingBindings).Error)
		require.NoError(t, db.Table("governance_delegations").Where("id=? AND state=?", 701, "ACTIVE").Count(&remainingDelegations).Error)
		require.NoError(t, db.Table("collab_channel_members").Where("channel_id=? AND principal_id=? AND left_at IS NULL", 702, created.PrincipalID).Count(&remainingMemberships).Error)
		require.Zero(t, remainingBindings)
		require.Zero(t, remainingDelegations)
		require.Zero(t, remainingMemberships)
	})
	t.Run("normal retirement completes genuine handover transport successor and cleanup protocol", func(t *testing.T) {
		input := in
		input.Name, input.Handle = "Handover executor", "handover-executor"
		input.SkillIDs = []guid.ID{skill.Skill.ID}
		var target fleet.RegisteredAgent
		require.Equal(t, 201, call("/tenants/201/agent-registrations", "handover-registration", input, &target))
		var tokenVersion int64
		require.NoError(t, db.Table("iam_service_accounts").Select("token_version").Where("principal_id=?", account.ID).Scan(&tokenVersion).Error)
		machineToken, _, err := web.IssueTenantServiceAccountToken(t.Context(), account.ID.String(), "201", "", "openid", "handover-fixture-runner", tokenVersion)
		require.NoError(t, err)
		request := func(bearer, method, path, key string, version int, body, out any) {
			t.Helper()
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			req := httptest.NewRequest(method, path, bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+bearer)
			req.Header.Set("Content-Type", "application/json")
			if key != "" {
				req.Header.Set("Idempotency-Key", key)
			}
			if version > 0 {
				req.Header.Set("If-Match", fmt.Sprint(version))
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			require.Less(t, response.Code, 300, response.Body.String())
			require.GreaterOrEqual(t, response.Code, 200)
			if out != nil {
				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
				require.NoError(t, json.Unmarshal(envelope.Data, out))
			}
		}
		machineFence := map[string]any{"session_id": machineSession.ID, "conn_epoch": machineSession.ConnEpoch}
		// Use genuine authenticated machine delivery/ACKs; do not patch the
		// credential-ready flag to make the HANDOVER runtime appear available.
		var pending int64
		require.NoError(t, db.Table("fleet_agent_machine_commands").Where("machine_id=? AND state IN ?", 401, []string{"QUEUED", "RUNNING"}).Count(&pending).Error)
		for n := int64(0); n <= pending; n++ {
			var commands []fleet.AgentMachineDelivery
			request(machineToken, "POST", "/machines/401/agent-command-pulls", "", 0, machineFence, &commands)
			if len(commands) == 0 {
				continue
			}
			require.Len(t, commands, 1)
			request(machineToken, "PUT", "/machines/401/agent-commands/"+commands[0].ID.String()+"/result", "", 0, map[string]any{"session_id": machineSession.ID, "conn_epoch": machineSession.ConnEpoch, "state": "DONE"}, nil)
		}
		profiles, err := module.Resolve[fleet.AgentProfiles](a.Modules().Services())
		require.NoError(t, err)
		var profile fleet.AgentProfile
		refreshProfile := func() {
			t.Helper()
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				profile, err = profiles.ReadOn(human, tx, 201, target.PrincipalID)
				return err
			}))
		}
		refreshProfile()
		var record struct {
			ID       guid.ID `json:"id"`
			Version  int     `json:"version"`
			State    string  `json:"state"`
			Step     string  `json:"step"`
			Precheck struct {
				Progress struct {
					ExecutionID *guid.ID `json:"handover_execution_id"`
					ArtifactID  *guid.ID `json:"handover_artifact_id"`
					Source      string   `json:"handover_source"`
					Cleanup     string   `json:"cleanup_status"`
				} `json:"progress"`
			} `json:"precheck"`
		}
		request(token, "POST", "/tenants/201/agents/"+target.PrincipalID.String()+"/retirements", "handover-start", profile.Version, map[string]string{"mode": "NORMAL", "reason": "Transfer actual protocol responsibilities"}, &record)
		var recovery fleet.AgentMachineBinding
		request(token, "GET", "/tenants/201/agents/"+target.PrincipalID.String()+"/machine-binding", "", 0, nil, &recovery)
		require.Equal(t, &record.ID, recovery.RetirementID, "opening the Agent detail without a retirement query must recover the durable protocol")
		path := "/tenants/201/agent-retirements/" + record.ID.String()
		advance := func(key string) {
			t.Helper()
			refreshProfile()
			request(token, "PATCH", path, key, record.Version, map[string]any{"advance": true, "agent_version": profile.Version}, &record)
		}
		advance("handover-launch")
		require.NotNil(t, record.Precheck.Progress.ExecutionID)
		fence := map[string]any{"machine_id": guid.ID(401), "session_id": machineSession.ID, "conn_epoch": machineSession.ConnEpoch}
		var pulls []execution.ExecutionView
		request(machineToken, "POST", "/runner/execution-pulls", "", 0, fence, &pulls)
		require.Len(t, pulls, 1)
		producer := pulls[0]
		require.Equal(t, *record.Precheck.Progress.ExecutionID, producer.ID)
		require.NotNil(t, producer.Plan.Handover)
		require.Zero(t, producer.AssignmentID)
		require.Zero(t, producer.WorkItemID)
		require.Empty(t, producer.Plan.BaseCommit)
		require.Zero(t, producer.Plan.RepositoryID)
		// This is an explicitly deterministic authenticated Runner fixture;
		// separate Runner subprocess tests prove actual model-process capture.
		document := "## 在途工作项\nNo active work.\n## 进行中执行与未决交互\nNo pending operations.\n## 近 30 天裁决与拒绝反馈摘要\nNo decisions.\n## 规则型记忆清单\nNo rules.\n## 频道中未闭合的对话\nNo pending references.\n## 给接手人的注意事项\nFixture runtime document."
		for _, receipt := range []execution.ReceiptInput{{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}, {DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", Content: document}} {
			request(machineToken, "POST", "/executions/"+producer.ID.String()+"/receipts", "", 0, map[string]any{"fence": fence, "receipt": receipt}, nil)
		}
		advance("handover-artifact")
		require.Equal(t, "SUCCESSOR", record.Step)
		require.Equal(t, "AGENT", record.Precheck.Progress.Source)
		var replay execution.ReceiptView
		request(machineToken, "POST", "/executions/"+producer.ID.String()+"/receipts", "", 0, map[string]any{"fence": fence, "receipt": execution.ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", Content: document}}, &replay)
		require.True(t, replay.Replayed, "response-loss retry survives protocol advance with current IAM and machine fences")
		var artifact agents.HandoverArtifact
		request(token, "GET", path+"/handover", "", 0, nil, &artifact)
		require.Equal(t, document, artifact.Content)
		require.Equal(t, &producer.ID, artifact.ExecutionID)
		require.Equal(t, record.Precheck.Progress.ArtifactID, &artifact.ID)
		refreshProfile()
		request(token, "PATCH", path, "handover-successor", record.Version, map[string]any{"successor_kind": "LATER", "agent_version": profile.Version}, &record)
		advance("handover-reassign")
		require.Equal(t, "CLEANUP", record.Step)
		advance("handover-cleanup-request")
		require.Equal(t, "PENDING", record.Precheck.Progress.Cleanup)
		var cleanup []fleet.AgentMachineDelivery
		request(machineToken, "POST", "/machines/401/agent-command-pulls", "", 0, machineFence, &cleanup)
		require.Len(t, cleanup, 1)
		require.Equal(t, "CLEANUP", cleanup[0].Kind)
		require.Equal(t, target.PrincipalID, cleanup[0].AgentPrincipalID)
		request(machineToken, "PUT", "/machines/401/agent-commands/"+cleanup[0].ID.String()+"/result", "", 0, map[string]any{"session_id": machineSession.ID, "conn_epoch": machineSession.ConnEpoch, "state": "DONE"}, nil)
		advance("handover-complete")
		require.Equal(t, "COMPLETED", record.State)
		require.Equal(t, "ACKED", record.Precheck.Progress.Cleanup)
		var count int64
		require.NoError(t, db.Table("delivery_result_versions").Where("source_execution_id=?", producer.ID).Count(&count).Error)
		require.Zero(t, count, "HANDOVER must not fabricate a work result")
		require.NoError(t, db.Table("iam_service_account_credentials").Where("principal_id=? AND revoked_at IS NULL", target.PrincipalID).Count(&count).Error)
		require.Zero(t, count)
	})
}

// Opt-in acceptance runs the installed native machine program against the
// composed production HTTP/IAM/Fleet implementation. No device, credential,
// session or inventory endpoint is replaced with a fixture handler.
func TestMachineEnrollmentInstalledRunnerLoginAndOnline(t *testing.T) {
	binary := os.Getenv("RUNNER_ENROLLMENT_BINARY")
	if binary == "" {
		t.Skip("requires RUNNER_ENROLLMENT_BINARY from an installed local package")
	}
	binary, err := filepath.Abs(binary)
	require.NoError(t, err)
	a := bootstrapComposedAppConfigured(t, func(c *platformapp.AppConfig) {
		c.OAuth.Device.VerificationURI = "https://console.example.test/device"
		c.OAuth.Device.CodeTTL = 5 * time.Minute
		c.OAuth.Device.PollInterval = time.Second
	})
	db := a.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 201, "slug": "machine-enrollment", "name": "Machine enrollment", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": 301, "login_name": "machine-owner", "display_name": "Human", "status": "active", "created_at": now, "updated_at": now}).Error)
	bootstrap, err := module.Resolve[core.TenantBootstrapper](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Human"}, now)
		return e
	}))
	human := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, SubjectType: authn.SubjectTypePrincipal})
	roles, err := module.Resolve[governance.SeededRoles](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, roles.EnsureTenant(human, 201))
	web, err := module.Resolve[iamauthn.WebService](a.Modules().Services())
	require.NoError(t, err)
	token, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	owner, ok := a.Modules().ByName(oauth.Name)
	require.True(t, ok)
	client, _, err := owner.(*oauth.Module).CreateClient(human, 201, "Machine enrollment", nil, []string{"urn:ietf:params:oauth:grant-type:device_code"}, []string{"openid"}, true, oauth.ConsentImplicit)
	require.NoError(t, err)
	server := httptest.NewServer(moduleRouter(t, a))
	defer server.Close()
	call := func(method, path string, body any, out any) int {
		t.Helper()
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		req, e := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(raw))
		require.NoError(t, e)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response, e := server.Client().Do(req)
		require.NoError(t, e)
		defer response.Body.Close()
		if response.StatusCode < 300 && out != nil {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
			require.NoError(t, json.Unmarshal(envelope.Data, out))
		}
		return response.StatusCode
	}
	profile := filepath.Join(t.TempDir(), "new-machine")
	loginCtx, stopLogin := context.WithTimeout(t.Context(), 100*time.Second)
	defer stopLogin()
	login := exec.CommandContext(loginCtx, binary, "login", "--profile", profile, "--server", server.URL, "--tenant", "201", "--client", client.ID, "--name", "Initial machine")
	hide(login)
	output, err := login.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	login.Stderr = &stderr
	require.NoError(t, login.Start())
	loginDone := make(chan error, 1)
	defer func() { stopLogin(); <-loginDone }()
	codes := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		pattern := regexp.MustCompile(`confirm code ([A-Z0-9-]+)\.`)
		for scanner.Scan() {
			match := pattern.FindStringSubmatch(scanner.Text())
			if len(match) == 2 {
				select {
				case codes <- match[1]:
				default:
				}
			}
		}
		loginDone <- login.Wait()
	}()
	var code string
	select {
	case code = <-codes:
	case <-time.After(60 * time.Second):
		t.Fatal("installed machine program did not produce an enrollment code")
	}
	var enrollment fleet.EnrollmentStatus
	require.Equal(t, http.StatusOK, call(http.MethodGet, "/tenants/201/machine-enrollments?user_code="+url.QueryEscape(code), nil, &enrollment))
	require.Equal(t, "Initial machine", enrollment.Name)
	require.NotNil(t, enrollment.Facts)
	hostname, e := os.Hostname()
	require.NoError(t, e)
	require.Equal(t, hostname, enrollment.Facts.Hostname)
	require.NotEmpty(t, enrollment.Facts.Arch)
	require.Len(t, enrollment.Facts.Runtimes, 2)
	for _, r := range enrollment.Facts.Runtimes {
		if r.Available {
			require.NotEmpty(t, r.Version)
		} else {
			require.NotEmpty(t, r.Error)
		}
	}
	require.Equal(t, http.StatusOK, call(http.MethodPut, "/tenants/201/machine-enrollments/"+enrollment.ID.String()+"/decision", map[string]any{"user_code": code, "approve": true, "name": "build-01"}, nil))
	select {
	case e := <-loginDone:
		loginDone <- nil
		require.NoError(t, e, "installed machine login failed")
	case <-time.After(30 * time.Second):
		t.Fatal("installed machine did not collect its credential")
	}
	require.NotContains(t, stderr.String(), "client_secret")
	require.Equal(t, http.StatusNotFound, call(http.MethodGet, "/tenants/201/machine-enrollments?user_code="+url.QueryEscape(code), nil, nil))
	daemonCtx, stopDaemon := context.WithTimeout(t.Context(), 80*time.Second)
	defer stopDaemon()
	daemon := exec.CommandContext(daemonCtx, binary, "daemon", "--profile", profile)
	hide(daemon)
	var daemonOutput bytes.Buffer
	daemon.Stdout = &daemonOutput
	daemon.Stderr = &daemonOutput
	require.NoError(t, daemon.Start())
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- daemon.Wait() }()
	defer func() { stopDaemon(); <-daemonDone }()
	path := "/tenants/201/machine-enrollments/" + enrollment.ID.String()
	require.Eventually(t, func() bool {
		require.Equal(t, http.StatusOK, call(http.MethodGet, path, nil, &enrollment))
		return enrollment.State == "COMPLETE" && enrollment.MachineState == "ONLINE"
	}, 45*time.Second, 250*time.Millisecond, "authenticated installed machine never became online")
	require.Equal(t, "build-01", enrollment.Name)
	require.NotNil(t, enrollment.MachineID)
	var machine struct {
		PrincipalID      guid.ID
		Name             string
		State            string
		CurrentSessionID *guid.ID
	}
	require.NoError(t, db.Table("fleet_machines").Where("id=?", *enrollment.MachineID).Take(&machine).Error)
	require.Equal(t, "build-01", machine.Name)
	require.Equal(t, "ONLINE", machine.State)
	require.NotEqual(t, guid.ID(301), machine.PrincipalID)
	require.NotNil(t, machine.CurrentSessionID)
	var active int64
	require.NoError(t, db.Table("iam_service_account_credentials").Where("principal_id=? AND revoked_at IS NULL", machine.PrincipalID).Count(&active).Error)
	require.EqualValues(t, 1, active)
	var acknowledged *time.Time
	require.NoError(t, db.Table("fleet_machine_enrollments").Select("acknowledged_at").Where("id=?", enrollment.ID).Scan(&acknowledged).Error)
	require.NotNil(t, acknowledged)
}

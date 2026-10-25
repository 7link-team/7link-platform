package composition_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	businessgovernance "7link.com/platform/internal/governance"
	iamaudit "7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/policyx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type projectRouteVerifier struct{ principal guid.ID }

func (v projectRouteVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{SubjectType: authn.SubjectTypePrincipal, PrincipalID: v.principal}, nil
}

func TestProjectProvisioningBridgePreservesMissingCapabilities(t *testing.T) {
	services := module.NewServices()
	require.NoError(t, services.Seal())
	require.NoError(t, delivery.WireProjectProvisioning(services))
	services = module.NewServices()
	require.NoError(t, module.Provide[delivery.ProjectProvisioningConnector](services, delivery.ProjectProvisioningConnector{}))
	require.NoError(t, services.Seal())
	require.ErrorIs(t, delivery.WireProjectProvisioning(services), delivery.ErrUnavailable)
	services = module.NewServices()
	called := false
	require.NoError(t, module.Provide[delivery.ProjectProvisioningConnector](services, delivery.ProjectProvisioningConnector{Bind: func(entities core.EntityProvisioner, roles core.EntityRoleBindingStore, channels collab.ChannelProvisioner) error {
		called = true
		require.Nil(t, entities.CreateOn)
		require.Nil(t, roles.PutOn)
		require.Nil(t, channels.CreateDefaultOn)
		return nil
	}}))
	require.NoError(t, services.Seal())
	require.NoError(t, delivery.WireProjectProvisioning(services))
	require.True(t, called)
}
func TestDeliveryComposesRealProvidersAndReservesProjectType(t *testing.T) {
	app := bootstrapComposedApp(t)
	reservations, err := module.Resolve[core.EntityTypeReservations](app.Modules().Services())
	require.NoError(t, err)
	require.Equal(t, []string{"project"}, reservations())
	current, ok := app.Modules().ByName(delivery.Name)
	require.True(t, ok)
	require.IsType(t, &delivery.Module{}, current)
	connector, err := module.Resolve[delivery.ProjectProvisioningConnector](app.Modules().Services())
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(core.EntityProvisioner{}, core.EntityRoleBindingStore{}, collab.ChannelProvisioner{}), delivery.ErrInvalidArgument, "production wiring must already be bound and sealed")
	require.True(t, app.Runtime().Database().Write().Migrator().HasTable("delivery_projects"))
	reader, err := module.Resolve[intake.IntakeReader](app.Modules().Services())
	require.NoError(t, err)
	directory, err := module.Resolve[fleet.AgentDirectory](app.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, app.Runtime().Database().Write().Transaction(func(tx *gorm.DB) error {
		refs, err := reader.ContactsOn(t.Context(), tx, 1, []guid.ID{1})
		require.NoError(t, err)
		require.Empty(t, refs)
		_, found, err := directory.ByPrincipalOn(t.Context(), tx, 1, 1)
		require.NoError(t, err)
		require.False(t, found)
		return nil
	}))

}

func TestProjectCreateAuthorityUsesRealIAMAndGovernance(t *testing.T) {
	app := bootstrapComposedApp(t)
	db := app.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Exec("INSERT INTO iam_tenants (id,slug,name,status,version,created_at,updated_at) VALUES (201,'authority-project','Project authority','active',1,?,?)", now, now).Error)
	for _, id := range []int{301, 302} {
		require.NoError(t, db.Exec("INSERT INTO iam_principals (id,login_name,display_name,status,created_at,updated_at) VALUES (?,?,?,'active',?,?)", id, fmt.Sprintf("project-authority-%d", id), "Authority test", now, now).Error)
	}
	bootstrap, err := module.Resolve[core.TenantBootstrapper](app.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Owner"}, now)
		if err != nil {
			return err
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, now)
	}))
	require.NoError(t, db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status,created_at,updated_at) VALUES (201,302,'active',?,?)", now, now).Error)
	roles, err := module.Resolve[businessgovernance.SeededRoles](app.Modules().Services())
	require.NoError(t, err)
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 201, PrincipalID: 301})
	require.NoError(t, roles.EnsureTenant(actor, 201))
	checker, err := module.Resolve[businessgovernance.ProjectCreationAuthority](app.Modules().Services())
	require.NoError(t, err)
	request := businessgovernance.AuthorityRequest{TenantID: 201, PrincipalID: 301, ActorKind: "HUMAN", Code: "project_create"}
	owner, err := checker(actor, nil, request.TenantID, request.PrincipalID, request.ActorKind)
	require.NoError(t, err)
	require.True(t, owner.Allowed)
	require.Equal(t, "ROLE:team_owner", owner.AuthoritySource)
	request.ActorKind = "AGENT"
	denied, err := checker(actor, nil, request.TenantID, request.PrincipalID, request.ActorKind)
	require.NoError(t, err)
	require.False(t, denied.Allowed)
	require.Equal(t, "NON_DELEGABLE", denied.ReasonCode)
	request.ActorKind = "HUMAN"
	request.PrincipalID = 302
	memberCtx := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 201, PrincipalID: 302})
	denied, err = checker(memberCtx, nil, request.TenantID, request.PrincipalID, request.ActorKind)
	require.NoError(t, err)
	require.False(t, denied.Allowed)
	require.Equal(t, "NON_DELEGABLE", denied.ReasonCode)
	var lead guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		lead, err = roles.RoleIDOn(actor, tx, 201, "delivery_lead")
		if err != nil {
			return err
		}
		// Fixture assignment is persisted; the authorization path below uses the
		// actual IAM evaluator and seed ledger, not injected Explanation values.
		if err = tx.Exec("INSERT INTO iam_role_members (tenant_id,role_id,principal_id,created_at) VALUES (201,?,302,?)", lead, now).Error; err != nil {
			return err
		}
		return bootstrap.BumpRevision(actor, tx, 201, now)
	}))
	// A conditional project grant must see the new project's facts, not a
	// previous resource's context or missing environment defaults.
	condition := `{"version":1,"all":[{"eq":[{"context":"resource.type"},{"value":"project"}]},{"eq":[{"context":"resource.owner"},{"value":"302"}]},{"eq":[{"context":"resource.attr.environment"},{"value":"DEFAULT"}]},{"eq":[{"context":"resource.attr.project_mode"},{"value":"HYBRID"}]}]}`
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE iam_role_permissions SET condition_json=? WHERE tenant_id=201 AND role_id=? AND permission_code='project_manage'", condition, lead).Error; err != nil {
			return err
		}
		return bootstrap.BumpRevision(actor, tx, 201, now)
	}))
	memberCtx = policyx.WithResourceContext(memberCtx, policyx.ResourceContext{Type: "unrelated", Owner: "999", Attrs: map[string]string{"environment": "PRODUCTION", "project_mode": "AUTONOMOUS"}})
	allowed, err := checker(memberCtx, nil, request.TenantID, request.PrincipalID, request.ActorKind)
	require.NoError(t, err)
	require.True(t, allowed.Allowed)
	require.Equal(t, "ROLE:delivery_lead", allowed.AuthoritySource)
	// The transport guard must receive the same server-owned creation facts
	// before its IAM check; otherwise a valid conditional grant never reaches
	// the domain checker. Use real IAM membership/evaluation, only authentication
	// is a fixture supplying the already-verified principal.
	evaluator, err := module.Resolve[core.AuthorizationEvaluator](app.Modules().Services())
	require.NoError(t, err)
	members, err := module.Resolve[core.ActiveMemberChecker](app.Modules().Services())
	require.NoError(t, err)
	sharedRegistrar, err := module.Resolve[*authz.Registrar](app.Modules().Services())
	require.NoError(t, err)
	registrar := authz.NewRegistrar(sharedRegistrar.Registry(), projectRouteVerifier{principal: 302}, evaluator, members)
	_, httpAPI := humatest.New(t)
	calls := 0
	handler := func(ctx context.Context, _ *struct{}) (*struct{ Body string }, error) {
		calls++
		decision, err := checker(ctx, nil, 201, 302, "HUMAN")
		require.NoError(t, err)
		require.True(t, decision.Allowed)
		return &struct{ Body string }{Body: "authorized"}, nil
	}
	for _, facts := range []bool{false, true} {
		guard := authz.Guard{Resource: "project", Verb: "manage"}
		if facts {
			guard.CreationAttributes = map[string]string{"environment": "DEFAULT", "project_mode": "HYBRID"}
		}
		path := fmt.Sprintf("/project-create-facts-%t", facts)
		authz.Register(registrar, httpAPI, huma.Operation{OperationID: fmt.Sprintf("test.project.create.%t", facts), Method: http.MethodPost, Path: path, Tags: []string{"tenants"}}, guard, handler)
	}
	rejected := httpAPI.Post("/tenants/201/project-create-facts-false")
	require.Equal(t, http.StatusForbidden, rejected.Code, rejected.Body.String())
	accepted := httpAPI.Post("/tenants/201/project-create-facts-true")
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	require.Equal(t, 1, calls)

	var auditCount int64
	require.NoError(t, db.Table("iam_audit_events").Where("tenant_id=201").Count(&auditCount).Error)
	abort := errors.New("later business write failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		initial, err := checker(memberCtx, tx, request.TenantID, request.PrincipalID, request.ActorKind)
		require.NoError(t, err)
		require.True(t, initial.Allowed)
		// Simulate persisted revocation in this transaction, then roll it back.
		require.NoError(t, tx.Exec("DELETE FROM iam_role_permissions WHERE tenant_id=201 AND role_id=? AND permission_code='project_manage'", lead).Error)
		require.NoError(t, bootstrap.BumpRevision(actor, tx, 201, now))
		revoked, err := checker(memberCtx, tx, request.TenantID, request.PrincipalID, request.ActorKind)
		require.NoError(t, err)
		require.False(t, revoked.Allowed)
		require.Empty(t, revoked.AuthoritySource)
		require.Greater(t, revoked.PolicyRevision, initial.PolicyRevision)
		return abort
	}), abort)
	restored, err := checker(memberCtx, nil, request.TenantID, request.PrincipalID, request.ActorKind)
	require.NoError(t, err)
	require.Equal(t, allowed, restored)
	var after int64
	require.NoError(t, db.Table("iam_audit_events").Where("tenant_id=201").Count(&after).Error)
	require.Equal(t, auditCount, after, "authorization itself must not append business audit records")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM iam_role_permissions WHERE tenant_id=201 AND role_id=? AND permission_code='project_manage'", lead).Error; err != nil {
			return err
		}
		return bootstrap.BumpRevision(actor, tx, 201, now)
	}))
	revokedHTTP := httpAPI.Post("/tenants/201/project-create-facts-true")
	require.Equal(t, http.StatusForbidden, revokedHTTP.Code, revokedHTTP.Body.String())
	require.Equal(t, 1, calls, "declared facts must never become an authority grant")

}

// Exercise the production route with real JWT verification and real providers.
func TestProjectCreateHTTPUsesEntityAndReplaysAtomically(t *testing.T) {
	app := bootstrapComposedApp(t)
	db := app.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Exec("INSERT INTO iam_tenants (id,slug,name,status,version,created_at,updated_at) VALUES (201,'project-http','Project HTTP','active',1,?,?)", now, now).Error)
	require.NoError(t, db.Exec("INSERT INTO iam_principals (id,login_name,display_name,status,created_at,updated_at) VALUES (301,'project-http-owner','Owner','active',?,?)", now, now).Error)
	bootstrap, err := module.Resolve[core.TenantBootstrapper](app.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Owner"}, now)
		if err != nil {
			return err
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, now)
	}))
	roles, err := module.Resolve[businessgovernance.SeededRoles](app.Modules().Services())
	require.NoError(t, err)
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 201, PrincipalID: 301})
	require.NoError(t, roles.EnsureTenant(actor, 201))
	web, err := module.Resolve[iamauthn.WebService](app.Modules().Services())
	require.NoError(t, err)
	token, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	router := moduleRouter(t, app)
	post := func(body, key, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/tenants/201/projects", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}

	options := func(method, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/tenants/201/projects?method="+method, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	unauthContract := options("POST", "")
	require.Equal(t, http.StatusUnauthorized, unauthContract.Code, unauthContract.Body.String())
	contractResponse := options("POST", token)
	require.Equal(t, http.StatusOK, contractResponse.Code, contractResponse.Body.String())
	require.Equal(t, "private, no-store", contractResponse.Header().Get("Cache-Control"))
	var contract struct {
		Data authz.OperationContract `json:"data"`
	}
	require.NoError(t, json.Unmarshal(contractResponse.Body.Bytes(), &contract))
	require.Equal(t, "project.create", contract.Data.OperationID)
	require.Equal(t, "POST", contract.Data.Method)
	require.Len(t, contract.Data.Version, 64)
	schemaBytes, err := json.Marshal(contract.Data.Schemas)
	require.NoError(t, err)
	require.Contains(t, string(schemaBytes), `"pattern":"^[A-Z][A-Z0-9-]{1,15}$"`)
	require.Contains(t, string(schemaBytes), `"maxLength":2000`)
	require.Contains(t, string(schemaBytes), `"format":"date"`)
	var beforeCreate int64
	require.NoError(t, db.Table("delivery_projects").Count(&beforeCreate).Error)
	require.Zero(t, beforeCreate, "OPTIONS must not create a project")
	unsupported := options("DELETE", token)
	require.Equal(t, http.StatusUnprocessableEntity, unsupported.Code, unsupported.Body.String())
	body := `{"name":"HTTP project","code":"HTTP-1","budget_cap_micros":9007199254740993}`
	unauth := post(body, "", "")
	require.Equal(t, http.StatusUnauthorized, unauth.Code, unauth.Body.String())
	first := post(body, "project-http-1", token)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	var result struct {
		Data struct {
			Project delivery.ProjectView `json:"project"`
			Entity  struct {
				ID guid.ID `json:"id"`
			} `json:"entity"`
			ChannelID guid.ID `json:"channel_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &result))
	require.Positive(t, result.Data.Project.ProjectID)
	require.Equal(t, result.Data.Entity.ID, result.Data.Project.ProjectID)
	require.Equal(t, int64(9007199254740993), *result.Data.Project.BudgetCapMicros)
	require.Positive(t, result.Data.ChannelID)
	get := func(id guid.ID, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/entities/"+id.String()+"/project", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	require.Equal(t, http.StatusUnauthorized, get(result.Data.Entity.ID, "").Code)
	detail := get(result.Data.Entity.ID, token)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	require.Equal(t, `"1"`, detail.Header().Get("ETag"))
	var detailBody struct {
		Data struct {
			delivery.ProjectView
			Name   string `json:"name"`
			Client any    `json:"client"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(detail.Body.Bytes(), &detailBody))
	require.Equal(t, "HTTP project", detailBody.Data.Name)
	require.Nil(t, detailBody.Data.Client)
	require.Equal(t, result.Data.Project, detailBody.Data.ProjectView)
	require.Equal(t, http.StatusNotFound, get(999, token).Code)
	list := func(raw, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/tenants/201/projects"+raw, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	require.Equal(t, http.StatusUnauthorized, list("", "").Code)
	listed := list("?name=like:HTTP&limit=1&sort=name", token)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var projectList struct {
		Data []struct {
			Name   string `json:"name"`
			Budget struct {
				Cap   *string `json:"cap"`
				Spent string  `json:"spent"`
			} `json:"budget"`
			Counts map[string]int64 `json:"counts"`
		} `json:"data"`
		Meta struct {
			Page struct {
				Total int64 `json:"total"`
				Limit int   `json:"limit"`
			} `json:"page"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &projectList))
	require.Len(t, projectList.Data, 1)
	require.Equal(t, "HTTP project", projectList.Data[0].Name)
	require.Equal(t, "9007199254740993", *projectList.Data[0].Budget.Cap)
	require.Equal(t, "0", projectList.Data[0].Budget.Spent)
	require.Equal(t, map[string]int64{"open_work_items": 0, "needs_review": 0, "waiting_deliveries": 0}, projectList.Data[0].Counts)
	require.Equal(t, int64(1), projectList.Meta.Page.Total)
	require.Equal(t, 1, projectList.Meta.Page.Limit)
	for _, raw := range []string{"?unknown=eq:1", "?name=eq:HTTP", "?stage=eq:INVALID", "?limit=101", "?offset=-1", "?sort=unsupported", "?archived=true"} {
		require.Equal(t, http.StatusUnprocessableEntity, list(raw, token).Code, raw)
	}
	replay := post(body, "project-http-1", token)
	require.Equal(t, http.StatusCreated, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	require.JSONEq(t, first.Body.String(), replay.Body.String())
	changed := post(strings.Replace(body, "HTTP-1", "HTTP-2", 1), "project-http-1", token)
	require.Equal(t, http.StatusUnprocessableEntity, changed.Code, changed.Body.String())

	duplicate := post(strings.Replace(body, "HTTP project", "Other project", 1), "project-duplicate", token)
	require.Equal(t, http.StatusConflict, duplicate.Code, duplicate.Body.String())
	duplicateReplay := post(strings.Replace(body, "HTTP project", "Other project", 1), "project-duplicate", token)
	require.Equal(t, http.StatusConflict, duplicateReplay.Code, duplicateReplay.Body.String())
	require.Equal(t, "true", duplicateReplay.Header().Get("Idempotent-Replayed"))
	require.JSONEq(t, duplicate.Body.String(), duplicateReplay.Body.String())
	// A missing contact must fail without leaving a client, entity or project.
	failed := post(`{"name":"Failed project","code":"FAILED","client":{"new_name":"Rolled back client"},"contacts":[{"intake_contact_id":"999","role":"PRIMARY"}]}`, "", token)
	require.Equal(t, http.StatusUnprocessableEntity, failed.Code, failed.Body.String())
	var residue int64
	require.NoError(t, db.Table("iam_entities").Where("tenant_id=201 AND name='Rolled back client'").Count(&residue).Error)
	require.Zero(t, residue)
	for _, check := range []struct {
		table, where string
		args         []any
	}{
		{"file_nodes", "root_scope='entity' AND root_owner_id=?", []any{result.Data.Entity.ID}},
		{"collab_channels", "tenant_id=201 AND project_id=?", []any{result.Data.Entity.ID}},
		{"collab_channel_members", "tenant_id=201 AND channel_id=? AND principal_id=301", []any{result.Data.ChannelID}},
		{"iam_role_bindings", "tenant_id=201 AND scope_type='entity' AND scope_id=? AND principal_id=301", []any{result.Data.Entity.ID}},
		{"domain_events", "tenant_id=201 AND project_id=? AND event_type='project.created'", []any{result.Data.Entity.ID}},
	} {
		require.NoError(t, db.Table(check.table).Where(check.where, check.args...).Count(&residue).Error)
		require.Equal(t, int64(1), residue, check.table+" must be provisioned exactly once")
	}
	var count int64
	require.NoError(t, db.Table("delivery_projects").Where("tenant_id=201").Count(&count).Error)
	require.Equal(t, int64(1), count)
	require.NoError(t, db.Table("iam_entities").Where("tenant_id=201 AND type='project'").Count(&count).Error)
	require.Equal(t, int64(1), count)

	// Fail at the final outbox insert, after real provisioning has written all
	// resources. Compare complete table counts before and after the HTTP call.
	tables := []string{"iam_entities", "delivery_projects", "delivery_project_contacts", "delivery_project_repositories", "file_nodes", "file_node_closure", "collab_channels", "collab_channel_members", "iam_role_bindings", "iam_audit_events", "domain_events", "platform_idempotency_keys"}
	baseline := make(map[string]int64, len(tables))
	for _, table := range tables {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		baseline[table] = n
	}
	const callback = "test:project-outbox-failure"
	reached := false
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "domain_events" {
			reached = true
			tx.AddError(errors.New("injected outbox write failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
	lateBody := `{"name":"Late failure","code":"LATE-1","client":{"new_name":"Atomic client"},"repositories":[{"name":"main","remote_url":"https://example.test/repo.git"}]}`
	lateFailure := post(lateBody, "late-failure", token)
	require.True(t, reached, "the test must reach the final write")
	require.Equal(t, http.StatusInternalServerError, lateFailure.Code, lateFailure.Body.String())
	require.NotContains(t, lateFailure.Body.String(), "injected outbox")
	for _, table := range tables {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.Equal(t, baseline[table], n, table+" must roll back")
	}
	require.NoError(t, db.Callback().Create().Remove(callback))
	retry := post(lateBody, "late-failure", token)
	require.Equal(t, http.StatusCreated, retry.Code, retry.Body.String())
	require.Empty(t, retry.Header().Get("Idempotent-Replayed"), "failed server response must not be cached")
	var clientResult struct {
		Data struct {
			Project        delivery.ProjectView `json:"project"`
			ClientEntityID guid.ID              `json:"client_entity_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(retry.Body.Bytes(), &clientResult))
	clientDetail := get(clientResult.Data.Project.ProjectID, token)
	require.Equal(t, http.StatusOK, clientDetail.Code, clientDetail.Body.String())
	var clientBody struct {
		Data struct {
			Name   string `json:"name"`
			Client struct {
				ID   guid.ID `json:"id"`
				Name string  `json:"name"`
			} `json:"client"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(clientDetail.Body.Bytes(), &clientBody))
	require.Equal(t, "Late failure", clientBody.Data.Name)
	require.Equal(t, "Atomic client", clientBody.Data.Client.Name)
	require.Equal(t, clientResult.Data.ClientEntityID, clientBody.Data.Client.ID)
	nonProject := get(clientResult.Data.ClientEntityID, token)
	require.Equal(t, http.StatusNotFound, nonProject.Code, nonProject.Body.String())
	// A genuine token for another tenant is not project authority.
	require.NoError(t, db.Exec("INSERT INTO iam_tenants (id,slug,name,status,version,created_at,updated_at) VALUES (202,'other-project-http','Other tenant','active',1,?,?)", now, now).Error)
	require.NoError(t, db.Exec("INSERT INTO iam_principals (id,login_name,display_name,status,created_at,updated_at) VALUES (302,'other-project-reader','Other reader','active',?,?)", now, now).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 202, OwnerID: 302, DisplayName: "Owner"}, now)
		return err
	}))
	otherToken, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 302, "202", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	denied := get(result.Data.Entity.ID, otherToken)
	require.Equal(t, http.StatusNotFound, denied.Code, denied.Body.String(), "existing owned-resource guard conceals resources from nonmembers")
	require.NotContains(t, denied.Body.String(), "HTTP project")
	require.NoError(t, db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status,created_at,updated_at) VALUES (201,302,'active',?,?)", now, now).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return bootstrap.BumpRevision(t.Context(), tx, 201, now) }))
	memberToken, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 302, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	deniedMember := get(result.Data.Entity.ID, memberToken)
	require.Equal(t, http.StatusForbidden, deniedMember.Code, deniedMember.Body.String())
	require.NotContains(t, deniedMember.Body.String(), "HTTP project")

}

// Persisted identity/Agent/delegation fixtures represent an existing grant.
// Every decision and write below uses the composed IAM/Governance/Intake/
// Delivery/Clientwork providers and the real authenticated HTTP route.
func TestDelegatedNewComposedCurrentAuthorityAndReplay(t *testing.T) {
	app := bootstrapComposedApp(t)
	db := app.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	insert := func(table string, row map[string]any) {
		t.Helper()
		require.NoError(t, db.Table(table).Create(row).Error)
	}
	insert("iam_tenants", map[string]any{"id": 201, "slug": "delegated-intake", "name": "Delegation test", "status": "active", "version": 1, "created_at": now, "updated_at": now})
	insert("iam_principals", map[string]any{"id": 301, "login_name": "delegated-owner", "display_name": "Owner", "status": "active", "created_at": now, "updated_at": now})
	bootstrap, err := module.Resolve[core.TenantBootstrapper](app.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Owner"}, now)
		if e != nil {
			return e
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, now)
	}))
	owner := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 201, PrincipalID: 301})
	roles, err := module.Resolve[businessgovernance.SeededRoles](app.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, roles.EnsureTenant(owner, 201))
	web, err := module.Resolve[iamauthn.WebService](app.Modules().Services())
	require.NoError(t, err)
	humanToken, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	enrollment, err := module.Resolve[fleet.EnrollmentIAM](app.Modules().Services())
	require.NoError(t, err)
	account, err := enrollment.CreateAccount(owner, 201, "intake-converter", "Intake converter", "Delegation test fixture", nil)
	require.NoError(t, err)
	agentToken, _, err := web.IssueTenantServiceAccountToken(t.Context(), account.ID.String(), "201", "", "", account.LoginName, 1)
	require.NoError(t, err)
	insert("fleet_agents", map[string]any{"id": 601, "principal_id": account.ID, "name": "Converter", "handle": "converter", "avatar_seed": 1, "system_prompt": "Test fixture", "runtime_kind": "CODEX", "model_id": "fixture", "model_family": "GPT", "state": "ACTIVE", "origin": "HUMAN_CREATED", "created_by_principal_id": 301, "owner_principal_id": 301, "tenant_id": 201, "created_at": now, "updated_at": now})
	insert("fleet_agent_roles", map[string]any{"agent_id": 601, "role": "REVIEWER", "iam_role_key": "agent_reviewer", "tenant_id": 201, "created_at": now, "updated_at": now})
	router := moduleRouter(t, app)
	call := func(method, path, token, key, version string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if version != "" {
			req.Header.Set("If-Match", version)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	created := call("POST", "/tenants/201/projects", humanToken, "n-delegated-project", "", map[string]any{"name": "Delegated project", "code": "DEL-N", "budget_cap_micros": 10000})
	require.Equal(t, 201, created.Code, created.Body.String())
	var project struct {
		Data struct {
			Entity struct {
				ID guid.ID `json:"id"`
			} `json:"entity"`
			ChannelID guid.ID `json:"channel_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &project))
	p := project.Data.Entity.ID
	// Existing project mode is a fixture; this test does not claim mode-change or
	// external Agent/model execution acceptance.
	require.NoError(t, db.Table("delivery_projects").Where("tenant_id=201 AND project_id=?", p).Updates(map[string]any{"team_mode": "AUTONOMOUS"}).Error)
	owner = authn.WithClaims(owner, &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 201, EntityID: p, PrincipalID: 301})
	insert("intake_records", map[string]any{"id": 701, "ref_no": 1, "channel_id": project.Data.ChannelID, "channel_kind": "SHARED_CHANNEL", "contact_id": 801, "title": "Original request", "raw_content": "Immutable source text", "raw_content_hash": strings.Repeat("a", 64), "reply_token": "n-delegated-card", "project_id": p, "received_at": now, "tenant_id": 201, "created_at": now, "updated_at": now})
	sources, err := module.Resolve[intake.ChangeSources](app.Modules().Services())
	require.NoError(t, err)
	var card intake.TriageSource
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		card, e = sources.TriageOn(owner, tx, 201, p, 701, 1, intake.TriageDraft{Summary: "Export selected rows", RequirementDraft: "Preserve the frozen request", Criteria: []string{"Every selected row is present"}, Size: "S"})
		return e
	}))
	path := "/entities/" + p.String() + "/intakes/701/delegated-requirement"
	body := map[string]any{"triage_card_revision": card.Revision, "proposal_hash": card.Hash}
	require.NotEqual(t, 201, call("POST", path, agentToken, "n-no-grant", `"2"`, body).Code)
	var role guid.ID
	binding, err := module.Resolve[core.EntityRoleBindingStore](app.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		role, e = roles.RoleIDOn(owner, tx, 201, "delegated_intake_convert")
		if e != nil {
			return e
		}
		_, _, e = binding.PutOn(owner, tx, 201, p, role, account.ID, core.BindingAllow, now.Add(time.Hour))
		return e
	}))
	t.Run("P-C33 Agent evidence uses current IAM cursor and immutable replay", func(t *testing.T) {
		registry, e := module.Resolve[*agents.OperationRegistry](app.Modules().Services())
		require.NoError(t, e)
		mounted := map[string]agents.Operation{}
		for _, op := range registry.List() {
			mounted[op.Name] = op
		}
		for _, expected := range []struct{ id, method, path, permission string }{
			{"evidence.create", "POST", "/executions/{execution_id}/evidence", "evidence_submit"},
			{"evidence.update", "PATCH", "/evidence/{evidence_id}", "evidence_submit"},
			{"artifact_comment.create", "POST", "/executions/{execution_id}/artifact-comments", "artifact_comment_write"},
			{"artifact_comment.reply", "POST", "/comment-threads/{thread_id}/comments", "artifact_comment_write"},
		} {
			op, found := mounted[expected.id]
			require.True(t, found, "mounted operation %s", expected.id)
			require.Equal(t, expected.method, op.Method)
			require.Equal(t, expected.path, op.Path)
			require.Equal(t, expected.permission, op.RequiredPermission)
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			executorRole, e := roles.RoleIDOn(owner, tx, 201, "agent_executor")
			if e != nil {
				return e
			}
			_, _, e = binding.PutOn(owner, tx, 201, p, executorRole, account.ID, core.BindingAllow, now.Add(time.Hour))
			return e
		}))
		insert("delivery_work_items", map[string]any{"id": 951, "tenant_id": 201, "project_id": p, "number": 951, "title": "Evidence context fixture", "type": "TASK", "state": "IN_PROGRESS", "baseline_version_no": 1, "current_execution_id": 952, "accountable_principal_id": 301, "state_changed_at": now, "created_at": now, "updated_at": now, "created_by": 301})
		plan, e := json.Marshal(map[string]any{"executor_principal_id": account.ID})
		require.NoError(t, e)
		digest := sha256.Sum256(plan)
		insert("execution_executions", map[string]any{"id": 952, "tenant_id": 201, "project_id": p, "work_item_id": 951, "assignment_id": 953, "machine_id": 954, "initiator_id": 301, "state": "RUNNING", "state_reason": "", "result_state": "NONE", "plan_json": string(plan), "plan_hash": hex.EncodeToString(digest[:]), "dispatch_epoch": 1, "last_seq": 1, "lease_expires_at": now.Add(time.Hour), "version": 1, "created_at": now, "updated_at": now})
		recorder, e := module.Resolve[delivery.WorkItemActivityRecorder](app.Modules().Services())
		require.NoError(t, e)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := recorder.RecordOn(owner, tx, 201, 951, delivery.WorkActivityInput{Kind: "COMMENT_ADDED", ActorKind: "HUMAN", ActorID: 301, RefKind: "work_item", RefID: 951, Title: "New acceptance context"})
			return e
		}))
		evidencePath := "/executions/952/evidence"
		evidenceBody := map[string]any{"evidence_type": "OBSERVATION", "title": "Scoped observation", "scope_text": "Acceptance screen", "text": "The submit action confirms success"}
		count := func(table, predicate string, values ...any) int64 {
			var n int64
			require.NoError(t, db.Table(table).Where(predicate, values...).Count(&n).Error)
			return n
		}
		eventCount := func() int64 {
			return count("domain_events", "tenant_id=201 AND event_type=?", "execution.evidence_recorded")
		}
		before := eventCount()
		missingEvidenceCursor := call("POST", evidencePath, agentToken, "t-context-evidence", "", evidenceBody)
		require.Equal(t, 422, missingEvidenceCursor.Code, missingEvidenceCursor.Body.String())
		evidenceBody["seen_seq"] = 0
		stale := call("POST", evidencePath, agentToken, "t-context-evidence", "", evidenceBody)
		require.Equal(t, 409, stale.Code, stale.Body.String())
		require.Contains(t, stale.Body.String(), `"state":"interrupted"`)
		require.Zero(t, count("execution_evidence", "execution_id=952"))
		require.Equal(t, before, eventCount(), "interruption cannot publish evidence")
		evidenceBody["seen_seq"] = 1
		fresh := call("POST", evidencePath, agentToken, "t-context-evidence", "", evidenceBody)
		require.Equal(t, 201, fresh.Code, fresh.Body.String())
		require.EqualValues(t, 1, count("execution_evidence", "execution_id=952 AND producer_kind='AGENT' AND producer_id=?", account.ID))
		require.Equal(t, before+1, eventCount())
		require.EqualValues(t, 1, count("delivery_work_item_activities", "work_item_id=951 AND kind='EVIDENCE_ADDED'"))
		require.Zero(t, count("agents_inbox_events", "agent_principal_id=? AND subject_id=952 AND stream_key='work_item:951' AND stream_seq=2", account.ID), "the executor does not receive an inbox echo for its own evidence")
		evidenceBody["seen_seq"] = 2
		replay := call("POST", evidencePath, agentToken, "t-context-evidence", "", evidenceBody)
		require.Equal(t, 201, replay.Code, replay.Body.String())
		require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
		require.JSONEq(t, fresh.Body.String(), replay.Body.String())
		require.Equal(t, before+1, eventCount())
		// Fail the real transactional outbox insert after source, activity and
		// audit writes; none of those facts may survive the rejected command.
		require.NoError(t, db.Exec(`CREATE TRIGGER pc33_evidence_outbox_failure BEFORE INSERT ON domain_events WHEN NEW.event_type = 'execution.evidence_recorded' BEGIN SELECT RAISE(ABORT, 'evidence outbox unavailable'); END`).Error)
		evidenceBody["text"] = "A second independently observed outcome"
		rejected := call("POST", evidencePath, agentToken, "t-context-outbox-failure", "", evidenceBody)
		require.Equal(t, 503, rejected.Code, rejected.Body.String())
		require.NoError(t, db.Exec("DROP TRIGGER pc33_evidence_outbox_failure").Error)
		require.EqualValues(t, 1, count("execution_evidence", "execution_id=952"))
		require.EqualValues(t, 1, count("delivery_work_item_activities", "work_item_id=951 AND kind='EVIDENCE_ADDED'"))
		require.Equal(t, before+1, eventCount())
		var current struct{ ActivitySeq int64 }
		require.NoError(t, db.Table("delivery_work_items").Where("id=951 AND tenant_id=201").Take(&current).Error)
		require.EqualValues(t, 2, current.ActivitySeq)
		evidenceBody["text"] = "The submit action confirms success"
		var sourceEvidence struct{ ID guid.ID }
		require.NoError(t, db.Table("execution_evidence").Where("execution_id=952").Take(&sourceEvidence).Error)
		insert("execution_artifact_comment_threads", map[string]any{"id": 956, "tenant_id": 201, "project_id": p, "execution_id": 952, "work_item_id": 951, "evidence_id": sourceEvidence.ID, "visibility": "INTERNAL", "state": "OPEN", "anchor_snapshot": "{}", "first_body": "Human context to read", "created_by": 301, "created_at": now, "version": 1})
		insert("execution_artifact_comment_messages", map[string]any{"id": 957, "tenant_id": 201, "thread_id": 956, "seq": 1, "body": "Human context to read", "created_by": 301, "created_at": now})
		replyPath := "/comment-threads/956/comments"
		replyBody := map[string]any{"body": "Agent read the latest thread context"}
		require.Equal(t, 422, call("POST", replyPath, agentToken, "t-context-reply", "", replyBody).Code)
		replyBody["seen_seq"] = 0
		staleReply := call("POST", replyPath, agentToken, "t-context-reply", "", replyBody)
		require.Equal(t, 409, staleReply.Code, staleReply.Body.String())
		require.Contains(t, staleReply.Body.String(), `"stream":"comment_thread:956"`)
		require.EqualValues(t, 1, count("execution_artifact_comment_messages", "thread_id=956"))
		replyBody["seen_seq"] = 1
		replied := call("POST", replyPath, agentToken, "t-context-reply", "", replyBody)
		require.Equal(t, 201, replied.Code, replied.Body.String())
		humanReply := call("POST", replyPath, humanToken, "t-context-human-reply", "", map[string]any{"body": "A further Human clarification"})
		require.Equal(t, 201, humanReply.Code, humanReply.Body.String())
		require.EqualValues(t, 1, count("agents_inbox_events", "agent_principal_id=? AND subject_id=956 AND stream_key='comment_thread:956' AND stream_seq=3", account.ID))
		replyBody["seen_seq"] = 3
		replyReplay := call("POST", replyPath, agentToken, "t-context-reply", "", replyBody)
		require.Equal(t, 201, replyReplay.Code, replyReplay.Body.String())
		require.Equal(t, "true", replyReplay.Header().Get("Idempotent-Replayed"))
		require.JSONEq(t, replied.Body.String(), replyReplay.Body.String())
		require.EqualValues(t, 3, count("execution_artifact_comment_messages", "thread_id=956"))
		require.EqualValues(t, 2, count("delivery_work_item_activities", "work_item_id=951 AND kind='COMMENT_ADDED' AND ref_kind='artifact_comment'"))
		require.NoError(t, db.Exec(`CREATE TRIGGER pc33_reply_outbox_failure BEFORE INSERT ON domain_events WHEN NEW.event_type = 'execution.artifact_comment_added' BEGIN SELECT RAISE(ABORT, 'comment outbox unavailable'); END`).Error)
		failedReply := call("POST", replyPath, agentToken, "t-context-reply-fail", "", map[string]any{"body": "Uncommitted reply", "seen_seq": 3})
		require.Equal(t, 503, failedReply.Code, failedReply.Body.String())
		require.NoError(t, db.Exec("DROP TRIGGER pc33_reply_outbox_failure").Error)
		require.EqualValues(t, 3, count("execution_artifact_comment_messages", "thread_id=956"))
		require.EqualValues(t, 2, count("delivery_work_item_activities", "work_item_id=951 AND kind='COMMENT_ADDED' AND ref_kind='artifact_comment'"))
		require.NoError(t, db.Table("delivery_work_items").Where("id=951 AND tenant_id=201").Take(&current).Error)
		require.EqualValues(t, 4, current.ActivitySeq)
		t.Cleanup(func() {
			require.NoError(t, db.Table("iam_tenant_members").Where("tenant_id=201 AND principal_id=?", account.ID).Update("status", "active").Error)
		})
		require.NoError(t, db.Table("iam_tenant_members").Where("tenant_id=201 AND principal_id=?", account.ID).Update("status", "disabled").Error)
		revoked := call("POST", evidencePath, agentToken, "t-context-evidence", "", evidenceBody)
		require.Equal(t, 401, revoked.Code, revoked.Body.String())
		revokedReply := call("POST", replyPath, agentToken, "t-context-reply", "", replyBody)
		require.Equal(t, 401, revokedReply.Code, revokedReply.Body.String())
		require.EqualValues(t, 1, count("execution_evidence", "execution_id=952"))
		require.NoError(t, db.Table("iam_tenant_members").Where("tenant_id=201 AND principal_id=?", account.ID).Update("status", "active").Error)
	})
	// A real carrier alone is insufficient without the domain delegation record.
	require.Equal(t, 403, call("POST", path, agentToken, "n-carrier-only", `"2"`, body).Code)
	conditions, _ := json.Marshal(businessgovernance.DelegationConditions{Actions: []string{"intake_convert"}, MaxRisk: "LOW", CostCapMicros: 1, MaxRework: 1})
	bindings, _ := json.Marshal([]businessgovernance.DelegationBinding{{RoleKey: "delegated_intake_convert", RoleID: role}})
	insert("governance_delegations", map[string]any{"id": 901, "tenant_id": 201, "project_id": p, "grantee_principal_id": account.ID, "grantee_agent_id": 601, "grantor_principal_id": 301, "conditions_json": string(conditions), "iam_bindings": string(bindings), "starts_at": now.Add(-time.Minute), "ends_at": now.Add(time.Hour), "activated_at": now.Add(-time.Minute), "reason": "Existing test grant", "state": "ACTIVE", "active_key": "n-delegated-active", "version": 1, "created_at": now, "updated_at": now})
	preview := call("GET", path, agentToken, "", "", nil)
	require.Equal(t, 200, preview.Code, preview.Body.String())
	require.NoError(t, db.Table("intake_triage_cards").Where("tenant_id=201 AND intake_id=701").Update("actor_id", account.ID).Error)
	require.Equal(t, 403, call("POST", path, agentToken, "n-self-card", `"2"`, body).Code)
	require.NoError(t, db.Table("intake_triage_cards").Where("tenant_id=201 AND intake_id=701").Update("actor_id", 301).Error)
	require.NoError(t, db.Table("delivery_projects").Where("project_id=?", p).Update("team_mode", "HYBRID").Error)
	require.Equal(t, 403, call("POST", path, agentToken, "n-hybrid", `"2"`, body).Code)
	require.NoError(t, db.Table("delivery_projects").Where("project_id=?", p).Update("team_mode", "AUTONOMOUS").Error)
	require.NoError(t, db.Table("delivery_projects").Where("project_id=?", p).Update("stage", "CLOSED").Error)
	require.Equal(t, 409, call("POST", path, agentToken, "n-closed-project", `"2"`, body).Code, "canonical project read-only fencing rejects a closed project")
	require.NoError(t, db.Table("delivery_projects").Where("project_id=?", p).Update("stage", "INTAKE").Error)
	first := call("POST", path, agentToken, "n-convert-once", `"2"`, body)
	require.Equal(t, 201, first.Code, first.Body.String())
	replay := call("POST", path, agentToken, "n-convert-once", `"2"`, body)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	require.JSONEq(t, first.Body.String(), replay.Body.String())
	var receipt struct {
		Data struct {
			ID guid.ID `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &receipt))
	var stored struct {
		Status, ReviewState       string
		CurrentVersionNo          int
		SourceIntakeID, CreatedBy guid.ID
	}
	require.NoError(t, db.Table("delivery_requirements").Where("id=?", receipt.Data.ID).Take(&stored).Error)
	require.Equal(t, "DRAFT", stored.Status)
	require.Equal(t, "NEEDS_WORK", stored.ReviewState)
	require.Zero(t, stored.CurrentVersionNo)
	require.EqualValues(t, 701, stored.SourceIntakeID)
	require.Equal(t, account.ID, stored.CreatedBy)
	var resolution struct {
		ResolvedActorKind, ResolvedAuthoritySource, RawContent string
		RequirementID                                          guid.ID
	}
	require.NoError(t, db.Table("intake_records").Where("id=701").Take(&resolution).Error)
	require.Equal(t, "AGENT", resolution.ResolvedActorKind)
	require.Equal(t, "DELEGATION:901", resolution.ResolvedAuthoritySource)
	require.Equal(t, "Immutable source text", resolution.RawContent)
	require.Equal(t, receipt.Data.ID, resolution.RequirementID)
	t.Run("P-C11 actual Agent merge clarification closure and client receipt routing", func(t *testing.T) {
		insert("iam_principals", map[string]any{"id": 310, "login_name": "disposition-client", "display_name": "Client", "status": "active", "created_at": now, "updated_at": now})
		insert("iam_tenant_members", map[string]any{"tenant_id": 201, "principal_id": 310, "display_name": "Client", "email": "", "status": "active", "created_at": now, "updated_at": now})
		// Real persisted IAM permission plus external channel membership; no
		// provider doubles participate in any command below.
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			clientRole, e := roles.RoleIDOn(owner, tx, 201, "engineer")
			if e != nil {
				return e
			}
			_, _, e = binding.PutOn(owner, tx, 201, p, clientRole, 310, core.BindingAllow, now.Add(time.Hour))
			return e
		}))
		clientToken, _, e := web.IssueTenantAccessTokenWithAssurance(t.Context(), 310, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
		require.NoError(t, e)
		base := "/entities/" + p.String()
		createdChannel := call("POST", base+"/channels", humanToken, "t-shared-create", "", map[string]any{"name": "Client source threads", "visibility": "CLIENT_SHARED"})
		require.Equal(t, 201, createdChannel.Code, createdChannel.Body.String())
		var channel struct {
			Data struct {
				ID guid.ID `json:"id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(createdChannel.Body.Bytes(), &channel))
		channelPath := "/channels/" + channel.Data.ID.String()
		invited := call("PUT", channelPath+"/members/310", humanToken, "t-client-invite", "", map[string]any{"external": true})
		require.Equal(t, 200, invited.Code, invited.Body.String())
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			agentRole, e := roles.RoleIDOn(owner, tx, 201, "agent_reviewer")
			if e != nil {
				return e
			}
			_, _, e = binding.PutOn(owner, tx, 201, p, agentRole, account.ID, core.BindingAllow, now.Add(time.Hour))
			return e
		}))
		agentInvited := call("PUT", channelPath+"/members/"+account.ID.String(), humanToken, "t-agent-invite", "", map[string]any{"external": false})
		require.Equal(t, 200, agentInvited.Code, agentInvited.Body.String())
		insert("intake_contacts", map[string]any{"id": 810, "tenant_id": 201, "display_name": "Client", "principal_id": 310, "created_at": now, "updated_at": now})
		roots := map[int]guid.ID{}
		for _, id := range []int{710, 711, 712} {
			text := fmt.Sprintf("Immutable client source %d", id)
			rootResponse := call("POST", channelPath+"/messages", clientToken, fmt.Sprintf("t-root-%d", id), "", map[string]any{"body": text, "visibility": "CHANNEL"})
			require.Equal(t, 201, rootResponse.Code, rootResponse.Body.String())
			var message struct {
				Data struct {
					ID guid.ID `json:"id"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rootResponse.Body.Bytes(), &message))
			roots[id] = message.Data.ID
			digest := sha256.Sum256([]byte(text))
			insert("intake_records", map[string]any{"id": id, "ref_no": id, "channel_id": channel.Data.ID, "channel_kind": "SHARED_CHANNEL", "contact_id": 810, "title": "Client source", "raw_content": text, "raw_content_hash": hex.EncodeToString(digest[:]), "reply_token": fmt.Sprintf("t-src-%d", id), "project_id": p, "received_at": now, "tenant_id": 201, "created_at": now, "updated_at": now})
			linked := call("POST", fmt.Sprintf("%s/intakes/%d/conversation", base, id), humanToken, fmt.Sprintf("t-link-%d", id), `"1"`, map[string]any{"channel_id": channel.Data.ID, "root_message_id": message.Data.ID, "recipient_id": "310"})
			require.Equal(t, 201, linked.Code, linked.Body.String())
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := sources.TriageOn(owner, tx, 201, p, 710, 1, intake.TriageDraft{Summary: "Merge duplicate", RequirementDraft: "Keep target source", Criteria: []string{"Original content retained"}, Size: "S"})
			return e
		}))
		opened := call("POST", base+"/client-clarifications", humanToken, "t-open-round", "", map[string]any{"intake_id": "710", "intake_version": 2, "recipient_id": "310", "questions": []string{"Confirm duplicate?"}})
		require.Equal(t, 201, opened.Code, opened.Body.String())
		mergePath := base + "/intakes/710/delegated-merge"
		mergeBody := map[string]any{"action": "MERGE", "target_id": "711", "reason": "Independently assessed duplicate", "notify_client": true, "client_message": "Your request was merged after independent review"}
		missingCursor := call("POST", mergePath, agentToken, "t-agent-merge", `"3"`, mergeBody)
		require.Equal(t, 422, missingCursor.Code, missingCursor.Body.String())
		mergeBody["seen_seq"] = 0
		staleCursor := call("POST", mergePath, agentToken, "t-agent-merge", `"3"`, mergeBody)
		require.Equal(t, 409, staleCursor.Code, staleCursor.Body.String())
		require.Contains(t, staleCursor.Body.String(), `"state":"interrupted"`)
		var current struct{ LastSeq int64 }
		require.NoError(t, db.Table("collab_channels").Where("id=?", channel.Data.ID).Take(&current).Error)
		var unchanged struct {
			Status                      string
			OpenRoundID                 *guid.ID
			ClientNotificationMessageID *guid.ID
		}
		require.NoError(t, db.Table("intake_records").Where("id=710").Take(&unchanged).Error)
		require.Equal(t, "CLARIFYING", unchanged.Status)
		require.NotNil(t, unchanged.OpenRoundID)
		require.Nil(t, unchanged.ClientNotificationMessageID)
		mergeBody["seen_seq"] = current.LastSeq
		merged := call("POST", mergePath, agentToken, "t-agent-merge", `"3"`, mergeBody)
		require.Equal(t, 200, merged.Code, merged.Body.String())
		mergeBody["seen_seq"] = current.LastSeq + 1
		replayed := call("POST", mergePath, agentToken, "t-agent-merge", `"3"`, mergeBody)
		require.Equal(t, 200, replayed.Code, replayed.Body.String())
		require.Equal(t, "true", replayed.Header().Get("Idempotent-Replayed"))
		require.NoError(t, db.Table("governance_delegations").Where("id=901").Updates(map[string]any{"state": "REVOKED", "active_key": nil, "ended_at": now, "end_reason": "REVOKED"}).Error)
		require.Equal(t, 403, call("POST", mergePath, agentToken, "t-agent-merge", `"3"`, mergeBody).Code, "current revoked delegation rejects Agent merge replay")
		require.NoError(t, db.Table("governance_delegations").Where("id=901").Updates(map[string]any{"state": "ACTIVE", "active_key": "n-delegated-active", "ended_at": nil, "end_reason": nil}).Error)
		var mergedRow struct {
			Status, ResolvedActorKind, ResolvedAuthoritySource, RawContent, ClientNotificationState string
			OpenRoundID                                                                             *guid.ID
		}
		require.NoError(t, db.Table("intake_records").Where("id=710").Take(&mergedRow).Error)
		require.Equal(t, "MERGED", mergedRow.Status)
		require.Equal(t, "AGENT", mergedRow.ResolvedActorKind)
		require.Equal(t, "DELEGATION:901", mergedRow.ResolvedAuthoritySource)
		require.Equal(t, "Immutable client source 710", mergedRow.RawContent)
		require.Equal(t, "POSTED", mergedRow.ClientNotificationState)
		require.Nil(t, mergedRow.OpenRoundID)
		var roundState string
		require.NoError(t, db.Table("clientwork_clarification_rounds").Select("state").Where("intake_id=710").Scan(&roundState).Error)
		require.Equal(t, "CANCELLED", roundState) //nolint:misspell // Preserve the published domain or serialized contract spelling.
		require.Equal(t, 403, call("POST", base+"/intakes/712/decline", agentToken, "t-agent-no-decline", `"1"`, map[string]any{"action": "DECLINE", "reason_code": "OTHER", "reason": "Forbidden"}).Code)
		notified := call("POST", base+"/intakes/712/decline", humanToken, "t-human-notice", `"1"`, map[string]any{"action": "DECLINE", "reason_code": "OTHER", "reason": "Closed with response", "notify_client": true, "client_message": "Actual shared response"})
		require.Equal(t, 200, notified.Code, notified.Body.String())
		receiptPath := base + "/intakes/712/disposition-receipt"
		receipt := call("GET", receiptPath, humanToken, "", "", nil)
		require.Equal(t, 200, receipt.Code, receipt.Body.String())
		require.Contains(t, receipt.Body.String(), `"state":"POSTED"`)
		var notice struct{ ClientNotificationMessageID guid.ID }
		require.NoError(t, db.Table("intake_records").Where("id=712").Take(&notice).Error)
		require.Positive(t, notice.ClientNotificationMessageID)
		var posted struct {
			Seq              int64
			Body             string
			ReplyToMessageID guid.ID
		}
		require.NoError(t, db.Table("collab_messages").Where("id=?", notice.ClientNotificationMessageID).Take(&posted).Error)
		require.Equal(t, "Actual shared response", posted.Body)
		require.Equal(t, roots[712], posted.ReplyToMessageID)
		read := call("PUT", channelPath+"/read-marker", clientToken, "t-read-notice", "", map[string]any{"seq": posted.Seq})
		require.Equal(t, 200, read.Code, read.Body.String())
		receipt = call("GET", receiptPath, humanToken, "", "", nil)
		require.Equal(t, 200, receipt.Code, receipt.Body.String())
		require.Contains(t, receipt.Body.String(), `"state":"READ"`)
		publisher, e := module.Resolve[events.Publisher](app.Modules().Services())
		require.NoError(t, e)
		dispatcher := publisher.(*events.Module)
		require.NoError(t, dispatcher.Quiesce(t.Context()))
		flush := func() {
			t.Helper()
			for {
				n, e := dispatcher.Sequence(t.Context())
				require.NoError(t, e)
				if n == 0 {
					break
				}
			}
			_, e := dispatcher.DispatchPartition(t.Context(), "clientwork.merged_source_replies", 0)
			require.NoError(t, e)
		}
		flush()
		lateText := "Later original-thread reply remains intact"
		late := call("POST", channelPath+"/messages", clientToken, "t-late-client", "", map[string]any{"body": lateText, "reply_to_message_id": roots[710], "visibility": "CHANNEL"})
		require.Equal(t, 201, late.Code, late.Body.String())
		flush()
		flush()
		var routed []struct {
			Body, Visibility string
			AuthorID         guid.ID
			ReplyToMessageID guid.ID
		}
		require.NoError(t, db.Table("collab_messages").Where("channel_id=? AND reply_to_message_id=?", channel.Data.ID, roots[711]).Find(&routed).Error)
		require.Len(t, routed, 1)
		require.Equal(t, "INTERNAL", routed[0].Visibility)
		require.Contains(t, routed[0].Body, "[From merged IN-710;")
		require.Contains(t, routed[0].Body, lateText)
		require.NotEqualValues(t, 310, routed[0].AuthorID)
		var original string
		require.NoError(t, db.Table("collab_messages").Select("body").Where("id=?", roots[710]).Scan(&original).Error)
		require.Equal(t, "Immutable client source 710", original)
		require.NoError(t, db.Table("collab_channel_members").Where("channel_id=? AND principal_id=310", channel.Data.ID).Update("left_at", now).Error)
		require.Equal(t, 404, call("GET", receiptPath, humanToken, "", "", nil).Code)
	})
	// The original bearer token and replay key never freeze IAM or domain grant
	// authority. Expire each independently while preserving the other record.
	require.NoError(t, db.Table("iam_role_bindings").Where("tenant_id=201 AND scope_id=? AND principal_id=? AND role_id=?", p, account.ID, role).Update("expires_at", now.Add(-time.Second)).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return bootstrap.BumpRevision(t.Context(), tx, 201, time.Now().UTC()) }))
	require.Equal(t, 403, call("POST", path, agentToken, "n-convert-once", `"2"`, body).Code, "expired current IAM carrier must refuse replay")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := binding.PutOn(owner, tx, 201, p, role, account.ID, core.BindingAllow, now.Add(time.Hour))
		return e
	}))
	require.Equal(t, 201, call("POST", path, agentToken, "n-convert-once", `"2"`, body).Code)
	require.NoError(t, db.Table("governance_delegations").Where("id=901").Updates(map[string]any{"starts_at": now.Add(-2 * time.Minute), "ends_at": now.Add(-time.Minute)}).Error)
	require.Equal(t, 403, call("POST", path, agentToken, "n-convert-once", `"2"`, body).Code, "expired current domain grant must refuse replay despite a valid IAM carrier")
	require.NoError(t, db.Table("governance_delegations").Where("id=901").Update("ends_at", now.Add(time.Hour)).Error)
	require.NoError(t, db.Table("governance_delegations").Where("id=901").Updates(map[string]any{"state": "REVOKED", "active_key": nil, "ended_at": now, "end_reason": "REVOKED"}).Error)
	require.Equal(t, 403, call("POST", path, agentToken, "n-convert-once", `"2"`, body).Code, "domain revocation must refuse replay even if carrier is still present")
	require.Equal(t, 403, call("POST", "/entities/"+p.String()+"/requirements", agentToken, "n-general-write", "", map[string]any{"title": "Forbidden general write"}).Code)
	require.Equal(t, 403, call("POST", "/tenants/201/projects", agentToken, "n-agent-project", "", map[string]any{"name": "Forbidden Agent project", "code": "NO-N"}).Code)
	var total int64
	require.NoError(t, db.Table("delivery_requirements").Where("project_id=?", p).Count(&total).Error)
	require.EqualValues(t, 1, total)

	t.Run("submission projection uses actual owner and current project authority", func(t *testing.T) {
		path := "/tenants/201/intake-submissions/701"
		require.Equal(t, 404, call("GET", path, humanToken, "", "", nil).Code)
		require.NoError(t, db.Table("intake_records").Where("id=701").Updates(map[string]any{"submitted_by_principal_id": 301, "client_message": "Public submission response"}).Error)
		view := call("GET", path, humanToken, "", "", nil)
		require.Equal(t, 200, view.Code, view.Body.String())
		require.Contains(t, view.Body.String(), "Public submission response")
		require.NotContains(t, view.Body.String(), "Immutable source text")
		require.NotContains(t, view.Body.String(), "proposal_hash")
		require.NotContains(t, view.Body.String(), "raw_content")
		page := call("GET", "/tenants/201/intake-submissions?project_id="+p.String(), humanToken, "", "", nil)
		require.Equal(t, 200, page.Code, page.Body.String())
		require.Contains(t, page.Body.String(), `"id":"701"`)
		require.NoError(t, db.Table("intake_records").Where("id=701").Update("submitted_by_principal_id", nil).Error)
		require.Equal(t, 404, call("GET", path, humanToken, "", "", nil).Code)
	})

	t.Run("requested triage uses current Human Agent authority and real inbox wake", func(t *testing.T) {
		insert("intake_records", map[string]any{"id": 702, "ref_no": 2, "channel_id": 703, "channel_kind": "API", "contact_id": 801, "title": "Retriage request", "raw_content": "Unchanged request", "raw_content_hash": strings.Repeat("b", 64), "reply_token": "n-triage-request", "project_id": p, "received_at": now, "tenant_id": 201, "created_at": now, "updated_at": now})
		document := `{"enabled":true,"triage_agent_id":"601","display_name":"API receiving","language":"en-US","daily_limit":20,"reminder_hours":48,"timeout_hours":168}`
		insert("intake_channel_settings", map[string]any{"id": 703, "tenant_id": 201, "project_id": p, "kind": "API", "document": document, "version": 1, "updated_by": 301, "updated_at": now})
		publisher, e := module.Resolve[events.Publisher](app.Modules().Services())
		require.NoError(t, e)
		dispatcher, ok := publisher.(*events.Module)
		require.True(t, ok)
		require.NoError(t, dispatcher.Quiesce(t.Context()))
		for {
			n, e := dispatcher.Sequence(t.Context())
			require.NoError(t, e)
			if n == 0 {
				break
			}
		}
		_, e = dispatcher.DispatchPartition(t.Context(), "clientwork.intake_triage_wake", 0)
		require.NoError(t, e)
		requestPath := "/entities/" + p.String() + "/intakes/702/triage-requests"
		requestBody := map[string]any{"note": "Please reconsider the latest information"}
		first := call("POST", requestPath, humanToken, "n-request-triage-human", "", requestBody)
		require.Equal(t, 201, first.Code, first.Body.String())
		for {
			n, e := dispatcher.Sequence(t.Context())
			require.NoError(t, e)
			if n == 0 {
				break
			}
		}
		_, e = dispatcher.DispatchPartition(t.Context(), "clientwork.intake_triage_wake", 0)
		require.NoError(t, e)
		var parked []struct{ LastError string }
		require.NoError(t, db.Table("domain_event_parked").Select("last_error").Where("consumer=? AND subject_id=702", "clientwork.intake_triage_wake").Find(&parked).Error)
		require.Empty(t, parked, "triage subscriber failed: %v", parked)
		var inboxCount, wakeCount int64
		require.NoError(t, db.Table("agents_inbox_events").Where("tenant_id=201 AND agent_principal_id=? AND subject_id=702", account.ID).Count(&inboxCount).Error)
		require.EqualValues(t, 1, inboxCount)
		require.NoError(t, db.Table("domain_events").Where("tenant_id=201 AND event_type=?", "agents.wake").Count(&wakeCount).Error)
		require.Positive(t, wakeCount)
		require.Equal(t, 403, call("POST", requestPath, agentToken, "n-request-triage-agent", "", requestBody).Code)
		var coordinatorRole guid.ID
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			coordinatorRole, e = roles.RoleIDOn(owner, tx, 201, "agent_coordinator")
			if e != nil {
				return e
			}
			_, _, e = binding.PutOn(owner, tx, 201, p, coordinatorRole, account.ID, core.BindingAllow, now.Add(time.Hour))
			return e
		}))
		combined := call("POST", requestPath, agentToken, "n-request-triage-agent", "", requestBody)
		require.Equal(t, 202, combined.Code, combined.Body.String())
		require.Contains(t, combined.Body.String(), `"coalesced":true`)
		require.NoError(t, db.Table("intake_records").Where("id=702").Update("last_triage_requested_at", now.Add(-11*time.Minute)).Error)
		agentFirst := call("POST", requestPath, agentToken, "n-request-triage-next", "", requestBody)
		require.Equal(t, 201, agentFirst.Code, agentFirst.Body.String())
		var eventActor struct {
			ActorKind string
			ActorID   guid.ID
		}
		require.NoError(t, db.Table("domain_events").Where("tenant_id=201 AND event_type=? AND subject_id=702", "intake.triage_requested").Order("id DESC").Take(&eventActor).Error)
		require.Equal(t, "AGENT", eventActor.ActorKind)
		require.Equal(t, account.ID, eventActor.ActorID)
		replay := call("POST", requestPath, agentToken, "n-request-triage-next", "", requestBody)
		require.Equal(t, 201, replay.Code, replay.Body.String())
		require.JSONEq(t, agentFirst.Body.String(), replay.Body.String())
		require.NoError(t, db.Table("iam_role_bindings").Where("tenant_id=201 AND scope_id=? AND principal_id=? AND role_id=?", p, account.ID, coordinatorRole).Update("expires_at", now.Add(-time.Second)).Error)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return bootstrap.BumpRevision(t.Context(), tx, 201, time.Now().UTC()) }))
		require.Equal(t, 403, call("POST", requestPath, agentToken, "n-request-triage-next", "", requestBody).Code)
		require.NoError(t, db.Table("intake_channel_settings").Where("id=703").Update("document", strings.Replace(document, `"enabled":true`, `"enabled":false`, 1)).Error)
		denied := call("POST", requestPath, humanToken, "n-request-triage-human", "", requestBody)
		require.Equal(t, 422, denied.Code, denied.Body.String())
	})
}

func TestBusinessAuditComposedCurrentIAMAndExternalExportNegatives(t *testing.T) {
	a := bootstrapComposedApp(t)
	db := a.Runtime().Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table("iam_tenants").Create(map[string]any{"id": 201, "slug": "business-audit", "name": "Audit test", "status": "active", "version": 1, "created_at": now, "updated_at": now}).Error)
	for _, id := range []int{301, 302} {
		require.NoError(t, db.Table("iam_principals").Create(map[string]any{"id": id, "login_name": fmt.Sprintf("audit-%d", id), "display_name": "Audit actor", "status": "active", "created_at": now, "updated_at": now}).Error)
	}
	bootstrap, err := module.Resolve[core.TenantBootstrapper](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Owner"}, now)
		return err
	}))
	require.NoError(t, db.Table("iam_tenant_members").Create(map[string]any{"tenant_id": 201, "principal_id": 302, "status": "active", "created_at": now, "updated_at": now}).Error)
	require.NoError(t, db.Table("intake_contacts").Create(map[string]any{"id": 401, "tenant_id": 201, "kind": "PERSON", "display_name": "External contact", "principal_id": 302, "state": "ACTIVE", "version": 1, "created_at": now, "updated_at": now}).Error)
	owner := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 201, PrincipalID: 301, SubjectType: authn.SubjectTypePrincipal})
	roles, err := module.Resolve[businessgovernance.SeededRoles](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, roles.EnsureTenant(owner, 201))
	appendEvent, err := module.Resolve[iamaudit.Appender](a.Modules().Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return appendEvent(owner, tx, iamaudit.DomainEvent{TenantID: 201, PrincipalID: 301, EventType: "governance.audit_probe", TargetType: "probe", TargetID: 501, Detail: map[string]any{"actor_kind": "HUMAN", "authority_source": "ROLE:team_owner", "private": "PRIVATE-AUDIT-DETAIL"}})
	}))
	var eventRow struct{ ID guid.ID }
	require.NoError(t, db.Table("iam_audit_events").Select("id").Where("tenant_id=? AND event_type=?", 201, "governance.audit_probe").Take(&eventRow).Error)
	eventID := eventRow.ID
	web, err := module.Resolve[iamauthn.WebService](a.Modules().Services())
	require.NoError(t, err)
	token := func(id guid.ID) string {
		value, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), id, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
		require.NoError(t, err)
		return value
	}
	ownerToken, clientToken := token(301), token(302)
	router := moduleRouter(t, a)
	call := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	response := call("/tenants/201/business-audit-events?event_type=governance.audit_probe", ownerToken)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), eventID.String())
	require.Contains(t, response.Body.String(), `"index_lag_seconds":0`)
	require.NotContains(t, response.Body.String(), "PRIVATE-AUDIT-DETAIL")
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	for _, path := range []string{
		"/tenants/201/business-audit-events",
		"/tenants/201/business-audit-events?project_id=999&offset=100",
		"/tenants/201/audit/events",
		"/tenants/201/audit/events/" + eventID.String(),
		"/tenants/201/audit/integrity-verifications",
		"/tenants/201/audit/exports",
		"/tenants/201/audit/exports?principal_id=302&module=governance",
	} {
		denied := call(path, clientToken)
		require.Equal(t, 403, denied.Code, path+": "+denied.Body.String())
		require.NotContains(t, denied.Body.String(), "PRIVATE-AUDIT-DETAIL")
	}
	require.NoError(t, db.Table("iam_tenant_members").Where("tenant_id=? AND principal_id=?", 201, 301).Update("status", "disabled").Error)
	require.Equal(t, 403, call("/tenants/201/business-audit-events", ownerToken).Code)
}

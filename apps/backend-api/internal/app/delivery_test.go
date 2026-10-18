package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	businessgovernance "7link.com/platform/internal/governance"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
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
	require.NoError(t, wireProjectProvisioning(services))
	services = module.NewServices()
	require.NoError(t, module.Provide[delivery.ProjectProvisioningConnector](services, delivery.ProjectProvisioningConnector{}))
	require.NoError(t, services.Seal())
	require.ErrorIs(t, wireProjectProvisioning(services), delivery.ErrUnavailable)
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
	require.NoError(t, wireProjectProvisioning(services))
	require.True(t, called)
}
func TestDeliveryComposesRealProvidersAndReservesProjectType(t *testing.T) {
	app := bootstrapComposedApp(t)
	reservations, err := module.Resolve[core.EntityTypeReservations](app.modules.Services())
	require.NoError(t, err)
	require.Equal(t, []string{"project"}, reservations())
	current, ok := app.modules.ByName(delivery.Name)
	require.True(t, ok)
	require.IsType(t, &delivery.Module{}, current)
	connector, err := module.Resolve[delivery.ProjectProvisioningConnector](app.modules.Services())
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(core.EntityProvisioner{}, core.EntityRoleBindingStore{}, collab.ChannelProvisioner{}), delivery.ErrInvalidArgument, "production wiring must already be bound and sealed")
	require.True(t, app.runtime.Database().Write().Migrator().HasTable("delivery_projects"))
	reader, err := module.Resolve[intake.IntakeReader](app.modules.Services())
	require.NoError(t, err)
	directory, err := module.Resolve[fleet.AgentDirectory](app.modules.Services())
	require.NoError(t, err)
	require.NoError(t, app.runtime.Database().Write().Transaction(func(tx *gorm.DB) error {
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
	db := app.runtime.Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Exec("INSERT INTO iam_tenants (id,slug,name,status,version,created_at,updated_at) VALUES (201,'authority-project','Project authority','active',1,?,?)", now, now).Error)
	for _, id := range []int{301, 302} {
		require.NoError(t, db.Exec("INSERT INTO iam_principals (id,login_name,display_name,status,created_at,updated_at) VALUES (?,?,?,'active',?,?)", id, fmt.Sprintf("project-authority-%d", id), "Authority test", now, now).Error)
	}
	bootstrap, err := module.Resolve[core.TenantBootstrapper](app.modules.Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Owner"}, now)
		if err != nil {
			return err
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, now)
	}))
	require.NoError(t, db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status,created_at,updated_at) VALUES (201,302,'active',?,?)", now, now).Error)
	roles, err := module.Resolve[businessgovernance.SeededRoles](app.modules.Services())
	require.NoError(t, err)
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 201, PrincipalID: 301})
	require.NoError(t, roles.EnsureTenant(actor, 201))
	checker, err := module.Resolve[businessgovernance.ProjectCreationAuthority](app.modules.Services())
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
	evaluator, err := module.Resolve[core.AuthorizationEvaluator](app.modules.Services())
	require.NoError(t, err)
	members, err := module.Resolve[core.ActiveMemberChecker](app.modules.Services())
	require.NoError(t, err)
	sharedRegistrar, err := module.Resolve[*authz.Registrar](app.modules.Services())
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
	db := app.runtime.Database().Write()
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Exec("INSERT INTO iam_tenants (id,slug,name,status,version,created_at,updated_at) VALUES (201,'project-http','Project HTTP','active',1,?,?)", now, now).Error)
	require.NoError(t, db.Exec("INSERT INTO iam_principals (id,login_name,display_name,status,created_at,updated_at) VALUES (301,'project-http-owner','Owner','active',?,?)", now, now).Error)
	bootstrap, err := module.Resolve[core.TenantBootstrapper](app.modules.Services())
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := bootstrap.Bootstrap(t.Context(), tx, core.TenantBootstrapInput{TenantID: 201, OwnerID: 301, DisplayName: "Owner"}, now)
		if err != nil {
			return err
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, now)
	}))
	roles, err := module.Resolve[businessgovernance.SeededRoles](app.modules.Services())
	require.NoError(t, err)
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 201, PrincipalID: 301})
	require.NoError(t, roles.EnsureTenant(actor, 201))
	web, err := module.Resolve[iamauthn.WebService](app.modules.Services())
	require.NoError(t, err)
	token, _, err := web.IssueTenantAccessTokenWithAssurance(t.Context(), 301, "201", "", "", authn.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	router, _ := moduleRouter(t, app)
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

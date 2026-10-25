package composition_test

import (
	platformapp "7link.com/platform/internal/app"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This is an authorization integration test, not a hidden-case verdict test.
// A valid dedicated verifier reaches the real result lookup (deliberately
// absent); invalid identities or revoked authority must stop before that read.
func TestHiddenVerifierComposedServiceIdentityAndCurrentGrant(t *testing.T) {
	f := newApprovalFixture(t)
	db := f.app.Runtime().Database().Write()
	actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 201, PrincipalID: 301})
	iam, err := module.Resolve[fleet.EnrollmentIAM](f.app.Modules().Services())
	require.NoError(t, err)
	verifier, err := iam.CreateAccount(actor, 201, "hidden-verifier", "Dedicated acceptance verifier", "Authorization integration fixture", nil)
	require.NoError(t, err)
	var project struct{ ProjectID guid.ID }
	require.NoError(t, db.Table("delivery_work_items").Select("project_id").Where("tenant_id=201 AND id=501").Take(&project).Error)
	require.Positive(t, project.ProjectID)
	ids, err := module.Resolve[guid.Source](f.app.Modules().Services())
	require.NoError(t, err)
	next, err := ids.Next()
	require.NoError(t, err)
	role := guid.ID(next)
	// Reuse the migrated fixture database and configuration. Stop its workers
	// before opening an independently configured composition; its registered
	// cleanup still owns the original runtime and temporary database.
	require.NoError(t, f.app.Modules().Quiesce(t.Context()))
	require.NoError(t, f.app.Modules().Stop(t.Context()))

	configured := hiddenComposition(t, f.app.Config(), verifier.ID)
	svc, err := module.Resolve[identity.ServiceAccountValidator](configured.Modules().Services())
	require.NoError(t, err)
	permission, err := module.Resolve[core.EntityConstraintOn](configured.Modules().Services())
	require.NoError(t, err)
	principals, err := module.Resolve[identity.PrincipalStateStore](configured.Modules().Services())
	require.NoError(t, err)
	work, err := module.Resolve[delivery.ExecutionWork](configured.Modules().Services())
	require.NoError(t, err)
	db = configured.Runtime().Database().Write()

	run := hiddenHandler(t, configured)
	payload, err := json.Marshal(delivery.HiddenAcceptanceRequest{ProjectID: project.ProjectID, WorkItemID: 501, ResultID: 999999})
	require.NoError(t, err)
	job := jobs.Job{TenantID: 201, Kind: "clientwork.hidden_verification", Payload: payload}
	// The job carries identifiers only. Its configured service identity cannot
	// be overridden by a Human or executor principal in the caller's context.
	require.ErrorIs(t, run.Run(actor, job), clientwork.ErrForbidden)
	now := time.Now().UTC().Truncate(time.Millisecond)
	bootstrap, err := module.Resolve[core.TenantBootstrapper](configured.Modules().Services())
	require.NoError(t, err)
	grant := func(tx *gorm.DB) error {
		if e := tx.Table("iam_roles").Create(map[string]any{"tenant_id": 201, "id": role, "name": "Hidden verifier", "name_key": "hidden verifier", "description": "Dedicated project acceptance verifier", "created_at": now, "updated_at": now}).Error; e != nil {
			return e
		}
		if e := tx.Table("iam_role_permissions").Create(map[string]any{"tenant_id": 201, "role_id": role, "permission_code": "acceptance_suite_verify", "created_at": now}).Error; e != nil {
			return e
		}
		if e := tx.Table("iam_role_bindings").Create(map[string]any{"tenant_id": 201, "role_id": role, "principal_id": verifier.ID, "scope_type": "entity", "scope_id": project.ProjectID, "effect": "allow", "created_at": now}).Error; e != nil {
			return e
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, now)
	}
	require.NoError(t, db.Transaction(grant))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		active, e := svc(t.Context(), tx, 201, verifier.ID, time.Now().UTC())
		require.NoError(t, e)
		require.True(t, active, "the account must be a current real IAM service identity")
		_, human, e := principals.ActiveHumanByID(t.Context(), tx, verifier.ID)
		require.NoError(t, e)
		require.False(t, human)
		constraint, e := permission(t.Context(), tx, 201, project.ProjectID, "acceptance_suite_verify", verifier.ID)
		require.NoError(t, e)
		require.True(t, constraint.AllowsEntityRows(project.ProjectID))
		require.Contains(t, constraint.ResourceIDs, project.ProjectID.String())
		// The same identity cannot act as the Human execution initiator, even
		// though it now has the independent verifier permission.
		e = work.AuthorizeOn(actor, tx, delivery.ExecutionWorkLink{TenantID: 201, ProjectID: project.ProjectID, WorkItemID: 501, AssignmentID: 502, ExecutionID: 601, ActorID: 301, InitiatorID: verifier.ID}, "RECEIPT")
		require.ErrorIs(t, e, delivery.ErrWorkForbidden)
		return nil
	}))
	// Authorization succeeds; the deliberately missing immutable result is the
	// next real boundary. This is never counted as a PASSED hidden verdict.
	require.ErrorIs(t, run.Run(actor, job), gorm.ErrRecordNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Exec("DELETE FROM iam_role_permissions WHERE tenant_id=201 AND role_id=? AND permission_code='acceptance_suite_verify'", role).Error; e != nil {
			return e
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, time.Now().UTC())
	}))
	require.ErrorIs(t, run.Run(actor, job), clientwork.ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		active, e := svc(t.Context(), tx, 201, verifier.ID, time.Now().UTC())
		require.NoError(t, e)
		require.True(t, active, "grant revocation must not be confused with account disablement")
		constraint, e := permission(t.Context(), tx, 201, project.ProjectID, "acceptance_suite_verify", verifier.ID)
		require.NoError(t, e)
		require.False(t, constraint.AllowsEntityRows(project.ProjectID))
		return nil
	}))
	// Restore just the grant and disable the account to exercise the other
	// canonical IAM dependency independently of permission revocation.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Table("iam_role_permissions").Create(map[string]any{"tenant_id": 201, "role_id": role, "permission_code": "acceptance_suite_verify", "created_at": now}).Error; e != nil {
			return e
		}
		if e := tx.Table("iam_service_accounts").Where("principal_id=?", verifier.ID).Update("status", "disabled").Error; e != nil {
			return e
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, time.Now().UTC())
	}))
	require.ErrorIs(t, run.Run(actor, job), clientwork.ErrForbidden)
	var receipts int64
	require.NoError(t, db.Table("clientwork_hidden_verification_receipts").Count(&receipts).Error)
	require.Zero(t, receipts, "authorization-only probes must never fabricate verifier receipts")

	// Configuring the ordinary Human executor as the verifier is rejected by
	// the actual job even if it has the verifier permission. Permission alone
	// cannot turn a Human executor into the configured service identity.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Table("iam_role_bindings").Create(map[string]any{"tenant_id": 201, "role_id": role, "principal_id": 302, "scope_type": "entity", "scope_id": project.ProjectID, "effect": "allow", "created_at": now}).Error; e != nil {
			return e
		}
		return bootstrap.BumpRevision(t.Context(), tx, 201, time.Now().UTC())
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		constraint, e := permission(t.Context(), tx, 201, project.ProjectID, "acceptance_suite_verify", 302)
		require.NoError(t, e)
		require.True(t, constraint.AllowsEntityRows(project.ProjectID))
		active, e := svc(t.Context(), tx, 201, 302, time.Now().UTC())
		require.NoError(t, e)
		require.False(t, active)
		return nil
	}))
	humanConfigured := hiddenComposition(t, f.app.Config(), 302)
	require.ErrorIs(t, hiddenHandler(t, humanConfigured).Run(actor, job), clientwork.ErrForbidden)
}

func hiddenComposition(t *testing.T, config platformapp.AppConfig, principal guid.ID) *platformapp.App {
	t.Helper()
	config.HiddenVerifier.PrincipalID = int64(principal)
	app := platformapp.New(config)
	require.NoError(t, app.PrepareComposition(t.Context()))
	t.Cleanup(func() { require.NoError(t, app.Shutdown()) })
	// Existing fixture migrations already created the database. Initialize the
	// real providers but do not Start background jobs or network listeners.
	require.NoError(t, app.Modules().Initialize(t.Context()))
	return app
}

func hiddenHandler(t *testing.T, app *platformapp.App) jobs.Handler {
	t.Helper()
	m, ok := app.Modules().ByName(clientwork.Name)
	require.True(t, ok)
	for _, handler := range m.(jobs.HandlerProvider).JobHandlers() {
		if handler.Kind == "clientwork.hidden_verification" {
			return handler
		}
	}
	t.Fatal("production hidden verifier handler is not registered")
	return jobs.Handler{}
}

package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRequirementReviewUsesCurrentProductionAuthorityPath(t *testing.T) {
	for _, verb := range []string{"approve", "request_changes"} {
		t.Run(verb, func(t *testing.T) {
			seed, db, rt := seedHarnessRuntime(t)
			require.NoError(t, seed.registry.Contribute([]authz.Action{{Resource: "decision", Verb: verb, Scope: authz.ActionScopeTenant}}, nil))
			q := query.BindRoleSeeds(db, seed.names)
			require.NoError(t, q.WithContext(t.Context()).Create(&model.RoleSeed{ID: 1, TenantID: 1, RoleID: 21, RoleKey: "delivery_lead", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: seed.now(), UpdatedAt: seed.now()}))
			allowed := true
			code := "decision_" + verb
			owner := &Module{runtime: rt, seed: seed, explain: func(_ context.Context, tx *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				require.NotNil(t, tx)
				require.Equal(t, code, req.Permission)
				return authz.Explanation{Allowed: allowed, Revision: 9, Matches: []authz.DecisionMatch{{RoleID: "21", PermissionCode: code, Effect: "allow"}}}, nil
			}}
			req := AuthorityRequest{TenantID: 1, ProjectID: 99, PrincipalID: 11, ActorKind: "HUMAN", Code: code, Resource: ResourceRef{Kind: "requirement", ID: 77}}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				decision, err := owner.Check(t.Context(), tx, req)
				require.NoError(t, err)
				require.True(t, decision.Allowed)
				require.Equal(t, "ROLE:delivery_lead", decision.AuthoritySource)
				return nil
			}))
			allowed = false
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				decision, err := owner.Check(t.Context(), tx, req)
				require.NoError(t, err)
				require.False(t, decision.Allowed)
				require.Equal(t, "NO_ROLE_GRANT", decision.ReasonCode)
				return nil
			}))
			req.ActorKind = "MACHINE"
			decision, err := owner.Check(t.Context(), nil, req)
			require.NoError(t, err)
			require.False(t, decision.Allowed)
		})
	}
}

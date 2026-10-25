package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeliveryAuthorityUsesCurrentDirectHumanGrant(t *testing.T) {
	for _, code := range []string{"scope_change", "decision_approve", "decision_request_changes", "decision_pause", "delivery_send"} {
		t.Run(code, func(t *testing.T) {
			seed, db, rt := seedHarnessRuntime(t)
			resource, verb := "decision", "approve"
			kind := "work_item"
			if code == "scope_change" {
				resource, verb, kind = "scope", "change", "requirement"
			}
			if code == "decision_request_changes" {
				verb = "request_changes"
			}
			if code == "decision_pause" {
				verb = "pause"
			}
			if code == "delivery_send" {
				resource, verb, kind = "delivery", "send", "delivery"
			}
			require.NoError(t, seed.registry.Contribute([]authz.Action{{Resource: resource, Verb: verb, Scope: authz.ActionScopeTenant}}, nil))
			q := query.BindRoleSeeds(db, seed.names)
			require.NoError(t, q.WithContext(t.Context()).Create(&model.RoleSeed{ID: 1, TenantID: 1, RoleID: 21, RoleKey: "delivery_lead", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: seed.now(), UpdatedAt: seed.now()}))
			allowed := true
			owner := &Module{runtime: rt, seed: seed, explain: func(_ context.Context, actual *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				require.NotNil(t, actual)
				require.Equal(t, code, req.Permission)
				require.EqualValues(t, 99, req.EntityID)
				return authz.Explanation{Allowed: allowed, Revision: 9, Matches: []authz.DecisionMatch{{RoleID: "21", PermissionCode: code, Effect: "allow"}}}, nil
			}}
			req := AuthorityRequest{TenantID: 1, ProjectID: 99, PrincipalID: 11, ActorKind: "HUMAN", Code: code, Resource: ResourceRef{Kind: kind, ID: 77}}
			if kind == "work_item" {
				req.Facts = &WorkItemFacts{}
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				result, err := owner.deliveryAuthority(t.Context(), tx, req)
				require.NoError(t, err)
				require.True(t, result.Allowed)
				require.Equal(t, "ROLE:delivery_lead", result.AuthoritySource)
				return nil
			}))
			allowed = false
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				result, err := owner.Check(t.Context(), tx, req)
				require.NoError(t, err)
				require.False(t, result.Allowed)
				require.Equal(t, "NO_ROLE_GRANT", result.ReasonCode)
				return nil
			}))
			allowed = true
			_, err := q.WithContext(t.Context()).Where(q.TenantID.Eq(guid.ID(1)), q.RoleID.Eq(guid.ID(21))).UpdateSimple(q.RoleKey.Value("delegated_decision_approve"))
			require.NoError(t, err)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				result, err := owner.Check(t.Context(), tx, req)
				require.NoError(t, err)
				require.False(t, result.Allowed)
				require.Equal(t, "NO_DIRECT_AUTHORITY", result.ReasonCode)
				return nil
			}))
			for _, actorKind := range []string{"AGENT", "MACHINE"} {
				req.ActorKind = actorKind
				result, err := owner.Check(t.Context(), nil, req)
				if actorKind == "AGENT" && RulesForAction(code, actorKind).Delegable {
					require.ErrorIs(t, err, ErrUnavailable)
					continue
				}
				require.NoError(t, err)
				require.False(t, result.Allowed)
				require.Equal(t, "ACTOR_NOT_ELIGIBLE", result.ReasonCode)
			}
			req.ActorKind = "HUMAN"
			if code == "decision_approve" || code == "decision_request_changes" {
				req.Facts.SelfProduced = true
				result, err := owner.Check(t.Context(), nil, req)
				require.NoError(t, err)
				require.Equal(t, "SELF_REVIEW", result.ReasonCode)
				req.Facts = nil
				result, err = owner.Check(t.Context(), nil, req)
				require.NoError(t, err)
				require.Equal(t, "INVALID_REQUEST", result.ReasonCode)
			}
			req.Resource.Kind = "project"
			result, err := owner.Check(t.Context(), nil, req)
			require.NoError(t, err)
			require.Equal(t, "INVALID_REQUEST", result.ReasonCode)
		})
	}
}

func TestProjectLeadRequiresCurrentSeededRoleAndReadableProject(t *testing.T) {
	seed, db, rt := seedHarnessRuntime(t)
	q := query.BindRoleSeeds(db, seed.names)
	require.NoError(t, q.WithContext(t.Context()).Create(&model.RoleSeed{ID: 1, TenantID: 1, RoleID: 21, RoleKey: "delivery_lead", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: seed.now(), UpdatedAt: seed.now()}))
	for _, tc := range []struct {
		name, role, effect string
		allowed, want      bool
	}{
		{"current lead", "21", "allow", true, true},
		{"other role", "22", "allow", true, false},
		{"revoked read", "21", "allow", false, false},
		{"deny match", "21", "deny", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Module{seed: seed, runtime: rt, explain: func(_ context.Context, actual *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				require.NotNil(t, actual)
				require.Equal(t, core.AuthorizationExplanationRequest{TenantID: 1, EntityID: 99, PrincipalID: 11, Permission: "project_read"}, req)
				return authz.Explanation{Allowed: tc.allowed, Matches: []authz.DecisionMatch{{RoleID: tc.role, Effect: tc.effect}}}, nil
			}}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				got, err := m.projectLeadOn(t.Context(), tx, 1, 99, 11)
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
				return nil
			}))
		})
	}
}

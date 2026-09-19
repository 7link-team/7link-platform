package files

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func aclFixtureRule(subject aclSubject, effect, applies string, actions ...string) nodeACLRule {
	return nodeACLRule{subject: subject, effect: effect, actions: actions, applies: applies}
}

func TestNodeACLInheritanceAndExplicitDenial(t *testing.T) {
	actor := aclSubject{"principal", 10}
	department := aclSubject{"department", 20}
	subjects := map[aclSubject]struct{}{actor: {}, department: {}}
	for _, scenario := range []struct {
		name    string
		layers  []nodeACLLayer
		allowed bool
		allows  []guid.ID
		denies  []guid.ID
	}{
		{"no-rules-denies", []nodeACLLayer{{nodeID: 1}}, false, nil, nil},
		{"direct-account-allow", []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(actor, "allow", "self", "read")}}}, true, []guid.ID{1}, nil},
		{"inherited-department-allow", []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(department, "allow", "descendants", "read")}}, {nodeID: 2}}, true, []guid.ID{1}, nil},
		{"ancestor-self-not-inherited", []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(actor, "allow", "self", "read")}}, {nodeID: 2}}, false, nil, nil},
		{"descendants-does-not-grant-self", []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(actor, "allow", "descendants", "read")}}}, false, nil, nil},
		{"deny-beats-same-layer-allow", []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(actor, "allow", "self", "read"), aclFixtureRule(department, "deny", "self", "read")}}}, false, []guid.ID{1}, []guid.ID{1}},
		{"child-allow-cannot-override-ancestor-deny", []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(department, "deny", "descendants", "read")}}, {nodeID: 2, rules: []nodeACLRule{aclFixtureRule(actor, "allow", "self", "read")}}}, false, []guid.ID{2}, []guid.ID{1}},
		{"stop-inheriting-deny", []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(department, "deny", "descendants", "read")}}, {nodeID: 2, stopInherit: true, rules: []nodeACLRule{aclFixtureRule(actor, "allow", "self", "read")}}}, true, []guid.ID{2}, nil},
		{"stop-inheriting-allow", []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(department, "allow", "descendants", "read")}}, {nodeID: 2, stopInherit: true}}, false, nil, nil},
		{"nearest-stop-allows-own-subtree", []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(actor, "deny", "descendants", "read")}}, {nodeID: 2, stopInherit: true, rules: []nodeACLRule{aclFixtureRule(actor, "allow", "self_and_descendants", "read")}}, {nodeID: 3}}, true, []guid.ID{2}, nil},
		{"multiple-sources-are-explained", []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(department, "allow", "self_and_descendants", "read")}}, {nodeID: 2, rules: []nodeACLRule{aclFixtureRule(actor, "allow", "self", "read")}}}, true, []guid.ID{2, 1}, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			result, err := evaluateNodeACL("read", subjects, scenario.layers)
			require.NoError(t, err)
			require.Equal(t, scenario.allowed, result.allowed)
			require.Equal(t, scenario.allows, result.allowNodes)
			require.Equal(t, scenario.denies, result.deniedNodes)
		})
	}
}

func TestNodeACLSubjectsAndActionsAreIndependent(t *testing.T) {
	for _, kind := range []string{"principal", "department", "group", "role", "owner"} {
		t.Run(kind, func(t *testing.T) {
			subject := aclSubject{kind, 10}
			layers := []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(subject, "allow", "self", "read", "traverse")}}}
			for _, action := range []string{"read", "traverse", "list", "download", "create", "update", "rename", "move", "delete", "restore", "purge", "share", "manage_acl"} {
				result, err := evaluateNodeACL(action, map[aclSubject]struct{}{subject: {}}, layers)
				require.NoError(t, err)
				require.Equal(t, action == "read" || action == "traverse", result.allowed)
			}
			for _, unrelated := range []aclSubject{{kind, 11}, {"principal", 10}} {
				if unrelated == subject {
					continue
				}
				result, err := evaluateNodeACL("read", map[aclSubject]struct{}{unrelated: {}}, layers)
				require.NoError(t, err)
				require.False(t, result.allowed)
			}
			result, err := evaluateNodeACL("read", nil, layers)
			require.NoError(t, err)
			require.False(t, result.allowed, "ownership must be verified, never inferred from a rule")
		})
	}
}

func TestNodeACLMalformedInputFailsClosed(t *testing.T) {
	subject := aclSubject{"principal", 10}
	for _, failure := range []string{"action", "no-path", "zero-node", "duplicate-node", "deep-path", "subject-kind", "subject-id", "rule-subject", "effect", "no-actions", "unknown-action", "applies", "partial-allow"} {
		t.Run(failure, func(t *testing.T) {
			action := "read"
			subjects := map[aclSubject]struct{}{subject: {}}
			rule := aclFixtureRule(subject, "allow", "self", "read")
			layers := []nodeACLLayer{{nodeID: 1, rules: []nodeACLRule{rule}}}
			switch failure {
			case "action":
				action = "*"
			case "no-path":
				layers = nil
			case "zero-node":
				layers[0].nodeID = 0
			case "duplicate-node":
				layers = append(layers, nodeACLLayer{nodeID: 1})
			case "deep-path":
				layers = make([]nodeACLLayer, maxNodeDepth+2)
				for index := range layers {
					layers[index].nodeID = guid.ID(index + 1)
				}
			case "subject-kind":
				subjects[aclSubject{"everyone", 10}] = struct{}{}
			case "subject-id":
				subjects[aclSubject{"principal", 0}] = struct{}{}
			case "rule-subject":
				layers[0].rules[0].subject = aclSubject{"principal", -1}
			case "effect":
				layers[0].rules[0].effect = "grant"
			case "no-actions":
				layers[0].rules[0].actions = nil
			case "unknown-action":
				layers[0].rules[0].actions = []string{"read", "*"}
			case "applies":
				layers[0].rules[0].applies = "all"
			case "partial-allow":
				layers = []nodeACLLayer{
					{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(subject, "grant", "descendants", "read")}},
					{nodeID: 2, rules: []nodeACLRule{rule}},
				}
			}
			result, err := evaluateNodeACL(action, subjects, layers)
			require.ErrorIs(t, err, errFileACL)
			require.Empty(t, result, "invalid policy must not return partial authority")
		})
	}
}

func nodeAuthorizationPolicy(grants map[string]authz.DataConstraint) core.EntityConstraintOn {
	return func(ctx context.Context, tx *gorm.DB, tenantID, entityID guid.ID, permission string, actorID guid.ID) (authz.DataConstraint, error) {
		if tenantID != 90 || entityID <= 0 || actorID <= 0 {
			return authz.DataConstraint{}, errOperationForbidden
		}
		if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
			return authz.DataConstraint{}, ErrAccountTransaction
		}
		constraint := grants[permission]
		constraint.Revision = 1
		return constraint, ctx.Err()
	}
}

func nodeAuthorizationReferences(valid bool, check func(core.AuthorizationReferences)) core.AuthorizationReferencesOn {
	return func(ctx context.Context, tx *gorm.DB, tenantID guid.ID, references core.AuthorizationReferences) (bool, error) {
		if tenantID != 90 {
			return false, errOperationForbidden
		}
		if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
			return false, ErrAccountTransaction
		}
		if check != nil {
			check(references)
		}
		return valid, ctx.Err()
	}
}

func TestNodeAuthorizerSeparatesManagementReadFromOrdinaryACL(t *testing.T) {
	h := newOperationServiceHarness(t)
	q := query.Bind(h.db, h.tree.prefix)
	require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(
		&model.NodeACL{ID: 9990, SpaceID: 1, NodeID: 30, SubjectKind: "principal", SubjectID: 70, Effect: "deny", Action: "read", Applies: "self", CreatedBy: 70, CreatedAt: time.Now().UnixMilli()},
		&model.NodeACL{ID: 9991, SpaceID: 1, NodeID: 30, SubjectKind: "principal", SubjectID: 70, Effect: "deny", Action: "delete", Applies: "self", CreatedBy: 70, CreatedAt: time.Now().UnixMilli()},
	))
	subjects := core.AuthorizationSubjects{PrincipalID: 70, Revision: 1}
	ordinary, err := newNodeAuthorizer(h.tree, nodeAuthorizationPolicy(map[string]authz.DataConstraint{
		"file_read": {AllowAll: true}, "file_delete": {AllowAll: true},
	}), nodeAuthorizationReferences(true, nil), 90, 80, 70, subjects)
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, ordinary.access(t.Context(), tx, 1, 70, 30, "copy"), errOperationForbidden)
		require.ErrorIs(t, ordinary.access(t.Context(), tx, 1, 70, 30, "delete"), errOperationForbidden)
		return nil
	}))
	manager, err := newNodeAuthorizer(h.tree, nodeAuthorizationPolicy(map[string]authz.DataConstraint{
		"file_read": {AllowAll: true}, "file_delete": {AllowAll: true}, fileReadAllPermission: {AllowAll: true},
	}), nodeAuthorizationReferences(true, nil), 90, 80, 70, subjects)
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, manager.access(t.Context(), tx, 1, 70, 30, "read"), "management read bypasses ordinary ACL deny")
		require.ErrorIs(t, manager.access(t.Context(), tx, 1, 70, 30, "delete"), errOperationForbidden, "management read never grants writes")
		return nil
	}))
}

func TestNodeAuthorizerValidatesRootScopeAndReferences(t *testing.T) {
	h := newOperationServiceHarness(t)
	require.NoError(t, h.db.Exec("UPDATE "+h.tree.prefix+"file_nodes SET root_scope = 'department', root_owner_id = 100 WHERE space_id = 1 AND id = 2").Error)
	grants := map[string]authz.DataConstraint{"file_read": {DepartmentIDs: []string{"100"}}}
	checked := false
	authorizer, err := newNodeAuthorizer(h.tree, nodeAuthorizationPolicy(grants), nodeAuthorizationReferences(true, func(references core.AuthorizationReferences) {
		checked = true
		require.Equal(t, core.AuthorizationReferences{EntityID: 80, DepartmentID: 100}, references)
	}), 90, 80, 70, core.AuthorizationSubjects{PrincipalID: 70, DepartmentID: 100, Revision: 1})
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return authorizer.access(t.Context(), tx, 1, 70, 30, "read")
	}))
	require.True(t, checked)

	invalid, err := newNodeAuthorizer(h.tree, nodeAuthorizationPolicy(grants), nodeAuthorizationReferences(false, nil), 90, 80, 70, core.AuthorizationSubjects{PrincipalID: 70, DepartmentID: 100, Revision: 1})
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, invalid.access(t.Context(), tx, 1, 70, 30, "read"), errOperationForbidden)
		return nil
	}))

	foreignEntity, err := newNodeAuthorizer(h.tree, nodeAuthorizationPolicy(grants), nodeAuthorizationReferences(false, func(references core.AuthorizationReferences) {
		require.Equal(t, core.AuthorizationReferences{EntityID: 81, DepartmentID: 100}, references)
	}), 90, 81, 70, core.AuthorizationSubjects{PrincipalID: 70, DepartmentID: 100, Revision: 1})
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, foreignEntity.access(t.Context(), tx, 1, 70, 30, "read"), errOperationForbidden)
		return nil
	}))
}

func TestNodeAuthorizerAddsOwnerOnlyForVerifiedPersonalRootOwner(t *testing.T) {
	h := newOperationServiceHarness(t)
	q := query.Bind(h.db, h.tree.prefix)
	require.NoError(t, h.db.Exec("UPDATE "+h.tree.prefix+"file_nodes SET root_scope = 'personal', root_owner_id = 70 WHERE space_id = 1 AND id = 2").Error)
	_, err := q.NodeACL.WithContext(t.Context()).Where(q.NodeACL.SpaceID.Eq(guid.ID(1)), q.NodeACL.NodeID.Eq(guid.ID(2))).Delete()
	require.NoError(t, err)
	require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(&model.NodeACL{
		ID: 9992, SpaceID: 1, NodeID: 2, SubjectKind: "owner", SubjectID: 70, Effect: "allow", Action: "read", Applies: "self_and_descendants", CreatedBy: 70, CreatedAt: time.Now().UnixMilli(),
	}))
	policy := nodeAuthorizationPolicy(map[string]authz.DataConstraint{"file_read": {OwnerIDs: []string{"70"}}})
	owner, err := newNodeAuthorizer(h.tree, policy, nodeAuthorizationReferences(true, nil), 90, 80, 70, core.AuthorizationSubjects{PrincipalID: 70, Revision: 1})
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return owner.access(t.Context(), tx, 1, 70, 30, "read")
	}))
	notOwner, err := newNodeAuthorizer(h.tree, policy, nodeAuthorizationReferences(true, nil), 90, 80, 71, core.AuthorizationSubjects{PrincipalID: 71, Revision: 1})
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, notOwner.access(t.Context(), tx, 1, 71, 30, "read"), errOperationForbidden)
		return nil
	}))
}

func TestNodeAuthorizerRejectsMalformedSubjectsAndChangedPolicy(t *testing.T) {
	policy := nodeAuthorizationPolicy(nil)
	references := nodeAuthorizationReferences(true, nil)
	for _, subjects := range []core.AuthorizationSubjects{
		{}, {PrincipalID: 70}, {PrincipalID: 71, Revision: 1}, {PrincipalID: 70, Revision: 1, RoleIDs: []guid.ID{0}}, {PrincipalID: 70, Revision: 1, GroupIDs: []guid.ID{-1}},
	} {
		_, err := newNodeAuthorizer(directoryTree{}, policy, references, 90, 80, 70, subjects)
		require.ErrorIs(t, err, errOperationForbidden)
	}
	h := newOperationServiceHarness(t)
	changed := nodeAuthorizationPolicy(map[string]authz.DataConstraint{"file_read": {AllowAll: true}})
	authorizer, err := newNodeAuthorizer(h.tree, changed, references, 90, 80, 70, core.AuthorizationSubjects{PrincipalID: 70, Revision: 2})
	require.NoError(t, err)
	err = h.db.Transaction(func(tx *gorm.DB) error { return authorizer.access(t.Context(), tx, 1, 70, 30, "read") })
	require.ErrorIs(t, err, core.ErrAuthorizationChanged)
}

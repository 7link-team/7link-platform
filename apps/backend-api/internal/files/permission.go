package files

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
)

var errFileACL = errors.New("files: invalid node permissions")

const (
	fileViewPermission    = "file_view"
	fileReadAllPermission = "file_read_all"
	filePermissionPrefix  = "file_"
)

func fileAuthorizationActions() []authz.Action {
	actions := []authz.Action{
		{Resource: "file", Verb: "view", Scope: "tenant", Summary: "view file workspaces", DataScoped: true, Menu: true},
		{Resource: "file", Verb: "read_all", Scope: "tenant", Summary: "read all files in the authorized management scope", DataScoped: true},
	}
	for _, verb := range []string{"list", "traverse", "read", "download", "create", "update", "rename", "move", "delete", "restore", "purge", "share", "manage_acl"} {
		actions = append(actions, authz.Action{Resource: "file", Verb: verb, Scope: "tenant", Summary: verb + " files", DataScoped: true})
	}
	for index := range actions {
		actions[index].Code = authz.PermissionCode(actions[index].Resource, actions[index].Verb)
	}
	return actions
}

type aclSubject struct {
	kind string
	id   guid.ID
}

type nodeACLRule struct {
	subject aclSubject
	effect  string
	actions []string
	applies string
}

type nodeACLLayer struct {
	nodeID      guid.ID
	stopInherit bool
	rules       []nodeACLRule
}

type nodeACLDecision struct {
	allowed     bool
	allowNodes  []guid.ID
	deniedNodes []guid.ID
}

func validACLAction(action string) bool {
	switch action {
	case "list", "traverse", "read", "download", "create", "update", "rename", "move", "delete", "restore", "purge", "share", "manage_acl":
		return true
	default:
		return false
	}
}

func validACLSubject(subject aclSubject) bool {
	if subject.id <= 0 {
		return false
	}
	switch subject.kind {
	case "principal", "department", "group", "role", "owner":
		return true
	default:
		return false
	}
}

// evaluateNodeACL consumes a verified root-to-node path and current IAM subjects.
// It answers only the ACL layer, never IAM scope, traversal, or delegation rights.
func evaluateNodeACL(action string, subjects map[aclSubject]struct{}, layers []nodeACLLayer) (nodeACLDecision, error) {
	var result nodeACLDecision
	if !validACLAction(action) || len(layers) == 0 || len(layers) > maxNodeDepth+1 {
		return result, errFileACL
	}
	for subject := range subjects {
		if !validACLSubject(subject) {
			return result, errFileACL
		}
	}
	seen := make(map[guid.ID]struct{}, len(layers))
	for _, layer := range layers {
		if layer.nodeID <= 0 {
			return result, errFileACL
		}
		if _, duplicate := seen[layer.nodeID]; duplicate {
			return result, errFileACL
		}
		seen[layer.nodeID] = struct{}{}
	}
	for index := len(layers) - 1; index >= 0; index-- {
		layer := layers[index]
		allowed, denied := false, false
		for _, rule := range layer.rules {
			if !validACLSubject(rule.subject) || (rule.effect != "allow" && rule.effect != "deny") || len(rule.actions) == 0 {
				return nodeACLDecision{}, errFileACL
			}
			self := index == len(layers)-1
			applicable := false
			switch rule.applies {
			case "self":
				applicable = self
			case "descendants":
				applicable = !self
			case "self_and_descendants":
				applicable = true
			default:
				return nodeACLDecision{}, errFileACL
			}
			matchesAction := false
			for _, candidate := range rule.actions {
				if !validACLAction(candidate) {
					return nodeACLDecision{}, errFileACL
				}
				matchesAction = matchesAction || candidate == action
			}
			if _, matchesSubject := subjects[rule.subject]; !matchesSubject || !applicable || !matchesAction {
				continue
			}
			if rule.effect == "deny" {
				denied = true
			} else {
				allowed = true
			}
		}
		if allowed {
			result.allowNodes = append(result.allowNodes, layer.nodeID)
		}
		if denied {
			result.deniedNodes = append(result.deniedNodes, layer.nodeID)
		}
		if layer.stopInherit {
			break
		}
	}
	result.allowed = len(result.allowNodes) > 0 && len(result.deniedNodes) == 0
	return result, nil
}

type nodeAuthorizer struct {
	tree        directoryTree
	account     bool
	policy      core.EntityConstraintOn
	references  core.AuthorizationReferencesOn
	tenantID    guid.ID
	entityID    guid.ID
	actorID     guid.ID
	subjects    core.AuthorizationSubjects
	acl         map[aclSubject]struct{}
	constraints map[string]authz.DataConstraint
	validRoots  map[rootBinding]bool
}

func newAccountNodeAuthorizer(tree directoryTree, actorID guid.ID) (*nodeAuthorizer, error) {
	if actorID <= 0 {
		return nil, errOperationForbidden
	}
	return &nodeAuthorizer{
		tree: tree, account: true, actorID: actorID,
		acl: map[aclSubject]struct{}{{kind: "principal", id: actorID}: {}},
	}, nil
}

func newNodeAuthorizer(tree directoryTree, policy core.EntityConstraintOn, references core.AuthorizationReferencesOn, tenantID, entityID, actorID guid.ID, subjects core.AuthorizationSubjects) (*nodeAuthorizer, error) {
	if policy == nil || references == nil || tenantID <= 0 || entityID <= 0 || actorID <= 0 || subjects.PrincipalID != actorID || subjects.Revision <= 0 {
		return nil, errOperationForbidden
	}
	aclSubjects := map[aclSubject]struct{}{{kind: "principal", id: actorID}: {}}
	if subjects.DepartmentID > 0 {
		aclSubjects[aclSubject{kind: "department", id: subjects.DepartmentID}] = struct{}{}
	}
	for _, id := range subjects.GroupIDs {
		if id <= 0 {
			return nil, errOperationForbidden
		}
		aclSubjects[aclSubject{kind: "group", id: id}] = struct{}{}
	}
	for _, id := range subjects.RoleIDs {
		if id <= 0 {
			return nil, errOperationForbidden
		}
		aclSubjects[aclSubject{kind: "role", id: id}] = struct{}{}
	}
	return &nodeAuthorizer{
		tree: tree, policy: policy, references: references, tenantID: tenantID,
		entityID: entityID, actorID: actorID, subjects: subjects, acl: aclSubjects,
		constraints: make(map[string]authz.DataConstraint), validRoots: make(map[rootBinding]bool),
	}, nil
}

func (a *nodeAuthorizer) access(ctx context.Context, tx *gorm.DB, spaceID, actorID, nodeID guid.ID, action string) error {
	allowed, err := a.accessMany(ctx, tx, spaceID, actorID, []guid.ID{nodeID}, action)
	if err != nil {
		return err
	}
	if !allowed[nodeID] {
		return errOperationForbidden
	}
	return nil
}

func (a *nodeAuthorizer) accessMany(
	ctx context.Context,
	tx *gorm.DB,
	spaceID, actorID guid.ID,
	nodeIDs []guid.ID,
	action string,
) (map[guid.ID]bool, error) {
	if a == nil || tx == nil || actorID != a.actorID || spaceID <= 0 || len(nodeIDs) == 0 {
		return nil, errOperationForbidden
	}
	for _, nodeID := range nodeIDs {
		if nodeID <= 0 {
			return nil, errOperationForbidden
		}
	}
	if action == "copy" {
		action = "read"
	}
	if !validACLAction(action) {
		return nil, errOperationForbidden
	}
	var constraint, management authz.DataConstraint
	if !a.account {
		var err error
		constraint, err = a.constraint(ctx, tx, filePermissionPrefix+action)
		if err != nil {
			return nil, err
		}
		if managementReadAction(action) {
			management, err = a.constraint(ctx, tx, fileReadAllPermission)
			if err != nil {
				return nil, err
			}
		}
	}
	permissionsByNode, nodeErrors, err := a.tree.loadNodePermissionsBatch(ctx, tx, spaceID, nodeIDs)
	if err != nil {
		return nil, err
	}
	allowed := make(map[guid.ID]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeErr := nodeErrors[nodeID]; nodeErr != nil {
			if errors.Is(nodeErr, errRootUnbound) || errors.Is(nodeErr, ErrNotFound) {
				continue
			}
			return nil, nodeErr
		}
		permissions, ok := permissionsByNode[nodeID]
		if !ok {
			return nil, errFileACL
		}
		permitted, err := a.authorizeLoaded(ctx, tx, permissions, constraint, management, action)
		if err != nil {
			if errors.Is(err, errOperationForbidden) {
				continue
			}
			return nil, err
		}
		allowed[nodeID] = permitted
	}
	return allowed, nil
}

func (a *nodeAuthorizer) authorizeLoaded(
	ctx context.Context,
	tx *gorm.DB,
	permissions storedNodePermissions,
	constraint, management authz.DataConstraint,
	action string,
) (bool, error) {
	if permissions.managementMode != "user" {
		return false, errOperationForbidden
	}
	if a.account {
		if permissions.space.ScopeKind != "account" || permissions.space.PrincipalID == nil ||
			*permissions.space.PrincipalID != a.actorID || permissions.space.TenantID != nil ||
			permissions.root.scope != "account" || permissions.root.ownerID != a.actorID {
			return false, errOperationForbidden
		}
		subjects := make(map[aclSubject]struct{}, len(a.acl)+1)
		for subject := range a.acl {
			subjects[subject] = struct{}{}
		}
		subjects[aclSubject{kind: "owner", id: a.actorID}] = struct{}{}
		decision, err := evaluateNodeACL(action, subjects, permissions.layers)
		if err != nil {
			return false, err
		}
		if !decision.allowed {
			return false, errOperationForbidden
		}
		return true, nil
	}
	if permissions.space.ScopeKind != "tenant" || permissions.space.TenantID == nil || *permissions.space.TenantID != a.tenantID {
		return false, errOperationForbidden
	}
	if permissions.root.scope == "entity" && permissions.root.ownerID != a.entityID {
		return false, errOperationForbidden
	}
	if err := a.validateRootOnce(ctx, tx, permissions.root); err != nil {
		return false, err
	}
	if managementReadAction(action) && constraintAllowsRoot(management, permissions.root, a.entityID) {
		return true, nil
	}
	if !constraintAllowsRoot(constraint, permissions.root, a.entityID) {
		return false, errOperationForbidden
	}
	subjects := a.acl
	if (permissions.root.scope == "personal" || permissions.root.scope == "account") && permissions.root.ownerID == a.actorID {
		subjects = make(map[aclSubject]struct{}, len(a.acl)+1)
		for subject := range a.acl {
			subjects[subject] = struct{}{}
		}
		subjects[aclSubject{kind: "owner", id: a.actorID}] = struct{}{}
	}
	layers := permissions.layers
	if organizationRoot(permissions.root.scope) && len(layers) > 0 && layers[0].nodeID == permissions.rootID {
		layers = append([]nodeACLLayer(nil), layers...)
		layers[0].rules = append([]nodeACLRule{{
			subject: aclSubject{kind: "principal", id: a.actorID}, effect: "allow",
			actions: []string{action}, applies: "self_and_descendants",
		}}, layers[0].rules...)
	}
	decision, err := evaluateNodeACL(action, subjects, layers)
	if err != nil {
		return false, err
	}
	if !decision.allowed {
		return false, errOperationForbidden
	}
	return true, nil
}

func (a *nodeAuthorizer) constraint(ctx context.Context, tx *gorm.DB, permission string) (authz.DataConstraint, error) {
	if constraint, ok := a.constraints[permission]; ok {
		return constraint, nil
	}
	constraint, err := a.policy(ctx, tx, a.tenantID, a.entityID, permission, a.actorID)
	if err != nil {
		return authz.DataConstraint{}, err
	}
	if constraint.Revision != a.subjects.Revision {
		return authz.DataConstraint{}, core.ErrAuthorizationChanged
	}
	a.constraints[permission] = constraint
	return constraint, nil
}

func (a *nodeAuthorizer) validateRoot(ctx context.Context, tx *gorm.DB, root rootBinding) error {
	var references core.AuthorizationReferences
	switch root.scope {
	case "tenant":
		return nil
	case "entity":
		references.EntityID = root.ownerID
	case "department":
		references.EntityID, references.DepartmentID = a.entityID, root.ownerID
	case "personal", "account":
		references.PrincipalID = root.ownerID
	default:
		return errOperationForbidden
	}
	if references.EntityID == 0 && references.DepartmentID == 0 && references.PrincipalID == 0 {
		return nil
	}
	valid, err := a.references(ctx, tx, a.tenantID, references)
	if err != nil {
		return err
	}
	if !valid {
		return errOperationForbidden
	}
	return nil
}

func (a *nodeAuthorizer) validateRootOnce(ctx context.Context, tx *gorm.DB, root rootBinding) error {
	if a.validRoots[root] {
		return nil
	}
	if err := a.validateRoot(ctx, tx, root); err != nil {
		return err
	}
	a.validRoots[root] = true
	return nil
}

func managementReadAction(action string) bool {
	return action == "list" || action == "traverse" || action == "read" || action == "download"
}

func constraintAllowsRoot(constraint authz.DataConstraint, root rootBinding, currentEntityID guid.ID) bool {
	entityID := guid.ID(0)
	if root.scope == "entity" {
		entityID = root.ownerID
	} else if root.scope == "department" {
		entityID = currentEntityID
	}
	if entityID > 0 && slices.Contains(constraint.DeniedIDs, entityID.String()) {
		return false
	}
	if constraint.AllowAll {
		return true
	}
	entityAllowed := entityID > 0 && slices.Contains(constraint.ResourceIDs, entityID.String())
	switch root.scope {
	case "tenant":
		return false
	case "entity":
		return entityAllowed
	case "department":
		return entityAllowed || slices.Contains(constraint.DepartmentIDs, root.ownerID.String())
	case "personal":
		return slices.Contains(constraint.OwnerIDs, root.ownerID.String())
	default:
		return false
	}
}

func organizationRoot(scope string) bool {
	return scope == "tenant" || scope == "entity" || scope == "department"
}

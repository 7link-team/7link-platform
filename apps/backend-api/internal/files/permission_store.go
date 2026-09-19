package files

import (
	"context"
	"database/sql/driver"
	"errors"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

const maxNodeACLRules = 256

func fileIDValues(ids []guid.ID) []driver.Valuer {
	values := make([]driver.Valuer, len(ids))
	for index, id := range ids {
		values[index] = id
	}
	return values
}

var errRootUnbound = errors.New("files: node has no root binding")

type rootBinding struct {
	scope   string
	ownerID guid.ID
}

type storedNodePermissions struct {
	space          *model.Space
	rootID         guid.ID
	root           rootBinding
	managementMode string
	layers         []nodeACLLayer
}

func validRootBinding(space *model.Space, node *model.Node, binding rootBinding) bool {
	if space == nil || node == nil || node.SpaceID != space.ID || node.Kind != "directory" || node.State != "active" {
		return false
	}
	if binding.scope == "system" {
		return binding.ownerID == 0 && space.ScopeKind == "system" && space.SystemSlot != nil && *space.SystemSlot == 1 && space.TenantID == nil && space.PrincipalID == nil && node.ParentID == nil
	}
	if binding.ownerID <= 0 {
		return false
	}
	if space.ScopeKind == "account" {
		return space.PrincipalID != nil && *space.PrincipalID == binding.ownerID && space.TenantID == nil && space.SystemSlot == nil && binding.scope == "account" && node.ParentID == nil
	}
	if space.ScopeKind != "tenant" || space.TenantID == nil || *space.TenantID <= 0 || space.PrincipalID != nil || space.SystemSlot != nil {
		return false
	}
	switch binding.scope {
	case "tenant":
		return binding.ownerID == *space.TenantID
	case "entity", "department", "personal":
		return true
	default:
		return false
	}
}

func nodeRootBinding(space *model.Space, node *model.Node) (rootBinding, error) {
	if space != nil && node != nil && space.ScopeKind == "system" && node.ParentID == nil {
		binding := rootBinding{scope: "system"}
		if node.RootScope != nil || node.RootOwnerID != nil || !validRootBinding(space, node, binding) {
			return rootBinding{}, errFileACL
		}
		return binding, nil
	}
	if node == nil || (node.RootScope == nil) != (node.RootOwnerID == nil) {
		return rootBinding{}, errFileACL
	}
	if node.RootScope == nil {
		return rootBinding{}, nil
	}
	binding := rootBinding{scope: *node.RootScope, ownerID: *node.RootOwnerID}
	if !validRootBinding(space, node, binding) {
		return rootBinding{}, errFileACL
	}
	return binding, nil
}

// loadNodePermissions is persistence only. Callers must fence current IAM before
// this first business read and verify live root affiliations and subjects.
func (t directoryTree) loadNodePermissions(ctx context.Context, tx *gorm.DB, spaceID, nodeID guid.ID) (storedNodePermissions, error) {
	var result storedNodePermissions
	if tx == nil || spaceID <= 0 || nodeID <= 0 {
		return result, errFileACL
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return result, ErrAccountTransaction
	}
	q := query.Bind(tx, t.prefix)
	space, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).Take()
	if err != nil {
		return result, err
	}
	path, err := t.pathNodes(ctx, tx, spaceID, nodeID)
	if err != nil {
		return result, err
	}
	rootIndex := -1
	for index, node := range path {
		binding, err := nodeRootBinding(space, node)
		if err != nil {
			return storedNodePermissions{}, err
		}
		if binding.scope != "" {
			rootIndex = index
			result.rootID, result.root = node.ID, binding
		}
	}
	if rootIndex < 0 {
		return storedNodePermissions{}, errRootUnbound
	}
	start := rootIndex
	for index := rootIndex; index < len(path); index++ {
		if path[index].ACLStopInheritance {
			start = index
		}
	}
	result.space = space
	result.managementMode = path[len(path)-1].ManagementMode
	for _, node := range path[start:] {
		acl := q.NodeACL
		rows, err := acl.WithContext(ctx).Where(acl.SpaceID.Eq(spaceID), acl.NodeID.Eq(node.ID)).Order(acl.ID).Limit(maxNodeACLRules + 1).Find()
		if err != nil {
			return storedNodePermissions{}, err
		}
		if len(rows) > maxNodeACLRules {
			return storedNodePermissions{}, errFileACL
		}
		layer := nodeACLLayer{nodeID: node.ID, stopInherit: node.ACLStopInheritance || node.ID == result.rootID}
		for _, row := range rows {
			layer.rules = append(layer.rules, nodeACLRule{subject: aclSubject{row.SubjectKind, row.SubjectID}, effect: row.Effect, actions: []string{row.Action}, applies: row.Applies})
		}
		result.layers = append(result.layers, layer)
	}
	return result, nil
}

type permissionPathRow struct {
	TargetID guid.ID `gorm:"column:permission_target_id"`
	model.Node
}

// loadNodePermissionsBatch loads one bounded page's paths and ACL layers with
// fixed-query work. Each target is still validated independently, so one
// unbound target cannot inherit another target's root.
func (t directoryTree) loadNodePermissionsBatch(
	ctx context.Context,
	tx *gorm.DB,
	spaceID guid.ID,
	targetIDs []guid.ID,
) (map[guid.ID]storedNodePermissions, map[guid.ID]error, error) {
	results := make(map[guid.ID]storedNodePermissions, len(targetIDs))
	targetErrors := make(map[guid.ID]error)
	if tx == nil || spaceID <= 0 || len(targetIDs) == 0 || len(targetIDs) > maxBrowserPage+maxNodeDepth+1 {
		return nil, nil, errFileACL
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return nil, nil, ErrAccountTransaction
	}
	seenTargets := make(map[guid.ID]struct{}, len(targetIDs))
	for _, id := range targetIDs {
		if id <= 0 {
			return nil, nil, errFileACL
		}
		if _, duplicate := seenTargets[id]; duplicate {
			continue
		}
		seenTargets[id] = struct{}{}
	}

	q := query.Bind(tx, t.prefix)
	space, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).Take()
	if err != nil {
		return nil, nil, err
	}
	names := query.BindNames(t.prefix)
	rows := make([]permissionPathRow, 0, len(seenTargets)*2)
	ids := make([]guid.ID, 0, len(seenTargets))
	for id := range seenTargets {
		ids = append(ids, id)
	}
	err = tx.WithContext(ctx).Table(names.NodeClosure.String()+" AS c").
		Select("c.descendant_id AS permission_target_id, n.*").
		Joins("JOIN "+names.Nodes.String()+" AS n ON n.space_id = c.space_id AND n.id = c.ancestor_id").
		Where("c.space_id = ? AND c.descendant_id IN ? AND n.state IN ?", spaceID, ids, []string{"active", "trashed"}).
		Order("c.descendant_id ASC, c.depth DESC").
		Limit(len(seenTargets) * (maxNodeDepth + 2)).Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	paths := make(map[guid.ID][]*model.Node, len(seenTargets))
	ancestorIDs := make(map[guid.ID]struct{})
	for index := range rows {
		row := &rows[index]
		paths[row.TargetID] = append(paths[row.TargetID], &row.Node)
		ancestorIDs[row.ID] = struct{}{}
	}

	aclByNode := make(map[guid.ID][]nodeACLRule, len(ancestorIDs))
	if len(ancestorIDs) > 0 {
		aclNodeIDs := make([]guid.ID, 0, len(ancestorIDs))
		for id := range ancestorIDs {
			aclNodeIDs = append(aclNodeIDs, id)
		}
		acl := q.NodeACL
		aclRowLimit := len(aclNodeIDs)*maxNodeACLRules + 1
		aclRows, err := acl.WithContext(ctx).Where(
			acl.SpaceID.Eq(spaceID), acl.NodeID.In(fileIDValues(aclNodeIDs)...),
		).Order(acl.NodeID.Asc(), acl.ID.Asc()).Limit(aclRowLimit).Find()
		if err != nil {
			return nil, nil, err
		}
		if len(aclRows) == aclRowLimit {
			return nil, nil, errFileACL
		}
		counts := make(map[guid.ID]int)
		for _, row := range aclRows {
			counts[row.NodeID]++
			if counts[row.NodeID] > maxNodeACLRules {
				return nil, nil, errFileACL
			}
			aclByNode[row.NodeID] = append(aclByNode[row.NodeID], nodeACLRule{
				subject: aclSubject{row.SubjectKind, row.SubjectID}, effect: row.Effect,
				actions: []string{row.Action}, applies: row.Applies,
			})
		}
	}

	for id := range seenTargets {
		path := paths[id]
		if len(path) == 0 {
			targetErrors[id] = ErrNotFound
			continue
		}
		if len(path) > maxNodeDepth+1 || path[0].ParentID != nil || path[len(path)-1].ID != id {
			targetErrors[id] = errHierarchy
			continue
		}
		validPath := true
		for index := 1; index < len(path); index++ {
			if path[index].ParentID == nil || *path[index].ParentID != path[index-1].ID {
				validPath = false
				break
			}
		}
		if !validPath {
			targetErrors[id] = errHierarchy
			continue
		}
		permissions := storedNodePermissions{space: space, managementMode: path[len(path)-1].ManagementMode}
		rootIndex := -1
		for index, node := range path {
			binding, err := nodeRootBinding(space, node)
			if err != nil {
				targetErrors[id] = err
				validPath = false
				break
			}
			if binding.scope != "" {
				rootIndex, permissions.rootID, permissions.root = index, node.ID, binding
			}
		}
		if !validPath {
			continue
		}
		if rootIndex < 0 {
			targetErrors[id] = errRootUnbound
			continue
		}
		start := rootIndex
		for index := rootIndex; index < len(path); index++ {
			if path[index].ACLStopInheritance {
				start = index
			}
		}
		for _, node := range path[start:] {
			permissions.layers = append(permissions.layers, nodeACLLayer{
				nodeID: node.ID, stopInherit: node.ACLStopInheritance || node.ID == permissions.rootID,
				rules: aclByNode[node.ID],
			})
		}
		results[id] = permissions
	}
	return results, targetErrors, nil
}

// bindRoot initializes a previously unbound directory. It never changes an
// existing affiliation. The caller authorizes/validates IAM IDs and appends audit
// in this same transaction; every error must roll back the caller transaction.
func (t directoryTree) bindRoot(ctx context.Context, tx *gorm.DB, spaceID guid.ID, node nodeRevision, spaceVersion int64, binding rootBinding, now time.Time) error {
	if tx == nil || spaceID <= 0 || node.ID <= 0 || node.Version <= 0 || spaceVersion <= 0 {
		return errFileACL
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return ErrAccountTransaction
	}
	q, space, err := t.lockOperationSpace(ctx, tx, spaceID)
	if err != nil {
		return err
	}
	if space.Version != spaceVersion {
		return errTreeConflict
	}
	path, err := t.pathNodes(ctx, tx, spaceID, node.ID)
	if err != nil {
		return err
	}
	row := path[len(path)-1]
	if row.Version != node.Version {
		return errTreeConflict
	}
	if row.RootScope != nil || row.RootOwnerID != nil || !validRootBinding(space, row, binding) {
		return errFileACL
	}
	n := q.Node
	updates := []field.AssignExpr{
		n.RootScope.Value(binding.scope), n.RootOwnerID.Value(binding.ownerID),
		n.ACLStopInheritance.Value(true), n.Version.Add(1), n.UpdatedAt.Value(now.UnixMilli()),
	}
	updated, err := n.WithContext(ctx).Where(n.SpaceID.Eq(spaceID), n.ID.Eq(node.ID), n.Version.Eq(node.Version)).UpdateSimple(updates...)
	if err != nil {
		return err
	}
	if updated.RowsAffected != 1 {
		return errTreeConflict
	}
	_, err = q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).UpdateSimple(q.Space.Version.Add(1))
	return err
}

// replaceNodeACL replaces the entire direct rule set atomically. It does not
// authorize edits or delegate actions: the service must check both before calling.
func (t directoryTree) replaceNodeACL(ctx context.Context, tx *gorm.DB, spaceID guid.ID, node nodeRevision, spaceVersion int64, stopInherit bool, actor guid.ID, rules []nodeACLRule, now time.Time, next func() (int64, error)) error {
	if tx == nil || spaceID <= 0 || node.ID <= 0 || node.Version <= 0 || spaceVersion <= 0 || actor <= 0 || next == nil {
		return errFileACL
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return ErrAccountTransaction
	}
	q, space, err := t.lockOperationSpace(ctx, tx, spaceID)
	if err != nil {
		return err
	}
	if space.Version != spaceVersion {
		return errTreeConflict
	}
	if _, err := evaluateNodeACL("read", nil, []nodeACLLayer{{nodeID: node.ID, rules: rules}}); err != nil {
		return err
	}
	type ruleKey struct {
		subject                 aclSubject
		effect, action, applies string
	}
	seen := make(map[ruleKey]struct{})
	rows := make([]*model.NodeACL, 0)
	for _, rule := range rules {
		for _, action := range rule.actions {
			key := ruleKey{rule.subject, rule.effect, action, rule.applies}
			if _, duplicate := seen[key]; duplicate {
				return errFileACL
			}
			seen[key] = struct{}{}
			if len(seen) > maxNodeACLRules {
				return errFileACL
			}
			rows = append(rows, &model.NodeACL{SpaceID: spaceID, NodeID: node.ID, SubjectKind: rule.subject.kind, SubjectID: rule.subject.id, Effect: rule.effect, Action: action, Applies: rule.applies, CreatedBy: actor, CreatedAt: now.UnixMilli()})
		}
	}
	permissions, err := t.loadNodePermissions(ctx, tx, spaceID, node.ID)
	if err != nil {
		return err
	}
	if permissions.space.Version != spaceVersion {
		return errTreeConflict
	}
	if permissions.rootID == node.ID && !stopInherit {
		return errFileACL
	}
	n := q.Node
	updated, err := n.WithContext(ctx).Where(n.SpaceID.Eq(spaceID), n.ID.Eq(node.ID), n.Version.Eq(node.Version)).UpdateSimple(n.ACLStopInheritance.Value(stopInherit), n.Version.Add(1), n.UpdatedAt.Value(now.UnixMilli()))
	if err != nil {
		return err
	}
	if updated.RowsAffected != 1 {
		return errTreeConflict
	}
	acl := q.NodeACL
	if _, err := acl.WithContext(ctx).Where(acl.SpaceID.Eq(spaceID), acl.NodeID.Eq(node.ID)).Delete(); err != nil {
		return err
	}
	for _, row := range rows {
		id, err := next()
		if err != nil {
			return err
		}
		if id <= 0 {
			return errFileACL
		}
		row.ID = guid.ID(id)
	}
	if len(rows) > 0 {
		if err := acl.WithContext(ctx).Create(rows...); err != nil {
			return err
		}
	}
	_, err = q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).UpdateSimple(q.Space.Version.Add(1))
	return err
}

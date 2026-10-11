package files

import (
	"context"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxNodeDepth = 64

var errHierarchy = errors.New("files: invalid or over-depth directory hierarchy")

// directoryTree is persistence only. HTTP/services must authorize the space and
// every exposed node; tenant ownership alone never grants access to this tree.
type directoryTree struct{ prefix string }

func (t directoryTree) createSpace(ctx context.Context, db *gorm.DB, id, rootID, ownerID guid.ID, scope string, now time.Time) error {
	if db == nil || id <= 0 || rootID <= 0 || ownerID <= 0 || (scope != spaceTenant && scope != spaceAccount) {
		return errHierarchy
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return t.createSpaceOn(ctx, tx, id, rootID, ownerID, scope, now)
	})
}

func (t directoryTree) createSpaceOn(ctx context.Context, tx *gorm.DB, id, rootID, ownerID guid.ID, scope string, now time.Time) error {
	if tx == nil || id <= 0 || rootID <= 0 || ownerID <= 0 || (scope != spaceTenant && scope != spaceAccount) {
		return errHierarchy
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return ErrAccountTransaction
	}
	space := model.Space{ID: id, ScopeKind: scope, Version: 1, CreatedAt: now.UnixMilli()}
	if scope == spaceTenant {
		space.TenantID = &ownerID
	} else {
		space.PrincipalID = &ownerID
	}
	q := query.Bind(tx, t.prefix)
	if err := q.Space.WithContext(ctx).Create(&space); err != nil {
		return err
	}
	return t.createSpaceRootOn(ctx, tx, id, rootID, now)
}

func (t directoryTree) createSpaceRootOn(ctx context.Context, tx *gorm.DB, id, rootID guid.ID, now time.Time) error {
	return t.createRootOn(ctx, tx, id, rootID, false, now)
}

// createRootOn writes a space's physical root. A system root stops ACL
// inheritance because nothing sits above it to inherit from.
func (t directoryTree) createRootOn(ctx context.Context, tx *gorm.DB, id, rootID guid.ID, stopInheritance bool, now time.Time) error {
	if tx == nil || id <= 0 || rootID <= 0 {
		return errHierarchy
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return ErrAccountTransaction
	}
	slot := 1
	root := model.Node{ID: rootID, SpaceID: id, ParentKind: "directory", RootSlot: &slot, LiveSlot: &slot, Kind: "directory", State: "active", Version: 1, ManagementMode: "user", ACLStopInheritance: stopInheritance, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}
	q := query.Bind(tx, t.prefix)
	if err := q.Node.WithContext(ctx).Create(&root); err != nil {
		return err
	}
	return q.NodeClosure.WithContext(ctx).Create(&model.NodeClosure{SpaceID: id, AncestorID: rootID, DescendantID: rootID, Depth: 0})
}

func (t directoryTree) createSystemSpaceOn(ctx context.Context, tx *gorm.DB, id, rootID guid.ID, now time.Time) error {
	if tx == nil || id <= 0 || rootID <= 0 {
		return errHierarchy
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return ErrAccountTransaction
	}
	slot := 1
	space := model.Space{ID: id, ScopeKind: spaceSystem, SystemSlot: &slot, Version: 1, CreatedAt: now.UnixMilli()}
	q := query.Bind(tx, t.prefix)
	if err := q.Space.WithContext(ctx).Create(&space); err != nil {
		return err
	}
	return t.createRootOn(ctx, tx, id, rootID, true, now)
}

// ensureSpaceOn returns the one space that owner names -- an account's
// (principalID), a tenant's (tenantID) or the system's (neither) -- creating it
// with its physical root when none exists yet. created reports whether this
// call created it.
//
// The insert comes before any read and does nothing on conflict, so
// concurrent initializers serialize on the space's unique key instead of each
// seeing "no space" and racing to insert. Whether this call created the space
// is judged by the ID read back, never by an affected-row count, which MySQL
// reports as matched rather than changed under clientFoundRows. The read-back
// locks the row, so a reader also sees a space committed after its snapshot.
func (t directoryTree) ensureSpaceOn(ctx context.Context, tx *gorm.DB, principalID, tenantID guid.ID, nextID func() (guid.ID, error), now time.Time) (*model.Space, bool, error) {
	if tx == nil || nextID == nil || principalID < 0 || tenantID < 0 || (principalID > 0 && tenantID > 0) {
		return nil, false, errHierarchy
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return nil, false, ErrAccountTransaction
	}
	id, err := nextID()
	if err != nil {
		return nil, false, err
	}
	candidate := model.Space{ID: id, Version: 1, CreatedAt: now.UnixMilli()}
	q := query.Bind(tx, t.prefix)
	read := q.Space.WithContext(ctx)
	switch {
	case principalID > 0:
		candidate.ScopeKind, candidate.PrincipalID = spaceAccount, &principalID
		read = read.Where(q.Space.ScopeKind.Eq(spaceAccount), q.Space.PrincipalID.Eq(principalID))
	case tenantID > 0:
		candidate.ScopeKind, candidate.TenantID = spaceTenant, &tenantID
		read = read.Where(q.Space.ScopeKind.Eq(spaceTenant), q.Space.TenantID.Eq(tenantID))
	default:
		slot := 1
		candidate.ScopeKind, candidate.SystemSlot = spaceSystem, &slot
		read = read.Where(q.Space.ScopeKind.Eq(spaceSystem), q.Space.SystemSlot.Eq(slot))
	}
	if err := q.Space.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate); err != nil {
		return nil, false, err
	}
	if tx.Dialector.Name() != "sqlite" {
		// SQLite already holds the write lock the insert took.
		read = read.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate})
	}
	space, err := read.Take()
	if err != nil {
		return nil, false, err
	}
	if space.ID != candidate.ID {
		return space, false, nil
	}
	rootID, err := nextID()
	if err != nil {
		return nil, false, err
	}
	if err := t.createRootOn(ctx, tx, space.ID, rootID, space.ScopeKind == spaceSystem, now); err != nil {
		return nil, false, err
	}
	return space, true, nil
}

func (t directoryTree) createDirectory(ctx context.Context, db *gorm.DB, spaceID, id, parentID guid.ID, input string, now time.Time) (*model.Node, error) {
	if db == nil || spaceID <= 0 || id <= 0 || parentID <= 0 {
		return nil, errHierarchy
	}
	var row *model.Node
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = t.createNodeOn(ctx, tx, spaceID, id, parentID, "directory", input, now)
		return err
	})
	return row, err
}

func (t directoryTree) createNodeOn(ctx context.Context, tx *gorm.DB, spaceID, id, parentID guid.ID, kind, input string, now time.Time) (*model.Node, error) {
	if tx == nil || spaceID <= 0 || id <= 0 || parentID <= 0 || (kind != "directory" && kind != "file") {
		return nil, errHierarchy
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return nil, ErrAccountTransaction
	}
	name, key, err := PortableName(input)
	if err != nil {
		return nil, err
	}
	slot := 1
	row := model.Node{ID: id, SpaceID: spaceID, ParentID: &parentID, ParentKind: "directory", LiveSlot: &slot, Kind: kind, Name: name, NameKey: key, Ext: Extension(name), State: "active", Version: 1, ManagementMode: "user", CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}
	if kind == "directory" {
		row.Ext = ""
	}
	q := query.Bind(tx, t.prefix)
	// Take the space write lock before reads (including on SQLite), and
	// advance the revision future operations use for conflict checks.
	result, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).UpdateSimple(q.Space.Version.Add(1))
	if err != nil {
		return nil, err
	}
	if result.RowsAffected != 1 {
		return nil, ErrNotFound
	}
	if _, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(spaceID), q.Node.ID.Eq(parentID), q.Node.Kind.Eq("directory"), q.Node.State.Eq("active")).Take(); err != nil {
		return nil, err
	}
	conflicts, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(spaceID), q.Node.ParentID.Eq(parentID), q.Node.NameKey.Eq(key), q.Node.LiveSlot.Eq(1)).Count()
	if err != nil {
		return nil, err
	}
	if conflicts != 0 {
		return nil, errTreeConflict
	}
	closure := q.NodeClosure
	ancestors, err := closure.WithContext(ctx).Where(closure.SpaceID.Eq(spaceID), closure.DescendantID.Eq(parentID)).Order(closure.Depth.Asc()).Limit(maxNodeDepth + 1).Find()
	if err != nil {
		return nil, err
	}
	if len(ancestors) == 0 || len(ancestors) > maxNodeDepth || ancestors[0].AncestorID != parentID {
		return nil, errHierarchy
	}
	links := make([]*model.NodeClosure, 0, len(ancestors)+1)
	links = append(links, &model.NodeClosure{SpaceID: spaceID, AncestorID: id, DescendantID: id, Depth: 0})
	for depth, ancestor := range ancestors {
		if ancestor.Depth != depth {
			return nil, errHierarchy
		}
		links = append(links, &model.NodeClosure{SpaceID: spaceID, AncestorID: ancestor.AncestorID, DescendantID: id, Depth: depth + 1})
	}
	if err := q.Node.WithContext(ctx).Create(&row); err != nil {
		return nil, err
	}
	if err := closure.WithContext(ctx).Create(links...); err != nil {
		return nil, err
	}
	return &row, nil
}

func (t directoryTree) children(ctx context.Context, db *gorm.DB, spaceID, parentID guid.ID, kind string, limit, offset int) ([]*model.Node, error) {
	if db == nil || spaceID <= 0 || parentID <= 0 || (kind != "" && kind != "file" && kind != "directory") || limit < 1 || limit > 200 || offset < 0 {
		return nil, errHierarchy
	}
	q := query.Bind(db, t.prefix)
	node := q.Node
	if _, err := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ID.Eq(parentID), node.Kind.Eq("directory"), node.State.Eq("active")).Take(); err != nil {
		return nil, err
	}
	children := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ParentID.Eq(parentID), node.State.Eq("active"))
	if kind != "" {
		children = children.Where(node.Kind.Eq(kind))
	}
	return children.Order(node.NameKey.Asc(), node.ID.Asc()).Limit(limit).Offset(offset).Find()
}

func (t directoryTree) renameNodeOn(ctx context.Context, tx *gorm.DB, spaceID, nodeID guid.ID, expectedVersion int64, input string, now time.Time) (*model.Node, error) {
	if tx == nil || spaceID <= 0 || nodeID <= 0 || expectedVersion <= 0 {
		return nil, errHierarchy
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return nil, ErrAccountTransaction
	}
	name, key, err := PortableName(input)
	if err != nil {
		return nil, err
	}
	q := query.Bind(tx, t.prefix)
	result, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).UpdateSimple(q.Space.Version.Add(1))
	if err != nil {
		return nil, err
	}
	if result.RowsAffected != 1 {
		return nil, ErrNotFound
	}
	node := q.Node
	row, err := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ID.Eq(nodeID), node.State.Eq("active")).Take()
	if err != nil {
		return nil, err
	}
	if row.ParentID == nil || row.RootScope != nil || row.Version != expectedVersion {
		return nil, errTreeConflict
	}
	conflicts, err := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ParentID.Eq(*row.ParentID), node.NameKey.Eq(key), node.LiveSlot.Eq(1), node.ID.Neq(nodeID)).Count()
	if err != nil {
		return nil, err
	}
	if conflicts != 0 {
		return nil, errTreeConflict
	}
	extension := ""
	if row.Kind == "file" {
		extension = Extension(name)
	}
	result, err = node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ID.Eq(nodeID), node.Version.Eq(expectedVersion), node.State.Eq("active")).UpdateSimple(
		node.Name.Value(name), node.NameKey.Value(key), node.Ext.Value(extension), node.Version.Add(1), node.UpdatedAt.Value(now.UnixMilli()),
	)
	if err != nil {
		return nil, err
	}
	if result.RowsAffected != 1 {
		return nil, errTreeConflict
	}
	row.Name, row.NameKey, row.Ext, row.Version, row.UpdatedAt = name, key, extension, row.Version+1, now.UnixMilli()
	return row, nil
}

func (t directoryTree) resolvePath(ctx context.Context, db *gorm.DB, spaceID guid.ID, path string) (*model.Node, error) {
	if db == nil || spaceID <= 0 || !strings.HasPrefix(path, "/") {
		return nil, errHierarchy
	}
	var segments []string
	if path != "/" {
		segments = strings.Split(strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/"), "/")
	}
	if len(segments) > maxNodeDepth {
		return nil, errHierarchy
	}
	keys := make([]string, len(segments))
	for i, segment := range segments {
		_, key, err := PortableName(segment)
		if err != nil {
			return nil, err
		}
		keys[i] = key
	}
	q := query.Bind(db, t.prefix)
	node := q.Node
	row, err := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ParentID.IsNull(), node.RootSlot.Eq(1), node.State.Eq("active")).Take()
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if row.Kind != "directory" {
			return nil, ErrNotFound
		}
		row, err = node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ParentID.Eq(row.ID), node.NameKey.Eq(key), node.LiveSlot.Eq(1)).Take()
		if err != nil {
			return nil, err
		}
	}
	return row, nil
}

func (t directoryTree) pathNodes(ctx context.Context, db *gorm.DB, spaceID, id guid.ID) ([]*model.Node, error) {
	if db == nil || spaceID <= 0 || id <= 0 {
		return nil, errHierarchy
	}
	q := query.Bind(db, t.prefix)
	closure := q.NodeClosure
	node := q.Node
	rows, err := node.WithContext(ctx).Join(closure, closure.SpaceID.EqCol(node.SpaceID), closure.AncestorID.EqCol(node.ID)).Where(closure.SpaceID.Eq(spaceID), closure.DescendantID.Eq(id), node.State.Eq("active")).Order(closure.Depth.Desc()).Limit(maxNodeDepth + 2).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) > maxNodeDepth+1 {
		return nil, errHierarchy
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].ParentID == nil || *rows[i].ParentID != rows[i-1].ID {
			return nil, errHierarchy
		}
	}
	if rows[0].ParentID != nil || rows[len(rows)-1].ID != id {
		return nil, errHierarchy
	}
	return rows, nil
}

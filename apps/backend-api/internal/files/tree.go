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
)

const maxNodeDepth = 64

var errHierarchy = errors.New("files: invalid or over-depth directory hierarchy")

// directoryTree is persistence only. HTTP/services must authorize the space and
// every exposed node; tenant ownership alone never grants access to this tree.
type directoryTree struct{ prefix string }

func (t directoryTree) createSpace(ctx context.Context, db *gorm.DB, id, rootID, ownerID guid.ID, scope string, now time.Time) error {
	if db == nil || id <= 0 || rootID <= 0 || ownerID <= 0 || (scope != "tenant" && scope != "account") {
		return errHierarchy
	}
	space := model.Space{ID: id, ScopeKind: scope, Version: 1, CreatedAt: now.UnixMilli()}
	if scope == "tenant" {
		space.TenantID = &ownerID
	} else {
		space.PrincipalID = &ownerID
	}
	slot := 1
	root := model.Node{ID: rootID, SpaceID: id, ParentKind: "directory", RootSlot: &slot, LiveSlot: &slot, Kind: "directory", State: "active", Version: 1, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.Bind(tx, t.prefix)
		if err := q.Space.WithContext(ctx).Create(&space); err != nil {
			return err
		}
		if err := q.Node.WithContext(ctx).Create(&root); err != nil {
			return err
		}
		return q.NodeClosure.WithContext(ctx).Create(&model.NodeClosure{SpaceID: id, AncestorID: rootID, DescendantID: rootID, Depth: 0})
	})
}

func (t directoryTree) createDirectory(ctx context.Context, db *gorm.DB, spaceID, id, parentID guid.ID, input string, now time.Time) (*model.Node, error) {
	if db == nil || spaceID <= 0 || id <= 0 || parentID <= 0 {
		return nil, errHierarchy
	}
	name, key, err := PortableName(input)
	if err != nil {
		return nil, err
	}
	slot := 1
	row := model.Node{ID: id, SpaceID: spaceID, ParentID: &parentID, ParentKind: "directory", LiveSlot: &slot, Kind: "directory", Name: name, NameKey: key, State: "active", Version: 1, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.Bind(tx, t.prefix)
		// Take the space write lock before reads (including on SQLite), and
		// advance the revision future operation plans use for conflict checks.
		result, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).UpdateSimple(q.Space.Version.Add(1))
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return ErrNotFound
		}
		if _, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(spaceID), q.Node.ID.Eq(parentID), q.Node.Kind.Eq("directory"), q.Node.State.Eq("active")).Take(); err != nil {
			return err
		}
		closure := q.NodeClosure
		ancestors, err := closure.WithContext(ctx).Where(closure.SpaceID.Eq(spaceID), closure.DescendantID.Eq(parentID)).Order(closure.Depth.Asc()).Limit(maxNodeDepth + 1).Find()
		if err != nil {
			return err
		}
		if len(ancestors) == 0 || len(ancestors) > maxNodeDepth || ancestors[0].AncestorID != parentID {
			return errHierarchy
		}
		links := make([]*model.NodeClosure, 0, len(ancestors)+1)
		links = append(links, &model.NodeClosure{SpaceID: spaceID, AncestorID: id, DescendantID: id, Depth: 0})
		for depth, ancestor := range ancestors {
			if ancestor.Depth != depth {
				return errHierarchy
			}
			links = append(links, &model.NodeClosure{SpaceID: spaceID, AncestorID: ancestor.AncestorID, DescendantID: id, Depth: depth + 1})
		}
		if err := q.Node.WithContext(ctx).Create(&row); err != nil {
			return err
		}
		return closure.WithContext(ctx).Create(links...)
	})
	if err != nil {
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

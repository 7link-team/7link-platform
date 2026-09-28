package files

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"sync"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

var (
	errResourcePurpose         = errors.New("files: invalid resource purpose")
	errResourceBinding         = errors.New("files: invalid resource binding")
	errResourceBindingConflict = errors.New("files: resource binding conflict")
	resourceBindingName        = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

type ResourcePurpose struct {
	Name             string
	ResourceKind     string
	OwnerScopes      []string
	VersionMode      string
	AllowExpiry      bool
	ValidateResource func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error
}

// ResourceOwner is derived from the file's verified root. Business modules use
// it to prove that their resource belongs to this exact, narrowest owner.
type ResourceOwner struct {
	Scope string
	ID    guid.ID
}

type ResourceBindingInput struct {
	ID                  guid.ID
	SpaceID             guid.ID
	FileID              guid.ID
	ExpectedFileVersion int64
	VersionID           *guid.ID
	Purpose             string
	ResourceKind        string
	ResourceID          guid.ID
	Slot                string
	ExpiresAt           *time.Time
	ActorID             guid.ID
}

type ResourceBindings struct {
	prefix   string
	mu       sync.RWMutex
	purposes map[string]ResourcePurpose
}

func NewResourceBindings(prefix string) (*ResourceBindings, error) {
	if err := goosex.ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	return &ResourceBindings{prefix: prefix, purposes: make(map[string]ResourcePurpose)}, nil
}

func (r *ResourceBindings) RegisterPurpose(purpose ResourcePurpose) error {
	if r == nil || !validPurpose(purpose) {
		return errResourcePurpose
	}
	purpose.OwnerScopes = append([]string(nil), purpose.OwnerScopes...)
	slices.Sort(purpose.OwnerScopes)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.purposes[purpose.Name]; exists {
		return errResourcePurpose
	}
	r.purposes[purpose.Name] = purpose
	return nil
}

func validPurpose(purpose ResourcePurpose) bool {
	if !resourceBindingName.MatchString(purpose.Name) || !resourceBindingName.MatchString(purpose.ResourceKind) || len(purpose.OwnerScopes) == 0 || len(purpose.OwnerScopes) > 6 || (purpose.VersionMode != "head" && purpose.VersionMode != "pinned") || purpose.ValidateResource == nil {
		return false
	}
	seen := make(map[string]struct{}, len(purpose.OwnerScopes))
	for _, scope := range purpose.OwnerScopes {
		switch scope {
		case "system", ownerTenant, ownerEntity, ownerDepartment, ownerAccount:
		default:
			return false
		}
		if _, duplicate := seen[scope]; duplicate {
			return false
		}
		seen[scope] = struct{}{}
	}
	return true
}

func (r *ResourceBindings) purpose(name string) (ResourcePurpose, bool) {
	if r == nil {
		return ResourcePurpose{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	purpose, ok := r.purposes[name]
	return purpose, ok
}

func (r *ResourceBindings) Bind(ctx context.Context, tx *gorm.DB, input ResourceBindingInput, now time.Time) (*model.ResourceBinding, error) {
	if tx == nil || input.ID <= 0 || input.SpaceID <= 0 || input.FileID <= 0 || input.ExpectedFileVersion <= 0 || input.ResourceID <= 0 || input.ActorID <= 0 || !resourceBindingName.MatchString(input.Slot) {
		return nil, errResourceBinding
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return nil, ErrAccountTransaction
	}
	purpose, ok := r.purpose(input.Purpose)
	if !ok || purpose.ResourceKind != input.ResourceKind || (purpose.VersionMode == "head") != (input.VersionID == nil) || (!purpose.AllowExpiry && input.ExpiresAt != nil) || (input.ExpiresAt != nil && !input.ExpiresAt.After(now)) {
		return nil, errResourcePurpose
	}

	tree := directoryTree{prefix: r.prefix}
	q, _, err := tree.lockOperationSpace(ctx, tx, input.SpaceID)
	if err != nil {
		return nil, err
	}
	node := q.Node
	file, err := node.WithContext(ctx).Where(
		node.SpaceID.Eq(input.SpaceID), node.ID.Eq(input.FileID), node.Kind.Eq("file"),
		node.State.Eq("active"), node.Version.Eq(input.ExpectedFileVersion),
	).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errResourceBindingConflict
	}
	if err != nil {
		return nil, err
	}
	owner, err := tree.resourceOwner(ctx, tx, input.SpaceID, input.FileID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(purpose.OwnerScopes, owner.Scope) {
		return nil, errResourcePurpose
	}
	if err := purpose.ValidateResource(ctx, tx, input.ResourceID, owner); err != nil {
		return nil, errors.Join(errResourceBinding, err)
	}
	versionID := file.CurrentVersionID
	if input.VersionID != nil {
		versionID = input.VersionID
	}
	if versionID == nil {
		return nil, errResourceBinding
	}
	version := q.Version
	if _, err := version.WithContext(ctx).Where(
		version.SpaceID.Eq(input.SpaceID), version.NodeID.Eq(input.FileID), version.ID.Eq(*versionID),
	).Take(); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errResourceBinding
		}
		return nil, err
	}

	live := 1
	row := &model.ResourceBinding{
		ID: input.ID, SpaceID: input.SpaceID, FileID: input.FileID, FileKind: "file",
		VersionID: input.VersionID, Purpose: input.Purpose, ResourceKind: input.ResourceKind,
		ResourceID: input.ResourceID, Slot: input.Slot, State: "active", LiveSlot: &live,
		Version: 1, CreatedBy: storageActorID(ctx, input.ActorID), CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(),
	}
	if input.ExpiresAt != nil {
		expiresAt := input.ExpiresAt.UnixMilli()
		row.ExpiresAt = &expiresAt
	}
	if err := q.ResourceBinding.WithContext(ctx).Create(row); err != nil {
		return nil, errors.Join(errResourceBindingConflict, err)
	}
	updated, err := node.WithContext(ctx).Where(
		node.SpaceID.Eq(input.SpaceID), node.ID.Eq(input.FileID), node.Version.Eq(file.Version),
	).UpdateSimple(node.ManagementMode.Value("resource"), node.Version.Add(1), node.UpdatedAt.Value(now.UnixMilli()))
	if err != nil {
		return nil, err
	}
	if updated.RowsAffected != 1 {
		return nil, errResourceBindingConflict
	}
	if _, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(input.SpaceID)).UpdateSimple(q.Space.Version.Add(1)); err != nil {
		return nil, err
	}
	return row, nil
}

func (r *ResourceBindings) Unbind(ctx context.Context, tx *gorm.DB, spaceID, id guid.ID, expectedVersion int64, now time.Time) error {
	return r.transition(ctx, tx, spaceID, id, expectedVersion, "inactive", now)
}

func (r *ResourceBindings) transition(ctx context.Context, tx *gorm.DB, spaceID, id guid.ID, expectedVersion int64, state string, now time.Time) error {
	if r == nil || tx == nil || spaceID <= 0 || id <= 0 || expectedVersion <= 0 {
		return errResourceBinding
	}
	if state != "inactive" && state != "expired" {
		return errResourceBinding
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return ErrAccountTransaction
	}
	tree := directoryTree{prefix: r.prefix}
	q, _, err := tree.lockOperationSpace(ctx, tx, spaceID)
	if err != nil {
		return err
	}
	binding := q.ResourceBinding
	updated, err := binding.WithContext(ctx).Where(
		binding.SpaceID.Eq(spaceID), binding.ID.Eq(id), binding.State.Eq("active"), binding.Version.Eq(expectedVersion),
	).UpdateSimple(
		binding.State.Value(state), binding.LiveSlot.Null(), binding.Version.Add(1), binding.UpdatedAt.Value(now.UnixMilli()),
	)
	if err != nil {
		return err
	}
	if updated.RowsAffected != 1 {
		return errResourceBindingConflict
	}
	_, err = q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).UpdateSimple(q.Space.Version.Add(1))
	return err
}

// Expire deactivates one bounded keyset page. Each binding uses the same Space
// fence as an explicit unbind; concurrent changes are already complete work.
func (r *ResourceBindings) Expire(ctx context.Context, db *gorm.DB, now time.Time, after guid.ID, limit int) (guid.ID, error) {
	if r == nil || db == nil || after < 0 || limit < 1 || limit > 1000 {
		return after, errResourceBinding
	}
	q := query.Bind(db, r.prefix)
	binding := q.ResourceBinding
	rows, err := binding.WithContext(ctx).Where(
		binding.State.Eq("active"), binding.ExpiresAt.IsNotNull(), binding.ExpiresAt.Lte(now.UnixMilli()), binding.ID.Gt(after),
	).Order(binding.ID.Asc()).Limit(limit).Find()
	if err != nil {
		return after, err
	}
	var errs []error
	for _, row := range rows {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return r.transition(ctx, tx, row.SpaceID, row.ID, row.Version, "expired", now)
		})
		if err != nil && !errors.Is(err, errResourceBindingConflict) {
			errs = append(errs, err)
		}
	}
	if len(rows) < limit {
		return 0, errors.Join(errs...)
	}
	return rows[len(rows)-1].ID, errors.Join(errs...)
}

func (r *ResourceBindings) OrphanedFiles(ctx context.Context, db *gorm.DB, after guid.ID, limit int) ([]*model.Node, error) {
	if r == nil || db == nil || after < 0 || limit < 1 || limit > 1000 {
		return nil, errResourceBinding
	}
	names := query.BindNames(r.prefix)
	rows := make([]*model.Node, 0, limit)
	err := db.WithContext(ctx).Table(names.Nodes.String()+" AS n").
		Select("n.*").
		Joins("LEFT JOIN "+names.ResourceBindings.String()+" AS b ON b.space_id = n.space_id AND b.file_id = n.id AND b.state = ?", "active").
		Where("n.management_mode = ? AND n.kind = ? AND n.state = ? AND n.id > ? AND b.id IS NULL", "resource", "file", "active", after).
		Order("n.id ASC").Limit(limit).Scan(&rows).Error
	return rows, err
}

func (t directoryTree) resourceOwner(ctx context.Context, tx *gorm.DB, spaceID, nodeID guid.ID) (ResourceOwner, error) {
	q := query.Bind(tx, t.prefix)
	space, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID)).Take()
	if err != nil {
		return ResourceOwner{}, err
	}
	path, err := t.ownerPathNodes(ctx, tx, spaceID, nodeID)
	if err != nil {
		return ResourceOwner{}, err
	}
	owner := ResourceOwner{}
	for _, node := range path {
		binding, err := nodeRootBinding(space, node)
		if err != nil {
			return ResourceOwner{}, err
		}
		if binding.scope != "" {
			owner = ResourceOwner{Scope: binding.scope, ID: binding.ownerID}
		}
	}
	if owner.Scope == "" || (owner.Scope != "system" && owner.ID <= 0) {
		return ResourceOwner{}, errRootUnbound
	}
	return owner, nil
}

func (t directoryTree) ownerPathNodes(ctx context.Context, db *gorm.DB, spaceID, id guid.ID) ([]*model.Node, error) {
	if db == nil || spaceID <= 0 || id <= 0 {
		return nil, errHierarchy
	}
	q := query.Bind(db, t.prefix)
	closure := q.NodeClosure
	node := q.Node
	rows, err := node.WithContext(ctx).
		Join(closure, closure.SpaceID.EqCol(node.SpaceID), closure.AncestorID.EqCol(node.ID)).
		Where(closure.SpaceID.Eq(spaceID), closure.DescendantID.Eq(id)).
		Order(closure.Depth.Desc()).Limit(maxNodeDepth + 2).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) > maxNodeDepth+1 {
		return nil, errHierarchy
	}
	for index := 1; index < len(rows); index++ {
		if rows[index].ParentID == nil || *rows[index].ParentID != rows[index-1].ID {
			return nil, errHierarchy
		}
	}
	if rows[0].ParentID != nil || rows[len(rows)-1].ID != id {
		return nil, errHierarchy
	}
	return rows, nil
}

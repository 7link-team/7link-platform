package files

import (
	"context"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

const (
	defaultBrowserPage = 50
	maxBrowserPage     = 100
	maxBrowserScan     = 1000
)

type browserPageRequest struct {
	After guid.ID
	Limit int
}

type fileCollection struct {
	scope    string
	tenantID guid.ID
	ownerID  guid.ID
}

type filePage struct {
	Items     []fileEntryView `json:"items"`
	NextAfter *guid.ID        `json:"next_after,omitempty"`
}

type filePathView struct {
	ID   guid.ID `json:"id"`
	Name string  `json:"name"`
}

type fileEntryView struct {
	ID           guid.ID        `json:"id"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	Size         *int64         `json:"size,omitempty"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DetectedType string         `json:"detected_type,omitempty"`
	Extension    string         `json:"extension,omitempty"`
	Version      int64          `json:"version,omitempty"`
	Revision     int64          `json:"revision"`
	Scope        string         `json:"scope,omitempty"`
	TenantID     *guid.ID       `json:"tenant_id,omitempty"`
	EntityID     *guid.ID       `json:"entity_id,omitempty"`
	DepartmentID *guid.ID       `json:"department_id,omitempty"`
	PrincipalID  *guid.ID       `json:"principal_id,omitempty"`
	RestorePath  []filePathView `json:"restore_path,omitempty"`
}

type fileChildrenPage struct {
	Path      []filePathView  `json:"path"`
	Items     []fileEntryView `json:"items"`
	NextAfter *guid.ID        `json:"next_after,omitempty"`
}

func normalizeBrowserPage(request browserPageRequest) (browserPageRequest, error) {
	if request.After < 0 || request.Limit < 0 || request.Limit > maxBrowserPage {
		return browserPageRequest{}, errOperationInput
	}
	if request.Limit == 0 {
		request.Limit = defaultBrowserPage
	}
	return request, nil
}

func (s *operationService) listRoots(ctx context.Context, collection fileCollection, request browserPageRequest) (filePage, error) {
	var result filePage
	request, err := normalizeBrowserPage(request)
	if err != nil {
		return result, err
	}
	claims, err := s.browserClaims(ctx)
	if err != nil {
		return result, err
	}
	workContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	err = s.runtime.Database().Write().WithContext(workContext).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(workContext, tx, claims, tree)
		if err != nil {
			return err
		}
		if err := collection.validate(claims); err != nil {
			return err
		}
		result, err = tree.authorizedRoots(workContext, tx, authorizer, collection, request)
		return err
	})
	if err != nil {
		return filePage{}, err
	}
	return result, nil
}

func (collection fileCollection) validate(claims *authn.Claims) error {
	if claims == nil || collection.ownerID <= 0 {
		return errOperationInput
	}
	switch collection.scope {
	case ownerAccount:
		if collection.tenantID != 0 || claims.TenantID != 0 || claims.EntityID != 0 || collection.ownerID != claims.PrincipalID {
			return errOperationForbidden
		}
	case ownerTenant:
		if collection.tenantID != collection.ownerID || claims.TenantID != collection.tenantID {
			return errOperationForbidden
		}
	case ownerEntity, ownerDepartment:
		if collection.tenantID <= 0 || claims.TenantID != collection.tenantID || claims.EntityID <= 0 {
			return errOperationForbidden
		}
	default:
		return errOperationInput
	}
	return nil
}

func (s *operationService) listTrash(ctx context.Context, collection fileCollection, request browserPageRequest) (filePage, error) {
	var result filePage
	request, err := normalizeBrowserPage(request)
	if err != nil {
		return result, err
	}
	claims, err := s.browserClaims(ctx)
	if err != nil {
		return result, err
	}
	workContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	err = s.runtime.Database().Write().WithContext(workContext).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(workContext, tx, claims, tree)
		if err != nil {
			return err
		}
		if err := collection.validate(claims); err != nil {
			return err
		}
		result, err = tree.authorizedTrash(workContext, tx, authorizer, collection, request)
		return err
	})
	if err != nil {
		return filePage{}, err
	}
	return result, nil
}

func (s *operationService) listChildren(ctx context.Context, nodeID guid.ID, request browserPageRequest) (fileChildrenPage, error) {
	var result fileChildrenPage
	request, err := normalizeBrowserPage(request)
	if err != nil || nodeID <= 0 {
		return result, errOperationInput
	}
	claims, err := s.browserClaims(ctx)
	if err != nil {
		return result, err
	}
	workContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	err = s.runtime.Database().Write().WithContext(workContext).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(workContext, tx, claims, tree)
		if err != nil {
			return err
		}
		node := query.Bind(tx, tree.prefix).Node
		row, err := node.WithContext(workContext).Where(node.ID.Eq(nodeID), node.State.Eq("active")).Take()
		if err != nil {
			return err
		}
		result, err = tree.authorizedChildren(workContext, tx, authorizer, row.SpaceID, nodeID, request)
		return err
	})
	if err != nil {
		return fileChildrenPage{}, err
	}
	return result, nil
}

func (s *operationService) getFileEntry(ctx context.Context, nodeID guid.ID) (fileEntryView, error) {
	return s.readFileEntry(ctx, nodeID, "")
}

func (s *operationService) getFileEntryByPath(ctx context.Context, rootID guid.ID, rawPath string) (fileEntryView, error) {
	return s.readFileEntry(ctx, rootID, rawPath)
}

func (s *operationService) readFileEntry(ctx context.Context, rootID guid.ID, rawPath string) (fileEntryView, error) {
	var result fileEntryView
	if rootID <= 0 {
		return result, errOperationInput
	}
	segments, err := filePathSegments(rawPath)
	if err != nil {
		return result, err
	}
	claims, err := s.browserClaims(ctx)
	if err != nil {
		return result, err
	}
	workContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	err = s.runtime.Database().Write().WithContext(workContext).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(workContext, tx, claims, tree)
		if err != nil {
			return err
		}
		row, err := activeNode(workContext, tx, tree.prefix, rootID)
		if err != nil {
			return err
		}
		if rawPath != "" && (row.Kind != "directory" || row.RootScope == nil) {
			return ErrNotFound
		}
		if err := authorizer.access(workContext, tx, row.SpaceID, claims.PrincipalID, row.ID, "traverse"); err != nil {
			return err
		}
		q := query.Bind(tx, tree.prefix)
		for _, key := range segments {
			if row.Kind != "directory" {
				return ErrNotFound
			}
			row, err = q.Node.WithContext(workContext).Where(
				q.Node.SpaceID.Eq(row.SpaceID), q.Node.ParentID.Eq(row.ID),
				q.Node.NameKey.Eq(key), q.Node.LiveSlot.Eq(1), q.Node.State.Eq("active"),
			).Take()
			if err != nil {
				return err
			}
			if err := authorizer.access(workContext, tx, row.SpaceID, claims.PrincipalID, row.ID, "traverse"); err != nil {
				return err
			}
		}
		if err := authorizer.access(workContext, tx, row.SpaceID, claims.PrincipalID, row.ID, "read"); err != nil {
			return err
		}
		result, err = fileEntryViewOf(workContext, q, row)
		return err
	})
	if err != nil {
		return fileEntryView{}, err
	}
	return result, nil
}

func filePathSegments(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "/") || strings.HasSuffix(raw, "/") || strings.Contains(raw, "\\") {
		return nil, errOperationInput
	}
	parts := strings.Split(raw, "/")
	if len(parts) == 0 || len(parts) > maxNodeDepth {
		return nil, errOperationInput
	}
	keys := make([]string, len(parts))
	for index, part := range parts {
		if part == "" {
			return nil, errOperationInput
		}
		_, key, err := PortableName(part)
		if err != nil {
			return nil, errOperationInput
		}
		keys[index] = key
	}
	return keys, nil
}

func (s *operationService) browserClaims(ctx context.Context) (*authn.Claims, error) {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID <= 0 ||
		!((claims.TenantID > 0 && claims.EntityID >= 0) || (claims.TenantID == 0 && claims.EntityID == 0)) {
		return nil, errOperationForbidden
	}
	if s == nil || s.runtime == nil || s.runtime.Database() == nil || s.runtime.Database().Write() == nil || s.timeout <= 0 {
		return nil, errOperationUnavailable
	}
	if claims.TenantID > 0 && claims.EntityID == 0 && (s.tenantPolicy == nil || s.references == nil) {
		return nil, errOperationUnavailable
	}
	if claims.EntityID > 0 && (s.policy == nil || s.subjects == nil || s.references == nil) {
		return nil, errOperationUnavailable
	}
	return claims, nil
}

func (t directoryTree) authorizedRoots(ctx context.Context, tx *gorm.DB, authorizer *nodeAuthorizer, collection fileCollection, request browserPageRequest) (filePage, error) {
	result := filePage{Items: []fileEntryView{}}
	q := query.Bind(tx, t.prefix)
	if collection.ownerID <= 0 {
		return filePage{}, errOperationInput
	}
	spaceQuery := q.Space.WithContext(ctx)
	if collection.scope == ownerAccount {
		spaceQuery = spaceQuery.Where(q.Space.ScopeKind.Eq(spaceAccount), q.Space.PrincipalID.Eq(collection.ownerID))
	} else {
		spaceQuery = spaceQuery.Where(q.Space.ScopeKind.Eq(spaceTenant), q.Space.TenantID.Eq(collection.tenantID))
	}
	space, err := spaceQuery.Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return filePage{}, err
	}
	after, scanned, exhausted := request.After, 0, false
	for len(result.Items) <= request.Limit && scanned < maxBrowserScan {
		batch := min(100, maxBrowserScan-scanned)
		rows, err := q.Node.WithContext(ctx).Where(
			q.Node.SpaceID.Eq(space.ID), q.Node.RootScope.Eq(collection.scope),
			q.Node.RootOwnerID.Eq(collection.ownerID), q.Node.State.Eq("active"), q.Node.ID.Gt(after),
		).Order(q.Node.ID.Asc()).Limit(batch).Find()
		if err != nil {
			return filePage{}, err
		}
		if len(rows) == 0 {
			exhausted = true
			break
		}
		scanned += len(rows)
		after = rows[len(rows)-1].ID
		ids := make([]guid.ID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		traversable, err := authorizer.accessMany(ctx, tx, space.ID, authorizer.actorID, ids, "traverse")
		if err != nil {
			return filePage{}, err
		}
		listable, err := authorizer.accessMany(ctx, tx, space.ID, authorizer.actorID, ids, "list")
		if err != nil {
			return filePage{}, err
		}
		visible := make([]*model.Node, 0, len(rows))
		for _, row := range rows {
			if traversable[row.ID] && listable[row.ID] {
				visible = append(visible, row)
			}
		}
		views, err := fileEntryViewsOf(ctx, q, visible, space)
		if err != nil {
			return filePage{}, err
		}
		for _, view := range views {
			result.Items = append(result.Items, view)
			if len(result.Items) > request.Limit {
				break
			}
		}
		if len(rows) < batch {
			exhausted = true
			break
		}
	}
	if len(result.Items) > request.Limit {
		next := result.Items[request.Limit-1].ID
		result.Items, result.NextAfter = result.Items[:request.Limit], &next
	} else if !exhausted && scanned >= maxBrowserScan {
		next := after
		result.NextAfter = &next
	}
	return result, nil
}

func (t directoryTree) authorizedTrash(ctx context.Context, tx *gorm.DB, authorizer *nodeAuthorizer, collection fileCollection, request browserPageRequest) (filePage, error) {
	result := filePage{Items: []fileEntryView{}}
	q := query.Bind(tx, t.prefix)
	if collection.ownerID <= 0 {
		return filePage{}, errOperationInput
	}
	spaceQuery := q.Space.WithContext(ctx)
	if collection.scope == ownerAccount {
		spaceQuery = spaceQuery.Where(q.Space.ScopeKind.Eq(spaceAccount), q.Space.PrincipalID.Eq(collection.ownerID))
	} else {
		spaceQuery = spaceQuery.Where(q.Space.ScopeKind.Eq(spaceTenant), q.Space.TenantID.Eq(collection.tenantID))
	}
	space, err := spaceQuery.Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return filePage{}, err
	}
	after, scanned, exhausted := request.After, 0, false
	for len(result.Items) <= request.Limit && scanned < maxBrowserScan {
		batch := min(100, maxBrowserScan-scanned)
		rows, err := q.Node.WithContext(ctx).
			Join(q.TrashRoot,
				q.TrashRoot.SpaceID.EqCol(q.Node.SpaceID),
				q.TrashRoot.NodeID.EqCol(q.Node.ID),
				q.TrashRoot.BatchID.EqCol(q.Node.TrashBatchID),
			).
			Where(q.Node.SpaceID.Eq(space.ID), q.Node.State.Eq("trashed"), q.Node.ID.Gt(after)).
			Order(q.Node.ID.Asc()).Limit(batch).Find()
		if err != nil {
			return filePage{}, err
		}
		if len(rows) == 0 {
			exhausted = true
			break
		}
		scanned += len(rows)
		after = rows[len(rows)-1].ID
		rowIDs := make([]guid.ID, 0, len(rows))
		for _, row := range rows {
			rowIDs = append(rowIDs, row.ID)
		}
		owners, err := t.resourceOwners(ctx, tx, space, rowIDs)
		if err != nil {
			return filePage{}, err
		}
		ids := make([]guid.ID, 0, len(rows))
		owned := make(map[guid.ID]bool, len(rows))
		for _, row := range rows {
			owner, ok := owners[row.ID]
			if !ok {
				return filePage{}, errRootUnbound
			}
			if owner.Scope == collection.scope && owner.ID == collection.ownerID {
				ids = append(ids, row.ID)
				owned[row.ID] = true
			}
		}
		if len(ids) == 0 {
			continue
		}
		readable, err := authorizer.accessMany(ctx, tx, space.ID, authorizer.actorID, ids, "read")
		if err != nil {
			return filePage{}, err
		}
		visible := make([]*model.Node, 0, len(rows))
		for _, row := range rows {
			if owned[row.ID] && readable[row.ID] {
				visible = append(visible, row)
			}
		}
		views, err := fileEntryViewsOf(ctx, q, visible, nil)
		if err != nil {
			return filePage{}, err
		}
		if err := t.attachAuthorizedRestorePaths(ctx, tx, authorizer, space.ID, visible, views); err != nil {
			return filePage{}, err
		}
		result.Items = append(result.Items, views...)
		if len(rows) < batch {
			exhausted = true
			break
		}
	}
	if len(result.Items) > request.Limit {
		next := result.Items[request.Limit-1].ID
		result.Items, result.NextAfter = result.Items[:request.Limit], &next
	} else if !exhausted && scanned >= maxBrowserScan {
		next := after
		result.NextAfter = &next
	}
	return result, nil
}

func (t directoryTree) resourceOwners(ctx context.Context, tx *gorm.DB, space *model.Space, nodeIDs []guid.ID) (map[guid.ID]ResourceOwner, error) {
	result := make(map[guid.ID]ResourceOwner, len(nodeIDs))
	if tx == nil || space == nil || len(nodeIDs) == 0 {
		return result, nil
	}
	type ownerRow struct {
		DescendantID guid.ID `gorm:"column:descendant_id"`
		model.Node
		Depth int `gorm:"column:owner_depth"`
	}
	names := query.BindNames(t.prefix)
	rows := make([]ownerRow, 0, len(nodeIDs))
	err := tx.WithContext(ctx).Table(names.NodeClosure.String()+" AS c").
		Select("c.descendant_id, c.depth AS owner_depth, r.*").
		Joins("JOIN "+names.Nodes.String()+" AS r ON r.space_id = c.space_id AND r.id = c.ancestor_id").
		Where("c.space_id = ? AND c.descendant_id IN ? AND r.root_scope IS NOT NULL", space.ID, nodeIDs).
		Order("c.descendant_id ASC, c.depth ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for index := len(rows) - 1; index >= 0; index-- {
		binding, err := nodeRootBinding(space, &rows[index].Node)
		if err != nil {
			return nil, err
		}
		result[rows[index].DescendantID] = ResourceOwner{Scope: binding.scope, ID: binding.ownerID}
	}
	return result, nil
}

func (t directoryTree) attachAuthorizedRestorePaths(
	ctx context.Context,
	tx *gorm.DB,
	authorizer *nodeAuthorizer,
	spaceID guid.ID,
	rows []*model.Node,
	views []fileEntryView,
) error {
	if len(rows) == 0 {
		return nil
	}
	if tx == nil || authorizer == nil || spaceID <= 0 || len(rows) != len(views) || len(rows) > maxBrowserPage {
		return errFileACL
	}
	targetIDs := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		if row == nil || row.SpaceID != spaceID || row.State != "trashed" || row.TrashBatchID == nil {
			return errFileACL
		}
		targetIDs = append(targetIDs, row.ID)
	}

	q := query.Bind(tx, t.prefix)
	batchIDs := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		batchIDs = append(batchIDs, *row.TrashBatchID)
	}
	placementLimit := len(targetIDs)*len(batchIDs) + 1
	placements, err := q.TrashRoot.WithContext(ctx).Where(
		q.TrashRoot.SpaceID.Eq(spaceID),
		q.TrashRoot.NodeID.In(fileIDValues(targetIDs)...),
		q.TrashRoot.BatchID.In(fileIDValues(batchIDs)...),
	).Limit(placementLimit).Find()
	if err != nil {
		return err
	}
	if len(placements) == placementLimit {
		return errHierarchy
	}
	currentByID := make(map[guid.ID]*model.Node, len(rows))
	for _, row := range rows {
		currentByID[row.ID] = row
	}
	placementByTarget := make(map[guid.ID]*model.TrashRoot, len(rows))
	parentSet := make(map[guid.ID]struct{}, len(rows))
	for _, placement := range placements {
		current := currentByID[placement.NodeID]
		if current == nil || current.TrashBatchID == nil || *current.TrashBatchID != placement.BatchID {
			continue
		}
		placementByTarget[placement.NodeID] = placement
		parentSet[placement.ParentID] = struct{}{}
	}
	if len(parentSet) == 0 {
		return nil
	}
	parentIDs := make([]guid.ID, 0, len(parentSet))
	for id := range parentSet {
		parentIDs = append(parentIDs, id)
	}
	closureLimit := len(parentIDs)*(maxNodeDepth+1) + 1
	closures, err := q.NodeClosure.WithContext(ctx).Where(
		q.NodeClosure.SpaceID.Eq(spaceID),
		q.NodeClosure.DescendantID.In(fileIDValues(parentIDs)...),
	).Order(q.NodeClosure.DescendantID.Asc(), q.NodeClosure.Depth.Desc()).Limit(closureLimit).Find()
	if err != nil {
		return err
	}
	if len(closures) == closureLimit {
		return errHierarchy
	}
	ancestorSet := make(map[guid.ID]struct{}, len(closures))
	closuresByParent := make(map[guid.ID][]*model.NodeClosure, len(parentIDs))
	for _, closure := range closures {
		closuresByParent[closure.DescendantID] = append(closuresByParent[closure.DescendantID], closure)
		ancestorSet[closure.AncestorID] = struct{}{}
	}
	ancestorIDs := make([]guid.ID, 0, len(ancestorSet))
	for id := range ancestorSet {
		ancestorIDs = append(ancestorIDs, id)
	}
	ancestors, err := q.Node.WithContext(ctx).Where(
		q.Node.SpaceID.Eq(spaceID), q.Node.ID.In(fileIDValues(ancestorIDs)...), q.Node.State.Eq("active"),
	).Find()
	if err != nil {
		return err
	}
	ancestorByID := make(map[guid.ID]*model.Node, len(ancestors))
	for _, ancestor := range ancestors {
		ancestorByID[ancestor.ID] = ancestor
	}
	candidates := make(map[guid.ID][]*model.Node, len(rows))
	uniquePathIDs := make(map[guid.ID]struct{})
	for _, targetID := range targetIDs {
		placement := placementByTarget[targetID]
		if placement == nil {
			continue
		}
		links := closuresByParent[placement.ParentID]
		path := make([]*model.Node, 0, len(links))
		for _, link := range links {
			ancestor := ancestorByID[link.AncestorID]
			if ancestor == nil {
				path = nil
				break
			}
			path = append(path, ancestor)
		}
		if len(path) == 0 || len(path) > maxNodeDepth+1 || path[0].ParentID != nil || path[len(path)-1].ID != placement.ParentID {
			continue
		}
		valid, rootIndex := true, -1
		for index, node := range path {
			if index > 0 && (node.ParentID == nil || *node.ParentID != path[index-1].ID) {
				valid = false
				break
			}
			if node.RootScope != nil {
				rootIndex = index
			}
		}
		if !valid || rootIndex < 0 {
			continue
		}
		candidate := path[rootIndex:]
		candidates[targetID] = candidate
		for _, node := range candidate {
			uniquePathIDs[node.ID] = struct{}{}
		}
	}
	if len(uniquePathIDs) == 0 {
		return nil
	}
	pathIDs := make([]guid.ID, 0, len(uniquePathIDs))
	for id := range uniquePathIDs {
		pathIDs = append(pathIDs, id)
	}
	allowed := make(map[guid.ID]bool, len(pathIDs))
	for start := 0; start < len(pathIDs); start += maxBrowserPage {
		end := min(start+maxBrowserPage, len(pathIDs))
		chunk, err := authorizer.accessMany(ctx, tx, spaceID, authorizer.actorID, pathIDs[start:end], "traverse")
		if err != nil {
			return err
		}
		for id, permitted := range chunk {
			allowed[id] = permitted
		}
	}
	for index, targetID := range targetIDs {
		path := candidates[targetID]
		if len(path) == 0 {
			continue
		}
		segments := make([]filePathView, 0, len(path))
		for _, node := range path {
			if !allowed[node.ID] {
				segments = nil
				break
			}
			segments = append(segments, filePathView{ID: node.ID, Name: browserPathName(node)})
		}
		if len(segments) > 0 {
			views[index].RestorePath = segments
		}
	}
	return nil
}

func (t directoryTree) authorizedChildren(ctx context.Context, tx *gorm.DB, authorizer *nodeAuthorizer, spaceID, nodeID guid.ID, request browserPageRequest) (fileChildrenPage, error) {
	result := fileChildrenPage{Path: []filePathView{}, Items: []fileEntryView{}}
	if err := authorizer.access(ctx, tx, spaceID, authorizer.actorID, nodeID, "traverse"); err != nil {
		return result, err
	}
	if err := authorizer.access(ctx, tx, spaceID, authorizer.actorID, nodeID, "list"); err != nil {
		return result, err
	}
	path, err := t.pathNodes(ctx, tx, spaceID, nodeID)
	if err != nil {
		return result, err
	}
	parent := path[len(path)-1]
	if parent.Kind != "directory" || parent.State != "active" {
		return result, ErrNotFound
	}
	permissions, err := t.loadNodePermissions(ctx, tx, spaceID, nodeID)
	if err != nil {
		return result, err
	}
	rootIndex := -1
	for index, row := range path {
		if row.ID == permissions.rootID {
			rootIndex = index
			break
		}
	}
	if rootIndex < 0 {
		return result, errFileACL
	}
	pathIDs := make([]guid.ID, 0, len(path)-rootIndex)
	for _, row := range path[rootIndex:] {
		pathIDs = append(pathIDs, row.ID)
	}
	traversable, err := authorizer.accessMany(ctx, tx, spaceID, authorizer.actorID, pathIDs, "traverse")
	if err != nil {
		return fileChildrenPage{}, err
	}
	for _, row := range path[rootIndex:] {
		if !traversable[row.ID] {
			return fileChildrenPage{}, errOperationForbidden
		}
		result.Path = append(result.Path, filePathView{ID: row.ID, Name: browserPathName(row)})
	}
	q := query.Bind(tx, t.prefix)
	after, scanned, exhausted := request.After, 0, false
	for len(result.Items) <= request.Limit && scanned < maxBrowserScan {
		batch := min(100, maxBrowserScan-scanned)
		rows, err := q.Node.WithContext(ctx).
			Where(q.Node.SpaceID.Eq(spaceID), q.Node.ParentID.Eq(nodeID), q.Node.LiveSlot.Eq(1), q.Node.State.Eq("active"), q.Node.ManagementMode.Eq("user"), q.Node.ID.Gt(after)).
			Order(q.Node.ID.Asc()).Limit(batch).Find()
		if err != nil {
			return fileChildrenPage{}, err
		}
		if len(rows) == 0 {
			exhausted = true
			break
		}
		scanned += len(rows)
		after = rows[len(rows)-1].ID
		ids := make([]guid.ID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		listable, err := authorizer.accessMany(ctx, tx, spaceID, authorizer.actorID, ids, "list")
		if err != nil {
			return fileChildrenPage{}, err
		}
		visible := make([]*model.Node, 0, len(rows))
		for _, row := range rows {
			if listable[row.ID] {
				visible = append(visible, row)
			}
		}
		views, err := fileEntryViewsOf(ctx, q, visible, nil)
		if err != nil {
			return fileChildrenPage{}, err
		}
		for _, view := range views {
			result.Items = append(result.Items, view)
			if len(result.Items) > request.Limit {
				break
			}
		}
		if len(rows) < batch {
			exhausted = true
			break
		}
	}
	if len(result.Items) > request.Limit {
		next := result.Items[request.Limit-1].ID
		result.Items, result.NextAfter = result.Items[:request.Limit], &next
	} else if !exhausted && scanned >= maxBrowserScan {
		next := after
		result.NextAfter = &next
	}
	return result, nil
}

func fileEntryViewOf(ctx context.Context, q *query.Query, row *model.Node) (fileEntryView, error) {
	var space *model.Space
	if row != nil && row.RootScope != nil {
		var err error
		space, err = q.Space.WithContext(ctx).Where(q.Space.ID.Eq(row.SpaceID)).Take()
		if err != nil {
			return fileEntryView{}, err
		}
	}
	return fileEntryViewInSpace(ctx, q, row, space)
}

func fileEntryViewInSpace(ctx context.Context, q *query.Query, row *model.Node, space *model.Space) (fileEntryView, error) {
	views, err := fileEntryViewsOf(ctx, q, []*model.Node{row}, space)
	if err != nil {
		return fileEntryView{}, err
	}
	if len(views) != 1 {
		return fileEntryView{}, errFileACL
	}
	return views[0], nil
}

func fileEntryViewsOf(ctx context.Context, q *query.Query, rows []*model.Node, space *model.Space) ([]fileEntryView, error) {
	views := make([]fileEntryView, len(rows))
	versionIDs := make([]guid.ID, 0, len(rows))
	for index, row := range rows {
		if row == nil {
			return nil, errFileACL
		}
		views[index] = fileEntryView{
			ID: row.ID, Name: row.Name, Kind: row.Kind, UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
			Extension: row.Ext, Revision: row.Version,
		}
		if row.RootScope != nil {
			views[index].Scope = *row.RootScope
			views[index].Name = rootDisplayName(row)
			if err := setRootOwnership(&views[index], space, row); err != nil {
				return nil, err
			}
		}
		if row.Kind == "file" && row.CurrentVersionID != nil {
			versionIDs = append(versionIDs, *row.CurrentVersionID)
		}
	}
	if len(versionIDs) == 0 {
		return views, nil
	}
	versions, err := q.Version.WithContext(ctx).Where(q.Version.ID.In(fileIDValues(versionIDs)...)).Find()
	if err != nil {
		return nil, err
	}
	versionByID := make(map[guid.ID]*model.Version, len(versions))
	objectIDs := make([]guid.ID, 0, len(versions))
	for _, version := range versions {
		versionByID[version.ID] = version
		objectIDs = append(objectIDs, version.ObjectID)
	}
	objects, err := q.Object.WithContext(ctx).Where(q.Object.ID.In(fileIDValues(objectIDs)...), q.Object.State.Eq("verified")).Find()
	if err != nil {
		return nil, err
	}
	objectByID := make(map[guid.ID]*model.Object, len(objects))
	for _, object := range objects {
		objectByID[object.ID] = object
	}
	for index, row := range rows {
		if row.Kind != "file" || row.CurrentVersionID == nil {
			continue
		}
		version := versionByID[*row.CurrentVersionID]
		if version == nil || version.SpaceID != row.SpaceID || version.NodeID != row.ID {
			return nil, errFileACL
		}
		object := objectByID[version.ObjectID]
		if object == nil {
			return nil, errFileACL
		}
		views[index].Size, views[index].DetectedType, views[index].Version = &object.Size, object.MIME, version.Number
	}
	return views, nil
}

func setRootOwnership(view *fileEntryView, space *model.Space, row *model.Node) error {
	if view == nil || space == nil || row == nil || row.RootScope == nil || row.RootOwnerID == nil {
		return errFileACL
	}
	binding, err := nodeRootBinding(space, row)
	if err != nil {
		return err
	}
	ownerID := binding.ownerID
	switch binding.scope {
	case ownerAccount:
		view.PrincipalID = &ownerID
	case ownerTenant:
		view.TenantID = &ownerID
	case ownerEntity:
		tenantID := *space.TenantID
		view.TenantID, view.EntityID = &tenantID, &ownerID
	case ownerDepartment:
		tenantID := *space.TenantID
		view.TenantID, view.DepartmentID = &tenantID, &ownerID
	default:
		return errFileACL
	}
	return nil
}

func browserNodeName(row *model.Node) string {
	if row == nil {
		return ""
	}
	if row.Name != "" {
		return row.Name
	}
	if row.RootScope != nil {
		return *row.RootScope
	}
	return ""
}

func browserPathName(row *model.Node) string {
	if row != nil && row.RootScope != nil {
		return rootDisplayName(row)
	}
	return browserNodeName(row)
}

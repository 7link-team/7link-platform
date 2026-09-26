package core

import (
	"context"
	"database/sql/driver"
	"fmt"
	"sort"
	"time"

	corequery "7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/policyx"
)

type MemberAuthorizationSourceType string

const (
	AuthorizationSourcePermanentRole MemberAuthorizationSourceType = "permanent_role"
	AuthorizationSourceTemporaryRole MemberAuthorizationSourceType = "temporary_role"
	AuthorizationSourceStaticGroup   MemberAuthorizationSourceType = "static_group"
	AuthorizationSourceDynamicGroup  MemberAuthorizationSourceType = "dynamic_group"
	AuthorizationSourceEntityBinding MemberAuthorizationSourceType = "entity_binding"
	AuthorizationSourceRelationship  MemberAuthorizationSourceType = "relationship"
)

// MemberAuthorizationSource is one independently removable path that currently
// participates in the member's authorization. Multiple paths to the same role
// remain separate so an administrator cannot mistake one removal for full
// revocation.
type MemberAuthorizationSource struct {
	Type             MemberAuthorizationSourceType
	SourceID         guid.ID
	SourceName       string
	RoleID           guid.ID
	RoleName         string
	PermissionCodes  []string
	Effect           BindingEffect
	ScopeType        string
	ScopeID          guid.ID
	ScopeName        string
	DataScope        DataScope
	DepartmentIDs    []guid.ID
	ExpiresAt        *time.Time
	Inherited        bool
	Relation         string
	RelationshipPath []authz.RelationshipStep
}

type MemberAuthorizationSnapshot struct {
	Subject     guid.ID
	Revision    int64
	EvaluatedAt time.Time
	Sources     []MemberAuthorizationSource
}

// DirectoryAuthorizationSourceStore is the narrow read capability bound by
// the organization module. Core owns the authorization snapshot while the
// organization module retains ownership of its generated persistence queries.
type DirectoryAuthorizationSourceStore struct {
	ListMembershipExpiries func(context.Context, guid.ID, guid.ID, time.Time) (DirectoryAuthorizationSourceExpiries, error)
	ListLabels             func(context.Context, guid.ID, []guid.ID, []guid.ID) (DirectoryAuthorizationSourceLabels, error)
}

type DirectoryAuthorizationSourceExpiries struct {
	Groups map[guid.ID]time.Time
}

type DirectoryAuthorizationSourceLabels struct {
	Groups          map[guid.ID]string
	GroupTypes      map[guid.ID]string
	RoleDepartments map[guid.ID][]guid.ID
}

// DirectoryAuthorizationSourceConnector lets the dependent organization
// module bind its reader after core has initialized its stable authorizer.
type DirectoryAuthorizationSourceConnector struct {
	Bind func(DirectoryAuthorizationSourceStore) error
}

type memberAuthorizationSourceReader interface {
	MemberAuthorizationSources(context.Context, guid.ID, guid.ID) (MemberAuthorizationSnapshot, error)
}

// ListMemberAuthorizationSources returns effective paths from the same
// evaluator used by request-time authorization.
func (s *Service) ListMemberAuthorizationSources(
	ctx context.Context,
	tenantID, subject guid.ID,
) (MemberAuthorizationSnapshot, error) {
	if err := validateMemberRef(tenantID, subject); err != nil {
		return MemberAuthorizationSnapshot{}, err
	}
	if _, err := s.repo.GetMember(ctx, tenantID, subject); err != nil {
		return MemberAuthorizationSnapshot{}, err
	}
	reader, ok := s.checker.(memberAuthorizationSourceReader)
	if !ok {
		return MemberAuthorizationSnapshot{}, ErrUnavailable
	}
	return reader.MemberAuthorizationSources(ctx, tenantID, subject)
}

// MemberAuthorizationSources takes a revision-checked snapshot of every
// currently matching tenant role, scoped entity role, and relationship path.
func (a *Authorizer) MemberAuthorizationSources(
	ctx context.Context,
	tenantID, subject guid.ID,
) (MemberAuthorizationSnapshot, error) {
	pinned, err := a.pinWriter()
	if err != nil {
		return MemberAuthorizationSnapshot{}, err
	}
	if pinned != a {
		return pinned.MemberAuthorizationSources(ctx, tenantID, subject)
	}
	if tenantID.Zero() || subject.Zero() {
		return MemberAuthorizationSnapshot{}, fmt.Errorf("%w: tenant and subject are required", ErrInvalidArgument)
	}
	trusted := policyx.TrustedFromContext(ctx, a.now())
	requested := make([]string, 0)
	actions := make([]authz.Action, 0)
	for _, action := range a.registry.All() {
		if action.Scope == "platform" {
			continue
		}
		requested = append(requested, action.Code)
		actions = append(actions, action)
	}
	names, err := a.authorizationTables()
	if err != nil {
		return MemberAuthorizationSnapshot{}, err
	}
	for range policySnapshotAttempts {
		before, err := a.revisions.Current(ctx, a.db, tenantID)
		if err != nil {
			return MemberAuthorizationSnapshot{}, err
		}
		_, memberActive, err := tenantMembershipState(ctx, a.db, names, tenantID, subject)
		if err != nil {
			return MemberAuthorizationSnapshot{}, err
		}
		snapshot := MemberAuthorizationSnapshot{
			Subject: subject, EvaluatedAt: trusted.Time.UTC().Truncate(time.Millisecond),
			Sources: []MemberAuthorizationSource{},
		}
		if memberActive {
			tenantGrants, err := a.loadTenantGrants(ctx, tenantID, subject, requested, trusted)
			if err != nil {
				return MemberAuthorizationSnapshot{}, err
			}
			scopedGrants, err := a.loadScopedGrants(ctx, tenantID, subject, requested, trusted)
			if err != nil {
				return MemberAuthorizationSnapshot{}, err
			}
			relationshipGrants, err := a.loadRelationshipGrants(ctx, tenantID, subject, trusted)
			if err != nil {
				return MemberAuthorizationSnapshot{}, err
			}
			expiries, err := a.loadAuthorizationSourceExpiries(ctx, tenantID, subject, snapshot.EvaluatedAt)
			if err != nil {
				return MemberAuthorizationSnapshot{}, err
			}
			labels, err := a.loadAuthorizationSourceLabels(ctx, tenantID, subject, tenantGrants, scopedGrants, relationshipGrants)
			if err != nil {
				return MemberAuthorizationSnapshot{}, err
			}
			snapshot.Sources = aggregateAuthorizationSources(
				tenantID, tenantGrants, scopedGrants, relationshipGrants, actions, labels, expiries,
			)
		}
		after, err := a.revisions.Current(ctx, a.db, tenantID)
		if err != nil {
			return MemberAuthorizationSnapshot{}, err
		}
		if before == after {
			snapshot.Revision = after
			return snapshot, nil
		}
	}
	return MemberAuthorizationSnapshot{}, ErrAuthorizationChanged
}

func (a *Authorizer) BindDirectoryAuthorizationSources(store DirectoryAuthorizationSourceStore) error {
	if a == nil || store.ListMembershipExpiries == nil || store.ListLabels == nil {
		return ErrUnavailable
	}
	a.directorySources = store
	return nil
}

type entityBindingSourceKey struct {
	roleID  guid.ID
	scopeID guid.ID
	effect  string
}

type authorizationSourceExpiries struct {
	temporaryRoles map[guid.ID]time.Time
	groups         map[guid.ID]time.Time
	entityBindings map[entityBindingSourceKey]time.Time
}

func (a *Authorizer) loadAuthorizationSourceExpiries(
	ctx context.Context,
	tenantID, subject guid.ID,
	evaluatedAt time.Time,
) (authorizationSourceExpiries, error) {
	expiries := authorizationSourceExpiries{
		temporaryRoles: map[guid.ID]time.Time{}, groups: map[guid.ID]time.Time{},
		entityBindings: map[entityBindingSourceKey]time.Time{},
	}
	coreTables := corequery.BindWithNames(a.db, a.names)
	temporaryRoles, err := coreTables.TemporaryRoleGrants.WithContext(ctx).Where(
		coreTables.TemporaryRoleGrants.TenantID.Eq(tenantID),
		coreTables.TemporaryRoleGrants.PrincipalID.Eq(subject),
	).Find()
	if err != nil {
		return authorizationSourceExpiries{}, fmt.Errorf("list authorization temporary role expiries: %w", err)
	}
	for _, grant := range temporaryRoles {
		if !grant.StartsAt.After(evaluatedAt) && grant.EndsAt.After(evaluatedAt) {
			expiries.temporaryRoles[grant.ID] = grant.EndsAt.UTC()
		}
	}
	bindings, err := coreTables.RoleBindings.WithContext(ctx).Where(
		coreTables.RoleBindings.TenantID.Eq(tenantID),
		coreTables.RoleBindings.PrincipalID.Eq(subject),
		coreTables.RoleBindings.ScopeType.Eq("entity"),
	).Find()
	if err != nil {
		return authorizationSourceExpiries{}, fmt.Errorf("list authorization entity binding expiries: %w", err)
	}
	for _, binding := range bindings {
		if binding.ExpiresAt == nil || !binding.ExpiresAt.After(evaluatedAt) {
			continue
		}
		expiries.entityBindings[entityBindingSourceKey{
			roleID: binding.RoleID, scopeID: binding.ScopeID, effect: binding.Effect,
		}] = binding.ExpiresAt.UTC()
	}

	if a.directorySources.ListMembershipExpiries != nil {
		directoryExpiries, err := a.directorySources.ListMembershipExpiries(ctx, tenantID, subject, evaluatedAt)
		if err != nil {
			return authorizationSourceExpiries{}, fmt.Errorf("list authorization directory membership expiries: %w", err)
		}
		for id, expiry := range directoryExpiries.Groups {
			expiries.groups[id] = expiry.UTC()
		}
	}
	return expiries, nil
}

func authorizationSourceExpiry(value time.Time, ok bool) *time.Time {
	if !ok {
		return nil
	}
	value = value.UTC()
	return &value
}

type authorizationSourceLabels struct {
	roles           map[guid.ID]string
	groups          map[guid.ID]string
	groupTypes      map[guid.ID]string
	entities        map[guid.ID]string
	principals      map[guid.ID]string
	roleDepartments map[guid.ID][]guid.ID
}

func (a *Authorizer) loadAuthorizationSourceLabels(
	ctx context.Context,
	tenantID, subject guid.ID,
	tenantGrants []tenantGrant,
	scopedGrants []scopedGrant,
	relationshipGrants []relationshipGrant,
) (authorizationSourceLabels, error) {
	labels := authorizationSourceLabels{
		roles: map[guid.ID]string{}, groups: map[guid.ID]string{}, groupTypes: map[guid.ID]string{},
		entities: map[guid.ID]string{}, principals: map[guid.ID]string{},
		roleDepartments: map[guid.ID][]guid.ID{},
	}
	roleIDs, groupIDs := map[guid.ID]bool{}, map[guid.ID]bool{}
	entityIDs, principalIDs := map[guid.ID]bool{}, map[guid.ID]bool{subject: true}
	for _, grant := range tenantGrants {
		roleIDs[grant.RoleID] = true
		switch grant.SourceType {
		case "group":
			groupIDs[grant.SourceID] = true
		case "role_member":
			principalIDs[grant.SourceID] = true
		}
	}
	for _, grant := range scopedGrants {
		roleIDs[grant.RoleID], entityIDs[grant.ScopeID], entityIDs[grant.EntityID] = true, true, true
	}
	for _, grant := range relationshipGrants {
		if grant.EntityID.Zero() {
			entityIDs[grant.ResourceID] = true
		}
		if !grant.EntityID.Zero() {
			entityIDs[grant.EntityID] = true
		}
		if len(grant.Path) == 0 {
			continue
		}
		id, parseErr := guid.Parse(grant.Path[0].SubjectID)
		if parseErr != nil {
			return authorizationSourceLabels{}, fmt.Errorf("parse authorization source: %w", parseErr)
		}
		switch grant.Path[0].SubjectType {
		case "principal":
			principalIDs[id] = true
		case "group":
			groupIDs[id] = true
		case "entity":
			entityIDs[id] = true
		}
	}
	coreTables := corequery.BindWithNames(a.db, a.names)
	if len(roleIDs) > 0 {
		rows, err := coreTables.Roles.WithContext(ctx).Where(
			coreTables.Roles.TenantID.Eq(tenantID),
			coreTables.Roles.ID.In(databaseIDValues(idSet(roleIDs))...),
		).Find()
		if err != nil {
			return authorizationSourceLabels{}, fmt.Errorf("list authorization role names: %w", err)
		}
		for _, row := range rows {
			labels.roles[row.ID] = row.Name
		}
	}
	if len(entityIDs) > 0 {
		rows, err := coreTables.Entities.WithContext(ctx).Where(
			coreTables.Entities.TenantID.Eq(tenantID),
			coreTables.Entities.ID.In(databaseIDValues(idSet(entityIDs))...),
		).Find()
		if err != nil {
			return authorizationSourceLabels{}, fmt.Errorf("list authorization entity names: %w", err)
		}
		for _, row := range rows {
			labels.entities[row.ID] = row.Name
		}
	}
	if len(principalIDs) > 0 {
		rows, err := coreTables.TenantMembers.WithContext(ctx).Where(
			coreTables.TenantMembers.TenantID.Eq(tenantID),
			coreTables.TenantMembers.PrincipalID.In(databaseIDValues(idSet(principalIDs))...),
		).Find()
		if err != nil {
			return authorizationSourceLabels{}, fmt.Errorf("list authorization principal names: %w", err)
		}
		for _, row := range rows {
			labels.principals[row.PrincipalID] = row.DisplayName
		}
	}
	if a.directorySources.ListLabels != nil {
		directoryLabels, err := a.directorySources.ListLabels(
			ctx, tenantID, idSet(roleIDs), idSet(groupIDs),
		)
		if err != nil {
			return authorizationSourceLabels{}, fmt.Errorf("list authorization directory labels: %w", err)
		}
		for id, name := range directoryLabels.Groups {
			labels.groups[id] = name
		}
		for id, groupType := range directoryLabels.GroupTypes {
			labels.groupTypes[id] = groupType
		}
		for roleID, departmentIDs := range directoryLabels.RoleDepartments {
			labels.roleDepartments[roleID] = append([]guid.ID(nil), departmentIDs...)
		}
	}
	return labels, nil
}

func aggregateAuthorizationSources(
	tenantID guid.ID,
	tenantGrants []tenantGrant,
	scopedGrants []scopedGrant,
	relationshipGrants []relationshipGrant,
	actions []authz.Action,
	labels authorizationSourceLabels,
	expiries authorizationSourceExpiries,
) []MemberAuthorizationSource {
	sources := make([]MemberAuthorizationSource, 0)
	indexes := map[string]int{}
	appendGrant := func(key string, source MemberAuthorizationSource, permission string) {
		if index, ok := indexes[key]; ok {
			sources[index].PermissionCodes = appendUnique(sources[index].PermissionCodes, permission)
			return
		}
		source.PermissionCodes = []string{permission}
		indexes[key] = len(sources)
		sources = append(sources, source)
	}
	for _, grant := range tenantGrants {
		typeName := AuthorizationSourcePermanentRole
		sourceName := labels.principals[grant.SourceID]
		var expiresAt *time.Time
		switch grant.SourceType {
		case "temporary_role":
			typeName, sourceName = AuthorizationSourceTemporaryRole, ""
			expiry, ok := expiries.temporaryRoles[grant.SourceID]
			expiresAt = authorizationSourceExpiry(expiry, ok)
		case "group":
			typeName, sourceName = AuthorizationSourceStaticGroup, labels.groups[grant.SourceID]
			if labels.groupTypes[grant.SourceID] == "dynamic" {
				typeName = AuthorizationSourceDynamicGroup
			} else {
				expiry, ok := expiries.groups[grant.SourceID]
				expiresAt = authorizationSourceExpiry(expiry, ok)
			}
		}
		key := fmt.Sprintf("tenant:%s:%d:%d:%s", typeName, grant.RoleID, grant.SourceID, grant.DataScope)
		appendGrant(key, MemberAuthorizationSource{
			Type: typeName, SourceID: grant.SourceID, SourceName: sourceName,
			RoleID: grant.RoleID, RoleName: labels.roles[grant.RoleID],
			Effect: BindingAllow, ScopeType: "tenant", ScopeID: tenantID,
			DataScope: DataScope(grant.DataScope), DepartmentIDs: append([]guid.ID(nil), labels.roleDepartments[grant.RoleID]...),
			ExpiresAt: expiresAt, RelationshipPath: []authz.RelationshipStep{},
		}, grant.PermissionCode)
	}
	for _, grant := range scopedGrants {
		expiry, hasExpiry := expiries.entityBindings[entityBindingSourceKey{
			roleID: grant.RoleID, scopeID: grant.ScopeID, effect: grant.Effect,
		}]
		key := fmt.Sprintf("entity:%d:%d:%d:%s", grant.RoleID, grant.ScopeID, grant.EntityID, grant.Effect)
		appendGrant(key, MemberAuthorizationSource{
			Type: AuthorizationSourceEntityBinding, SourceID: grant.ScopeID, SourceName: labels.entities[grant.ScopeID],
			RoleID: grant.RoleID, RoleName: labels.roles[grant.RoleID], Effect: BindingEffect(grant.Effect),
			ScopeType: "entity", ScopeID: grant.EntityID, ScopeName: labels.entities[grant.EntityID],
			ExpiresAt: authorizationSourceExpiry(expiry, hasExpiry), Inherited: grant.Depth > 0,
			DepartmentIDs: []guid.ID{}, RelationshipPath: []authz.RelationshipStep{},
		}, grant.PermissionCode)
	}
	for _, grant := range relationshipGrants {
		permissions := make([]string, 0)
		for _, action := range actions {
			if relationshipAllowed(action, grant) {
				permissions = append(permissions, action.Code)
			}
		}
		if len(permissions) == 0 || len(grant.Path) == 0 {
			continue
		}
		sourceID, err := guid.Parse(grant.Path[0].SubjectID)
		if err != nil {
			continue
		}
		sourceName := authorizationSourceName(grant.Path[0].SubjectType, sourceID, labels)
		scopeName := ""
		if grant.EntityID.Zero() {
			scopeName = labels.entities[grant.ResourceID]
		}
		source := MemberAuthorizationSource{
			Type: AuthorizationSourceRelationship, SourceID: sourceID, SourceName: sourceName,
			PermissionCodes: permissions, Effect: BindingAllow, ScopeType: grant.ResourceType,
			ScopeID: grant.ResourceID, ScopeName: scopeName, Relation: grant.Relation,
			Inherited: len(grant.Path) > 1, DepartmentIDs: []guid.ID{},
			RelationshipPath: append([]authz.RelationshipStep(nil), grant.Path...),
		}
		sources = append(sources, source)
	}
	for index := range sources {
		sort.Strings(sources[index].PermissionCodes)
	}
	sort.SliceStable(sources, func(i, j int) bool {
		left, right := sources[i], sources[j]
		if left.Type != right.Type {
			return left.Type < right.Type
		}
		if left.RoleID != right.RoleID {
			return left.RoleID < right.RoleID
		}
		if left.SourceID != right.SourceID {
			return left.SourceID < right.SourceID
		}
		if left.ScopeID != right.ScopeID {
			return left.ScopeID < right.ScopeID
		}
		return left.Effect < right.Effect
	})
	return sources
}

func authorizationSourceName(sourceType string, id guid.ID, labels authorizationSourceLabels) string {
	switch sourceType {
	case "principal":
		return labels.principals[id]
	case "group":
		return labels.groups[id]
	case "entity":
		return labels.entities[id]
	default:
		return ""
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func idSet(values map[guid.ID]bool) []guid.ID {
	ids := make([]guid.ID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func databaseIDValues(ids []guid.ID) []driver.Valuer {
	values := make([]driver.Valuer, len(ids))
	for index, id := range ids {
		values[index] = id
	}
	return values
}

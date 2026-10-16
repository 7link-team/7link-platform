package core

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestEntityProvisionerSharesCallerTransaction(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			h := newCoreServiceHarness(t)
			h.seedMember(t, 1, 11, MemberActive)
			require.NoError(t, h.db.Exec("CREATE TABLE provisioning_evidence (kind TEXT NOT NULL, entity_id BIGINT NOT NULL)").Error)
			h.service.roots = func(ctx context.Context, tx *gorm.DB, input FileRootProvisionInput, _ time.Time) (guid.ID, error) {
				require.Equal(t, "entity", input.Scope)
				var count int64
				require.NoError(t, tx.Table("sl_iam_entities").Where("id = ?", input.OwnerID).Count(&count).Error)
				require.EqualValues(t, 1, count)
				return 9001, tx.WithContext(ctx).Exec("INSERT INTO provisioning_evidence VALUES (?, ?)", "root", input.OwnerID).Error
			}
			h.service.writes.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
				return tx.WithContext(ctx).Exec("INSERT INTO provisioning_evidence VALUES (?, ?)", "audit", event.TargetID).Error
			}
			before, err := h.repository.revisions.Current(t.Context(), h.db, 1)
			require.NoError(t, err)
			p := (&Module{service: h.service}).entityProvisioner()
			abort := errors.New("later project step failed")
			err = h.db.Transaction(func(tx *gorm.DB) error {
				entity, err := p.CreateOn(t.Context(), tx, Entity{TenantID: 1, Type: "project", Name: "Project"})
				require.NoError(t, err)
				read, err := p.GetOn(t.Context(), tx, 1, entity.ID)
				require.NoError(t, err)
				require.Equal(t, entity, read, "reads must observe the caller's uncommitted entity")
				_, err = p.GetOn(t.Context(), tx, 2, entity.ID)
				require.ErrorIs(t, err, ErrEntityNotFound)
				_, err = p.GetOn(t.Context(), tx, 0, entity.ID)
				require.ErrorIs(t, err, ErrInvalidArgument)
				name := "Renamed"
				updated, err := p.UpdateOn(t.Context(), tx, 1, entity.ID, EntityPatch{Name: &name})
				require.NoError(t, err)
				require.Equal(t, name, updated.Name)
				if rollback {
					return abort
				}
				return nil
			})
			if rollback {
				require.ErrorIs(t, err, abort)
			} else {
				require.NoError(t, err)
			}
			entities, err := h.service.ListEntities(t.Context(), 1)
			require.NoError(t, err)
			var evidence int64
			require.NoError(t, h.db.Table("provisioning_evidence").Count(&evidence).Error)
			after, err := h.repository.revisions.Current(t.Context(), h.db, 1)
			require.NoError(t, err)
			if rollback {
				require.Empty(t, entities)
				require.Zero(t, evidence)
				require.Equal(t, before, after)
			} else {
				require.Len(t, entities, 1)
				require.Equal(t, "Renamed", entities[0].Name)
				require.EqualValues(t, 3, evidence)
				require.Greater(t, after, before)
			}
			require.Same(t, h.repository, h.service.repo)
		})
	}
}

func TestEntityProvisionerRejectsMissingDependenciesAndNonTransaction(t *testing.T) {
	h := newCoreServiceHarness(t)
	m := &Module{service: h.service}
	for _, owner := range []*Module{nil, {}, m} {
		p := owner.entityProvisioner()
		_, err := p.GetOn(t.Context(), nil, 1, 1)
		require.ErrorIs(t, err, ErrUnavailable)
		_, err = p.CreateOn(t.Context(), nil, Entity{})
		require.ErrorIs(t, err, ErrUnavailable)
		_, err = p.UpdateOn(t.Context(), nil, 1, 1, EntityPatch{})
		require.ErrorIs(t, err, ErrUnavailable)
	}
	_, err := m.entityProvisioner().CreateOn(t.Context(), h.db, Entity{})
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.entityProvisioner().GetOn(t.Context(), h.db, 1, 1)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := m.entityProvisioner().CreateOn(t.Context(), tx, Entity{TenantID: 1, Type: "project", Name: "Unavailable"})
		require.ErrorIs(t, err, ErrUnavailable)
		return nil
	}))
	entities, err := h.service.ListEntities(t.Context(), 1)
	require.NoError(t, err)
	require.Empty(t, entities)
}

func TestEntityProvisionerPreservesValidationAndRootFailure(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	failure := errors.New("file root failed")
	h.service.roots = func(context.Context, *gorm.DB, FileRootProvisionInput, time.Time) (guid.ID, error) { return 0, failure }
	p := (&Module{service: h.service}).entityProvisioner()
	err := h.db.Transaction(func(tx *gorm.DB) error {
		_, err := p.CreateOn(t.Context(), tx, Entity{TenantID: 1, Type: "project", Name: "Project"})
		return err
	})
	require.ErrorIs(t, err, failure)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := p.CreateOn(t.Context(), tx, Entity{TenantID: 1, Type: "project", Name: ""})
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = p.UpdateOn(t.Context(), tx, 2, 999, EntityPatch{})
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
	entities, err := h.service.ListEntities(t.Context(), 1)
	require.NoError(t, err)
	require.Empty(t, entities)
}

func TestEntityProvisionerBindingRollsBackWithProject(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	role, err := h.service.CreateRole(t.Context(), 1, "Project lead", "")
	require.NoError(t, err)
	h.service.roots = func(context.Context, *gorm.DB, FileRootProvisionInput, time.Time) (guid.ID, error) { return 9001, nil }
	m := &Module{service: h.service}
	abort := errors.New("channel provisioning failed")
	err = h.db.Transaction(func(tx *gorm.DB) error {
		entity, err := m.entityProvisioner().CreateOn(t.Context(), tx, Entity{TenantID: 1, Type: "project", Name: "Atomic project"})
		require.NoError(t, err)
		binding, changed, err := m.entityRoleBindingStore().PutOn(t.Context(), tx, 1, entity.ID, role.ID, 11, BindingAllow, time.Time{})
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, entity.ID, binding.EntityID)
		_, changed, err = m.entityRoleBindingStore().PutOn(t.Context(), tx, 1, entity.ID, role.ID, 11, BindingAllow, time.Time{})
		require.NoError(t, err)
		require.False(t, changed)
		_, _, err = m.entityRoleBindingStore().PutOn(t.Context(), tx, 1, entity.ID, role.ID, 99, BindingAllow, time.Time{})
		require.ErrorIs(t, err, ErrMemberInactive)
		return abort
	})
	require.ErrorIs(t, err, abort)
	var count int64
	require.NoError(t, h.db.Table("sl_iam_role_bindings").Where("scope_type = ?", "entity").Count(&count).Error)
	require.Zero(t, count)
	entities, err := h.service.ListEntities(t.Context(), 1)
	require.NoError(t, err)
	require.Empty(t, entities)
	_, _, err = (&Module{}).entityRoleBindingStore().PutOn(t.Context(), nil, 1, 1, 1, 1, BindingAllow, time.Time{})
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestEntityProvisionerUpdateKeepsTenantBoundary(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	h.seedMember(t, 2, 22, MemberActive)
	original, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "project", Name: "Original"})
	require.NoError(t, err)
	p := (&Module{service: h.service}).entityProvisioner()
	name := "Cross tenant overwrite"
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, err := p.UpdateOn(t.Context(), tx, 2, original.ID, EntityPatch{Name: &name})
		return err
	})
	require.ErrorIs(t, err, ErrEntityNotFound)
	stored, err := h.service.GetEntity(t.Context(), 1, original.ID)
	require.NoError(t, err)
	require.Equal(t, original.Name, stored.Name)
}

func TestReservedEntityLifecycleCannotBypassBusinessOwner(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.service.reservedEntityTypes = map[string]struct{}{"project": {}}
	h.service.roots = func(context.Context, *gorm.DB, FileRootProvisionInput, time.Time) (guid.ID, error) { return 1, nil }
	owner := (&Module{service: h.service}).entityProvisioner()
	var project Entity
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		project, err = owner.CreateOn(t.Context(), tx, Entity{TenantID: 1, Type: "project", Name: "Owned"})
		return err
	}))
	ordinary, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "client", Name: "Client"})
	require.NoError(t, err)
	before, err := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	for _, kind := range []string{"project", " project "} {
		_, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: kind, Name: "Bypass"})
		require.ErrorIs(t, err, ErrEntityTypeReserved)
	}
	name, kind, targetType := "Changed", "client", " project "
	parent := ordinary.ID
	for _, patch := range []EntityPatch{{Name: &name}, {Type: &kind}, {ParentID: &parent}} {
		_, err := h.service.UpdateEntity(t.Context(), 1, project.ID, patch)
		require.ErrorIs(t, err, ErrEntityTypeReserved)
	}
	_, err = h.service.UpdateEntity(t.Context(), 1, ordinary.ID, EntityPatch{Type: &targetType})
	require.ErrorIs(t, err, ErrEntityTypeReserved)
	require.ErrorIs(t, h.service.DeleteEntity(t.Context(), 1, project.ID), ErrEntityTypeReserved)
	_, err = h.service.UpdateEntity(t.Context(), 2, project.ID, EntityPatch{Name: &name})
	require.ErrorIs(t, err, ErrEntityNotFound)
	after, err := h.repository.revisions.Current(t.Context(), h.db, 1)
	require.NoError(t, err)
	require.Equal(t, before, after, "rejected mutations must not change policy revision")
	stored, err := h.service.GetEntity(t.Context(), 1, project.ID)
	require.NoError(t, err)
	require.Equal(t, project, stored)
	entities, err := h.service.ListEntities(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, entities, 2)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := owner.UpdateOn(t.Context(), tx, 1, project.ID, EntityPatch{Name: &name})
		return err
	}))
	_, err = h.service.UpdateEntity(t.Context(), 1, project.ID, EntityPatch{Name: &name})
	require.ErrorIs(t, err, ErrEntityTypeReserved, "owner capability must not escape its service copy")
	_, err = h.service.UpdateEntity(t.Context(), 1, ordinary.ID, EntityPatch{Name: &name})
	require.NoError(t, err)
	require.NoError(t, h.service.DeleteEntity(t.Context(), 1, ordinary.ID))
}

func TestBusinessBindingCapabilityIsReservedScopedAndTransactional(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			h := newCoreServiceHarness(t)
			h.seedMember(t, 1, 11, MemberActive)
			h.seedAdditionalMember(t, 1, 12, MemberActive)
			h.seedMember(t, 2, 22, MemberActive)
			h.service.reservedEntityTypes = map[string]struct{}{"project": {}}
			h.service.roots = func(context.Context, *gorm.DB, FileRootProvisionInput, time.Time) (guid.ID, error) { return 9001, nil }
			role, err := h.service.CreateRole(t.Context(), 1, "Project lead", "")
			require.NoError(t, err)
			_, err = h.service.GrantPermission(t.Context(), 1, role.ID, "role_view")
			require.NoError(t, err)
			ordinary, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "client", Name: "Ordinary"})
			require.NoError(t, err)
			m := &Module{service: h.service}
			var project Entity
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				project, err = m.entityProvisioner().CreateOn(t.Context(), tx, Entity{TenantID: 1, Type: "project", Name: "Owned"})
				return err
			}))
			actor := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 1, PrincipalID: 11})
			_, _, err = h.service.PutEntityRoleBinding(actor, 1, project.ID, role.ID, 11, BindingAllow, time.Time{})
			require.ErrorIs(t, err, ErrPrivilegeEscalation)
			require.NoError(t, h.db.Exec("CREATE TABLE owned_binding_audit (actor BIGINT, source TEXT)").Error)
			h.service.writes.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
				require.Equal(t, guid.ID(11), authn.PrincipalIDFromContext(ctx))
				require.Equal(t, guid.ID(11), event.PrincipalID)
				require.Equal(t, "entity_provisioning", event.Detail["operation_source"])
				return tx.Exec("INSERT INTO owned_binding_audit VALUES (?,?)", event.PrincipalID, event.Detail["operation_source"]).Error
			}
			before, err := h.repository.revisions.Current(t.Context(), h.db, 1)
			require.NoError(t, err)
			store := m.entityRoleBindingStore()
			for _, denied := range []struct {
				tenant, entity, principal guid.ID
				want                      error
			}{
				{1, ordinary.ID, 11, ErrPrivilegeEscalation},
				{2, project.ID, 22, ErrEntityNotFound},
				{1, project.ID, 999, ErrMemberInactive},
			} {
				err := h.db.Transaction(func(tx *gorm.DB) error {
					_, _, err := store.PutOn(actor, tx, denied.tenant, denied.entity, role.ID, denied.principal, BindingAllow, time.Time{})
					return err
				})
				require.ErrorIs(t, err, denied.want)
			}
			abort := errors.New("project event failed")
			err = h.db.Transaction(func(tx *gorm.DB) error {
				_, changed, err := store.PutOn(actor, tx, 1, project.ID, role.ID, 11, BindingAllow, time.Time{})
				require.NoError(t, err)
				require.True(t, changed)
				if rollback {
					return abort
				}
				return nil
			})
			if rollback {
				require.ErrorIs(t, err, abort)
			} else {
				require.NoError(t, err)
			}
			require.False(t, h.service.ownedEntityBindings)
			peer := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 1, PrincipalID: 12})
			_, _, err = h.service.PutEntityRoleBinding(peer, 1, project.ID, role.ID, 12, BindingAllow, time.Time{})
			require.ErrorIs(t, err, ErrPrivilegeEscalation)
			var bindings, audits int64
			require.NoError(t, h.db.Table("sl_iam_role_bindings").Where("scope_type='entity'").Count(&bindings).Error)
			require.NoError(t, h.db.Table("owned_binding_audit").Count(&audits).Error)
			after, err := h.repository.revisions.Current(t.Context(), h.db, 1)
			require.NoError(t, err)
			if rollback {
				require.Zero(t, bindings)
				require.Zero(t, audits)
				require.Equal(t, before, after)
			} else {
				require.EqualValues(t, 1, bindings)
				require.EqualValues(t, 1, audits)
				require.Greater(t, after, before)
			}
		})
	}
}

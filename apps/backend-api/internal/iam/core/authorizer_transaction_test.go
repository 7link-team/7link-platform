package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func transactionAuthorizationFixture(t *testing.T) (coreServiceHarness, Entity, Role) {
	t.Helper()
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	entity, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "store", Name: "Workspace"})
	require.NoError(t, err)
	role, err := h.service.CreateRole(t.Context(), 1, "Reader", "")
	require.NoError(t, err)
	_, err = h.service.GrantPermission(t.Context(), 1, role.ID, "user_view")
	require.NoError(t, err)
	_, err = h.service.AddMember(t.Context(), 1, role.ID, 11)
	require.NoError(t, err)
	return h, entity, role
}

func TestEntityConstraintOnSeesUncommittedPolicyAndRollsBack(t *testing.T) {
	h, entity, role := transactionAuthorizationFixture(t)
	before, err := h.authorizer.EntityConstraint(t.Context(), 1, entity.ID, "user_view", 11)
	require.NoError(t, err)
	require.True(t, before.AllowAll)
	want := errors.New("business transaction failed")
	err = h.db.Transaction(func(tx *gorm.DB) error {
		initial, err := h.authorizer.EntityConstraintOn(t.Context(), tx, 1, entity.ID, "user_view", 11)
		if err != nil {
			return err
		}
		require.Equal(t, before, initial)
		result := tx.Table(query.BindNames("sl_").RolePermissions.String()).Where("tenant_id = ? AND role_id = ? AND permission_code = ?", 1, role.ID, "user_view").Delete(&model.RolePermission{})
		require.NoError(t, result.Error)
		require.EqualValues(t, 1, result.RowsAffected)
		require.NoError(t, h.authorizer.revisions.Bump(t.Context(), tx, 1, *h.now))
		denied, err := h.authorizer.EntityConstraintOn(t.Context(), tx, 1, entity.ID, "user_view", 11)
		if err != nil {
			return err
		}
		require.False(t, denied.AllowsEntityRows(entity.ID), "must not evaluate the independent writer")
		require.Greater(t, denied.Revision, initial.Revision)
		return want
	})
	require.ErrorIs(t, err, want)
	after, err := h.authorizer.EntityConstraint(t.Context(), 1, entity.ID, "user_view", 11)
	require.NoError(t, err)
	require.Equal(t, before, after, "policy fence and evaluation do not independently commit")
	require.Nil(t, h.authorizer.db, "transaction must not mutate shared evaluator")
}

func TestEntityConstraintOnFencesConcurrentRevocation(t *testing.T) {
	h, entity, role := transactionAuthorizationFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first := h.db.WithContext(ctx).Begin()
	require.NoError(t, first.Error)
	t.Cleanup(func() { _ = first.Rollback().Error })
	constraint, err := h.authorizer.EntityConstraintOn(ctx, first, 1, entity.ID, "user_view", 11)
	require.NoError(t, err)
	require.True(t, constraint.AllowAll)
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := h.service.RevokePermission(ctx, 1, role.ID, "user_view")
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("revocation completed before business transaction ended: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	constraint, err = h.authorizer.EntityConstraintOn(ctx, first, 1, entity.ID, "user_view", 11)
	require.NoError(t, err)
	require.True(t, constraint.AllowAll)
	require.NoError(t, first.Commit().Error)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		constraint, err := h.authorizer.EntityConstraintOn(ctx, tx, 1, entity.ID, "user_view", 11)
		if err != nil {
			return err
		}
		require.False(t, constraint.AllowsEntityRows(entity.ID))
		return nil
	}))
}

func TestEntityConstraintOnRejectsInvalidAndMissingDependencies(t *testing.T) {
	h, entity, _ := transactionAuthorizationFixture(t)
	for _, db := range []*gorm.DB{nil, h.db} {
		_, err := h.authorizer.EntityConstraintOn(t.Context(), db, 1, entity.ID, "user_view", 11)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, input := range []struct{ tenant, entity, principal guid.ID }{{0, entity.ID, 11}, {1, 0, 11}, {1, entity.ID, 0}} {
		err := h.db.Transaction(func(tx *gorm.DB) error {
			_, err := h.authorizer.EntityConstraintOn(t.Context(), tx, input.tenant, input.entity, "user_view", input.principal)
			return err
		})
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, authorizer := range []*Authorizer{nil, {}, NewAuthorizer(h.runtime)} {
		err := h.db.Transaction(func(tx *gorm.DB) error {
			_, err := authorizer.EntityConstraintOn(t.Context(), tx, 1, entity.ID, "user_view", 11)
			return err
		})
		require.ErrorIs(t, err, ErrUnavailable)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.authorizer.EntityConstraintOn(ctx, tx, 1, entity.ID, "user_view", 11)
		return err
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestEntityConstraintOnPreservesTenantAndEntityDenials(t *testing.T) {
	h, entity, _ := transactionAuthorizationFixture(t)
	h.seedMember(t, 2, 22, MemberActive)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		for _, input := range []struct{ tenant, entity, principal guid.ID }{{2, entity.ID, 22}, {1, entity.ID, 22}, {1, 99999, 11}} {
			constraint, err := h.authorizer.EntityConstraintOn(t.Context(), tx, input.tenant, input.entity, "user_view", input.principal)
			if err != nil {
				return err
			}
			require.False(t, constraint.AllowsEntityRows(input.entity))
		}
		return nil
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Table(query.BindNames("sl_").Entities.String()).Where("tenant_id = ? AND id = ?", 1, entity.ID).UpdateColumn("status", EntityDisabled).Error)
		constraint, err := h.authorizer.EntityConstraintOn(t.Context(), tx, 1, entity.ID, "user_view", 11)
		if err != nil {
			return err
		}
		require.False(t, constraint.AllowsEntityRows(entity.ID))
		return nil
	}))
}

func TestModuleProvidesTransactionAuthorizationBeforeWiring(t *testing.T) {
	h, entity, _ := transactionAuthorizationFixture(t)
	instance := NewModule(h.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	evaluate, err := module.Resolve[EntityConstraintOn](services)
	require.NoError(t, err)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, err := evaluate(t.Context(), tx, 1, entity.ID, "user_view", 11)
		return err
	})
	require.ErrorIs(t, err, ErrUnavailable)
	// Wiring initializes the same checker the bound method already references.
	*instance.checker = *h.authorizer
	err = h.db.Transaction(func(tx *gorm.DB) error {
		constraint, err := evaluate(t.Context(), tx, 1, entity.ID, "user_view", 11)
		if err == nil {
			require.True(t, constraint.AllowAll)
		}
		return err
	})
	require.NoError(t, err)
	duplicate := module.NewServices()
	require.NoError(t, module.Provide[EntityConstraintOn](duplicate, func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}))
	require.Error(t, instance.ProvideServices(duplicate))
}

func TestAuthorizationSubjectsOnResolvesCurrentDirectoryAndEntityAssignments(t *testing.T) {
	h, entity, directRole := transactionAuthorizationFixture(t)
	now := repositoryTestTime
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_departments
		(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
		VALUES (1,100,NULL,'Engineering','engineering','active',0,1,?,?)`, now, now).Error)
	require.NoError(t, h.repository.runInTx(t.Context(), func(repo *Repository) error {
		return repo.setMemberDepartment(t.Context(), 1, 11, 100)
	}))
	dynamicRule := `{"version":1,"match":"all","conditions":[{"field":"member.status","operator":"in","values":["active"]}]}`
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_groups
		(tenant_id,id,name,name_key,group_type,rule_json,description,status,sort_order,version,created_at,updated_at)
		VALUES (1,201,'Static','static','static','','','active',0,1,?,?),
		       (1,202,'Dynamic','dynamic','dynamic',?,'','active',0,1,?,?),
		       (1,203,'Expired','expired','static','','','active',0,1,?,?)`,
		now, now, dynamicRule, now, now, now, now).Error)
	expired := now.Add(-time.Minute)
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_group_members
		(tenant_id,group_id,principal_id,starts_at,ends_at,created_at,updated_at)
		VALUES (1,201,11,NULL,NULL,?,?),(1,203,11,NULL,?,?,?)`, now, now, expired, now, now).Error)
	staticRole, err := h.service.CreateRole(t.Context(), 1, "Static role", "")
	require.NoError(t, err)
	dynamicRole, err := h.service.CreateRole(t.Context(), 1, "Dynamic role", "")
	require.NoError(t, err)
	entityRole, err := h.service.CreateRole(t.Context(), 1, "Entity role", "")
	require.NoError(t, err)
	_, err = h.service.AddDirectoryBinding(t.Context(), 1, staticRole.ID, DirectoryAssigneeGroup, 201)
	require.NoError(t, err)
	_, err = h.service.AddDirectoryBinding(t.Context(), 1, dynamicRole.ID, DirectoryAssigneeGroup, 202)
	require.NoError(t, err)
	_, _, err = h.service.PutEntityRoleBinding(t.Context(), 1, entity.ID, entityRole.ID, 11, BindingAllow, time.Time{})
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		subjects, err := h.authorizer.AuthorizationSubjectsOn(t.Context(), tx, 1, entity.ID, 11)
		if err != nil {
			return err
		}
		require.Equal(t, guid.ID(11), subjects.PrincipalID)
		require.Equal(t, guid.ID(100), subjects.DepartmentID)
		require.Equal(t, []guid.ID{201, 202}, subjects.GroupIDs)
		require.ElementsMatch(t, []guid.ID{directRole.ID, staticRole.ID, dynamicRole.ID, entityRole.ID}, subjects.RoleIDs)
		require.Positive(t, subjects.Revision)
		return nil
	}))
}

func TestAuthorizationSubjectsOnSeesUncommittedRevocationAndRollsBack(t *testing.T) {
	h, entity, role := transactionAuthorizationFixture(t)
	before := AuthorizationSubjects{}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		before, err = h.authorizer.AuthorizationSubjectsOn(t.Context(), tx, 1, entity.ID, 11)
		return err
	}))
	require.Contains(t, before.RoleIDs, role.ID)
	want := errors.New("business rollback")
	err := h.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Table(query.BindNames("sl_").RoleMembers.String()).Where("tenant_id = ? AND role_id = ? AND principal_id = ?", 1, role.ID, 11).Delete(&model.RoleMember{})
		if result.Error != nil {
			return result.Error
		}
		if err := h.authorizer.revisions.Bump(t.Context(), tx, 1, repositoryTestTime); err != nil {
			return err
		}
		after, err := h.authorizer.AuthorizationSubjectsOn(t.Context(), tx, 1, entity.ID, 11)
		if err != nil {
			return err
		}
		require.NotContains(t, after.RoleIDs, role.ID)
		require.Greater(t, after.Revision, before.Revision)
		return want
	})
	require.ErrorIs(t, err, want)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		after, err := h.authorizer.AuthorizationSubjectsOn(t.Context(), tx, 1, entity.ID, 11)
		require.Equal(t, before, after)
		return err
	}))
}

func TestAuthorizationSubjectsOnFailsClosedForInvalidInactiveAndUnwired(t *testing.T) {
	h, entity, _ := transactionAuthorizationFixture(t)
	for _, input := range []struct{ tenant, entity, principal guid.ID }{{0, entity.ID, 11}, {1, 0, 11}, {1, entity.ID, 0}} {
		err := h.db.Transaction(func(tx *gorm.DB) error {
			_, err := h.authorizer.AuthorizationSubjectsOn(t.Context(), tx, input.tenant, input.entity, input.principal)
			return err
		})
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, db := range []*gorm.DB{nil, h.db} {
		_, err := h.authorizer.AuthorizationSubjectsOn(t.Context(), db, 1, entity.ID, 11)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, authorizer := range []*Authorizer{nil, {}, NewAuthorizer(h.runtime)} {
		err := h.db.Transaction(func(tx *gorm.DB) error {
			_, err := authorizer.AuthorizationSubjectsOn(t.Context(), tx, 1, entity.ID, 11)
			return err
		})
		require.ErrorIs(t, err, ErrUnavailable)
	}
	for _, input := range []struct{ tenant, entity, principal guid.ID }{{1, 999, 11}, {1, entity.ID, 999}} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			subjects, err := h.authorizer.AuthorizationSubjectsOn(t.Context(), tx, input.tenant, input.entity, input.principal)
			require.Empty(t, subjects)
			return err
		}))
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.authorizer.AuthorizationSubjectsOn(ctx, tx, 1, entity.ID, 11)
		return err
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestModuleProvidesStableTransactionAuthorizationSubjects(t *testing.T) {
	h, entity, _ := transactionAuthorizationFixture(t)
	instance := NewModule(h.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	resolve, err := module.Resolve[AuthorizationSubjectsOn](services)
	require.NoError(t, err)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, err := resolve(t.Context(), tx, 1, entity.ID, 11)
		return err
	})
	require.ErrorIs(t, err, ErrUnavailable)
	*instance.checker = *h.authorizer
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		subjects, err := resolve(t.Context(), tx, 1, entity.ID, 11)
		require.Equal(t, guid.ID(11), subjects.PrincipalID)
		return err
	}))
	duplicate := module.NewServices()
	require.NoError(t, module.Provide[AuthorizationSubjectsOn](duplicate, resolve))
	require.ErrorContains(t, instance.ProvideServices(duplicate), "provide IAM transaction authorization subjects")
}

func TestAuthorizationReferencesOnValidatesCurrentTenantAnchors(t *testing.T) {
	h, entity, _ := transactionAuthorizationFixture(t)
	h.seedAdditionalMember(t, 1, 12, MemberActive)
	now := repositoryTestTime
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_departments
		(tenant_id,id,entity_id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
		VALUES (1,100,?,NULL,'Engineering','engineering','active',0,1,?,?),
		       (1,101,?,NULL,'Disabled','disabled','disabled',0,1,?,?)`, entity.ID, now, now, entity.ID, now, now).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		valid, err := h.authorizer.AuthorizationReferencesOn(t.Context(), tx, 1, AuthorizationReferences{
			EntityID: entity.ID, DepartmentID: 100, PrincipalID: 12,
		})
		require.True(t, valid)
		return err
	}))
	for _, references := range []AuthorizationReferences{
		{EntityID: 999}, {DepartmentID: 101}, {DepartmentID: 999}, {PrincipalID: 999},
	} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			valid, err := h.authorizer.AuthorizationReferencesOn(t.Context(), tx, 1, references)
			require.False(t, valid)
			return err
		}))
	}
	want := errors.New("rollback reference change")
	err := h.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Table(query.BindNames("sl_").Entities.String()).Where("tenant_id = ? AND id = ?", 1, entity.ID).UpdateColumn("status", EntityDisabled).Error)
		require.NoError(t, h.authorizer.revisions.Bump(t.Context(), tx, 1, repositoryTestTime))
		valid, err := h.authorizer.AuthorizationReferencesOn(t.Context(), tx, 1, AuthorizationReferences{EntityID: entity.ID})
		require.False(t, valid)
		if err != nil {
			return err
		}
		return want
	})
	require.ErrorIs(t, err, want)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		valid, err := h.authorizer.AuthorizationReferencesOn(t.Context(), tx, 1, AuthorizationReferences{EntityID: entity.ID})
		require.True(t, valid)
		return err
	}))
}

func TestAuthorizationReferencesOnFailsClosedAndPublishesStableCapability(t *testing.T) {
	h, entity, _ := transactionAuthorizationFixture(t)
	for _, input := range []struct {
		tenant     guid.ID
		references AuthorizationReferences
	}{{0, AuthorizationReferences{EntityID: entity.ID}}, {1, AuthorizationReferences{}}, {1, AuthorizationReferences{EntityID: -1}}} {
		err := h.db.Transaction(func(tx *gorm.DB) error {
			_, err := h.authorizer.AuthorizationReferencesOn(t.Context(), tx, input.tenant, input.references)
			return err
		})
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, db := range []*gorm.DB{nil, h.db} {
		_, err := h.authorizer.AuthorizationReferencesOn(t.Context(), db, 1, AuthorizationReferences{EntityID: entity.ID})
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, authorizer := range []*Authorizer{nil, {}, NewAuthorizer(h.runtime)} {
		err := h.db.Transaction(func(tx *gorm.DB) error {
			_, err := authorizer.AuthorizationReferencesOn(t.Context(), tx, 1, AuthorizationReferences{EntityID: entity.ID})
			return err
		})
		require.ErrorIs(t, err, ErrUnavailable)
	}
	instance := NewModule(h.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	resolve, err := module.Resolve[AuthorizationReferencesOn](services)
	require.NoError(t, err)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, err := resolve(t.Context(), tx, 1, AuthorizationReferences{EntityID: entity.ID})
		return err
	})
	require.ErrorIs(t, err, ErrUnavailable)
	*instance.checker = *h.authorizer
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		valid, err := resolve(t.Context(), tx, 1, AuthorizationReferences{EntityID: entity.ID})
		require.True(t, valid)
		return err
	}))
	duplicate := module.NewServices()
	require.NoError(t, module.Provide[AuthorizationReferencesOn](duplicate, resolve))
	require.ErrorContains(t, instance.ProvideServices(duplicate), "provide IAM transaction authorization references")
}

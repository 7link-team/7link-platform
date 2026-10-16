package core

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// EntityTypeReservations is supplied by the business module that owns entity
// lifecycles. Core snapshots the declarations during dependency injection.
type EntityTypeReservations func() []string

// EntityProvisioner lets a business owner read, create or update an IAM entity in its
// own transaction. The caller must authorize its business operation first and
// roll back the transaction on every error. IAM validation, file-root creation,
// policy revision changes and audit remain owned by core.
type EntityProvisioner struct {
	GetOn    func(context.Context, *gorm.DB, guid.ID, guid.ID) (Entity, error)
	CreateOn func(context.Context, *gorm.DB, Entity) (Entity, error)
	UpdateOn func(context.Context, *gorm.DB, guid.ID, guid.ID, EntityPatch) (Entity, error)
}

func (m *Module) entityProvisioner() EntityProvisioner {
	return EntityProvisioner{
		GetOn: func(ctx context.Context, tx *gorm.DB, tenantID, entityID guid.ID) (Entity, error) {
			service, err := m.entityServiceOn(tx)
			if err != nil {
				return Entity{}, err
			}
			return service.GetEntity(ctx, tenantID, entityID)
		},
		CreateOn: func(ctx context.Context, tx *gorm.DB, entity Entity) (Entity, error) {
			service, err := m.entityServiceOn(tx)
			if err != nil {
				return Entity{}, err
			}
			// Business provisioning must not silently omit the entity's file root.
			if service.roots == nil {
				return Entity{}, ErrUnavailable
			}
			service.ownedEntityWrites = true
			return service.CreateEntity(ctx, entity)
		},
		UpdateOn: func(ctx context.Context, tx *gorm.DB, tenantID, entityID guid.ID, patch EntityPatch) (Entity, error) {
			service, err := m.entityServiceOn(tx)
			if err != nil {
				return Entity{}, err
			}
			service.ownedEntityWrites = true
			return service.UpdateEntity(ctx, tenantID, entityID, patch)
		},
	}
}

// Bind both the repository and write coordinator: binding only one would let
// validation and mutation observe different transactions.
func (m *Module) entityServiceOn(tx *gorm.DB) (*Service, error) {
	if m == nil || m.service == nil || m.service.repo == nil || m.service.writes == nil || tx == nil || tx.Statement == nil {
		return nil, ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	service := *m.service
	service.repo = m.service.repo.withDB(tx)
	service.writes = newTransactionCoordinator(service.repo, m.service.writes.audit)
	return &service, nil
}

// EntityRoleBindingStore reuses IAM grant validation and auditing when a
// business owner changes entity members in its own atomic operation. For a
// reserved entity type the owner must authorize the operation and validate its
// role/subject mapping first; this capability then writes the business-owned
// binding without borrowing the initiating user's general role-grant authority.
// Unreserved entity types retain ordinary grantability checks.
type EntityRoleBindingStore struct {
	PutOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, BindingEffect, time.Time) (EntityRoleBinding, bool, error)
}

func (m *Module) entityRoleBindingStore() EntityRoleBindingStore {
	return EntityRoleBindingStore{PutOn: func(ctx context.Context, tx *gorm.DB, tenantID, entityID, roleID, principalID guid.ID, effect BindingEffect, expiresAt time.Time) (EntityRoleBinding, bool, error) {
		service, err := m.entityServiceOn(tx)
		if err != nil {
			return EntityRoleBinding{}, false, err
		}
		service.ownedEntityBindings = true
		return service.PutEntityRoleBinding(ctx, tenantID, entityID, roleID, principalID, effect, expiresAt)
	}}
}

package files

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

var (
	errOperationForbidden   = errors.New("files: operation forbidden")
	errOperationUnavailable = errors.New("files: operation service unavailable")
)

// operationService owns the request transaction; the access capability must
// enforce live, transaction-bound root IAM. No public route is registered
// until that capability is wired. Nil dependencies never degrade to allow-all.
type operationService struct {
	runtime        *appenv.Runtime
	driver         Driver
	objects        *AccountObjects
	ids            guid.Source
	policy         core.EntityConstraintOn
	tenantPolicy   core.TenantConstraintOn
	subjects       core.AuthorizationSubjectsOn
	references     core.AuthorizationReferencesOn
	appender       audit.Appender
	budget         operationBudget
	timeout        time.Duration
	uploadTimeout  time.Duration
	maxUploadBytes int64
}

func (s *operationService) run(ctx context.Context, key string, input immediateOperationRequest) (operationResult, error) {
	var result operationResult
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID <= 0 ||
		(claims.TenantID == 0 && claims.EntityID != 0) {
		return result, errOperationForbidden
	}
	if s == nil || s.runtime == nil || s.runtime.Database() == nil || s.ids == nil || s.appender == nil || s.timeout <= 0 ||
		(claims.EntityID > 0 && (s.policy == nil || s.subjects == nil || s.references == nil)) ||
		(claims.TenantID > 0 && claims.EntityID == 0 && (s.tenantPolicy == nil || s.references == nil)) {
		return result, errOperationUnavailable
	}
	db := s.runtime.Database().Write()
	if db == nil {
		return result, errOperationUnavailable
	}
	workContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	err := db.WithContext(workContext).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(workContext, tx, claims, tree)
		if err != nil {
			return err
		}
		destination, err := activeNode(workContext, tx, tree.prefix, input.DestinationID)
		if err != nil {
			return err
		}
		request := input
		if request.SpaceID != 0 && request.SpaceID != destination.SpaceID {
			return errOperationForbidden
		}
		request.SpaceID = destination.SpaceID
		request, _, _, err = canonicalImmediateRequest(request, key, s.budget)
		if err != nil {
			return err
		}
		q, space, err := tree.lockOperationSpace(workContext, tx, request.SpaceID)
		if err != nil {
			return err
		}
		if (claims.TenantID == 0 && (space.ScopeKind != spaceAccount || space.PrincipalID == nil || *space.PrincipalID != claims.PrincipalID)) ||
			(claims.TenantID > 0 && (space.ScopeKind != spaceTenant || space.TenantID == nil || *space.TenantID != claims.TenantID)) {
			// A caller must not be able to distinguish an absent destination
			// from an active node owned by another account or tenant.
			return ErrNotFound
		}
		operation := q.Operation
		previous, err := operation.WithContext(workContext).Where(operation.SpaceID.Eq(space.ID), operation.ActorID.Eq(claims.PrincipalID), operation.IdempotencyKey.Eq(key)).Count()
		if err != nil {
			return err
		}
		id, err := s.ids.Next()
		if err != nil || id <= 0 {
			return errors.Join(errOperationInput, err)
		}
		now := time.Now()
		result, err = tree.executeImmediateOperation(workContext, tx, guid.ID(id), claims.PrincipalID, key, request, s.budget, now.Add(s.timeout), now, s.ids.Next, authorizer.access)
		if err != nil || previous != 0 {
			return err
		}
		event := audit.NewDomainEvent(workContext, claims.TenantID, "file_"+request.Action, "file_operation", guid.ID(id))
		event.Detail["space_id"] = space.ID
		event.Detail["entity_id"] = claims.EntityID
		event.Detail["destination_id"] = request.DestinationID
		event.Detail["result"] = result
		return s.appender(workContext, tx, event)
	})
	if err != nil {
		return operationResult{}, err
	}
	return result, nil
}

func (s *operationService) authorize(ctx context.Context, tx *gorm.DB, claims *authn.Claims, tree directoryTree) (*nodeAuthorizer, error) {
	if s == nil || tx == nil || claims == nil || claims.PrincipalID <= 0 {
		return nil, errOperationUnavailable
	}
	if claims.TenantID == 0 && claims.EntityID == 0 {
		return newAccountNodeAuthorizer(tree, claims.PrincipalID)
	}
	if claims.TenantID <= 0 || s.references == nil {
		return nil, errOperationUnavailable
	}
	if claims.EntityID == 0 {
		if s.tenantPolicy == nil {
			return nil, errOperationUnavailable
		}
		constraint, err := s.tenantPolicy(ctx, tx, claims.TenantID, fileViewPermission, claims.PrincipalID)
		if err != nil {
			return nil, err
		}
		if !constraint.AllowAll {
			return nil, errOperationForbidden
		}
		return newTenantNodeAuthorizer(tree, s.tenantPolicy, s.references, claims.TenantID, claims.PrincipalID, constraint.Revision)
	}
	if s.policy == nil || s.subjects == nil {
		return nil, errOperationUnavailable
	}
	// Fence IAM before business reads. This evaluation alone is not a node
	// grant: every file root and action still pass through nodeAuthorizer.
	constraint, err := s.policy(ctx, tx, claims.TenantID, claims.EntityID, fileViewPermission, claims.PrincipalID)
	if err != nil {
		return nil, err
	}
	if !constraint.AllowsEntityRows(claims.EntityID) {
		return nil, errOperationForbidden
	}
	subjects, err := s.subjects(ctx, tx, claims.TenantID, claims.EntityID, claims.PrincipalID)
	if err != nil {
		return nil, err
	}
	if subjects.PrincipalID != claims.PrincipalID || subjects.Revision <= 0 || subjects.Revision != constraint.Revision {
		return nil, errOperationForbidden
	}
	return newNodeAuthorizer(tree, s.policy, s.references, claims.TenantID, claims.EntityID, claims.PrincipalID, subjects)
}

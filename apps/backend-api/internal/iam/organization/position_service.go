package organization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
)

type PositionService struct {
	repo           *Repository
	members        core.ActiveMemberChecker
	administrators core.AdministratorGuard
	now            func() time.Time
}

func NewPositionService(runtime *appenv.Runtime, appender audit.Appender, members core.ActiveMemberChecker, administrators core.AdministratorGuard, ids guid.Source) *PositionService {
	if runtime == nil || appender == nil || members == nil || administrators == nil || ids == nil {
		panic("position service requires runtime, audit appender, active member checker, administrator guard, and id generator")
	}
	return &PositionService{repo: NewRepository(runtime, ids, appender), members: members, administrators: administrators, now: time.Now}
}

func (s *PositionService) List(ctx context.Context, tenantID guid.ID) ([]Position, error) {
	if tenantID.Zero() {
		return nil, ErrPositionInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	rows, err := repo.listPositions(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	result := make([]Position, len(rows))
	for i := range rows {
		result[i] = positionFromRow(&rows[i])
	}
	return result, nil
}
func (s *PositionService) Get(ctx context.Context, tenantID, id guid.ID) (Position, error) {
	if tenantID.Zero() || !validID(id) {
		return Position{}, ErrPositionInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return Position{}, err
	}
	row, err := repo.getPosition(ctx, tenantID, id)
	if err != nil {
		return Position{}, err
	}
	return positionFromRow(row), nil
}
func (s *PositionService) Create(ctx context.Context, tenantID guid.ID, input CreatePosition) (Position, error) {
	tenantID, input, err := normalizePositionCreate(tenantID, input)
	if err != nil {
		return Position{}, err
	}
	id, err := s.repo.nextID()
	if err != nil {
		return Position{}, fmt.Errorf("generate position id: %w", err)
	}
	now := organizationTime(s.now())
	row := positionRow{TenantID: tenantID, ID: id, Code: input.Code, Name: input.Name, Status: input.Status, SortOrder: input.SortOrder, Version: 1, CreatedAt: now, UpdatedAt: now}
	event := audit.NewDomainEvent(ctx, tenantID, "position_created", "position", id)
	event.Detail["status"] = input.Status
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.insertPosition(ctx, &row); err != nil {
			return err
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Position{}, fmt.Errorf("create position: %w", err)
	}
	return positionFromRow(&row), nil
}
func (s *PositionService) Update(ctx context.Context, tenantID, id guid.ID, input UpdatePosition) (Position, error) {
	tenantID, id, input, err := normalizePositionUpdate(tenantID, id, input)
	if err != nil {
		return Position{}, err
	}
	now := organizationTime(s.now())
	var updated *positionRow
	event := audit.NewDomainEvent(ctx, tenantID, "position_updated", "position", id)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getPosition(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if current.Version != input.Version {
			return ErrPositionVersionConflict
		}
		candidate := *current
		updated = &candidate
		if input.Code != nil {
			updated.Code = *input.Code
		}
		if input.Name != nil {
			updated.Name = *input.Name
		}
		if input.Status != nil {
			updated.Status = *input.Status
		}
		if input.SortOrder != nil {
			updated.SortOrder = *input.SortOrder
		}
		changed := updated.Code != current.Code || updated.Name != current.Name || updated.Status != current.Status || updated.SortOrder != current.SortOrder
		if !changed {
			return nil
		}
		var verify func() error
		if current.Status == StatusActive && updated.Status == StatusDisabled {
			verify, err = s.administrators.Protect(ctx, repo.writer(), tenantID)
			if err != nil {
				return err
			}
		}
		updated.Version, updated.UpdatedAt = current.Version+1, now
		if err := repo.updatePosition(ctx, updated, current.Version); err != nil {
			return err
		}
		if verify != nil {
			if err := verify(); err != nil {
				return err
			}
		}
		event.Detail["code_changed"], event.Detail["name_changed"] = updated.Code != current.Code, updated.Name != current.Name
		event.Detail["status_changed"], event.Detail["sort_order_changed"] = updated.Status != current.Status, updated.SortOrder != current.SortOrder
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Position{}, fmt.Errorf("update position: %w", err)
	}
	return positionFromRow(updated), nil
}
func (s *PositionService) Delete(ctx context.Context, tenantID, id guid.ID, version int64) error {
	if tenantID.Zero() || !validID(id) || version < 1 {
		return ErrPositionInvalid
	}
	now := organizationTime(s.now())
	event := audit.NewDomainEvent(ctx, tenantID, "position_deleted", "position", id)
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getPosition(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if current.Version != version {
			return ErrPositionVersionConflict
		}
		if err := repo.deletePosition(ctx, tenantID, id, version); err != nil {
			return err
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return fmt.Errorf("delete position: %w", err)
	}
	return nil
}
func (s *PositionService) ListMembers(ctx context.Context, tenantID, positionID guid.ID) ([]PositionMember, error) {
	if tenantID.Zero() || !validID(positionID) {
		return nil, ErrPositionInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	if _, err := repo.getPosition(ctx, tenantID, positionID); err != nil {
		return nil, err
	}
	rows, err := repo.listPositionMembers(ctx, tenantID, positionID)
	if err != nil {
		return nil, err
	}
	result := make([]PositionMember, len(rows))
	for i := range rows {
		result[i] = positionMemberFromRow(&rows[i])
	}
	return result, nil
}
func (s *PositionService) PutMember(ctx context.Context, tenantID, positionID, principalID guid.ID, input PositionMemberWindow) (PositionMember, error) {
	tenantID, positionID, principalID, input, err := normalizePositionMember(tenantID, positionID, principalID, input)
	if err != nil {
		return PositionMember{}, err
	}
	now := organizationTime(s.now())
	var result *positionMemberRow
	event := audit.NewDomainEvent(ctx, tenantID, "position_member_assigned", "position", positionID)
	event.Detail["principal_id"] = principalID
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if _, err := repo.getPosition(ctx, tenantID, positionID); err != nil {
			return err
		}
		verify, err := s.administrators.Protect(ctx, repo.writer(), tenantID)
		if err != nil {
			return err
		}
		active, err := s.members.IsMemberActiveOn(ctx, repo.writer(), tenantID, principalID)
		if err != nil {
			return fmt.Errorf("check position member: %w", err)
		}
		if !active {
			return ErrPositionMemberInactive
		}
		current, getErr := repo.getPositionMember(ctx, tenantID, positionID, principalID)
		if getErr == nil && optionalTimesEqual(current.StartsAt, input.StartsAt) && optionalTimesEqual(current.EndsAt, input.EndsAt) {
			result = current
			return verify()
		}
		if getErr != nil && !errors.Is(getErr, ErrPositionMemberNotFound) {
			return getErr
		}
		createdAt := now
		if getErr == nil {
			createdAt = current.CreatedAt
		}
		row := model.PositionMember{TenantID: tenantID, PositionID: positionID, PrincipalID: principalID, StartsAt: input.StartsAt, EndsAt: input.EndsAt, CreatedAt: createdAt, UpdatedAt: now}
		if err := repo.putPositionMember(ctx, &row); err != nil {
			return err
		}
		result, err = repo.getPositionMember(ctx, tenantID, positionID, principalID)
		if err != nil {
			return err
		}
		if err := verify(); err != nil {
			return err
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return PositionMember{}, fmt.Errorf("assign position member: %w", err)
	}
	return positionMemberFromRow(result), nil
}
func (s *PositionService) DeleteMember(ctx context.Context, tenantID, positionID, principalID guid.ID) (bool, error) {
	tenantID, positionID, principalID, _, err := normalizePositionMember(tenantID, positionID, principalID, PositionMemberWindow{})
	if err != nil {
		return false, err
	}
	now := organizationTime(s.now())
	changed := false
	event := audit.NewDomainEvent(ctx, tenantID, "position_member_removed", "position", positionID)
	event.Detail["principal_id"] = principalID
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if _, err := repo.getPosition(ctx, tenantID, positionID); err != nil {
			return err
		}
		verify, err := s.administrators.Protect(ctx, repo.writer(), tenantID)
		if err != nil {
			return err
		}
		changed, err = repo.deletePositionMember(ctx, tenantID, positionID, principalID)
		if err != nil {
			return err
		}
		if err := verify(); err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return false, fmt.Errorf("remove position member: %w", err)
	}
	return changed, nil
}
func positionFromRow(row *positionRow) Position {
	return Position{ID: row.ID, TenantID: row.TenantID, Code: row.Code, Name: row.Name, Status: row.Status, SortOrder: row.SortOrder, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
func positionMemberFromRow(row *positionMemberRow) PositionMember {
	return PositionMember{PositionID: row.PositionID, PrincipalID: row.PrincipalID, DisplayName: row.DisplayName, Email: row.Email, StartsAt: row.StartsAt, EndsAt: row.EndsAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

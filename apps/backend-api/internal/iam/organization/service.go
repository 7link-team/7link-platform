package organization

import (
	"context"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
)

type Service struct {
	repo *Repository
	now  func() time.Time
}

func NewService(runtime *appenv.Runtime, appender audit.Appender, ids guid.Source) *Service {
	if runtime == nil || appender == nil || ids == nil {
		panic("organization service requires runtime, audit appender, and id generator")
	}
	return &Service{repo: NewRepository(runtime, ids, appender), now: time.Now}
}

func (s *Service) List(ctx context.Context, tenantID guid.ID) ([]Department, error) {
	if tenantID.Zero() {
		return nil, ErrInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	rows, err := repo.listDepartments(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return orderDepartments(rows)
}

func (s *Service) Get(ctx context.Context, tenantID, id guid.ID) (Department, error) {
	if tenantID.Zero() || !validID(id) {
		return Department{}, ErrInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return Department{}, err
	}
	row, err := repo.getDepartment(ctx, tenantID, id)
	if err != nil {
		return Department{}, err
	}
	depth, err := departmentDepth(ctx, repo, tenantID, id)
	if err != nil {
		return Department{}, err
	}
	return departmentFromRow(row, depth), nil
}

func (s *Service) Create(ctx context.Context, tenantID guid.ID, input CreateDepartment) (Department, error) {
	tenantID, input, err := normalizeCreate(tenantID, input)
	if err != nil {
		return Department{}, err
	}
	id, err := s.repo.nextID()
	if err != nil {
		return Department{}, fmt.Errorf("generate department id: %w", err)
	}
	now := organizationTime(s.now())
	row := departmentRow{TenantID: tenantID, ID: id, ParentID: idOrNil(input.ParentID), Name: input.Name, NameKey: nameKey(input.Name), Status: input.Status, SortOrder: input.SortOrder, Version: 1, CreatedAt: now, UpdatedAt: now}
	event := audit.NewDomainEvent(ctx, tenantID, "department_created", "department", id)
	event.Detail["status"], event.Detail["has_parent"] = input.Status, !input.ParentID.Zero()
	createdDepth := 0
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.insertDepartment(ctx, &row); err != nil {
			return err
		}
		createdDepth, err = departmentDepth(ctx, repo, tenantID, id)
		if err != nil {
			return err
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Department{}, fmt.Errorf("create department: %w", err)
	}
	return departmentFromRow(&row, createdDepth), nil
}

func (s *Service) Update(ctx context.Context, tenantID, id guid.ID, input UpdateDepartment) (Department, error) {
	tenantID, id, input, err := normalizeUpdate(tenantID, id, input)
	if err != nil {
		return Department{}, err
	}
	now := organizationTime(s.now())
	var updated *departmentRow
	updatedDepth := 0
	event := audit.NewDomainEvent(ctx, tenantID, "department_updated", "department", id)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getDepartment(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if current.Version != input.Version {
			return ErrVersionConflict
		}
		candidate := *current
		updated = &candidate
		if input.ParentID != nil {
			updated.ParentID = idOrNil(*input.ParentID)
		}
		if input.Name != nil {
			updated.Name, updated.NameKey = *input.Name, nameKey(*input.Name)
		}
		if input.Status != nil {
			updated.Status = *input.Status
		}
		if input.SortOrder != nil {
			updated.SortOrder = *input.SortOrder
		}
		parentChanged := optionalID(updated.ParentID) != optionalID(current.ParentID)
		if parentChanged && updated.ParentID != nil {
			if _, err := repo.getDepartment(ctx, tenantID, *updated.ParentID); err != nil {
				return err
			}
			cycle, err := repo.isDepartmentDescendant(ctx, tenantID, id, *updated.ParentID)
			if err != nil {
				return err
			}
			if cycle {
				return ErrHierarchyCycle
			}
		}
		changed := parentChanged || updated.Name != current.Name || updated.Status != current.Status || updated.SortOrder != current.SortOrder
		if !changed {
			updatedDepth, err = departmentDepth(ctx, repo, tenantID, id)
			return err
		}
		updated.Version, updated.UpdatedAt = current.Version+1, now
		if err := repo.updateDepartment(ctx, updated, current.Version); err != nil {
			return err
		}
		if parentChanged {
			if err := repo.moveDepartment(ctx, tenantID, id, updated.ParentID); err != nil {
				return err
			}
		}
		updatedDepth, err = departmentDepth(ctx, repo, tenantID, id)
		if err != nil {
			return err
		}
		event.Detail["parent_changed"], event.Detail["name_changed"] = parentChanged, updated.Name != current.Name
		event.Detail["status_changed"], event.Detail["sort_order_changed"] = updated.Status != current.Status, updated.SortOrder != current.SortOrder
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Department{}, fmt.Errorf("update department: %w", err)
	}
	return departmentFromRow(updated, updatedDepth), nil
}

func (s *Service) Delete(ctx context.Context, tenantID, id guid.ID, version int64) error {
	if tenantID.Zero() || !validID(id) || version < 1 {
		return ErrInvalid
	}
	now := organizationTime(s.now())
	event := audit.NewDomainEvent(ctx, tenantID, "department_deleted", "department", id)
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getDepartment(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if current.Version != version {
			return ErrVersionConflict
		}
		if err := repo.deleteDepartment(ctx, tenantID, id, version); err != nil {
			return err
		}
		event.Detail["had_parent"] = current.ParentID != nil
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return fmt.Errorf("delete department: %w", err)
	}
	return nil
}

func departmentDepth(ctx context.Context, repo *Repository, tenantID, id guid.ID) (int, error) {
	ancestors, err := repo.departmentAncestors(ctx, tenantID, id)
	if err != nil {
		return 0, err
	}
	if len(ancestors) == 0 {
		return 0, ErrNotFound
	}
	return len(ancestors) - 1, nil
}

func organizationTime(value time.Time) time.Time { return value.UTC().Truncate(time.Millisecond) }

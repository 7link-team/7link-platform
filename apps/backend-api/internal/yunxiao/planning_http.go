package yunxiao

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PlanningPath struct {
	WorkProjectPath
	PlanningID string `path:"planning_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type planningCreateInput struct {
	WorkProjectPath
	WriteHeaders
	Body PlanningEntityInput
}
type planningUpdateInput struct {
	PlanningPath
	WriteHeaders
	Body PlanningEntityInput
}
type planningStateBody struct {
	State             string   `json:"state" enum:"ACTIVE,CLOSED,LOCKED,PLANNING,RELEASED,ARCHIVED,ACHIEVED"`
	Reason            string   `json:"reason" maxLength:"500"`
	TargetIterationID *guid.ID `json:"target_iteration_id,omitempty"`
}
type planningStateInput struct {
	PlanningPath
	WriteHeaders
	Body planningStateBody
}
type planningListInput struct {
	WorkProjectPath
	Cursor string `query:"cursor" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Sort   string `query:"sort" default:"starts_at" enum:"id,starts_at"`
	State  string `query:"state" maxLength:"16" enum:"PLANNED,ACTIVE,CLOSED,PLANNING,LOCKED,RELEASED,ARCHIVED,ACHIEVED"`
}

// PlanningAssignmentPath stays exported for embedded Huma parameter discovery.
type PlanningAssignmentPath struct {
	WorkProjectPath
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type assignmentInput struct {
	PlanningAssignmentPath
	WriteHeaders
	Body PlanningAssignmentInput
}

func (m *Module) registerPlanningHTTP(api huma.API) error {
	op := func(id, method, path string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: id, Tags: []string{"work-planning"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 412, 422, 500, 503}}
	}
	base := "/projects/{project_id}"
	for _, kind := range []string{"iterations", "versions", "milestones"} {
		path := base + "/" + kind
		authz.RegisterTenantMember(m.registrar, api, op("planning."+kind+".list", "GET", path, 200), func(ctx context.Context, in *planningListInput) (*respx.Body[Page[PlanningEntity]], error) {
			tenant, project, err := workIDs(in.WorkProjectPath)
			if err != nil {
				return nil, httpError(ctx, err)
			}
			out := Page[PlanningEntity]{Items: []PlanningEntity{}}
			err = m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := m.projectAuthorityOn(ctx, tx, tenant, project, "work_item_read"); err != nil {
					return err
				}
				q := m.planningStore(tx, kind).Where(map[string]any{"tenant_id": tenant, "project_id": project})
				if in.State != "" {
					if kind == "milestones" {
						if in.State != "ACHIEVED" {
							return ErrInvalid
						}
						q = q.Where(clause.Neq{Column: clause.Column{Name: "achieved_at"}, Value: nil})
					} else {
						q = q.Where(map[string]any{"state": in.State})
					}
				}
				sortDates := kind == "iterations" && in.Sort != "id"
				if sortDates {
					q = q.Order(clause.OrderByColumn{Column: clause.Column{Name: "starts_at"}})
				}
				if in.Cursor != "" {
					cursor, e := guid.RequireID(in.Cursor)
					if e != nil {
						return ErrInvalid
					}
					if sortDates {
						anchor, e := m.planningEntityOn(ctx, tx, tenant, project, cursor, kind)
						if e != nil {
							return e
						}
						q = q.Where(clause.Or(clause.Gt{Column: clause.Column{Name: "starts_at"}, Value: anchor.StartsAt}, clause.And(clause.Eq{Column: clause.Column{Name: "starts_at"}, Value: anchor.StartsAt}, clause.Gt{Column: clause.Column{Name: "id"}, Value: cursor})))
					} else {
						q = q.Where(clause.Gt{Column: clause.Column{Name: "id"}, Value: cursor})
					}
				}
				limit := in.Limit
				if limit < 1 || limit > 100 {
					limit = 50
				}
				if err := q.Order("id").Limit(limit + 1).Find(&out.Items).Error; err != nil {
					return err
				}
				if len(out.Items) > limit {
					out.HasMore = true
					out.Items = out.Items[:limit]
					out.NextCursor = out.Items[limit-1].ID.String()
				}
				return nil
			})
			if err != nil {
				return nil, httpError(ctx, err)
			}
			return respx.OK(ctx, out), nil
		})
		authz.RegisterTenantMember(m.registrar, api, op("planning."+kind+".create", "POST", path, 201), func(ctx context.Context, in *planningCreateInput) (*commandOutput[PlanningEntity], error) {
			tenant, project, err := workIDs(in.WorkProjectPath)
			if err != nil {
				return nil, httpError(ctx, err)
			}
			return workCommand(m, ctx, in.WorkProjectPath, in.WriteHeaders, "planning."+kind+".create", "workflow_manage", in.Body, 201, func(tx *gorm.DB) (PlanningEntity, int, error) {
				v, e := m.savePlanningEntityOn(ctx, tx, tenant, project, 0, kind, 0, in.Body)
				return v, v.Version, e
			})
		})
		authz.RegisterTenantMember(m.registrar, api, op("planning."+kind+".update", "PATCH", path+"/{planning_id}", 200), func(ctx context.Context, in *planningUpdateInput) (*commandOutput[PlanningEntity], error) {
			tenant, project, err := workIDs(in.WorkProjectPath)
			id, e := guid.RequireID(in.PlanningID)
			version, ve := versionHeader(in.IfMatch)
			if err != nil || e != nil || ve != nil {
				return nil, httpError(ctx, ErrInvalid)
			}
			return workCommand(m, ctx, in.WorkProjectPath, in.WriteHeaders, "planning."+kind+".update:"+id.String(), "workflow_manage", in.Body, 200, func(tx *gorm.DB) (PlanningEntity, int, error) {
				v, e := m.savePlanningEntityOn(ctx, tx, tenant, project, id, kind, version, in.Body)
				return v, v.Version, e
			})
		})
		authz.RegisterTenantMember(m.registrar, api, op("planning."+kind+".state", "PUT", path+"/{planning_id}/state", 200), func(ctx context.Context, in *planningStateInput) (*commandOutput[PlanningEntity], error) {
			tenant, project, err := workIDs(in.WorkProjectPath)
			id, e := guid.RequireID(in.PlanningID)
			version, ve := versionHeader(in.IfMatch)
			if err != nil || e != nil || ve != nil {
				return nil, httpError(ctx, ErrInvalid)
			}
			return commandAuthorized(m, ctx, tenant, in.WriteHeaders, "planning."+kind+".state:"+project.String()+":"+id.String(), in.Body, 200, func(tx *gorm.DB) error {
				return m.planningStateAuthorityOn(ctx, tx, tenant, project, id, kind, in.Body.State)
			}, func(tx *gorm.DB) (PlanningEntity, int, error) {
				v, e := m.changePlanningStateOn(ctx, tx, tenant, project, id, kind, version, in.Body.State, in.Body.Reason, in.Body.TargetIterationID)
				return v, v.Version, e
			})
		})
	}
	authz.RegisterTenantMember(m.registrar, api, op("planning.assignment.get", "GET", base+"/work-items/{work_item_id}/planning", 200), m.getPlanningAssignmentHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("planning.assignment.update", "PUT", base+"/work-items/{work_item_id}/planning", 200), m.putPlanningAssignmentHTTP)
	return nil
}
func (m *Module) getPlanningAssignmentHTTP(ctx context.Context, in *PlanningAssignmentPath) (*respx.Body[PlanningAssignment], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	id, e := guid.RequireID(in.WorkItemID)
	if err != nil || e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	out := PlanningAssignment{WorkItemID: id}
	err = m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.projectAuthorityOn(ctx, tx, tenant, project, "work_item_read"); err != nil {
			return err
		}
		if m.planningWork == nil {
			return ErrUnavailable
		}
		if _, err := m.planningWork(ctx, tx, tenant, project, id); err != nil {
			return err
		}
		row, err := m.assignmentOn(ctx, tx, tenant, project, id)
		if row != nil {
			out = assignmentView(row)
		}
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) putPlanningAssignmentHTTP(ctx context.Context, in *assignmentInput) (*commandOutput[PlanningAssignment], error) {
	tenant, project, err := workIDs(in.WorkProjectPath)
	id, e := guid.RequireID(in.WorkItemID)
	version, ve := versionHeader(in.IfMatch)
	if in.IfMatch == "\"0\"" {
		version = 0
		ve = nil
	}
	if err != nil || e != nil || ve != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return commandAuthorized(m, ctx, tenant, in.WriteHeaders, "planning.assignment.update:"+project.String()+":"+id.String(), in.Body, 200, func(tx *gorm.DB) error {
		if err := m.projectAuthorityOn(ctx, tx, tenant, project, "work_item_write"); err != nil {
			return err
		}
		if m.planningWork == nil {
			return ErrUnavailable
		}
		_, err := m.planningWork(ctx, tx, tenant, project, id)
		return err
	}, func(tx *gorm.DB) (PlanningAssignment, int, error) {
		v, e := m.assignPlanningOn(ctx, tx, tenant, project, id, version, in.Body)
		return v, v.Version, e
	})
}

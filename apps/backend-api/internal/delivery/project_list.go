package delivery

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/url"
	"strings"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformquery "7link.com/platform/pkg/query"
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

// projectRecordPage is the record portion of H2. Activity counts must be joined
// from their real owners before exposing the complete HTTP list response.
type projectRecordPage struct {
	Records []projectDetailView
	Total   int64
}

type projectListInput struct {
	RawQuery url.Values
	Sort     string
	Offset   int
	Limit    int
}

func projectFilterID(raw string) (driver.Valuer, error) {
	id, err := guid.RequireID(raw)
	return id, err
}

// listRecords keeps permission predicates before COUNT and pagination. Entity
// names are joined from IAM's published table, never copied into project rows.
func (s *projectService) listRecords(ctx context.Context, db *gorm.DB, access core.AccessTableNames, tenant guid.ID, constraint authz.DataConstraint, in projectListInput) (projectRecordPage, error) {
	var page projectRecordPage
	if s == nil || db == nil || s.names.Projects.IsZero() || access.Entities.IsZero() {
		return page, ErrUnavailable
	}
	if tenant <= 0 || in.Offset < 0 || in.Limit < 1 || in.Limit > 100 {
		return page, ErrInvalidArgument
	}
	p := query.BindProjects(db, s.names).As("project")
	var entityTable gen.DO
	entityTable.UseDB(db)
	entityTable.UseTable(access.Entities.String())
	entity := entityTable.As("project_entity")
	client := entityTable.As("project_client")
	name := field.NewString("project_entity", "name")
	clientName := field.NewString("project_client", "name")
	contract := platformquery.New(
		platformquery.Enum("stage", p.Stage, []string{"INTAKE", "DISCOVERY", "PLANNING", "ACTIVE", "BLOCKED", "ACCEPTANCE_PENDING", "DELIVERED", "CLOSED", "CANCELLED"}).Ops(platformquery.Eq, platformquery.In), //nolint:misspell // Spec 07 freezes the stage value.
		platformquery.Enum("team_mode", p.TeamMode, []string{"HUMAN_ONLY", "HYBRID", "AUTONOMOUS"}).Ops(platformquery.Eq),
		platformquery.Enum("health", p.Health, []string{"GREEN", "AMBER", "RED", "NONE"}).Ops(platformquery.Eq, platformquery.In),
		platformquery.ID("client_entity_id", p.ClientEntityID, projectFilterID).Ops(platformquery.Eq),
		platformquery.ID("lead_agent_id", p.LeadAgentID, projectFilterID).Ops(platformquery.Eq),
		platformquery.String("code", p.Code).Ops(platformquery.Eq),
		platformquery.String("name", name).Ops(platformquery.Like),
	).Sortable(map[string]platformquery.SortKey{
		"name": {Column: name}, "stage_changed_at": {Column: p.StageChangedAt}, "health": {Column: p.Health}, "target_date": {Column: p.TargetDate, Nullable: true}, "created_at": {Column: p.CreatedAt},
	}).Reserve("archived", "sort", "offset", "limit", "lang")
	predicates, err := contract.Parse(in.RawQuery)
	if err != nil {
		return page, err
	}
	archived := false
	if raw, present := in.RawQuery["archived"]; present {
		if len(raw) != 1 || (raw[0] != "eq:true" && raw[0] != "eq:false") {
			return page, platformquery.Error{Field: "archived", Message: "must be eq:true or eq:false"}
		}
		archived = raw[0] == "eq:true"
	}
	if archived {
		predicates = append(predicates, p.ArchivedAt.IsNotNull())
	} else {
		predicates = append(predicates, p.ArchivedAt.IsNull())
	}
	parseIDs := func(raw []string) ([]driver.Valuer, error) {
		ids := make([]driver.Valuer, 0, len(raw))
		for _, value := range raw {
			id, err := guid.RequireID(value)
			if err != nil {
				return nil, ErrUnavailable
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	denied, err := parseIDs(constraint.DeniedIDs)
	if err != nil {
		return page, err
	}
	if len(denied) > 0 {
		predicates = append(predicates, p.ProjectID.NotIn(denied...))
	}
	if !constraint.AllowAll {
		resources, err := parseIDs(constraint.ResourceIDs)
		if err != nil {
			return page, err
		}
		owners, err := parseIDs(constraint.OwnerIDs)
		if err != nil {
			return page, err
		}
		var grants []field.Expr
		if len(resources) > 0 {
			grants = append(grants, p.ProjectID.In(resources...))
		}
		if len(owners) > 0 {
			grants = append(grants, p.CreatedBy.In(owners...))
		}
		// Projects have no department column. Department-only row grants cannot
		// widen access to every project; entity grants remain in ResourceIDs.
		if len(grants) == 0 {
			predicates = append(predicates, p.ProjectID.Eq(guid.ID(0)))
		} else if len(grants) == 1 {
			predicates = append(predicates, grants[0])
		} else {
			predicates = append(predicates, field.Or(grants...))
		}
	}
	sortBy := in.Sort
	if strings.TrimSpace(sortBy) == "" {
		sortBy = "-created_at"
	}
	order, err := contract.Ordering(sortBy, 3, p.ProjectID.Desc())
	if err != nil {
		return page, err
	}
	base := p.WithContext(ctx).Join(entity,
		field.NewField("project_entity", "tenant_id").EqCol(p.TenantID),
		field.NewField("project_entity", "id").EqCol(p.ProjectID),
		field.NewString("project_entity", "type").Eq("project"),
	).LeftJoin(client,
		field.NewField("project_client", "tenant_id").EqCol(p.TenantID),
		field.NewField("project_client", "id").EqCol(p.ClientEntityID),
		field.NewString("project_client", "type").Eq("client"),
	).Where(p.TenantID.Eq(tenant))
	for _, predicate := range predicates {
		base = base.Where(predicate)
	}
	page.Total, err = base.Count()
	if err != nil {
		return projectRecordPage{}, err
	}
	var rows []struct {
		model.Project
		Name       string  `gorm:"column:project_name"`
		ClientName *string `gorm:"column:client_name"`
	}
	err = base.Select(p.ALL, name.As("project_name"), clientName.As("client_name")).Order(order...).Offset(in.Offset).Limit(in.Limit).Scan(&rows)
	if err != nil {
		return projectRecordPage{}, err
	}
	page.Records = make([]projectDetailView, 0, len(rows))
	for _, row := range rows {
		record, err := projectRecordView(row.Project)
		if err != nil {
			return projectRecordPage{}, err
		}
		view := projectDetailView{ProjectView: record, Name: row.Name}
		if row.ClientEntityID != nil {
			if row.ClientName == nil {
				return projectRecordPage{}, ErrProjectNotFound
			}
			view.Client = &projectClientView{ID: *row.ClientEntityID, Name: *row.ClientName}
		}
		page.Records = append(page.Records, view)
	}
	return page, nil
}

// listProjectRecords resolves policy from the authenticated caller. A future H2
// handler must add activity summaries before serializing its public response.
func (m *Module) listProjectRecords(ctx context.Context, db *gorm.DB, in projectListInput) (projectRecordPage, error) {
	var page projectRecordPage
	if m == nil || db == nil || m.listConstraint == nil {
		return page, ErrUnavailable
	}
	tenant, principal := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	if tenant <= 0 || principal <= 0 {
		return page, ErrProjectForbidden
	}
	constraint, err := m.listConstraint(ctx, tenant, "project_read", principal)
	if err != nil {
		return page, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		page, err = m.service.listRecords(ctx, tx, m.projectAccess, tenant, constraint, in)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return projectRecordPage{}, err
	}
	return page, nil
}

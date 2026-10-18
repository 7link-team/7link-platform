package delivery

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	platformquery "7link.com/platform/pkg/query"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

// projectRecordPage is the record portion of H2. Activity counts must be joined
// from their real owners before exposing the complete HTTP list response.
type projectRecordPage struct {
	Records []ProjectListRecordView
	Total   int64
}

type projectListCounts struct {
	OpenWorkItems     int64 `json:"open_work_items"`
	NeedsReview       int64 `json:"needs_review"`
	WaitingDeliveries int64 `json:"waiting_deliveries"`
}

type projectListView struct {
	ProjectListRecordView
	Counts projectListCounts `json:"counts"`
}

// TransformSchema permits the explicit null emitted when a project has no
// client. Huma's nullable tag only supports scalar schemas, not object refs.
func (projectListView) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Properties["client"] = &huma.Schema{AnyOf: []*huma.Schema{
		schema.Properties["client"], {Type: "null"},
	}}
	return schema
}

type projectPage struct {
	Records []projectListView
	Total   int64
}

// ProjectListRecordView is the non-activity portion of the H2 response. It must
// not embed ProjectView: that detail contract contains numeric int64 amounts
// and configuration fields that do not belong in the list response.
type ProjectListRecordView struct {
	ProjectID     guid.ID            `json:"project_id"`
	Code          string             `json:"code"`
	Name          string             `json:"name"`
	Client        *projectClientView `json:"client"`
	Stage         string             `json:"stage"`
	TeamMode      string             `json:"team_mode"`
	Health        string             `json:"health"`
	HealthReasons json.RawMessage    `json:"health_reasons"`
	Budget        projectListBudget  `json:"budget"`
	TargetDate    *string            `json:"target_date"`
	LeadAgentID   *guid.ID           `json:"lead_agent_id"`
	Version       int                `json:"version"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type projectListBudget struct {
	Cap   *string  `json:"cap" doc:"Budget cap in USD micros as a decimal integer string; null means no cap."`
	Spent string   `json:"spent" doc:"Spent USD micros as a decimal integer string."`
	State string   `json:"state"`
	Pct   *float64 `json:"pct" doc:"Percentage of cap used; null when no cap exists. Amounts remain exact in cap and spent."`
}

func projectListRecord(record ProjectView, name string) ProjectListRecordView {
	budget := projectListBudget{Spent: strconv.FormatInt(record.BudgetSpentMicros, 10), State: record.BudgetState}
	if record.BudgetCapMicros != nil {
		capMicros := strconv.FormatInt(*record.BudgetCapMicros, 10)
		budget.Cap = &capMicros
		// The persisted cap is strictly positive. Divide before multiplying so
		// int64 amounts cannot overflow while computing the display percentage.
		pct := float64(record.BudgetSpentMicros) / float64(*record.BudgetCapMicros) * 100
		budget.Pct = &pct
	}
	return ProjectListRecordView{
		ProjectID: record.ProjectID, Code: record.Code, Name: name,
		Stage: record.Stage, TeamMode: record.TeamMode, Health: record.Health,
		HealthReasons: record.HealthReasons, Budget: budget, TargetDate: record.TargetDate,
		LeadAgentID: record.LeadAgentID, Version: record.Version, UpdatedAt: record.UpdatedAt,
	}
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
	page.Records = make([]ProjectListRecordView, 0, len(rows))
	for _, row := range rows {
		record, err := projectRecordView(row.Project)
		if err != nil {
			return projectRecordPage{}, err
		}
		view := projectListRecord(record, row.Name)
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

// listProjectRecords reads authorized records and real activity in one snapshot.
func (m *Module) listProjectRecords(ctx context.Context, db *gorm.DB, in projectListInput) (projectPage, error) {
	var page projectPage
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
		records, err := m.service.listRecords(ctx, tx, m.projectAccess, tenant, constraint, in)
		if err != nil {
			return err
		}
		page.Total = records.Total
		page.Records, err = m.service.listActivity(ctx, tx, tenant, records.Records)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return projectPage{}, err
	}
	return page, nil
}

// listActivity uses separate grouped reads to avoid work-item/delivery JOIN
// fanout. Only authorized page IDs are queried, never the whole tenant's rows.
func (s *projectService) listActivity(ctx context.Context, db *gorm.DB, tenant guid.ID, records []ProjectListRecordView) ([]projectListView, error) {
	views := make([]projectListView, len(records))
	if len(records) == 0 {
		return views, nil
	}
	ids := make([]driver.Valuer, len(records))
	indices := make(map[guid.ID]int, len(records))
	for i, record := range records {
		ids[i], indices[record.ProjectID] = record.ProjectID, i
		views[i].ProjectListRecordView = record
	}
	w := query.BindWorkItems(db, s.names)
	var work []struct {
		ProjectID guid.ID
		State     string
		RowCount  int64
	}
	err := w.WithContext(ctx).Select(w.ProjectID, w.State, w.ID.Count().As("row_count")).
		Where(w.TenantID.Eq(tenant), w.ProjectID.In(ids...), w.ArchivedAt.IsNull(), w.State.NotIn("ACCEPTED", "CANCELLED", "SKIPPED")). //nolint:misspell // Frozen business state.
		Group(w.ProjectID, w.State).Scan(&work)
	if err != nil {
		return nil, err
	}
	for _, row := range work {
		i := indices[row.ProjectID]
		views[i].Counts.OpenWorkItems += row.RowCount
		if row.State == "NEEDS_HUMAN_REVIEW" {
			views[i].Counts.NeedsReview += row.RowCount
		}
	}
	d := query.BindDeliveries(db, s.names)
	var deliveries []struct {
		ProjectID guid.ID
		RowCount  int64
	}
	err = d.WithContext(ctx).Select(d.ProjectID, d.ID.Count().As("row_count")).
		Where(d.TenantID.Eq(tenant), d.ProjectID.In(ids...), d.ArchivedAt.IsNull(), d.State.Eq("SENT")).
		Group(d.ProjectID).Scan(&deliveries)
	if err != nil {
		return nil, err
	}
	for _, row := range deliveries {
		views[indices[row.ProjectID]].Counts.WaitingDeliveries = row.RowCount
	}
	return views, nil
}

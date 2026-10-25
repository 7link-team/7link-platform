package planning

import (
	"cmp"
	"context"
	"database/sql/driver"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// WorkbenchLoad counts current visible work, not machine utilization or capacity.
type WorkbenchLoad struct {
	Active              *int    `json:"active" doc:"Current unexpired executor assignments, distinct from backlog."`
	Capacity            *int    `json:"capacity" doc:"Configured Agent concurrency; unavailable for Humans without a persisted capacity setting."`
	CapacityUnavailable string  `json:"capacity_unavailable,omitempty"`
	DisplayName         string  `json:"display_name,omitempty" doc:"Current active tenant member display name, when available."`
	PrincipalID         guid.ID `json:"principal_id"`
	Open                int     `json:"open"`
	Blocked             int     `json:"blocked"`
	Review              int     `json:"review"`
}

type WorkbenchActivity struct {
	Kind        string    `json:"kind" enum:"WORK_ACCEPTED,REPORT_CONFIRMED,WORK_ASSIGNED,WORK_CLAIMED,WORK_REWORK_REQUESTED,DELEGATED_ACCEPTANCE,CLIENT_RECEIPT"`
	ProjectID   guid.ID   `json:"project_id"`
	ObjectID    guid.ID   `json:"object_id"`
	PrincipalID guid.ID   `json:"principal_id"`
	Title       string    `json:"title"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type WorkbenchAcceptance struct {
	ProjectID  guid.ID   `json:"project_id"`
	WorkItemID guid.ID   `json:"work_item_id"`
	Title      string    `json:"title"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type WorkbenchSummary struct {
	KPIs                []WorkbenchKPI              `json:"kpis,omitempty" doc:"Five prototype metrics with explicit per-field unavailable reasons; no substitute work counts."`
	ProjectHealth       []delivery.HomeProjectFacts `json:"project_health,omitempty"`
	Activity            []WorkbenchActivity         `json:"activity,omitempty" doc:"Latest ten source activities: assignment, completion claim, rework, acceptance, applied delegation, client receipt and report confirmation; current authority is rechecked."`
	Machines            []fleet.MachineLoad         `json:"machines" doc:"Currently authorized machine slot reservations; not CPU utilization."`
	From                time.Time                   `json:"from"`
	To                  time.Time                   `json:"to"`
	Projects            int                         `json:"projects"`
	WorkItems           int                         `json:"work_items"`
	Open                int                         `json:"open"`
	Blocked             int                         `json:"blocked"`
	Review              int                         `json:"review"`
	Accepted            int                         `json:"accepted" doc:"Current accepted work whose actual accepted_at is in [from,to)."`
	ExecutionCreated    *int64                      `json:"execution_created,omitempty"`
	ExecutionCostMicros *string                     `json:"execution_cost_micros,omitempty" pattern:"^[0-9]+$" doc:"Exact decimal micros; encoded as text to preserve int64 precision in browser clients."`
	Load                []WorkbenchLoad             `json:"load"`
	Acceptances         []WorkbenchAcceptance       `json:"acceptances" doc:"Latest ten visible current acceptances in this period; not a general audit feed."`
	MissingSources      []string                    `json:"missing_sources"`
}

type workbenchInput struct {
	PlanningTenantInput
	From string `query:"from" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"UTC inclusive date; defaults to seven days before today."`
	To   string `query:"to" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$" doc:"UTC exclusive date; defaults to tomorrow. Maximum range 93 days."`
}

func (m *Module) workbenchOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, from, to string) (WorkbenchSummary, error) {
	out := WorkbenchSummary{Load: []WorkbenchLoad{}, Acceptances: []WorkbenchAcceptance{}, Activity: []WorkbenchActivity{}, MissingSources: []string{}}
	if m == nil || m.now == nil || m.facts.ProjectsOn == nil {
		return out, ErrUnavailable
	}
	today := m.now().UTC().Truncate(24 * time.Hour)
	start, end := today.AddDate(0, 0, -7), today.AddDate(0, 0, 1)
	var err error
	if from != "" {
		start, err = time.Parse(time.DateOnly, from)
		if err != nil {
			return out, ErrInvalid
		}
	}
	if to != "" {
		end, err = time.Parse(time.DateOnly, to)
		if err != nil {
			return out, ErrInvalid
		}
	}
	if !start.Before(end) || end.Sub(start) > 93*24*time.Hour {
		return out, ErrInvalid
	}
	out.From, out.To = start, end
	// Home is available to current internal members. Enumeration and every
	// source reader retain their current project and row authority checks.
	if err = m.authorizeHomeOn(ctx, tx, tenant); err != nil {
		return out, err
	}
	if m.workbenchMachines == nil {
		out.MissingSources = append(out.MissingSources, "MACHINE_LOAD")
	} else {
		out.Machines, err = m.workbenchMachines(ctx, tx, tenant)
		if errors.Is(err, fleet.ErrMachineForbidden) {
			out.Machines = []fleet.MachineLoad{}
			out.MissingSources = append(out.MissingSources, "MACHINE_LOAD")
		} else if err != nil {
			return out, err
		}
	}
	projects, err := m.facts.ProjectsOn(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	if len(projects) > 500 {
		return out, ErrUnavailable
	}
	loads := map[guid.ID]WorkbenchLoad{}
	seen := map[guid.ID]bool{}
	seenProjects := map[guid.ID]bool{}
	var created, cost int64
	createdKnown, costKnown := true, true
	for _, project := range projects {
		if project <= 0 || seenProjects[project] {
			return out, ErrUnavailable
		}
		seenProjects[project] = true
		facts, err := m.snapshotOn(ctx, tx, tenant, project, 0, start, end)
		if err != nil {
			return out, err
		}
		out.Projects++
		for _, source := range facts.MissingSources {
			if !slices.Contains(out.MissingSources, source) {
				out.MissingSources = append(out.MissingSources, source)
			}
		}
		for _, v := range []struct {
			value *int64
			total *int64
			known *bool
		}{{facts.ExecutionCreated, &created, &createdKnown}, {facts.ExecutionCostMicros, &cost, &costKnown}} {
			if v.value == nil {
				*v.known = false
			} else if *v.value < 0 || *v.total > int64(^uint64(0)>>1)-*v.value {
				return out, ErrUnavailable
			} else {
				*v.total += *v.value
			}
		}
		for _, work := range facts.Work {
			if work.ID <= 0 || seen[work.ID] || len(seen) >= 500 {
				return out, ErrUnavailable
			}
			seen[work.ID] = true
			out.WorkItems++
			if work.State == "ACCEPTED" && work.AcceptedAt != nil && !work.AcceptedAt.Before(start) && work.AcceptedAt.Before(end) {
				out.Accepted++
				out.Acceptances = append(out.Acceptances, WorkbenchAcceptance{ProjectID: project, WorkItemID: work.ID, Title: work.Title, AcceptedAt: *work.AcceptedAt})
			}
			if work.State == "ACCEPTED" || work.State == goalCancelled {
				continue
			}
			out.Open++
			load := loads[work.AccountablePrincipalID]
			load.PrincipalID = work.AccountablePrincipalID
			load.Open++
			if work.State == "BLOCKED" {
				out.Blocked++
				load.Blocked++
			}
			if work.State == "NEEDS_HUMAN_REVIEW" {
				out.Review++
				load.Review++
			}
			loads[work.AccountablePrincipalID] = load
		}
	}
	if createdKnown {
		out.ExecutionCreated = &created
	}
	if costKnown {
		exactCost := strconv.FormatInt(cost, 10)
		out.ExecutionCostMicros = &exactCost
	}
	for _, load := range loads {
		if m.principals.ActiveRow != nil && load.PrincipalID > 0 {
			member, err := m.members.IsMemberActiveOn(ctx, tx, tenant, load.PrincipalID)
			if err != nil {
				return out, err
			}
			if member {
				principal, active, err := m.principals.ActiveRow(ctx, tx, load.PrincipalID)
				if err != nil {
					return out, err
				}
				if active && principal.ID == load.PrincipalID {
					load.DisplayName = principal.DisplayName
				}
			}
		}
		out.Load = append(out.Load, load)
	}
	slices.SortFunc(out.Load, func(a, b WorkbenchLoad) int {
		if a.Open != b.Open {
			return b.Open - a.Open
		}
		if a.PrincipalID < b.PrincipalID {
			return -1
		}
		if a.PrincipalID > b.PrincipalID {
			return 1
		}
		return 0
	})
	slices.SortFunc(out.Acceptances, func(a, b WorkbenchAcceptance) int {
		if c := b.AcceptedAt.Compare(a.AcceptedAt); c != 0 {
			return c
		}
		if a.WorkItemID < b.WorkItemID {
			return -1
		}
		return 1
	})
	if len(out.Acceptances) > 10 {
		out.Acceptances = out.Acceptances[:10]
	}
	for _, work := range out.Acceptances {
		out.Activity = append(out.Activity, WorkbenchActivity{Kind: "WORK_ACCEPTED", ProjectID: work.ProjectID, ObjectID: work.WorkItemID, Title: work.Title, OccurredAt: work.AcceptedAt})
	}
	reports := query.BindReports(tx, m.names)
	projectScope := []driver.Valuer{}
	if _, err := m.authorize(ctx, tx, tenant, 0, authn.PrincipalIDFromContext(ctx), "report_read", true); err == nil {
		projectScope = append(projectScope, guid.ID(0))
	} else if !errors.Is(err, ErrForbidden) {
		return out, err
	}
	for _, project := range projects {
		projectScope = append(projectScope, project)
	}
	stmt := reports.WithContext(ctx).Where(reports.TenantID.Eq(tenant), reports.ProjectID.In(projectScope...), reports.State.Eq("CONFIRMED"), reports.ConfirmedAt.Gte(start), reports.ConfirmedAt.Lt(end))
	visibleReports := 0
	for offset := 0; visibleReports < 10; offset += 100 {
		rows, err := stmt.Order(reports.ConfirmedAt.Desc(), reports.ID.Desc()).Limit(100).Offset(offset).Find()
		if err != nil {
			return out, err
		}
		for _, report := range rows {
			if _, err := m.authorize(ctx, tx, tenant, report.ProjectID, report.PrincipalID, "report_read", false); errors.Is(err, ErrForbidden) || errors.Is(err, delivery.ErrWorkForbidden) {
				continue
			} else if err != nil {
				return out, err
			}
			out.Activity = append(out.Activity, WorkbenchActivity{Kind: "REPORT_CONFIRMED", ProjectID: report.ProjectID, ObjectID: report.ID, PrincipalID: report.PrincipalID, Title: report.PeriodStart, OccurredAt: *report.ConfirmedAt})
			visibleReports++
			if visibleReports == 10 {
				break
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	if err := m.homeMetricsOn(ctx, tx, tenant, projects, &out); err != nil {
		return out, err
	}
	slices.SortFunc(out.Activity, func(a, b WorkbenchActivity) int {
		if order := b.OccurredAt.Compare(a.OccurredAt); order != 0 {
			return order
		}
		if order := strings.Compare(a.Kind, b.Kind); order != 0 {
			return order
		}
		return cmp.Compare(b.ObjectID, a.ObjectID)
	})
	if len(out.Activity) > 10 {
		out.Activity = out.Activity[:10]
	}
	slices.Sort(out.MissingSources)
	return out, nil
}

func (m *Module) workbenchHTTP(ctx context.Context, in *workbenchInput) (*respx.Body[WorkbenchSummary], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, planningError(ctx, ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return nil, planningError(ctx, err)
	}
	var out WorkbenchSummary
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.workbenchOn(ctx, tx, tenant, in.From, in.To)
		return err
	})
	if err != nil {
		return nil, planningError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) registerWorkbenchHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "planning.workbench.summary", Method: http.MethodGet, Path: "/workbench-summary", Summary: "Summarize current authorized work, workload and period acceptances", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}, m.workbenchHTTP)
}

// authorizeHomeOn validates membership without granting any tenant permission.
func (m *Module) authorizeHomeOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) error {
	if tx == nil || tx.Statement == nil {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.principals.ActiveHumanByID == nil || m.members == nil {
		return ErrUnavailable
	}
	principal, active, err := m.principals.LockActiveRow(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active || principal.ID != actor {
		return ErrForbidden
	}
	principal, active, err = m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active || principal.ID != actor {
		return ErrForbidden
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	return nil
}

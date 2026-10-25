package delivery

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// PipelineStageRun is rebuilt from source receipts. Missing work, execution,
// start times or verdicts never acquire synthetic pending or successful rows.
type PipelineStageRun struct {
	ID             string     `json:"id"`
	ProjectID      guid.ID    `json:"project_id"`
	WorkItemID     guid.ID    `json:"work_item_id"`
	ResultID       guid.ID    `json:"result_id"`
	Stage          string     `json:"stage" enum:"PLATFORM_RERUN,HIDDEN_SUITE,TESTER,ACCEPTOR,HUMAN_REVIEW,DELIVERY"`
	InternalRound  *int       `json:"internal_round"`
	Attempt        int        `json:"attempt"`
	State          string     `json:"state" enum:"PENDING,RUNNING,PASSED,FAILED,INCONCLUSIVE,SKIPPED,BLOCKED"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at"`
	ExecutionID    *guid.ID   `json:"execution_id"`
	VerificationID *guid.ID   `json:"verification_id"`
	DecisionID     *guid.ID   `json:"decision_id"`
	DeliveryID     *guid.ID   `json:"delivery_id"`
	EvidenceRefs   []string   `json:"evidence_refs"`
}

type PipelineDeliveryLink struct{ DeliveryID, WorkItemID, ResultID guid.ID }
type PipelineClientSource func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, []PipelineDeliveryLink) ([]PipelineStageRun, error)
type PipelineClientConnector struct {
	Bind func(PipelineClientSource) error
}

func (m *Module) providePipeline(s *module.Services) error {
	return module.Provide(s, PipelineClientConnector{Bind: func(source PipelineClientSource) error {
		if source == nil || m.pipelineClient != nil {
			return ErrInvalidArgument
		}
		m.pipelineClient = source
		return nil
	}})
}

type PipelinePage struct {
	Items []PipelineStageRun `json:"items"`
	Total int                `json:"total"`
}

func pipelineVerdict(state, verdict string) string {
	if state != "DONE" {
		if state == "RUNNING" {
			return "RUNNING"
		}
		if state == "PENDING" {
			return "PENDING"
		}
		return "INCONCLUSIVE"
	}
	if slices.Contains([]string{"PASSED", "FAILED", "INCONCLUSIVE", "SKIPPED", "BLOCKED"}, verdict) {
		return verdict
	}
	return "INCONCLUSIVE"
}

func (m *Module) pipelineOn(ctx context.Context, tx *gorm.DB, tenant, project, workID guid.ID, offset, limit int) (PipelinePage, error) {
	out := PipelinePage{Items: []PipelineStageRun{}}
	if tx == nil || tenant <= 0 || project <= 0 || workID < 0 || offset < 0 || limit < 1 || limit > 200 {
		return out, ErrInvalidArgument
	}
	if authn.TenantIDFromContext(ctx) != tenant {
		return out, ErrWorkForbidden
	}
	if m == nil || m.service == nil || m.workPolicy == nil || m.pipelineClient == nil {
		return out, ErrUnavailable
	}
	policy, err := m.workPolicy(ctx, tx, tenant, project, "work_item_read", authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return out, err
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(policy, project, w.CreatedBy)
	if err != nil {
		return out, err
	}
	stmt := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ArchivedAt.IsNull())
	if scope != nil {
		stmt = stmt.Where(scope)
	}
	if workID > 0 {
		stmt = stmt.Where(w.ID.Eq(workID))
	}
	works, err := stmt.Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(works) > 10000 {
		return out, ErrUnavailable
	}
	if len(works) == 0 {
		return out, nil
	}
	ids := make([]driver.Valuer, len(works))
	workIDs := make([]guid.ID, len(works))
	allowed := map[guid.ID]bool{}
	for i, row := range works {
		ids[i] = row.ID
		workIDs[i] = row.ID
		allowed[row.ID] = true
	}
	versions := query.BindResultVersions(tx, m.service.names)
	results, err := versions.WithContext(ctx).Where(versions.TenantID.Eq(tenant), versions.ProjectID.Eq(project), versions.WorkItemID.In(ids...)).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(results) > 10000 {
		return out, ErrUnavailable
	}
	byResult := map[guid.ID]*model.ResultVersion{}
	for _, r := range results {
		byResult[r.ID] = r
	}
	rows := []PipelineStageRun{}
	v := query.BindVerificationRuns(tx, m.service.names)
	runs, err := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ProjectID.Eq(project), v.WorkItemID.In(ids...)).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(runs) > 10000 {
		return out, ErrUnavailable
	}
	for _, r := range runs {
		result := byResult[r.ResultID]
		if result == nil || result.WorkItemID != r.WorkItemID {
			return out, ErrUnavailable
		}
		stage := map[string]string{"AUTOMATED": "PLATFORM_RERUN", "AGENT_TEST": "TESTER", "AGENT_ACCEPTANCE": "ACCEPTOR", "HUMAN": "HUMAN_REVIEW"}[r.Stage]
		if stage == "" {
			return out, ErrUnavailable
		}
		state := pipelineVerdict(r.State, r.Verdict)
		if r.State == "DONE" && (r.CompletedAt == nil || (r.Method != "HUMAN" && len(r.ReceiptHash) != 64)) {
			state = "INCONCLUSIVE"
		}
		row := PipelineStageRun{ID: "verification:" + r.ID.String(), ProjectID: project, WorkItemID: r.WorkItemID, ResultID: r.ResultID, Stage: stage, InternalRound: &r.Round, State: state, CreatedAt: r.CreatedAt, EndedAt: r.CompletedAt, ExecutionID: result.SourceExecutionID, VerificationID: &r.ID, EvidenceRefs: []string{}}
		if r.ReceiptHash != "" {
			row.EvidenceRefs = append(row.EvidenceRefs, "receipt-sha256:"+r.ReceiptHash)
		}
		if r.TaskID != nil {
			row.EvidenceRefs = append(row.EvidenceRefs, "verification-task:"+r.TaskID.String())
		}
		rows = append(rows, row)
	}
	a := query.BindResultActions(tx, m.service.names)
	actions, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.In(ids...), a.Kind.In("ACCEPT", "RETURN")).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(actions) > 10000 {
		return out, ErrUnavailable
	}
	for _, r := range actions {
		result := byResult[r.ResultID]
		if result == nil || result.WorkItemID != r.WorkItemID {
			return out, ErrUnavailable
		}
		rows = append(rows, PipelineStageRun{ID: "decision:" + r.ID.String(), ProjectID: project, WorkItemID: r.WorkItemID, ResultID: r.ResultID, Stage: "HUMAN_REVIEW", State: pipelineVerdict("DONE", r.Verdict), CreatedAt: r.CreatedAt, EndedAt: &r.CreatedAt, ExecutionID: result.SourceExecutionID, DecisionID: &r.ID, EvidenceRefs: []string{"result:" + r.ResultID.String()}})
	}
	h := query.BindResultHandoffs(tx, m.service.names)
	handoffs, err := h.WithContext(ctx).Where(h.TenantID.Eq(tenant), h.ProjectID.Eq(project), h.WorkItemID.In(ids...)).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(handoffs) > 10000 {
		return out, ErrUnavailable
	}
	links := make([]PipelineDeliveryLink, 0, len(handoffs))
	for _, r := range handoffs {
		if result := byResult[r.ResultID]; result == nil || result.WorkItemID != r.WorkItemID {
			return out, ErrUnavailable
		}
		links = append(links, PipelineDeliveryLink{r.DeliveryID, r.WorkItemID, r.ResultID})
	}
	clientRows, err := m.pipelineClient(ctx, tx, tenant, project, workIDs, links)
	if err != nil {
		return out, err
	}
	if len(clientRows) > 20000 {
		return out, ErrUnavailable
	}
	seen := map[string]bool{}
	for _, r := range clientRows {
		if result := byResult[r.ResultID]; result == nil || result.WorkItemID != r.WorkItemID {
			return out, ErrUnavailable
		}
		if !allowed[r.WorkItemID] || r.ProjectID != project || r.ID == "" || !slices.Contains([]string{"HIDDEN_SUITE", "DELIVERY"}, r.Stage) || !slices.Contains([]string{"PENDING", "RUNNING", "PASSED", "FAILED", "INCONCLUSIVE", "SKIPPED", "BLOCKED"}, r.State) {
			return out, ErrUnavailable
		}
		r.ExecutionID = byResult[r.ResultID].SourceExecutionID
		rows = append(rows, r)
	}
	slices.SortFunc(rows, func(a, b PipelineStageRun) int {
		if n := a.CreatedAt.Compare(b.CreatedAt); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	attempts := map[string]int{}
	for i := range rows {
		r := &rows[i]
		if seen[r.ID] {
			return out, ErrUnavailable
		}
		seen[r.ID] = true
		key := r.WorkItemID.String() + ":" + r.ResultID.String() + ":" + r.Stage
		attempts[key]++
		r.Attempt = attempts[key]
		if r.EvidenceRefs == nil {
			r.EvidenceRefs = []string{}
		}
	}
	out.Total = len(rows)
	if offset >= len(rows) {
		return out, nil
	}
	out.Items = rows[offset:min(offset+limit, len(rows))]
	return out, nil
}

type pipelineInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `query:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset     int    `query:"offset" default:"0" minimum:"0"`
	Limit      int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

func (m *Module) registerPipelineHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "pipeline-run.list", Method: "GET", Path: "/entities/{entity_id}/pipeline-runs", Summary: "Rebuild authorized pipeline stages from immutable source receipts", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "work_item", Verb: "read"}, func(ctx context.Context, in *pipelineInput) (*respx.Body[[]PipelineStageRun], error) {
		project, err := guid.RequireID(in.EntityID)
		if err != nil {
			return nil, workHTTPError(ctx, ErrInvalidArgument)
		}
		work, err := guid.OptionalID(in.WorkItemID)
		if err != nil {
			return nil, workHTTPError(ctx, ErrInvalidArgument)
		}
		if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
			return nil, workHTTPError(ctx, ErrUnavailable)
		}
		var out PipelinePage
		err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.pipelineOn(ctx, tx, authn.TenantIDFromContext(ctx), project, work, in.Offset, in.Limit)
			return e
		})
		if err != nil {
			return nil, workHTTPError(ctx, err)
		}
		return respx.List(ctx, out.Items, respx.Page{Total: int64(out.Total), Limit: in.Limit, Offset: in.Offset, Count: len(out.Items)}), nil
	})
}

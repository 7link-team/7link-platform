package execution

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type executionTraceInput struct {
	ExecutionInput
	Cursor string `query:"cursor" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}

// TraceFact contains identifiers and recorded outcomes, never source bodies,
// private verifier plans/nonces, command arguments or filesystem paths.
type TraceFact struct {
	ID              guid.ID      `json:"id"`
	TenantID        guid.ID      `json:"tenant_id"`
	ProjectID       guid.ID      `json:"project_id"`
	WorkItemID      guid.ID      `json:"work_item_id"`
	ExecutionID     guid.ID      `json:"execution_id"`
	CommandID       string       `json:"command_id,omitempty"`
	ClaimID         *guid.ID     `json:"claim_id,omitempty"`
	Correlation     *Correlation `json:"correlation,omitempty"`
	Kind            string       `json:"kind"`
	State           string       `json:"state"`
	Runtime         string       `json:"runtime,omitempty"`
	Model           string       `json:"model,omitempty"`
	ModelFamily     string       `json:"model_family,omitempty"`
	VerifierVersion string       `json:"verifier_version,omitempty"`
	ResultID        *guid.ID     `json:"result_id,omitempty"`
	ResultVersion   int          `json:"result_version,omitempty"`
	ReceiptHash     string       `json:"receipt_hash,omitempty"`
}

// AGUITraceEvent uses AG-UI's CUSTOM envelope. It does not turn a runtime's
// terminal status into an Agent claim or an acceptance verdict.
type AGUITraceEvent struct {
	Type      string    `json:"type" enum:"CUSTOM"`
	Name      string    `json:"name"`
	Timestamp int64     `json:"timestamp" doc:"UTC Unix milliseconds of the source record."`
	Value     TraceFact `json:"value"`
}

type executionTraceOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         respx.Envelope[ExecutionTraceExport]
}

type ExecutionTraceExport struct {
	Events     []AGUITraceEvent `json:"events"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func (m *Module) registerCorrelationHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "execution.trace.export", Method: "GET", Path: "/executions/{execution_id}/trace", Summary: "Export authorized safe execution and verifier facts as AG-UI custom events", Tags: []string{"executions"}, Errors: []int{401, 403, 404, 422, 503}}, "execution_id", m.owner, authz.Guard{Resource: "execution", Verb: "read", FilterData: true}, func(ctx context.Context, in *executionTraceInput) (*executionTraceOutput, error) {
		id, err := guid.RequireID(in.ExecutionID)
		if err != nil {
			return nil, httpError(ctx, ErrInvalidArgument)
		}
		var cursor guid.ID
		if in.Cursor != "" {
			cursor, err = guid.RequireID(in.Cursor)
			if err != nil {
				return nil, httpError(ctx, ErrInvalidArgument)
			}
		}
		db, err := m.writer()
		if err != nil {
			return nil, httpError(ctx, err)
		}
		var out ExecutionTraceExport
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.traceExportOn(ctx, tx, authn.TenantIDFromContext(ctx), id, cursor, in.Limit)
			return e
		})
		if err != nil {
			return nil, httpError(ctx, err)
		}
		return &executionTraceOutput{CacheControl: "no-store", Body: respx.OK(ctx, out).Body}, nil
	})
}

func (m *Module) traceExportOn(ctx context.Context, tx *gorm.DB, tenant, id, cursor guid.ID, limit int) (ExecutionTraceExport, error) {
	out := ExecutionTraceExport{Events: []AGUITraceEvent{}}
	if limit < 1 || limit > 100 || cursor < 0 {
		return out, ErrInvalidArgument
	}
	row, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return out, err
	}
	if err = m.humanAuthority(ctx, tx, tenant, row.ProjectID, "execution_read"); err != nil {
		return out, err
	}
	v, err := view(row)
	if err != nil {
		return out, err
	}
	fact := func(source guid.ID) TraceFact {
		return TraceFact{ID: source, TenantID: tenant, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, ExecutionID: id, Correlation: v.Plan.Correlation, Runtime: v.Plan.Runtime, Model: v.Plan.Model, ModelFamily: v.Plan.ModelFamily}
	}
	r := query.BindReceipts(tx, m.names)
	receipts, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ExecutionID.Eq(id), r.ID.Gt(cursor)).Order(r.ID).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	for _, receipt := range receipts {
		f := fact(receipt.ID)
		f.Kind, f.State = receipt.Kind, receipt.State
		f.CommandID = id.String() + ":" + strconv.FormatInt(receipt.DispatchEpoch, 10)
		f.ReceiptHash = receipt.ContentHash
		if receipt.Kind == "COMPLETED" && receipt.State == "SUCCEEDED" && row.BranchReason != "ENSEMBLE" && row.BranchReason != "SKILL_DISTILLATION" && row.BranchReason != "HANDOVER" {
			claim := id
			f.ClaimID = &claim
		}
		out.Events = append(out.Events, AGUITraceEvent{Type: "CUSTOM", Name: "execution.receipt", Timestamp: receipt.CreatedAt.UnixMilli(), Value: f})
	}
	t := query.BindVerificationTasks(tx, m.names)
	tasks, err := t.WithContext(ctx).Where(t.TenantID.Eq(tenant), t.ExecutionID.Eq(id), t.ID.Gt(cursor)).Order(t.ID).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	for _, task := range tasks {
		var p VerificationPlan
		if task.ProjectID != row.ProjectID || task.WorkItemID != row.WorkItemID {
			return out, ErrUnavailable
		}
		if hash([]byte(task.PlanJSON)) != task.PlanHash || json.Unmarshal([]byte(task.PlanJSON), &p) != nil {
			return out, ErrUnavailable
		}
		if task.State == "DONE" && (hash([]byte(task.ObservationJSON)) != task.ReceiptHash || !json.Valid([]byte(task.ObservationJSON))) {
			return out, ErrUnavailable
		}
		f := fact(task.ID)
		f.Correlation = p.Correlation
		f.Kind, f.State = "VERIFICATION", task.State
		f.Runtime, f.Model, f.ModelFamily = p.ReviewerRuntime, p.ReviewerModel, p.ReviewerFamily
		f.ResultID, f.ResultVersion = &task.ResultID, task.ResultVersion
		f.ReceiptHash = task.ReceiptHash
		if row.BranchReason != "ENSEMBLE" && row.BranchReason != "SKILL_DISTILLATION" && row.BranchReason != "HANDOVER" {
			claim := id
			f.ClaimID = &claim
		}
		if pin := p.TemplateVerifier; pin != nil {
			f.VerifierVersion = pin.TemplateVersionID.String() + ":" + pin.DefinitionHash
		}
		out.Events = append(out.Events, AGUITraceEvent{Type: "CUSTOM", Name: "execution.verification", Timestamp: task.CreatedAt.UnixMilli(), Value: f})
	}
	slices.SortFunc(out.Events, func(a, b AGUITraceEvent) int {
		if a.Value.ID < b.Value.ID {
			return -1
		}
		if a.Value.ID > b.Value.ID {
			return 1
		}
		return 0
	})
	if len(out.Events) > limit {
		out.HasMore = true
		out.Events = out.Events[:limit]
		out.NextCursor = out.Events[len(out.Events)-1].Value.ID.String()
	}
	return out, nil
}

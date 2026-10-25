package execution

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type ExecutionInput struct {
	ExecutionID string `path:"execution_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type createInput struct{ Body ExecutionCreate }
type listInput struct {
	ProjectID string `query:"project_id" required:"true" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor    string `query:"cursor" maxLength:"19"`
	Limit     int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type ExecutionPage struct {
	Items      []ExecutionView `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
	HasMore    bool            `json:"has_more"`
}
type controlInput struct {
	ExecutionInput
	IfMatch string `header:"If-Match" required:"true"`
	Body    struct {
		AcknowledgeUnknownResult bool `json:"acknowledge_unknown_result"`
	}
}
type pullInput struct{ Body RunnerFence }
type receiptHTTPInput struct {
	ExecutionInput
	Body struct {
		Fence   RunnerFence  `json:"fence"`
		Receipt ReceiptInput `json:"receipt"`
	}
}
type LogView struct {
	Seq           int64  `json:"seq"`
	DispatchEpoch int64  `json:"dispatch_epoch"`
	Content       string `json:"content"`
	ContentHash   string `json:"content_hash"`
}
type ExecutionDetail struct {
	Approvals       []ApprovalView `json:"approvals,omitempty"`
	Execution       ExecutionView  `json:"execution"`
	Logs            []LogView      `json:"logs"`
	Evidence        []EvidenceFact `json:"evidence"`
	HasMoreLogs     bool           `json:"has_more_logs"`
	HasMoreEvidence bool           `json:"has_more_evidence"`
}

func writeContextHTTPError(ctx context.Context, err error, operation string) error {
	var interrupted *delivery.WorkWriteInterrupted
	if errors.As(err, &interrupted) || errors.Is(err, delivery.ErrWorkSeenSeqRequired) {
		ctx = respx.WithNext(ctx, respx.NextStep{Operation: "inbox_events.list", Reason: "READ_NEWER_EVENTS"}, respx.NextStep{Operation: operation, Reason: "RETRY_WITH_SAME_IDEMPOTENCY_KEY"})
	}
	return httpError(ctx, err)
}

func httpError(ctx context.Context, err error) error {
	var anchorError *ClientAnchorError
	if errors.As(err, &anchorError) {
		code, key := 130476, "execution.anchor_invalid"
		if anchorError.Unavailable {
			code, key = 130480, "execution.anchor_kind_unavailable"
		}
		return respx.ErrStatus(ctx, 422, code, key, map[string]any{"field": anchorError.Field})
	}
	if errors.Is(err, ErrCommentLimit) {
		return respx.ErrStatus(ctx, 422, 130481, "execution.comment_limit", nil)
	}
	if errors.Is(err, ErrClientThreadReadOnly) {
		return respx.ErrStatus(ctx, 409, 130479, "execution.client_thread_read_only", nil)
	}
	if errors.Is(err, ErrEvidenceAttached) {
		return respx.ErrStatus(ctx, 409, 130430, "execution.evidence_attached", nil)
	}
	var interrupted *delivery.WorkWriteInterrupted
	if errors.As(err, &interrupted) {
		return respx.ErrStatus(ctx, 409, 130474, "execution.write_interrupted", interrupted)
	}
	if errors.Is(err, delivery.ErrWorkSeenSeqRequired) {
		return respx.ErrStatus(ctx, 422, 130475, "execution.seen_seq_required", nil)
	}
	status, key := 503, "unavailable"
	switch {
	case errors.Is(err, ErrFullAutoForbidden), errors.Is(err, ErrReauthenticationRequired):
		reason := "REAUTH_REQUIRED"
		if errors.Is(err, ErrFullAutoForbidden) {
			reason = "FULL_AUTO_FORBIDDEN"
		}
		return respx.ErrStatus(ctx, 403, 130403, "execution.forbidden", map[string]any{"reason_code": reason})
	case errors.Is(err, idempotency.ErrInvalidKey), errors.Is(err, idempotency.ErrKeyReused):
		status, key = 422, "invalid"
	case errors.Is(err, idempotency.ErrInProgress):
		status, key = 409, "conflict"
	case errors.Is(err, ErrInvalidArgument), errors.Is(err, fleet.ErrInvalidArgument):
		status, key = 422, "invalid"
	case errors.Is(err, ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, fleet.ErrMachineNotFound):
		status, key = 404, "not_found"
	case errors.Is(err, ErrForbidden), errors.Is(err, fleet.ErrMachineForbidden):
		status, key = 403, "forbidden"
	case errors.Is(err, ErrApprovalUnavailable):
		status, key = 422, "approval_unavailable"
	case errors.Is(err, ErrConflict), errors.Is(err, ErrFenced), errors.Is(err, ErrBudgetExceeded), errors.Is(err, ErrPriceUnknown), errors.Is(err, fleet.ErrSessionStale), errors.Is(err, fleet.ErrMachineRevoked), errors.Is(err, fleet.ErrMachineOffline), errors.Is(err, fleet.ErrMachineDraining), errors.Is(err, fleet.ErrSlotsFull), errors.Is(err, fleet.ErrSlotReleased):
		status, key = 409, "conflict"
	}
	return respx.ErrStatus(ctx, status, status, "execution."+key, nil)
}
func (m *Module) owner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.writer()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	q := query.BindExecutions(db, m.names)
	row, err := q.WithContext(ctx).Select(q.TenantID, q.ProjectID).Where(q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: row.TenantID, EntityID: row.ProjectID}, true, nil
}
func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if err := m.registerCodeMergeHTTP(api); err != nil {
		return err
	}
	if m.registrar == nil {
		return ErrUnavailable
	}
	if err := m.registerVerifierRegistryHTTP(api); err != nil {
		return err
	}
	if err := m.registerBranchesHTTP(api); err != nil {
		return err
	}
	m.registerCorrelationHTTP(api)
	m.registerProvenanceHTTP(api)
	m.registerHubSessionsHTTP(api)
	if err := m.registerPreviewHTTP(api); err != nil {
		return err
	}
	if err := m.registerFullAutoHTTP(api); err != nil {
		return err
	}
	if err := m.registerEvidenceHTTP(api); err != nil {
		return err
	}
	if err := m.registerArtifactCommentsHTTP(api); err != nil {
		return err
	}
	if err := m.registerVisualReviewHTTP(api); err != nil {
		return err
	}
	if err := m.registerApprovalHTTP(api); err != nil {
		return err
	}
	if err := m.registerVerificationHTTP(api); err != nil {
		return err
	}
	guard := func(verb string) authz.Guard { return authz.Guard{Resource: "execution", Verb: verb, FilterData: true} }
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"executions"}, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	authz.Register(m.registrar, api, op("execution.list", http.MethodGet, "/executions", "List project executions"), guard("read"), m.listHTTP)
	create := op("execution.create", http.MethodPost, "/executions", "Create an immutable execution plan")
	create.DefaultStatus = 201
	authz.Register(m.registrar, api, create, guard("start"), m.createHTTP)
	authz.RegisterOwned(m.registrar, api, op("execution.get", http.MethodGet, "/executions/{execution_id}", "Read execution, logs and evidence"), "execution_id", m.owner, guard("read"), m.getHTTP)
	authz.RegisterOwned(m.registrar, api, op("execution.cancel", http.MethodPut, "/executions/{execution_id}/cancellation", "Request execution cancellation"), "execution_id", m.owner, guard("stop"), m.cancelHTTP)
	authz.RegisterOwned(m.registrar, api, op("execution.recover", http.MethodPut, "/executions/{execution_id}/recovery", "Acknowledge unknown result and recover execution"), "execution_id", m.owner, guard("stop"), m.recoverHTTP)
	authz.RegisterAuthenticated(m.registrar, api, op("execution.runner.pull", http.MethodPost, "/runner/execution-pulls", "Lease the next execution command"), m.pullHTTP)
	authz.RegisterAuthenticated(m.registrar, api, op("execution.runner.receipt", http.MethodPost, "/executions/{execution_id}/receipts", "Apply a fenced idempotent runner receipt"), m.receiptHTTP)
	for _, path := range []string{"/executions/{execution_id}/cancellation", "/executions/{execution_id}/recovery", "/executions/{execution_id}/receipts"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) createHTTP(ctx context.Context, in *createInput) (*respx.Body[ExecutionView], error) {
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out ExecutionView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.CreateOn(ctx, tx, authn.TenantIDFromContext(ctx), in.Body)
		if e != nil {
			return e
		}
		return m.authorizeKnowledgeViewOn(ctx, tx, authn.TenantIDFromContext(ctx), out)
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listHTTP(ctx context.Context, in *listInput) (*respx.Body[ExecutionPage], error) {
	project, err := guid.RequireID(in.ProjectID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	cursor := guid.ID(0)
	if in.Cursor != "" {
		cursor, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, httpError(ctx, ErrInvalidArgument)
		}
	}
	if in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	out := ExecutionPage{Items: []ExecutionView{}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.humanAuthority(ctx, tx, tenant, project, "execution_read"); e != nil {
			return e
		}
		q := query.BindExecutions(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Gt(cursor)).Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
		}
		for _, row := range rows {
			v, e := view(row)
			if e != nil {
				return e
			}
			if e = m.authorizeKnowledgeViewOn(ctx, tx, tenant, v); e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		if out.HasMore {
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) getHTTP(ctx context.Context, in *ExecutionInput) (*respx.Body[ExecutionDetail], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	out := ExecutionDetail{Logs: []LogView{}, Evidence: []EvidenceFact{}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.rowOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if e = m.humanAuthority(ctx, tx, tenant, row.ProjectID, "execution_read"); e != nil {
			return e
		}
		out.Execution, e = view(row)
		if e != nil {
			return e
		}
		if e = m.authorizeKnowledgeViewOn(ctx, tx, tenant, out.Execution); e != nil {
			return e
		}
		if e = m.historicalResponseAuthorityOn(ctx, tx, tenant, row.ProjectID); e == nil {
			a := query.BindApprovals(tx, m.names)
			requests, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ExecutionID.Eq(id), a.DispatchEpoch.Eq(row.DispatchEpoch), a.State.Eq("PENDING")).Order(a.ID).Limit(100).Find()
			if err != nil {
				return err
			}
			for _, r := range requests {
				v := approvalView(r)
				if v.State == "PENDING" {
					out.Approvals = append(out.Approvals, v)
				}
			}
		} else if !errors.Is(e, ErrForbidden) {
			return e
		}
		l := query.BindLogs(tx, m.names)
		logs, e := l.WithContext(ctx).Where(l.TenantID.Eq(tenant), l.ExecutionID.Eq(id)).Order(l.ID.Desc()).Limit(201).Find()
		if e != nil {
			return e
		}
		if len(logs) > 200 {
			out.HasMoreLogs = true
			logs = logs[:200]
		}
		for i := len(logs) - 1; i >= 0; i-- {
			r := logs[i]
			out.Logs = append(out.Logs, LogView{Seq: r.Seq, DispatchEpoch: r.DispatchEpoch, Content: r.Content, ContentHash: r.ContentHash})
		}
		q := query.BindEvidence(tx, m.names)
		facts, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(id)).Order(q.ID).Limit(201).Find()
		if e != nil {
			return e
		}
		if len(facts) > 200 {
			out.HasMoreEvidence = true
			facts = facts[:200]
		}
		for _, r := range facts {
			out.Evidence = append(out.Evidence, EvidenceFact{ID: r.ID, Kind: r.Kind, Title: r.Title, ContentHash: r.ContentHash})
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) controlHTTP(ctx context.Context, in *controlInput, recovery bool) (*respx.Body[ExecutionView], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	version, err := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || version < 1 {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out ExecutionView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		if recovery {
			out, e = m.RecoverOn(ctx, tx, authn.TenantIDFromContext(ctx), id, version, in.Body.AcknowledgeUnknownResult)
		} else {
			out, e = m.CancelOn(ctx, tx, authn.TenantIDFromContext(ctx), id, version)
		}
		if e != nil {
			return e
		}
		return m.authorizeKnowledgeViewOn(ctx, tx, authn.TenantIDFromContext(ctx), out)
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) cancelHTTP(ctx context.Context, in *controlInput) (*respx.Body[ExecutionView], error) {
	return m.controlHTTP(ctx, in, false)
}
func (m *Module) recoverHTTP(ctx context.Context, in *controlInput) (*respx.Body[ExecutionView], error) {
	return m.controlHTTP(ctx, in, true)
}
func (m *Module) pullHTTP(ctx context.Context, in *pullInput) (*respx.Body[[]ExecutionView], error) {
	if m.machines.BindRunnerContext == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	bound, bindErr := m.machines.BindRunnerContext(ctx, in.Body.MachineID)
	if bindErr != nil {
		return nil, httpError(ctx, bindErr)
	}
	ctx = bound
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out []ExecutionView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.PullOn(ctx, tx, authn.TenantIDFromContext(ctx), in.Body)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) receiptHTTP(ctx context.Context, in *receiptHTTPInput) (*respx.Body[ReceiptView], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	if m.machines.BindRunnerContext == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	bound, err := m.machines.BindRunnerContext(ctx, in.Body.Fence.MachineID)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	ctx = bound
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out ReceiptView
	in.Body.Receipt, err = m.PrepareReceipt(ctx, authn.TenantIDFromContext(ctx), id, in.Body.Fence, in.Body.Receipt)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.ReceiptOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.Fence, in.Body.Receipt)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

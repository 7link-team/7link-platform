package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type templateVerifierServices struct {
	catalog templates.VerifierCatalog
	read    delivery.RealtimeTopics
}
type TemplateVerifierPin struct {
	TemplateVersionID guid.ID                      `json:"template_version_id"`
	DefinitionHash    string                       `json:"definition_hash"`
	Verifier          templates.VerifierDefinition `json:"verifier"`
}

func templateVerifierDependencies() []string { return []string{templates.Name, delivery.Name} }
func (m *Module) injectTemplateVerifiers(s *module.Services) error {
	var e error
	if m.templateVerifiers.catalog, e = module.Resolve[templates.VerifierCatalog](s); e != nil {
		return e
	}
	m.templateVerifiers.read, e = module.Resolve[delivery.RealtimeTopics](s)
	return e
}
func (m *Module) pinTemplateVerifierOn(ctx context.Context, tx *gorm.DB, in VerificationRequest, plan *VerificationPlan) error {
	// Production injection requires the catalog. Older isolated consumers retain
	// their protocol when no template service was injected.
	if m.templateVerifiers.catalog.ForWorkOn == nil {
		return nil
	}
	binding, found, e := m.templateVerifiers.catalog.ForWorkOn(ctx, tx, in.TenantID, in.ProjectID, in.WorkItemID)
	if e != nil {
		return e
	}
	if !found || len(binding.Verifiers) == 0 {
		return nil
	}
	var selected *templates.VerifierDefinition
	for _, v := range binding.Verifiers {
		if v.Runner != in.Method || v.TimeoutSec != in.TimeoutSec || v.ReportPath != in.ReportPath || !slices.Equal(v.Argv, in.Argv) {
			continue
		}
		if selected != nil {
			return ErrCapabilityUnavailable
		}
		definition := v
		selected = &definition
	}
	if selected == nil {
		return ErrCapabilityUnavailable
	}
	plan.TemplateVerifier = &TemplateVerifierPin{TemplateVersionID: binding.TemplateVersionID, DefinitionHash: binding.DefinitionHash, Verifier: *selected}
	return nil
}

type VerifierResult struct {
	RegistryVerifiers []RegistryVerifierPin `json:"registry_verifiers,omitempty"`
	TaskID            guid.ID               `json:"task_id"`
	ResultID          guid.ID               `json:"result_id"`
	CriterionID       guid.ID               `json:"criterion_id"`
	VerifierKey       string                `json:"verifier_key"`
	Kind              string                `json:"kind"`
	Runner            string                `json:"runner"`
	Stage             string                `json:"stage"`
	Verdict           string                `json:"verdict"`
	Reason            string                `json:"reason"`
	ParamsResolved    map[string]any        `json:"params_resolved"`
	Metrics           map[string]int64      `json:"metrics"`
	EvidenceRefs      []string              `json:"evidence_refs"`
	Limitations       string                `json:"limitations"`
	ToolVersion       string                `json:"tool_version"`
	CalibrationState  string                `json:"calibration_state"`
	DurationMS        *int64                `json:"duration_ms,omitempty"`
	TemplatePin       *TemplateVerifierPin  `json:"template_pin,omitempty"`
}
type verifierResultsInput struct {
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Stage      string `query:"stage" maxLength:"32"`
	Cursor     string `query:"cursor" maxLength:"19"`
	Limit      int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type VerifierResultPage struct {
	Items      []VerifierResult `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
	HasMore    bool             `json:"has_more"`
}

func (m *Module) registerTemplateVerifierHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "template.verifier-result.list", Method: http.MethodGet, Path: "/work-items/{work_item_id}/verifier-results", Summary: "Read actual platform verifier receipts and frozen template pins", Tags: []string{"work-items"}, Errors: []int{401, 403, 404, 422, 503}}, authz.Guard{Resource: "work_item", Verb: "read", FilterData: true}, m.verifierResultsHTTP)
}
func (m *Module) verifierResultsHTTP(ctx context.Context, in *verifierResultsInput) (*respx.Body[VerifierResultPage], error) {
	work, e := guid.RequireID(in.WorkItemID)
	if e != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	if m.templateVerifiers.read.AuthorizeWork == nil {
		return nil, httpError(ctx, ErrCapabilityUnavailable)
	}
	allowed, e := m.templateVerifiers.read.AuthorizeWork(ctx, tenant, actor, work, false)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	if !allowed {
		return nil, httpError(ctx, ErrForbidden)
	}
	db, e := m.writer()
	if e != nil {
		return nil, httpError(ctx, e)
	}
	var out VerifierResultPage
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.verifierResultsOn(ctx, tx, tenant, work, in.Stage, in.Cursor, in.Limit)
		return err
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) verifierResultsOn(ctx context.Context, tx *gorm.DB, tenant, work guid.ID, stage, cursor string, limit int) (VerifierResultPage, error) {
	out := VerifierResultPage{Items: []VerifierResult{}}
	if limit < 1 || limit > 100 {
		return out, ErrInvalidArgument
	}
	q := query.BindVerificationTasks(tx, m.names)
	cursorID := guid.ID(0)
	if cursor != "" {
		id, e := guid.RequireID(cursor)
		if e != nil {
			return out, ErrInvalidArgument
		}
		cursorID = id
	}
	// Stage lives in immutable plan JSON. Filter before applying the page limit.
	for {
		base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.WorkItemID.Eq(work), q.ID.Gt(cursorID))
		rows, e := base.Order(q.ID).Limit(100).Find()
		if e != nil {
			return out, e
		}
		for _, row := range rows {
			var p VerificationPlan
			if hash([]byte(row.PlanJSON)) != row.PlanHash || json.Unmarshal([]byte(row.PlanJSON), &p) != nil {
				return out, ErrUnavailable
			}
			cursorID = row.ID
			if stage != "" && p.Stage != stage {
				continue
			}
			if len(out.Items) == limit {
				out.HasMore = true
				out.NextCursor = out.Items[len(out.Items)-1].TaskID.String()
				return out, nil
			}
			v := VerifierResult{TaskID: row.ID, ResultID: row.ResultID, CriterionID: row.CriterionID, VerifierKey: p.Method, Kind: "DETERMINISTIC", Runner: p.Method, Stage: p.Stage, Verdict: "INCONCLUSIVE", Reason: "VERIFICATION_PENDING", ParamsResolved: map[string]any{}, Metrics: map[string]int64{}, EvidenceRefs: []string{}, Limitations: "Platform rerun outcome only; external tool binary version and full product acceptance are not established.", ToolVersion: "platform-verification-v1", CalibrationState: "CALIBRATED", RegistryVerifiers: p.RegistryVerifiers, TemplatePin: p.TemplateVerifier}
			if p.Method == "AGENT_JUDGE" {
				v.Kind = "MODEL_SCORED"
				v.CalibrationState = "UNCALIBRATED"
				v.Limitations = "Model review is advisory; calibration and full product acceptance are not established."
			}
			if p.Method == "VISUAL" {
				v.CalibrationState = "UNCALIBRATED"
				v.Limitations = "Screenshots establish collected rendering only; interaction and tool calibration are not established."
			}
			if p.TemplateVerifier != nil {
				v.VerifierKey = p.TemplateVerifier.Verifier.Key
				v.ParamsResolved = p.TemplateVerifier.Verifier.Params
				v.Limitations = p.TemplateVerifier.Verifier.Limitations + " " + v.Limitations
			}
			if row.State == "DONE" {
				var o VerificationObservation
				if hash([]byte(row.ObservationJSON)) != row.ReceiptHash || json.Unmarshal([]byte(row.ObservationJSON), &o) != nil || o.PlanHash != row.PlanHash {
					return out, ErrUnavailable
				}
				r, e := m.verificationReceiptOn(ctx, tx, tenant, row.ProjectID, work, row.ResultID, row.ID)
				if e != nil && !errors.Is(e, ErrConflict) {
					return out, e
				}
				if e == nil {
					v.Verdict, v.Reason = r.Verdict, r.Reason
					v.EvidenceRefs = append(v.EvidenceRefs, "verification-receipt:"+row.ID.String()+":"+r.ReceiptHash)
				} else {
					v.Reason = "VISUAL_REVIEW_REQUIRED"
				}
				if !o.StartedAt.IsZero() && !o.EndedAt.Before(o.StartedAt) {
					n := o.EndedAt.Sub(o.StartedAt) / time.Millisecond
					duration := int64(n)
					v.DurationMS = &duration
				}
				if o.ExitCode != nil {
					v.Metrics["exit_code"] = int64(*o.ExitCode)
				}
				for key, value := range map[string]*int{"passed": o.Passed, "failed": o.Failed, "skipped": o.Skipped} {
					if value != nil {
						v.Metrics[key] = int64(*value)
					}
				}
				if o.OutputSHA256 != "" {
					v.EvidenceRefs = append(v.EvidenceRefs, "output-sha256:"+o.OutputSHA256)
				}
				if o.ReportSHA256 != "" {
					v.EvidenceRefs = append(v.EvidenceRefs, "report-sha256:"+o.ReportSHA256)
				}
			}
			if e := m.verifierCalibrationOn(ctx, tx, tenant, row.MachineID, &v); e != nil {
				return out, e
			}
			out.Items = append(out.Items, v)
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
}

package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// Provenance is a projection of recorded facts. Neither a successful process nor
// an artifact's COVERED association is an independent verification verdict.
type EvidenceProvenance struct {
	EvidenceFact
	ExecutionID        guid.ID   `json:"execution_id"`
	ProducerID         guid.ID   `json:"producer_id"`
	MachineID          guid.ID   `json:"machine_id"`
	CreatedAt          time.Time `json:"created_at"`
	PlanHash           string    `json:"plan_hash"`
	BaseCommit         string    `json:"base_commit"`
	Commit             string    `json:"commit,omitempty"`
	OutputState        string    `json:"output_state" enum:"RECORDED,PARTIAL"`
	Comparison         string    `json:"comparison" enum:"NOT_REQUESTED,SAME,CHANGED,ONLY_CURRENT,UNKNOWN"`
	ComparedEvidenceID *guid.ID  `json:"compared_evidence_id,omitempty"`
}
type EvidenceVerifierReference struct {
	ID            guid.ID `json:"id"`
	CriterionID   guid.ID `json:"criterion_id"`
	ResultID      guid.ID `json:"result_id"`
	ResultVersion int     `json:"result_version"`
	State         string  `json:"state"`
	PlanHash      string  `json:"plan_hash"`
	ReceiptHash   string  `json:"receipt_hash"`
}
type EvidenceProvenancePage struct {
	Items               []EvidenceProvenance        `json:"items"`
	Verifiers           []EvidenceVerifierReference `json:"verifiers"`
	HasMore             bool                        `json:"has_more"`
	NextBeforeID        *guid.ID                    `json:"next_before_id,omitempty"`
	ComparedExecutionID *guid.ID                    `json:"compared_execution_id,omitempty"`
	ComparisonTruncated bool                        `json:"comparison_truncated"`
	VerifiersTruncated  bool                        `json:"verifiers_truncated"`
	Limitations         []string                    `json:"limitations"`
}
type provenanceInput struct {
	ExecutionInput
	Cursor    string `query:"cursor" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	BeforeID  string `query:"before_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	CompareID string `query:"compare_execution_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit     int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Sort      string `query:"sort" default:"-id" enum:"-id"`
}

func evidenceSource(row *model.Execution, plan ExecutionPlan, record *model.Evidence) (EvidenceProvenance, error) {
	out := EvidenceProvenance{EvidenceFact: EvidenceFact{ID: record.ID, Kind: record.Kind, Title: record.Title, ContentHash: record.ContentHash}, ExecutionID: row.ID, ProducerID: plan.ExecutorPrincipalID, MachineID: row.MachineID, CreatedAt: record.CreatedAt, PlanHash: row.PlanHash, BaseCommit: plan.BaseCommit, OutputState: "RECORDED", Comparison: "NOT_REQUESTED"}
	if record.ProjectID != row.ProjectID || record.TenantID != row.TenantID || record.ExecutionID != row.ID {
		return out, ErrUnavailable
	}
	if record.Kind == "FILE" {
		var artifact ArtifactFact
		if json.Unmarshal([]byte(record.Content), &artifact) != nil || artifact.BindingID <= 0 || artifact.FileID <= 0 || artifact.VersionID <= 0 || artifact.FileVersion < 1 || artifact.SHA256 != record.ContentHash || (artifact.Coverage != "COVERED" && artifact.Coverage != "PARTIAL" && artifact.Coverage != "NOT_APPLICABLE") {
			return out, ErrUnavailable
		}
		out.Artifact = &artifact
		if artifact.Coverage == "PARTIAL" {
			out.OutputState = "PARTIAL"
		}
	} else {
		if hash([]byte(record.Content)) != record.ContentHash {
			return out, ErrUnavailable
		}
		if record.Kind == "COMMIT" {
			out.Commit = strings.TrimSpace(record.Content)
			if !commitHash(out.Commit) {
				return out, ErrUnavailable
			}
		}
	}
	if row.State == "FAILED" || row.State == StateCanceled || row.State == "INTERRUPTED" {
		out.OutputState = "PARTIAL"
	}
	return out, nil
}
func provenanceKey(f EvidenceProvenance) string {
	key := f.Kind + ":" + f.Title
	if f.Artifact != nil && f.Artifact.CriterionID != nil {
		key += ":" + f.Artifact.CriterionID.String()
	}
	return key
}

func (m *Module) provenanceOn(ctx context.Context, tx *gorm.DB, tenant, id, before, compare guid.ID, limit int) (EvidenceProvenancePage, error) {
	out := EvidenceProvenancePage{Items: []EvidenceProvenance{}, Verifiers: []EvidenceVerifierReference{}, Limitations: []string{"ASSOCIATION_IS_NOT_VERIFICATION", "PARTIAL_OUTPUT_REQUIRES_REVIEW", "COMPARISON_MATCHES_KIND_TITLE_CRITERION"}}
	if limit < 1 || limit > 100 || before < 0 || compare < 0 || compare == id {
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
	other := map[string][]EvidenceProvenance{}
	if compare > 0 {
		prior, e := m.rowOn(ctx, tx, tenant, compare, false)
		if e != nil {
			return out, e
		}
		if e = m.humanAuthority(ctx, tx, tenant, prior.ProjectID, "execution_read"); e != nil {
			return out, e
		}
		pv, e := view(prior)
		if e != nil {
			return out, e
		}
		if prior.ProjectID != row.ProjectID || prior.WorkItemID != row.WorkItemID || pv.Plan.RepositoryID != v.Plan.RepositoryID {
			return out, ErrForbidden
		}
		q := query.BindEvidence(tx, m.names)
		records, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(compare)).Order(q.ID.Desc()).Limit(201).Find()
		if e != nil {
			return out, e
		}
		out.ComparedExecutionID = &compare
		out.ComparisonTruncated = len(records) > 200
		if out.ComparisonTruncated {
			records = records[:200]
		}
		for _, r := range records {
			f, e := evidenceSource(prior, pv.Plan, r)
			if e != nil {
				return out, e
			}
			k := provenanceKey(f)
			other[k] = append(other[k], f)
		}
	}
	q := query.BindEvidence(tx, m.names)
	selection := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(id))
	if before > 0 {
		selection = selection.Where(q.ID.Lt(before))
	}
	records, err := selection.Order(q.ID.Desc()).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	out.HasMore = len(records) > limit
	if out.HasMore {
		records = records[:limit]
	}
	for _, r := range records {
		f, e := evidenceSource(row, v.Plan, r)
		if e != nil {
			return out, e
		}
		if compare > 0 {
			matches := other[provenanceKey(f)]
			switch {
			case out.ComparisonTruncated || len(matches) > 1:
				f.Comparison = "UNKNOWN"
			case len(matches) == 1:
				f.Comparison = "CHANGED"
				f.ComparedEvidenceID = &matches[0].ID
				if f.ContentHash == matches[0].ContentHash {
					f.Comparison = "SAME"
				}
			case out.ComparisonTruncated:
				f.Comparison = "UNKNOWN"
			default:
				f.Comparison = "ONLY_CURRENT"
			}
		}
		out.Items = append(out.Items, f)
	}
	if out.HasMore && len(records) > 0 {
		out.NextBeforeID = &records[len(records)-1].ID
	}
	tasks := query.BindVerificationTasks(tx, m.names)
	refs, err := tasks.WithContext(ctx).Where(tasks.TenantID.Eq(tenant), tasks.ExecutionID.Eq(id)).Order(tasks.ID.Desc()).Limit(101).Find()
	if err != nil {
		return out, err
	}
	out.VerifiersTruncated = len(refs) > 100
	if out.VerifiersTruncated {
		refs = refs[:100]
	}
	for _, r := range refs {
		out.Verifiers = append(out.Verifiers, EvidenceVerifierReference{ID: r.ID, CriterionID: r.CriterionID, ResultID: r.ResultID, ResultVersion: r.ResultVersion, State: r.State, PlanHash: r.PlanHash, ReceiptHash: r.ReceiptHash})
	}
	return out, nil
}

func (m *Module) registerProvenanceHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "execution.evidence-provenance.list", Method: http.MethodGet, Path: "/executions/{execution_id}/evidence-provenance", Summary: "Read pinned output provenance and compare recovery outputs", Tags: []string{"executions"}, Errors: []int{401, 403, 404, 422, 503}}, "execution_id", m.owner, authz.Guard{Resource: "execution", Verb: "read", FilterData: true}, func(ctx context.Context, in *provenanceInput) (*respx.Body[EvidenceProvenancePage], error) {
		id, err := guid.RequireID(in.ExecutionID)
		if err != nil {
			return nil, httpError(ctx, ErrInvalidArgument)
		}
		if in.Cursor != "" {
			if in.BeforeID != "" && in.BeforeID != in.Cursor {
				return nil, httpError(ctx, ErrInvalidArgument)
			}
			in.BeforeID = in.Cursor
		}
		var before, compare guid.ID
		if in.BeforeID != "" {
			before, err = guid.RequireID(in.BeforeID)
			if err != nil {
				return nil, httpError(ctx, ErrInvalidArgument)
			}
		}
		if in.CompareID != "" {
			compare, err = guid.RequireID(in.CompareID)
			if err != nil {
				return nil, httpError(ctx, ErrInvalidArgument)
			}
		}
		db, err := m.writer()
		if err != nil {
			return nil, httpError(ctx, err)
		}
		var out EvidenceProvenancePage
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.provenanceOn(ctx, tx, authn.TenantIDFromContext(ctx), id, before, compare, in.Limit)
			return e
		})
		if err != nil {
			return nil, httpError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
}

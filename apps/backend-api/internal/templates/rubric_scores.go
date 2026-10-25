package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type RubricProofRequest struct {
	TemplateVersionID               guid.ID
	DefinitionHash                  string
	ProjectID, WorkItemID, ResultID guid.ID
	ResultVersion                   int
	HeadCommit                      string
	VerifierKeys                    []string
}
type RubricProofFact struct {
	TaskID      guid.ID `json:"task_id"`
	ReceiptHash string  `json:"receipt_hash"`
	VerifierKey string  `json:"verifier_key"`
	Passed      bool    `json:"passed"`
}
type RubricProof func(context.Context, *gorm.DB, RubricProofRequest) (RubricProofFact, error)
type RubricProofConnector struct{ Bind func(RubricProof) error }

func (m *Module) provideRubricScores(s *module.Services) error {
	return module.Provide[RubricProofConnector](s, RubricProofConnector{Bind: func(f RubricProof) error {
		if f == nil || m.rubricProof != nil || m.calibrationSealed {
			return ErrInvalid
		}
		m.rubricProof = f
		return nil
	}})
}

type RubricScoreValue struct {
	RubricDimensionScore
	Proofs       []RubricProofFact `json:"proofs,omitempty"`
	EvidenceRefs []guid.ID         `json:"evidence_refs" maxItems:"100"`
	Note         string            `json:"note" minLength:"1" maxLength:"2000"`
}
type RubricScoreInput struct {
	Stage       string             `json:"stage" enum:"AGENT_ACCEPTANCE,HUMAN_REVIEW,CLIENT_ACCEPTANCE"`
	ResultID    guid.ID            `json:"result_id"`
	WorkVersion int                `json:"work_version" minimum:"1"`
	Scores      []RubricScoreValue `json:"scores" minItems:"1" maxItems:"50"`
}
type RubricScoreReceipt struct {
	ID                guid.ID            `json:"id"`
	TemplateVersionID guid.ID            `json:"template_version_id"`
	DefinitionHash    string             `json:"definition_hash"`
	WorkItemID        guid.ID            `json:"work_item_id,omitempty"`
	ResultID          guid.ID            `json:"result_id,omitempty"`
	ResultVersion     int                `json:"result_version,omitempty"`
	HeadCommit        string             `json:"head_commit,omitempty"`
	PackageHash       string             `json:"package_hash,omitempty"`
	Stage             string             `json:"stage"`
	ScorerKind        string             `json:"scorer_kind"`
	ScorerID          guid.ID            `json:"scorer_id,omitempty"`
	Authority         string             `json:"authority,omitempty"`
	Scores            []RubricScoreValue `json:"scores"`
	Calculation       RubricCalculation  `json:"calculation"`
	CreatedAt         time.Time          `json:"created_at"`
}
type RubricDivergence struct {
	Dimension  string  `json:"dimension"`
	LeftID     guid.ID `json:"left_id"`
	RightID    guid.ID `json:"right_id"`
	LeftStage  string  `json:"left_stage"`
	RightStage string  `json:"right_stage"`
	Delta      string  `json:"delta"`
}
type RubricScoreboard struct {
	EvidenceRefs      []guid.ID            `json:"evidence_refs"`
	TemplateVersionID guid.ID              `json:"template_version_id"`
	DefinitionHash    string               `json:"definition_hash"`
	Definition        *RubricDefinition    `json:"definition"`
	WorkVersion       int                  `json:"work_version"`
	ResultID          guid.ID              `json:"result_id,omitempty"`
	ResultVersion     int                  `json:"result_version,omitempty"`
	Receipts          []RubricScoreReceipt `json:"receipts"`
	Divergences       []RubricDivergence   `json:"divergences"`
	AllowedStages     []string             `json:"allowed_stages"`
	Availability      string               `json:"availability"`
	// Calculation is a score fact only; command does not accept/reject work.
	AcceptanceEffect string `json:"acceptance_effect"`
}
type rubricScorePayload struct {
	SourcePackageID guid.ID            `json:"source_package_id"`
	Divergences     []RubricDivergence `json:"divergences"`
	View            LifecycleView      `json:"view"`
	Score           RubricScoreReceipt `json:"score"`
}

func rubricScoreObject(work, result guid.ID) string {
	return evalDigest([]byte("rubric-score:" + work.String() + ":" + result.String()))
}
func (m *Module) rubricSubjectOn(ctx context.Context, tx *gorm.DB, project, work, packageID guid.ID, item string, in *RubricScoreInput) (delivery.RubricSubject, error) {
	if packageID > 0 {
		if m.rubricSubjects.PackageOn == nil {
			return delivery.RubricSubject{}, ErrUnavailable
		}
		request := delivery.RubricClientRequest{ProjectID: project, PackageID: packageID, ItemID: item, Write: in != nil}
		if in != nil {
			if in.ResultID != 0 {
				return delivery.RubricSubject{}, ErrInvalid
			}
			request.Version = in.WorkVersion
			request.Stage = in.Stage
		}
		return m.rubricSubjects.PackageOn(ctx, tx, request)
	}
	if m.rubricSubjects.WorkOn == nil {
		return delivery.RubricSubject{}, ErrUnavailable
	}
	result, version, stage := guid.ID(0), 0, ""
	if in != nil {
		result, version, stage = in.ResultID, in.WorkVersion, in.Stage
	}
	return m.rubricSubjects.WorkOn(ctx, tx, project, work, result, version, stage)
}
func (m *Module) rubricBoardOn(ctx context.Context, tx *gorm.DB, target delivery.RubricSubject, public bool) (RubricScoreboard, error) {
	out := RubricScoreboard{EvidenceRefs: slices.Clone(target.EvidenceIDs), WorkVersion: target.Version, ResultID: target.ResultID, ResultVersion: target.ResultVersion, Receipts: []RubricScoreReceipt{}, Divergences: []RubricDivergence{}, AllowedStages: []string{}, Availability: "UNAVAILABLE", AcceptanceEffect: "NONE"}
	pin, found, e := m.verifiersForWorkOn(ctx, tx, authn.TenantIDFromContext(ctx), target.ProjectID, target.WorkItemID)
	if e != nil {
		return out, e
	}
	if !found || pin.Rubric == nil {
		return out, nil
	}
	out.TemplateVersionID, out.DefinitionHash, out.Definition, out.Availability = pin.TemplateVersionID, pin.DefinitionHash, pin.Rubric, "AVAILABLE"
	// UI capabilities are source-authorized in the same transaction. A denial
	// removes only that affordance; infrastructure errors are not hidden.
	if public {
		out.EvidenceRefs = []guid.ID{}
		out.ResultID = 0
		out.ResultVersion = 0
		if target.ActorKind == "CLIENT" {
			out.AllowedStages = []string{"CLIENT_ACCEPTANCE"}
		}
	}
	q := query.BindLifecycleReceipts(tx, m.names)
	rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(target.ProjectID), q.ObjectKey.Eq(rubricScoreObject(target.WorkItemID, target.ResultID)), q.EventType.In("rubric.score_recorded", "rubric.divergence_detected"), q.TemplateVersionID.Eq(pin.TemplateVersionID)).Order(q.ID.Desc()).Find()
	if e != nil {
		return out, e
	}
	for _, row := range rows {
		var p rubricScorePayload
		if json.Unmarshal([]byte(row.Payload), &p) != nil || p.Score.DefinitionHash != pin.DefinitionHash || p.Score.ResultID != target.ResultID || p.Score.ResultVersion != target.ResultVersion || p.Score.HeadCommit != target.HeadCommit {
			return out, ErrConflict
		}
		if public && p.Score.Stage == "CLIENT_ACCEPTANCE" && p.SourcePackageID != target.PackageID {
			continue
		}
		out.Receipts = append(out.Receipts, p.Score)
	}
	out.Divergences = rubricDivergences(out.Receipts)
	if public {
		for i := range out.Receipts {
			r := &out.Receipts[i]
			r.WorkItemID = 0
			r.ResultID = 0
			r.ResultVersion = 0
			r.HeadCommit = ""
			r.Authority = ""
			r.ScorerID = 0
			for j := range r.Scores {
				r.Scores[j].EvidenceRefs = []guid.ID{}
				r.Scores[j].Proofs = nil
				if r.ScorerKind != "CLIENT" {
					r.Scores[j].Note = ""
				}
			}
		}
	}
	return out, nil
}
func rubricDivergences(receipts []RubricScoreReceipt) []RubricDivergence {
	out := []RubricDivergence{}
	latest := map[string]RubricScoreReceipt{}
	for _, r := range receipts {
		if _, ok := latest[r.Stage]; !ok {
			latest[r.Stage] = r
		}
	}
	stages := []string{"AGENT_ACCEPTANCE", "HUMAN_REVIEW", "CLIENT_ACCEPTANCE"}
	for i, left := range stages {
		for _, right := range stages[i+1:] {
			a, ok := latest[left]
			if !ok {
				continue
			}
			b, ok := latest[right]
			if !ok {
				continue
			}
			for _, x := range a.Scores {
				if x.Score == nil {
					continue
				}
				for _, y := range b.Scores {
					if x.Key != y.Key || y.Score == nil {
						continue
					}
					av, e := rubricNumber(*x.Score)
					if e != nil {
						continue
					}
					bv, e := rubricNumber(*y.Score)
					if e != nil {
						continue
					}
					delta := new(big.Rat).Abs(new(big.Rat).Sub(av, bv))
					if delta.Cmp(big.NewRat(1, 1)) >= 0 {
						out = append(out, RubricDivergence{Dimension: x.Key, LeftID: a.ID, RightID: b.ID, LeftStage: left, RightStage: right, Delta: delta.RatString()})
					}
				}
			}
		}
	}
	return out
}
func (m *Module) recordRubricScoreOn(ctx context.Context, tx *gorm.DB, target delivery.RubricSubject, in RubricScoreInput) (RubricScoreReceipt, error) {
	in.Scores = slices.Clone(in.Scores)
	var empty RubricScoreReceipt
	if len(in.Scores) < 1 || len(in.Scores) > 50 || target.Authority == "" || !slices.Contains([]string{"AGENT", "HUMAN", "CLIENT"}, target.ActorKind) {
		return empty, ErrInvalid
	}
	if (in.Stage == "AGENT_ACCEPTANCE" && target.ActorKind != "AGENT") || (in.Stage == "HUMAN_REVIEW" && target.ActorKind != "HUMAN") || (in.Stage == "CLIENT_ACCEPTANCE" && target.ActorKind != "CLIENT") {
		return empty, ErrForbidden
	}
	if !slices.Contains([]string{"AGENT_ACCEPTANCE", "HUMAN_REVIEW", "CLIENT_ACCEPTANCE"}, in.Stage) {
		return empty, ErrInvalid
	}
	board, e := m.rubricBoardOn(ctx, tx, target, false)
	if e != nil {
		return empty, e
	}
	if board.Definition == nil {
		return empty, ErrUnavailable
	}
	raw := make([]RubricDimensionScore, len(in.Scores))
	for i, s := range in.Scores {
		if len(s.Proofs) > 0 || strings.TrimSpace(s.Note) == "" || len(s.Note) > 2000 || len(s.EvidenceRefs) > 100 {
			return empty, ErrInvalid
		}
		if target.PackageHash != "" {
			s.EvidenceRefs = slices.Clone(target.EvidenceIDs)
			in.Scores[i].EvidenceRefs = s.EvidenceRefs
		}
		if len(s.EvidenceRefs) == 0 {
			return empty, ErrInvalid
		}
		seen := map[guid.ID]bool{}
		for _, id := range s.EvidenceRefs {
			if id <= 0 || seen[id] || !slices.Contains(target.EvidenceIDs, id) {
				return empty, ErrInvalid
			}
			seen[id] = true
		}
		raw[i] = s.RubricDimensionScore
	}
	minimum := json.Number("")
	pin, found, e := m.verifiersForWorkOn(ctx, tx, authn.TenantIDFromContext(ctx), target.ProjectID, target.WorkItemID)
	if e != nil {
		return empty, e
	}
	if !found {
		return empty, ErrUnavailable
	}
	// A frozen Brief parameter may raise the threshold; the calculator rejects a
	// lower value. Never accept a request-supplied minimum.
	if value, exists := pin.Parameters["rubric_weighted_min"]; exists {
		switch v := value.(type) {
		case string:
			minimum = json.Number(v)
		case json.Number:
			minimum = v
		case float64:
			minimum = json.Number(fmt.Sprint(v))
		default:
			return empty, ErrInvalid
		}
	}
	calculation, e := CalculateRubric(*board.Definition, raw, minimum)
	if e != nil {
		return empty, e
	}
	for _, dim := range board.Definition.Dimensions {
		if dim.Kind != "HARD_GATE" {
			continue
		}
		index := slices.IndexFunc(in.Scores, func(s RubricScoreValue) bool { return s.Key == dim.Key })
		score := in.Scores[index]
		if score.PassFail == nil || !*score.PassFail {
			continue
		}
		if m.rubricProof == nil {
			return empty, ErrUnavailable
		}
		count := 0
		for _, key := range dim.VerifierKeys {
			defIndex := slices.IndexFunc(pin.Verifiers, func(v VerifierDefinition) bool { return v.Key == key && v.Kind == "DETERMINISTIC" })
			if defIndex < 0 {
				continue
			}
			count++
			fact, e := m.rubricProof(ctx, tx, RubricProofRequest{TemplateVersionID: pin.TemplateVersionID, DefinitionHash: pin.DefinitionHash, ProjectID: target.ProjectID, WorkItemID: target.WorkItemID, ResultID: target.ResultID, ResultVersion: target.ResultVersion, HeadCommit: target.HeadCommit, VerifierKeys: []string{key}})
			if e != nil {
				return empty, e
			}
			if !fact.Passed || fact.TaskID <= 0 || fact.VerifierKey != key || len(fact.ReceiptHash) != 64 {
				return empty, ErrConflict
			}
			in.Scores[index].Proofs = append(in.Scores[index].Proofs, fact)
		}
		if count == 0 {
			return empty, ErrUnavailable
		}
	}

	id, e := m.ids.Next()
	if e != nil {
		return empty, e
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	receipt := RubricScoreReceipt{ID: guid.ID(id), TemplateVersionID: board.TemplateVersionID, DefinitionHash: board.DefinitionHash, WorkItemID: target.WorkItemID, ResultID: target.ResultID, ResultVersion: target.ResultVersion, HeadCommit: target.HeadCommit, PackageHash: target.PackageHash, Stage: in.Stage, ScorerKind: target.ActorKind, ScorerID: authn.PrincipalIDFromContext(ctx), Authority: target.Authority, Scores: in.Scores, Calculation: calculation, CreatedAt: at}
	object := rubricScoreObject(target.WorkItemID, target.ResultID)
	compared := append([]RubricScoreReceipt{receipt}, board.Receipts...)
	relevant := []RubricDivergence{}
	for _, d := range rubricDivergences(compared) {
		if d.LeftID == receipt.ID || d.RightID == receipt.ID {
			relevant = append(relevant, d)
		}
	}
	view := LifecycleView{ID: receipt.ID, TemplateVersionID: board.TemplateVersionID, ProjectID: target.ProjectID, ObjectKey: object, Reference: object, InitiatorID: receipt.ScorerID, EventType: "rubric.score_recorded", State: "RECORDED", CreatedAt: at}
	if len(relevant) > 0 {
		keys := []string{}
		for _, d := range relevant {
			if !slices.Contains(keys, d.Dimension) {
				keys = append(keys, d.Dimension)
			}
		}
		view.EventType = "rubric.divergence_detected"
		view.NoticeType = view.EventType
		view.Params = map[string]string{"subject": target.WorkItemID.String(), "dimensions": strings.Join(keys, ", ")}
	}
	payload, e := json.Marshal(rubricScorePayload{SourcePackageID: target.PackageID, View: view, Score: receipt, Divergences: relevant})
	if e != nil {
		return empty, e
	}
	row := model.LifecycleReceipt{ID: receipt.ID, TenantID: authn.TenantIDFromContext(ctx), ProjectID: target.ProjectID, TemplateVersionID: board.TemplateVersionID, ObjectKey: object, SourceKey: "rubric-score:" + receipt.ID.String(), EventType: view.EventType, State: "RECORDED", Payload: string(payload), RequestHash: evalDigest(payload), CreatedBy: receipt.ScorerID, CreatedAt: at}
	if e = query.BindLifecycleReceipts(tx, m.names).WithContext(ctx).Create(&row); e != nil {
		return empty, e
	}
	a := audit.NewDomainEvent(ctx, row.TenantID, "rubric.score_recorded", "template_lifecycle", row.ID)
	a.Detail["authority_source"], a.Detail["actor_kind"], a.Detail["result_id"], a.Detail["stage"] = target.Authority, target.ActorKind, target.ResultID, in.Stage
	if e = m.audit(ctx, tx, a); e != nil {
		return empty, e
	}
	actor := events.Actor{Kind: target.ActorKind, ID: row.CreatedBy, AuthoritySource: target.Authority}
	// Client is an authenticated Human principal in the existing portal model.
	if actor.Kind == "CLIENT" {
		actor.Kind = "HUMAN"
	}
	_, e = m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: target.ProjectID, Type: row.EventType, Actor: actor, Subject: events.Subject{Kind: "template_lifecycle", ID: row.ID}, SubjectVersion: 1, Data: map[string]any{"score_id": receipt.ID, "result_id": target.ResultID, "stage": in.Stage, "passed": calculation.Passed, "dimensions": relevant}})
	if e != nil {
		return empty, e
	}

	return receipt, e
}

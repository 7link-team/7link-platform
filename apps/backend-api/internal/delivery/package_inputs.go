package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"time"
)

// PackageInputs returns current accepted decisions and portal policy in the
// caller's transaction. These facts are private build inputs, never portal IDs.
type PackageInputs struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []ClientArtifactRef) (PackageInputFacts, error)
}
type PackageInputItem struct {
	WorkItemID         guid.ID `json:"work_item_id"`
	AcceptedDecisionID guid.ID `json:"accepted_decision_id"`
}
type PackageInputFacts struct {
	Items              []PackageInputItem  `json:"items"`
	PortalSettings     json.RawMessage     `json:"portal_settings"`
	RedactionTerms     []string            `json:"redaction_terms"`
	Project            PackageProjectFacts `json:"project"`
	WorkItems          []PackageWorkFacts  `json:"work_items"`
	SourceExecutionIDs []guid.ID           `json:"-"`
}
type PackageProjectFacts struct {
	ID   guid.ID `json:"id"`
	Name string  `json:"name"`
	Code string  `json:"code"`
}
type PackageRequirementFacts struct {
	ID      guid.ID `json:"id"`
	Version int     `json:"version"`
}
type PackageCriterionFacts struct {
	ID          guid.ID   `json:"id"`
	Text        string    `json:"text"`
	EvidenceIDs []guid.ID `json:"evidence_ids"`
}
type PackageVerificationFacts struct {
	Verifier    string `json:"verifier"`
	Layer       string `json:"layer"`
	Result      string `json:"result"`
	ReceiptHash string `json:"receipt_hash,omitempty"`
}
type PackageEvidenceFacts struct {
	ID             guid.ID                    `json:"id"`
	Type           string                     `json:"type"`
	Producer       string                     `json:"producer"`
	ProducedAt     time.Time                  `json:"produced_at"`
	OriginalSHA256 string                     `json:"original_sha256"`
	Limitations    string                     `json:"limitations"`
	IncludeMode    string                     `json:"include_mode"`
	Verification   []PackageVerificationFacts `json:"verification"`
}
type PackageWorkFacts struct {
	WorkItemID  guid.ID                  `json:"work_item_id"`
	Key         string                   `json:"key"`
	Title       string                   `json:"title"`
	Type        string                   `json:"type"`
	Requirement *PackageRequirementFacts `json:"requirement"`
	AcceptedAt  *time.Time               `json:"accepted_at"`
	AcceptedBy  string                   `json:"accepted_by"`
	Criteria    []PackageCriterionFacts  `json:"criteria"`
	Evidence    []PackageEvidenceFacts   `json:"evidence"`
}

func (m *Module) providePackageInputs(s *module.Services) error {
	return module.Provide[PackageInputs](s, PackageInputs{ReadOn: m.packageInputsOn})
}
func (m *Module) packageInputsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, refs []ClientArtifactRef) (PackageInputFacts, error) {
	out := PackageInputFacts{Items: []PackageInputItem{}}
	ids := []guid.ID{}
	seen := map[guid.ID]bool{}
	for _, r := range refs {
		if !seen[r.WorkItemID] {
			ids = append(ids, r.WorkItemID)
			seen[r.WorkItemID] = true
		}
	}
	slices.Sort(ids)
	q := query.BindWorkItems(tx, m.service.names)
	for _, id := range ids {
		w, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.ArchivedAt.IsNull()).First()
		if e != nil {
			return out, e
		}
		if w.State != "ACCEPTED" || w.AcceptedDecisionID == nil || w.BaselineStale {
			return out, ErrResultState
		}
		out.Items = append(out.Items, PackageInputItem{id, *w.AcceptedDecisionID})
	}
	if _, e := (clientWork{m: m}).AcceptedArtifactsOn(ctx, tx, tenant, project, refs); e != nil {
		return out, e
	}
	p := query.BindProjects(tx, m.service.names)
	row, e := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).First()
	if e != nil {
		return out, e
	}
	out.PortalSettings = json.RawMessage(row.PortalSettings)
	if m.service.entities.GetOn == nil {
		return out, ErrUnavailable
	}
	entity, e := m.service.entities.GetOn(ctx, tx, tenant, project)
	if e != nil {
		return out, e
	}
	if entity.ID != project || entity.TenantID != tenant || entity.Type != "project" {
		return out, ErrProjectNotFound
	}
	out.Project = PackageProjectFacts{ID: project, Name: entity.Name, Code: row.Code}
	workFacts := map[guid.ID]*PackageWorkFacts{}
	for _, id := range ids {
		work, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
		if e != nil {
			return out, e
		}
		actorLabel, e := m.packageActorLabelOn(ctx, tx, tenant, project)
		if e != nil {
			return out, e
		}
		fact := PackageWorkFacts{WorkItemID: id, Key: fmt.Sprintf("%s-%d", row.Code, work.Number), Title: work.Title, Type: work.Type, AcceptedAt: work.AcceptedAt, AcceptedBy: actorLabel, Criteria: []PackageCriterionFacts{}, Evidence: []PackageEvidenceFacts{}}
		if work.RequirementID != nil {
			fact.Requirement = &PackageRequirementFacts{ID: *work.RequirementID, Version: work.BaselineVersionNo}
		}
		criteria := query.BindWorkCriteria(tx, m.service.names)
		clauses, e := criteria.WithContext(ctx).Where(criteria.TenantID.Eq(tenant), criteria.ProjectID.Eq(project), criteria.WorkItemID.Eq(id)).Order(criteria.Position, criteria.ID).Find()
		if e != nil {
			return out, e
		}
		for _, clause := range clauses {
			fact.Criteria = append(fact.Criteria, PackageCriterionFacts{ID: clause.ID, Text: clause.Text, EvidenceIDs: []guid.ID{}})
		}
		out.WorkItems = append(out.WorkItems, fact)
		workFacts[id] = &out.WorkItems[len(out.WorkItems)-1]
	}
	// Slice growth can move earlier values; rebuild pointers once capacity is fixed.
	for i := range out.WorkItems {
		workFacts[out.WorkItems[i].WorkItemID] = &out.WorkItems[i]
	}
	actorIDs := map[guid.ID]bool{}
	for _, ref := range refs {
		result, e := m.resultOn(ctx, tx, tenant, project, ref.WorkItemID, ref.ResultID)
		if e != nil {
			return out, e
		}
		actorIDs[result.ActorID] = true
		if result.SourceExecutionID != nil {
			out.SourceExecutionIDs = append(out.SourceExecutionIDs, *result.SourceExecutionID)
		}
		view, e := m.resultViewOn(ctx, tx, result)
		if e != nil {
			return out, e
		}
		for _, action := range view.Actions {
			if action.ActorID > 0 {
				actorIDs[action.ActorID] = true
			}
		}
		for _, verification := range view.Verifications {
			if verification.ActorID > 0 {
				actorIDs[verification.ActorID] = true
			}
			if verification.ReviewerID != nil && *verification.ReviewerID > 0 {
				actorIDs[*verification.ReviewerID] = true
			}
		}
		for _, evidence := range view.Evidence {
			if evidence.ID != ref.EvidenceID {
				continue
			}
			producer, e := m.packageActorLabelOn(ctx, tx, tenant, project)
			if e != nil {
				return out, e
			}
			if producer == "ENGINEER" && m.agents.ByPrincipalOn != nil {
				_, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, result.ActorID)
				if err != nil {
					return out, err
				}
				if found {
					producer = "AI_MEMBER"
				}
			}
			fact := PackageEvidenceFacts{ID: evidence.ID, Type: "FILE", Producer: producer, ProducedAt: result.CreatedAt, OriginalSHA256: evidence.SHA256, Limitations: evidence.Note, IncludeMode: "FILE", Verification: []PackageVerificationFacts{}}
			for _, mapping := range view.CriteriaMap {
				for _, eid := range mapping.EvidenceIDs {
					if eid != evidence.ID {
						continue
					}
					for _, run := range view.Verifications {
						if run.CriterionID == mapping.CriterionID && run.State == "DONE" {
							fact.Verification = append(fact.Verification, PackageVerificationFacts{Verifier: run.Method, Layer: run.Stage, Result: run.Verdict, ReceiptHash: run.ReceiptHash})
						}
					}
				}
			}
			work := workFacts[ref.WorkItemID]
			work.Evidence = append(work.Evidence, fact)
			for i := range work.Criteria {
				if evidence.CriterionID != nil && work.Criteria[i].ID == *evidence.CriterionID {
					work.Criteria[i].EvidenceIDs = append(work.Criteria[i].EvidenceIDs, evidence.ID)
				}
			}
		}
		overrides, e := m.verificationOverridesOn(ctx, tx, result)
		if e != nil {
			return out, e
		}
		for _, override := range overrides {
			actorIDs[override.ActorID] = true
		}
	}
	for actor := range actorIDs {
		if actor >= 1_000_000_000_000_000 {
			out.RedactionTerms = append(out.RedactionTerms, actor.String())
		}
		if m.principals.ActiveRow == nil {
			return out, ErrUnavailable
		}
		principal, found, e := m.principals.ActiveRow(ctx, tx, actor)
		if e != nil {
			return out, e
		}
		if !found {
			return out, ErrUnavailable
		}
		if found {
			for _, term := range []string{principal.DisplayName, principal.LoginName} {
				if term != "" {
					out.RedactionTerms = append(out.RedactionTerms, term)
				}
			}
		}
		if m.agents.ByPrincipalOn != nil {
			agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
			if e != nil {
				return out, e
			}
			if found && agent.Name != "" {
				out.RedactionTerms = append(out.RedactionTerms, agent.Name)
			}
		}
	}
	slices.Sort(out.RedactionTerms)
	out.RedactionTerms = slices.Compact(out.RedactionTerms)
	slices.Sort(out.SourceExecutionIDs)
	out.SourceExecutionIDs = slices.Compact(out.SourceExecutionIDs)
	for i := range out.WorkItems {
		slices.SortFunc(out.WorkItems[i].Evidence, func(a, b PackageEvidenceFacts) int {
			if a.ID < b.ID {
				return -1
			}
			if a.ID > b.ID {
				return 1
			}
			return 0
		})
		for j := range out.WorkItems[i].Criteria {
			slices.Sort(out.WorkItems[i].Criteria[j].EvidenceIDs)
		}
	}
	return out, nil
}

// Missing/malformed portal configuration keeps the restrictive TEAM_ONLY label.
func (m *Module) packageActorLabelOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (string, error) {
	q := query.BindProjects(tx, m.service.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).First()
	if e != nil {
		return "", e
	}
	var policy struct {
		ContributorDisplay string `json:"contributor_display"`
	}
	if json.Unmarshal([]byte(row.PortalSettings), &policy) == nil && policy.ContributorDisplay == "ROLES" {
		return "ENGINEER", nil
	}
	return "TEAM", nil
}

package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) bindKnowledgeEventReader(s *module.Services) error {
	connector, err := module.Resolve[knowledge.EventSourceReaderConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = connector.Bind(knowledge.EventSourceReader{ReadOn: m.knowledgeEventDocumentsOn}); err != nil {
		return err
	}
	injection, err := module.Resolve[knowledge.InjectionWorkConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return injection.Bind(knowledge.InjectionWorkReader{VerifyOn: m.verifyKnowledgeInjectionWorkOn})
}

func (m *Module) verifyKnowledgeInjectionWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, work, agent guid.ID) error {
	if agent <= 0 {
		return ErrInvalidArgument
	}
	// Canonical ready-context reader performs current principal/member/project
	// locks and the work item's current data scope before any context is read.
	if _, err := m.workKnowledgeContextOn(ctx, tx, tenant, project, work); err != nil {
		return err
	}
	a := query.BindAssignments(tx, m.service.names)
	row, err := a.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.Eq(work), a.AssigneePrincipalID.Eq(agent), a.AssigneeKind.Eq("AGENT"), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE")).First()
	if err != nil {
		return err
	}
	if row.LeaseExpiresAt != nil && !row.LeaseExpiresAt.After(time.Now().UTC()) {
		return ErrWorkForbidden
	}
	return nil
}

// The event is a pointer to immutable owner records, never authoritative content.
// Historical replay deliberately does not impersonate the original actor.
func (m *Module) knowledgeEventDocumentsOn(ctx context.Context, tx *gorm.DB, event events.Envelope) ([]knowledge.EventSourceDocument, error) {
	if m == nil || m.service == nil || tx == nil || tx.Statement == nil || event.TenantID <= 0 || event.ProjectID <= 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	if event.EventType == "requirement.versioned" {
		var payload struct {
			Version int `json:"version_no"`
		}
		if json.Unmarshal(event.Data, &payload) != nil || payload.Version < 1 {
			return nil, ErrInvalidArgument
		}
		q := query.BindRequirementVersions(tx, m.service.names)
		baseline, err := q.WithContext(ctx).Where(q.TenantID.Eq(event.TenantID), q.ProjectID.Eq(event.ProjectID), q.RequirementID.Eq(event.Subject.ID), q.VersionNo.Eq(payload.Version)).First()
		if err != nil {
			return nil, err
		}
		var criteria []requirementCriterionView
		if err = json.Unmarshal([]byte(baseline.Criteria), &criteria); err != nil {
			return nil, err
		}
		var body strings.Builder
		fmt.Fprintf(&body, "# %s\n\n%s\n\n## Acceptance criteria\n", baseline.Title, baseline.Description)
		for _, criterion := range criteria {
			fmt.Fprintf(&body, "\n### [%s]\n\n%s\n", criterion.ID, criterion.Text)
		}
		return []knowledge.EventSourceDocument{{Kind: "REQUIREMENT", Origin: "requirement", OriginID: baseline.RequirementID, Version: baseline.VersionNo, Title: baseline.Title, Content: body.String()}}, nil
	}
	if event.EventType == "requirement.scope_changed" {
		var payload struct {
			ChangeRecordID guid.ID `json:"change_record_id"`
		}
		if json.Unmarshal(event.Data, &payload) != nil || payload.ChangeRecordID <= 0 {
			return nil, ErrInvalidArgument
		}
		q := query.BindChanges(tx, m.service.names)
		change, err := q.WithContext(ctx).Where(q.TenantID.Eq(event.TenantID), q.ProjectID.Eq(event.ProjectID), q.ID.Eq(payload.ChangeRecordID), q.RequirementID.Eq(event.Subject.ID)).First()
		if err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(change.Summary) < 20 {
			return nil, nil
		}
		return []knowledge.EventSourceDocument{{Kind: "DECISION", Origin: "decision", OriginID: change.ID, Version: 1, Title: "Scope change", Content: "## SCOPE_CHANGE\n\n" + change.Summary + fmt.Sprintf("\n\n- Requirement [%s], baseline %d → %d\n", change.RequirementID, change.FromVersionNo, change.ToVersionNo)}}, nil
	}
	if event.EventType != "work_item.state_changed" && event.EventType != "result.feedback_added" {
		return nil, nil
	}
	var payload struct {
		Reason     string  `json:"reason_code"`
		ResultID   string  `json:"result_id"`
		DecisionID guid.ID `json:"decision_id"`
	}
	if json.Unmarshal(event.Data, &payload) != nil {
		return nil, ErrInvalidArgument
	}
	kind := ""
	switch payload.Reason {
	case "RESULT_ACCEPTED":
		kind = "ACCEPT"
	case "RESULT_RETURNED", "SCOPE_REQUEST_CHANGES":
		kind = "RETURN"
	case "RESULT_RECEIVED":
		kind = "RECEIPT"
	default:
		if event.EventType != "result.feedback_added" || payload.DecisionID <= 0 {
			return nil, nil
		}
		kind, payload.ResultID = "RETURN", event.Subject.ID.String()
	}
	a := query.BindResultActions(tx, m.service.names)
	// Work transitions pin result IDs; time and actor bind this exact action,
	// preventing a later decision from leaking into an older event replay.
	var action *model.ResultAction
	var err error
	if event.EventType == "result.feedback_added" {
		action, err = a.WithContext(ctx).Where(a.TenantID.Eq(event.TenantID), a.ProjectID.Eq(event.ProjectID), a.ID.Eq(payload.DecisionID), a.Kind.Eq("RETURN"), a.ActorID.Eq(guid.ID(0)), a.CreatedAt.Lte(event.OccurredAt)).First()
	} else {
		action, err = a.WithContext(ctx).Where(a.TenantID.Eq(event.TenantID), a.ProjectID.Eq(event.ProjectID), a.WorkItemID.Eq(event.Subject.ID), a.Kind.Eq(kind), a.ActorID.Eq(event.Actor.ID), a.CreatedAt.Lte(event.OccurredAt)).Order(a.ID.Desc()).First()
	}
	if err != nil {
		return nil, err
	}
	if payload.ResultID != action.ResultID.String() {
		return nil, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, err := w.WithContext(ctx).Where(w.TenantID.Eq(event.TenantID), w.ProjectID.Eq(event.ProjectID), w.ID.Eq(action.WorkItemID)).First()
	if err != nil {
		return nil, err
	}
	r := query.BindResultVersions(tx, m.service.names)
	result, err := r.WithContext(ctx).Where(r.TenantID.Eq(event.TenantID), r.ProjectID.Eq(event.ProjectID), r.ID.Eq(action.ResultID), r.WorkItemID.Eq(work.ID)).First()
	if err != nil {
		return nil, err
	}
	e := query.BindResultEvidence(tx, m.service.names)
	evidence, err := e.WithContext(ctx).Where(e.TenantID.Eq(event.TenantID), e.ProjectID.Eq(event.ProjectID), e.ResultID.Eq(result.ID)).Order(e.ID).Find()
	if err != nil {
		return nil, err
	}
	var refs strings.Builder
	for _, item := range evidence {
		fmt.Fprintf(&refs, "- [%s] %s (%s)\n", item.ID, item.Name, item.Coverage)
	}
	out := []knowledge.EventSourceDocument{}
	if kind == "RETURN" {
		f := query.BindResultFeedback(tx, m.service.names)
		feedback, e := f.WithContext(ctx).Where(f.TenantID.Eq(event.TenantID), f.ProjectID.Eq(event.ProjectID), f.WorkItemID.Eq(work.ID), f.ResultVersionID.Eq(result.ID), f.CreatedAt.Lte(action.CreatedAt)).Order(f.ID).Find()
		if e != nil {
			return nil, e
		}
		var body strings.Builder
		fmt.Fprintf(&body, "## Rejection feedback\n\n%s\n", action.Reason)
		for _, item := range feedback {
			fmt.Fprintf(&body, "\n### %s\n\n%s\n", item.Category, item.Detail)
		}
		out = append(out, knowledge.EventSourceDocument{Kind: "FEEDBACK", Origin: "decision", OriginID: action.ID, Version: 1, WorkItemID: &work.ID, Title: work.Title, Content: body.String()})
	}
	if kind == "ACCEPT" && utf8.RuneCountInString(action.Reason) >= 20 {
		out = append(out, knowledge.EventSourceDocument{Kind: "DECISION", Origin: "decision", OriginID: action.ID, Version: 1, WorkItemID: &work.ID, Title: work.Title, Content: "## APPROVE\n\n" + action.Reason + "\n\n## Evidence references\n\n" + refs.String()})
	}
	if kind == "ACCEPT" || kind == "RECEIPT" {
		out = append(out, knowledge.EventSourceDocument{Kind: "DELIVERY", Origin: "work_item_delivery", OriginID: work.ID, Version: int(event.SubjectVersion), WorkItemID: &work.ID, Title: work.Title,
			Content: "# " + work.Title + "\n\n## Declaration summary\n\n" + result.Summary + "\n\n## Evidence summary\n\n" + refs.String()})
	}
	return out, nil
}

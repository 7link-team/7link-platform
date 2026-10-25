package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"gorm.io/gorm"
	"time"
)

// publishQualityEscapeOn derives gate facts exclusively from exact-result
// receipts. A rejection without prior internal acceptance is not an escape.
func (m *Module) publishQualityEscapeOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, result *model.ResultVersion, decision, actor guid.ID, summary string) error {
	q := query.BindVerificationRuns(tx, m.service.names)
	runs, err := q.WithContext(ctx).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID), q.ResultID.Eq(result.ID), q.State.Eq("DONE"), q.Verdict.Eq("PASSED"), q.Stage.In("AGENT_TEST", "AGENT_ACCEPTANCE")).Order(q.ID).Find()
	if err != nil {
		return err
	}
	passed := []map[string]any{}
	acceptance := false
	for _, r := range runs {
		if r.ReviewerID == nil || r.ReviewerFamily == "" || len(r.ReceiptHash) != 64 {
			continue
		}
		if r.Stage == "AGENT_ACCEPTANCE" {
			acceptance = true
		}
		passed = append(passed, map[string]any{"stage": r.Stage, "principal_id": r.ReviewerID, "model_family": r.ReviewerFamily, "verifier_version": r.ReceiptHash})
	}
	if !acceptance {
		return nil
	}
	if err = m.recordReportQualityOn(ctx, tx, work.TenantID, work.ProjectID, work.ID, "ESCAPE_CLIENT", decision, 0, time.Now().UTC().Truncate(time.Millisecond), result.ID); err != nil {
		return err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: work.TenantID, ProjectID: work.ProjectID, Type: "quality.escaped", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:decision_request_changes"}, Subject: events.Subject{Kind: "work_item", ID: work.ID}, Data: map[string]any{"escape_key": work.ID.String() + ":" + decision.String(), "work_item_id": work.ID, "stage": "CLIENT_ACCEPTANCE", "category": "FUNCTIONAL_DEFECT", "summary": summary, "passed_by": passed, "decision_ref": decision}})
	return err
}

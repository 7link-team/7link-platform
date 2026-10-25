package intake

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/intake/model"
	"7link.com/platform/internal/intake/query"
	"gorm.io/gorm"
)

// SubmissionChangeView is a serialization whitelist, never a triage-card map.
// Fields without canonical persisted facts remain absent, not inferred from
// internal impact prose or approval-like user payloads.
type SubmissionChangeView struct {
	Title             string                   `json:"title"`
	RefNo             int                      `json:"ref_no"`
	Status            string                   `json:"status"`
	ResultVersionNo   *int                     `json:"result_version_no,omitempty"`
	ClientMessage     *string                  `json:"client_message,omitempty"`
	CriteriaDiff      []SubmissionCriteriaDiff `json:"criteria_diff"`
	ScheduleDeltaDays *int                     `json:"schedule_delta_days,omitempty"`
}
type SubmissionCriteriaDiff struct {
	Action string `json:"action" enum:"ADD,REMOVE,CHANGE"`
	Text   string `json:"text"`
}

func (m *Module) submissionChangeOn(ctx context.Context, tx *gorm.DB, row *model.Record) (*SubmissionChangeView, error) {
	view := &SubmissionChangeView{Title: row.Title, RefNo: row.RefNo, Status: "ASSESSING", ResultVersionNo: row.ResultVersionNo, ClientMessage: row.ClientMessage, CriteriaDiff: []SubmissionCriteriaDiff{}}
	switch row.Status {
	case "CLIENT_APPROVAL":
		view.Status = "AWAITING_YOUR_APPROVAL"
	case "CONVERTED":
		view.Status = "ACCEPTED"
	case "DECLINED":
		view.Status = "NOT_ACCEPTED"
	case "MERGED":
		view.Status = "MERGED"
	}
	if row.LatestCardRevision == 0 {
		return view, nil
	}
	q := query.BindTriageCards(tx, m.names)
	card, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.IntakeID.Eq(row.ID), q.Revision.Eq(row.LatestCardRevision)).Take()
	if err != nil {
		return nil, err
	}
	// Decode only named safe facts from the frozen canonical proposal. In
	// particular cost_delta_micros, stale_work_ids and ImpactJSON are never read.
	var proposal struct {
		ToleranceDelta struct {
			ScheduleDeltaDays *int `json:"schedule_delta_days"`
		} `json:"tolerance_delta"`
		Change struct {
			Criteria struct {
				Add []struct {
					Text string `json:"text"`
				} `json:"add"`
				Supersede []struct {
					Text string `json:"text"`
				} `json:"supersede"`
			} `json:"criteria"`
		} `json:"change"`
	}
	if err = json.Unmarshal([]byte(card.ProposalJSON), &proposal); err != nil {
		return nil, err
	}
	view.ScheduleDeltaDays = proposal.ToleranceDelta.ScheduleDeltaDays
	for _, criterion := range proposal.Change.Criteria.Add {
		view.CriteriaDiff = append(view.CriteriaDiff, SubmissionCriteriaDiff{Action: "ADD", Text: criterion.Text})
	}
	for _, criterion := range proposal.Change.Criteria.Supersede {
		view.CriteriaDiff = append(view.CriteriaDiff, SubmissionCriteriaDiff{Action: "CHANGE", Text: criterion.Text})
	}
	return view, nil
}

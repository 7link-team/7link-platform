package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"gorm.io/gorm"
)

// Each section has a byte budget so a large manifest remains a valid producer
// objective. Whole records are retained and omissions are explicit references
// to the durable retirement manifest, never silently truncated source facts.
func retirementSummaryList[T any](rows []T, budget int) (string, error) {
	var out strings.Builder
	for i, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			return "", err
		}
		if out.Len()+len(raw)+1 > budget {
			fmt.Fprintf(&out, "Omitted %d of %d source records for the document size limit; consult the source/retirement manifest.\n", len(rows)-i, len(rows))
			return out.String(), nil
		}
		out.Write(raw)
		out.WriteByte('\n')
	}
	if len(rows) == 0 {
		out.WriteString("No matching source records.\n")
	}
	return out.String(), nil
}

type retirementInboxReference struct {
	Seq       int64   `json:"seq"`
	Kind      string  `json:"kind"`
	SubjectID guid.ID `json:"subject_id"`
	StreamKey string  `json:"stream_key,omitempty"`
}

func (m *Module) retirementSummaryOn(ctx context.Context, tx *gorm.DB, r *model.Retirement) (string, error) {
	if err := m.checkRetirementSourceReadsOn(ctx, tx, r); err != nil {
		return "", err
	}
	work, err := retirementSummaryList(r.Precheck.WorkItems, 3400)
	if err != nil {
		return "", err
	}
	executions, err := retirementSummaryList(r.Precheck.Executions, 2400)
	if err != nil {
		return "", err
	}
	decisions, rules := "Source capability unavailable.\n", "Source capability unavailable.\n"
	if m.retirementWork.DecisionsOn != nil {
		rows, err := m.retirementWork.DecisionsOn(ctx, tx, r.TenantID, r.AgentPrincipalID, m.now().Add(-30*24*time.Hour))
		if err != nil && !errors.Is(err, delivery.ErrUnavailable) {
			return "", retirementWorkError(err)
		}
		if err == nil {
			if r.Precheck.Progress == nil {
				r.Precheck.Progress = &model.RetirementProgress{}
			}
			for _, row := range rows {
				r.Precheck.Progress.SourceWorkItems = append(r.Precheck.Progress.SourceWorkItems, model.RetirementSourceWork{ProjectID: row.ProjectID, WorkItemID: row.WorkItemID})
			}
			decisions, err = retirementSummaryList(rows, 3000)
		} else {
			err = nil
		}
		if err != nil {
			return "", err
		}
	}
	if m.retirementRules.ReadOn != nil {
		rows, err := m.retirementRules.ReadOn(ctx, tx, r.TenantID, r.AgentPrincipalID)
		if err != nil && !errors.Is(err, knowledge.ErrForbidden) && !errors.Is(err, knowledge.ErrNotFound) && !errors.Is(err, knowledge.ErrUnavailable) {
			return "", err
		}
		if err == nil {
			if r.Precheck.Progress == nil {
				r.Precheck.Progress = &model.RetirementProgress{}
			}
			for _, row := range rows {
				r.Precheck.Progress.SourceRuleIDs = append(r.Precheck.Progress.SourceRuleIDs, row.ID)
			}
			rules, err = retirementSummaryList(rows, 3000)
		} else {
			err = nil
		}
		if err != nil {
			return "", err
		}
	}
	state := query.BindLiveState(tx, m.names)
	live, err := state.WithContext(ctx).Where(state.TenantID.Eq(r.TenantID), state.AgentPrincipalID.Eq(r.AgentPrincipalID)).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	var interactions, conversations []retirementInboxReference
	more := false
	if err == nil {
		inbox := query.BindInboxEvent(tx, m.names)
		rows, e := inbox.WithContext(ctx).Where(inbox.TenantID.Eq(r.TenantID), inbox.AgentPrincipalID.Eq(r.AgentPrincipalID), inbox.Seq.Gt(live.Ack), inbox.Seq.Lte(live.Head)).Order(inbox.Seq).Limit(1001).Find()
		if e != nil {
			return "", e
		}
		if len(rows) > 1000 {
			more, rows = true, rows[:1000]
		}
		for _, row := range rows {
			ref := retirementInboxReference{Seq: row.Seq, Kind: row.Kind, SubjectID: row.SubjectID, StreamKey: row.StreamKey}
			if row.Kind == "MESSAGE" || row.Kind == "THREAD" {
				conversations = append(conversations, ref)
			} else {
				interactions = append(interactions, ref)
			}
		}
	}
	pending, err := retirementSummaryList(interactions, 1800)
	if err != nil {
		return "", err
	}
	channels, err := retirementSummaryList(conversations, 1800)
	if err != nil {
		return "", err
	}
	if more {
		channels += "Additional pending inbox references omitted after 1000; consult the live inbox.\n"
	}
	return fmt.Sprintf("## 在途工作项\n%sBranch/blocker details unavailable unless present in cited source records; no Git state inferred.\n\n## 进行中执行与未决交互\n%sPending inbox references (unacknowledged, not proof of an unresolved source operation):\n%s\n## 近 30 天裁决与拒绝反馈摘要\n%s\n## 规则型记忆清单\n%sReferences only; current source authorization still applies.\n\n## 频道中未闭合的对话\n%sThese are unacknowledged inbox references. The source has no canonical thread-closed state; no closure status is inferred.\n", work, executions, pending, decisions, rules, channels), nil
}

// Frozen facts remain immutable, but copying or reading them never lends the
// caller the original author's source permissions. Retired/frozen RULE state
// is permitted by the source checker; current read authorization still applies.
func (m *Module) checkRetirementSourceReadsOn(ctx context.Context, tx *gorm.DB, r *model.Retirement) error {
	work := make(map[model.RetirementSourceWork]bool)
	for _, row := range r.Precheck.WorkItems {
		work[model.RetirementSourceWork{ProjectID: row.ProjectID, WorkItemID: row.WorkItemID}] = true
	}
	for _, row := range r.Precheck.Executions {
		work[model.RetirementSourceWork{ProjectID: row.ProjectID, WorkItemID: row.WorkItemID}] = true
	}
	if r.Precheck.Progress != nil {
		for _, row := range r.Precheck.Progress.SourceWorkItems {
			work[row] = true
		}
		for _, id := range r.Precheck.Progress.SourceRuleIDs {
			if m.retirementRules.CheckOn == nil {
				return ErrUnavailable
			}
			if err := m.retirementRules.CheckOn(ctx, tx, r.TenantID, r.AgentPrincipalID, id); err != nil {
				return ErrForbidden
			}
		}
	}
	for row := range work {
		if m.retirementWork.CheckReadOn == nil {
			return ErrUnavailable
		}
		if err := m.retirementWork.CheckReadOn(ctx, tx, r.TenantID, row.ProjectID, row.WorkItemID); err != nil {
			return retirementWorkError(err)
		}
	}
	return nil
}

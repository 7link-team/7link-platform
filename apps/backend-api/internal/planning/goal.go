package planning

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const goalCancelled = "CANCELLED" //nolint:misspell // Canonical domain status from the planning contract.

type PlanningGoalChange struct {
	Version             int                `json:"version" minimum:"1"`
	ExpectedContentHash string             `json:"expected_content_hash" minLength:"64" maxLength:"64"`
	Action              string             `json:"action" enum:"EDIT,ACTIVATE,CLOSE,MANUAL,REFRESH,ASSIGN_CONTRIBUTOR,REMOVE_CONTRIBUTOR"`
	PrincipalID         guid.ID            `json:"principal_id,omitempty"`
	Content             *PlanningGoalInput `json:"content,omitempty"`
	Result              string             `json:"result,omitempty" enum:"ACHIEVED,MISSED,CANCELLED"` //nolint:misspell // Canonical domain status.
	Reason              string             `json:"reason,omitempty" maxLength:"2000"`
	Key                 string             `json:"key,omitempty" maxLength:"64"`
	Current             int64              `json:"current,omitempty" minimum:"0"`
	Evidence            string             `json:"evidence,omitempty" maxLength:"2000"`
}

// goalProgress uses explicit KR weights; legacy omitted weights default to one.
func goalProgress(g PlanningGoalInput) float64 {
	if len(g.KeyResults) == 0 {
		return 0
	}
	var total, weights float64
	for _, k := range g.KeyResults {
		weight := progressWeight(k.Weight)
		weights += weight
		if k.Target == k.Baseline {
			continue
		}
		p := float64(k.Current-k.Baseline) / float64(k.Target-k.Baseline)
		total += weight * max(0, min(1, p))
	}
	return total / weights
}

func (m *Module) changeGoalOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in PlanningGoalChange) (PlanningRecord, error) {
	contributorChange := in.Action == "ASSIGN_CONTRIBUTOR" || in.Action == "REMOVE_CONTRIBUTOR"
	if contributorChange {
		if tx == nil || tx.Statement == nil || in.PrincipalID <= 0 {
			return PlanningRecord{}, ErrInvalid
		}
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			return PlanningRecord{}, ErrInvalid
		}
		if m.principals.LockActiveRow == nil {
			return PlanningRecord{}, ErrUnavailable
		}
		ids := []guid.ID{authn.PrincipalIDFromContext(ctx), in.PrincipalID}
		slices.Sort(ids)
		for _, principal := range slices.Compact(ids) {
			if _, _, e := m.principals.LockActiveRow(ctx, tx, principal); e != nil {
				return PlanningRecord{}, e
			}
		}
	}
	r, err := m.readOn(ctx, tx, tenant, id, "goal")
	if err != nil {
		return r, err
	}
	human := in.Action == "ACTIVATE" || in.Action == "CLOSE" || contributorChange
	var scopeGoal PlanningGoalInput
	if err = json.Unmarshal(r.Content, &scopeGoal); err != nil {
		return PlanningRecord{}, err
	}
	permission := "goal_manage"
	if scopeGoal.Level == "MEMBER" {
		permission = "goal_member_manage"
	} else {
		human = true
	}
	kind, err := m.authorize(ctx, tx, tenant, r.ProjectID, r.PrincipalID, permission, human)
	if err != nil {
		return PlanningRecord{}, err
	}
	q := query.BindGoals(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return PlanningRecord{}, err
	}
	if row.Version != in.Version || row.ContentHash != in.ExpectedContentHash {
		return PlanningRecord{}, ErrConflict
	}
	if !slices.Contains([]string{"DRAFT", "ACTIVE", "AT_RISK"}, row.State) {
		return PlanningRecord{}, ErrConflict
	}
	var g PlanningGoalInput
	if err = json.Unmarshal([]byte(row.Content), &g); err != nil {
		return PlanningRecord{}, err
	}
	if kind == "AGENT" && (g.Level != "MEMBER" || r.PrincipalID != authn.PrincipalIDFromContext(ctx)) {
		return PlanningRecord{}, ErrForbidden
	}
	switch in.Action {
	case "ASSIGN_CONTRIBUTOR", "REMOVE_CONTRIBUTOR":
		if err = m.changeContributorOn(ctx, tx, tenant, row, g, in); err != nil {
			return PlanningRecord{}, err
		}
	case "EDIT":
		if row.State != "DRAFT" || in.Content == nil {
			return PlanningRecord{}, ErrConflict
		}
		if err = m.validateGoal(ctx, tx, tenant, PlanningWriteInput{ProjectID: r.ProjectID, PrincipalID: r.PrincipalID, PeriodKind: r.PeriodKind, PeriodStart: r.PeriodStart, Goal: in.Content}); err != nil {
			return PlanningRecord{}, err
		}
		g = *in.Content
	case "ACTIVATE":
		if row.State != "DRAFT" {
			return PlanningRecord{}, ErrConflict
		}
		if err = m.validateGoal(ctx, tx, tenant, PlanningWriteInput{ProjectID: r.ProjectID, PrincipalID: r.PrincipalID, PeriodKind: r.PeriodKind, PeriodStart: r.PeriodStart, Goal: &g}); err != nil {
			return PlanningRecord{}, err
		}
		r.State = "ACTIVE"
	case "CLOSE":
		if !slices.Contains([]string{"ACHIEVED", "MISSED", goalCancelled}, in.Result) {
			return PlanningRecord{}, ErrInvalid
		}
		if in.Result == goalCancelled {
			if !validText(in.Reason, 2000) {
				return PlanningRecord{}, ErrInvalid
			}
		} else {
			_, end, e := period(r.PeriodKind, r.PeriodStart)
			if e != nil {
				return PlanningRecord{}, e
			}
			if row.State == "DRAFT" || (m.now().Before(end) && goalProgress(g) < 1) {
				return PlanningRecord{}, ErrConflict
			}
		}
		r.State = in.Result
	case "MANUAL":
		if utf8.RuneCountInString(in.Evidence) > 2000 {
			return PlanningRecord{}, ErrInvalid
		}
		idx := slices.IndexFunc(g.KeyResults, func(k PlanningKeyResult) bool { return k.Key == in.Key })
		if idx < 0 || g.KeyResults[idx].Source != "MANUAL" || in.Current < 0 {
			return PlanningRecord{}, ErrInvalid
		}
		if kind == "AGENT" && !validText(in.Evidence, 2000) {
			return PlanningRecord{}, ErrInvalid
		}
		g.KeyResults[idx].Current = in.Current
		g.KeyResults[idx].Evidence = strings.TrimSpace(in.Evidence)
	case "REFRESH":
		if row.State == "DRAFT" {
			return PlanningRecord{}, ErrConflict
		}
		if m.facts.ReadOn == nil || r.ProjectID <= 0 {
			return PlanningRecord{}, ErrUnavailable
		}
		start, end, e := period(r.PeriodKind, r.PeriodStart)
		if e != nil {
			return PlanningRecord{}, e
		}
		subject := guid.ID(0)
		if g.Level == "MEMBER" {
			subject = r.PrincipalID
		}
		facts, e := m.facts.ReadOn(ctx, tx, tenant, r.ProjectID, subject, start, end)
		if e != nil {
			return PlanningRecord{}, e
		}
		accepted := map[guid.ID]bool{}
		for _, w := range facts.Work {
			if w.State == "ACCEPTED" && w.AcceptedAt != nil && !w.AcceptedAt.Before(start) && w.AcceptedAt.Before(end) && (subject == 0 || w.AccountablePrincipalID == subject) {
				accepted[w.ID] = true
			}
		}
		for i, k := range g.KeyResults {
			if k.Source == "AUTO" {
				if k.Metric != "ACCEPTED_WORK_ITEMS" {
					return PlanningRecord{}, ErrUnavailable
				}
				g.KeyResults[i].Current = int64(len(accepted))
			}
		}
	default:
		return PlanningRecord{}, ErrInvalid
	}
	if in.Action == "REFRESH" {
		settings, err := m.readSettingsOn(ctx, tx, tenant)
		if err != nil {
			return PlanningRecord{}, err
		}
		start, end, err := m.periodForOn(ctx, tx, tenant, r.PeriodKind, r.PeriodStart)
		if err != nil {
			return PlanningRecord{}, err
		}
		r.State, err = goalRiskState(g, r.State, settings, start, end, m.now())
		if err != nil {
			return PlanningRecord{}, err
		}
	}
	body, hash, err := hashJSON(g)
	if err != nil {
		return PlanningRecord{}, err
	}
	info, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(in.Version)).Updates(map[string]any{"content": body, "content_hash": hash, "state": r.State, "version": in.Version + 1, "updated_at": m.now()})
	if err != nil {
		return PlanningRecord{}, err
	}
	if info.RowsAffected != 1 {
		return PlanningRecord{}, ErrConflict
	}
	r.Content = json.RawMessage(body)
	r.ContentHash = hash
	r.Version++
	r.Progress = goalProgress(g)
	if contributorChange {
		return r, m.record(ctx, tx, tenant, r, kind, in.Action, in.Reason, map[string]any{"contributor_principal_id": in.PrincipalID.String(), "kr": in.Key, "action": in.Action})
	}
	return r, m.record(ctx, tx, tenant, r, kind, in.Action, in.Reason, map[string]any{"from_state": row.State})
}

package planning

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid            = errors.New("planning: invalid")
	ErrPlanLimit          = errors.Join(ErrInvalid, errors.New("planning: plan item limit"))
	ErrImprovementInvalid = errors.Join(ErrInvalid, errors.New("planning: invalid improvement plan"))
	ErrForbidden          = errors.New("planning: forbidden")
	ErrNotFound           = errors.New("planning: not found")
	ErrConflict           = errors.New("planning: conflict")
	ErrUnavailable        = errors.New("planning: unavailable")
)

type PlanningKeyResult struct {
	Weight   float64 `json:"weight,omitempty" minimum:"0" maximum:"10"`
	Key      string  `json:"key" minLength:"1" maxLength:"64"`
	Title    string  `json:"title" minLength:"1" maxLength:"200"`
	Source   string  `json:"source" enum:"MANUAL,AUTO"`
	Metric   string  `json:"metric,omitempty" enum:"ACCEPTED_WORK_ITEMS"`
	Baseline int64   `json:"baseline"`
	Target   int64   `json:"target"`
	Current  int64   `json:"current"`
	Evidence string  `json:"evidence,omitempty" maxLength:"2000"`
}
type PlanningGoalInput struct {
	Weight       float64             `json:"weight,omitempty" minimum:"0" maximum:"10"`
	Level        string              `json:"level" enum:"TEAM,PROJECT,MEMBER"`
	Title        string              `json:"title" minLength:"1" maxLength:"200"`
	ParentGoalID *guid.ID            `json:"parent_goal_id,omitempty"`
	ParentKR     string              `json:"parent_kr,omitempty" maxLength:"64"`
	KeyResults   []PlanningKeyResult `json:"key_results" minItems:"1" maxItems:"5"`
}
type PlanningPlanItem struct {
	ID                  guid.ID    `json:"id,omitempty"`
	CarriedFromItemID   *guid.ID   `json:"carried_from_item_id,omitempty"`
	Notes               string     `json:"notes,omitempty" maxLength:"2000"`
	EstimateHours       *float64   `json:"estimate_hours,omitempty" minimum:"0"`
	EstimateCostMicros  *string    `json:"estimate_cost_micros,omitempty" pattern:"^[0-9]+$" maxLength:"19" doc:"Exact optional cost in micros, encoded as a decimal string."`
	CarriedFromReportID *guid.ID   `json:"carried_from_report_id,omitempty"`
	CarriedFromKey      string     `json:"carried_from_key,omitempty" maxLength:"64"`
	CarriedFromHash     string     `json:"carried_from_hash,omitempty" maxLength:"64"`
	Weight              float64    `json:"weight,omitempty" minimum:"0" maximum:"10"`
	DueAt               *time.Time `json:"due_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	CompletionEvidence  string     `json:"completion_evidence,omitempty" maxLength:"2000"`
	Title               string     `json:"title" minLength:"1" maxLength:"200"`
	GoalID              guid.ID    `json:"goal_id"`
	KR                  string     `json:"kr" minLength:"1" maxLength:"64"`
	WorkItemID          *guid.ID   `json:"work_item_id,omitempty"`
	Category            string     `json:"category,omitempty" enum:"LEARNING,OPS,REVIEW,SUPPORT,OTHER"`
}
type PlanningPlanInput struct {
	Items       []PlanningPlanItem `json:"items" minItems:"1" doc:"At most 200 items; DAY plans at most 30. Domain validation returns 180413 for either limit."`
	Suggestions []PlanningPlanItem `json:"suggestions,omitempty" maxItems:"100" doc:"Optional KR suggestions; not committed plan items until explicitly added."`
}

// PlanningUncompleted preserves an explicit disposition and a typed plan seed.
// Key is stable within the report; prose is never interpreted as identifiers.
type PlanningUncompleted struct {
	DetailAIDrafted bool             `json:"detail_ai_drafted,omitempty"`
	DetailConfirmed bool             `json:"detail_confirmed,omitempty"`
	Key             string           `json:"key" minLength:"1" maxLength:"64"`
	Reason          string           `json:"reason" minLength:"1" maxLength:"64"`
	Detail          string           `json:"detail" minLength:"1" maxLength:"2000"`
	CarryOver       string           `json:"carry_over" enum:"CARRY_TO_NEXT,DROP,ESCALATE"`
	Item            PlanningPlanItem `json:"item"`
}
type PlanningCompletion struct {
	WorkItemID guid.ID `json:"work_item_id"`
	Note       string  `json:"note,omitempty" maxLength:"2000"`
}
type PlanningImprovement struct {
	ProposeMemory    bool     `json:"propose_memory,omitempty"`
	MemoryProjectID  *guid.ID `json:"memory_project_id,omitempty"`
	Action           string   `json:"action" minLength:"1" maxLength:"2000"`
	OwnerPrincipalID guid.ID  `json:"owner_principal_id"`
	DueDate          string   `json:"due_date" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
	KRID             *guid.ID `json:"kr_id,omitempty"`
}

func validImprovements(items []PlanningImprovement) bool {
	if len(items) > 10 {
		return false
	}
	for _, item := range items {
		if !validText(item.Action, 2000) || item.OwnerPrincipalID <= 0 || (item.KRID != nil && *item.KRID <= 0) || (item.MemoryProjectID != nil && *item.MemoryProjectID <= 0) {
			return false
		}
		if _, e := time.Parse("2006-01-02", item.DueDate); e != nil {
			return false
		}
	}
	return true
}

type PlanningReportInput struct {
	RetrospectiveSections  *PlanningRetrospective   `json:"retrospective_sections,omitempty"`
	RetrospectiveAIDrafted bool                     `json:"retrospective_ai_drafted,omitempty"`
	RetrospectiveConfirmed bool                     `json:"retrospective_confirmed,omitempty"`
	CalendarTimezone       string                   `json:"calendar_timezone,omitempty"`
	Scope                  string                   `json:"scope,omitempty" enum:"MEMBER,PROJECT,TEAM"`
	Children               []PlanningChildReportRef `json:"children,omitempty" maxItems:"500"`
	BoundaryStart          *time.Time               `json:"boundary_start,omitempty"`
	BoundaryEnd            *time.Time               `json:"boundary_end,omitempty"`
	Completed              []PlanningCompletion     `json:"completed" maxItems:"500"`
	Uncompleted            string                   `json:"uncompleted" maxLength:"20000"`
	UncompletedItems       []PlanningUncompleted    `json:"uncompleted_items,omitempty" maxItems:"100"`
	Retrospective          string                   `json:"retrospective" maxLength:"20000"`
	NextPlan               []PlanningPlanItem       `json:"next_plan" minItems:"1" maxItems:"100"`
	DeclaredConfidence     string                   `json:"declared_confidence" enum:"GREEN,YELLOW,RED"`
	StatusExplanation      string                   `json:"status_explanation,omitempty" maxLength:"2000"`
	ImprovementPlan        []PlanningImprovement    `json:"improvement_plan,omitempty" maxItems:"10"`
}
type PlanningRecord struct {
	Unconfirmed             bool                       `json:"unconfirmed,omitempty" doc:"This submitted plan has exceeded its confirmation deadline."`
	CanReopen               bool                       `json:"can_reopen,omitempty" doc:"Current Human authority to reopen this confirmed report; computed on detail reads."`
	ReopenedFromConfirmedBy *guid.ID                   `json:"reopened_from_confirmed_by,omitempty" doc:"Recorded confirmer on this immutable reopen decision receipt."`
	NeedsRecheck            bool                       `json:"needs_recheck"`
	LastCheckHash           string                     `json:"last_check_hash,omitempty"`
	DueAt                   *time.Time                 `json:"due_at,omitempty"`
	SubmittedLate           bool                       `json:"submitted_late"`
	AuthorID                guid.ID                    `json:"author_id,omitempty"`
	ID                      guid.ID                    `json:"id"`
	Kind                    string                     `json:"kind"`
	ProjectID               guid.ID                    `json:"project_id"`
	PrincipalID             guid.ID                    `json:"principal_id"`
	PeriodKind              string                     `json:"period_kind"`
	PeriodStart             string                     `json:"period_start"`
	State                   string                     `json:"state"`
	Content                 json.RawMessage            `json:"content"`
	ContentHash             string                     `json:"content_hash"`
	Facts                   *delivery.PlanningSnapshot `json:"facts,omitempty"`
	Version                 int                        `json:"version"`
	CreatedAt               time.Time                  `json:"created_at"`
	Progress                float64                    `json:"progress,omitempty"`
	ConfirmedBy             *guid.ID                   `json:"confirmed_by,omitempty"`
	ConfirmedAt             *time.Time                 `json:"confirmed_at,omitempty"`
}
type PlanningWriteInput struct {
	ProjectID   guid.ID              `json:"project_id"`
	PrincipalID guid.ID              `json:"principal_id"`
	PeriodKind  string               `json:"period_kind" enum:"DAY,WEEK,MONTH,QUARTER,YEAR"`
	PeriodStart string               `json:"period_start" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
	Goal        *PlanningGoalInput   `json:"goal,omitempty"`
	Plan        *PlanningPlanInput   `json:"plan,omitempty"`
	Report      *PlanningReportInput `json:"report,omitempty"`
}
type PlanningDecisionInput struct {
	Version             int    `json:"version" minimum:"1"`
	Action              string `json:"action" enum:"SUBMIT,CONFIRM,RETURN"`
	Reason              string `json:"reason,omitempty" maxLength:"2000"`
	ExpectedContentHash string `json:"expected_content_hash" minLength:"64" maxLength:"64"`
}
type PlanningIssue struct {
	Code       string   `json:"code"`
	Blocking   bool     `json:"blocking"`
	WorkItemID *guid.ID `json:"work_item_id,omitempty"`
}
type PlanningCheck struct {
	Progress    PlanningReportProgress    `json:"report_progress"`
	Issues      []PlanningIssue           `json:"issues"`
	Facts       delivery.PlanningSnapshot `json:"facts"`
	ContentHash string                    `json:"content_hash"`
	Result      string                    `json:"result"`
}

func period(kind, date string) (time.Time, time.Time, error) {
	start, e := time.Parse("2006-01-02", date)
	if e != nil {
		return start, start, ErrInvalid
	}
	var end time.Time
	switch kind {
	case "DAY":
		end = start.AddDate(0, 0, 1)
	case "WEEK":
		if start.Weekday() != time.Monday {
			return start, start, ErrInvalid
		}
		end = start.AddDate(0, 0, 7)
	case "MONTH":
		if start.Day() != 1 {
			return start, start, ErrInvalid
		}
		end = start.AddDate(0, 1, 0)
	case "QUARTER":
		if start.Day() != 1 || (int(start.Month())-1)%3 != 0 {
			return start, start, ErrInvalid
		}
		end = start.AddDate(0, 3, 0)
	case "YEAR":
		if start.Day() != 1 || start.Month() != time.January {
			return start, start, ErrInvalid
		}
		end = start.AddDate(1, 0, 0)
	default:
		return start, start, ErrInvalid
	}
	return start, end, nil
}
func hashJSON(v any) (string, string, error) {
	b, e := json.Marshal(v)
	h := sha256.Sum256(b)
	return string(b), hex.EncodeToString(h[:]), e
}
func validText(s string, n int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= n
}
func (m *Module) authorize(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, permission string, humanOnly bool) (string, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project < 0 || subject < 0 {
		return "", ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return "", ErrInvalid
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return "", ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.principals.ActiveHumanByID == nil || m.members == nil || m.policy == nil || m.entityPolicy == nil {
		return "", ErrUnavailable
	}
	p, active, e := m.principals.LockActiveRow(ctx, tx, actor)
	if e != nil {
		return "", e
	}
	if !active || p.ID != actor {
		return "", ErrForbidden
	}
	active, e = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if e != nil {
		return "", e
	}
	if !active {
		return "", ErrForbidden
	}
	h, human, e := m.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return "", e
	}
	human = human && h.ID == actor
	kind := "HUMAN"
	if !human {
		kind = "AGENT"
		if humanOnly {
			return "", ErrForbidden
		}
		if m.agents.ByPrincipalOn == nil {
			return "", ErrUnavailable
		}
		a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
		if err != nil {
			return "", err
		}
		if !found || a.TenantID != tenant || a.PrincipalID != actor || a.State != "ACTIVE" || (subject != actor && permission != "goal_read") {
			return "", ErrForbidden
		}
	}
	var c authz.DataConstraint
	if project > 0 {
		if m.project.AuthorizeOn == nil {
			return "", ErrUnavailable
		}
		if e = m.project.AuthorizeOn(ctx, tx, tenant, project, "project_read"); e != nil {
			return "", e
		}
		c, e = m.entityPolicy(ctx, tx, tenant, project, permission, actor)
	} else {
		c, e = m.policy(ctx, tx, tenant, permission, actor)
	}
	if e != nil {
		return "", e
	}
	allowed := c.AllowAll || (subject == actor && slices.Contains(c.OwnerIDs, actor.String()))
	if project > 0 {
		allowed = c.AllowsEntityRows(project) && (allowed || slices.Contains(c.ResourceIDs, project.String()))
	}
	if !allowed {
		return "", ErrForbidden
	}
	return kind, nil
}
func (m *Module) goalOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (*model.Goal, PlanningGoalInput, error) {
	var g PlanningGoalInput
	q := query.BindGoals(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, g, ErrNotFound
	}
	if e != nil {
		return nil, g, e
	}
	if r.ProjectID != 0 && r.ProjectID != project {
		return nil, g, ErrForbidden
	}
	if _, e = m.authorize(ctx, tx, tenant, r.ProjectID, r.PrincipalID, "goal_read", false); e != nil {
		return nil, g, e
	}
	if e = json.Unmarshal([]byte(r.Content), &g); e != nil {
		return nil, g, e
	}
	return r, g, nil
}
func (m *Module) validateGoal(ctx context.Context, tx *gorm.DB, tenant guid.ID, in PlanningWriteInput) error {
	g := in.Goal
	if g == nil || !validText(g.Title, 200) || len(g.KeyResults) < 1 || len(g.KeyResults) > 5 || !slices.Contains([]string{"TEAM", "PROJECT", "MEMBER"}, g.Level) || !slices.Contains([]string{"MONTH", "QUARTER", "YEAR"}, in.PeriodKind) {
		return ErrInvalid
	}
	if g.Level == "TEAM" {
		if in.ProjectID != 0 || in.PrincipalID <= 0 || g.ParentGoalID != nil || g.ParentKR != "" || !slices.Contains([]string{"QUARTER", "YEAR"}, in.PeriodKind) {
			return ErrInvalid
		}
	} else {
		if !slices.Contains([]string{"QUARTER", "MONTH"}, in.PeriodKind) {
			return ErrInvalid
		}
		if g.ParentGoalID == nil || *g.ParentGoalID <= 0 || g.ParentKR == "" || in.ProjectID <= 0 || in.PrincipalID <= 0 {
			return ErrInvalid
		}
		parent, p, e := m.goalOn(ctx, tx, tenant, in.ProjectID, *g.ParentGoalID)
		if e != nil {
			return e
		}
		if p.Level == "MEMBER" || (g.Level == "PROJECT" && p.Level != "TEAM") || (g.Level == "MEMBER" && p.Level != "PROJECT") || !slices.Contains([]string{"ACTIVE", "AT_RISK"}, parent.State) {
			return ErrInvalid
		}
		start, end, e := period(in.PeriodKind, in.PeriodStart)
		if e != nil {
			return e
		}
		parentStart, parentEnd, e := period(parent.PeriodKind, parent.PeriodStart)
		if e != nil {
			return e
		}
		if start.Before(parentStart) || end.After(parentEnd) {
			return ErrInvalid
		}
		found := false
		for _, k := range p.KeyResults {
			found = found || k.Key == g.ParentKR
		}
		if !found {
			return ErrInvalid
		}
	}
	seen := map[string]bool{}
	if !validProgressWeight(g.Weight) {
		return ErrInvalid
	}
	for _, k := range g.KeyResults {
		if !validProgressWeight(k.Weight) {
			return ErrInvalid
		}
		if !validText(k.Key, 64) || !validText(k.Title, 200) || seen[k.Key] || k.Target == k.Baseline || k.Baseline < 0 || k.Target < 0 || k.Current < 0 || !slices.Contains([]string{"AUTO", "MANUAL"}, k.Source) {
			return ErrInvalid
		}
		seen[k.Key] = true
		if k.Source == "AUTO" && (k.Metric != "ACCEPTED_WORK_ITEMS" || k.Current != k.Baseline) {
			return ErrInvalid
		}
		if k.Source == "MANUAL" && k.Current != k.Baseline && !validText(k.Evidence, 2000) {
			if m.principals.ActiveHumanByID == nil {
				return ErrUnavailable
			}
			_, human, e := m.principals.ActiveHumanByID(ctx, tx, authn.PrincipalIDFromContext(ctx))
			if e != nil {
				return e
			}
			if !human {
				return ErrInvalid
			}
		}
	}
	return nil
}

func validProgressWeight(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 10
}
func (m *Module) validateItems(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, items []PlanningPlanItem) error {
	if len(items) < 1 || len(items) > 200 {
		return ErrInvalid
	}
	for _, i := range items {
		if !validPlanAnnotations(i) {
			return ErrInvalid
		}
		if !validProgressWeight(i.Weight) || (i.DueAt != nil && i.DueAt.IsZero()) || (i.CompletedAt != nil && (i.CompletedAt.IsZero() || i.CompletedAt.After(m.now()) || !validText(i.CompletionEvidence, 2000))) || (i.CompletedAt == nil && i.CompletionEvidence != "") || (i.WorkItemID != nil && (i.CompletedAt != nil || i.CompletionEvidence != "")) {
			return ErrInvalid
		}
		if !validText(i.Title, 200) || i.GoalID <= 0 || i.KR == "" {
			return ErrInvalid
		}
		r, g, e := m.goalOn(ctx, tx, tenant, project, i.GoalID)
		if e != nil {
			return e
		}
		if !slices.Contains([]string{"ACTIVE", "AT_RISK"}, r.State) {
			return ErrForbidden
		}
		found := false
		for _, k := range g.KeyResults {
			found = found || k.Key == i.KR
		}
		if !found {
			return ErrInvalid
		}
		contributor, e := m.contributesOn(ctx, tx, tenant, r, g, subject, i.KR)
		if e != nil {
			return e
		}
		if !contributor {
			return ErrForbidden
		}
		if i.WorkItemID == nil {
			if !slices.Contains([]string{"LEARNING", "OPS", "REVIEW", "SUPPORT", "OTHER"}, i.Category) {
				return ErrInvalid
			}
		} else if *i.WorkItemID <= 0 || i.Category != "" {
			return ErrInvalid
		}
	}
	// A linked work item must be a real work item owned by this report subject.
	// Read once for the whole plan rather than probing each ID separately.
	linked := false
	for _, i := range items {
		linked = linked || i.WorkItemID != nil
	}
	if linked {
		if m.facts.ReadOn == nil {
			return ErrUnavailable
		}
		facts, err := m.facts.ReadOn(ctx, tx, tenant, project, subject, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), m.now().Add(time.Millisecond))
		if err != nil {
			return err
		}
		for _, i := range items {
			if i.WorkItemID != nil {
				found := false
				for _, f := range facts.Work {
					found = found || (f.ID == *i.WorkItemID && f.AccountablePrincipalID == subject)
				}
				if !found {
					return ErrInvalid
				}
			}
		}
	}
	return nil
}
func (m *Module) createOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, in PlanningWriteInput) (PlanningRecord, error) {
	out := PlanningRecord{}
	start, end, e := m.periodForOn(ctx, tx, tenant, in.PeriodKind, in.PeriodStart)
	if e != nil {
		return out, e
	}
	if in.PrincipalID <= 0 || in.ProjectID < 0 {
		return out, ErrInvalid
	}
	if kind == "goal" {
		if m.principals.LockActiveRow == nil || m.members == nil {
			return out, ErrUnavailable
		}
		locks := slices.Compact(slices.Sorted(slices.Values([]guid.ID{authn.PrincipalIDFromContext(ctx), in.PrincipalID})))
		for _, id := range locks {
			_, active, err := m.principals.LockActiveRow(ctx, tx, id)
			if err != nil {
				return out, err
			}
			if !active {
				return out, ErrForbidden
			}
			active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, id)
			if err != nil {
				return out, err
			}
			if !active {
				return out, ErrForbidden
			}
		}
	}
	permission := kind + "_submit"
	if kind == "goal" {
		permission = "goal_manage"
		if in.Goal != nil && in.Goal.Level == "MEMBER" {
			permission = "goal_member_manage"
		}
	}
	actorKind, e := m.authorize(ctx, tx, tenant, in.ProjectID, in.PrincipalID, permission, false)
	if e != nil {
		return out, e
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if kind != "goal" && in.PrincipalID != actor {
		return out, ErrForbidden
	}
	var payload any
	facts := delivery.PlanningSnapshot{}
	switch kind {
	case "goal":
		if actorKind == "AGENT" && in.Goal != nil && in.Goal.Level != "MEMBER" {
			return out, ErrForbidden
		}
		if e = m.validateGoal(ctx, tx, tenant, in); e != nil {
			return out, e
		}
		payload = in.Goal
	case "plan":
		if in.Plan == nil || in.Goal != nil || in.Report != nil {
			return out, ErrInvalid
		}
		if e = validatePlanLimit(in.PeriodKind, in.Plan.Items); e != nil {
			return out, e
		}
		if e = m.assignPlanItemIDs(in.Plan.Items, nil); e != nil {
			return out, e
		}
		if e = m.validatePlanItemsOn(ctx, tx, tenant, in.ProjectID, in.PrincipalID, in.Plan.Items); e != nil {
			return out, e
		}
		if e = validatePlanSchedule(in.Plan.Items, start, end); e != nil {
			return out, e
		}
		if e = m.validatePlanCarryPeriodOn(ctx, tx, tenant, in.PeriodKind, in.PeriodStart, in.Plan.Items); e != nil {
			return out, e
		}
		in.Plan.Suggestions = nil
		payload = in.Plan
	case "report":
		if in.Report != nil || in.Goal != nil || in.Plan != nil || in.ProjectID <= 0 {
			return out, ErrInvalid
		}
		if m.facts.ReadOn == nil {
			return out, ErrUnavailable
		}
		facts, e = m.snapshotOn(ctx, tx, tenant, in.ProjectID, in.PrincipalID, start, end)
		if e != nil {
			return out, e
		}
		settings, err := m.readSettingsOn(ctx, tx, tenant)
		if err != nil {
			return out, err
		}
		r := PlanningReportInput{CalendarTimezone: settings.Timezone, BoundaryStart: &start, BoundaryEnd: &end, Scope: "MEMBER", Completed: []PlanningCompletion{}, NextPlan: []PlanningPlanItem{}}
		for _, f := range facts.Work {
			if f.State == "ACCEPTED" && f.AcceptedAt != nil && !f.AcceptedAt.Before(start) && f.AcceptedAt.Before(end) {
				r.Completed = append(r.Completed, PlanningCompletion{WorkItemID: f.ID})
			}
		}
		if e := m.reportLinksOn(ctx, tx, tenant, &model.Report{ProjectID: in.ProjectID, PrincipalID: in.PrincipalID, PeriodKind: in.PeriodKind, PeriodStart: in.PeriodStart}, &r); e != nil {
			return out, e
		}
		payload = r
	default:
		return out, ErrInvalid
	}
	if m.ids == nil || m.audit == nil || m.events == nil {
		return out, ErrUnavailable
	}
	id, e := m.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	body, hash, e := hashJSON(payload)
	if e != nil {
		return out, e
	}
	now := m.now()
	state := "DRAFT"
	if kind == "goal" {
		state = "DRAFT"
	}
	out = PlanningRecord{ID: guid.ID(id), Kind: kind, ProjectID: in.ProjectID, PrincipalID: in.PrincipalID, PeriodKind: in.PeriodKind, PeriodStart: in.PeriodStart, State: state, Content: json.RawMessage(body), ContentHash: hash, Version: 1, CreatedAt: now}
	switch kind {
	case "goal":
		e = query.BindGoals(tx, m.names).WithContext(ctx).Create(&model.Goal{TenantID: tenant, ID: out.ID, ProjectID: out.ProjectID, PrincipalID: out.PrincipalID, PeriodKind: out.PeriodKind, PeriodStart: out.PeriodStart, State: state, Content: body, ContentHash: hash, Version: 1, CreatedBy: actor, CreatedAt: now, UpdatedAt: now})
	case "plan":
		e = query.BindPlans(tx, m.names).WithContext(ctx).Create(&model.Plan{TenantID: tenant, ID: out.ID, ProjectID: out.ProjectID, PrincipalID: out.PrincipalID, PeriodKind: out.PeriodKind, PeriodStart: out.PeriodStart, State: state, Content: body, ContentHash: hash, Version: 1, CreatedBy: actor, CreatedAt: now, UpdatedAt: now})
	case "report":
		out.Facts = &facts
		b, _, err := hashJSON(facts)
		if err != nil {
			return PlanningRecord{}, err
		}
		reportRow := &model.Report{TenantID: tenant, ID: out.ID, ProjectID: out.ProjectID, PrincipalID: out.PrincipalID, PeriodKind: out.PeriodKind, PeriodStart: out.PeriodStart, State: state, Content: body, ContentHash: hash, Facts: b, Version: 1, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
		if e = m.initializeReportDueOn(ctx, tx, tenant, reportRow); e != nil {
			return PlanningRecord{}, e
		}
		out.DueAt = reportRow.DueAt
		e = query.BindReports(tx, m.names).WithContext(ctx).Create(reportRow)
	}
	if e != nil {
		return PlanningRecord{}, e
	}
	return out, m.record(ctx, tx, tenant, out, actorKind, "CREATE", "")
}
func (m *Module) record(ctx context.Context, tx *gorm.DB, tenant guid.ID, r PlanningRecord, actorKind, action, reason string, details ...map[string]any) error {
	if err := m.appendGoalSnapshotOn(ctx, tx, tenant, r); err != nil {
		return err
	}
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	ev := audit.NewDomainEvent(ctx, tenant, "planning.changed", "planning_record", r.ID)
	if r.Kind == "report" && action == "REOPEN" {
		ev.EventType = "planning.report_reopened"
	}
	permission := r.Kind + "_submit"
	if r.Kind == "goal" {
		permission = "goal_manage"
		var goal PlanningGoalInput
		if json.Unmarshal(r.Content, &goal) == nil && goal.Level == "MEMBER" {
			permission = "goal_member_manage"
		}
	}
	if action == "CONFIRM" || action == "RETURN" || action == "REOPEN" {
		permission = r.Kind + "_confirm"
	}
	if action == "SUBMIT" && r.Kind == "report" {
		permission = "report_submit"
	}
	authority := "IAM:" + permission
	if actorKind == "SYSTEM" && action == "RECHECK" {
		authority = "subscriber:planning.recheck_on_change"
	}
	if actorKind == "SYSTEM" && r.Kind == "plan" && action == "UNCONFIRMED" {
		authority = "scheduled:planning.report_overdue"
	}
	if actorKind == "SYSTEM" && r.Kind == "goal" && action == "REFRESH" {
		authority = "scheduled:planning.goal_refresh"
	}
	ev.Detail["authority_source"] = authority
	ev.Detail["actor_kind"] = actorKind
	if action == "RETURN" || action == "REOPEN" || action == "CLOSE" {
		ev.Detail["reason"] = reason
	}
	ev.Detail["kind"] = r.Kind
	ev.Detail["state"] = r.State
	ev.Detail["content_hash"] = r.ContentHash
	data := map[string]any{"kind": r.Kind, "state": r.State, "action": action, "content_hash": r.ContentHash, "reason": reason}
	for _, detail := range details {
		for key, value := range detail {
			ev.Detail[key] = value
			data[key] = value
		}
	}
	if e := m.audit(ctx, tx, ev); e != nil {
		return e
	}
	_, e := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: r.ProjectID, Type: "planning.changed", Actor: events.Actor{Kind: actorKind, ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: authority}, Subject: events.Subject{Kind: "planning_record", ID: r.ID}, SubjectVersion: int64(r.Version), Data: data})
	return e
}
func (m *Module) reportOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, permission string, humanOnly bool) (*model.Report, string, error) {
	q := query.BindReports(tx, m.names)
	r, e := q.WithContext(ctx).Select(q.ID, q.ProjectID, q.PrincipalID).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, "", ErrNotFound
	}
	if e != nil {
		return nil, "", e
	}
	kind, e := m.authorize(ctx, tx, tenant, r.ProjectID, r.PrincipalID, permission, humanOnly)
	if e != nil {
		return nil, "", e
	}
	r, e = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	return r, kind, e
}
func reportView(r *model.Report) (PlanningRecord, error) {
	var facts delivery.PlanningSnapshot
	e := json.Unmarshal([]byte(r.Facts), &facts)
	return PlanningRecord{NeedsRecheck: r.NeedsRecheck, LastCheckHash: r.LastCheckHash, DueAt: r.DueAt, SubmittedLate: r.SubmittedLate, AuthorID: reportAuthor(r), ID: r.ID, Kind: "report", ProjectID: r.ProjectID, PrincipalID: r.PrincipalID, PeriodKind: r.PeriodKind, PeriodStart: r.PeriodStart, State: r.State, Content: json.RawMessage(r.Content), ContentHash: r.ContentHash, Facts: &facts, Version: r.Version, CreatedAt: r.CreatedAt, ConfirmedBy: r.ConfirmedBy, ConfirmedAt: r.ConfirmedAt}, e
}
func (m *Module) checkReport(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Report) (PlanningCheck, error) {
	out := PlanningCheck{Issues: []PlanningIssue{}, ContentHash: r.ContentHash, Result: "PASS"}
	var body PlanningReportInput
	if e := json.Unmarshal([]byte(r.Content), &body); e != nil {
		return out, e
	}
	start, end, e := reportPeriod(r)
	if e != nil {
		return out, e
	}
	if m.facts.ReadOn == nil {
		return out, ErrUnavailable
	}
	out.Facts, e = m.reportFactsOn(ctx, tx, tenant, r, start, end)
	if e != nil {
		return out, e
	}
	add := func(code string, blocking bool, id *guid.ID) {
		out.Issues = append(out.Issues, PlanningIssue{Code: code, Blocking: blocking, WorkItemID: id})
		if blocking {
			out.Result = "BLOCKED"
		} else if out.Result == "PASS" {
			out.Result = "PASS_WITH_FLAGS"
		}
	}
	if !reportSectionsReady(body) || (!validText(body.Uncompleted, 20000) && len(body.UncompletedItems) == 0) || !validUncompleted(body.UncompletedItems) || !validText(body.Retrospective, 20000) || len(body.NextPlan) == 0 || !slices.Contains([]string{"GREEN", "YELLOW", "RED"}, body.DeclaredConfidence) {
		add("B4_MISSING_SECTION", true, nil)
	}
	seen := map[guid.ID]bool{}
	for _, c := range body.Completed {
		backed := false
		for _, f := range out.Facts.Work {
			if f.ID == c.WorkItemID && f.State == "ACCEPTED" && f.AcceptedAt != nil && !f.AcceptedAt.Before(start) && f.AcceptedAt.Before(end) {
				backed = true
			}
		}
		if !backed || seen[c.WorkItemID] {
			id := c.WorkItemID
			add("B1_UNBACKED_COMPLETION", true, &id)
		}
		seen[c.WorkItemID] = true
	}
	if len(body.NextPlan) > 0 {
		if e = m.validateReportItems(ctx, tx, tenant, r, body.NextPlan); e != nil {
			if errors.Is(e, ErrInvalid) || errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) {
				add("B3_UNLINKED_PLAN_ITEM", true, nil)
			} else {
				return out, e
			}
		}
	}
	// Confirmation is advisory independently of the 72h reminder clock.
	confirmed, err := m.reportPlansConfirmedOn(ctx, tx, tenant, r, end)
	if err != nil {
		return out, err
	}
	if !confirmed {
		add("N2_PLAN_UNCONFIRMED", false, nil)
	}
	if len(out.Facts.MissingSources) > 0 {
		add("N3_KR_NO_EVIDENCE", false, nil)
	}
	if e := m.checkConfidenceDrop(ctx, tx, tenant, r, body, &out); e != nil {
		return out, e
	}
	if e := m.checkProgressOn(ctx, tx, tenant, r, body, &out); e != nil {
		return out, e
	}
	if e := m.checkReportChildrenOn(ctx, tx, tenant, r, &out); e != nil {
		return out, e
	}
	return out, nil
}

// checkConfidenceDrop compares the latest earlier report in this same reporting stream.
func (m *Module) checkConfidenceDrop(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Report, body PlanningReportInput, out *PlanningCheck) error {
	q := query.BindReports(tx, m.names)
	rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(r.ProjectID), q.PrincipalID.Eq(r.PrincipalID), q.PeriodKind.Eq(r.PeriodKind), q.PeriodStart.Lt(r.PeriodStart)).Order(q.PeriodStart.Desc(), q.ID.Desc()).Find()
	if e != nil {
		return e
	}
	rank := map[string]int{"GREEN": 3, "YELLOW": 2, "RED": 1}
	for _, row := range rows {
		var previous PlanningReportInput
		if e := json.Unmarshal([]byte(row.Content), &previous); e != nil {
			return e
		}
		if previous.Scope != body.Scope {
			continue
		}
		if rank[body.DeclaredConfidence] > 0 && rank[body.DeclaredConfidence] < rank[previous.DeclaredConfidence] && (len(body.ImprovementPlan) == 0 || !validImprovements(body.ImprovementPlan)) {
			out.Issues = append(out.Issues, PlanningIssue{Code: "B7_CONFIDENCE_DROP_NO_PLAN", Blocking: true})
			out.Result = "BLOCKED"
		}
		break
	}
	return nil
}

func (m *Module) editReportOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, in PlanningReportInput) (PlanningRecord, error) {
	r, kind, e := m.reportOn(ctx, tx, tenant, id, "report_submit", false)
	if e != nil {
		return PlanningRecord{}, e
	}
	adopting := r.CreatedBy == 0 && r.PrincipalID == 0 && r.State == "DRAFT"
	if !adopting && reportAuthor(r) != authn.PrincipalIDFromContext(ctx) {
		return PlanningRecord{}, ErrForbidden
	}
	if r.Version != version || !slices.Contains([]string{"DRAFT", "RETURNED", "BLOCKED", "READY", "OVERDUE"}, r.State) {
		return PlanningRecord{}, ErrConflict
	}
	if !validImprovements(in.ImprovementPlan) {
		return PlanningRecord{}, ErrImprovementInvalid
	}
	if !validUncompleted(in.UncompletedItems) {
		return PlanningRecord{}, ErrInvalid
	}
	if len(in.Completed) > 500 || len(in.NextPlan) > 100 || utf8.RuneCountInString(in.Uncompleted) > 20000 || utf8.RuneCountInString(in.Retrospective) > 20000 || !utf8.ValidString(in.StatusExplanation) || utf8.RuneCountInString(in.StatusExplanation) > 2000 {
		return PlanningRecord{}, ErrInvalid
	}
	var previous PlanningReportInput
	if e := json.Unmarshal([]byte(r.Content), &previous); e != nil {
		return PlanningRecord{}, e
	}
	if err := normalizeReportSections(previous, &in); err != nil {
		return PlanningRecord{}, err
	}
	knownItems := []PlanningPlanItem{}
	for _, unfinished := range previous.UncompletedItems {
		knownItems = append(knownItems, unfinished.Item)
	}
	plans := query.BindPlans(tx, m.names)
	currentPlans, e := plans.WithContext(ctx).Where(plans.TenantID.Eq(tenant), plans.PrincipalID.Eq(r.PrincipalID), plans.PeriodKind.Eq(r.PeriodKind), plans.PeriodStart.Eq(r.PeriodStart)).Find()
	if e != nil {
		return PlanningRecord{}, e
	}
	for _, plan := range currentPlans {
		var content PlanningPlanInput
		if e := json.Unmarshal([]byte(plan.Content), &content); e != nil {
			return PlanningRecord{}, e
		}
		knownItems = append(knownItems, content.Items...)
	}
	seeds := make([]PlanningPlanItem, len(in.UncompletedItems))
	for i := range in.UncompletedItems {
		seeds[i] = in.UncompletedItems[i].Item
	}
	if e := m.assignPlanItemIDs(seeds, knownItems); e != nil {
		return PlanningRecord{}, e
	}
	for i := range seeds {
		in.UncompletedItems[i].Item = seeds[i]
	}
	in.Scope, in.Children, in.BoundaryStart, in.BoundaryEnd, in.CalendarTimezone = previous.Scope, previous.Children, previous.BoundaryStart, previous.BoundaryEnd, previous.CalendarTimezone
	body, hash, e := hashJSON(in)
	if e != nil || len(body) > 1048576 {
		return PlanningRecord{}, ErrInvalid
	}
	q := query.BindReports(tx, m.names)
	if adopting {
		if kind != "HUMAN" {
			return PlanningRecord{}, ErrForbidden
		}
		r.CreatedBy = authn.PrincipalIDFromContext(ctx)
	}
	info, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Updates(map[string]any{"content": body, "content_hash": hash, "state": "DRAFT", "last_check_hash": "", "created_by": r.CreatedBy, "version": version + 1, "updated_at": m.now()})
	if e != nil {
		return PlanningRecord{}, e
	}
	if info.RowsAffected != 1 {
		return PlanningRecord{}, ErrConflict
	}
	r.Content = body
	r.ContentHash = hash
	r.State = "DRAFT"
	r.Version++
	v, e := reportView(r)
	if e != nil {
		return v, e
	}
	action := "EDIT"
	if adopting {
		action = "ADOPT"
	}
	return v, m.record(ctx, tx, tenant, v, kind, action, "")
}
func (m *Module) decideReportOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in PlanningDecisionInput) (PlanningRecord, error) {
	permission := "report_submit"
	if in.Action != "SUBMIT" {
		permission = "report_confirm"
	}
	r, kind, e := m.reportOn(ctx, tx, tenant, id, permission, in.Action != "SUBMIT")
	if e != nil {
		return PlanningRecord{}, e
	}
	if in.Version != r.Version || in.ExpectedContentHash != r.ContentHash {
		return PlanningRecord{}, ErrConflict
	}
	actor := authn.PrincipalIDFromContext(ctx)
	var reopenedFrom *guid.ID
	switch in.Action {
	case "REOPEN":
		if r.State != "CONFIRMED" {
			return PlanningRecord{}, ErrConflict
		}
		if e := m.authorizeReportReopenOn(ctx, tx, r, r.ConfirmedBy); e != nil {
			return PlanningRecord{}, e
		}
		if !validText(in.Reason, 2000) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 4 {
			return PlanningRecord{}, ErrInvalid
		}
		r.State = "RETURNED"
		reopenedFrom = r.ConfirmedBy
		r.ConfirmedBy, r.ConfirmedAt = nil, nil
	case "SUBMIT":
		if r.NeedsRecheck {
			return PlanningRecord{}, ErrStaleCheck
		}
		if actor != reportAuthor(r) {
			return PlanningRecord{}, ErrForbidden
		}
		if !slices.Contains([]string{"DRAFT", "RETURNED", "BLOCKED", "READY", "OVERDUE"}, r.State) {
			return PlanningRecord{}, ErrConflict
		}
		check, e := m.checkReport(ctx, tx, tenant, r)
		if e != nil {
			return PlanningRecord{}, e
		}
		if check.Result == "BLOCKED" {
			return PlanningRecord{}, ErrConflict
		}
		b, _, e := hashJSON(check.Facts)
		if e != nil {
			return PlanningRecord{}, e
		}
		r.Facts = b
		r.State = "SUBMITTED"
		r.LastCheckHash = r.ContentHash
		r.SubmittedLate = r.DueAt != nil && m.now().After(*r.DueAt)
		if e := m.recordReportSubmissionOn(ctx, tx, r); e != nil {
			return PlanningRecord{}, e
		}
	case "CONFIRM", "RETURN":
		confirmer, err := m.reportConfirmerOn(ctx, tx, r)
		if err != nil {
			return PlanningRecord{}, err
		}
		if confirmer != actor {
			return PlanningRecord{}, ErrForbidden
		}
		if actor == reportAuthor(r) {
			return PlanningRecord{}, ErrForbidden
		}
		if r.State != "SUBMITTED" {
			return PlanningRecord{}, ErrConflict
		}
		if in.Action == "RETURN" {
			if !validText(in.Reason, 2000) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 4 {
				return PlanningRecord{}, ErrInvalid
			}
			r.State = "RETURNED"
		} else {
			if r.NeedsRecheck {
				return PlanningRecord{}, ErrStaleCheck
			}
			check, e := m.checkReport(ctx, tx, tenant, r)
			if e != nil {
				return PlanningRecord{}, e
			}
			if check.Result == "BLOCKED" {
				return PlanningRecord{}, ErrConflict
			}
			r.State = "CONFIRMED"
			now := m.now()
			r.ConfirmedBy = &actor
			r.ConfirmedAt = &now
			if e := m.proposeReportExperiencesOn(ctx, tx, r); e != nil {
				return PlanningRecord{}, e
			}
		}
	default:
		return PlanningRecord{}, ErrInvalid
	}
	q := query.BindReports(tx, m.names)
	info, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(r.Version)).Updates(map[string]any{"state": r.State, "facts": r.Facts, "last_check_hash": r.LastCheckHash, "version": r.Version + 1, "confirmed_by": r.ConfirmedBy, "confirmed_at": r.ConfirmedAt, "submitted_late": r.SubmittedLate, "updated_at": m.now()})
	if e != nil {
		return PlanningRecord{}, e
	}
	if info.RowsAffected != 1 {
		return PlanningRecord{}, ErrConflict
	}
	r.Version++
	v, e := reportView(r)
	if e != nil {
		return v, e
	}
	// A successful confirmation has just established this exact Human and
	// current authority; expose its correction action without a page reload.
	v.CanReopen = in.Action == "CONFIRM"
	v.ReopenedFromConfirmedBy = reopenedFrom
	return v, m.record(ctx, tx, tenant, v, kind, in.Action, in.Reason)
}

func (m *Module) snapshotOn(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, start, end time.Time) (delivery.PlanningSnapshot, error) {
	if m.facts.ReadOn == nil {
		return delivery.PlanningSnapshot{}, ErrUnavailable
	}
	out, err := m.facts.ReadOn(ctx, tx, tenant, project, subject, start, end)
	if err != nil {
		return out, err
	}
	if subject == 0 {
		if err := m.hydrateReportQualityOn(ctx, tx, tenant, project, start, end, &out); err != nil {
			return out, err
		}
	} else if m.memberQuality != nil {
		scope, e := m.facts.ReadOn(ctx, tx, tenant, project, 0, start, end)
		if e != nil {
			return out, e
		}
		if e = m.hydrateMemberQualityOn(ctx, tx, tenant, project, subject, start, end, scope, &out); e != nil {
			return out, e
		}
	}
	if !slices.Contains(out.MissingSources, "EXECUTIONS") {
		out.MissingSources = append(append([]string{}, out.MissingSources...), "EXECUTIONS")
	}
	if err := m.hydrateRuntimeCostsOn(ctx, tx, tenant, project, subject, start, end, &out); err != nil {
		return out, err
	}
	if m.executions.ReadOn == nil {
		return out, nil
	}
	ids := make([]guid.ID, len(out.Work))
	for i, w := range out.Work {
		ids[i] = w.ID
	}
	facts, err := m.executions.ReadOn(ctx, tx, tenant, project, ids, start, end)
	if errors.Is(err, execution.ErrForbidden) || errors.Is(err, execution.ErrUnavailable) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.ExecutionCreated = &facts.Created
	out.ExecutionStates = facts.States
	out.MissingSources = slices.DeleteFunc(out.MissingSources, func(s string) bool { return s == "EXECUTIONS" })
	return out, nil
}

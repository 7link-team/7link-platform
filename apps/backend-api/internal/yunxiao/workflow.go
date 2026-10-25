package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	yi18n "7link.com/platform/internal/yunxiao/i18n"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

type StateDefinition struct {
	Archived    bool   `json:"archived,omitempty"`
	MigrateTo   string `json:"migrate_to,omitempty" maxLength:"40"`
	Key         string `json:"key" minLength:"2" maxLength:"40"`
	DisplayName string `json:"display_name" minLength:"1" maxLength:"40"`
	Group       string `json:"group" enum:"TODO,IN_PROGRESS,VERIFYING,DONE,CANCELLED"` //nolint:misspell // Frozen protocol state.
	MapsTo      string `json:"maps_to"`
}
type TransitionCondition struct {
	Kind            string    `json:"kind" enum:"REQUIRED_FIELDS,ACTOR_SCOPE,RELATED_OBJECTS,EVIDENCE"`
	Fields          []string  `json:"fields,omitempty" maxItems:"12"`
	Rule            string    `json:"rule,omitempty"`
	PrincipalIDs    []guid.ID `json:"principal_ids,omitempty" maxItems:"50"`
	RoleKeys        []string  `json:"role_keys,omitempty" maxItems:"20"`
	WorkItemRoles   []string  `json:"work_item_roles,omitempty" maxItems:"4"`
	ActorKinds      []string  `json:"actor_kinds,omitempty" maxItems:"3"`
	Group           string    `json:"group,omitempty"`
	SeverityAtLeast string    `json:"severity_at_least,omitempty"`
	PipelineIDs     []guid.ID `json:"pipeline_ids,omitempty" maxItems:"10"`
}
type TransitionDefinition struct {
	From          string                `json:"from"`
	To            string                `json:"to"`
	Enabled       bool                  `json:"enabled"`
	ManualAllowed bool                  `json:"manual_allowed"`
	Conditions    []TransitionCondition `json:"conditions" maxItems:"12"`
}
type WorkflowInput struct {
	CarryOverMode string                 `json:"carry_over_mode,omitempty" enum:"NEXT_ITERATION,BACKLOG"`
	States        []StateDefinition      `json:"states" minItems:"1" maxItems:"64"`
	Transitions   []TransitionDefinition `json:"transitions" maxItems:"256"`
}
type WorkflowView struct {
	Source       string  `json:"source" enum:"SYSTEM,TEAM,PROJECT"`
	TeamVersion  int     `json:"team_version"`
	ID           guid.ID `json:"id"`
	WorkItemType string  `json:"work_item_type"`
	Version      int     `json:"version"`
	WorkflowInput
}

// WorkflowPreview compares effective configuration only. It never claims that
// existing work has migrated or that transition evidence has been evaluated.
type WorkflowPreview struct {
	Before                WorkflowView `json:"before"`
	After                 WorkflowView `json:"after"`
	ChangedStates         []string     `json:"changed_states"`
	ChangedTransitions    []string     `json:"changed_transitions"`
	ProjectsAffected      int          `json:"projects_affected"`
	ItemsInArchivedStates int          `json:"items_in_archived_states"`
	RuntimeImpact         string       `json:"runtime_impact" enum:"NOT_EVALUATED,EVALUATED"`
}

func (m *Module) previewWorkflowOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, kind string, version int, in WorkflowInput, remove bool) (WorkflowPreview, error) {
	out := WorkflowPreview{ChangedStates: []string{}, ChangedTransitions: []string{}, RuntimeImpact: "NOT_EVALUATED"}
	if err := m.workflowAuthorityOn(ctx, tx, tenant, project); err != nil {
		return out, err
	}
	if !slices.Contains([]string{"TASK", "BUG", "RISK", "INCIDENT"}, kind) {
		return out, ErrInvalid
	}
	if !remove {
		if err := validateWorkflow(kind, in); err != nil {
			return out, err
		}
	}
	projects := []guid.ID{project}
	if m.workflowStates.ProjectsOn != nil {
		if project == 0 {
			var err error
			projects, err = m.workflowStates.ProjectsOn(ctx, tx, tenant)
			if err != nil {
				return out, err
			}
		} else {
			// Fence project writers before taking configuration locks.
			if err := m.projects.AuthorizeOn(ctx, tx, tenant, project, "workflow_manage"); err != nil {
				return out, err
			}
		}
	}
	q := query.BindWorkflowConfigs(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemType.Eq(kind)).Take()
	fresh := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !fresh {
		return out, err
	}
	if (fresh && (version != 0 || remove)) || (!fresh && (version <= 0 || row.Version != version || (remove && row.Document == ""))) {
		return out, ErrConflict
	}
	out.Before, err = m.effectiveWorkflowOn(ctx, tx, tenant, project, kind)
	if err != nil {
		return out, err
	}
	base := defaultWorkflow(kind)
	if project > 0 {
		base, err = m.effectiveWorkflowOn(ctx, tx, tenant, 0, kind)
		if err != nil {
			return out, err
		}
		base.ID, base.Version = 0, 0
	}
	out.After = base
	if !remove {
		out.After = mergeWorkflow(base, WorkflowView{WorkflowInput: in})
		out.After.Source = "PROJECT"
		if project == 0 {
			out.After.Source = "TEAM"
		}
	}
	out.After.Version = 1
	if !fresh {
		out.After.ID, out.After.Version = row.ID, row.Version+1
	}
	if project == 0 {
		out.After.TeamVersion = out.After.Version
	}
	beforeStates, afterStates := map[string]StateDefinition{}, map[string]StateDefinition{}
	for _, s := range out.Before.States {
		beforeStates[s.Key] = s
	}
	for _, s := range out.After.States {
		afterStates[s.Key] = s
	}
	for key, value := range beforeStates {
		if other, ok := afterStates[key]; !ok || value != other {
			out.ChangedStates = append(out.ChangedStates, key)
		}
	}
	for key := range afterStates {
		if _, ok := beforeStates[key]; !ok {
			out.ChangedStates = append(out.ChangedStates, key)
		}
	}
	beforeCells, afterCells := map[string]TransitionDefinition{}, map[string]TransitionDefinition{}
	for _, cell := range out.Before.Transitions {
		beforeCells[cell.From+":"+cell.To] = cell
	}
	for _, cell := range out.After.Transitions {
		afterCells[cell.From+":"+cell.To] = cell
	}
	for key, value := range beforeCells {
		if other, ok := afterCells[key]; !ok || !sameTransition(value, other) {
			out.ChangedTransitions = append(out.ChangedTransitions, key)
		}
	}
	for key := range afterCells {
		if _, ok := beforeCells[key]; !ok {
			out.ChangedTransitions = append(out.ChangedTransitions, key)
		}
	}
	if err := m.workflowImpactOn(ctx, tx, tenant, project, kind, projects, &out, false); err != nil {
		return out, err
	}
	slices.Sort(out.ChangedStates)
	slices.Sort(out.ChangedTransitions)
	return out, nil
}

var stateKey = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)
var stateGroups = map[string][]string{
	"TODO":        {"DRAFT", "READY", "BLOCKED"},
	"IN_PROGRESS": {"CLAIMED", "PLAN_REVIEW", "IN_PROGRESS", "REWORK", "EXECUTION_FAILED", "STOPPED", "EXCEPTION"},
	"VERIFYING":   {"AGENT_CLAIMED_DONE", "VERIFYING", "INTERNAL_QA", "NEEDS_HUMAN_REVIEW"},
	"DONE":        {"ACCEPTED"}, "CANCELLED": {"CANCELLED"}, //nolint:misspell // Frozen protocol state.
}

func validateWorkflow(kind string, in WorkflowInput) error {
	if in.CarryOverMode != "" && in.CarryOverMode != "NEXT_ITERATION" && in.CarryOverMode != "BACKLOG" {
		return ErrInvalid
	}
	if !slices.Contains([]string{"TASK", "BUG", "RISK", "INCIDENT"}, kind) || len(in.States) == 0 || len(in.States) > 64 || len(in.Transitions) > 256 {
		return ErrInvalid
	}
	states := map[string]StateDefinition{}
	for _, s := range in.States {
		if !stateKey.MatchString(s.Key) || strings.TrimSpace(s.DisplayName) == "" || !textValid(s.DisplayName, 40) || !slices.Contains(stateGroups[s.Group], s.MapsTo) {
			return ErrInvalid
		}
		if _, ok := states[s.Key]; ok {
			return ErrInvalid
		}
		states[s.Key] = s
	}
	seen := map[string]bool{}
	for _, t := range in.Transitions {
		from, ok := states[t.From]
		if !ok {
			return ErrInvalid
		}
		to, ok := states[t.To]
		if !ok {
			return ErrInvalid
		}
		key := t.From + ":" + t.To
		if seen[key] || t.From == t.To || len(t.Conditions) > 12 {
			return ErrInvalid
		}
		seen[key] = true
		if err := defectFloor(kind, from, to, t); err != nil {
			return err
		}
		// Delivery explicitly supports a new result or client rejection after
		// acceptance. Configuration cannot invent any other terminal reopening.
		if t.Enabled && (from.MapsTo == "CANCELLED" || (from.MapsTo == "ACCEPTED" && to.MapsTo != "REWORK" && to.MapsTo != "NEEDS_HUMAN_REVIEW")) { //nolint:misspell // Frozen protocol state.
			return ErrInvalid
		}
		if t.Enabled && from.MapsTo == "DRAFT" && to.MapsTo == "ACCEPTED" {
			return ErrInvalid
		}
		for _, c := range t.Conditions {
			if !validCondition(c) {
				return ErrInvalid
			}
		}
	}
	return nil
}
func allIn(values, allowed []string) bool {
	for _, v := range values {
		if !slices.Contains(allowed, v) {
			return false
		}
	}
	return true
}
func validCondition(c TransitionCondition) bool {
	switch c.Kind {
	case "REQUIRED_FIELDS":
		return len(c.Fields) > 0 && len(c.Fields) <= 12 && allIn(c.Fields, []string{"iteration_id", "version_id", "fixed_version_id", "assignee", "estimate_minutes", "introduced_stage", "introduced_reason", "resolution", "root_cause", "module", "worklog_minutes", "acceptance_criteria"})
	case "ACTOR_SCOPE":
		if len(c.PrincipalIDs) > 50 || len(c.RoleKeys) > 20 || !allIn(c.ActorKinds, []string{"HUMAN", "AGENT", "SYSTEM"}) || !allIn(c.WorkItemRoles, []string{"ACCOUNTABLE", "EXECUTOR", "REVIEWER", "REPORTER"}) {
			return false
		}
		for _, id := range c.PrincipalIDs {
			if id <= 0 {
				return false
			}
		}
		for _, key := range c.RoleKeys {
			if key == "" || !textValid(key, 80) {
				return false
			}
		}
		return true
	case "RELATED_OBJECTS":
		return slices.Contains([]string{"ALL_CHILDREN_IN_GROUP", "NO_OPEN_BLOCKING_DEFECTS", "HAS_PARENT", "ALL_DEPENDENCIES_DONE", "LINKED_REVIEW_PASSED"}, c.Rule) && (c.Group == "" || stateGroups[c.Group] != nil) && (c.SeverityAtLeast == "" || slices.Contains([]string{"BLOCKER", "CRITICAL", "MAJOR", "MINOR"}, c.SeverityAtLeast))
	case "EVIDENCE":
		if !slices.Contains([]string{"EVERY_AC_HAS_PASS_EVIDENCE", "INTERNAL_QA_PASSED", "LINKED_MR_MERGED", "DEPLOYMENT_CONTAINS_MERGE", "TEST_PLAN_PASSED", "EXTERNAL_CI_PASSED"}, c.Rule) || len(c.PipelineIDs) > 10 {
			return false
		}
		for _, id := range c.PipelineIDs {
			if id <= 0 {
				return false
			}
		}
		return true
	}
	return false
}
func workflowView(row *model.WorkflowConfig) (WorkflowView, error) {
	out := WorkflowView{ID: row.ID, WorkItemType: row.WorkItemType, Version: row.Version}
	if row.Document == "" {
		out.States = []StateDefinition{}
		out.Transitions = []TransitionDefinition{}
		return out, nil
	}
	err := json.Unmarshal([]byte(row.Document), &out.WorkflowInput)
	return out, err
}
func (m *Module) saveWorkflowOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, kind string, version int, in WorkflowInput, remove bool) (WorkflowView, error) {
	var out WorkflowView
	if err := m.workflowAuthorityOn(ctx, tx, tenant, project); err != nil {
		return out, err
	}
	if !remove {
		if err := validateWorkflow(kind, in); err != nil {
			return out, err
		}
	}
	impact, err := m.previewWorkflowOn(ctx, tx, tenant, project, kind, version, in, remove)
	if err != nil {
		return out, err
	}
	projects := []guid.ID{project}
	if project == 0 && m.workflowStates.ProjectsOn != nil {
		projects, err = m.workflowStates.ProjectsOn(ctx, tx, tenant)
		if err != nil {
			return out, err
		}
	}
	if err = m.workflowImpactOn(ctx, tx, tenant, project, kind, projects, &impact, true); err != nil {
		return out, err
	}
	q := query.BindWorkflowConfigs(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemType.Eq(kind)).First()
	fresh := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !fresh {
		return out, err
	}
	if fresh {
		if version != 0 || remove {
			return out, ErrConflict
		}
		id, e := m.nextID()
		if e != nil {
			return out, e
		}
		row = &model.WorkflowConfig{ID: id, TenantID: tenant, ProjectID: project, WorkItemType: kind, Version: 1}
	} else {
		if version <= 0 || row.Version != version {
			return out, ErrConflict
		}
		row.Version++
	}
	if remove {
		if row.Document == "" {
			return out, ErrConflict
		}
		row.Document = ""
		row.UpdatedAt = m.now()
		row.UpdatedBy = authn.PrincipalIDFromContext(ctx)
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(version)).Select(q.Document, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(row)
		if e != nil {
			return out, e
		}
		if result.RowsAffected != 1 {
			return out, ErrConflict
		}
		out = WorkflowView{ID: row.ID, WorkItemType: kind, Version: row.Version}
	} else {
		// Persist only differing project cells so future team edits propagate to
		// every inherited cell, including when the editor sends the effective view.
		if project > 0 {
			base, e := m.effectiveWorkflowOn(ctx, tx, tenant, 0, kind)
			if e != nil {
				return out, e
			}
			cells := make([]TransitionDefinition, 0, len(in.Transitions))
			for _, cell := range in.Transitions {
				i := slices.IndexFunc(base.Transitions, func(t TransitionDefinition) bool { return t.From == cell.From && t.To == cell.To })
				if i >= 0 && sameTransition(base.Transitions[i], cell) {
					continue
				}
				cells = append(cells, cell)
			}
			states := make([]StateDefinition, 0, len(in.States))
			for _, state := range in.States {
				i := slices.IndexFunc(base.States, func(s StateDefinition) bool { return s.Key == state.Key })
				if i >= 0 && base.States[i] == state {
					continue
				}
				states = append(states, state)
			}
			in.States = states
			in.Transitions = cells
		}
		data, e := json.Marshal(in)
		if e != nil {
			return out, e
		}
		row.Document = string(data)
		row.UpdatedAt = m.now()
		row.UpdatedBy = authn.PrincipalIDFromContext(ctx)
		if fresh {
			err = q.WithContext(ctx).Create(row)
		} else {
			result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(version)).Select(q.Document, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(row)
			err = e
			if err == nil && result.RowsAffected != 1 {
				err = ErrConflict
			}
		}
		if err != nil {
			return out, err
		}
		out, err = workflowView(row)
		if err != nil {
			return out, err
		}
	}
	if err = m.workChangedOn(ctx, tx, tenant, project, row.ID, row.Version, "workflow.config_changed", false); err != nil {
		return out, err
	}
	return out, nil
}

func sameTransition(a, b TransitionDefinition) bool {
	return a.From == b.From && a.To == b.To && a.Enabled == b.Enabled && a.ManualAllowed == b.ManualAllowed && ((len(a.Conditions) == 0 && len(b.Conditions) == 0) || reflect.DeepEqual(a.Conditions, b.Conditions))
}

func (m *Module) workflowAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	if project == 0 {
		return m.tenantAuthorityOn(ctx, tx, tenant, "workflow_manage")
	}
	return m.projectAuthorityOn(ctx, tx, tenant, project, "workflow_manage")
}

// Defaults constrain no additional business edge: Delivery's command-specific
// state machine still decides legality. They are an editable policy overlay,
// never an alternate command for jumping to an arbitrary business state.
func defaultWorkflow(kind string) WorkflowView {
	out := WorkflowView{WorkItemType: kind, Source: "SYSTEM", WorkflowInput: WorkflowInput{States: []StateDefinition{}, Transitions: []TransitionDefinition{}}}
	for _, group := range []string{"TODO", "IN_PROGRESS", "VERIFYING", "DONE", "CANCELLED"} { //nolint:misspell // Frozen protocol state.
		for _, state := range stateGroups[group] {
			out.States = append(out.States, StateDefinition{Key: strings.ToLower(state), DisplayName: state, Group: group, MapsTo: state})
		}
	}
	for _, from := range out.States {
		for _, to := range out.States {
			if from.Key == to.Key || from.MapsTo == "CANCELLED" || (from.MapsTo == "ACCEPTED" && to.MapsTo != "REWORK" && to.MapsTo != "NEEDS_HUMAN_REVIEW") || (from.MapsTo == "DRAFT" && to.MapsTo == "ACCEPTED") { //nolint:misspell // Frozen protocol state.
				continue
			}
			cell := TransitionDefinition{From: from.Key, To: to.Key, Enabled: true, ManualAllowed: true, Conditions: []TransitionCondition{}}
			if kind == "BUG" || kind == "INCIDENT" {
				if to.MapsTo == "ACCEPTED" || ((from.MapsTo == "ACCEPTED" || from.MapsTo == "NEEDS_HUMAN_REVIEW") && to.MapsTo == "REWORK") {
					cell.Conditions = append(cell.Conditions, TransitionCondition{Kind: "ACTOR_SCOPE", ActorKinds: []string{"HUMAN"}})
				}
			}
			out.Transitions = append(out.Transitions, cell)
		}
	}
	return out
}

func mergeWorkflow(base WorkflowView, overlay WorkflowView) WorkflowView {
	if overlay.CarryOverMode != "" {
		base.CarryOverMode = overlay.CarryOverMode
	}
	for _, state := range overlay.States {
		i := slices.IndexFunc(base.States, func(s StateDefinition) bool { return s.Key == state.Key })
		if i < 0 {
			base.States = append(base.States, state)
		} else {
			base.States[i] = state
		}
	}
	for _, cell := range overlay.Transitions {
		i := slices.IndexFunc(base.Transitions, func(t TransitionDefinition) bool { return t.From == cell.From && t.To == cell.To })
		if i < 0 {
			base.Transitions = append(base.Transitions, cell)
		} else {
			base.Transitions[i] = cell
		}
	}
	return base
}

func (m *Module) effectiveWorkflowOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, kind string) (WorkflowView, error) {
	out := defaultWorkflow(kind)
	q := query.BindWorkflowConfigs(tx, m.names)
	for _, scope := range []guid.ID{0, project} {
		if scope == 0 && project == 0 && out.TeamVersion > 0 {
			continue
		}
		row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(scope), q.WorkItemType.Eq(kind)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			if project == 0 {
				break
			}
			continue
		}
		if e != nil {
			return out, e
		}
		if scope == 0 {
			out.TeamVersion = row.Version
		}
		if scope == project {
			out.ID = row.ID
			out.Version = row.Version
		}
		if row.Document != "" {
			v, e := workflowView(row)
			if e != nil {
				return out, e
			}
			out = mergeWorkflow(out, v)
			if scope == 0 {
				out.Source = "TEAM"
			} else {
				out.Source = "PROJECT"
			}
		}
		if project == 0 {
			break
		}
	}
	return out, nil
}

func (m *Module) selectWorkflowStateOn(ctx context.Context, tx *gorm.DB, request delivery.WorkflowTransition) (string, error) {
	if tx == nil || request.TenantID <= 0 || request.ProjectID <= 0 || request.ToKey == "" {
		return "", delivery.ErrInvalidArgument
	}
	doc, err := m.effectiveWorkflowOn(ctx, tx, request.TenantID, request.ProjectID, request.Type)
	if err != nil {
		return "", err
	}
	from, to := "", ""
	for _, state := range doc.States {
		if state.Key == request.FromKey {
			from = state.MapsTo
		}
		if state.Key == request.ToKey && !state.Archived {
			to = state.MapsTo
		}
	}
	if from == "" || from != request.From || to == "" || request.FromKey == request.ToKey {
		return "", delivery.ErrWorkPreparationState
	}
	if request.ActorKind == "AGENT" && to == "ACCEPTED" {
		return "", respx.ErrStatus(ctx, 403, 120403, yi18n.Forbidden, map[string]any{"reason_code": "AGENT_CANNOT_ACCEPT"})
	}
	request.To = to
	if err = m.checkTransitionOn(ctx, tx, request); err != nil {
		return "", err
	}
	return to, nil
}

func (m *Module) checkTransitionOn(ctx context.Context, tx *gorm.DB, request delivery.WorkflowTransition) error {
	if tx == nil || request.TenantID <= 0 || request.ProjectID <= 0 {
		return delivery.ErrInvalidArgument
	}
	if !slices.Contains([]string{"TASK", "BUG", "RISK", "INCIDENT"}, request.Type) {
		return nil
	}
	doc, e := m.effectiveWorkflowOn(ctx, tx, request.TenantID, request.ProjectID, request.Type)
	if e != nil {
		return e
	}
	states := map[string]string{}
	for _, s := range doc.States {
		states[s.Key] = s.MapsTo
	}
	archived := map[string]bool{}
	for _, state := range doc.States {
		archived[state.Key] = state.Archived
	}
	matched := false
	system := request.ActorKind == "SYSTEM" && request.Trigger == "SYSTEM"
	for _, cell := range doc.Transitions {
		if archived[cell.To] {
			continue
		}
		if (request.FromKey != "" && cell.From != request.FromKey) || (request.ToKey != "" && cell.To != request.ToKey) {
			continue
		}
		if states[cell.From] != request.From || states[cell.To] != request.To {
			continue
		}
		matched = true
		if !cell.Enabled || (!cell.ManualAllowed && !system) {
			return delivery.ErrWorkPreparationState
		}
		for _, kind := range []string{"ACTOR_SCOPE", "REQUIRED_FIELDS", "RELATED_OBJECTS", "EVIDENCE"} {
			// Spec 12 §4.1: source-owned system transitions bypass actor scope,
			// while their evidence and other conditions still apply.
			if system && kind == "ACTOR_SCOPE" {
				continue
			}
			for _, condition := range cell.Conditions {
				if condition.Kind != kind {
					continue
				}
				ok, e := evaluateTransitionCondition(request, condition)
				if errors.Is(e, delivery.ErrUnavailable) {
					return respx.ErrStatus(ctx, 503, 126503, yi18n.TransitionUnavailable, map[string]any{"failed": []TransitionCondition{condition}})
				}
				if e != nil {
					return e
				}
				if !ok {
					return respx.ErrStatus(ctx, 409, 126004, yi18n.TransitionBlocked, map[string]any{"failed": []TransitionCondition{condition}})
				}
			}
		}
	}
	if !matched {
		return delivery.ErrWorkPreparationState
	}
	return nil
}

func evaluateTransitionCondition(r delivery.WorkflowTransition, c TransitionCondition) (bool, error) {
	if c.Kind == "ACTOR_SCOPE" {
		if len(c.ActorKinds) > 0 && !slices.Contains(c.ActorKinds, r.ActorKind) {
			return false, nil
		}
		if len(c.PrincipalIDs) > 0 && !slices.Contains(c.PrincipalIDs, r.ActorID) {
			return false, nil
		}
		if len(c.RoleKeys) > 0 {
			return false, delivery.ErrUnavailable
		}
		if len(c.WorkItemRoles) > 0 {
			if r.FactOn == nil {
				return false, delivery.ErrUnavailable
			}
			match := false
			for _, role := range c.WorkItemRoles {
				ok, e := r.FactOn("WORK_ITEM_ROLE", role, "")
				if e != nil {
					return false, e
				}
				match = match || ok
			}
			if !match {
				return false, nil
			}
		}
		return true, nil
	}
	if r.FactOn == nil {
		return false, delivery.ErrUnavailable
	}
	if c.Kind == "REQUIRED_FIELDS" {
		for _, f := range c.Fields {
			ok, e := r.FactOn(c.Kind, f, "")
			if e != nil || !ok {
				return ok, e
			}
		}
		return true, nil
	}
	return r.FactOn(c.Kind, c.Rule, c.Group)
}

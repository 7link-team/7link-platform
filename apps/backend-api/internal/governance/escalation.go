package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func EscalationKinds() []string {
	return strings.Fields("BUDGET VERIFICATION_INCONCLUSIVE CLIENT_REJECTION REWORK_LIMIT SPINNING REVIEW_SLA LOW_SELF_CONFIDENCE SENSITIVE_PATH NO_ELIGIBLE_AUTHORITY EXECUTION_STALLED SPAWN_BUDGET")
}
func hardEscalation(kind string) bool {
	return slices.Contains([]string{"BUDGET", "VERIFICATION_INCONCLUSIVE", "CLIENT_REJECTION", "REWORK_LIMIT", "SPAWN_BUDGET"}, kind)
}

var sensitiveSetKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,31}$`)

type EscalationRuleValue struct {
	Enabled           bool            `json:"enabled"`
	Params            json.RawMessage `json:"params"`
	ActionsAutonomous []string        `json:"actions_autonomous"`
	ActionsHybrid     []string        `json:"actions_hybrid"`
}
type EscalationRuleView struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`
	Locked  bool   `json:"locked"`
	EscalationRuleValue
}

func defaultRule(kind string) EscalationRuleView {
	params := json.RawMessage(`{}`)
	switch kind {
	case "REWORK_LIMIT":
		params = json.RawMessage(`{"threshold":3}`)
	case "SPINNING":
		params = json.RawMessage(`{"ratio_pct":8}`)
	case "NO_ELIGIBLE_AUTHORITY":
		params = json.RawMessage(`{"agent_review_timeout_min":30}`)
	case "LOW_SELF_CONFIDENCE":
		params = json.RawMessage(`{"min_confidence":60}`)
	case "EXECUTION_STALLED":
		params = json.RawMessage(`{"stall_minutes":30}`)
	case "CLIENT_REJECTION":
		params = json.RawMessage(`{"include_partial":true}`)
	case "SENSITIVE_PATH":
		params = json.RawMessage(`{"max_paths_checked":2000,"pattern_sets":[{"key":"MIGRATIONS","label":"Migrations","enabled":true,"globs":["**/migrations/**","**/migrate/**","**/sql/**/*.sql","**/*.migration.*","**/db/schema.*","**/prisma/schema.prisma"]},{"key":"DEPENDENCIES","label":"Dependencies","enabled":true,"globs":["**/go.mod","**/go.sum","**/package.json","**/package-lock.json","**/bun.lock","**/bun.lockb","**/pnpm-lock.yaml","**/yarn.lock","**/Cargo.toml","**/Cargo.lock","**/requirements*.txt","**/pyproject.toml","**/poetry.lock","**/uv.lock","**/Gemfile.lock","**/pom.xml","**/build.gradle*"]},{"key":"AUTH","label":"Authorization","enabled":true,"globs":["**/auth/**","**/authn/**","**/authz/**","**/iam/**","**/*permission*","**/*rbac*","**/*acl*","**/security/**","**/*oauth*","**/*session*"]},{"key":"SECRETS_CI","label":"Credentials and CI","enabled":true,"globs":["**/.env*","**/*.pem","**/*.key","**/secrets/**",".github/workflows/**",".gitlab-ci.yml","**/Dockerfile*","**/deploy/**","**/k8s/**","**/terraform/**"]}]}`)
	}
	autonomous, hybrid := []string{"NOTIFY"}, []string{"NOTIFY"}
	switch kind {
	case "VERIFICATION_INCONCLUSIVE", "SENSITIVE_PATH":
		autonomous = []string{"NOTIFY", "HOLD_DELEGATIONS"}
		hybrid = []string{"NOTIFY", "HOLD_DELEGATIONS"}
	case "CLIENT_REJECTION":
		autonomous = []string{"NOTIFY", "HOLD_DELEGATIONS", "PROJECT_HOLD"}
		hybrid = []string{"NOTIFY", "HOLD_DELEGATIONS"}
	case "LOW_SELF_CONFIDENCE":
		autonomous = []string{"HOLD_DELEGATIONS", "NOTIFY"}
		hybrid = []string{"HOLD_DELEGATIONS"}
	case "NO_ELIGIBLE_AUTHORITY":
		hybrid = []string{}
	}
	return EscalationRuleView{Kind: kind, Locked: hardEscalation(kind), EscalationRuleValue: EscalationRuleValue{Enabled: true, Params: params, ActionsAutonomous: autonomous, ActionsHybrid: hybrid}}
}

func validRule(kind string, v EscalationRuleValue) bool {
	if !slices.Contains(EscalationKinds(), kind) || len(v.Params) > 16384 || !json.Valid(v.Params) {
		return false
	}
	for index, actions := range [][]string{v.ActionsAutonomous, v.ActionsHybrid} {
		if (len(actions) < 1 && !(kind == "NO_ELIGIBLE_AUTHORITY" && index == 1)) || len(actions) > 3 {
			return false
		}
		seen := map[string]bool{}
		for _, action := range actions {
			if !slices.Contains([]string{"NOTIFY", "HOLD_DELEGATIONS", "PROJECT_HOLD"}, action) || seen[action] {
				return false
			}
			seen[action] = true
		}
	}
	var params map[string]json.RawMessage
	if json.Unmarshal(v.Params, &params) != nil || params == nil {
		return false
	}
	if kind == "SENSITIVE_PATH" {
		for key := range params {
			if key != "pattern_sets" && key != "max_paths_checked" {
				return false
			}
		}
		var sets []struct {
			Key     string   `json:"key"`
			Label   string   `json:"label"`
			Enabled *bool    `json:"enabled"`
			Globs   []string `json:"globs"`
		}
		decoder := json.NewDecoder(bytes.NewReader(params["pattern_sets"]))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&sets) != nil || sets == nil || len(sets) > 20 {
			return false
		}
		seen := map[string]bool{}
		for _, set := range sets {
			if !sensitiveSetKeyPattern.MatchString(set.Key) || seen[set.Key] || utf8.RuneCountInString(set.Label) > 64 || set.Enabled == nil || len(set.Globs) == 0 || len(set.Globs) > 50 {
				return false
			}
			seen[set.Key] = true
			for _, pattern := range set.Globs {
				if len(pattern) == 0 || len(pattern) > 200 || strings.Contains(pattern, "..") || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "\\") || strings.HasPrefix(pattern, "!") {
					return false
				}
			}
		}
		if raw, ok := params["max_paths_checked"]; ok {
			var n int
			if json.Unmarshal(raw, &n) != nil || n < 100 || n > 5000 {
				return false
			}
		}
		return true
	}
	key := ""
	minimum, maximum := 0, 0
	switch kind {
	case "REWORK_LIMIT":
		key, minimum, maximum = "threshold", 3, 3
	case "SPINNING":
		key, minimum, maximum = "ratio_pct", 8, 8
	case "NO_ELIGIBLE_AUTHORITY":
		key, minimum, maximum = "agent_review_timeout_min", 30, 30
	case "LOW_SELF_CONFIDENCE":
		key, minimum, maximum = "min_confidence", 30, 90
	case "EXECUTION_STALLED":
		key, minimum, maximum = "stall_minutes", 5, 1440
	case "CLIENT_REJECTION":
		var value bool
		return len(params) == 1 && json.Unmarshal(params["include_partial"], &value) == nil
	}
	if key == "" {
		return len(params) == 0
	}
	var n int
	return len(params) == 1 && json.Unmarshal(params[key], &n) == nil && n >= minimum && n <= maximum
}

func (m *Module) readRulesOn(ctx context.Context, db *gorm.DB, tenant guid.ID) ([]EscalationRuleView, error) {
	if m == nil || m.seed == nil || db == nil {
		return nil, ErrUnavailable
	}
	if tenant <= 0 {
		return nil, ErrInvalidArgument
	}
	q := query.BindEscalationRules(db, m.seed.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant)).Find()
	if err != nil {
		return nil, err
	}
	stored := map[string]*model.EscalationRule{}
	for _, row := range rows {
		stored[row.Kind] = row
	}
	views := []EscalationRuleView{}
	for _, kind := range EscalationKinds() {
		v := defaultRule(kind)
		if row := stored[kind]; row != nil {
			var storedValue EscalationRuleValue
			if json.Unmarshal([]byte(row.ValueJSON), &storedValue) != nil || !validRule(kind, storedValue) {
				return nil, ErrUnavailable
			}
			v.EscalationRuleValue = storedValue
			v.Version = row.Version
		}
		views = append(views, v)
	}
	return views, nil
}

func (m *Module) putRuleOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind string, expected int, value EscalationRuleValue, cookie, proof string) (EscalationRuleView, error) {
	if m == nil || m.seed == nil || m.seed.ids == nil {
		return EscalationRuleView{}, ErrUnavailable
	}
	if expected < 0 || !validRule(kind, value) || (hardEscalation(kind) && !value.Enabled) {
		return EscalationRuleView{}, ErrInvalidArgument
	}
	if err := m.authorizeHumanOn(ctx, tx, tenant, "policy_manage"); err != nil {
		return EscalationRuleView{}, err
	}
	q := query.BindEscalationRules(tx, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Kind.Eq(kind)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return EscalationRuleView{}, err
	}
	old := defaultRule(kind)
	if row != nil {
		var storedValue EscalationRuleValue
		if json.Unmarshal([]byte(row.ValueJSON), &storedValue) != nil || !validRule(kind, storedValue) {
			return EscalationRuleView{}, ErrUnavailable
		}
		old.EscalationRuleValue = storedValue
		old.Version = row.Version
	}
	if old.Version != expected {
		return EscalationRuleView{}, ErrVersion
	}
	if hardEscalation(kind) {
		for _, required := range defaultRule(kind).ActionsAutonomous {
			if !slices.Contains(value.ActionsAutonomous, required) {
				return EscalationRuleView{}, ErrInvalidArgument
			}
		}
	}
	if ruleRelaxed(kind, old.EscalationRuleValue, value) {
		if m.reauth == nil {
			return EscalationRuleView{}, ErrUnavailable
		}
		if err := m.reauth(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, proof, "escalation_rule_relax", tenant.String()+":escalation-rule:"+kind+":"+fmtVersion(expected)); err != nil {
			return EscalationRuleView{}, ErrForbidden
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return EscalationRuleView{}, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if row == nil {
		id, err := m.seed.ids.Next()
		if err != nil {
			return EscalationRuleView{}, err
		}
		if id <= 0 {
			return EscalationRuleView{}, ErrUnavailable
		}
		row = &model.EscalationRule{ID: guid.ID(id), TenantID: tenant, Kind: kind, ValueJSON: string(encoded), Version: 1, CreatedAt: now, UpdatedAt: now}
		if err = q.WithContext(ctx).Create(row); err != nil {
			return EscalationRuleView{}, err
		}
	} else {
		result, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.Version.Eq(expected)).Updates(map[string]any{"value_json": string(encoded), "version": expected + 1, "updated_at": now})
		if err != nil {
			return EscalationRuleView{}, err
		}
		if result.RowsAffected != 1 {
			return EscalationRuleView{}, ErrVersion
		}
		row.Version = expected + 1
	}
	if err := m.governanceEventOn(ctx, tx, tenant, 0, row.ID, row.Version, "governance.rule_updated", "escalation_rule", map[string]any{"kind": kind, "before": old, "after": value, "version": row.Version}); err != nil {
		return EscalationRuleView{}, err
	}
	return EscalationRuleView{Kind: kind, Version: row.Version, Locked: hardEscalation(kind), EscalationRuleValue: value}, nil
}

func ruleRelaxed(kind string, old, next EscalationRuleValue) bool {
	if old.Enabled && !next.Enabled {
		return true
	}
	for _, action := range old.ActionsAutonomous {
		if !slices.Contains(next.ActionsAutonomous, action) {
			return true
		}
	}
	for _, action := range old.ActionsHybrid {
		if !slices.Contains(next.ActionsHybrid, action) {
			return true
		}
	}
	var a, b map[string]any
	if json.Unmarshal(old.Params, &a) != nil || json.Unmarshal(next.Params, &b) != nil {
		return true
	}
	switch kind {
	case "LOW_SELF_CONFIDENCE":
		before, oldValid := a["min_confidence"].(float64)
		after, nextValid := b["min_confidence"].(float64)
		return !oldValid || !nextValid || after < before
	case "EXECUTION_STALLED":
		before, oldValid := a["stall_minutes"].(float64)
		after, nextValid := b["stall_minutes"].(float64)
		return !oldValid || !nextValid || after > before
	case "CLIENT_REJECTION":
		return a["include_partial"] == true && b["include_partial"] == false
	case "SENSITIVE_PATH":
		return !jsonEquivalent(old.Params, next.Params)
	}
	return false
}
func jsonEquivalent(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	aa, _ := json.Marshal(x)
	bb, _ := json.Marshal(y)
	return string(aa) == string(bb)
}

// EscalationFact is supplied only by the owning domain after publishing an
// actual escalation event. Recording it participates in that same transaction.
// Recording does not claim notifications/holds were applied by other modules.
type EscalationFact struct {
	TenantID, ProjectID, SubjectID, TriggerEventID guid.ID
	Kind, SubjectKind, Reason                      string
	Facts                                          map[string]any
	Actor                                          events.Actor
}
type EscalationRecorder func(context.Context, *gorm.DB, EscalationFact) (guid.ID, error)

type IncidentView struct {
	ID             guid.ID         `json:"id"`
	ProjectID      guid.ID         `json:"project_id"`
	Kind           string          `json:"kind"`
	SubjectKind    string          `json:"subject_kind"`
	SubjectID      guid.ID         `json:"subject_id"`
	TriggerEventID guid.ID         `json:"trigger_event_id"`
	Reason         string          `json:"reason"`
	Facts          json.RawMessage `json:"facts"`
	State          string          `json:"state"`
	Version        int             `json:"version"`
	HandledBy      *guid.ID        `json:"handled_by,omitempty"`
	HandledAt      *time.Time      `json:"handled_at,omitempty"`
	ResolutionNote *string         `json:"resolution_note,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

func incidentView(row *model.EscalationIncident) IncidentView {
	return IncidentView{ID: row.ID, ProjectID: row.ProjectID, Kind: row.Kind, SubjectKind: row.SubjectKind, SubjectID: row.SubjectID, TriggerEventID: row.TriggerEventID, Reason: row.Reason, Facts: json.RawMessage(row.FactsJSON), State: row.State, Version: row.Version, HandledBy: row.HandledBy, HandledAt: row.HandledAt, ResolutionNote: row.ResolutionNote, CreatedAt: row.CreatedAt}
}

func (m *Module) canReadIncidentProject(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (bool, error) {
	if m == nil || m.explain == nil {
		return false, ErrUnavailable
	}
	e, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: authn.PrincipalIDFromContext(ctx), Permission: "project_read"})
	return e.Allowed && e.Reason != "explicit_deny" && e.Reason != "administrator_scope_denied", err
}

func (m *Module) incidentDecisionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, expected int, state, note string) (IncidentView, error) {
	permission := "policy_read"
	if state == "DISMISSED" {
		permission = "policy_manage"
	}
	if id <= 0 || expected < 1 || (state != "ACKNOWLEDGED" && state != "DISMISSED") || (state == "DISMISSED" && (utf8.RuneCountInString(strings.TrimSpace(note)) < 10 || utf8.RuneCountInString(note) > 2000)) {
		return IncidentView{}, ErrInvalidArgument
	}
	if err := m.authorizeHumanOn(ctx, tx, tenant, permission); err != nil {
		return IncidentView{}, err
	}
	q := query.BindEscalationIncidents(tx, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return IncidentView{}, ErrNotFound
	}
	if err != nil {
		return IncidentView{}, err
	}
	allowed, err := m.canReadIncidentProject(ctx, tx, tenant, row.ProjectID)
	if err != nil {
		return IncidentView{}, err
	}
	if !allowed {
		return IncidentView{}, ErrNotFound
	}
	if row.State == state {
		if state == "DISMISSED" && (row.ResolutionNote == nil || *row.ResolutionNote != note) {
			return IncidentView{}, ErrVersion
		}
		return incidentView(row), nil
	}
	if row.Version != expected {
		return IncidentView{}, ErrVersion
	}
	if row.State != "OPEN" && !(row.State == "ACKNOWLEDGED" && state == "DISMISSED") {
		return IncidentView{}, ErrVersion
	}
	now, actor := time.Now().UTC().Truncate(time.Millisecond), authn.PrincipalIDFromContext(ctx)
	updates := map[string]any{"state": state, "handled_at": now, "handled_by": actor, "updated_at": now, "version": expected + 1}
	if state == "DISMISSED" {
		updates["resolution_note"] = note
		updates["open_key"] = nil
	}
	result, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.Version.Eq(expected)).Updates(updates)
	if err != nil {
		return IncidentView{}, err
	}
	if result.RowsAffected != 1 {
		return IncidentView{}, ErrVersion
	}
	row.State, row.Version, row.HandledAt, row.HandledBy = state, expected+1, &now, &actor
	if state == "DISMISSED" {
		row.ResolutionNote = &note
	}
	if err := m.governanceEventOn(ctx, tx, tenant, row.ProjectID, id, row.Version, "governance.incident_changed", "escalation_incident", map[string]any{"state": state, "version": row.Version, "note": note}); err != nil {
		return IncidentView{}, err
	}
	return incidentView(row), nil
}

func (m *Module) recordEscalationOn(ctx context.Context, tx *gorm.DB, fact EscalationFact) (guid.ID, error) {
	if m == nil || m.seed == nil || m.seed.ids == nil {
		return 0, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil {
		return 0, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	if fact.TenantID <= 0 || fact.ProjectID <= 0 || fact.SubjectID <= 0 || fact.TriggerEventID <= 0 || !slices.Contains(EscalationKinds(), fact.Kind) || !slices.Contains([]string{"PROJECT", "WORK_ITEM", "DELIVERY", "CLAIM"}, fact.SubjectKind) || utf8.RuneCountInString(fact.Reason) < 1 || utf8.RuneCountInString(fact.Reason) > 2000 || !utf8.ValidString(fact.Reason) || !slices.Contains([]string{"HUMAN", "AGENT", "SYSTEM"}, fact.Actor.Kind) || (fact.Actor.Kind != "SYSTEM" && fact.Actor.ID <= 0) {
		return 0, ErrInvalidArgument
	}
	encoded, err := json.Marshal(fact.Facts)
	if err != nil || len(encoded) > 32768 {
		return 0, ErrInvalidArgument
	}
	q := query.BindEscalationIncidents(tx, m.seed.names)
	old, err := q.WithContext(ctx).Where(q.TenantID.Eq(fact.TenantID), q.TriggerEventID.Eq(fact.TriggerEventID), q.Kind.Eq(fact.Kind), q.SubjectID.Eq(fact.SubjectID)).First()
	if err == nil {
		return old.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	openKey := fact.ProjectID.String() + ":" + fact.Kind + ":" + fact.SubjectKind + ":" + fact.SubjectID.String()
	open, err := q.WithContext(ctx).Where(q.TenantID.Eq(fact.TenantID), q.OpenKey.Eq(openKey)).First()
	if err == nil {
		return open.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	id, err := m.seed.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := &model.EscalationIncident{ID: guid.ID(id), TenantID: fact.TenantID, ProjectID: fact.ProjectID, Kind: fact.Kind, SubjectKind: fact.SubjectKind, SubjectID: fact.SubjectID, TriggerEventID: fact.TriggerEventID, Reason: fact.Reason, FactsJSON: string(encoded), State: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}
	row.OpenKey = &openKey
	if err = q.WithContext(ctx).Create(row); err != nil {
		return 0, err
	}
	if m.seed.audit == nil || m.publisher == nil {
		return 0, ErrUnavailable
	}
	detail := map[string]any{"state": "OPEN", "rule_kind": fact.Kind, "source_event_id": fact.TriggerEventID, "subject_id": fact.SubjectID, "actor_kind": fact.Actor.Kind}
	if err = m.seed.audit(ctx, tx, audit.DomainEvent{TenantID: fact.TenantID, PrincipalID: fact.Actor.ID, EventType: "governance.incident_changed", TargetType: "escalation_incident", TargetID: row.ID, Detail: detail}); err != nil {
		return 0, err
	}
	if _, err = m.publisher.Publish(ctx, tx, events.Event{TenantID: fact.TenantID, ProjectID: fact.ProjectID, Type: "governance.incident_changed", Actor: fact.Actor, Subject: events.Subject{Kind: "escalation_incident", ID: row.ID}, SubjectVersion: 1, Data: detail}); err != nil {
		return 0, err
	}
	return row.ID, nil
}

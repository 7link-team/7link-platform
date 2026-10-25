package governance

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type sensitiveRuleSet struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Enabled bool     `json:"enabled"`
	Globs   []string `json:"globs"`
}
type sensitiveRuleParams struct {
	Sets     []sensitiveRuleSet `json:"pattern_sets"`
	MaxPaths int                `json:"max_paths_checked,omitempty"`
}

func unionRuleActions(a, b []string) []string {
	out := append([]string{}, a...)
	for _, action := range b {
		if !slices.Contains(out, action) {
			out = append(out, action)
		}
	}
	return out
}

// mergeProjectRule retains both current tenant safeguards and the project floor.
// Glob strictness is proven by retaining exact patterns; we do not guess whether
// one arbitrary glob language includes another.
func mergeProjectRule(kind string, tenant, project EscalationRuleValue) EscalationRuleValue {
	out := project
	out.Enabled = tenant.Enabled || project.Enabled
	out.ActionsAutonomous = unionRuleActions(tenant.ActionsAutonomous, project.ActionsAutonomous)
	out.ActionsHybrid = unionRuleActions(tenant.ActionsHybrid, project.ActionsHybrid)
	var a, b map[string]any
	_ = json.Unmarshal(tenant.Params, &a)
	_ = json.Unmarshal(project.Params, &b)
	switch kind {
	case "LOW_SELF_CONFIDENCE":
		b["min_confidence"] = max(a["min_confidence"].(float64), b["min_confidence"].(float64)) //nolint:errcheck // validRule verifies both parameter sets before merging.
	case "EXECUTION_STALLED":
		b["stall_minutes"] = min(a["stall_minutes"].(float64), b["stall_minutes"].(float64)) //nolint:errcheck // validRule verifies both parameter sets before merging.
	case "CLIENT_REJECTION":
		b["include_partial"] = a["include_partial"].(bool) || b["include_partial"].(bool) //nolint:errcheck // validRule verifies both parameter sets before merging.
	case "SENSITIVE_PATH":
		var left, right sensitiveRuleParams
		_ = json.Unmarshal(tenant.Params, &left)
		_ = json.Unmarshal(project.Params, &right)
		for _, base := range left.Sets {
			if !base.Enabled {
				continue
			}
			i := slices.IndexFunc(right.Sets, func(set sensitiveRuleSet) bool { return set.Key == base.Key })
			if i < 0 {
				right.Sets = append(right.Sets, base)
				continue
			}
			right.Sets[i].Enabled = true
			right.Sets[i].Globs = unionRuleActions(base.Globs, right.Sets[i].Globs)
		}
		// Both limits are retained conservatively: exhausting either scan budget
		// must still require human handling.
		if left.MaxPaths == 0 {
			left.MaxPaths = 2000
		}
		if right.MaxPaths == 0 {
			right.MaxPaths = 2000
		}
		right.MaxPaths = min(left.MaxPaths, right.MaxPaths)
		out.Params, _ = json.Marshal(right)
		return out
	}
	out.Params, _ = json.Marshal(b)
	return out
}

func projectRuleAtLeastAsStrict(kind string, floor, next EscalationRuleValue) bool {
	if floor.Enabled && !next.Enabled {
		return false
	}
	for _, pair := range [][2][]string{{floor.ActionsAutonomous, next.ActionsAutonomous}, {floor.ActionsHybrid, next.ActionsHybrid}} {
		for _, action := range pair[0] {
			if !slices.Contains(pair[1], action) {
				return false
			}
		}
	}
	if kind != "SENSITIVE_PATH" {
		return !ruleRelaxed(kind, floor, next)
	}
	var a, b sensitiveRuleParams
	if json.Unmarshal(floor.Params, &a) != nil || json.Unmarshal(next.Params, &b) != nil {
		return false
	}
	if a.MaxPaths == 0 {
		a.MaxPaths = 2000
	}
	if b.MaxPaths == 0 {
		b.MaxPaths = 2000
	}
	if b.MaxPaths > a.MaxPaths {
		return false
	}
	for _, base := range a.Sets {
		if !base.Enabled {
			continue
		}
		i := slices.IndexFunc(b.Sets, func(set sensitiveRuleSet) bool { return set.Key == base.Key && set.Enabled })
		if i < 0 {
			return false
		}
		for _, glob := range base.Globs {
			if !slices.Contains(b.Sets[i].Globs, glob) {
				return false
			}
		}
	}
	return true
}

func (m *Module) readEffectiveRulesOn(ctx context.Context, db *gorm.DB, tenant, project guid.ID) ([]EscalationRuleView, error) {
	rules, err := m.readRulesOn(ctx, db, tenant)
	if err != nil || project == 0 {
		return rules, err
	}
	if project < 0 {
		return nil, ErrInvalidArgument
	}
	q := query.BindProjectEscalationRules(db, m.seed.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Find()
	if err != nil {
		return nil, err
	}
	stored := map[string]*model.ProjectEscalationRule{}
	for _, row := range rows {
		stored[row.Kind] = row
	}
	for i := range rules {
		rules[i].Version = 0 // Version identifies the project override, not its tenant floor.
		if row := stored[rules[i].Kind]; row != nil {
			var value EscalationRuleValue
			if json.Unmarshal([]byte(row.ValueJSON), &value) != nil || !validRule(row.Kind, value) {
				return nil, ErrUnavailable
			}
			rules[i].EscalationRuleValue = mergeProjectRule(row.Kind, rules[i].EscalationRuleValue, value)
			rules[i].Version = row.Version
		}
	}
	return rules, nil
}

func (m *Module) authorizeProjectRulesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
	if project <= 0 {
		return ErrInvalidArgument
	}
	if err := m.authorizeHumanOn(ctx, tx, tenant, permission); err != nil {
		return err
	}
	allowed, err := m.canReadIncidentProject(ctx, tx, tenant, project)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrNotFound
	}
	if m.delegations.projects.InspectOn == nil {
		return ErrUnavailable
	}
	_, err = m.delegations.projects.InspectOn(ctx, tx, tenant, project)
	return err
}

func (m *Module) putProjectRuleOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, kind string, expected int, value EscalationRuleValue, cookie, proof string) (EscalationRuleView, error) {
	if m == nil || m.seed == nil || m.seed.ids == nil || m.seed.now == nil {
		return EscalationRuleView{}, ErrUnavailable
	}
	if expected < 0 || !validRule(kind, value) {
		return EscalationRuleView{}, ErrInvalidArgument
	}
	if err := m.authorizeProjectRulesOn(ctx, tx, tenant, project, "policy_manage"); err != nil {
		return EscalationRuleView{}, err
	}
	rules, err := m.readRulesOn(ctx, tx, tenant)
	if err != nil {
		return EscalationRuleView{}, err
	}
	var floor EscalationRuleValue
	for _, rule := range rules {
		if rule.Kind == kind {
			floor = rule.EscalationRuleValue
		}
	}
	if !projectRuleAtLeastAsStrict(kind, floor, value) {
		return EscalationRuleView{}, ErrInvalidArgument
	}
	q := query.BindProjectEscalationRules(tx, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Kind.Eq(kind)).Clauses(clause.Locking{Strength: "UPDATE"}).Take()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return EscalationRuleView{}, err
	}
	old := floor
	version := 0
	if row != nil {
		if json.Unmarshal([]byte(row.ValueJSON), &old) != nil || !validRule(kind, old) {
			return EscalationRuleView{}, ErrUnavailable
		}
		old = mergeProjectRule(kind, floor, old)
		version = row.Version
	}
	if expected != version {
		return EscalationRuleView{}, ErrVersion
	}
	if !projectRuleAtLeastAsStrict(kind, old, value) {
		if m.reauth == nil {
			return EscalationRuleView{}, ErrUnavailable
		}
		target := tenant.String() + ":project:" + project.String() + ":escalation-rule:" + kind + ":" + fmtVersion(expected)
		if err = m.reauth(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, proof, "escalation_rule_relax", target); err != nil {
			return EscalationRuleView{}, ErrForbidden
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return EscalationRuleView{}, err
	}
	now := m.seed.now()
	if row == nil {
		id, err := m.seed.ids.Next()
		if err != nil {
			return EscalationRuleView{}, err
		}
		if id <= 0 {
			return EscalationRuleView{}, ErrUnavailable
		}
		row = &model.ProjectEscalationRule{ID: guid.ID(id), TenantID: tenant, ProjectID: project, Kind: kind, ValueJSON: string(encoded), Version: 1, CreatedAt: now, UpdatedAt: now}
		if err = q.WithContext(ctx).Create(row); err != nil {
			return EscalationRuleView{}, err
		}
	} else {
		changed, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.Version.Eq(expected)).Updates(map[string]any{"value_json": string(encoded), "version": expected + 1, "updated_at": now})
		if err != nil {
			return EscalationRuleView{}, err
		}
		if changed.RowsAffected != 1 {
			return EscalationRuleView{}, ErrVersion
		}
		row.Version++
	}
	if err = m.governanceEventOn(ctx, tx, tenant, project, row.ID, row.Version, "governance.rule_updated", "escalation_rule", map[string]any{"kind": kind, "project_id": project.String(), "before": old, "after": value, "version": row.Version}); err != nil {
		return EscalationRuleView{}, err
	}
	return EscalationRuleView{Kind: kind, Version: row.Version, Locked: hardEscalation(kind), EscalationRuleValue: value}, nil
}

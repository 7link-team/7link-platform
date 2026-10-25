package templates

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"reflect"
	"slices"
	"strings"
	"time"
)

var ErrOverrideRange = errors.New("templates: override outside published policy")
var ErrBreakingAck = errors.New("templates: major upgrade needs Human acknowledgement")
var ErrDiffMismatch = errors.New("templates: cannot compare different templates")

type TemplateDiffEntry struct {
	Path   string          `json:"path"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}
type TemplateDiff struct {
	Added   []TemplateDiffEntry `json:"added"`
	Removed []TemplateDiffEntry `json:"removed"`
	Changed []TemplateDiffEntry `json:"changed"`
}

func diffDefinitions(before, after TemplateDefinition) (TemplateDiff, error) {
	out := TemplateDiff{Added: []TemplateDiffEntry{}, Removed: []TemplateDiffEntry{}, Changed: []TemplateDiffEntry{}}
	if before.Domain != after.Domain || before.Workflow.WorkflowID != after.Workflow.WorkflowID {
		return out, ErrDiffMismatch
	}
	flatten := func(d TemplateDefinition) map[string]json.RawMessage {
		out := map[string]json.RawMessage{}
		add := func(key string, value any) { out[key], _ = json.Marshal(value) }
		for _, node := range d.Workflow.Nodes {
			add("nodes/"+node.ID, node)
		}
		for _, edge := range d.Workflow.Edges {
			add("edges/"+edge.ID, edge)
		}
		for key, value := range d.Workflow.Parameters {
			add("parameters/"+key, value)
		}
		for key, value := range d.Metadata {
			add("metadata/"+key, value)
		}
		add("constitution_seed", d.ConstitutionSeed)
		add("workflow/name", d.Workflow.Name)
		add("workflow/entry_node_id", d.Workflow.EntryNodeID)
		add("workflow/output_bindings", d.Workflow.OutputBindings)
		return out
	}
	left, right := flatten(before), flatten(after)
	keys := []string{}
	for key := range left {
		keys = append(keys, key)
	}
	for key := range right {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range slices.Compact(keys) {
		a, aok := left[key]
		b, bok := right[key]
		entry := TemplateDiffEntry{key, a, b}
		if !aok {
			out.Added = append(out.Added, entry)
		} else if !bok {
			out.Removed = append(out.Removed, entry)
		} else if string(a) != string(b) {
			out.Changed = append(out.Changed, entry)
		}
	}
	return out, nil
}

// resolveBindingParameters accepts only published, explicitly overridable
// declarations. Effective values feed runtime conditions and transformations.
func resolveBindingParameters(def TemplateDefinition, row *model.Binding) (map[string]any, map[string]any, error) {
	effective := map[string]any{}
	for key, value := range def.Workflow.Parameters {
		effective[key] = value
	}
	overrides := map[string]any{}
	if row.BriefOverrides != "" {
		if json.Unmarshal([]byte(row.BriefOverrides), &overrides) != nil {
			return nil, nil, ErrUnavailable
		}
	}
	if overrides == nil {
		overrides = map[string]any{}
	}
	if len(overrides) == 0 {
		return effective, overrides, nil
	}
	if row.OverridesApprovedBy == nil || row.OverridesApprovedAt == nil {
		return nil, nil, ErrOverrideRange
	}
	policies, _ := def.Metadata["parameters"].(map[string]any)
	for key, value := range overrides {
		policy, ok := policies[key].(map[string]any)
		if !ok || policy["brief_overridable"] != true {
			return nil, nil, ErrOverrideRange
		}
		original, exists := effective[key]
		if !exists {
			return nil, nil, ErrOverrideRange
		}
		if reflect.TypeOf(original) != reflect.TypeOf(value) {
			return nil, nil, ErrOverrideRange
		}
		if choices, ok := policy["values"].([]any); ok {
			if !slices.ContainsFunc(choices, func(v any) bool { return reflect.DeepEqual(v, value) }) {
				return nil, nil, ErrOverrideRange
			}
		}
		if number, ok := value.(float64); ok {
			if minimum, ok := policy["min"].(float64); ok && number < minimum {
				return nil, nil, ErrOverrideRange
			}
			if maximum, ok := policy["max"].(float64); ok && number > maximum {
				return nil, nil, ErrOverrideRange
			}
		}
		if policy["only_increase"] == true {
			a, aok := original.(float64)
			b, bok := value.(float64)
			if !aok || !bok || b < a {
				return nil, nil, ErrOverrideRange
			}
		}
		effective[key] = value
	}
	return effective, overrides, nil
}
func (m *Module) approveBindingOverridesOn(ctx context.Context, tx *gorm.DB, project guid.ID, version int, overrides map[string]any) (TemplateInstanceView, error) {
	var empty TemplateInstanceView
	row, e := m.studioAuthorizeOn(ctx, tx, project)
	if e != nil {
		return empty, e
	}
	if row.Version != version {
		return empty, ErrConflict
	}
	target, e := m.versionOn(ctx, tx, row.TemplateVersionID, "template_read")
	if e != nil {
		return empty, e
	}
	def, e := versionView(target)
	if e != nil {
		return empty, e
	}
	if overrides == nil {
		overrides = map[string]any{}
	}
	raw, e := json.Marshal(overrides)
	if e != nil || len(raw) > 12000 {
		return empty, ErrOverrideRange
	}
	now := time.Now().UTC()
	actor := authn.PrincipalIDFromContext(ctx)
	row.BriefOverrides = string(raw)
	row.OverridesApprovedBy = &actor
	row.OverridesApprovedAt = &now
	if _, _, e = resolveBindingParameters(def.Definition, row); e != nil {
		return empty, e
	}
	result := tx.WithContext(ctx).Table(m.names.Bindings.String()).Where("tenant_id = ? AND id = ? AND version = ?", row.TenantID, row.ID, version).Updates(map[string]any{"brief_overrides": row.BriefOverrides, "overrides_approved_by": actor, "overrides_approved_at": now, "version": version + 1})
	if result.Error != nil {
		return empty, result.Error
	}
	if result.RowsAffected != 1 {
		return empty, ErrConflict
	}
	if e = m.bindingEventOn(ctx, tx, row, target, "template.overrides_approved", row.TemplateVersionID); e != nil {
		return empty, e
	}
	return m.instanceOn(ctx, tx, project)
}
func (m *Module) upgradeBindingOn(ctx context.Context, tx *gorm.DB, project, targetID guid.ID, version int, ack bool) (TemplateInstanceView, error) {
	var empty TemplateInstanceView
	row, e := m.studioAuthorizeOn(ctx, tx, project)
	if e != nil {
		return empty, e
	}
	if row.Version != version {
		return empty, ErrConflict
	}
	old, e := m.versionOn(ctx, tx, row.TemplateVersionID, "template_read")
	if e != nil {
		return empty, e
	}
	// Published targets are immutable. Do not lock a second version after the
	// current binding version: projects can upgrade from different starting pins.
	versions := query.BindVersions(tx, m.names)
	target, e := versions.WithContext(ctx).Where(versions.TenantID.Eq(row.TenantID), versions.ID.Eq(targetID)).Take()
	if e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return empty, ErrNotFound
		}
		return empty, e
	}
	if e = m.authorizeOn(ctx, tx, "template_read", target.CreatedBy); e != nil {
		return empty, e
	}
	if old.Key != target.Key || target.State != "PUBLISHED" {
		return empty, ErrDiffMismatch
	}
	a, e := versionView(old)
	if e != nil {
		return empty, e
	}
	b, e := versionView(target)
	if e != nil {
		return empty, e
	}
	oldParameters, _, e := resolveBindingParameters(a.Definition, row)
	if e != nil {
		return empty, e
	}
	majorA, _, _ := strings.Cut(old.Semver, ".")
	majorB, _, _ := strings.Cut(target.Semver, ".")
	if majorA != majorB && !ack {
		return empty, ErrBreakingAck
	}
	if _, _, e = resolveBindingParameters(b.Definition, row); e != nil {
		return empty, e
	}
	children, e := m.childrenOn(ctx, tx)
	if e != nil {
		return empty, e
	}
	if e = b.Definition.Validate(children); e != nil {
		return empty, e
	}
	if e = validateRuntime(b.Definition.Workflow, children, 0); e != nil {
		return empty, e
	}
	var runs []TemplateNodeRun
	if json.Unmarshal([]byte(row.Runs), &runs) != nil || len(runs) == 0 {
		return empty, ErrUnavailable
	}
	accountable := runs[0].AccountableID
	pinnedChildren := runs[0].Children
	if pinnedChildren == nil {
		pinnedChildren = map[string]TemplateWorkflowDef{}
	}
	before, after := map[string]TemplateNode{}, map[string]TemplateNode{}
	for _, node := range a.Definition.Workflow.Nodes {
		before[node.ID] = node
	}
	for _, node := range b.Definition.Workflow.Nodes {
		after[node.ID] = node
	}
	for i, run := range runs {
		if run.ExpansionKey != runtimeKey("", run.NodeID) {
			continue
		}
		previous, exists := before[run.NodeID]
		if !exists {
			continue
		}
		next, exists := after[run.NodeID]
		started := run.State != "WAITING" || run.WorkItemID != nil || run.ChildHash != "" || run.DecisionID != nil
		if started && (!exists || next.Kind != previous.Kind) {
			return empty, ErrConflict
		}
		if started && previous.Kind != "WORK_ITEM" && !reflect.DeepEqual(previous, next) {
			return empty, ErrConflict
		}
		if started {
			for _, edge := range a.Definition.Workflow.Edges {
				if edge.Target == run.NodeID {
					if !slices.Contains(b.Definition.Workflow.Edges, edge) {
						return empty, ErrConflict
					}
				}
			}
			for _, edge := range b.Definition.Workflow.Edges {
				if edge.Target == run.NodeID {
					if !slices.Contains(a.Definition.Workflow.Edges, edge) {
						return empty, ErrConflict
					}
				}
			}
			if runs[i].DefinitionHash == "" {
				runs[i].DefinitionHash = old.SHA256
				runs[i].TemplateVersionID = old.ID
				runs[i].Parameters, _ = json.Marshal(oldParameters)
			}
		}
	}
	// Keep all materialized runs; discard only removed, untouched pending nodes.
	kept := runs[:0]
	for _, run := range runs {
		_, exists := after[run.NodeID]
		if exists || run.ExpansionKey != runtimeKey("", run.NodeID) || run.State != "WAITING" || run.WorkItemID != nil {
			kept = append(kept, run)
		}
	}
	if len(kept) == 0 {
		return empty, ErrConflict
	}
	kept[0].AccountableID = accountable
	kept[0].Children = pinnedChildren
	pinRuntimeChildren(b.Definition.Workflow, children, kept[0].Children)
	raw, e := json.Marshal(kept)
	if e != nil {
		return empty, e
	}
	result := tx.WithContext(ctx).Table(m.names.Bindings.String()).Where("tenant_id = ? AND id = ? AND version = ?", row.TenantID, row.ID, version).Updates(map[string]any{"template_version_id": target.ID, "definition_sha256": target.SHA256, "runs": string(raw), "version": version + 1})
	if result.Error != nil {
		return empty, result.Error
	}
	if result.RowsAffected != 1 {
		return empty, ErrConflict
	}
	if e = m.bindingEventOn(ctx, tx, row, target, "template.binding_changed", old.ID); e != nil {
		return empty, e
	}
	return m.instanceOn(ctx, tx, project)
}
func (m *Module) bindingEventOn(ctx context.Context, tx *gorm.DB, row *model.Binding, target *model.Version, kind string, from guid.ID) error {
	event := audit.NewDomainEvent(ctx, row.TenantID, kind, "workflow_binding", row.ID)
	event.Detail["authority_source"] = "IAM:project_manage:HUMAN"
	event.Detail["project_id"] = row.ProjectID
	event.Detail["from_version_id"] = from
	event.Detail["to_version_id"] = target.ID
	if e := m.audit(ctx, tx, event); e != nil {
		return e
	}
	_, e := m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: kind, Subject: events.Subject{Kind: "workflow_binding", ID: row.ID}, SubjectVersion: int64(row.Version + 1), Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM:project_manage"}, Data: map[string]any{"from_version_id": from, "to_version_id": target.ID, "definition_sha256": target.SHA256}})
	return e
}

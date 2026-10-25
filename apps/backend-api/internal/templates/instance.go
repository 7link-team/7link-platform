package templates

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"slices"
	"time"
)

type TemplateNodeRun struct {
	TemplateVersionID guid.ID                        `json:"template_version_id,omitempty"`
	DefinitionHash    string                         `json:"definition_hash,omitempty"`
	Parameters        json.RawMessage                `json:"parameters,omitempty"`
	BriefHash         string                         `json:"brief_hash,omitempty"`
	Gate              string                         `json:"gate,omitempty"`
	DecisionID        *guid.ID                       `json:"decision_id,omitempty"`
	Output            json.RawMessage                `json:"output,omitempty"`
	FrozenItems       []string                       `json:"frozen_items,omitempty"`
	Iteration         int                            `json:"iteration,omitempty"`
	NodeID            string                         `json:"node_id"`
	ExpansionKey      string                         `json:"expansion_key"`
	WorkItemID        *guid.ID                       `json:"work_item_id,omitempty"`
	State             string                         `json:"state"`
	Reason            string                         `json:"reason,omitempty"`
	AccountableID     guid.ID                        `json:"accountable_principal_id,omitempty"`
	Children          map[string]TemplateWorkflowDef `json:"children,omitempty"`
	ChildHash         string                         `json:"child_hash,omitempty"`
	Value             *bool                          `json:"value,omitempty"`
}
type TemplateInstanceView struct {
	TemplateState       string              `json:"template_state"`
	Version             int                 `json:"version"`
	BriefOverrides      map[string]any      `json:"brief_overrides"`
	EffectiveParameters map[string]any      `json:"effective_parameters"`
	OverridesApprovedBy *guid.ID            `json:"overrides_approved_by,omitempty"`
	ID                  guid.ID             `json:"id"`
	ProjectID           guid.ID             `json:"project_id"`
	TemplateVersionID   guid.ID             `json:"template_version_id"`
	Hash                string              `json:"hash"`
	ConstitutionID      guid.ID             `json:"constitution_id"`
	Workflow            TemplateWorkflowDef `json:"workflow"`
	Runs                []TemplateNodeRun   `json:"runs"`
}

// instantiateOn locks current IAM and the project through the delivery provider.
// Repeating the same pinned version returns its binding, while a different
// version requires a separate approved upgrade rather than silently replacing it.
func (m *Module) instantiateOn(ctx context.Context, tx *gorm.DB, id, project, accountable guid.ID) (TemplateInstanceView, error) {
	var empty TemplateInstanceView
	if tx == nil || accountable <= 0 || project <= 0 {
		return empty, ErrInvalid
	}
	if m.principals.Lock == nil {
		return empty, ErrUnavailable
	}
	// The work provider locks actor and accountable in sorted order. Acquire the
	// same complete set before project/policy fences to avoid reversing it here.
	principals := []guid.ID{authn.PrincipalIDFromContext(ctx), accountable}
	slices.Sort(principals)
	for _, principal := range slices.Compact(principals) {
		if principal <= 0 {
			return empty, ErrForbidden
		}
		if e := m.principals.Lock(ctx, tx, principal); e != nil {
			return empty, e
		}
	}
	if e := m.scope.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), project, "project_manage"); e != nil {
		return empty, e
	}
	r, e := m.versionOn(ctx, tx, id, "template_read")
	if e != nil {
		return empty, e
	}
	if r.State != "PUBLISHED" {
		return empty, ErrConflict
	}
	q := query.BindBindings(tx, m.names)
	existing, e := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ProjectID.Eq(project)).Take()
	if e == nil {
		if existing.TemplateVersionID != id || existing.SHA256 != r.SHA256 {
			return empty, ErrConflict
		}
		return m.instanceOn(ctx, tx, project)
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return empty, e
	}
	v, e := versionView(r)
	if e != nil {
		return empty, e
	}
	children, e := m.childrenOn(ctx, tx)
	if e != nil {
		return empty, e
	}
	if e = v.Definition.Validate(children); e != nil {
		return empty, e
	}
	if e = validateRuntime(v.Definition.Workflow, children, 0); e != nil {
		return empty, e
	}
	// Materialization is lazy: neither a gate nor an unresolved branch may
	// expose downstream work to ordinary assignment commands.
	runs := make([]TemplateNodeRun, 0, len(v.Definition.Workflow.Nodes))
	for _, node := range v.Definition.Workflow.Nodes {
		runs = append(runs, TemplateNodeRun{NodeID: node.ID, ExpansionKey: runtimeKey("", node.ID), State: "WAITING"})
	}
	runs[0].AccountableID = accountable
	runs[0].Children = map[string]TemplateWorkflowDef{}
	pinRuntimeChildren(v.Definition.Workflow, children, runs[0].Children)
	constitution, e := m.delivery.InitializeConstitutionOn(ctx, tx, r.TenantID, project, v.Definition.ConstitutionSeed)
	if e != nil {
		return empty, e
	}
	newID, e := m.ids.Next()
	if e != nil {
		return empty, e
	}
	raw, e := json.Marshal(runs)
	if e != nil {
		return empty, e
	}
	binding := model.Binding{ID: guid.ID(newID), TenantID: r.TenantID, ProjectID: project, TemplateVersionID: id, SHA256: r.SHA256, ConstitutionID: constitution, Runs: string(raw), CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: time.Now().UTC()}
	if e = q.WithContext(ctx).Create(&binding); e != nil {
		return empty, e
	}
	if e = m.eventOn(ctx, tx, r, project, "template.instantiated"); e != nil {
		return empty, e
	}
	return m.advanceOn(ctx, tx, project)
}
func (m *Module) instanceOn(ctx context.Context, tx *gorm.DB, project guid.ID) (TemplateInstanceView, error) {
	var empty TemplateInstanceView
	tenant := authn.TenantIDFromContext(ctx)
	if e := m.scope.AuthorizeOn(ctx, tx, tenant, project, "work_item_read"); e != nil {
		return empty, e
	}
	q := query.BindBindings(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return empty, ErrNotFound
	}
	if e != nil {
		return empty, e
	}
	versions := query.BindVersions(tx, m.names)
	version, e := versions.WithContext(ctx).Where(versions.TenantID.Eq(tenant), versions.ID.Eq(r.TemplateVersionID)).Take()
	if e != nil {
		return empty, e
	}
	if version.SHA256 != r.SHA256 {
		return empty, ErrConflict
	}
	v, e := versionView(version)
	if e != nil {
		return empty, e
	}
	var runs []TemplateNodeRun
	if e = json.Unmarshal([]byte(r.Runs), &runs); e != nil {
		return empty, e
	}
	for i, run := range runs {
		if run.WorkItemID != nil {
			state, e := m.delivery.WorkStatusOn(ctx, tx, tenant, project, *run.WorkItemID)
			if e != nil {
				return empty, e
			}
			runs[i].State = state
		}
	}
	parameters, overrides, e := resolveBindingParameters(v.Definition, r)
	if e != nil {
		return empty, e
	}
	v.Definition.Workflow.Parameters = parameters
	return TemplateInstanceView{TemplateState: v.State, Version: r.Version, BriefOverrides: overrides, EffectiveParameters: parameters, OverridesApprovedBy: r.OverridesApprovedBy, ID: r.ID, ProjectID: project, TemplateVersionID: r.TemplateVersionID, Hash: r.SHA256, ConstitutionID: r.ConstitutionID, Workflow: v.Definition.Workflow, Runs: runs}, nil
}

package templates

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
)

type TemplateVersionView struct {
	Name             string             `json:"name"`
	Origin           string             `json:"origin" enum:"FIRST_PARTY,TEAM"`
	BasedOnVersionID *guid.ID           `json:"based_on_version_id,omitempty"`
	BasedOnSHA256    string             `json:"based_on_sha256,omitempty"`
	ID               guid.ID            `json:"id"`
	Key              string             `json:"key"`
	State            string             `json:"state"`
	Definition       TemplateDefinition `json:"definition"`
	Hash             string             `json:"hash"`
	Version          int                `json:"version"`
}

func versionView(r *model.Version) (TemplateVersionView, error) {
	var d TemplateDefinition
	if e := json.Unmarshal([]byte(r.Definition), &d); e != nil {
		return TemplateVersionView{}, e
	}
	return TemplateVersionView{ID: r.ID, Name: r.Name, Origin: r.Origin, BasedOnVersionID: r.BasedOnVersionID, BasedOnSHA256: r.BasedOnSHA256, Key: r.Key, State: r.State, Definition: d, Hash: r.SHA256, Version: r.Version}, nil
}
func (m *Module) authorizeOn(ctx context.Context, tx *gorm.DB, permission string, owner guid.ID) error {
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	if tx == nil || tx.Statement == nil || tenant <= 0 || actor <= 0 {
		return ErrForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	if m.policy == nil || m.principals.Lock == nil {
		return ErrUnavailable
	}
	if e := m.principals.Lock(ctx, tx, actor); e != nil {
		return e
	}
	if m.principals.ActiveHumanByID == nil {
		return ErrUnavailable
	}
	human, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return e
	}
	if !active || human.ID != actor {
		return ErrForbidden
	}
	c, e := m.policy(ctx, tx, tenant, permission, actor)
	if e != nil {
		return e
	}
	if !c.AllowAll && !(owner == 0 && len(c.OwnerIDs) > 0) && !slices.Contains(c.OwnerIDs, owner.String()) {
		return ErrForbidden
	}
	return nil
}
func (m *Module) versionOn(ctx context.Context, tx *gorm.DB, id guid.ID, permission string) (*model.Version, error) {
	if e := m.authorizeOn(ctx, tx, permission, 0); e != nil {
		return nil, e
	}
	q := query.BindVersions(tx, m.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.ID.Eq(id), q.TenantID.Eq(authn.TenantIDFromContext(ctx))).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if e = m.authorizeOn(ctx, tx, permission, r.CreatedBy); e != nil {
		return nil, e
	}
	return r, nil
}
func (m *Module) childrenOn(ctx context.Context, tx *gorm.DB) (map[TemplateWorkflowRef]TemplateWorkflowDef, error) {
	c, e := m.policy(ctx, tx, authn.TenantIDFromContext(ctx), "template_read", authn.PrincipalIDFromContext(ctx))
	if e != nil {
		return nil, e
	}
	q := query.BindVersions(tx, m.names)
	d := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.State.In("PUBLISHED", "DEPRECATED"))
	if !c.AllowAll {
		owners := []driver.Valuer{}
		for _, s := range c.OwnerIDs {
			id, e := guid.RequireID(s)
			if e != nil {
				return nil, e
			}
			owners = append(owners, id)
		}
		d = d.Where(q.CreatedBy.In(owners...))
	}
	rows, e := d.Limit(1001).Find()
	if e != nil {
		return nil, e
	}
	if len(rows) > 1000 {
		return nil, ErrInvalid
	}
	out := map[TemplateWorkflowRef]TemplateWorkflowDef{}
	for _, r := range rows {
		v, e := versionView(r)
		if e != nil {
			return nil, e
		}
		ref := TemplateWorkflowRef{v.Definition.Workflow.WorkflowID, v.Definition.Workflow.Version}
		if prior, exists := out[ref]; exists {
			a, _ := json.Marshal(prior)
			b, _ := json.Marshal(v.Definition.Workflow)
			if string(a) != string(b) {
				return nil, ErrConflict
			}
		}
		out[ref] = v.Definition.Workflow
	}
	return out, nil
}
func (m *Module) draftOn(ctx context.Context, tx *gorm.DB, key string, d TemplateDefinition) (TemplateVersionView, error) {
	if e := m.authorizeOn(ctx, tx, "template_manage", authn.PrincipalIDFromContext(ctx)); e != nil {
		return TemplateVersionView{}, e
	}
	if len(key) == 0 || len(key) > 64 || strings.TrimSpace(key) != key {
		return TemplateVersionView{}, ErrInvalid
	}
	children, e := m.childrenOn(ctx, tx)
	if e != nil {
		return TemplateVersionView{}, e
	}
	if e = d.Validate(children); e != nil {
		return TemplateVersionView{}, e
	}
	id, e := m.ids.Next()
	if e != nil {
		return TemplateVersionView{}, e
	}
	h, e := d.Hash()
	if e != nil {
		return TemplateVersionView{}, e
	}
	b, e := json.Marshal(d)
	if e != nil {
		return TemplateVersionView{}, e
	}
	now := time.Now().UTC()
	actor := authn.PrincipalIDFromContext(ctx)
	r := model.Version{ID: guid.ID(id), TenantID: authn.TenantIDFromContext(ctx), Key: key, Semver: d.Semver, State: "DRAFT", Definition: string(b), SHA256: h, Version: 1, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}
	// Later versions of a copied template retain its immutable ancestry. The
	// legacy draft endpoint must not provide a way to drop inherited Human gates.
	versions := query.BindVersions(tx, m.names)
	lineage, err := versions.WithContext(ctx).Where(versions.TenantID.Eq(r.TenantID), versions.Key.Eq(key), versions.BasedOnVersionID.IsNotNull()).Order(versions.ID).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return TemplateVersionView{}, err
	}
	if lineage != nil {
		if err = m.authorizeOn(ctx, tx, "template_manage", lineage.CreatedBy); err != nil {
			return TemplateVersionView{}, err
		}
		r.Name, r.Origin, r.BasedOnVersionID, r.BasedOnSHA256 = lineage.Name, "TEAM", lineage.BasedOnVersionID, lineage.BasedOnSHA256
		if err = m.validateInheritedGatesOn(ctx, tx, &r, d); err != nil {
			return TemplateVersionView{}, err
		}
	}
	if e = query.BindVersions(tx, m.names).WithContext(ctx).Create(&r); e != nil {
		return TemplateVersionView{}, e
	}
	if e = m.eventOn(ctx, tx, &r, 0, "template.drafted"); e != nil {
		return TemplateVersionView{}, e
	}
	return versionView(&r)
}
func (m *Module) patchOn(ctx context.Context, tx *gorm.DB, id guid.ID, p TemplateWorkflowPatch) (TemplateVersionView, error) {
	r, e := m.versionOn(ctx, tx, id, "template_manage")
	if e != nil {
		return TemplateVersionView{}, e
	}
	if r.State != "DRAFT" {
		return TemplateVersionView{}, ErrConflict
	}
	v, e := versionView(r)
	if e != nil {
		return TemplateVersionView{}, e
	}
	children, e := m.childrenOn(ctx, tx)
	if e != nil {
		return TemplateVersionView{}, e
	}
	d, e := ApplyPatch(v.Definition, p, children)
	if e != nil {
		return TemplateVersionView{}, e
	}
	if e = m.validateInheritedGatesOn(ctx, tx, r, d); e != nil {
		return TemplateVersionView{}, e
	}
	h, e := d.Hash()
	if e != nil {
		return TemplateVersionView{}, e
	}
	b, e := json.Marshal(d)
	if e != nil {
		return TemplateVersionView{}, e
	}
	q := query.BindVersions(tx, m.names)
	result, e := q.WithContext(ctx).Where(q.ID.Eq(r.ID), q.Version.Eq(r.Version), q.State.Eq("DRAFT")).Updates(map[string]any{"definition": string(b), "definition_sha256": h, "version": r.Version + 1, "updated_by": authn.PrincipalIDFromContext(ctx), "updated_at": time.Now().UTC()})
	if e != nil {
		return TemplateVersionView{}, e
	}
	if result.RowsAffected != 1 {
		return TemplateVersionView{}, ErrConflict
	}
	r.Definition, r.SHA256, r.Version = string(b), h, r.Version+1
	if e = m.eventOn(ctx, tx, r, 0, "template.patched"); e != nil {
		return TemplateVersionView{}, e
	}
	return versionView(r)
}
func (m *Module) publishOn(ctx context.Context, tx *gorm.DB, id guid.ID, hash string) (TemplateVersionView, error) {
	r, e := m.versionOn(ctx, tx, id, "template_manage")
	if e != nil {
		return TemplateVersionView{}, e
	}
	if r.State != "DRAFT" || hash != r.SHA256 {
		return TemplateVersionView{}, ErrConflict
	}
	v, e := versionView(r)
	if e != nil {
		return TemplateVersionView{}, e
	}
	children, e := m.childrenOn(ctx, tx)
	if e != nil {
		return TemplateVersionView{}, e
	}
	// A workflow reference identifies one immutable published definition. A
	// concurrent collision is also rejected by readers before any expansion.
	if _, exists := children[TemplateWorkflowRef{v.Definition.Workflow.WorkflowID, v.Definition.Workflow.Version}]; exists {
		return TemplateVersionView{}, ErrConflict
	}
	if e = v.Definition.Validate(children); e != nil {
		return TemplateVersionView{}, e
	}
	if e = m.validateInheritedGatesOn(ctx, tx, r, v.Definition); e != nil {
		return TemplateVersionView{}, e
	}
	q := query.BindVersions(tx, m.names)
	res, e := q.WithContext(ctx).Where(q.ID.Eq(id), q.Version.Eq(r.Version), q.State.Eq("DRAFT")).Updates(map[string]any{"state": "PUBLISHED", "version": r.Version + 1, "updated_by": authn.PrincipalIDFromContext(ctx), "updated_at": time.Now().UTC()})
	if e != nil {
		return TemplateVersionView{}, e
	}
	if res.RowsAffected != 1 {
		return TemplateVersionView{}, ErrConflict
	}
	r.State, r.Version = "PUBLISHED", r.Version+1
	if e = m.eventOn(ctx, tx, r, 0, "template.published"); e != nil {
		return TemplateVersionView{}, e
	}
	return versionView(r)
}
func (m *Module) eventOn(ctx context.Context, tx *gorm.DB, r *model.Version, project guid.ID, kind string) error {
	a := audit.NewDomainEvent(ctx, r.TenantID, kind, "template_version", r.ID)
	a.Detail["definition_sha256"] = r.SHA256
	a.Detail["template_key"], a.Detail["origin"], a.Detail["based_on_version_id"], a.Detail["based_on_sha256"] = r.Key, r.Origin, r.BasedOnVersionID, r.BasedOnSHA256
	authority := "IAM:template_manage"
	if kind == "template.instantiated" || kind == "template.advanced" {
		authority = "IAM:template_read+project_manage"
	}
	a.Detail["authority_source"] = authority
	if e := m.audit(ctx, tx, a); e != nil {
		return e
	}
	_, e := m.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, ProjectID: project, Type: kind, Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: authority}, Subject: events.Subject{Kind: "template_version", ID: r.ID}, SubjectVersion: int64(r.Version), Data: map[string]any{"hash": r.SHA256, "template_key": r.Key, "version_id": r.ID, "semver": r.Semver, "origin": r.Origin, "based_on_version_id": r.BasedOnVersionID, "based_on_sha256": r.BasedOnSHA256}})
	return e
}

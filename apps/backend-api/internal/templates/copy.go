package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrKeyTaken       = errors.New("templates: key already exists")
	ErrNoNewerVersion = errors.New("templates: no higher published version")
	ErrMandatoryGate  = errors.New("templates: inherited mandatory Human gate changed")
)

func (m *Module) copySourceOn(ctx context.Context, tx *gorm.DB, id guid.ID) (*model.Version, error) {
	if err := m.authorizeOn(ctx, tx, "template_read", 0); err != nil {
		return nil, err
	}
	q := query.BindVersions(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.TenantID.In(authn.TenantIDFromContext(ctx), guid.ID(0))).Clauses(clause.Locking{Strength: "UPDATE"}).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if r.TenantID == 0 {
		if r.Origin != "FIRST_PARTY" {
			return nil, ErrNotFound
		}
	} else if err = m.authorizeOn(ctx, tx, "template_read", r.CreatedBy); err != nil {
		return nil, err
	}
	return r, nil
}

func (m *Module) copyOn(ctx context.Context, tx *gorm.DB, source guid.ID, key, name string) (TemplateVersionView, error) {
	empty := TemplateVersionView{}
	if source <= 0 || strings.TrimSpace(name) == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 200 {
		return empty, ErrInvalid
	}
	if err := m.authorizeOn(ctx, tx, "template_manage", authn.PrincipalIDFromContext(ctx)); err != nil {
		return empty, err
	}
	r, err := m.copySourceOn(ctx, tx, source)
	if err != nil {
		return empty, err
	}
	if r.State != "PUBLISHED" && r.State != "DEPRECATED" {
		return empty, ErrConflict
	}
	q := query.BindVersions(tx, m.names)
	count, err := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.Key.Eq(key)).Count()
	if err != nil {
		return empty, err
	}
	if count > 0 {
		return empty, ErrKeyTaken
	}
	v, err := versionView(r)
	if err != nil {
		return empty, err
	}
	if hash, err := v.Definition.Hash(); err != nil || hash != r.SHA256 {
		return empty, ErrConflict
	}
	// A copy has its own workflow identity, so publishing cannot collide with
	// or mutate the immutable source reference. All workflow content is retained.
	v.Definition.Workflow.WorkflowID = "team:" + authn.TenantIDFromContext(ctx).String() + ":" + key
	v.Definition.Workflow.Version = 1
	v.Definition.Workflow.Name = name
	created, err := m.draftOn(ctx, tx, key, v.Definition)
	if err != nil {
		return empty, err
	}
	_, err = q.WithContext(ctx).Where(q.ID.Eq(created.ID)).Updates(map[string]any{"name": name, "origin": "TEAM", "based_on_version_id": r.ID, "based_on_sha256": r.SHA256})
	if err != nil {
		return empty, err
	}
	created.Name, created.Origin, created.BasedOnVersionID, created.BasedOnSHA256 = name, "TEAM", &r.ID, r.SHA256
	copyRow := &model.Version{ID: created.ID, TenantID: authn.TenantIDFromContext(ctx), Key: key, Semver: created.Definition.Semver, State: "DRAFT", Version: created.Version, SHA256: created.Hash, Name: name, Origin: "TEAM", BasedOnVersionID: &r.ID, BasedOnSHA256: r.SHA256}
	if err = m.eventOn(ctx, tx, copyRow, 0, "template.copied"); err != nil {
		return empty, err
	}
	return created, nil
}

type deprecationInput struct {
	VersionID      string `path:"version_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
}

func (m *Module) registerCopyHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "template.version.deprecate", Method: "PUT", Path: "/template-versions/{version_id}/deprecation", Summary: "Deprecate a template version after a higher version is published", Tags: []string{"template-versions"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "template", Verb: "manage", FilterData: true}, m.deprecateHTTP)
}
func (m *Module) deprecateHTTP(ctx context.Context, in *deprecationInput) (*respx.Body[TemplateVersionView], error) {
	id, err := guid.RequireID(in.VersionID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.deprecate:"+in.VersionID, struct{}{}, func(tx *gorm.DB) (TemplateVersionView, error) { return m.deprecateOn(ctx, tx, id) })
}

func (m *Module) inheritedMandatoryGatesOn(ctx context.Context, tx *gorm.DB, row *model.Version) ([]TemplateNode, error) {
	if row.BasedOnVersionID == nil {
		return nil, nil
	}
	q := query.BindVersions(tx, m.names)
	source, err := q.WithContext(ctx).Where(q.ID.Eq(*row.BasedOnVersionID), q.TenantID.In(row.TenantID, guid.ID(0))).Take()
	if err != nil {
		return nil, err
	}
	if source.SHA256 != row.BasedOnSHA256 {
		return nil, ErrConflict
	}
	var original TemplateDefinition
	if json.Unmarshal([]byte(source.Definition), &original) != nil {
		return nil, ErrUnavailable
	}
	if digest, err := original.Hash(); err != nil || digest != row.BasedOnSHA256 {
		return nil, ErrConflict
	}
	return original.Workflow.Nodes, nil
}

func (m *Module) validateInheritedGatesOn(ctx context.Context, tx *gorm.DB, row *model.Version, d TemplateDefinition) error {
	gates, err := m.inheritedMandatoryGatesOn(ctx, tx, row)
	if err != nil {
		return err
	}
	for _, gate := range gates {
		if !gate.Mandatory {
			continue
		}
		found := false
		for _, candidate := range d.Workflow.Nodes {
			if candidate.ID == gate.ID && candidate.Kind == "HUMAN_GATE" && candidate.Mandatory && candidate.Gate == gate.Gate {
				found = true
				break
			}
		}
		if !found {
			return ErrMandatoryGate
		}
	}
	return nil
}

func higherSemver(candidate, current string) bool {
	var a, b [3]int
	if n, e := fmt.Sscanf(candidate, "%d.%d.%d", &a[0], &a[1], &a[2]); e != nil || n != 3 {
		return false
	}
	if n, e := fmt.Sscanf(current, "%d.%d.%d", &b[0], &b[1], &b[2]); e != nil || n != 3 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func (m *Module) deprecateOn(ctx context.Context, tx *gorm.DB, id guid.ID) (TemplateVersionView, error) {
	empty := TemplateVersionView{}
	r, err := m.versionOn(ctx, tx, id, "template_manage")
	if err != nil {
		return empty, err
	}
	if r.State != "PUBLISHED" {
		return empty, ErrConflict
	}
	q := query.BindVersions(tx, m.names)
	versions, err := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.Key.Eq(r.Key), q.State.Eq("PUBLISHED")).Find()
	if err != nil {
		return empty, err
	}
	found := false
	for _, v := range versions {
		if higherSemver(v.Semver, r.Semver) {
			found = true
			break
		}
	}
	if !found {
		return empty, ErrNoNewerVersion
	}
	result, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.Version.Eq(r.Version), q.State.Eq("PUBLISHED")).Updates(map[string]any{"state": "DEPRECATED", "version": r.Version + 1, "updated_by": authn.PrincipalIDFromContext(ctx), "updated_at": time.Now().UTC()})
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrConflict
	}
	r.State, r.Version = "DEPRECATED", r.Version+1
	if err = m.eventOn(ctx, tx, r, 0, "template.version_deprecated"); err != nil {
		return empty, err
	}
	return versionView(r)
}

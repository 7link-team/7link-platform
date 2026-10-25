package templates

import (
	"context"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type ProviderPage struct {
	Items      []ProviderView `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}
type ProviderAdapterPage struct {
	Items      []ProviderAdapterView `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more"`
}
type providerListInput struct {
	Kind   string `query:"kind" enum:"VIDEO_GEN,IMAGE_GEN,TTS,MUSIC_GEN,ASR_ALIGN,VLM_JUDGE,LLM_JUDGE"`
	State  string `query:"state" enum:"ENABLED,DISABLED,UNAVAILABLE"`
	Cursor string `query:"cursor" maxLength:"19"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort   string `query:"sort" enum:"id" default:"id"`
}
type providerGetInput struct {
	ID string `path:"provider_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type providerCreateInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           ProviderDraft
}
type providerPatchInput struct {
	ID             string `path:"provider_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           ProviderPatch
}
type providerHealthInput struct {
	ID             string `path:"provider_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
}
type providerBindingInput struct {
	ProjectID string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type providerBindingChangeInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Providers map[string]guid.ID `json:"providers"`
	}
}

func (m *Module) registerProviderHTTP(api huma.API) {
	read := authz.Guard{Resource: "template", Verb: "read", FilterData: true}
	manage := authz.Guard{Resource: "integration", Verb: "manage", FilterData: true}
	project := authz.Guard{Resource: "project", Verb: "manage", FilterData: true}
	op := func(id, method, path, tag, summary string) huma.Operation {
		status := 200
		if id == "template.provider-create" {
			status = 201
		}
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{tag}, Summary: summary, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("template.provider-adapters", "GET", "/template-provider-adapters", "template-provider-adapters", "List installed callable adapters and their licence notices"), read, m.providerAdaptersHTTP) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	authz.Register(m.registrar, api, op("template.provider-list", "GET", "/template-providers", "template-providers", "List provider configuration without credentials"), read, m.providerListHTTP)
	authz.Register(m.registrar, api, op("template.provider-get", "GET", "/template-providers/{provider_id}", "template-providers", "Read provider health, accepted licence and public configuration"), read, m.providerGetHTTP) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	authz.Register(m.registrar, api, op("template.provider-create", "POST", "/template-providers", "template-providers", "Configure a registered adapter and seal its credential"), manage, m.providerCreateHTTP)
	authz.Register(m.registrar, api, op("template.provider-patch", "PATCH", "/template-providers/{provider_id}", "template-providers", "Update provider configuration under current Human authority"), manage, m.providerPatchHTTP)
	authz.Register(m.registrar, api, op("template.provider-health", "POST", "/template-providers/{provider_id}/health-checks", "template-providers", "Run the actual adapter health check"), manage, m.providerHealthHTTP)
	authz.Register(m.registrar, api, op("template.provider-binding-get", "GET", "/entities/{project_id}/template-binding/providers", "entities", "Read selected project providers and configuration versions"), read, m.providerBindingGetHTTP)
	authz.Register(m.registrar, api, op("template.provider-binding-set", "PUT", "/entities/{project_id}/template-binding/providers", "entities", "Select currently healthy providers without rewriting generated outputs"), project, m.providerBindingSetHTTP)
}
func (m *Module) providerAdaptersHTTP(ctx context.Context, _ *struct{}) (*respx.Body[ProviderAdapterPage], error) {
	out := ProviderAdapterPage{Items: []ProviderAdapterView{}}
	err := m.transaction(ctx, func(tx *gorm.DB) error {
		if err := m.authorizeOn(ctx, tx, "template_read", 0); err != nil {
			return err
		}
		for _, a := range m.providerAdapters {
			out.Items = append(out.Items, a.Definition)
		}
		slices.SortFunc(out.Items, func(a, b ProviderAdapterView) int { return strings.Compare(a.Key, b.Key) })
		return nil
	})
	if err != nil {
		return nil, templateError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) providerListHTTP(ctx context.Context, in *providerListInput) (*respx.Body[ProviderPage], error) {
	after, err := calibrationCursor(in.Cursor)
	if err != nil || in.Limit < 1 || in.Limit > 100 || in.Sort != "id" {
		return nil, templateError(ctx, ErrInvalid)
	}
	out := ProviderPage{Items: []ProviderView{}}
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		if err := m.authorizeOn(ctx, tx, "template_read", 0); err != nil {
			return err
		}
		q := query.BindProviders(tx, m.names)
		d := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Gt(after))
		if in.Kind != "" {
			d = d.Where(q.Kind.Eq(in.Kind))
		}
		if in.State != "" {
			d = d.Where(q.State.Eq(in.State))
		}
		rows, e := d.Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
		}
		for _, row := range rows {
			if e = m.authorizeOn(ctx, tx, "template_read", row.CreatedBy); e != nil {
				return e
			}
			out.Items = append(out.Items, m.providerView(row))
		}
		if out.HasMore {
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		return nil
	})
	if err != nil {
		return nil, templateError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) providerGetHTTP(ctx context.Context, in *providerGetInput) (*respx.Body[ProviderView], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	var out ProviderView
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		row, e := m.providerOn(ctx, tx, id, "template_read")
		if e != nil {
			return e
		}
		out = m.providerView(row)
		return nil
	})
	if err != nil {
		return nil, templateError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) providerCreateHTTP(ctx context.Context, in *providerCreateInput) (*respx.Body[ProviderView], error) {
	return command(m, ctx, in.IdempotencyKey, "template.provider-create", in.Body, func(tx *gorm.DB) (ProviderView, error) { return m.createProviderOn(ctx, tx, in.Body) }, func(tx *gorm.DB) error {
		return m.authorizeOn(ctx, tx, "integration_manage", authn.PrincipalIDFromContext(ctx))
	})
}
func (m *Module) providerPatchHTTP(ctx context.Context, in *providerPatchInput) (*respx.Body[ProviderView], error) {
	id, err := guid.RequireID(in.ID)
	version, e := bindingVersionHeader(in.IfMatch)
	if err != nil || e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	input := struct {
		Version int
		Patch   ProviderPatch
	}{version, in.Body}
	return command(m, ctx, in.IdempotencyKey, "template.provider-patch:"+in.ID, input, func(tx *gorm.DB) (ProviderView, error) { return m.patchProviderOn(ctx, tx, id, version, in.Body) }, func(tx *gorm.DB) error { _, e := m.providerOn(ctx, tx, id, "integration_manage"); return e })
}
func (m *Module) providerHealthHTTP(ctx context.Context, in *providerHealthInput) (*respx.Body[ProviderView], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	var snapshot *model.TemplateProvider
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		var e error
		snapshot, e = m.providerOn(ctx, tx, id, "integration_manage")
		return e
	})
	if err != nil {
		return nil, templateError(ctx, err)
	}
	a, exists := m.providerAdapters[snapshot.AdapterKey]
	if !exists {
		return nil, templateError(ctx, ErrProviderAdapter)
	}
	connection, cleanup, err := m.providerConnection(snapshot)
	if err != nil {
		return nil, templateError(ctx, ErrUnavailable)
	}
	defer cleanup()
	work, cancel := context.WithTimeout(ctx, 30*time.Second)
	health, checkErr := a.Health(work, connection)
	cancel()
	state, note := "UNAVAILABLE", "Actual adapter health check failed or was unavailable"
	if checkErr == nil && health.Available {
		state = "ENABLED"
		note = "Actual adapter health check completed"
	}
	// Adapter errors/notes may contain remote request data; retain only the
	// observed availability and neutral wording, never a credential-bearing error.
	return command(m, ctx, in.IdempotencyKey, "template.provider-health:"+in.ID, in.ID, func(tx *gorm.DB) (ProviderView, error) {
		row, e := m.providerOn(ctx, tx, id, "integration_manage")
		if e != nil {
			return ProviderView{}, e
		}
		if row.Version != snapshot.Version {
			return ProviderView{}, ErrConflict
		}
		if row.State == "DISABLED" {
			state = "DISABLED"
		}
		if providerLicenseHash(a) != row.LicenseSHA256 {
			state = "UNAVAILABLE"
			note = "The current adapter licence notice requires acceptance" //nolint:misspell // Preserve the published domain or serialized contract spelling.
		}
		now := time.Now().UTC()
		result := tx.WithContext(ctx).Table(m.names.Providers.String()).Where("tenant_id=? AND id=? AND version=?", row.TenantID, id, row.Version).Updates(map[string]any{"state": state, "health_note": note, "health_checked_at": now, "version": row.Version + 1})
		if result.Error != nil {
			return ProviderView{}, result.Error
		}
		if result.RowsAffected != 1 {
			return ProviderView{}, ErrConflict
		}
		row.State = state
		row.HealthNote = note
		row.HealthCheckedAt = &now
		row.Version++
		if e = m.providerEventOn(ctx, tx, row.TenantID, id, "template.provider_health_checked", map[string]any{"state": state, "version": row.Version}); e != nil {
			return ProviderView{}, e
		}
		return m.providerView(row), nil
	}, func(tx *gorm.DB) error { _, e := m.providerOn(ctx, tx, id, "integration_manage"); return e })
}
func (m *Module) providerBindingGetHTTP(ctx context.Context, in *providerBindingInput) (*respx.Body[ProviderBindingsView], error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	var out ProviderBindingsView
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		view, err := m.instanceOn(ctx, tx, project)
		if err != nil {
			return err
		}
		out, err = m.providerBindingsOn(ctx, tx, &model.Binding{TenantID: authn.TenantIDFromContext(ctx), ID: view.ID, ProjectID: project, Version: view.Version})
		return err
	})
	if e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) providerBindingSetHTTP(ctx context.Context, in *providerBindingChangeInput) (*respx.Body[ProviderBindingsView], error) {
	project, e := guid.RequireID(in.ProjectID)
	version, e2 := bindingVersionHeader(in.IfMatch)
	if e != nil || e2 != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	input := struct {
		Version   int
		Providers map[string]guid.ID
	}{version, in.Body.Providers}
	return command(m, ctx, in.IdempotencyKey, "template.provider-binding:"+in.ProjectID, input, func(tx *gorm.DB) (ProviderBindingsView, error) {
		return m.bindProvidersOn(ctx, tx, project, version, in.Body.Providers)
	}, func(tx *gorm.DB) error { _, e := m.studioAuthorizeOn(ctx, tx, project); return e })
}

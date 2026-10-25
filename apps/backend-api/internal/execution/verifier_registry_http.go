package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type verifierListInput struct {
	TenantID     string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor       string `query:"cursor" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit        int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	State        string `query:"state" enum:",ACTIVE,ARCHIVED"`
	WorkItemType string `query:"work_item_type" maxLength:"16"`
	RiskLevel    string `query:"risk_level" enum:",LOW,MEDIUM,HIGH"`
	ProjectID    string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type VerifierIDInput struct {
	VerifierID string `path:"verifier_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type verifierCreateInput struct {
	TenantID       string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           TenantVerifierDefinition
}
type VerifierPatch struct {
	Name       *string         `json:"name,omitempty" minLength:"1" maxLength:"200"`
	State      *string         `json:"state,omitempty" enum:"ACTIVE,ARCHIVED"`
	AppliesTo  *VerifierPolicy `json:"applies_to,omitempty"`
	Required   *bool           `json:"required,omitempty"`
	Spec       *VerifierSpec   `json:"spec,omitempty"`
	TimeoutMS  *int64          `json:"timeout_ms,omitempty" minimum:"1000" maximum:"3600000"`
	ChangeNote *string         `json:"change_note,omitempty" maxLength:"2000"`
}
type verifierPatchInput struct {
	VerifierIDInput
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           VerifierPatch
}
type VerifierPage struct {
	Items      []VerifierView `json:"items"`
	HasMore    bool           `json:"has_more"`
	NextCursor string         `json:"next_cursor,omitempty"`
}
type VerifierVersionPage struct {
	Items      []VerifierVersionView `json:"items"`
	HasMore    bool                  `json:"has_more"`
	NextCursor string                `json:"next_cursor,omitempty"`
}
type verifierVersionsInput struct {
	VerifierIDInput
	Cursor string `query:"cursor" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type verifierMutationOutput struct {
	Status       int
	Replayed     string `header:"Idempotent-Replayed"`
	CacheControl string `header:"Cache-Control"`
	Body         respx.Envelope[VerifierView]
}

func (m *Module) verifierOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.writer()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	var r model.Verifier
	err = db.WithContext(ctx).Table(m.names.Verifiers.String()).Select("tenant_id").Where("id = ?", id).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: r.TenantID}, err == nil, err
}
func (m *Module) registerVerifierRegistryHTTP(api huma.API) error {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"verifiers"}, Errors: []int{400, 401, 403, 404, 409, 422, 503}}
	}
	read := authz.Guard{Resource: "policy", Verb: "read"}
	write := authz.Guard{Resource: "verifier", Verb: "manage"}
	authz.Register(m.registrar, api, op("verifier.list", "GET", "/verifiers", "List tenant verifier definitions and matching policies"), read, m.listVerifiersHTTP)
	create := op("verifier.create", "POST", "/verifiers", "Create a versioned tenant verifier")
	create.DefaultStatus = 201
	authz.Register(m.registrar, api, create, write, m.createVerifierHTTP)
	authz.RegisterOwned(m.registrar, api, op("verifier.get", "GET", "/verifiers/{verifier_id}", "Read verifier and current immutable definition"), "verifier_id", m.verifierOwner, read, m.getVerifierHTTP)
	authz.RegisterOwned(m.registrar, api, op("verifier.update", "PATCH", "/verifiers/{verifier_id}", "Change verifier metadata or append a definition version"), "verifier_id", m.verifierOwner, write, m.patchVerifierHTTP)
	authz.RegisterOwned(m.registrar, api, op("verifier.version.list", "GET", "/verifiers/{verifier_id}/versions", "Read immutable verifier version history"), "verifier_id", m.verifierOwner, read, m.verifierVersionsHTTP)
	return authz.RegisterRequestContract(api, "/verifiers/{verifier_id}")
}
func (m *Module) getVerifierHTTP(ctx context.Context, in *VerifierIDInput) (*respx.Body[VerifierView], error) {
	id, err := guid.RequireID(in.VerifierID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out VerifierView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if err := m.verifierAuthorityOn(ctx, tx, tenant, "policy_read"); err != nil {
			return err
		}
		var r model.Verifier
		if err := tx.Table(m.names.Verifiers.String()).Where("tenant_id = ? AND id = ?", tenant, id).First(&r).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		var err error
		out, err = m.verifierViewOn(ctx, tx, r)
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listVerifiersHTTP(ctx context.Context, in *verifierListInput) (*respx.Body[VerifierPage], error) {
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	cursor, project := guid.ID(0), guid.ID(0)
	if in.Cursor != "" {
		cursor, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, httpError(ctx, ErrInvalidArgument)
		}
	}
	if in.ProjectID != "" {
		project, err = guid.RequireID(in.ProjectID)
		if err != nil {
			return nil, httpError(ctx, ErrInvalidArgument)
		}
	}
	if in.Limit < 1 || in.Limit > 100 || !validVerifierPolicy(VerifierPolicy{WorkItemTypes: nonemptyVerifierFilter(in.WorkItemType), RiskLevels: nonemptyVerifierFilter(in.RiskLevel)}) {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	out := VerifierPage{Items: []VerifierView{}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if err := m.verifierAuthorityOn(ctx, tx, tenant, "policy_read"); err != nil {
			return err
		}
		for {
			var rows []model.Verifier
			q := tx.Table(m.names.Verifiers.String()).Where("tenant_id = ? AND id > ?", tenant, cursor)
			if in.State != "" {
				q = q.Where("state = ?", in.State)
			}
			if err := q.Order("id ASC").Limit(100).Find(&rows).Error; err != nil {
				return err
			}
			for _, r := range rows {
				cursor = r.ID
				if in.State != "" && r.State != in.State {
					continue
				}
				v, err := m.verifierViewOn(ctx, tx, r)
				if err != nil {
					return err
				}
				p := v.AppliesTo
				if project > 0 && len(p.ProjectIDs) > 0 && !slices.Contains(p.ProjectIDs, project) {
					continue
				}
				if in.WorkItemType != "" && len(p.WorkItemTypes) > 0 && !slices.Contains(p.WorkItemTypes, in.WorkItemType) {
					continue
				}
				if in.RiskLevel != "" && len(p.RiskLevels) > 0 && !slices.Contains(p.RiskLevels, in.RiskLevel) {
					continue
				}
				if len(out.Items) == in.Limit {
					out.HasMore = true
					out.NextCursor = out.Items[len(out.Items)-1].ID.String()
					return nil
				}
				out.Items = append(out.Items, v)
			}
			if len(rows) < 100 {
				return nil
			}
		}
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func nonemptyVerifierFilter(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}
func (m *Module) verifierVersionsHTTP(ctx context.Context, in *verifierVersionsInput) (*respx.Body[VerifierVersionPage], error) {
	id, err := guid.RequireID(in.VerifierID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	if in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	cursor := guid.ID(0)
	if in.Cursor != "" {
		cursor, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, httpError(ctx, ErrInvalidArgument)
		}
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	out := VerifierVersionPage{Items: []VerifierVersionView{}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if err := m.verifierAuthorityOn(ctx, tx, tenant, "policy_read"); err != nil {
			return err
		}
		var count int64
		if err := tx.Table(m.names.Verifiers.String()).Where("tenant_id = ? AND id = ?", tenant, id).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return ErrNotFound
		}
		var rows []model.VerifierVersion
		if err := tx.Table(m.names.VerifierVersions.String()).Where("tenant_id = ? AND verifier_id = ? AND id > ?", tenant, id, cursor).Order("id ASC").Limit(in.Limit + 1).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		for _, r := range rows {
			v, err := verifierVersionView(r)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, v)
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) mutateVerifierHTTP(ctx context.Context, id guid.ID, expected int, operation, key string, body any, apply func(*gorm.DB) (VerifierView, error)) (*verifierMutationOutput, error) {
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.commentIdempotency == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	tenant := authn.TenantIDFromContext(ctx)
	method := http.MethodPost
	if id > 0 {
		method = http.MethodPatch
	}
	digest, err := idempotency.HashRequest(method, "/verifiers", map[string]string{"verifier_id": id.String(), "version": strconv.Itoa(expected)}, body)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	k := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: digest}
	if k.Validate() != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	var result idempotency.Result
	var replay bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.verifierAuthorityOn(ctx, tx, tenant, "verifier_manage"); err != nil {
			return err
		}
		var err error
		result, replay, err = m.commentIdempotency.Do(ctx, tx, k, func(tx *gorm.DB) (idempotency.Result, error) {
			view, err := apply(tx)
			if err != nil {
				return idempotency.Result{}, err
			}
			data, err := respx.MarshalEnvelope(ctx, respx.OK(ctx, view).Body)
			status := 200
			if id == 0 {
				status = 201
			}
			return idempotency.Result{StatusCode: status, Body: data, ResourceKind: "verifier", ResourceID: view.ID}, err
		})
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	out := &verifierMutationOutput{Status: result.StatusCode, CacheControl: "private, no-store"}
	if err = json.Unmarshal(result.Body, &out.Body); err != nil {
		return nil, httpError(ctx, err)
	}
	if replay {
		out.Replayed = "true"
	}
	return out, nil
}
func (m *Module) createVerifierHTTP(ctx context.Context, in *verifierCreateInput) (*verifierMutationOutput, error) {
	return m.mutateVerifierHTTP(ctx, 0, 0, "verifier.create", in.IdempotencyKey, in.Body, func(tx *gorm.DB) (VerifierView, error) {
		return m.putVerifierOn(ctx, tx, authn.TenantIDFromContext(ctx), 0, 0, in.Body)
	})
}
func (m *Module) patchVerifierHTTP(ctx context.Context, in *verifierPatchInput) (*verifierMutationOutput, error) {
	id, err := guid.RequireID(in.VerifierID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	version, err := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || version < 1 {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	return m.mutateVerifierHTTP(ctx, id, version, "verifier.update", in.IdempotencyKey, in.Body, func(tx *gorm.DB) (VerifierView, error) {
		var r model.Verifier
		if err := tx.Table(m.names.Verifiers.String()).Where("tenant_id = ? AND id = ?", authn.TenantIDFromContext(ctx), id).First(&r).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return VerifierView{}, ErrNotFound
			}
			return VerifierView{}, err
		}
		if r.Version != version {
			return VerifierView{}, ErrConflict
		}
		old, err := m.verifierViewOn(ctx, tx, r)
		if err != nil {
			return VerifierView{}, err
		}
		v := TenantVerifierDefinition{Key: old.Key, Name: old.Name, Layer: old.Layer, State: old.State, AppliesTo: old.AppliesTo, Required: old.Required, Spec: old.Current.Spec, TimeoutMS: old.Current.TimeoutMS}
		if in.Body.Name != nil {
			v.Name = *in.Body.Name
		}
		if in.Body.State != nil {
			v.State = *in.Body.State
		}
		if in.Body.AppliesTo != nil {
			v.AppliesTo = *in.Body.AppliesTo
		}
		if in.Body.Required != nil {
			v.Required = *in.Body.Required
		}
		if in.Body.Spec != nil {
			v.Spec = *in.Body.Spec
		}
		if in.Body.TimeoutMS != nil {
			v.TimeoutMS = *in.Body.TimeoutMS
		}
		if in.Body.ChangeNote != nil {
			v.ChangeNote = *in.Body.ChangeNote
		}
		return m.putVerifierOn(ctx, tx, authn.TenantIDFromContext(ctx), id, version, v)
	})
}

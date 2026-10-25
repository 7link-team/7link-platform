package fleet

import (
	fleeti18n "7link.com/platform/internal/fleet/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"strings"
)

type priceListInput struct {
	Cursor string `query:"cursor" maxLength:"19"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type ModelPriceResourceInput struct {
	Runtime string `path:"runtime_kind" enum:"CODEX,CLAUDE_CODE"`
	Model   string `path:"model" minLength:"1" maxLength:"160"`
	IfMatch string `header:"If-Match"`
}
type ModelPriceAmounts struct {
	Input      string `json:"input_micros_per_mtok" pattern:"^(0|[1-9][0-9]{0,18})$" maxLength:"19"`
	Output     string `json:"output_micros_per_mtok" pattern:"^(0|[1-9][0-9]{0,18})$" maxLength:"19"`
	CacheRead  string `json:"cache_read_micros_per_mtok" pattern:"^(0|[1-9][0-9]{0,18})$" maxLength:"19"`
	CacheWrite string `json:"cache_write_micros_per_mtok" pattern:"^(0|[1-9][0-9]{0,18})$" maxLength:"19"`
}
type priceWriteInput struct {
	ModelPriceResourceInput
	Body ModelPriceAmounts
}
type priceOutput struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[ModelPriceView]
}

func (m *Module) registerModelPricesHTTP(api huma.API) error {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-price.list", Method: http.MethodGet, Path: "/model-prices", Summary: "List configured token prices", Tags: []string{"machines"}, Errors: []int{401, 403, 422, 503}}, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, m.modelPricesHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-price.update", Method: http.MethodPut, Path: "/model-prices/{runtime_kind}/{model}", Summary: "Set a versioned token price", Tags: []string{"machines"}, Errors: []int{401, 403, 409, 422, 428, 503}}, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, m.putModelPriceHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-price.delete", Method: http.MethodDelete, Path: "/model-prices/{runtime_kind}/{model}", Summary: "Remove a configured token price", Tags: []string{"machines"}, DefaultStatus: 204, Errors: []int{401, 403, 409, 422, 428, 503}}, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, m.deleteModelPriceHTTP)
	for _, path := range []string{"/model-prices", "/model-prices/{runtime_kind}/{model}"} {
		if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+path); err != nil {
			return err
		}
	}
	return nil
}
func priceVersion(ctx context.Context, raw string) (int, error) {
	if raw == "" {
		return 0, respx.ErrStatus(ctx, 428, 140428, fleeti18n.Invalid, nil)
	}
	v, err := strconv.Atoi(strings.Trim(raw, "\""))
	if err != nil || v < 0 {
		return 0, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	return v, nil
}
func (m *Module) modelPricesHTTP(ctx context.Context, in *priceListInput) (*respx.Body[ModelPricePage], error) {
	cursor := guid.ID(0)
	var err error
	if in.Cursor != "" {
		cursor, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out ModelPricePage
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.ModelPricesOn(ctx, tx, authn.TenantIDFromContext(ctx), cursor, in.Limit)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) priceHTTPCommand(ctx context.Context, in ModelPriceResourceInput, body *ModelPriceWrite) (ModelPriceView, error) {
	v, err := priceVersion(ctx, in.IfMatch)
	if err != nil {
		return ModelPriceView{}, err
	}
	db, err := m.writer()
	if err != nil {
		return ModelPriceView{}, fleetHTTPError(ctx, err)
	}
	var out ModelPriceView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.WriteModelPriceOn(ctx, tx, authn.TenantIDFromContext(ctx), in.Runtime, in.Model, v, body)
		return e
	})
	if errors.Is(err, ErrVersionConflict) {
		return out, respx.ErrStatus(ctx, 409, 140409, fleeti18n.Conflict, nil)
	}
	if err != nil {
		return out, fleetHTTPError(ctx, err)
	}
	return out, nil
}
func (m *Module) putModelPriceHTTP(ctx context.Context, in *priceWriteInput) (*priceOutput, error) {
	var amounts ModelPriceWrite
	for _, pair := range []struct {
		raw   string
		value *int64
	}{{in.Body.Input, &amounts.Input}, {in.Body.Output, &amounts.Output}, {in.Body.CacheRead, &amounts.CacheRead}, {in.Body.CacheWrite, &amounts.CacheWrite}} {
		value, e := strconv.ParseInt(pair.raw, 10, 64)
		if e != nil || value < 0 {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
		*pair.value = value
	}
	out, err := m.priceHTTPCommand(ctx, in.ModelPriceResourceInput, &amounts)
	if err != nil {
		return nil, err
	}
	return &priceOutput{ETag: strconv.Quote(strconv.Itoa(out.Version)), Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) deleteModelPriceHTTP(ctx context.Context, in *ModelPriceResourceInput) (*struct{}, error) {
	_, err := m.priceHTTPCommand(ctx, *in, nil)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

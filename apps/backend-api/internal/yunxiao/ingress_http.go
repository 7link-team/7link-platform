package yunxiao

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type ingressHTTPInput struct {
	ConnectionID  string `path:"connection_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	DeliveryKey   string `header:"X-Delivery-Key" required:"true" minLength:"1" maxLength:"128" doc:"Stable source delivery identity; reusing it with different bytes is rejected."`
	CodeupToken   string `header:"X-Codeup-Token" hidden:"true"`
	FlowSignature string `header:"X-Flow-Signature" hidden:"true"`
	RawBody       []byte `contentType:"application/json"`
}
type ingressHTTPOutput struct {
	Status int
	Body   respx.Envelope[IngressReceipt]
}

type ingressObservationListInput struct {
	MappingPath
	Cursor string `query:"cursor" maxLength:"200"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Sort   string `query:"sort" default:"id" enum:"id"`
}
type IngressObservationView struct {
	ID          guid.ID            `json:"id"`
	Outcome     string             `json:"outcome" enum:"REFERENCE_ONLY"`
	Observation IngressObservation `json:"observation"`
}

func (m *Module) registerIngressHTTP(api huma.API) {
	for _, source := range []string{"codeup", "flow"} {
		op := huma.Operation{OperationID: "integration.webhook." + source, Method: "POST", Path: "/integration-webhooks/{connection_id}/" + source, Summary: "Receive signed " + source + " observations", Description: "Authenticates the configured connection secret before durable storage. Codeup: X-Codeup-Token constant-time token comparison. Flow custom callback: X-Flow-Signature is sha256= followed by the hexadecimal HMAC-SHA256 of the exact raw JSON bytes. X-Delivery-Key is mandatory. Observations cannot substitute local acceptance or an undeclared frozen verifier.", Tags: []string{"integration-webhooks"}, DefaultStatus: 202, MaxBodyBytes: 1 << 20, Errors: []int{400, 401, 409, 413, 422, 500, 503}}
		// Huma must retain exact bytes and leave source-body validation until
		// after signature authentication, rather than interpret []byte as base64.
		op.SkipValidateBody = true
		op.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: "object", AdditionalProperties: true}}}}
		if source == "codeup" {
			authz.RegisterPublic(api, op, m.codeupIngressHTTP)
		} else {
			authz.RegisterPublic(api, op, m.flowIngressHTTP)
		}
		registered := api.OpenAPI().Paths[op.Path].Post
		if accepted := registered.Responses["202"]; accepted != nil {
			replayed := *accepted
			replayed.Description = "Authenticated delivery already received with the same payload"
			registered.Responses["200"] = &replayed
		}
	}
	authz.Register(m.registrar, api, huma.Operation{OperationID: "integration.observation.list", Method: "GET", Path: "/integration-connections/{connection_id}/project-mappings/{mapping_id}/observations", Summary: "Read signed reference evidence and external gate mirrors", Tags: []string{"integration-connections"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.Guard{Resource: "integration", Verb: "manage", FilterData: true}, m.ingressObservationsHTTP)
}

func (m *Module) codeupIngressHTTP(ctx context.Context, in *ingressHTTPInput) (*ingressHTTPOutput, error) {
	return m.ingressHTTP(ctx, in, "CODEUP", in.CodeupToken)
}
func (m *Module) flowIngressHTTP(ctx context.Context, in *ingressHTTPInput) (*ingressHTTPOutput, error) {
	return m.ingressHTTP(ctx, in, "FLOW", in.FlowSignature)
}
func (m *Module) ingressHTTP(ctx context.Context, in *ingressHTTPInput, source, signature string) (*ingressHTTPOutput, error) {
	id, err := guid.RequireID(in.ConnectionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	receipt, err := m.receiveIngress(ctx, id, source, in.DeliveryKey, signature, in.RawBody)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	status := 202
	if receipt.Replayed {
		status = 200
	}
	return &ingressHTTPOutput{Status: status, Body: respx.OK(ctx, receipt).Body}, nil
}

func (m *Module) ingressObservationsHTTP(ctx context.Context, in *ingressObservationListInput) (*respx.Body[Page[IngressObservationView]], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	connection, e := guid.RequireID(in.ConnectionID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	mapping, e := guid.RequireID(in.MappingID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	last, e := parseCursor(in.Cursor, tenant, mapping)
	if e != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalid)
	}
	db := m.database()
	if db == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := Page[IngressObservationView]{Items: []IngressObservationView{}}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.authorityOn(ctx, tx, tenant); err != nil {
			return err
		}
		// Discover project before locking mapping, preserving project-before-child order.
		q := query.BindProjectMappings(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(mapping), q.ConnectionID.Eq(connection)).First()
		if err != nil {
			return err
		}
		if m.projects.AuthorizeOn == nil {
			return ErrUnavailable
		}
		if err = m.projects.AuthorizeOn(ctx, tx, tenant, r.ProjectID, "project_manage"); err != nil {
			return err
		}
		r, err = m.mappingOn(ctx, tx, tenant, mapping)
		if err != nil {
			return err
		}
		if r.ConnectionID != connection {
			return ErrNotFound
		}
		l := query.BindIngressObservations(tx, m.names)
		rows, err := l.WithContext(ctx).Where(l.TenantID.Eq(tenant), l.MappingID.Eq(mapping), l.ID.Gt(last)).Order(l.ID).Limit(in.Limit + 1).Find()
		if err != nil {
			return err
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
			out.NextCursor = pageCursor(tenant, mapping, rows[len(rows)-1].ID)
		}
		for _, row := range rows {
			var observation IngressObservation
			if ingressDigest([]byte(row.Detail)) != row.ContentHash || json.Unmarshal([]byte(row.Detail), &observation) != nil || observation.TrustedVerification || observation.LocalAcceptance {
				return ErrUnavailable
			}
			out.Items = append(out.Items, IngressObservationView{row.ID, "REFERENCE_ONLY", observation})
		}
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

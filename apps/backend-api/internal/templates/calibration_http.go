package templates

import (
	"context"
	"encoding/json"
	"strconv"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type calibrationListInput struct {
	TemplateKey string `query:"template_key" maxLength:"64"`
	VerifierKey string `query:"verifier_key" maxLength:"64"`
	State       string `query:"state" enum:"UNCALIBRATED,CALIBRATING,CALIBRATED,DRIFTED"`
	Cursor      string `query:"cursor" maxLength:"19"`
	Limit       int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort        string `query:"sort" enum:"id" default:"id"`
}
type CalibrationPage struct {
	Items      []CalibrationView `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}
type calibrationStartInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           CalibrationStart
}
type calibrationGetInput struct {
	ID string `path:"calibration_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type calibrationConfirmInput struct {
	ID             string `path:"calibration_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Accept bool   `json:"accept"`
		Note   string `json:"note,omitempty" maxLength:"2000"`
	}
}
type calibrationSampleInput struct {
	VersionID      string `path:"version_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		VerifierKey string             `json:"verifier_key" minLength:"1" maxLength:"64"`
		Samples     []CalibrationLabel `json:"samples" minItems:"1" maxItems:"100"`
	}
}
type CalibrationLabel struct {
	Input    json.RawMessage `json:"input"`
	Expected string          `json:"expected" enum:"PASS,WARN,FAIL"`
}
type calibrationSampleListInput struct {
	VersionID   string `path:"version_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	VerifierKey string `query:"verifier_key" maxLength:"64"`
	Cursor      string `query:"cursor" maxLength:"19"`
	Limit       int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	Sort        string `query:"sort" enum:"id" default:"id"`
}
type CalibrationSamplePage struct {
	Items      []CalibrationSampleView `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
	HasMore    bool                    `json:"has_more"`
}

func (m *Module) registerCalibrationHTTP(api huma.API) {
	read := authz.Guard{Resource: "template", Verb: "read", FilterData: true}
	manage := authz.Guard{Resource: "template", Verb: "manage", FilterData: true}
	op := func(id, method, path, tag, summary string) huma.Operation {
		status := 200
		if id == "template.calibration-sample-create" {
			status = 201
		}
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{tag}, Summary: summary, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("template.calibration-list", "GET", "/verifier-calibrations", "verifier-calibrations", "List source evaluated verifier calibrations"), read, m.calibrationListHTTP)
	authz.Register(m.registrar, api, op("template.calibration-start", "POST", "/verifier-calibrations", "verifier-calibrations", "Freeze labelled samples and request actual calibration evaluation"), manage, m.calibrationStartHTTP) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	authz.Register(m.registrar, api, op("template.calibration-get", "GET", "/verifier-calibrations/{calibration_id}", "verifier-calibrations", "Read calibration agreement, evidence and availability"), read, m.calibrationGetHTTP)
	authz.Register(m.registrar, api, op("template.calibration-confirm", "PUT", "/verifier-calibrations/{calibration_id}/confirmation", "verifier-calibrations", "Human confirmation of completed calibration above its frozen target"), manage, m.calibrationConfirmHTTP)
	authz.Register(m.registrar, api, op("template.calibration-sample-list", "GET", "/template-versions/{version_id}/calibration-samples", "template-versions", "List immutable Human labelled calibration inputs"), manage, m.calibrationSampleListHTTP)                      //nolint:misspell // Preserve the published domain or serialized contract spelling.
	authz.Register(m.registrar, api, op("template.calibration-sample-create", "POST", "/template-versions/{version_id}/calibration-samples", "template-versions", "Retain immutable Human labelled samples for a published verifier"), manage, m.calibrationSampleCreateHTTP) //nolint:misspell // Preserve the published domain or serialized contract spelling.
}
func calibrationCursor(value string) (guid.ID, error) {
	if value == "" {
		return 0, nil
	}
	n, e := strconv.ParseInt(value, 10, 64)
	if e != nil || n < 1 {
		return 0, ErrInvalid
	}
	return guid.ID(n), nil
}
func (m *Module) calibrationListHTTP(ctx context.Context, in *calibrationListInput) (*respx.Body[CalibrationPage], error) {
	after, err := calibrationCursor(in.Cursor)
	if err != nil || in.Limit < 1 || in.Limit > 100 || in.Sort != "id" {
		return nil, templateError(ctx, ErrInvalid)
	}
	out := CalibrationPage{Items: []CalibrationView{}}
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		if e := m.authorizeOn(ctx, tx, "template_read", 0); e != nil {
			return e
		}
		q := query.BindCalibrations(tx, m.names)
		d := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Gt(after))
		if in.TemplateKey != "" {
			d = d.Where(q.TemplateKey.Eq(in.TemplateKey))
		}
		if in.VerifierKey != "" {
			d = d.Where(q.VerifierKey.Eq(in.VerifierKey))
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
			if _, e = m.versionOn(ctx, tx, row.TemplateVersionID, "template_read"); e != nil {
				return e
			}
			view, e := m.calibrationViewOn(ctx, tx, row)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, view)
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
func (m *Module) calibrationStartHTTP(ctx context.Context, in *calibrationStartInput) (*respx.Body[CalibrationView], error) {
	return command(m, ctx, in.IdempotencyKey, "template.calibration-start", in.Body, func(tx *gorm.DB) (CalibrationView, error) { return m.startCalibrationOn(ctx, tx, in.Body) }, func(tx *gorm.DB) error {
		_, e := m.versionOn(ctx, tx, in.Body.TemplateVersionID, "template_manage")
		return e
	})
}
func (m *Module) calibrationGetHTTP(ctx context.Context, in *calibrationGetInput) (*respx.Body[CalibrationView], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	var out CalibrationView
	err = m.transaction(ctx, func(tx *gorm.DB) error {
		row, e := m.calibrationOn(ctx, tx, id, "template_read")
		if e != nil {
			return e
		}
		out, e = m.calibrationViewOn(ctx, tx, row)
		return e
	})
	if err != nil {
		return nil, templateError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) calibrationConfirmHTTP(ctx context.Context, in *calibrationConfirmInput) (*respx.Body[CalibrationView], error) {
	id, e := guid.RequireID(in.ID)
	version, e2 := bindingVersionHeader(in.IfMatch)
	if e != nil || e2 != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	input := struct {
		Version int
		Accept  bool
		Note    string
	}{version, in.Body.Accept, in.Body.Note}
	return command(m, ctx, in.IdempotencyKey, "template.calibration-confirm:"+in.ID, input, func(tx *gorm.DB) (CalibrationView, error) {
		return m.confirmCalibrationOn(ctx, tx, id, version, in.Body.Accept, in.Body.Note)
	}, func(tx *gorm.DB) error { _, e := m.calibrationOn(ctx, tx, id, "template_manage"); return e })
}
func (m *Module) calibrationSampleCreateHTTP(ctx context.Context, in *calibrationSampleInput) (*respx.Body[CalibrationSamplePage], error) {
	id, e := guid.RequireID(in.VersionID)
	if e != nil || len(in.Body.Samples) < 1 || len(in.Body.Samples) > 100 {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.calibration-samples:"+in.VersionID, in.Body, func(tx *gorm.DB) (CalibrationSamplePage, error) {
		out := CalibrationSamplePage{Items: []CalibrationSampleView{}}
		for _, sample := range in.Body.Samples {
			view, err := m.addCalibrationSampleOn(ctx, tx, CalibrationSampleDraft{TemplateVersionID: id, VerifierKey: in.Body.VerifierKey, Input: sample.Input, Expected: sample.Expected}, "MANUAL")
			if err != nil {
				return out, err
			}
			out.Items = append(out.Items, view)
		}
		return out, nil
	}, func(tx *gorm.DB) error { _, e := m.versionOn(ctx, tx, id, "template_manage"); return e })
}
func (m *Module) calibrationSampleListHTTP(ctx context.Context, in *calibrationSampleListInput) (*respx.Body[CalibrationSamplePage], error) {
	id, e := guid.RequireID(in.VersionID)
	after, e2 := calibrationCursor(in.Cursor)
	if e != nil || e2 != nil || in.Limit < 1 || in.Limit > 100 || in.Sort != "id" {
		return nil, templateError(ctx, ErrInvalid)
	}
	out := CalibrationSamplePage{Items: []CalibrationSampleView{}}
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		if _, err := m.versionOn(ctx, tx, id, "template_manage"); err != nil {
			return err
		}
		q := query.BindCalibrationSamples(tx, m.names)
		d := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.TemplateVersionID.Eq(id), q.ID.Gt(after))
		if in.VerifierKey != "" {
			d = d.Where(q.VerifierKey.Eq(in.VerifierKey))
		}
		rows, err := d.Order(q.ID).Limit(in.Limit + 1).Find()
		if err != nil {
			return err
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
		}
		for _, row := range rows {
			out.Items = append(out.Items, calibrationSampleView(row))
		}
		if out.HasMore {
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		return nil
	})
	if e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

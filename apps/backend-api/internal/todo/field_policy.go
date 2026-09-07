package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"7link.com/platform/internal/todo/model"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
)

func (s *service) fieldConstraint(ctx context.Context) (authz.FieldConstraint, error) {
	if s == nil || s.fields == nil {
		return authz.FieldConstraint{}, ErrUnavailable
	}
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.TenantID.Zero() || claims.EntityID.Zero() || claims.PrincipalID.Zero() {
		return authz.FieldConstraint{}, ErrForbidden
	}
	constraint, err := s.fields.FieldConstraint(ctx, claims.TenantID, claims.EntityID, Name, claims.PrincipalID)
	if err != nil {
		return authz.FieldConstraint{}, errors.Join(ErrUnavailable, err)
	}
	if constraint.Fields["id"].Read != "allow" || constraint.Fields["version"].Read != "allow" {
		return authz.FieldConstraint{}, ErrForbidden
	}
	return constraint, nil
}

func checkFieldWrites(constraint authz.FieldConstraint, names []string, create bool) error {
	for _, name := range names {
		decision := constraint.Fields[name]
		allowed := decision.Update
		if create {
			allowed = decision.Create
		}
		if !allowed {
			return fmt.Errorf("%w: field %s is not writable", ErrForbidden, name)
		}
	}
	return nil
}

// The raw JSON preserves explicitly submitted nulls and values equal to defaults.
func submittedFields(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	return names, nil
}

func (d Draft) submitted() []string {
	if d.provided != nil {
		return d.provided
	}
	return []string{"title", "description", "status", "priority", "due_at"}
}

func (p Patch) submitted() []string {
	names := append([]string{}, p.provided...)
	for _, candidate := range []struct {
		name string
		set  bool
	}{
		{"title", p.Title != nil}, {"description", p.Description != nil},
		{"status", p.Status != nil}, {"priority", p.Priority != nil}, {"due_at", p.DueAt != nil},
	} {
		if candidate.set {
			names = append(names, candidate.name)
		}
	}
	return names
}

func readableFields(constraint authz.FieldConstraint) map[string]bool {
	readable := map[string]bool{}
	for name, decision := range constraint.Fields {
		readable[name] = decision.Read == "allow"
	}
	return readable
}

func renderedFields(requested map[string]bool, constraint authz.FieldConstraint) map[string]bool {
	rendered := map[string]bool{}
	for name := range requested {
		decision := constraint.Fields[name]
		if decision.Read == "allow" || (decision.Read == "redact" && (name == "title" || name == "description")) {
			rendered[name] = true
		}
	}
	return rendered
}

func maskedRow(row *model.TodoRow, constraint authz.FieldConstraint) *model.TodoRow {
	masked := *row
	if constraint.Fields["title"].Read == "redact" {
		masked.Title = "[REDACTED]"
	}
	if constraint.Fields["description"].Read == "redact" {
		masked.Description = "[REDACTED]"
	}
	return &masked
}

func (s *service) taskResponse(ctx context.Context, row *model.TodoRow) (*taskBody, error) {
	constraint, err := s.fieldConstraint(ctx)
	if err != nil {
		return nil, httpError(err)
	}
	view := viewOfSelected(maskedRow(row, constraint), renderedFields(allFields, constraint))
	return &taskBody{ETag: etag(row.Version), Body: respx.OK(ctx, view).Body}, nil
}

func (s *service) importTemplate(ctx context.Context, sample bool) (grid, error) {
	if _, _, err := s.creationScope(ctx, "import"); err != nil {
		return nil, err
	}
	constraint, err := s.fieldConstraint(ctx)
	if err != nil {
		return nil, err
	}
	cols := []column{}
	for _, c := range importableColumns() {
		if constraint.Fields[c.key].Create {
			cols = append(cols, c)
		} else if c.required {
			return nil, ErrForbidden
		}
	}
	return templateGridColumns(ctx, sample, cols), nil
}

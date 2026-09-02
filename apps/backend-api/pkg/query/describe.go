package query

import (
	"fmt"
	"strings"

	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

// Describe renders the contract for a client.
//
// The same declaration drives parsing and this description, so a filter
// interface built from it cannot drift from what the server accepts.
func (f *Fields) Describe() *respx.Capabilities {
	filters := make([]respx.FilterCapability, 0, len(f.filters))
	for _, declared := range f.filters {
		filters = append(filters, respx.FilterCapability{
			Field:  declared.name,
			Type:   string(declared.kind),
			Ops:    opNames(declared.ops),
			Values: declared.values,
			Min:    declared.boundMin(),
			Max:    declared.boundMax(),
		})
	}
	return &respx.Capabilities{
		Filters:    filters,
		Sortable:   f.SortableNames(),
		Selectable: f.SelectableNames(),
	}
}

// Parameters documents the filter parameters for OpenAPI.
//
// These parameters are parsed from the raw query rather than bound to struct
// fields, and huma documents only what a request struct declares — so without
// this they are absent from the generated document and from every client built
// from it. Producing them from the same declaration that parses them is what
// keeps the document and the server from drifting.
//
// Each is an array: one parameter may be repeated with a different operator,
// which is how several conditions on one field are expressed.
func (f *Fields) Parameters() []*huma.Param {
	params := make([]*huma.Param, 0, len(f.filters))
	for _, declared := range f.filters {
		params = append(params, &huma.Param{
			Name:        declared.name,
			In:          "query",
			Description: declared.describe(),
			Schema: &huma.Schema{
				Type:  huma.TypeArray,
				Items: &huma.Schema{Type: huma.TypeString},
			},
		})
	}
	return params
}

// describe is the human-readable half of the contract: what this field accepts,
// in the same terms the parse errors use.
func (f Field) describe() string {
	text := "written as <operator>:<value>; accepts " + strings.Join(opNames(f.ops), ", ")
	if len(f.values) > 0 {
		text += "; values: " + strings.Join(f.values, ", ")
	}
	if f.bounds != nil && f.bounds.set {
		text += fmt.Sprintf("; between %d and %d", f.bounds.min, f.bounds.max)
	}
	return text
}

package query

import "7link.com/platform/pkg/extension/huma/respx"

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

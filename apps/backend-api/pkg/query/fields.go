package query

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"gorm.io/gen/field"
)

// Fields is a module's filter contract: which parameters exist, what each one
// accepts, and how it becomes a predicate.
type Fields struct {
	filters    []Field
	sortable   map[string]SortKey
	selectable map[string]field.Expr
	// reserved names are consumed by the transport rather than by a filter, and
	// must not be reported as unknown parameters.
	reserved map[string]bool
}

// SortKey is an orderable column. Nullable marks a column whose null placement
// differs between dialects and therefore has to be ordered explicitly.
type SortKey struct {
	Column   field.Expr
	Nullable bool
}

// New builds a contract from its filterable fields.
func New(filters ...Field) *Fields {
	f := &Fields{
		filters:    filters,
		sortable:   map[string]SortKey{},
		selectable: map[string]field.Expr{},
		reserved:   map[string]bool{},
	}
	return f
}

// Sortable declares the orderable columns.
func (f *Fields) Sortable(keys map[string]SortKey) *Fields {
	f.sortable = keys
	return f
}

// Selectable declares the columns a caller may ask for by name.
func (f *Fields) Selectable(columns map[string]field.Expr) *Fields {
	f.selectable = columns
	return f
}

// Reserve marks parameters the transport consumes, so they are not reported as
// unknown. Anything not declared as a filter or reserved is rejected: silently
// ignoring an unrecognized parameter is how a typo becomes an unfiltered query,
// and how an injected parameter goes unnoticed.
func (f *Fields) Reserve(names ...string) *Fields {
	for _, name := range names {
		f.reserved[name] = true
	}
	return f
}

// Readable copies a contract with only permitted filter, sort and select keys.
// Reserved transport parameters remain the transport's responsibility.
func (f *Fields) Readable(allowed map[string]bool) *Fields {
	out := New()
	for _, filter := range f.filters {
		if allowed[filter.name] {
			out.filters = append(out.filters, filter)
		}
	}
	for name, key := range f.sortable {
		if allowed[name] {
			out.sortable[name] = key
		}
	}
	for name, column := range f.selectable {
		if allowed[name] {
			out.selectable[name] = column
		}
	}
	for name, reserved := range f.reserved {
		out.reserved[name] = reserved
	}
	return out
}

// Error names the parameter a request was rejected for.
//
// It deliberately does not echo the offending value. A message that carries
// caller input back is rendered by some client somewhere, and a filter error is
// exactly the sort of message a UI drops into the DOM unescaped.
type Error struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e Error) Error() string { return e.Field + ": " + e.Message }

// Parse turns query parameters into predicates.
func (f *Fields) Parse(values url.Values) ([]field.Expr, error) {
	byName := map[string]Field{}
	for _, filter := range f.filters {
		byName[filter.name] = filter
	}

	var out []field.Expr
	for name, raws := range values {
		if f.reserved[name] {
			continue
		}
		declared, known := byName[name]
		if !known {
			return nil, Error{Field: name, Message: "unknown parameter; accepts " + strings.Join(f.names(), ", ")}
		}

		seen := map[Op]bool{}
		for _, raw := range raws {
			if len(raw) > MaxValueLength {
				return nil, Error{Field: name, Message: fmt.Sprintf("must be at most %d characters", MaxValueLength)}
			}
			op, value, found := split(raw)
			if !found {
				return nil, Error{
					Field:   name,
					Message: "must be written as <operator>:<value>; accepts " + strings.Join(opNames(declared.ops), ", "),
				}
			}
			if !declared.allows(op) {
				return nil, Error{
					Field:   name,
					Message: "unsupported operator; accepts " + strings.Join(opNames(declared.ops), ", "),
				}
			}
			// An operator that compares against a value needs one. Every kind got
			// this wrong differently: like: built LIKE '%%' and matched every
			// row, in: built an empty list and matched none, and eq: on an
			// identifier could reach "= NULL" through a parse function that
			// reads "" as no identifier. None of those were designed.
			//
			// The reserved parameters do tolerate an empty value — q, sort and
			// fields each have a documented no-op for it — but they carry no
			// operator, so an empty one is unambiguously "no search term". A
			// filter states an operator, which is a client declaring it wants a
			// comparison; leaving out the comparand is a malformed statement,
			// not a blank input. A blank input is the parameter being absent,
			// which is what the message says.
			//
			// The switch operators — isNull, isEmpty — are exempt: their own
			// builders reject an empty value with a better message than this
			// generic one, because for them the value is true or false rather
			// than something to compare against.
			if !op.takesNoValue() && value == "" {
				return nil, Error{
					Field:   name,
					Message: "operator " + string(op) + " needs a value; omit the parameter to leave this field unfiltered",
				}
			}
			// A list operator needs at least one member once the value is split.
			// The check above is not enough because the split does its own
			// normalization: it trims each entry and drops the blanks, so
			// "in:,,," and "in:   " arrive here non-empty and leave as an empty
			// list. An empty IN () matches nothing while still looking like a
			// filter, which is the failure this rule exists to prevent — it is
			// only reached after normalization rather than before.
			if op.takesList() && len(splitList(value)) == 0 {
				return nil, Error{
					Field:   name,
					Message: "operator " + string(op) + " needs at least one value",
				}
			}
			// The same operator twice on one field is a client error, not a
			// last-one-wins merge: silently keeping the last is how an injected
			// duplicate overrides a legitimate filter unnoticed.
			if seen[op] {
				return nil, Error{Field: name, Message: "operator " + string(op) + " is repeated; use a comma-separated list instead"}
			}
			seen[op] = true

			expr, err := declared.build(op, value)
			if err != nil {
				return nil, Error{Field: name, Message: err.Error()}
			}
			out = append(out, expr)
		}
	}
	return out, nil
}

// Ordering resolves a sort request into ORDER BY expressions.
//
// tiebreak is appended unconditionally. Without a unique final key the database
// may return rows with equal sort values in any order, and offset paging then
// repeats or skips rows with nothing reported.
func (f *Fields) Ordering(raw string, maxKeys int, tiebreak field.Expr) ([]field.Expr, error) {
	if strings.TrimSpace(raw) == "" {
		return []field.Expr{tiebreak}, nil
	}
	requests := strings.Split(raw, ",")
	if len(requests) > maxKeys {
		return nil, Error{Field: "sort", Message: fmt.Sprintf("accepts at most %d keys", maxKeys)}
	}

	var order []field.Expr
	for _, request := range requests {
		name := strings.TrimSpace(request)
		descending := strings.HasPrefix(name, "-")
		name = strings.TrimPrefix(name, "-")
		key, ok := f.sortable[name]
		if !ok {
			return nil, Error{Field: "sort", Message: "unknown key; accepts " + strings.Join(f.SortableNames(), ", ")}
		}
		// Null placement differs per dialect, so it is ordered first and
		// explicitly: false sorts before true everywhere, putting rows with a
		// value ahead of rows without one.
		if key.Nullable {
			order = append(order, field.NewUnsafeFieldRaw("? IS NULL", key.Column.RawExpr()))
		}
		if descending {
			order = append(order, key.Column.Desc())
		} else {
			order = append(order, key.Column)
		}
	}
	return append(order, tiebreak), nil
}

// Projection resolves a field selection into columns to read and names to
// render. always names columns read regardless of the request, for values the
// transport needs even when the caller did not ask for them.
func (f *Fields) Projection(raw string, always ...string) ([]field.Expr, map[string]bool, error) {
	if strings.TrimSpace(raw) == "" {
		rendered := make(map[string]bool, len(f.selectable))
		for name := range f.selectable {
			rendered[name] = true
		}
		return nil, rendered, nil
	}

	rendered := map[string]bool{}
	read := map[string]field.Expr{}
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		column, ok := f.selectable[name]
		if !ok {
			return nil, nil, Error{Field: "fields", Message: "unknown field; accepts " + strings.Join(f.SelectableNames(), ", ")}
		}
		rendered[name] = true
		read[name] = column
	}
	if len(rendered) == 0 {
		return nil, nil, Error{Field: "fields", Message: "must name at least one field"}
	}
	for _, name := range always {
		if column, ok := f.selectable[name]; ok {
			read[name] = column
		}
	}

	columns := make([]field.Expr, 0, len(read))
	for _, name := range sortedNames(read) {
		columns = append(columns, read[name])
	}
	return columns, rendered, nil
}

func (f *Fields) names() []string {
	out := make([]string, 0, len(f.filters)+len(f.reserved))
	for _, filter := range f.filters {
		out = append(out, filter.name)
	}
	sort.Strings(out)
	return out
}

// SortableNames lists the accepted sort keys.
func (f *Fields) SortableNames() []string { return sortedNames(f.sortable) }

// SelectableNames lists the columns a selection may name.
func (f *Fields) SelectableNames() []string { return sortedNames(f.selectable) }

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func opNames(ops []Op) []string {
	out := make([]string, len(ops))
	for index, op := range ops {
		out[index] = string(op)
	}
	return out
}

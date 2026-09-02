package query

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gen/field"
)

// Kind is a field's value type. It selects how a value is parsed and tells a
// client which input control to render.
type Kind string

const (
	KindString Kind = "string"
	KindEnum   Kind = "enum"
	KindInt    Kind = "int"
	KindTime   Kind = "time"
	KindID     Kind = "id"
	KindBool   Kind = "bool"
)

// Field is one filterable column: its public name, the generated column it
// reads, the operators it accepts, and how a raw value becomes a predicate.
type Field struct {
	name   string
	kind   Kind
	ops    []Op
	values []string // enum members, for validation and for the descriptor
	bounds *intBounds

	// text marks a field whose column holds a string, and so has an empty value
	// distinct from null. Empty is refused on anything else: for a number, a
	// timestamp or an identifier, "no value" is null and isNull already says it.
	text bool

	// build turns one already-validated operator and value into a predicate.
	build func(op Op, raw string) (field.Expr, error)
}

// Name is the query parameter this field is addressed by.
func (f Field) Name() string { return f.name }

func (f Field) allows(op Op) bool {
	for _, allowed := range f.ops {
		if allowed == op {
			return true
		}
	}
	return false
}

// Ops narrows the operators a field accepts. Declaring them per field rather
// than offering every operator everywhere keeps nonsensical-but-valid queries
// out of the contract: a status is never greater than another status.
func (f Field) Ops(ops ...Op) Field {
	f.ops = ops
	return f
}

// intBounds is shared between a field and the closure that validates it, so
// Range takes effect on a builder created before it was called.
type intBounds struct {
	min, max int
	set      bool
}

// Range documents and enforces the bounds of an integer field.
func (f Field) Range(minimum, maximum int) Field {
	if f.bounds != nil {
		f.bounds.min, f.bounds.max, f.bounds.set = minimum, maximum, true
	}
	return f
}

// String declares a free-text field.
func String(name string, column field.String) Field {
	f := Field{name: name, kind: KindString, ops: []Op{Eq, NotEq, Like}, text: true}
	f.build = func(op Op, raw string) (field.Expr, error) {
		if op == IsNull {
			return nullExpr(column, raw)
		}
		if op == IsEmpty {
			return emptyExpr(column, raw)
		}
		switch op {
		case Eq:
			return column.Eq(raw), nil
		case NotEq:
			return column.Neq(raw), nil
		case Like:
			// EscapeLike normalizes too, by removing the wildcards, so a pattern
			// of nothing but wildcards arrives non-empty and leaves empty —
			// LIKE '%%', which matches every row. Same rule as an absent value,
			// reached one step later.
			pattern := EscapeLike(raw)
			if pattern == "" {
				return nil, fmt.Errorf("must contain something other than wildcards")
			}
			return column.Like("%" + pattern + "%"), nil
		case In:
			return column.In(splitList(raw)...), nil
		case NotIn:
			return column.NotIn(splitList(raw)...), nil
		}
		return nil, unsupported(name, op)
	}
	return f
}

// Enum declares a field whose values come from a closed set. A value outside
// the set is rejected before it reaches the database.
func Enum(name string, column field.String, values []string) Field {
	f := Field{name: name, kind: KindEnum, ops: []Op{Eq, NotEq, In, NotIn}, values: values, text: true}
	member := func(candidate string) error {
		for _, value := range values {
			if candidate == value {
				return nil
			}
		}
		return fmt.Errorf("must be one of %s", strings.Join(values, ", "))
	}
	f.build = func(op Op, raw string) (field.Expr, error) {
		if op == IsNull {
			return nullExpr(column, raw)
		}
		// An empty value is never an enum member, so isEmpty is the only way to
		// ask for one — member() would reject eq: "" before it reached SQL.
		if op == IsEmpty {
			return emptyExpr(column, raw)
		}
		if op.takesList() {
			parts := splitList(raw)
			for _, part := range parts {
				if err := member(part); err != nil {
					return nil, err
				}
			}
			if op == In {
				return column.In(parts...), nil
			}
			return column.NotIn(parts...), nil
		}
		if err := member(raw); err != nil {
			return nil, err
		}
		if op == NotEq {
			return column.Neq(raw), nil
		}
		return column.Eq(raw), nil
	}
	return f
}

// Int declares a whole-number field.
//
// The bounds are read through a pointer the builder closes over rather than
// captured by value: Range returns a modified copy, and a closure created in
// this constructor would otherwise keep the bounds the field had before Range
// was ever called — that is, none.
func Int(name string, column field.Int) Field {
	bounds := &intBounds{}
	f := Field{name: name, kind: KindInt, ops: []Op{Eq, NotEq, Gt, Gte, Lt, Lte, In}, bounds: bounds}
	// inRange is applied to every parsed value, list members included. The
	// descriptor advertises the bounds, so a list that skipped them would accept
	// values the same field rejects one at a time.
	inRange := func(parsed int) error {
		if bounds.set && (parsed < bounds.min || parsed > bounds.max) {
			return fmt.Errorf("must be between %d and %d", bounds.min, bounds.max)
		}
		return nil
	}
	f.build = func(op Op, raw string) (field.Expr, error) {
		if op == IsNull {
			return nullExpr(column, raw)
		}
		if op.takesList() {
			var values []int
			for _, part := range splitList(raw) {
				parsed, err := strconv.Atoi(part)
				if err != nil {
					return nil, fmt.Errorf("must be whole numbers")
				}
				if err := inRange(parsed); err != nil {
					return nil, err
				}
				values = append(values, parsed)
			}
			return column.In(values...), nil
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("must be a whole number")
		}
		if err := inRange(parsed); err != nil {
			return nil, err
		}
		switch op {
		case Eq:
			return column.Eq(parsed), nil
		case NotEq:
			return column.Neq(parsed), nil
		case Gt:
			return column.Gt(parsed), nil
		case Gte:
			return column.Gte(parsed), nil
		case Lt:
			return column.Lt(parsed), nil
		case Lte:
			return column.Lte(parsed), nil
		}
		return nil, unsupported(name, op)
	}
	return f
}

// TimeLayouts are accepted in order. The readable layout is what exports and
// templates write; RFC 3339 is what programmatic clients send.
var TimeLayouts = []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}

// Time declares a timestamp field.
func Time(name string, column field.Time) Field {
	f := Field{name: name, kind: KindTime, ops: []Op{Gt, Gte, Lt, Lte, Eq}}
	f.build = func(op Op, raw string) (field.Expr, error) {
		if op == IsNull {
			return nullExpr(column, raw)
		}
		parsed, err := parseTime(raw)
		if err != nil {
			return nil, fmt.Errorf("must look like %s", TimeLayouts[1])
		}
		switch op {
		case Eq:
			return column.Eq(parsed), nil
		case Gt:
			return column.Gt(parsed), nil
		case Gte:
			return column.Gte(parsed), nil
		case Lt:
			return column.Lt(parsed), nil
		case Lte:
			return column.Lte(parsed), nil
		}
		return nil, unsupported(name, op)
	}
	return f
}

// ID declares an identifier field. Parse converts the wire form, which is a
// decimal string so large identifiers survive JavaScript clients.
func ID(name string, column field.Field, parse func(string) (driver.Valuer, error)) Field {
	f := Field{name: name, kind: KindID, ops: []Op{Eq, NotEq, In}}
	f.build = func(op Op, raw string) (field.Expr, error) {
		if op == IsNull {
			return nullExpr(column, raw)
		}
		if op.takesList() {
			var values []driver.Valuer
			for _, part := range splitList(raw) {
				parsed, err := parse(part)
				if err != nil {
					return nil, fmt.Errorf("must be identifiers")
				}
				values = append(values, parsed)
			}
			return column.In(values...), nil
		}
		parsed, err := parse(raw)
		if err != nil {
			return nil, fmt.Errorf("must be an identifier")
		}
		if op == NotEq {
			return column.Neq(parsed), nil
		}
		return column.Eq(parsed), nil
	}
	return f
}

// Nullable adds the IsNull operator, for a column the schema permits to be
// empty. Every field kind honours it: a contract that advertises an operator
// and then rejects it at parse time is worse than one that never offered it.
func (f Field) Nullable() Field {
	return f.Ops(append(append([]Op{}, f.ops...), IsNull)...)
}

// Empty adds the IsEmpty operator, for a text column that is not null but may
// hold "". It panics on any other kind: a number, a timestamp or an identifier
// has no empty distinct from null, and advertising an operator the field would
// then reject at parse time is a contract no client can use. Failing here makes
// that a wiring error the process cannot start with.
//
// The test is exact: a column holding only whitespace is not empty. Trimming
// would make isEmpty:true and isEmpty:false disagree with what eq: matches.
func (f Field) Empty() Field {
	if !f.text {
		panic(fmt.Sprintf("query: field %q has no empty value; use Nullable for a column that can be null", f.name))
	}
	return f.Ops(append(append([]Op{}, f.ops...), IsEmpty)...)
}

// emptiable is the comparison a text column offers. Only the string-valued
// generated types satisfy it, so the type system, not a kind check, is what
// keeps IsEmpty off a number or a timestamp.
type emptiable interface {
	Eq(string) field.Expr
	Neq(string) field.Expr
}

func emptyExpr(column emptiable, raw string) (field.Expr, error) {
	switch raw {
	case "true":
		return column.Eq(""), nil
	case "false":
		return column.Neq(""), nil
	}
	return nil, fmt.Errorf("must be true or false")
}

// nullable is the null comparison every generated column type offers. gorm's
// field types all embed the same expression, so one interface covers them and
// IsNull does not have to be reimplemented per kind.
type nullable interface {
	IsNull() field.Expr
	IsNotNull() field.Expr
}

// nullExpr builds the IsNull predicate. It takes true or false rather than no
// value at all, so one parameter can ask for either side of the comparison.
func nullExpr(column nullable, raw string) (field.Expr, error) {
	switch raw {
	case "true":
		return column.IsNull(), nil
	case "false":
		return column.IsNotNull(), nil
	}
	return nil, fmt.Errorf("must be true or false")
}

func parseTime(raw string) (time.Time, error) {
	for _, layout := range TimeLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time")
}

func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func unsupported(name string, op Op) error {
	return fmt.Errorf("operator %s is not supported", op)
}

// boundMin and boundMax expose the declared range for the descriptor, and nil
// when the field is unbounded.
func (f Field) boundMin() *int {
	if f.bounds == nil || !f.bounds.set {
		return nil
	}
	return &f.bounds.min
}

func (f Field) boundMax() *int {
	if f.bounds == nil || !f.bounds.set {
		return nil
	}
	return &f.bounds.max
}

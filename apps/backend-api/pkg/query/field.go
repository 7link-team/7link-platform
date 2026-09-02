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

	// nulls records whether Nullable was declared, which the negative operators
	// need in order to be complete. It is shared through a pointer for the same
	// reason intBounds is: a builder closure is created in the constructor,
	// before Nullable can have been called, so it has to read the declaration
	// rather than capture it.
	nulls *declaredNulls

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
	nulls := &declaredNulls{}
	f := Field{name: name, kind: KindString, ops: []Op{Eq, NotEq, Like}, text: true, nulls: nulls}
	f.build = func(op Op, raw string) (field.Expr, error) {
		if op == IsNull {
			return nullExpr(column, raw)
		}
		if op == IsEmpty {
			return emptyExpr(column, raw, nulls)
		}
		switch op {
		case Eq:
			return column.Eq(raw), nil
		case NotEq:
			return admitNulls(column, column.Neq(raw), nulls), nil
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
			return admitNulls(column, column.NotIn(splitList(raw)...), nulls), nil
		}
		return nil, unsupported(name, op)
	}
	return f
}

// Enum declares a field whose values come from a closed set. A value outside
// the set is rejected before it reaches the database.
func Enum(name string, column field.String, values []string) Field {
	nulls := &declaredNulls{}
	f := Field{name: name, kind: KindEnum, ops: []Op{Eq, NotEq, In, NotIn}, values: values, text: true, nulls: nulls}
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
			return emptyExpr(column, raw, nulls)
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
			return admitNulls(column, column.NotIn(parts...), nulls), nil
		}
		if err := member(raw); err != nil {
			return nil, err
		}
		switch op {
		case Eq:
			return column.Eq(raw), nil
		case NotEq:
			return admitNulls(column, column.Neq(raw), nulls), nil
		}
		return nil, unsupported(name, op)
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
	nulls := &declaredNulls{}
	f := Field{name: name, kind: KindInt, ops: []Op{Eq, NotEq, Gt, Gte, Lt, Lte, In}, bounds: bounds, nulls: nulls}
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
			if op == NotIn {
				return admitNulls(column, column.NotIn(values...), nulls), nil
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
			return admitNulls(column, column.Neq(parsed), nulls), nil
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
	f := Field{name: name, kind: KindTime, ops: []Op{Gt, Gte, Lt, Lte, Eq}, nulls: &declaredNulls{}}
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
	nulls := &declaredNulls{}
	f := Field{name: name, kind: KindID, ops: []Op{Eq, NotEq, In}, nulls: nulls}
	// The parse function belongs to the calling module, so this cannot assume it
	// reports every value it refuses as an error: one that reads "" or "null" as
	// "no identifier" returns a nil value and a nil error. A nil comparand does
	// not become "no rows" — gorm renders Eq(nil) as IS NULL and Neq(nil) as IS
	// NOT NULL — so on a non-nullable column one silently matches nothing and
	// the other silently matches everything. Neither is a filter.
	usable := func(raw string) (driver.Valuer, bool) {
		parsed, err := parse(raw)
		return parsed, err == nil && parsed != nil
	}
	f.build = func(op Op, raw string) (field.Expr, error) {
		if op == IsNull {
			return nullExpr(column, raw)
		}
		if op.takesList() {
			var values []driver.Valuer
			for _, part := range splitList(raw) {
				parsed, ok := usable(part)
				if !ok {
					return nil, fmt.Errorf("must be identifiers")
				}
				values = append(values, parsed)
			}
			if op == NotIn {
				return admitNulls(column, column.NotIn(values...), nulls), nil
			}
			return column.In(values...), nil
		}
		parsed, ok := usable(raw)
		if !ok {
			return nil, fmt.Errorf("must be an identifier")
		}
		switch op {
		case Eq:
			return column.Eq(parsed), nil
		case NotEq:
			return admitNulls(column, column.Neq(parsed), nulls), nil
		}
		return nil, unsupported(name, op)
	}
	return f
}

// declaredNulls records whether Nullable was called, shared between a field and
// the closure that builds its predicates.
type declaredNulls struct{ yes bool }

// Nullable declares that the column may be null. It adds the IsNull operator —
// every field kind honors it, because a contract that advertises an operator
// and then rejects it at parse time is worse than one that never offered it —
// and it changes what the negative operators mean, which is the part that
// cannot be inferred from the column type.
func (f Field) Nullable() Field {
	if f.nulls != nil {
		f.nulls.yes = true
	}
	return f.Ops(append(append([]Op{}, f.ops...), IsNull)...)
}

// admitNulls completes a negative predicate over a column that may be null.
//
// SQL compares NULL to nothing: `col <> 'x'` and `col NOT IN (...)` are both
// UNKNOWN for a null row, so it drops out of the negative answer as well as the
// positive one. A caller asking for everything that is not urgent would
// silently lose the rows that are not anything, and the two halves of the
// question would not add up to the table.
//
// Only a field that declared Nullable gets the extra clause. On a NOT NULL
// column it can never be true, and it is the kind of dead disjunct that costs
// an index scan.
func admitNulls(column nullComparable, predicate field.Expr, nulls *declaredNulls) field.Expr {
	if nulls == nil || !nulls.yes {
		return predicate
	}
	return field.Or(predicate, column.IsNull())
}

// Empty adds the IsEmpty operator, for a text column that may hold the empty
// string. It panics on any other kind: a number, a timestamp or an identifier
// has no empty distinct from null, and advertising an operator the field would
// then reject at parse time is a contract no client can use. Failing here makes
// that a wiring error the process cannot start with.
//
// It composes with Nullable. On a column that is both, a null counts as empty,
// which is what emptyExpr explains.
//
// The test is exact: a column holding only whitespace is not empty. Trimming
// would make isEmpty:true and isEmpty:false disagree with what eq: matches.
func (f Field) Empty() Field {
	if !f.text {
		panic(fmt.Sprintf("query: field %q has no empty value; use Nullable for a column that can be null", f.name))
	}
	return f.Ops(append(append([]Op{}, f.ops...), IsEmpty)...)
}

// emptyExpr builds the IsEmpty predicate. It takes the concrete string column
// rather than an interface: only a text column has an empty value at all, and
// the parameter type is what keeps IsEmpty off a number or a timestamp.
//
// The two halves treat null differently on purpose. A null column has no text,
// so it belongs in "empty" — and `col <> ”` already excludes it from "has
// text" without any help, because that comparison is UNKNOWN. Admitting nulls
// on one side and not the other is exactly what makes the two answers add up
// to the whole table.
func emptyExpr(column field.String, raw string, nulls *declaredNulls) (field.Expr, error) {
	switch raw {
	case "true":
		return admitNulls(column, column.Eq(""), nulls), nil
	case "false":
		return column.Neq(""), nil
	}
	return nil, fmt.Errorf("must be true or false")
}

// nullComparable is the null comparison every generated column type offers.
// gorm's field types all embed the same expression, so one interface covers
// them and IsNull does not have to be reimplemented per kind.
type nullComparable interface {
	IsNull() field.Expr
	IsNotNull() field.Expr
}

// nullExpr builds the IsNull predicate. It takes true or false rather than no
// value at all, so one parameter can ask for either side of the comparison.
func nullExpr(column nullComparable, raw string) (field.Expr, error) {
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

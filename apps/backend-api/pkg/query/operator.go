// Package query turns URL query parameters into typed database predicates.
//
// A module declares which fields it exposes and which operators each one
// accepts; this package parses requests against that declaration, rejects
// anything outside it, and describes it back to clients so a filter UI can be
// built without hard-coding the contract.
//
// The caller never influences SQL structure. The column comes from generated,
// compile-checked field expressions and the operator is chosen from a
// per-field whitelist; only the value is caller-supplied, and it is always
// bound as a parameter.
package query

import "strings"

// Op is a comparison. The wire form is the constant's own text, so the URL and
// the declaration read the same.
type Op string

const (
	Eq     Op = "eq"
	NotEq  Op = "notEq"
	In     Op = "in"
	NotIn  Op = "notIn"
	Like   Op = "like"
	Gte    Op = "gte"
	Lte    Op = "lte"
	Gt     Op = "gt"
	Lt     Op = "lt"
	IsNull Op = "isNull"
)

// listOps take a comma-separated value; every other operator takes one value
// verbatim. The distinction matters when a value legitimately contains a comma:
// only these operators split on it.
func (o Op) takesList() bool { return o == In || o == NotIn }

// takesNoValue marks operators whose value is a boolean switch rather than a
// comparand.
func (o Op) takesNoValue() bool { return o == IsNull }

// MaxValueLength caps one filter value. An unbounded value would reach LIKE
// patterns and logs alike; the limit is generous for real input and small
// enough that neither becomes a lever.
const MaxValueLength = 256

// split separates the operator from the value.
//
// Only the first colon separates: a value may contain as many more as it likes,
// which timestamps routinely do. The operator is mandatory — allowing it to be
// omitted would make a value that happens to begin with "eq:" parse as an
// operator, silently searching for something the user did not ask for, with no
// error and no way for a client to defend itself short of knowing every field's
// operator set.
func split(raw string) (Op, string, bool) {
	op, value, found := strings.Cut(raw, ":")
	if !found {
		return "", "", false
	}
	return Op(op), value, true
}

// escapeLike removes the wildcards a caller could otherwise use to widen a
// match into a full scan.
//
// They are removed rather than escaped because escaping is not portable: MySQL
// and PostgreSQL default to backslash, SQLite has no default escape character
// at all and requires an explicit ESCAPE clause the query builder does not
// emit. Removing costs the ability to search for a literal percent sign, which
// no caller has asked for.
func EscapeLike(value string) string {
	return strings.NewReplacer("%", "", "_", "", "\\", "").Replace(value)
}

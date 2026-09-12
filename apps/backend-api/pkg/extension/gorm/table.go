package gormx

// TableName is a physical table name: a logical name with the deployment's
// prefix already applied.
//
// A table name cannot be a bind parameter, so a deployment that prefixes its
// tables has to build one by concatenation. Done in more than one place, that
// produces a JOIN with half its tables prefixed -- a defect unit tests do not
// see, because they run without a prefix, and which only shows up in the
// deployment that has one. This type exists so the concatenation happens once,
// inside the module that owns the table, and so every other module receives a
// name it cannot invent for itself.
type TableName struct {
	physical string
	logical  string
}

// NewTableName applies prefix to a logical table name.
//
// It is the only way to obtain a TableName, and the query gate restricts the
// file that calls it to each module's query/table.go, so no other file can
// name a table this way.
func NewTableName(prefix, logical string) TableName {
	return TableName{physical: prefix + logical, logical: logical}
}

// String returns the name to send to the database.
func (name TableName) String() string { return name.physical }

// Logical returns the unprefixed name, for error messages and logs. It is
// never valid in a statement: using it there reads a different table.
func (name TableName) Logical() string { return name.logical }

// IsZero reports a name that was never constructed. A zero name must not reach
// a statement, so a caller that resolves names asserts this rather than letting
// an empty string travel into a query.
func (name TableName) IsZero() bool { return name.physical == "" }

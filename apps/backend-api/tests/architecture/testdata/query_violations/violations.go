// Package queryviolations is the query gate's fixture. Go tooling ignores
// testdata, so this file is parsed but never built, which is what lets it name
// identifiers the repository does not have. Every rule must fire on it;
// TestQueryGateRulesRejectTheirFixtures asserts that, because a rule that
// silently matched nothing would look exactly like a clean repository.
package queryviolations

import (
	"fmt"

	"gorm.io/gorm"
)

// handwritten-sql, concatenated-table-name and gorm-expr-dynamic.
func readRows(db *gorm.DB, prefix, name string) {
	db.Raw("SELECT id FROM iam_roles WHERE tenant_id = ?", name)
	db.Exec("UPDATE iam_roles SET status = ?", name)
	db.Table(prefix + "iam_roles").Find(nil)
	_ = gorm.Expr("SELECT id FROM iam_roles WHERE name = " + name)
}

// naked-table-literal and sql-in-format-string.
func filterRows(db *gorm.DB, name string) {
	db.Table("iam_groups").Find(nil)
	db.Where(fmt.Sprintf("SELECT id FROM iam_role_members WHERE tenant_id = %s", name)).Find(nil)
}

// concatenated-table-name: the helper itself is the second half of the rule,
// because a module that owns a table("name") function has moved the prefix
// concatenation out of Bind.
func table(name string) string {
	return prefix + name
}

// concatenated-table-name through a return. The rule cannot depend on the
// helper being called table, or renaming it would be enough to avoid the gate;
// this is the case a mutation test found missing.
func physicalName(prefix string) string {
	return prefix + "iam_positions"
}

// Not a violation: this builds an object key, not a table name, and the two
// are indistinguishable unless the rule looks for the table itself.
func objectKey(prefix, tenant string) string {
	return prefix + "/" + tenant
}

// naked-table-literal through a constant. Parking the name in a constant and
// handing that to the helper used to be enough to leave the rule behind, which
// is how thirteen sites in one module were counted as three.
const roleTable = "iam_roles"

func readThroughConstant(db *gorm.DB) {
	db.Table(roleTable).Find(nil)
}

// sql-in-format-string through a local variable. The repository's one Sprintf
// builds the statement into a variable and hands that to Exec; a rule that only
// looked at the call argument saw nothing.
//
// The verb sits where the table name goes, which is the shape that exposed the
// second half of the same hole: a keyword pattern bounded by  on both sides
// cannot match "UPDATE %s", because whitespace followed by %% is not a boundary.
func execThroughVariable(db *gorm.DB, tenant string) {
	statement := fmt.Sprintf("UPDATE %s SET status = 'x'", tenant)
	db.Exec(statement)
}

// Not a violation: a Prometheus metric name is not a table, however much it
// looks like one. Eleven of these were counted as tables before the gate read
// the names the models actually declare.
func metricName() string {
	return "iam_scim_sync_batches_total"
}

// Not a violation: appending a SQL alias to a name the owner already resolved
// is not assembling a table name out of a prefix.
func joinResolved(db *gorm.DB, resolved string) {
	db.Table(resolved + " AS role").Find(nil)
}

// Not a violation: integer arithmetic in an update map.
func bumpVersion(db *gorm.DB, version int) {
	db.Updates(map[string]any{"version": version + 1})
}

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

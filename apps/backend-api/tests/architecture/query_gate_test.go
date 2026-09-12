package architecture_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The query gate keeps hand-written SQL and hand-concatenated table names from
// spreading. Both are invisible to the rest of the suite: a table name built
// from the wrong prefix still compiles and still passes every test that runs
// without a prefix, and it only misbehaves in a deployment that has one.
//
// The gate is enforced against a frozen ledger rather than an empty set,
// because the repository already has violations. The ledger can only shrink:
// QUERY_GATE_UPDATE records the current findings but refuses to record more
// than the existing budget, and every recorded entry must still be found, so a
// stale entry fails the build instead of lingering.
const (
	queryGateLedgerFile = "query_gate_ledger.json"
	queryGateUpdateEnv  = "QUERY_GATE_UPDATE"
	// Raising the budget is the one way to make this gate pass without fixing
	// anything, so it takes a deliberate act with a written reason rather than
	// deleting the ledger. The reason lands in the ledger and in the diff.
	queryGateRebaselineEnv = "QUERY_GATE_REBASELINE"

	ruleHandwrittenSQL        = "handwritten-sql"
	ruleConcatenatedTableName = "concatenated-table-name"
	ruleNakedTableLiteral     = "naked-table-literal"
	ruleSQLInFormatString     = "sql-in-format-string"
	ruleGormExprDynamic       = "gorm-expr-dynamic"
)

// tableScopeMethods receive a table name. Select, Where and Order are
// deliberately absent: they receive column names, and a column comes from the
// table object the generated query layer already bound, so including them would
// report the sanctioned pattern as a violation.
var tableScopeMethods = map[string]bool{
	"Table": true, "TableName": true, "From": true, "Joins": true,
	"Raw": true, "Exec": true, "Delete": true, "Updates": true,
}

// argumentScopeMethods is the wider set whose arguments must not be assembled
// by fmt.Sprintf carrying a SQL keyword.
var argumentScopeMethods = map[string]bool{
	"Table": true, "TableName": true, "From": true, "Joins": true, "Raw": true,
	"Exec": true, "Delete": true, "Updates": true, "Select": true, "Where": true, "Order": true,
}

var (
	// A name shaped like a table, used by the gorm.Expr rule alone: there the
	// subject is assembled source text rather than a parsed literal, so there
	// is nothing to look up. Every other rule asks knownTableNames whether the
	// name belongs to a model, because shape alone read eleven metric names as
	// tables.
	tableLiteralPattern = regexp.MustCompile(`^(iam_|goose_)[a-z0-9_]*(\s|$)`)
	// The migration version tables have no model to declare them, so they are
	// recognized by shape. Everything else must be a name a model actually
	// returns -- see knownTableNames.
	gooseTablePattern = regexp.MustCompile(`^goose_[a-z0-9_]*$`)
	// A module's own prefix helper. A table name reaching one of these is in a
	// table position just as surely as one reaching Table(), and the rule must
	// not depend on the helper being called exactly "table".
	tableHelperPattern = regexp.MustCompile(`^([a-z][a-zA-Z0-9]*)?[Tt]able$`)
	// UPDATE carries no trailing whitespace class: the pattern is bounded by word
	// boundaries, and one after a whitespace class can never match "UPDATE %s" --
	// whitespace followed by a non-word character is not a boundary. That is why
	// the repository's one Sprintf-built statement stayed unreported even once the
	// rule followed the variable it was assigned to.
	sqlKeywordPattern    = regexp.MustCompile(`(?i)\b(SELECT|INSERT\s+INTO|UPDATE|DELETE\s+FROM|JOIN|FROM|WITH\s+RECURSIVE)\b`)
	exprKeywordPattern   = regexp.MustCompile(`(?i)^[a-z_0-9]*\s*(FROM|JOIN)\s`)
	prefixOperandPattern = regexp.MustCompile(`(?i)prefix`)
)

type queryViolation struct {
	Rule  string
	Key   string
	Where string
}

type queryGateEntry struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type queryGateLedger struct {
	SchemaVersion int              `json:"schema_version"`
	Budget        int              `json:"budget"`
	Rebaseline    string           `json:"rebaseline,omitempty"`
	Entries       []queryGateEntry `json:"entries"`
}

func TestQueryGate(t *testing.T) {
	root := backendRoot(t)
	violations := detectQueryViolations(t, root)

	// Counted, not merely present: a new use of a table name a directory
	// already writes would otherwise slip past an unchanged entry.
	current := map[string]int{}
	where := map[string][]string{}
	for _, violation := range violations {
		current[violation.Key]++
		where[violation.Key] = append(where[violation.Key], violation.Where)
	}
	for key := range where {
		sort.Strings(where[key])
	}

	ledgerPath := filepath.Join("testdata", queryGateLedgerFile)
	if os.Getenv(queryGateUpdateEnv) == "1" {
		writeQueryGateLedger(t, ledgerPath, current)
		return
	}

	ledger := readQueryGateLedger(t, ledgerPath)
	recorded := map[string]int{}
	for _, entry := range ledger.Entries {
		recorded[entry.Key] = entry.Count
	}

	for _, key := range sortedKeys(current) {
		switch count, known := recorded[key]; {
		case !known:
			t.Errorf("query gate: new violation %s\n    at %s\n    Use the module's query package, or the owning module's published table name.",
				key, strings.Join(where[key], "\n    at "))
		case count != current[key]:
			t.Errorf("query gate: violation %s now appears %d times, ledger records %d\n    at %s",
				key, current[key], count, strings.Join(where[key], "\n    at "))
		}
	}
	for _, key := range sortedKeys(recorded) {
		if _, found := current[key]; !found {
			t.Errorf("query gate: ledger entry %s was not found; delete it and lower the budget", key)
		}
	}
	total := 0
	for _, entry := range ledger.Entries {
		total += entry.Count
	}
	if total > ledger.Budget {
		t.Errorf("query gate: ledger records %d violations above its budget of %d", total, ledger.Budget)
	}
}

// TestQueryGateRulesRejectTheirFixtures proves each rule fires. Without this a
// rule that silently matched nothing would look exactly like a clean repository.
func TestQueryGateRulesRejectTheirFixtures(t *testing.T) {
	known := knownTableNames(t, backendRoot(t))
	fixture := filepath.Join("testdata", "query_violations", "violations.go")
	violations := inspectQueryFile(t, "", fixture, known)
	rules := map[string]bool{}
	for _, violation := range violations {
		rules[violation.Rule] = true
	}
	for _, rule := range []string{
		ruleHandwrittenSQL, ruleConcatenatedTableName, ruleNakedTableLiteral,
		ruleSQLInFormatString, ruleGormExprDynamic,
	} {
		assert.True(t, rules[rule], "fixture must violate %s", rule)
	}

	// Which lines fire, not merely which rules. A rule can keep firing on the
	// fixture while a narrowing quietly stops it from seeing the case it was
	// narrowed for, and "every rule fired" would still be true. Reading the
	// source back at each reported line is what makes the negative cases --
	// the metric name, the alias, the arithmetic -- assertable at all.
	assert.Equal(t, []string{
		`concatenated-table-name	db.Table(prefix + "iam_roles").Find(nil)`,
		`concatenated-table-name	func table(name string) string {`,
		`concatenated-table-name	return prefix + "iam_positions"`,
		`gorm-expr-dynamic	_ = gorm.Expr("SELECT id FROM iam_roles WHERE name = " + name)`,
		`handwritten-sql	db.Exec("UPDATE iam_roles SET status = ?", name)`,
		`handwritten-sql	db.Exec(statement)`,
		`handwritten-sql	db.Raw("SELECT id FROM iam_roles WHERE tenant_id = ?", name)`,
		`naked-table-literal	db.Table("iam_groups").Find(nil)`,
		`naked-table-literal	db.Table(prefix + "iam_roles").Find(nil)`,
		`naked-table-literal	db.Table(roleTable).Find(nil)`,
		`naked-table-literal	return prefix + "iam_positions"`,
		`sql-in-format-string	db.Exec(statement)`,
		`sql-in-format-string	db.Where(fmt.Sprintf("SELECT id FROM iam_role_members WHERE tenant_id = %s", name)).Find(nil)`,
	}, fixtureViolationLines(t, fixture, violations))

	// The sanctioned concatenation points must stay quiet. Binding the prefix
	// onto a table name is what the module shape requires of them, so a gate
	// that reported them would be asking for the opposite of the rule.
	for _, exempt := range []string{
		filepath.Join("..", "..", "internal", "iam", "core", "query", "table.go"),
		filepath.Join("..", "..", "pkg", "extension", "goose", "goose.go"),
		filepath.Join("..", "..", "pkg", "extension", "gorm", "clock.go"),
	} {
		assert.Empty(t, inspectQueryFile(t, "", exempt, known), "%s is a sanctioned concatenation point", exempt)
	}
}

// fixtureViolationLines pairs each violation with the fixture line it was
// reported on, sorted, so the expectation reads as the fixture does.
func fixtureViolationLines(t *testing.T, fixture string, violations []queryViolation) []string {
	t.Helper()
	source, err := os.ReadFile(fixture)
	require.NoError(t, err)
	lines := strings.Split(string(source), "\n")
	reported := make([]string, 0, len(violations))
	for _, violation := range violations {
		fields := strings.Split(violation.Where, ":")
		require.GreaterOrEqual(t, len(fields), 2, violation.Where)
		number, err := strconv.Atoi(fields[len(fields)-2])
		require.NoError(t, err, violation.Where)
		require.LessOrEqual(t, number, len(lines), violation.Where)
		reported = append(reported, violation.Rule+"\t"+strings.TrimSpace(lines[number-1]))
	}
	sort.Strings(reported)
	return reported
}

func detectQueryViolations(t *testing.T, root string) []queryViolation {
	t.Helper()
	known := knownTableNames(t, root)
	packages := goList(t, root, "./internal/...", "./pkg/...")
	var violations []queryViolation
	for _, pkg := range packages {
		if strings.Contains(filepath.ToSlash(pkg.ImportPath), "/proto/gen/") {
			continue
		}
		for _, name := range append(append([]string{}, pkg.GoFiles...), pkg.CgoFiles...) {
			if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".gen.go") {
				continue
			}
			violations = append(violations, inspectQueryFile(t, root, filepath.Join(pkg.Dir, name), known)...)
		}
	}
	return violations
}

// knownTableNames reads the logical table names the models declare, so the gate
// can tell a table from a string that merely looks like one.
//
// Shape alone was not enough: internal/iam/provisioning names eleven Prometheus
// metrics iam_scim_*, and the rule counted all eleven as tables. A name is a
// table when a model returns it from TableName(), which is the same source the
// migrations and the schema lock read, so the two cannot drift. The migration
// version tables are the exception -- no model owns them -- and are matched by
// shape instead.
func knownTableNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	require.NoError(t, filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if !strings.Contains(filepath.ToSlash(path), "/model/") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "TableName" || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				if value, err := strconv.Unquote(literal.Value); err == nil && value != "" {
					names[value] = true
				}
				return true
			})
		}
		return nil
	}))
	require.NotEmpty(t, names, "the models declare the table names this gate reads")
	return names
}

// tableNameOf reports the table a string literal names. A name may carry a SQL
// alias ("iam_roles AS role"), so only the first word is the name.
func tableNameOf(value string, known map[string]bool) (string, bool) {
	name := strings.TrimSpace(value)
	if index := strings.IndexAny(name, " 	"); index >= 0 {
		name = name[:index]
	}
	if known[name] || gooseTablePattern.MatchString(name) {
		return name, true
	}
	return "", false
}

// isTableHelperCall reports a call to a module's own prefix helper, whose
// argument is a table position.
func isTableHelperCall(fun ast.Expr) bool {
	switch typed := fun.(type) {
	case *ast.Ident:
		return tableHelperPattern.MatchString(typed.Name)
	case *ast.SelectorExpr:
		return tableHelperPattern.MatchString(typed.Sel.Name)
	}
	return false
}

// stringValues maps every identifier the file binds to a string literal, so a
// table name parked in a constant and handed to Table() later is still seen.
// Without it a module could put the name in a const and leave the rule behind.
func stringValues(file *ast.File) map[string]string {
	values := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for index, name := range spec.Names {
			if index >= len(spec.Values) {
				continue
			}
			if literal, ok := spec.Values[index].(*ast.BasicLit); ok && literal.Kind == token.STRING {
				if value, err := strconv.Unquote(literal.Value); err == nil {
					values[name.Name] = value
				}
			}
		}
		return true
	})
	return values
}

// assignedExpressions maps every identifier assigned once in the file to what
// it was assigned. It is how a Sprintf that reaches Exec through a local
// variable is still read as a format string: without it, one variable is enough
// to step around the rule.
func assignedExpressions(file *ast.File) map[string]ast.Expr {
	assigned := map[string]ast.Expr{}
	ast.Inspect(file, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			return true
		}
		if name, ok := assignment.Lhs[0].(*ast.Ident); ok {
			assigned[name.Name] = assignment.Rhs[0]
		}
		return true
	})
	return assigned
}

func inspectQueryFile(t *testing.T, root, filename string, known map[string]bool) []queryViolation {
	t.Helper()
	source, err := os.ReadFile(filename)
	require.NoError(t, err)
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, source, parser.ParseComments)
	require.NoError(t, err)

	// The key must not depend on where the checkout lives. Hashing an absolute
	// path would make the ledger fail for every other clone and in CI, and the
	// failure would look like a new violation rather than a moved repository.
	relative := filepath.ToSlash(filename)
	if root != "" {
		if rel, err := filepath.Rel(root, filename); err == nil {
			relative = filepath.ToSlash(rel)
		}
	}
	directory := path.Dir(relative)
	violations := make([]queryViolation, 0)

	// One physical name reported once. Five host nodes nest, so the same
	// literal used to be counted two or three times and the total said more
	// about the AST than about the repository.
	seen := map[string]bool{}
	record := func(rule, scope, text string, node ast.Node) {
		position := fileSet.Position(node.Pos()).String()
		if seen[rule+"@"+position] {
			return
		}
		seen[rule+"@"+position] = true
		violations = append(violations, queryViolation{
			Rule:  rule,
			Key:   queryViolationKey(rule, scope, text),
			Where: position,
		})
	}
	text := func(node ast.Node) string { return sourceText(fileSet, source, node) }
	constants, assignments := stringValues(file), assignedExpressions(file)

	// recordTableNames reports the table names an expression carries, whether
	// written there or bound to an identifier earlier. It is called only for
	// expressions in a table position -- an argument to a query method or to a
	// module's prefix helper, or a returned name -- so a string that merely
	// looks like a table, in a metric or a log line, is not read as one.
	recordTableNames := func(expressions []ast.Expr) {
		for _, expression := range expressions {
			ast.Inspect(expression, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.CallExpr, *ast.CompositeLit:
					// Do not descend. A nested call or struct is judged on its
					// own terms: fileResponse("todos", ...) returns a file named
					// after the table rather than a query against it, and
					// goosex.Source{TableName: "goose_iam"} declares the name of
					// a table the module owns, exactly as a model's TableName()
					// does -- the prefix is applied once, inside goosex.
					return false
				case *ast.BasicLit:
					if typed.Kind != token.STRING {
						return true
					}
					value, err := strconv.Unquote(typed.Value)
					if err != nil {
						return true
					}
					if name, ok := tableNameOf(value, known); ok {
						record(ruleNakedTableLiteral, directory, name, typed)
					}
				case *ast.Ident:
					if value, ok := constants[typed.Name]; ok {
						if name, found := tableNameOf(value, known); found {
							record(ruleNakedTableLiteral, directory, name, typed)
						}
					}
				}
				return true
			})
		}
	}

	if queryGateExemptFile(relative) {
		return nil
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncDecl:
			if typed.Name.Name == "table" && typed.Body != nil && containsPrefixAddition(fileSet, source, typed.Body) {
				record(ruleConcatenatedTableName, directory, "func "+typed.Name.Name, typed)
			}
		case *ast.CallExpr:
			// A module's own prefix helper takes a table name, so its argument
			// is a table position even though the callee is not a query method.
			if isTableHelperCall(typed.Fun) {
				recordTableNames(typed.Args)
			}
			selector, ok := typed.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			method := selector.Sel.Name
			if (method == "Raw" || method == "Exec") && len(typed.Args) > 0 && !isCallbackRegistration(selector) {
				record(ruleHandwrittenSQL, "", text(typed.Args[0]), typed.Args[0])
			}
			if method == "Expr" && len(typed.Args) > 0 && isGormImport(selector, file) {
				if format, dynamic := gormExprViolation(fileSet, source, typed.Args[0]); dynamic {
					record(ruleGormExprDynamic, "", format, typed.Args[0])
				}
			}
			for _, argument := range typed.Args {
				// Only an addition that builds a name out of a prefix. Any
				// addition also caught "version": version + 1, a sync.Map key,
				// and the ninety-odd places that append " AS alias" to a name
				// an owner already resolved -- none of which assembles a name.
				//
				// No second test that the result looks like a table: what
				// Table() receives is a table name by definition, and demanding
				// a literal here lost Table(prefix+owner.versions), where the
				// name arrives in a struct field and the prefix is applied
				// right there. On a return there is no such guarantee, which is
				// why that path still asks.
				if tableScopeMethods[method] && containsPrefixAddition(fileSet, source, argument) {
					record(ruleConcatenatedTableName, directory, text(argument), argument)
				}
				if argumentScopeMethods[method] {
					if format, dynamic := formatStringSQL(argument, assignments); dynamic {
						record(ruleSQLInFormatString, "", format, argument)
					}
				}
			}
			if tableScopeMethods[method] {
				recordTableNames(typed.Args)
			}
		case *ast.ReturnStmt:
			// A helper that returns prefix + "iam_roles" names a table just as
			// surely as one that passes it to Table(), and leaving the return
			// out let any function rename itself out of the rule. The table
			// test is what keeps it off the S3 object key and the URL prefix,
			// which are also built from something called prefix.
			recordTableNames(typed.Results)
			for _, result := range typed.Results {
				if containsPrefixAddition(fileSet, source, result) && namesATable(result, known) {
					record(ruleConcatenatedTableName, directory, text(result), result)
				}
			}
		}
		return true
	})
	return violations
}

func queryGateExemptFile(slashed string) bool {
	directory := path.Base(path.Dir(slashed))
	base := path.Base(slashed)
	// A module's bound table names. Applying the prefix is what this file is
	// for, and its Bind is the single place a table name is assembled.
	if directory == "query" && base == "table.go" {
		return true
	}
	// The model owns the logical name: its TableName() is the one place the
	// name is written down, and every other mention is derived from it.
	if strings.Contains(slashed, "/model/") {
		return true
	}
	// The dialect and prefix primitive layer. The database clock expression and
	// the migration version table name cannot be written without SQL text or
	// without the prefix, and both are called from every module -- which is
	// precisely why they are collected here instead of repeated everywhere.
	//
	// The leading slash is what makes this work on a relative path too: the
	// path is either absolute or rooted at the backend, and prepending one
	// slash normalizes both instead of silently dropping the exemption.
	rooted := "/" + strings.TrimPrefix(slashed, "/")
	if strings.Contains(rooted, "/pkg/extension/gorm/") || strings.Contains(rooted, "/pkg/extension/goose/") {
		return true
	}
	return false
}

func isCallbackRegistration(selector *ast.SelectorExpr) bool {
	inner, ok := selector.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	innerSelector, ok := inner.Fun.(*ast.SelectorExpr)
	return ok && innerSelector.Sel.Name == "Callback"
}

func isGormImport(selector *ast.SelectorExpr, file *ast.File) bool {
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	return importAliases(file)[identifier.Name] == "gorm.io/gorm"
}

// gormExprViolation reports a gorm.Expr that carries SQL: a literal naming a
// FROM or JOIN clause, or an assembled expression whose text does. The forms
// the repository relies on -- a column name and an operator, a server-side
// increment -- stay allowed, because a generator cannot express them and
// refusing them would push callers towards something worse.
func gormExprViolation(fileSet *token.FileSet, source []byte, expression ast.Expr) (string, bool) {
	if literal, ok := expression.(*ast.BasicLit); ok {
		if literal.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil || !exprKeywordPattern.MatchString(value) {
			return "", false
		}
		return value, true
	}
	text := sourceText(fileSet, source, expression)
	if sqlKeywordPattern.MatchString(text) || tableLiteralPattern.MatchString(strings.TrimSpace(text)) {
		return text, true
	}
	return "", false
}

// formatStringSQL reports a fmt.Sprintf carrying SQL. It follows one
// assignment, because the repository's single instance builds the statement
// into a local variable and hands that to Exec -- and a rule that only looked
// at the call argument was stepped around by naming the string.
func formatStringSQL(node ast.Node, assignments map[string]ast.Expr) (string, bool) {
	if identifier, ok := node.(*ast.Ident); ok {
		if assigned, found := assignments[identifier.Name]; found {
			return formatStringSQL(assigned, nil)
		}
	}
	var format string
	found := false
	ast.Inspect(node, func(child ast.Node) bool {
		call, ok := child.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Sprintf" || len(call.Args) == 0 {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil || !sqlKeywordPattern.MatchString(value) {
			return true
		}
		format, found = value, true
		return false
	})
	return format, found
}

// namesATable reports whether an expression builds a table name: it carries a
// logical table literal or calls the model's TableName(). A concatenation of
// something called prefix with an object key or a URL is not this, and the two
// are indistinguishable without looking for the table itself.
func namesATable(node ast.Node, known map[string]bool) bool {
	found := false
	ast.Inspect(node, func(child ast.Node) bool {
		if found {
			return false
		}
		switch typed := child.(type) {
		case *ast.BasicLit:
			if typed.Kind != token.STRING {
				return true
			}
			if value, err := strconv.Unquote(typed.Value); err == nil {
				if _, ok := tableNameOf(value, known); ok {
					found = true
					return false
				}
			}
		case *ast.SelectorExpr:
			if typed.Sel.Name == "TableName" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func containsPrefixAddition(fileSet *token.FileSet, source []byte, node ast.Node) bool {
	found := false
	ast.Inspect(node, func(child ast.Node) bool {
		if found {
			return false
		}
		binary, ok := child.(*ast.BinaryExpr)
		if !ok || binary.Op != token.ADD {
			return true
		}
		if prefixOperandPattern.MatchString(sourceText(fileSet, source, binary.X)) ||
			prefixOperandPattern.MatchString(sourceText(fileSet, source, binary.Y)) {
			found = true
			return false
		}
		return true
	})
	return found
}

func sourceText(fileSet *token.FileSet, source []byte, node ast.Node) string {
	start := fileSet.Position(node.Pos()).Offset
	end := fileSet.Position(node.End()).Offset
	if start < 0 || end > len(source) || start >= end {
		return ""
	}
	return string(source[start:end])
}

func queryViolationKey(rule, scope, text string) string {
	normalized := strings.Join(strings.Fields(text), " ")
	digest := sha256.Sum256([]byte(rule + "|" + scope + "|" + normalized))
	return rule + ":" + hex.EncodeToString(digest[:8])
}

func sortedKeys[V any](set map[string]V) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func readQueryGateLedger(t *testing.T, filename string) queryGateLedger {
	t.Helper()
	contents, err := os.ReadFile(filename)
	require.NoError(t, err)
	var ledger queryGateLedger
	require.NoError(t, json.Unmarshal(contents, &ledger))
	return ledger
}

// writeQueryGateLedger records the current findings. It refuses to raise the
// budget, so the only way to add a violation is to lower it deliberately in a
// reviewed change.
func writeQueryGateLedger(t *testing.T, filename string, current map[string]int) {
	t.Helper()
	total := 0
	for _, count := range current {
		total += count
	}
	budget, rebaseline := total, os.Getenv(queryGateRebaselineEnv)
	if previous, err := os.ReadFile(filename); err == nil {
		var ledger queryGateLedger
		require.NoError(t, json.Unmarshal(previous, &ledger))
		if total > ledger.Budget {
			if rebaseline == "" {
				t.Fatalf("query gate: %d violations exceed the frozen budget of %d. Fix them, or, if a rule change legitimately found more, set %s to the reason",
					total, ledger.Budget, queryGateRebaselineEnv)
			}
			t.Logf("query gate: budget raised from %d to %d: %s", ledger.Budget, total, rebaseline)
		} else {
			budget = ledger.Budget
			if rebaseline == "" {
				rebaseline = ledger.Rebaseline
			}
		}
	}
	entries := make([]queryGateEntry, 0, len(current))
	for _, key := range sortedKeys(current) {
		entries = append(entries, queryGateEntry{Key: key, Count: current[key]})
	}
	contents, err := json.MarshalIndent(queryGateLedger{SchemaVersion: 1, Budget: budget, Rebaseline: rebaseline, Entries: entries}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filename, append(contents, '\n'), 0o600))
	t.Logf("query gate ledger updated: %d entries, %d violations, budget %d", len(entries), total, budget)
}

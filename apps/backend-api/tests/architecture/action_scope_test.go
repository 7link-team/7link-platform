package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const authzImportPath = modulePathPrefix + "pkg/extension/authz"

// TestContributedActionsNameTheirScopeByConstant keeps an action's scope a
// named authz.ActionScope* value wherever a module contributes actions. The
// scope decides which grants an action is looked for in, and "platform" reads
// like the operating platform while it means the system scope: a literal is how
// a module ends up declaring the scope it did not mean.
func TestContributedActionsNameTheirScopeByConstant(t *testing.T) {
	root := backendRoot(t)
	checked := 0
	for _, pkg := range goList(t, root, "./internal/...", "./pkg/...") {
		if pkg.ImportPath == authzImportPath {
			continue // the package that defines the constants and the core catalog
		}
		for _, name := range pkg.GoFiles {
			if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".gen.go") {
				continue
			}
			filename := filepath.Join(pkg.Dir, name)
			found, literals := literalActionScopes(t, filename)
			checked += found
			for _, where := range literals {
				relative, err := filepath.Rel(root, where)
				require.NoError(t, err)
				t.Errorf("%s: authz.Action declares its Scope with a string literal; use an authz.ActionScope* constant", filepath.ToSlash(relative))
			}
		}
	}
	assert.Positive(t, checked, "the scan must reach the modules that contribute actions")
}

// literalActionScopes reports how many authz.Action literals the file declares
// and where one of them sets Scope to a string literal.
func literalActionScopes(t *testing.T, filename string) (int, []string) {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	alias := ""
	for name, importPath := range packageAliases(file) {
		if importPath == authzImportPath {
			alias = name
		}
	}
	if alias == "" {
		return 0, nil
	}
	isAction := func(expression ast.Expr) bool {
		selector, ok := expression.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Action" {
			return false
		}
		identifier, ok := selector.X.(*ast.Ident)
		return ok && identifier.Name == alias
	}
	var actions []*ast.CompositeLit
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if isAction(literal.Type) {
			actions = append(actions, literal)
			return true
		}
		// Elements of []authz.Action{...} elide their type.
		if array, ok := literal.Type.(*ast.ArrayType); ok && isAction(array.Elt) {
			for _, element := range literal.Elts {
				if nested, ok := element.(*ast.CompositeLit); ok && nested.Type == nil {
					actions = append(actions, nested)
				}
			}
		}
		return true
	})
	var literals []string
	for _, action := range actions {
		for _, element := range action.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := field.Key.(*ast.Ident)
			if !ok || key.Name != "Scope" {
				continue
			}
			if _, literal := field.Value.(*ast.BasicLit); literal {
				literals = append(literals, fileSet.Position(field.Value.Pos()).String())
			}
		}
	}
	return len(actions), literals
}

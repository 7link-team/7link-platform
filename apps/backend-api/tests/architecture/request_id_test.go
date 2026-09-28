package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const guidImportPath = modulePathPrefix + "internal/infra/guid"

// TestHandlersParseRequestIDsThroughRequireID keeps one rule for what a valid
// request identifier is. A handler reads an identifier from its input (in.X,
// input.X) through guid.RequireID or guid.OptionalID, which answer 422 for an
// empty, malformed, zero or negative value; a handler calling guid.Parse on its
// input grows its own copy of that rule and its own error.
func TestHandlersParseRequestIDsThroughRequireID(t *testing.T) {
	root := backendRoot(t)
	for _, pkg := range goList(t, root, "./internal/...", "./pkg/...") {
		if pkg.ImportPath == guidImportPath {
			continue
		}
		for _, name := range pkg.GoFiles {
			if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".gen.go") {
				continue
			}
			filename := filepath.Join(pkg.Dir, name)
			for _, where := range handlerInputParses(t, filename) {
				relative, err := filepath.Rel(root, where)
				require.NoError(t, err)
				t.Errorf("%s: guid.Parse on handler input; use guid.RequireID or guid.OptionalID", filepath.ToSlash(relative))
			}
		}
	}
}

func TestHandlerInputParseGateRejectsItsFixture(t *testing.T) {
	found := handlerInputParses(t, filepath.Join("testdata", "request_ids", "violations.go"))
	require.Len(t, found, 2, "both handler-input spellings must be reported: %v", found)
}

// handlerInputParses reports guid.Parse calls whose argument is a field of a
// handler input, i.e. a selector rooted at an identifier named in or input.
func handlerInputParses(t *testing.T, filename string) []string {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	alias := ""
	for name, importPath := range packageAliases(file) {
		if importPath == guidImportPath {
			alias = name
		}
	}
	if alias == "" {
		return nil
	}
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		function, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || function.Sel.Name != "Parse" {
			return true
		}
		if pkgIdent, ok := function.X.(*ast.Ident); !ok || pkgIdent.Name != alias {
			return true
		}
		argument := call.Args[0]
		for {
			selector, ok := argument.(*ast.SelectorExpr)
			if !ok {
				break
			}
			argument = selector.X
		}
		if root, ok := argument.(*ast.Ident); ok && root != call.Args[0] && (root.Name == "in" || root.Name == "input") {
			found = append(found, fileSet.Position(call.Pos()).String())
		}
		return true
	})
	return found
}

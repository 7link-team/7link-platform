package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// productName matches the spellings of the product that first ran this code.
// The Go module path (7link.com/platform/...) is the code's own name and is
// exempt; a deployment's name is configuration.
var productName = regexp.MustCompile(`(?i)seven[\s_-]*link|7link`)

// TestSourceCarriesNoDeploymentIdentity keeps the product name out of Go string
// literals -- production code, tests and fixtures alike (CLAUDE.md: deployment
// identity is configuration, not code). A fixture named after the product
// reads as the default a deployment gets, and the next one copies it.
func TestSourceCarriesNoDeploymentIdentity(t *testing.T) {
	root := backendRoot(t)
	scanned := 0
	for _, directory := range []string{"cmd", "internal", "pkg", "tests"} {
		require.NoError(t, filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir // fixtures, including this gate's own
				}
				return nil
			}
			// This file spells the pattern it looks for.
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".pb.go") || filepath.Base(path) == "deployment_identity_test.go" {
				return nil
			}
			scanned++
			for _, where := range productNameLiterals(t, path) {
				relative, relErr := filepath.Rel(root, where)
				require.NoError(t, relErr)
				t.Errorf("%s: string literal names the product; use neutral wording or read it from configuration", filepath.ToSlash(relative))
			}
			return nil
		}))
	}
	assert.Greater(t, scanned, 500, "the scan must reach the source tree")
}

func TestDeploymentIdentityGateRejectsItsFixture(t *testing.T) {
	found := productNameLiterals(t, filepath.Join("testdata", "deployment_identity", "violations.go"))
	assert.Len(t, found, 3, "each spelling must be reported and the module path must not: %v", found)
}

func productNameLiterals(t *testing.T, filename string) []string {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	imports := map[*ast.BasicLit]bool{}
	for _, spec := range file.Imports {
		imports[spec.Path] = true
	}
	var found []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING || imports[literal] {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			value = literal.Value
		}
		if productName.MatchString(strings.ReplaceAll(value, modulePathPrefix, "")) {
			found = append(found, fileSet.Position(literal.Pos()).String())
		}
		return true
	})
	return found
}

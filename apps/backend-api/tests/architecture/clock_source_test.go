package architecture_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClockSourcesHandOutUTC keeps every stored clock in UTC.
//
// The drivers serialize a time.Time with the offset it carries and the engines
// order those values as text, so an instant stored with a local offset sorts by
// its wall clock instead of its instant. In UTC+8 a session expired an hour ago
// sorted seven hours into the future and kept authenticating, because
// "…17:21+08:00" is greater than "…10:21+00:00" as a string.
//
// The rule is about where the normalization lives, not whether it happens.
// Every caller in internal/iam/authn already wrote now := s.now().UTC() — all
// thirty-three of them — and the defect still landed, in the thirty-fourth
// place nobody thought of. Normalizing at the clock is the only version of this
// that a reader cannot get wrong.
//
// The check looks for time.Now stored as a value rather than called, because
// that is unambiguously a clock a service will hand around, whatever the field
// is named. Measuring elapsed time with such a clock is unaffected: UTC changes
// the zone a time carries, never the instant it names.
//
// It exists as a static gate because the defect is invisible on a UTC host.
// There time.Local is UTC, every write is already normalized, and a behavioral
// test passes whether or not the bug is present. Only reading the source
// catches it before someone in another zone does.
func TestClockSourcesHandOutUTC(t *testing.T) {
	root := backendRoot(t)
	violations, err := bareClockSources(root)
	require.NoError(t, err)
	assert.Empty(t, violations,
		"a stored clock must hand out UTC: write func() time.Time { return time.Now().UTC() }, "+
			"the way internal/iam/core/repository.go does, instead of bare time.Now")
}

// bareClockSources reports every place that stores the time.Now function itself
// instead of a UTC-normalizing wrapper. Calls to time.Now() are left alone: a
// value that is used and discarded never reaches a column.
func bareClockSources(root string) ([]string, error) {
	var violations []string
	fileSet := token.NewFileSet()
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if filename != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".gen.go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(filename), "/proto/gen/") {
			return nil
		}
		file, parseErr := parser.ParseFile(fileSet, filename, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", filename, parseErr)
		}
		relative, relErr := filepath.Rel(root, filename)
		if relErr != nil {
			relative = filename
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.KeyValueExpr:
				if key, ok := typed.Key.(*ast.Ident); ok && isBareTimeNow(typed.Value) {
					violations = append(violations, fmt.Sprintf("%s:%d field %s",
						filepath.ToSlash(relative), fileSet.Position(typed.Value.Pos()).Line, key.Name))
				}
			case *ast.AssignStmt:
				for _, value := range typed.Rhs {
					if isBareTimeNow(value) {
						violations = append(violations, fmt.Sprintf("%s:%d assignment",
							filepath.ToSlash(relative), fileSet.Position(value.Pos()).Line))
					}
				}
			}
			return true
		})
		return nil
	})
	sort.Strings(violations)
	return violations, err
}

// isBareTimeNow reports whether the expression is time.Now referenced as a
// value. A CallExpr is a different thing entirely and never matches here.
func isBareTimeNow(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil || selector.Sel.Name != "Now" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "time"
}

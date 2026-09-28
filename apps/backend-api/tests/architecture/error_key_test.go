package architecture_test

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The error-key gate keeps huma and respx error messages localizable. The
// localizer looks a message up exactly, and every bundle is registered under its
// namespace ("respx.unauthorized", "authn.invalid_login"), so a bare
// "unauthorized" never resolves: the client receives the raw key in every
// locale, and nothing in the rest of the suite notices, because the status code
// is still right.
const (
	ruleBareErrorKey       = "bare-error-key"
	ruleUnresolvedErrorKey = "unresolved-error-key"

	modulePathPrefix = "7link.com/platform/"
	humaImportPath   = "github.com/danielgtaylor/huma/v2"
	respxImportPath  = modulePathPrefix + "pkg/extension/huma/respx"
)

// frontendContractKeys are bare keys the web client still branches on, because
// the error envelope carries no other discriminator between two failures that
// share a status (a wrong captcha and a wrong email are both 422). Qualifying
// them would localize the message and silently break that branch, so they stay
// bare until the envelope carries a machine-readable reason. The list is closed:
// a new entry needs the same justification, and an entry no longer found fails.
var frontendContractKeys = map[string]string{
	"internal/iam/authn/http.go|captcha_invalid":        "register page tells a rejected captcha from rejected registration data",
	"internal/files/http.go|file_restore_name_conflict": "files page offers a rename when a restore target name is taken",
	"internal/files/http.go|file_state_changed":         "file operations restart preflight when the tree changed underneath",
}

// errorKeyArguments names, per package, the helpers whose argument at the given
// index is an i18n message key. A huma Error<status> helper takes the key first.
var errorKeyArguments = map[string]map[string]int{
	humaImportPath:  {"NewError": 1},
	respxImportPath: {"Err": 2, "OKMessage": 2, "WriteError": 3},
}

var supportedErrorLocales = []string{"en-US", "zh-CN", "ms-MY"}

type errorKeyViolation struct {
	Rule  string
	Where string
	Key   string
}

func TestErrorKeysAreNamespacedAndResolve(t *testing.T) {
	root := backendRoot(t)
	catalog := loadLocaleCatalog(t, root)
	resolver := newConstantResolver(root)
	var violations []errorKeyViolation
	allowed := map[string]bool{}
	for _, pkg := range goList(t, root, "./internal/...", "./pkg/...") {
		if strings.Contains(filepath.ToSlash(pkg.ImportPath), "/proto/gen/") {
			continue
		}
		for _, name := range pkg.GoFiles {
			if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".gen.go") {
				continue
			}
			filename := filepath.Join(pkg.Dir, name)
			relative, err := filepath.Rel(root, filename)
			require.NoError(t, err)
			for _, violation := range inspectErrorKeys(t, filename, pkg.Dir, catalog, resolver) {
				contract := filepath.ToSlash(relative) + "|" + violation.Key
				if violation.Rule == ruleBareErrorKey && frontendContractKeys[contract] != "" {
					allowed[contract] = true
					continue
				}
				violation.Where = strings.Replace(violation.Where, filename, filepath.ToSlash(relative), 1)
				violations = append(violations, violation)
			}
		}
	}
	for _, violation := range violations {
		t.Errorf("%s: %s: %q; pass a namespaced key constant (respx.Unauthorized, the module's i18n package) present in every locale",
			violation.Rule, violation.Where, violation.Key)
	}
	for contract := range frontendContractKeys {
		assert.True(t, allowed[contract], "frontend contract key %s is no longer used; delete the entry", contract)
	}
}

// TestErrorKeyGateRejectsItsFixture proves both rules fire, and on which lines:
// a rule that silently matched nothing would look exactly like a clean tree.
func TestErrorKeyGateRejectsItsFixture(t *testing.T) {
	root := backendRoot(t)
	fixture := filepath.Join("testdata", "error_keys", "violations.go")
	violations := inspectErrorKeys(t, fixture, filepath.Dir(fixture), loadLocaleCatalog(t, root), newConstantResolver(root))
	source, err := os.ReadFile(fixture)
	require.NoError(t, err)
	lines := strings.Split(string(source), "\n")
	reported := make([]string, 0, len(violations))
	for _, violation := range violations {
		fields := strings.Split(violation.Where, ":")
		number, err := strconv.Atoi(fields[len(fields)-2])
		require.NoError(t, err, violation.Where)
		reported = append(reported, violation.Rule+"\t"+strings.TrimSpace(lines[number-1]))
	}
	sort.Strings(reported)
	assert.Equal(t, []string{
		"bare-error-key\t\"not_found\",",
		"bare-error-key\treturn huma.Error401Unauthorized(unauthorized)",
		"bare-error-key\treturn huma.Error403Forbidden(\"forbidden\")",
		"bare-error-key\treturn huma.NewError(http.StatusConflict, \"conflict\")",
		"bare-error-key\treturn respx.Err(ctx, 100001, \"business_rule\")",
		"unresolved-error-key\treturn huma.Error404NotFound(\"respx.no_such_key\")",
		"unresolved-error-key\treturn huma.Error422UnprocessableEntity(respx.Name + \".missing\")",
	}, reported)
}

func inspectErrorKeys(t *testing.T, filename, dir string, catalog localeCatalog, resolver *constantResolver) []errorKeyViolation {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	aliases := packageAliases(file)
	locals := stringValues(file)
	var violations []errorKeyViolation
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		index, ok := errorKeyArgument(call, aliases)
		if !ok || index >= len(call.Args) {
			return true
		}
		argument := call.Args[index]
		where := fileSet.Position(argument.Pos()).String()
		key, known := errorKeyValue(argument, locals, aliases, dir, resolver)
		switch {
		case !known, key == "":
			// A computed message (a field error's text, a helper's result) is
			// not a literal; the gate judges only what it can read. An empty
			// message is no key at all: huma.NewError(0, "") probes the
			// installed error type for its schema and is never sent.
		case !strings.Contains(key, "."):
			violations = append(violations, errorKeyViolation{Rule: ruleBareErrorKey, Where: where, Key: key})
		case !catalog.resolves(key):
			violations = append(violations, errorKeyViolation{Rule: ruleUnresolvedErrorKey, Where: where, Key: key})
		}
		return true
	})
	return violations
}

func errorKeyArgument(call *ast.CallExpr, aliases map[string]string) (int, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return 0, false
	}
	importPath := aliases[identifier.Name]
	if importPath == humaImportPath && selector.Sel.Name != "NewError" && strings.HasPrefix(selector.Sel.Name, "Error") {
		return 0, true
	}
	index, ok := errorKeyArguments[importPath][selector.Sel.Name]
	return index, ok
}

// errorKeyValue reads the key an argument carries when it is a constant
// expression: a literal, a file-level constant, or an exported constant of a
// platform package ("respx.Unauthorized", "authni18n.InvalidLogin").
func errorKeyValue(argument ast.Expr, locals map[string]string, aliases map[string]string, dir string, resolver *constantResolver) (string, bool) {
	switch expression := argument.(type) {
	case *ast.BasicLit:
		if expression.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(expression.Value)
		return value, err == nil
	case *ast.Ident:
		if value, ok := locals[expression.Name]; ok {
			return value, true
		}
		return resolver.value(dir, expression.Name)
	default:
		return resolver.evaluate(dir, aliases, argument)
	}
}

// packageAliases maps each import's local name to its path, naming a
// major-version suffixed module ("huma/v2") by its package, not by "v2".
func packageAliases(file *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		segments := strings.Split(importPath, "/")
		alias := segments[len(segments)-1]
		if regexp.MustCompile(`^v[0-9]+$`).MatchString(alias) && len(segments) > 1 {
			alias = segments[len(segments)-2]
		}
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		aliases[alias] = importPath
	}
	return aliases
}

// constantResolver evaluates string constants across the platform's own
// packages, parsing each package directory once.
type constantResolver struct {
	root     string
	packages map[string]*constantPackage
}

type constantPackage struct {
	constants map[string]ast.Expr
	aliases   map[string]map[string]string
}

func newConstantResolver(root string) *constantResolver {
	return &constantResolver{root: root, packages: map[string]*constantPackage{}}
}

func (r *constantResolver) load(dir string) *constantPackage {
	if loaded, ok := r.packages[dir]; ok {
		return loaded
	}
	loaded := &constantPackage{constants: map[string]ast.Expr{}, aliases: map[string]map[string]string{}}
	r.packages[dir] = loaded
	entries, err := os.ReadDir(dir)
	if err != nil {
		return loaded
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		aliases := packageAliases(file)
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				for index, identifier := range value.Names {
					if index < len(value.Values) {
						loaded.constants[identifier.Name] = value.Values[index]
						loaded.aliases[identifier.Name] = aliases
					}
				}
			}
		}
	}
	return loaded
}

func (r *constantResolver) value(dir, name string) (string, bool) {
	loaded := r.load(dir)
	expression, ok := loaded.constants[name]
	if !ok {
		return "", false
	}
	return r.evaluate(dir, loaded.aliases[name], expression)
}

func (r *constantResolver) evaluate(dir string, aliases map[string]string, expression ast.Expr) (string, bool) {
	switch node := expression.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(node.Value)
		return value, err == nil
	case *ast.Ident:
		return r.value(dir, node.Name)
	case *ast.ParenExpr:
		return r.evaluate(dir, aliases, node.X)
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}
		left, ok := r.evaluate(dir, aliases, node.X)
		if !ok {
			return "", false
		}
		right, ok := r.evaluate(dir, aliases, node.Y)
		return left + right, ok
	case *ast.SelectorExpr:
		identifier, ok := node.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		importPath, ok := aliases[identifier.Name]
		if !ok || !strings.HasPrefix(importPath, modulePathPrefix) {
			return "", false
		}
		return r.value(filepath.Join(r.root, filepath.FromSlash(strings.TrimPrefix(importPath, modulePathPrefix))), node.Sel.Name)
	}
	return "", false
}

// localeCatalog holds, per namespace, the keys every supported locale defines:
// the same set RegisterFS publishes as "namespace.key".
type localeCatalog map[string]map[string]bool

func (c localeCatalog) resolves(key string) bool {
	namespace, local, ok := strings.Cut(key, ".")
	return ok && c[namespace][local]
}

// loadLocaleCatalog finds every locale bundle under internal/ and pkg/ and reads
// its namespace from the constant beside it (Namespace, or respx's Name).
func loadLocaleCatalog(t *testing.T, root string) localeCatalog {
	t.Helper()
	catalog := localeCatalog{}
	resolver := newConstantResolver(root)
	for _, base := range []string{"internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, base), func(filename string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || !entry.IsDir() || entry.Name() != "locales" {
				return walkErr
			}
			owner := filepath.Dir(filename)
			namespace, ok := resolver.value(owner, "Namespace")
			if !ok {
				namespace, ok = resolver.value(owner, "Name")
			}
			if !ok {
				return fmt.Errorf("%s: locale bundle without a Namespace or Name constant", owner)
			}
			keys := map[string]bool{}
			for index, locale := range supportedErrorLocales {
				content, err := os.ReadFile(filepath.Join(filename, locale+".json"))
				if err != nil {
					return err
				}
				messages := map[string]string{}
				if err := json.Unmarshal(content, &messages); err != nil {
					return fmt.Errorf("%s/%s.json: %w", filename, locale, err)
				}
				for key, message := range messages {
					if index == 0 && message != "" {
						keys[key] = true
					}
				}
				for key := range keys {
					if messages[key] == "" {
						delete(keys, key)
					}
				}
			}
			catalog[namespace] = keys
			return filepath.SkipDir
		})
		require.NoError(t, err)
	}
	require.Contains(t, catalog, "respx", "the respx bundle must be discovered")
	return catalog
}

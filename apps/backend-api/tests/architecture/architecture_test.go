package architecture_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	iamImportPrefix = "7link.com/platform/internal/iam"
	appImportPrefix = "7link.com/platform/internal/app"
)

var iamModules = []string{
	"audit", "authn", "core", "federation", "governance", "identity", "oauth", "organization", "provisioning",
}

type listedPackage struct {
	Dir        string
	ImportPath string
	GoFiles    []string
	CgoFiles   []string
}

type architectureViolation struct {
	Rule    string
	File    string
	Message string
}

type sourceInspection struct {
	violations   []architectureViolation
	declaredDeps map[string]bool
	resolvedDeps map[string]bool
}

func TestIAMModuleArchitecture(t *testing.T) {
	root := backendRoot(t)
	packages := goList(t, root, "./internal/...")
	violations := validateIAMArchitecture(root, packages)
	for _, violation := range violations {
		t.Errorf("%s: %s: %s", violation.Rule, violation.File, violation.Message)
	}
}

func TestIAMModuleArchitectureRejectsViolatingFixture(t *testing.T) {
	fixture := filepath.Join("testdata", "violating_module", "module.go")
	inspection := inspectSourceFile(t, fixture, "provisioning", "7link.com/platform/internal/iam/provisioning")
	identityPath := iamImportPrefix + "/identity"
	guidPath := "7link.com/platform/internal/infra/guid"
	assert.True(t, inspection.declaredDeps[identityPath])
	assert.True(t, inspection.resolvedDeps[identityPath])
	assert.True(t, inspection.resolvedDeps[guidPath])
	assert.False(t, inspection.declaredDeps[guidPath])
	rules := make(map[string]bool)
	for _, violation := range inspection.violations {
		rules[violation.Rule] = true
	}
	for dependency := range inspection.resolvedDeps {
		if !inspection.declaredDeps[dependency] {
			rules["module-dependency"] = true
		}
	}
	assert.True(t, rules["cross-module-storage-import"])
	assert.True(t, rules["cross-module-concrete-type"])
	assert.True(t, rules["cross-module-constructor"])
	assert.True(t, rules["module-dependency"])
	assert.True(t, rules["public-storage-type"])

	appLookalike := inspectSourceFile(t, fixture, "provisioning", appImportPrefix+"lookalike")
	assert.Contains(t, violationRules(appLookalike.violations), "cross-module-constructor")
}

func TestIAMBusinessSourceClassification(t *testing.T) {
	testCases := []struct {
		name  string
		files []string
		want  bool
	}{
		{name: "empty"},
		{name: "lifecycle only", files: []string{"module.go", "migrate.go"}},
		{name: "documentation only", files: []string{"module.go", "migrate.go", "doc.go"}},
		{name: "tests do not count", files: []string{"module.go", "migrate.go", "service_test.go"}},
		{name: "business source", files: []string{"module.go", "migrate.go", "service.go"}, want: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, hasIAMBusinessSource(testCase.files))
		})
	}
}

// TestIAMModulesOwnTheirLifecycleWiring prevents the composition root from
// drifting back into constructing IAM collaborators or maintaining a second
// declaration-only module graph.
func TestIAMModulesOwnTheirLifecycleWiring(t *testing.T) {
	root := backendRoot(t)
	for _, moduleName := range iamModules {
		filename := filepath.Join(root, "internal", "iam", moduleName, "module.go")
		parsed, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.SkipObjectResolution)
		require.NoError(t, err)

		var constructor *ast.FuncDecl
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			assert.NotEqual(t, "NewDeclarationOnlyModule", function.Name.Name, "%s must have one runtime construction path", moduleName)
			if function.Name.Name == "NewModule" {
				constructor = function
			}
		}
		require.NotNil(t, constructor, "%s must expose NewModule", moduleName)
		require.Len(t, constructor.Type.Params.List, 2, "%s NewModule accepts only runtime and config", moduleName)
		assert.True(t, isRuntimeParameter(constructor.Type.Params.List[0].Type), "%s NewModule first parameter must be *appenv.Runtime", moduleName)
		config, ok := constructor.Type.Params.List[1].Type.(*ast.Ident)
		assert.True(t, ok && config.Name == "Config", "%s NewModule second parameter must be Config", moduleName)

		source, err := os.ReadFile(filename)
		require.NoError(t, err)
		assert.NotContains(t, string(source), "declarationOnly", "%s must not keep a synthetic lifecycle branch", moduleName)
	}

	appSource, err := os.ReadFile(filepath.Join(root, "internal", "app", "app.go"))
	require.NoError(t, err)
	assert.NotContains(t, string(appSource), "authzRegistrar", "App must not retain IAM authorization state")

	bootstrapSource, err := os.ReadFile(filepath.Join(root, "internal", "app", "bootstrap.go"))
	require.NoError(t, err)
	for _, forbidden := range []string{
		"composeIAMDeclarationModules", "authz.NewRegistrar", "authz.NewUnboundRegistrar",
		"core.NewAuthorizer", "core.NewMembershipChecker", "WithRegistrationPrincipalCreator",
	} {
		assert.NotContains(t, string(bootstrapSource), forbidden, "composition root must only declare the IAM inventory")
	}
}

func isRuntimeParameter(expression ast.Expr) bool {
	pointer, ok := expression.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Runtime" {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	return ok && qualifier.Name == "appenv"
}

// TestGoTestFileNamesMatchProductionFiles keeps tests traceable to the
// production file whose behavior they exercise. Dedicated black-box test
// packages are exempt because they intentionally have no production Go files.
func TestGoTestFileNamesMatchProductionFiles(t *testing.T) {
	root := backendRoot(t)
	violations, err := testFileNamingViolations(root)
	require.NoError(t, err)
	assert.Empty(t, violations,
		"a test must be named xx_test.go or xx_subxx_test.go for an existing xx.go in the same directory")
}

func TestTestFileMatchesProductionPrefix(t *testing.T) {
	production := []string{"service", "service_account", "http"}
	for _, testCase := range []struct {
		name string
		stem string
		want bool
	}{
		{name: "exact", stem: "service", want: true},
		{name: "qualified", stem: "service_failure", want: true},
		{name: "longer exact", stem: "service_account", want: true},
		{name: "longer qualified", stem: "service_account_http", want: true},
		{name: "partial word", stem: "services"},
		{name: "missing source", stem: "repository"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testFileMatchesProductionPrefix(testCase.stem, production))
		})
	}
}

func testFileNamingViolations(root string) ([]string, error) {
	type directoryFiles struct {
		production []string
		tests      []string
	}
	directories := map[string]*directoryFiles{}
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if filename != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".go" {
			return nil
		}
		directory := filepath.Dir(filename)
		files := directories[directory]
		if files == nil {
			files = &directoryFiles{}
			directories[directory] = files
		}
		stem := strings.TrimSuffix(entry.Name(), ".go")
		if strings.HasSuffix(stem, "_test") {
			files.tests = append(files.tests, strings.TrimSuffix(stem, "_test"))
		} else {
			files.production = append(files.production, stem)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	violations := make([]string, 0)
	for directory, files := range directories {
		if len(files.production) == 0 {
			continue
		}
		for _, testStem := range files.tests {
			if !testFileMatchesProductionPrefix(testStem, files.production) {
				relative, relativeErr := filepath.Rel(root, filepath.Join(directory, testStem+"_test.go"))
				if relativeErr != nil {
					return nil, relativeErr
				}
				violations = append(violations, filepath.ToSlash(relative))
			}
		}
	}
	sort.Strings(violations)
	return violations, nil
}

func testFileMatchesProductionPrefix(testStem string, production []string) bool {
	for _, productionStem := range production {
		if testStem == productionStem || strings.HasPrefix(testStem, productionStem+"_") {
			return true
		}
	}
	return false
}

func validateIAMArchitecture(root string, packages []listedPackage) []architectureViolation {
	moduleSet := stringSet(iamModules)
	iamRoot := filepath.Join(root, "internal", "iam")
	violations := validateIAMLayout(iamRoot, packages, moduleSet)
	declared := make(map[string]map[string]bool, len(iamModules))
	resolved := make(map[string]map[string]bool, len(iamModules))
	for _, moduleName := range iamModules {
		declared[moduleName] = make(map[string]bool)
		resolved[moduleName] = make(map[string]bool)
	}

	for _, pkg := range packages {
		owner, _, isIAM := iamImport(pkg.ImportPath)
		if pkg.ImportPath == iamImportPrefix {
			violations = append(violations, architectureViolation{
				Rule: "domain-composition-root", File: pkg.ImportPath,
				Message: "internal/iam must remain a directory namespace without a Go package",
			})
		}
		for _, name := range append(append([]string{}, pkg.GoFiles...), pkg.CgoFiles...) {
			filename := filepath.Join(pkg.Dir, name)
			inspection := inspectSourceFile(nil, filename, owner, pkg.ImportPath)
			violations = append(violations, inspection.violations...)
			if isIAM && moduleSet[owner] {
				mergeSet(declared[owner], inspection.declaredDeps)
				mergeSet(resolved[owner], inspection.resolvedDeps)
			}
		}
	}

	for _, moduleName := range iamModules {
		for dependency := range resolved[moduleName] {
			if !declared[moduleName][dependency] {
				violations = append(violations, architectureViolation{
					Rule: "module-dependency", File: moduleName + "/module.go",
					Message: fmt.Sprintf("module.Resolve uses a contract from %s without declaring that provider's Name in ModuleDependencies", dependency),
				})
			}
		}
	}
	return uniqueViolations(violations)
}

func validateIAMLayout(iamRoot string, packages []listedPackage, moduleSet map[string]bool) []architectureViolation {
	var violations []architectureViolation
	entries, err := os.ReadDir(iamRoot)
	if err != nil {
		return []architectureViolation{{Rule: "module-layout", File: iamRoot, Message: err.Error()}}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !moduleSet[entry.Name()] {
			violations = append(violations, architectureViolation{
				Rule: "module-inventory", File: filepath.Join(iamRoot, entry.Name()),
				Message: "unexpected directory directly under the IAM namespace",
			})
		}
		if isCentralPackageName(entry.Name()) {
			violations = append(violations, architectureViolation{
				Rule: "central-port-schema", File: filepath.Join(iamRoot, entry.Name()),
				Message: "IAM contracts and schemas must remain provider-owned",
			})
		}
	}
	rootFiles, err := filepath.Glob(filepath.Join(iamRoot, "*.go"))
	if err != nil {
		violations = append(violations, architectureViolation{Rule: "module-layout", File: iamRoot, Message: err.Error()})
	} else if len(rootFiles) != 0 {
		violations = append(violations, architectureViolation{
			Rule: "domain-composition-root", File: iamRoot,
			Message: "internal/iam must not contain Go files",
		})
	}

	rootPackages := make(map[string]bool)
	rootPackageFiles := make(map[string][]string)
	listedPackages := make(map[string]bool)
	for _, pkg := range packages {
		moduleName, suffix, ok := iamImport(pkg.ImportPath)
		if !ok {
			continue
		}
		listedPackages[pkg.ImportPath] = true
		if suffix == "" {
			rootPackages[moduleName] = true
			rootPackageFiles[moduleName] = append(append([]string{}, pkg.GoFiles...), pkg.CgoFiles...)
		}
		for _, segment := range strings.Split(suffix, "/") {
			if isCentralPackageName(segment) {
				violations = append(violations, architectureViolation{
					Rule: "central-port-schema", File: pkg.ImportPath,
					Message: "central IAM port/schema packages are forbidden",
				})
			}
		}
	}
	for moduleName := range rootPackages {
		if !moduleSet[moduleName] {
			violations = append(violations, architectureViolation{
				Rule: "module-inventory", File: iamImportPrefix + "/" + moduleName,
				Message: "unexpected IAM module root",
			})
		}
	}

	for _, moduleName := range iamModules {
		moduleDir := filepath.Join(iamRoot, moduleName)
		if !rootPackages[moduleName] {
			violations = append(violations, architectureViolation{
				Rule: "module-inventory", File: moduleDir, Message: "missing Go package for IAM module root",
			})
		}
		for _, child := range []string{"model", "query", "dbgen"} {
			childDir := filepath.Join(moduleDir, child)
			if !hasFileWithExtension(childDir, ".go") {
				violations = append(violations, architectureViolation{
					Rule: "module-ownership", File: childDir, Message: "module must own Go source in " + child,
				})
			}
			if !listedPackages[iamImportPrefix+"/"+moduleName+"/"+child] {
				violations = append(violations, architectureViolation{
					Rule: "module-ownership", File: childDir, Message: "go list did not discover the owned " + child + " package",
				})
			}
		}
		if !fileExists(filepath.Join(moduleDir, "module.go")) ||
			!fileExists(filepath.Join(moduleDir, "migrate.go")) ||
			!hasIAMBusinessSource(rootPackageFiles[moduleName]) {
			violations = append(violations, architectureViolation{
				Rule: "module-ownership", File: moduleDir, Message: "module root must own module.go, migrate.go, and production Go source",
			})
		}
		for _, dialect := range []string{"postgres", "mysql", "sqlite"} {
			dialectDir := filepath.Join(moduleDir, "sql", dialect)
			if !hasFileWithExtension(dialectDir, ".sql") {
				violations = append(violations, architectureViolation{
					Rule: "module-ownership", File: dialectDir, Message: "module must own at least one migration",
				})
			}
		}
	}
	return violations
}

func inspectSourceFile(t *testing.T, filename, owner, importPath string) sourceInspection {
	if t != nil {
		t.Helper()
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.SkipObjectResolution)
	if err != nil {
		if t != nil {
			require.NoError(t, err)
		}
		return sourceInspection{violations: []architectureViolation{{Rule: "go-ast", File: filename, Message: err.Error()}}}
	}
	aliases := importAliases(parsed)
	inspection := sourceInspection{declaredDeps: make(map[string]bool), resolvedDeps: make(map[string]bool)}

	for _, spec := range parsed.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		target, suffix, ok := iamImport(imported)
		if !ok || target == owner || suffix == "" {
			continue
		}
		first := strings.Split(suffix, "/")[0]
		if first == "model" || first == "query" || first == "dbgen" {
			inspection.violations = append(inspection.violations, architectureViolation{
				Rule: "cross-module-storage-import", File: filename,
				Message: fmt.Sprintf("%s imports %s-owned persistence package %s", owner, target, imported),
			})
		}
	}

	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && importPath == iamImportPrefix+"/"+owner && isModuleDependenciesMethod(function) {
			recordReturnedDependencies(function, aliases, inspection.declaredDeps)
		}
		if importPath == iamImportPrefix+"/"+owner {
			inspectExportedSignature(declaration, aliases, filename, &inspection)
		}
	}

	ast.Inspect(parsed, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.SelectorExpr:
			target, suffix, ok := importedSelector(current, aliases)
			if ok && target != owner && suffix == "" && isConcreteModuleType(current.Sel.Name) && !isAppPackage(importPath) {
				inspection.violations = append(inspection.violations, architectureViolation{
					Rule: "cross-module-concrete-type", File: filename,
					Message: fmt.Sprintf("%s references concrete %s.%s", owner, target, current.Sel.Name),
				})
			}
		case *ast.CallExpr:
			inspectCall(current, aliases, owner, importPath, filename, &inspection)
		}
		return true
	})
	return inspection
}

func isModuleDependenciesMethod(function *ast.FuncDecl) bool {
	if function.Name.Name != "ModuleDependencies" || function.Recv == nil || len(function.Recv.List) != 1 {
		return false
	}
	receiver := function.Recv.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	identifier, ok := receiver.(*ast.Ident)
	return ok && identifier.Name == "Module"
}

func recordReturnedDependencies(function *ast.FuncDecl, aliases map[string]string, dependencies map[string]bool) {
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			for _, result := range current.Results {
				ast.Inspect(result, func(candidate ast.Node) bool {
					selector, ok := candidate.(*ast.SelectorExpr)
					if ok && selector.Sel.Name == "Name" {
						if dependency, found := importedPath(selector, aliases); found {
							dependencies[dependency] = true
						}
					}
					return true
				})
			}
			return false
		default:
			return true
		}
	})
}

func inspectCall(call *ast.CallExpr, aliases map[string]string, owner, importPath, filename string, inspection *sourceInspection) {
	base, typeArguments := genericCall(call.Fun)
	selector, ok := base.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if imported, found := importedPath(selector, aliases); found && imported == "7link.com/platform/pkg/module" && selector.Sel.Name == "Resolve" {
		for _, argument := range typeArguments {
			if isStableWiringCapability(argument, aliases) {
				continue
			}
			dependency, found := importedTypePath(argument, aliases)
			if !found {
				continue
			}
			target, suffix, isIAM := iamImport(dependency)
			if !isIAM || target != owner && suffix == "" {
				inspection.resolvedDeps[dependency] = true
			}
		}
	}
	target, suffix, isIAM := importedSelector(selector, aliases)
	if isIAM && target != owner && suffix == "" && isModuleConstructor(selector.Sel.Name) && !isAppPackage(importPath) {
		inspection.violations = append(inspection.violations, architectureViolation{
			Rule: "cross-module-constructor", File: filename,
			Message: fmt.Sprintf("%s calls %s.%s outside internal/app", owner, target, selector.Sel.Name),
		})
	}
}

func TestStableWiringCapabilitiesRemainNarrow(t *testing.T) {
	aliases := map[string]string{"authnext": "7link.com/platform/pkg/extension/authn"}
	for _, testCase := range []struct {
		expression string
		want       bool
	}{
		{"authnext.BrowserSessionSecurity", true},
		{"authnext.ReauthenticationConsumer", true},
		{"authnext.Claims", false},
		{"authnext.UnknownProvider", false},
	} {
		expression, err := parser.ParseExpr(testCase.expression)
		require.NoError(t, err)
		assert.Equal(t, testCase.want, isStableWiringCapability(expression, aliases), testCase.expression)
	}
}

func isStableWiringCapability(expression ast.Expr, aliases map[string]string) bool {
	for {
		switch current := expression.(type) {
		case *ast.ParenExpr:
			expression = current.X
		case *ast.StarExpr:
			expression = current.X
		default:
			selector, ok := expression.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			importPath, ok := importedPath(selector, aliases)
			if !ok {
				return false
			}
			if importPath == "7link.com/platform/pkg/extension/authz" {
				return selector.Sel.Name == "Registrar" || selector.Sel.Name == "TokenVerifier"
			}
			// Provider-neutral capabilities are wired during service injection,
			// like TokenVerifier; an authn lifecycle edge would create a
			// core -> authn -> core initialization cycle.
			if importPath == "7link.com/platform/pkg/extension/authn" {
				return selector.Sel.Name == "BrowserSessionSecurity" || selector.Sel.Name == "ReauthenticationConsumer" ||
					selector.Sel.Name == "AccountViewAuthorizerConnector"
			}
			return importPath == iamImportPrefix+"/identity" && selector.Sel.Name == "RegistrationPrincipalCreator"
		}
	}
}

func inspectExportedSignature(declaration ast.Decl, aliases map[string]string, filename string, inspection *sourceInspection) {
	inspect := func(node ast.Node) {
		ast.Inspect(node, func(candidate ast.Node) bool {
			_, suffix, ok := importedSelectorNode(candidate, aliases)
			if ok && isStorageSuffix(suffix) {
				inspection.violations = append(inspection.violations, architectureViolation{
					Rule: "public-storage-type", File: filename,
					Message: "exported API leaks a model/query/dbgen type",
				})
			}
			return true
		})
	}
	switch current := declaration.(type) {
	case *ast.FuncDecl:
		if ast.IsExported(current.Name.Name) {
			inspect(current.Type)
		}
	case *ast.GenDecl:
		for _, spec := range current.Specs {
			switch typed := spec.(type) {
			case *ast.TypeSpec:
				if ast.IsExported(typed.Name.Name) {
					inspect(typed.Type)
				}
			case *ast.ValueSpec:
				exported := false
				for _, name := range typed.Names {
					exported = exported || ast.IsExported(name.Name)
				}
				if exported && typed.Type != nil {
					inspect(typed.Type)
				}
			}
		}
	}
}

func importAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string, len(file.Imports))
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		alias := path.Base(importPath)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		if alias != "_" && alias != "." {
			aliases[alias] = importPath
		}
	}
	return aliases
}

func importedSelector(selector *ast.SelectorExpr, aliases map[string]string) (string, string, bool) {
	importPath, ok := importedPath(selector, aliases)
	if !ok {
		return "", "", false
	}
	return iamImport(importPath)
}

func importedSelectorNode(node ast.Node, aliases map[string]string) (string, string, bool) {
	selector, ok := node.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	return importedSelector(selector, aliases)
}

func importedPath(selector *ast.SelectorExpr, aliases map[string]string) (string, bool) {
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	importPath, ok := aliases[identifier.Name]
	return importPath, ok
}

func importedTypePath(expression ast.Expr, aliases map[string]string) (string, bool) {
	switch current := expression.(type) {
	case *ast.SelectorExpr:
		return importedPath(current, aliases)
	case *ast.ParenExpr:
		return importedTypePath(current.X, aliases)
	case *ast.StarExpr:
		return importedTypePath(current.X, aliases)
	case *ast.IndexExpr:
		return importedTypePath(current.X, aliases)
	case *ast.IndexListExpr:
		return importedTypePath(current.X, aliases)
	default:
		return "", false
	}
}

func genericCall(expression ast.Expr) (ast.Expr, []ast.Expr) {
	switch current := expression.(type) {
	case *ast.IndexExpr:
		return current.X, []ast.Expr{current.Index}
	case *ast.IndexListExpr:
		return current.X, current.Indices
	default:
		return expression, nil
	}
}

func iamImport(importPath string) (string, string, bool) {
	prefix := iamImportPrefix + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return "", "", false
	}
	remainder := strings.TrimPrefix(importPath, prefix)
	moduleName, suffix, _ := strings.Cut(remainder, "/")
	if moduleName == "" {
		return "", "", false
	}
	return moduleName, suffix, true
}

func isStorageSuffix(suffix string) bool {
	first, _, _ := strings.Cut(suffix, "/")
	return first == "model" || first == "query" || first == "dbgen"
}

func isConcreteModuleType(name string) bool {
	return name == "Service" || name == "Module" || name == "Repository"
}

func isModuleConstructor(name string) bool {
	return name == "NewModule" || name == "NewDeclarationOnlyModule" || name == "NewService" || name == "NewRepository"
}

func isAppPackage(importPath string) bool {
	return importPath == appImportPrefix || strings.HasPrefix(importPath, appImportPrefix+"/")
}

func isCentralPackageName(name string) bool {
	switch strings.ToLower(name) {
	case "port", "ports", "schema", "schemas", "iamschema":
		return true
	default:
		return false
	}
}

func backendRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func goList(t *testing.T, root string, patterns ...string) []listedPackage {
	t.Helper()
	arguments := append([]string{"list", "-json"}, patterns...)
	command := exec.Command("go", arguments...)
	command.Dir = root
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	decoder := json.NewDecoder(bytes.NewReader(output))
	var packages []listedPackage
	for {
		var pkg listedPackage
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		packages = append(packages, pkg)
	}
	return packages
}

func hasFileWithExtension(directory, extension string) bool {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == extension {
			return true
		}
	}
	return false
}

func hasIAMBusinessSource(files []string) bool {
	for _, filename := range files {
		if filepath.Ext(filename) != ".go" || strings.HasSuffix(filename, "_test.go") {
			continue
		}
		switch filename {
		case "module.go", "migrate.go", "doc.go":
			continue
		default:
			return true
		}
	}
	return false
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	return err == nil && !info.IsDir()
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func mergeSet(target, source map[string]bool) {
	for value := range source {
		target[value] = true
	}
}

func violationRules(violations []architectureViolation) map[string]bool {
	rules := make(map[string]bool, len(violations))
	for _, violation := range violations {
		rules[violation.Rule] = true
	}
	return rules
}

func uniqueViolations(input []architectureViolation) []architectureViolation {
	seen := make(map[string]bool, len(input))
	result := make([]architectureViolation, 0, len(input))
	for _, violation := range input {
		key := violation.Rule + "\x00" + violation.File + "\x00" + violation.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, violation)
	}
	sort.Slice(result, func(i, j int) bool {
		left := result[i].Rule + result[i].File + result[i].Message
		right := result[j].Rule + result[j].File + result[j].Message
		return left < right
	})
	return result
}

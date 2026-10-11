package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// domainErrorMappers is every function that turns a handler's error into an
// HTTP response. Each one must return an error that already chose its status
// (an input check's 422) unchanged: seventeen of them once flattened it into
// 500. The set is closed so a new mapper cannot be added without landing here,
// where the rule is checked; each package's test also feeds huma.Error422
// through its own mappers (TestErrorMappersKeepChosenStatus).
var domainErrorMappers = []string{
	"internal/files.fileHTTPError",
	"internal/iam/audit.auditAPIError",
	"internal/iam/authn.accountViewHTTPError",
	"internal/iam/authn.authenticationError",
	"internal/iam/authn.personalTokenError",
	// Not a domain error mapper in the usual sense: it turns a captcha
	// verification outcome into the refusal a client can act on. It is listed
	// because it has the same shape and the same obligation -- a status chosen
	// below it must survive.
	"internal/iam/authn.refuseUnansweredCaptcha",
	"internal/iam/core.apiError",
	"internal/iam/core.platformAPIError",
	"internal/iam/federation.federationError",
	"internal/iam/federation.samlError",
	"internal/iam/governance.governanceError",
	"internal/iam/identity.closureError",
	"internal/iam/identity.identityError",
	"internal/iam/oauth.consentError",
	"internal/iam/oauth.deviceError",
	"internal/iam/oauth.oauthClientError",
	"internal/iam/oauth.oauthConsentError",
	"internal/iam/organization.departmentError",
	"internal/iam/organization.groupError",
	"internal/iam/organization.invitationError",
	"internal/iam/organization.tenantError",
	"internal/iam/provisioning.provisioningError",
	"internal/todo.httpError",
	"internal/iam/webhook.webhookError",
}

func TestDomainErrorMappersKeepChosenStatus(t *testing.T) {
	root := backendRoot(t)
	found := map[string]*ast.FuncDecl{}
	var discovered []string
	for _, pkg := range goList(t, root, "./internal/...", "./pkg/...") {
		relative := strings.TrimPrefix(pkg.ImportPath, modulePathPrefix)
		for _, name := range pkg.GoFiles {
			if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".gen.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkg.Dir, name), nil, parser.SkipObjectResolution)
			require.NoError(t, err)
			aliases := packageAliases(file)
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Recv != nil || function.Body == nil {
					continue
				}
				qualified := relative + "." + function.Name.Name
				found[qualified] = function
				if isDomainErrorMapper(function, aliases) {
					discovered = append(discovered, qualified)
				}
			}
		}
	}
	sort.Strings(discovered)
	listed := map[string]bool{}
	for _, mapper := range domainErrorMappers {
		listed[mapper] = true
	}
	for _, mapper := range discovered {
		assert.True(t, listed[mapper], "%s maps errors to huma responses; add it to domainErrorMappers and pass chosen statuses through", mapper)
	}
	for _, mapper := range domainErrorMappers {
		function := found[mapper]
		if !assert.NotNil(t, function, "%s is listed but no longer exists; delete the entry", mapper) {
			continue
		}
		assert.True(t, startsWithStatusPassthrough(function), "%s must begin with `if respx.IsStatusError(err) { return err }`", mapper)
	}
}

// isDomainErrorMapper recognizes func(..., err error, ...) error that returns a
// huma status error.
func isDomainErrorMapper(function *ast.FuncDecl, aliases map[string]string) bool {
	results := function.Type.Results
	if results == nil || len(results.List) != 1 || !isIdent(results.List[0].Type, "error") || errorParameter(function) == "" {
		return false
	}
	mapper := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		statement, ok := node.(*ast.ReturnStmt)
		if !ok || len(statement.Results) != 1 {
			return true
		}
		if call, ok := statement.Results[0].(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				if identifier, ok := selector.X.(*ast.Ident); ok && aliases[identifier.Name] == humaImportPath {
					mapper = true
				}
			}
		}
		return true
	})
	return mapper
}

func errorParameter(function *ast.FuncDecl) string {
	for _, field := range function.Type.Params.List {
		if isIdent(field.Type, "error") && len(field.Names) == 1 {
			return field.Names[0].Name
		}
	}
	return ""
}

func startsWithStatusPassthrough(function *ast.FuncDecl) bool {
	parameter := errorParameter(function)
	if parameter == "" || len(function.Body.List) == 0 {
		return false
	}
	statement, ok := function.Body.List[0].(*ast.IfStmt)
	if !ok || statement.Init != nil || statement.Else != nil || len(statement.Body.List) != 1 {
		return false
	}
	call, ok := statement.Cond.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || !isIdent(call.Args[0], parameter) {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "IsStatusError" || !isIdent(selector.X, "respx") {
		return false
	}
	returned, ok := statement.Body.List[0].(*ast.ReturnStmt)
	return ok && len(returned.Results) == 1 && isIdent(returned.Results[0], parameter)
}

func isIdent(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

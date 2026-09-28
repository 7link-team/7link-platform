package app

import (
	"testing"

	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// composedOpenAPIOperation is one operation of the composed application
// document, with the method and path it is published under.
type composedOpenAPIOperation struct {
	method    string
	path      string
	operation *huma.Operation
}

func (o composedOpenAPIOperation) String() string {
	return o.method + " " + o.path + " (" + o.operation.OperationID + ")"
}

// composedOpenAPI registers every module of the composed application and
// returns the document it publishes together with its operations.
func composedOpenAPI(t *testing.T) (*huma.OpenAPI, []composedOpenAPIOperation) {
	t.Helper()
	app := bootstrapComposedApp(t)
	api := humax.New(chi.NewRouter(), humax.Config{Name: app.config.Name, Version: app.config.Version})
	require.NoError(t, app.modules.RegisterHTTP(t.Context(), api))
	var operations []composedOpenAPIOperation
	for path, item := range api.OpenAPI().Paths {
		for method, operation := range openAPIPathOperations(item) {
			if operation != nil {
				operations = append(operations, composedOpenAPIOperation{method: method, path: path, operation: operation})
			}
		}
	}
	require.NotEmpty(t, operations)
	return api.OpenAPI(), operations
}

// TestComposedOpenAPIAddressesPrincipalsByPrincipalID keeps one name for one
// identifier: a principal in a path is principal_id on every route (D75/D81),
// never a synonym that makes parallel routes read as different resources.
func TestComposedOpenAPIAddressesPrincipalsByPrincipalID(t *testing.T) {
	_, operations := composedOpenAPI(t)
	for _, entry := range operations {
		for _, parameter := range entry.operation.Parameters {
			if parameter.In == "path" {
				assert.NotEqual(t, "subject", parameter.Name, "%s names a principal subject; use principal_id", entry)
			}
		}
	}
}

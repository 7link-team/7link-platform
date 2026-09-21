package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// newOpenAPIExportConfig reuses the platform-administrator configuration and
// turns migration on, because the export composes the same application the
// server does and that composition expects a schema to be there.
func newOpenAPIExportConfig(t *testing.T) OpenAPIExportConfig {
	t.Helper()
	admin := newPlatformAdminConfig(t, newPlatformAdminDSN(t))
	admin.AppConfig.Migrate = true
	return OpenAPIExportConfig{AppConfig: admin.AppConfig}
}

// TestOpenAPIExportDescribesTheRunningApplicationWithoutRunningIt is the whole
// point of the command: the document that was previously reachable only from a
// listening process, produced by one that never binds an address.
func TestOpenAPIExportDescribesTheRunningApplicationWithoutRunningIt(t *testing.T) {
	document, err := (&App{}).RunOpenAPIExport(t.Context(), newOpenAPIExportConfig(t))
	require.NoError(t, err)

	var specification struct {
		OpenAPI string                           `json:"openapi"`
		Info    struct{ Title, Version string }  `json:"info"`
		Paths   map[string]map[string]any        `json:"paths"`
		Comps   struct{ Schemas map[string]any } `json:"components"`
	}
	require.NoError(t, json.Unmarshal(document, &specification))
	assert.Equal(t, "3.1.0", specification.OpenAPI)
	assert.Equal(t, "1", specification.Info.Version, "the document carries the configured version")
	assert.NotEmpty(t, specification.Paths)
	assert.NotEmpty(t, specification.Comps.Schemas,
		"a document with paths and no schemas describes requests nobody can build")
	// A named operation rather than only a count: a document that lost every IAM
	// route would still have paths, and a count would not notice.
	assert.Contains(t, specification.Paths, "/tenants")

	assert.True(t, strings.HasSuffix(string(document), "}\n"),
		"a file people and tools read line by line ends with a newline")
}

func TestOpenAPIExportGroupsOperationsByPublicResource(t *testing.T) {
	document, err := (&App{}).RunOpenAPIExport(t.Context(), newOpenAPIExportConfig(t))
	require.NoError(t, err)

	var specification struct {
		Paths map[string]map[string]struct {
			OperationID string   `json:"operationId"`
			Tags        []string `json:"tags"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(document, &specification))
	for path, item := range specification.Paths {
		root := strings.Split(strings.TrimPrefix(path, "/"), "/")[0]
		expected := root
		switch root {
		case ".well-known":
			expected = "oauth"
		case "healthz", "livez", "readyz":
			expected = "health"
		}
		for method, operation := range item {
			if operation.OperationID == "" {
				continue
			}
			assert.Equal(t, []string{expected}, operation.Tags, method+" "+path)
		}
	}
}

// TestOpenAPIExportProducesTheSameBytesEveryTime is what makes the document
// usable as a checked-in file. If it varied between runs, every rebuild would
// report a difference, and a real API change would be indistinguishable from
// noise -- which is the failure that makes people stop reading the diff.
func TestOpenAPIExportProducesTheSameBytesEveryTime(t *testing.T) {
	config := newOpenAPIExportConfig(t)
	first, err := (&App{}).RunOpenAPIExport(t.Context(), config)
	require.NoError(t, err)
	second, err := (&App{}).RunOpenAPIExport(t.Context(), newOpenAPIExportConfig(t))
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))
}

// TestOpenAPIExportRefusesToGuessAFormat keeps a misspelled format from quietly
// producing the default. The mistake would otherwise be found by whatever
// consumed the file, a long way from the command that made it.
func TestOpenAPIExportRefusesToGuessAFormat(t *testing.T) {
	config := newOpenAPIExportConfig(t)
	config.Format = "toml"
	_, err := (&App{}).RunOpenAPIExport(t.Context(), config)
	require.Error(t, err)
	assert.ErrorContains(t, err, "toml")
	assert.ErrorContains(t, err, "json")

	for _, accepted := range []string{"", "json", "JSON", " yaml ", "yml"} {
		format, err := normalizeOpenAPIFormat(accepted)
		require.NoError(t, err, accepted)
		assert.Contains(t, []string{OpenAPIFormatJSON, OpenAPIFormatYAML}, format)
	}
}

// TestOpenAPIExportWritesYAMLWhenAskedTo covers the second serialization. Both
// formats have to describe the same API, or the choice of format would change
// what a generator believes the service accepts.
func TestOpenAPIExportWritesYAMLWhenAskedTo(t *testing.T) {
	config := newOpenAPIExportConfig(t)
	config.Format = OpenAPIFormatYAML
	document, err := (&App{}).RunOpenAPIExport(t.Context(), config)
	require.NoError(t, err)

	var fromYAML map[string]any
	require.NoError(t, yaml.Unmarshal(document, &fromYAML))
	assert.Equal(t, "3.1.0", fromYAML["openapi"])

	asJSON, err := (&App{}).RunOpenAPIExport(t.Context(), newOpenAPIExportConfig(t))
	require.NoError(t, err)
	var fromJSON map[string]any
	require.NoError(t, json.Unmarshal(asJSON, &fromJSON))
	yamlPaths, _ := fromYAML["paths"].(map[string]any)
	jsonPaths, _ := fromJSON["paths"].(map[string]any)
	require.NotEmpty(t, yamlPaths)
	assert.Len(t, yamlPaths, len(jsonPaths), "the format may not change which operations exist")
}

// TestOpenAPIExportKeepsTheVersionStableWhenNoneIsConfigured guards the field
// that would otherwise move on its own. The service stamps its build here, and
// a build stamp in a checked-in document means every commit produces a
// difference that says nothing about the API.
func TestOpenAPIExportKeepsTheVersionStableWhenNoneIsConfigured(t *testing.T) {
	config := newOpenAPIExportConfig(t)
	config.Version = ""
	document, err := (&App{}).RunOpenAPIExport(t.Context(), config)
	require.NoError(t, err)

	var specification struct {
		Info struct{ Version string } `json:"info"`
	}
	require.NoError(t, json.Unmarshal(document, &specification))
	assert.Equal(t, openAPIFallbackVersion, specification.Info.Version)
	assert.NotEqual(t, "dev", specification.Info.Version,
		"the build stamp belongs to the running process, not to a document in a repository")
}

package app

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// newOpenAPIExportConfig owns the minimal application fixture needed to compose
// and migrate the API without depending on an IAM maintenance command.
func newOpenAPIExportConfig(t *testing.T) OpenAPIExportConfig {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)
	config := validTestAppConfig()
	config.Version = "1"
	config.Migrate = true
	config.Database = map[string]Database{
		"primary": {Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "openapi.sqlite"), Writable: true, Readable: true},
	}
	config.Files.LocalRoot = filepath.Join(t.TempDir(), "files")
	config.Authn = IAMAuthn(authnext.Config{
		Enabled: true, Issuer: "https://iam.example.test", SigningKey: key,
		AccessTokenTTL: time.Hour,
		MFA:            authnext.MFAConfig{EncryptionKey: key},
	})
	return OpenAPIExportConfig{AppConfig: config}
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

// documentSections is the approved grouping of the published API: what a
// reader is trying to do, not which segment a URI happens to start with.
//
// The rule it replaced derived the tag from the first path segment. That tied
// the table of contents to the URI, so a section moved whenever a route did,
// and it produced thirty-three sections in which tenants held eighty-four
// operations while ten held two or fewer.
var documentSections = []string{
	"system", "authentication", "account", "directory", "access-control",
	"files", "applications", "audit", "platform", "oauth", "saml", "scim",
	"todos",
}

// TestOpenAPIExportGroupsOperationsByApprovedSection keeps the published
// grouping inside the agreed vocabulary in both directions: no operation
// invents a section, and no section in the list has gone empty. A one-way
// check would let the list keep naming sections the API stopped publishing.
func TestOpenAPIExportGroupsOperationsByApprovedSection(t *testing.T) {
	document, err := (&App{}).RunOpenAPIExport(t.Context(), newOpenAPIExportConfig(t))
	require.NoError(t, err)

	var specification struct {
		Paths map[string]map[string]struct {
			OperationID string   `json:"operationId"`
			Tags        []string `json:"tags"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(document, &specification))

	approved := make(map[string]int, len(documentSections))
	for _, section := range documentSections {
		approved[section] = 0
	}
	for path, item := range specification.Paths {
		for method, operation := range item {
			if operation.OperationID == "" {
				continue
			}
			location := method + " " + path
			if !assert.Len(t, operation.Tags, 1, location) {
				continue
			}
			section := operation.Tags[0]
			if _, ok := approved[section]; !ok {
				assert.Failf(t, "unapproved section",
					"%s is published under %q, which is not one of %s",
					location, section, strings.Join(documentSections, ", "))
				continue
			}
			approved[section]++
		}
	}
	// A deployment registers SAML only when it federates, and this export runs
	// on a minimal configuration. Its absence is a fact about the config, not a
	// vocabulary entry nothing uses, so it is named rather than assumed.
	featureGated := map[string]bool{"saml": true}
	for _, section := range documentSections {
		if featureGated[section] {
			continue
		}
		assert.NotZerof(t, approved[section],
			"section %q is approved but no operation is published under it", section)
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

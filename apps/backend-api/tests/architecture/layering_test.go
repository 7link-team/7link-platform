package architecture_test

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const storageSeparator = "/"

// layout names the module's two top-level trees. The module path is read from
// go list rather than spelled here, so the rules hold for any module name.
type layout struct {
	internal string
	shared   string
}

func layoutOf(modulePath string) layout {
	return layout{internal: modulePath + "/internal/", shared: modulePath + "/pkg/"}
}

// importEdge is one production import: tests may reach across any boundary to
// assert a contract, so only non-test sources are listed.
type importEdge struct {
	from string
	to   string
}

// TestSharedPackagesDoNotImportInternal keeps pkg/ reusable on its own. When
// pkg/extension/authz imported internal/infra/guid for its ID type, guid could
// no longer import authz to protect its own routes without a cycle; nothing
// stopped the edge from appearing because the module gate only scans
// internal/iam.
func TestSharedPackagesDoNotImportInternal(t *testing.T) {
	for _, violation := range moduleLayout(t).sharedPackageViolations(productionImports(t, "./pkg/...")) {
		t.Error(violation)
	}
}

// TestModulesDoNotImportAnotherModulesStorage applies the storage boundary to
// every module under internal/, not only internal/iam: a module reads another
// module's tables through that module's published capability, never through
// its model, query or dbgen packages.
func TestModulesDoNotImportAnotherModulesStorage(t *testing.T) {
	for _, violation := range moduleLayout(t).storageViolations(productionImports(t, "./internal/...")) {
		t.Error(violation)
	}
}

func TestLayeringRulesRejectViolatingEdges(t *testing.T) {
	fixture := layoutOf("example.test/app")
	internalPrefix, sharedPkgPrefix := fixture.internal, fixture.shared
	assert.Len(t, fixture.sharedPackageViolations([]importEdge{
		{from: sharedPkgPrefix + "extension/authz", to: internalPrefix + "infra/guid"},
		{from: sharedPkgPrefix + "extension/authz", to: sharedPkgPrefix + "guid"},
	}), 1)
	assert.Len(t, fixture.storageViolations([]importEdge{
		{from: internalPrefix + "todo", to: internalPrefix + "files/query"},
		{from: internalPrefix + "iam/identity", to: internalPrefix + "iam/core/model"},
		{from: internalPrefix + "infra/metrics", to: internalPrefix + "infra/dlock/dbgen"},
		{from: internalPrefix + "files", to: internalPrefix + "files/query"},
		{from: internalPrefix + "iam/core", to: internalPrefix + "iam/core/model"},
		{from: internalPrefix + "files", to: internalPrefix + "iam/core"},
	}), 3)
}

func (l layout) sharedPackageViolations(edges []importEdge) []string {
	var violations []string
	for _, edge := range edges {
		if strings.HasPrefix(edge.from, l.shared) && strings.HasPrefix(edge.to, l.internal) {
			violations = append(violations, edge.from+" imports "+edge.to+": pkg/ must not depend on internal/")
		}
	}
	return violations
}

func (l layout) storageViolations(edges []importEdge) []string {
	var violations []string
	for _, edge := range edges {
		owner, storage := l.moduleOf(edge.to)
		if !storage {
			continue
		}
		if from, _ := l.moduleOf(edge.from); from != "" && from != owner {
			violations = append(violations, edge.from+" imports "+edge.to+": use the owning module's published capability")
		}
	}
	return violations
}

// moduleOf names the module an internal package belongs to and reports
// whether the package is one of its storage packages. Modules sit one level
// below internal/, except under the iam/ and infra/ namespaces, which group
// modules one level deeper.
func (l layout) moduleOf(importPath string) (string, bool) {
	rest, ok := strings.CutPrefix(importPath, l.internal)
	if !ok {
		return "", false
	}
	segments := strings.Split(rest, storageSeparator)
	depth := 1
	if segments[0] == "iam" || segments[0] == "infra" {
		depth = 2
	}
	if len(segments) < depth {
		return "", false
	}
	owner := strings.Join(segments[:depth], storageSeparator)
	storage := len(segments) > depth && (segments[depth] == "model" || segments[depth] == "query" || segments[depth] == "dbgen")
	return owner, storage
}

func moduleLayout(t *testing.T) layout {
	t.Helper()
	command := exec.Command("go", "list", "-m")
	command.Dir = backendRoot(t)
	output, err := command.Output()
	require.NoError(t, err)
	modulePath := strings.TrimSpace(string(output))
	require.NotEmpty(t, modulePath)
	return layoutOf(modulePath)
}

func productionImports(t *testing.T, pattern string) []importEdge {
	t.Helper()
	command := exec.Command("go", "list", "-f", `{{$p := .ImportPath}}{{range .Imports}}{{$p}} {{.}}{{"\n"}}{{end}}`, pattern)
	command.Dir = backendRoot(t)
	output, err := command.Output()
	require.NoError(t, err)
	var edges []importEdge
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		from, to, ok := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
		if ok {
			edges = append(edges, importEdge{from: from, to: to})
		}
	}
	require.NoError(t, scanner.Err())
	require.NotEmpty(t, edges)
	return edges
}

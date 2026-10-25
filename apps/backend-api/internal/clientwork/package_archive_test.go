package clientwork

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPackageArchiveCanonicalRedactionRejectsInvalidFactsAndPreservesNumbers(t *testing.T) {
	for _, value := range []any{map[string]any{"nested": []any{1.5}}, map[string]any{"nested": map[string]any{"fraction": 1.5}}, make(chan int)} {
		raw, err := packageCanonical(value)
		require.Error(t, err)
		require.Nil(t, raw)
	}
	raw, err := packageCanonical([]any{nil, true, false, int64(9007199254740993)})
	require.NoError(t, err)
	require.JSONEq(t, `[null,true,false,9007199254740993]`, string(raw))
	clean, count, err := redactPackageText(t.Context(), []byte("opaque=0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz unchanged=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), nil, "Team")
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Contains(t, string(clean), "opaque=[REDACTED:HIGH_ENTROPY]")
	require.Contains(t, string(clean), "unchanged=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, value := range []string{`{"value":"private"}`, `["private"]`, `{`} {
		out, err := redactPackageJSON(ctx, []byte(value), nil, "Team")
		require.Error(t, err)
		require.Nil(t, out)
	}
	require.False(t, packageTextMIME("text/plain; broken"))
	require.Equal(t, "evidence", packageArchiveSafeName(".."))
	require.Len(t, packageArchiveSafeName(strings.Repeat("x", 120)), 100)
}

func TestPackageArchiveRejectsUnverifiedAssessmentsAndInvalidItemIdentities(t *testing.T) {
	data := []byte("actual source")
	base := packageArchiveInput{ItemID: strings.Repeat("a", 64), Name: "report.txt", MIME: "text/plain", SHA256: packageSHA(data), Raw: data}
	for _, fault := range []string{"short_id", "bad_id", "invalid_name", "wrong_scan", "unscanned"} {
		t.Run(fault, func(t *testing.T) {
			in := base
			switch fault {
			case "short_id":
				in.ItemID = "short"
			case "bad_id":
				in.ItemID = strings.Repeat("z", 64)
			case "invalid_name":
				in.Name = string([]byte{0xff})
			case "wrong_scan":
				in.Assessment = &execution.PackageEvidenceAssessment{SHA256: strings.Repeat("b", 64)}
			case "unscanned":
				in.Assessment = &execution.PackageEvidenceAssessment{SHA256: in.SHA256, RulesVersion: execution.PackageEvidenceScanVersion, Provenance: "execution:source_bytes"}
			}
			archive, manifest, err := buildPackageArchive(t.Context(), "f", "Release", "Notes", []packageArchiveInput{in}, nil, "Team")
			require.Error(t, err)
			require.Nil(t, archive)
			require.Nil(t, manifest)
		})
	}
}

func TestPackageArchiveDeterministicRedactionAndExclusion(t *testing.T) {
	raw := []byte("password=private\nAKIA0123456789ABCDEF\nAuthorization: Bearer credential\n/home/operator/private/report\n")
	inputs := []packageArchiveInput{
		{ItemID: strings.Repeat("a", 64), Name: "report.txt", MIME: "text/plain", SHA256: packageSHA(raw), Raw: raw, Overrides: json.RawMessage(`[]`)},
		{ItemID: strings.Repeat("b", 64), Name: ".env.production", MIME: "text/plain", SHA256: packageSHA([]byte("SECRET=x")), Raw: []byte("SECRET=x"), Overrides: json.RawMessage(`[]`)},
		{ItemID: strings.Repeat("c", 64), Name: "archive.zip", MIME: "application/zip", SHA256: packageSHA([]byte("unscanned")), Raw: []byte("unscanned"), Overrides: json.RawMessage(`[]`)},
		{ItemID: strings.Repeat("d", 64), Name: "execution.log", MIME: "text/plain", SHA256: packageSHA([]byte("raw observed log")), Raw: []byte("raw observed log")},
	}
	zipBytes, manifest, e := buildPackageArchive(t.Context(), strings.Repeat("d", 64), "Release", "Actual notes", inputs, nil, "Team")
	require.NoError(t, e)
	inputs[0], inputs[2] = inputs[2], inputs[0]
	again, manifestAgain, e := buildPackageArchive(t.Context(), strings.Repeat("d", 64), "Release", "Actual notes", inputs, nil, "Team")
	require.NoError(t, e)
	require.Equal(t, zipBytes, again)
	require.Equal(t, manifest, manifestAgain)
	var m packageArchiveManifest
	require.NoError(t, json.Unmarshal(manifest, &m))
	require.Equal(t, 4, m.Evidence[0].RedactionCount)
	require.Equal(t, "DENY_LISTED_FILE", m.Evidence[1].ExcludedReason)
	require.Equal(t, "UNSUPPORTED_TYPE", m.Evidence[2].ExcludedReason)
	require.Equal(t, "LOG_SOURCE_UNAVAILABLE", m.Evidence[3].ExcludedReason)
	zr, e := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	require.NoError(t, e)
	names := []string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		require.Equal(t, uint32(0644), uint32(f.Mode().Perm()))
		require.Equal(t, 1980, f.Modified.Year())
		r, e := f.Open()
		require.NoError(t, e)
		raw, e := io.ReadAll(r)
		require.NoError(t, e)
		require.NoError(t, r.Close())
		require.NotContains(t, string(raw), "private")
		require.NotContains(t, string(raw), "AKIA0123456789ABCDEF")
	}
	require.Equal(t, []string{"RELEASE_NOTES.md", "SHA256SUMS.txt", "evidence/" + strings.Repeat("a", 64), "manifest.json"}, names)
}

func TestPackageArchiveTypedManifestKeepsSourceHashesAndSafeEvidencePaths(t *testing.T) {
	raw := []byte("operator verified the result")
	hash := packageSHA(raw)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	scan, e := execution.ScanPackageEvidence(t.Context(), "operator-report.txt", "text/plain", hash, raw)
	require.NoError(t, e)
	facts := delivery.PackageInputFacts{Project: delivery.PackageProjectFacts{ID: 3, Name: "Project", Code: "PRJ"}, WorkItems: []delivery.PackageWorkFacts{{WorkItemID: 11, Key: "PRJ-42", Title: "operator acceptance", Type: "FEATURE", AcceptedAt: &at, AcceptedBy: "TEAM", Criteria: []delivery.PackageCriterionFacts{{ID: 12, Text: "Confirmed", EvidenceIDs: []guid.ID{30}}}, Evidence: []delivery.PackageEvidenceFacts{{ID: 30, Type: "FILE", Producer: "TEAM", ProducedAt: at, OriginalSHA256: hash, IncludeMode: "FILE", Verification: []delivery.PackageVerificationFacts{{Verifier: "HUMAN", Layer: "HUMAN", Result: "PASSED"}}}}}}}
	archive, manifest, e := buildPackageArchive(t.Context(), "fingerprint", "Release", "Notes", []packageArchiveInput{{ItemID: strings.Repeat("a", 64), WorkItemID: 11, EvidenceID: 30, WorkKey: "PRJ-42", Name: "operator-report.txt", MIME: "text/plain", SHA256: hash, Raw: raw, Assessment: &scan}}, []string{"operator", hash[:3]}, "Team", packageArchiveMetadata{ID: 70, RevisionAt: at, Facts: facts})
	require.NoError(t, e)
	var m packageArchiveManifest
	require.NoError(t, json.Unmarshal(manifest, &m))
	require.Equal(t, "Project", m.Delivery.Project.Name)
	require.Len(t, m.Items, 1)
	require.Equal(t, "Team acceptance", m.Items[0].Title)
	require.Equal(t, hash, m.Items[0].Evidence[0].OriginalSHA256)
	require.Equal(t, "evidence/PRJ-42/30-Team-report.txt", m.Items[0].Evidence[0].File.Path)
	require.Equal(t, "PASSED", m.Items[0].Evidence[0].Verification[0].Result)
	require.Equal(t, 1, m.Totals.Files)
	zr, e := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, e)
	for _, entry := range zr.File {
		require.NotContains(t, entry.Name, "operator")
	}
}
func TestPackageArchiveFailsClosedOnTamperInvalidTextAndCancellation(t *testing.T) {
	raw := []byte("actual bytes")
	input := packageArchiveInput{ItemID: strings.Repeat("a", 64), Name: "report.txt", MIME: "text/plain", SHA256: strings.Repeat("f", 64), Raw: raw}
	zipBytes, manifest, e := buildPackageArchive(t.Context(), "f", "Title", "Notes", []packageArchiveInput{input}, nil, "Team")
	require.ErrorIs(t, e, ErrConflict)
	require.Nil(t, zipBytes)
	require.Nil(t, manifest)
	input.Raw = []byte{0xff}
	input.SHA256 = packageSHA(input.Raw)
	_, _, e = buildPackageArchive(t.Context(), "f", "Title", "Notes", []packageArchiveInput{input}, nil, "Team")
	require.Error(t, e)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, e = buildPackageArchive(ctx, "f", "Title", "Notes", []packageArchiveInput{input}, nil, "Team")
	require.ErrorIs(t, e, context.Canceled)
}
func TestPackageCanonicalUsesJCSStringsAndKeyOrdering(t *testing.T) {
	raw, e := packageCanonical(map[string]any{"z": "<>&\u2028", "a": 1})
	require.NoError(t, e)
	require.Equal(t, "{\"a\":1,\"z\":\"<>&\u2028\"}", string(raw)) //nolint:testifylint // Serialized bytes are frozen by this regression contract.
	_, e = packageCanonical(map[string]any{"number": 1.5})
	require.Error(t, e)
}

func TestPackageArchiveRejectsDuplicateExcludedInputsAndPreservesJSONKeys(t *testing.T) {
	raw := []byte("secret")
	in := packageArchiveInput{ItemID: strings.Repeat("a", 64), Name: ".env", MIME: "text/plain", SHA256: packageSHA(raw), Raw: raw}
	_, _, e := buildPackageArchive(t.Context(), "f", "Title", "Notes", []packageArchiveInput{in, in}, nil, "Team")
	require.ErrorIs(t, e, ErrInvalid)
	_, _, e = buildPackageArchive(t.Context(), "f", "Title", "Notes", make([]packageArchiveInput, 501), nil, "Team")
	require.ErrorIs(t, e, ErrInvalid)
	clean, e := redactPackageJSON(t.Context(), []byte(`{"secret":"password=private-value","support":"Alice Smith"}`), []string{"Alice", "Alice Smith"}, "Team")
	require.NoError(t, e)
	var value map[string]string
	require.NoError(t, json.Unmarshal(clean, &value))
	require.Equal(t, "[REDACTED:PASSWORD]", value["secret"])
	require.Equal(t, "Team", value["support"])
}

func TestPackageArchiveRedactsShortIdentityValuesWithoutCascading(t *testing.T) {
	identities := []string{"AI", "CI", "é", "李", "T", ""}
	clean, count, err := redactPackageText(t.Context(), []byte("AI CI é 李 T"), identities, "Team")
	require.NoError(t, err)
	require.Equal(t, "Team Team Team Team Team", string(clean))
	require.Equal(t, 5, count)
	clean, err = redactPackageJSON(t.Context(), []byte(`{"AI":"CI é 李 T","sha256":"AI","receipt_hash":"CI","fingerprint":"é","path":"李"}`), identities, "Team")
	require.NoError(t, err)
	var value map[string]string
	require.NoError(t, json.Unmarshal(clean, &value))
	require.Equal(t, "Team Team Team Team", value["AI"])
	require.Equal(t, "AI", value["sha256"])
	require.Equal(t, "CI", value["receipt_hash"])
	require.Equal(t, "é", value["fingerprint"])
	require.Equal(t, "李", value["path"])
}

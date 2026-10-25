package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"gorm.io/gorm"
	"io"
	"math"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"7link.com/platform/pkg/module"
)

const PackageEvidenceScanVersion = "source-secrets-r1"

// PackageEvidenceScanner assesses the exact immutable source bytes supplied by
// the authorized evidence reader. It grants no file or execution authority.
type PackageEvidenceScanner struct {
	Scan            func(context.Context, string, string, string, []byte) (PackageEvidenceAssessment, error)
	IdentityTermsOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]string, error)
}
type PackageEvidenceAssessment struct {
	SHA256        string `json:"sha256"`
	RulesVersion  string `json:"rules_version"`
	Provenance    string `json:"provenance"`
	SecretFlagged bool   `json:"secret_flagged"`
	SecretScanned bool   `json:"secret_scanned"`
}

var packageSourceSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{36,}|sk-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`eyJ[\w-]+\.[\w-]+\.[\w-]+`),
	regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key)\s*[:=]\s*\S+|Authorization:\s*(Bearer|Basic)\s+\S+`),
	regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`),
}
var packageSourceEntropy = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_.-]*=([^\s]{32,})`)

func (m *Module) providePackageEvidenceScanner(s *module.Services) error {
	return module.Provide[PackageEvidenceScanner](s, PackageEvidenceScanner{Scan: ScanPackageEvidence, IdentityTermsOn: m.packageEvidenceIdentityTermsOn})
}

func (m *Module) packageEvidenceIdentityTermsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ids []guid.ID) ([]string, error) {
	if tx == nil || m.machines.ByIDOn == nil {
		return nil, ErrUnavailable
	}
	out := []string{}
	q := query.BindExecutions(tx, m.names)
	for _, id := range ids {
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
		if err != nil {
			return nil, err
		}
		machine, err := m.machines.ByIDOn(ctx, tx, tenant, row.MachineID)
		if err != nil {
			return nil, err
		}
		if machine.Name != "" {
			out = append(out, machine.Name)
		}
		if machine.PrincipalID >= 1_000_000_000_000_000 {
			out = append(out, machine.PrincipalID.String())
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// ScanPackageEvidence never infers a runner scan or a passed verification. Its
// provenance describes this local deterministic inspection, tied to source SHA.
func ScanPackageEvidence(ctx context.Context, name, mime, expected string, raw []byte) (PackageEvidenceAssessment, error) {
	sum := sha256.Sum256(raw)
	out := PackageEvidenceAssessment{SHA256: hex.EncodeToString(sum[:]), RulesVersion: PackageEvidenceScanVersion, Provenance: "execution:source_bytes"}
	if out.SHA256 != expected || len(raw) > 100<<20 {
		return out, ErrInvalidArgument
	}
	scan := func(name string, raw []byte) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		base := strings.ToLower(path.Base(strings.ReplaceAll(name, "\\", "/")))
		for _, prefix := range []string{".env", "id_rsa", "id_ed25519", "credentials"} {
			if strings.HasPrefix(base, prefix) {
				return true, nil
			}
		}
		for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".kdbx", ".tfstate"} {
			if strings.HasSuffix(base, suffix) {
				return true, nil
			}
		}
		if base == ".npmrc" || base == ".pypirc" || base == "kubeconfig" {
			return true, nil
		}
		for _, pattern := range packageSourceSecretPatterns {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if pattern.Match(raw) {
				return true, nil
			}
		}
		for _, match := range packageSourceEntropy.FindAllSubmatch(raw, -1) {
			counts := map[byte]int{}
			for _, b := range match[1] {
				counts[b]++
			}
			entropy := float64(0)
			for _, count := range counts {
				p := float64(count) / float64(len(match[1]))
				entropy -= p * math.Log2(p)
			}
			if entropy >= 4.5 {
				return true, nil
			}
		}
		return false, ctx.Err()
	}
	flagged, err := scan(name, raw)
	if err != nil {
		return out, err
	}
	out.SecretFlagged = flagged
	if strings.EqualFold(strings.TrimSpace(strings.Split(mime, ";")[0]), "application/zip") {
		archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			return out, ErrInvalidArgument
		}
		if len(archive.File) > 500 {
			return out, ErrInvalidArgument
		}
		total := int64(0)
		for _, entry := range archive.File {
			if entry.FileInfo().IsDir() {
				continue
			}
			if entry.Mode()&os.ModeType != 0 || strings.HasSuffix(strings.ToLower(entry.Name), ".zip") {
				return out, nil
			}
			reader, err := entry.Open()
			if err != nil {
				return out, err
			}
			remaining := (100 << 20) - total
			content, readErr := io.ReadAll(io.LimitReader(reader, remaining+1))
			closeErr := reader.Close()
			if readErr != nil {
				return out, readErr
			}
			if closeErr != nil {
				return out, closeErr
			}
			total += int64(len(content))
			if total > 100<<20 {
				return out, ErrInvalidArgument
			}
			if bytes.HasPrefix(content, []byte{'P', 'K', 3, 4}) {
				return out, nil
			}
			flagged, err := scan(entry.Name, content)
			if err != nil {
				return out, err
			}
			out.SecretFlagged = out.SecretFlagged || flagged
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.SecretScanned = true
	return out, nil
}

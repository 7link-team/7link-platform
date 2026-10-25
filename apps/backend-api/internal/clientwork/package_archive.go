package clientwork

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const packageRulesVersion = "r1"
const packageBundleLimit = 512 << 20
const packageLogLimit = 5 << 20

func packageSHA(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

// All input numbers are integral domain IDs/counts; rejecting other numbers
// prevents a non-JCS numeric representation from entering a signed manifest.
func packageCanonical(v any) ([]byte, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var x any
	if e = d.Decode(&x); e != nil {
		return nil, e
	}
	var b strings.Builder
	var write func(any) error
	write = func(v any) error {
		switch t := v.(type) {
		case nil:
			b.WriteString("null")
		case bool:
			b.WriteString(strconv.FormatBool(t))
		case string:
			b.WriteString(receiptJSONString(t))
		case json.Number:
			if _, e := strconv.ParseInt(string(t), 10, 64); e != nil {
				return ErrInvalid
			}
			b.WriteString(string(t))
		case []any:
			b.WriteByte('[')
			for i, v := range t {
				if i > 0 {
					b.WriteByte(',')
				}
				if e := write(v); e != nil {
					return e
				}
			}
			b.WriteByte(']')
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			slices.SortFunc(keys, func(a, b string) int { return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b))) })
			b.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(receiptJSONString(k))
				b.WriteByte(':')
				if e := write(t[k]); e != nil {
					return e
				}
			}
			b.WriteByte('}')
		default:
			return ErrInvalid
		}
		return nil
	}
	if e = write(x); e != nil {
		return nil, e
	}
	return []byte(b.String()), nil
}

type packageRedactionRule struct {
	id string
	re *regexp.Regexp
}

var packageRedactionRules = []packageRedactionRule{
	{"PRIVATE_KEY", regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)},
	{"AWS", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"GITHUB", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)},
	{"API_SECRET", regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)},
	{"SLACK", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"JWT", regexp.MustCompile(`eyJ[\w-]+\.[\w-]+\.[\w-]+`)},
	{"AUTHORIZATION", regexp.MustCompile(`(?i)Authorization:\s*(Bearer|Basic)\s+\S+`)},
	{"URL_CREDENTIAL", regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`)},
	{"PASSWORD", regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key)\s*[:=]\s*\S+`)},
	{"LOCAL_PATH", regexp.MustCompile(`(?:/Users/|/home/|[A-Za-z]:[\\/]Users[\\/])[^\s"'<>]+`)},
	{"INTERNAL_IDENTITY", regexp.MustCompile(`(?i)(?:principal[_ -]?id|agent[_ -]?name|machine[_ -]?name|host(?:name)?)\s*[:=]\s*[^\s,;]+`)},
}
var packageHighEntropy = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.-]*=)([^\s]{32,})`)

func redactPackageText(ctx context.Context, raw []byte, identities []string, label string) ([]byte, int, error) {
	if e := ctx.Err(); e != nil {
		return nil, 0, e
	}
	if !utf8.Valid(raw) {
		return nil, 0, ErrInvalid
	}
	value := string(raw)
	count := 0
	for _, rule := range packageRedactionRules {
		value = rule.re.ReplaceAllStringFunc(value, func(string) string { count++; return "[REDACTED:" + rule.id + "]" })
		if e := ctx.Err(); e != nil {
			return nil, 0, e
		}
	}
	value = packageHighEntropy.ReplaceAllStringFunc(value, func(s string) string {
		key, v, _ := strings.Cut(s, "=")
		freq := map[rune]int{}
		for _, r := range v {
			freq[r]++
		}
		n := float64(utf8.RuneCountInString(v))
		entropy := 0.0
		for _, c := range freq {
			p := float64(c) / n
			entropy -= p * math.Log2(p)
		}
		if entropy >= 4.5 {
			count++
			return key + "=[REDACTED:HIGH_ENTROPY]"
		}
		return s
	})
	// Replace complete names before their shorter aliases.
	identities = slices.Clone(identities)
	slices.SortFunc(identities, func(a, b string) int { return len(b) - len(a) })
	patterns := make([]string, 0, len(identities))
	for _, identity := range identities {
		if identity != "" {
			patterns = append(patterns, regexp.QuoteMeta(identity))
		}
	}
	if len(patterns) > 0 {
		// One pass prevents short aliases from rewriting an inserted role label.
		value = regexp.MustCompile(strings.Join(patterns, "|")).ReplaceAllStringFunc(value, func(string) string { count++; return label })
	}
	return []byte(value), count, ctx.Err()
}
func packageDeniedFile(name string) bool {
	n := strings.ToLower(path.Base(strings.ReplaceAll(name, "\\", "/")))
	for _, prefix := range []string{".env", "id_rsa", "id_ed25519", "credentials"} {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".kdbx", ".tfstate"} {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return slices.Contains([]string{".npmrc", ".pypirc", "kubeconfig"}, n)
}
func packageTextMIME(value string) bool {
	v, _, e := mime.ParseMediaType(value)
	if e != nil {
		return false
	}
	return strings.HasPrefix(v, "text/") || slices.Contains([]string{"application/json", "application/xml", "application/yaml", "application/x-yaml", "application/javascript"}, v) || strings.HasSuffix(v, "+json") || strings.HasSuffix(v, "+xml")
}
func packageAllowedMIME(value string) bool {
	if packageTextMIME(value) {
		return true
	}
	v, _, e := mime.ParseMediaType(value)
	return e == nil && slices.Contains([]string{"image/png", "image/jpeg", "image/webp", "image/gif", "application/pdf"}, v)
}

type packageArchiveEvidence struct {
	ID               guid.ID                              `json:"id,omitempty"`
	Type             string                               `json:"type,omitempty"`
	Producer         string                               `json:"producer,omitempty"`
	ProducedAt       *time.Time                           `json:"produced_at,omitempty"`
	Verification     []delivery.PackageVerificationFacts  `json:"verification,omitempty"`
	Limitations      string                               `json:"limitations,omitempty"`
	IncludeMode      string                               `json:"include_mode"`
	File             *packageArchiveFile                  `json:"file,omitempty"`
	ItemID           string                               `json:"item_id"`
	Name             string                               `json:"name"`
	OriginalSHA256   string                               `json:"original_sha256"`
	Path             string                               `json:"path,omitempty"`
	SHA256           string                               `json:"bundled_sha256,omitempty"`
	Size             int                                  `json:"size_bytes"`
	RedactionCount   int                                  `json:"redaction_count"`
	Truncated        bool                                 `json:"truncated"`
	ExcludedReason   string                               `json:"excluded_reason,omitempty"`
	Overrides        json.RawMessage                      `json:"verification_overrides"`
	SecretAssessment *execution.PackageEvidenceAssessment `json:"secret_assessment,omitempty"`
}
type packageArchiveManifest struct {
	SchemaVersion      string                     `json:"schema_version"`
	RulesVersion       string                     `json:"rules_version"`
	Fingerprint        string                     `json:"fingerprint"`
	Title              string                     `json:"title"`
	ReleaseNotesSHA256 string                     `json:"release_notes_sha256"`
	Evidence           []packageArchiveEvidence   `json:"evidence"`
	Delivery           *packageArchiveDelivery    `json:"delivery,omitempty"`
	GeneratedAt        *time.Time                 `json:"generated_at,omitempty"`
	Generator          packageArchiveGenerator    `json:"generator"`
	Items              []packageArchiveWork       `json:"items"`
	ReleaseNotes       packageArchiveReleaseNotes `json:"release_notes"`
	Totals             packageArchiveTotals       `json:"totals"`
}
type packageArchiveFile struct {
	Path           string `json:"path"`
	SHA256         string `json:"sha256"`
	Size           int    `json:"size"`
	Redacted       bool   `json:"redacted"`
	RedactionCount int    `json:"redaction_count"`
	Truncated      bool   `json:"truncated"`
}
type packageArchiveDelivery struct {
	ID      guid.ID                      `json:"id"`
	Number  string                       `json:"number"`
	Title   string                       `json:"title"`
	Project delivery.PackageProjectFacts `json:"project"`
}
type packageArchiveGenerator struct {
	Name         string `json:"name"`
	RulesVersion string `json:"rules_version"`
}
type packageArchiveReleaseNotes struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type packageArchiveTotals struct {
	Items    int `json:"items"`
	Evidence int `json:"evidence"`
	Files    int `json:"files"`
	Bytes    int `json:"bytes"`
}
type packageArchiveWork struct {
	delivery.PackageWorkFacts
	Evidence []packageArchiveEvidence `json:"evidence"`
}
type packageArchiveMetadata struct {
	ID         guid.ID
	RevisionAt time.Time
	Facts      delivery.PackageInputFacts
}
type packageArchiveInput struct {
	ItemID, Name, MIME, SHA256 string
	Raw                        []byte
	Overrides                  json.RawMessage
	Assessment                 *execution.PackageEvidenceAssessment
	WorkItemID                 guid.ID
	EvidenceID                 guid.ID
	WorkKey                    string
}

// Archive assembly receives verified bytes only. Never include arbitrary domain
// JSON, source paths, messages, prompts or internal acceptance reasons.
func buildPackageArchive(ctx context.Context, fingerprint, title, notes string, inputs []packageArchiveInput, identities []string, label string, metadata ...packageArchiveMetadata) ([]byte, []byte, error) {
	if len(inputs) == 0 || len(inputs) > 500 {
		return nil, nil, ErrInvalid
	}
	entries := map[string][]byte{}
	manifest := packageArchiveManifest{SchemaVersion: "1", RulesVersion: packageRulesVersion, Fingerprint: fingerprint, Evidence: []packageArchiveEvidence{}}
	var e error
	var cleaned []byte
	cleaned, _, e = redactPackageText(ctx, []byte(title), identities, label)
	if e != nil {
		return nil, nil, e
	}
	manifest.Title = string(cleaned)
	manifest.Generator = packageArchiveGenerator{Name: "delivery-bundle", RulesVersion: packageRulesVersion}
	manifest.Items = []packageArchiveWork{}
	if len(metadata) > 0 {
		meta := metadata[0]
		manifest.Delivery = &packageArchiveDelivery{ID: meta.ID, Number: meta.ID.String(), Title: manifest.Title, Project: meta.Facts.Project}
		manifest.GeneratedAt = &meta.RevisionAt
		for _, work := range meta.Facts.WorkItems {
			manifest.Items = append(manifest.Items, packageArchiveWork{PackageWorkFacts: work, Evidence: []packageArchiveEvidence{}})
		}
	}
	cleaned, _, e = redactPackageText(ctx, []byte(notes), identities, label)
	if e != nil {
		return nil, nil, e
	}
	entries["RELEASE_NOTES.md"] = cleaned
	manifest.ReleaseNotesSHA256 = packageSHA(cleaned)
	manifest.ReleaseNotes = packageArchiveReleaseNotes{Path: "RELEASE_NOTES.md", SHA256: manifest.ReleaseNotesSHA256}
	inputs = slices.Clone(inputs)
	slices.SortFunc(inputs, func(a, b packageArchiveInput) int { return strings.Compare(a.ItemID, b.ItemID) })
	seen := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		if len(in.Raw) > 100<<20 {
			return nil, nil, ErrBundleTooLarge
		}
		if packageSHA(in.Raw) != in.SHA256 {
			return nil, nil, ErrConflict
		}
		if len(in.ItemID) != 64 {
			return nil, nil, ErrInvalid
		}
		if _, e := hex.DecodeString(in.ItemID); e != nil {
			return nil, nil, ErrInvalid
		}
		if seen[in.ItemID] {
			return nil, nil, ErrInvalid
		}
		seen[in.ItemID] = true
		item := packageArchiveEvidence{ID: in.EvidenceID, IncludeMode: "FILE", ItemID: in.ItemID, Name: path.Base(strings.ReplaceAll(in.Name, "\\", "/")), OriginalSHA256: in.SHA256, Overrides: in.Overrides, SecretAssessment: in.Assessment}
		safeName, _, nameErr := redactPackageText(ctx, []byte(item.Name), identities, label)
		if nameErr != nil {
			return nil, nil, nameErr
		}
		item.Name = string(safeName)
		for _, work := range manifest.Items {
			if work.WorkItemID != in.WorkItemID {
				continue
			}
			for _, evidence := range work.PackageWorkFacts.Evidence {
				if evidence.ID == in.EvidenceID {
					item.Type = evidence.Type
					item.Producer = evidence.Producer
					at := evidence.ProducedAt
					item.ProducedAt = &at
					item.Verification = evidence.Verification
					item.Limitations = evidence.Limitations
				}
			}
		}
		if in.Assessment != nil && (in.Assessment.SHA256 != in.SHA256 || in.Assessment.RulesVersion != execution.PackageEvidenceScanVersion || in.Assessment.Provenance != "execution:source_bytes") {
			return nil, nil, ErrBundleScan
		}
		if in.Assessment != nil && !in.Assessment.SecretScanned && !strings.EqualFold(strings.TrimSpace(strings.Split(in.MIME, ";")[0]), "application/zip") {
			return nil, nil, ErrBundleScan
		}
		if packageDeniedFile(in.Name) {
			item.ExcludedReason = "DENY_LISTED_FILE"
		} else if packageRawLog(in.Name, in.MIME) {
			// The existing result contract exposes FILE pins, never an observed
			// Execution LOG summary or an explicit FULL_LOG selection. Do not
			// disguise the original log as a selected, summarized file.
			item.ExcludedReason = "LOG_SOURCE_UNAVAILABLE"
		} else if in.Assessment != nil && in.Assessment.SecretFlagged {
			item.ExcludedReason = "SECRET_FLAGGED"
		} else if !packageAllowedMIME(in.MIME) && !(strings.EqualFold(strings.TrimSpace(strings.Split(in.MIME, ";")[0]), "application/zip") && in.Assessment != nil && in.Assessment.SecretScanned) {
			item.ExcludedReason = "UNSUPPORTED_TYPE"
		} else {
			raw := in.Raw
			if packageTextMIME(in.MIME) {
				scan, cancel := context.WithTimeout(ctx, 30*time.Second)
				raw, item.RedactionCount, e = redactPackageText(scan, raw, identities, label)
				cancel()
				if e != nil {
					return nil, nil, e
				}
				if len(raw) > packageLogLimit {
					raw = raw[:packageLogLimit]
					for !utf8.Valid(raw) {
						raw = raw[:len(raw)-1]
					}
					item.Truncated = true
				}
			}
			item.Path = "evidence/" + in.ItemID
			if in.WorkKey != "" && in.EvidenceID > 0 {
				key, _, err := redactPackageText(ctx, []byte(in.WorkKey), identities, label)
				if err != nil {
					return nil, nil, err
				}
				item.Path = "evidence/" + packageArchiveSafeName(string(key)) + "/" + in.EvidenceID.String() + "-" + packageArchiveSafeName(item.Name)
			}
			if _, exists := entries[item.Path]; exists {
				return nil, nil, ErrInvalid
			}
			item.SHA256 = packageSHA(raw)
			item.Size = len(raw)
			entries[item.Path] = raw
			item.File = &packageArchiveFile{Path: item.Path, SHA256: item.SHA256, Size: item.Size, Redacted: item.RedactionCount > 0, RedactionCount: item.RedactionCount, Truncated: item.Truncated}
			manifest.Totals.Files++
			manifest.Totals.Bytes += len(raw)
		}
		safe, _, e := redactPackageText(ctx, []byte(item.Name), identities, label)
		if e != nil {
			return nil, nil, e
		}
		item.Name = string(safe)
		// Overrides are a typed public projection. Redact textual content again as
		// reasons/supporting file names can themselves contain credentials or paths.
		if len(item.Overrides) > 0 {
			safe, err := redactPackageJSON(ctx, item.Overrides, identities, label)
			if err != nil {
				return nil, nil, err
			}
			item.Overrides = safe
		}

		manifest.Evidence = append(manifest.Evidence, item)
		for i := range manifest.Items {
			if manifest.Items[i].WorkItemID == in.WorkItemID {
				manifest.Items[i].Evidence = append(manifest.Items[i].Evidence, item)
			}
		}
	}
	manifest.Totals.Items = len(manifest.Items)
	manifest.Totals.Evidence = len(manifest.Evidence)
	raw, e := packageCanonical(manifest)
	if e != nil {
		return nil, nil, e
	}
	raw, e = redactPackageJSON(ctx, raw, identities, label)
	if e != nil {
		return nil, nil, e
	}
	entries["manifest.json"] = raw
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	var sums strings.Builder
	total := 0
	for _, name := range names {
		total += len(entries[name])
		if total > packageBundleLimit {
			return nil, nil, ErrBundleTooLarge
		}
		fmt.Fprintf(&sums, "%s  %s\n", packageSHA(entries[name]), name)
	}
	entries["SHA256SUMS.txt"] = []byte(sums.String())
	names = append(names, "SHA256SUMS.txt")
	slices.Sort(names)
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	archive.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, 6) })
	for _, name := range names {
		if e := ctx.Err(); e != nil {
			return nil, nil, e
		}
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
		h.SetMode(0644)
		w, e := archive.CreateHeader(h)
		if e != nil {
			return nil, nil, e
		}
		if _, e = w.Write(entries[name]); e != nil {
			return nil, nil, e
		}
	}
	if e = archive.Close(); e != nil {
		return nil, nil, e
	}
	if out.Len() > packageBundleLimit {
		return nil, nil, ErrBundleTooLarge
	}
	return out.Bytes(), raw, nil
}

func packageRawLog(name, mimeType string) bool {
	name = strings.ToLower(path.Base(strings.ReplaceAll(name, "\\", "/")))
	media, _, _ := mime.ParseMediaType(mimeType)
	return strings.HasSuffix(name, ".log") || strings.Contains(name, ".log.") || slices.Contains([]string{"text/log", "text/x-log", "application/x-log"}, media)
}

var packageUnsafeFileName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func packageArchiveSafeName(name string) string {
	name = packageUnsafeFileName.ReplaceAllString(name, "_")
	if len(name) > 100 {
		name = name[:100]
	}
	if name == "" || name == "." || name == ".." {
		return "evidence"
	}
	return name
}

func redactPackageJSON(ctx context.Context, raw []byte, identities []string, label string) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if e := d.Decode(&value); e != nil {
		return nil, e
	}
	var clean func(any) (any, error)
	clean = func(v any) (any, error) {
		switch t := v.(type) {
		case string:
			raw, _, e := redactPackageText(ctx, []byte(t), identities, label)
			return string(raw), e
		case []any:
			for i, v := range t {
				next, e := clean(v)
				if e != nil {
					return nil, e
				}
				t[i] = next
			}
		case map[string]any:
			for k, v := range t {
				// Integrity facts and generated ZIP paths must retain the exact
				// bytes they describe. Domain projections exclude principal IDs.
				if k == "sha256" || strings.HasSuffix(k, "_sha256") || k == "fingerprint" || k == "receipt_hash" || k == "path" || k == "provenance" || k == "rules_version" {
					continue
				}
				next, e := clean(v)
				if e != nil {
					return nil, e
				}
				t[k] = next
			}
		}
		return v, nil
	}
	out, e := clean(value)
	if e != nil {
		return nil, e
	}
	return packageCanonical(out)
}

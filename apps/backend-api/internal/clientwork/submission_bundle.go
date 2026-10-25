package clientwork

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/keyringx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// Signing keys are deployment supplied base64-encoded 32-byte keys. An empty
// active key disables URL issuance; no application or process key substitutes.
type SubmissionBundleConfig struct {
	SigningKey          string   `mapstructure:"signing_key" secret:"true"`
	PreviousSigningKeys []string `mapstructure:"previous_signing_keys" secret:"true"`
}

// Only this whitelist enters the source manifest, including inside the ZIP.
type SubmissionBundleFile struct {
	Overrides json.RawMessage `json:"verification_overrides"`
	ItemID    string          `json:"item_id"`
	Name      string          `json:"name"`
	VersionID guid.ID         `json:"version_id"`
	SHA256    string          `json:"sha256"`
}
type submissionBundleClaims struct {
	Tenant, Principal, Submission, Project, Package guid.ID
	PackageHash, ManifestHash                       string
	Issued, Expires                                 time.Time
}
type submissionBundleGrantKey struct{}
type submissionBundleGrant struct {
	claims  submissionBundleClaims
	binding guid.ID
}
type SubmissionBundleInput struct {
	ID         string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	DeliveryID string `path:"delivery_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Token      string `query:"token" required:"true" maxLength:"4096"`
}

func submissionBundleManifest(items []PackageItem) []SubmissionBundleFile {
	out := make([]SubmissionBundleFile, 0, len(items))
	for _, item := range items {
		out = append(out, SubmissionBundleFile{ItemID: item.ItemID, Name: item.Artifact.Name, VersionID: item.Artifact.VersionID, SHA256: item.Artifact.SHA256, Overrides: item.Artifact.Overrides})
	}
	return out
}
func bundleDigest(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func (m *Module) submissionBundleKeys() (*keyringx.Keyring, error) {
	ring, err := keyringx.New("clientwork:submission-bundle:", m.config.SubmissionBundles.SigningKey, m.config.SubmissionBundles.PreviousSigningKeys...)
	if err != nil {
		return nil, ErrUnavailable
	}
	return ring, nil
}
func (m *Module) signSubmissionBundle(ctx context.Context, source guid.ID, row *model.DeliveryPackage, view *SubmissionDelivery, at time.Time) error {
	ring, err := m.submissionBundleKeys()
	if err != nil {
		return err
	}
	claims := submissionBundleClaims{Tenant: authn.TenantIDFromContext(ctx), Principal: authn.PrincipalIDFromContext(ctx), Submission: source, Project: row.ProjectID, Package: row.ID, PackageHash: row.ContentHash, ManifestHash: bundleDigest(view.Manifest), Issued: at.UTC(), Expires: at.UTC().Add(72 * time.Hour)}
	raw, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw) + "." + ring.MAC("download", raw)
	view.DownloadURL = fmt.Sprintf("/tenants/%s%s/%s/deliveries/%s/bundle?token=%s", claims.Tenant, submissionsPath, source, row.ID, url.QueryEscape(token))
	view.DownloadURLExpiresAt = &claims.Expires
	return nil
}
func (m *Module) verifySubmissionBundle(ctx context.Context, in *SubmissionBundleInput, at time.Time) (submissionBundleClaims, error) {
	var c submissionBundleClaims
	ring, err := m.submissionBundleKeys()
	if err != nil {
		return c, err
	}
	encoded, mac, ok := strings.Cut(in.Token, ".")
	if !ok {
		return c, ErrNotFound
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || !ring.MACMatches("download", raw, mac) || json.Unmarshal(raw, &c) != nil {
		return c, ErrNotFound
	}
	if c.Tenant != authn.TenantIDFromContext(ctx) || c.Principal != authn.PrincipalIDFromContext(ctx) || c.Submission.String() != in.ID || c.Package.String() != in.DeliveryID || c.Project <= 0 || c.Issued.After(at) || at.After(c.Expires) || !c.Expires.Equal(c.Issued.Add(72*time.Hour)) {
		return c, ErrNotFound
	}
	return c, nil
}

// Reuse the canonical source projection on every open. The private context is
// set only by the bundle handler and never expands ordinary package downloads.
func (m *Module) submissionBundleSelectionOn(ctx context.Context, tx *gorm.DB, c submissionBundleClaims) (SubmissionDelivery, packageManifest, error) {
	var empty SubmissionDelivery
	source, projectCtx, err := m.ownedSubmissionOn(ctx, tx, c.Submission)
	if err != nil {
		return empty, packageManifest{}, err
	}
	if source.ProjectID != c.Project || source.Status != "CONVERTED" {
		return empty, packageManifest{}, ErrNotFound
	}
	out := SubmissionDetail{SubmissionSummary: SubmissionSummary{Submission: source}}
	// Issuance is unnecessary during revalidation, and must not renew this URL.
	copyModule := *m
	copyModule.config.SubmissionBundles.SigningKey = ""
	if err = copyModule.submissionDeliveriesOn(projectCtx, tx, &out); err != nil {
		return empty, packageManifest{}, err
	}
	var view SubmissionDelivery
	for _, item := range out.Deliveries {
		if item.ID == c.Package {
			view = item
		}
	}
	if view.ID == 0 || bundleDigest(view.Manifest) != c.ManifestHash {
		return empty, packageManifest{}, ErrNotFound
	}
	row, err := m.loadSubmissionBundlePackageOn(projectCtx, tx, c)
	if err != nil {
		return empty, packageManifest{}, err
	}
	var manifest packageManifest
	if err = json.Unmarshal([]byte(row.Manifest), &manifest); err != nil {
		return empty, manifest, err
	}
	if bundleDigest(manifest.Content) != row.ContentHash {
		return empty, manifest, ErrConflict
	}
	if manifest.Bundle == nil || manifest.Bundle.State != "BUILT" {
		return empty, manifest, ErrBundleStale
	}
	return view, manifest, nil
}
func (m *Module) submissionBundleResourceOn(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner, grant submissionBundleGrant) error {
	c := grant.claims
	if owner.Scope != "entity" || owner.ID != c.Project || id != c.Package {
		return ErrNotFound
	}
	view, manifest, err := m.submissionBundleSelectionOn(ctx, tx, c)
	if err != nil {
		return err
	}
	for _, file := range view.Manifest {
		for _, binding := range manifest.Bindings {
			if binding.BindingID == grant.binding && binding.VersionID == file.VersionID {
				return nil
			}
		}
	}
	return ErrNotFound
}
func (m *Module) loadSubmissionBundlePackageOn(ctx context.Context, tx *gorm.DB, c submissionBundleClaims) (*model.DeliveryPackage, error) {
	q := query.BindDeliveryPackage(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(c.Tenant), q.ProjectID.Eq(c.Project), q.ID.Eq(c.Package), q.ArchivedAt.IsNull(), q.State.Neq("DRAFT")).Take()
	if err != nil {
		return nil, err
	}
	if row.ContentHash != c.PackageHash {
		return nil, ErrNotFound
	}
	return row, nil
}
func (m *Module) submissionBundleHTTP(ctx context.Context, in *SubmissionBundleInput) (*huma.StreamResponse, error) {
	c, err := m.verifySubmissionBundle(ctx, in, now())
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.writer() == nil || m.files.OpenOn == nil || m.bundleScanner.Scan == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	// Build outside the response callback so any integrity or storage error is a
	// normal API failure. The temporary ZIP is removed on failure and after streaming.
	scratch, err := os.CreateTemp("", "submission-bundle-*.zip")
	if err != nil {
		return nil, httpError(ctx, err)
	}
	failed := true
	defer func() {
		if failed {
			_ = scratch.Close()
			_ = os.Remove(scratch.Name())
		}
	}()
	inputs := []packageArchiveInput{}
	total := int64(0)
	var archive []byte
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		view, manifest, err := m.submissionBundleSelectionOn(ctx, tx, c)
		if err != nil {
			return err
		}
		for _, file := range view.Manifest {
			var binding guid.ID
			for _, pin := range manifest.Bindings {
				if pin.VersionID == file.VersionID {
					if binding != 0 && binding != pin.BindingID {
						return ErrConflict
					}
					binding = pin.BindingID
				}
			}
			if binding <= 0 {
				return ErrConflict
			}
			openCtx := context.WithValue(submissionContext(ctx, c.Project), submissionBundleGrantKey{}, submissionBundleGrant{claims: c, binding: binding})
			openCtx, cancel := context.WithTimeout(openCtx, 30*time.Second)
			read, err := m.files.OpenOn(openCtx, tx, binding)
			if err != nil {
				cancel()
				if read.Reader != nil {
					_ = read.Reader.Close()
				}
				return err
			}
			if read.Reader == nil {
				cancel()
				return ErrConflict
			}
			err = func() error {
				defer func() { _ = read.Reader.Close() }()
				if read.BindingID != binding || read.ResourceID != c.Package || read.Purpose != packagePurpose || read.ResourceKind != "client_package" || read.VersionID != file.VersionID || read.SHA256 != file.SHA256 {
					return ErrConflict
				}
				raw, err := io.ReadAll(io.LimitReader(read.Reader, (100<<20)+1))
				if err != nil {
					return err
				}
				total += int64(len(raw))
				if len(raw) > 100<<20 || total > packageBundleLimit {
					return ErrBundleTooLarge
				}
				if int64(len(raw)) != read.Size || packageSHA(raw) != file.SHA256 {
					return ErrConflict
				}
				assessment, scanErr := m.bundleScanner.Scan(openCtx, file.Name, read.MIME, file.SHA256, raw)
				if scanErr != nil {
					return ErrBundleScan
				}
				inputs = append(inputs, packageArchiveInput{ItemID: file.ItemID, Name: file.Name, MIME: read.MIME, SHA256: file.SHA256, Raw: raw, Overrides: file.Overrides, Assessment: &assessment})
				return nil
			}()
			if err == nil {
				err = openCtx.Err()
			}
			cancel()
			if err != nil {
				return err
			}
		}
		// A source-scoped export must retain its own item subset; using the
		// complete package ZIP would disclose other Intake sources. Reuse the
		// canonical redaction/deterministic builder after exact source binding.
		var identities []string
		label := "Team"
		if manifest.Bundle != nil {
			identities = manifest.Bundle.RedactionTerms
			if manifest.Bundle.ContributorLabel != "" {
				label = manifest.Bundle.ContributorLabel
			}
		}
		// Keep the established source-only whitelist: no raw work/project IDs.
		archive, _, err = buildPackageArchive(ctx, c.ManifestHash, "Delivery", "", inputs, identities, label)
		return err
	})
	if err == nil {
		_, err = scratch.Write(archive)
	}
	if err != nil {
		return nil, httpError(ctx, err)
	}
	size, err := scratch.Seek(0, io.SeekEnd)
	if err == nil {
		_, err = scratch.Seek(0, io.SeekStart)
	}
	if err != nil {
		return nil, httpError(ctx, err)
	}
	failed = false
	return &huma.StreamResponse{Body: func(stream huma.Context) {
		defer func() { _ = os.Remove(scratch.Name()) }()
		defer func() { _ = scratch.Close() }()
		stream.SetHeader("Content-Type", "application/zip")
		stream.SetHeader("Content-Disposition", "attachment; filename=delivery.zip")
		stream.SetHeader("Cache-Control", "private, no-store")
		stream.SetHeader("X-Content-Type-Options", "nosniff")
		stream.SetHeader("Content-Length", fmt.Sprint(size))
		_, _ = io.Copy(stream.BodyWriter(), scratch)
	}}, nil
}

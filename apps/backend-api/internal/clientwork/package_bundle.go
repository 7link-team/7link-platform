package clientwork

import (
	activityi18n "7link.com/platform/internal/clientwork/i18n"
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"io"
	"slices"
	"strings"
	"time"
)

var ErrBundleTooLarge = errors.New("clientwork: bundle too large")
var ErrBundleStale = errors.New("clientwork: bundle stale")
var ErrBundleScan = errors.New("clientwork: source secret scan failed")

const packageBundleJob = "clientwork.bundle_build"
const packageBundlePath = entityBase + "/delivery-packages/{id}/bundles"

type PackageBundleView struct {
	State              string          `json:"state" enum:"NONE,BUILDING,BUILT,FAILED,STALE"`
	JobID              guid.ID         `json:"job_id,omitempty"`
	Fingerprint        string          `json:"fingerprint,omitempty"`
	CurrentFingerprint string          `json:"current_fingerprint,omitempty"`
	BundleFileID       guid.ID         `json:"bundle_file_id,omitempty"`
	ManifestFileID     guid.ID         `json:"manifest_file_id,omitempty"`
	BundleSHA256       string          `json:"bundle_sha256,omitempty"`
	ManifestSHA256     string          `json:"manifest_sha256,omitempty"`
	Size               int64           `json:"size_bytes"`
	ErrorCode          string          `json:"error_code,omitempty"`
	Manifest           json.RawMessage `json:"manifest,omitempty"`
	BuiltAt            *time.Time      `json:"built_at,omitempty"`
}
type packageBundle struct {
	PackageBundleView
	ActorID           guid.ID  `json:"actor_id"`
	BundleBindingID   guid.ID  `json:"bundle_binding_id"`
	ManifestBindingID guid.ID  `json:"manifest_binding_id"`
	RedactionTerms    []string `json:"redaction_terms,omitempty"`
	ContributorLabel  string   `json:"contributor_label,omitempty"`
}
type packageBundleJobBody struct {
	ProjectID   guid.ID `json:"project_id"`
	PackageID   guid.ID `json:"package_id"`
	ActorID     guid.ID `json:"actor_id"`
	Fingerprint string  `json:"fingerprint"`
}

func (m *Module) injectPackageBundles(s *module.Services) error {
	if defects, err := module.Resolve[delivery.TestFailureDefects](s); err == nil {
		m.testDefects = defects
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	var e error
	if m.bundleScanner, e = module.Resolve[execution.PackageEvidenceScanner](s); e != nil {
		return e
	}
	if m.bundleInputs, e = module.Resolve[delivery.PackageInputs](s); e != nil {
		return e
	}
	if m.bundleFiles, e = module.Resolve[files.DeliveryBundleFiles](s); e != nil {
		return e
	}
	bindings, e := module.Resolve[*files.ResourceBindings](s)
	if e != nil {
		return e
	}
	if e = bindings.RegisterPurpose(files.ResourcePurpose{Name: testReportPurpose, ResourceKind: "delivery", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: m.testReportResourceOn}); e != nil {
		return e
	}
	for _, name := range []string{"delivery_bundle", "delivery_manifest"} {
		if e = bindings.RegisterPurpose(files.ResourcePurpose{Name: name, ResourceKind: "delivery", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: m.packageBundleResourceOn}); e != nil {
			return e
		}
	}
	return nil
}
func (m *Module) packageBundleResourceOn(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
	p := authn.EntityIDFromContext(ctx)
	if owner.Scope != "entity" || owner.ID != p {
		return ErrNotFound
	}
	if files.DeliveryBundleWrite(ctx) {
		if _, e := m.authorize(ctx, tx, p, "delivery_compose", false); e != nil {
			return e
		}
		if id == 0 {
			return nil
		}
		row, e := m.packageOn(ctx, tx, p, id, false)
		if e != nil {
			return e
		}
		if row.State != "DRAFT" || row.ArchivedAt != nil {
			return ErrConflict
		}
		return nil
	}
	if id == 0 {
		return ErrNotFound
	}
	ext, e := m.authorize(ctx, tx, p, "delivery_read", true)
	if e != nil {
		return e
	}
	_, e = m.packageOn(ctx, tx, p, id, ext)
	return e
}
func (m *Module) packageInputFactsOn(ctx context.Context, tx *gorm.DB, row *model.DeliveryPackage, manifest packageManifest) (delivery.PackageInputFacts, error) {
	if m.bundleInputs.ReadOn == nil {
		return delivery.PackageInputFacts{}, ErrUnavailable
	}
	facts, e := m.bundleInputs.ReadOn(ctx, tx, row.TenantID, row.ProjectID, manifest.Refs)
	if e != nil {
		return facts, e
	}
	if len(facts.SourceExecutionIDs) > 0 {
		if m.bundleScanner.IdentityTermsOn == nil {
			return facts, ErrUnavailable
		}
		terms, e := m.bundleScanner.IdentityTermsOn(ctx, tx, row.TenantID, row.ProjectID, facts.SourceExecutionIDs)
		if e != nil {
			return facts, e
		}
		facts.RedactionTerms = append(facts.RedactionTerms, terms...)
		slices.Sort(facts.RedactionTerms)
		facts.RedactionTerms = slices.Compact(facts.RedactionTerms)
	}
	return facts, nil
}
func (m *Module) packageFingerprintOn(ctx context.Context, tx *gorm.DB, row *model.DeliveryPackage, manifest packageManifest) (string, error) {
	if m.bundleInputs.ReadOn == nil {
		return "", ErrUnavailable
	}
	facts, e := m.packageInputFactsOn(ctx, tx, row, manifest)
	if e != nil {
		return "", e
	}
	type selection struct {
		EvidenceID     guid.ID `json:"evidence_id"`
		IncludeMode    string  `json:"include_mode"`
		OriginalSHA256 string  `json:"original_sha256"`
	}
	if len(manifest.Refs) != len(manifest.Content.Artifacts) || len(manifest.Refs) == 0 {
		return "", ErrInvalid
	}
	selections := make([]selection, 0, len(manifest.Refs))
	for i, ref := range manifest.Refs {
		selections = append(selections, selection{ref.EvidenceID, "FILE", manifest.Content.Artifacts[i].SHA256})
	}
	slices.SortFunc(selections, func(a, b selection) int {
		if a.EvidenceID < b.EvidenceID {
			return -1
		}
		if a.EvidenceID > b.EvidenceID {
			return 1
		}
		return 0
	})
	raw, e := packageCanonical(map[string]any{"items": facts.Items, "selections": selections, "release_notes_sha256": packageSHA([]byte(row.ReleaseNotes)), "portal_settings": facts.PortalSettings, "rules_version": packageRulesVersion, "source_scan_rules": execution.PackageEvidenceScanVersion, "title": row.Title, "identity_rules_sha256": bundleDigest(facts.RedactionTerms), "public_facts_sha256": bundleDigest(struct {
		Project delivery.PackageProjectFacts
		Items   []delivery.PackageWorkFacts
	}{facts.Project, facts.WorkItems}), "revision_at": row.UpdatedAt.UTC()})
	if e != nil {
		return "", e
	}
	return packageSHA(raw), nil
}
func (m *Module) storePackageManifestOn(ctx context.Context, tx *gorm.DB, row *model.DeliveryPackage, manifest packageManifest) error {
	raw, e := json.Marshal(manifest)
	if e != nil {
		return e
	}
	q := query.BindDeliveryPackage(tx, m.names)
	_, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.ID.Eq(row.ID)).UpdateSimple(q.Manifest.Value(string(raw)))
	return e
}
func (m *Module) markPackageBundleStaleOn(ctx context.Context, tx *gorm.DB, row *model.DeliveryPackage, manifest packageManifest) error {
	manifest.Bundle.State = "STALE"
	manifest.Bundle.ErrorCode = ""
	if err := m.storePackageManifestOn(ctx, tx, row, manifest); err != nil {
		return err
	}
	return m.event(ctx, tx, row.ProjectID, row.ID, "package_bundle_stale", "delivery_compose", row.Version)
}
func (m *Module) packageBundleViewOn(ctx context.Context, tx *gorm.DB, row *model.DeliveryPackage, manifest packageManifest) (PackageBundleView, error) {
	out := PackageBundleView{State: "NONE"}
	if manifest.Bundle == nil {
		return out, nil
	}
	out = manifest.Bundle.PackageBundleView
	if row.State != "DRAFT" {
		return out, nil
	}
	current, e := m.packageFingerprintOn(ctx, tx, row, manifest)
	if e != nil {
		if errors.Is(e, ErrUnavailable) {
			return out, e
		}
		out.State = "STALE"
		return out, nil
	}
	out.CurrentFingerprint = current
	if out.Fingerprint != current {
		out.State = "STALE"
	}
	return out, nil
}
func (m *Module) registerPackageBundlesHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.owner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.bundle_build", Method: "POST", Path: packageBundlePath, Summary: "Build a deterministic redacted fixed delivery bundle", Tags: []string{"entities"}, DefaultStatus: 202, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "delivery", Verb: "compose"}, m.packageBundleBuildHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.bundle", Method: "GET", Path: entityBase + "/delivery-packages/{id}/bundle", Summary: "Read current delivery bundle state and immutable hashes", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "delivery", Verb: "read"}, m.packageBundleHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.bundle_content", Method: "GET", Path: entityBase + "/delivery-packages/{id}/bundle/content", Summary: "Download the authorized redacted delivery ZIP", Tags: []string{"entities"}, Responses: binaryContentResponses(), Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "delivery", Verb: "read"}, m.packageBundleContentHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, packageBundlePath)
}
func (m *Module) packageBundleBuildHTTP(ctx context.Context, in *EditInput[ClientworkPublishBody]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.package.bundle_build", "POST", packageBundlePath, in.Key, 202, in, "delivery_compose", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.queuePackageBundleOn(ctx, tx, p, id, v) }, func(tx *gorm.DB) error {
		return m.lockRecordedParticipantsOn(ctx, tx, authn.PrincipalIDFromContext(ctx))
	})
}
func (m *Module) queuePackageBundleOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int) (Receipt, error) {
	row, e := m.packageOn(ctx, tx, p, id, false)
	if e != nil {
		return Receipt{}, e
	}
	if row.State != "DRAFT" || row.Version != v || row.ArchivedAt != nil {
		return Receipt{}, ErrConflict
	}
	var manifest packageManifest
	if e = json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
		return Receipt{}, e
	}
	fp, e := m.packageFingerprintOn(ctx, tx, row, manifest)
	if e != nil {
		return Receipt{}, e
	}
	if manifest.Bundle != nil {
		if manifest.Bundle.State == "BUILDING" {
			return Receipt{}, ErrConflict
		}
		if manifest.Bundle.State == "BUILT" && manifest.Bundle.Fingerprint == fp {
			return Receipt{ID: id, Version: row.Version, State: "BUILT", Bundle: &manifest.Bundle.PackageBundleView}, nil
		}
	}
	if m.forecastQueue == nil {
		return Receipt{}, ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	generation, e := m.nextID()
	if e != nil {
		return Receipt{}, e
	}
	job, e := m.forecastQueue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: row.TenantID, Kind: packageBundleJob, Payload: packageBundleJobBody{p, id, actor, fp}, CreatedBy: actor, ConcurrencyKey: "delivery-bundle:" + id.String(), IdempotencyKey: fmt.Sprintf("bundle:%s:%d:%s:%s", id, row.Version, fp, generation)})
	if e != nil {
		return Receipt{}, e
	}
	manifest.Bundle = &packageBundle{PackageBundleView: PackageBundleView{State: "BUILDING", JobID: job.JobID, Fingerprint: fp}, ActorID: actor}
	if e = m.storePackageManifestOn(ctx, tx, row, manifest); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "package_bundle_requested", "delivery_compose", row.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: row.Version, State: "BUILDING", Bundle: &manifest.Bundle.PackageBundleView}, nil
}
func (m *Module) packageBundleHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[PackageBundleView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out PackageBundleView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ext, e := m.authorize(ctx, tx, p, "delivery_read", true)
		if e != nil {
			return e
		}
		row, e := m.packageOn(ctx, tx, p, id, ext)
		if e != nil {
			return e
		}
		var manifest packageManifest
		if e = json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
			return e
		}
		out, e = m.packageBundleViewOn(ctx, tx, row, manifest)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) packageBundleContentHTTP(ctx context.Context, in *ObjectInput) (*huma.StreamResponse, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.streamContent(ctx, p, func(tx *gorm.DB, ext bool) (guid.ID, string, error) {
		row, e := m.packageOn(ctx, tx, p, id, ext)
		if e != nil {
			return 0, "", e
		}
		var manifest packageManifest
		if e = json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
			return 0, "", e
		}
		view, e := m.packageBundleViewOn(ctx, tx, row, manifest)
		if e != nil {
			return 0, "", e
		}
		if view.State != "BUILT" || manifest.Bundle == nil {
			return 0, "", ErrBundleStale
		}
		return manifest.Bundle.BundleBindingID, view.BundleSHA256, nil
	})
}
func (m *Module) packageBundleHandler() jobs.Handler {
	return jobs.Handler{DisplayNameKey: activityi18n.Namespace + ".activity_job_clientwork_bundle_build_name", DescriptionKey: activityi18n.Namespace + ".activity_job_clientwork_bundle_build_description", Kind: packageBundleJob, Timeout: 10 * time.Minute, MaxAttempts: 4, BackoffBase: 30 * time.Second, BackoffMax: 10 * time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		var in packageBundleJobBody
		if json.Unmarshal(job.Payload, &in) != nil || job.TenantID <= 0 || in.ProjectID <= 0 || in.PackageID <= 0 || in.ActorID <= 0 || job.CreatedBy != in.ActorID || len(in.Fingerprint) != 64 {
			return jobs.Permanent(ErrInvalid)
		}
		ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: job.TenantID, EntityID: in.ProjectID, PrincipalID: in.ActorID, SubjectType: authn.SubjectTypePrincipal})
		err := m.buildPackageBundle(ctx, in, job.ID)
		if err != nil && job.MaxAttempts > 0 && job.Attempt >= job.MaxAttempts {
			return m.failPackageBundle(ctx, in, job.ID, err, true)
		}
		return err
	}}
}
func (m *Module) buildPackageBundle(ctx context.Context, in packageBundleJobBody, jobID guid.ID) error {
	if m.writer() == nil {
		return ErrUnavailable
	}
	if m.bundleScanner.Scan == nil {
		return m.failPackageBundle(ctx, in, jobID, ErrBundleScan)
	}
	if m.writer() == nil || m.files.OpenOn == nil || m.bundleFiles.Stage == nil || m.bundleFiles.AttachOn == nil {
		return ErrUnavailable
	}
	var archive, public []byte
	var redactionTerms []string
	var contributorLabel string
	var version int
	stale := false
	e := m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.lockRecordedParticipantsOn(ctx, tx, in.ActorID); e != nil {
			return e
		}
		if _, e := m.authorize(ctx, tx, in.ProjectID, "delivery_compose", false); e != nil {
			return e
		}
		row, e := m.packageOn(ctx, tx, in.ProjectID, in.PackageID, false)
		if e != nil {
			return e
		}
		version = row.Version
		var manifest packageManifest
		if e = json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
			return e
		}
		if manifest.Bundle == nil || manifest.Bundle.State != "BUILDING" || manifest.Bundle.JobID != jobID || manifest.Bundle.ActorID != in.ActorID || manifest.Bundle.Fingerprint != in.Fingerprint {
			return nil
		}
		current, e := m.packageFingerprintOn(ctx, tx, row, manifest)
		if e != nil {
			return e
		}
		if row.State != "DRAFT" || current != in.Fingerprint {
			stale = true
			return m.markPackageBundleStaleOn(ctx, tx, row, manifest)
		}
		items := packageItems(row, manifest.Content)
		inputs := make([]packageArchiveInput, 0, len(items))
		total := int64(0)
		for _, item := range items {
			var binding guid.ID
			for _, pin := range manifest.Bindings {
				if pin.VersionID == item.Artifact.VersionID {
					binding = pin.BindingID
				}
			}
			if binding <= 0 {
				return ErrConflict
			}
			scan, cancel := context.WithTimeout(ctx, 30*time.Second)
			read, e := m.files.OpenOn(scan, tx, binding)
			if e != nil {
				cancel()
				return e
			}
			if read.Reader == nil {
				cancel()
				return ErrUnavailable
			}
			raw, e := io.ReadAll(io.LimitReader(read.Reader, (100<<20)+1))
			closeErr := read.Reader.Close()
			scanErr := scan.Err()
			cancel()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
			if scanErr != nil {
				return scanErr
			}
			total += int64(len(raw))
			if len(raw) > 100<<20 || total > packageBundleLimit {
				return ErrBundleTooLarge
			}
			if read.BindingID != binding || read.ResourceID != row.ID || read.Purpose != packagePurpose || read.ResourceKind != "client_package" || read.SHA256 != item.Artifact.SHA256 || read.VersionID != item.Artifact.VersionID || int64(len(raw)) != read.Size || packageSHA(raw) != item.Artifact.SHA256 {
				return ErrConflict
			}
			scan, cancel = context.WithTimeout(ctx, 30*time.Second)
			assessment, scanErr := m.bundleScanner.Scan(scan, item.Artifact.Name, read.MIME, item.Artifact.SHA256, raw)
			if scanErr == nil {
				scanErr = scan.Err()
			}
			cancel()
			if scanErr != nil {
				return ErrBundleScan
			}
			inputs = append(inputs, packageArchiveInput{ItemID: item.ItemID, Name: item.Artifact.Name, MIME: read.MIME, SHA256: item.Artifact.SHA256, Raw: raw, Overrides: item.Artifact.Overrides, Assessment: &assessment})
		}
		facts, err := m.packageInputFactsOn(ctx, tx, row, manifest)
		if err != nil {
			return err
		}
		redactionTerms = facts.RedactionTerms
		contributorLabel = packageContributorLabel(facts.PortalSettings)
		for i, ref := range manifest.Refs {
			inputs[i].WorkItemID = ref.WorkItemID
			inputs[i].EvidenceID = ref.EvidenceID
			for _, work := range facts.WorkItems {
				if work.WorkItemID == ref.WorkItemID {
					inputs[i].WorkKey = work.Key
				}
			}
		}
		archive, public, e = buildPackageArchive(ctx, in.Fingerprint, row.Title, row.ReleaseNotes, inputs, redactionTerms, contributorLabel, packageArchiveMetadata{ID: row.ID, RevisionAt: row.UpdatedAt, Facts: facts})
		return e
	})
	if e != nil {
		return m.failPackageBundle(ctx, in, jobID, e)
	}
	if stale || archive == nil {
		return nil
	}
	zipObject, e := m.bundleFiles.Stage(ctx, in.ProjectID, "delivery_bundle", bytes.NewReader(archive), int64(len(archive)))
	if e != nil {
		return e
	}
	manifestObject, e := m.bundleFiles.Stage(ctx, in.ProjectID, "delivery_manifest", bytes.NewReader(public), int64(len(public)))
	if e != nil {
		return e
	}
	return m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.lockRecordedParticipantsOn(ctx, tx, in.ActorID); e != nil {
			return e
		}
		if _, e := m.authorize(ctx, tx, in.ProjectID, "delivery_compose", false); e != nil {
			return e
		}
		row, e := m.packageOn(ctx, tx, in.ProjectID, in.PackageID, false)
		if e != nil {
			return e
		}
		var manifest packageManifest
		if e = json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
			return e
		}
		if manifest.Bundle == nil || manifest.Bundle.State != "BUILDING" || manifest.Bundle.JobID != jobID {
			return nil
		}
		current, e := m.packageFingerprintOn(ctx, tx, row, manifest)
		if e != nil {
			return e
		}
		if row.Version != version || row.State != "DRAFT" || current != in.Fingerprint {
			return m.markPackageBundleStaleOn(ctx, tx, row, manifest)
		}
		zipFile, e := m.bundleFiles.AttachOn(ctx, tx, zipObject, row.ID)
		if e != nil {
			return e
		}
		manifestFile, e := m.bundleFiles.AttachOn(ctx, tx, manifestObject, row.ID)
		if e != nil {
			return e
		}
		if zipFile.SHA256 != packageSHA(archive) || manifestFile.SHA256 != packageSHA(public) {
			return ErrConflict
		}
		at := now()
		manifest.Bundle = &packageBundle{PackageBundleView: PackageBundleView{State: "BUILT", JobID: jobID, Fingerprint: in.Fingerprint, BundleFileID: zipFile.FileID, ManifestFileID: manifestFile.FileID, BundleSHA256: zipFile.SHA256, ManifestSHA256: manifestFile.SHA256, Size: int64(len(archive)), Manifest: public, BuiltAt: &at}, ActorID: in.ActorID, BundleBindingID: zipFile.BindingID, ManifestBindingID: manifestFile.BindingID, RedactionTerms: redactionTerms, ContributorLabel: contributorLabel}
		if e = m.storePackageManifestOn(ctx, tx, row, manifest); e != nil {
			return e
		}
		return m.event(ctx, tx, in.ProjectID, row.ID, "package_bundle_built", "delivery_compose", row.Version)
	})
}
func (m *Module) failPackageBundle(ctx context.Context, in packageBundleJobBody, jobID guid.ID, cause error, exhausted ...bool) error {
	if m.writer() == nil {
		return ErrUnavailable
	}
	terminal := len(exhausted) > 0 && exhausted[0]
	stale := errors.Is(cause, delivery.ErrResultState) || errors.Is(cause, delivery.ErrVerificationPending) || errors.Is(cause, delivery.ErrResultCoverage)
	redaction := errors.Is(cause, ErrBundleScan) || errors.Is(cause, ErrInvalid) || errors.Is(cause, ErrConflict) || errors.Is(cause, ErrBundleTooLarge) || errors.Is(cause, context.DeadlineExceeded)
	if !terminal && !stale && !redaction {
		return cause
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return m.writer().WithContext(finish).Transaction(func(tx *gorm.DB) error {
		// This can only close the exact queued build and creates no artifact/read
		// authority. It must also terminate when the original actor was revoked.
		if m.principals.Lock == nil || m.audit == nil || m.events == nil {
			return ErrUnavailable
		}
		if e := m.principals.Lock(finish, tx, in.ActorID); e != nil {
			return e
		}
		row, e := m.packageOn(finish, tx, in.ProjectID, in.PackageID, false)
		if e != nil {
			return e
		}
		var manifest packageManifest
		if e = json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
			return e
		}
		if manifest.Bundle == nil || manifest.Bundle.ActorID != in.ActorID || manifest.Bundle.JobID != jobID || manifest.Bundle.Fingerprint != in.Fingerprint || manifest.Bundle.State != "BUILDING" {
			return nil
		}
		manifest.Bundle.State = "FAILED"
		manifest.Bundle.ErrorCode = "BUILD_FAILED"
		if redaction {
			manifest.Bundle.ErrorCode = "REDACTION_FAILED"
		}
		if errors.Is(cause, ErrBundleTooLarge) {
			manifest.Bundle.ErrorCode = "TOO_LARGE"
		}
		if stale {
			manifest.Bundle.State = "STALE"
			manifest.Bundle.ErrorCode = ""
		}
		if e = m.storePackageManifestOn(finish, tx, row, manifest); e != nil {
			return e
		}
		detail := map[string]any{"project_id": in.ProjectID.String(), "job_id": jobID.String(), "state": manifest.Bundle.State, "error_code": manifest.Bundle.ErrorCode, "authority_source": "system:bundle_build"}
		eventType := "clientwork.package_bundle_failed"
		if stale {
			eventType = "clientwork.package_bundle_stale"
		}
		if e = m.audit(finish, tx, audit.DomainEvent{TenantID: row.TenantID, EventType: eventType, TargetType: Name, TargetID: row.ID, Detail: detail}); e != nil {
			return e
		}
		_, e = m.events.Publish(finish, tx, events.Event{TenantID: row.TenantID, ProjectID: in.ProjectID, Type: eventType, Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "system:bundle_build"}, Subject: events.Subject{Kind: Name, ID: row.ID}, SubjectVersion: int64(row.Version), Data: detail})
		return e
	})
}

// A canonical public role label is controlled by portal policy, never by a
// caller-supplied display name. IDs and operational identities stay private.
func packageContributorLabel(portal json.RawMessage) string {
	var p struct {
		ContributorDisplay string `json:"contributor_display"`
	}
	if json.Unmarshal(portal, &p) == nil && strings.EqualFold(p.ContributorDisplay, "ROLES") {
		return "Engineer"
	}
	return "Team"
}

func (m *Module) requireCurrentPackageBundleOn(ctx context.Context, tx *gorm.DB, row *model.DeliveryPackage, manifest packageManifest) error {
	if manifest.Bundle == nil || manifest.Bundle.State != "BUILT" || manifest.Bundle.BundleBindingID <= 0 || manifest.Bundle.ManifestBindingID <= 0 {
		return ErrBundleStale
	}
	fp, e := m.packageFingerprintOn(ctx, tx, row, manifest)
	if e != nil {
		return e
	}
	if fp != manifest.Bundle.Fingerprint {
		return ErrBundleStale
	}
	return nil
}

package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"strings"
	"testing"
)

func TestPackageBundleBuildSourceFailuresDoNotAttachOrPublishArtifacts(t *testing.T) {
	for _, fault := range []string{"binding", "open", "reader", "read", "integrity", "facts", "first_stage", "second_stage"} {
		t.Run(fault, func(t *testing.T) {
			m, db, projects, _ := harness(t)
			ctx := testContext(t, 2)
			data := []byte("actual evidence\n")
			m.projects = packageTestArtifacts{ClientWorkAccess: projects, raw: data}
			m.files.PinOn = func(_ context.Context, _ *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
				return files.PinnedResource{BindingID: in.ID, VersionID: *in.VersionID, SHA256: packageSHA(data)}, nil
			}
			created, err := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "build-source", Body: PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
			require.NoError(t, err)
			pkg := created.Body.Data.ID
			queue := &packageTestQueue{}
			m.forecastQueue = queue
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.queuePackageBundleOn(ctx, tx, 3, pkg, 1); return e }))
			payload, err := json.Marshal(queue.requests[0].Payload)
			require.NoError(t, err)
			var in packageBundleJobBody
			require.NoError(t, json.Unmarshal(payload, &in))
			if fault == "binding" {
				var row model.DeliveryPackage
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&row).Error)
				var manifest packageManifest
				require.NoError(t, json.Unmarshal([]byte(row.Manifest), &manifest))
				manifest.Bindings = nil
				require.NoError(t, m.storePackageManifestOn(ctx, db, &row, manifest))
			}
			m.files.OpenOn = func(_ context.Context, _ *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
				if fault == "open" {
					return files.PinnedResourceRead{}, ErrUnavailable
				}
				read := files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: id, ResourceID: pkg, Purpose: packagePurpose, ResourceKind: "client_package", VersionID: 40, SHA256: packageSHA(data), MIME: "text/plain", Size: int64(len(data))}, Reader: io.NopCloser(bytes.NewReader(data))}
				if fault == "reader" {
					require.NoError(t, read.Reader.Close())
					read.Reader = nil
				}
				if fault == "read" {
					read.Reader = failingBundleReader{}
				}
				if fault == "integrity" {
					read.ResourceID++
				}
				return read, nil
			}
			factsCalls := 0
			if fault == "facts" {
				m.bundleInputs.ReadOn = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, refs []delivery.ClientArtifactRef) (delivery.PackageInputFacts, error) {
					factsCalls++
					if factsCalls == 2 {
						return delivery.PackageInputFacts{}, ErrUnavailable
					}
					return packageTestInputFacts(ctx, tx, tenant, project, refs)
				}
			}
			stages := 0
			m.bundleFiles.Stage = func(context.Context, guid.ID, string, io.Reader, int64) (*files.PreparedDeliveryBundle, error) {
				stages++
				if fault == "first_stage" || stages == 2 {
					return nil, ErrUnavailable
				}
				return &files.PreparedDeliveryBundle{}, nil
			}
			m.bundleFiles.AttachOn = func(context.Context, *gorm.DB, *files.PreparedDeliveryBundle, guid.ID) (files.PinnedResource, error) {
				t.Fatal("failed source/staging must never attach")
				return files.PinnedResource{}, nil
			}
			err = m.buildPackageBundle(ctx, in, 501)
			terminal := fault == "binding" || fault == "integrity"
			if terminal {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			var stored model.DeliveryPackage
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&stored).Error)
			var manifest packageManifest
			require.NoError(t, json.Unmarshal([]byte(stored.Manifest), &manifest))
			if terminal {
				require.Equal(t, "FAILED", manifest.Bundle.State)
				require.Equal(t, "REDACTION_FAILED", manifest.Bundle.ErrorCode)
			} else {
				require.Equal(t, "BUILDING", manifest.Bundle.State)
			}
			require.Zero(t, manifest.Bundle.BundleFileID)
			require.Equal(t, 1, stored.Version)
		})
	}
}

func TestPackageBundleQueueAndFingerprintFailuresPreserveDraft(t *testing.T) {
	for _, fault := range []string{"version", "missing", "manifest", "fingerprint", "queue", "ids", "update", "audit", "building", "built"} {
		t.Run(fault, func(t *testing.T) {
			m, db, pkg := releaseNoteHarness(t)
			ctx := testContext(t, 2)
			queue := &packageTestQueue{}
			m.forecastQueue = queue
			version := 1
			id := pkg
			switch fault {
			case "version":
				version = 9
			case "missing":
				id = 999
			case "manifest":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", "[]").Error)
			case "fingerprint":
				m.bundleInputs.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) (delivery.PackageInputFacts, error) {
					return delivery.PackageInputFacts{}, ErrUnavailable
				}
			case "queue":
				m.forecastQueue = nil
			case "ids":
				m.ids = &unavailableIDs{source: m.ids, at: 1}
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("queue_write_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.DeliveryPackage.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
			case "building":
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.queuePackageBundleOn(ctx, tx, 3, pkg, 1); return e }))
			case "built":
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return packageBuiltFixture(t, m, ctx, tx, 3, pkg) }))
			}
			var receipt Receipt
			err := db.Transaction(func(tx *gorm.DB) error {
				var e error
				receipt, e = m.queuePackageBundleOn(ctx, tx, 3, id, version)
				return e
			})
			if fault == "built" {
				require.NoError(t, err)
				require.Equal(t, "BUILT", receipt.State)
				require.Empty(t, queue.requests)
			} else {
				require.Error(t, err)
			}
			require.NoError(t, db.Callback().Update().Remove("queue_write_failure"))
			var row model.DeliveryPackage
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&row).Error)
			require.Equal(t, "DRAFT", row.State)
			require.Equal(t, 1, row.Version)
			if fault == "update" || fault == "audit" {
				var stored packageManifest
				require.NoError(t, json.Unmarshal([]byte(row.Manifest), &stored))
				require.Nil(t, stored.Bundle)
			}
		})
	}
}

func TestPackageBundleSourceIdentityTermsMustBeAvailableAndCanonical(t *testing.T) {
	m, db, pkg := releaseNoteHarness(t)
	var row model.DeliveryPackage
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&row).Error)
	var manifest packageManifest
	require.NoError(t, json.Unmarshal([]byte(row.Manifest), &manifest))
	ctx := testContext(t, 2)
	m.bundleInputs.ReadOn = nil
	_, err := m.packageInputFactsOn(ctx, db, &row, manifest)
	require.ErrorIs(t, err, ErrUnavailable)
	m.bundleInputs.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) (delivery.PackageInputFacts, error) {
		return delivery.PackageInputFacts{SourceExecutionIDs: []guid.ID{80}, RedactionTerms: []string{"Zed"}}, nil
	}
	m.bundleScanner.IdentityTermsOn = nil
	_, err = m.packageInputFactsOn(ctx, db, &row, manifest)
	require.ErrorIs(t, err, ErrUnavailable)
	m.bundleScanner.IdentityTermsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]string, error) {
		return nil, ErrUnavailable
	}
	_, err = m.packageInputFactsOn(ctx, db, &row, manifest)
	require.ErrorIs(t, err, ErrUnavailable)
	m.bundleScanner.IdentityTermsOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, executions []guid.ID) ([]string, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		require.Equal(t, []guid.ID{80}, executions)
		return []string{"Ada", "Zed", "Ada"}, nil
	}
	facts, err := m.packageInputFactsOn(ctx, db, &row, manifest)
	require.NoError(t, err)
	require.Equal(t, []string{"Ada", "Zed"}, facts.RedactionTerms)
	_, err = m.packageFingerprintOn(ctx, db, &row, packageManifest{})
	require.ErrorIs(t, err, ErrInvalid)
	bad := manifest
	bad.Content.Artifacts = append(bad.Content.Artifacts, delivery.ClientArtifact{})
	_, err = m.packageFingerprintOn(ctx, db, &row, bad)
	require.ErrorIs(t, err, ErrInvalid)
	manifest.Bundle = &packageBundle{PackageBundleView: PackageBundleView{State: "BUILT", Fingerprint: "outdated"}, BundleBindingID: 901, ManifestBindingID: 902}
	require.ErrorIs(t, m.requireCurrentPackageBundleOn(ctx, db, &row, manifest), ErrBundleStale)
	view, err := m.packageBundleViewOn(ctx, db, &row, manifest)
	require.NoError(t, err)
	require.Equal(t, "STALE", view.State)
	m.bundleInputs.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) (delivery.PackageInputFacts, error) {
		return delivery.PackageInputFacts{}, ErrForbidden
	}
	view, err = m.packageBundleViewOn(ctx, db, &row, manifest)
	require.NoError(t, err)
	require.Equal(t, "STALE", view.State)
	m.bundleInputs.ReadOn = nil
	_, err = m.packageBundleViewOn(ctx, db, &row, manifest)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestPackageBundleDownloadRequiresBuiltFrozenBytesAndCurrentAuthority(t *testing.T) {
	m, db, pkg := releaseNoteHarness(t)
	ctx := testContext(t, 2)
	in := &ObjectInput{EntityID: "3", ID: pkg.String()}
	for _, bad := range []*ObjectInput{{EntityID: "bad", ID: pkg.String()}, {EntityID: "3", ID: "bad"}} {
		out, err := m.packageBundleContentHTTP(ctx, bad)
		require.Error(t, err)
		require.Nil(t, out)
	}
	out, err := m.packageBundleContentHTTP(ctx, in)
	require.Error(t, err, "draft without built ZIP is unavailable")
	require.Nil(t, out)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return packageBuiltFixture(t, m, ctx, tx, 3, pkg) }))
	var row model.DeliveryPackage
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&row).Error)
	var manifest packageManifest
	require.NoError(t, json.Unmarshal([]byte(row.Manifest), &manifest))
	manifest.Bundle.BundleSHA256 = strings.Repeat("a", 64)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.storePackageManifestOn(ctx, tx, &row, manifest) }))
	out, err = m.packageBundleContentHTTP(ctx, in)
	require.NoError(t, err)
	require.NotNil(t, out)
	_, api := humatest.New(t)
	response := out
	huma.Register(api, huma.Operation{OperationID: "test.bundle.bytes", Method: "GET", Path: "/bundle"}, func(context.Context, *struct{}) (*huma.StreamResponse, error) { return response, nil })
	downloaded := api.Get("/bundle")
	require.Equal(t, "test", downloaded.Body.String())
	_, err = m.packageBundleContentHTTP(testContext(t, 4), in)
	require.Error(t, err, "draft ZIP stays hidden from client")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishPackageOn(ctx, tx, 3, pkg, 1); return e }))
	out, err = m.packageBundleContentHTTP(testContext(t, 4), in)
	require.NoError(t, err)
	require.NotNil(t, out)
	response = out
	downloaded = api.Get("/bundle")
	require.Equal(t, "test", downloaded.Body.String())
	for _, owner := range []files.ResourceOwner{{Scope: "tenant", ID: 3}, {Scope: "entity", ID: 9}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.packageBundleResourceOn(ctx, tx, pkg, owner) }), ErrNotFound)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.packageBundleResourceOn(testContext(t, 4), tx, pkg, files.ResourceOwner{Scope: "entity", ID: 3})
	}))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return m.packageBundleResourceOn(testContext(t, 5), tx, pkg, files.ResourceOwner{Scope: "entity", ID: 3})
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.packageBundleResourceOn(ctx, tx, 0, files.ResourceOwner{Scope: "entity", ID: 3})
	}), ErrNotFound)
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", "[]").Error)
	out, err = m.packageBundleContentHTTP(ctx, in)
	require.Error(t, err)
	require.Nil(t, out)
}

type packageTestQueue struct {
	jobs.Queue
	requests []jobs.EnqueueRequest
}

func (q *packageTestQueue) Enqueue(_ context.Context, _ *gorm.DB, in jobs.EnqueueRequest) (jobs.EnqueueResult, error) {
	q.requests = append(q.requests, in)
	return jobs.EnqueueResult{JobID: guid.ID(500 + len(q.requests)), Created: true}, nil
}

type packageTestArtifacts struct {
	delivery.ClientWorkAccess
	raw []byte
}

func (p packageTestArtifacts) AcceptedArtifactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, refs []delivery.ClientArtifactRef) ([]delivery.ClientArtifact, error) {
	out, e := p.ClientWorkAccess.AcceptedArtifactsOn(ctx, tx, tenant, project, refs)
	for i := range out {
		out[i].SHA256 = packageSHA(p.raw)
	}
	return out, e
}
func packageTestInputFacts(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, refs []delivery.ClientArtifactRef) (delivery.PackageInputFacts, error) {
	seen := map[guid.ID]bool{}
	facts := delivery.PackageInputFacts{PortalSettings: json.RawMessage(`{"contributor_display":"TEAM_ONLY"}`)}
	for _, ref := range refs {
		if !seen[ref.WorkItemID] {
			seen[ref.WorkItemID] = true
			facts.Items = append(facts.Items, delivery.PackageInputItem{WorkItemID: ref.WorkItemID, AcceptedDecisionID: 99})
		}
	}
	return facts, nil
}

// Older domain tests isolate signing/rework/current-client semantics using an
// already-built bundle fixture. Bundle production and integrity are exercised
// below with actual ZIP bytes, queue envelopes and transactional file effects.
func packageBuiltFixture(t *testing.T, m *Module, ctx context.Context, tx *gorm.DB, p, id guid.ID) error {
	t.Helper()
	row, err := m.packageOn(ctx, tx, p, id, false)
	if err != nil {
		return err
	}
	if row.State != "DRAFT" {
		return nil
	}
	var manifest packageManifest
	if err := json.Unmarshal([]byte(row.Manifest), &manifest); err != nil {
		return err
	}
	fp, err := m.packageFingerprintOn(ctx, tx, row, manifest)
	if err != nil {
		return err
	}
	manifest.Bundle = &packageBundle{PackageBundleView: PackageBundleView{State: "BUILT", Fingerprint: fp}, BundleBindingID: 901, ManifestBindingID: 902}
	return m.storePackageManifestOn(ctx, tx, row, manifest)
}
func publishBuiltPackage(t *testing.T, m *Module, ctx context.Context, tx *gorm.DB, p, id guid.ID, v int) (Receipt, error) {
	if err := packageBuiltFixture(t, m, ctx, tx, p, id); err != nil {
		return Receipt{}, err
	}
	return m.publishPackageOn(ctx, tx, p, id, v)
}
func publishBuiltPackageHTTP(t *testing.T, m *Module, ctx context.Context, in *EditInput[PackagePublishBody]) (*CommandOutput, error) {
	t.Helper()
	p, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.ID)
	if e == nil && e2 == nil && m.writer() != nil {
		require.NoError(t, m.writer().Transaction(func(tx *gorm.DB) error { return packageBuiltFixture(t, m, testContext(t, 2), tx, p, id) }))
	}
	return m.package_publishHTTP(ctx, in)
}

func TestPackageBundleJobBuildStalePublishAndAtomicFileEffects(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	raw := []byte("report: actual test\npassword=private-value\n")
	m.projects = packageTestArtifacts{ClientWorkAccess: projects, raw: raw}
	m.bundleInputs = delivery.PackageInputs{ReadOn: packageTestInputFacts}
	// Pins are immutable file facts; only the test source bytes change here.
	m.files.PinOn = func(_ context.Context, _ *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
		return files.PinnedResource{BindingID: 701, VersionID: *in.VersionID, SHA256: packageSHA(raw)}, nil
	}
	var packageID guid.ID
	m.files.OpenOn = func(_ context.Context, _ *gorm.DB, b guid.ID) (files.PinnedResourceRead, error) {
		return files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: b, ResourceID: packageID, Purpose: packagePurpose, ResourceKind: "client_package", VersionID: 40, SHA256: packageSHA(raw), MIME: "text/plain", Size: int64(len(raw))}, Reader: io.NopCloser(bytes.NewReader(raw))}, nil
	}
	queue := &packageTestQueue{}
	m.forecastQueue = queue
	staged := map[*files.PreparedDeliveryBundle][]byte{}
	m.bundleFiles.Stage = func(_ context.Context, _ guid.ID, _ string, r io.Reader, _ int64) (*files.PreparedDeliveryBundle, error) {
		raw, e := io.ReadAll(r)
		p := &files.PreparedDeliveryBundle{}
		staged[p] = raw
		return p, e
	}
	attachments := 0
	m.bundleFiles.AttachOn = func(_ context.Context, tx *gorm.DB, p *files.PreparedDeliveryBundle, id guid.ID) (files.PinnedResource, error) {
		attachments++
		if e := tx.Exec("INSERT INTO baseline_probe VALUES (?)", attachments).Error; e != nil {
			return files.PinnedResource{}, e
		}
		return files.PinnedResource{FileID: guid.ID(800 + attachments), BindingID: guid.ID(900 + attachments), SHA256: packageSHA(staged[p])}, nil
	}
	body := PackageBody{Title: "Release", ReleaseNotes: "Actual release notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}
	created, e := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "bundle-create", Body: body})
	require.NoError(t, e)
	id := created.Body.Data.ID
	packageID = id
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishPackageOn(ctx, tx, 3, id, 1); return e }), ErrBundleStale)
	input := &EditInput[ClientworkPublishBody]{EntityID: "3", ID: id.String(), IfMatch: `"1"`, Key: "bundle-build"}
	receipt, e := m.packageBundleBuildHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, "BUILDING", receipt.Body.Data.State)
	require.Len(t, queue.requests, 1)
	replay, e := m.packageBundleBuildHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Len(t, queue.requests, 1)
	payload, e := json.Marshal(queue.requests[0].Payload)
	require.NoError(t, e)
	job := jobs.Job{ID: 501, TenantID: 1, CreatedBy: 2, Payload: payload}
	publisher := m.events
	m.events = failPublisher{}
	require.Error(t, m.packageBundleHandler().Run(ctx, job))
	var count int64
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.Zero(t, count, "file metadata and first binding roll back if completion outbox fails")
	m.events = publisher
	require.NoError(t, m.packageBundleHandler().Run(ctx, job))
	view, e := m.packageBundleHTTP(ctx, &ObjectInput{EntityID: "3", ID: id.String()})
	require.NoError(t, e)
	require.Equal(t, "BUILT", view.Body.Data.State)
	require.NotEmpty(t, view.Body.Data.BundleSHA256)
	require.NotContains(t, string(view.Body.Data.Manifest), "private-value")
	require.NoError(t, m.packageBundleHandler().Run(ctx, job))
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.EqualValues(t, 2, count, "duplicate job cannot attach another pair")
	body.ReleaseNotes = "Edited release notes"
	_, e = m.editPackageHTTP(ctx, &EditInput[PackageBody]{EntityID: "3", ID: id.String(), IfMatch: `"1"`, Key: "bundle-edit", Body: body})
	require.NoError(t, e)
	view, e = m.packageBundleHTTP(ctx, &ObjectInput{EntityID: "3", ID: id.String()})
	require.NoError(t, e)
	require.Equal(t, "STALE", view.Body.Data.State)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishPackageOn(ctx, tx, 3, id, 2); return e }), ErrBundleStale)

	// Losing the original actor's project authority retries transiently, then
	// closes only this exact build without creating any new file authority.
	_, e = m.packageBundleBuildHTTP(ctx, &EditInput[ClientworkPublishBody]{EntityID: "3", ID: id.String(), IfMatch: `"2"`, Key: "bundle-retry-revoked"})
	require.NoError(t, e)
	payload, e = json.Marshal(queue.requests[1].Payload)
	require.NoError(t, e)
	job = jobs.Job{ID: 502, TenantID: 1, CreatedBy: 2, Payload: payload, Attempt: 1, MaxAttempts: 4}
	projects.deny = true
	require.Error(t, m.packageBundleHandler().Run(ctx, job))
	job.Attempt = 4
	require.NoError(t, m.packageBundleHandler().Run(ctx, job))
	projects.deny = false
	view, e = m.packageBundleHTTP(ctx, &ObjectInput{EntityID: "3", ID: id.String()})
	require.NoError(t, e)
	require.Equal(t, "FAILED", view.Body.Data.State)
	require.Equal(t, "BUILD_FAILED", view.Body.Data.ErrorCode)

	// An integrity failure is terminal immediately, and a successful retry can
	// still become stale between staging and the atomic metadata transaction.
	_, e = m.packageBundleBuildHTTP(ctx, &EditInput[ClientworkPublishBody]{EntityID: "3", ID: id.String(), IfMatch: `"2"`, Key: "bundle-retry-tamper"})
	require.NoError(t, e)
	payload, e = json.Marshal(queue.requests[2].Payload)
	require.NoError(t, e)
	job = jobs.Job{ID: 503, TenantID: 1, CreatedBy: 2, Payload: payload}
	open := m.files.OpenOn
	m.files.OpenOn = func(ctx context.Context, tx *gorm.DB, b guid.ID) (files.PinnedResourceRead, error) {
		read, err := open(ctx, tx, b)
		read.Reader = io.NopCloser(bytes.NewReader([]byte("tampered")))
		return read, err
	}
	require.NoError(t, m.packageBundleHandler().Run(ctx, job))
	view, e = m.packageBundleHTTP(ctx, &ObjectInput{EntityID: "3", ID: id.String()})
	require.NoError(t, e)
	require.Equal(t, "FAILED", view.Body.Data.State)
	require.Equal(t, "REDACTION_FAILED", view.Body.Data.ErrorCode)
	m.files.OpenOn = open
	_, e = m.packageBundleBuildHTTP(ctx, &EditInput[ClientworkPublishBody]{EntityID: "3", ID: id.String(), IfMatch: `"2"`, Key: "bundle-retry-stale"})
	require.NoError(t, e)
	payload, e = json.Marshal(queue.requests[3].Payload)
	require.NoError(t, e)
	stage := m.bundleFiles.Stage
	m.bundleFiles.Stage = func(ctx context.Context, p guid.ID, purpose string, r io.Reader, size int64) (*files.PreparedDeliveryBundle, error) {
		prepared, err := stage(ctx, p, purpose, r, size)
		m.bundleInputs.ReadOn = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, refs []delivery.ClientArtifactRef) (delivery.PackageInputFacts, error) {
			facts, err := packageTestInputFacts(ctx, tx, tenant, project, refs)
			facts.PortalSettings = json.RawMessage(`{"contributor_display":"ROLES"}`)
			return facts, err
		}
		return prepared, err
	}
	require.NoError(t, m.packageBundleHandler().Run(ctx, jobs.Job{ID: 504, TenantID: 1, CreatedBy: 2, Payload: payload}))
	view, e = m.packageBundleHTTP(ctx, &ObjectInput{EntityID: "3", ID: id.String()})
	require.NoError(t, e)
	require.Equal(t, "STALE", view.Body.Data.State)
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.EqualValues(t, 2, count, "failed and stale builds cannot publish partial bindings")
	for i, broken := range []func(context.Context, string, string, string, []byte) (execution.PackageEvidenceAssessment, error){nil, func(context.Context, string, string, string, []byte) (execution.PackageEvidenceAssessment, error) {
		return execution.PackageEvidenceAssessment{}, ErrUnavailable
	}} {
		m.bundleScanner.Scan = broken
		_, e = m.packageBundleBuildHTTP(ctx, &EditInput[ClientworkPublishBody]{EntityID: "3", ID: id.String(), IfMatch: `"2"`, Key: fmt.Sprintf("bundle-scan-failure-%d", i)})
		require.NoError(t, e)
		payload, e = json.Marshal(queue.requests[4+i].Payload)
		require.NoError(t, e)
		require.NoError(t, m.packageBundleHandler().Run(ctx, jobs.Job{ID: guid.ID(505 + i), TenantID: 1, CreatedBy: 2, Payload: payload}))
		view, e = m.packageBundleHTTP(ctx, &ObjectInput{EntityID: "3", ID: id.String()})
		require.NoError(t, e)
		require.Equal(t, "FAILED", view.Body.Data.State)
		require.Equal(t, "REDACTION_FAILED", view.Body.Data.ErrorCode)
	}
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestPackageBundleFingerprintBindsDecisionPortalSelectionAndNotes(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	facts, _ := packageTestInputFacts(ctx, db, 1, 3, []delivery.ClientArtifactRef{{WorkItemID: 11}})
	m.bundleInputs = delivery.PackageInputs{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, []delivery.ClientArtifactRef) (delivery.PackageInputFacts, error) {
		return facts, nil
	}}
	created, e := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "fingerprint", Body: PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
	require.NoError(t, e)
	q := query.BindDeliveryPackage(db, m.names)
	row, e := q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).First()
	require.NoError(t, e)
	var manifest packageManifest
	require.NoError(t, json.Unmarshal([]byte(row.Manifest), &manifest))
	original, e := m.packageFingerprintOn(ctx, db, row, manifest)
	require.NoError(t, e)
	facts.Items[0].AcceptedDecisionID++
	changed, e := m.packageFingerprintOn(ctx, db, row, manifest)
	require.NoError(t, e)
	require.NotEqual(t, original, changed)
	facts.Items[0].AcceptedDecisionID--
	facts.PortalSettings = json.RawMessage(`{"contributor_display":"ROLES"}`)
	changed, e = m.packageFingerprintOn(ctx, db, row, manifest)
	require.NoError(t, e)
	require.NotEqual(t, original, changed)
	m.bundleInputs.ReadOn = nil
	_, e = m.packageFingerprintOn(ctx, db, row, manifest)
	require.ErrorIs(t, e, ErrUnavailable)
}

func TestPackageBundleFailureTracksExactJobAndRollsBackTerminalOutbox(t *testing.T) {
	for _, scenario := range []string{"transient", "too_large", "scan", "stale", "superseded", "lock", "audit", "update", "publisher", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, id := releaseNoteHarness(t)
			queue := &packageTestQueue{}
			m.forecastQueue = queue
			ctx := testContext(t, 2)
			var receipt Receipt
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				receipt, err = m.queuePackageBundleOn(ctx, tx, 3, id, 1)
				return err
			}))
			require.Len(t, queue.requests, 1)
			payload, err := json.Marshal(queue.requests[0].Payload)
			require.NoError(t, err)
			var in packageBundleJobBody
			require.NoError(t, json.Unmarshal(payload, &in))
			job := receipt.Bundle.JobID
			fault := errors.New("terminal bundle persistence failed")
			cause := ErrBundleScan
			var want error
			state := "FAILED"
			code := "REDACTION_FAILED"
			switch scenario {
			case "transient":
				cause = fault
				want = fault
				state = "BUILDING"
				code = ""
			case "too_large":
				cause = ErrBundleTooLarge
				code = "TOO_LARGE"
			case "stale":
				cause = delivery.ErrResultState
				state = "STALE"
				code = ""
			case "superseded":
				job++
				state = "BUILDING"
				code = ""
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return fault }
				want = fault
				state = "BUILDING"
				code = ""
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
				want = fault
				state = "BUILDING"
				code = ""
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:bundle-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.DeliveryPackage.String() {
						tx.AddError(fault)
					}
				}))
				want = fault
				state = "BUILDING"
				code = ""
			case "publisher":
				m.events = failPublisher{}
				state = "BUILDING"
				code = ""
			case "unavailable":
				m.audit = nil
				want = ErrUnavailable
				state = "BUILDING"
				code = ""
			}
			err = m.failPackageBundle(ctx, in, job, cause)
			if scenario == "publisher" {
				require.Error(t, err)
			} else {
				require.ErrorIs(t, err, want)
			}
			require.NoError(t, db.Callback().Update().Remove("test:bundle-failure"))
			var row model.DeliveryPackage
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", id).Take(&row).Error)
			var manifest packageManifest
			require.NoError(t, json.Unmarshal([]byte(row.Manifest), &manifest))
			require.Equal(t, state, manifest.Bundle.State)
			require.Equal(t, code, manifest.Bundle.ErrorCode)
			require.Equal(t, receipt.Bundle.JobID, manifest.Bundle.JobID)
			require.Zero(t, manifest.Bundle.BundleFileID)
		})
	}
}

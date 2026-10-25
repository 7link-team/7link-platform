package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestSheetDraftAndPublicationRecheckSignerAndCanonicalSnapshot(t *testing.T) {
	for _, stage := range []string{"invalid", "draft_client_error", "draft_revoked", "draft_requirements", "draft_denied", "publish_client_error", "publish_revoked", "publish_changed", "publish_stale", "publish_denied", "sign_internal", "sign_invalid", "sign_denied"} {
		t.Run(stage, func(t *testing.T) {
			m, db, projects, clients := harness(t)
			ctx := testContext(t, 2)
			body := SheetDraft{Title: "Agreed scope", SignerID: 4, RequirementIDs: []guid.ID{10}}
			var draft SheetView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				draft, err = m.draftSheetOn(ctx, tx, 3, 0, 0, body)
				return err
			}))
			before := collaborationSnapshot(t, m, db)
			switch stage {
			case "invalid":
				body.Title = " "
			case "draft_client_error", "publish_client_error":
				clients.clientErr = errors.New("client membership unavailable")
			case "draft_revoked", "publish_revoked":
				clients.revoked = true
			case "draft_requirements":
				body.RequirementIDs = []guid.ID{99}
			case "draft_denied", "publish_denied", "sign_denied":
				projects.deny = true
			case "publish_changed":
				projects.changed = true
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				if strings.HasPrefix(stage, "publish_") {
					version := draft.Version
					if stage == "publish_stale" {
						version++
					}
					_, err := m.publishSheetOn(ctx, tx, 3, draft.ID, version)
					return err
				}
				if strings.HasPrefix(stage, "sign_") {
					actor := guid.ID(4)
					decision := "SIGN"
					if stage == "sign_internal" {
						actor = 2
					}
					if stage == "sign_invalid" {
						decision = "APPROVE"
					}
					_, err := m.signSheetOn(testContext(t, actor), tx, 3, draft.ID, draft.Version, SignoffBody{Revision: 1, ContentHash: draft.ContentHash, Decision: decision, Reason: "Reviewed"})
					return err
				}
				_, err := m.draftSheetOn(ctx, tx, 3, 0, 0, body)
				return err
			})
			require.Error(t, err)
			require.Equal(t, before, collaborationSnapshot(t, m, db))
		})
	}
}

func TestSheetRejectionPersistsDecisionWithoutSigningBaseline(t *testing.T) {
	m, db, _, _ := harness(t)
	sheet := draftAndPublish(t, m, db)
	var baselineBefore int64
	require.NoError(t, db.Table("baseline_probe").Count(&baselineBefore).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		view, err := m.signSheetOn(testContext(t, 4), tx, 3, sheet.ID, sheet.Version, SignoffBody{Revision: 1, ContentHash: sheet.ContentHash, Decision: "REJECT", Reason: "Criteria need revision"})
		require.NoError(t, err)
		require.Equal(t, "REJECTED", view.State)
		require.Equal(t, "REJECT", view.Decision.Decision)
		return nil
	}))
	var baselineAfter int64
	require.NoError(t, db.Table("baseline_probe").Count(&baselineAfter).Error)
	require.Equal(t, baselineBefore, baselineAfter)
	visible, err := m.getSheetHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: sheet.ID.String(), Revision: 1})
	require.NoError(t, err)
	require.Equal(t, "REJECTED", visible.Body.Data.State)
	_, err = m.getSheetHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: sheet.ID.String(), Revision: 2})
	require.Error(t, err)
}

type testClients struct {
	revoked                                        bool
	externalErr, clientErr, channelErr, projectErr error
	projectIDs                                     []guid.ID
}

func (c *testClients) IsClientOn(_ context.Context, _ *gorm.DB, t, p, a guid.ID) (bool, error) {
	return !c.revoked && t == 1 && p == 3 && (a == 4 || a == 5), c.clientErr
}
func (c *testClients) IsExternalOn(_ context.Context, _ *gorm.DB, _ guid.ID, a guid.ID) (bool, error) {
	return a == 4 || a == 5, c.externalErr
}

type testMembers struct{}

func (testMembers) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) { return true, nil }
func (testMembers) IsMemberActiveOn(_ context.Context, _ *gorm.DB, t, a guid.ID) (bool, error) {
	return t == 1 && a != 9, nil
}

type testProjects struct {
	changed    bool
	deny       bool
	stale      bool
	applyErr   error
	lastChange delivery.ClientScopeChange
	lastCookie string
}

func (p *testProjects) LockCancellationOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	return p.LockProjectOn(ctx, tx, tenant, project, "exception_decide")
}

func (p *testProjects) CancelProjectOn(ctx context.Context, tx *gorm.DB, tenant, project, decision guid.ID, reason, cookie, proof string) (string, error) {
	if decision <= 0 || proof == "" || cookie == "" || len([]rune(reason)) < 10 {
		return "", ErrInvalid
	}
	return "ROLE:test", p.ExceptionProjectOn(ctx, tx, tenant, project, "KILL", reason, cookie, proof)
}

func (p *testProjects) LockProjectOn(_ context.Context, _ *gorm.DB, t, project guid.ID, permission string) error {
	if p.deny || t != 1 || project != 3 {
		return ErrNotFound
	}
	return nil
}
func (p *testProjects) RequirementsOn(_ context.Context, _ *gorm.DB, t, project guid.ID, ids []guid.ID) ([]delivery.ClientRequirement, error) {
	if t != 1 || project != 3 || len(ids) != 1 || ids[0] != 10 {
		return nil, ErrNotFound
	}
	v := 1
	if p.changed {
		v = 2
	}
	return []delivery.ClientRequirement{{ID: 10, Version: v, Title: "Requirement", Description: "Client safe description", Priority: 1, Criteria: []delivery.ClientCriterion{{ID: 20, Code: "C1", Text: "Works", Required: true, Position: 0}}}}, nil
}

func (p *testProjects) LockRequirementReadOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) error {
	if e := p.LockProjectOn(ctx, tx, tenant, project, "requirement_read"); e != nil {
		return e
	}
	_, e := p.RequirementsOn(ctx, tx, tenant, project, []guid.ID{id})
	return e
}
func (p *testProjects) SignRequirementsOn(ctx context.Context, tx *gorm.DB, t, project guid.ID, in []delivery.ClientRequirement) error {
	if p.changed {
		return delivery.ErrRequirementVersion
	}
	return tx.Exec("INSERT INTO baseline_probe VALUES (?)", in[0].ID).Error
}
func (p *testProjects) AcceptedArtifactsOn(_ context.Context, _ *gorm.DB, t, project guid.ID, refs []delivery.ClientArtifactRef) ([]delivery.ClientArtifact, error) {
	if p.stale {
		return nil, delivery.ErrResultCoverage
	}
	if len(refs) < 1 || len(refs) > 100 {
		return nil, ErrInvalid
	}
	var out []delivery.ClientArtifact
	seen := map[guid.ID]bool{}
	for _, ref := range refs {
		if ref.EvidenceID < 30 || ref.EvidenceID > 32 || seen[ref.EvidenceID] {
			return nil, ErrNotFound
		}
		seen[ref.EvidenceID] = true
		version := guid.ID(40)
		if ref.EvidenceID == 31 {
			version = 41
		}
		out = append(out, delivery.ClientArtifact{Name: "report.txt", VersionID: version, SHA256: strings.Repeat("a", 64), Coverage: "COVERED", Overrides: json.RawMessage("[]")})
	}
	return out, nil
}
func (p *testProjects) ValidateWorkOn(_ context.Context, _ *gorm.DB, t, project, id guid.ID, defect bool) error {
	if t != 1 || project != 3 || (defect && id != 12) || (!defect && id != 11) {
		return ErrNotFound
	}
	return nil
}
func (p *testProjects) SettingsOn(_ context.Context, tx *gorm.DB, tenant, project guid.ID, in *delivery.ClientSettingsPatch, expected int) (delivery.ClientSettings, error) {
	if tenant != 1 || project != 3 {
		return delivery.ClientSettings{}, ErrNotFound
	}
	var v int
	if e := tx.Raw("SELECT version FROM settings_probe").Scan(&v).Error; e != nil {
		return delivery.ClientSettings{}, e
	}
	if in == nil {
		return delivery.ClientSettings{Version: v}, nil
	}
	if expected != v {
		return delivery.ClientSettings{}, delivery.ErrWorkVersion
	}
	if e := tx.Exec("UPDATE settings_probe SET version = ?", v+1).Error; e != nil {
		return delivery.ClientSettings{}, e
	}
	return delivery.ClientSettings{Description: in.Description, TargetDate: in.TargetDate, Version: v + 1}, nil
}
func (p *testProjects) ArtifactFileOn(_ context.Context, _ *gorm.DB, _, _ guid.ID, ref delivery.ClientArtifactRef) (files.PinnedCandidate, error) {
	version := guid.ID(40)
	if ref.EvidenceID == 31 {
		version = 41
	}
	return files.PinnedCandidate{PinnedResource: files.PinnedResource{SpaceID: 50, FileID: 60, VersionID: version, SHA256: strings.Repeat("a", 64), Name: "report.txt", MIME: "text/plain", Size: 4}, NodeVersion: 1}, nil
}

type testFiles struct{}

func (testFiles) InspectOn(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
	return (&testProjects{}).ArtifactFileOn(ctx, tx, 1, id, delivery.ClientArtifactRef{})
}
func (testFiles) PinOn(_ context.Context, _ *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
	return files.PinnedResource{BindingID: in.ID, VersionID: *in.VersionID, SHA256: strings.Repeat("a", 64)}, nil
}
func (testFiles) OpenOn(context.Context, *gorm.DB, guid.ID) (files.PinnedResourceRead, error) {
	return files.PinnedResourceRead{PinnedResource: files.PinnedResource{SHA256: strings.Repeat("a", 64), Name: "report.txt", MIME: "text/plain", Size: 4}, Reader: io.NopCloser(strings.NewReader("test"))}, nil
}
func testContext(t *testing.T, a guid.ID) context.Context {
	t.Helper()
	return authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: a, TenantID: 1, EntityID: 3, SubjectType: authn.SubjectTypePrincipal})
}
func harness(t *testing.T) (*Module, *gorm.DB, *testProjects, *testClients) {
	t.Helper()
	router, e := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "client.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("g_"), appenv.WithDatabase(router))
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}})
	m.packageSources.ReadOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, refs []delivery.ClientArtifactRef) ([]delivery.PackageSource, error) {
		facts := make([]delivery.PackageSource, len(refs))
		for i, ref := range refs {
			facts[i].WorkItemID = ref.WorkItemID
		}
		return facts, nil // These package-only fixtures have no Intake origin.
	}
	m.packageRecipient = func(_ context.Context, _ *gorm.DB, tenant, project, recipient guid.ID) error {
		if tenant != 1 || project != 3 || (recipient != 4 && recipient != 5) {
			return ErrNotFound
		}
		return nil
	}
	m.acceptanceProof = func(_ context.Context, _ *gorm.DB, _ guid.ID, _ string, proof, purpose, target string) error {
		if proof != "test-publish-proof" || purpose != "delivery_send" || target == "" {
			return authn.ErrInvalidCredentials
		}
		return nil
	}
	m.clientSigners = delivery.ClientSignerFacts{ContactOn: func(_ context.Context, _ *gorm.DB, tenant, project, principal guid.ID) (guid.ID, error) {
		if tenant == 1 && project == 3 && (principal == 4 || principal == 5) {
			return principal, nil
		}
		return 0, ErrForbidden
	}}
	m.packageCommentContacts = delivery.ClientCommentContacts{ContactOn: m.clientSigners.ContactOn}
	db := router.Write()
	m.ids, e = guid.New(1)
	require.NoError(t, e)
	projects, clients := &testProjects{}, &testClients{}
	m.projects = projects
	m.bundleInputs = delivery.PackageInputs{ReadOn: packageTestInputFacts}
	m.files = files.ClientCollaborationResources{InspectOn: testFiles{}.InspectOn, PinOn: testFiles{}.PinOn, OpenOn: testFiles{}.OpenOn}
	m.bundleScanner = execution.PackageEvidenceScanner{Scan: execution.ScanPackageEvidence}
	m.clients = clients
	m.members = testMembers{}
	m.principals = identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }, ActiveHumanByID: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id != 7, nil
	}}
	require.NoError(t, db.Exec("CREATE TABLE audit_probe(kind TEXT)").Error)
	require.NoError(t, db.Exec("CREATE TABLE baseline_probe(id BIGINT)").Error)
	require.NoError(t, db.Exec("CREATE TABLE settings_probe(version INTEGER)").Error)
	require.NoError(t, db.Exec("INSERT INTO settings_probe VALUES (1)").Error)
	m.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		return tx.Exec("INSERT INTO audit_probe VALUES (?)", e.EventType).Error
	}
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	locks := dlock.NewModule(rt)
	require.NoError(t, locks.ProvideServices(services))
	publisher := events.NewModule(rt)
	require.NoError(t, publisher.ProvideServices(services))
	replay := idempotency.NewModule(rt)
	require.NoError(t, replay.ProvideServices(services))
	require.NoError(t, services.Seal())
	require.NoError(t, publisher.InjectServices(services))
	require.NoError(t, replay.InjectServices(services))
	catalog, e := module.Resolve[events.Catalog](services)
	require.NoError(t, e)
	for _, kind := range []string{"package_bundle_requested", "package_bundle_built", "package_bundle_failed", "package_bundle_stale", "package_sent", "package_send_failed", "release_note_requested", "release_note_drafted", "release_note_adopted", "intake_suggested", "intake_thread_linked", "intake_thread_replied", "dispatch_starved", "rework_disposed", "clarification_drafted", "clarification_expired", "clarification_reminder", "hidden_verified", "clarification_requested", "clarification_answered", "sheet_drafted", "sheet_published", "sheet_decided", "change_requested", "change_decided", "issue_created", "issue_updated", "package_drafted", "package_published", "package_decided", "package_archived", "preview_created", "annotation_created", "test_report_requested", "test_report_built", "test_report_failed", "test_case_written", "test_plan_created", "test_run_recorded", "settings_changed", "tolerance_changed", "tolerance_forecasted", "exception_raised", "exception_reported", "exception_decided", "preview_share_requested", "preview_share_decided", "preview_share_ended", "dispatch_proposed", "dispatch_decided", "dispatch_expired"} {
		require.NoError(t, catalog.AddTypes(Name, events.TypeSpec{Name: "clientwork." + kind, SchemaVersion: 1, SubjectKind: Name, Realtime: events.RealtimeNone}))
	}
	require.NoError(t, catalog.AddTypes(Name, events.TypeSpec{Name: "clientwork.clarification_cancelled", SchemaVersion: 1, SubjectKind: Name, Realtime: events.RealtimeNone})) //nolint:misspell // Frozen event name.
	require.NoError(t, publisher.Initialize(t.Context()))
	require.NoError(t, platformi18n.NewModule(rt, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, replay.Initialize(t.Context()))
	m.events, e = module.Resolve[events.Publisher](services)
	require.NoError(t, e)
	m.replay, e = module.Resolve[idempotency.Store](services)
	require.NoError(t, e)
	raw, e := db.DB()
	require.NoError(t, e)
	for _, source := range []goosex.Source{m.MigrationSource(), locks.MigrationSource(), publisher.MigrationSource(), replay.MigrationSource()} {
		source.Prefix = "g_"
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	}
	m.acceptanceProjects = delivery.AcceptanceReminderProjects{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (bool, error) { return true, nil }}
	return m, db, projects, clients
}
func draftAndPublish(t *testing.T, m *Module, db *gorm.DB) SheetView {
	t.Helper()
	var v SheetView
	ctx := testContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.draftSheetOn(ctx, tx, 3, 0, 0, SheetDraft{Title: "Scope", RequirementIDs: []guid.ID{10}, SignerID: 4, Milestones: []string{"Milestone"}})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.publishSheetOn(ctx, tx, 3, v.ID, v.Version); return e }))
	return v
}
func TestSheetExactSignoffRejectVersionHistoryAndProjection(t *testing.T) {
	m, db, _, clients := harness(t)
	v := draftAndPublish(t, m, db)
	ctx := testContext(t, 4)
	for _, in := range []SignoffBody{{Revision: 1, ContentHash: strings.Repeat("b", 64), Decision: "SIGN", Reason: "Reviewed"}, {Revision: 2, ContentHash: v.ContentHash, Decision: "SIGN", Reason: "Reviewed"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.signSheetOn(ctx, tx, 3, v.ID, v.Version, in); return e }), ErrConflict)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.signSheetOn(testContext(t, 5), tx, 3, v.ID, v.Version, SignoffBody{1, v.ContentHash, "SIGN", "Reviewed"})
		return e
	}), ErrNotFound)
	var signed SheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		signed, e = m.signSheetOn(ctx, tx, 3, v.ID, v.Version, SignoffBody{1, v.ContentHash, "SIGN", "Reviewed"})
		return e
	}))
	require.Equal(t, "SIGNED", signed.State)
	require.NotNil(t, signed.Decision)
	var count int64
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	var draft SheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		draft, e = m.draftSheetOn(testContext(t, 2), tx, 3, v.ID, signed.Version, SheetDraft{Title: "PRIVATE DRAFT TITLE", RequirementIDs: []guid.ID{10}, SignerID: 4})
		return e
	}))
	require.Equal(t, 2, draft.Revision)
	public, e := m.getSheetHTTP(ctx, &ObjectInput{EntityID: "3", ID: v.ID.String()})
	require.NoError(t, e)
	require.Equal(t, "Scope", public.Body.Data.Title)
	require.Equal(t, 1, public.Body.Data.Revision)
	require.Equal(t, "SIGNED", public.Body.Data.State)
	clients.revoked = true
	_, e = m.getSheetHTTP(ctx, &ObjectInput{EntityID: "3", ID: v.ID.String()})
	require.Error(t, e)
}
func TestSignoffAuditFailureRollsBackBaselineDecisionAndSheet(t *testing.T) {
	m, db, _, _ := harness(t)
	v := draftAndPublish(t, m, db)
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit down") }
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.signSheetOn(testContext(t, 4), tx, 3, v.ID, v.Version, SignoffBody{1, v.ContentHash, "SIGN", "Reviewed"})
		return e
	}))
	var count int64
	for _, table := range []string{"baseline_probe", "g_clientwork_signoffs"} {
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Zero(t, count)
	}
	q := query.BindSheets(db, m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "AWAITING_SIGNOFF", row.State)
}
func TestChangedCanonicalRequirementAndHumanBoundary(t *testing.T) {
	m, db, p, _ := harness(t)
	v := draftAndPublish(t, m, db)
	p.changed = true
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.signSheetOn(testContext(t, 4), tx, 3, v.ID, v.Version, SignoffBody{1, v.ContentHash, "SIGN", "Reviewed"})
		return e
	}), delivery.ErrRequirementVersion)
	for _, actor := range []guid.ID{7, 9} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.authorize(testContext(t, actor), tx, 3, "requirement_write", false)
			return e
		}))
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.authorize(testContext(t, 4), tx, 3, "requirement_write", false)
		return e
	}), ErrNotFound)
}

func (c *testClients) ProjectsOn(_ context.Context, _ *gorm.DB, _ guid.ID, _ guid.ID, after guid.ID, limit int) ([]guid.ID, error) {
	ids := c.projectIDs
	if ids == nil {
		ids = []guid.ID{3}
	}
	out := make([]guid.ID, 0)
	for _, id := range ids {
		if id > after {
			out = append(out, id)
			if len(out) == limit {
				break
			}
		}
	}
	return out, c.projectErr
}
func (p *testProjects) ClientSummaryOn(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ClientProjectSummary, error) {
	return delivery.ClientProjectSummary{ID: 3, Name: "Project", Type: "project", Status: "active"}, nil
}

func (c *testClients) ChannelOn(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, bool, error) {
	return 1, !c.revoked, c.channelErr
}

func (p *testProjects) ApplyClientChangeOn(_ context.Context, tx *gorm.DB, tenant, project, requirement guid.ID, expected int, cookie string, in delivery.ClientScopeChange) (guid.ID, error) {
	p.lastChange, p.lastCookie = in, cookie
	if p.applyErr != nil {
		return 0, p.applyErr
	}
	if tenant != 1 || project != 3 || requirement != 10 || expected != 1 {
		return 0, delivery.ErrRequirementVersion
	}
	return 88, tx.Exec("INSERT INTO baseline_probe VALUES (?)", 88).Error
}

func (p *testProjects) ExceptionProjectOn(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, string, string) error {
	return nil
}

func (p *testProjects) ProgressArtifactOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ref delivery.ClientArtifactRef) (delivery.ClientArtifact, files.PinnedCandidate, error) {
	items, e := p.AcceptedArtifactsOn(ctx, tx, tenant, project, []delivery.ClientArtifactRef{ref})
	if e != nil {
		return delivery.ClientArtifact{}, files.PinnedCandidate{}, e
	}
	file, e := p.ArtifactFileOn(ctx, tx, tenant, project, ref)
	return items[0], file, e
}

func (p *testProjects) ApprovePreviewShareOn(context.Context, *gorm.DB, guid.ID, guid.ID) error {
	return nil
}

func (p *testProjects) SourceArtifactOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ref delivery.ClientSourceRef) (delivery.ClientArtifact, files.PinnedCandidate, error) {
	if e := p.ValidateWorkOn(ctx, tx, tenant, project, ref.WorkItemID, false); e != nil {
		return delivery.ClientArtifact{}, files.PinnedCandidate{}, e
	}
	if p.stale || ref.FileID != 60 || ref.VersionID != 40 || ref.SHA256 != strings.Repeat("a", 64) {
		return delivery.ClientArtifact{}, files.PinnedCandidate{}, delivery.ErrResultCoverage
	}
	file, e := p.ArtifactFileOn(ctx, tx, tenant, project, delivery.ClientArtifactRef{})
	return delivery.ClientArtifact{Name: file.Name, VersionID: file.VersionID, SHA256: file.SHA256, Coverage: "UNVERIFIED", Overrides: json.RawMessage("[]")}, file, e
}

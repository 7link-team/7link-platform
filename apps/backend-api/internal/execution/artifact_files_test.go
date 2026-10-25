package execution

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"7link.com/platform/internal/files"
	filesquery "7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func bindRealArtifactFiles(t *testing.T, f *executionFixture, allowRead ...bool) (*files.ExecutionArtifactFiles, files.PinnedResources, *files.ResourceBindings, files.Driver) {
	t.Helper()
	fm := files.NewModule(f.m.runtime, files.Config{LocalRoot: t.TempDir()})
	s := module.NewServices()
	require.NoError(t, fm.ProvideServices(s))
	require.NoError(t, module.Provide[*authz.Registrar](s, authz.NewUnboundRegistrar(authz.MustRegistry())))
	require.NoError(t, module.Provide[guid.Source](s, f.m.ids))
	require.NoError(t, module.Provide[audit.Appender](s, f.m.audit))
	require.NoError(t, module.Provide[core.EntityConstraintOn](s, func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		if len(allowRead) > 0 && allowRead[0] {
			return authz.DataConstraint{AllowAll: true, Revision: 1}, nil
		}
		return authz.DataConstraint{}, nil
	}))
	require.NoError(t, module.Provide[core.TenantConstraintOn](s, func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}))
	require.NoError(t, module.Provide[core.PlatformPermissionOn](s, func(context.Context, *gorm.DB, string, guid.ID) (bool, error) { return false, nil }))
	require.NoError(t, module.Provide[core.AuthorizationSubjectsOn](s, func(_ context.Context, _ *gorm.DB, _, _ guid.ID, principal guid.ID) (core.AuthorizationSubjects, error) {
		if len(allowRead) > 0 && allowRead[0] {
			return core.AuthorizationSubjects{PrincipalID: principal, Revision: 1}, nil
		}
		return core.AuthorizationSubjects{}, nil
	}))
	require.NoError(t, module.Provide[core.AuthorizationReferencesOn](s, func(_ context.Context, _ *gorm.DB, tenant guid.ID, r core.AuthorizationReferences) (bool, error) {
		return tenant == 7 && r.EntityID == 9, nil
	}))
	require.NoError(t, module.Provide[core.EntityOwnerResolver](s, func(_ context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{TenantID: 7, EntityID: id}, id == 9, nil
	}))
	require.NoError(t, module.Provide[core.DepartmentOwnerResolver](s, func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, nil
	}))
	require.NoError(t, module.Provide[*core.SystemMenus](s, &core.SystemMenus{}))
	require.NoError(t, s.Seal())
	require.NoError(t, fm.InjectServices(s))
	raw, err := f.db.DB()
	require.NoError(t, err)
	source := fm.MigrationSource()
	source.Prefix = "ex_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	store, err := module.Resolve[*files.ExecutionArtifactFiles](s)
	require.NoError(t, err)
	require.NoError(t, store.BindAuthorization(f.m.authorizeArtifactOn))
	f.m.artifacts = artifactFiles{stage: store.Stage, attach: store.AttachOn, references: store.WithReferencesOn}
	pinned, err := module.Resolve[files.PinnedResources](s)
	require.NoError(t, err)
	bindings, err := module.Resolve[*files.ResourceBindings](s)
	require.NoError(t, err)
	driver, err := module.Resolve[files.Driver](s)
	require.NoError(t, err)
	return store, pinned, bindings, driver
}

func TestRunnerArtifactMetadataValidationAndDuplicateConflict(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	valid := ArtifactUpload{Name: "report.txt", Data: []byte("immutable report bytes"), Coverage: "COVERED"}
	for _, mutate := range []func(*ArtifactUpload){
		func(v *ArtifactUpload) { v.Coverage = "UNKNOWN" },
		func(v *ArtifactUpload) { v.Coverage = "NOT_APPLICABLE" },
		func(v *ArtifactUpload) { v.Note = strings.Repeat("x", 2001) },
		func(v *ArtifactUpload) { v.Name = "../report.txt" },
		func(v *ArtifactUpload) { v.Data = make([]byte, 65537) },
	} {
		bad := valid
		mutate(&bad)
		_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Report", Upload: &bad})
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "LOG", Content: "log", Upload: &valid})
	require.ErrorIs(t, err, ErrInvalidArgument)
	in := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Report", Upload: &valid}
	_, err = f.receipt(v.ID, in)
	require.NoError(t, err)
	in.Seq = 3
	changed := valid
	changed.Coverage = "PARTIAL"
	in.Upload = &changed
	_, err = f.receipt(v.ID, in)
	require.ErrorIs(t, err, ErrConflict)
	in.Upload = &valid
	_, err = f.receipt(v.ID, in)
	require.NoError(t, err)
}

func TestRunnerArtifactIngestionPinsRealBytesAndCompletesWithoutFileACL(t *testing.T) {
	f := executionHarness(t)
	_, pinned, bindings, driver := bindRealArtifactFiles(t, f)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "LOG", Content: "real runner output"})
	require.NoError(t, err)
	upload := ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Runner report", Upload: &ArtifactUpload{Name: "report.txt", Data: []byte("actual runner artifact bytes"), Coverage: "COVERED"}}
	_, err = f.receipt(v.ID, upload)
	require.NoError(t, err)
	q := filesquery.Bind(f.db, "ex_")
	source, err := q.ResourceBinding.WithContext(t.Context()).Where(q.ResourceBinding.Purpose.Eq("execution_evidence")).First()
	require.NoError(t, err)
	version, err := q.Version.WithContext(t.Context()).Where(q.Version.ID.Eq(*source.VersionID)).First()
	require.NoError(t, err)
	object, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(version.ObjectID)).First()
	require.NoError(t, err)
	r, err := driver.Open(t.Context(), object.StorageKey)
	require.NoError(t, err)
	body, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, upload.Upload.Data, body)
	require.Equal(t, hash(body), object.SHA256)
	count, err := q.ResourceBinding.WithContext(t.Context()).Where(q.ResourceBinding.Purpose.Eq("execution_log")).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	before, err := q.AccountPreparation.WithContext(t.Context()).Count()
	require.NoError(t, err)
	replay, err := f.receipt(v.ID, upload)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	after, err := q.AccountPreparation.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Equal(t, before, after, "replay must not stage another object")
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := pinned.InspectOn(f.runner, tx, source.FileID)
		require.Error(t, e, "Machine has no generic file ACL")
		return nil
	}))
	require.NoError(t, bindings.RegisterPurpose(files.ResourcePurpose{Name: "delivery.result_evidence", ResourceKind: "delivery_result", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(ctx context.Context, _ *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
		if id != v.ID || !files.ExecutionReferenceAuthorized(ctx, id, owner.ID) {
			return ErrForbidden
		}
		return nil
	}}))
	var resultPin files.PinnedResource
	f.m.work.FinishedOn = func(ctx context.Context, tx *gorm.DB, l WorkLink, result WorkResult) error {
		require.Equal(t, guid.ID(101), authn.PrincipalIDFromContext(ctx), "Machine identity must be retained")
		for _, e := range result.Evidence {
			if e.Artifact == nil {
				continue
			}
			a := e.Artifact
			require.Equal(t, source.ID, a.BindingID)
			var e2 error
			resultPin, e2 = pinned.(files.ExecutionArtifactReferences).PinReferenceOn(ctx, tx, a.BindingID, l.ExecutionID, files.ResourceBindingInput{ID: 9000001, SpaceID: a.SpaceID, FileID: a.FileID, VersionID: &a.VersionID, Purpose: "delivery.result_evidence", ResourceKind: "delivery_result", ResourceID: l.ExecutionID, Slot: "evidence.1", ActorID: 101})
			if e2 != nil {
				return e2
			}
		}
		return nil
	}
	head := strings.Repeat("a", 40)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Head", Content: head})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 5, Kind: "COMPLETED", HeadCommit: head})
	require.NoError(t, err)
	require.Equal(t, "SUCCEEDED", f.read(t, v.ID).State)
	require.Equal(t, object.SHA256, resultPin.SHA256)
	require.Equal(t, *source.VersionID, resultPin.VersionID)
}

func TestRunnerArtifactFenceAndRollbackRetainTrackedBytes(t *testing.T) {
	f := executionHarness(t)
	_, _, _, _ = bindRealArtifactFiles(t, f)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	in := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Report", Upload: &ArtifactUpload{Name: "report.txt", Data: []byte("private bytes"), Coverage: "COVERED"}}
	prepared, err := f.m.PrepareReceipt(f.runner, 7, v.ID, f.fence, in)
	require.NoError(t, err)
	f.sessionValid = false
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.ReceiptOn(f.runner, tx, 7, v.ID, f.fence, prepared); return e }))
	q := filesquery.Bind(f.db, "ex_")
	count, err := q.ResourceBinding.WithContext(t.Context()).Where(q.ResourceBinding.Purpose.Eq("execution_evidence")).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	f.sessionValid = true
	f.pub.fail = true
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.ReceiptOn(f.runner, tx, 7, v.ID, f.fence, prepared); return e }))
	count, err = q.ResourceBinding.WithContext(t.Context()).Where(q.ResourceBinding.Purpose.Eq("execution_evidence")).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	intents, err := q.AccountPreparation.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, intents, 1)
	require.Equal(t, "ready", intents[0].State)
	require.EqualValues(t, 1, f.read(t, v.ID).LastSeq)
	f.pub.fail = false
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.ReceiptOn(f.runner, tx, 7, v.ID, f.fence, prepared); return e }))
	evidence := queryEvidenceManifest(t, f, v.ID)
	require.Positive(t, int64(evidence.BindingID))
	require.Equal(t, hash(in.Upload.Data), evidence.SHA256)
}

func queryEvidenceManifest(t *testing.T, f *executionFixture, id guid.ID) ArtifactFact {
	t.Helper()
	var row struct{ Content string }
	require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Select("content").Where("execution_id = ?", id).Take(&row).Error)
	var out ArtifactFact
	require.NoError(t, json.Unmarshal([]byte(row.Content), &out))
	return out
}

func TestInjectArtifactFilesRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.injectArtifactFiles(module.NewServices()), module.ErrServicesProviding)
}

func TestPrepareReceiptRequiresExistingExecutionAndStorage(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	in := ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "LOG", Content: "actual runner log"}
	_, err := f.m.PrepareReceipt(f.runner, 0, v.ID, f.fence, in)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = f.m.PrepareReceipt(f.runner, 7, v.ID+1000, f.fence, in)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	f.m.artifacts.stage = nil
	_, err = f.m.PrepareReceipt(f.runner, 7, v.ID, f.fence, in)
	require.ErrorIs(t, err, ErrCapabilityUnavailable)
	f.m.runtime = nil
	_, err = f.m.PrepareReceipt(f.runner, 7, v.ID, f.fence, in)
	require.ErrorIs(t, err, ErrUnavailable)
}

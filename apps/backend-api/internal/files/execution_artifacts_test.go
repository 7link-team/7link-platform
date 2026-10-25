package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func delegatedArtifactFixture(t *testing.T) (operationServiceHarness, *ExecutionArtifactFiles, ExecutionArtifactAuthority, *bool) {
	h, p, _, _ := pinnedFixture(t)
	m := p.module
	m.runtime, m.accounts, m.ids, m.appender = h.service.runtime, h.service.objects, h.service.ids, h.service.appender
	m.references = func(_ context.Context, _ *gorm.DB, tenant guid.ID, r core.AuthorizationReferences) (bool, error) {
		return tenant == 90 && r.EntityID == 80, nil
	}
	store := &ExecutionArtifactFiles{module: m}
	m.executionArtifacts = store
	allowed := true
	a := ExecutionArtifactAuthority{TenantID: 90, EntityID: 80, ExecutionID: 700, MachineID: 1, SessionID: 2, ConnEpoch: 3, DispatchEpoch: 4, Seq: 5}
	require.NoError(t, store.BindAuthorization(func(ctx context.Context, tx *gorm.DB, got ExecutionArtifactAuthority, _ string, id guid.ID, owner ResourceOwner) error {
		if !allowed || authn.PrincipalIDFromContext(ctx) != 70 || got != a || owner.Scope != "entity" || owner.ID != 80 || (id != 0 && id != 9001) {
			return errOperationForbidden
		}
		_, active := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, active)
		return nil
	}))
	return h, store, a, &allowed
}

func TestExecutionArtifactStagingBindingAndExactReference(t *testing.T) {
	h, p, a, allowed := delegatedArtifactFixture(t)
	content := "actual immutable runner artifact"
	prepared, err := p.Stage(h.ctx, a, "execution_evidence", "report.txt", "receipt.4.5", strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)
	q := query.Bind(h.db, h.tree.prefix)
	intent, err := q.AccountPreparation.WithContext(t.Context()).Where(q.AccountPreparation.ID.Eq(prepared.object.object.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ready", intent.State)
	var pin PinnedResource
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { var e error; pin, e = p.AttachOn(h.ctx, tx, prepared, 9001); return e }))
	digest := sha256.Sum256([]byte(content))
	require.Equal(t, hex.EncodeToString(digest[:]), pin.SHA256)
	require.Positive(t, int64(pin.BindingID))
	binding, err := q.ResourceBinding.WithContext(t.Context()).Where(q.ResourceBinding.ID.Eq(pin.BindingID)).First()
	require.NoError(t, err)
	require.Equal(t, "execution_evidence", binding.Purpose)
	require.Equal(t, guid.ID(9001), binding.ResourceID)
	require.Equal(t, pin.VersionID, *binding.VersionID)
	obj, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(prepared.object.object.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "verified", obj.State)
	r, err := p.module.driver.Open(h.ctx, obj.StorageKey)
	require.NoError(t, err)
	body, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, content, string(body))
	require.NoError(t, p.module.bindings.RegisterPurpose(ResourcePurpose{Name: "delivery.result_evidence", ResourceKind: "delivery_result", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(ctx context.Context, _ *gorm.DB, id guid.ID, owner ResourceOwner) error {
		if id != 700 || !ExecutionReferenceAuthorized(ctx, id, owner.ID) {
			return errOperationForbidden
		}
		return nil
	}}))
	target := ResourceBindingInput{ID: 9005, SpaceID: pin.SpaceID, FileID: pin.FileID, VersionID: &pin.VersionID, Purpose: "delivery.result_evidence", ResourceKind: "delivery_result", ResourceID: 700, Slot: "evidence.1", ActorID: 70}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := p.PinReferenceOn(h.ctx, tx, pin.BindingID, 700, target)
		require.ErrorIs(t, e, errOperationForbidden)
		return p.WithReferencesOn(h.ctx, tx, a, func(scoped context.Context) error {
			_, e := p.PinReferenceOn(scoped, tx, 999, 700, target)
			require.ErrorIs(t, e, gorm.ErrRecordNotFound)
			*allowed = false
			_, e = p.PinReferenceOn(scoped, tx, pin.BindingID, 700, target)
			require.ErrorIs(t, e, errOperationForbidden)
			*allowed = true
			wrong := target
			wrong.ResourceID = 701
			_, e = p.PinReferenceOn(scoped, tx, pin.BindingID, 700, wrong)
			require.ErrorIs(t, e, errOperationForbidden)
			wrong = target
			wrong.FileID++
			_, e = p.PinReferenceOn(scoped, tx, pin.BindingID, 700, wrong)
			require.ErrorIs(t, e, errOperationForbidden)
			attached, e := (pinnedResources{module: p.module}).PinReferenceOn(scoped, tx, pin.BindingID, 700, target)
			require.NoError(t, e)
			require.Equal(t, pin.SHA256, attached.SHA256)
			require.Equal(t, pin.VersionID, attached.VersionID)
			return nil
		})
	}))
	*allowed = false
	_, err = p.Stage(h.ctx, a, "execution_log", "runner.log", "receipt.4.6", strings.NewReader("x"), 1)
	require.ErrorIs(t, err, errOperationForbidden)
}

func TestExecutionArtifactRollbackLeavesTrackedPreparation(t *testing.T) {
	h, p, a, allowed := delegatedArtifactFixture(t)
	prepared, err := p.Stage(h.ctx, a, "execution_evidence", "report.txt", "receipt.4.5", strings.NewReader("abc"), 3)
	require.NoError(t, err)
	injected := errors.New("later receipt failure")
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, e := p.AttachOn(h.ctx, tx, prepared, 9001)
		require.NoError(t, e)
		return injected
	})
	require.ErrorIs(t, err, injected)
	q := query.Bind(h.db, h.tree.prefix)
	intent, err := q.AccountPreparation.WithContext(t.Context()).Where(q.AccountPreparation.ID.Eq(prepared.object.object.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ready", intent.State)
	count, err := q.ResourceBinding.WithContext(t.Context()).Where(q.ResourceBinding.Purpose.Eq("execution_evidence")).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	*allowed = false
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := p.AttachOn(h.ctx, tx, prepared, 9001); return e }), errOperationForbidden)
	// Existing cleanup owns staged bytes after receipt rollback.
	require.True(t, time.UnixMilli(intent.ExpiresAt).After(time.Now()))
}

func TestExecutionArtifactStageRejectsInvalidRequestsWithoutPreparingBytes(t *testing.T) {
	h, p, a, _ := delegatedArtifactFixture(t)
	for _, tc := range []struct {
		name, purpose, slot string
		size                int64
		source              io.Reader
		ctx                 context.Context
		want                error
	}{
		{"../escape", "execution_evidence", "receipt.1", 1, strings.NewReader("x"), h.ctx, errOperationInput},
		{"report.txt", "unknown", "receipt.1", 1, strings.NewReader("x"), h.ctx, errResourcePurpose},
		{"report.txt", "execution_evidence", "bad slot", 1, strings.NewReader("x"), h.ctx, errOperationInput},
		{"report.txt", "execution_evidence", "receipt.1", 65537, strings.NewReader("x"), h.ctx, errOperationInput},
		{"report.txt", "execution_evidence", "receipt.1", -1, strings.NewReader("x"), h.ctx, errOperationInput},
		{"report.txt", "execution_evidence", "receipt.1", 1, nil, h.ctx, errOperationInput},
		{"report.txt", "execution_evidence", "receipt.1", 1, strings.NewReader("x"), t.Context(), errOperationForbidden},
	} {
		prepared, err := p.Stage(tc.ctx, a, tc.purpose, tc.name, tc.slot, tc.source, tc.size)
		require.ErrorIs(t, err, tc.want)
		require.Nil(t, prepared)
	}
	q := query.Bind(h.db, h.tree.prefix)
	count, err := q.AccountPreparation.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	p.module.ids = &failingIDs{}
	prepared, err := p.Stage(h.ctx, a, "execution_evidence", "report.txt", "receipt.1", strings.NewReader("x"), 1)
	require.ErrorIs(t, err, ErrUnsupported)
	require.Nil(t, prepared)
	count, err = q.AccountPreparation.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestExecutionArtifactReferencesRequireTransactionAndCurrentAuthority(t *testing.T) {
	h, p, a, allowed := delegatedArtifactFixture(t)
	called := false
	run := func(context.Context) error { called = true; return nil }
	require.ErrorIs(t, p.WithReferencesOn(h.ctx, h.db, a, run), ErrAccountTransaction)
	require.False(t, called)
	require.ErrorIs(t, p.WithReferencesOn(h.ctx, nil, a, run), ErrAccountTransaction)
	require.False(t, called)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, p.WithReferencesOn(h.ctx, tx, a, nil), errOperationInput)
		*allowed = false
		require.ErrorIs(t, p.WithReferencesOn(h.ctx, tx, a, run), errOperationForbidden)
		require.False(t, called)
		_, err := p.AttachOn(h.ctx, tx, nil, 9001)
		require.ErrorIs(t, err, errOperationForbidden)
		return nil
	}))
	require.ErrorIs(t, p.BindAuthorization(p.authorize), errOperationUnavailable)
	duplicate := &ExecutionArtifactFiles{module: p.module}
	require.Error(t, duplicate.BindAuthorization(p.authorize))
	_, err := p.PinReferenceOn(h.ctx, h.db, 1, 700, ResourceBindingInput{})
	require.ErrorIs(t, err, ErrAccountTransaction)
	unavailable := pinnedResources{}
	_, err = unavailable.PinReferenceOn(h.ctx, h.db, 1, 700, ResourceBindingInput{})
	require.ErrorIs(t, err, errOperationUnavailable)
	p.module.appender = nil
	_, err = p.Stage(h.ctx, a, "execution_evidence", "report.txt", "receipt.1", strings.NewReader("x"), 1)
	require.ErrorIs(t, err, errOperationUnavailable)
	var absent *ExecutionArtifactFiles
	_, err = absent.Stage(h.ctx, a, "execution_evidence", "report.txt", "receipt.1", strings.NewReader("x"), 1)
	require.ErrorIs(t, err, errOperationUnavailable)
}

func TestExecutionArtifactAttachFailureRollsBackFilesAndPreservesPreparation(t *testing.T) {
	for _, stage := range []string{"version", "binding", "audit", "object", "root"} {
		t.Run(stage, func(t *testing.T) {
			h, p, a, _ := delegatedArtifactFixture(t)
			prepared, err := p.Stage(h.ctx, a, "execution_evidence", "report.txt", "receipt.1", strings.NewReader("abc"), 3)
			require.NoError(t, err)
			failure := errors.New("artifact attachment persistence failed")
			fired := false
			if stage == "audit" {
				p.module.appender = func(context.Context, *gorm.DB, audit.DomainEvent) error { fired = true; return failure }
			} else if stage == "root" {
				table := query.BindNames(h.tree.prefix).Spaces.String()
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("artifact_root_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						fired = true
						_ = tx.AddError(failure)
					}
				}))
			} else if stage == "object" {
				table := query.BindNames(h.tree.prefix).Objects.String()
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("artifact_object_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						fired = true
						_ = tx.AddError(failure)
					}
				}))
			} else {
				names := query.BindNames(h.tree.prefix)
				table := names.Versions.String()
				if stage == "binding" {
					table = names.ResourceBindings.String()
				}
				require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("artifact_attach_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						fired = true
						_ = tx.AddError(failure)
					}
				}))
			}
			err = h.db.Transaction(func(tx *gorm.DB) error { _, e := p.AttachOn(h.ctx, tx, prepared, 9001); return e })
			require.True(t, fired)
			require.ErrorIs(t, err, failure)
			q := query.Bind(h.db, h.tree.prefix)
			count, err := q.ResourceBinding.WithContext(t.Context()).Where(q.ResourceBinding.Purpose.Eq("execution_evidence")).Count()
			require.NoError(t, err)
			require.Zero(t, count)
			intent, err := q.AccountPreparation.WithContext(t.Context()).Where(q.AccountPreparation.ID.Eq(prepared.object.object.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "ready", intent.State)
			object, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(prepared.object.object.ID)).First()
			require.NoError(t, err)
			require.NotEqual(t, "verified", object.State)
		})
	}
}

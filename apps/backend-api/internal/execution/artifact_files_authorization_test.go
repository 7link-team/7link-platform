package execution

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestArtifactAttachmentFailureRollsBackSequenceAndStoredFacts(t *testing.T) {
	for _, kind := range []string{"LOG", "EVIDENCE"} {
		for _, missing := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/failure", true: "/missing"}[missing], func(t *testing.T) {
				f := executionHarness(t)
				v := f.create(t)
				f.pull(t, v.ID)
				_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
				require.NoError(t, err)
				before := f.read(t, v.ID)
				failure := errors.New("artifact storage failed")
				f.m.artifacts.attach = func(context.Context, *gorm.DB, *files.PreparedExecutionArtifact, guid.ID) (files.PinnedResource, error) {
					return files.PinnedResource{}, failure
				}
				want := failure
				if missing {
					f.m.artifacts.attach = nil
					want = ErrCapabilityUnavailable
				}
				_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: kind, Content: "actual bytes", EvidenceKind: "OBSERVATION", EvidenceTitle: "Observed result"})
				require.ErrorIs(t, err, want)
				require.Equal(t, before, f.read(t, v.ID))
				for _, table := range []string{f.m.names.Logs.String(), f.m.names.Evidence.String()} {
					var count int64
					require.NoError(t, f.db.Table(table).Count(&count).Error)
					require.Zero(t, count, table)
				}
				var count int64
				require.NoError(t, f.db.Table(f.m.names.Receipts.String()).Count(&count).Error)
				require.EqualValues(t, 1, count)
			})
		}
	}
}

func TestArtifactHumanReadsRequireCurrentExecutionAuthority(t *testing.T) {
	for _, purpose := range []string{"execution_log", "execution_evidence"} {
		t.Run(purpose, func(t *testing.T) {
			f := executionHarness(t)
			f.m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, id == 11 && f.humanActive, nil
			}
			v := f.create(t)
			f.pull(t, v.ID)
			_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, err)
			in := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "LOG", Content: "actual output"}
			table := f.m.names.Logs.String()
			if purpose == "execution_evidence" {
				in.Kind, in.EvidenceKind, in.EvidenceTitle = "EVIDENCE", "OBSERVATION", "Observed result"
				table = f.m.names.Evidence.String()
			}
			_, err = f.receipt(v.ID, in)
			require.NoError(t, err)
			var id guid.ID
			require.NoError(t, f.db.Table(table).Select("id").Scan(&id).Error)
			check := func(ctx context.Context, resource guid.ID, owner files.ResourceOwner) error {
				return f.db.Transaction(func(tx *gorm.DB) error {
					return f.m.authorizeArtifactOn(ctx, tx, files.ExecutionArtifactAuthority{}, purpose, resource, owner)
				})
			}
			owner := files.ResourceOwner{Scope: "entity", ID: 9}
			require.NoError(t, check(f.human, id, owner))
			require.ErrorIs(t, check(f.human, id, files.ResourceOwner{Scope: "tenant", ID: 9}), ErrForbidden)
			require.ErrorIs(t, check(f.human, id, files.ResourceOwner{Scope: "entity", ID: 10}), ErrForbidden)
			require.Error(t, check(f.human, id+100, owner))
			require.ErrorIs(t, check(f.runner, id, owner), ErrForbidden)
			f.authorized = false
			require.ErrorIs(t, check(f.human, id, owner), ErrForbidden)
			f.authorized = true
			f.humanActive = false
			require.ErrorIs(t, check(f.human, id, owner), ErrForbidden)
		})
	}
}

func TestArtifactRunnerAuthorizationFailsClosedBeforeStorage(t *testing.T) {
	for _, fault := range []string{"lock", "missing_authority", "denied", "session", "tenant", "sequence", "resource", "valid"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, err)
			a := artifactAuthority(7, 9, v.ID, f.fence, ReceiptInput{DispatchEpoch: 1, Seq: 2})
			var resource guid.ID
			switch fault {
			case "lock":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrForbidden }
			case "missing_authority":
				f.m.work.AuthorizeOn = nil
			case "denied":
				f.authorized = false
			case "session":
				f.sessionValid = false
			case "tenant":
				a.TenantID = 8
			case "sequence":
				a.Seq++
			case "resource":
				resource = 999
			}
			err = f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.authorizeArtifactOn(f.runner, tx, a, "execution_evidence", resource, files.ResourceOwner{Scope: "entity", ID: 9})
			})
			if fault == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.EqualValues(t, 1, f.read(t, v.ID).LastSeq)
		})
	}
}

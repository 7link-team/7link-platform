package execution

import (
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/files"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReceiptStorageAndDomainFailuresRollbackFactsAndSlots(t *testing.T) {
	for _, fault := range []string{"authorize_missing", "authorize_error", "session_missing", "started_missing", "started_error", "log_id", "log_insert", "evidence_id", "evidence_insert", "evidence_query", "finished_missing", "finished_error", "release_missing", "release_error", "update", "receipt_id", "receipt_insert", "evidence_outbox", "terminal_outbox"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			in := ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}
			if fault != "started_missing" && fault != "started_error" {
				_, err := f.receipt(v.ID, in)
				require.NoError(t, err)
				in.Seq, in.Kind = 2, "HEARTBEAT"
			}
			before := f.read(t, v.ID)
			var effectsBefore int64
			require.NoError(t, f.db.Table("effects").Count(&effectsBefore).Error)
			trigger := func(table, operation string) {
				require.NoError(t, f.db.Exec(fmt.Sprintf("CREATE TRIGGER reject_write BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, 'test receipt storage failure'); END", operation, table)).Error)
			}
			switch fault {
			case "authorize_missing":
				f.m.work.AuthorizeOn = nil
			case "authorize_error":
				f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return ErrForbidden }
			case "session_missing":
				f.m.machines.ValidateSessionOn = nil
			case "started_missing":
				f.m.work.StartedOn = nil
			case "started_error":
				f.m.work.StartedOn = func(context.Context, *gorm.DB, WorkLink) error { return ErrCapabilityUnavailable }
			case "log_id", "log_insert":
				in.Kind, in.Content = "LOG", "real output"
				if fault == "log_id" {
					f.m.ids = failedExecutionIDs{}
				} else {
					trigger(f.m.names.Logs.String(), "INSERT")
				}
			case "evidence_id", "evidence_insert", "evidence_query", "evidence_outbox":
				in.Kind, in.Content, in.EvidenceKind, in.EvidenceTitle = "EVIDENCE", "actual evidence", "OBSERVATION", "Runner observation"
				switch fault {
				case "evidence_id":
					f.m.ids = failedExecutionIDs{}
				case "evidence_insert":
					trigger(f.m.names.Evidence.String(), "INSERT")
				case "evidence_query":
					require.NoError(t, f.db.Exec("ALTER TABLE "+f.m.names.Evidence.String()+" RENAME TO hidden_evidence").Error)
				case "evidence_outbox":
					f.pub.fail = true
				}
			case "finished_missing", "finished_error", "release_missing", "release_error", "terminal_outbox":
				in.Kind = "FAILED"
				switch fault {
				case "finished_missing":
					f.m.work.FinishedOn = nil
				case "finished_error":
					f.m.work.FinishedOn = func(context.Context, *gorm.DB, WorkLink, WorkResult) error { return ErrCapabilityUnavailable }
				case "release_missing":
					f.m.machines.ReleaseSlotOn = nil
				case "release_error":
					f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return fleet.ErrUnavailable }
				case "terminal_outbox":
					f.pub.fail = true
				}
			case "update":
				trigger(f.m.names.Executions.String(), "UPDATE")
			case "receipt_id":
				f.m.ids = failedExecutionIDs{}
			case "receipt_insert":
				trigger(f.m.names.Receipts.String(), "INSERT")
			}
			_, err := f.receipt(v.ID, in)
			require.Error(t, err)
			require.Equal(t, before, f.read(t, v.ID))
			var active int
			require.NoError(t, f.db.Raw("SELECT active FROM slots WHERE execution_id=?", v.ID).Scan(&active).Error)
			require.Equal(t, 1, active)
			var count int64
			require.NoError(t, f.db.Table("effects").Count(&count).Error)
			require.Equal(t, effectsBefore, count)
			require.NoError(t, f.db.Table(f.m.names.Receipts.String()).Count(&count).Error)
			require.Equal(t, before.LastSeq, count)
			for _, table := range []string{f.m.names.Logs.String(), f.m.names.Evidence.String()} {
				if fault == "evidence_query" && table == f.m.names.Evidence.String() {
					table = "hidden_evidence"
				}
				require.NoError(t, f.db.Table(table).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
}

func TestPullFailuresNeverLeaseCommand(t *testing.T) {
	for _, fault := range []string{"lock", "authorize_missing", "authorize_denied", "session_missing", "invalid_session", "update_lease", "update_version", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			before := f.read(t, v.ID)
			switch fault {
			case "lock":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrForbidden }
			case "authorize_missing":
				f.m.work.AuthorizeOn = nil
			case "authorize_denied":
				f.authorized = false
			case "session_missing":
				f.m.machines.ValidateSessionOn = nil
			case "invalid_session":
				f.fence.SessionID = 0
			case "update_lease", "update_version":
				column := "lease_expires_at"
				if fault == "update_version" {
					column = "version"
				}
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_pull BEFORE UPDATE OF "+column+" ON "+f.m.names.Executions.String()+" BEGIN SELECT RAISE(ABORT, 'test lease failure'); END").Error)
			case "outbox":
				f.pub.fail = true
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.PullOn(f.runner, tx, 7, f.fence); return err }))
			require.Equal(t, before, f.read(t, v.ID))
		})
	}
}

func TestInvalidReceiptFactsCannotAdvanceSequence(t *testing.T) {
	for _, fault := range []string{"invalid_kind", "invalid_content", "wrong_machine", "heartbeat_before_start", "empty_log", "bad_evidence_kind", "bad_commit", "empty_title", "complete_without_head", "cancel_without_intent"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			in := ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}
			if fault != "heartbeat_before_start" {
				_, err := f.receipt(v.ID, in)
				require.NoError(t, err)
				in.Seq = 2
			}
			before := f.read(t, v.ID)
			switch fault {
			case "invalid_kind":
				in.Kind = "SUCCEEDED"
			case "invalid_content":
				in.Content = string([]byte{255})
			case "wrong_machine":
				f.fence.MachineID = 3
			case "heartbeat_before_start":
				in.Kind = "HEARTBEAT"
			case "empty_log":
				in.Kind = "LOG"
			case "bad_evidence_kind", "bad_commit", "empty_title":
				in.Kind, in.EvidenceKind, in.EvidenceTitle, in.Content = "EVIDENCE", "OBSERVATION", "Runner evidence", "actual observation"
				if fault == "bad_evidence_kind" {
					in.EvidenceKind = "APPROVAL"
				}
				if fault == "bad_commit" {
					in.EvidenceKind = "COMMIT"
				}
				if fault == "empty_title" {
					in.EvidenceTitle = ""
				}
			case "complete_without_head":
				in.Kind, in.HeadCommit = "COMPLETED", strings.Repeat("a", 40)
			case "cancel_without_intent":
				in.Kind = StateCanceled
			}
			_, err := f.receipt(v.ID, in)
			require.Error(t, err)
			require.Equal(t, before, f.read(t, v.ID))
		})
	}
}

func TestReceiptReplayEvidenceAndClaimAtomicity(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	leased := f.pull(t, v.ID)
	require.NotNil(t, leased.LeaseExpiresAt)
	require.Greater(t, leased.Version, v.Version)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		rows, e := f.m.PullOn(f.runner, tx, 7, f.fence)
		require.Empty(t, rows)
		return e
	}))
	accepted := ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}
	r, err := f.receipt(v.ID, accepted)
	require.NoError(t, err)
	require.Equal(t, "RUNNING", r.State)
	replay, err := f.receipt(v.ID, accepted)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	changed := accepted
	changed.Kind = "HEARTBEAT"
	_, err = f.receipt(v.ID, changed)
	require.ErrorIs(t, err, ErrConflict)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "HEARTBEAT"})
	require.ErrorIs(t, err, ErrFenced)
	log := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "LOG", Content: "actual runner output"}
	f.pub.fail = true
	_, err = f.receipt(v.ID, log)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Equal(t, int64(1), f.read(t, v.ID).LastSeq)
	f.pub.fail = false
	_, err = f.receipt(v.ID, log)
	require.NoError(t, err)
	q := query.BindLogs(f.db, f.m.names)
	logs, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, hash([]byte(log.Content)), logs[0].ContentHash)
	head := strings.Repeat("b", 40)
	evidence := ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "EVIDENCE", Content: head, EvidenceKind: "COMMIT", EvidenceTitle: "Runner head"}
	_, err = f.receipt(v.ID, evidence)
	require.NoError(t, err)
	conflict := evidence
	conflict.Seq = 4
	conflict.EvidenceKind = "OBSERVATION"
	_, err = f.receipt(v.ID, conflict)
	require.ErrorIs(t, err, ErrConflict)
	completed := ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: "COMPLETED", HeadCommit: head}
	finish := f.m.work.FinishedOn
	f.m.work.FinishedOn = func(context.Context, *gorm.DB, WorkLink, WorkResult) error { return ErrCapabilityUnavailable }
	_, err = f.receipt(v.ID, completed)
	require.ErrorIs(t, err, ErrCapabilityUnavailable)
	require.Equal(t, "RUNNING", f.read(t, v.ID).State)
	var active int
	require.NoError(t, f.db.Raw("SELECT active FROM slots WHERE execution_id=?", v.ID).Scan(&active).Error)
	require.Equal(t, 1, active)
	f.m.work.FinishedOn = finish
	r, err = f.receipt(v.ID, completed)
	require.NoError(t, err)
	require.Equal(t, "SUCCEEDED", r.State)
	replay, err = f.receipt(v.ID, completed)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.NoError(t, f.db.Raw("SELECT active FROM slots WHERE execution_id=?", v.ID).Scan(&active).Error)
	require.Zero(t, active)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 5, Kind: "LOG", Content: "late"})
	require.ErrorIs(t, err, ErrFenced)
	var n int64
	require.NoError(t, f.db.Table("effects").Where("kind = ?", "work.SUCCEEDED").Count(&n).Error)
	require.Equal(t, int64(1), n)
}
func TestRunnerFencesCurrentIAMTenantEpochAndLease(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	in := ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}
	f.sessionValid = false
	_, err := f.receipt(v.ID, in)
	require.ErrorIs(t, err, ErrFenced)
	f.sessionValid = true
	f.runner = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 102})
	_, err = f.receipt(v.ID, in)
	require.ErrorIs(t, err, ErrFenced)
	f.runner = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 101})
	in.DispatchEpoch = 2
	_, err = f.receipt(v.ID, in)
	require.ErrorIs(t, err, ErrFenced)
	in.DispatchEpoch = 1
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.ReceiptOn(f.runner, tx, 8, v.ID, f.fence, in); return e }), ErrNotFound)
	old := time.Now().UTC().Add(-time.Second)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("lease_expires_at", old).Error)
	_, err = f.receipt(v.ID, in)
	require.ErrorIs(t, err, ErrFenced)
	require.Equal(t, "CREATED", f.read(t, v.ID).State)
}
func TestReceiptKindsRequireValidRuntimeFacts(t *testing.T) {
	for _, kind := range []string{"FAILED", "LOST", StateCanceled} {
		t.Run(kind, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, err)
			if kind == StateCanceled {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, v.ID, f.read(t, v.ID).Version); return e }))
			}
			r, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: kind})
			require.NoError(t, err)
			if kind == "LOST" {
				require.Equal(t, "INTERRUPTED", r.State)
				require.Equal(t, "UNKNOWN", f.read(t, v.ID).ResultState)
			} else {
				require.Equal(t, kind, r.State)
				require.Equal(t, "CONFIRMED", f.read(t, v.ID).ResultState)
			}
		})
	}
}
func TestPinnedArtifactReceiptPassesExactManifestToDelivery(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	artifact := ArtifactFact{BindingID: 504, SpaceID: 501, FileID: 502, VersionID: 503, FileVersion: 1, Coverage: "COVERED", SHA256: hash([]byte("actual artifact"))}
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Report", Upload: &ArtifactUpload{Name: "report.txt", Data: []byte("actual artifact"), Coverage: "COVERED"}})
	require.NoError(t, err)
	head := strings.Repeat("a", 40)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Head", Content: head})
	require.NoError(t, err)
	var received WorkResult
	f.m.work.FinishedOn = func(_ context.Context, _ *gorm.DB, _ WorkLink, r WorkResult) error { received = r; return nil }
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: "COMPLETED", HeadCommit: head})
	require.NoError(t, err)
	require.Len(t, received.Evidence, 2)
	require.Equal(t, &artifact, received.Evidence[0].Artifact)
	require.Equal(t, hash([]byte("actual artifact")), received.Evidence[0].ContentHash)
	completed := f.read(t, v.ID)
	require.NotNil(t, completed.Claim)
	require.Equal(t, v.ID, completed.Claim.ID)
	require.Equal(t, "EXECUTION", completed.Claim.Origin)
}
func TestMalformedFileArtifactReceiptRollsBack(t *testing.T) {
	for _, content := range []string{"bogus", `{}`, `{"file_id":"1","space_id":"2","version_id":"3","file_version":0,"sha256":"a"}`} {
		t.Run(content, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, err)
			_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "FILE", EvidenceTitle: "Report", Content: content})
			require.ErrorIs(t, err, ErrInvalidArgument)
			require.EqualValues(t, 1, f.read(t, v.ID).LastSeq)
		})
	}
}

func TestRunnerReceiptsRejectUnavailableCommandDependenciesWithoutAdvancing(t *testing.T) {
	for _, command := range []string{"ACCEPTED", "LOG", "EVIDENCE", "FAILED", StateCanceled} {
		for _, fault := range []string{"missing", "error"} {
			t.Run(command+"/"+fault, func(t *testing.T) {
				f := executionHarness(t)
				v := f.create(t)
				f.pull(t, v.ID)
				in := ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: command}
				if command != "ACCEPTED" {
					_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
					require.NoError(t, e)
					in.Seq = 2
				}
				if command == StateCanceled {
					version := f.read(t, v.ID).Version
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.CancelOn(f.human, tx, 7, v.ID, version); return e }))
				}
				in.Content = "Actual runtime log"
				in.EvidenceKind = "OBSERVATION"
				in.EvidenceTitle = "Observed screen"
				var e error
				in, e = f.m.PrepareReceipt(f.runner, 7, v.ID, f.fence, in)
				require.NoError(t, e)
				failure := errors.New("receipt dependency failed")
				expected := ErrCapabilityUnavailable
				if fault == "error" {
					expected = failure
				}
				switch command {
				case "ACCEPTED":
					f.m.work.StartedOn = nil
					if fault == "error" {
						f.m.work.StartedOn = func(context.Context, *gorm.DB, WorkLink) error { return failure }
					}
				case "LOG", "EVIDENCE":
					f.m.artifacts.attach = nil
					if fault == "error" {
						f.m.artifacts.attach = func(context.Context, *gorm.DB, *files.PreparedExecutionArtifact, guid.ID) (files.PinnedResource, error) {
							return files.PinnedResource{}, failure
						}
					}
				default:
					f.m.work.FinishedOn = nil
					if fault == "error" {
						f.m.work.FinishedOn = func(context.Context, *gorm.DB, WorkLink, WorkResult) error { return failure }
					}
				}
				before := f.read(t, v.ID)
				_, e = f.receipt(v.ID, in)
				require.ErrorIs(t, e, expected)
				require.Equal(t, before, f.read(t, v.ID))
			})
		}
	}
}

func TestDispatchEveryStorageFailurePreservesRunnerSequenceAndEvidence(t *testing.T) {
	for _, command := range []string{"pull", "accepted", "log", "evidence", "complete"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f := executionHarness(t)
				v := f.create(t)
				if command != "pull" {
					f.pull(t, v.ID)
				}
				if command == "log" || command == "evidence" || command == "complete" {
					_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
					require.NoError(t, e)
				}
				if command == "complete" {
					_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Observed commit", Content: strings.Repeat("b", 40)})
					require.NoError(t, e)
				}
				var prepared ReceiptInput
				if command == "log" || command == "evidence" {
					input := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "LOG", Content: "Observed runtime output"}
					if command == "evidence" {
						input = ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Observed commit", Content: strings.Repeat("b", 40)}
					}
					var e error
					prepared, e = f.m.PrepareReceipt(f.runner, 7, v.ID, f.fence, input)
					require.NoError(t, e)
				}
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Executions.String(), f.m.names.Receipts.String(), f.m.names.Logs.String(), f.m.names.Evidence.String(), f.m.names.Approvals.String(), "slots", "effects"} {
						var rows []map[string]any
						require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
						out[table] = rows
					}
					return out
				}
				before := snapshot()
				count := 0
				injected := errors.New("execution transactional storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("execution_transaction_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("execution_transaction_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("execution_transaction_failure", hook))
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					switch command {
					case "pull":
						_, e := f.m.PullOn(f.runner, tx, 7, f.fence)
						return e
					case "accepted":
						_, e := f.m.ReceiptOn(f.runner, tx, 7, v.ID, f.fence, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
						return e
					case "log":
						_, e := f.m.ReceiptOn(f.runner, tx, 7, v.ID, f.fence, prepared)
						return e
					case "evidence":
						_, e := f.m.ReceiptOn(f.runner, tx, 7, v.ID, f.fence, prepared)
						return e
					default:
						_, e := f.m.ReceiptOn(f.runner, tx, 7, v.ID, f.fence, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "COMPLETED", HeadCommit: strings.Repeat("b", 40)})
						return e
					}
				})
				require.NoError(t, f.db.Callback().Query().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("execution_transaction_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				require.Equal(t, before, snapshot(), "failed command must roll back execution, journals, slots and effects")
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}

func TestExecutionCompletionDependenciesCannotConsumeSequenceOrEvidenceOnFailure(t *testing.T) {
	for _, fault := range []string{"principal", "usage", "finished_missing", "finished_error", "references_missing", "release_missing", "release_error", "id_error", "file_json"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			f.pull(t, v.ID)
			_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, e)
			head := strings.Repeat("b", 40)
			_, e = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "COMMIT", EvidenceTitle: "Actual head", Content: head})
			require.NoError(t, e)
			in := ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "COMPLETED", HeadCommit: head}
			failure := errors.New("completion dependency unavailable")
			switch fault {
			case "principal":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "usage":
				in.Usage = &ExecutionUsage{InputTokens: -1}
			case "finished_missing":
				f.m.work.FinishedOn = nil
			case "finished_error":
				f.m.work.FinishedOn = func(context.Context, *gorm.DB, WorkLink, WorkResult) error { return failure }
			case "references_missing":
				f.m.artifacts.references = nil
			case "release_missing":
				f.m.machines.ReleaseSlotOn = nil
			case "release_error":
				f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure }
			case "id_error":
				f.m.ids = failedExecutionIDs{err: failure}
			case "file_json":
				require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Create(&model.Evidence{ID: 999, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, Kind: "FILE", Title: "Corrupted legacy pin", Content: "{", ContentHash: hash(nil)}).Error)
			}
			before := f.read(t, v.ID)
			_, e = f.receipt(v.ID, in)
			require.Error(t, e)
			require.Equal(t, before, f.read(t, v.ID))
		})
	}
}

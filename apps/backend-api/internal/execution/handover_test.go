package execution

import (
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func handoverFixture(t *testing.T) (*executionFixture, *bool, *string) {
	f := executionHarness(t)
	allowed := true
	state := "RETIRING"
	f.m.handover.authority.AuthorizeOn = func(ctx context.Context, _ *gorm.DB, tenant, agent, retirement guid.ID) error {
		if !allowed || tenant != 7 || agent != 21 || retirement != 31 || authn.PrincipalIDFromContext(ctx) != 11 {
			return agents.ErrForbidden
		}
		return nil
	}
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{TenantID: 7, PrincipalID: 21, State: state, RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "family"}, true, nil
	}
	return f, &allowed, &state
}

func launchTestHandover(t *testing.T, f *executionFixture) guid.ID {
	t.Helper()
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = f.m.launchHandoverOn(f.human, tx, 7, 21, 31, "Write a handover document using the provided source facts.")
		return err
	}))
	return id
}

func TestHandoverCancellationRejectsUnavailableOrChangedCurrentFacts(t *testing.T) {
	for _, fault := range []string{"authority_missing", "authority_error", "invalid_id", "missing", "scope", "plan", "machine", "epoch", "release_missing", "release_error"} {
		t.Run(fault, func(t *testing.T) {
			f, _, _ := handoverFixture(t)
			id := launchTestHandover(t, f)
			failure := errors.New("handover dependency unavailable")
			expected := ErrCapabilityUnavailable
			agent := guid.ID(21)
			switch fault {
			case "authority_missing":
				f.m.handover.authority.AuthorizeOn = nil
			case "authority_error":
				f.m.handover.authority.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure }
				expected = failure
			case "invalid_id":
				id = 0
				expected = ErrInvalidArgument
			case "missing":
				id++
				expected = ErrNotFound
			case "scope":
				f.m.handover.authority.AuthorizeArtifactOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
				agent++
				expected = ErrForbidden
			case "plan":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("plan_json", "{").Error)
				expected = ErrUnavailable
			case "machine":
				f.m.machines.LockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
				expected = failure
			case "epoch":
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("dispatch_epoch", int64(^uint64(0)>>1)).Error)
				expected = ErrConflict
			case "release_missing":
				f.m.machines.ReleaseSlotOn = nil
			case "release_error":
				f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure }
				expected = failure
			}
			var before []map[string]any
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Find(&before).Error)
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.cancelHandoverOn(f.human, tx, 7, agent, 31, id) }), expected)
			var after []map[string]any
			require.NoError(t, f.db.Table(f.m.names.Executions.String()).Find(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func TestHandoverArtifactReadStorageErrorsNeverYieldTrustedDocument(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f, _, _ := handoverFixture(t)
		id := launchTestHandover(t, f)
		f.pull(t, id)
		_, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
		require.NoError(t, e)
		_, e = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", Content: "Actual final handover document"})
		require.NoError(t, e)
		return f, func(tx *gorm.DB) error { _, e := f.m.readHandoverOn(f.human, tx, 7, 21, 31); return e }
	})
}

func TestHandoverPreparationUsesActualBindingWithoutRequiringCredentialDelivery(t *testing.T) {
	f, _, _ := handoverFixture(t)
	f.m.agentRuntime.RetirementOn = f.m.agentRuntime.ByPrincipalOn
	f.m.agentRuntime.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentRuntimeFact, bool, error) {
		return fleet.AgentRuntimeFact{}, false, fleet.ErrVersionConflict
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.prepareHandoverOn(f.human, tx, 7, 21) }))
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.launchHandoverOn(f.human, tx, 7, 21, 31, "Actual source prompt")
		return err
	}), "launch must still require actual delivered credentials")
}

func TestHandoverCommittedReceiptReplaysAfterProtocolAdvancesWithCurrentAuthority(t *testing.T) {
	f, allowed, state := handoverFixture(t)
	f.m.handover.authority.AuthorizeArtifactOn = f.m.handover.authority.AuthorizeOn
	id := launchTestHandover(t, f)
	f.pull(t, id)
	_, err := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	in := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", Content: "Actual completed handover document"}
	_, err = f.receipt(id, in)
	require.NoError(t, err)
	f.m.handover.authority.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return agents.ErrConflict }
	*state = "RETIRED"
	replay, err := f.receipt(id, in)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	_, err = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "LOG", Content: "uncommitted late output"})
	require.Error(t, err, "historical replay must not authorize any new receipt")
	changed := in
	changed.Content += " tampered"
	_, err = f.receipt(id, changed)
	require.ErrorIs(t, err, ErrConflict)
	*allowed = false
	_, err = f.receipt(id, in)
	require.Error(t, err, "even identical replay must recheck the initiator's current retirement authority")
}

func TestHandoverCancellationFencesLateRuntimeWithoutInventingSuccess(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(map[bool]string{false: "never_delivered", true: "runtime_started"}[delivered], func(t *testing.T) {
			f, _, _ := handoverFixture(t)
			id := launchTestHandover(t, f)
			if delivered {
				f.pull(t, id)
				_, err := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
				require.NoError(t, err)
			}
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.cancelHandoverOn(f.human, tx, 7, 21, 31, id) }))
			v := f.read(t, id)
			require.Equal(t, StateCanceled, v.State)
			require.EqualValues(t, 2, v.DispatchEpoch)
			if delivered {
				require.Equal(t, "UNKNOWN", v.ResultState)
			} else {
				require.Equal(t, "CONFIRMED", v.ResultState)
			}
			_, err := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", Content: "late model document"})
			require.Error(t, err)
			q := query.BindHandoverArtifacts(f.db, f.m.names)
			count, err := q.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, count)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.cancelHandoverOn(f.human, tx, 7, 21, 31, id) }))
			require.EqualValues(t, 2, f.read(t, id).DispatchEpoch, "repeat stop does not alter the already fenced attempt")
		})
	}
}

func TestHandoverRunsWithoutAssignmentRepositoryOrCommitAndNeverCreatesClaim(t *testing.T) {
	f, allowed, _ := handoverFixture(t)
	id := launchTestHandover(t, f)
	v := f.pull(t, id)
	require.Zero(t, v.WorkItemID)
	require.Zero(t, v.AssignmentID)
	require.Zero(t, v.Plan.RepositoryID)
	require.Empty(t, v.Plan.BaseCommit)
	require.Empty(t, v.Plan.Workdir)
	require.Equal(t, "HANDOVER", v.BranchReason)
	require.Equal(t, guid.ID(21), v.Plan.ExecutorPrincipalID)
	_, err := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	doc := "## In-flight work\nActual source snapshot, with unavailable fields identified."
	in := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", Content: doc}
	result, err := f.receipt(id, in)
	require.NoError(t, err)
	require.Equal(t, "SUCCEEDED", result.State)
	replayed, err := f.receipt(id, in)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	changed := in
	changed.Content += " changed"
	_, err = f.receipt(id, changed)
	require.ErrorIs(t, err, ErrConflict)
	read := f.read(t, id)
	require.Nil(t, read.Claim)
	var artifact agents.HandoverArtifact
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		artifact, e = f.m.readHandoverOn(f.human, tx, 7, 21, 31)
		return e
	}))
	require.Equal(t, "AGENT", artifact.Source)
	require.NotNil(t, artifact.ExecutionID)
	require.Equal(t, id, *artifact.ExecutionID)
	require.Equal(t, doc, artifact.Content)
	require.Equal(t, hash([]byte(doc)), artifact.SHA256)
	var workEffects int64
	require.NoError(t, f.db.Table("effects").Where("kind LIKE ?", "work.%").Count(&workEffects).Error)
	require.Zero(t, workEffects)
	var active int
	require.NoError(t, f.db.Table("slots").Select("active").Where("execution_id=?", id).Scan(&active).Error)
	require.Zero(t, active)
	*allowed = false
	_, err = f.receipt(id, in)
	require.ErrorIs(t, err, agents.ErrForbidden, "replay still rechecks current initiating Human authority")
}

func TestHandoverFailsClosedAndRollsBackArtifactReceiptSlotAndOutboxTogether(t *testing.T) {
	for _, fault := range []string{"authority", "active_identity", "offline", "session", "old_epoch", "gap", "no_document", "commit", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			f, allowed, state := handoverFixture(t)
			if fault == "authority" {
				*allowed = false
			}
			if fault == "active_identity" {
				*state = "ACTIVE"
			}
			if fault == "offline" {
				f.machineState = "OFFLINE"
			}
			if fault == "authority" || fault == "active_identity" || fault == "offline" {
				require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
					_, err := f.m.launchHandoverOn(f.human, tx, 7, 21, 31, "Write handover")
					return err
				}))
				return
			}
			id := launchTestHandover(t, f)
			f.pull(t, id)
			_, err := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, err)
			in := ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", Content: "Actual handover content"}
			switch fault {
			case "session":
				f.sessionValid = false
			case "old_epoch":
				in.DispatchEpoch = 2
			case "gap":
				in.Seq = 3
			case "no_document":
				in.Content = " "
			case "commit":
				in.HeadCommit = "invented"
			case "outbox":
				f.pub.fail = true
			}
			_, err = f.receipt(id, in)
			require.Error(t, err)
			a := query.BindHandoverArtifacts(f.db, f.m.names)
			count, err := a.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, count)
			r := f.read(t, id)
			require.Equal(t, "RUNNING", r.State)
			require.EqualValues(t, 1, r.LastSeq)
			var active int
			require.NoError(t, f.db.Table("slots").Select("active").Where("execution_id=?", id).Scan(&active).Error)
			require.Equal(t, 1, active)
		})
	}
}

func TestHandoverSystemArtifactNeverFabricatesExecutionAndHonorsCallerRollback(t *testing.T) {
	f, _, _ := handoverFixture(t)
	rollback := errors.New("source transaction failed")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.saveHandoverOn(f.human, tx, 7, 21, 31, nil, "System source snapshot")
		if err != nil {
			return err
		}
		return rollback
	}), rollback)
	q := query.BindHandoverArtifacts(f.db, f.m.names)
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		a, err := f.m.saveHandoverOn(f.human, tx, 7, 21, 31, nil, "System source snapshot")
		require.Equal(t, "SYSTEM", a.Source)
		require.Nil(t, a.ExecutionID)
		return err
	}))
	e := query.BindExecutions(f.db, f.m.names)
	n, err = e.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestHandoverEveryStorageFailurePreservesRetirementAndRuntimeJournal(t *testing.T) {
	for _, command := range []string{"launch", "pull", "accepted", "complete", "cancel"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f, _, _ := handoverFixture(t)
				f.m.handover.authority.AuthorizeArtifactOn = f.m.handover.authority.AuthorizeOn
				var id guid.ID
				if command != "launch" {
					id = launchTestHandover(t, f)
				}
				if command == "accepted" || command == "complete" {
					f.pull(t, id)
				}
				if command == "complete" {
					_, e := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
					require.NoError(t, e)
				}
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Executions.String(), f.m.names.Receipts.String(), f.m.names.Logs.String(), f.m.names.Evidence.String(), f.m.names.Approvals.String(), f.m.names.HandoverArtifacts.String(), "slots", "effects"} {
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
					case "launch":
						_, e := f.m.launchHandoverOn(f.human, tx, 7, 21, 31, "Write a handover document from current facts.")
						return e
					case "pull":
						_, e := f.m.PullOn(f.runner, tx, 7, f.fence)
						return e
					case "accepted":
						_, e := f.m.ReceiptOn(f.runner, tx, 7, id, f.fence, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
						return e
					case "complete":
						_, e := f.m.ReceiptOn(f.runner, tx, 7, id, f.fence, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "COMPLETED", Content: "Observed completed handover document"})
						return e
					default:
						return f.m.cancelHandoverOn(f.human, tx, 7, 21, 31, id)
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

func TestHandoverBoundSystemArtifactUsesCurrentOwnerAndImmutableStoredHash(t *testing.T) {
	f, allowed, _ := handoverFixture(t)
	var runtime agents.RetirementHandover
	connector := f.m.handover.authority
	connector.Bind = func(value agents.RetirementHandover) error { runtime = value; return nil }
	connector.AuthorizeArtifactOn = connector.AuthorizeOn
	services := module.NewServices()
	require.NoError(t, module.Provide[agents.RetirementHandoverConnector](services, connector))
	require.NoError(t, services.Seal())
	require.NoError(t, f.m.bindHandover(services))
	var saved agents.HandoverArtifact
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		saved, e = runtime.SystemOn(f.human, tx, 7, 21, 31, "Current system source facts")
		return e
	}))
	require.Equal(t, "SYSTEM", saved.Source)
	require.Nil(t, saved.ExecutionID)
	require.Equal(t, hash([]byte(saved.Content)), saved.SHA256)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		read, e := runtime.ReadOn(f.human, tx, 7, 21, 31)
		require.Equal(t, saved, read)
		return e
	}))
	*allowed = false
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := runtime.SystemOn(f.human, tx, 7, 21, 31, "Must be denied"); return e }))
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := runtime.ReadOn(f.human, tx, 7, 21, 31); return e }))
	*allowed = true
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := runtime.ReadOn(f.human, tx, 7, 21, 999)
		require.ErrorIs(t, e, agents.ErrForbidden)
		return nil
	}))
	require.NoError(t, f.db.Table(f.m.names.HandoverArtifacts.String()).Where("id=?", saved.ID).Update("sha256", strings.Repeat("0", 64)).Error)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := runtime.ReadOn(f.human, tx, 7, 21, 31); return e }), ErrUnavailable)
	var n int64
	require.NoError(t, f.db.Table(f.m.names.HandoverArtifacts.String()).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestHandoverCurrentSourcesNeverCreateDocumentOrReserveExecution(t *testing.T) {
	for _, fault := range []string{"authority_missing", "authority_source", "agent_missing_capability", "agent_source", "agent_absent", "agent_state", "agent_tenant", "agent_identity", "agent_kind", "agent_model", "agent_prompt", "runtime_missing_capability", "runtime_source", "runtime_absent", "runtime_machine", "runtime_offline", "runtime_kind", "machine_source", "lock", "id_error", "id_zero", "objective", "reserve_source"} {
		t.Run(fault, func(t *testing.T) {
			f, _, _ := handoverFixture(t)
			failure := errors.New("handover source unavailable")
			objective := "Write from current source facts"
			switch fault {
			case "authority_missing":
				f.m.handover.authority.AuthorizeOn = nil
			case "authority_source":
				f.m.handover.authority.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure }
			case "agent_missing_capability":
				f.m.agents.ByPrincipalOn = nil
			case "runtime_missing_capability":
				f.m.agentRuntime.ByPrincipalOn = nil
			case "agent_source", "agent_absent", "agent_state", "agent_tenant", "agent_identity", "agent_kind", "agent_model", "agent_prompt":
				old := f.m.agents.ByPrincipalOn
				f.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
					a, ok, e := old(ctx, tx, tenant, id)
					switch fault {
					case "agent_source":
						e = failure
					case "agent_absent":
						ok = false
					case "agent_state":
						a.State = "RETIRED"
					case "agent_tenant":
						a.TenantID = 8
					case "agent_identity":
						a.PrincipalID = 22
					case "agent_kind":
						a.RuntimeKind = "INVALID"
					case "agent_model":
						a.ModelID = ""
					case "agent_prompt":
						a.SystemPrompt = strings.Repeat("x", 20001)
					}
					return a, ok, e
				}
			case "runtime_source", "runtime_absent", "runtime_machine", "runtime_offline", "runtime_kind":
				old := f.m.agentRuntime.ByPrincipalOn
				f.m.agentRuntime.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentRuntimeFact, bool, error) {
					a, ok, e := old(ctx, tx, tenant, id)
					switch fault {
					case "runtime_source":
						e = failure
					case "runtime_absent":
						ok = false
					case "runtime_machine":
						a.MachineID = nil
					case "runtime_offline":
						a.MachineState = "OFFLINE"
					case "runtime_kind":
						a.RuntimeKind = "INVALID"
					}
					return a, ok, e
				}
			case "machine_source":
				f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
			case "lock":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "id_error":
				f.m.ids = failedExecutionIDs{err: failure}
			case "id_zero":
				f.m.ids = failedExecutionIDs{}
			case "objective":
				objective = strings.Repeat("x", 20001)
			case "reserve_source":
				f.m.machines.ReserveSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.SlotFact, error) {
					return fleet.SlotFact{}, failure
				}
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				id, e := f.m.launchHandoverOn(f.human, tx, 7, 21, 31, objective)
				require.Zero(t, id)
				return e
			}))
			for _, table := range []string{f.m.names.Executions.String(), f.m.names.HandoverArtifacts.String(), "slots", "effects"} {
				var n int64
				require.NoError(t, f.db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

func TestHandoverRuntimeLogAndUncertainTerminationUsePersistedReceipts(t *testing.T) {
	for _, terminal := range []string{"FAILED", "LOST", StateCanceled} {
		t.Run(terminal, func(t *testing.T) {
			f, _, _ := handoverFixture(t)
			id := launchTestHandover(t, f)
			f.pull(t, id)
			_, err := f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
			require.NoError(t, err)
			_, err = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "LOG", Content: "Actual handover runtime output"})
			require.NoError(t, err)
			_, err = f.receipt(id, ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "HEARTBEAT"})
			require.NoError(t, err)
			if terminal == StateCanceled {
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Update("cancel_requested_at", time.Now().UTC()).Error)
			}
			receipt := ReceiptInput{DispatchEpoch: 1, Seq: 4, Kind: terminal}
			result, err := f.receipt(id, receipt)
			require.NoError(t, err)
			want := terminal
			if terminal == "LOST" {
				want = "INTERRUPTED"
			}
			require.Equal(t, want, result.State)
			saved := f.read(t, id)
			require.Equal(t, want, saved.State)
			require.Nil(t, saved.LeaseExpiresAt)
			if terminal == "LOST" {
				require.Equal(t, "UNKNOWN", saved.ResultState)
			} else {
				require.Equal(t, "CONFIRMED", saved.ResultState)
			}
			var logs []model.Log
			require.NoError(t, f.db.Table(f.m.names.Logs.String()).Find(&logs).Error)
			require.Len(t, logs, 1)
			require.Equal(t, "Actual handover runtime output", logs[0].Content)
			require.EqualValues(t, 2, logs[0].Seq)
			var count int64
			require.NoError(t, f.db.Table(f.m.names.HandoverArtifacts.String()).Count(&count).Error)
			require.Zero(t, count, "termination must never manufacture a completed document")
			replay, err := f.receipt(id, receipt)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
		})
	}
}

func TestHandoverInvalidReceiptTransitionsAndLogFailuresAreAtomic(t *testing.T) {
	for _, fault := range []string{"heartbeat_before_start", "log_before_start", "complete_before_start", "accept_twice", "empty_log", "log_id", "log_write", "cancel_without_intent", "release_missing", "release_error", "receipt_id"} {
		t.Run(fault, func(t *testing.T) {
			f, _, _ := handoverFixture(t)
			id := launchTestHandover(t, f)
			f.pull(t, id)
			in := ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}
			if !strings.HasSuffix(fault, "before_start") {
				_, e := f.receipt(id, in)
				require.NoError(t, e)
				in.Seq = 2
			}
			failure := errors.New("handover runtime storage unavailable")
			switch fault {
			case "heartbeat_before_start":
				in.Kind = "HEARTBEAT"
			case "log_before_start", "empty_log", "log_id", "log_write":
				in.Kind = "LOG"
			case "complete_before_start":
				in.Kind = "COMPLETED"
				in.Content = "Actual output"
			case "accept_twice":
				in.Kind = "ACCEPTED"
			case "cancel_without_intent":
				in.Kind = StateCanceled
			default:
				in.Kind = "FAILED"
			}
			if fault == "log_id" || fault == "log_write" {
				in.Content = "Actual runtime log"
			}
			if fault == "log_id" || fault == "receipt_id" {
				f.m.ids = failedExecutionIDs{failure}
			}
			if fault == "log_write" {
				require.NoError(t, f.db.Exec("CREATE TRIGGER reject_log BEFORE INSERT ON "+f.m.names.Logs.String()+" BEGIN SELECT RAISE(ABORT,'log unavailable'); END").Error)
			}
			if fault == "release_missing" {
				f.m.machines.ReleaseSlotOn = nil
			}
			if fault == "release_error" {
				f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure }
			}
			before := f.read(t, id)
			var effects, receipts int64
			require.NoError(t, f.db.Table("effects").Count(&effects).Error)
			require.NoError(t, f.db.Table(f.m.names.Receipts.String()).Count(&receipts).Error)
			_, e := f.receipt(id, in)
			require.Error(t, e)
			require.Equal(t, before, f.read(t, id))
			var count int64
			require.NoError(t, f.db.Table("effects").Count(&count).Error)
			require.Equal(t, effects, count)
			require.NoError(t, f.db.Table(f.m.names.Receipts.String()).Count(&count).Error)
			require.Equal(t, receipts, count)
			require.NoError(t, f.db.Table(f.m.names.Logs.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestHandoverAuthorizationRejectsChangedIdentityAndCurrentRuntime(t *testing.T) {
	for _, fault := range []string{"plan_hash", "purpose", "work_item", "initiator", "missing_authority", "missing_agents", "missing_runtime", "principal_lock", "agent_error", "agent_absent", "agent_state", "agent_model", "runtime_error", "runtime_absent", "runtime_machine", "runtime_kind"} {
		t.Run(fault, func(t *testing.T) {
			f, _, _ := handoverFixture(t)
			id := launchTestHandover(t, f)
			before := f.read(t, id)
			failure := errors.New("handover current source unavailable")
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				row, e := f.m.rowOn(f.human, tx, 7, id, false)
				require.NoError(t, e)
				switch fault {
				case "plan_hash":
					row.PlanHash = strings.Repeat("f", 64)
				case "purpose":
					row.BranchReason = "NORMAL"
				case "work_item":
					row.WorkItemID = 3
				case "initiator":
					row.InitiatorID = 0
				case "missing_authority":
					f.m.handover.authority.AuthorizeOn = nil
				case "missing_agents":
					f.m.agents.ByPrincipalOn = nil
				case "missing_runtime":
					f.m.agentRuntime.ByPrincipalOn = nil
				case "principal_lock":
					f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
				case "agent_error", "agent_absent", "agent_state", "agent_model":
					old := f.m.agents.ByPrincipalOn
					f.m.agents.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (fleet.AgentIdentity, bool, error) {
						a, ok, e := old(ctx, tx, tenant, agent)
						switch fault {
						case "agent_error":
							e = failure
						case "agent_absent":
							ok = false
						case "agent_state":
							a.State = "ACTIVE"
						case "agent_model":
							a.ModelID = "changed"
						}
						return a, ok, e
					}
				case "runtime_error", "runtime_absent", "runtime_machine", "runtime_kind":
					old := f.m.agentRuntime.ByPrincipalOn
					f.m.agentRuntime.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (fleet.AgentRuntimeFact, bool, error) {
						a, ok, e := old(ctx, tx, tenant, agent)
						switch fault {
						case "runtime_error":
							e = failure
						case "runtime_absent":
							ok = false
						case "runtime_machine":
							machine := guid.ID(2)
							a.MachineID = &machine
						case "runtime_kind":
							a.RuntimeKind = "changed"
						}
						return a, ok, e
					}
				}
				_, e = f.m.authorizeHandoverOn(f.human, tx, row, false)
				require.Error(t, e)
				return nil
			}))
			require.Equal(t, before, f.read(t, id))
		})
	}
}
func TestHandoverPreparationCurrentSourcesAndArtifactStorageFailures(t *testing.T) {
	for _, fault := range []string{"missing_runtime", "missing_machine", "runtime_error", "runtime_absent", "runtime_unbound", "machine_error", "principal_error"} {
		t.Run(fault, func(t *testing.T) {
			f, _, _ := handoverFixture(t)
			failure := errors.New("handover preparation source unavailable")
			success := false
			switch fault {
			case "missing_runtime":
				f.m.agentRuntime.ByPrincipalOn = nil
			case "missing_machine":
				f.m.machines.ByIDOn = nil
			case "runtime_error", "runtime_absent", "runtime_unbound":
				old := f.m.agentRuntime.ByPrincipalOn
				f.m.agentRuntime.ByPrincipalOn = func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (fleet.AgentRuntimeFact, bool, error) {
					r, ok, e := old(ctx, tx, tenant, agent)
					switch fault {
					case "runtime_error":
						e = failure
					case "runtime_absent":
						ok = false
					case "runtime_unbound":
						r.MachineID = nil
						success = true
					}
					return r, ok, e
				}
			case "machine_error":
				f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
			case "principal_error":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			}
			e := f.db.Transaction(func(tx *gorm.DB) error { return f.m.prepareHandoverOn(f.human, tx, 7, 21) })
			if success {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
		})
	}
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f, _, _ := handoverFixture(t)
		return f, func(tx *gorm.DB) error {
			out, e := f.m.saveHandoverOn(f.human, tx, 7, 21, 31, nil, "Actual system facts")
			if e == nil {
				require.Equal(t, "SYSTEM", out.Source)
				require.Equal(t, hash([]byte(out.Content)), out.SHA256)
			}
			return e
		}
	})
}

func TestBindHandoverRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindHandover(module.NewServices()), module.ErrServicesProviding)
}

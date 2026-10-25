package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

// The production InjectServices catalog is captured by the module wiring test;
// use those declarations unchanged with the real outbox publisher, rather than
// duplicating a catalog here that could hide a missing production declaration.
func testCodeMergeRealOutboxCatalog(t *testing.T, specs []events.TypeSpec) {
	t.Helper()
	for _, outcome := range []string{"MERGED", "CONFLICT"} {
		t.Run(outcome, func(t *testing.T) {
			f, id, head := mergeFixture(t)
			outbox := events.NewModule(f.m.runtime)
			rawDB, err := f.db.DB()
			require.NoError(t, err)
			source := outbox.MigrationSource()
			source.Prefix = f.m.runtime.TablePrefix()
			require.NoError(t, goosex.Migrate(t.Context(), rawDB, source, f.db.Dialector.Name()))
			services := module.NewServices()
			require.NoError(t, outbox.ProvideServices(services))
			require.NoError(t, module.Provide[guid.Source](services, f.m.ids))
			require.NoError(t, module.Provide[dlock.TransactionGuard](services, func(context.Context, *gorm.DB, string) error { return nil }))
			require.NoError(t, module.Provide[dlock.TryRenewingLease](services, func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
				return nil, dlock.ErrNotAcquired
			}))
			require.NoError(t, services.Seal())
			require.NoError(t, outbox.InjectServices(services))
			require.NoError(t, outbox.AddTypes(Name, specs...))
			require.NoError(t, outbox.Initialize(t.Context()))
			f.m.events, err = module.Resolve[events.Publisher](services)
			require.NoError(t, err)
			reader, err := module.Resolve[events.EventReader](services)
			require.NoError(t, err)
			request := func(tx *gorm.DB) (MergeView, error) {
				return f.m.RequestMergeOn(f.human, tx, 7, id, MergeRequest{Reason: "Accepted integration with real outbox"})
			}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := request(tx)
				if err != nil {
					return err
				}
				return ErrConflict
			}), ErrConflict)
			rows, err := reader(t.Context(), f.db, 0, 200)
			require.NoError(t, err)
			require.Empty(t, rows, "business rollback must also roll back real outbox")
			var created MergeView
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; created, e = request(tx); return e }))
			var plans []MergePlan
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; plans, e = f.m.pullMergeOn(f.runner, tx, 7, f.fence); return e }))
			require.Len(t, plans, 1)
			planJSON, err := json.Marshal(plans[0])
			require.NoError(t, err)
			now := time.Now().UTC()
			observation := MergeObservation{PlanHash: hash(planJSON), State: outcome, SourceCommit: head, StartedAt: now, EndedAt: now, Reason: "Actual runner outcome"}
			if outcome == "MERGED" {
				observation.MergeCommit = strings.Repeat("c", 40)
			} else {
				observation.ConflictFiles = []string{"conflict.txt"}
			}
			receipt := func() error {
				return f.db.Transaction(func(tx *gorm.DB) error {
					return f.m.completeMergeOn(f.runner, tx, 7, created.ID, f.fence, plans[0].Nonce, observation)
				})
			}
			require.NoError(t, receipt())
			require.NoError(t, receipt(), "exact replay must not append another event")
			rows, err = reader(t.Context(), f.db, 0, 200)
			require.NoError(t, err)
			require.Len(t, rows, 2)
			require.Equal(t, "execution.merge_requested", rows[0].Type)
			terminal := "execution.merge_finished"
			if outcome == "MERGED" {
				terminal = "execution.merged"
			}
			require.Equal(t, terminal, rows[1].Type)
			require.Equal(t, guid.ID(11), rows[0].ActorID)
			require.Zero(t, rows[1].ActorID)
			for _, row := range rows {
				require.Equal(t, guid.ID(7), row.TenantID)
				require.Equal(t, guid.ID(9), row.ProjectID)
				require.Equal(t, id, row.SubjectID)
				require.Equal(t, "execution", row.SubjectKind)
				var detail map[string]any
				require.NoError(t, json.Unmarshal([]byte(row.Data), &detail))
				require.Equal(t, created.ID.String(), detail["merge_id"])
			}
		})
	}
}

func TestMergeReceiptRejectsUntrustedOutcomeAndUnavailableSlotRelease(t *testing.T) {
	for _, fault := range []string{"runner", "plan", "state", "signature", "merge_hash", "source_hash", "unrequested_push", "conflict_commit", "release_missing", "release_error", "time"} {
		t.Run(fault, func(t *testing.T) {
			f, id, head := mergeFixture(t)
			var created MergeView
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var e error
				created, e = f.m.RequestMergeOn(f.human, tx, 7, id, MergeRequest{Reason: "Integrate verified result"})
				return e
			}))
			var plans []MergePlan
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; plans, e = f.m.pullMergeOn(f.runner, tx, 7, f.fence); return e }))
			require.Len(t, plans, 1)
			raw, e := json.Marshal(plans[0])
			require.NoError(t, e)
			now := time.Now().UTC()
			o := MergeObservation{PlanHash: hash(raw), SourceCommit: head, State: "MERGED", MergeCommit: strings.Repeat("c", 40), StartedAt: now, EndedAt: now}
			expected := ErrInvalidArgument
			failure := fmt.Errorf("slot release failed")
			switch fault {
			case "runner":
				f.fence.SessionID++
				expected = ErrFenced
			case "plan":
				require.NoError(t, f.m.mergeTable(f.db).Where("id=?", created.ID).Update("plan_json", "{").Error)
				expected = ErrUnavailable
			case "state":
				o.State = "SUCCESS"
			case "signature":
				o.Contained = true
				expected = ErrFenced
			case "merge_hash":
				o.MergeCommit = "HEAD"
			case "source_hash":
				o.MergeCommit = head
			case "unrequested_push":
				o.Pushed = true
			case "conflict_commit":
				o.State = "CONFLICT"
			case "release_missing":
				f.m.machines.ReleaseSlotOn = nil
				expected = ErrCapabilityUnavailable
			case "release_error":
				f.m.machines.ReleaseSlotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure }
				expected = failure
			case "time":
				o.EndedAt = now.Add(-time.Second)
				expected = ErrFenced
			}
			var before model.Merge
			require.NoError(t, f.m.mergeTable(f.db).Where("id=?", created.ID).First(&before).Error)
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.completeMergeOn(f.runner, tx, 7, created.ID, f.fence, plans[0].Nonce, o)
			}), expected)
			var after model.Merge
			require.NoError(t, f.m.mergeTable(f.db).Where("id=?", created.ID).First(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func mergeFixture(t *testing.T) (*executionFixture, guid.ID, string) {
	f, in := verificationFixture(t)
	id := in.ExecutionID
	r, err := f.m.rowOnForMergeTest(f, id)
	require.NoError(t, err)
	v, err := view(r)
	require.NoError(t, err)
	v.Plan.AuthorizationMode = "WORKSPACE_WRITE"
	v.Plan.ExecutorPrincipalID = 12
	raw, _ := json.Marshal(v.Plan)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)}).Error)
	f.m.mergeGuard = func(ctx context.Context, tx *gorm.DB, tenant, project, work, execution, repository, producer guid.ID) (delivery.CodeMergeFacts, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(9), project)
		require.Equal(t, id, execution)
		require.Equal(t, guid.ID(4), repository)
		if !f.authorized {
			return delivery.CodeMergeFacts{}, delivery.ErrWorkForbidden
		}
		return delivery.CodeMergeFacts{HeadCommit: in.HeadCommit, DefaultBranch: "main", AuthoritySource: "ROLE:test", ActorKind: "HUMAN"}, nil
	}
	return f, id, in.HeadCommit
}
func (m *Module) rowOnForMergeTest(f *executionFixture, id guid.ID) (*model.Execution, error) {
	return query.BindExecutions(f.db, m.names).WithContext(f.human).Where(query.BindExecutions(f.db, m.names).ID.Eq(id)).First()
}
func TestCodeMergeCurrentGuardPinnedReceiptReplayAndImmutableOutcome(t *testing.T) {
	f, id, head := mergeFixture(t)
	var created MergeView
	request := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			created, e = f.m.RequestMergeOn(f.human, tx, 7, id, MergeRequest{Reason: "Accepted verified integration"})
			return e
		})
	}
	require.NoError(t, request())
	first := created
	require.Equal(t, "PENDING", created.State)
	require.Equal(t, head, created.SourceCommit)
	require.ErrorIs(t, request(), ErrConflict)
	var plans []MergePlan
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; plans, e = f.m.pullMergeOn(f.runner, tx, 7, f.fence); return e }))
	require.Len(t, plans, 1)
	raw, _ := json.Marshal(plans[0])
	now := time.Now().UTC()
	o := MergeObservation{PlanHash: hash(raw), SourceCommit: head, State: "MERGED", MergeCommit: strings.Repeat("c", 40), Reason: "MERGE_COMMITTED", StartedAt: now, EndedAt: now}
	receipt := func(nonce string, o MergeObservation) error {
		return f.db.Transaction(func(tx *gorm.DB) error { return f.m.completeMergeOn(f.runner, tx, 7, first.ID, f.fence, nonce, o) })
	}
	require.ErrorIs(t, receipt("wrong", o), ErrFenced)
	require.NoError(t, receipt(plans[0].Nonce, o))
	require.NoError(t, receipt(plans[0].Nonce, o))
	changed := o
	changed.Pushed = true
	require.ErrorIs(t, receipt(plans[0].Nonce, changed), ErrConflict)
	var stored model.Merge
	require.NoError(t, f.m.mergeTable(f.db).Where("id=?", first.ID).First(&stored).Error)
	v, e := mergeView(&stored)
	require.NoError(t, e)
	require.Equal(t, "MERGED", v.State)
	require.Equal(t, o.MergeCommit, v.Observation.MergeCommit)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Where("execution_id=? AND content=?", id, o.MergeCommit).Count(&count).Error)
	require.Equal(t, int64(1), count)
	require.ErrorIs(t, request(), ErrConflict)
}
func TestCodeMergeRevocationDeadlineAndOutboxFailureNeverCreateSuccess(t *testing.T) {
	for _, mode := range []string{"revoke", "deadline", "outbox", "readonly", "missing_evidence", "malformed_branch"} {
		t.Run(mode, func(t *testing.T) {
			f, id, _ := mergeFixture(t)
			if mode == "readonly" {
				r, e := f.m.rowOnForMergeTest(f, id)
				require.NoError(t, e)
				v, e := view(r)
				require.NoError(t, e)
				v.Plan.AuthorizationMode = "READ_ONLY"
				b, _ := json.Marshal(v.Plan)
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", id).Updates(map[string]any{"plan_json": string(b), "plan_hash": hash(b)}).Error)
			}
			if mode == "missing_evidence" {
				require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Where("execution_id=?", id).Delete(&model.Evidence{}).Error)
			}
			if mode == "outbox" {
				f.pub.fail = true
			}
			in := MergeRequest{Reason: "Required guard test"}
			if mode == "malformed_branch" {
				in.TargetBranch = "--upload-pack=evil"
			}
			var v MergeView
			e := f.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = f.m.RequestMergeOn(f.human, tx, 7, id, in); return e })
			if mode == "outbox" || mode == "readonly" || mode == "missing_evidence" || mode == "malformed_branch" {
				require.Error(t, e)
				var n int64
				require.NoError(t, f.m.mergeTable(f.db).Count(&n).Error)
				require.Zero(t, n)
				return
			}
			require.NoError(t, e)
			if mode == "revoke" {
				f.authorized = false
			} else {
				require.NoError(t, f.m.mergeTable(f.db).Where("id=?", v.ID).Update("deadline_at", time.Now().Add(-time.Second)).Error)
			}
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				plans, e := f.m.pullMergeOn(f.runner, tx, 7, f.fence)
				require.Empty(t, plans)
				return e
			}))
			var r model.Merge
			require.NoError(t, f.m.mergeTable(f.db).Where("id=?", v.ID).First(&r).Error)
			require.Equal(t, "FAILED", r.State)
			out, e := mergeView(&r)
			require.NoError(t, e)
			require.Empty(t, out.Observation.MergeCommit)
		})
	}
}

func TestCodeMergeWatchdogUnknownOutcomeReleasesSlotAtomically(t *testing.T) {
	for _, leased := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "leased"}[leased], func(t *testing.T) {
			f, id, _ := mergeFixture(t)
			var created MergeView
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				var err error
				created, err = f.m.RequestMergeOn(f.human, tx, 7, id, MergeRequest{Reason: "Recover unknown outcome"})
				return err
			}))
			if leased {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					plans, err := f.m.pullMergeOn(f.runner, tx, 7, f.fence)
					require.Len(t, plans, 1)
					return err
				}))
			}
			now := time.Now().UTC().Add(time.Hour)
			f.pub.fail = true
			require.Error(t, f.m.expireMerges(f.runner, f.db, 7, now))
			var before model.Merge
			require.NoError(t, f.m.mergeTable(f.db).Where("id=?", created.ID).First(&before).Error)
			require.Nil(t, before.FinishedAt)
			f.pub.fail = false
			require.NoError(t, f.m.expireMerges(f.runner, f.db, 7, now))
			var stored model.Merge
			require.NoError(t, f.m.mergeTable(f.db).Where("id=?", created.ID).First(&stored).Error)
			require.Equal(t, "FAILED", stored.State)
			require.Nil(t, stored.ActiveExecutionID)
			out, err := mergeView(&stored)
			require.NoError(t, err)
			require.Equal(t, "DEADLINE_EXCEEDED_OUTCOME_UNKNOWN", out.Observation.Reason)
			require.Empty(t, out.Observation.MergeCommit)
			require.NoError(t, f.m.expireMerges(f.runner, f.db, 7, now))
		})
	}
}

func TestCodeMergeEveryStorageFailureRollsBackCommandAndObservedOutcome(t *testing.T) {
	for _, command := range []string{"request", "pull", "complete"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f, id, head := mergeFixture(t)
				var created MergeView
				var plans []MergePlan
				if command != "request" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						var e error
						created, e = f.m.RequestMergeOn(f.human, tx, 7, id, MergeRequest{Reason: "Accepted verified integration"})
						return e
					}))
				}
				if command == "complete" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; plans, e = f.m.pullMergeOn(f.runner, tx, 7, f.fence); return e }))
					require.Len(t, plans, 1)
				}
				return f, func(tx *gorm.DB) error {
					switch command {
					case "request":
						out, e := f.m.RequestMergeOn(f.human, tx, 7, id, MergeRequest{Reason: "Accepted verified integration"})
						if e == nil {
							require.Equal(t, "PENDING", out.State)
							require.Equal(t, head, out.SourceCommit)
						}
						return e
					case "pull":
						out, e := f.m.pullMergeOn(f.runner, tx, 7, f.fence)
						if e == nil {
							require.Len(t, out, 1)
							require.Equal(t, head, out[0].SourceCommit)
						}
						return e
					default:
						raw, e := json.Marshal(plans[0])
						require.NoError(t, e)
						now := time.Now().UTC()
						return f.m.completeMergeOn(f.runner, tx, 7, created.ID, f.fence, plans[0].Nonce, MergeObservation{PlanHash: hash(raw), SourceCommit: head, State: "CONFLICT", ConflictFiles: []string{"feature.go"}, Reason: "Observed conflict", StartedAt: now, EndedAt: now})
					}
				}
			})
		})
	}
}

func TestInjectCodeMergeRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.injectCodeMerge(module.NewServices()), module.ErrServicesProviding)
}

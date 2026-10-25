package execution

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExecutionKnowledgeCountsDeliveryAndPersistedOutputNotReferences(t *testing.T) {
	f, item := executionKnowledgeFixture(t)
	var count int64
	counts := func() int64 { require.NoError(t, f.db.Table("usage_probe").Count(&count).Error); return count }
	in := validCreate()
	in.Plan.Knowledge = []knowledge.ContextItem{{EntryID: 999, Body: "Posted impostor"}}
	var execution ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var err error; execution, err = f.m.CreateOn(f.human, tx, 7, in); return err }))
	require.Equal(t, []knowledge.ContextItem{item}, execution.Plan.Knowledge)
	require.EqualValues(t, 0, counts())
	f.pull(t, execution.ID)
	require.EqualValues(t, 1, counts())
	_, err := f.receipt(execution.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(execution.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "OBSERVATION", EvidenceTitle: "No citation", Content: "Unknown [K2]"})
	require.NoError(t, err)
	require.EqualValues(t, 1, counts())
	cite := ReceiptInput{DispatchEpoch: 1, Seq: 3, Kind: "EVIDENCE", EvidenceKind: "OBSERVATION", EvidenceTitle: "Model answer", Content: "Source [K1] supports this answer."}
	_, err = f.receipt(execution.ID, cite)
	require.NoError(t, err)
	require.EqualValues(t, 2, counts())
	replay, err := f.receipt(execution.ID, cite)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.EqualValues(t, 2, counts())
	bad := validCreate()
	bad.Plan.ContextRefs = []ContextRef{{Kind: "KNOWLEDGE", Ref: 999}}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.CreateOn(f.human, tx, 7, bad); return err }), ErrInvalidArgument)
}

func TestExecutionKnowledgePlanFitsMySQLText(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", 8000), strings.Repeat("界", 2500), strings.Repeat(`"`, 4000)} {
		t.Run(fmt.Sprintf("body-%d-bytes", len(body)), func(t *testing.T) {
			f := executionHarness(t)
			f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", Roles: []string{"EXECUTOR"}, RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "family", AuthorizationMode: "READ_ONLY"}, true, nil
			}
			ids := []guid.ID{90, 91, 92, 93, 94, 95}
			f.m.knowledge.work = delivery.WorkKnowledgeContext{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return ids, nil }}
			f.m.knowledge.context.ResolveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]knowledge.ContextItem, error) {
				items := make([]knowledge.ContextItem, len(ids))
				for i, id := range ids {
					items[i] = knowledge.ContextItem{EntryID: id, Version: 1, Label: fmt.Sprintf("[K%d]", i+1), Title: "Policy", Body: body, ContentHash: hash([]byte("Policy\n" + body))}
				}
				return items, nil
			}
			baseline := f.create(t)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.CancelOn(f.human, tx, 7, baseline.ID, baseline.Version)
				return err
			}))
			plan := baseline.Plan
			plan.Objective = ""
			encoded, err := json.Marshal(plan)
			require.NoError(t, err)
			padding := 65535 - len(encoded)
			require.Positive(t, padding)
			require.LessOrEqual(t, padding, 20000)
			in := validCreate()
			in.Plan.Objective = strings.Repeat("a", padding)
			var bounded ExecutionView
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var err error; bounded, err = f.m.CreateOn(f.human, tx, 7, in); return err }))
			actual, err := json.Marshal(bounded.Plan)
			require.NoError(t, err)
			require.Len(t, actual, 65535)
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.CancelOn(f.human, tx, 7, bounded.ID, bounded.Version)
				return err
			}))
			var before int64
			require.NoError(t, f.db.Table("slots").Count(&before).Error)
			in.Plan.Objective += "a"
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.CreateOn(f.human, tx, 7, in); return err }), ErrInvalidArgument)
			var after int64
			require.NoError(t, f.db.Table("slots").Count(&after).Error)
			require.Equal(t, before, after, "oversized snapshot must fail before slot reservation")
		})
	}
}

func executionKnowledgeFixture(t *testing.T) (*executionFixture, knowledge.ContextItem) {
	t.Helper()
	f := executionHarness(t)
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", Roles: []string{"EXECUTOR"}, RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "family", AuthorizationMode: "READ_ONLY"}, true, nil
	}
	item := knowledge.ContextItem{EntryID: 90, Version: 1, Label: "[K1]", Title: "Policy", Body: "Keep evidence", ContentHash: hash([]byte("Policy\nKeep evidence"))}
	f.m.knowledge.work = delivery.WorkKnowledgeContext{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
		return []guid.ID{90}, nil
	}}
	f.m.knowledge.context = knowledge.RuntimeContext{ResolveOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]knowledge.ContextItem, error) {
		return []knowledge.ContextItem{item}, nil
	}}
	require.NoError(t, f.db.Exec("CREATE TABLE usage_probe (execution_id BIGINT, entry_id BIGINT, kind TEXT, evidence_id BIGINT, UNIQUE(execution_id,entry_id,kind,evidence_id))").Error)
	f.m.knowledge.recorder = knowledge.UsageRecorder{RecordOn: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, fact knowledge.UsageFact) error {
		if err := f.m.verifyKnowledgeUsageOn(ctx, tx, tenant, fact); err != nil {
			return err
		}
		return tx.Exec("INSERT OR IGNORE INTO usage_probe VALUES (?,?,?,?)", fact.ExecutionID, fact.EntryID, fact.Kind, fact.EvidenceID).Error
	}}

	return f, item
}

func TestExecutionKnowledgeUsageRejectsUnleasedAndUnrelatedPersistedOutput(t *testing.T) {
	for _, fault := range []string{"unleased", "principal", "tenant", "entry", "hash", "missing_evidence", "uncited_output", "corrupted_output", "source_unavailable"} {
		t.Run(fault, func(t *testing.T) {
			f, item := executionKnowledgeFixture(t)
			v := f.create(t)
			ctx := executorContext(f.human, 7, 21)
			fact := knowledge.UsageFact{ExecutionID: v.ID, EntryID: item.EntryID, ContentHash: item.ContentHash, Kind: "INCLUSION", EvidenceID: v.ID}
			if fault != "unleased" {
				f.pull(t, v.ID)
			}
			switch fault {
			case "principal":
				ctx = f.human
			case "tenant":
				ctx = executorContext(f.human, 8, 21)
			case "entry":
				fact.EntryID = 999
			case "hash":
				fact.ContentHash = strings.Repeat("0", 64)
			case "missing_evidence":
				fact.Kind = "CITATION"
				fact.EvidenceID = 999
			case "uncited_output", "corrupted_output":
				_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
				require.NoError(t, e)
				text := "No source citation"
				if fault == "corrupted_output" {
					text = "Actual [K1] source citation"
				}
				_, e = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "EVIDENCE", EvidenceKind: "OBSERVATION", EvidenceTitle: "Observed response", Content: text})
				require.NoError(t, e)
				var output struct{ ID guid.ID }
				require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Select("id").Where("execution_id=? AND title=?", v.ID, "Observed response").First(&output).Error)
				fact.Kind = "CITATION"
				fact.EvidenceID = output.ID
				if fault == "corrupted_output" {
					require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Where("id=?", output.ID).Update("content_hash", strings.Repeat("0", 64)).Error)
				}
			case "source_unavailable":
				fact.EvidenceID = 999
				f.m.knowledge.agentEvidence.VerifyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, string) error {
					return errors.New("current source unavailable")
				}
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.verifyKnowledgeUsageOn(ctx, tx, 7, fact) }))
		})
	}
}

func TestKnowledgeRuntimeBindingRequiresCompleteEvidenceProviders(t *testing.T) {
	for _, stage := range []string{"unsealed", "work_missing", "recorder_missing", "usage_missing", "usage_invalid", "usage_error", "agent_missing", "agent_invalid", "agent_error", "bound"} {
		t.Run(stage, func(t *testing.T) {
			f, item := executionKnowledgeFixture(t)
			s := module.NewServices()
			failure := errors.New("evidence provider rejected binding")
			require.NoError(t, module.Provide(s, f.m.knowledge.context))
			if stage != "work_missing" {
				require.NoError(t, module.Provide(s, f.m.knowledge.work))
			}
			if stage != "recorder_missing" {
				require.NoError(t, module.Provide(s, f.m.knowledge.recorder))
			}
			var usage knowledge.UsageEvidence
			var read agents.ReadContext
			connector := knowledge.UsageEvidenceConnector{Bind: func(v knowledge.UsageEvidence) error {
				usage = v
				if stage == "usage_error" {
					return failure
				}
				return nil
			}}
			if stage == "usage_invalid" {
				connector.Bind = nil
			}
			if stage != "usage_missing" {
				require.NoError(t, module.Provide(s, connector))
			}
			agent := agents.ContextConnector{Evidence: agents.ContextEvidence{VerifyOn: func(ctx context.Context, tx *gorm.DB, tenant, agent, execution, evidence guid.ID, digest string) error {
				var row struct{ ID guid.ID }
				return tx.Table("context_probe").Where("id=? AND execution_id=? AND digest=?", evidence, execution, digest).First(&row).Error
			}}, Bind: func(v agents.ReadContext) error {
				read = v
				if stage == "agent_error" {
					return failure
				}
				return nil
			}}
			if stage == "agent_invalid" {
				agent.Bind = nil
			}
			if stage != "agent_missing" {
				require.NoError(t, module.Provide(s, agent))
			}
			if stage != "unsealed" {
				require.NoError(t, s.Seal())
			}
			e := f.m.injectKnowledgeRuntime(s)
			switch stage {
			case "unsealed":
				require.ErrorIs(t, e, module.ErrServicesProviding)
			case "work_missing", "recorder_missing", "usage_missing":
				require.ErrorIs(t, e, module.ErrServiceMissing)
			case "usage_invalid", "agent_invalid":
				require.ErrorIs(t, e, ErrCapabilityUnavailable)
			case "usage_error", "agent_error":
				require.ErrorIs(t, e, failure)
			case "agent_missing":
				require.NoError(t, e)
				require.NotNil(t, usage.VerifyOn)
			case "bound":
				require.NoError(t, e)
				require.NotNil(t, read)
				require.NotNil(t, usage.VerifyOn)
				require.NoError(t, f.db.Exec("CREATE TABLE context_probe(id BIGINT PRIMARY KEY,execution_id BIGINT,digest TEXT)").Error)
				v := f.create(t)
				f.m.knowledge.context.ValidateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []knowledge.ContextItem) error { return nil }
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					got, e := read(executorContext(f.human, 7, 21), tx, 7, 21, v.ID, func(id guid.ID, digest string) (guid.ID, error) {
						require.Equal(t, v.ID, id)
						require.Equal(t, v.PlanHash, digest)
						return 8001, tx.Exec("INSERT INTO context_probe VALUES (?,?,?)", 8001, id, digest).Error
					})
					require.NoError(t, e)
					require.Equal(t, []knowledge.ContextItem{item}, got)
					return e
				}))
				var count int64
				require.NoError(t, f.db.Table("usage_probe").Count(&count).Error)
				require.EqualValues(t, 1, count)
			}
		})
	}
}

func TestAgentKnowledgeAssemblyFailuresCannotPersistUsage(t *testing.T) {
	for _, fault := range []string{"missing_work", "missing_validation", "principal_lock", "work_error", "validation", "persist", "recorder_missing", "recorder_error", "empty"} {
		t.Run(fault, func(t *testing.T) {
			f, _ := executionKnowledgeFixture(t)
			v := f.create(t)
			failure := errors.New("knowledge assembly unavailable")
			f.m.knowledge.context.ValidateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []knowledge.ContextItem) error {
				if fault == "validation" {
					return failure
				}
				return nil
			}
			switch fault {
			case "missing_work":
				f.m.knowledge.work.ReadOn = nil
			case "missing_validation":
				f.m.knowledge.context.ValidateOn = nil
			case "principal_lock":
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "work_error":
				f.m.knowledge.work.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return nil, failure }
			case "recorder_missing":
				f.m.knowledge.recorder.RecordOn = nil
			case "recorder_error":
				f.m.knowledge.recorder.RecordOn = func(context.Context, *gorm.DB, guid.ID, knowledge.UsageFact) error { return failure }
			case "empty":
				v.Plan.Knowledge = nil
				raw, e := json.Marshal(v.Plan)
				require.NoError(t, e)
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)}).Error)
			}
			persisted := false
			e := f.db.Transaction(func(tx *gorm.DB) error {
				_, e := f.m.agentKnowledgeOn(executorContext(f.human, 7, 21), tx, 7, 21, v.ID, func(guid.ID, string) (guid.ID, error) {
					persisted = true
					if fault == "persist" {
						return 0, failure
					}
					return 8002, nil
				})
				return e
			})
			if fault == "empty" {
				require.NoError(t, e)
				require.False(t, persisted)
			} else {
				require.Error(t, e)
			}
			var count int64
			require.NoError(t, f.db.Table("usage_probe").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestInjectKnowledgeRuntimeRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.injectKnowledgeRuntime(module.NewServices()), module.ErrServicesProviding)
}

package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestKnowledgeConnectorsBindActualReadersAndPropagateRegistrationFailures(t *testing.T) {
	m, db, _, _ := harness(t)
	for _, fault := range []string{"retirement", "performance", "roster", "acceptance", "documents", "success"} {
		t.Run(fault, func(t *testing.T) {
			s := module.NewServices()
			failure := errors.New("connector rejected duplicate binding")
			var acceptance knowledge.ClientAcceptanceReader
			var documents knowledge.EventSourceReader
			var retirement delivery.RetirementDispatch
			rosterCalled := false
			require.NoError(t, module.Provide[delivery.RetirementDispatchConnector](s, delivery.RetirementDispatchConnector{Bind: func(p delivery.RetirementDispatch) error {
				retirement = p
				if fault == "retirement" {
					return failure
				}
				return nil
			}}))
			require.NoError(t, module.Provide[agents.AgentPerformanceConnector](s, agents.AgentPerformanceConnector{BindHistory: func(read func(context.Context, *gorm.DB, guid.ID, guid.ID, int, string, time.Time) ([]agents.AgentPerformanceMeasures, error)) error {
				require.NotNil(t, read)
				if fault == "performance" {
					return failure
				}
				return nil
			}}))
			require.NoError(t, module.Provide[agents.AgentRosterAnalyticsConnector](s, agents.AgentRosterAnalyticsConnector{Bind: func(read func(context.Context, *gorm.DB, guid.ID, []guid.ID) (map[guid.ID]agents.RosterAnalytics, error)) error {
				rosterCalled = true
				require.NotNil(t, read)
				if fault == "roster" {
					return failure
				}
				return nil
			}}))
			require.NoError(t, module.Provide[knowledge.ClientAcceptanceReaderConnector](s, knowledge.ClientAcceptanceReaderConnector{Bind: func(p knowledge.ClientAcceptanceReader) error {
				acceptance = p
				if fault == "acceptance" {
					return failure
				}
				return nil
			}}))
			require.NoError(t, module.Provide[knowledge.EventSourceReaderConnector](s, knowledge.EventSourceReaderConnector{Bind: func(p knowledge.EventSourceReader) error {
				documents = p
				if fault == "documents" {
					return failure
				}
				return nil
			}}))
			require.NoError(t, s.Seal())
			err := m.bindKnowledgeAcceptance(s)
			if fault != "success" {
				require.ErrorIs(t, err, failure)
				return
			}
			require.NoError(t, err)
			require.True(t, rosterCalled)
			require.NotNil(t, retirement.TransferOn)
			require.NotNil(t, acceptance.ReadOn)
			require.NotNil(t, documents.ReadOn)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				docs, e := documents.ReadOn(t.Context(), tx, events.Envelope{EventType: "unrelated"})
				require.Empty(t, docs)
				return e
			}))
		})
	}
}

func TestKnowledgeAcceptanceSourceFailuresNeverYieldAcceptedWork(t *testing.T) {
	m, db, _, _ := harness(t)
	event := events.Envelope{TenantID: 1, ProjectID: 3, Subject: events.Subject{ID: 80}, Data: json.RawMessage(`{"receipt_sha256":"` + strings.Repeat("a", 64) + `"}`)}
	_, err := m.knowledgeAcceptedWorkOn(t.Context(), db, event)
	require.ErrorIs(t, err, ErrInvalid)
	for _, raw := range []json.RawMessage{json.RawMessage(`{`), json.RawMessage(`{"receipt_sha256":"short"}`), event.Data} {
		event.Data = raw
		err = db.Transaction(func(tx *gorm.DB) error {
			ids, e := m.knowledgeAcceptedWorkOn(t.Context(), tx, event)
			require.Empty(t, ids)
			return e
		})
		require.Error(t, err)
	}
	manifest := packageManifest{Content: PackageContent{Artifacts: []delivery.ClientArtifact{{VersionID: 40}}}, Refs: []delivery.ClientArtifactRef{{WorkItemID: 11}}}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	pack := model.DeliveryPackage{TenantID: 1, ProjectID: 3, ID: 80, Manifest: string(raw), ContentHash: strings.Repeat("b", 64), State: "ACCEPTED", Version: 3, RecipientID: 4, ActorID: 2, CreatedAt: now(), UpdatedAt: now()}
	item := packageItems(&pack, manifest.Content)[0].ItemID
	valid, err := json.Marshal([]PackageItemDecision{{ItemID: item, Result: "ACCEPTED"}})
	require.NoError(t, err)
	text := string(valid)
	sha := strings.Repeat("a", 64)
	receipt := model.PackageReceipt{TenantID: 1, ProjectID: 3, ID: 90, PackageID: 80, ContentHash: pack.ContentHash, ReceiptSHA256: &sha, Decision: "ACCEPT", ItemOutcomes: &text, CreatedAt: now()}
	require.NoError(t, db.Table(m.names.PackageReceipt.String()).Create(&receipt).Error)
	err = db.Transaction(func(tx *gorm.DB) error { _, e := m.knowledgeAcceptedWorkOn(t.Context(), tx, event); return e })
	require.Error(t, err, "receipt without frozen package is not an acceptance")
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Create(&pack).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ids, e := m.knowledgeAcceptedWorkOn(t.Context(), tx, event)
		require.Equal(t, []guid.ID{11}, ids)
		return e
	}))
	for _, bad := range []string{"{", `[]`, `[{"item_id":"foreign","result":"ACCEPTED"}]`, `[{"item_id":"` + item + `","result":"MAYBE"}]`, `[{"item_id":"` + item + `","result":"ACCEPTED"},{"item_id":"` + item + `","result":"REJECTED"}]`} {
		receipt.ItemOutcomes = &bad
		ids, e := acceptedWorkFromReceipt(&pack, &receipt)
		require.ErrorIs(t, e, ErrInvalid)
		require.Nil(t, ids)
	}
}

func TestRetirementDispatchProducesGenuineProposalDecisionAndAssignmentAtomically(t *testing.T) {
	for _, fault := range []string{"", "authority", "version", "audit", "apply", "kind", "tenant", "transaction"} {
		t.Run(fault, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			assignment := delivery.RetirementAssignment{AssignmentID: 70, ProjectID: 3, WorkItemID: 11, WorkVersion: 1, AssignmentVersion: 1}
			in := delivery.RetirementTransferInput{RetirementID: 900, ArtifactID: 901, AssigneePrincipalID: 21, AssigneeKind: "AGENT"}
			switch fault {
			case "authority":
				m.dispatch.AuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error) {
					return "", ErrForbidden
				}
			case "version":
				assignment.WorkVersion++
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			case "apply":
				m.dispatch.ApplyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.DispatchAssignmentInput) (guid.ID, error) {
					return 0, errors.New("assignment failure")
				}
			case "kind":
				in.AssigneeKind = "OTHER"
			}
			if fault == "transaction" {
				_, err := m.retirementDispatchOn(ctx, db, 1, assignment, in)
				require.ErrorIs(t, err, ErrInvalid)
				return
			}
			tenant := guid.ID(1)
			if fault == "tenant" {
				tenant = 99
			}
			var assigned guid.ID
			err := db.Transaction(func(tx *gorm.DB) error {
				var e error
				assigned, e = m.retirementDispatchOn(ctx, tx, tenant, assignment, in)
				return e
			})
			for _, table := range []string{m.names.DispatchProposal.String(), m.names.CommandDecision.String(), m.names.DispatchAction.String(), "baseline_probe"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				if fault != "" {
					require.Error(t, err)
					require.Zero(t, count, "failure rolls back "+table)
				} else {
					require.NoError(t, err)
					require.EqualValues(t, 1, count, table)
				}
			}
			if fault != "" {
				return
			}
			require.EqualValues(t, 88, assigned)
			q := query.BindDispatchProposal(db, m.names)
			proposal, err := q.WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, "REDISPATCH", proposal.Trigger)
			require.Equal(t, "OVERRIDDEN", proposal.State)
			require.Contains(t, *proposal.Reason, "900; artifact: 901")
			d := query.BindCommandDecision(db, m.names)
			decision, err := d.WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, "REASSIGN", decision.Action)
			require.Equal(t, proposal.ID, decision.SourceID)
			require.EqualValues(t, 11, decision.TargetID)
			a := query.BindDispatchAction(db, m.names)
			action, err := a.WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, decision.ID, action.ID)
			require.EqualValues(t, 21, *action.SelectedID)
			require.EqualValues(t, 88, *action.AssignmentID)
		})
	}
}

func TestAgentRosterAnalyticsUsesThirtyDaysAndActualHumanClaimActors(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	at := now()
	ex := guid.ID(101)
	m.dispatchDecisions.ReadOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, kind string, _ time.Time) ([]delivery.DispatchHistoryWork, error) {
		if kind != "TASK" {
			return nil, nil
		}
		return []delivery.DispatchHistoryWork{
			{ProjectID: 3, WorkItemID: 11, Claims: []delivery.DispatchClaimFact{{ID: 1, ExecutionID: &ex, CreatedAt: at.Add(-time.Hour)}, {ID: 2, ActorID: 2, CreatedAt: at.Add(-time.Hour)}}, Decisions: []delivery.DispatchDecisionFact{{Action: "APPROVE", CreatedAt: at}}},
			{ProjectID: 3, WorkItemID: 12, Claims: []delivery.DispatchClaimFact{{ID: 3, ActorID: 2, CreatedAt: at.Add(-40 * 24 * time.Hour)}}, Decisions: []delivery.DispatchDecisionFact{{Action: "RETURN", CreatedAt: at.Add(-40 * 24 * time.Hour)}}},
		}, nil
	}
	m.dispatchExecutions.ForWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) ([]execution.DispatchExecutionFact, error) {
		return []execution.DispatchExecutionFact{{ID: ex, WorkItemID: 11, ExecutorPrincipalID: 21}}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		got, e := m.agentRosterAnalyticsOn(testContext(t, 2), tx, 1, []guid.ID{2, 21, 99})
		require.NoError(t, e)
		require.EqualValues(t, 1, got[2].FirstDecisions)
		require.EqualValues(t, 1, got[2].FirstPassAccepts)
		require.EqualValues(t, 1, got[21].FirstDecisions)
		require.EqualValues(t, 1, got[21].FirstPassAccepts)
		require.Zero(t, got[99].FirstDecisions)
		_, e = m.agentRosterAnalyticsOn(testContext(t, 2), tx, 1, []guid.ID{0})
		require.ErrorIs(t, e, ErrInvalid)
		return nil
	}))
}

func TestKnowledgeAcceptanceSkipsLegacyEventsWithoutSignedReceiptMetadata(t *testing.T) {
	m, db, _, _ := harness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ids, err := m.knowledgeAcceptedWorkOn(t.Context(), tx, events.Envelope{TenantID: 1, ProjectID: 3, Subject: events.Subject{ID: 100}, Data: json.RawMessage(`{"decision":"ACCEPT"}`)})
		require.Empty(t, ids)
		return err
	}))
}

func TestKnowledgeCancellationRendersImmutableDecisionAndRejectsForeignProject(t *testing.T) {
	m, db, _, _ := harness(t)
	at := time.Now().UTC()
	decision := model.CommandDecision{TenantID: 1, ID: 601, ProjectID: 3, TargetKind: "PROJECT", TargetID: 3, Action: "CANCEL", SourceID: 602, SourceVersion: 1, ActorID: 2, Reason: "Cancel this project because the independent evidence invalidates its original scope.", AuthoritySource: "IAM:project_cancel", CreatedAt: at}
	require.NoError(t, query.BindCommandDecision(db, m.names).WithContext(t.Context()).Create(&decision))
	event := events.Envelope{TenantID: 1, ProjectID: 3, EventType: "project.stage_changed", Subject: events.Subject{Kind: "project", ID: 3}, Actor: events.Actor{ID: 2}, OccurredAt: at.Add(time.Second), Data: json.RawMessage(`{"to_stage":"CANCELLED","decision_id":"601","reason":"Untrusted event text"}`)} //nolint:misspell // Preserve the published domain or serialized contract spelling.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		docs, err := m.knowledgeCancellationDocumentsOn(t.Context(), tx, event)
		require.NoError(t, err)
		require.Len(t, docs, 1)
		require.Equal(t, decision.ID, docs[0].OriginID)
		require.Contains(t, docs[0].Content, decision.Reason)
		require.NotContains(t, docs[0].Content, "Untrusted event text")
		return err
	}))
	event.ProjectID = 4
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.knowledgeCancellationDocumentsOn(t.Context(), tx, event)
		return err
	}))
}

func TestKnowledgeAcceptanceUsesExactSignedItemOutcomesAndFrozenPrivateReferences(t *testing.T) {
	manifest := packageManifest{Content: PackageContent{Artifacts: []delivery.ClientArtifact{{Name: "Accepted"}, {Name: "Rejected"}, {Name: "Also accepted"}}}, Refs: []delivery.ClientArtifactRef{{WorkItemID: 10}, {WorkItemID: 20}, {WorkItemID: 30}}}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	pack := &model.DeliveryPackage{TenantID: 1, ProjectID: 99, ID: 100, Manifest: string(raw), ContentHash: strings.Repeat("a", 64)}
	items := packageItems(pack, manifest.Content)
	outcomes := []PackageItemDecision{{ItemID: items[0].ItemID, Result: "ACCEPTED"}, {ItemID: items[1].ItemID, Result: "REJECTED"}, {ItemID: items[2].ItemID, Result: "ACCEPTED"}}
	encoded, err := json.Marshal(outcomes)
	require.NoError(t, err)
	text := string(encoded)
	receipt := &model.PackageReceipt{TenantID: 1, ProjectID: 99, PackageID: 100, ContentHash: pack.ContentHash, Decision: "PARTIALLY_ACCEPTED", ItemOutcomes: &text}
	ids, err := acceptedWorkFromReceipt(pack, receipt)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{10, 30}, ids)
	receipt.Decision = "REJECT"
	ids, err = acceptedWorkFromReceipt(pack, receipt)
	require.NoError(t, err)
	require.Empty(t, ids)
	receipt.Decision = "PARTIALLY_ACCEPTED"
	receipt.ContentHash = strings.Repeat("b", 64)
	_, err = acceptedWorkFromReceipt(pack, receipt)
	require.ErrorIs(t, err, ErrInvalid)
	receipt.ContentHash = pack.ContentHash
	receipt.ItemOutcomes = nil
	_, err = acceptedWorkFromReceipt(pack, receipt)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestChannelDispatchBridgeRequiresCurrentLeadAndHumanConfirmationAtomically(t *testing.T) {
	for _, scenario := range []string{"success", "not_lead", "card_failure", "check_only"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			old := m.dispatchFleet.ListOn
			m.dispatchFleet.ListOn = func(ctx context.Context, tx *gorm.DB, tenant guid.ID) ([]fleet.DispatchAgentFact, error) {
				facts, err := old(ctx, tx, tenant)
				if scenario != "not_lead" {
					facts = append(facts, fleet.DispatchAgentFact{PrincipalID: 2, State: "ACTIVE", Roles: []string{"LEAD"}})
				}
				return facts, err
			}
			var propose collab.ChannelDispatchProposer
			var read collab.ChannelDispatchFacts
			var card collab.SystemCardInput
			failure := errors.New("card store unavailable")
			services := module.NewServices()
			require.NoError(t, module.Provide[collab.ChannelDispatchConnector](services, collab.ChannelDispatchConnector{Bind: func(p collab.ChannelDispatchProposer) error { propose = p; return nil }, BindFacts: func(f collab.ChannelDispatchFacts) error { read = f; return nil }}))
			require.NoError(t, module.Provide[collab.ChannelPoster](services, collab.ChannelPoster{PostSystemOn: func(_ context.Context, _ *gorm.DB, in collab.SystemCardInput) (collab.MessageView, error) {
				card = in
				if scenario == "card_failure" {
					return collab.MessageView{}, failure
				}
				return collab.MessageView{}, nil
			}}))
			require.NoError(t, services.Seal())
			require.NoError(t, m.bindChannelDispatch(services))
			var result collab.ChannelDispatchResult
			err := db.Transaction(func(tx *gorm.DB) error {
				var err error
				result, err = propose(testContext(t, 2), tx, collab.ChannelDispatchRequest{TenantID: 1, ProjectID: 3, ChannelID: 51, SourceMessageID: 52, RequestedBy: 7, WorkItemID: 11, ExpectedWorkVersion: 1, CheckOnly: scenario == "check_only"})
				return err
			})
			q := query.BindDispatchProposal(db, m.names)
			rows, readErr := q.WithContext(t.Context()).Find()
			require.NoError(t, readErr)
			var assignments int64
			require.NoError(t, db.Table("baseline_probe").Count(&assignments).Error)
			require.Zero(t, assignments, "proposing never assigns or executes")
			switch scenario {
			case "not_lead":
				require.ErrorIs(t, err, collab.ErrForbidden)
				require.Empty(t, rows)
			case "card_failure":
				require.ErrorIs(t, err, failure)
				require.Empty(t, rows, "proposal rolls back with the card")
			case "check_only":
				require.NoError(t, err)
				require.Empty(t, rows, "replay authority check must not create a second proposal")
			default:
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.True(t, result.RequiresHuman)
				var snapshot DispatchSnapshot
				require.NoError(t, json.Unmarshal([]byte(rows[0].Snapshot), &snapshot))
				require.True(t, snapshot.RequiresHuman)
				_, digest, encodeErr := encodeDispatchSnapshot(snapshot)
				require.NoError(t, encodeErr)
				require.Equal(t, digest, rows[0].InputsDigest)
				require.Equal(t, "7", card.Payload["requested_by"])
				require.Equal(t, "52", card.Payload["source_message_id"])
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					facts, e := read(t.Context(), tx, 1, 3, result.ProposalID)
					require.Equal(t, "PROPOSED", facts["status"])
					require.Equal(t, "11", facts["work_item_id"])
					return e
				}))
			}
		})
	}
}

func TestChannelDispatchSourceFailuresCannotCreateCardsOrAssignment(t *testing.T) {
	for _, fault := range []string{"fleet_missing", "fleet_read", "work_missing", "work_read", "proposal_read", "snapshot", "snapshot_write"} {
		t.Run(fault, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			m.dispatchFleet.ListOn = func(context.Context, *gorm.DB, guid.ID) ([]fleet.DispatchAgentFact, error) {
				return []fleet.DispatchAgentFact{{PrincipalID: 2, State: "ACTIVE", Roles: []string{"LEAD"}}}, nil
			}
			var propose collab.ChannelDispatchProposer
			var read collab.ChannelDispatchFacts
			s := module.NewServices()
			require.NoError(t, module.Provide[collab.ChannelDispatchConnector](s, collab.ChannelDispatchConnector{Bind: func(p collab.ChannelDispatchProposer) error { propose = p; return nil }, BindFacts: func(f collab.ChannelDispatchFacts) error { read = f; return nil }}))
			require.NoError(t, module.Provide[collab.ChannelPoster](s, collab.ChannelPoster{PostSystemOn: func(context.Context, *gorm.DB, collab.SystemCardInput) (collab.MessageView, error) {
				t.Fatal("failed source must never create a card")
				return collab.MessageView{}, nil
			}}))
			require.NoError(t, s.Seal())
			require.NoError(t, m.bindChannelDispatch(s))
			switch fault {
			case "fleet_missing":
				m.dispatchFleet.ListOn = nil
			case "fleet_read":
				m.dispatchFleet.ListOn = func(context.Context, *gorm.DB, guid.ID) ([]fleet.DispatchAgentFact, error) {
					return nil, ErrUnavailable
				}
			case "work_missing":
				m.dispatch.WorkOn = nil
			case "work_read":
				m.dispatch.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
					return delivery.DispatchWorkFacts{}, ErrUnavailable
				}
			case "snapshot_write":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("dispatch_snapshot_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.DispatchProposal.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				if fault == "proposal_read" {
					facts, e := read(ctx, tx, 1, 3, 999)
					require.Nil(t, facts)
					return e
				}
				if fault == "snapshot" {
					receipt, e := m.createDispatchOn(ctx, tx, 3, DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"})
					if e != nil {
						return e
					}
					require.NoError(t, tx.Table(m.names.DispatchProposal.String()).Where("id=?", receipt.ID).Update("snapshot", "{").Error)
					facts, e := read(ctx, tx, 1, 3, receipt.ID)
					require.Nil(t, facts)
					return e
				}
				_, e := propose(ctx, tx, collab.ChannelDispatchRequest{TenantID: 1, ProjectID: 3, WorkItemID: 11, ExpectedWorkVersion: 1, CheckOnly: fault != "snapshot_write"})
				return e
			})
			require.Error(t, err)
			require.NoError(t, db.Callback().Update().Remove("dispatch_snapshot_failure"))
			for _, table := range []string{m.names.DispatchProposal.String(), "baseline_probe"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
}

package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestPipelineStageFailuresAndHistoricalStatesCannotInventPassedAcceptance(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	manifest := packageManifest{Content: PackageContent{Artifacts: []delivery.ClientArtifact{{VersionID: 40}}}, Refs: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20}}}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	pkg := model.DeliveryPackage{TenantID: 1, ProjectID: 3, ID: 80, Manifest: string(raw), State: "PUBLISHED", ContentHash: strings.Repeat("a", 64), RecipientID: 4, ActorID: 2, Version: 2, CreatedAt: now(), UpdatedAt: now()}
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Create(&pkg).Error)
	for i, state := range []string{"DRAFT", "WITHDRAWN", "ARCHIVED"} {
		require.NoError(t, db.Table(m.names.PackageTimeline.String()).Create(&model.PackageTimeline{TenantID: 1, ProjectID: 3, ID: guid.ID(90 + i), PackageID: 80, State: state, ContentHash: pkg.ContentHash, Version: i + 1, CreatedAt: now()}).Error)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, e := m.pipelineStagesOn(ctx, tx, 1, 3, []guid.ID{11}, []delivery.PipelineDeliveryLink{{DeliveryID: 999, WorkItemID: 11, ResultID: 20}})
		require.NoError(t, e)
		require.Len(t, rows, 3)
		require.Equal(t, "PENDING", rows[0].State)
		require.Equal(t, "BLOCKED", rows[1].State)
		require.Equal(t, "BLOCKED", rows[2].State)
		return nil
	}))
	for _, table := range []string{m.names.HiddenVerificationReceipt.String(), m.names.DeliveryPackage.String(), m.names.PackageTimeline.String(), m.names.PackageReceipt.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("pipeline_source_failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(ErrUnavailable)
			}
		}))
		err := db.Transaction(func(tx *gorm.DB) error { _, e := m.pipelineStagesOn(ctx, tx, 1, 3, []guid.ID{11}, nil); return e })
		require.ErrorIs(t, err, ErrUnavailable)
		require.NoError(t, db.Callback().Query().Remove("pipeline_source_failure"))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, e := m.pipelineStagesOn(ctx, tx, 1, 3, nil, nil)
		require.Empty(t, rows)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.pipelineStagesOn(ctx, tx, 1, 3, make([]guid.ID, 10001), nil)
		return e
	}), delivery.ErrUnavailable)
	projects.deny = true
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.pipelineStagesOn(ctx, tx, 1, 3, []guid.ID{11}, nil); return e }), delivery.ErrProjectNotFound)
	projects.deny = false
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=80").Update("manifest", "[]").Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.pipelineStagesOn(ctx, tx, 1, 3, []guid.ID{11}, nil); return e }), delivery.ErrUnavailable)
}

func TestMergedReplyFailuresCannotForwardUnfencedOrUnavailableSource(t *testing.T) {
	for _, fault := range []string{"invalid", "capability", "read_missing", "read_failure", "no_source", "query_source", "route", "reread", "client", "target_missing", "target_query"} {
		t.Run(fault, func(t *testing.T) {
			m, db, _, clients := clarificationHarness(t)
			e := events.Envelope{TenantID: 1, ProjectID: 3, EventType: "collab.message_posted", Subject: events.Subject{Kind: "message", ID: 91}}
			require.NoError(t, db.Table(m.names.IntakeThread.String()).Create(&model.IntakeThread{TenantID: 1, ProjectID: 3, IntakeID: 50, ChannelID: 70, RootMessageID: 71, RecipientID: 4, CreatedBy: 2, CreatedAt: now()}).Error)
			m.intakeReplyRouting = collab.IntakeReplyRouting{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (collab.IntakeReplyFact, error) {
				return collab.IntakeReplyFact{ChannelID: 70, RootID: 71, RecipientID: 4}, nil
			}, ForwardOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, collab.IntakeReplyForward) (guid.ID, error) {
				t.Fatal("missing authority/source must not forward")
				return 0, nil
			}}
			reads := 0
			m.mergedReplyRoutes = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (intake.MergedReplyRoute, error) {
				reads++
				if fault == "route" || (fault == "reread" && reads == 2) {
					return intake.MergedReplyRoute{}, ErrUnavailable
				}
				return intake.MergedReplyRoute{MergeRoute: intake.MergeRoute{TargetID: 51}, TargetRecipientID: 4}, nil
			}
			m.intakeReplyFence = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) error { return nil }
			switch fault {
			case "invalid":
				e.Subject.Kind = "wrong"
			case "capability":
				m.intakeReplyFence = nil
			case "read_missing":
				m.intakeReplyRouting.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (collab.IntakeReplyFact, error) {
					return collab.IntakeReplyFact{}, collab.ErrNotFound
				}
			case "read_failure":
				m.intakeReplyRouting.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (collab.IntakeReplyFact, error) {
					return collab.IntakeReplyFact{}, ErrUnavailable
				}
			case "no_source":
				require.NoError(t, db.Table(m.names.IntakeThread.String()).Where("intake_id=50").Delete(&model.IntakeThread{}).Error)
			case "query_source", "target_query":
				calls := 0
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("merged_source_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.IntakeThread.String() {
						calls++
						if fault == "query_source" || calls == 2 {
							tx.AddError(ErrUnavailable)
						}
					}
				}))
			case "client":
				clients.clientErr = ErrUnavailable
			}
			err := db.Transaction(func(tx *gorm.DB) error { return m.routeMergedReplyOn(t.Context(), tx, e) })
			if fault == "read_missing" || fault == "no_source" || fault == "target_missing" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestDispositionReceiptReadsActualThreadAcknowledgementAndRejectsMissingSources(t *testing.T) {
	m, db, projects, clients := clarificationHarness(t)
	ctx := testContext(t, 2)
	for _, in := range []*ObjectInput{{EntityID: "bad", ID: "50"}, {EntityID: "3", ID: "bad"}} {
		out, err := m.dispositionReceiptHTTP(ctx, in)
		require.Error(t, err)
		require.Nil(t, out)
	}
	in := &ObjectInput{EntityID: "3", ID: "50"}
	original := m.intakeSources.SourceOn
	m.intakeSources.SourceOn = nil
	_, err := m.dispositionReceiptHTTP(ctx, in)
	require.Error(t, err)
	m.intakeSources.SourceOn = original
	out, err := m.dispositionReceiptHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "NOT_REQUESTED", out.Body.Data.State)
	message := guid.ID(91)
	m.intakeSources.SourceOn = func(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (intake.IntakeLedgerSource, error) {
		source, e := original(ctx, tx, tenant, project, id)
		source.DispositionDelivery = intake.DispositionDelivery{State: "POSTED", MessageID: &message}
		return source, e
	}
	_, err = m.dispositionReceiptHTTP(ctx, in)
	require.Error(t, err, "posted without receipt capability is unavailable")
	called := 0
	read := false
	m.dispositionReceipts = func(_ context.Context, tx *gorm.DB, tenant, project, channel, root, recipient, msg guid.ID, hash string) (bool, error) {
		require.NotNil(t, tx)
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		require.EqualValues(t, 70, channel)
		require.EqualValues(t, 71, root)
		require.EqualValues(t, 4, recipient)
		require.Equal(t, message, msg)
		require.Len(t, hash, 64)
		called++
		return read, nil
	}
	_, err = m.dispositionReceiptHTTP(ctx, in)
	require.Error(t, err, "no linked original thread")
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
	require.NoError(t, db.Table(m.names.IntakeThread.String()).Create(&model.IntakeThread{TenantID: 1, ProjectID: 3, IntakeID: 50, ChannelID: 70, RootMessageID: 71, RecipientID: 4, CreatedBy: 2, CreatedAt: now()}).Error)
	m.intakeConversation.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string) error { return nil }
	out, err = m.dispositionReceiptHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "POSTED", out.Body.Data.State)
	read = true
	out, err = m.dispositionReceiptHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "READ", out.Body.Data.State)
	require.Equal(t, 2, called)
	clients.revoked = true
	_, err = m.dispositionReceiptHTTP(ctx, in)
	require.Error(t, err)
	require.Equal(t, 2, called)
	clients.revoked = false
	projects.deny = true
	_, err = m.dispositionReceiptHTTP(ctx, in)
	require.Error(t, err)
	require.Equal(t, 2, called)
	projects.deny = false
	m.dispositionReceipts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string) (bool, error) {
		return false, ErrUnavailable
	}
	_, err = m.dispositionReceiptHTTP(ctx, in)
	require.Error(t, err)
	m.intakeSources.SourceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.IntakeLedgerSource, error) {
		return intake.IntakeLedgerSource{}, ErrUnavailable
	}
	_, err = m.dispositionReceiptHTTP(ctx, in)
	require.Error(t, err)
}

func TestAgentMergeHTTPRechecksDelegationBeforeReplayAndCannotDecline(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	projects.deny = true
	allowed := true
	effects := 0
	checks := 0
	m.agentMerge = intake.AgentMerge{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error {
		checks++
		if !allowed {
			return ErrForbidden
		}
		return nil
	}, MergeOn: func(_ context.Context, tx *gorm.DB, tenant, project, source guid.ID, version int, body intake.SourceDisposition) (intake.IntakeLedgerEntry, error) {
		effects++
		require.Equal(t, "MERGE", body.Action)
		require.Equal(t, 1, version)
		return intake.IntakeLedgerEntry{ID: source, Version: version + 1, State: "MERGED"}, tx.Exec("INSERT INTO baseline_probe VALUES (100)").Error
	}}
	in := &AgentMergeInput{EditInput[intake.SourceDisposition]{EntityID: "3", ID: "50", IfMatch: `"1"`, Key: "agent-merge-exact", Body: intake.SourceDisposition{Action: "MERGE", TargetID: 51, Reason: "Duplicate matching scope"}}}
	first, err := m.agentMergeHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "MERGED", first.Body.Data.State)
	replay, err := m.agentMergeHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, 1, effects)
	require.Equal(t, 2, checks)
	cursor := int64(99)
	in.Body.SeenSeq = &cursor
	refreshed, err := m.agentMergeHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "true", refreshed.Replayed)
	require.Equal(t, 1, effects, "refreshing only seen_seq must retain the canonical replay hash")
	in.Body.Reason = "Different business decision"
	_, err = m.agentMergeHTTP(testContext(t, 2), in)
	require.Error(t, err, "excluding the cursor must not exclude changed disposition business data")
	in.Body.Reason = "Duplicate matching scope"
	allowed = false
	_, err = m.agentMergeHTTP(testContext(t, 2), in)
	require.Error(t, err)
	require.Equal(t, 1, effects)
	in.Body.Action = "DECLINE"
	_, err = m.agentMergeHTTP(testContext(t, 2), in)
	require.Error(t, err)
	require.Equal(t, 1, effects)
	var count int64
	require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestMergedReplyProjectorUsesStoredFactsCurrentFenceAndExactTarget(t *testing.T) {
	for _, scenario := range []string{"success", "unmerged", "ambiguous_source", "not_client", "changed_route", "fence", "forward_failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _, clients := clarificationHarness(t)
			require.NoError(t, db.Table(m.names.IntakeThread.String()).Create(&model.IntakeThread{TenantID: 1, ProjectID: 3, IntakeID: 50, ChannelID: 70, RootMessageID: 71, RecipientID: 4, CreatedBy: 2, CreatedAt: now()}).Error)
			require.NoError(t, db.Table(m.names.IntakeThread.String()).Create(&model.IntakeThread{TenantID: 1, ProjectID: 3, IntakeID: 51, ChannelID: 80, RootMessageID: 81, RecipientID: 4, CreatedBy: 2, CreatedAt: now()}).Error)
			if scenario == "ambiguous_source" {
				require.NoError(t, db.Table(m.names.IntakeThread.String()).Create(&model.IntakeThread{TenantID: 1, ProjectID: 3, IntakeID: 52, ChannelID: 70, RootMessageID: 71, RecipientID: 4, CreatedBy: 2, CreatedAt: now()}).Error)
			}
			calls := 0
			fenced := false
			m.mergedReplyRoutes = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (intake.MergedReplyRoute, error) {
				calls++
				if scenario == "unmerged" {
					return intake.MergedReplyRoute{}, gorm.ErrRecordNotFound
				}
				target := guid.ID(51)
				if scenario == "changed_route" && calls > 1 {
					target = 52
				}
				return intake.MergedReplyRoute{MergeRoute: intake.MergeRoute{SourceID: 50, TargetID: target, SourceRef: 9}, TargetRecipientID: 4, SourceHash: "source-hash", TargetHash: "target-hash"}, nil
			}
			m.intakeReplyFence = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) error {
				fenced = true
				if scenario == "fence" {
					return ErrForbidden
				}
				return nil
			}
			m.intakeReplyRouting = collab.IntakeReplyRouting{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (collab.IntakeReplyFact, error) {
				return collab.IntakeReplyFact{ChannelID: 70, RootID: 71, RecipientID: 4}, nil
			}, ForwardOn: func(_ context.Context, tx *gorm.DB, tenant, project guid.ID, in collab.IntakeReplyForward) (guid.ID, error) {
				require.True(t, fenced)
				require.Equal(t, guid.ID(71), in.SourceRootID)
				require.Equal(t, guid.ID(81), in.TargetRootID)
				require.Equal(t, guid.ID(91), in.MessageID)
				if err := tx.Exec("INSERT INTO baseline_probe VALUES (101)").Error; err != nil {
					return 0, err
				}
				if scenario == "forward_failure" {
					return 0, ErrUnavailable
				}
				return 101, nil
			}}
			if scenario == "not_client" {
				clients.revoked = true
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return m.routeMergedReplyOn(t.Context(), tx, events.Envelope{EventID: 92, EventType: "collab.message_posted", TenantID: 1, ProjectID: 3, Subject: events.Subject{Kind: "message", ID: 91}})
			})
			var n int64
			require.NoError(t, db.Table("baseline_probe").Count(&n).Error)
			if scenario == "success" {
				require.NoError(t, err)
				require.EqualValues(t, 1, n)
			} else {
				if scenario == "unmerged" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
				require.Zero(t, n)
			}
		})
	}
}

func TestPipelineClientReceiptsAreScopedPrivateAndRebuilt(t *testing.T) {
	m, db, projects, _ := harness(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for i, work := range []guid.ID{10, 11} {
		row := model.HiddenVerificationReceipt{TenantID: 1, ID: guid.ID(900 + i), ProjectID: 3, WorkItemID: work, ResultID: guid.ID(20 + i), Verdict: "PASSED", ReceiptHash: strings.Repeat("a", 64), Reason: "private reason", CreatedAt: at}
		require.NoError(t, db.Table(m.names.HiddenVerificationReceipt.String()).Create(&row).Error)
	}
	pkg := model.DeliveryPackage{TenantID: 1, ID: 30, ProjectID: 3, Title: "Fixture", Manifest: `{"refs":[{"work_item_id":"10","result_id":"20","evidence_id":"40"}]}`, State: "PUBLISHED", ContentHash: strings.Repeat("b", 64), RecipientID: 4, ActorID: 2, Version: 2, CreatedAt: at, UpdatedAt: at}
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Create(&pkg).Error)
	timeline := model.PackageTimeline{TenantID: 1, ID: 910, ProjectID: 3, PackageID: 30, State: "PUBLISHED", ContentHash: strings.Repeat("b", 64), Version: 2, CreatedAt: at}
	require.NoError(t, db.Table(m.names.PackageTimeline.String()).Create(&timeline).Error)
	receipt := model.PackageReceipt{TenantID: 1, ID: 911, ProjectID: 3, PackageID: 30, Decision: "REJECT", ContentHash: strings.Repeat("b", 64), Reason: "private feedback", CreatedAt: at}
	require.NoError(t, db.Table(m.names.PackageReceipt.String()).Create(&receipt).Error)
	links := []delivery.PipelineDeliveryLink{{DeliveryID: 30, WorkItemID: 10, ResultID: 20}, {DeliveryID: 30, WorkItemID: 10, ResultID: 20}}
	read := func(ctx context.Context, tenant guid.ID) (rows []delivery.PipelineStageRun, err error) {
		err = db.Transaction(func(tx *gorm.DB) error {
			var e error
			rows, e = m.pipelineStagesOn(ctx, tx, tenant, 3, []guid.ID{10}, links)
			return e
		})
		return
	}
	rows, err := read(testContext(t, 2), 1)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, "FAILED", rows[2].State)
	again, err := read(testContext(t, 2), 1)
	require.NoError(t, err)
	require.Equal(t, rows, again)
	var direct []delivery.PipelineStageRun
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		direct, e = m.pipelineStagesOn(testContext(t, 2), tx, 1, 3, []guid.ID{10}, nil)
		return e
	}))
	require.Equal(t, rows, direct)

	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private")
	require.NotContains(t, string(raw), "case")
	_, err = read(testContext(t, 2), 2)
	require.Error(t, err)
	projects.deny = true
	_, err = read(testContext(t, 2), 1)
	require.Error(t, err)
}

func TestPipelinePackageMixedOutcomesNeverAcceptSiblingOrStaleManifest(t *testing.T) {
	manifest := packageManifest{Refs: []delivery.ClientArtifactRef{{WorkItemID: 10, ResultID: 20}, {WorkItemID: 11, ResultID: 21}}, Content: PackageContent{Artifacts: []delivery.ClientArtifact{{Name: "a"}, {Name: "b"}}}}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	pkg := model.DeliveryPackage{ID: 30, ContentHash: strings.Repeat("a", 64), Manifest: string(raw)}
	items := packageItems(&pkg, manifest.Content)
	outcomes, err := json.Marshal([]PackageItemDecision{{ItemID: items[0].ItemID, Result: "ACCEPTED"}, {ItemID: items[1].ItemID, Result: "REJECTED"}})
	require.NoError(t, err)
	frozen := string(outcomes)
	receipt := model.PackageReceipt{ContentHash: pkg.ContentHash, ItemOutcomes: &frozen}
	require.Equal(t, "PASSED", pipelinePackageOutcome(pkg, receipt, delivery.PipelineDeliveryLink{WorkItemID: 10, ResultID: 20}))
	require.Equal(t, "FAILED", pipelinePackageOutcome(pkg, receipt, delivery.PipelineDeliveryLink{WorkItemID: 11, ResultID: 21}))
	require.Equal(t, "INCONCLUSIVE", pipelinePackageOutcome(pkg, receipt, delivery.PipelineDeliveryLink{WorkItemID: 12, ResultID: 22}))
	receipt.ContentHash = "stale"
	require.Equal(t, "INCONCLUSIVE", pipelinePackageOutcome(pkg, receipt, delivery.PipelineDeliveryLink{WorkItemID: 10, ResultID: 20}))
}

func TestPipelinePackageCorruptOrAmbiguousItemDecisionsRemainInconclusive(t *testing.T) {
	manifest := packageManifest{Content: PackageContent{Artifacts: []delivery.ClientArtifact{{VersionID: 40}}}, Refs: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20}}}
	raw, e := json.Marshal(manifest)
	require.NoError(t, e)
	pkg := model.DeliveryPackage{ID: 70, Manifest: string(raw), ContentHash: strings.Repeat("a", 64)}
	item := packageItems(&pkg, manifest.Content)[0].ItemID
	for _, outcomes := range []string{"{", "[]", `[{"item_id":"` + item + `","result":"PASSED"}]`, `[{"item_id":"` + item + `","result":"ACCEPTED"},{"item_id":"` + item + `","result":"REJECTED"}]`, `[{"item_id":"` + strings.Repeat("b", 64) + `","result":"ACCEPTED"}]`} {
		receipt := model.PackageReceipt{ContentHash: pkg.ContentHash, ItemOutcomes: &outcomes}
		require.Equal(t, "INCONCLUSIVE", pipelinePackageOutcome(pkg, receipt, delivery.PipelineDeliveryLink{WorkItemID: 11, ResultID: 20}), outcomes)
	}
}

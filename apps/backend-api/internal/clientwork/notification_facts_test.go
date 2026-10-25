package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestHomeReceiptSourcesFlagIncompleteItemsAndPropagateCorruption(t *testing.T) {
	m, db, pkg := releaseNoteHarness(t)
	ctx := testContext(t, 2)
	var pack model.DeliveryPackage
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&pack).Error)
	var manifest packageManifest
	require.NoError(t, json.Unmarshal([]byte(pack.Manifest), &manifest))
	item := packageItems(&pack, manifest.Content)[0].ItemID
	valid := `[{"item_id":"` + item + `","result":"REJECTED"}]`
	receipt := model.PackageReceipt{TenantID: 1, ID: 900, ProjectID: 3, PackageID: pkg, ContentHash: pack.ContentHash, Decision: "REJECT", ItemOutcomes: &valid, CreatedAt: now()}
	require.NoError(t, db.Table(m.names.PackageReceipt.String()).Create(&receipt).Error)
	for _, fault := range []string{"package_query", "manifest", "refs", "outcomes", "count", "unknown", "accepted"} {
		t.Run(fault, func(t *testing.T) {
			switch fault {
			case "package_query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("home_package_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.DeliveryPackage.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "manifest":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", "{").Error)
			case "refs":
				bad := manifest
				bad.Refs = nil
				raw, e := json.Marshal(bad)
				require.NoError(t, e)
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", string(raw)).Error)
			default:
				text := "{"
				if fault == "count" {
					text = "[]"
				}
				if fault == "unknown" {
					text = `[{"item_id":"` + strings.Repeat("b", 64) + `","result":"REJECTED"}]`
				}
				if fault == "accepted" {
					text = `[{"item_id":"` + item + `","result":"ACCEPTED"}]`
				}
				require.NoError(t, db.Table(m.names.PackageReceipt.String()).Where("id=900").Update("item_outcomes", text).Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				out, e := m.homeReceiptsOn(ctx, tx, 1, 3, now().Add(time.Hour))
				if fault == "count" || fault == "unknown" || fault == "refs" {
					require.NoError(t, e)
					require.False(t, out.Complete)
				}
				if fault == "accepted" {
					require.NoError(t, e)
					require.True(t, out.Complete)
					require.Len(t, out.Events, 1)
					require.False(t, out.Events[0].Rejected)
				}
				return e
			})
			if fault == "package_query" || fault == "manifest" || fault == "outcomes" {
				require.Error(t, err)
			}
			require.NoError(t, db.Callback().Query().Remove("home_package_failure"))
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("manifest", pack.Manifest).Error)
			require.NoError(t, db.Table(m.names.PackageReceipt.String()).Where("id=900").Update("item_outcomes", valid).Error)
		})
	}
}

func TestFactPublicationRejectsConflictingCapabilityWithoutReplacingReader(t *testing.T) {
	m, _, _, _ := harness(t)
	for _, provide := range []func(*module.Services) error{
		func(s *module.Services) error {
			return module.Provide[AcceptanceReminderFacts](s, m.acceptanceReminderFactsOn)
		},
		func(s *module.Services) error {
			return module.Provide[ClarificationReminderFacts](s, m.clarificationReminderFactsOn)
		},
		func(s *module.Services) error {
			return module.Provide[ClarificationNotificationFacts](s, m.clarificationNotificationFactsOn)
		},
		func(s *module.Services) error { return module.Provide[HomeReceipts](s, m.homeReceiptsOn) },
		func(s *module.Services) error { return module.Provide[CollaborationInbox](s, m.collaborationInboxOn) },
		func(s *module.Services) error {
			return module.Provide[CollaborationNotificationFacts](s, m.collaborationNotificationFactsOn)
		},
		func(s *module.Services) error {
			return module.Provide[ExceptionNotificationFacts](s, m.exceptionNotificationFactsOn)
		},
	} {
		s := module.NewServices()
		require.NoError(t, provide(s))
		require.ErrorIs(t, m.ProvideServices(s), module.ErrServiceDuplicate)
	}
}

func TestRoutingFactsDoNotTurnStorageFailuresIntoAbsentNotifications(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	for _, kind := range []string{"preview", "tolerance", "package", "dispatch"} {
		t.Run(kind, func(t *testing.T) {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, found, err := m.collaborationNotificationFactsOn(ctx, tx, 1, 999, kind)
				require.NoError(t, err)
				require.False(t, found)
				return nil
			}))
			failure := errors.New("routing storage failed")
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_routing", func(tx *gorm.DB) { tx.AddError(failure) }))
			defer func() { require.NoError(t, db.Callback().Query().Remove("regression_routing")) }()
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, found, err := m.collaborationNotificationFactsOn(ctx, tx, 1, 999, kind)
				require.ErrorIs(t, err, failure)
				require.False(t, found)
				return nil
			}))
		})
	}
	for _, kind := range []string{"DISPATCH_PROPOSAL", "EXCEPTION_DECISION", "PREVIEW_SHARE_APPROVAL"} {
		failure := errors.New("inbox read failed")
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_inbox", func(tx *gorm.DB) { tx.AddError(failure) }))
		err := db.Transaction(func(tx *gorm.DB) error {
			rows, err := m.collaborationInboxOn(ctx, tx, 1, 3, kind)
			require.Empty(t, rows)
			return err
		})
		require.NoError(t, db.Callback().Query().Remove("regression_inbox"))
		require.ErrorIs(t, err, failure)
	}
	failure := errors.New("notification read failed")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_notification", func(tx *gorm.DB) { tx.AddError(failure) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, err := m.clarificationNotificationFactsOn(ctx, tx, 1, 999)
		require.ErrorIs(t, err, failure)
		require.False(t, found)
		_, found, err = m.exceptionNotificationFactsOn(ctx, tx, 1, 999)
		require.ErrorIs(t, err, failure)
		require.False(t, found)
		out, err := m.homeReceiptsOn(ctx, tx, 1, 3, time.Now())
		require.ErrorIs(t, err, failure)
		require.Empty(t, out.Events)
		return nil
	}))
	require.NoError(t, db.Callback().Query().Remove("regression_notification"))
	_, _, err := m.clarificationNotificationFactsOn(ctx, nil, 1, 1)
	require.ErrorIs(t, err, ErrInvalid)
	_, _, err = m.clarificationNotificationFactsOn(ctx, db, 1, 1)
	require.ErrorIs(t, err, ErrInvalid)
	_, _, err = m.exceptionNotificationFactsOn(ctx, nil, 1, 1)
	require.ErrorIs(t, err, ErrInvalid)
	_, _, err = m.exceptionNotificationFactsOn(ctx, db, 1, 1)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = m.collaborationInboxOn(ctx, nil, 1, 3, "DISPATCH_PROPOSAL")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = m.collaborationInboxOn(context.Background(), db, 1, 3, "DISPATCH_PROPOSAL")
	require.ErrorIs(t, err, ErrForbidden)
	_, err = m.homeReceiptsOn(context.Background(), db, 1, 3, time.Now())
	require.ErrorIs(t, err, ErrForbidden)
}

func TestHomeReceiptPermissionIsDataScopedAndRegistersNoGrant(t *testing.T) {
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	m := &Module{registrar: authz.NewDeclarationOnlyRegistrar(registry)}
	require.NoError(t, m.registerNotificationFacts())
	action, exists := registry.Find("delivery_read")
	require.True(t, exists)
	require.True(t, action.DataScoped)
	require.Equal(t, authz.ActionScopeTenant, action.Scope)
	_, exists = registry.Find("delivery_compose")
	require.False(t, exists, "read integration must not add unrelated write permissions")
}

func TestClarificationNotificationUsesPersistedRequesterAndAnswers(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, err)
	read := func(tenant guid.ID) (ClarificationNotificationFact, bool) {
		var fact ClarificationNotificationFact
		var found bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			fact, found, err = m.clarificationNotificationFactsOn(t.Context(), tx, tenant, opened.Body.Data.ID)
			return err
		}))
		return fact, found
	}
	fact, found := read(1)
	require.True(t, found)
	require.EqualValues(t, 2, fact.RequesterID)
	require.EqualValues(t, 50, fact.IntakeID)
	require.False(t, fact.Answered)
	_, found = read(9)
	require.False(t, found)
	_, err = m.answerClarificationHTTP(testContext(t, 4), &EditInput[ClarificationAnswer]{EntityID: "3", ID: opened.Body.Data.ID.String(), IfMatch: `"1"`, Key: "notify-answer", Body: ClarificationAnswer{QuestionNo: 1, Answer: "Private source answer"}})
	require.NoError(t, err)
	fact, found = read(1)
	require.True(t, found)
	require.True(t, fact.Answered)
	require.False(t, fact.LateAnswer)
}

func TestHomeReceiptTimelineMapsFrozenRejectedItemsAndFlagsLegacyFacts(t *testing.T) {
	m, db, _, _ := harness(t)
	var pkg Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		pkg, err = m.createPackageOn(testContext(t, 2), tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Frozen", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		pkg, err = publishBuiltPackage(t, m, testContext(t, 2), tx, 3, pkg.ID, pkg.Version)
		return err
	}))
	v, err := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: pkg.ID.String()})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.decidePackageOn(testContext(t, 4), tx, 3, pkg.ID, pkg.Version, PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: v.Body.Data.ContentHash, Reason: "Actual client rejection", Items: []PackageItemDecision{{ItemID: v.Body.Data.Items[0].ItemID, Result: "REJECTED", FeedbackCategory: "FUNCTIONAL_DEFECT", Comment: "Evidence does not meet the requirement"}}})
		return err
	}))
	read := func() HomeReceiptHistory {
		t.Helper()
		var out HomeReceiptHistory
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.homeReceiptsOn(testContext(t, 2), tx, 1, 3, time.Now().Add(time.Hour))
			return err
		}))
		return out
	}
	out := read()
	require.True(t, out.Complete)
	require.Len(t, out.Events, 2)
	require.Equal(t, guid.ID(11), out.Events[0].WorkItemID)
	require.Equal(t, guid.ID(4), out.Events[0].ActorID)
	require.True(t, out.Events[0].Rejected)
	q := query.BindPackageReceipt(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.PackageID.Eq(pkg.ID)).Update(q.ItemOutcomes, nil)
	require.NoError(t, err)
	require.False(t, read().Complete)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.homeReceiptsOn(testContext(t, 4), tx, 1, 3, time.Now().Add(time.Hour))
		return err
	}), "external clients cannot enumerate the team's private receipt timeline")
}

func TestCollaborationInboxUsesCurrentSourceGuards(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	_, share := previewShareFixture(t, m, db)
	tol := toleranceFixture(t, m, db, "PROJECT", 3)
	ex := raisedException(t, m, db, tol)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reportExceptionOn(testContext(t, 2), tx, 3, ex.ID, ex.Version, ExceptionReport{Situation: "Observed current breach", Options: []ExceptionOption{{Decision: "HOLD", Impact: "Wait for correction"}}, Recommendation: "HOLD"})
		return err
	}))
	var proposal Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		proposal, err = m.createDispatchOn(testContext(t, 2), tx, 3, DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"})
		return err
	}))
	for kind, id := range map[string]guid.ID{"DISPATCH_PROPOSAL": proposal.ID, "EXCEPTION_DECISION": ex.ID, "PREVIEW_SHARE_APPROVAL": share.ID} {
		read := func(actor guid.ID, project guid.ID) []CollaborationInboxItem {
			var rows []CollaborationInboxItem
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				rows, err = m.collaborationInboxOn(testContext(t, actor), tx, 1, project, kind)
				return err
			}))
			return rows
		}
		rows := read(6, 3)
		require.Len(t, rows, 1)
		require.Equal(t, id, rows[0].ID)
		require.Empty(t, read(4, 3), "external client never receives internal decisions")
		require.Empty(t, read(6, 8), "project filter cannot select another project")
		if kind == "PREVIEW_SHARE_APPROVAL" {
			require.Empty(t, read(2, 3), "share requester cannot approve their own request")
		}
	}
	_, err := m.collaborationInboxOn(context.Background(), db, 1, 3, "DISPATCH_PROPOSAL")
	require.ErrorIs(t, err, ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.decideDispatchOn(testContext(t, 2), tx, 3, proposal.ID, proposal.Version, DispatchDecision{Decision: "DISMISS", Reason: "Current proposal no longer needed"})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, err := m.collaborationInboxOn(testContext(t, 6), tx, 1, 3, "DISPATCH_PROPOSAL")
		require.Empty(t, rows)
		return err
	}))
}

func TestCollaborationFactsUseActualPackageReceiptAndDispatchSnapshot(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	var pkg, proposal Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		pkg, err = m.createPackageOn(ctx, tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		if err != nil {
			return err
		}
		pkg, err = publishBuiltPackage(t, m, ctx, tx, 3, pkg.ID, pkg.Version)
		if err != nil {
			return err
		}
		proposal, err = m.createDispatchOn(ctx, tx, 3, DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"})
		return err
	}))
	fact, found, err := m.collaborationNotificationFactsOn(t.Context(), db, 1, pkg.ID, "package")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "PUBLISHED", fact.State)
	require.Empty(t, fact.Decision, "publication is not a client receipt")
	view, err := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: pkg.ID.String()})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.decidePackageOn(testContext(t, 4), tx, 3, pkg.ID, pkg.Version, PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: view.Body.Data.ContentHash, Decision: "ACCEPT", Reason: "Reviewed"})
		return err
	}))
	fact, found, err = m.collaborationNotificationFactsOn(t.Context(), db, 1, pkg.ID, "package")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "ACCEPTED", fact.State)
	require.Equal(t, "ACCEPT", fact.Decision)
	fact, found, err = m.collaborationNotificationFactsOn(t.Context(), db, 1, proposal.ID, "dispatch")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, fact.RequiresHuman)
	require.Equal(t, "PROPOSED", fact.State)
	require.False(t, fact.ExpiresAt.IsZero())
	for _, kind := range []string{"package", "dispatch"} {
		_, found, err = m.collaborationNotificationFactsOn(t.Context(), db, 9, pkg.ID, kind)
		require.NoError(t, err)
		require.False(t, found)
	}
	_, _, err = m.collaborationNotificationFactsOn(t.Context(), db, 1, pkg.ID, "unknown")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestExceptionSourceUsesCurrentTenantAndPersistedState(t *testing.T) {
	m, db, _, _ := harness(t)
	tol := toleranceFixture(t, m, db, "PROJECT", 3)
	ex := raisedException(t, m, db, tol)
	_, _, err := m.exceptionNotificationFactsOn(t.Context(), db, 1, ex.ID)
	require.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		fact, exists, err := m.exceptionNotificationFactsOn(t.Context(), tx, 1, ex.ID)
		require.NoError(t, err)
		require.True(t, exists)
		require.Equal(t, guid.ID(3), fact.ProjectID)
		require.Equal(t, "RAISED", fact.State)
		_, exists, err = m.exceptionNotificationFactsOn(t.Context(), tx, 9, ex.ID)
		require.NoError(t, err)
		require.False(t, exists)
		return nil
	}))
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.Error(t, m.ProvideServices(services))
}

func TestPreviewFactsRequireActualIndependentApprovalAuthority(t *testing.T) {
	m, db, _, _ := harness(t)
	_, share := previewShareFixture(t, m, db)
	read := func(actor, tenant guid.ID) (CollaborationNotificationFact, bool) {
		var fact CollaborationNotificationFact
		var found bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			fact, found, err = m.collaborationNotificationFactsOn(testContext(t, actor), tx, tenant, share.ID, "preview")
			return err
		}))
		return fact, found
	}
	fact, found := read(2, 1)
	require.True(t, found)
	require.EqualValues(t, 2, fact.RequesterID)
	require.False(t, fact.ApprovalEligible)
	fact, found = read(6, 1)
	require.True(t, found)
	require.True(t, fact.ApprovalEligible)
	_, found = read(6, 99)
	require.False(t, found)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.decideShareOn(testContext(t, 6), tx, 3, share.ID, 1, PreviewShareDecision{Decision: "REJECT", Reason: "Needs a new preview"})
		return err
	}))
	fact, found = read(6, 1)
	require.True(t, found)
	require.False(t, fact.ApprovalEligible)
	require.Equal(t, "REJECTED", fact.Decision)
}

func TestAppliedDispatchRequiresImmutableAutomaticAction(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	created, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "source-dispatch", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
	require.NoError(t, err)
	applied, err := m.decideDispatchHTTP(ctx, &EditInput[DispatchDecision]{EntityID: "3", ID: created.Body.Data.ID.String(), Key: "source-confirm", IfMatch: `"1"`, Body: DispatchDecision{Decision: "CONFIRM", Reason: "Checked candidate"}})
	require.NoError(t, err)
	read := func() CollaborationNotificationFact {
		fact, found, err := m.collaborationNotificationFactsOn(ctx, db, 1, applied.Body.Data.ID, "dispatch")
		require.NoError(t, err)
		require.True(t, found)
		return fact
	}
	fact := read()
	require.Equal(t, "APPLIED", fact.State)
	require.EqualValues(t, 11, fact.WorkItemID)
	require.Positive(t, fact.AppliedID)
	require.Positive(t, fact.AssignmentID)
	require.False(t, fact.Automatic, "a real manual confirmation is not an automatic dispatch")
	// Seed the distinct immutable automatic receipt to exercise the reader's
	// exact action/assignment/selection binding. No production command infers it.
	a := query.BindDispatchAction(db, m.names)
	_, err = a.WithContext(ctx).Where(a.ProposalID.Eq(applied.Body.Data.ID)).Update(a.Action, "AUTO_APPLY")
	require.NoError(t, err)
	require.True(t, read().Automatic)
	_, err = a.WithContext(ctx).Where(a.ProposalID.Eq(applied.Body.Data.ID)).Update(a.AssignmentID, guid.ID(999))
	require.NoError(t, err)
	require.False(t, read().Automatic)
}

func TestClarificationInboxAuthorAndLatestHandlerCurrentRevisionAndClosure(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	opened, e := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, e)
	id := opened.Body.Data.ID.String()
	for n, answer := range []string{"CSV", "UTC"} {
		_, e = m.answerClarificationHTTP(testContext(t, 4), &EditInput[ClarificationAnswer]{EntityID: "3", ID: id, IfMatch: fmt.Sprintf("\"%d\"", n+1), Key: fmt.Sprint("inbox-answer-", n), Body: ClarificationAnswer{QuestionNo: n + 1, Answer: answer}})
		require.NoError(t, e)
	}
	read := func(actor guid.ID) []CollaborationInboxItem {
		var rows []CollaborationInboxItem
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			rows, e = m.collaborationInboxOn(testContext(t, actor), tx, 1, 3, "CLARIFICATION_REPLY")
			return e
		}))
		return rows
	}
	rows := read(2)
	require.Len(t, rows, 1)
	require.Equal(t, opened.Body.Data.ID, rows[0].ID)
	require.Equal(t, guid.ID(50), rows[0].IntakeID)
	require.Equal(t, "3", rows[0].Revision)
	require.Empty(t, read(4), "client who updated source is not the internal handler")
	require.Empty(t, read(6), "unrelated eligible Human is not a recipient")
	sourceOn := m.intakeSources.SourceOn
	m.intakeSources.SourceOn = func(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (intake.IntakeLedgerSource, error) {
		source, err := sourceOn(ctx, tx, tenant, project, id)
		handler := guid.ID(6)
		source.LastHandlerID = &handler
		return source, err
	}
	rows = read(6)
	require.Len(t, rows, 1)
	require.Equal(t, opened.Body.Data.ID, rows[0].ID)
	m.intakeSources.SourceOn = sourceOn
	require.Empty(t, read(6), "removing current handler assignment removes the reply")
	// A later internal triage card identifies the current handler; its source
	// revision also closes the preceding round projection, even before ACK.
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("version", 5).Error)
	require.Empty(t, read(2))
}

func TestClarificationInboxSourceVisibilityAndOutageErrors(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, err)
	for n, answer := range []string{"CSV", "UTC"} {
		_, err = m.answerClarificationHTTP(testContext(t, 4), &EditInput[ClarificationAnswer]{EntityID: "3", ID: opened.Body.Data.ID.String(), IfMatch: fmt.Sprintf("\"%d\"", n+1), Key: fmt.Sprint("source-filter-answer-", n), Body: ClarificationAnswer{QuestionNo: n + 1, Answer: answer}})
		require.NoError(t, err)
	}
	outage := errors.New("source database unavailable")
	for _, sourceErr := range []error{ErrNotFound, ErrForbidden, intake.ErrForbidden, gorm.ErrRecordNotFound, outage} {
		t.Run(sourceErr.Error(), func(t *testing.T) {
			copyModule := *m
			copyModule.intakeSources.SourceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.IntakeLedgerSource, error) {
				return intake.IntakeLedgerSource{}, fmt.Errorf("read source: %w", sourceErr)
			}
			var rows []CollaborationInboxItem
			err := db.Transaction(func(tx *gorm.DB) error {
				var err error
				rows, err = copyModule.collaborationInboxOn(testContext(t, 2), tx, 1, 3, "CLARIFICATION_REPLY")
				return err
			})
			if errors.Is(sourceErr, outage) {
				require.ErrorIs(t, err, outage)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, rows)
		})
	}
}

package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
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
)

func TestPackageViewRejectsCorruptReceiptAndUnavailableReworkSources(t *testing.T) {
	for _, fault := range []string{"receipt_query", "evidence", "outcomes", "rework", "disposition_query", "disposition"} {
		t.Run(fault, func(t *testing.T) {
			m, db, pkg := releaseNoteHarness(t)
			var pack model.DeliveryPackage
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Take(&pack).Error)
			pack.State = "REJECTED"
			name, sha, receiptJSON := "Signer", strings.Repeat("a", 64), `{}`
			receipt := model.PackageReceipt{TenantID: 1, ID: 900, ProjectID: 3, PackageID: pkg, Decision: "REJECT", ContentHash: pack.ContentHash, SignerName: &name, ReceiptSHA256: &sha, ReceiptJSON: &receiptJSON, CreatedAt: now()}
			bad := "{"
			if fault == "evidence" {
				receipt.EvidenceJSON = &bad
			}
			if fault == "outcomes" {
				receipt.ItemOutcomes = &bad
			}
			require.NoError(t, db.Table(m.names.PackageReceipt.String()).Create(&receipt).Error)
			if fault == "rework" {
				m.reworkSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.ReworkSourceView, bool, error) {
					return intake.ReworkSourceView{}, false, ErrUnavailable
				}
			}
			if fault == "disposition" || fault == "disposition_query" {
				m.reworkSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.ReworkSourceView, bool, error) {
					return intake.ReworkSourceView{ID: 901}, true, nil
				}
				if fault == "disposition" {
					require.NoError(t, db.Table(m.names.ReworkDisposition.String()).Create(&model.ReworkDisposition{TenantID: 1, ID: 902, ProjectID: 3, IntakeID: 901, ReceiptID: 900, ItemID: strings.Repeat("b", 64), Mode: "DECLINE", ClientMessage: "Frozen decline reason", CreatedAt: now()}).Error)
				}
			}
			if fault == "receipt_query" || fault == "disposition_query" {
				target := m.names.PackageReceipt.String()
				if fault == "disposition_query" {
					target = m.names.ReworkDisposition.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("view_source_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			out, err := m.packageView(testContext(t, 2), db, &pack)
			if fault == "disposition" {
				require.NoError(t, err)
				require.Len(t, out.Dispositions, 1)
				require.Equal(t, "Frozen decline reason", out.Dispositions[0].ClientMessage)
				require.False(t, out.ReworkUnavailable)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestClientChangeRejectionAndTransitionFailuresPreserveFrozenRequest(t *testing.T) {
	m, db, projects, _ := harness(t)
	internal, client := testContext(t, 2), testContext(t, 4)
	sheet := draftAndPublish(t, m, db)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		sheet, err = m.signSheetOn(client, tx, 3, sheet.ID, sheet.Version, SignoffBody{1, sheet.ContentHash, "SIGN", "Reviewed"})
		return err
	}))
	var request Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		request, err = m.createChangeOn(client, tx, 3, ChangeBody{sheet.ID, 1, "Requested new scope"})
		return err
	}))
	for _, stage := range []string{"missing", "version", "empty_impact", "unknown_action", "internal_confirmation", "invalid_proposal", "denied", "event"} {
		t.Run(stage, func(t *testing.T) {
			before := collaborationSnapshot(t, m, db)
			id, version := request.ID, request.Version
			body := ChangeAction{Action: "ASSESS", Impact: "Actual scope impact"}
			switch stage {
			case "missing":
				id = 999
			case "version":
				version++
			case "empty_impact":
				body.Impact = " "
			case "unknown_action":
				body.Action = "APPROVE"
			case "internal_confirmation":
				body.Action = "CONFIRM"
			case "invalid_proposal":
				body.Proposal = &ClientChangeProposal{}
			case "denied":
				projects.deny = true
			case "event":
				require.NoError(t, db.Exec("CREATE TRIGGER regression_change_event BEFORE INSERT ON g_domain_events BEGIN SELECT RAISE(ABORT,'event unavailable'); END").Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.actChangeOn(internal, tx, 3, id, version, body); return err })
			require.Error(t, err)
			projects.deny = false
			if stage == "event" {
				require.NoError(t, db.Exec("DROP TRIGGER regression_change_event").Error)
			}
			require.Equal(t, before, collaborationSnapshot(t, m, db))
		})
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		request, err = m.actChangeOn(internal, tx, 3, request.ID, request.Version, ChangeAction{Action: "ASSESS", Impact: "Actual impact"})
		return err
	}))
	view, err := m.get_changeHTTP(client, &ObjectInput{EntityID: "3", ID: request.ID.String()})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		request, err = m.actChangeOn(client, tx, 3, request.ID, request.Version, ChangeAction{Action: "REJECT", ProposalHash: view.Body.Data.ProposalHash})
		return err
	}))
	require.Equal(t, "REJECTED", request.State)
	view, err = m.get_changeHTTP(client, &ObjectInput{EntityID: "3", ID: request.ID.String()})
	require.NoError(t, err)
	require.False(t, view.Body.Data.Applied)
}

func TestCorruptClientIssueAndPackageSnapshotsDoNotReturnPartialViews(t *testing.T) {
	m, db, _, _ := harness(t)
	var issue, pkg, preview Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		preview, err = m.createPreviewOn(testContext(t, 2), tx, 3, PreviewBody{Title: "Preview", Artifact: delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}, ClientVisible: true})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		issue, err = m.createIssueOn(testContext(t, 4), tx, 3, IssueBody{Title: "Observed", Description: "Issue", PreviewID: &preview.ID, Anchor: &ArtifactAnchor{Kind: "PAGE", Page: 1}})
		return err
	}))
	require.NoError(t, db.Table(m.names.ClientIssue.String()).Where("id=?", issue.ID).Update("anchor", "{").Error)
	out, err := m.get_issueHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: issue.ID.String()})
	require.Error(t, err)
	require.Nil(t, out)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		pkg, err = m.createPackageOn(testContext(t, 2), tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		return err
	}))
	require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg.ID).Update("manifest", "{").Error)
	packageOut, err := m.get_packageHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: pkg.ID.String()})
	require.Error(t, err)
	require.Nil(t, packageOut)
}

func TestClientIssuePrivacyReplyAndDefectLink(t *testing.T) {
	m, db, _, _ := harness(t)
	var r Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = m.createIssueOn(testContext(t, 4), tx, 3, IssueBody{Title: "Problem", Description: "Visible issue"})
		return e
	}))
	_, e := m.get_issueHTTP(testContext(t, 5), &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.Error(t, e)
	list, e := m.list_issueHTTP(testContext(t, 5), &ListInput{EntityID: "3", Limit: 50})
	require.NoError(t, e)
	require.Empty(t, list.Body.Data.Items)
	defect := guid.ID(12)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = m.actIssueOn(testContext(t, 2), tx, 3, r.ID, r.Version, IssueAction{State: "TRIAGED", InternalDefectID: &defect})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = m.actIssueOn(testContext(t, 2), tx, 3, r.ID, r.Version, IssueAction{State: "RESPONDED", Reply: "Approved public reply"})
		return e
	}))
	visible, e := m.get_issueHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.NoError(t, e)
	require.Equal(t, "RESPONDED", visible.Body.Data.State)
	require.Len(t, visible.Body.Data.Replies, 1)
	raw, e := json.Marshal(visible.Body.Data)
	require.NoError(t, e)
	require.NotContains(t, string(raw), "internal_defect")
	require.NotContains(t, string(raw), "actor_id")
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.actIssueOn(testContext(t, 4), tx, 3, r.ID, r.Version, IssueAction{State: "CLOSED"})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.actIssueOn(testContext(t, 2), tx, 3, r.ID, r.Version, IssueAction{State: "CLOSED"})
		return e
	}))
}
func TestClientChangesRequireFrozenBaseAndExactImpactConfirmation(t *testing.T) {
	m, db, _, _ := harness(t)
	sheet := draftAndPublish(t, m, db)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		sheet, e = m.signSheetOn(testContext(t, 4), tx, 3, sheet.ID, sheet.Version, SignoffBody{1, sheet.ContentHash, "SIGN", "Reviewed"})
		return e
	}))
	var r Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = m.createChangeOn(testContext(t, 4), tx, 3, ChangeBody{sheet.ID, 1, "Additional feature"})
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.actChangeOn(testContext(t, 4), tx, 3, r.ID, r.Version, ChangeAction{Action: "ASSESS", Impact: "Injected"})
		return e
	}), ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = m.actChangeOn(testContext(t, 2), tx, 3, r.ID, r.Version, ChangeAction{Action: "ASSESS", Impact: "Scope and schedule impact"})
		return e
	}))
	v, e := m.get_changeHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.NoError(t, e)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.actChangeOn(testContext(t, 4), tx, 3, r.ID, r.Version, ChangeAction{Action: "CONFIRM", ProposalHash: strings.Repeat("0", 64)})
		return e
	}), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.actChangeOn(testContext(t, 4), tx, 3, r.ID, r.Version, ChangeAction{Action: "CONFIRM", ProposalHash: v.Body.Data.ProposalHash})
		return e
	}))
	q := query.BindSignoffs(db, m.names)
	count, e := q.WithContext(t.Context()).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, count, "client change confirmation must not manufacture another baseline signoff")
}
func TestDeliveryPublicationRevalidatesAndClientReceiptIndependent(t *testing.T) {
	m, db, p, clients := harness(t)
	var r Receipt
	in := PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; r, e = m.createPackageOn(testContext(t, 2), tx, 3, in); return e }))
	_, e := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.Error(t, e)
	p.stale = true
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := publishBuiltPackage(t, m, testContext(t, 2), tx, 3, r.ID, r.Version)
		return e
	}), delivery.ErrResultCoverage)
	p.stale = false
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = publishBuiltPackage(t, m, testContext(t, 2), tx, 3, r.ID, r.Version)
		return e
	}))
	view, e := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.NoError(t, e)
	raw, _ := json.Marshal(view.Body.Data)
	require.NotContains(t, string(raw), "work_item_id")
	require.NotContains(t, string(raw), "result_id")
	require.Contains(t, string(raw), "verification_overrides")
	decision := PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: view.Body.Data.ContentHash, Decision: "ACCEPT", Reason: "Received and checked"}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decidePackageOn(testContext(t, 2), tx, 3, r.ID, r.Version, decision)
		return e
	}), ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = m.decidePackageOn(testContext(t, 4), tx, 3, r.ID, r.Version, decision)
		return e
	}))
	require.Equal(t, "ACCEPTED", r.State)
	frozen, e := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.NoError(t, e)
	require.Len(t, frozen.Body.Data.Decision.Items, 1)
	require.Equal(t, "ACCEPTED", frozen.Body.Data.Decision.Items[0].Result)
	require.Equal(t, frozen.Body.Data.Items[0].ItemID, frozen.Body.Data.Decision.Items[0].ItemID)
	clients.revoked = true
	_, e = m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.Error(t, e)
}

func TestPackageItemAcceptanceFreezesExactManifestOutcomesAndReplays(t *testing.T) {
	m, db, projects, clients := harness(t)
	var pkg Receipt
	draft := PackageBody{Title: "Release", ReleaseNotes: "Frozen notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}, {WorkItemID: 11, ResultID: 20, EvidenceID: 31}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		pkg, e = m.createPackageOn(testContext(t, 2), tx, 3, draft)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		pkg, e = publishBuiltPackage(t, m, testContext(t, 2), tx, 3, pkg.ID, pkg.Version)
		return e
	}))
	view, e := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: pkg.ID.String()})
	require.NoError(t, e)
	require.Len(t, view.Body.Data.Items, 2)
	items := view.Body.Data.Items
	require.NotEqual(t, items[0].ItemID, items[1].ItemID)
	for _, item := range items {
		require.Len(t, item.ItemID, 64)
	}
	// Input order is irrelevant; persisted outcomes follow the immutable manifest.
	body := PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: view.Body.Data.ContentHash, Reason: "Checked both files", Items: []PackageItemDecision{{ItemID: items[1].ItemID, Result: "REJECTED", FeedbackCategory: "FUNCTIONAL_DEFECT", Comment: "登录之后页面显示为空白状态"}, {ItemID: items[0].ItemID, Result: "ACCEPTED"}}}
	decide := func(actor guid.ID, version int, body PackageDecision) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.decidePackageOn(testContext(t, actor), tx, 3, pkg.ID, version, body)
			return e
		})
	}
	require.Error(t, decide(2, pkg.Version, body), "internal actor cannot impersonate recipient")
	require.Error(t, decide(5, pkg.Version, body), "another external client cannot sign")
	clients.revoked = true
	require.Error(t, decide(4, pkg.Version, body))
	clients.revoked = false
	wrongHash := body
	wrongHash.ContentHash = strings.Repeat("0", 64)
	require.ErrorIs(t, decide(4, pkg.Version, wrongHash), ErrConflict)
	require.ErrorIs(t, decide(4, pkg.Version+1, body), ErrConflict)
	for name, mutate := range map[string]func(*PackageDecision){
		"omitted":               func(v *PackageDecision) { v.Items = v.Items[:1] },
		"empty":                 func(v *PackageDecision) { v.Items = []PackageItemDecision{} },
		"duplicate":             func(v *PackageDecision) { v.Items[1].ItemID = v.Items[0].ItemID },
		"unknown":               func(v *PackageDecision) { v.Items[1].ItemID = strings.Repeat("0", 64) },
		"invalid result":        func(v *PackageDecision) { v.Items[1].Result = "PENDING" },
		"missing category":      func(v *PackageDecision) { v.Items[0].FeedbackCategory = "" },
		"invalid category":      func(v *PackageDecision) { v.Items[0].FeedbackCategory = "MISC" },
		"short unicode comment": func(v *PackageDecision) { v.Items[0].Comment = "不足十个字符" },
		"blank comment":         func(v *PackageDecision) { v.Items[0].Comment = strings.Repeat(" ", 20) },
		"long comment":          func(v *PackageDecision) { v.Items[0].Comment = strings.Repeat("a", 2001) },
		"accepted category":     func(v *PackageDecision) { v.Items[1].FeedbackCategory = "OTHER" },
		"false aggregate":       func(v *PackageDecision) { v.Decision = "ACCEPT" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := body
			invalid.Items = append([]PackageItemDecision(nil), body.Items...)
			mutate(&invalid)
			require.ErrorIs(t, decide(4, pkg.Version, invalid), ErrInvalid)
		})
	}
	var count int64
	require.NoError(t, db.Table("g_clientwork_package_receipts").Where("package_id=?", pkg.ID).Count(&count).Error)
	require.Zero(t, count)
	// Receipt, aggregate, audit and outbox must all roll back together.
	publisher := m.events
	m.events = failPublisher{}
	require.ErrorIs(t, decide(4, pkg.Version, body), ErrUnavailable)
	m.events = publisher
	require.NoError(t, db.Table("g_clientwork_package_receipts").Where("package_id=?", pkg.ID).Count(&count).Error)
	require.Zero(t, count)
	afterFailure, e := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: pkg.ID.String()})
	require.NoError(t, e)
	require.Equal(t, "PUBLISHED", afterFailure.Body.Data.State)
	require.Nil(t, afterFailure.Body.Data.Decision)
	request := &EditInput[PackageDecision]{EntityID: "3", ID: pkg.ID.String(), IfMatch: "\"2\"", Key: "partial-acceptance", Body: body}
	accepted, e := m.package_acceptanceHTTP(testContext(t, 4), request)
	require.NoError(t, e)
	require.Equal(t, "PARTIALLY_ACCEPTED", accepted.Body.Data.State)
	replay, e := m.package_acceptanceHTTP(testContext(t, 4), request)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, accepted.Body.Data, replay.Body.Data)
	final, e := m.get_packageHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: pkg.ID.String()})
	require.NoError(t, e)
	require.Equal(t, "PARTIALLY_ACCEPTED", final.Body.Data.Decision.Decision)
	require.Equal(t, items[0].ItemID, final.Body.Data.Decision.Items[0].ItemID)
	require.Equal(t, "ACCEPTED", final.Body.Data.Decision.Items[0].Result)
	require.Equal(t, body.Items[0], final.Body.Data.Decision.Items[1])
	require.Equal(t, view.Body.Data.ContentHash, final.Body.Data.ContentHash)
	require.Equal(t, items, final.Body.Data.Items)
	public, e := json.Marshal(final.Body.Data)
	require.NoError(t, e)
	for _, private := range []string{"work_item_id", "result_id", "evidence_id", "actor_id"} {
		require.NotContains(t, string(public), private)
	}
	require.False(t, projects.changed, "external outcomes must not reopen internal work")
	require.ErrorIs(t, decide(4, accepted.Body.Data.Version, body), ErrConflict)
	request.Body.Items[0].Comment = "A materially different rejection"
	_, e = m.package_acceptanceHTTP(testContext(t, 4), request)
	require.Error(t, e, "same key cannot overwrite immutable outcomes")
	require.NoError(t, db.Table("g_clientwork_package_receipts").Where("package_id=?", pkg.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestPackageItemOutcomeAggregatesAndLegacyCommands(t *testing.T) {
	row := &model.DeliveryPackage{ID: 42, ContentHash: strings.Repeat("a", 64)}
	content := PackageContent{Artifacts: []delivery.ClientArtifact{{Name: "one"}, {Name: "two"}}}
	items := packageItems(row, content)
	for _, result := range []string{"ACCEPTED", "REJECTED"} {
		legacy := "ACCEPT"
		if result == "REJECTED" {
			legacy = "REJECT"
		}
		input := PackageDecision{SignerName: "Client signer", Confirmed: true, Decision: legacy, Reason: "Legacy reason"}
		out, aggregate, e := packageItemOutcomes(row, content, input)
		require.NoError(t, e)
		require.Equal(t, legacy, aggregate)
		if result == "REJECTED" {
			require.Nil(t, out, "legacy rejection must not fabricate structured feedback")
		} else {
			require.Len(t, out, 2)
		}
		for i, outcome := range out {
			require.Equal(t, items[i].ItemID, outcome.ItemID)
			require.Equal(t, result, outcome.Result)
		}
		input.Items = make([]PackageItemDecision, len(items))
		for i, item := range items {
			input.Items[i] = PackageItemDecision{ItemID: item.ItemID, Result: result, Comment: "Actual detailed client feedback"}
			if result == "REJECTED" {
				input.Items[i].FeedbackCategory = "OTHER"
			}
		}
		_, aggregate, e = packageItemOutcomes(row, content, input)
		require.NoError(t, e)
		require.Equal(t, legacy, aggregate)
	}
	other := *row
	other.ID++
	require.NotEqual(t, items[0].ItemID, packageItems(&other, content)[0].ItemID)
	other = *row
	other.ContentHash = strings.Repeat("b", 64)
	require.NotEqual(t, items[0].ItemID, packageItems(&other, content)[0].ItemID)
}
func TestAnnotationsFreezeVersionAnchorAndVisibilityBeforePagination(t *testing.T) {
	m, db, _, _ := harness(t)
	var preview Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		preview, e = m.createPreviewOn(testContext(t, 2), tx, 3, PreviewBody{Title: "Preview", Artifact: delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}, ClientVisible: true})
		return e
	}))
	var internal Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		internal, e = m.createPreviewOn(testContext(t, 2), tx, 3, PreviewBody{Title: "Internal", Artifact: delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}})
		return e
	}))
	_, e := m.get_previewHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: internal.ID.String()})
	require.Error(t, e)
	in := AnnotationBody{PreviewID: preview.ID, FileVersionID: 40, ContentHash: strings.Repeat("a", 64), Anchor: ArtifactAnchor{Kind: "TEXT", Start: 1, End: 4}, Body: "Comment"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createAnnotationOn(testContext(t, 4), tx, 3, in); return e }))
	in.Body = "Internal note"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createAnnotationOn(testContext(t, 2), tx, 3, in); return e }))
	page, e := m.list_annotationHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.False(t, page.Body.Data.HasMore)
	require.Equal(t, "Comment", page.Body.Data.Items[0].Body)
	in.FileVersionID = 41
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createAnnotationOn(testContext(t, 4), tx, 3, in); return e }), ErrConflict)
	in.FileVersionID = 40
	in.Anchor.End = 0
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createAnnotationOn(testContext(t, 4), tx, 3, in); return e }), ErrInvalid)
}

type failPublisher struct{}

func (failPublisher) Publish(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
	return 0, ErrUnavailable
}
func TestIssueOutboxFailureIsAtomic(t *testing.T) {
	m, db, _, _ := harness(t)
	m.events = failPublisher{}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.createIssueOn(testContext(t, 4), tx, 3, IssueBody{Title: "Issue", Description: "Description"})
		return e
	}), ErrUnavailable)
	var count int64
	require.NoError(t, db.Table("g_clientwork_issues").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Table("audit_probe").Count(&count).Error)
	require.Zero(t, count)
}

func TestIssueRepliesPaginateWithoutCrossIssueOrCrossClientCursors(t *testing.T) {
	m, db, _, _ := harness(t)
	var issue Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		issue, e = m.createIssueOn(testContext(t, 4), tx, 3, IssueBody{Title: "Issue", Description: "Observed"})
		return e
	}))
	q := query.BindIssueReply(db, m.names)
	for i := 0; i < 101; i++ {
		id, e := m.nextID()
		require.NoError(t, e)
		require.NoError(t, q.WithContext(t.Context()).Create(&model.IssueReply{TenantID: 1, ProjectID: 3, ID: id, IssueID: issue.ID, Body: "Public reply", ActorID: 2, CreatedAt: now()}))
	}
	first, e := m.get_issueHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: issue.ID.String()})
	require.NoError(t, e)
	require.Len(t, first.Body.Data.Replies, 100)
	require.True(t, first.Body.Data.RepliesHasMore)
	second, e := m.get_issueHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: issue.ID.String(), ReplyCursor: first.Body.Data.RepliesNextCursor})
	require.NoError(t, e)
	require.Len(t, second.Body.Data.Replies, 1)
	require.False(t, second.Body.Data.RepliesHasMore)
	require.NotEqual(t, first.Body.Data.Replies[99].ID, second.Body.Data.Replies[0].ID)
	_, e = m.get_issueHTTP(testContext(t, 5), &ObjectInput{EntityID: "3", ID: issue.ID.String(), ReplyCursor: first.Body.Data.RepliesNextCursor})
	require.Error(t, e)
	_, e = m.get_issueHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: issue.ID.String(), ReplyCursor: cursor(cursorScope(testContext(t, 4), 3, "issue_replies:999"), 1)})
	require.Error(t, e)
}

func TestProgressPreviewFreezesSourceWithoutDeliveryAcceptance(t *testing.T) {
	m, db, projects, clients := harness(t)
	ctx := testContext(t, 2)
	in := PreviewBody{Title: "Work in progress", SourceStage: "PROGRESS", Artifact: delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}
	var receipt Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; receipt, e = m.createPreviewOn(ctx, tx, 3, in); return e }))
	got, e := m.get_previewHTTP(ctx, &ObjectInput{EntityID: "3", ID: receipt.ID.String()})
	require.NoError(t, e)
	require.Equal(t, "PROGRESS", got.Body.Data.SourceStage)
	require.Equal(t, guid.ID(40), got.Body.Data.FileVersionID)
	_, e = m.get_previewHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: receipt.ID.String()})
	require.Error(t, e)
	in.ClientVisible = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; receipt, e = m.createPreviewOn(ctx, tx, 3, in); return e }))
	got, e = m.get_previewHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: receipt.ID.String()})
	require.NoError(t, e)
	require.Equal(t, "PROGRESS", got.Body.Data.SourceStage)
	projects.stale = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createPreviewOn(ctx, tx, 3, in); return e }))
	projects.stale = false
	clients.revoked = true
	_, e = m.get_previewHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: receipt.ID.String()})
	require.Error(t, e)
}

func TestProgressPreviewUsesFileSourceBeforeResult(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	source := &delivery.ClientSourceRef{WorkItemID: 11, FileID: 60, VersionID: 40, SHA256: strings.Repeat("a", 64)}
	in := PreviewBody{Title: "Unsubmitted source", SourceStage: "PROGRESS", ProgressSource: source}
	var created Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; created, e = m.createPreviewOn(ctx, tx, 3, in); return e }))
	row, e := m.previewOn(ctx, db, 3, created.ID, false)
	require.NoError(t, e)
	require.NotNil(t, row.SourceFileID)
	require.Equal(t, source.FileID, *row.SourceFileID)
	require.Equal(t, source.WorkItemID, row.WorkItemID)
	require.Zero(t, row.ResultID)
	require.Zero(t, row.EvidenceID)
	require.Equal(t, source.VersionID, row.FileVersionID)
	require.Equal(t, source.SHA256, row.ContentHash)
	projects.stale = true
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createPreviewOn(ctx, tx, 3, in); return e }))
	projects.stale = false
	in.SourceStage = "ACCEPTED"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createPreviewOn(ctx, tx, 3, in); return e }), ErrInvalid)
	in.SourceStage = "PROGRESS"
	in.Artifact.ResultID = 20
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createPreviewOn(ctx, tx, 3, in); return e }), ErrInvalid)
}

package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"context"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestCollaborationHTTPCommandsQueriesAndReplay(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx, client := testContext(t, 2), testContext(t, 4)
	sheet := draftAndPublish(t, m, db)
	_, e := m.signSheetHTTP(client, &EditInput[SignoffBody]{EntityID: "3", ID: sheet.ID.String(), Key: "sign", IfMatch: fmt.Sprintf("\"%d\"", sheet.Version), Body: SignoffBody{Revision: 1, ContentHash: sheet.ContentHash, Decision: "SIGN", Reason: "Reviewed"}})
	require.NoError(t, e)
	change, e := m.create_changeHTTP(client, &CreateInput[ChangeBody]{EntityID: "3", Key: "change", Body: ChangeBody{SheetID: sheet.ID, BaseRevision: 1, Description: "More scope"}})
	require.NoError(t, e)
	assessed, e := m.changeHTTP(ctx, &EditInput[ChangeAction]{EntityID: "3", ID: change.Body.Data.ID.String(), Key: "assess", IfMatch: "\"1\"", Body: ChangeAction{Action: "ASSESS", Impact: "Schedule impact"}})
	require.NoError(t, e)
	require.Equal(t, 2, assessed.Body.Data.Version)
	cv, e := m.get_changeHTTP(client, &ObjectInput{EntityID: "3", ID: change.Body.Data.ID.String()})
	require.NoError(t, e)
	_, e = m.changeHTTP(client, &EditInput[ChangeAction]{EntityID: "3", ID: cv.Body.Data.ID.String(), Key: "confirm", IfMatch: "\"2\"", Body: ChangeAction{Action: "CONFIRM", ProposalHash: cv.Body.Data.ProposalHash}})
	require.NoError(t, e)
	changes, e := m.list_changeHTTP(client, &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, e)
	require.Len(t, changes.Body.Data.Items, 1)
	issue, e := m.create_issueHTTP(client, &CreateInput[IssueBody]{EntityID: "3", Key: "issue", Body: IssueBody{Title: "Problem", Description: "Observed"}})
	require.NoError(t, e)
	_, e = m.issueHTTP(ctx, &EditInput[IssueAction]{EntityID: "3", ID: issue.Body.Data.ID.String(), Key: "reply", IfMatch: "\"1\"", Body: IssueAction{State: "RESPONDED", Reply: "Public reply"}})
	require.NoError(t, e)
	ref := delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}
	preview, e := m.create_previewHTTP(ctx, &CreateInput[PreviewBody]{EntityID: "3", Key: "preview", Body: PreviewBody{Title: "Approved", Artifact: ref, ClientVisible: true}})
	require.NoError(t, e)
	pv, e := m.get_previewHTTP(client, &ObjectInput{EntityID: "3", ID: preview.Body.Data.ID.String()})
	require.NoError(t, e)
	annotation, e := m.create_annotationHTTP(client, &CreateInput[AnnotationBody]{EntityID: "3", Key: "comment", Body: AnnotationBody{PreviewID: pv.Body.Data.ID, FileVersionID: pv.Body.Data.FileVersionID, ContentHash: pv.Body.Data.ContentHash, Anchor: ArtifactAnchor{Kind: "PAGE", Page: 1}, Body: "Comment"}})
	require.NoError(t, e)
	_, e = m.get_annotationHTTP(client, &ObjectInput{EntityID: "3", ID: annotation.Body.Data.ID.String()})
	require.NoError(t, e)
	previews, e := m.list_previewHTTP(client, &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, e)
	require.Len(t, previews.Body.Data.Items, 1)
	pkg, e := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "package", Body: PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{ref}}})
	require.NoError(t, e)
	pub, e := publishBuiltPackageHTTP(t, m, ctx, &EditInput[PackagePublishBody]{Body: PackagePublishBody{ReauthProof: "test-publish-proof"}, EntityID: "3", ID: pkg.Body.Data.ID.String(), Key: "send", IfMatch: "\"1\""})
	require.NoError(t, e)
	require.Equal(t, 2, pub.Body.Data.Version)
	packages, e := m.list_packageHTTP(client, &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, e)
	require.Len(t, packages.Body.Data.Items, 1)
	packageView := packages.Body.Data.Items[0]
	_, e = m.package_acceptanceHTTP(client, &EditInput[PackageDecision]{EntityID: "3", ID: packageView.ID.String(), Key: "receipt", IfMatch: "\"2\"", Body: PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: packageView.ContentHash, Decision: "ACCEPT", Reason: "Checked"}})
	require.NoError(t, e)
	c, e := m.create_test_caseHTTP(ctx, &CreateInput[TestCaseBody]{EntityID: "3", Key: "case", Body: TestCaseBody{Title: "Login", Steps: "Log in", Expected: "Home", WorkItemID: 11}})
	require.NoError(t, e)
	_, e = m.test_case_editHTTP(ctx, &EditInput[TestCaseBody]{EntityID: "3", ID: c.Body.Data.ID.String(), Key: "case-edit", IfMatch: "\"1\"", Body: TestCaseBody{Title: "Login", Steps: "Log in", Expected: "Dashboard", WorkItemID: 11}})
	require.NoError(t, e)
	plan, e := m.create_test_planHTTP(ctx, &CreateInput[TestPlanBody]{EntityID: "3", Key: "plan", Body: TestPlanBody{Title: "Regression", CaseIDs: []guid.ID{c.Body.Data.ID}}})
	require.NoError(t, e)
	run, e := m.test_runHTTP(ctx, &EditInput[TestRunBody]{EntityID: "3", ID: plan.Body.Data.ID.String(), Key: "run", IfMatch: "\"1\"", Body: TestRunBody{CaseID: c.Body.Data.ID, CaseVersion: 2, Result: "PASSED", Evidence: "Observed manually"}})
	require.NoError(t, e)
	_, e = m.get_test_caseHTTP(ctx, &ObjectInput{EntityID: "3", ID: c.Body.Data.ID.String()})
	require.NoError(t, e)
	_, e = m.get_test_planHTTP(ctx, &ObjectInput{EntityID: "3", ID: plan.Body.Data.ID.String()})
	require.NoError(t, e)
	_, e = m.get_test_runHTTP(ctx, &ObjectInput{EntityID: "3", ID: run.Body.Data.ID.String()})
	require.NoError(t, e)
	cases, e := m.list_test_caseHTTP(ctx, &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, e)
	require.Len(t, cases.Body.Data.Items, 1)
	plans, e := m.list_test_planHTTP(ctx, &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, e)
	require.Len(t, plans.Body.Data.Items, 1)
	runs, e := m.list_test_runHTTP(ctx, &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, e)
	require.Len(t, runs.Body.Data.Items, 1)
}

func TestProjectSettingsHTTPVersionReplayAndAtomicEvent(t *testing.T) {
	m, _, _, _ := harness(t)
	ctx := testContext(t, 2)
	current, e := m.getSettingsHTTP(ctx, &ListInput{EntityID: "3"})
	require.NoError(t, e)
	require.Equal(t, 1, current.Body.Data.Version)
	description := "Updated brief"
	in := &SettingsEditInput{EntityID: "3", IfMatch: "\"1\"", Key: "settings-update", Body: delivery.ClientSettingsPatch{Description: &description}}
	publisher := m.events
	m.events = failPublisher{}
	_, e = m.patchSettingsHTTP(ctx, in)
	require.Error(t, e)
	current, e = m.getSettingsHTTP(ctx, &ListInput{EntityID: "3"})
	require.NoError(t, e)
	require.Equal(t, 1, current.Body.Data.Version)
	m.events = publisher
	first, e := m.patchSettingsHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, 2, first.Body.Data.Version)
	replay, e := m.patchSettingsHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	in.Key = "settings-stale"
	_, e = m.patchSettingsHTTP(ctx, in)
	require.Error(t, e)
	_, e = m.getSettingsHTTP(testContext(t, 4), &ListInput{EntityID: "3"})
	require.Error(t, e)
	_, e = m.patchSettingsHTTP(testContext(t, 4), in)
	require.Error(t, e)
	_, e = m.getSettingsHTTP(ctx, &ListInput{EntityID: "bad"})
	require.Error(t, e)
	in.EntityID = "bad"
	_, e = m.patchSettingsHTTP(ctx, in)
	require.Error(t, e)
	in.EntityID = "3"
	in.IfMatch = "bad"
	_, e = m.patchSettingsHTTP(ctx, in)
	require.Error(t, e)
	m.runtime = nil
	_, e = m.getSettingsHTTP(ctx, &ListInput{EntityID: "3"})
	require.Error(t, e)
}

func TestProjectSettingsFailuresCannotAdvanceVersionOrPublish(t *testing.T) {
	for _, fault := range []string{"tolerance_query", "open_exception", "settings", "audit", "lock", "unavailable_lock"} {
		t.Run(fault, func(t *testing.T) {
			m, db, projects, _ := harness(t)
			ctx := testContext(t, 2)
			lock := m.principals.Lock
			date := "2027-01-01"
			in := &SettingsEditInput{EntityID: "3", Key: "settings-failure", IfMatch: "\"1\"", Body: delivery.ClientSettingsPatch{TargetDate: &date}}
			switch fault {
			case "tolerance_query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("settings_source_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Tolerance.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "open_exception":
				tol := toleranceFixture(t, m, db, "REQUIREMENT", 10)
				raisedException(t, m, db, tol)
			case "settings":
				projects.deny = true
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "unavailable_lock":
				m.principals.Lock = nil
			}
			out, err := m.patchSettingsHTTP(ctx, in)
			require.Error(t, err)
			require.Nil(t, out)
			require.NoError(t, db.Callback().Query().Remove("settings_source_failure"))
			projects.deny = false
			m.principals.Lock = lock
			current, err := m.getSettingsHTTP(ctx, &ListInput{EntityID: "3"})
			require.NoError(t, err)
			require.Equal(t, 1, current.Body.Data.Version)
		})
	}
}

func TestPackagePublishHTTPFailurePreservesDraftAndRequiresCurrentRecipient(t *testing.T) {
	for _, fault := range []string{"missing", "recipient", "recipient_error", "inactive", "client_error", "proof_missing", "proof_empty", "proof_error"} {
		t.Run(fault, func(t *testing.T) {
			m, db, id := releaseNoteHarness(t)
			in := &EditInput[PackagePublishBody]{EntityID: "3", ID: id.String(), Key: "publish-failure", IfMatch: "\"1\"", Body: PackagePublishBody{ReauthProof: "proof"}}
			want := 503
			switch fault {
			case "missing":
				in.ID = "999"
				want = 404
			case "recipient":
				m.packageRecipient = nil
			case "recipient_error":
				m.packageRecipient = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrUnavailable }
			case "inactive":
				m.clients = &testClients{revoked: true}
				want = 404
			case "client_error":
				m.clients = &testClients{clientErr: ErrUnavailable}
			case "proof_missing":
				m.acceptanceProof = nil
			case "proof_empty":
				in.Body.ReauthProof = ""
				want = 403
			case "proof_error":
				m.acceptanceProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return ErrUnavailable }
			}
			out, err := m.package_publishHTTP(testContext(t, 2), in)
			require.Error(t, err)
			require.Nil(t, out)
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, want, status.GetStatus())
			var row model.DeliveryPackage
			require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", id).Take(&row).Error)
			require.Equal(t, "DRAFT", row.State)
			require.Equal(t, 1, row.Version)
		})
	}
}

func TestDomainListsPropagateStorageFailuresRatherThanEmptySuccess(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	in := &ListInput{EntityID: "3", Limit: 20}
	for _, tc := range []struct {
		name, table string
		read        func() error
	}{
		{"changes", m.names.ChangeRequest.String(), func() error { out, e := m.list_changeHTTP(ctx, in); require.Nil(t, out); return e }},
		{"issues", m.names.ClientIssue.String(), func() error { out, e := m.list_issueHTTP(ctx, in); require.Nil(t, out); return e }},
		{"packages", m.names.DeliveryPackage.String(), func() error { out, e := m.list_packageHTTP(ctx, in); require.Nil(t, out); return e }},
		{"previews", m.names.Preview.String(), func() error { out, e := m.list_previewHTTP(ctx, in); require.Nil(t, out); return e }},
		{"annotations", m.names.Annotation.String(), func() error { out, e := m.list_annotationHTTP(ctx, in); require.Nil(t, out); return e }},
		{"cases", m.names.TestCase.String(), func() error { out, e := m.list_test_caseHTTP(ctx, in); require.Nil(t, out); return e }},
		{"plans", m.names.TestPlan.String(), func() error { out, e := m.list_test_planHTTP(ctx, in); require.Nil(t, out); return e }},
		{"runs", m.names.TestRun.String(), func() error { out, e := m.list_test_runHTTP(ctx, in); require.Nil(t, out); return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("domain_list_read_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == tc.table {
					tx.AddError(ErrUnavailable)
				}
			}))
			var status huma.StatusError
			require.ErrorAs(t, tc.read(), &status)
			require.Equal(t, 503, status.GetStatus())
			require.NoError(t, db.Callback().Query().Remove("domain_list_read_failure"))
		})
	}
}
func TestIssueListPaginationBindsProjectAndPropagatesCorruptRows(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 4)
	var ids []guid.ID
	for i := 0; i < 2; i++ {
		out, e := m.create_issueHTTP(ctx, &CreateInput[IssueBody]{EntityID: "3", Key: fmt.Sprintf("issue-page-%d", i), Body: IssueBody{Title: "Actual observed issue", Description: "Reproducible client report"}})
		require.NoError(t, e)
		ids = append(ids, out.Body.Data.ID)
	}
	first, e := m.list_issueHTTP(ctx, &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, e)
	require.True(t, first.Body.Data.HasMore)
	require.Len(t, first.Body.Data.Items, 1)
	require.Equal(t, ids[1], first.Body.Data.Items[0].ID)
	second, e := m.list_issueHTTP(ctx, &ListInput{EntityID: "3", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, e)
	require.False(t, second.Body.Data.HasMore)
	require.Equal(t, ids[0], second.Body.Data.Items[0].ID)
	out, e := m.list_issueHTTP(testContext(t, 5), &ListInput{EntityID: "3", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.Error(t, e)
	require.Nil(t, out)
	require.NoError(t, db.Table(m.names.ClientIssue.String()).Where("id=?", ids[0]).Update("anchor", "{").Error)
	out, e = m.list_issueHTTP(ctx, &ListInput{EntityID: "3", Limit: 20})
	require.Error(t, e)
	require.Nil(t, out)
}

func TestPrivateTestListsKeepFrozenPlanCasesAndCursorScope(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	var cases, plans []guid.ID
	for i := 0; i < 2; i++ {
		c, e := m.create_test_caseHTTP(ctx, &CreateInput[TestCaseBody]{EntityID: "3", Key: fmt.Sprintf("page-case-%d", i), Body: TestCaseBody{Title: "Observed behavior", Steps: "Run action", Expected: "Expected state", WorkItemID: 11}})
		require.NoError(t, e)
		cases = append(cases, c.Body.Data.ID)
		p, e := m.create_test_planHTTP(ctx, &CreateInput[TestPlanBody]{EntityID: "3", Key: fmt.Sprintf("page-plan-%d", i), Body: TestPlanBody{Title: "Regression", CaseIDs: []guid.ID{c.Body.Data.ID}}})
		require.NoError(t, e)
		plans = append(plans, p.Body.Data.ID)
	}
	first, e := m.list_test_caseHTTP(ctx, &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, e)
	require.True(t, first.Body.Data.HasMore)
	second, e := m.list_test_caseHTTP(ctx, &ListInput{EntityID: "3", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, e)
	require.False(t, second.Body.Data.HasMore)
	require.Equal(t, cases[0], second.Body.Data.Items[0].ID)
	page, e := m.list_test_planHTTP(ctx, &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, e)
	require.True(t, page.Body.Data.HasMore)
	next, e := m.list_test_planHTTP(ctx, &ListInput{EntityID: "3", Limit: 1, Cursor: page.Body.Data.NextCursor})
	require.NoError(t, e)
	require.False(t, next.Body.Data.HasMore)
	require.Equal(t, plans[0], next.Body.Data.Items[0].ID)
	require.Len(t, next.Body.Data.Items[0].Cases, 1)
	out, e := m.list_test_planHTTP(ctx, &ListInput{EntityID: "3", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.Error(t, e)
	require.Nil(t, out)
	require.NoError(t, db.Table(m.names.TestPlan.String()).Where("id=?", plans[0]).Update("cases", "{").Error)
	out, e = m.list_test_planHTTP(ctx, &ListInput{EntityID: "3", Limit: 20})
	require.Error(t, e)
	require.Nil(t, out)
}

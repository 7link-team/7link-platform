package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Exercise every database operation in each command, not just its last write.
// The snapshot also detects updates that escaped a failed transaction.
func collaborationSnapshot(t *testing.T, m *Module, db *gorm.DB) map[string]string {
	t.Helper()
	out := map[string]string{}
	names := reflect.ValueOf(m.names)
	for i := 0; i < names.NumField(); i++ {
		name := fmt.Sprint(names.Field(i).Interface())
		columns, err := db.Migrator().ColumnTypes(name)
		require.NoError(t, err)
		request := db.Table(name)
		// Some aggregates use a composite key and have no id. Ordering every
		// column keeps the complete row multiset deterministic for those too.
		for _, column := range columns {
			request = request.Order(clause.OrderByColumn{Column: clause.Column{Name: column.Name()}})
		}
		var rows []map[string]any
		require.NoError(t, request.Find(&rows).Error)
		data, e := json.Marshal(rows)
		require.NoError(t, e)
		out[name] = string(data)
	}
	for _, name := range []string{"audit_probe", "baseline_probe", "g_domain_events"} {
		var count int64
		require.NoError(t, db.Table(name).Count(&count).Error)
		out[name] = fmt.Sprint(count)
	}
	return out
}

func TestCollaborationEveryDatabaseFailureRollsBackAggregate(t *testing.T) {
	for _, operation := range []string{"draft", "revise", "publish", "sign", "change", "assess", "issue", "reply", "package", "publish_package", "edit_package", "receipt", "preview", "annotation", "case", "edit_case", "plan", "run"} {
		t.Run(operation, func(t *testing.T) {
			m, db, _, _ := harness(t)
			internal, client := testContext(t, 2), testContext(t, 4)
			sheet := draftAndPublish(t, m, db)
			var issue, pkg, preview, testCase, plan, change Receipt
			seed := func(f func(*gorm.DB) error) { require.NoError(t, db.Transaction(f)) }
			seed(func(tx *gorm.DB) error {
				var e error
				issue, e = m.createIssueOn(client, tx, 3, IssueBody{Title: "Issue", Description: "Observed"})
				return e
			})
			seed(func(tx *gorm.DB) error {
				var e error
				pkg, e = m.createPackageOn(internal, tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
				return e
			})
			seed(func(tx *gorm.DB) error {
				var e error
				preview, e = m.createPreviewOn(internal, tx, 3, PreviewBody{Title: "Preview", Artifact: delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}, ClientVisible: true})
				return e
			})
			caseBody := TestCaseBody{Title: "Test", Steps: "Exercise", Expected: "Success", WorkItemID: 11}
			seed(func(tx *gorm.DB) error {
				var e error
				testCase, e = m.writeCaseOn(internal, tx, 3, 0, 0, caseBody)
				return e
			})
			seed(func(tx *gorm.DB) error {
				var e error
				plan, e = m.createPlanOn(internal, tx, 3, TestPlanBody{Title: "Plan", CaseIDs: []guid.ID{testCase.ID}})
				return e
			})
			if operation == "change" || operation == "assess" {
				seed(func(tx *gorm.DB) error {
					var e error
					sheet, e = m.signSheetOn(client, tx, 3, sheet.ID, sheet.Version, SignoffBody{1, sheet.ContentHash, "SIGN", "Reviewed"})
					return e
				})
			}
			if operation == "assess" {
				seed(func(tx *gorm.DB) error {
					var e error
					change, e = m.createChangeOn(client, tx, 3, ChangeBody{sheet.ID, 1, "New scope"})
					return e
				})
			}
			if operation == "receipt" {
				seed(func(tx *gorm.DB) error {
					var e error
					pkg, e = publishBuiltPackage(t, m, internal, tx, 3, pkg.ID, pkg.Version)
					return e
				})
			}
			if operation == "publish" {
				seed(func(tx *gorm.DB) error {
					var e error
					sheet, e = m.draftSheetOn(internal, tx, 3, sheet.ID, sheet.Version, SheetDraft{Title: "Next", SignerID: 4, RequirementIDs: []guid.ID{10}})
					return e
				})
			}
			run := func(tx *gorm.DB) error {
				var e error
				switch operation {
				case "draft":
					_, e = m.draftSheetOn(internal, tx, 3, 0, 0, SheetDraft{Title: "Scope", SignerID: 4, RequirementIDs: []guid.ID{10}})
				case "revise":
					_, e = m.draftSheetOn(internal, tx, 3, sheet.ID, sheet.Version, SheetDraft{Title: "Next", SignerID: 4, RequirementIDs: []guid.ID{10}})
				case "publish":
					_, e = m.publishSheetOn(internal, tx, 3, sheet.ID, sheet.Version)
				case "sign":
					_, e = m.signSheetOn(client, tx, 3, sheet.ID, sheet.Version, SignoffBody{1, sheet.ContentHash, "SIGN", "Reviewed"})
				case "change":
					_, e = m.createChangeOn(client, tx, 3, ChangeBody{sheet.ID, 1, "New scope"})
				case "assess":
					_, e = m.actChangeOn(internal, tx, 3, change.ID, change.Version, ChangeAction{Action: "ASSESS", Impact: "One milestone"})
				case "issue":
					_, e = m.createIssueOn(client, tx, 3, IssueBody{Title: "Issue", Description: "Observed"})
				case "reply":
					_, e = m.actIssueOn(internal, tx, 3, issue.ID, issue.Version, IssueAction{State: "RESPONDED", Reply: "Approved reply"})
				case "package":
					_, e = m.createPackageOn(internal, tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
				case "edit_package":
					_, e = m.editPackageOn(internal, tx, 3, pkg.ID, pkg.Version, PackageBody{Title: "Edited release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
				case "publish_package":
					_, e = publishBuiltPackage(t, m, internal, tx, 3, pkg.ID, pkg.Version)
				case "receipt":
					row, err := m.packageOn(internal, tx, 3, pkg.ID, false)
					if err != nil {
						return err
					}
					_, e = m.decidePackageOn(client, tx, 3, pkg.ID, pkg.Version, PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: row.ContentHash, Decision: "ACCEPT", Reason: "Checked"})
				case "preview":
					_, e = m.createPreviewOn(internal, tx, 3, PreviewBody{Title: "Preview", Artifact: delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}, ClientVisible: true})
				case "annotation":
					_, e = m.createAnnotationOn(client, tx, 3, AnnotationBody{PreviewID: preview.ID, FileVersionID: 40, ContentHash: strings.Repeat("a", 64), Anchor: ArtifactAnchor{Kind: "PAGE", Page: 1}, Body: "Observed"})
				case "case":
					_, e = m.writeCaseOn(internal, tx, 3, 0, 0, caseBody)
				case "edit_case":
					_, e = m.writeCaseOn(internal, tx, 3, testCase.ID, testCase.Version, caseBody)
				case "plan":
					_, e = m.createPlanOn(internal, tx, 3, TestPlanBody{Title: "Plan", CaseIDs: []guid.ID{testCase.ID}})
				case "run":
					_, e = m.recordRunOn(internal, tx, 3, plan.ID, plan.Version, TestRunBody{CaseID: testCase.ID, CaseVersion: 1, Result: "FAILED", Evidence: "Observed"})
				}
				return e
			}
			source := m.ids
			for failAt := 1; failAt < 5; failAt++ {
				before := collaborationSnapshot(t, m, db)
				ids := &unavailableIDs{source: source, at: failAt}
				m.ids = ids
				e := db.Transaction(func(tx *gorm.DB) error {
					if e := run(tx); e != nil {
						return e
					}
					return context.Canceled
				})
				m.ids = source
				require.Equal(t, before, collaborationSnapshot(t, m, db))
				if ids.count < failAt {
					require.ErrorIs(t, e, context.Canceled)
					break
				}
				require.ErrorIs(t, e, ErrUnavailable)
			}
			before := collaborationSnapshot(t, m, db)
			injected := errors.New("injected database failure")
			for failAt := 1; failAt < 40; failAt++ {
				seen := 0
				fail := func(tx *gorm.DB) {
					if strings.Contains(tx.Statement.Table, "clientwork_") {
						seen++
						if seen == failAt {
							_ = tx.AddError(injected)
						}
					}
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("fixture:failure", fail))
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fixture:failure", fail))
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("fixture:failure", fail))
				e := db.Transaction(func(tx *gorm.DB) error {
					if err := run(tx); err != nil {
						return err
					}
					return context.Canceled
				})
				require.NoError(t, db.Callback().Query().Remove("fixture:failure"))
				require.NoError(t, db.Callback().Create().Remove("fixture:failure"))
				require.NoError(t, db.Callback().Update().Remove("fixture:failure"))
				require.Equal(t, before, collaborationSnapshot(t, m, db), "failure at database operation %d", failAt)
				if seen < failAt {
					require.ErrorIs(t, e, context.Canceled)
					break
				}
				require.ErrorIs(t, e, injected, "failure at database operation %d", failAt)
			}
		})
	}
}

package query

import (
	"7link.com/platform/internal/clientwork/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct {
	TestReport                                                                                                                                                                                                                                                                                                                                                                                                                                             gormx.TableName
	PackageSend                                                                                                                                                                                                                                                                                                                                                                                                                                            gormx.TableName
	AcceptanceReminder, ReleaseNoteDraft, IntakeSuggestion, IntakeThread, ReworkDisposition, ClarificationDraft, HiddenCaseArchive, HiddenVerificationReceipt, ClarificationRound, DispatchAction, CommandDecision, DispatchProposal, PackageTimeline, PreviewShare, Tolerance, ToleranceException, Sheets, Revisions, Signoffs, ChangeRequest, ClientIssue, IssueReply, DeliveryPackage, PackageReceipt, Preview, Annotation, TestCase, TestPlan, TestRun gormx.TableName
}

func BindNames(prefix string) TableNames {
	return TableNames{TestReport: gormx.NewTableName(prefix, model.TestReport{}.TableName()), PackageSend: gormx.NewTableName(prefix, model.PackageSend{}.TableName()), AcceptanceReminder: gormx.NewTableName(prefix, model.AcceptanceReminder{}.TableName()), ReleaseNoteDraft: gormx.NewTableName(prefix, model.ReleaseNoteDraft{}.TableName()), IntakeSuggestion: gormx.NewTableName(prefix, model.IntakeSuggestion{}.TableName()), IntakeThread: gormx.NewTableName(prefix, model.IntakeThread{}.TableName()), ReworkDisposition: gormx.NewTableName(prefix, model.ReworkDisposition{}.TableName()), ClarificationDraft: gormx.NewTableName(prefix, model.ClarificationDraft{}.TableName()), HiddenCaseArchive: gormx.NewTableName(prefix, model.HiddenCaseArchive{}.TableName()), HiddenVerificationReceipt: gormx.NewTableName(prefix, model.HiddenVerificationReceipt{}.TableName()), ClarificationRound: gormx.NewTableName(prefix, model.ClarificationRound{}.TableName()), DispatchAction: gormx.NewTableName(prefix, model.DispatchAction{}.TableName()), CommandDecision: gormx.NewTableName(prefix, model.CommandDecision{}.TableName()), DispatchProposal: gormx.NewTableName(prefix, model.DispatchProposal{}.TableName()), PackageTimeline: gormx.NewTableName(prefix, model.PackageTimeline{}.TableName()), PreviewShare: gormx.NewTableName(prefix, model.PreviewShare{}.TableName()), Tolerance: gormx.NewTableName(prefix, model.Tolerance{}.TableName()), ToleranceException: gormx.NewTableName(prefix, model.ToleranceException{}.TableName()), ChangeRequest: gormx.NewTableName(prefix, model.ChangeRequest{}.TableName()), ClientIssue: gormx.NewTableName(prefix, model.ClientIssue{}.TableName()), IssueReply: gormx.NewTableName(prefix, model.IssueReply{}.TableName()), DeliveryPackage: gormx.NewTableName(prefix, model.DeliveryPackage{}.TableName()), PackageReceipt: gormx.NewTableName(prefix, model.PackageReceipt{}.TableName()), Preview: gormx.NewTableName(prefix, model.Preview{}.TableName()), Annotation: gormx.NewTableName(prefix, model.Annotation{}.TableName()), TestCase: gormx.NewTableName(prefix, model.TestCase{}.TableName()), TestPlan: gormx.NewTableName(prefix, model.TestPlan{}.TableName()), TestRun: gormx.NewTableName(prefix, model.TestRun{}.TableName()), Sheets: gormx.NewTableName(prefix, model.Sheet{}.TableName()), Revisions: gormx.NewTableName(prefix, model.SheetRevision{}.TableName()), Signoffs: gormx.NewTableName(prefix, model.Signoff{}.TableName())}
}
func BindSheets(db *gorm.DB, n TableNames) *sheet { return Use(db).Sheet.Table(n.Sheets.String()) }
func BindRevisions(db *gorm.DB, n TableNames) *sheetRevision {
	return Use(db).SheetRevision.Table(n.Revisions.String())
}
func BindSignoffs(db *gorm.DB, n TableNames) *signoff {
	return Use(db).Signoff.Table(n.Signoffs.String())
}

func BindChangeRequest(db *gorm.DB, n TableNames) *changeRequest {
	return Use(db).ChangeRequest.Table(n.ChangeRequest.String())
}
func BindClientIssue(db *gorm.DB, n TableNames) *clientIssue {
	return Use(db).ClientIssue.Table(n.ClientIssue.String())
}
func BindIssueReply(db *gorm.DB, n TableNames) *issueReply {
	return Use(db).IssueReply.Table(n.IssueReply.String())
}
func BindDeliveryPackage(db *gorm.DB, n TableNames) *deliveryPackage {
	return Use(db).DeliveryPackage.Table(n.DeliveryPackage.String())
}
func BindPackageReceipt(db *gorm.DB, n TableNames) *packageReceipt {
	return Use(db).PackageReceipt.Table(n.PackageReceipt.String())
}
func BindPreview(db *gorm.DB, n TableNames) *preview {
	return Use(db).Preview.Table(n.Preview.String())
}
func BindAnnotation(db *gorm.DB, n TableNames) *annotation {
	return Use(db).Annotation.Table(n.Annotation.String())
}
func BindTestCase(db *gorm.DB, n TableNames) *testCase {
	return Use(db).TestCase.Table(n.TestCase.String())
}
func BindTestPlan(db *gorm.DB, n TableNames) *testPlan {
	return Use(db).TestPlan.Table(n.TestPlan.String())
}
func BindTestRun(db *gorm.DB, n TableNames) *testRun {
	return Use(db).TestRun.Table(n.TestRun.String())
}

func BindTolerance(db *gorm.DB, n TableNames) *tolerance {
	return Use(db).Tolerance.Table(n.Tolerance.String())
}
func BindToleranceException(db *gorm.DB, n TableNames) *toleranceException {
	return Use(db).ToleranceException.Table(n.ToleranceException.String())
}

func BindPreviewShare(db *gorm.DB, n TableNames) *previewShare {
	return Use(db).PreviewShare.Table(n.PreviewShare.String())
}

func BindPackageTimeline(db *gorm.DB, n TableNames) *packageTimeline {
	return Use(db).PackageTimeline.Table(n.PackageTimeline.String())
}

func BindDispatchProposal(db *gorm.DB, n TableNames) *dispatchProposal {
	return Use(db).DispatchProposal.Table(n.DispatchProposal.String())
}

func BindCommandDecision(db *gorm.DB, n TableNames) *commandDecision {
	return Use(db).CommandDecision.Table(n.CommandDecision.String())
}

func BindDispatchAction(db *gorm.DB, n TableNames) *dispatchAction {
	return Use(db).DispatchAction.Table(n.DispatchAction.String())
}

func BindClarificationRound(db *gorm.DB, n TableNames) *clarificationRound {
	return Use(db).ClarificationRound.Table(n.ClarificationRound.String())
}

func BindHiddenCaseArchive(db *gorm.DB, n TableNames) *hiddenCaseArchive {
	return Use(db).HiddenCaseArchive.Table(n.HiddenCaseArchive.String())
}
func BindHiddenVerificationReceipt(db *gorm.DB, n TableNames) *hiddenVerificationReceipt {
	return Use(db).HiddenVerificationReceipt.Table(n.HiddenVerificationReceipt.String())
}

func BindClarificationDraft(db *gorm.DB, n TableNames) *clarificationDraft {
	return Use(db).ClarificationDraft.Table(n.ClarificationDraft.String())
}

func BindReworkDisposition(db *gorm.DB, n TableNames) *reworkDisposition {
	return Use(db).ReworkDisposition.Table(n.ReworkDisposition.String())
}

func BindIntakeThread(db *gorm.DB, n TableNames) *intakeThread {
	return Use(db).IntakeThread.Table(n.IntakeThread.String())
}

func BindIntakeSuggestion(db *gorm.DB, n TableNames) *intakeSuggestion {
	return Use(db).IntakeSuggestion.Table(n.IntakeSuggestion.String())
}

func BindReleaseNoteDraft(db *gorm.DB, n TableNames) *releaseNoteDraft {
	return Use(db).ReleaseNoteDraft.Table(n.ReleaseNoteDraft.String())
}

func BindAcceptanceReminder(db *gorm.DB, n TableNames) *acceptanceReminder {
	return Use(db).AcceptanceReminder.Table(n.AcceptanceReminder.String())
}

func BindPackageSend(db *gorm.DB, n TableNames) *packageSend {
	return Use(db).PackageSend.Table(n.PackageSend.String())
}

func BindTestReport(db *gorm.DB, n TableNames) *testReport {
	return Use(db).TestReport.Table(n.TestReport.String())
}

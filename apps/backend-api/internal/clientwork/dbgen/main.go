package main

import (
	"7link.com/platform/internal/clientwork/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.TestReport{}, model.PackageSend{}, model.AcceptanceReminder{}, model.ReleaseNoteDraft{}, model.IntakeSuggestion{}, model.IntakeThread{}, model.ReworkDisposition{}, model.ClarificationDraft{}, model.HiddenCaseArchive{}, model.HiddenVerificationReceipt{}, model.ClarificationRound{}, model.Sheet{}, model.SheetRevision{}, model.Signoff{}, model.ChangeRequest{}, model.ClientIssue{}, model.IssueReply{}, model.DeliveryPackage{}, model.PackageReceipt{}, model.Preview{}, model.Annotation{}, model.TestCase{}, model.TestPlan{}, model.TestRun{}, model.Tolerance{}, model.ToleranceException{}, model.PreviewShare{}, model.PackageTimeline{}, model.DispatchProposal{}, model.CommandDecision{}, model.DispatchAction{})
}

package query

import (
	"7link.com/platform/internal/execution/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct {
	CommentEvents                                                                                                                            gormx.TableName
	Merges                                                                                                                                   gormx.TableName
	ClientCommentLinks                                                                                                                       gormx.TableName
	HandoverArtifacts                                                                                                                        gormx.TableName
	Verifiers, VerifierVersions                                                                                                              gormx.TableName
	PreviewSessions, PreviewAccess                                                                                                           gormx.TableName
	Executions, Receipts, Logs, Evidence, VerificationTasks, Approvals, ProjectPolicies, CommentThreads, CommentMessages, CommentConversions gormx.TableName
}

func BindNames(prefix string) TableNames {
	return TableNames{
		CommentEvents:      gormx.NewTableName(prefix, model.ArtifactCommentEvent{}.TableName()),
		Merges:             gormx.NewTableName(prefix, model.Merge{}.TableName()),
		ClientCommentLinks: gormx.NewTableName(prefix, model.ClientCommentLink{}.TableName()),
		HandoverArtifacts:  gormx.NewTableName(prefix, model.HandoverArtifact{}.TableName()),
		Verifiers:          gormx.NewTableName(prefix, model.Verifier{}.TableName()),
		VerifierVersions:   gormx.NewTableName(prefix, model.VerifierVersion{}.TableName()),
		PreviewSessions:    gormx.NewTableName(prefix, model.PreviewSession{}.TableName()),
		PreviewAccess:      gormx.NewTableName(prefix, model.PreviewAccess{}.TableName()),
		CommentThreads:     gormx.NewTableName(prefix, model.ArtifactCommentThread{}.TableName()),
		CommentMessages:    gormx.NewTableName(prefix, model.ArtifactCommentMessage{}.TableName()),
		CommentConversions: gormx.NewTableName(prefix, model.ArtifactCommentConversion{}.TableName()),
		ProjectPolicies:    gormx.NewTableName(prefix, model.ProjectExecutionPolicy{}.TableName()),
		Approvals:          gormx.NewTableName(prefix, model.Approval{}.TableName()),
		Executions:         gormx.NewTableName(prefix, model.Execution{}.TableName()),
		Receipts:           gormx.NewTableName(prefix, model.Receipt{}.TableName()),
		Logs:               gormx.NewTableName(prefix, model.Log{}.TableName()),
		Evidence:           gormx.NewTableName(prefix, model.Evidence{}.TableName()),
		VerificationTasks:  gormx.NewTableName(prefix, model.VerificationTask{}.TableName()),
	}
}
func BindHandoverArtifacts(db *gorm.DB, n TableNames) *handoverArtifact {
	return Use(db).HandoverArtifact.Table(n.HandoverArtifacts.String())
}
func BindVerificationTasks(db *gorm.DB, n TableNames) *verificationTask {
	return Use(db).VerificationTask.Table(n.VerificationTasks.String())
}
func BindExecutions(db *gorm.DB, n TableNames) *execution {
	return Use(db).Execution.Table(n.Executions.String())
}
func BindReceipts(db *gorm.DB, n TableNames) *receipt {
	return Use(db).Receipt.Table(n.Receipts.String())
}
func BindLogs(db *gorm.DB, n TableNames) *log { return Use(db).Log.Table(n.Logs.String()) }
func BindEvidence(db *gorm.DB, n TableNames) *evidence {
	return Use(db).Evidence.Table(n.Evidence.String())
}

func BindApprovals(db *gorm.DB, n TableNames) *approval {
	return Use(db).Approval.Table(n.Approvals.String())
}

func BindProjectPolicies(db *gorm.DB, n TableNames) *projectExecutionPolicy {
	return Use(db).ProjectExecutionPolicy.Table(n.ProjectPolicies.String())
}

func BindPreviewSessions(db *gorm.DB, n TableNames) *previewSession {
	return Use(db).PreviewSession.Table(n.PreviewSessions.String())
}
func BindPreviewAccess(db *gorm.DB, n TableNames) *previewAccess {
	return Use(db).PreviewAccess.Table(n.PreviewAccess.String())
}

func BindClientCommentLinks(db *gorm.DB, n TableNames) *clientCommentLink {
	return Use(db).ClientCommentLink.Table(n.ClientCommentLinks.String())
}
func BindArtifactCommentThreads(db *gorm.DB, n TableNames) *artifactCommentThread {
	return Use(db).ArtifactCommentThread.Table(n.CommentThreads.String())
}

func BindArtifactCommentMessages(db *gorm.DB, n TableNames) *artifactCommentMessage {
	return Use(db).ArtifactCommentMessage.Table(n.CommentMessages.String())
}
func BindArtifactCommentConversions(db *gorm.DB, n TableNames) *artifactCommentConversion {
	return Use(db).ArtifactCommentConversion.Table(n.CommentConversions.String())
}

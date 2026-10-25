package main

import (
	"7link.com/platform/internal/execution/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.ArtifactCommentEvent{}, model.Merge{}, model.ClientCommentLink{}, model.HandoverArtifact{}, model.Verifier{}, model.VerifierVersion{}, model.PreviewSession{}, model.PreviewAccess{}, model.ArtifactCommentThread{}, model.ArtifactCommentMessage{}, model.ArtifactCommentConversion{}, model.Execution{}, model.Receipt{}, model.Log{}, model.Evidence{}, model.VerificationTask{}, model.Approval{}, model.ProjectExecutionPolicy{})
}

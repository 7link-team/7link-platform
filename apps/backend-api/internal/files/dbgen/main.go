// Command dbgen generates the file module's typed queries.
package main

import (
	"7link.com/platform/internal/files/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.Object{}, model.AccountReference{}, model.AccountPreparation{}, model.Upload{}, model.UploadPart{}, model.Space{}, model.Node{}, model.NodeClosure{}, model.Version{}, model.TrashBatch{}, model.TrashRoot{}, model.Operation{}, model.OperationItem{})
}

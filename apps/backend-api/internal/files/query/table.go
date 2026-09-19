package query

import (
	"7link.com/platform/internal/files/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct {
	Objects             gormx.TableName
	AccountReferences   gormx.TableName
	AccountPreparations gormx.TableName
	Uploads             gormx.TableName
	UploadParts         gormx.TableName
	Spaces              gormx.TableName
	Nodes               gormx.TableName
	NodeClosure         gormx.TableName
	NodeACL             gormx.TableName
	Versions            gormx.TableName
	TrashBatches        gormx.TableName
	TrashRoots          gormx.TableName
	Operations          gormx.TableName
	OperationItems      gormx.TableName
}

func BindNames(prefix string) TableNames {
	return TableNames{
		Objects:             gormx.NewTableName(prefix, model.Object{}.TableName()),
		AccountReferences:   gormx.NewTableName(prefix, model.AccountReference{}.TableName()),
		AccountPreparations: gormx.NewTableName(prefix, model.AccountPreparation{}.TableName()),
		Uploads:             gormx.NewTableName(prefix, model.Upload{}.TableName()),
		UploadParts:         gormx.NewTableName(prefix, model.UploadPart{}.TableName()),
		Spaces:              gormx.NewTableName(prefix, model.Space{}.TableName()),
		Nodes:               gormx.NewTableName(prefix, model.Node{}.TableName()),
		NodeClosure:         gormx.NewTableName(prefix, model.NodeClosure{}.TableName()),
		NodeACL:             gormx.NewTableName(prefix, model.NodeACL{}.TableName()),
		Versions:            gormx.NewTableName(prefix, model.Version{}.TableName()),
		TrashBatches:        gormx.NewTableName(prefix, model.TrashBatch{}.TableName()),
		TrashRoots:          gormx.NewTableName(prefix, model.TrashRoot{}.TableName()),
		Operations:          gormx.NewTableName(prefix, model.Operation{}.TableName()),
		OperationItems:      gormx.NewTableName(prefix, model.OperationItem{}.TableName()),
	}
}

func Bind(db *gorm.DB, prefix string) *Query {
	names := BindNames(prefix)
	q := Use(db)
	q.Object = *q.Object.Table(names.Objects.String())
	q.AccountReference = *q.AccountReference.Table(names.AccountReferences.String())
	q.AccountPreparation = *q.AccountPreparation.Table(names.AccountPreparations.String())
	q.Upload = *q.Upload.Table(names.Uploads.String())
	q.UploadPart = *q.UploadPart.Table(names.UploadParts.String())
	q.Space = *q.Space.Table(names.Spaces.String())
	q.Node = *q.Node.Table(names.Nodes.String())
	q.NodeClosure = *q.NodeClosure.Table(names.NodeClosure.String())
	q.NodeACL = *q.NodeACL.Table(names.NodeACL.String())
	q.Version = *q.Version.Table(names.Versions.String())
	q.TrashBatch = *q.TrashBatch.Table(names.TrashBatches.String())
	q.TrashRoot = *q.TrashRoot.Table(names.TrashRoots.String())
	q.Operation = *q.Operation.Table(names.Operations.String())
	q.OperationItem = *q.OperationItem.Table(names.OperationItems.String())
	return q
}

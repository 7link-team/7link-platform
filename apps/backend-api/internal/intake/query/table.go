package query

import (
	"7link.com/platform/internal/intake/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// TableNames binds intake's own logical table names to one deployment.
type TableNames struct{ Contacts, Records gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Contacts: gormx.NewTableName(prefix, model.Contact{}.TableName()), Records: gormx.NewTableName(prefix, model.Record{}.TableName())}
}
func BindContacts(db *gorm.DB, names TableNames) *contact {
	return Use(db).Contact.Table(names.Contacts.String())
}

func BindRecords(db *gorm.DB, names TableNames) *record {
	return Use(db).Record.Table(names.Records.String())
}

package todoi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace = "todo"

	NotFound        = Namespace + ".not_found"
	Forbidden       = Namespace + ".forbidden"
	Deleted         = Namespace + ".deleted"
	NotDeleted      = Namespace + ".not_deleted"
	VersionConflict = Namespace + ".version_conflict"
	VersionRequired = Namespace + ".version_required"
	VersionInvalid  = Namespace + ".version_invalid"
	Unavailable     = Namespace + ".unavailable"

	// Column headers. The template renders these, and the importer matches an
	// uploaded header against every locale's rendering of each one.
	ColumnID          = Namespace + ".column_id"
	ColumnTitle       = Namespace + ".column_title"
	ColumnDescription = Namespace + ".column_description"
	ColumnStatus      = Namespace + ".column_status"
	ColumnPriority    = Namespace + ".column_priority"
	ColumnDueAt       = Namespace + ".column_due_at"
	ColumnCompletedAt = Namespace + ".column_completed_at"
	ColumnVersion     = Namespace + ".column_version"
	ColumnCreatedAt   = Namespace + ".column_created_at"
	ColumnCreatedBy   = Namespace + ".column_created_by"
	ColumnUpdatedAt   = Namespace + ".column_updated_at"
	ColumnUpdatedBy   = Namespace + ".column_updated_by"

	// Transfer failures.
	ImportEmpty         = Namespace + ".import_empty"
	ImportTooManyRows   = Namespace + ".import_too_many_rows"
	ImportTooLarge      = Namespace + ".import_too_large"
	ImportUnknownColumn = Namespace + ".import_unknown_column"
	ImportMissingColumn = Namespace + ".import_missing_column"
	ImportRowsInvalid   = Namespace + ".import_rows_invalid"
	ExportTooManyRows   = Namespace + ".export_too_many_rows"
	FormatUnsupported   = Namespace + ".format_unsupported"
	FileUnreadable      = Namespace + ".file_unreadable"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}

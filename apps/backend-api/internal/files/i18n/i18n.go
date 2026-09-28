// Package filesi18n owns the files module's message keys and locale bundle.
package filesi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace = "files"

	InvalidFileUpdate          = Namespace + ".invalid_file_update"
	InvalidFileContent         = Namespace + ".invalid_file_content"
	InvalidFileRoot            = Namespace + ".invalid_file_root"
	InvalidFileScope           = Namespace + ".invalid_file_scope"
	DirectoryContentNotAllowed = Namespace + ".directory_content_not_allowed"
	InvalidFileKind            = Namespace + ".invalid_file_kind"
	InvalidFileOperation       = Namespace + ".invalid_file_operation"
	InvalidFileRequest         = Namespace + ".invalid_file_request"
	FileServiceUnavailable     = Namespace + ".file_service_unavailable"
	FileRestoreNameConflict    = Namespace + ".file_restore_name_conflict"
	FileStateChanged           = Namespace + ".file_state_changed"
	FileOperationConflict      = Namespace + ".file_operation_conflict"
)

// MenuSystemFiles labels the system file management page the module adds to
// the default system menu. It is a bundle-local key: the menu row stores every
// locale's text rather than a key resolved per request.
const MenuSystemFiles = "menu_system_files"

//go:embed locales/*.json
var locales embed.FS

var labels = platformi18n.NewModuleI18nFS(platformi18n.Base, locales, "locales")

// Translations renders a stored files label in every supported locale.
func Translations(key string) map[string]string {
	return labels.Translations(key)
}

// Register publishes the files locale bundle under its module namespace.
func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}

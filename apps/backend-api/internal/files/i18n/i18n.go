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
)

//go:embed locales/*.json
var locales embed.FS

// Register publishes the files locale bundle under its module namespace.
func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}

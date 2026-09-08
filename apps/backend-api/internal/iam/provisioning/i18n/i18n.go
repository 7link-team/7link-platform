// Package provisioningi18n publishes provisioning's locale bundle.
package provisioningi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace = "provisioning"

	InvalidDirectory             = Namespace + ".invalid_scim_directory"
	DirectoryNotFound            = Namespace + ".scim_directory_not_found"
	DirectoryNameExists          = Namespace + ".scim_directory_name_exists"
	DirectoryVersion             = Namespace + ".scim_directory_version_conflict"
	CredentialNotFound           = Namespace + ".scim_credential_not_found"
	CredentialLimit              = Namespace + ".scim_credential_limit"
	Unavailable                  = Namespace + ".provisioning_unavailable"
	Unauthorized                 = Namespace + ".scim_unauthorized"
	ResourceNotFound             = Namespace + ".scim_resource_not_found"
	InvalidSyntax                = Namespace + ".scim_invalid_syntax"
	InvalidFilter                = Namespace + ".scim_invalid_filter"
	InvalidPath                  = Namespace + ".scim_invalid_path"
	UniquenessConflict           = Namespace + ".scim_uniqueness_conflict"
	VersionConflict              = Namespace + ".scim_version_conflict"
	LastTenantAdministrator      = Namespace + ".scim_last_tenant_administrator"
	TooMany                      = Namespace + ".scim_too_many"
	BulkFailed                   = Namespace + ".scim_bulk_failed"
	OperationFailed              = Namespace + ".scim_operation_failed"
	InvalidTarget                = Namespace + ".invalid_scim_target"
	TargetNotFound               = Namespace + ".scim_target_not_found"
	TargetNameExists             = Namespace + ".scim_target_name_exists"
	TargetVersion                = Namespace + ".scim_target_version_conflict"
	TargetKeyMissing             = Namespace + ".scim_target_key_missing"
	TargetDisabled               = Namespace + ".scim_target_disabled"
	TargetHasLiveMappings        = Namespace + ".scim_target_has_live_mappings"
	TargetServiceAccountInactive = Namespace + ".scim_target_service_account_inactive"
	TargetResourceNotMapped      = Namespace + ".scim_target_resource_not_mapped"
	TargetRemoteFailed           = Namespace + ".scim_target_remote_failed"
)

//go:embed locales/*.json
var locales embed.FS

func Register() error { return platformi18n.RegisterFS(Namespace, locales, "locales") }

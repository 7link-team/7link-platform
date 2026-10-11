package oauth

import (
	"7link.com/platform/internal/iam/oauth/query"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// GrantTableNames is the physical name of every table oauth owns, as oauth hands
// them to the modules that JOIN them.
//
// The fields are named, not looked up by string, so a consumer cannot ask for a
// table oauth does not have and cannot misspell the one it wants.
type GrantTableNames struct {
	Clients            gormx.TableName
	AuthorizationCodes gormx.TableName
	Consents           gormx.TableName
	ConsentRequests    gormx.TableName
	RefreshTokens      gormx.TableName

	DeviceAuthorizations gormx.TableName
	ExchangeGrants       gormx.TableName
}

// PublishedGrantTableNames binds every name oauth owns to one deployment prefix. It
// builds from the module's own BindNames, so the prefix is applied the same way
// it is everywhere else oauth queries, and a table added to oauth cannot be
// forgotten here without the drift test noticing.
func PublishedGrantTableNames(prefix string) GrantTableNames {
	names := query.BindNames(prefix)
	return GrantTableNames{
		Clients:            names.Clients,
		AuthorizationCodes: names.AuthorizationCodes,
		Consents:           names.Consents,
		ConsentRequests:    names.ConsentRequests,
		RefreshTokens:      names.RefreshTokens,

		DeviceAuthorizations: names.DeviceAuthorizations,
		ExchangeGrants:       names.ExchangeGrants,
	}
}

// Bound reports whether these names arrived. PublishedGrantTableNames fills every field
// at once, so one zero field means the consumer was never given the set rather
// than that oauth published an incomplete one.
func (t GrantTableNames) Bound() bool { return !t.RefreshTokens.IsZero() }

package query

import (
	"7link.com/platform/internal/iam/oauth/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type (
	Clients            = client
	AuthorizationCodes = authorizationCode
	Consents           = consent
	ConsentRequests    = consentRequest
	RefreshTokens      = refreshToken

	DeviceAuthorizations = deviceAuthorization
	ExchangeGrants       = tokenExchangeGrant
)

type Tables struct {
	Clients            *Clients
	AuthorizationCodes *AuthorizationCodes
	Consents           *Consents
	ConsentRequests    *ConsentRequests
	RefreshTokens      *RefreshTokens

	DeviceAuthorizations *DeviceAuthorizations
	ExchangeGrants       *ExchangeGrants
}

// TableNames is the physical name of every table oauth owns under one
// deployment prefix.
type TableNames struct {
	Clients              gormx.TableName
	AuthorizationCodes   gormx.TableName
	Consents             gormx.TableName
	ConsentRequests      gormx.TableName
	RefreshTokens        gormx.TableName
	DeviceAuthorizations gormx.TableName
	ExchangeGrants       gormx.TableName
}

// BindNames returns the physical name of every table oauth owns. It is the only
// place the deployment prefix is applied to one of them, so a deployment
// cannot end up with half of oauth's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		Clients:              gormx.NewTableName(prefix, model.Client{}.TableName()),
		AuthorizationCodes:   gormx.NewTableName(prefix, model.AuthorizationCode{}.TableName()),
		Consents:             gormx.NewTableName(prefix, model.Consent{}.TableName()),
		ConsentRequests:      gormx.NewTableName(prefix, model.ConsentRequest{}.TableName()),
		RefreshTokens:        gormx.NewTableName(prefix, model.RefreshToken{}.TableName()),
		DeviceAuthorizations: gormx.NewTableName(prefix, model.DeviceAuthorization{}.TableName()),
		ExchangeGrants:       gormx.NewTableName(prefix, model.TokenExchangeGrant{}.TableName()),
	}
}

// Bind applies prefix to every table owned by the OAuth module. It builds from
// BindNames, so the prefix reaches the generated query objects through one path
// rather than five.
func Bind(db *gorm.DB, prefix string) *Tables {
	return BindWithNames(db, BindNames(prefix))
}

// BindWithNames binds every table owned by the OAuth module to names prepared
// by BindNames. Bind delegates here so a repository carrying its own TableNames
// binds the generated query objects through the same single path.
func BindWithNames(db *gorm.DB, names TableNames) *Tables {
	q := Use(db)
	return &Tables{
		Clients:            q.Client.Table(names.Clients.String()),
		AuthorizationCodes: q.AuthorizationCode.Table(names.AuthorizationCodes.String()),
		Consents:           q.Consent.Table(names.Consents.String()),
		ConsentRequests:    q.ConsentRequest.Table(names.ConsentRequests.String()),
		RefreshTokens:      q.RefreshToken.Table(names.RefreshTokens.String()),

		DeviceAuthorizations: q.DeviceAuthorization.Table(names.DeviceAuthorizations.String()),
		ExchangeGrants:       q.TokenExchangeGrant.Table(names.ExchangeGrants.String()),
	}
}

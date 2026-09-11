package query

import (
	"7link.com/platform/internal/iam/federation/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type (
	Providers                = provider
	IdentityLinks            = identityLink
	SAMLServiceProviders     = sAMLServiceProvider
	SAMLIDPKeys              = sAMLIDPKey
	IdentityLinkTransactions = identityLinkTransaction
)

type Tables struct {
	Providers                *Providers
	IdentityLinks            *IdentityLinks
	SAMLServiceProviders     *SAMLServiceProviders
	SAMLIDPKeys              *SAMLIDPKeys
	IdentityLinkTransactions *IdentityLinkTransactions
}

// TableNames is the physical name of every table federation owns under one
// deployment prefix.
type TableNames struct {
	Providers                gormx.TableName
	IdentityLinks            gormx.TableName
	SAMLServiceProviders     gormx.TableName
	SAMLIDPKeys              gormx.TableName
	IdentityLinkTransactions gormx.TableName
}

// BindNames returns the physical name of every table federation owns. It is the
// only place the deployment prefix is applied to one of them, so a deployment
// cannot end up with half of federation's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		Providers:                gormx.NewTableName(prefix, model.Provider{}.TableName()),
		IdentityLinks:            gormx.NewTableName(prefix, model.IdentityLink{}.TableName()),
		SAMLServiceProviders:     gormx.NewTableName(prefix, model.SAMLServiceProvider{}.TableName()),
		SAMLIDPKeys:              gormx.NewTableName(prefix, model.SAMLIDPKey{}.TableName()),
		IdentityLinkTransactions: gormx.NewTableName(prefix, model.IdentityLinkTransaction{}.TableName()),
	}
}

// Map indexes the names by logical name so the module root can publish them as
// a set to the modules that JOIN federation's tables.
func (names TableNames) Map() map[string]gormx.TableName {
	return map[string]gormx.TableName{
		model.Provider{}.TableName():                names.Providers,
		model.IdentityLink{}.TableName():            names.IdentityLinks,
		model.SAMLServiceProvider{}.TableName():     names.SAMLServiceProviders,
		model.SAMLIDPKey{}.TableName():              names.SAMLIDPKeys,
		model.IdentityLinkTransaction{}.TableName(): names.IdentityLinkTransactions,
	}
}

// Bind applies prefix to every table owned by the federation module. It builds
// from BindNames, so the prefix reaches the generated query objects through one
// path rather than five.
func Bind(db *gorm.DB, prefix string) *Tables {
	names := BindNames(prefix)
	q := Use(db)
	return &Tables{
		Providers:                q.Provider.Table(names.Providers.String()),
		IdentityLinks:            q.IdentityLink.Table(names.IdentityLinks.String()),
		SAMLServiceProviders:     q.SAMLServiceProvider.Table(names.SAMLServiceProviders.String()),
		SAMLIDPKeys:              q.SAMLIDPKey.Table(names.SAMLIDPKeys.String()),
		IdentityLinkTransactions: q.IdentityLinkTransaction.Table(names.IdentityLinkTransactions.String()),
	}
}

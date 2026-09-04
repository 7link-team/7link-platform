package query

import (
	"7link.com/platform/internal/iam/federation/model"
	"gorm.io/gorm"
)

type (
	Providers            = provider
	IdentityLinks        = identityLink
	SAMLServiceProviders = sAMLServiceProvider
	SAMLIDPKeys          = sAMLIDPKey
)

type Tables struct {
	Providers            *Providers
	IdentityLinks        *IdentityLinks
	SAMLServiceProviders *SAMLServiceProviders
	SAMLIDPKeys          *SAMLIDPKeys
}

func Bind(db *gorm.DB, prefix string) *Tables {
	q := Use(db)
	return &Tables{
		Providers:            q.Provider.Table(prefix + model.Provider{}.TableName()),
		IdentityLinks:        q.IdentityLink.Table(prefix + model.IdentityLink{}.TableName()),
		SAMLServiceProviders: q.SAMLServiceProvider.Table(prefix + model.SAMLServiceProvider{}.TableName()),
		SAMLIDPKeys:          q.SAMLIDPKey.Table(prefix + model.SAMLIDPKey{}.TableName()),
	}
}

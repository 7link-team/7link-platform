package query

import (
	"7link.com/platform/internal/iam/identity/model"
	"gorm.io/gorm"
)

type (
	Principals                = principal
	PrincipalLifecycles       = principalLifecycle
	ServiceAccounts           = serviceAccount
	ServiceAccountCredentials = serviceAccountCredential
)

type Tables struct {
	Principals                *Principals
	PrincipalLifecycles       *PrincipalLifecycles
	ServiceAccounts           *ServiceAccounts
	ServiceAccountCredentials *ServiceAccountCredentials
}

// Bind applies prefix to every table owned by the identity module.
func Bind(db *gorm.DB, prefix string) *Tables {
	q := Use(db)
	return &Tables{
		Principals:                q.Principal.Table(prefix + model.Principal{}.TableName()),
		PrincipalLifecycles:       q.PrincipalLifecycle.Table(prefix + model.PrincipalLifecycle{}.TableName()),
		ServiceAccounts:           q.ServiceAccount.Table(prefix + model.ServiceAccount{}.TableName()),
		ServiceAccountCredentials: q.ServiceAccountCredential.Table(prefix + model.ServiceAccountCredential{}.TableName()),
	}
}

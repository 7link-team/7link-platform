package query

import (
	"7link.com/platform/internal/iam/identity/model"
	"gorm.io/gorm"
)

type (
	Principals                = principal
	ClosureRequests           = closureRequest
	PrincipalLifecycles       = principalLifecycle
	ServiceAccounts           = serviceAccount
	ServiceAccountCredentials = serviceAccountCredential
)

type Tables struct {
	ClosureRequests           *ClosureRequests
	Principals                *Principals
	PrincipalLifecycles       *PrincipalLifecycles
	ServiceAccounts           *ServiceAccounts
	ServiceAccountCredentials *ServiceAccountCredentials
}

// Bind applies prefix to every table owned by the identity module.
func Bind(db *gorm.DB, prefix string) *Tables {
	q := Use(db)
	return &Tables{
		ClosureRequests:           q.ClosureRequest.Table(prefix + model.ClosureRequest{}.TableName()),
		Principals:                q.Principal.Table(prefix + model.Principal{}.TableName()),
		PrincipalLifecycles:       q.PrincipalLifecycle.Table(prefix + model.PrincipalLifecycle{}.TableName()),
		ServiceAccounts:           q.ServiceAccount.Table(prefix + model.ServiceAccount{}.TableName()),
		ServiceAccountCredentials: q.ServiceAccountCredential.Table(prefix + model.ServiceAccountCredential{}.TableName()),
	}
}

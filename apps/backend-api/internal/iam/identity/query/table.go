package query

import (
	"7link.com/platform/internal/iam/identity/model"
	gormx "7link.com/platform/pkg/extension/gorm"
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

// TableNames is the physical name of every table identity owns under one
// deployment prefix.
type TableNames struct {
	ClosureRequests           gormx.TableName
	Principals                gormx.TableName
	PrincipalLifecycles       gormx.TableName
	ServiceAccounts           gormx.TableName
	ServiceAccountCredentials gormx.TableName
}

// BindNames returns the physical name of every table identity owns. It is the
// only place the deployment prefix is applied to one of them, so a deployment
// cannot end up with half of identity's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		ClosureRequests:           gormx.NewTableName(prefix, model.ClosureRequest{}.TableName()),
		Principals:                gormx.NewTableName(prefix, model.Principal{}.TableName()),
		PrincipalLifecycles:       gormx.NewTableName(prefix, model.PrincipalLifecycle{}.TableName()),
		ServiceAccounts:           gormx.NewTableName(prefix, model.ServiceAccount{}.TableName()),
		ServiceAccountCredentials: gormx.NewTableName(prefix, model.ServiceAccountCredential{}.TableName()),
	}
}

// Bind applies prefix to every table owned by the identity module. It builds
// from BindNames, so the prefix reaches the generated query objects through one
// path rather than five.
func Bind(db *gorm.DB, prefix string) *Tables {
	names := BindNames(prefix)
	q := Use(db)
	return &Tables{
		ClosureRequests:           q.ClosureRequest.Table(names.ClosureRequests.String()),
		Principals:                q.Principal.Table(names.Principals.String()),
		PrincipalLifecycles:       q.PrincipalLifecycle.Table(names.PrincipalLifecycles.String()),
		ServiceAccounts:           q.ServiceAccount.Table(names.ServiceAccounts.String()),
		ServiceAccountCredentials: q.ServiceAccountCredential.Table(names.ServiceAccountCredentials.String()),
	}
}

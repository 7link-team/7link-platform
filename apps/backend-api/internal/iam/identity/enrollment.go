package identity

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
)

// EnrollmentAccounts supplies deferred IAM enrollment operations.
type EnrollmentAccounts interface {
	EnrollmentAvailable() bool
	ListServiceAccounts(context.Context, guid.ID, string, int, int) ([]ServiceAccount, int64, error)
	ListServiceAccountCredentials(context.Context, guid.ID, guid.ID) ([]ServiceAccountCredential, error)
	CreateServiceAccount(context.Context, guid.ID, string, string, string, *time.Time) (ServiceAccount, error)
	GetServiceAccount(context.Context, guid.ID, guid.ID) (ServiceAccount, error)
	DeleteServiceAccount(context.Context, guid.ID, guid.ID, int64) error
	CreateServiceAccountCredential(context.Context, guid.ID, guid.ID, string, []string, *time.Time) (ServiceAccountCredentialSecret, error)
	RevokeServiceAccountCredential(context.Context, guid.ID, guid.ID, guid.ID) error
}

func (m *Module) EnrollmentAvailable() bool { return m != nil && m.Service != nil }
func (m *Module) provideEnrollment(s *module.Services) error {
	return module.Provide[EnrollmentAccounts](s, m)
}

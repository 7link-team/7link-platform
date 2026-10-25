package oauth

import (
	"context"
	"net/url"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
)

// EnrollmentTokens supplies deferred IAM enrollment operations.
type EnrollmentTokens interface {
	EnrollmentAvailable() bool
	Token(context.Context, url.Values, string) (TokenResponse, error)
	StartDeviceAuthorization(context.Context, string, string) (DeviceAuthorization, error)
	LookupDeviceRequest(context.Context, guid.ID, string) (DeviceRequest, error)
	DecideDeviceRequest(context.Context, guid.ID, string, guid.ID, bool, time.Time, int, string) error
}

func (m *Module) EnrollmentAvailable() bool { return m != nil && m.Service != nil }
func (m *Module) provideEnrollment(s *module.Services) error {
	return module.Provide[EnrollmentTokens](s, m)
}

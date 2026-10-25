package fleet

import (
	"context"
	"net/url"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/infra/guid"
)

// EnrollmentIAM is the consumer contract for existing IAM workflows. IAM owns
// all credentials and OAuth grants; fleet never hashes or validates IAM secrets.
// These calls own transactions, so enrollment must persist retry/compensation
// state instead of pretending they participate in a fleet database transaction.
type EnrollmentIAM struct {
	StartDevice      func(context.Context, string, string) (oauth.DeviceAuthorization, error)
	LookupDevice     func(context.Context, guid.ID, string) (oauth.DeviceRequest, error)
	DecideDevice     func(context.Context, guid.ID, string, guid.ID, bool, time.Time, int, string) error
	CreateAccount    func(context.Context, guid.ID, string, string, string, *time.Time) (identity.ServiceAccount, error)
	GetAccount       func(context.Context, guid.ID, guid.ID) (identity.ServiceAccount, error)
	DeleteAccount    func(context.Context, guid.ID, guid.ID, int64) error
	IssueCredential  func(context.Context, guid.ID, guid.ID, string, []string, *time.Time) (identity.ServiceAccountCredentialSecret, error)
	RevokeCredential func(context.Context, guid.ID, guid.ID, guid.ID) error
	ListCredentials  func(context.Context, guid.ID, guid.ID) ([]identity.ServiceAccountCredential, error)
	FindAccount      func(context.Context, guid.ID, string) (identity.ServiceAccount, bool, error)
	Token            func(context.Context, url.Values, string) (oauth.TokenResponse, error)
}

const IAMBridgeName = "fleet_iam"

func (p EnrollmentIAM) Bound() bool {
	return p.StartDevice != nil && p.LookupDevice != nil && p.DecideDevice != nil && p.CreateAccount != nil && p.GetAccount != nil && p.DeleteAccount != nil && p.IssueCredential != nil && p.RevokeCredential != nil && p.ListCredentials != nil && p.FindAccount != nil && p.Token != nil
}

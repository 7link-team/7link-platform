package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"7link.com/platform/internal/collab"
	deliveryi18n "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/idempotency"
	idemi18n "7link.com/platform/internal/infra/idempotency/i18n"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
)

func TestErrorMappersKeepChosenStatus(t *testing.T) {
	chosen := huma.Error422UnprocessableEntity("invalid input")
	for _, err := range []error{chosen, fmt.Errorf("input: %w", chosen), huma.ErrorWithHeaders(chosen, http.Header{"Retry-After": {"1"}})} {
		require.Same(t, err, projectHTTPError(t.Context(), err))
	}
}

func TestProjectErrorResponsesPreservePublicContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		status int
		code   int
		key    string
		reason string
	}{
		{"invalid_key", idempotency.ErrInvalidKey, 400, 176400, idemi18n.InvalidKey, ""},
		{"key_reused", idempotency.ErrKeyReused, 422, 176425, idemi18n.KeyReused, ""},
		{"code_taken", ErrProjectCodeTaken, 409, 127001, deliveryi18n.ProjectCodeTaken, ""},
		{"name_taken", core.ErrEntityConflict, 409, 127002, deliveryi18n.ProjectNameTaken, ""},
		{"parent", ErrParentNotClient, 422, 127003, deliveryi18n.ParentNotClient, ""},
		{"contact", ErrContactNotFound, 422, 127040, deliveryi18n.ContactNotFound, ""},
		{"intake", ErrIntakeNotFound, 422, 120404, deliveryi18n.IntakeNotFound, ""},
		{"member", ErrInvalidMember, 422, 127043, deliveryi18n.MemberRoleMismatch, ""},
		{"inactive_member", collab.ErrMemberInactive, 422, 127043, deliveryi18n.MemberRoleMismatch, ""},
		{"limit", ErrProjectLimit, 422, 127044, deliveryi18n.ProjectLimitReached, ""},
		{"human_only", ErrProjectNonDelegable, 403, 120403, deliveryi18n.HumanOnly, "NON_DELEGABLE"},
		{"forbidden", ErrProjectForbidden, 403, 120403, respx.Forbidden, "NON_DELEGABLE"},
		{"authorization_changed", core.ErrAuthorizationChanged, 409, 120409, respx.InvalidArgument, ""},
		{"missing_project", ErrProjectNotFound, 404, 120404, respx.NotFound, ""},
		{"missing_entity", core.ErrEntityNotFound, 404, 120404, respx.NotFound, ""},
		{"invalid_input", ErrInvalidArgument, 422, 120400, respx.InvalidArgument, ""},
		{"unavailable", ErrUnavailable, 503, 127004, deliveryi18n.ProvisioningUnavailable, ""},
		{"internal", errors.New("database secret"), 500, 120500, respx.InternalServerError, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Wrapped infrastructure details must never enter the cached public response.
			result, err := projectErrorResult(t.Context(), fmt.Errorf("private SQL details: %w", tc.cause))
			require.NoError(t, err)
			require.Equal(t, tc.status, result.StatusCode)
			var body respx.Envelope[map[string]string]
			require.NoError(t, json.Unmarshal(result.Body, &body))
			require.Equal(t, tc.code, body.Code)
			require.Equal(t, tc.key, body.Message)
			if tc.reason == "" {
				require.Nil(t, body.Data)
			} else {
				require.Equal(t, map[string]string{"reason_code": tc.reason}, body.Data)
			}
			require.NotContains(t, string(result.Body), "private SQL details")
			require.NotContains(t, string(result.Body), "database secret")
		})
	}
}

package organization

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestInvitationNormalization(t *testing.T) {
	t.Parallel()
	_, input, err := normalizeInvitation(1, CreateInvitation{Email: " User@Example.COM ", RoleIDs: []guid.ID{3, 2, 3}})
	require.NoError(t, err)
	assert.Equal(t, "user@example.com", input.Email)
	assert.Equal(t, defaultInvitationTTL, input.TTL)
	assert.Equal(t, []guid.ID{2, 3}, input.RoleIDs)

	for _, input := range []CreateInvitation{
		{}, {Email: "not-an-email"}, {Email: "user@example.com", TTL: time.Minute},
		{Email: "user@example.com", DepartmentID: -1}, {Email: "user@example.com", RoleIDs: []guid.ID{0}},
	} {
		_, _, err := normalizeInvitation(1, input)
		assert.ErrorIs(t, err, ErrInvitationInvalid)
	}
	_, _, err = normalizeInvitation(0, CreateInvitation{Email: "user@example.com"})
	assert.ErrorIs(t, err, ErrInvitationInvalid)
}

func TestInvitationTokenIDParsing(t *testing.T) {
	t.Parallel()
	id, err := invitationIDFromToken("inv1_42.signature")
	require.NoError(t, err)
	assert.EqualValues(t, 42, id)
	for _, token := range []string{"", "invalid", "inv1_missing.signature", "inv1_0.signature", "inv1_42"} {
		_, err := invitationIDFromToken(token)
		assert.ErrorIs(t, err, ErrInvitationCredential)
	}
}

type invitationCredentialFixture struct{ sequence atomic.Int64 }

func (fixture *invitationCredentialFixture) IssueInvitationToken(id guid.ID) (string, string, error) {
	token := fmt.Sprintf("inv1_%d.%s-%d", id, strings.Repeat("s", 40), fixture.sequence.Add(1))
	digest, err := fixture.InvitationTokenDigest(token)
	return token, digest, err
}

func (*invitationCredentialFixture) InvitationTokenDigest(token string) (string, error) {
	if !strings.HasPrefix(token, "inv1_") || !strings.Contains(token, ".") {
		return "", ErrInvitationCredential
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:]), nil
}

func (fixture *invitationCredentialFixture) InvitationTokenMatches(token, digest string) bool {
	expected, err := fixture.InvitationTokenDigest(token)
	return err == nil && subtle.ConstantTimeCompare([]byte(expected), []byte(digest)) == 1
}

type invitationCredentialFailure struct {
	base      invitationCredentialFixture
	issueErr  error
	digestErr error
}

func (fixture *invitationCredentialFailure) IssueInvitationToken(id guid.ID) (string, string, error) {
	if fixture.issueErr != nil {
		return "", "", fixture.issueErr
	}
	return fixture.base.IssueInvitationToken(id)
}

func (fixture *invitationCredentialFailure) InvitationTokenDigest(token string) (string, error) {
	if fixture.digestErr != nil {
		return "", fixture.digestErr
	}
	return fixture.base.InvitationTokenDigest(token)
}

func (fixture *invitationCredentialFailure) InvitationTokenMatches(token, digest string) bool {
	if fixture.digestErr != nil {
		return false
	}
	return fixture.base.InvitationTokenMatches(token, digest)
}

func (h *organizationHarness) seedTenant(t *testing.T, tenantID guid.ID, status string) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_tenants")).Create(map[string]any{
		"id": tenantID, "slug": fmt.Sprintf("tenant-%d", tenantID), "name": fmt.Sprintf("Tenant %d", tenantID),
		"status": status, "version": 1, "created_at": organizationTestNow, "updated_at": organizationTestNow,
	}).Error)
}

func (h *organizationHarness) seedRole(t *testing.T, tenantID, roleID guid.ID) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_roles")).Create(map[string]any{"tenant_id": tenantID, "id": roleID, "name": "Operator"}).Error)
}

func newInvitationService(t *testing.T, h *organizationHarness, credentials InvitationCredentials, principalCalls *atomic.Int64) *InvitationService {
	t.Helper()
	creator := InvitationPrincipalCreator(func(ctx context.Context, db *gorm.DB, tenantID guid.ID, _, _, displayName, email string) (guid.ID, error) {
		id := guid.ID(900 + principalCalls.Add(1))
		err := db.WithContext(ctx).Table(h.table("iam_tenant_members")).Create(map[string]any{
			"tenant_id": tenantID, "principal_id": id, "display_name": displayName, "email": email,
			"status": StatusActive, "created_at": organizationTestNow, "updated_at": organizationTestNow,
		}).Error
		return id, err
	})
	service := newBoundInvitationService(t, h.runtime, h.appender, newOrganizationTestIDs(600), credentials, creator)
	service.now = func() time.Time { return organizationTestNow }
	return service
}

func TestInvitationJoinUsesCurrentAccountAndCannotBeReusedByAnother(t *testing.T) {
	h := newOrganizationHarness(t, "join_")
	h.seedTenant(t, 1, TenantActive)
	h.seedRole(t, 1, 10)
	var calls atomic.Int64
	s := newInvitationService(t, h, &invitationCredentialFixture{}, &calls)
	joined := guid.ID(0)
	s.acceptMember = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) error {
		assert.Equal(t, guid.ID(1), tenant)
		joined = principal
		return nil
	}
	_, token, err := s.Create(t.Context(), 1, CreateInvitation{Email: "invitee@example.test", RoleIDs: []guid.ID{10}})
	require.NoError(t, err)
	accepted, err := s.Accept(t.Context(), AcceptInvitation{Token: token, PrincipalID: 91})
	require.NoError(t, err)
	assert.Equal(t, guid.ID(91), joined)
	assert.Equal(t, guid.ID(91), accepted.PrincipalID)
	assert.Zero(t, calls.Load(), "joining must not create another account")
	again, err := s.Accept(t.Context(), AcceptInvitation{Token: token, PrincipalID: 91})
	require.NoError(t, err)
	assert.True(t, again.AlreadyAccepted)
	_, err = s.Accept(t.Context(), AcceptInvitation{Token: token, PrincipalID: 92})
	assert.ErrorIs(t, err, ErrInvitationCredential)
}

func TestInvitationLifecycleTenantBindingsAndAcceptance(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	h.seedTenant(t, 2, TenantActive)
	h.seedRole(t, 1, 10)
	h.seedRole(t, 2, 20)
	department, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	require.NoError(t, err)
	credentials := &invitationCredentialFixture{}
	var principalCalls atomic.Int64
	service := newInvitationService(t, h, credentials, &principalCalls)

	invitation, token, err := service.Create(t.Context(), 1, CreateInvitation{Email: "User@Example.test", DepartmentID: department.ID, RoleIDs: []guid.ID{10}})
	require.NoError(t, err)
	assert.Equal(t, "user@example.test", invitation.Email)
	assert.NotEmpty(t, token)
	_, _, err = service.Create(t.Context(), 1, CreateInvitation{Email: "other@example.com", RoleIDs: []guid.ID{20}})
	assert.ErrorIs(t, err, ErrInvitationBindingMissing, "a role from another tenant must remain invisible")
	assert.ErrorIs(t, service.Revoke(t.Context(), 2, invitation.ID), ErrInvitationNotFound)
	_, _, err = service.Resend(t.Context(), 2, invitation.ID, 0)
	assert.ErrorIs(t, err, ErrInvitationNotFound)

	accepted, err := service.Accept(t.Context(), AcceptInvitation{Token: token, LoginName: "user", Password: "correct horse battery staple", DisplayName: "User"})
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), accepted.TenantID)
	assert.False(t, accepted.AlreadyAccepted)
	assert.EqualValues(t, 1, principalCalls.Load())
	again, err := service.Accept(t.Context(), AcceptInvitation{Token: token, LoginName: "user", Password: "correct horse battery staple", DisplayName: "User"})
	require.NoError(t, err)
	assert.True(t, again.AlreadyAccepted)
	assert.Equal(t, accepted.PrincipalID, again.PrincipalID)
	assert.EqualValues(t, 1, principalCalls.Load())
	items, err := service.List(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, InvitationAccepted, items[0].Status)
	require.NotNil(t, items[0].AcceptedAt)
	assert.False(t, items[0].AcceptedAt.IsZero())

	var departmentID guid.ID
	require.NoError(t, h.db.Table(h.table("iam_member_departments")).Select("department_id").Where("tenant_id = ? AND principal_id = ?", 1, accepted.PrincipalID).Scan(&departmentID).Error)
	assert.Equal(t, department.ID, departmentID)
	var roleCount int64
	require.NoError(t, h.db.Table(h.table("iam_role_members")).Where("tenant_id = ? AND role_id = ? AND principal_id = ?", 1, 10, accepted.PrincipalID).Count(&roleCount).Error)
	assert.EqualValues(t, 1, roleCount)
	_, err = service.Accept(t.Context(), AcceptInvitation{Token: token + "tampered", LoginName: "other", Password: "correct horse battery staple"})
	assert.ErrorIs(t, err, ErrInvitationCredential)
}

// TestAPendingInvitationSerializesIntoWhatAClientCanRender pins the wire shape
// rather than the Go values. A nil slice marshals to null and a zero time.Time
// marshals to year one, so the plainest invitation there is -- no roles, not yet
// accepted -- is the one a client is most likely to crash on or misrender.
func TestAPendingInvitationSerializesIntoWhatAClientCanRender(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	var principalCalls atomic.Int64
	service := newInvitationService(t, h, &invitationCredentialFixture{}, &principalCalls)

	created, _, err := service.Create(t.Context(), 1, CreateInvitation{Email: "user@example.com"})
	require.NoError(t, err)
	listed, err := service.List(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	for name, invitation := range map[string]Invitation{"the created invitation": created, "the listed invitation": listed[0]} {
		t.Run(name, func(t *testing.T) {
			assert.NotNil(t, invitation.RoleIDs)
			assert.Empty(t, invitation.RoleIDs)
			assert.Nil(t, invitation.AcceptedAt)
			assert.Nil(t, invitation.RevokedAt)
			encoded, err := json.Marshal(invitation)
			require.NoError(t, err)
			assert.Contains(t, string(encoded), `"role_ids":[]`)
			assert.NotContains(t, string(encoded), "accepted_at")
			assert.NotContains(t, string(encoded), "revoked_at")
		})
	}
}

func TestInvitationResendRevocationAndRollback(t *testing.T) {
	h := newOrganizationHarness(t, "")
	h.seedTenant(t, 1, TenantActive)
	credentials := &invitationCredentialFixture{}
	var principalCalls atomic.Int64
	service := newInvitationService(t, h, credentials, &principalCalls)
	invitation, original, err := service.Create(t.Context(), 1, CreateInvitation{Email: "user@example.com", TTL: time.Hour})
	require.NoError(t, err)
	resent, replacement, err := service.Resend(t.Context(), 1, invitation.ID, 2*time.Hour)
	require.NoError(t, err)
	assert.NotEqual(t, original, replacement)
	assert.True(t, resent.ExpiresAt.After(invitation.ExpiresAt))
	_, err = service.Accept(t.Context(), AcceptInvitation{Token: original, LoginName: "user", Password: "correct horse battery staple"})
	assert.ErrorIs(t, err, ErrInvitationCredential)
	require.NoError(t, service.Revoke(t.Context(), 1, invitation.ID))
	_, err = service.Accept(t.Context(), AcceptInvitation{Token: replacement, LoginName: "user", Password: "correct horse battery staple"})
	assert.ErrorIs(t, err, ErrInvitationCredential)

	rollback, rollbackToken, err := service.Create(t.Context(), 1, CreateInvitation{Email: "rollback@example.com"})
	require.NoError(t, err)
	h.auditErr = assert.AnError
	_, err = service.Accept(t.Context(), AcceptInvitation{Token: rollbackToken, LoginName: "rollback", Password: "correct horse battery staple", DisplayName: "Rollback"})
	assert.ErrorIs(t, err, assert.AnError)
	var rowStatus string
	require.NoError(t, h.db.Table(h.table("iam_invitations")).Select("status").Where("tenant_id = ? AND id = ?", 1, rollback.ID).Scan(&rowStatus).Error)
	assert.Equal(t, InvitationPending, rowStatus)
	var memberCount int64
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("tenant_id = ? AND email = ?", 1, "rollback@example.com").Count(&memberCount).Error)
	assert.Zero(t, memberCount)
}

func TestInvitationExpiryBindingAndStateFailures(t *testing.T) {
	h := newOrganizationHarness(t, "")
	h.seedTenant(t, 1, TenantActive)
	h.seedTenant(t, 2, TenantSuspended)
	disabledDepartment, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Disabled", Status: StatusDisabled})
	require.NoError(t, err)
	credentials := &invitationCredentialFixture{}
	var principalCalls atomic.Int64
	service := newInvitationService(t, h, credentials, &principalCalls)

	_, err = service.List(t.Context(), 0)
	assert.ErrorIs(t, err, ErrInvitationInvalid)
	assert.ErrorIs(t, service.Revoke(t.Context(), 0, 1), ErrInvitationInvalid)
	_, _, err = service.Resend(t.Context(), 1, 1, time.Minute)
	assert.ErrorIs(t, err, ErrInvitationInvalid)
	_, _, err = service.Create(t.Context(), 999, CreateInvitation{Email: "missing@example.com"})
	assert.ErrorIs(t, err, ErrTenantNotFound)
	_, _, err = service.Create(t.Context(), 2, CreateInvitation{Email: "suspended@example.com"})
	assert.ErrorIs(t, err, ErrInvitationBindingInactive)
	_, _, err = service.Create(t.Context(), 1, CreateInvitation{Email: "missing-department@example.com", DepartmentID: 999})
	assert.ErrorIs(t, err, ErrInvitationBindingMissing)
	_, _, err = service.Create(t.Context(), 1, CreateInvitation{Email: "disabled-department@example.com", DepartmentID: disabledDepartment.ID})
	assert.ErrorIs(t, err, ErrInvitationBindingInactive)

	pending, token, err := service.Create(t.Context(), 1, CreateInvitation{Email: "expired@example.com", TTL: time.Hour})
	require.NoError(t, err)
	service.now = func() time.Time { return organizationTestNow.Add(2 * time.Hour) }
	items, err := service.List(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, InvitationExpired, items[0].Status)
	assert.ErrorIs(t, service.Revoke(t.Context(), 1, pending.ID), ErrInvitationExpired)
	_, err = service.Accept(t.Context(), AcceptInvitation{
		Token: token, LoginName: "expired", Password: "correct horse battery staple",
	})
	assert.ErrorIs(t, err, ErrInvitationExpired)

	service.now = func() time.Time { return organizationTestNow }
	resent, _, err := service.Resend(t.Context(), 1, pending.ID, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, InvitationPending, resent.Status)
	require.NoError(t, service.Revoke(t.Context(), 1, pending.ID))
	assert.ErrorIs(t, service.Revoke(t.Context(), 1, pending.ID), ErrInvitationState)
	items, err = service.List(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, InvitationRevoked, items[0].Status)
	require.NotNil(t, items[0].RevokedAt)
	assert.False(t, items[0].RevokedAt.IsZero())
	_, _, err = service.Resend(t.Context(), 1, pending.ID, time.Hour)
	assert.ErrorIs(t, err, ErrInvitationState)
}

func TestInvitationCredentialAndPrincipalFailures(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	var principalCalls atomic.Int64
	failingCredentials := &invitationCredentialFailure{issueErr: assert.AnError}
	service := newInvitationService(t, h, failingCredentials, &principalCalls)
	_, _, err := service.Create(t.Context(), 1, CreateInvitation{Email: "issue@example.com"})
	assert.ErrorIs(t, err, assert.AnError)

	credentials := &invitationCredentialFixture{}
	service = newBoundInvitationService(t,
		h.runtime, h.appender, newOrganizationTestIDs(1700), credentials,
		func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
			return 0, assert.AnError
		},
	)
	service.now = func() time.Time { return organizationTestNow }
	invitation, token, err := service.Create(t.Context(), 1, CreateInvitation{Email: "principal@example.com"})
	require.NoError(t, err)
	_, err = service.Accept(t.Context(), AcceptInvitation{
		Token: token, LoginName: "principal", Password: "correct horse battery staple",
	})
	assert.ErrorIs(t, err, assert.AnError)

	digestFailure := &invitationCredentialFailure{digestErr: assert.AnError}
	digestService := newBoundInvitationService(t,
		h.runtime, h.appender, newOrganizationTestIDs(1800), digestFailure,
		func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
			return 1, nil
		},
	)
	_, err = digestService.Accept(t.Context(), AcceptInvitation{
		Token: token, LoginName: "principal", Password: "correct horse battery staple",
	})
	assert.ErrorIs(t, err, ErrInvitationCredential)

	failingCredentials.issueErr = assert.AnError
	service.credentials = failingCredentials
	_, _, err = service.Resend(t.Context(), 1, invitation.ID, time.Hour)
	assert.ErrorIs(t, err, assert.AnError)
	_, err = service.Accept(t.Context(), AcceptInvitation{Token: token, LoginName: "", Password: "short"})
	assert.ErrorIs(t, err, ErrInvitationCredential)
}

// TestInvitationAcceptStopsWhenTheRowIsConsumedMidTransaction pins the guard on
// the claiming UPDATE. Accept reads the invitation, creates the principal, and
// only then claims the row, so the claim repeats the whole pending-token-
// unexpired predicate instead of trusting the earlier read. Here the row stops
// being claimable while the principal is being created -- the shape a second
// acceptance racing the first would take -- and the claim must match nothing
// and roll the acceptance back rather than hand out a second principal for an
// invitation that is no longer pending.
func TestInvitationAcceptStopsWhenTheRowIsConsumedMidTransaction(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	var consumed guid.ID
	creator := InvitationPrincipalCreator(func(ctx context.Context, db *gorm.DB, tenantID guid.ID, _, _, displayName, email string) (guid.ID, error) {
		err := db.WithContext(ctx).Table(h.table("iam_invitations")).Where("tenant_id = ? AND id = ?", tenantID, consumed).
			Updates(map[string]any{"status": InvitationRevoked, "revoked_at": organizationTestNow, "updated_at": organizationTestNow}).Error
		if err != nil {
			return 0, err
		}
		return 901, db.WithContext(ctx).Table(h.table("iam_tenant_members")).Create(map[string]any{
			"tenant_id": tenantID, "principal_id": 901, "display_name": displayName, "email": email,
			"status": StatusActive, "created_at": organizationTestNow, "updated_at": organizationTestNow,
		}).Error
	})
	service := newBoundInvitationService(t, h.runtime, h.appender, newOrganizationTestIDs(700), &invitationCredentialFixture{}, creator)
	service.now = func() time.Time { return organizationTestNow }

	invitation, token, err := service.Create(t.Context(), 1, CreateInvitation{Email: "user@example.com"})
	require.NoError(t, err)
	consumed = invitation.ID

	_, err = service.Accept(t.Context(), AcceptInvitation{Token: token, LoginName: "user", Password: "correct horse battery staple", DisplayName: "User"})
	assert.ErrorIs(t, err, ErrInvitationCredential)

	var status string
	require.NoError(t, h.db.Table(h.table("iam_invitations")).Select("status").Where("tenant_id = ? AND id = ?", 1, invitation.ID).Scan(&status).Error)
	assert.Equal(t, InvitationPending, status, "the refused acceptance must roll back the row it failed to claim")
	var members int64
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("tenant_id = ? AND principal_id = ?", 1, 901).Count(&members).Error)
	assert.Zero(t, members, "the principal created inside the refused acceptance must not survive it")
}

// Acceptance attaches an invitation's roles with no actor to check, so the
// inviter's authority is checked where the roles are chosen and re-issued.
func TestInvitationRolesMustBeGrantableByTheInviter(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	h.seedRole(t, 1, 10)
	h.seedRole(t, 1, 11)
	var principalCalls atomic.Int64
	service := newInvitationService(t, h, &invitationCredentialFixture{}, &principalCalls)
	guard := &grantingAdministratorGuard{}
	service.administrators = guard
	inviter := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: 5, TenantID: 1})
	invitations := func() int64 {
		var count int64
		require.NoError(t, h.db.Table(h.table("iam_invitations")).Where("tenant_id = 1").Count(&count).Error)
		return count
	}

	guard.requireErr = core.ErrPrivilegeEscalation
	_, _, err := service.Create(inviter, 1, CreateInvitation{Email: "user@example.com", RoleIDs: []guid.ID{11, 10}})
	require.ErrorIs(t, err, core.ErrPrivilegeEscalation)
	assert.Equal(t, [][]guid.ID{{10, 11}}, guard.required)
	assert.Equal(t, []guid.ID{1}, guard.prepared, "the grant fence precedes the policy lock")
	assert.Zero(t, invitations(), "a refused invitation leaves nothing to accept")

	guard.requireErr = nil
	pending, _, err := service.Create(inviter, 1, CreateInvitation{Email: "user@example.com", RoleIDs: []guid.ID{10}})
	require.NoError(t, err)
	_, _, err = service.Create(inviter, 1, CreateInvitation{Email: "plain@example.com"})
	require.NoError(t, err)
	assert.Len(t, guard.required, 2, "an invitation without roles grants nothing")

	guard.requireErr = core.ErrPrivilegeEscalation
	before, err := getInvitation(t.Context(), service.repo, 1, pending.ID)
	require.NoError(t, err)
	_, _, err = service.Resend(inviter, 1, pending.ID, 0)
	require.ErrorIs(t, err, core.ErrPrivilegeEscalation, "re-issuing a credential re-grants the roles")
	assert.Equal(t, []guid.ID{10}, guard.required[len(guard.required)-1])
	after, err := getInvitation(t.Context(), service.repo, 1, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, before.TokenHMAC, after.TokenHMAC)
	assert.Equal(t, before.ExpiresAt, after.ExpiresAt)

	// Without a caller there is no grantor whose authority could be exceeded.
	calls := len(guard.required)
	_, _, err = service.Resend(t.Context(), 1, pending.ID, 0)
	require.NoError(t, err)
	assert.Len(t, guard.required, calls)

	service.administrators = &organizationAdministratorGuard{}
	_, _, err = service.Create(inviter, 1, CreateInvitation{Email: "other@example.com", RoleIDs: []guid.ID{10}})
	require.ErrorIs(t, err, ErrUnavailable, "a guard that cannot judge grantability fails closed")
}

// Acceptance also places the invitee in the invitation's department without an
// actor, and department-scoped roles reach what that department reaches, so
// the inviter's data scope is checked where the department is chosen and
// wherever the credential is re-issued (audit residue d, as S4c for members).
func TestInvitationDepartmentMustStayWithinTheInvitersDataScope(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	h.seedRole(t, 1, 10)
	department, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	require.NoError(t, err)
	var principalCalls atomic.Int64
	service := newInvitationService(t, h, &invitationCredentialFixture{}, &principalCalls)
	guard := &grantingAdministratorGuard{}
	service.administrators = guard
	inviter := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: 5, TenantID: 1})
	invitations := func() int64 {
		var count int64
		require.NoError(t, h.db.Table(h.table("iam_invitations")).Where("tenant_id = 1").Count(&count).Error)
		return count
	}

	guard.placeErr = core.ErrPrivilegeEscalation
	_, _, err = service.Create(inviter, 1, CreateInvitation{Email: "user@example.com", DepartmentID: department.ID, RoleIDs: []guid.ID{10}})
	require.ErrorIs(t, err, core.ErrPrivilegeEscalation)
	assert.Equal(t, []guid.ID{department.ID}, guard.placed)
	assert.Zero(t, invitations(), "a refused placement leaves nothing to accept")

	guard.placeErr = nil
	pending, _, err := service.Create(inviter, 1, CreateInvitation{Email: "user@example.com", DepartmentID: department.ID, RoleIDs: []guid.ID{10}})
	require.NoError(t, err)
	_, _, err = service.Create(inviter, 1, CreateInvitation{Email: "plain@example.com", DepartmentID: department.ID})
	require.NoError(t, err)
	assert.Len(t, guard.placed, 2, "a department without roles reaches nothing")

	guard.placeErr = core.ErrPrivilegeEscalation
	before, err := getInvitation(t.Context(), service.repo, 1, pending.ID)
	require.NoError(t, err)
	_, _, err = service.Resend(inviter, 1, pending.ID, 0)
	require.ErrorIs(t, err, core.ErrPrivilegeEscalation, "re-issuing a credential re-places the invitee")
	assert.Equal(t, department.ID, guard.placed[len(guard.placed)-1])
	after, err := getInvitation(t.Context(), service.repo, 1, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, before.TokenHMAC, after.TokenHMAC)

	service.administrators = &organizationAdministratorGuard{}
	_, _, err = service.Create(inviter, 1, CreateInvitation{Email: "other@example.com", DepartmentID: department.ID, RoleIDs: []guid.ID{10}})
	require.ErrorIs(t, err, ErrUnavailable, "a guard that cannot judge the placement fails closed")
}

// TestInvitationAcceptanceStorageFaultIsNotABadCredential separates an
// invitation that cannot be found from one that could not be read. Only the
// first is a bad credential; telling the invitee their link is invalid when the
// store failed sends them to ask for a new one they do not need.
func TestInvitationAcceptanceStorageFaultIsNotABadCredential(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	service := newInvitationService(t, h, &invitationCredentialFixture{}, &atomic.Int64{})
	_, token, err := service.Create(t.Context(), 1, CreateInvitation{Email: "user@example.test"})
	require.NoError(t, err)

	table := h.table("iam_invitations")
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("test:fail_invitation_reads", func(db *gorm.DB) {
		if db.Statement.Table == table {
			db.AddError(assert.AnError)
		}
	}))
	_, err = service.Accept(t.Context(), AcceptInvitation{Token: token, LoginName: "user", Password: "correct horse battery staple", DisplayName: "User"})
	assert.ErrorIs(t, err, assert.AnError)
	assert.NotErrorIs(t, err, ErrInvitationCredential)
}

package identity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePhoneAcceptsHowPeopleWriteANumber(t *testing.T) {
	t.Parallel()

	// Spaces, dashes, dots and parentheses are how a number is written down, not
	// part of it. What has to survive is the country code: without a leading plus
	// the same digits mean different people in different countries.
	for typed, want := range map[string]string{
		"+8613800138000":       "+8613800138000",
		"+86 138 0013 8000":    "+8613800138000",
		"+86-138-0013-8000":    "+8613800138000",
		"  +1 (415) 555-0132 ": "+14155550132",
		"+44.20.7946.0958":     "+442079460958",
		"":                     "",
		"   ":                  "",
	} {
		got, ok := normalizePhone(typed)
		require.True(t, ok, "typed %q", typed)
		assert.Equal(t, want, got, "typed %q", typed)
	}

	for _, refused := range []string{
		"13800138000",                   // no country code: ambiguous
		"+8613800138000123",             // sixteen digits exceeds E.164
		"+86",                           // a country code and nothing to reach
		"+1-800-FLOWERS",                // letters are not digits
		"++8613800138000",               // one plus, at the front
		"+86 138 0013 8000 extension 4", // free text is not a number
	} {
		_, ok := normalizePhone(refused)
		assert.False(t, ok, "%q", refused)
	}
}

func TestAPrincipalCarriesAPhoneNumberAndAChangeUnverifiesIt(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	assert.Empty(t, principal.Phone, "a principal starts with no number")
	assert.False(t, principal.PhoneVerified)

	ctx := selfContext(t.Context(), principal.ID)
	typed := "+86 138 0013 8000"
	updated, err := harness.service.Update(ctx, 7, principal.ID, nil, nil, &typed)
	require.NoError(t, err)
	assert.Equal(t, "+8613800138000", updated.Phone, "it is stored in the one unambiguous shape")

	// Whatever proved the old number proves nothing about a different one.
	require.NoError(t, harness.db.Table(harness.table("iam_principals")).
		Where("id = ?", principal.ID).Update("phone_verified", true).Error)
	another := "+14155550132"
	updated, err = harness.service.Update(ctx, 7, principal.ID, nil, nil, &another)
	require.NoError(t, err)
	assert.Equal(t, another, updated.Phone)
	assert.False(t, updated.PhoneVerified, "a different number is unproven")

	// Writing the same number again changes nothing, so a proven number is not
	// unproven by a no-op write.
	require.NoError(t, harness.db.Table(harness.table("iam_principals")).
		Where("id = ?", principal.ID).Update("phone_verified", true).Error)
	same := "+1 415 555 0132"
	updated, err = harness.service.Update(ctx, 7, principal.ID, nil, nil, &same)
	require.NoError(t, err)
	assert.True(t, updated.PhoneVerified, "the same number written differently is the same number")

	// Somebody can take their number back.
	empty := ""
	updated, err = harness.service.Update(ctx, 7, principal.ID, nil, nil, &empty)
	require.NoError(t, err)
	assert.Empty(t, updated.Phone)
	assert.False(t, updated.PhoneVerified)

	// A number that is not a number is refused, and nothing is written.
	nonsense := "not a phone"
	_, err = harness.service.Update(ctx, 7, principal.ID, nil, nil, &nonsense)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestTheAuditChainRecordsThatANumberChangedAndNotTheNumber(t *testing.T) {
	// The chain is read by more people than the profile is, and a number in a
	// detail map would be a copy of somebody's PII that erasure cannot reach.
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	number := "+8613800138000"
	_, err = harness.service.Update(selfContext(t.Context(), principal.ID), 7, principal.ID, nil, nil, &number)
	require.NoError(t, err)

	var found bool
	harness.auditMu.Lock()
	events := append([]audit.DomainEvent(nil), harness.audits...)
	harness.auditMu.Unlock()
	for _, event := range events {
		if event.EventType != "principal_updated" {
			continue
		}
		found = true
		assert.Equal(t, true, event.Detail["phone_changed"])
		for key, value := range event.Detail {
			assert.NotContains(t, fmt.Sprint(value), "13800138000", "detail %q carries the number", key)
		}
	}
	assert.True(t, found, "the update was recorded")
}

func TestAnonymizationClearsTheNumberAndNothingPutsItBack(t *testing.T) {
	// The number is PII, so erasure has to reach it. The tombstone trigger on
	// this table already refuses every update to an anonymized principal, which
	// is what keeps it from coming back by any route.
	h := newIdentityHarness(t, "sl_")
	ctx := t.Context()
	principal, err := h.service.Create(ctx, 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	number := "+8613800138000"
	_, err = h.service.Update(selfContext(ctx, principal.ID), 7, principal.ID, nil, nil, &number)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.table("iam_principals")).
		Where("id = ?", principal.ID).Update("phone_verified", true).Error)
	require.Equal(t, number, h.principal(t, principal.ID).Phone)

	// The shortest route to a tombstone: no legal hold, a retention deadline in
	// the past, and the principal suspended first.
	h.service.erasureRecorder = func(context.Context, audit.ErasureObligation) error { return nil }
	h.service.anonymizer = func(context.Context, *gorm.DB, Principal, time.Time) error { return nil }
	policy, err := h.service.SetPrincipalRetention(
		ctx, principal.ID, 0, time.Now().UTC().Add(-time.Hour), false, "case-phone",
	)
	require.NoError(t, err)
	_, err = h.service.SetStatus(ctx, 7, principal.ID, "disabled")
	require.NoError(t, err)
	_, err = h.service.AnonymizePrincipal(ctx, principal.ID, policy.Version, principal.LoginName)
	require.NoError(t, err)

	erased := h.principal(t, principal.ID)
	assert.Empty(t, erased.Phone, "the number is gone with the address")
	assert.False(t, erased.PhoneVerified)
	assert.Empty(t, erased.Email)

	require.Error(t, h.db.Table(h.table("iam_principals")).
		Where("id = ?", principal.ID).Update("phone", "+14155550132").Error,
		"the tombstone refuses a number written back directly")
	back := "+14155550132"
	_, err = h.service.Update(selfContext(ctx, principal.ID), 7, principal.ID, nil, nil, &back)
	require.Error(t, err, "and through the profile endpoint")
}

func TestAFieldPolicyHidingTheNumberHidesWhetherItIsProven(t *testing.T) {
	// Verification is a derivative of the field. Reporting it where the number
	// itself is hidden would answer a question the policy refused.
	principal := Principal{ID: 1, Phone: "+8613800138000", PhoneVerified: true, Email: "a@b.test"}
	for _, decision := range []string{"deny", "redact"} {
		view := profileView(principal, authz.FieldConstraint{Fields: map[string]authz.FieldDecision{
			"id": {Read: "allow"}, "phone": {Read: decision},
		}})
		assert.Nil(t, view.PhoneVerified, "%s hides the flag too", decision)
	}
	view := profileView(principal, authz.FieldConstraint{Fields: map[string]authz.FieldDecision{
		"id": {Read: "allow"}, "phone": {Read: "allow"},
	}})
	require.NotNil(t, view.Phone)
	require.NotNil(t, view.PhoneVerified)
	assert.Equal(t, principal.Phone, *view.Phone)
	assert.True(t, *view.PhoneVerified)
}

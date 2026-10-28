package identity

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type auditLabelPermissions struct{ human, service bool }

type auditLabelFailingPermissions struct{ fail string }

func (p auditLabelFailingPermissions) Check(_ context.Context, _ guid.ID, permission string, _ guid.ID) (bool, error) {
	if permission == p.fail {
		return false, errors.New("permission backend unavailable")
	}
	return true, nil
}

func (auditLabelFailingPermissions) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, errors.New("permission backend unavailable")
}

func TestAuditPrincipalLabelsFailClosedWhenAuthorizationOrStorageFails(t *testing.T) {
	h := newServiceAccountHarness(t, "labels_failure_")
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11})
	for _, permission := range []string{"user_view", "service_account_view"} {
		t.Run(permission, func(t *testing.T) {
			h.service.avatarPermissions = auditLabelFailingPermissions{fail: permission}
			labels, err := h.service.auditPrincipalLabels(ctx, 7, []guid.ID{11})
			assert.EqualError(t, err, "permission backend unavailable")
			assert.Empty(t, labels)
		})
	}
	h.service.avatarPermissions = auditLabelFailingPermissions{}
	labels, err := h.service.auditPrincipalLabels(ctx, 0, []guid.ID{11})
	assert.EqualError(t, err, "permission backend unavailable")
	assert.Empty(t, labels)
	h.service.avatarPermissions = auditLabelPermissions{human: true, service: true}
	want := errors.New("field policy unavailable")
	h.service.fields = avatarFieldChecker{err: want}
	labels, err = h.service.auditPrincipalLabels(ctx, 7, []guid.ID{11})
	assert.ErrorIs(t, err, want)
	assert.Empty(t, labels)
	h.service.fields = avatarFieldChecker{}
	require.NoError(t, h.db.Exec("DROP TABLE "+h.table("iam_principals")).Error)
	labels, err = h.service.auditPrincipalLabels(ctx, 7, []guid.ID{11})
	require.Error(t, err, "a storage failure must not appear to be a successful empty label page")
	assert.Empty(t, labels)
	labels, err = h.service.auditPrincipalLabels(context.Background(), 7, []guid.ID{11})
	require.NoError(t, err)
	assert.Empty(t, labels, "unauthenticated callers must not resolve identity labels")
}

func (p auditLabelPermissions) Check(_ context.Context, _ guid.ID, permission string, _ guid.ID) (bool, error) {
	return permission == "user_view" && p.human || permission == "service_account_view" && p.service, nil
}

func (auditLabelPermissions) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, nil
}

func TestAuditPrincipalLabelsKeepHumanAndServiceAccountPermissionsSeparate(t *testing.T) {
	h := newServiceAccountHarness(t, "")
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11})
	human, err := h.service.Create(ctx, 7, "human", testPassword, "Human", "")
	require.NoError(t, err)
	service, err := h.service.CreateServiceAccount(ctx, 7, "automation", "Automation", "", nil)
	require.NoError(t, err)
	fields, err := (identityRouteAuthorizer{}).FieldConstraint(ctx, 7, 0, authz.ProfileResource, 11)
	require.NoError(t, err)
	h.service.fields = identityProfileChecker{fields}
	for _, permissions := range []auditLabelPermissions{{human: true}, {service: true}, {human: true, service: true}, {}} {
		h.service.avatarPermissions = permissions
		labels, err := h.service.auditPrincipalLabels(ctx, 7, []guid.ID{human.ID, service.ID})
		require.NoError(t, err)
		_, hasHuman := labels[human.ID]
		_, hasService := labels[service.ID]
		assert.Equal(t, permissions.human, hasHuman)
		assert.Equal(t, permissions.service, hasService)
	}
	require.NoError(t, h.db.Table(h.table("iam_service_accounts")).Where("principal_id = ?", service.ID).Update("status", "deleted").Error)
	h.service.avatarPermissions = auditLabelPermissions{human: true, service: true}
	labels, err := h.service.auditPrincipalLabels(ctx, 7, []guid.ID{human.ID, service.ID})
	require.NoError(t, err)
	assert.NotContains(t, labels, service.ID, "deleted service accounts cannot be read through their normal directory endpoint")
}

func TestAuditPrincipalLabelsRespectMembershipFieldsAndAnonymization(t *testing.T) {
	h := newServiceAccountHarness(t, "labels_")
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 11})
	local, err := h.service.Create(ctx, 7, "local", testPassword, "Local nickname", "local@example.test")
	require.NoError(t, err)
	foreign, err := h.service.Create(ctx, 8, "foreign", testPassword, "Foreign private nickname", "")
	require.NoError(t, err)
	erased, err := h.service.Create(ctx, 7, "erased-original", testPassword, "Original name", "")
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.table("iam_principal_lifecycles")).Create(&model.PrincipalLifecycle{
		PrincipalID: erased.ID, Version: 1, RetainUntil: identityTestNow, UpdatedAt: identityTestNow, AnonymizedAt: &identityTestNow,
	}).Error)
	h.service.avatarPermissions = profilePermissions{avatarPermissionChecker{allowed: true}}
	fields, err := (identityRouteAuthorizer{}).FieldConstraint(ctx, 7, 0, authz.ProfileResource, 11)
	require.NoError(t, err)
	fields.Fields["login_name"] = authz.FieldDecision{Read: "omit"}
	h.service.fields = identityProfileChecker{fields}
	reads := 0
	require.NoError(t, h.db.Callback().Row().After("gorm:row").Register("test:audit-label-batch", func(*gorm.DB) { reads++ }))
	t.Cleanup(func() { _ = h.db.Callback().Row().Remove("test:audit-label-batch") })
	labels, err := h.service.auditPrincipalLabels(ctx, 7, []guid.ID{local.ID, erased.ID, foreign.ID, 9999})
	require.NoError(t, err)
	assert.Equal(t, 1, reads, "one SQL read for every identity in the page")
	require.Len(t, labels, 2)
	assert.Nil(t, labels[local.ID].LoginName)
	require.NotNil(t, labels[local.ID].DisplayName)
	assert.Equal(t, "Local nickname", *labels[local.ID].DisplayName)
	assert.True(t, labels[erased.ID].Anonymized)
	assert.Nil(t, labels[erased.ID].LoginName)
	assert.Nil(t, labels[erased.ID].DisplayName)
	assert.NotContains(t, labels, foreign.ID)
	assert.NotContains(t, labels, guid.ID(9999))

	fields.Fields["display_name"] = authz.FieldDecision{Read: "redact"}
	labels, err = h.service.auditPrincipalLabels(ctx, 7, []guid.ID{local.ID})
	require.NoError(t, err)
	assert.Equal(t, "[REDACTED]", *labels[local.ID].DisplayName)
	fields.Fields["display_name"] = authz.FieldDecision{Read: "omit"}
	labels, err = h.service.auditPrincipalLabels(ctx, 7, []guid.ID{local.ID})
	require.NoError(t, err)
	assert.Empty(t, labels)

	h.service.avatarPermissions = profilePermissions{avatarPermissionChecker{allowed: false}}
	labels, err = h.service.auditPrincipalLabels(ctx, 7, []guid.ID{local.ID})
	require.NoError(t, err)
	assert.Empty(t, labels, "audit permission does not grant directory permission")
	labels, err = h.service.auditPrincipalLabels(ctx, 0, []guid.ID{local.ID})
	require.NoError(t, err)
	assert.Empty(t, labels, "system audit permission does not grant system account permission")
	h.service.avatarPermissions = profilePermissions{avatarPermissionChecker{allowed: true}}
	labels, err = h.service.auditPrincipalLabels(ctx, 8, []guid.ID{foreign.ID})
	require.NoError(t, err)
	assert.Empty(t, labels, "caller tenant mismatch must not resolve foreign identities")
	labels, err = h.service.auditPrincipalLabels(ctx, 0, []guid.ID{local.ID, foreign.ID, erased.ID})
	require.NoError(t, err)
	require.Len(t, labels, 3)
	assert.Equal(t, local.LoginName, *labels[local.ID].LoginName)
	assert.Equal(t, foreign.DisplayName, *labels[foreign.ID].DisplayName)
	assert.True(t, labels[erased.ID].Anonymized)
}

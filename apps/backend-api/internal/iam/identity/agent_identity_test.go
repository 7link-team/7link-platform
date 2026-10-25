package identity

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"testing"

	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/iam/identity/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentAvatarUsesSharedRendererWithStableValidatedSeed(t *testing.T) {
	for _, style := range []string{"PIXEL", "ROBOT", "MONSTER", "GEOMETRIC"} {
		a, err := renderAgentAvatar(t.Context(), style, 42)
		require.NoError(t, err)
		b, err := renderAgentAvatar(t.Context(), style, 42)
		require.NoError(t, err)
		require.Equal(t, a, b)
		other, err := renderAgentAvatar(t.Context(), style, 43)
		require.NoError(t, err)
		require.NotEqual(t, a, other)
		img, err := png.Decode(bytes.NewReader(a))
		require.NoError(t, err)
		require.Equal(t, 256, img.Bounds().Dx())
	}
	_, err := renderAgentAvatar(t.Context(), "invalid", 0)
	require.Error(t, err)
	_, err = renderAgentAvatar(t.Context(), "PIXEL", -1)
	require.Error(t, err)
	_, err = renderAgentAvatar(t.Context(), "PIXEL", 9007199254740992)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = renderAgentAvatar(ctx, "PIXEL", 42)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAtomicAgentIdentityCreatesGovernedAvatarCredentialAndRollsBackTogether(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			h := newServiceAccountHarness(t, "atomic_")
			m := &Module{Service: h.service}
			prepared, err := m.prepareAgentIdentity(t.Context(), "PIXEL", 42)
			require.NoError(t, err)
			failure := errors.New("downstream skill/delivery failure")
			var secret ServiceAccountCredentialSecret
			err = h.db.Transaction(func(tx *gorm.DB) error {
				var e error
				secret, e = m.createAgentIdentityOn(t.Context(), tx, 7, "atomic-agent", "Atomic Agent", prepared)
				if e != nil {
					return e
				}
				require.Equal(t, prepared.id, secret.Credential.ServiceAccountID)
				require.NotEmpty(t, secret.Secret)
				if rollback {
					return failure
				}
				return nil
			})
			if rollback {
				require.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
			}
			for _, table := range []string{"iam_principals", "iam_service_accounts", "iam_tenant_members", "iam_service_account_credentials"} {
				var count int64
				column := "principal_id"
				if table == "iam_principals" {
					column = "id"
				}
				require.NoError(t, h.db.Table(h.table(table)).Where(column+" = ?", prepared.id).Count(&count).Error)
				if rollback {
					require.Zero(t, count, table)
				} else {
					require.EqualValues(t, 1, count, table)
				}
			}
			if !rollback {
				principal, e := query.Bind(h.db, h.prefix).Principals.WithContext(t.Context()).Where(query.Bind(h.db, h.prefix).Principals.ID.Eq(prepared.id)).Take()
				require.NoError(t, e)
				require.Equal(t, "pixel", storedAvatar(principal.Avatar).Style)
				read, e := h.resources.Open(t.Context(), h.db, prepared.id, "principal.avatar", "current", h.service.now())
				require.NoError(t, e)
				require.NotNil(t, read)
				require.NoError(t, read.Reader.Close())
			}
		})
	}
}

func TestTeamRosterPrincipalClassificationNeverTreatsServiceAccountsAsHuman(t *testing.T) {
	h := newIdentityHarness(t, "roster_")
	h.seedTenant(t, 7, "active")
	human, err := h.service.Create(t.Context(), 7, "roster-human", "VerySecurePassword123!", "Human", "")
	require.NoError(t, err)
	service, err := h.service.Create(t.Context(), 7, "roster-service", "VerySecurePassword123!", "Service", "")
	require.NoError(t, err)
	q := query.Bind(h.db, h.prefix)
	require.NoError(t, q.ServiceAccounts.WithContext(t.Context()).Create(&model.ServiceAccount{PrincipalID: service.ID, OwnerTenantID: 7, Status: "disabled"}))
	m := &Module{Service: h.service}
	rows, err := m.teamRosterPrincipalsOn(t.Context(), h.db, []guid.ID{human.ID, service.ID, 999})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "HUMAN", rows[human.ID].Kind)
	require.Equal(t, "SERVICE_ACCOUNT", rows[service.ID].Kind, "even disabled service accounts must never become Human identities")
	require.Nil(t, rows[human.ID].LastActiveAt, "identity does not invent a last activity timestamp")
	_, err = m.teamRosterPrincipalsOn(t.Context(), h.db, make([]guid.ID, 201))
	require.ErrorIs(t, err, ErrInvalid)
}

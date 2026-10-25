package identity

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/passwordx"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"strings"
)

// AgentCredentialRotator preserves the current credential's exact scopes and
// expiry. The domain owner authorizes and principal-locks the caller transaction.
// Returned plaintext is for encrypted machine delivery only, never a receipt.
type AgentCredentialRotator func(context.Context, *gorm.DB, guid.ID, guid.ID, *guid.ID) (ServiceAccountCredentialSecret, error)

func (m *Module) provideAgentCredentialRotation(s *module.Services) error {
	return module.Provide[AgentCredentialRotator](s, m.rotateAgentCredentialOn)
}
func (m *Module) rotateAgentCredentialOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, old *guid.ID) (ServiceAccountCredentialSecret, error) {
	var out ServiceAccountCredentialSecret
	if m == nil || m.Service == nil || tx == nil || tx.Statement == nil {
		return out, ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrServiceAccountInvalid
	}
	service := m.Service
	repo, err := service.repo.on(tx)
	if err != nil {
		return out, err
	}
	now := identityTime(service.now())
	account, err := repo.getServiceAccount(ctx, tenant, principal, true)
	if err != nil {
		return out, err
	}
	if account.Status != "active" || account.ExpiresAt != nil && !account.ExpiresAt.After(now) {
		return out, ErrServiceAccountInvalid
	}
	rows, err := repo.listServiceAccountCredentials(ctx, principal)
	if err != nil {
		return out, err
	}
	var source *model.ServiceAccountCredential
	for _, r := range rows {
		if old != nil && r.ID == *old {
			source = r
		}
	}
	// An unbound profile may lack a recorded credential. Select only an
	// unambiguous active IAM credential; never invent or broaden token scopes.
	if old == nil {
		for _, r := range rows {
			if r.RevokedAt == nil && (r.ExpiresAt == nil || r.ExpiresAt.After(now)) {
				if source != nil {
					return out, ErrServiceAccountConflict
				}
				source = r
			}
		}
	}
	if source == nil || source.RevokedAt != nil || source.ExpiresAt != nil && !source.ExpiresAt.After(now) {
		return out, ErrCredentialNotFound
	}
	scopes, ok := normalizeServiceAccountScopes(strings.Fields(source.Scopes))
	if !ok {
		return out, ErrServiceAccountInvalid
	}
	id, err := repo.nextID()
	if err != nil {
		return out, err
	}
	plain, err := randomServiceAccountSecret()
	if err != nil {
		return out, err
	}
	hash, err := passwordx.Hash(plain)
	if err != nil {
		return out, err
	}
	row := &model.ServiceAccountCredential{ID: id, PrincipalID: principal, Name: "Agent machine binding", SecretHash: hash, Scopes: strings.Join(scopes, " "), ExpiresAt: source.ExpiresAt, CreatedAt: now}
	if err = repo.revokeServiceAccountCredentials(ctx, principal, now); err != nil {
		return out, err
	}
	if err = repo.incrementServiceAccountTokenVersion(ctx, tenant, principal, now); err != nil {
		return out, err
	}
	if err = repo.insertServiceAccountCredential(ctx, row); err != nil {
		return out, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "service_account_credential_rotated", "service_account_credential", id)
	event.Detail["service_account_id"] = principal
	if err = repo.appendAudit(ctx, event); err != nil {
		return out, err
	}
	return ServiceAccountCredentialSecret{Credential: serviceAccountCredentialFromRow(row), Secret: plain}, nil
}

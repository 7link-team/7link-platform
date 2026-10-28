package authn

import (
	"context"
	"net/mail"
	"strings"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/passwordx"
)

func (s *Service) registerUnverifiedAccount(ctx context.Context, login, email, password, name string) error {
	login = strings.ToLower(strings.TrimSpace(login))
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if login == "" {
		login = email
	}
	if login == "" || len(login) > 200 || len(email) > 320 || len(password) < 12 || len(password) > 1024 || len(name) > 128 {
		return authnext.ErrInvalidRegistration
	}
	if email != "" {
		parsed, err := mail.ParseAddress(email)
		if err != nil || !strings.EqualFold(parsed.Address, email) {
			return authnext.ErrInvalidRegistration
		}
	}
	hash, err := passwordx.Hash(password)
	if err != nil {
		return err
	}
	return s.repo.runInTx(ctx, func(repo *Repository) error {
		id, err := s.directRegistrationCreator(ctx, repo.writer(), login, email, hash, name, s.now().UTC(), repo.ids)
		if err != nil {
			return err
		}
		event := securityAudit(id, "principal_registration_requested", "success")
		event.Detail["activation_required"] = false
		event.Detail["tenant_membership_created"] = false
		return repo.appendAudit(ctx, event)
	})
}

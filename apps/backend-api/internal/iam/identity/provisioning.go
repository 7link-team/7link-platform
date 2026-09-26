package identity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var ErrImmutableProvisionedLogin = errors.New("provisioned login is immutable")

func (s *Service) revokeTenantSecurityState(ctx context.Context, db *gorm.DB, tenantID, id guid.ID) error {
	if s.revokeTenantTokens == nil || s.revokeTenantOAuth == nil {
		return ErrUnavailable
	}
	if err := s.revokeTenantTokens(ctx, db, tenantID, id); err != nil {
		return err
	}
	return s.revokeTenantOAuth(ctx, db, tenantID, id)
}

func (s *Service) GetProvisioned(ctx context.Context, tenantID, id guid.ID) (ProvisioningPrincipal, error) {
	principal, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return ProvisioningPrincipal{}, err
	}
	member, err := s.repo.getTenantMember(ctx, tenantID, id)
	if err != nil {
		return ProvisioningPrincipal{}, err
	}
	principal.DisplayName, principal.Email = member.DisplayName, member.Email
	principal.EmailVerified = false
	return ProvisioningPrincipal{Principal: principal, MembershipStatus: member.Status}, nil
}

// CreateProvisionedTo writes directory-owned identity state through the
// caller's transaction. The provisioning module owns its mapping and audit.
func (s *Service) CreateProvisionedTo(
	ctx context.Context,
	db *gorm.DB,
	tenantID guid.ID,
	input ProvisionedPrincipalInput,
) (Principal, error) {
	tenantID, input, err := normalizeProvisionedInput(tenantID, input, true)
	if err != nil || s == nil || s.repo == nil || db == nil {
		return Principal{}, ErrInvalid
	}
	repo, err := s.repo.on(db)
	if err != nil {
		return Principal{}, err
	}
	id, err := repo.nextID()
	if err != nil {
		return Principal{}, err
	}
	now := identityTime(s.now())
	principal := model.Principal{
		ID: id, LoginName: input.LoginName, Email: input.Email, ActivationRequired: true,
		DisplayName: input.DisplayName, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.insertPrincipal(ctx, &principal); err != nil {
		return Principal{}, fmt.Errorf("insert provisioned principal: %w", err)
	}
	if err := repo.createCredential(ctx, id, input.PasswordHash, now); err != nil {
		return Principal{}, fmt.Errorf("insert provisioned credential: %w", err)
	}
	status := "active"
	var disabledAt *time.Time
	if !input.Active {
		status, disabledAt = "disabled", &now
	}
	if err := repo.insertTenantMember(ctx, tenantMemberRecord{
		TenantID: tenantID, PrincipalID: id, DisplayName: input.DisplayName,
		Email: input.Email, Status: status, CreatedAt: now, UpdatedAt: now, DisabledAt: disabledAt,
	}); err != nil {
		return Principal{}, fmt.Errorf("insert provisioned membership: %w", err)
	}
	if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
		return Principal{}, err
	}
	return principalFromRow(&principal), nil
}

// ReplaceProvisionedTo replaces directory-owned profile and tenant admission
// state without overriding a global principal disable.
func (s *Service) ReplaceProvisionedTo(
	ctx context.Context,
	db *gorm.DB,
	tenantID, id guid.ID,
	input ProvisionedPrincipalInput,
) (Principal, error) {
	tenantID, input, err := normalizeProvisionedInput(tenantID, input, false)
	if err != nil || s == nil || s.repo == nil || db == nil || id.Zero() {
		return Principal{}, ErrInvalid
	}
	repo, err := s.repo.on(db)
	if err != nil {
		return Principal{}, err
	}
	if _, err := repo.lifecyclePrincipal(ctx, id, true); err != nil {
		return Principal{}, err
	}
	current, err := repo.getPrincipal(ctx, tenantID, id)
	if err != nil {
		return Principal{}, err
	}
	member, err := repo.getTenantMember(ctx, tenantID, id)
	if err != nil {
		return Principal{}, err
	}
	if input.Active && current.Status != "active" && member.Status != "active" {
		return Principal{}, ErrInvalid
	}
	if current.LoginName != input.LoginName {
		return Principal{}, ErrImmutableProvisionedLogin
	}
	now := identityTime(s.now())
	wantedMemberStatus := "disabled"
	var disabledAt *time.Time = &now
	if input.Active {
		wantedMemberStatus, disabledAt = "active", nil
	}
	var verify func() error
	if member.Status == "active" && wantedMemberStatus == "disabled" {
		verify, err = s.administrators.Protect(ctx, repo.writer(), tenantID)
		if err != nil {
			return Principal{}, err
		}
	}
	emailChanged := member.Email != input.Email
	profileChanged := emailChanged || member.DisplayName != input.DisplayName
	membershipChanged := member.Status != wantedMemberStatus
	if err := repo.updateTenantMember(ctx, tenantID, id, map[string]any{
		"display_name": input.DisplayName, "email": input.Email,
		"status": wantedMemberStatus, "disabled_at": disabledAt, "updated_at": now,
	}); err != nil {
		return Principal{}, err
	}
	if membershipChanged && !input.Active {
		if err := s.revokeTenantSecurityState(ctx, repo.writer(), tenantID, id); err != nil {
			return Principal{}, err
		}
	}
	if verify != nil {
		if err := verify(); err != nil {
			return Principal{}, err
		}
	}
	if emailChanged || membershipChanged {
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return Principal{}, err
		}
	}
	current.DisplayName, current.Email = input.DisplayName, input.Email
	if profileChanged || membershipChanged {
		current.UpdatedAt = now
	}
	current.EmailVerified = false
	return principalFromRow(current), nil
}

func normalizeProvisionedInput(
	tenantID guid.ID,
	input ProvisionedPrincipalInput,
	creating bool,
) (guid.ID, ProvisionedPrincipalInput, error) {
	input.LoginName = normalizeLogin(input.LoginName)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.DisplayName == "" {
		input.DisplayName = input.LoginName
	}
	validEmail := true
	if input.Email != "" {
		parsed, err := mail.ParseAddress(input.Email)
		validEmail = err == nil && strings.EqualFold(parsed.Address, input.Email)
	}
	if tenantID.Zero() || input.LoginName == "" || len(input.LoginName) > 200 ||
		input.DisplayName == "" || len(input.DisplayName) > 128 || len(input.Email) > 320 ||
		!validEmail || creating && input.PasswordHash == "" {
		return 0, ProvisionedPrincipalInput{}, ErrInvalid
	}
	return tenantID, input, nil
}

func (r *Repository) getTenantMember(ctx context.Context, tenantID, principalID guid.ID) (*tenantMemberRecord, error) {
	_, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	members := r.foreign.access.TenantMembers.String()
	var member tenantMemberRecord
	err = db.WithContext(ctx).Table(members).
		Where("tenant_id = ? AND principal_id = ?", tenantID, principalID).Take(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant membership: %w", err)
	}
	return &member, nil
}

var _ IdentityProvisioner = (*Service)(nil)

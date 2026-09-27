package federation

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/federation/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/crewjam/saml"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type reauthenticationConsumer = authn.ReauthenticationConsumer
type federatedReauthentication = authn.FederatedReauthentication

var (
	ErrIdentityLinkConflict = errors.New("external identity is already linked")
	ErrLastLoginMethod      = errors.New("cannot remove the last login method")
)

const identityOperationCookie = "federation_account_state"

type PersonalIdentityLink struct {
	ID              string  `json:"id"`
	ProviderID      guid.ID `json:"provider_id"`
	TenantID        guid.ID `json:"tenant_id"`
	ProviderName    string  `json:"provider_name"`
	ProviderType    string  `json:"provider_type"`
	ExternalSubject string  `json:"external_subject"`
	Email           string  `json:"email"`
	DisplayName     string  `json:"display_name"`
}

type LinkableProvider struct {
	ID           guid.ID `json:"id"`
	TenantID     guid.ID `json:"tenant_id"`
	Name         string  `json:"name"`
	ProviderType string  `json:"provider_type"`
}

func identityOperationHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func identityLinkID(providerID guid.ID, subject string) string {
	return providerID.String() + ":" + base64.RawURLEncoding.EncodeToString([]byte(subject))
}

func parseIdentityLinkID(id string) (guid.ID, string, error) {
	provider, subject, ok := strings.Cut(id, ":")
	providerID, err := guid.Parse(provider)
	if !ok || err != nil {
		return 0, "", ErrOIDCState
	}
	decoded, err := base64.RawURLEncoding.DecodeString(subject)
	if err != nil || len(decoded) == 0 || len(decoded) > 128 {
		return 0, "", ErrOIDCState
	}
	return providerID, string(decoded), nil
}

func (s *Service) accountSessionHash(cookieHeader string) (string, error) {
	value, err := cookieValue(cookieHeader, s.authn.SessionCookieName())
	if err != nil {
		return "", authnext.ErrInvalidSession
	}
	return identityOperationHash(value), nil
}

func (s *Service) ListPersonalIdentityLinks(ctx context.Context, cookieHeader string) ([]PersonalIdentityLink, error) {
	claims, err := s.personalReadClaims(ctx, cookieHeader)
	if err != nil {
		return nil, err
	}
	rows := []PersonalIdentityLink{}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	names := repo.names
	err = repo.writer().WithContext(ctx).Table(names.IdentityLinks.String()+" AS link").
		Select("link.provider_id, link.tenant_id, provider.name AS provider_name, provider.provider_type, link.external_subject, link.email, link.display_name").
		Joins("JOIN "+names.Providers.String()+" AS provider ON provider.id = link.provider_id AND provider.tenant_id = link.tenant_id").
		Where("link.principal_id = ?", claims.PrincipalID).Order("provider.name, link.external_subject").Scan(&rows).Error
	for i := range rows {
		rows[i].ID = identityLinkID(rows[i].ProviderID, rows[i].ExternalSubject)
	}
	return rows, err
}

func (s *Service) ListLinkableProviders(ctx context.Context, cookieHeader string) ([]LinkableProvider, error) {
	claims, err := s.personalReadClaims(ctx, cookieHeader)
	if err != nil {
		return nil, err
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	rows := []LinkableProvider{}
	names := repo.names
	members := repo.foreign.access.TenantMembers.String()
	tenants := repo.foreign.directory.Tenants.String()
	err = repo.writer().WithContext(ctx).Table(names.Providers.String()+" AS provider").
		Select("provider.id, provider.tenant_id, provider.name, provider.provider_type").
		Joins("JOIN "+members+" AS member ON member.tenant_id = provider.tenant_id AND member.principal_id = ? AND member.status = ?", claims.PrincipalID, "active").
		Joins("JOIN "+tenants+" AS tenant ON tenant.id = provider.tenant_id AND tenant.status = ?", "active").
		Where("provider.status = ?", ProviderActive).Order("provider.name, provider.id").Scan(&rows).Error
	return rows, err
}

func (s *Service) personalReadClaims(ctx context.Context, cookieHeader string) (*authnext.Claims, error) {
	if claims, ok := authnext.FromContext(ctx); ok && claims != nil && claims.AccountView != nil {
		if claims.PrincipalID.Zero() || claims.AccountView.ID.Zero() || claims.AccountView.PrincipalID.Zero() {
			return nil, authnext.ErrInvalidSession
		}
		return claims, nil
	}
	return s.authn.Authenticate(ctx, "", cookieHeader)
}

// StartIdentityOperation starts only linking or proof verification. It never
// calls authenticate, whose ordinary login path may JIT-provision an identity.
func (s *Service) StartIdentityOperation(ctx context.Context, cookieHeader string, providerID guid.ID, proof, existingLinkID, returnURL, callback string) (LoginStart, error) {
	if s.accountReauthentication.ConsumeIndependentOn == nil || s.accountReauthentication.Inspect == nil || s.accountReauthentication.ValidateSessionOn == nil {
		return LoginStart{}, ErrUnavailable
	}
	claims, err := s.authn.Authenticate(ctx, "", cookieHeader)
	if err != nil {
		return LoginStart{}, err
	}
	provider, err := s.getProviderByID(ctx, providerID)
	if err != nil {
		return LoginStart{}, err
	}
	if provider.Status != ProviderActive {
		return LoginStart{}, ErrProviderDisabled
	}
	if !validHTTPURL(callback) || !strings.HasSuffix(callback, callbackURL(providerID)) {
		return LoginStart{}, ErrInvalidProvider
	}
	resolved, err := s.authn.ResolveReturnURL(returnURL)
	if err != nil {
		return LoginStart{}, err
	}
	sessionHash, err := s.accountSessionHash(cookieHeader)
	if err != nil {
		return LoginStart{}, err
	}
	state, err := randomToken(32)
	if err != nil {
		return LoginStart{}, err
	}
	state = "account." + state
	verifier, err := randomToken(32)
	if err != nil {
		return LoginStart{}, err
	}
	now := s.now().UTC()
	fingerprint, err := s.bindingFingerprint(ctx, provider)
	if err != nil {
		return LoginStart{}, err
	}
	row := model.IdentityLinkTransaction{IDHash: identityOperationHash(state), PrincipalID: claims.PrincipalID,
		ProviderID: providerID, TenantID: provider.TenantID, SessionHash: sessionHash, Purpose: "link",
		ProviderFingerprint: fingerprint, CallbackURL: callback, ReturnURL: resolved,
		CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	row.CodeVerifierCiphertext, err = s.keys.Seal("account-code-verifier", []byte(row.IDHash), []byte(verifier))
	if err != nil {
		return LoginStart{}, err
	}
	if existingLinkID != "" {
		linkedProvider, subject, err := parseIdentityLinkID(existingLinkID)
		if err != nil || linkedProvider != providerID {
			return LoginStart{}, ErrOIDCState
		}
		binding, err := s.accountReauthentication.Inspect(ctx, cookieHeader, proof)
		if err != nil {
			return LoginStart{}, err
		}
		if binding.PrincipalID != claims.PrincipalID {
			return LoginStart{}, ErrOIDCState
		}
		row.Purpose, row.ExpectedSubject, row.ChallengeHash = "reauthentication", subject, binding.ChallengeHash
		if binding.ExpiresAt.Before(row.ExpiresAt) {
			row.ExpiresAt = binding.ExpiresAt
		}
	}
	redirect, err := s.identityOperationRedirect(ctx, provider, state, verifier, callback, now)
	if err != nil {
		return LoginStart{}, err
	}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		version, err := s.accountReauthentication.ValidateSessionOn(ctx, repo.writer(), claims.PrincipalID, cookieHeader)
		if err != nil {
			return err
		}
		row.CredentialVersion = version
		if err := repo.verifyPrincipalAndMember(ctx, provider.TenantID, claims.PrincipalID); err != nil {
			return err
		}
		if row.Purpose == "link" {
			if err := s.recoveryEligible(ctx, repo.writer(), claims.PrincipalID); err != nil {
				if errors.Is(err, authnext.ErrInvalidRecovery) {
					return ErrIdentityOwnershipRequired
				}
				return err
			}
			if err := s.accountReauthentication.ConsumeIndependentOn(ctx, repo.writer(), claims.PrincipalID, cookieHeader, proof, authn.ReauthenticationIdentityLink, providerID.String()); err != nil {
				return err
			}
		} else {
			link, err := repo.identityLink(ctx, providerID, provider.TenantID, row.ExpectedSubject)
			if err != nil || link.PrincipalID != claims.PrincipalID || !trustedIdentityLink(link, fingerprint) {
				return ErrPrincipalInactive
			}
		}
		if err := repo.writer().WithContext(ctx).Table(tables.IdentityLinkTransactions.TableName()).Create(&row).Error; err != nil {
			return err
		}
		return s.audit(ctx, repo.writer(), audit.NewDomainEvent(ctx, provider.TenantID, "identity_operation_started", "identity_provider", providerID))
	})
	if err != nil {
		return LoginStart{}, err
	}
	cookie := (&http.Cookie{Name: identityOperationCookie, Value: state, Path: "/", HttpOnly: true,
		Secure: s.authn.CookieSecure(), SameSite: http.SameSiteLaxMode, MaxAge: 300}).String()
	return LoginStart{AuthorizationURL: redirect, StateCookie: cookie}, nil
}

func (s *Service) identityOperationRedirect(ctx context.Context, provider providerRow, state, verifier, callback string, now time.Time) (string, error) {
	if provider.ProviderType == ProviderSAML {
		sp, err := s.samlServiceProvider(ctx, provider, callback)
		if err != nil {
			return "", err
		}
		force := true
		request := &saml.AuthnRequest{ID: state, Version: "2.0", IssueInstant: now,
			Destination: sp.GetSSOBindingLocation(saml.HTTPRedirectBinding), Issuer: &saml.Issuer{Format: samlNameIDEntity, Value: sp.EntityID},
			AssertionConsumerServiceURL: callback, ProtocolBinding: saml.HTTPPostBinding, ForceAuthn: &force}
		redirect, err := request.Redirect(state, sp)
		if err != nil {
			return "", err
		}
		return redirect.String(), nil
	}
	discovery, err := s.oidc.discovery(ctx, provider.Issuer)
	if err != nil {
		return "", err
	}
	endpoint, err := url.Parse(discovery.AuthorizationEndpoint)
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	for key, value := range map[string]string{"response_type": "code", "client_id": provider.ClientID, "redirect_uri": callback,
		"scope": provider.Scopes, "state": state, "nonce": state, "code_challenge": codeChallenge(verifier), "code_challenge_method": "S256",
		"prompt": "login", "max_age": "0"} {
		query.Set(key, value)
	}
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (s *Service) identityOperation(ctx context.Context, repo *Repository, providerID guid.ID, state string) (*model.IdentityLinkTransaction, providerRow, error) {
	var row model.IdentityLinkTransaction
	if !strings.HasPrefix(state, "account.") {
		return nil, providerRow{}, ErrOIDCState
	}
	tables, _, err := repo.tables()
	if err != nil {
		return nil, providerRow{}, err
	}
	err = repo.writer().WithContext(ctx).Table(tables.IdentityLinkTransactions.TableName()).Where("id_hash = ? AND provider_id = ?", identityOperationHash(state), providerID).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = ErrOIDCState
		}
		return nil, providerRow{}, err
	}
	if row.ConsumedAt != nil || !row.ExpiresAt.After(s.now().UTC()) {
		return nil, providerRow{}, ErrOIDCState
	}
	var provider providerRow
	providerQuery := repo.writer().WithContext(ctx).Table(tables.Providers.TableName()).Where("id = ?", providerID)
	if repo.inTx {
		providerQuery = providerQuery.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err = providerQuery.Take(&provider).Error
	if err != nil {
		return nil, providerRow{}, err
	}
	fingerprint, err := s.bindingFingerprint(ctx, provider)
	if err != nil {
		return nil, providerRow{}, err
	}
	if provider.Status != ProviderActive || provider.TenantID != row.TenantID || fingerprint != row.ProviderFingerprint {
		return nil, providerRow{}, ErrProviderDisabled
	}
	tenants := repo.foreign.directory.Tenants.String()
	var tenant struct{ Status string }
	if err := repo.writer().WithContext(ctx).Table(tenants).Where("id = ?", provider.TenantID).Take(&tenant).Error; err != nil {
		return nil, providerRow{}, err
	}
	if tenant.Status != "active" {
		return nil, providerRow{}, ErrProviderDisabled
	}
	return &row, provider, nil
}

// VerifyIdentityOperation stores only verified identity attributes. For SAML's
// cross-site POST, browser/session binding is checked on a same-site GET next.
func (s *Service) VerifyIdentityOperation(ctx context.Context, providerID guid.ID, state, code, samlResponse, callback string) error {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return err
	}
	row, provider, err := s.identityOperation(ctx, repo, providerID, state)
	if err != nil {
		return err
	}
	if row.VerifiedAt != nil || row.CallbackURL != callback {
		return ErrOIDCState
	}
	var claims idTokenClaims
	if provider.ProviderType == ProviderSAML {
		assertion, err := s.parseSAMLResponse(ctx, provider, samlResponse, callback, state)
		if err != nil {
			return err
		}
		if assertion.ID == "" || assertion.Subject.NameID.Format == "urn:oasis:names:tc:SAML:2.0:nameid-format:transient" || len(assertion.AuthnStatements) == 0 {
			return ErrSAMLResponse
		}
		claimed, err := s.replays.claim(ctx, "assertion:"+provider.ID.String()+":"+assertion.ID, row.ExpiresAt, s.now().UTC())
		if err != nil {
			return err
		}
		if !claimed {
			return ErrSAMLResponse
		}
		claims = idTokenClaims{Subject: assertion.Subject.NameID.Value, Email: samlAttribute(assertion, "email"),
			Name: samlAttribute(assertion, "display_name"), AuthTime: assertion.AuthnStatements[0].AuthnInstant.Unix()}
	} else {
		verifier, err := s.keys.Open("account-code-verifier", []byte(row.IDHash), row.CodeVerifierCiphertext)
		if err != nil {
			return err
		}
		secret := ""
		if provider.ClientSecretCiphertext != "" {
			secret, err = s.decryptSecret(providerID, provider.ClientSecretCiphertext)
			if err != nil {
				return err
			}
		}
		raw, err := s.oidc.exchangeCode(ctx, provider.Issuer, provider.ClientID, secret, code, callback, string(verifier))
		if err != nil {
			return err
		}
		claims, err = s.oidc.verifyIDToken(ctx, provider.Issuer, provider.ClientID, raw, s.now().UTC())
		if err != nil {
			return err
		}
		if claims.Nonce != state {
			return ErrOIDCState
		}
	}
	if claims.Subject == "" || len(claims.Subject) > 128 || len(claims.Email) > 255 || len(claims.Name) > 128 ||
		claims.AuthTime == 0 || claims.AuthTime < row.CreatedAt.Add(-s.clockSkew).Unix() || claims.AuthTime > s.now().Add(s.clockSkew).Unix() {
		return ErrOIDCState
	}
	if row.Purpose == "reauthentication" && claims.Subject != row.ExpectedSubject {
		return ErrPrincipalInactive
	}
	tables, _, err := repo.tables()
	if err != nil {
		return err
	}
	result := repo.writer().WithContext(ctx).Table(tables.IdentityLinkTransactions.TableName()).Where("id_hash = ? AND verified_at IS NULL AND consumed_at IS NULL", row.IDHash).
		Updates(map[string]any{"external_subject": claims.Subject, "email": claims.Email, "display_name": claims.Name, "verified_at": s.now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrOIDCState
	}
	return nil
}

func (s *Service) CompleteIdentityOperation(ctx context.Context, providerID guid.ID, state, cookieHeader string) (string, error) {
	if s.accountReauthentication.ValidateSessionOn == nil || s.accountReauthentication.VerifyOn == nil {
		return "", ErrUnavailable
	}
	cookieState, err := cookieValue(cookieHeader, identityOperationCookie)
	if err != nil || cookieState != state {
		return "", ErrOIDCState
	}
	claims, err := s.authn.Authenticate(ctx, "", cookieHeader)
	if err != nil {
		return "", err
	}
	sessionHash, err := s.accountSessionHash(cookieHeader)
	if err != nil {
		return "", err
	}
	returnURL := ""
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		version, err := s.accountReauthentication.ValidateSessionOn(ctx, repo.writer(), claims.PrincipalID, cookieHeader)
		if err != nil {
			return err
		}
		row, provider, err := s.identityOperation(ctx, repo, providerID, state)
		if err != nil {
			return err
		}
		if row.PrincipalID != claims.PrincipalID || row.SessionHash != sessionHash || row.CredentialVersion != version || row.VerifiedAt == nil {
			return ErrOIDCState
		}
		if err := repo.verifyPrincipalAndMember(ctx, provider.TenantID, claims.PrincipalID); err != nil {
			return err
		}
		if row.Purpose == "reauthentication" {
			link, err := repo.identityLink(ctx, providerID, provider.TenantID, row.ExternalSubject)
			if err != nil || link.PrincipalID != claims.PrincipalID || !trustedIdentityLink(link, row.ProviderFingerprint) {
				return ErrPrincipalInactive
			}
			if err := s.accountReauthentication.VerifyOn(ctx, repo.writer(), claims.PrincipalID, cookieHeader, row.ChallengeHash, identityLinkID(providerID, row.ExternalSubject), row.ProviderFingerprint); err != nil {
				return err
			}
		} else if row.Purpose == "link" {
			if err := s.recoveryEligible(ctx, repo.writer(), claims.PrincipalID); err != nil {
				if errors.Is(err, authnext.ErrInvalidRecovery) {
					return ErrIdentityOwnershipRequired
				}
				return err
			}
			link, err := repo.identityLink(ctx, providerID, provider.TenantID, row.ExternalSubject)
			if err == nil && (link.PrincipalID != claims.PrincipalID || trustedIdentityLink(link, row.ProviderFingerprint)) {
				return ErrIdentityLinkConflict
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			now := s.now().UTC()
			if err == nil {
				if err := repo.writer().WithContext(ctx).Table(repo.names.IdentityLinks.String()).
					Where("provider_id = ? AND tenant_id = ? AND external_subject = ? AND principal_id = ?", providerID, provider.TenantID, row.ExternalSubject, claims.PrincipalID).
					Updates(map[string]any{"binding_provenance": BindingPrincipalVerified, "security_fingerprint": row.ProviderFingerprint, "quarantined_at": nil, "updated_at": now}).Error; err != nil {
					return err
				}
			} else if err := repo.insertIdentityLink(ctx, &identityLinkRow{ProviderID: providerID, TenantID: provider.TenantID, ExternalSubject: row.ExternalSubject,
				BindingProvenance: BindingPrincipalVerified, SecurityFingerprint: row.ProviderFingerprint,
				PrincipalID: claims.PrincipalID, Email: row.Email, DisplayName: row.DisplayName, CreatedAt: now, UpdatedAt: now, LastLoginAt: now}); err != nil {
				if errors.Is(err, gorm.ErrDuplicatedKey) {
					return ErrIdentityLinkConflict
				}
				return err
			}
		} else {
			return ErrOIDCState
		}
		result := repo.writer().WithContext(ctx).Table(tables.IdentityLinkTransactions.TableName()).Where("id_hash = ? AND consumed_at IS NULL", row.IDHash).Update("consumed_at", s.now().UTC())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrOIDCState
		}
		returnURL = row.ReturnURL
		return s.audit(ctx, repo.writer(), audit.NewDomainEvent(ctx, provider.TenantID, "identity_"+row.Purpose+"_completed", "identity_provider", providerID))
	})
	return returnURL, err
}

func (s *Service) UnlinkPersonalIdentity(ctx context.Context, cookieHeader, id, proof string) error {
	if s.accountProofs == nil || s.accountReauthentication.LocalMethodsOn == nil {
		return ErrUnavailable
	}
	claims, err := s.authn.Authenticate(ctx, "", cookieHeader)
	if err != nil {
		return err
	}
	providerID, subject, err := parseIdentityLinkID(id)
	if err != nil {
		return err
	}
	return s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := s.accountProofs(ctx, repo.writer(), claims.PrincipalID, cookieHeader, proof, authn.ReauthenticationIdentityUnlink, id); err != nil {
			return err
		}
		provider, err := repo.getProviderByID(ctx, providerID)
		if err != nil {
			return err
		}
		link, err := repo.identityLink(ctx, providerID, provider.TenantID, subject)
		if err != nil || link.PrincipalID != claims.PrincipalID {
			return ErrPrincipalInactive
		}
		local, err := s.accountReauthentication.LocalMethodsOn(ctx, repo.writer(), claims.PrincipalID)
		if err != nil {
			return err
		}
		var alternatives int64
		var candidates []identityLinkRow
		names := repo.names
		members := repo.foreign.access.TenantMembers.String()
		tenants := repo.foreign.directory.Tenants.String()
		err = repo.writer().WithContext(ctx).Table(names.IdentityLinks.String()+" AS link").
			Select("link.*").
			Joins("JOIN "+names.Providers.String()+" AS provider ON provider.id = link.provider_id AND provider.tenant_id = link.tenant_id AND provider.status = ?", ProviderActive).
			Joins("JOIN "+members+" AS member ON member.tenant_id = link.tenant_id AND member.principal_id = link.principal_id AND member.status = ?", "active").
			Joins("JOIN "+tenants+" AS tenant ON tenant.id = link.tenant_id AND tenant.status = ?", "active").
			Where("link.principal_id = ? AND NOT (link.provider_id = ? AND link.external_subject = ?)", claims.PrincipalID, providerID, subject).Find(&candidates).Error
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			provider, err := repo.getProviderByID(ctx, candidate.ProviderID)
			if err != nil {
				return err
			}
			fingerprint, err := s.bindingFingerprint(ctx, provider)
			if err != nil {
				return err
			}
			if trustedIdentityLink(candidate, fingerprint) {
				alternatives++
			}
		}
		if local == 0 && alternatives == 0 {
			return ErrLastLoginMethod
		}
		result := repo.writer().WithContext(ctx).Table(names.IdentityLinks.String()).Where("principal_id = ? AND provider_id = ? AND tenant_id = ? AND external_subject = ?", claims.PrincipalID, providerID, provider.TenantID, subject).Delete(&model.IdentityLink{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrPrincipalInactive
		}
		return s.audit(ctx, repo.writer(), audit.NewDomainEvent(ctx, provider.TenantID, "identity_unlinked", "identity_provider", providerID))
	})
}

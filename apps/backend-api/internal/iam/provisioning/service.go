package provisioning

import (
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/keyringx"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/passwordx"
	"gorm.io/gorm"
)

const maxActiveCredentials = 10

type Service struct {
	runtime  *appenv.Runtime
	repo     *Repository
	appender audit.Appender
	ids      guid.Source
	identity identity.IdentityProvisioner
	actors   identity.ServiceAccountValidator
	fields   authz.FieldPermissionChecker
	groups   organization.GroupProvisioner
	keys     *keyringx.Keyring
	http     *http.Client
	outbound httpx.Policy
	now      func() time.Time

	pushMu                   sync.Mutex
	pushLocks                map[string]*pushLock
	operationReleaseFailures atomic.Int64
}

type pushLock struct {
	mu   sync.Mutex
	refs int
}

func NewService(runtime *appenv.Runtime, appender audit.Appender, ids guid.Source, identities identity.IdentityProvisioner, groups organization.GroupProvisioner, actors identity.ServiceAccountValidator, fields authz.FieldPermissionChecker, cfg Config, keys *keyringx.Keyring, outbound httpx.Policy) *Service {
	if runtime == nil || runtime.Database() == nil || runtime.Database().Write() == nil || appender == nil || ids == nil || identities == nil || groups == nil {
		panic("provisioning service requires runtime database, audit appender, id generator, identity provisioner, and group provisioner")
	}
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Service{
		runtime: runtime, repo: NewRepository(runtime), appender: appender, ids: ids,
		identity: identities, groups: groups, actors: actors, fields: fields, keys: keys,
		http: httpx.NewClient(timeout, outbound), outbound: outbound,
		now: time.Now, pushLocks: make(map[string]*pushLock),
	}
}

func (s *Service) lockPushResource(targetID guid.ID, resourceType string, resourceID guid.ID) func() {
	key := targetID.String() + "\x00" + resourceType + "\x00" + resourceID.String()
	s.pushMu.Lock()
	entry, ok := s.pushLocks[key]
	if !ok {
		entry = &pushLock{}
		s.pushLocks[key] = entry
	}
	entry.refs++
	s.pushMu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		s.pushMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(s.pushLocks, key)
		}
		s.pushMu.Unlock()
	}
}

func (s *Service) nextID() (guid.ID, error) {
	id, err := s.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, errors.New("generated provisioning ID must be positive")
	}
	return guid.ID(id), nil
}

func (s *Service) ListDirectories(ctx context.Context, tenantID guid.ID) ([]Directory, error) {
	if tenantID.Zero() {
		return nil, ErrInvalidDirectory
	}
	rows, err := s.repo.listDirectories(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	result := make([]Directory, 0, len(rows))
	for _, row := range rows {
		result = append(result, directoryFromRow(row))
	}
	return result, nil
}

func (s *Service) CreateDirectory(ctx context.Context, tenantID guid.ID, name string, serviceAccountID *guid.ID) (Directory, error) {
	name = strings.TrimSpace(name)
	if tenantID.Zero() || name == "" || len(name) > 128 {
		return Directory{}, ErrInvalidDirectory
	}
	id, err := s.nextID()
	if err != nil {
		return Directory{}, fmt.Errorf("generate SCIM directory id: %w", err)
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	row := directoryRow{ID: id, TenantID: tenantID, Name: name, NameKey: strings.ToLower(name), Status: DirectoryActive, Version: 1, CreatedAt: now, UpdatedAt: now}
	row.ServiceAccountID = serviceAccountID
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := s.requireServiceAccount(ctx, repo, tenantID, serviceAccountID); err != nil {
			return errors.Join(ErrInvalidDirectory, err)
		}
		tenants := repo.foreign.directory.Tenants.String()
		var active int64
		err := repo.writer().WithContext(ctx).Table(tenants).
			Where("id = ? AND status = 'active'", tenantID).Count(&active).Error
		if err != nil {
			return err
		}
		if active != 1 {
			return ErrInvalidDirectory
		}
		if err := repo.insertDirectory(ctx, &row); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, tenantID, "scim_directory_created", "scim_directory", id)
		event.Detail["service_account_id"] = actorID(serviceAccountID)
		return s.appender(ctx, repo.writer(), event)
	})
	if err != nil {
		return Directory{}, fmt.Errorf("create SCIM directory: %w", err)
	}
	return directoryFromRow(row), nil
}

func (s *Service) ReplaceDirectory(ctx context.Context, tenantID, id guid.ID, name, status string, version int64, serviceAccountID *guid.ID) (Directory, error) {
	name, status = strings.TrimSpace(name), strings.TrimSpace(status)
	if tenantID.Zero() || id.Zero() || name == "" || len(name) > 128 || version < 1 || status != DirectoryActive && status != DirectoryDisabled {
		return Directory{}, ErrInvalidDirectory
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	var updated directoryRow
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getDirectory(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if current.Version != version {
			return ErrDirectoryVersion
		}
		updated = current
		updated.ServiceAccountID = serviceAccountID
		updated.Name, updated.NameKey, updated.Status = name, strings.ToLower(name), status
		actorChanged := actorID(updated.ServiceAccountID) != actorID(current.ServiceAccountID)
		if actorChanged {
			if err := s.requireServiceAccount(ctx, repo, tenantID, serviceAccountID); err != nil {
				return errors.Join(ErrInvalidDirectory, err)
			}
		}
		if updated.Name == current.Name && updated.Status == current.Status && !actorChanged {
			return nil
		}
		updated.Version, updated.UpdatedAt = current.Version+1, now
		if err := repo.updateDirectory(ctx, &updated, current.Version); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, tenantID, "scim_directory_updated", "scim_directory", id)
		event.Detail["name_changed"] = updated.Name != current.Name
		event.Detail["status_changed"] = updated.Status != current.Status
		event.Detail["service_account_id"] = actorID(updated.ServiceAccountID)
		return s.appender(ctx, repo.writer(), event)
	})
	if err != nil {
		return Directory{}, fmt.Errorf("replace SCIM directory: %w", err)
	}
	return directoryFromRow(updated), nil
}

func (s *Service) ListCredentials(ctx context.Context, tenantID, directoryID guid.ID) ([]Credential, error) {
	if _, err := s.repo.getDirectory(ctx, tenantID, directoryID); err != nil {
		return nil, err
	}
	rows, err := s.repo.listCredentials(ctx, directoryID)
	if err != nil {
		return nil, err
	}
	result := make([]Credential, 0, len(rows))
	for _, row := range rows {
		result = append(result, credentialFromRow(row))
	}
	return result, nil
}

func (s *Service) CreateCredential(ctx context.Context, tenantID, directoryID guid.ID, name string, expiresAt *time.Time) (CredentialSecret, error) {
	name = strings.TrimSpace(name)
	now := s.now().UTC().Truncate(time.Millisecond)
	if tenantID.Zero() || directoryID.Zero() || name == "" || len(name) > 128 || expiresAt != nil && !expiresAt.UTC().After(now) {
		return CredentialSecret{}, ErrInvalidDirectory
	}
	id, err := s.nextID()
	if err != nil {
		return CredentialSecret{}, err
	}
	secret, err := randomTokenSecret()
	if err != nil {
		return CredentialSecret{}, err
	}
	hash, err := passwordx.Hash(secret)
	if err != nil {
		return CredentialSecret{}, err
	}
	var expiry *time.Time
	if expiresAt != nil {
		value := expiresAt.UTC().Truncate(time.Millisecond)
		expiry = &value
	}
	row := credentialRow{ID: id, DirectoryID: directoryID, Name: name, TokenHash: hash, ExpiresAt: expiry, CreatedAt: now}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		directory, err := repo.getDirectory(ctx, tenantID, directoryID)
		if err != nil {
			return err
		}
		if directory.Status != DirectoryActive {
			return ErrInvalidDirectory
		}
		count, err := repo.activeCredentialCount(ctx, directoryID, now)
		if err != nil {
			return err
		}
		if count >= maxActiveCredentials {
			return ErrCredentialLimit
		}
		if err := repo.insertCredential(ctx, &row); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, tenantID, "scim_credential_created", "scim_credential", id)
		event.Detail["directory_id"] = directoryID
		event.Detail["expires_at"] = row.ExpiresAt
		return s.appender(ctx, repo.writer(), event)
	})
	if err != nil {
		return CredentialSecret{}, fmt.Errorf("create SCIM credential: %w", err)
	}
	return CredentialSecret{Credential: credentialFromRow(row), Token: id.String() + "." + secret}, nil
}

func (s *Service) RevokeCredential(ctx context.Context, tenantID, directoryID, id guid.ID) error {
	if tenantID.Zero() || directoryID.Zero() || id.Zero() {
		return ErrInvalidDirectory
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		if _, err := repo.getDirectory(ctx, tenantID, directoryID); err != nil {
			return err
		}
		changed, err := repo.revokeCredential(ctx, directoryID, id, now)
		if err != nil || !changed {
			return err
		}
		event := audit.NewDomainEvent(ctx, tenantID, "scim_credential_revoked", "scim_credential", id)
		event.Detail["directory_id"] = directoryID
		return s.appender(ctx, repo.writer(), event)
	})
	if err != nil {
		return fmt.Errorf("revoke SCIM credential: %w", err)
	}
	return nil
}

func (s *Service) Authenticate(ctx context.Context, authorization string) (AuthContext, error) {
	fields := strings.Fields(authorization)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return AuthContext{}, ErrUnauthorized
	}
	id, secret, ok := strings.Cut(fields[1], ".")
	if !ok || secret == "" || strings.Contains(secret, ".") {
		return AuthContext{}, ErrUnauthorized
	}
	credentialID, err := guid.Parse(id)
	if err != nil {
		return AuthContext{}, ErrUnauthorized
	}
	row, err := s.repo.credentialForAuth(ctx, credentialID, 0, 0)
	if err != nil {
		return AuthContext{}, err
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	if !row.active(now) || row.PrincipalID <= 0 {
		return AuthContext{}, ErrUnauthorized
	}
	if row.PrincipalID > 0 {
		if err := s.requireServiceAccount(ctx, s.repo, row.TenantID, &row.PrincipalID); err != nil {
			return AuthContext{}, err
		}
	}
	valid, err := passwordx.Verify(row.TokenHash, secret)
	if err != nil || !valid {
		return AuthContext{}, ErrUnauthorized
	}
	if err := s.repo.touchCredential(ctx, credentialID, now); err != nil {
		return AuthContext{}, fmt.Errorf("record SCIM credential use: %w", err)
	}
	return AuthContext{TenantID: row.TenantID, DirectoryID: row.DirectoryID, CredentialID: row.ID, PrincipalID: row.PrincipalID}, nil
}

func directoryFromRow(row directoryRow) Directory {
	return Directory{ID: row.ID, TenantID: row.TenantID, Name: row.Name, Status: row.Status, Version: row.Version, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(), ServiceAccountID: row.ServiceAccountID}
}

func actorID(id *guid.ID) guid.ID {
	if id == nil {
		return 0
	}
	return *id
}

func (s *Service) requireServiceAccount(ctx context.Context, repo *Repository, tenantID guid.ID, id *guid.ID) error {
	return repo.requireServiceAccount(ctx, tenantID, id, s.now(), s.actors)
}

func credentialFromRow(row credentialRow) Credential {
	return Credential{ID: row.ID, DirectoryID: row.DirectoryID, Name: row.Name, ExpiresAt: utcTime(row.ExpiresAt), LastUsedAt: utcTime(row.LastUsedAt), RevokedAt: utcTime(row.RevokedAt), CreatedAt: row.CreatedAt.UTC()}
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

func randomTokenSecret() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func firstError(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

func serviceErrorKind(err error) error {
	for _, known := range []error{ErrInvalidDirectory, ErrDirectoryMissing, ErrDirectoryName, ErrDirectoryVersion, ErrCredentialMissing, ErrCredentialLimit, ErrUnauthorized, ErrResourceMissing, ErrResourceConflict, ErrResourceVersion, ErrInvalidSCIM, ErrInvalidFilter, ErrInvalidPath, ErrTooMany, ErrInvalidTarget, ErrTargetMissing, ErrTargetName, ErrTargetVersion, ErrTargetKeyMissing, ErrTargetDisabled, ErrDeprovisionMissing, ErrRemoteUnavailable, ErrRemoteResponse} {
		if errors.Is(err, known) {
			return known
		}
	}
	return err
}

func (s *Service) appendResourceAudit(ctx context.Context, db *gorm.DB, auth AuthContext, eventType, resourceType string, resourceID guid.ID) error {
	event := audit.NewDomainEvent(ctx, auth.TenantID, eventType, "scim_"+strings.ToLower(resourceType), resourceID)
	if auth.PrincipalID > 0 {
		event.PrincipalID = auth.PrincipalID
	}
	event.Detail["directory_id"] = auth.DirectoryID
	event.Detail["credential_id"] = auth.CredentialID
	return s.appender(ctx, db, event)
}

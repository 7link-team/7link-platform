package provisioning

import (
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/keyringx"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

const (
	// TargetActive and TargetDisabled are the outbound SCIM target states.
	TargetActive   = DirectoryActive
	TargetDisabled = DirectoryDisabled

	// maxRemoteResponseBytes bounds the SCIM provider response body read.
	maxRemoteResponseBytes = 4 << 20
)

var (
	ErrInvalidTarget      = errors.New("invalid SCIM target")
	ErrTargetMissing      = errors.New("SCIM target not found")
	ErrTargetName         = errors.New("SCIM target name conflict")
	ErrTargetVersion      = errors.New("SCIM target version conflict")
	ErrTargetKeyMissing   = errors.New("SCIM target encryption key not configured")
	ErrTargetDisabled     = errors.New("SCIM target is disabled")
	ErrRemoteUnavailable  = errors.New("SCIM remote request failed")
	ErrRemoteResponse     = errors.New("SCIM remote rejected the request")
	ErrDeprovisionMissing = errors.New("SCIM remote resource has no active mapping")
	// ErrTargetHasLiveMappings stops a deletion that would destroy the bearer
	// token and remote identifiers which are the only way left to reach the
	// accounts this target created. The rows would go with it -- the database
	// cascades them -- and principal erasure reads exactly those rows to decide
	// whether a remote account still holds the person's data. Deleting the
	// target first therefore turns a refusal into an approval without anything
	// having been deprovisioned.
	ErrTargetHasLiveMappings = errors.New("deprovision the target's remote resources before deleting it")
)

// Target is an outbound SCIM endpoint managed per tenant.
type Target struct {
	ServiceAccountID *guid.ID  `json:"service_account_id,omitempty"`
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	Name             string    `json:"name"`
	BaseURL          string    `json:"base_url"`
	Status           string    `json:"status"`
	Version          int64     `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// TargetSecret wraps a target with its bearer token, shown once on creation.
type TargetSecret struct {
	Target      Target `json:"target"`
	BearerToken string `json:"bearer_token" doc:"Shown once on creation. Store it in a secret manager."`
}

// PushResult reports a completed outbound push and the resulting mapping.
type PushResult struct {
	TargetID     string `json:"target_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	ExternalID   string `json:"external_id"`
	RemoteID     string `json:"remote_id,omitempty"`
}

// remoteResource is a SCIM payload ready to be PUT to a target.
type remoteResource struct {
	externalID string
	resource   any
}

func (s *Service) ListTargets(ctx context.Context, tenantID guid.ID) ([]Target, error) {
	if tenantID.Zero() {
		return nil, ErrInvalidTarget
	}
	rows, err := s.repo.listTargets(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	result := make([]Target, 0, len(rows))
	for _, row := range rows {
		result = append(result, targetFromRow(row))
	}
	return result, nil
}

func (s *Service) CreateTarget(ctx context.Context, tenantID guid.ID, name, baseURL, bearerToken string, serviceAccountID *guid.ID) (TargetSecret, error) {
	name, baseURL, bearerToken = strings.TrimSpace(name), strings.TrimSpace(baseURL), strings.TrimSpace(bearerToken)
	if tenantID.Zero() || name == "" || len(name) > 128 || bearerToken == "" || len(bearerToken) > 4096 || !validTargetURL(baseURL, s.outbound) {
		return TargetSecret{}, ErrInvalidTarget
	}
	if s.keys == nil {
		return TargetSecret{}, ErrTargetKeyMissing
	}
	id, err := s.nextID()
	if err != nil || id.Zero() {
		return TargetSecret{}, fmt.Errorf("generate SCIM target id: %w", firstError(err, ErrInvalidTarget))
	}
	ciphertext, err := s.encryptToken(id, bearerToken)
	if err != nil {
		return TargetSecret{}, err
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	row := targetRow{ID: id, TenantID: tenantID, Name: name, NameKey: strings.ToLower(name), BaseURL: baseURL, BearerTokenCiphertext: ciphertext, Status: TargetActive, Version: 1, CreatedAt: now, UpdatedAt: now}
	row.ServiceAccountID = serviceAccountID
	event := audit.NewDomainEvent(ctx, tenantID, "scim_target_created", "scim_target", id)
	event.Detail["service_account_id"] = actorID(serviceAccountID)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := s.requireServiceAccount(ctx, repo, tenantID, serviceAccountID); err != nil {
			return errors.Join(ErrInvalidTarget, err)
		}
		tenants := repo.foreign.directory.Tenants.String()
		var active int64
		err := repo.writer().WithContext(ctx).Table(tenants).
			Where("id = ? AND status = 'active'", tenantID).Count(&active).Error
		if err != nil {
			return err
		}
		if active != 1 {
			return ErrInvalidTarget
		}
		if err := repo.insertTarget(ctx, &row); err != nil {
			return err
		}
		return s.appender(ctx, repo.writer(), event)
	})
	if err != nil {
		return TargetSecret{}, fmt.Errorf("create SCIM target: %w", err)
	}
	return TargetSecret{Target: targetFromRow(row), BearerToken: bearerToken}, nil
}

func (s *Service) ReplaceTarget(ctx context.Context, tenantID, id guid.ID, name, baseURL, status, bearerToken string, version int64, serviceAccountID *guid.ID) (Target, error) {
	name, baseURL, status, bearerToken = strings.TrimSpace(name), strings.TrimSpace(baseURL), strings.TrimSpace(status), strings.TrimSpace(bearerToken)
	if tenantID.Zero() || id.Zero() || name == "" || len(name) > 128 || !validTargetURL(baseURL, s.outbound) || len(bearerToken) > 4096 || version < 1 || (status != TargetActive && status != TargetDisabled) {
		return Target{}, ErrInvalidTarget
	}
	op, err := s.claimTarget(ctx, tenantID, id, "replace", false, "", 0)
	if err != nil {
		return Target{}, err
	}
	defer op.release()
	now := s.now().UTC().Truncate(time.Millisecond)
	var updated targetRow
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getTarget(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if current.Version != version {
			return ErrTargetVersion
		}
		updated = current
		updated.ServiceAccountID = serviceAccountID
		if actorID(updated.ServiceAccountID) != actorID(current.ServiceAccountID) {
			if err := s.requireServiceAccount(ctx, repo, tenantID, serviceAccountID); err != nil {
				return errors.Join(ErrInvalidTarget, err)
			}
		}
		updated.Name, updated.NameKey, updated.BaseURL, updated.Status = name, strings.ToLower(name), baseURL, status
		if bearerToken != "" {
			if s.keys == nil {
				return ErrTargetKeyMissing
			}
			updated.BearerTokenCiphertext, err = s.encryptToken(id, bearerToken)
			if err != nil {
				return err
			}
		}
		if updated.Name == current.Name && updated.BaseURL == current.BaseURL && updated.Status == current.Status && bearerToken == "" && actorID(updated.ServiceAccountID) == actorID(current.ServiceAccountID) {
			return nil
		}
		updated.Version, updated.UpdatedAt = current.Version+1, now
		if err := repo.replaceTarget(ctx, &updated, current.Version); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, tenantID, "scim_target_updated", "scim_target", id)
		event.Detail["name_changed"] = updated.Name != current.Name
		event.Detail["status_changed"] = updated.Status != current.Status
		event.Detail["token_rotated"] = bearerToken != ""
		event.Detail["service_account_id"] = actorID(updated.ServiceAccountID)
		return s.appender(ctx, repo.writer(), event)
	})
	if err != nil {
		return Target{}, fmt.Errorf("replace SCIM target: %w", err)
	}
	return targetFromRow(updated), nil
}

func (s *Service) DeleteTarget(ctx context.Context, tenantID, id guid.ID) error {
	if tenantID.Zero() || id.Zero() {
		return ErrInvalidTarget
	}
	op, err := s.claimTarget(ctx, tenantID, id, "delete", true, "", 0)
	if err != nil {
		return err
	}
	defer op.release()
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if _, err := repo.getTarget(ctx, tenantID, id); err != nil {
			return err
		}
		live, err := repo.countLiveTargetResources(ctx, id)
		if err != nil {
			return err
		}
		if live != 0 {
			return ErrTargetHasLiveMappings
		}
		if err := repo.deleteTarget(ctx, tenantID, id); err != nil {
			return err
		}
		return s.appender(ctx, repo.writer(), audit.NewDomainEvent(ctx, tenantID, "scim_target_deleted", "scim_target", id))
	})
	if err != nil {
		return fmt.Errorf("delete SCIM target: %w", err)
	}
	return nil
}

// RetireTarget deprovisions everything this target still provisions and then
// deletes it. It exists because DeleteTarget refuses while mappings are live,
// and doing that by hand is not an option for a directory of any size.
//
// The remote account is removed before the mapping and the bearer token that
// reach it, never after: a failure part-way leaves the target, its credentials
// and every remaining mapping exactly where they were, so the accounts that
// have not been dealt with are still reachable and still counted. Re-running
// resumes rather than restarts -- a deprovisioned mapping is tombstoned, and
// tombstoned mappings are not live -- so the answer to a remote that was down
// is to call this again once it is back.
//
// It returns the number of resources deprovisioned, including on failure, so
// the caller can say how far it got.
func (s *Service) RetireTarget(ctx context.Context, tenantID, id guid.ID) (int, error) {
	if tenantID.Zero() || id.Zero() {
		return 0, ErrInvalidTarget
	}
	// Deprovisioning talks to the remote, which a disabled target may not do.
	// Rather than make an exception here, retirement asks for the same target
	// state every other outbound call asks for.
	if err := s.markRetiring(ctx, tenantID, id); err != nil {
		return 0, err
	}
	deprovisioned := 0
	for {
		mappings, err := s.repo.targetResourcePage(ctx, id, "", 0, syncPageSize)
		if err != nil {
			return deprovisioned, err
		}
		if len(mappings) == 0 {
			break
		}
		for _, mapping := range mappings {
			if mapping.DeletedAt != nil || mapping.ExternalID == "" {
				continue
			}
			if err := s.DeprovisionResource(ctx, tenantID, id, mapping.ResourceType, mapping.ResourceID); err != nil {
				return deprovisioned, fmt.Errorf("retire SCIM target: %w", err)
			}
			deprovisioned++
		}
	}
	if err := s.DeleteTarget(ctx, tenantID, id); err != nil {
		return deprovisioned, err
	}
	return deprovisioned, nil
}

// PushResource PUTs the current local user or group to the target. The mapping
// is upserted with the remote id so later pushes and deprovisioning reuse it.
func (s *Service) PushResource(ctx context.Context, tenantID, targetID guid.ID, resourceType string, resourceID guid.ID) (PushResult, error) {
	result, err := s.pushResource(ctx, tenantID, targetID, resourceType, resourceID, false)
	if errors.Is(err, ErrResourceMissing) {
		if deprovisionErr := s.deprovisionResource(ctx, tenantID, targetID, resourceType, resourceID, true); deprovisionErr != nil && !errors.Is(deprovisionErr, ErrDeprovisionMissing) {
			return PushResult{}, deprovisionErr
		}
	}
	return result, err
}

func (s *Service) pushResource(ctx context.Context, tenantID, targetID guid.ID, resourceType string, resourceID guid.ID, liveOnly bool) (PushResult, error) {
	resourceType = strings.TrimSpace(resourceType)
	if tenantID.Zero() || targetID.Zero() || resourceID.Zero() || (resourceType != ResourceUser && resourceType != ResourceGroup) {
		return PushResult{}, ErrInvalidTarget
	}
	// Serialize push/deprovision per resource to avoid racing the remote
	// state between concurrent operations.
	unlock := s.lockPushResource(targetID, resourceType, resourceID)
	defer unlock()
	op, err := s.claimTarget(ctx, tenantID, targetID, "push", false, resourceType, resourceID)
	if err != nil {
		return PushResult{}, err
	}
	defer op.release()
	if liveOnly {
		mapping, err := s.repo.getTargetResource(ctx, targetID, resourceType, resourceID)
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && (mapping.DeletedAt != nil || mapping.ExternalID == "")) {
			return PushResult{}, ErrDeprovisionMissing
		}
		if err != nil {
			return PushResult{}, err
		}
	}
	target, err := s.repo.getTarget(ctx, tenantID, targetID)
	if err != nil {
		return PushResult{}, err
	}
	if target.Status != TargetActive {
		return PushResult{}, ErrTargetDisabled
	}
	if target.ServiceAccountID == nil {
		return PushResult{}, ErrUnauthorized
	}
	if err := s.requireServiceAccount(ctx, s.repo, tenantID, target.ServiceAccountID); err != nil {
		return PushResult{}, err
	}
	if s.keys == nil {
		return PushResult{}, ErrTargetKeyMissing
	}
	bearer, err := s.decryptToken(targetID, target.BearerTokenCiphertext)
	if err != nil {
		return PushResult{}, err
	}
	payload, err := s.buildRemoteResource(ctx, tenantID, targetID, resourceType, resourceID, actorID(target.ServiceAccountID))
	if err != nil {
		return PushResult{}, err
	}
	body, err := json.Marshal(payload.resource)
	if err != nil {
		return PushResult{}, fmt.Errorf("encode SCIM push payload: %w", err)
	}
	endpoint := strings.TrimSuffix(target.BaseURL, "/") + "/" + resourceType + "s/" + url.PathEscape(payload.externalID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return PushResult{}, fmt.Errorf("build SCIM push request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", SCIMContentType)
	request.Header.Set("Accept", SCIMContentType)
	// Commit an intent before any remote I/O. The normal live-mapping erasure
	// guard now also covers the first PUT and explicit reprovisioning.
	now := s.now().UTC().Truncate(time.Millisecond)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		mapping, err := repo.getTargetResource(ctx, targetID, resourceType, resourceID)
		if err == nil && mapping.DeletedAt == nil && mapping.ExternalID != "" {
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row := targetResourceRow{TargetID: targetID, ResourceType: resourceType, ResourceID: resourceID, ExternalID: payload.externalID, Version: 1, CreatedAt: now, UpdatedAt: now}
		return repo.upsertTargetResource(ctx, &row)
	})
	if err != nil {
		return PushResult{}, err
	}
	op.uncertain = true
	response, err := s.http.Do(request)
	if err != nil {
		return PushResult{}, fmt.Errorf("%w: %w", ErrRemoteUnavailable, err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			slog.Warn("close SCIM push response", "error", err)
		}
	}()
	if remoteRequestRejected(response.StatusCode) {
		op.uncertain = false
	}
	if response.StatusCode == http.StatusAccepted {
		return PushResult{}, fmt.Errorf("%w: asynchronous remote completion is unresolved", ErrRemoteUnavailable)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteResponseBytes))
	if err != nil {
		return PushResult{}, fmt.Errorf("read SCIM push response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return PushResult{}, fmt.Errorf("%w: status %d: %s", ErrRemoteResponse, response.StatusCode, trimRemoteDetail(responseBody))
	}
	remoteID := payload.externalID
	var created struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(responseBody, &created) == nil && created.ID != "" {
		remoteID = created.ID
	}
	now = s.now().UTC().Truncate(time.Millisecond)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		row := targetResourceRow{TargetID: targetID, ResourceType: resourceType, ResourceID: resourceID, ExternalID: remoteID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := repo.upsertTargetResource(ctx, &row); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, tenantID, "scim_target_pushed", "scim_"+strings.ToLower(resourceType), resourceID)
		event.Detail["target_id"] = targetID
		event.Detail["external_id"] = remoteID
		event.Detail["service_account_id"] = actorID(target.ServiceAccountID)
		if event.PrincipalID == 0 {
			event.PrincipalID = actorID(target.ServiceAccountID)
		}
		if err := s.appender(ctx, repo.writer(), event); err != nil {
			return err
		}
		return op.finish(ctx, repo)
	})
	if err != nil {
		return PushResult{}, fmt.Errorf("record SCIM push mapping: %w", err)
	}
	return PushResult{TargetID: targetID.String(), ResourceType: resourceType, ResourceID: resourceID.String(), ExternalID: payload.externalID, RemoteID: remoteID}, nil
}

// DeprovisionResource DELETEs the mapped remote resource and soft-deletes the
// mapping so a later push re-creates the resource on the target.
func (s *Service) DeprovisionResource(ctx context.Context, tenantID, targetID guid.ID, resourceType string, resourceID guid.ID) error {
	return s.deprovisionResource(ctx, tenantID, targetID, resourceType, resourceID, false)
}

func (s *Service) deprovisionResource(ctx context.Context, tenantID, targetID guid.ID, resourceType string, resourceID guid.ID, onlyMissing bool) error {
	resourceType = strings.TrimSpace(resourceType)
	if tenantID.Zero() || targetID.Zero() || resourceID.Zero() || (resourceType != ResourceUser && resourceType != ResourceGroup) {
		return ErrInvalidTarget
	}
	unlock := s.lockPushResource(targetID, resourceType, resourceID)
	defer unlock()
	op, err := s.claimTarget(ctx, tenantID, targetID, "deprovision", true, resourceType, resourceID)
	if err != nil {
		return err
	}
	defer op.release()
	target, err := s.repo.getTarget(ctx, tenantID, targetID)
	if err != nil {
		return err
	}
	if target.Status != TargetActive {
		return ErrTargetDisabled
	}
	if target.ServiceAccountID == nil {
		return ErrUnauthorized
	}
	if err := s.requireServiceAccount(ctx, s.repo, tenantID, target.ServiceAccountID); err != nil {
		return err
	}
	if s.keys == nil {
		return ErrTargetKeyMissing
	}
	if onlyMissing {
		// The earlier missing projection was read under a different lock
		// acquisition. A restored resource must not lose its new mapping.
		_, err := s.buildRemoteResource(ctx, tenantID, targetID, resourceType, resourceID, actorID(target.ServiceAccountID))
		if err == nil {
			return ErrDeprovisionMissing
		}
		if !errors.Is(err, ErrResourceMissing) {
			return err
		}
	}
	mapping, err := s.repo.getTargetResource(ctx, targetID, resourceType, resourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDeprovisionMissing
		}
		return err
	}
	if mapping.DeletedAt != nil {
		return ErrDeprovisionMissing
	}
	bearer, err := s.decryptToken(targetID, target.BearerTokenCiphertext)
	if err != nil {
		return err
	}
	endpoint := strings.TrimSuffix(target.BaseURL, "/") + "/" + resourceType + "s/" + url.PathEscape(mapping.ExternalID)
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build SCIM deprovision request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Accept", SCIMContentType)
	op.uncertain = true
	response, err := s.http.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRemoteUnavailable, err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			slog.Warn("close SCIM deprovision response", "error", err)
		}
	}()
	if remoteRequestRejected(response.StatusCode) && response.StatusCode != http.StatusNotFound {
		op.uncertain = false
	}
	if response.StatusCode == http.StatusAccepted {
		return fmt.Errorf("%w: asynchronous remote completion is unresolved", ErrRemoteUnavailable)
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteResponseBytes))
	if err != nil {
		return err
	}
	if (response.StatusCode < 200 || response.StatusCode >= 300) && response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("%w: status %d: %s", ErrRemoteResponse, response.StatusCode, trimRemoteDetail(responseBody))
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.markTargetResourceDeleted(ctx, targetID, resourceType, resourceID, now); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, tenantID, "scim_target_deprovisioned", "scim_"+strings.ToLower(resourceType), resourceID)
		event.Detail["target_id"] = targetID
		event.Detail["service_account_id"] = actorID(target.ServiceAccountID)
		if event.PrincipalID == 0 {
			event.PrincipalID = actorID(target.ServiceAccountID)
		}
		if err := s.appender(ctx, repo.writer(), event); err != nil {
			return err
		}
		return op.finish(ctx, repo)
	})
	if err != nil {
		return fmt.Errorf("record SCIM deprovision: %w", err)
	}
	return nil
}

func (s *Service) buildRemoteResource(ctx context.Context, tenantID, targetID guid.ID, resourceType string, resourceID, actor guid.ID) (remoteResource, error) {
	externalID := resourceID.String()
	if mapping, err := s.repo.getTargetResource(ctx, targetID, resourceType, resourceID); err == nil && mapping.ExternalID != "" {
		externalID = mapping.ExternalID
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return remoteResource{}, err
	}
	switch resourceType {
	case ResourceUser:
		fields, err := s.profileConstraint(ctx, tenantID, actor)
		if err != nil {
			return remoteResource{}, err
		}
		// SCIM replacement requires a real userName, not an omitted or masked
		// identifier that could overwrite another remote identity.
		if err := fields.RequireRead("login_name"); err != nil {
			return remoteResource{}, err
		}
		principal, err := s.identity.GetProvisioned(ctx, tenantID, resourceID)
		if err != nil {
			if errors.Is(err, identity.ErrNotFound) {
				return remoteResource{}, ErrResourceMissing
			}
			return remoteResource{}, err
		}
		user := UserResource{
			Schemas:     []string{UserSchema},
			ID:          externalID,
			ExternalID:  resourceID.String(),
			UserName:    principal.LoginName,
			DisplayName: principal.DisplayName,
			Active:      principal.Status == "active" && principal.MembershipStatus == "active",
			Meta:        ResourceMeta{ResourceType: "User"},
		}
		if principal.Email != "" {
			user.Emails = []UserEmail{{Value: principal.Email, Type: "work", Primary: true}}
		}
		return remoteResource{externalID: externalID, resource: projectUser(user, fields)}, nil
	case ResourceGroup:
		group, err := s.groups.Get(ctx, tenantID, resourceID)
		if err != nil {
			if errors.Is(err, organization.ErrGroupNotFound) {
				return remoteResource{}, ErrResourceMissing
			}
			return remoteResource{}, err
		}
		members, err := s.groups.ListMembers(ctx, tenantID, resourceID)
		if err != nil {
			return remoteResource{}, err
		}
		mapped := make([]SCIMGroupMember, 0, len(members))
		for _, member := range members {
			memberMapping, err := s.repo.getTargetResource(ctx, targetID, ResourceUser, member.PrincipalID)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return remoteResource{}, err
			}
			if memberMapping.DeletedAt == nil && memberMapping.ExternalID != "" {
				mapped = append(mapped, SCIMGroupMember{Value: memberMapping.ExternalID})
			}
		}
		resource := GroupResource{
			Schemas:     []string{GroupSchema},
			ID:          externalID,
			ExternalID:  resourceID.String(),
			DisplayName: group.Name,
			Members:     mapped,
			Meta:        ResourceMeta{ResourceType: "Group"},
		}
		return remoteResource{externalID: externalID, resource: resource}, nil
	}
	return remoteResource{}, ErrInvalidTarget
}

// targetTokenAAD binds a sealed bearer token to the target row it belongs to,
// so a ciphertext copied into another row stops opening.
func targetTokenAAD(targetID guid.ID) []byte {
	return []byte("provisioning:secret\x00" + targetID.String())
}

func (s *Service) encryptToken(targetID guid.ID, token string) (string, error) {
	sealed, err := s.keys.Seal("scim-target-bearer", targetTokenAAD(targetID), []byte(token))
	if err != nil {
		return "", fmt.Errorf("seal provisioning token: %w", err)
	}
	return sealed, nil
}

func (s *Service) decryptToken(targetID guid.ID, encoded string) (string, error) {
	plain, err := s.keys.Open("scim-target-bearer", targetTokenAAD(targetID), encoded)
	switch {
	case err == nil:
		return string(plain), nil
	case errors.Is(err, keyringx.ErrUnsupportedEnvelope):
		return "", errors.New("unsupported provisioning ciphertext")
	case errors.Is(err, keyringx.ErrInvalidEnvelope):
		return "", errors.New("invalid provisioning ciphertext")
	case errors.Is(err, keyringx.ErrUnknownKey):
		// Distinct from a failed decrypt because the remedy is different: the
		// retired key goes back into the configuration and the target works
		// again, rather than an operator re-entering a token they may not have.
		return "", errors.New("provisioning token was sealed with a key this deployment no longer holds")
	default:
		return "", errors.New("decrypt provisioning token")
	}
}

func targetFromRow(row targetRow) Target {
	return Target{ID: row.ID.String(), TenantID: row.TenantID.String(), Name: row.Name, BaseURL: row.BaseURL, Status: row.Status, Version: row.Version, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(), ServiceAccountID: row.ServiceAccountID}
}

func validTargetURL(raw string, outbound httpx.Policy) bool {
	if raw == "" || len(raw) > 1024 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	// The loopback exception this used to hard-code is deployment configuration
	// now: a local directory is reachable when outbound_allow_networks names it,
	// which keeps the convenience for whoever needs it without compiling the
	// hole into every deployment.
	return outbound.ValidateURL(raw) == nil
}

func trimRemoteDetail(body []byte) string {
	detail := strings.TrimSpace(string(body))
	if len(detail) > 500 {
		detail = detail[:500] + "..."
	}
	return strings.ReplaceAll(detail, "\n", " ")
}

// SyncTarget reconciles one bounded page and persists its cursor for the next
// call. Only mappings still live after acquiring the operation claim are
// pushed. It reports partial progress together with any per-resource failures.
func (s *Service) SyncTarget(ctx context.Context, tenantID, targetID guid.ID) (int, error) {
	if tenantID.Zero() || targetID.Zero() {
		return 0, ErrInvalidTarget
	}
	target, err := s.repo.getTarget(ctx, tenantID, targetID)
	if err != nil {
		return 0, err
	}
	if target.Status != TargetActive {
		return 0, ErrTargetDisabled
	}
	if target.ServiceAccountID == nil {
		return 0, ErrUnauthorized
	}
	if err := s.requireServiceAccount(ctx, s.repo, tenantID, target.ServiceAccountID); err != nil {
		return 0, err
	}
	if target.Retiring {
		return 0, ErrTargetRetiring
	}
	ctx, cancel := context.WithTimeout(ctx, syncBatchTimeout)
	defer cancel()
	mappings, err := s.repo.targetResourcePage(ctx, targetID, target.SyncResourceType, target.SyncResourceID, syncPageSize)
	if err != nil {
		return 0, fmt.Errorf("list SCIM target resource mappings: %w", err)
	}
	synced := 0
	deprovisioned := 0
	var failures []error
	afterType, afterID := target.SyncResourceType, target.SyncResourceID
	for _, m := range mappings {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		afterType, afterID = m.ResourceType, m.ResourceID
		if m.DeletedAt != nil {
			continue
		}
		_, err := s.pushResource(ctx, tenantID, targetID, m.ResourceType, m.ResourceID, true)
		if err != nil {
			if errors.Is(err, ErrDeprovisionMissing) {
				continue
			}
			// The local resource no longer exists (deleted in the platform);
			// drift-detection auto-deprovisions it from the target.
			if errors.Is(err, ErrResourceMissing) {
				if derr := s.deprovisionResource(ctx, tenantID, targetID, m.ResourceType, m.ResourceID, true); derr != nil {
					if !errors.Is(derr, ErrDeprovisionMissing) {
						failures = append(failures, derr)
					}
					slog.Warn("SCIM sync auto-deprovision failed", "target", targetID, "resource", m.ResourceID, "error", derr)
				} else {
					deprovisioned++
				}
				continue
			}
			slog.Warn("SCIM sync push failed", "target", targetID, "resource", m.ResourceID, "error", err)
			failures = append(failures, err)
			continue
		}
		synced++
	}
	if deprovisioned > 0 {
		slog.Info("SCIM sync auto-deprovisioned missing resources", "target", targetID, "count", deprovisioned)
	}
	if len(mappings) < syncPageSize && ctx.Err() == nil {
		afterType, afterID = "", 0
	}
	// Checkpoint even when the batch deadline fired. A compare-and-swap keeps
	// a concurrent batch from rewinding an already advanced cursor.
	checkpointCtx, checkpointCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer checkpointCancel()
	updates := map[string]any{"sync_resource_type": afterType, "sync_resource_id": afterID, "sync_failures": 0, "sync_after_ms": 0}
	if len(failures) > 0 {
		attempts := min(target.SyncFailures+1, int64(10))
		updates["sync_failures"] = attempts
		updates["sync_after_ms"] = s.now().Add(time.Second * time.Duration(1<<uint(attempts))).UnixMilli()
	}
	targets := s.repo.names.Targets.String()
	if err := s.repo.writer().WithContext(checkpointCtx).Table(targets).
		Where("id = ? AND tenant_id = ? AND sync_resource_type = ? AND sync_resource_id = ? AND sync_failures = ?", targetID, tenantID, target.SyncResourceType, target.SyncResourceID, target.SyncFailures).
		Updates(updates).Error; err != nil {
		failures = append(failures, err)
	}
	return synced, errors.Join(failures...)
}

package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/governance/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
)

const (
	StatusPending   = "pending"
	StatusApproved  = "approved"
	StatusRejected  = "rejected"
	StatusCancelled = "cancelled" //nolint:misspell // Frozen source state value.
	StatusRevoked   = "revoked"
	StatusExpired   = "expired"

	requestTTL        = 7 * 24 * time.Hour
	maxAccessDuration = 365 * 24 * time.Hour
)

var (
	ErrInvalid           = errors.New("invalid access request")
	ErrNotFound          = errors.New("access request not found")
	ErrRoleNotFound      = errors.New("requested role not found")
	ErrRequesterInactive = errors.New("access requester is not an active tenant member")
	ErrAlreadyGranted    = errors.New("requested access is already granted")
	ErrStateConflict     = errors.New("access request state conflict")
	ErrExpired           = errors.New("access request expired")
	ErrSelfApproval      = errors.New("access requester cannot approve their own request")
	ErrRequesterOnly     = errors.New("only the requester can withdraw this access request")
)

type AccessRequest struct {
	ID               guid.ID    `json:"id"`
	TenantID         guid.ID    `json:"tenant_id"`
	RequesterID      guid.ID    `json:"requester_id"`
	RoleID           guid.ID    `json:"role_id"`
	RoleName         string     `json:"role_name"`
	Reason           string     `json:"reason"`
	Status           string     `json:"status"`
	AccessExpiresAt  time.Time  `json:"access_expires_at"`
	RequestExpiresAt time.Time  `json:"request_expires_at"`
	DecidedBy        guid.ID    `json:"decided_by,omitempty"`
	DecisionNote     string     `json:"decision_note,omitempty"`
	DecidedAt        *time.Time `json:"decided_at,omitempty"`
	RevokedBy        guid.ID    `json:"revoked_by,omitempty"`
	RevokeReason     string     `json:"revoke_reason,omitempty"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type RequestableRole struct {
	ID          guid.ID `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
}

type CreateAccessRequest struct {
	RoleID          guid.ID
	Reason          string
	AccessExpiresAt time.Time
}

type requestSnapshot struct {
	Version         int     `json:"version"`
	RequesterID     guid.ID `json:"requester_id"`
	RoleID          guid.ID `json:"role_id"`
	RoleName        string  `json:"role_name"`
	AccessExpiresAt string  `json:"access_expires_at"`
}

type Service struct {
	repo   *Repository
	audit  audit.Appender
	grants core.RoleGrantStore
	now    func() time.Time
}

func NewService(runtime *appenv.Runtime, appender audit.Appender, grants core.RoleGrantStore, ids guid.Source) *Service {
	if runtime == nil || appender == nil || ids == nil || grants.Grant == nil || grants.Revoke == nil ||
		grants.RoleGranted == nil || grants.RemovePermanent == nil || grants.RemoveGroupMembership == nil ||
		grants.RemoveEntityRoleBinding == nil {
		panic("governance service requires runtime, audit appender, role grant store, and id generator")
	}
	return &Service{repo: NewRepository(runtime, ids), audit: appender, grants: grants, now: time.Now}
}

func (s *Service) resolveAccessRequestOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	tenantID, found, err := s.repo.accessRequestOwner(ctx, id)
	return authz.ResourceOwner{TenantID: tenantID}, found, err
}

func (s *Service) resolveAccessReviewOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	tenantID, found, err := s.repo.accessReviewOwner(ctx, id)
	return authz.ResourceOwner{TenantID: tenantID}, found, err
}

func (s *Service) RequestableRoles(ctx context.Context, tenantID guid.ID) ([]RequestableRole, error) {
	if tenantID.Zero() || s == nil || s.repo == nil {
		return nil, ErrInvalid
	}
	db := s.repo.writer()
	if db == nil {
		return nil, errRepositoryUnavailable
	}
	roleTable := s.repo.foreign.access.Roles.String()
	roles := make([]RequestableRole, 0)
	err := db.WithContext(ctx).Table(roleTable).
		Select("id, name, description").Where("tenant_id = ?", tenantID).
		Order("name ASC, id ASC").Find(&roles).Error
	if err != nil {
		return nil, fmt.Errorf("list requestable roles: %w", err)
	}
	return roles, nil
}

func (s *Service) Create(ctx context.Context, tenantID, requesterID guid.ID, input CreateAccessRequest) (AccessRequest, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	now := s.now().UTC().Truncate(time.Millisecond)
	input.AccessExpiresAt = input.AccessExpiresAt.UTC().Truncate(time.Millisecond)
	if tenantID.Zero() || requesterID.Zero() || input.RoleID.Zero() || len(input.Reason) < 3 || len(input.Reason) > 500 ||
		!input.AccessExpiresAt.After(now) || input.AccessExpiresAt.After(now.Add(maxAccessDuration)) {
		return AccessRequest{}, ErrInvalid
	}
	id, err := s.repo.nextID()
	if err != nil {
		return AccessRequest{}, fmt.Errorf("generate access request id: %w", err)
	}
	var row model.AccessRequest
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		active, err := repo.activeTenantMember(ctx, tenantID, requesterID)
		if err != nil {
			return err
		}
		if !active {
			return ErrRequesterInactive
		}
		role, err := repo.requestableRole(ctx, tenantID, input.RoleID)
		if err != nil {
			return err
		}
		granted, err := s.grants.RoleGranted(ctx, repo.writer(), tenantID, role.ID, requesterID, now)
		if err != nil {
			return err
		}
		if granted {
			return ErrAlreadyGranted
		}
		snapshot, err := json.Marshal(requestSnapshot{
			Version: 1, RequesterID: requesterID, RoleID: role.ID, RoleName: role.Name,
			AccessExpiresAt: input.AccessExpiresAt.Format(time.RFC3339Nano),
		})
		if err != nil {
			return fmt.Errorf("encode access request snapshot: %w", err)
		}
		row = model.AccessRequest{
			TenantID: tenantID, ID: id, RequesterID: requesterID, RoleID: role.ID,
			RoleName: role.Name, Reason: input.Reason, SnapshotJSON: string(snapshot), Status: StatusPending,
			AccessExpiresAt: input.AccessExpiresAt, RequestExpiresAt: now.Add(requestTTL), CreatedAt: now, UpdatedAt: now,
		}
		tables := repo.tables(repo.writer())
		if err := repo.writer().WithContext(ctx).Table(tables.AccessRequests.TableName()).Create(&row).Error; err != nil {
			return fmt.Errorf("insert access request: %w", err)
		}
		step := model.ApprovalStep{TenantID: tenantID, RequestID: id, Step: 1, Decision: StatusPending, Note: ""}
		if err := repo.writer().WithContext(ctx).Table(tables.ApprovalSteps.TableName()).Create(&step).Error; err != nil {
			return fmt.Errorf("insert access approval step: %w", err)
		}
		event := audit.NewDomainEvent(ctx, tenantID, "access_request_created", "access_request", id)
		event.Detail["role_id"] = role.ID
		event.Detail["requester_id"] = requesterID
		event.Detail["access_expires_at"] = input.AccessExpiresAt.Format(time.RFC3339Nano)
		return s.audit(ctx, repo.writer(), event)
	})
	if err != nil {
		return AccessRequest{}, fmt.Errorf("create access request: %w", err)
	}
	return requestFromRow(row, now), nil
}

func (s *Service) List(ctx context.Context, tenantID, requesterID guid.ID) ([]AccessRequest, error) {
	if tenantID.Zero() || s == nil || s.repo == nil {
		return nil, ErrInvalid
	}
	db := s.repo.writer()
	if db == nil {
		return nil, errRepositoryUnavailable
	}
	rows := make([]model.AccessRequest, 0)
	query := db.WithContext(ctx).Table(s.repo.tables(db).AccessRequests.TableName()).Where("tenant_id = ?", tenantID)
	if !requesterID.Zero() {
		query = query.Where("requester_id = ?", requesterID)
	}
	if err := query.Order("created_at DESC, id DESC").Limit(200).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list access requests: %w", err)
	}
	now := s.now().UTC()
	items := make([]AccessRequest, 0, len(rows))
	for _, row := range rows {
		items = append(items, requestFromRow(row, now))
	}
	return items, nil
}

func (s *Service) Approve(ctx context.Context, tenantID, requestID, actorID guid.ID, note string) (AccessRequest, error) {
	return s.decide(ctx, tenantID, requestID, actorID, note, StatusApproved)
}

func (s *Service) Reject(ctx context.Context, tenantID, requestID, actorID guid.ID, note string) (AccessRequest, error) {
	return s.decide(ctx, tenantID, requestID, actorID, note, StatusRejected)
}

func (s *Service) decide(ctx context.Context, tenantID, requestID, actorID guid.ID, note, decision string) (AccessRequest, error) {
	note = strings.TrimSpace(note)
	if tenantID.Zero() || requestID.Zero() || actorID.Zero() || len(note) > 500 ||
		(decision != StatusApproved && decision != StatusRejected) {
		return AccessRequest{}, ErrInvalid
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	var updated model.AccessRequest
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		row, err := repo.getAccessRequest(ctx, tenantID, requestID)
		if err != nil {
			return err
		}
		if row.RequesterID == actorID {
			return ErrSelfApproval
		}
		if row.Status != StatusPending {
			return ErrStateConflict
		}
		if !row.RequestExpiresAt.After(now) || !row.AccessExpiresAt.After(now) {
			return ErrExpired
		}
		if decision == StatusApproved {
			if err := repo.lockPolicy(ctx, tenantID); err != nil {
				return err
			}
		}
		tables := repo.tables(repo.writer())
		result := repo.writer().WithContext(ctx).Table(tables.AccessRequests.TableName()).
			Where("tenant_id = ? AND id = ? AND status = ? AND request_expires_at > ?", tenantID, requestID, StatusPending, now).
			Updates(map[string]any{"status": decision, "decided_by": actorID, "decision_note": note, "decided_at": now, "updated_at": now})
		if result.Error != nil {
			return fmt.Errorf("decide access request: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrStateConflict
		}
		result = repo.writer().WithContext(ctx).Table(tables.ApprovalSteps.TableName()).
			Where("tenant_id = ? AND request_id = ? AND step = 1 AND decision = ?", tenantID, requestID, StatusPending).
			Updates(map[string]any{"decision": decision, "decided_by": actorID, "decided_at": now, "note": note})
		if result.Error != nil {
			return fmt.Errorf("update access approval step: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrStateConflict
		}
		if decision == StatusApproved {
			if err := s.grants.Grant(ctx, repo.writer(), tenantID, requestID, row.RoleID, row.RequesterID, actorID, now, row.AccessExpiresAt); err != nil {
				return fmt.Errorf("grant approved role: %w", err)
			}
			if err := repo.bumpPolicy(ctx, tenantID, now); err != nil {
				return err
			}
		}
		row.Status, row.DecisionNote, row.UpdatedAt = decision, note, now
		row.DecidedBy, row.DecidedAt = idPointer(actorID), timePointer(now)
		updated = row
		event := audit.NewDomainEvent(ctx, tenantID, "access_request_"+decision, "access_request", requestID)
		event.Detail["role_id"] = row.RoleID
		event.Detail["requester_id"] = row.RequesterID
		return s.audit(ctx, repo.writer(), event)
	})
	if err != nil {
		return AccessRequest{}, fmt.Errorf("%s access request: %w", decision, err)
	}
	return requestFromRow(updated, now), nil
}

func (s *Service) Withdraw(ctx context.Context, tenantID, requestID, requesterID guid.ID, reason string) (AccessRequest, error) {
	return s.revoke(ctx, tenantID, requestID, requesterID, reason, true)
}

func (s *Service) Revoke(ctx context.Context, tenantID, requestID, actorID guid.ID, reason string) (AccessRequest, error) {
	return s.revoke(ctx, tenantID, requestID, actorID, reason, false)
}

func (s *Service) revoke(ctx context.Context, tenantID, requestID, actorID guid.ID, reason string, requesterOnly bool) (AccessRequest, error) {
	reason = strings.TrimSpace(reason)
	if tenantID.Zero() || requestID.Zero() || actorID.Zero() || len(reason) > 500 {
		return AccessRequest{}, ErrInvalid
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	var updated model.AccessRequest
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		row, err := repo.getAccessRequest(ctx, tenantID, requestID)
		if err != nil {
			return err
		}
		if requesterOnly && row.RequesterID != actorID {
			return ErrRequesterOnly
		}
		nextStatus, eventType := StatusCancelled, "access_request_cancelled" //nolint:misspell // Frozen source event type.
		policyChanged := false
		switch row.Status {
		case StatusPending:
			if !requesterOnly {
				return ErrStateConflict
			}
		case StatusApproved:
			if !row.AccessExpiresAt.After(now) {
				return ErrExpired
			}
			if err := repo.lockPolicy(ctx, tenantID); err != nil {
				return err
			}
			changed, err := s.grants.Revoke(ctx, repo.writer(), tenantID, requestID)
			if err != nil {
				return err
			}
			if !changed {
				return ErrStateConflict
			}
			nextStatus, eventType, policyChanged = StatusRevoked, "access_grant_revoked", true
		default:
			return ErrStateConflict
		}
		result := repo.writer().WithContext(ctx).Table(repo.tables(repo.writer()).AccessRequests.TableName()).
			Where("tenant_id = ? AND id = ? AND status = ?", tenantID, requestID, row.Status).
			Updates(map[string]any{"status": nextStatus, "revoked_by": actorID, "revoke_reason": reason, "revoked_at": now, "updated_at": now})
		if result.Error != nil {
			return fmt.Errorf("revoke access request: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrStateConflict
		}
		if policyChanged {
			if err := repo.bumpPolicy(ctx, tenantID, now); err != nil {
				return err
			}
		}
		row.Status, row.RevokeReason, row.UpdatedAt = nextStatus, reason, now
		row.RevokedBy, row.RevokedAt = idPointer(actorID), timePointer(now)
		updated = row
		event := audit.NewDomainEvent(ctx, tenantID, eventType, "access_request", requestID)
		event.Detail["role_id"] = row.RoleID
		event.Detail["requester_id"] = row.RequesterID
		return s.audit(ctx, repo.writer(), event)
	})
	if err != nil {
		return AccessRequest{}, fmt.Errorf("revoke access request: %w", err)
	}
	return requestFromRow(updated, now), nil
}

func (r *Repository) requestableRole(ctx context.Context, tenantID, roleID guid.ID) (RequestableRole, error) {
	var role RequestableRole
	roleTable := r.foreign.access.Roles.String()
	err := r.writer().WithContext(ctx).Table(roleTable).Select("id, name, description").
		Where("tenant_id = ? AND id = ?", tenantID, roleID).Take(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return role, ErrRoleNotFound
	}
	if err != nil {
		return role, fmt.Errorf("get requestable role: %w", err)
	}
	return role, nil
}

func (r *Repository) activeTenantMember(ctx context.Context, tenantID, principalID guid.ID) (bool, error) {
	members := r.foreign.access.TenantMembers.String()
	tenants := r.foreign.directory.Tenants.String()
	var count int64
	err := r.writer().WithContext(ctx).Table(members+" AS members").
		Joins("JOIN "+tenants+" AS tenants ON tenants.id = members.tenant_id AND tenants.status = 'active'").
		Where("members.tenant_id = ? AND members.principal_id = ? AND members.status = 'active'", tenantID, principalID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("check access requester membership: %w", err)
	}
	return count == 1, nil
}

func requestFromRow(row model.AccessRequest, now time.Time) AccessRequest {
	status := row.Status
	if (status == StatusPending && !row.RequestExpiresAt.After(now)) || (status == StatusApproved && !row.AccessExpiresAt.After(now)) {
		status = StatusExpired
	}
	return AccessRequest{
		ID: row.ID, TenantID: row.TenantID, RequesterID: row.RequesterID, RoleID: row.RoleID,
		RoleName: row.RoleName, Reason: row.Reason, Status: status, AccessExpiresAt: row.AccessExpiresAt.UTC(),
		RequestExpiresAt: row.RequestExpiresAt.UTC(), DecidedBy: optionalID(row.DecidedBy), DecisionNote: row.DecisionNote,
		DecidedAt: utcPointer(row.DecidedAt), RevokedBy: optionalID(row.RevokedBy), RevokeReason: row.RevokeReason,
		RevokedAt: utcPointer(row.RevokedAt), CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
}

func idPointer(id guid.ID) *guid.ID { return &id }

func timePointer(value time.Time) *time.Time { value = value.UTC(); return &value }

func optionalID(id *guid.ID) guid.ID {
	if id == nil {
		return 0
	}
	return *id
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

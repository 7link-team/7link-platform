package organization

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	InvitationPending    = "pending"
	InvitationAccepted   = "accepted"
	InvitationRevoked    = "revoked"
	InvitationExpired    = "expired"
	defaultInvitationTTL = 72 * time.Hour
	maxInvitationTTL     = 30 * 24 * time.Hour
)

var (
	ErrInvitationNotFound        = errors.New("invitation not found")
	ErrInvitationInvalid         = errors.New("invalid invitation")
	ErrInvitationCredential      = errors.New("invalid invitation credential")
	ErrInvitationExpired         = errors.New("invitation expired")
	ErrInvitationState           = errors.New("invalid invitation state")
	ErrInvitationLoginConflict   = errors.New("invitation login name conflict")
	ErrInvitationBindingMissing  = errors.New("invitation binding not found")
	ErrInvitationBindingInactive = errors.New("invitation binding inactive")
)

type Invitation struct {
	ID           guid.ID   `json:"id"`
	TenantID     guid.ID   `json:"tenant_id"`
	Email        string    `json:"email"`
	Status       string    `json:"status"`
	DepartmentID guid.ID   `json:"department_id,omitempty"`
	RoleIDs      []guid.ID `json:"role_ids"`
	ExpiresAt    time.Time `json:"expires_at"`
	AcceptedBy   guid.ID   `json:"accepted_by,omitempty"`
	// The two outcome timestamps are pointers because omitempty does not reach
	// inside a time.Time: a pending invitation would otherwise report that it
	// was accepted in year one, which a client formatting the field will show.
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
type InvitationAcceptance struct {
	InvitationID    guid.ID `json:"invitation_id"`
	TenantID        guid.ID `json:"tenant_id"`
	PrincipalID     guid.ID `json:"principal_id"`
	Email           string  `json:"email"`
	AlreadyAccepted bool    `json:"already_accepted"`
}
type CreateInvitation struct {
	Email        string
	DepartmentID guid.ID
	RoleIDs      []guid.ID
	TTL          time.Duration
}
type AcceptInvitation struct {
	Token       string
	LoginName   string
	Password    string
	DisplayName string
}

type InvitationCredentials = authn.InvitationCredentials
type InvitationPrincipalCreator = identity.InvitationPrincipalCreator

type InvitationService struct {
	repo            *Repository
	administrators  core.AdministratorGuard
	credentials     InvitationCredentials
	createPrincipal InvitationPrincipalCreator
	now             func() time.Time
}

func NewInvitationService(runtime *appenv.Runtime, appender audit.Appender, administrators core.AdministratorGuard, ids guid.Source, credentials InvitationCredentials, createPrincipal InvitationPrincipalCreator) *InvitationService {
	if runtime == nil || appender == nil || administrators == nil || ids == nil || credentials == nil || createPrincipal == nil {
		panic("invitation service requires runtime, audit appender, administrator guard, id generator, credentials, and principal creator")
	}
	return &InvitationService{repo: NewRepository(runtime, ids, appender), administrators: administrators, credentials: credentials, createPrincipal: createPrincipal, now: time.Now}
}

// requireGrantableInvitationRoles keeps an invitation from carrying roles the
// inviter could not grant directly: acceptance attaches them without an actor.
func (s *InvitationService) requireGrantableInvitationRoles(ctx context.Context, repo *Repository, tenantID guid.ID, roles []guid.ID) error {
	if authnext.PrincipalIDFromContext(ctx).Zero() || len(roles) == 0 {
		return nil
	}
	checker, ok := s.administrators.(interface {
		RequireGrantableRoles(context.Context, *gorm.DB, guid.ID, []guid.ID) error
	})
	if !ok {
		return ErrUnavailable
	}
	return checker.RequireGrantableRoles(ctx, repo.writer(), tenantID, roles)
}

// requireGrantableInvitationDepartment keeps an invitation from placing its
// invitee in a department where the invitation's department-scoped roles reach
// further than the inviter's own data scope: acceptance applies both without
// an actor, so this is the only point the inviter's authority can be checked.
func (s *InvitationService) requireGrantableInvitationDepartment(ctx context.Context, repo *Repository, tenantID, departmentID guid.ID, roles []guid.ID) error {
	if authnext.PrincipalIDFromContext(ctx).Zero() || departmentID.Zero() || len(roles) == 0 {
		return nil
	}
	checker, ok := s.administrators.(interface {
		RequireGrantableDepartmentPlacement(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) error
	})
	if !ok {
		return ErrUnavailable
	}
	return checker.RequireGrantableDepartmentPlacement(ctx, repo.writer(), tenantID, departmentID, roles)
}

func (s *InvitationService) prepareGrantableInvitationRoles(ctx context.Context, repo *Repository, tenantID guid.ID) error {
	if authnext.PrincipalIDFromContext(ctx).Zero() {
		return nil
	}
	if checker, ok := s.administrators.(interface {
		PrepareGrantableRoles(context.Context, *gorm.DB, guid.ID) error
	}); ok {
		return checker.PrepareGrantableRoles(ctx, repo.writer(), tenantID)
	}
	return nil
}

func (s *InvitationService) List(ctx context.Context, tenantID guid.ID) ([]Invitation, error) {
	if tenantID.Zero() {
		return nil, ErrInvitationInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	tables, _, err := repo.tables()
	if err != nil {
		return nil, err
	}
	var rows []model.Invitation
	err = repo.writer().WithContext(ctx).Table(tables.Invitations.TableName()).Where("tenant_id = ?", tenantID).Order("created_at DESC, id DESC").Limit(200).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	return listInvitations(ctx, repo, rows, organizationTime(s.now()))
}

func (s *InvitationService) Create(ctx context.Context, tenantID guid.ID, input CreateInvitation) (Invitation, string, error) {
	tenantID, input, err := normalizeInvitation(tenantID, input)
	if err != nil {
		return Invitation{}, "", err
	}
	id, err := s.repo.nextID()
	if err != nil {
		return Invitation{}, "", fmt.Errorf("generate invitation id: %w", err)
	}
	token, digest, err := s.credentials.IssueInvitationToken(id)
	if err != nil {
		return Invitation{}, "", fmt.Errorf("issue invitation credential: %w", err)
	}
	now := organizationTime(s.now())
	row := model.Invitation{TenantID: tenantID, ID: id, Email: input.Email, EmailKey: input.Email, TokenHMAC: digest, Status: InvitationPending, DepartmentID: idOrNil(input.DepartmentID), ExpiresAt: now.Add(input.TTL), CreatedAt: now, UpdatedAt: now}
	event := audit.NewDomainEvent(ctx, tenantID, "invitation_created", "invitation", id)
	event.Detail["department_assigned"], event.Detail["role_count"] = !input.DepartmentID.Zero(), len(input.RoleIDs)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		if err := s.prepareGrantableInvitationRoles(ctx, repo, tenantID); err != nil {
			return err
		}
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		if err := validateInvitationBindings(ctx, repo, tenantID, input.DepartmentID, input.RoleIDs); err != nil {
			return err
		}
		if err := s.requireGrantableInvitationRoles(ctx, repo, tenantID, input.RoleIDs); err != nil {
			return err
		}
		if err := s.requireGrantableInvitationDepartment(ctx, repo, tenantID, input.DepartmentID, input.RoleIDs); err != nil {
			return err
		}
		if err := repo.writer().WithContext(ctx).Table(tables.Invitations.TableName()).Where("tenant_id = ? AND email_key = ? AND status = ?", tenantID, input.Email, InvitationPending).
			Updates(map[string]any{"status": InvitationRevoked, "revoked_at": now, "updated_at": now}).Error; err != nil {
			return fmt.Errorf("revoke previous invitation: %w", err)
		}
		if err := repo.writer().WithContext(ctx).Table(tables.Invitations.TableName()).Create(&row).Error; err != nil {
			return fmt.Errorf("insert invitation: %w", err)
		}
		if err := insertInvitationRoles(ctx, repo, tenantID, id, input.RoleIDs); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Invitation{}, "", fmt.Errorf("create invitation: %w", err)
	}
	return invitationFromRow(&row, input.RoleIDs, now), token, nil
}

func (s *InvitationService) Revoke(ctx context.Context, tenantID, id guid.ID) error {
	if tenantID.Zero() || !validID(id) {
		return ErrInvitationInvalid
	}
	now := organizationTime(s.now())
	event := audit.NewDomainEvent(ctx, tenantID, "invitation_revoked", "invitation", id)
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		row, err := getInvitation(ctx, repo, tenantID, id)
		if err != nil {
			return err
		}
		if row.Status != InvitationPending {
			return ErrInvitationState
		}
		if !row.ExpiresAt.After(now) {
			return ErrInvitationExpired
		}
		result := repo.writer().WithContext(ctx).Table(tables.Invitations.TableName()).Where("tenant_id = ? AND id = ? AND status = ?", tenantID, id, InvitationPending).
			Updates(map[string]any{"status": InvitationRevoked, "revoked_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvitationState
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	return nil
}

func (s *InvitationService) Resend(ctx context.Context, tenantID, id guid.ID, ttl time.Duration) (Invitation, string, error) {
	if ttl == 0 {
		ttl = defaultInvitationTTL
	}
	if tenantID.Zero() || !validID(id) || ttl < time.Hour || ttl > maxInvitationTTL {
		return Invitation{}, "", ErrInvitationInvalid
	}
	token, digest, err := s.credentials.IssueInvitationToken(id)
	if err != nil {
		return Invitation{}, "", fmt.Errorf("issue invitation credential: %w", err)
	}
	now := organizationTime(s.now())
	var updated *model.Invitation
	event := audit.NewDomainEvent(ctx, tenantID, "invitation_resent", "invitation", id)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		if err := s.prepareGrantableInvitationRoles(ctx, repo, tenantID); err != nil {
			return err
		}
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		current, err := getInvitation(ctx, repo, tenantID, id)
		if err != nil {
			return err
		}
		if current.Status != InvitationPending {
			return ErrInvitationState
		}
		roles, err := invitationRoleIDs(ctx, repo, tenantID, id)
		if err != nil {
			return err
		}
		if err := s.requireGrantableInvitationRoles(ctx, repo, tenantID, roles); err != nil {
			return err
		}
		if err := s.requireGrantableInvitationDepartment(ctx, repo, tenantID, optionalID(current.DepartmentID), roles); err != nil {
			return err
		}
		result := repo.writer().WithContext(ctx).Table(tables.Invitations.TableName()).Where("tenant_id = ? AND id = ? AND status = ?", tenantID, id, InvitationPending).
			Updates(map[string]any{"token_hmac": digest, "expires_at": now.Add(ttl), "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvitationState
		}
		candidate := *current
		updated = &candidate
		updated.TokenHMAC, updated.ExpiresAt, updated.UpdatedAt = digest, now.Add(ttl), now
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Invitation{}, "", fmt.Errorf("resend invitation: %w", err)
	}
	roles, err := invitationRoleIDs(ctx, s.repo, tenantID, id)
	if err != nil {
		return Invitation{}, "", err
	}
	return invitationFromRow(updated, roles, now), token, nil
}

func (s *InvitationService) Accept(ctx context.Context, input AcceptInvitation) (InvitationAcceptance, error) {
	input.Token, input.LoginName, input.DisplayName = strings.TrimSpace(input.Token), strings.TrimSpace(input.LoginName), strings.TrimSpace(input.DisplayName)
	id, err := invitationIDFromToken(input.Token)
	if err != nil || input.LoginName == "" || len(input.LoginName) > 200 || len(input.Password) < 12 || len(input.Password) > 1024 || len(input.DisplayName) > 128 {
		return InvitationAcceptance{}, ErrInvitationCredential
	}
	now := organizationTime(s.now())
	var accepted InvitationAcceptance
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		row, err := getInvitationByID(ctx, repo, id)
		if err != nil || !s.credentials.InvitationTokenMatches(input.Token, row.TokenHMAC) {
			return ErrInvitationCredential
		}
		if err := repo.revisions.Lock(ctx, repo.writer(), row.TenantID); err != nil {
			return err
		}
		if row.Status == InvitationAccepted {
			accepted = InvitationAcceptance{InvitationID: row.ID, TenantID: row.TenantID, PrincipalID: optionalID(row.AcceptedBy), Email: row.Email, AlreadyAccepted: true}
			return nil
		}
		if row.Status != InvitationPending {
			return ErrInvitationCredential
		}
		if !row.ExpiresAt.After(now) {
			return ErrInvitationExpired
		}
		roles, err := invitationRoleIDs(ctx, repo, row.TenantID, row.ID)
		if err != nil {
			return err
		}
		if err := validateInvitationBindings(ctx, repo, row.TenantID, optionalID(row.DepartmentID), roles); err != nil {
			return err
		}
		principalID, err := s.createPrincipal(ctx, repo.writer(), row.TenantID, input.LoginName, input.Password, input.DisplayName, row.Email)
		if err != nil {
			return err
		}
		if row.DepartmentID != nil {
			membership := model.MemberDepartment{TenantID: row.TenantID, PrincipalID: principalID, DepartmentID: *row.DepartmentID, UpdatedAt: now}
			if err := repo.writer().WithContext(ctx).Table(tables.MemberDepartments.TableName()).Create(&membership).Error; err != nil {
				return fmt.Errorf("assign invitation department: %w", err)
			}
		}
		roleMembers := repo.foreign.access.RoleMembers.String()
		for _, roleID := range roles {
			if err := repo.writer().WithContext(ctx).Table(roleMembers).Create(map[string]any{"tenant_id": row.TenantID, "role_id": roleID, "principal_id": principalID, "created_at": now}).Error; err != nil {
				return fmt.Errorf("assign invitation role: %w", err)
			}
		}
		result := repo.writer().WithContext(ctx).Table(tables.Invitations.TableName()).Where("tenant_id = ? AND id = ? AND status = ? AND token_hmac = ? AND expires_at > ?", row.TenantID, row.ID, InvitationPending, row.TokenHMAC, now).
			Updates(map[string]any{"status": InvitationAccepted, "accepted_by": principalID, "accepted_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvitationCredential
		}
		if err := repo.bumpPolicyRevision(ctx, row.TenantID, now); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, row.TenantID, "invitation_accepted", "invitation", row.ID)
		event.PrincipalID = principalID
		event.Detail["department_assigned"], event.Detail["role_count"] = row.DepartmentID != nil, len(roles)
		if err := repo.appendAudit(ctx, event); err != nil {
			return err
		}
		accepted = InvitationAcceptance{InvitationID: row.ID, TenantID: row.TenantID, PrincipalID: principalID, Email: row.Email}
		return nil
	})
	if err != nil {
		return InvitationAcceptance{}, fmt.Errorf("accept invitation: %w", err)
	}
	return accepted, nil
}

func normalizeInvitation(tenantID guid.ID, input CreateInvitation) (guid.ID, CreateInvitation, error) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.TTL == 0 {
		input.TTL = defaultInvitationTTL
	}
	parsed, err := mail.ParseAddress(input.Email)
	if tenantID.Zero() || err != nil || !strings.EqualFold(parsed.Address, input.Email) || len(input.Email) > 320 || !validOptionalID(input.DepartmentID) || input.TTL < time.Hour || input.TTL > maxInvitationTTL {
		return 0, CreateInvitation{}, ErrInvitationInvalid
	}
	seen := make(map[guid.ID]struct{}, len(input.RoleIDs))
	roles := make([]guid.ID, 0, len(input.RoleIDs))
	for _, roleID := range input.RoleIDs {
		if !validID(roleID) {
			return 0, CreateInvitation{}, ErrInvitationInvalid
		}
		if _, ok := seen[roleID]; ok {
			continue
		}
		seen[roleID] = struct{}{}
		roles = append(roles, roleID)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	input.RoleIDs = roles
	return tenantID, input, nil
}

func invitationIDFromToken(token string) (guid.ID, error) {
	const prefix = "inv1_"
	if !strings.HasPrefix(token, prefix) {
		return 0, ErrInvitationCredential
	}
	id, _, ok := strings.Cut(strings.TrimPrefix(token, prefix), ".")
	if !ok {
		return 0, ErrInvitationCredential
	}
	parsed, err := guid.Parse(id)
	if err != nil || parsed.Zero() {
		return 0, ErrInvitationCredential
	}
	return parsed, nil
}

func getInvitation(ctx context.Context, repo *Repository, tenantID, id guid.ID) (*model.Invitation, error) {
	return scanInvitation(ctx, repo, "tenant_id = ? AND id = ?", tenantID, id)
}

func getInvitationByID(ctx context.Context, repo *Repository, id guid.ID) (*model.Invitation, error) {
	return scanInvitation(ctx, repo, "id = ?", id)
}

func scanInvitation(ctx context.Context, repo *Repository, condition string, values ...any) (*model.Invitation, error) {
	tables, _, err := repo.tables()
	if err != nil {
		return nil, err
	}
	var row model.Invitation
	err = repo.writer().WithContext(ctx).Table(tables.Invitations.TableName()).Clauses(clause.Locking{Strength: "UPDATE"}).Where(condition, values...).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvitationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get invitation: %w", err)
	}
	return &row, nil
}

func insertInvitationRoles(ctx context.Context, repo *Repository, tenantID, invitationID guid.ID, roleIDs []guid.ID) error {
	if len(roleIDs) == 0 {
		return nil
	}
	tables, _, err := repo.tables()
	if err != nil {
		return err
	}
	rows := make([]model.InvitationRole, len(roleIDs))
	for index, roleID := range roleIDs {
		rows[index] = model.InvitationRole{TenantID: tenantID, InvitationID: invitationID, RoleID: roleID}
	}
	if err := repo.writer().WithContext(ctx).Table(tables.InvitationRoles.TableName()).Create(&rows).Error; err != nil {
		return fmt.Errorf("insert invitation role: %w", err)
	}
	return nil
}

func invitationRoleIDs(ctx context.Context, repo *Repository, tenantID, invitationID guid.ID) ([]guid.ID, error) {
	tables, _, err := repo.tables()
	if err != nil {
		return nil, err
	}
	var roleIDs []guid.ID
	err = repo.writer().WithContext(ctx).Table(tables.InvitationRoles.TableName()).Select("role_id").
		Where("tenant_id = ? AND invitation_id = ?", tenantID, invitationID).Order("role_id ASC").Scan(&roleIDs).Error
	if err != nil {
		return nil, fmt.Errorf("list invitation roles: %w", err)
	}
	return roleIDs, nil
}

func validateInvitationBindings(ctx context.Context, repo *Repository, tenantID, departmentID guid.ID, roleIDs []guid.ID) error {
	tables, _, err := repo.tables()
	if err != nil {
		return err
	}
	tenant, err := getTenantRow(ctx, repo, tenantID)
	if err != nil {
		return err
	}
	if tenant.Status != TenantActive {
		return ErrInvitationBindingInactive
	}
	if !departmentID.Zero() {
		var status string
		err := repo.writer().WithContext(ctx).Table(tables.Departments.TableName()).Select("status").
			Where("tenant_id = ? AND id = ?", tenantID, departmentID).Scan(&status).Error
		if err != nil {
			return fmt.Errorf("get invitation department: %w", err)
		}
		if status == "" {
			return ErrInvitationBindingMissing
		}
		if status != StatusActive {
			return ErrInvitationBindingInactive
		}
	}
	if len(roleIDs) > 0 {
		roles := repo.foreign.access.Roles.String()
		var count int64
		err = repo.writer().WithContext(ctx).Table(roles).Where("tenant_id = ? AND id IN ?", tenantID, roleIDs).Count(&count).Error
		if err != nil {
			return fmt.Errorf("count invitation roles: %w", err)
		}
		if count != int64(len(roleIDs)) {
			return ErrInvitationBindingMissing
		}
	}
	return nil
}

func listInvitations(ctx context.Context, repo *Repository, rows []model.Invitation, now time.Time) ([]Invitation, error) {
	items := make([]Invitation, 0, len(rows))
	for index := range rows {
		roles, err := invitationRoleIDs(ctx, repo, rows[index].TenantID, rows[index].ID)
		if err != nil {
			return nil, err
		}
		items = append(items, invitationFromRow(&rows[index], roles, now))
	}
	return items, nil
}

// copyRoleIDs detaches the roles from the slice the caller owns and keeps the
// result non-nil. An invitation with no roles has to serialize as an empty
// array: a client that reads the length of role_ids should not have to treat
// null as a second spelling of empty.
func copyRoleIDs(roleIDs []guid.ID) []guid.ID {
	roles := make([]guid.ID, 0, len(roleIDs))
	return append(roles, roleIDs...)
}

// invitationTime detaches a stored timestamp from the row so a later reuse of
// that row cannot change an invitation already handed to a caller.
func invitationTime(stored *time.Time) *time.Time {
	if stored == nil {
		return nil
	}
	moment := *stored
	return &moment
}

func invitationFromRow(row *model.Invitation, roleIDs []guid.ID, now time.Time) Invitation {
	status := row.Status
	if status == InvitationPending && !row.ExpiresAt.After(now) {
		status = InvitationExpired
	}
	item := Invitation{
		ID: row.ID, TenantID: row.TenantID, Email: row.Email, Status: status,
		DepartmentID: optionalID(row.DepartmentID), RoleIDs: copyRoleIDs(roleIDs),
		ExpiresAt: row.ExpiresAt, AcceptedBy: optionalID(row.AcceptedBy), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	item.AcceptedAt, item.RevokedAt = invitationTime(row.AcceptedAt), invitationTime(row.RevokedAt)
	return item
}

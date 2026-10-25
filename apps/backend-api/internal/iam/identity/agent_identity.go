package identity

import (
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"context"
	"crypto/sha512"
	"database/sql/driver"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/iam/identity/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// AgentAvatar renders public deterministic portrait bytes. It confers no
// principal update authority; Fleet owns the authorized style/seed selection.
type AgentAvatar struct {
	Render func(context.Context, string, int64) ([]byte, error)
}

func renderAgentAvatar(ctx context.Context, style string, seed int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if seed < 0 || seed > 9007199254740991 {
		return nil, ErrInvalid
	}
	style = strings.ToLower(style)
	switch style {
	case "pixel", "robot", "monster", "geometric":
	default:
		return nil, ErrInvalid
	}
	return avatarPNG(style, sha512.Sum512([]byte(style+":"+strconv.FormatInt(seed, 10))))
}

func (m *Module) provideAgentAvatar(s *module.Services) error {
	if err := module.Provide(s, AgentIdentityRegistration{Prepare: m.prepareAgentIdentity, CreateOn: m.createAgentIdentityOn}); err != nil {
		return err
	}
	if err := module.Provide[TeamRosterPrincipals](s, TeamRosterPrincipals{ReadOn: m.teamRosterPrincipalsOn}); err != nil {
		return err
	}
	return module.Provide[AgentAvatar](s, AgentAvatar{Render: renderAgentAvatar})
}

// PreparedAgentIdentity carries an immutable governed avatar preparation. Bytes
// are staged before the account transaction; only CreateOn attaches the file.
// Unused preparations are reconciled by Files' existing expiry lifecycle.
type PreparedAgentIdentity struct {
	id     guid.ID
	avatar Avatar
	file   *files.PreparedAccountObject
	token  string
}

type AgentIdentityRegistration struct {
	Prepare  func(context.Context, string, int64) (*PreparedAgentIdentity, error)
	CreateOn func(context.Context, *gorm.DB, guid.ID, string, string, *PreparedAgentIdentity) (ServiceAccountCredentialSecret, error)
}

func (m *Module) prepareAgentIdentity(ctx context.Context, style string, seed int64) (*PreparedAgentIdentity, error) {
	if m == nil || m.Service == nil || m.Service.repo == nil {
		return nil, ErrUnavailable
	}
	pixels, err := renderAgentAvatar(ctx, style, seed)
	if err != nil {
		return nil, err
	}
	id, err := m.Service.repo.nextID()
	if err != nil {
		return nil, err
	}
	avatar, file, token, err := m.Service.repo.prepareAvatarFile(ctx, id, pixels, strings.ToLower(style))
	if err != nil {
		return nil, err
	}
	return &PreparedAgentIdentity{id: id, avatar: avatar, file: file, token: token}, nil
}

// reservedAgentID lets the existing service-account command consume the
// identity reserved for its staged avatar, without changing the shared source.
type reservedAgentID struct{ id guid.ID }

func (s *reservedAgentID) Next() (int64, error) {
	if s.id <= 0 {
		return 0, ErrUnavailable
	}
	id := s.id
	s.id = 0
	return int64(id), nil
}

func (m *Module) createAgentIdentityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, handle, name string, prepared *PreparedAgentIdentity) (ServiceAccountCredentialSecret, error) {
	var empty ServiceAccountCredentialSecret
	if m == nil || m.Service == nil || tx == nil || tx.Statement == nil {
		return empty, ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return empty, ErrInvalid
	}
	if prepared == nil || prepared.id <= 0 || prepared.file == nil {
		return empty, ErrInvalid
	}
	repo, err := m.Service.repo.on(tx)
	if err != nil {
		return empty, err
	}
	service := *m.Service
	service.repo = repo
	accountRepo := *repo
	accountRepo.ids = &reservedAgentID{id: prepared.id}
	service.repo = &accountRepo
	account, err := service.CreateServiceAccount(ctx, tenant, "agent-"+handle, name, "", nil)
	if err != nil {
		return empty, err
	}
	service.repo = repo
	_, err = repo.resourceFiles.Create(ctx, tx, files.AccountResourceInput{Prepared: prepared.file, PrincipalID: account.ID, Purpose: files.PrincipalAvatarPurpose, Slot: "current", Name: prepared.token + ".png", ActorID: avatarActorID(ctx, account.ID)}, service.now())
	if err != nil {
		return empty, err
	}
	raw, err := json.Marshal(prepared.avatar)
	if err != nil {
		return empty, err
	}
	if err = repo.updatePrincipalRow(ctx, account.ID, map[string]any{"avatar": string(raw)}); err != nil {
		return empty, err
	}
	if err = repo.appendAudit(ctx, audit.NewDomainEvent(ctx, tenant, "principal_avatar_updated", "principal", account.ID)); err != nil {
		return empty, err
	}
	// This is a host control-plane credential. It confers no project grants;
	// IAM still evaluates every operation, and runtimes never receive it.
	return service.CreateServiceAccountCredential(ctx, tenant, account.ID, "Agent machine binding", []string{"openid"}, nil)
}

type RosterPrincipal struct {
	Kind         string
	Name         string
	LastActiveAt *time.Time
}

// TeamRosterPrincipals accepts a bounded set selected by the tenant-membership
// owner. Service accounts are never presented as Humans; Fleet distinguishes
// registered Agents. No credential, contact or global account inventory is exposed.
type TeamRosterPrincipals struct {
	ReadOn func(context.Context, *gorm.DB, []guid.ID) (map[guid.ID]RosterPrincipal, error)
}

func (m *Module) teamRosterPrincipalsOn(ctx context.Context, tx *gorm.DB, ids []guid.ID) (map[guid.ID]RosterPrincipal, error) {
	if m == nil || m.Service == nil || tx == nil {
		return nil, ErrUnavailable
	}
	if len(ids) > 200 {
		return nil, ErrInvalid
	}
	out := map[guid.ID]RosterPrincipal{}
	if len(ids) == 0 {
		return out, nil
	}
	values := make([]driver.Valuer, len(ids))
	for i, id := range ids {
		if id <= 0 {
			return nil, ErrInvalid
		}
		values[i] = id
	}
	q := query.BindWithNames(tx, m.Service.repo.names)
	rows, err := q.Principals.WithContext(ctx).Where(q.Principals.ID.In(values...), q.Principals.Status.Eq("active")).Find()
	if err != nil {
		return nil, err
	}
	accounts, err := q.ServiceAccounts.WithContext(ctx).Where(q.ServiceAccounts.PrincipalID.In(values...)).Find()
	if err != nil {
		return nil, err
	}
	service := map[guid.ID]bool{}
	for _, row := range accounts {
		service[row.PrincipalID] = true
	}
	for _, row := range rows {
		kind := "HUMAN"
		if service[row.ID] {
			kind = "SERVICE_ACCOUNT"
		}
		out[row.ID] = RosterPrincipal{Kind: kind, Name: row.DisplayName}
	}
	return out, nil
}

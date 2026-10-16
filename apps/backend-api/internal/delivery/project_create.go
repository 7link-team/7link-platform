package delivery

import (
	"context"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

var (
	ErrProjectForbidden    = errors.New("delivery: project creation forbidden")
	ErrProjectNonDelegable = errors.New("delivery: project creation requires a human")
	ErrProjectCodeTaken    = errors.New("delivery: project code already used")
	ErrProjectLimit        = errors.New("delivery: active project limit reached")
)

// createProject consumes Huma-validated/defaulted H1 input and a server-authenticated
// principal. db must be the writer, not an existing transaction: role seeding
// commits before the business transaction. No result is returned on rollback.
func (m *Module) createProject(ctx context.Context, db *gorm.DB, tenant, principal guid.ID, input projectCreateBody, audit collab.ProvisioningAudit) (projectCreated, error) {
	ids, err := m.prepareProjectCreation(ctx, db, tenant, principal, input)
	if err != nil {
		return projectCreated{}, err
	}
	var result projectCreated
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = m.createProjectOn(ctx, tx, tenant, principal, input, audit, ids)
		return err
	})
	if err != nil {
		return projectCreated{}, err
	}
	return result, nil
}

// prepareProjectCreation resolves identity and seeds roles without establishing
// a read snapshot in the later business/idempotency transaction.
func (m *Module) prepareProjectCreation(ctx context.Context, db *gorm.DB, tenant, principal guid.ID, input projectCreateBody) ([]guid.ID, error) {
	if db == nil || db.Statement == nil || tenant <= 0 || principal <= 0 || len(input.Members) > 50 {
		return nil, ErrInvalidArgument
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return nil, ErrInvalidArgument
	}
	if authn.PrincipalIDFromContext(ctx) != principal {
		return nil, ErrProjectForbidden
	}
	if m == nil || m.service == nil || m.createAuthority == nil || m.roles.EnsureTenant == nil || m.principals.ActiveHumanByID == nil || m.principals.Lock == nil {
		return nil, ErrUnavailable
	}
	if m.service.entities.CreateOn == nil || m.service.entities.GetOn == nil || m.service.roles.PutOn == nil || m.service.channels.CreateDefaultOn == nil {
		return nil, ErrUnavailable
	}
	// Classify outside the transaction so this read cannot establish a MySQL
	// repeatable-read snapshot before the authorization fences. Recheck below.
	human, found, err := m.principals.ActiveHumanByID(ctx, db, principal)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrProjectNonDelegable
	}
	if human.ID != principal {
		return nil, ErrUnavailable
	}
	ids := []guid.ID{principal}
	for _, member := range input.Members {
		if member.PrincipalID <= 0 {
			return nil, ErrInvalidMember
		}
		ids = append(ids, member.PrincipalID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if err := m.roles.EnsureTenant(ctx, tenant); err != nil {
		return nil, err
	}
	return ids, nil
}

// createProjectOn runs after prepareProjectCreation, inside the caller's actual
// business transaction (and, when supplied, Store.Do's savepoint). ids is the
// sorted principal set returned by preparation, not client input. Every error
// requires rollback; callers must never independently commit an aggregate part.
func (m *Module) createProjectOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, input projectCreateBody, audit collab.ProvisioningAudit, ids []guid.ID) (projectCreated, error) {
	if tx == nil || tx.Statement == nil {
		return projectCreated{}, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return projectCreated{}, ErrInvalidArgument
	}
	// Channel provisioning locks all members. Take the same principal locks in
	// sorted order before IAM's system/tenant policy fences, not after them.
	for _, id := range ids {
		if err := m.principals.Lock(ctx, tx, id); err != nil {
			return projectCreated{}, err
		}
	}
	decision, err := m.createAuthority(ctx, tx, tenant, principal, "HUMAN")
	if err != nil {
		return projectCreated{}, err
	}
	if !decision.Allowed {
		return projectCreated{}, ErrProjectForbidden
	}
	if decision.AuthoritySource == "" {
		return projectCreated{}, ErrUnavailable
	}
	current, active, err := m.principals.ActiveHumanByID(ctx, tx, principal)
	if err != nil {
		return projectCreated{}, err
	}
	if !active {
		return projectCreated{}, ErrProjectNonDelegable
	}
	if current.ID != principal {
		return projectCreated{}, ErrUnavailable
	}
	// The tenant policy fence is held through commit by createAuthority. Every
	// project creation must hold it, serializing this count with other creators.
	projects := query.BindProjects(tx, m.service.names)
	count, err := projects.WithContext(ctx).Where(projects.TenantID.Eq(tenant), projects.ArchivedAt.IsNull()).Count()
	if err != nil {
		return projectCreated{}, err
	}
	if count >= 500 {
		return projectCreated{}, ErrProjectLimit
	}
	// The same tenant fence serializes project codes with concurrent creators.
	duplicate, err := projects.WithContext(ctx).Where(projects.TenantID.Eq(tenant), projects.Code.Eq(input.Code)).Count()
	if err != nil {
		return projectCreated{}, err
	}
	if duplicate > 0 {
		return projectCreated{}, ErrProjectCodeTaken
	}

	if err := m.projectReferencesOn(ctx, tx, tenant, input); err != nil {
		return projectCreated{}, err
	}
	members, err := m.projectMembersOn(ctx, tx, tenant, principal, input.Members)
	if err != nil {
		return projectCreated{}, err
	}
	actor := events.Actor{Kind: "HUMAN", ID: principal, AuthoritySource: decision.AuthoritySource}
	audit.AuthoritySource = decision.AuthoritySource
	draft, err := m.projectDraft(tenant, input, members, actor, audit, time.Now())
	if err != nil {
		return projectCreated{}, err
	}
	return m.service.createOn(ctx, tx, draft)
}

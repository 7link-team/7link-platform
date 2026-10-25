package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
)

// IntakeReplyRouteFence gives a system projector current principal/project
// fences without granting it any Human command permission.
type IntakeReplyRouteFence func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) error

func (m *Module) provideReplyRouting(s *module.Services) error {
	if err := module.Provide[PackageDeliveryRecipient](s, m.packageDeliveryRecipientOn); err != nil {
		return err
	}
	if err := module.Provide[ClientCommentContacts](s, ClientCommentContacts{ContactOn: m.clientCommentContactOn}); err != nil {
		return err
	}
	return module.Provide[IntakeReplyRouteFence](s, m.intakeReplyRouteFenceOn)
}

func (m *Module) intakeReplyRouteFenceOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, principals []guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || len(principals) == 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m.principals.LockActiveRow == nil || m.members == nil {
		return ErrUnavailable
	}
	ids := slices.Clone(principals)
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if id <= 0 {
			return ErrInvalidArgument
		}
		_, active, err := m.principals.LockActiveRow(ctx, tx, id)
		if err != nil {
			return err
		}
		if !active {
			return ErrWorkForbidden
		}
		active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if !active {
			return ErrWorkForbidden
		}
	}
	p := query.BindProjects(tx, m.service.names)
	_, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ArchivedAt.IsNull()).First()
	return err
}

func (m *Module) injectMerge(s *module.Services) error {
	c, err := module.Resolve[intake.AgentMergeConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(m.agentMergeAuthorityOn)
}

func (m *Module) agentMergeAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, card intake.TriageSource) (string, error) {
	if authn.TenantIDFromContext(ctx) != tenant || authn.EntityIDFromContext(ctx) != project || card.ProjectID != project || card.IntakeID <= 0 || card.ActorID <= 0 {
		return "", ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	row, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "intake_convert")
	if err != nil {
		return "", err
	}
	if kind != "AGENT" || row.TeamMode != "AUTONOMOUS" || card.ActorID == actor {
		return "", ErrProjectNonDelegable
	}
	if !slices.Contains([]string{"INTAKE", "DISCOVERY", "PLANNING", "ACTIVE", "BLOCKED", "ACCEPTANCE_PENDING"}, row.Stage) {
		return "", ErrRequirementStage
	}
	if m.deliveryAuthority == nil {
		return "", ErrUnavailable
	}
	decision, err := m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: actor, ActorKind: kind, Code: "intake_convert", Resource: governance.ResourceRef{Kind: "intake", ID: card.IntakeID}, Facts: &governance.WorkItemFacts{TriageCardAuthorID: card.ActorID.String(), IntakeConvertMode: "MERGE"}})
	if err != nil {
		return "", err
	}
	if !decision.Allowed || decision.AuthoritySource == "" || decision.DelegationID <= 0 {
		return "", ErrProjectNonDelegable
	}
	return decision.AuthoritySource, nil
}

// VerifierWorkPolicy exposes only current classification, inside the caller's
// already-authorized verification transaction; no cross-module table access.
type VerifierWorkPolicy func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, string, error)

func (m *Module) provideVerifierWorkPolicy(s *module.Services) error {
	return module.Provide[VerifierWorkPolicy](s, m.verifierWorkPolicyOn)
}
func (m *Module) verifierWorkPolicyOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) (string, string, error) {
	if m == nil || m.service == nil || tx == nil || tenant <= 0 || project <= 0 || work <= 0 {
		return "", "", ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return "", "", ErrInvalidArgument
	}
	q := query.BindWorkItems(tx, m.service.names)
	r, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(work), q.ArchivedAt.IsNull()).First()
	if err != nil {
		return "", "", err
	}
	return r.Type, r.Risk, nil
}

// ClientCommentContacts resolves current project contacts without granting
// acceptance authority; non-signers may annotate their delivered artifacts.
type ClientCommentContacts struct {
	ContactOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error)
}

func (m *Module) clientCommentContactOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID) (guid.ID, error) {
	if m == nil || m.service == nil || m.sources.ContactsOn == nil {
		return 0, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || principal <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != principal {
		return 0, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	q := query.BindContacts(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Order(q.IntakeContactID).Limit(21).Find()
	if err != nil {
		return 0, err
	}
	if len(rows) > 20 {
		return 0, ErrUnavailable
	}
	ids := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.IntakeContactID)
	}
	contacts, err := m.sources.ContactsOn(ctx, tx, tenant, ids)
	if err != nil {
		return 0, err
	}
	for _, c := range contacts {
		if slices.Contains(ids, c.ID) && c.TenantID == tenant && c.PrincipalID != nil && *c.PrincipalID == principal && c.State == "ACTIVE" && c.ArchivedAt == nil {
			return c.ID, nil
		}
	}
	return 0, ErrWorkForbidden
}

// PackageDeliveryRecipient validates the configured delivery audience, separately
// from channel membership or the signer's can_accept capability.
type PackageDeliveryRecipient func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error

func (m *Module) packageDeliveryRecipientOn(ctx context.Context, tx *gorm.DB, tenant, project, recipient guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || recipient <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if _, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), "delivery_send"); err != nil {
		return err
	} else if kind != "HUMAN" {
		return ErrWorkForbidden
	}
	if m.sources.ContactsOn == nil {
		return ErrUnavailable
	}
	q := query.BindContacts(tx, m.service.names)
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ReceivesDeliveries.Is(true)).Order(q.IntakeContactID).Limit(21).Find()
	if err != nil {
		return err
	}
	if len(rows) > 20 {
		return ErrUnavailable
	}
	ids := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.IntakeContactID)
	}
	contacts, err := m.sources.ContactsOn(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), tenant, ids)
	if err != nil {
		return err
	}
	for _, contact := range contacts {
		if contact.TenantID == tenant && contact.PrincipalID != nil && *contact.PrincipalID == recipient && contact.State == "ACTIVE" && contact.ArchivedAt == nil && slices.Contains(ids, contact.ID) {
			return nil
		}
	}
	return ErrWorkForbidden
}

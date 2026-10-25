package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"slices"
)

// createIntakeRequirementOn reuses canonical requirement creation without
// baselining or approving the resulting draft. Clientwork owns the exact frozen
// card check and Intake conversion in the same authorized command transaction.
func (m *Module) createIntakeRequirementOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID, draft intake.TriageDraft) (guid.ID, error) {
	if m.sources.GetOn == nil {
		return 0, ErrUnavailable
	}
	fact, found, err := m.sources.GetOn(ctx, tx, tenant, source)
	if err != nil {
		return 0, err
	}
	if !found || fact.Kind != "NEW" || fact.Status != "TRIAGED" || fact.ProjectID == nil || *fact.ProjectID != project {
		return 0, ErrInvalidArgument
	}
	criteria := make([]requirementCriterionInput, len(draft.Criteria))
	for i, text := range draft.Criteria {
		criteria[i] = requirementCriterionInput{Text: text, Required: true}
	}
	row, err := m.createRequirementOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), requirementCreateBody{Title: draft.Summary, Description: draft.RequirementDraft, Criteria: criteria})
	if err != nil {
		return 0, err
	}
	q := query.BindRequirements(tx, m.service.names)
	_, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(row.ID)).UpdateSimple(q.SourceIntakeID.Value(source))
	return row.ID, err
}

// IntakeWorkOwner resolves a canonical target requirement/work/owner binding
// under current IAM and the existing principal/project/work lock sequence.
type IntakeWorkOwner func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) (guid.ID, error)

// IntakeWorkFence acquires the canonical participant/project/work locks before
// the caller acquires its project fence. The final source match is checked by
// IntakeWorkOwner after the current Intake route has been resolved.
type IntakeWorkFence func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error

func (m *Module) intakeWorkFenceOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, version int) error {
	if tx == nil || tx.Statement == nil || work <= 0 || version < 1 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	q := query.BindWorkItems(tx, m.service.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(work), q.ArchivedAt.IsNull()).First()
	if err != nil {
		return err
	}
	if row.RequirementID == nil {
		return ErrInvalidArgument
	}
	_, err = m.intakeWorkOwnerOn(ctx, tx, tenant, project, *row.RequirementID, work, version)
	return err
}

func (m *Module) intakeWorkOwnerOn(ctx context.Context, tx *gorm.DB, tenant, project, requirement, work guid.ID, version int) (guid.ID, error) {
	if requirement <= 0 || work <= 0 || version < 1 || tx == nil {
		return 0, ErrInvalidArgument
	}
	q := query.BindWorkItems(tx, m.service.names)
	hint, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(work), q.ArchivedAt.IsNull()).First()
	if err != nil {
		return 0, err
	}
	_, row, active, err := m.lockedWorkOn(ctx, tx, tenant, project, work, authn.PrincipalIDFromContext(ctx), hint.AccountablePrincipalID, "work_item_read")
	if err != nil {
		return 0, err
	}
	if !active || row.AccountablePrincipalID != hint.AccountablePrincipalID {
		return 0, ErrInvalidMember
	}
	if row.RequirementID == nil || *row.RequirementID != requirement {
		return 0, ErrInvalidArgument
	}
	if row.Version != version {
		return 0, ErrWorkVersion
	}
	return row.AccountablePrincipalID, nil
}

// IntakeDelegatedConversion is the only Agent entry point to canonical draft
// creation. General requirement writes and NEW_PROJECT remain Human-only.
type IntakeDelegatedConversion struct {
	ReadOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.TriageSource, error)
	AuthorizeOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	CreateOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, int, string) (guid.ID, error)
}

func (m *Module) injectDelegatedNew(s *module.Services) error {
	sources, err := module.Resolve[intake.DelegatedNewSources](s)
	if err != nil {
		return err
	}
	connector, err := module.Resolve[intake.DelegatedNewConnector](s)
	if err != nil {
		return err
	}
	if connector.Bind == nil || sources.ReadOn == nil || sources.ConvertOn == nil {
		return ErrUnavailable
	}
	m.delegatedNewSources = sources
	return connector.Bind(m.delegatedNewAuthorityOn)
}
func (m *Module) delegatedNewAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, card intake.TriageSource) (string, error) {
	if authn.TenantIDFromContext(ctx) != tenant || authn.EntityIDFromContext(ctx) != project || card.IntakeID <= 0 || card.ProjectID != project || card.ActorID <= 0 {
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
	decision, err := m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: actor, ActorKind: kind, Code: "intake_convert", Resource: governance.ResourceRef{Kind: "intake", ID: card.IntakeID}, Facts: &governance.WorkItemFacts{TriageCardAuthorID: card.ActorID.String(), IntakeConvertMode: "NEW_REQUIREMENT"}})
	if err != nil {
		return "", err
	}
	if !decision.Allowed || decision.AuthoritySource == "" || decision.DelegationID <= 0 {
		return "", ErrProjectNonDelegable
	}
	return decision.AuthoritySource, nil
}
func (m *Module) authorizeDelegatedNewOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID) error {
	if m.delegatedNewSources.ReadOn == nil {
		return ErrUnavailable
	}
	// Principal and project fences precede the source/card lock, as in ordinary
	// canonical requirement creation. Governance rechecks under these fences.
	_, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), "intake_convert")
	if err != nil {
		return err
	}
	if kind != "AGENT" {
		return ErrProjectNonDelegable
	}
	_, _, err = m.delegatedNewSources.ReadOn(ctx, tx, tenant, project, source)
	return err
}
func (m *Module) createDelegatedNewOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID, expected, revision int, hash string) (guid.ID, error) {
	if m.delegatedNewSources.ReadOn == nil || m.delegatedNewSources.ConvertOn == nil {
		return 0, ErrUnavailable
	}
	if err := m.authorizeDelegatedNewOn(ctx, tx, tenant, project, source); err != nil {
		return 0, err
	}
	card, authority, err := m.delegatedNewSources.ReadOn(ctx, tx, tenant, project, source)
	if err != nil {
		return 0, err
	}
	if card.Version != expected || card.Revision != revision || card.Hash != hash {
		return 0, ErrRequirementVersion
	}
	criteria := make([]requirementCriterionInput, len(card.Draft.Criteria))
	for i, text := range card.Draft.Criteria {
		criteria[i] = requirementCriterionInput{Text: text, Required: true}
	}
	input, err := validateRequirement(requirementCreateBody{Title: card.Draft.Summary, Description: card.Draft.RequirementDraft, Criteria: criteria})
	if err != nil {
		return 0, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	row, err := m.createAuthorizedRequirementOn(ctx, tx, tenant, project, actor, input, events.Actor{Kind: "AGENT", ID: actor, AuthoritySource: authority})
	if err != nil {
		return 0, err
	}
	q := query.BindRequirements(tx, m.service.names)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(row.ID)).UpdateSimple(q.SourceIntakeID.Value(source)); err != nil {
		return 0, err
	}
	if err = m.delegatedNewSources.ConvertOn(ctx, tx, tenant, project, source, expected, revision, hash, row.ID); err != nil {
		return 0, err
	}
	return row.ID, nil
}

func (m *Module) readDelegatedNewCardOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID) (intake.TriageSource, error) {
	if err := m.authorizeDelegatedNewOn(ctx, tx, tenant, project, source); err != nil {
		return intake.TriageSource{}, err
	}
	card, _, err := m.delegatedNewSources.ReadOn(ctx, tx, tenant, project, source)
	return card, err
}

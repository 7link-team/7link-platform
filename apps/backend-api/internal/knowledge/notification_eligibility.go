package knowledge

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// RuleNotificationEligible rechecks a candidate's current source-owned scope
// authority and excludes the writer. It grants no command or session authority.
type RuleNotificationEligible func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)

func (m *Module) ProvideServices(s *module.Services) error {
	if err := m.provideRetirementRules(s); err != nil {
		return err
	}
	if err := m.provideArchiveResources(s); err != nil {
		return err
	}
	if err := m.provideDistillation(s); err != nil {
		return err
	}
	if err := m.provideMemoryPolicy(s); err != nil {
		return err
	}
	if err := m.provideExecutionView(s); err != nil {
		return err
	}
	if err := m.provideInjection(s); err != nil {
		return err
	}
	if err := m.provideEventIngestion(s); err != nil {
		return err
	}
	if err := m.provideMemoryEvidence(s); err != nil {
		return err
	}
	if err := m.provideSkills(s); err != nil {
		return err
	}
	if err := m.provideDiagnostics(s); err != nil {
		return err
	}
	if err := m.provideReportExperiences(s); err != nil {
		return err
	}
	if err := m.provideRuntimeContext(s); err != nil {
		return err
	}
	if err := m.providePendingRules(s); err != nil {
		return err
	}
	if err := m.provideUsage(s); err != nil {
		return err
	}
	if err := m.provideLearning(s); err != nil {
		return err
	}
	return module.Provide[RuleNotificationEligible](s, m.ruleNotificationEligible)
}

func (m *Module) ruleNotificationEligible(ctx context.Context, tx *gorm.DB, tenant, entry, principal guid.ID) (bool, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || entry <= 0 || principal <= 0 {
		return false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return false, ErrInvalid
	}
	q := query.BindEntries(tx, m.names)
	row, err := q.WithContext(ctx).Select(q.Scope, q.ProjectID, q.AgentPrincipalID, q.Kind, q.CreatedBy).Where(q.TenantID.Eq(tenant), q.ID.Eq(entry)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.Kind != "RULE" || row.CreatedBy == principal {
		return false, nil
	}
	// Only this internal candidate lookup supplies identity to the existing
	// authorizer; persisted principal/member/policy facts remain authoritative.
	candidateCtx := authn.WithClaims(ctx, &authn.Claims{PrincipalID: principal, TenantID: tenant, SubjectType: authn.SubjectTypePrincipal})
	_, err = m.authorize(candidateCtx, tx, tenant, Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}, "memory_approve", true)
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

var _ module.ServiceProvider = (*Module)(nil)

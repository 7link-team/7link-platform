package execution

import (
	"7link.com/platform/internal/delivery"
	"context"
	"errors"
	"slices"
	"strings"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type ContextRef struct {
	Kind string  `json:"kind" enum:"KNOWLEDGE"`
	Ref  guid.ID `json:"ref"`
}
type knowledgeRuntime struct {
	toolPins      func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]agents.RelayToolPin, error)
	work          delivery.WorkKnowledgeContext
	context       knowledge.RuntimeContext
	recorder      knowledge.UsageRecorder
	agentEvidence agents.ContextEvidence
}

func knowledgeRuntimeDependencies() []string { return []string{knowledge.Name, agents.Name} }
func (m *Module) injectKnowledgeRuntime(s *module.Services) error {
	capability, err := module.Resolve[knowledge.RuntimeContext](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	m.knowledge.context = capability
	if m.knowledge.work, err = module.Resolve[delivery.WorkKnowledgeContext](s); err != nil {
		return err
	}
	if m.knowledge.recorder, err = module.Resolve[knowledge.UsageRecorder](s); err != nil {
		return err
	}
	connector, err := module.Resolve[knowledge.UsageEvidenceConnector](s)
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrCapabilityUnavailable
	}
	if err = connector.Bind(knowledge.UsageEvidence{VerifyOn: m.verifyKnowledgeUsageOn}); err != nil {
		return err
	}
	agent, err := module.Resolve[agents.ContextConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if agent.Bind == nil {
		return ErrCapabilityUnavailable
	}
	m.knowledge.agentEvidence = agent.Evidence
	return agent.Bind(m.agentKnowledgeOn)
}

func executorContext(ctx context.Context, tenant, executor guid.ID) context.Context {
	return authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: executor, SubjectType: authn.SubjectTypePrincipal})
}
func (m *Module) pinKnowledgeOn(ctx context.Context, tx *gorm.DB, row *model.Execution, p *ExecutionPlan) error {
	if err := m.pinAgentToolsOn(ctx, tx, row, p); err != nil {
		return err
	}
	if handled, err := m.pinAutomaticKnowledgeOn(ctx, tx, row, p); handled {
		return err
	}
	p.Knowledge = nil // A posted snapshot never establishes source authority.
	if m.knowledge.work.ReadOn != nil {
		ids, err := m.knowledge.work.ReadOn(ctx, tx, row.TenantID, row.ProjectID, row.WorkItemID)
		if err != nil {
			return err
		}
		for _, ref := range p.ContextRefs {
			if ref.Kind != "KNOWLEDGE" || !slices.Contains(ids, ref.Ref) {
				return ErrInvalidArgument
			}
		}
		p.ContextRefs = nil
		for _, id := range ids {
			p.ContextRefs = append(p.ContextRefs, ContextRef{Kind: "KNOWLEDGE", Ref: id})
		}
	} else if len(p.ContextRefs) > 0 {
		return ErrCapabilityUnavailable
	}
	if len(p.ContextRefs) == 0 {
		return nil
	}
	if len(p.ContextRefs) > 50 || p.ExecutorPrincipalID <= 0 {
		return ErrInvalidArgument
	}
	if m.knowledge.context.ResolveOn == nil {
		return ErrCapabilityUnavailable
	}
	ids := make([]guid.ID, len(p.ContextRefs))
	for i, ref := range p.ContextRefs {
		if ref.Kind != "KNOWLEDGE" || ref.Ref <= 0 {
			return ErrInvalidArgument
		}
		ids[i] = ref.Ref
	}
	// Both the initiator and the actual assigned executor must currently read it.
	if _, err := m.knowledge.context.ResolveOn(ctx, tx, row.TenantID, row.ProjectID, ids); err != nil {
		return err
	}
	items, err := m.knowledge.context.ResolveOn(executorContext(ctx, row.TenantID, p.ExecutorPrincipalID), tx, row.TenantID, row.ProjectID, ids)
	p.Knowledge = items
	return err
}

func (m *Module) recordKnowledgeOn(ctx context.Context, tx *gorm.DB, row *model.Execution, evidence guid.ID, kind, text string) error {
	v, err := view(row)
	if err != nil {
		return err
	}
	if len(v.Plan.Knowledge) == 0 {
		return nil
	}
	if m.knowledge.recorder.RecordOn == nil {
		return ErrCapabilityUnavailable
	}
	scoped := executorContext(ctx, row.TenantID, v.Plan.ExecutorPrincipalID)
	for _, item := range v.Plan.Knowledge {
		if kind == "CITATION" && !strings.Contains(text, item.Label) {
			continue
		}
		if err = m.knowledge.recorder.RecordOn(scoped, tx, row.TenantID, knowledge.UsageFact{EntryID: item.EntryID, ExecutionID: row.ID, EvidenceID: evidence, Kind: kind, ContentHash: item.ContentHash}); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) verifyKnowledgeUsageOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, f knowledge.UsageFact) error {
	row, err := m.rowOn(ctx, tx, tenant, f.ExecutionID, false)
	if err != nil {
		return err
	}
	v, err := view(row)
	if err != nil {
		return err
	}
	if v.Plan.ExecutorPrincipalID <= 0 || authn.PrincipalIDFromContext(ctx) != v.Plan.ExecutorPrincipalID || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	var item *knowledge.ContextItem
	for i := range v.Plan.Knowledge {
		candidate := &v.Plan.Knowledge[i]
		if candidate.EntryID == f.EntryID && candidate.ContentHash == f.ContentHash {
			item = candidate
			break
		}
	}
	if item == nil {
		return ErrNotFound
	}
	if f.Kind == "INCLUSION" {
		// Runner inclusion is the persisted lease of this immutable plan. Agent
		// assembly has a separate source-owned durable inbox reference.
		if f.EvidenceID == row.ID && row.LeaseExpiresAt != nil {
			return nil
		}
		if m.knowledge.agentEvidence.VerifyOn == nil {
			return ErrNotFound
		}
		return m.knowledge.agentEvidence.VerifyOn(ctx, tx, tenant, v.Plan.ExecutorPrincipalID, row.ID, f.EvidenceID, row.PlanHash)
	}
	e := query.BindEvidence(tx, m.names)
	output, err := e.WithContext(ctx).Where(e.TenantID.Eq(tenant), e.ExecutionID.Eq(row.ID), e.ID.Eq(f.EvidenceID)).First()
	if err != nil {
		return err
	}
	if output.Kind != "OBSERVATION" && output.Kind != "TEST_REPORT" {
		return ErrInvalidArgument
	}
	if hash([]byte(output.Content)) != output.ContentHash || !strings.Contains(output.Content, item.Label) {
		return ErrConflict
	}
	return nil
}

// The caller is the real executor, not a human browsing an Agent's inbox.
func (m *Module) agentKnowledgeOn(ctx context.Context, tx *gorm.DB, tenant, agent, id guid.ID, persist agents.PersistContext) ([]knowledge.ContextItem, error) {
	row, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return nil, err
	}
	v, err := view(row)
	if err != nil {
		return nil, err
	}
	if authn.PrincipalIDFromContext(ctx) != agent || v.Plan.ExecutorPrincipalID != agent || row.CancelRequestedAt != nil || (row.State != "CREATED" && row.State != "RUNNING") {
		return nil, ErrForbidden
	}
	if m.knowledge.work.ReadOn == nil || m.knowledge.context.ValidateOn == nil {
		return nil, ErrCapabilityUnavailable
	}
	if err = m.workPrincipalLocks(ctx, tx, link(row, agent), agent, row.InitiatorID); err != nil {
		return nil, err
	}
	if _, err = m.knowledge.work.ReadOn(ctx, tx, tenant, row.ProjectID, row.WorkItemID); err != nil {
		return nil, err
	}
	if row, err = m.rowOn(ctx, tx, tenant, id, true); err != nil {
		return nil, err
	}
	if row.CancelRequestedAt != nil || (row.State != "CREATED" && row.State != "RUNNING") {
		return nil, ErrForbidden
	}
	if err = m.knowledge.context.ValidateOn(ctx, tx, tenant, row.ProjectID, v.Plan.Knowledge); err != nil {
		return nil, err
	}
	if len(v.Plan.Knowledge) == 0 {
		return []knowledge.ContextItem{}, nil
	}
	evidence, err := persist(row.ID, row.PlanHash)
	if err != nil {
		return nil, err
	}
	if err = m.recordKnowledgeOn(ctx, tx, row, evidence, "INCLUSION", ""); err != nil {
		return nil, err
	}
	return v.Plan.Knowledge, nil
}

func (m *Module) recordOutputKnowledgeOn(ctx context.Context, tx *gorm.DB, row *model.Execution, in ReceiptInput) error {
	if in.Kind != "EVIDENCE" || (in.EvidenceKind != "OBSERVATION" && in.EvidenceKind != "TEST_REPORT") {
		return nil
	}
	e := query.BindEvidence(tx, m.names)
	output, err := e.WithContext(ctx).Where(e.TenantID.Eq(row.TenantID), e.ExecutionID.Eq(row.ID), e.ContentHash.Eq(hash([]byte(in.Content)))).First()
	if err != nil {
		return err
	}
	return m.recordKnowledgeOn(ctx, tx, row, output.ID, "CITATION", output.Content)
}

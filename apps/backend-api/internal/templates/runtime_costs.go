package templates

import (
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

// RuntimeCosts sums only proven settled executions for these materialized work IDs.
// The adapter is bound after execution and budget activation; absence waits explicitly.
type RuntimeCosts struct {
	WorkOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error)
}
type RuntimeCostsConnector struct{ Bind func(RuntimeCosts) error }

func (m *Module) ProvideServices(s *module.Services) error {
	if err := m.provideProviders(s); err != nil {
		return err
	}
	if err := m.provideEval(s); err != nil {
		return err
	}
	if err := m.provideCalibration(s); err != nil {
		return err
	}
	if err := m.provideVerifiers(s); err != nil {
		return err
	}
	if err := m.provideLifecycle(s); err != nil {
		return err
	}
	if err := module.Provide[PublishedNotificationFacts](s, m.publishedNotificationProjectsOn); err != nil {
		return err
	}
	return module.Provide[RuntimeCostsConnector](s, RuntimeCostsConnector{Bind: func(costs RuntimeCosts) error {
		if costs.WorkOn == nil || m.loopCosts.WorkOn != nil {
			return ErrInvalid
		}
		m.loopCosts = costs
		return nil
	}})
}

var _ module.ServiceProvider = (*Module)(nil)

func (m *Module) escalateLoopOn(ctx context.Context, tx *gorm.DB, tenant, project, binding guid.ID, hash, key, kind string, accountable guid.ID) error {
	if m.escalate == nil || m.events == nil {
		return ErrUnavailable
	}
	actor := events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM:template_read+project_manage"}
	facts := map[string]any{"binding_id": binding.String(), "definition_hash": hash, "expansion_key": key, "accountable_principal_id": accountable.String()}
	event, e := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "template.loop_escalated", Actor: actor, Subject: events.Subject{Kind: "workflow_binding", ID: binding}, SubjectVersion: 1, Data: facts})
	if e != nil {
		return e
	}
	_, e = m.escalate(ctx, tx, governance.EscalationFact{TenantID: tenant, ProjectID: project, SubjectID: project, TriggerEventID: event, Kind: kind, SubjectKind: "PROJECT", Reason: "Workflow loop requires Human decision", Facts: facts, Actor: actor})
	return e
}

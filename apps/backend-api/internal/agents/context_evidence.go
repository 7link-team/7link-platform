package agents

import (
	"context"
	"errors"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type PersistContext func(guid.ID, string) (guid.ID, error)
type ReadContext func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, PersistContext) ([]knowledge.ContextItem, error)
type ContextEvidence struct {
	VerifyOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, string) error
}
type ContextConnector struct {
	Bind     func(ReadContext) error
	Evidence ContextEvidence
}

type ContextInput struct {
	ExecutionID string `query:"execution_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"Optional real execution whose frozen knowledge is delivered to its own assigned Agent. Human browsing does not create usage."`
}
type ManifestInput struct {
	TeamPath
	ContextInput
}

func (m *Module) provideContextEvidence(s *module.Services) error {
	return module.Provide[ContextConnector](s, ContextConnector{Bind: func(read ReadContext) error {
		if read == nil {
			return ErrInvalid
		}
		if m.contextReader != nil {
			return ErrConflict
		}
		m.contextReader = read
		return nil
	}, Evidence: ContextEvidence{VerifyOn: m.verifyContextOn}})
}
func contextSource(execution guid.ID, planHash string) string {
	return "context:" + execution.String() + ":" + planHash[:24]
}

func (m *Module) verifyContextOn(ctx context.Context, tx *gorm.DB, tenant, agent, execution, evidence guid.ID, planHash string) error {
	if len(planHash) != 64 || authn.PrincipalIDFromContext(ctx) != agent || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	q := query.BindInboxEvent(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.ID.Eq(evidence), q.SubjectID.Eq(execution), q.Kind.Eq("STATE"), q.SourceEventID.Eq(contextSource(execution, planHash))).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if row.ID != evidence {
		return ErrNotFound
	}
	return nil
}
func (m *Module) contextOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, execution string) ([]knowledge.ContextItem, error) {
	if execution == "" {
		return nil, nil
	}
	id, err := guid.RequireID(execution)
	if err != nil {
		return nil, ErrInvalid
	}
	if m.contextReader == nil {
		return nil, ErrUnavailable
	}
	items, err := m.contextReader(ctx, tx, tenant, agent, id, func(execution guid.ID, planHash string) (guid.ID, error) {
		if len(planHash) != 64 {
			return 0, ErrInvalid
		}
		source := contextSource(execution, planHash)
		if _, err := m.appendOn(ctx, tx, tenant, agent, InboxReference{SourceEventID: source, Kind: "STATE", SubjectID: execution}); err != nil {
			return 0, err
		}
		q := query.BindInboxEvent(tx, m.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.SourceEventID.Eq(source)).First()
		if err != nil {
			return 0, err
		}
		return row.ID, nil
	})
	if err != nil {
		return nil, err
	}
	if _, err = m.authorize(ctx, tx, tenant, agent, "SELF", false); err != nil {
		return nil, err
	}
	return items, nil
}
func (m *Module) manifestHTTP(ctx context.Context, in *ManifestInput) (*ProtocolBody[Manifest], error) {
	if in.TenantID != tenantID(ctx).String() {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	if in.ExecutionID == "" {
		return protocolOK(ctx, Manifest{Operations: m.registry.List(), Next: []Next{{Operation: "inbox_events.list"}}}), nil
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (Manifest, error) {
		items, err := m.contextOn(ctx, tx, tenantID(ctx), authn.PrincipalIDFromContext(ctx), in.ExecutionID)
		return Manifest{Operations: m.registry.List(), Next: []Next{{Operation: "inbox_events.list"}}, Knowledge: items}, err
	})
}

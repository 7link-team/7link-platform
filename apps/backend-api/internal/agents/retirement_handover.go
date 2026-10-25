package agents

import (
	"context"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// RetirementHandover is an Execution-owned command capability. A system
// artifact has no source execution; a model artifact names its actual producer.
type RetirementHandover struct {
	ReconcileOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
	PrepareOn   func(context.Context, *gorm.DB, guid.ID, guid.ID, ...guid.ID) error
	CancelOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error
	LaunchOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, error)
	ReadOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error)
	SystemOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (HandoverArtifact, error)
}

type HandoverArtifact struct {
	ID          guid.ID  `json:"id"`
	ExecutionID *guid.ID `json:"execution_id,omitempty"`
	Source      string   `json:"source" enum:"AGENT,SYSTEM"`
	Content     string   `json:"content"`
	SHA256      string   `json:"sha256"`
}

// The callback checks current Human authority against the real protocol row.
// Runner callers supply the recorded initiator, never a posted Human identity.
type RetirementHandoverConnector struct {
	Bind                func(RetirementHandover) error
	AuthorizeOn         func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	AuthorizeArtifactOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
}

func (m *Module) provideRetirementHandover(s *module.Services) error {
	return module.Provide(s, RetirementHandoverConnector{
		AuthorizeArtifactOn: func(ctx context.Context, tx *gorm.DB, tenant, agent, retirement guid.ID) error {
			r, err := m.retirementOn(ctx, tx, tenant, retirement, "agent_retire", false)
			if err != nil {
				return err
			}
			if r.AgentPrincipalID != agent {
				return ErrConflict
			}
			return m.checkRetirementSourceReadsOn(ctx, tx, r)
		},
		Bind: func(v RetirementHandover) error {
			if v.LaunchOn == nil || v.ReadOn == nil || v.SystemOn == nil || m.retirementHandover.LaunchOn != nil {
				return ErrConflict
			}
			m.retirementHandover = v
			return nil
		},
		AuthorizeOn: func(ctx context.Context, tx *gorm.DB, tenant, agent, retirement guid.ID) error {
			r, err := m.retirementOn(ctx, tx, tenant, retirement, "agent_retire", false)
			if err != nil {
				return err
			}
			if r.AgentPrincipalID != agent || r.State != "IN_PROGRESS" || r.Step != "HANDOVER" {
				return ErrConflict
			}
			return m.checkRetirementSourceReadsOn(ctx, tx, r)
		},
	})
}

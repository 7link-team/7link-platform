package knowledge

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"sync"
	"time"
)

// ExecutionKnowledgeView supplies only source-owned frozen context and verified
// output citations. Reading an execution never manufactures an injection log.
type FeedbackContextItem struct {
	Label            string     `json:"label"`
	Category         string     `json:"category"`
	Location         string     `json:"location"`
	Expected         string     `json:"expected"`
	Detail           string     `json:"detail"`
	SourceDecisionID guid.ID    `json:"source_decision_id"`
	ActorID          *guid.ID   `json:"actor_id,omitempty"`
	DecidedAt        *time.Time `json:"decided_at,omitempty"`
	ActorKind        string     `json:"actor_kind" enum:"HUMAN,SYSTEM"`
	Cited            bool       `json:"cited"`
}
type ExecutionKnowledgeSnapshot struct {
	ProjectID  guid.ID
	WorkItemID guid.ID
	Budget     int64
	Items      []ContextItem
	Feedback   []FeedbackContextItem
}
type FeedbackDecisionMetadata struct {
	ActorID   guid.ID
	CreatedAt time.Time
}
type ExecutionKnowledgeViewConnector struct {
	BindExecution func(func(context.Context, *gorm.DB, guid.ID, guid.ID) (ExecutionKnowledgeSnapshot, error)) error
	BindDecision  func(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (FeedbackDecisionMetadata, error)) error
}
type executionViewBinding struct {
	sync.RWMutex
	read     func(context.Context, *gorm.DB, guid.ID, guid.ID) (ExecutionKnowledgeSnapshot, error)
	decision func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (FeedbackDecisionMetadata, error)
}

func (m *Module) provideExecutionView(s *module.Services) error {
	return module.Provide[ExecutionKnowledgeViewConnector](s, ExecutionKnowledgeViewConnector{
		BindExecution: func(read func(context.Context, *gorm.DB, guid.ID, guid.ID) (ExecutionKnowledgeSnapshot, error)) error {
			m.view.Lock()
			defer m.view.Unlock()
			if read == nil {
				return ErrInvalid
			}
			if m.view.read != nil {
				return ErrConflict
			}
			m.view.read = read
			return nil
		},
		BindDecision: func(read func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (FeedbackDecisionMetadata, error)) error {
			m.view.Lock()
			defer m.view.Unlock()
			if read == nil {
				return ErrInvalid
			}
			if m.view.decision != nil {
				return ErrConflict
			}
			m.view.decision = read
			return nil
		},
	})
}
func (m *Module) executionKnowledgeViewOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (ExecutionKnowledgeSnapshot, error) {
	m.view.RLock()
	read, decision := m.view.read, m.view.decision
	m.view.RUnlock()
	if read == nil {
		return ExecutionKnowledgeSnapshot{}, nil
	}
	out, err := read(ctx, tx, tenant, id)
	if err != nil {
		return out, err
	}
	for i := range out.Feedback {
		if decision == nil {
			return out, ErrUnavailable
		}
		fact, e := decision(ctx, tx, tenant, out.ProjectID, out.WorkItemID, out.Feedback[i].SourceDecisionID)
		if e != nil {
			return out, e
		}
		out.Feedback[i].ActorKind = "SYSTEM"
		if fact.ActorID > 0 {
			out.Feedback[i].ActorID = &fact.ActorID
			out.Feedback[i].ActorKind = "HUMAN"
		}
		out.Feedback[i].DecidedAt = &fact.CreatedAt
	}
	return out, nil
}

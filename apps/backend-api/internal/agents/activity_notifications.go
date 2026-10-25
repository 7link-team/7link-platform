package agents

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// ActivityTransition is a runtime state change, not a domain event or audit fact.
type ActivityTransition struct {
	TenantID, AgentPrincipalID, ResponsiblePrincipalID guid.ID
	Name, Previous, Activity                           string
	SessionEpoch                                       int
	OccurredAt                                         time.Time
}

// ActivityObserver participates in the activity update's transaction.
type ActivityObserver func(context.Context, *gorm.DB, ActivityTransition) error

type ActivitySnapshot struct {
	TenantID, AgentPrincipalID, ResponsiblePrincipalID guid.ID
	Name, Activity                                     string
	SessionEpoch                                       int
}

// ActivityNotifications lets notification consumers observe transitions and
// recheck current activity and ownership before delivering delayed notices.
type ActivityNotifications struct {
	Bind       func(ActivityObserver) error
	SnapshotOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (ActivitySnapshot, bool, error)
}

func (m *Module) provideActivityNotifications(s *module.Services) error {
	return module.Provide[ActivityNotifications](s, ActivityNotifications{
		Bind: func(observer ActivityObserver) error {
			if observer == nil || m.activityObserver != nil {
				return ErrConflict
			}
			m.activityObserver = observer
			return nil
		},
		SnapshotOn: m.activitySnapshotOn,
	})
}

func (m *Module) activitySnapshotOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (ActivitySnapshot, bool, error) {
	var out ActivitySnapshot
	if tx == nil || tenant <= 0 || agent <= 0 {
		return out, false, ErrInvalid
	}
	if m.agents.ByPrincipalOn == nil {
		return out, false, ErrUnavailable
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil || !found {
		return out, false, err
	}
	if a.TenantID != tenant || a.PrincipalID != agent || a.State != "ACTIVE" {
		return out, false, nil
	}
	q := query.BindLiveState(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return ActivitySnapshot{TenantID: tenant, AgentPrincipalID: agent, ResponsiblePrincipalID: a.OwnerPrincipalID, Name: a.Name, Activity: r.Activity, SessionEpoch: r.SessionEpoch}, true, nil
}

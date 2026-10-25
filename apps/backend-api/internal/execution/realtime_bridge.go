package execution

import (
	"context"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/pkg/module"
)

// Domain owners keep their own read policy; the capability bridge translates
// topic names without granting generic permissions or reading foreign tables.
type realtimeTopicsAdapter struct{ enabled bool }

func (*realtimeTopicsAdapter) ModuleName() string { return "realtime-topics" }
func (m *realtimeTopicsAdapter) Enabled() bool    { return m.enabled }
func (*realtimeTopicsAdapter) ModuleDependencies() []string {
	return []string{delivery.Name, fleet.Name, collab.Name, Name, realtime.Name}
}
func (*realtimeTopicsAdapter) InjectServices(s *module.Services) error {
	topics, err := module.Resolve[realtime.TopicAuthorizers](s)
	if err != nil {
		return err
	}
	projects, err := module.Resolve[delivery.RealtimeTopics](s)
	if err != nil {
		return err
	}
	machines, err := module.Resolve[fleet.RealtimeTopics](s)
	if err != nil {
		return err
	}
	conversations, err := module.Resolve[collab.RealtimeTopics](s)
	if err != nil {
		return err
	}
	executions, err := module.Resolve[RealtimeTopics](s)
	if err != nil {
		return err
	}
	for _, registration := range []struct {
		kind      string
		authorize func(context.Context, guid.ID, guid.ID, guid.ID, bool) (bool, error)
	}{
		{"project", projects.Authorize}, {"projects", projects.Authorize}, {"work_item", projects.AuthorizeWork}, {"machine", machines.Authorize}, {"machines", machines.Authorize}, {"channel", conversations.AuthorizeChannel}, {"thread", conversations.AuthorizeThread}, {"execution", executions.Authorize},
	} {
		if registration.authorize == nil {
			return realtime.ErrUnavailable
		}
		fn := registration.authorize
		kind := registration.kind
		if err := topics.Register(kind, func(ctx context.Context, tenant, principal guid.ID, topic realtime.Topic) (bool, error) {
			collection := kind == "projects" || kind == "machines"
			if topic.Collection != collection {
				return false, nil
			}
			return fn(ctx, tenant, principal, topic.ID, collection)
		}); err != nil {
			return err
		}
	}
	return nil
}

var (
	_ module.Enabler         = (*realtimeTopicsAdapter)(nil)
	_ module.Depender        = (*realtimeTopicsAdapter)(nil)
	_ module.ServiceInjector = (*realtimeTopicsAdapter)(nil)
)

// NewRealtimeBridge registers the existing capability adapter.
func NewRealtimeBridge(enabled bool) module.Module { return &realtimeTopicsAdapter{enabled: enabled} }

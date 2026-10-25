package execution

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type verificationAutomationCatalog struct {
	subscriber events.Subscriber
	err        error
}

func (c *verificationAutomationCatalog) AddSubscribers(owner string, ss ...events.Subscriber) error {
	if len(ss) != 1 || owner != delivery.Name {
		return errors.New("unexpected subscriber registration")
	}
	c.subscriber = ss[0]
	return c.err
}

func TestVerificationAutomationUsesCommittedSourceScope(t *testing.T) {
	tx := &gorm.DB{}
	ctx := t.Context()
	var queued, collected, reads int
	var providerErr error
	state := "SUCCEEDED"
	s := module.NewServices()
	catalog := &verificationAutomationCatalog{}
	require.NoError(t, module.Provide[events.SubscriberCatalog](s, catalog))
	require.NoError(t, module.Provide[VerificationSourceFacts](s, VerificationSourceFacts{ReadOn: func(c context.Context, db *gorm.DB, tenant, project, id guid.ID) (VerificationSourceFact, error) {
		require.Same(t, tx, db)
		require.Equal(t, ctx, c)
		require.Equal(t, []guid.ID{7, 9, 12}, []guid.ID{tenant, project, id})
		reads++
		return VerificationSourceFact{WorkItemID: 3, State: state}, providerErr
	}}))
	check := func(c context.Context, db *gorm.DB, tenant, project, work, result guid.ID) error {
		require.Same(t, tx, db)
		require.Equal(t, ctx, c)
		require.Equal(t, []guid.ID{7, 9, 3, 12}, []guid.ID{tenant, project, work, result})
		return nil
	}
	require.NoError(t, module.Provide[delivery.VerificationAutomation](s, delivery.VerificationAutomation{
		QueueOn: func(c context.Context, db *gorm.DB, a, b, d, e guid.ID) error {
			queued++
			return check(c, db, a, b, d, e)
		},
		CollectOn: func(c context.Context, db *gorm.DB, a, b, d, e guid.ID) error {
			collected++
			return check(c, db, a, b, d, e)
		},
	}))
	require.NoError(t, s.Seal())
	a := &verificationAutomationAdapter{enabled: true}
	require.True(t, a.Enabled())
	require.Equal(t, "verification_automation_bridge", a.ModuleName())
	require.ElementsMatch(t, []string{delivery.Name, Name, events.Name}, a.ModuleDependencies())
	require.NoError(t, a.InjectServices(s))
	require.Equal(t, events.Transactional, catalog.subscriber.Mode)
	require.Equal(t, events.Earliest, catalog.subscriber.StartFrom)
	e := events.Envelope{TenantID: 7, ProjectID: 9, Subject: events.Subject{Kind: "execution", ID: 12}, EventType: "execution.state_changed", Data: []byte(`{"state":"RUNNING"}`)}
	require.NoError(t, catalog.subscriber.Handle(ctx, tx, e))
	require.Zero(t, reads)
	e.Data = []byte(`{"state":"SUCCEEDED","work_id":"fabricated"}`)
	require.NoError(t, catalog.subscriber.Handle(ctx, tx, e))
	require.Equal(t, 1, queued)
	e.EventType = "execution.verification_completed"
	e.Data = []byte(`{"verdict":"PASSED","result_id":"fabricated"}`)
	require.NoError(t, catalog.subscriber.Handle(ctx, tx, e))
	require.Equal(t, 1, collected)
	providerErr = ErrNotFound
	require.ErrorIs(t, catalog.subscriber.Handle(ctx, tx, e), ErrNotFound)
	providerErr = nil
	state = "RUNNING"
	require.ErrorIs(t, catalog.subscriber.Handle(ctx, tx, e), ErrConflict)
	e.Subject.Kind = "work_item"
	require.ErrorIs(t, catalog.subscriber.Handle(ctx, tx, e), ErrInvalidArgument)
	e.Subject.Kind = "execution"
	e.EventType = "execution.state_changed"
	e.Data = []byte(`broken`)
	require.Error(t, catalog.subscriber.Handle(ctx, tx, e))
	require.Equal(t, 1, queued)
	require.Equal(t, 1, collected)
}

func TestVerificationAutomationInjectionRejectsMissingDependencies(t *testing.T) {
	s := module.NewServices()
	require.Error(t, (&verificationAutomationAdapter{}).InjectServices(s))
	require.NoError(t, module.Provide[VerificationSourceFacts](s, VerificationSourceFacts{}))
	require.Error(t, (&verificationAutomationAdapter{}).InjectServices(s))
	require.NoError(t, module.Provide[delivery.VerificationAutomation](s, delivery.VerificationAutomation{}))
	require.Error(t, (&verificationAutomationAdapter{}).InjectServices(s))
	require.NoError(t, module.Provide[events.SubscriberCatalog](s, &verificationAutomationCatalog{}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, (&verificationAutomationAdapter{}).InjectServices(s), delivery.ErrUnavailable)
}

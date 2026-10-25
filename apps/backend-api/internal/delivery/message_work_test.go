package delivery

import (
	"7link.com/platform/internal/collab"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestMessageCreationUsesAuthorizedDomainCommand(t *testing.T) {
	m, db, ctx, in := workHarness(t)
	request := collab.ConvertWorkInput{Title: in.Title, Objective: in.Objective, AccountablePrincipalID: in.AccountablePrincipalID}
	for _, c := range in.Criteria {
		request.Criteria = append(request.Criteria, collab.WorkCriterion{Text: c.Text, VerificationMethod: c.VerificationMethod})
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		id, e := m.createMessageWorkOn(ctx, tx, 1, 99, request, "source markdown")
		require.NoError(t, e)
		require.Positive(t, id)
		require.NoError(t, m.replayMessageWorkOn(ctx, tx, 1, 99, id))
		return nil
	}))
	m.workPolicy = nil
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createMessageWorkOn(ctx, tx, 1, 99, request, "body"); return e }))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.replayMessageWorkOn(ctx, tx, 1, 99, 1) }))
	s := module.NewServices()
	require.NoError(t, s.Seal())
	require.Error(t, m.injectWork(s))
	var create collab.MessageWorkCreator
	s = module.NewServices()
	require.NoError(t, module.Provide[collab.MessageWorkConnector](s, collab.MessageWorkConnector{Bind: func(c collab.MessageWorkCreator, _ collab.MessageWorkReplay) error { create = c; return nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.injectWork(s))
	require.NotNil(t, create)
	for _, e := range []error{ErrWorkForbidden, ErrInvalidMember, ErrProjectNotFound, ErrWorkProjectReadOnly, ErrInvalidArgument} {
		require.Error(t, messageWorkError(e))
	}
	require.NoError(t, messageWorkError(nil))
}

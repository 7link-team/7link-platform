package clientwork

import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestClarificationReminderFactsOnlyCurrentClientAndOpenUnexpiredRound(t *testing.T) {
	m, db, opened, at := timedClarification(t)
	require.NoError(t, m.advanceClarifications(testContext(t, 2), 1, at.Add(48*time.Hour)))
	read := func() bool {
		var found bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			fact, yes, err := m.clarificationReminderFactsOn(t.Context(), tx, 1, opened.ID)
			found = yes
			if yes {
				require.EqualValues(t, 4, fact.RecipientID)
				require.Equal(t, 1, fact.Ordinal)
			}
			return err
		}))
		return found
	}
	require.True(t, read())
	require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", opened.ID).Update("due_at", at.Add(-time.Hour)).Error)
	require.False(t, read())
	_, _, err := m.clarificationReminderFactsOn(t.Context(), db, 1, opened.ID)
	require.Error(t, err)
}

func TestClarificationReminderSourceFailuresNeverPublishRecipientFacts(t *testing.T) {
	for _, scenario := range []string{"query", "missing_clients", "client_error", "revoked_client", "missing_round", "foreign_tenant"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, opened, at := timedClarification(t)
			require.NoError(t, m.advanceClarifications(testContext(t, 2), 1, at.Add(48*time.Hour)))
			fault := errors.New("reminder source failed")
			want := fault
			tenant := guid.ID(1)
			id := opened.ID
			switch scenario {
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:reminder-facts", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ClarificationRound.String() {
						tx.AddError(fault)
					}
				}))
			case "missing_clients":
				m.clients = nil
				want = ErrUnavailable
			case "client_error":
				m.clients.(*testClients).clientErr = fault
			case "revoked_client":
				m.clients.(*testClients).revoked = true
				want = nil
			case "missing_round":
				id++
				want = nil
			case "foreign_tenant":
				tenant = 2
				want = nil
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				fact, found, err := m.clarificationReminderFactsOn(context.Background(), tx, tenant, id)
				require.ErrorIs(t, err, want)
				require.False(t, found)
				require.Zero(t, fact.RecipientID)
				require.Zero(t, fact.ProjectID)
				return nil
			}))
		})
	}
}

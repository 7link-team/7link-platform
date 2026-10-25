package yunxiao

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorklogCurrentSourceFailuresAndStorageFailuresLeaveNoFacts(t *testing.T) {
	for _, fault := range []string{"lock", "human source", "inactive", "missing source", "invalid date", "missing row", "insert", "audit", "missing publisher"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			in, id := workInput(), guid.ID(0)
			switch fault {
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "human source", "inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					if fault == "human source" {
						return identity.Principal{}, false, ErrUnavailable
					}
					return identity.Principal{}, false, nil
				}
			case "missing source":
				m.projects.AuthorizeOn = nil
			case "invalid date":
				in.WorkDate = "2026-02-30"
			case "missing row":
				id = 999
			case "insert":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_worklog BEFORE INSERT ON "+m.names.Worklogs.String()+" BEGIN SELECT RAISE(ABORT,'worklog insert failed'); END").Error)
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
			case "missing publisher":
				m.events = nil
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.saveWorklogOn(ctx, tx, 1, 99, id, 1, in); return err }))
			q := query.BindWorklogs(db, m.names)
			count, err := q.WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestRuntimeConsumerRequiresActualTerminalReceiptSource(t *testing.T) {
	m, db, ctx := workHarness(t)
	for _, scenario := range []string{"malformed", "running", "no source", "source failure", "not found"} {
		event := events.Envelope{TenantID: 1, ProjectID: 99, Subject: events.Subject{ID: 200}, Data: []byte(`{"state":"SUCCEEDED"}`)}
		switch scenario {
		case "malformed":
			event.Data = []byte("invalid")
		case "running":
			event.Data = []byte(`{"state":"RUNNING"}`)
		case "no source":
			m.runtimeFacts = nil
		case "source failure":
			m.runtimeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (RuntimeFact, bool, error) {
				return RuntimeFact{}, false, ErrUnavailable
			}
		case "not found":
			m.runtimeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (RuntimeFact, bool, error) {
				return RuntimeFact{}, false, nil
			}
		}
		err := db.Transaction(func(tx *gorm.DB) error { return m.consumeRuntime(ctx, tx, event) })
		if scenario == "running" {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	q := query.BindWorklogs(db, m.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

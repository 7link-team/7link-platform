package intake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type triageRequestPublisher struct {
	events []events.Event
	fail   bool
}

func TestTriageRequestStorageAndIdentityFailuresRollbackAcceptedTimestamp(t *testing.T) {
	for _, stage := range []string{"missing_coordinator", "first_read", "locked_read", "channel_changed", "update", "cas", "missing_audit", "missing_human_lookup", "human_lookup"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, _, _ := changeSourceHarness(t)
			claims, _ := authn.FromContext(ctx)
			copyClaims := *claims
			copyClaims.EntityID = 20
			ctx = authn.WithClaims(ctx, &copyClaims)
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("status", "RECEIVED").Error)
			failure := errors.New("triage dependency failed")
			m.triageRequestAuthority = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }
			m.triageRequestCoordinator = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, TriageRequestAuthority) error {
				return nil
			}
			want := failure
			switch stage {
			case "missing_coordinator":
				m.triageRequestCoordinator = nil
				want = ErrUnavailable
			case "first_read", "locked_read":
				reads := 0
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_read", func(tx *gorm.DB) {
					reads++
					if stage == "first_read" || reads == 2 {
						tx.AddError(failure)
					}
				}))
			case "channel_changed":
				m.triageRequestCoordinator = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, _ string, _ TriageRequestAuthority) error {
					return tx.Table(m.names.Records.String()).Where("id=10").Update("channel_id", 999).Error
				}
				want = ErrConflict
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("regression_update", func(tx *gorm.DB) { tx.AddError(failure) }))
			case "cas":
				require.NoError(t, db.Callback().Update().After("gorm:update").Register("regression_update", func(tx *gorm.DB) { tx.RowsAffected = 0 }))
				want = ErrConflict
			case "missing_audit":
				m.brief.audit = nil
				want = ErrUnavailable
			case "missing_human_lookup":
				m.brief.principals.ActiveHumanByID = nil
				want = ErrUnavailable
			case "human_lookup":
				m.brief.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.requestTriageOn(ctx, tx, 7, 20, 10, "Please reassess")
				return err
			})
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Query().Remove("regression_read"))
			require.NoError(t, db.Callback().Update().Remove("regression_update"))
			var row model.Record
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&row).Error)
			require.Nil(t, row.LastTriageRequestedAt)
			require.Equal(t, 1, row.Version)
			require.EqualValues(t, 3, row.ChannelID, "failed coordinator changes roll back too")
			var count int64
			require.NoError(t, db.Table("brief_probe").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func (p *triageRequestPublisher) Publish(_ context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
	p.events = append(p.events, event)
	if err := tx.Exec("INSERT INTO brief_probe VALUES ('event')").Error; err != nil {
		return 0, err
	}
	if p.fail {
		return 0, errors.New("outbox failed")
	}
	return 501, nil
}

func TestTriageRequestDurableCoalescingAuthorityAndAtomicity(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	claims, _ := authn.FromContext(ctx)
	copyClaims := *claims
	copyClaims.EntityID = 20
	ctx = authn.WithClaims(ctx, &copyClaims)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"status": "TRIAGED", "kind": "NEW", "raw_content": "immutable", "last_team_activity_at": time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}).Error)
	allowed, configured := true, true
	checks := 0
	m.triageRequestAuthority = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string) error {
		checks++
		require.Equal(t, "intake_triage", permission)
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 20, project)
		if !allowed {
			return ErrForbidden
		}
		return nil
	}
	m.triageAgent = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, bool, error) {
		return 77, configured, nil
	}
	m.triageRequestCoordinator = func(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, kind string, check TriageRequestAuthority) error {
		if err := check(ctx, tx, tenant, project, "intake_triage"); err != nil {
			return err
		}
		_, found, err := m.triageAgent(ctx, tx, tenant, project, channel, kind)
		if err != nil {
			return err
		}
		if !found {
			return ErrInvalidArgument
		}
		return nil
	}
	pub := &triageRequestPublisher{}
	m.brief.events = pub
	run := func(note string) (TriageRequest, error) {
		var out TriageRequest
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.requestTriageOn(ctx, tx, 7, 20, 10, note)
			return err
		})
		return out, err
	}
	first, err := run("Review the new information")
	require.NoError(t, err)
	require.False(t, first.Coalesced)
	require.Equal(t, 2, first.Version)
	require.Len(t, pub.events, 1)
	require.Equal(t, "intake.triage_requested", pub.events[0].Type)
	require.Equal(t, "IAM:intake_triage", pub.events[0].Actor.AuthoritySource)
	// Reconstruct the module to prove coalescing is storage-backed.
	restarted := *m
	m = &restarted
	second, err := run("another note")
	require.NoError(t, err)
	require.True(t, second.Coalesced)
	require.True(t, first.RequestedAt.Equal(second.RequestedAt))
	require.Equal(t, first.Version, second.Version)
	require.Len(t, pub.events, 1)
	allowed = false
	_, err = run("")
	require.ErrorIs(t, err, ErrForbidden)
	allowed = true
	configured = false
	_, err = run("")
	require.ErrorIs(t, err, ErrInvalidArgument)
	configured = true
	old := first.RequestedAt.Add(-10 * time.Minute)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("last_triage_requested_at", old).Error)
	pub.fail = true
	_, err = run("retry")
	require.Error(t, err)
	var row model.Record
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&row).Error)
	require.Equal(t, 2, row.Version)
	require.True(t, old.Equal(*row.LastTriageRequestedAt))
	require.Equal(t, "immutable", *row.RawContent)
	require.Equal(t, 2025, row.LastTeamActivityAt.Year())
	var count int64
	require.NoError(t, db.Table("brief_probe").Count(&count).Error)
	require.EqualValues(t, 2, count, "audit and outbox must both roll back")
	pub.fail = false
	third, err := run("retry")
	require.NoError(t, err)
	require.False(t, third.Coalesced)
	require.Equal(t, 3, third.Version)
	for _, state := range []string{"CONVERTED", "MERGED", "DECLINED"} {
		// Terminal fixtures must respect existing canonical constraints.
		fields := map[string]any{"status": state, "merged_into_intake_id": 99, "decline_reason_code": "OTHER", "decline_reason": "closed"}
		require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(fields).Error)
		_, err = run("")
		require.ErrorIs(t, err, ErrConflict)
	}
	_, err = run(strings.Repeat("x", 2001))
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.Positive(t, checks)
	// Audit failure also leaves no accepted timestamp or version increment.
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"status": "RECEIVED", "last_triage_requested_at": nil}).Error)
	m.brief.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failed") }
	_, err = run("")
	require.Error(t, err)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&row).Error)
	// GORM keeps a previously populated pointer when scanning NULL; use a fresh row.
	var fresh model.Record
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&fresh).Error)
	require.Nil(t, fresh.LastTriageRequestedAt)
	require.Equal(t, 3, fresh.Version)
}

package delivery

import (
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestReplyRoutingLocksUniqueCurrentPrincipalsBeforeProject(t *testing.T) {
	for _, fault := range []string{"ok", "identity", "inactive", "member", "revoked", "missing", "project"} {
		t.Run(fault, func(t *testing.T) {
			h := newResultFixture(t)
			injected := errors.New("reply routing source unavailable")
			locked := []guid.ID{}
			want := error(nil)
			h.m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				locked = append(locked, id)
				if fault == "identity" {
					return identity.Principal{}, false, injected
				}
				return identity.Principal{ID: id}, fault != "inactive", nil
			}
			h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
				if fault == "member" {
					return false, injected
				}
				return fault != "revoked", nil
			})
			switch fault {
			case "identity", "member":
				want = injected
			case "inactive", "revoked":
				want = ErrWorkForbidden
			case "missing":
				h.m.principals.LockActiveRow = nil
				want = ErrUnavailable
			case "project":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("reply-source-failure", func(tx *gorm.DB) { tx.AddError(injected) }))
				want = injected
			}
			e := h.db.Transaction(func(tx *gorm.DB) error { return h.m.intakeReplyRouteFenceOn(h.ctx, tx, 1, 99, []guid.ID{12, 11, 12}) })
			require.ErrorIs(t, e, want)
			if fault == "ok" {
				require.Equal(t, []guid.ID{11, 12}, locked)
			}
		})
	}
}

func TestContactSourceFailuresNeverGrantCommentOrDeliveryAudience(t *testing.T) {
	for _, kind := range []string{"comment", "delivery"} {
		for _, fault := range []string{"source", "storage", "missing", "too_many"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				m, db, ctx := projectModeHarness(t)
				injected := errors.New("contact source unavailable")
				want := injected
				m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
					return nil, injected
				}
				switch fault {
				case "storage":
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("contact-source-failure", func(tx *gorm.DB) {
						if tx.Statement.Table == m.service.names.Contacts.String() {
							tx.AddError(injected)
						}
					}))
				case "missing":
					m.sources.ContactsOn = nil
					want = ErrUnavailable
				case "too_many":
					want = ErrUnavailable
					for i := 0; i < 21; i++ {
						require.NoError(t, db.Table(m.service.names.Contacts.String()).Create(map[string]any{"tenant_id": 1, "project_id": 99, "intake_contact_id": 400 + i, "role": "CC", "receives_deliveries": true, "can_accept": false, "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()}).Error)
					}
				}
				e := db.Transaction(func(tx *gorm.DB) error {
					if kind == "comment" {
						_, e := m.clientCommentContactOn(ctx, tx, 1, 99, 11)
						return e
					}
					return m.packageDeliveryRecipientOn(ctx, tx, 1, 99, 41)
				})
				require.ErrorIs(t, e, want)
			})
		}
	}
}

func TestAgentMergeAuthorityRequiresCurrentIndependentAutonomousDelegation(t *testing.T) {
	for _, scenario := range []string{"success", "hybrid", "self", "revoked", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, card := delegatedNewHarness(t)
			m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, req governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				require.Equal(t, "MERGE", req.Facts.IntakeConvertMode)
				require.Equal(t, "12", req.Facts.TriageCardAuthorID)
				if scenario == "revoked" {
					return governance.AuthorityDecision{}, nil
				}
				return governance.AuthorityDecision{Allowed: true, AuthoritySource: "DELEGATION:500", DelegationID: 500}, nil
			}
			switch scenario {
			case "hybrid":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("team_mode", "HYBRID").Error)
			case "self":
				card.ActorID = 11
			case "closed":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("stage", "CLOSED").Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				authority, err := m.agentMergeAuthorityOn(ctx, tx, guid.ID(1), guid.ID(99), card)
				if scenario == "success" {
					require.Equal(t, "DELEGATION:500", authority)
				}
				return err
			})
			if scenario == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestClientCommentContactDoesNotRequireSignerAndRechecksCurrentSource(t *testing.T) {
	f := newResultFixture(t)
	principal := guid.ID(11)
	require.NoError(t, f.db.Table(f.m.service.names.Contacts.String()).Create(map[string]any{"tenant_id": 1, "project_id": 99, "intake_contact_id": 401, "role": "CC", "receives_deliveries": true, "can_accept": false, "created_at": time.Now(), "updated_at": time.Now()}).Error)
	contact := intake.ContactReference{ID: 401, TenantID: 1, PrincipalID: &principal, State: "ACTIVE"}
	f.m.sources.ContactsOn = func(_ context.Context, _ *gorm.DB, tenant guid.ID, ids []guid.ID) ([]intake.ContactReference, error) {
		require.EqualValues(t, 1, tenant)
		require.Equal(t, []guid.ID{401}, ids)
		return []intake.ContactReference{contact}, nil
	}
	check := func(want bool) {
		t.Helper()
		err := f.db.Transaction(func(tx *gorm.DB) error {
			id, err := f.m.clientCommentContactOn(f.ctx, tx, 1, 99, 11)
			if want {
				require.EqualValues(t, 401, id)
			}
			return err
		})
		if want {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, ErrWorkForbidden)
		}
	}
	check(true)
	contact.State = "DISABLED"
	check(false)
	contact.State = "ACTIVE"
	at := time.Now()
	contact.ArchivedAt = &at
	check(false)
	contact.ArchivedAt = nil
	other := guid.ID(12)
	contact.PrincipalID = &other
	check(false)
	_, err := f.m.clientCommentContactOn(f.ctx, f.db, 1, 99, 11)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, f.db.Table(f.m.service.names.Contacts.String()).Where("tenant_id=1 AND project_id=99 AND intake_contact_id=401").Delete(map[string]any{}).Error)
	f.m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
		return nil, nil
	}
	check(false)
}

func TestVerifierPolicyUsesCurrentLockedWorkClassification(t *testing.T) {
	f := newResultFixture(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		kind, risk, err := f.m.verifierWorkPolicyOn(t.Context(), tx, 1, 99, f.work)
		require.NoError(t, err)
		require.NotEmpty(t, kind)
		require.Contains(t, []string{"LOW", "MEDIUM", "HIGH"}, risk)
		_, _, err = f.m.verifierWorkPolicyOn(t.Context(), tx, 2, 99, f.work)
		require.Error(t, err)
		_, _, err = f.m.verifierWorkPolicyOn(t.Context(), tx, 1, 100, f.work)
		require.Error(t, err)
		return nil
	}))
	_, _, err := f.m.verifierWorkPolicyOn(t.Context(), f.db, 1, 99, f.work)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestPackageDeliveryRecipientRequiresConfiguredCurrentDeliveryContact(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	recipient := guid.ID(41)
	m.sources.ContactsOn = func(_ context.Context, _ *gorm.DB, tenant guid.ID, ids []guid.ID) ([]intake.ContactReference, error) {
		return []intake.ContactReference{{ID: 42, TenantID: tenant, PrincipalID: &recipient, State: "ACTIVE"}}, nil
	}
	require.NoError(t, db.Table(m.service.names.Contacts.String()).Create(map[string]any{"tenant_id": 1, "project_id": 99, "intake_contact_id": 42, "role": "CC", "receives_deliveries": true, "can_accept": false, "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.packageDeliveryRecipientOn(ctx, tx, 1, 99, 41) }))
	require.NoError(t, db.Table(m.service.names.Contacts.String()).Where("tenant_id=1 AND project_id=99").Update("receives_deliveries", false).Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.packageDeliveryRecipientOn(ctx, tx, 1, 99, 41) }))
}

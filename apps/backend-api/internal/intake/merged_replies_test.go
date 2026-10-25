package intake

import (
	"7link.com/platform/internal/iam/audit"
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMergedReplyRouteRequiresOriginalRecipientAndActiveTarget(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("principal_id", 2).Error)
	var first model.Record
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Take(&first).Error)
	target := first
	target.ID, target.RefNo, target.ReplyToken = 11, 2, "reply-target-11"
	target.RawContentHash = strings.Repeat("b", 64)
	require.NoError(t, db.Table(m.names.Records.String()).Create(&target).Error)
	read := func(source, recipient guid.ID) (MergedReplyRoute, error) {
		var out MergedReplyRoute
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.mergedReplyRouteOn(ctx, tx, 7, 20, source, recipient)
			return err
		})
		return out, err
	}
	_, err := read(10, 0)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = read(10, 2)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"status": "MERGED", "merged_into_intake_id": 11}).Error)
	out, err := read(10, 2)
	require.NoError(t, err)
	require.EqualValues(t, 10, out.SourceID)
	require.EqualValues(t, 11, out.TargetID)
	require.EqualValues(t, 2, out.TargetRecipientID)
	require.Equal(t, first.RawContentHash, out.SourceHash)
	require.Equal(t, target.RawContentHash, out.TargetHash)
	_, err = read(10, 3)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("state", "BLOCKED").Error)
	_, err = read(10, 2)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Update("state", "ACTIVE").Error)
	// A different active target contact without a linked principal cannot route.
	var contact model.Contact
	require.NoError(t, db.Table(m.names.Contacts.String()).Where("id=1").Take(&contact).Error)
	contact.ID, contact.PrincipalID = 9, nil
	require.NoError(t, db.Table(m.names.Contacts.String()).Create(&contact).Error)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=11").Update("contact_id", 9).Error)
	_, err = read(10, 2)
	require.ErrorIs(t, err, ErrConflict)
}

func TestDispositionActorRetainsExplicitDelegatedAuthority(t *testing.T) {
	kind, authority := DispositionActor(t.Context())
	require.Equal(t, "HUMAN", kind)
	require.Empty(t, authority)
	ctx := context.WithValue(t.Context(), dispositionGrantKey{}, dispositionGrant{authority: "DELEGATION:1"})
	kind, authority = DispositionActor(ctx)
	require.Equal(t, "AGENT", kind)
	require.Equal(t, "DELEGATION:1", authority)
}

func TestAgentMergeUsesFrozenIndependentCardAndCanonicalDisposition(t *testing.T) {
	for _, scenario := range []string{"success", "self", "revoked", "stale", "decline", "audit", "outbox", "round_failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, human, _, original := changeSourceHarness(t)
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"kind": "NEW", "status": "RECEIVED", "requirement_id": nil}).Error)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.triageSourceOn(human, tx, 7, 20, 10, 1, triageDraft())
				return err
			}))
			var source model.Record
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").First(&source).Error)
			target := source
			target.ID = 11
			target.RefNo = 2
			target.ReplyToken = "target-11"
			require.NoError(t, db.Table(m.names.Records.String()).Create(&target).Error)
			pub := &dispositionPublisher{briefTestPublisher: original}
			m.brief.events = pub
			ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, EntityID: 20, PrincipalID: 3, SubjectType: authn.SubjectTypePrincipal})
			m.mergeAuthority = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, card TriageSource) (string, error) {
				require.Equal(t, guid.ID(7), tenant)
				require.Equal(t, guid.ID(20), project)
				require.Equal(t, guid.ID(10), card.IntakeID)
				require.Equal(t, guid.ID(2), card.ActorID)
				if scenario == "revoked" {
					return "", ErrForbidden
				}
				return "DELEGATION:500", nil
			}
			if scenario == "self" {
				ctx = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, EntityID: 20, PrincipalID: 2, SubjectType: authn.SubjectTypePrincipal})
			}
			if scenario == "audit" {
				m.brief.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			if scenario == "outbox" {
				original.fail = true
			}
			if scenario == "round_failure" {
				require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"status": "CLARIFYING", "open_round_id": 999}).Error)
				m.dispositionRoundCloser = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, string, string) error {
					return errors.New("round unavailable")
				}
			}
			in := SourceDisposition{Action: "MERGE", TargetID: 11, Reason: "Independent duplicate decision"}
			version := 2
			if scenario == "stale" {
				version = 1
			}
			if scenario == "decline" {
				in.Action = "DECLINE"
				in.ReasonCode = "OTHER"
				in.TargetID = 0
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.agentMergeOn(ctx, tx, 7, 20, 10, version, in); return err })
			var stored model.Record
			require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").First(&stored).Error)
			if scenario == "success" {
				require.NoError(t, err)
				require.Equal(t, "MERGED", stored.Status)
				require.Equal(t, "AGENT", *stored.ResolvedActorKind)
				require.Equal(t, "DELEGATION:500", *stored.ResolvedAuthoritySource)
				require.Equal(t, "AGENT", pub.events[len(pub.events)-1].Actor.Kind)
				require.Equal(t, "NOT_REQUESTED", stored.ClientNotificationState)
				require.Equal(t, source.RawContentHash, stored.RawContentHash)
			} else {
				require.Error(t, err)
				require.NotEqual(t, "MERGED", stored.Status)
				require.Equal(t, 2, stored.Version)
				require.Nil(t, stored.ResolvedActorKind)
			}
		})
	}
}

func TestAgentMergeConnectorBindsOnceAndCannotBroadenSourceReads(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideMergedReplies(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[AgentMergeConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, c.Bind(nil), ErrInvalidArgument)
	check := func(context.Context, *gorm.DB, guid.ID, guid.ID, TriageSource) (string, error) {
		return "DELEGATION:1", nil
	}
	require.NoError(t, c.Bind(check))
	require.ErrorIs(t, c.Bind(check), ErrInvalidArgument)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 3})
	tx := &gorm.DB{}
	ctx = context.WithValue(ctx, dispositionGrantKey{}, dispositionGrant{tx: tx, tenant: 7, project: 20, source: 10, actor: 3, authority: "DELEGATION:1"})
	require.NoError(t, m.dispositionSourceReadAuthorityOn(ctx, tx, 7, 20, 10))
	for _, scope := range [][3]guid.ID{{8, 20, 10}, {7, 21, 10}, {7, 20, 11}} {
		require.ErrorIs(t, m.dispositionSourceReadAuthorityOn(ctx, tx, scope[0], scope[1], scope[2]), ErrForbidden)
	}
	_, _, err = m.dispositionAuthorityOn(ctx, tx, 7, 20, 10, "DECLINE")
	require.ErrorIs(t, err, ErrForbidden)
}

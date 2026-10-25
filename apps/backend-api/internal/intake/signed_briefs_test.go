package intake

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

type briefTestIDs struct{ next int64 }

func (s *briefTestIDs) Next() (int64, error) { s.next++; return s.next, nil }

type briefTestPublisher struct{ fail bool }

func (p *briefTestPublisher) Publish(_ context.Context, tx *gorm.DB, _ events.Event) (guid.ID, error) {
	if p.fail {
		return 0, errors.New("outbox down")
	}
	return 1, tx.Exec("INSERT INTO brief_probe VALUES ('event')").Error
}
func TestSignedBriefPersistsImmutableSourceAndReauthorizes(t *testing.T) {
	m, db := referencesHarness(t)
	require.NoError(t, db.Exec("CREATE TABLE brief_probe(kind TEXT)").Error)
	now := time.Now().UTC()
	require.NoError(t, db.Table(m.names.Records.String()).Create(map[string]any{"id": 10, "tenant_id": 7, "ref_no": 10, "channel_id": 3, "channel_kind": "SHARED_CHANNEL", "contact_id": 1, "title": "Source", "raw_content_hash": strings.Repeat("a", 64), "received_at": now, "reply_token": "token-10", "status": "CONVERTED", "project_id": 20, "requirement_id": 100, "created_at": now, "updated_at": now}).Error)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 2, SubjectType: authn.SubjectTypePrincipal})
	allowed := true
	pub := &briefTestPublisher{}
	m.brief = signedBriefServices{ids: &briefTestIDs{next: 900}, principals: identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }, ActiveHumanByID: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}}, explain: func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, "brief_sign", r.Permission)
		return authz.Explanation{Allowed: allowed}, nil
	}, audit: func(_ context.Context, tx *gorm.DB, _ audit.DomainEvent) error {
		return tx.Exec("INSERT INTO brief_probe VALUES ('audit')").Error
	}, events: pub}
	in := SignedBriefInput{TenantID: 7, ProjectID: 20, BindingID: 30, IntakeID: 10, Items: []BriefItem{{ID: "one", Text: "Signed scope"}}}
	sign := func() (SignedBriefFact, error) {
		var out SignedBriefFact
		e := db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.signBriefOn(ctx, tx, in); return e })
		return out, e
	}
	first, e := sign()
	require.NoError(t, e)
	require.Len(t, first.Hash, 64)
	require.Equal(t, guid.ID(2), first.SignedBy)
	again, e := sign()
	require.NoError(t, e)
	require.Equal(t, first, again)
	allowed = false
	_, e = sign()
	require.ErrorIs(t, e, ErrForbidden)
	allowed = true
	in.Items[0].Text = "Changed scope"
	_, e = sign()
	require.ErrorIs(t, e, ErrConflict)
	in.BindingID = 31
	pub.fail = true
	_, e = sign()
	require.Error(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, e := m.readSignedBriefOn(ctx, tx, 7, 20, 31)
		require.False(t, found)
		return e
	}))
	var n int64
	require.NoError(t, db.Table("brief_probe").Count(&n).Error)
	require.EqualValues(t, 2, n)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, e := m.readSignedBriefOn(ctx, tx, 7, 21, 30)
		require.False(t, found)
		return e
	}))
}

func (*briefTestPublisher) AddTypes(string, ...events.TypeSpec) error { return nil }
func TestBriefServiceInjectionRequiresAllSources(t *testing.T) {
	m, _ := referencesHarness(t)
	pub := &briefTestPublisher{}
	providers := []func(*module.Services) error{
		func(s *module.Services) error { return module.Provide[guid.Source](s, &briefTestIDs{next: 900}) },
		func(s *module.Services) error {
			return module.Provide[identity.PrincipalStateStore](s, identity.PrincipalStateStore{})
		},
		func(s *module.Services) error {
			return module.Provide[core.AuthorizationExplanation](s, func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{}, nil
			})
		},
		func(s *module.Services) error {
			return module.Provide[audit.Appender](s, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
		},
		func(s *module.Services) error { return module.Provide[events.Publisher](s, pub) },
		func(s *module.Services) error { return module.Provide[events.Catalog](s, pub) },
	}
	for end := 0; end <= len(providers); end++ {
		services := module.NewServices()
		for _, provide := range providers[:end] {
			require.NoError(t, provide(services))
		}
		require.NoError(t, services.Seal())
		e := m.InjectServices(services)
		if end == len(providers) {
			require.NoError(t, e)
		} else {
			require.Error(t, e)
		}
	}
	require.Contains(t, m.ModuleDependencies(), core.Name)
}
func TestBriefValidationRejectsInvalidFactsAndTransactions(t *testing.T) {
	m, db := referencesHarness(t)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 2})
	base := SignedBriefInput{TenantID: 7, ProjectID: 20, BindingID: 30, IntakeID: 10, Items: []BriefItem{{ID: "one", Text: "Scope"}}}
	for _, mutate := range []func(*SignedBriefInput){func(i *SignedBriefInput) { i.TenantID = 0 }, func(i *SignedBriefInput) { i.Items = nil }, func(i *SignedBriefInput) { i.Items = []BriefItem{{ID: "", Text: "text"}} }, func(i *SignedBriefInput) { i.Items = append(i.Items, i.Items[0]) }, func(i *SignedBriefInput) { i.Items[0].Text = strings.Repeat("a", 4001) }} {
		in := base
		in.Items = append([]BriefItem{}, base.Items...)
		mutate(&in)
		_, e := m.signBriefOn(ctx, nil, in)
		require.ErrorIs(t, e, ErrInvalidArgument)
	}
	_, e := m.signBriefOn(t.Context(), nil, base)
	require.ErrorIs(t, e, ErrForbidden)
	_, e = m.signBriefOn(ctx, nil, base)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = m.signBriefOn(ctx, db, base)
	require.ErrorIs(t, e, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.signBriefOn(ctx, tx, base)
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
	_, _, e = m.readSignedBriefOn(ctx, db, 7, 20, 30)
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, _, e = m.readSignedBriefOn(t.Context(), nil, 7, 20, 30)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

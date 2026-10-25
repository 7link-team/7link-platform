package collab

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelLifecycleInvalidEditsAndMissingPeersPreserveState(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	empty, long := "", strings.Repeat("x", 201)
	for _, in := range []ChannelUpdateInput{{Version: 0, Name: &empty}, {Version: 1}, {Version: 1, Name: &empty}, {Version: 1, Topic: &long}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.updateChannelOn(conversationContext(t, 2), tx, 1, 3, ch.ID, in)
			return err
		}), ErrInvalidArgument)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.removeChannelMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 0) }), ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.removeChannelMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 999)
	}))
	var n int64
	require.NoError(t, db.Table(m.names.Members.String()).Where("channel_id = ? AND left_at IS NULL", ch.ID).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestChannelLifecycleVersionArchiveRestoreLastOwnerAndRollback(t *testing.T) {
	m, db := conversationHarness(t)
	ctx := conversationContext(t, 2)
	channel := createConversation(t, m, db, "INTERNAL")
	name, topic, archived := "Updated channel", "Updated topic", true
	update := func(in ChannelUpdateInput) (ChannelView, error) {
		var out ChannelView
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.updateChannelOn(ctx, tx, 1, 3, channel.ID, in)
			return e
		})
		return out, err
	}
	// Current OWNER may manage without a separate channel_manage grant.
	m.policy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "channel_read"}, nil
	}
	view, err := update(ChannelUpdateInput{Version: 1, Name: &name, Topic: &topic})
	require.NoError(t, err)
	require.Equal(t, name, view.Name)
	require.Equal(t, 2, view.Version)
	_, err = update(ChannelUpdateInput{Version: 1, Name: &name})
	require.ErrorIs(t, err, ErrChannelVersion)
	view, err = update(ChannelUpdateInput{Version: 2, Archived: &archived})
	require.NoError(t, err)
	require.True(t, view.Archived)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postMessageOn(ctx, tx, 1, 3, channel.ID, PostMessageInput{Body: "Archived", Visibility: "CHANNEL"})
		return e
	}))
	_, err = update(ChannelUpdateInput{Version: 3, Topic: &topic})
	require.ErrorIs(t, err, ErrChannelArchived)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listChannelsStateOn(ctx, tx, 1, 3, "", 10, true)
		require.Len(t, page.Items, 1)
		require.True(t, page.Items[0].Archived)
		return e
	}))
	archived = false
	view, err = update(ChannelUpdateInput{Version: 3, Archived: &archived})
	require.NoError(t, err)
	require.False(t, view.Archived)
	remove := func(actor, target guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error {
			return m.removeChannelMemberOn(conversationContext(t, actor), tx, 1, 3, channel.ID, target)
		})
	}
	require.ErrorIs(t, remove(2, 2), ErrLastOwner)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(ctx, tx, 1, 3, channel.ID, 4, MemberInput{Role: "OWNER"})
		return e
	}))
	require.NoError(t, remove(2, 2))
	require.NoError(t, remove(2, 2), "repeat leave is idempotent")
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.authorizeChannel(ctx, tx, 1, 3, channel.ID, "channel_read")
		return e
	}), "departed user cannot read")
	require.ErrorIs(t, remove(4, 4), ErrLastOwner)
	ctx = conversationContext(t, 4)
	oldAudit := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err = update(ChannelUpdateInput{Version: view.Version, Name: &name})
	require.Error(t, err)
	m.audit = oldAudit
	view, err = update(ChannelUpdateInput{Version: view.Version, Name: &name})
	require.NoError(t, err, "audit failure rolled back the version")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.CreateDefaultOn(ctx, tx, DefaultChannelInput{Audit: ProvisioningAudit{AuthoritySource: "fixture"}, TenantID: 1, ProjectID: 3, Name: "Default", MemberPrincipalIDs: []guid.ID{4}})
		return e
	}))
	var defaultID guid.ID
	require.NoError(t, db.Table("c_collab_channels").Select("id").Where("kind=?", "PROJECT_DEFAULT").Scan(&defaultID).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.updateChannelOn(ctx, tx, 1, 3, defaultID, ChannelUpdateInput{Version: 1, Archived: &archived})
		return e
	}), ErrForbidden, "default membership is not an OWNER grant")
	m.policy = core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.updateChannelOn(ctx, tx, 1, 3, defaultID, ChannelUpdateInput{Version: 1, Archived: &archived})
		return e
	}), ErrChannelImmutable)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.removeChannelMemberOn(ctx, tx, 1, 3, defaultID, 4) }), ErrChannelImmutable)
}

func TestChannelLifecycleHTTPDetailsReplayAndRemoval(t *testing.T) {
	m, db := conversationHarness(t)
	channel := createConversation(t, m, db, "INTERNAL")
	declarations := humachi.New(chi.NewRouter(), huma.DefaultConfig("declarations", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), declarations))
	authority := &directAuthority{contractAuthority{allow: true}}
	m.registrar = authz.NewRegistrar(m.registrar.Registry(), authority, authority, authority)
	api := humachi.New(chi.NewRouter(), huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	h := humatest.Wrap(t, api)
	path, auth := "/channels/"+channel.ID.String(), "Authorization: Bearer fixture"
	detail := h.Get(path, auth)
	require.Equal(t, 200, detail.Code, detail.Body.String())
	require.Equal(t, `"1"`, detail.Header().Get("ETag"))
	require.Equal(t, 200, h.Do(http.MethodOptions, path+"?method=PATCH", auth).Code)
	body := map[string]any{"version": 1, "archived": true}
	first := h.Patch(path, auth, "Idempotency-Key: archive", body)
	require.Equal(t, 200, first.Code, first.Body.String())
	replay := h.Patch(path, auth, "Idempotency-Key: archive", body)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	require.Equal(t, 409, h.Patch(path, auth, "Idempotency-Key: stale", map[string]any{"version": 1, "name": "Stale"}).Code)
	require.Equal(t, 200, h.Patch(path, auth, "Idempotency-Key: restore", map[string]any{"version": 2, "archived": false}).Code)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, channel.ID, 4, MemberInput{})
		return e
	}))
	require.Equal(t, 204, h.Delete(path+"/members/4", auth).Code)
	require.Equal(t, 204, h.Delete(path+"/members/4", auth).Code)
	require.Equal(t, 409, h.Delete(path+"/members/2", auth).Code)
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Equal(t, 403, h.Patch(path, auth, "Idempotency-Key: archive", body).Code, "replay rechecks current project permission")
}

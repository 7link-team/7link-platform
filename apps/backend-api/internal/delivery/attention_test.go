package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func attentionFixture(t *testing.T) (*resultFixture, ResultView, time.Time) {
	t.Helper()
	h := newResultFixture(t)
	r, e := h.submit()
	require.NoError(t, e)
	at := time.Now().UTC().Truncate(time.Millisecond)
	return h, r, at
}
func attentionStart(t *testing.T, h *resultFixture, r ResultView, actor guid.ID, at time.Time) attentionView {
	t.Helper()
	var out attentionView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = h.m.createAttentionOn(h.actor(actor), tx, 99, h.work, attentionCreateBody{ClaimID: r.ID, Surface: "REVIEW_DESK"}, at)
		return e
	}))
	return out
}
func attentionBeat(h *resultFixture, id, actor guid.ID, sequence, delta int64, ended bool, at time.Time) (attentionView, error) {
	var out attentionView
	e := h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = h.m.heartbeatAttentionOn(h.actor(actor), tx, 99, h.work, id, attentionBeatBody{Sequence: sequence, ActiveMSDelta: delta, Ended: ended}, at)
		return err
	})
	return out, e
}

func TestArchivedAttentionHistoryReadKeepsMutationAndOwnerFences(t *testing.T) {
	h, result, at := attentionFixture(t)
	session := attentionStart(t, h, result, 12, at)
	_, err := attentionBeat(h, session.ID, 12, 1, 15000, true, at.Add(15*time.Second))
	require.NoError(t, err)
	p := query.BindProjects(h.db, h.m.service.names)
	_, err = p.WithContext(h.ctx).Where(p.ProjectID.Eq(guid.ID(99))).UpdateSimple(p.ArchivedAt.Value(at.Add(time.Minute)))
	require.NoError(t, err)
	input := &ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}
	total, err := h.m.totalAttentionHTTP(h.actor(12), input)
	require.NoError(t, err)
	require.True(t, total.Body.Data.Measured)
	require.EqualValues(t, 15000, total.Body.Data.ActiveMS)
	require.EqualValues(t, 1, total.Body.Data.SessionCount)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.createAttentionOn(h.actor(12), tx, 99, h.work, attentionCreateBody{ClaimID: result.ID, Surface: "REVIEW_DESK"}, at)
		return err
	}))
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
	}
	_, err = h.m.totalAttentionHTTP(h.actor(12), input)
	require.Error(t, err)
}
func TestAttentionMeasuredTimeSequenceClampAcrossDevicesAndDecision(t *testing.T) {
	h, r, at := attentionFixture(t)
	first := attentionStart(t, h, r, 12, at)
	second := attentionStart(t, h, r, 12, at)
	other := attentionStart(t, h, r, 13, at)
	out, e := attentionBeat(h, first.ID, 12, 1, 999999, false, at.Add(time.Second))
	require.NoError(t, e)
	require.EqualValues(t, 3000, out.ActiveMS, "clamped to wall delta plus tolerance")
	out, e = attentionBeat(h, first.ID, 12, 1, 999999, false, at.Add(10*time.Second))
	require.NoError(t, e)
	require.EqualValues(t, 3000, out.ActiveMS)
	_, e = attentionBeat(h, first.ID, 12, 3, 1000, false, at.Add(15*time.Second))
	require.ErrorIs(t, e, ErrAttentionSequence)
	out, e = attentionBeat(h, first.ID, 12, 2, 999999, false, at.Add(16*time.Second))
	require.NoError(t, e)
	require.EqualValues(t, 20000, out.ActiveMS, "17s maximum")
	_, e = attentionBeat(h, second.ID, 12, 1, 10000, true, at.Add(15*time.Second))
	require.NoError(t, e)
	_, e = attentionBeat(h, other.ID, 13, 1, 15000, false, at.Add(15*time.Second))
	require.NoError(t, e)
	h.fail = "outbox"
	_, e = h.check(r.ID, 12, "PASSED")
	require.Error(t, e)
	var before model.AttentionSession
	require.NoError(t, h.db.Table(h.m.service.names.AttentionSessions.String()).Where("id=?", first.ID).Take(&before).Error)
	require.Nil(t, before.DecisionID)
	require.Nil(t, before.EndedAt)
	h.fail = ""
	checked, e := h.check(r.ID, 12, "PASSED")
	require.NoError(t, e)
	require.Len(t, checked.Actions, 1)
	require.NotNil(t, checked.Actions[0].AttentionMS)
	require.EqualValues(t, 30000, *checked.Actions[0].AttentionMS)
	var sessions []model.AttentionSession
	require.NoError(t, h.db.Table(h.m.service.names.AttentionSessions.String()).Order("id").Find(&sessions).Error)
	require.NotNil(t, sessions[0].EndedAt)
	require.NotNil(t, sessions[0].DecisionID)
	require.NotNil(t, sessions[1].DecisionID)
	require.Nil(t, sessions[2].DecisionID)
	total, e := h.m.totalAttentionHTTP(h.actor(12), &ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()})
	require.NoError(t, e)
	require.True(t, total.Body.Data.Measured)
	require.EqualValues(t, 45000, total.Body.Data.ActiveMS, "undecided sessions contribute to raw work denominator")
	_, e = attentionBeat(h, first.ID, 12, 3, 1000, false, at.Add(20*time.Second))
	require.ErrorIs(t, e, ErrAttentionClosed)
}
func TestAttentionCurrentHumanScopeExpiryAndRollback(t *testing.T) {
	for _, scenario := range []string{"agent", "scope", "wrong_actor", "negative", "stale", "four_hours", "claim_changed", "audit", "outbox"} {
		t.Run(scenario, func(t *testing.T) {
			h, r, at := attentionFixture(t)
			session := attentionStart(t, h, r, 12, at)
			actor := guid.ID(12)
			when := at.Add(15 * time.Second)
			delta := int64(15000)
			switch scenario {
			case "agent":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "scope":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
			case "wrong_actor":
				actor = 13
			case "negative":
				delta = -1
			case "stale":
				when = at.Add(45001 * time.Millisecond)
			case "four_hours":
				require.NoError(t, h.db.Table(h.m.service.names.AttentionSessions.String()).Where("id=?", session.ID).Update("started_at", at.Add(-4*time.Hour)).Error)
			case "claim_changed":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("current_claim_id", nil).Error)
			case "audit", "outbox":
				h.fail = scenario
			}
			_, e := attentionBeat(h, session.ID, actor, 1, delta, false, when)
			require.Error(t, e)
			var row model.AttentionSession
			require.NoError(t, h.db.Table(h.m.service.names.AttentionSessions.String()).Where("id=?", session.ID).Take(&row).Error)
			require.Zero(t, row.ActiveMS)
			require.Zero(t, row.LastSequence)
		})
	}
}
func TestAttentionStaleJobClosesWithoutInventingTime(t *testing.T) {
	h, r, at := attentionFixture(t)
	session := attentionStart(t, h, r, 12, at)
	require.NoError(t, h.m.closeStaleAttention(h.ctx, 1, at.Add(45*time.Second)))
	var row model.AttentionSession
	require.NoError(t, h.db.Table(h.m.service.names.AttentionSessions.String()).Where("id=?", session.ID).Take(&row).Error)
	require.Nil(t, row.EndedAt)
	require.NoError(t, h.m.closeStaleAttention(h.ctx, 1, at.Add(46*time.Second)))
	require.NoError(t, h.db.Table(h.m.service.names.AttentionSessions.String()).Where("id=?", session.ID).Take(&row).Error)
	require.NotNil(t, row.EndedAt)
	require.Zero(t, row.ActiveMS)
	require.Equal(t, at.Add(45*time.Second), *row.EndedAt)
	require.NoError(t, h.m.closeStaleAttention(h.ctx, 1, at.Add(time.Hour)))
	require.Error(t, h.m.attentionCloseHandler().Run(h.ctx, jobs.Job{}))
	require.Equal(t, attentionJob, h.m.JobSchedules()[3].Kind)
}
func TestAttentionReviewerLeaseRenewal(t *testing.T) {
	h, r, at := attentionFixture(t)
	session := attentionStart(t, h, r, 12, at)
	expires := at.Add(time.Minute)
	q := query.BindAssignments(h.db, h.m.service.names)
	require.NoError(t, q.WithContext(h.ctx).Create(&model.Assignment{TenantID: 1, ID: 900, ProjectID: 99, WorkItemID: h.work, ActiveSlot: &h.work, Role: "REVIEWER", AssigneeKind: "HUMAN", AssigneePrincipalID: 12, Status: "ACTIVE", LeaseTTLMS: 900000, LeaseExpiresAt: &expires, Version: 1, CreatedAt: at, UpdatedAt: at}))
	out, e := attentionBeat(h, session.ID, 12, 1, 15000, false, at.Add(15*time.Second))
	require.NoError(t, e)
	require.NotNil(t, out.ReviewerLeaseExpiresAt)
	require.Equal(t, at.Add(915*time.Second), *out.ReviewerLeaseExpiresAt)
}
func TestAttentionHTTPContractsAuthorizationAndUnknownLegacy(t *testing.T) {
	h, r, _ := attentionFixture(t)
	registry, e := authz.NewRegistry(authz.Action{Resource: "work_item", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true})
	require.NoError(t, e)
	guard := resultHTTPGuard{ctx: h.ctx}
	h.m.registrar = authz.NewRegistrar(registry, guard, guard, guard)
	_, api := humatest.New(t)
	require.NoError(t, h.m.registerAttentionHTTP(api))
	require.Equal(t, "attention_session.create", api.OpenAPI().Paths[attentionPath].Post.OperationID)
	require.Equal(t, "work_item_read", api.OpenAPI().Paths[attentionPath].Post.Extensions[authz.GuardExtensionKey])
	require.NotNil(t, api.OpenAPI().Paths[attentionPath+"/{attention_session_id}"].Put)
	total, e := h.m.totalAttentionHTTP(h.actor(12), &ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()})
	require.NoError(t, e)
	require.False(t, total.Body.Data.Measured)
	in := &attentionCreateInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, IdempotencyKey: "attention", Body: attentionCreateBody{ClaimID: r.ID, Surface: "REVIEW_DESK"}}
	out, e := h.m.createAttentionHTTP(h.actor(12), in)
	require.NoError(t, e)
	again, e := h.m.createAttentionHTTP(h.actor(12), in)
	require.NoError(t, e)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, out.Body.Data.ID, again.Body.Data.ID)
	beat := &attentionInput{ResultWorkInput: in.ResultWorkInput, ID: out.Body.Data.ID.String(), Body: attentionBeatBody{Sequence: 1, ActiveMSDelta: 15000}}
	_, e = h.m.heartbeatAttentionHTTP(h.actor(13), beat)
	require.Error(t, e)
	_, e = h.m.heartbeatAttentionHTTP(h.actor(12), beat)
	require.NoError(t, e)
	foreign := authn.WithClaims(h.ctx, &authn.Claims{TenantID: 2, EntityID: 99, PrincipalID: 12})
	_, e = h.m.createAttentionHTTP(foreign, in)
	require.Error(t, e)
	assertWorkStatus(t, attentionError(h.ctx, ErrAttentionClosed), 409)
	assertWorkStatus(t, attentionError(h.ctx, ErrAttentionSequence), 422)
	h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	_, e = h.m.createAttentionHTTP(h.actor(12), in)
	assertWorkStatus(t, e, 403)
}

func TestAttentionHTTPCreateRollbackAndSameKeyRecovery(t *testing.T) {
	h, r, _ := attentionFixture(t)
	in := &attentionCreateInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, IdempotencyKey: "retry-transient", Body: attentionCreateBody{ClaimID: r.ID, Surface: "REVIEW_DESK"}}
	h.fail = "outbox"
	_, e := h.m.createAttentionHTTP(h.actor(12), in)
	require.Error(t, e)
	var count int64
	require.NoError(t, h.db.Table(h.m.service.names.AttentionSessions.String()).Count(&count).Error)
	require.Zero(t, count)
	h.fail = ""
	out, e := h.m.createAttentionHTTP(h.actor(12), in)
	require.NoError(t, e)
	require.Positive(t, out.Body.Data.ID)
	require.NoError(t, h.db.Table(h.m.service.names.AttentionSessions.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAttentionHeartbeatStorageFailuresRetainMeasuredTime(t *testing.T) {
	h, r, at := attentionFixture(t)
	session := attentionStart(t, h, r, 12, at)
	failure := errors.New("attention persistence unavailable")
	rollback := errors.New("rollback measurement probe")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("attention-current-source", probe))
	defer h.db.Callback().Query().Remove("attention-current-source")
	require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("attention-current-source", probe))
	defer h.db.Callback().Update().Remove("attention-current-source")
	beat := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			out, e := h.m.heartbeatAttentionOn(h.actor(12), tx, 99, h.work, session.ID, attentionBeatBody{Sequence: 1, ActiveMSDelta: 15000}, at.Add(15*time.Second))
			if e != nil {
				return e
			}
			require.EqualValues(t, 15000, out.ActiveMS)
			return rollback
		})
	}
	require.ErrorIs(t, beat(), rollback)
	total := calls
	require.Greater(t, total, 5)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, beat(), failure, "attention persistence step %d", i)
	}
	failAt = 0
	var stored model.AttentionSession
	require.NoError(t, h.db.Table(h.m.service.names.AttentionSessions.String()).Where("id=?", session.ID).Take(&stored).Error)
	require.Zero(t, stored.ActiveMS)
	require.Zero(t, stored.LastSequence)
	require.Nil(t, stored.EndedAt)
}

func TestAttentionTotalsRejectUnavailableCurrentIdentityAndMembership(t *testing.T) {
	for _, mode := range []string{"lock", "human", "member", "inactive", "missing", "read", "scan"} {
		t.Run(mode, func(t *testing.T) {
			h, r, at := attentionFixture(t)
			attentionStart(t, h, r, 12, at)
			failure := errors.New("attention current facts unavailable")
			switch mode {
			case "lock":
				h.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "human":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "member", "inactive":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
					if mode == "member" {
						return false, failure
					}
					return false, nil
				})
			case "missing":
				h.m.principals.Lock = nil
			case "read":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "scan":
				require.NoError(t, h.db.Exec("ALTER TABLE "+h.m.service.names.AttentionSessions.String()+" RENAME TO unavailable_attention_sessions").Error)
			}
			out, e := h.m.totalAttentionHTTP(h.actor(12), &ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()})
			require.Error(t, e)
			require.Nil(t, out)
		})
	}
}

func TestAttentionHeartbeatHTTPRejectsMalformedScopeAndUnavailableRuntime(t *testing.T) {
	h := newResultFixture(t)
	for _, in := range []attentionInput{{ResultWorkInput: ResultWorkInput{EntityID: "9999999999999999999", WorkItemID: "1"}, ID: "1"}, {ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: "9999999999999999999"}, ID: "1"}, {ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: "1"}, ID: "9999999999999999999"}} {
		out, e := h.m.heartbeatAttentionHTTP(h.ctx, &in)
		require.Nil(t, out)
		assertWorkStatus(t, e, 422)
	}
	h.m.runtime = nil
	_, e := h.m.heartbeatAttentionHTTP(h.ctx, &attentionInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, ID: "1"})
	assertWorkStatus(t, e, 503)
	for _, in := range []ResultWorkInput{{EntityID: "9999999999999999999", WorkItemID: "1"}, {EntityID: "99", WorkItemID: "9999999999999999999"}} {
		_, e := h.m.totalAttentionHTTP(h.ctx, &in)
		assertWorkStatus(t, e, 422)
	}
}

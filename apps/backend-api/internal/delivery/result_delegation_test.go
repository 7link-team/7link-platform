package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentAcceptanceRequiresFreshContextAndSameKeyRefresh(t *testing.T) {
	h := newResultFixture(t)
	r, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 114, TenantID: 1, PrincipalID: 14, State: "ACTIVE"}, true, nil
	}
	h.m.acceptanceExecutions = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }
	h.m.acceptanceSettlements = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) { return 0, true, nil }
	h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, DelegationID: 901, AuthoritySource: "DELEGATION:901"}, nil
	}
	in := &resultAcceptInput{ResultVersionInput: h.versionInput(r.ID, "context-agent-accept"), Body: resultAcceptBody{Reason: "Verified independent criteria"}}
	before := h.snapshot()
	_, err = h.m.acceptResultHTTP(h.actor(14), in)
	assertWorkStatus(t, err, 422)
	require.Equal(t, before, h.snapshot())
	require.NoError(t, json.Unmarshal([]byte(`{"reason":"Verified independent criteria","seen_seq":0}`), &in.Body))
	_, err = h.m.acceptResultHTTP(h.actor(14), in)
	assertWorkStatus(t, err, 409)
	raw, marshalErr := respx.MarshalEnvelope(h.actor(14), err)
	require.NoError(t, marshalErr)
	require.Contains(t, string(raw), `"code":120469`)
	require.Contains(t, string(raw), `"operation":"result.accept"`)
	require.Contains(t, string(raw), `"operation":"inbox_events.list"`)
	require.Contains(t, string(raw), "RETRY_WITH_SAME_IDEMPOTENCY_KEY")
	require.Equal(t, before, h.snapshot())
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"reason":"Verified independent criteria","seen_seq":%d}`, h.stored().ActivitySeq)), &in.Body))
	first, err := h.m.acceptResultHTTP(h.actor(14), in)
	require.NoError(t, err)
	again, err := h.m.acceptResultHTTP(h.actor(14), in)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data, again.Body.Data)
	in.Body.Reason = "A different logical decision"
	_, err = h.m.acceptResultHTTP(h.actor(14), in)
	assertWorkStatus(t, err, 422)
}

func TestAgentAcceptanceHTTPReplayRechecksDelegation(t *testing.T) {
	h := newResultFixture(t)
	r, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 114, TenantID: 1, PrincipalID: 14, State: "ACTIVE"}, true, nil
	}
	h.m.acceptanceExecutions = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }
	h.m.acceptanceSettlements = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) { return 0, true, nil }
	allowed := true
	h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: allowed, DelegationID: 901, AuthoritySource: "DELEGATION:901"}, nil
	}
	in := &resultAcceptInput{ResultVersionInput: h.versionInput(r.ID, "fixture-agent-accept"), Body: resultAcceptBody{SeenSeq: new(h.stored().ActivitySeq), Reason: "Verified independent criteria"}}
	first, err := h.m.acceptResultHTTP(h.actor(14), in)
	require.NoError(t, err)
	again, err := h.m.acceptResultHTTP(h.actor(14), in)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, first.Body.Data, again.Body.Data)
	allowed = false
	_, err = h.m.acceptResultHTTP(h.actor(14), in)
	assertWorkStatus(t, err, 403)
}

func TestAgentAndonHTTPPullAndHumanOnlyClear(t *testing.T) {
	h := newResultFixture(t)
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 114, TenantID: 1, PrincipalID: 14, State: "ACTIVE"}, true, nil
	}
	allowed := true
	h.m.andonAuthority = func(_ context.Context, _ *gorm.DB, req governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		require.Equal(t, "AGENT", req.ActorKind)
		require.Equal(t, "andon_pull", req.Code)
		return governance.AuthorityDecision{Allowed: allowed, AuthoritySource: "ROLE:agent_executor"}, nil
	}
	in := &andonInput{EntityID: "99", WorkItemID: h.work.String(), Key: "fixture-agent-andon", Body: andonBody{Version: h.stored().Version, Reason: "Inconsistent requirement evidence"}}
	first, err := h.m.pullAndonHTTP(h.actor(14), in)
	require.NoError(t, err)
	again, err := h.m.pullAndonHTTP(h.actor(14), in)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	allowed = false
	_, err = h.m.pullAndonHTTP(h.actor(14), in)
	assertWorkStatus(t, err, 403)
	allowed = true
	in.Key = "fixture-agent-clear"
	in.Body.Version = first.Body.Data.Work.Version
	_, err = h.m.clearAndonHTTP(h.actor(14), in)
	require.Error(t, err)
	require.True(t, andonActive(h.stored()))
}

func TestDelegatedAcceptanceUsesActualAgentCostAndImmutableEvidence(t *testing.T) {
	for _, mode := range []string{"success", "revoked", "inactive", "human_required", "unknown_cost", "missing_cost", "forged_source", "failed_verification", "outbox", "stale", "execution_error", "settlement_error", "negative_cost", "authority_error", "zero_delegation"} {
		t.Run(mode, func(t *testing.T) {
			h := newResultFixture(t)
			failure := errors.New("acceptance dependency unavailable")
			result, err := h.submit()
			require.NoError(t, err)
			_, err = h.check(result.ID, 12, "PASSED")
			require.NoError(t, err)
			h.m.members = memberCheck(func(_ context.Context, _ *gorm.DB, tenant, actor guid.ID) (bool, error) {
				return tenant == 1 && actor >= 11 && actor <= 14, nil
			})
			h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 114, TenantID: 1, PrincipalID: 14, State: "ACTIVE", Roles: []string{"REVIEWER"}}, mode != "inactive", nil
			}
			h.m.acceptanceExecutions = func(_ context.Context, tx *gorm.DB, tenant, project, work guid.ID) ([]guid.ID, error) {
				require.Same(t, h.tx, tx)
				require.Equal(t, h.work, work)
				if mode == "execution_error" {
					return nil, failure
				}
				return []guid.ID{201, 202}, nil
			}
			h.m.acceptanceSettlements = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) (int64, bool, error) {
				require.Equal(t, []guid.ID{201, 202}, ids)
				if mode == "settlement_error" {
					return 0, false, failure
				}
				if mode == "negative_cost" {
					return -1, true, nil
				}
				return 42, mode != "unknown_cost", nil
			}
			h.m.deliveryAuthority = func(_ context.Context, tx *gorm.DB, req governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				require.Same(t, h.tx, tx)
				require.Equal(t, "AGENT", req.ActorKind)
				require.Equal(t, "decision_approve", req.Code)
				require.Equal(t, guid.ID(14), req.PrincipalID)
				require.Equal(t, result.ID, req.Facts.ClaimID)
				require.EqualValues(t, 42, req.Facts.CostMicros)
				require.True(t, req.Facts.RequiredVerificationsPassed)
				if mode == "authority_error" {
					return governance.AuthorityDecision{}, failure
				}
				if mode == "zero_delegation" {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "DELEGATION:0"}, nil
				}
				source := "DELEGATION:901"
				if mode == "forged_source" {
					source = "ROLE:reviewer"
				}
				return governance.AuthorityDecision{Allowed: mode != "revoked", DelegationID: 901, AuthoritySource: source}, nil
			}
			w := query.BindWorkItems(h.db, h.m.service.names)
			if mode == "human_required" {
				_, err = w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).Update(w.RequireHumanDecision, true)
				require.NoError(t, err)
			}
			if mode == "failed_verification" {
				v := query.BindVerificationRuns(h.db, h.m.service.names)
				_, err = v.WithContext(h.ctx).Where(v.ResultID.Eq(result.ID)).Update(v.Verdict, "FAILED")
				require.NoError(t, err)
			}
			if mode == "missing_cost" {
				h.m.acceptanceSettlements = nil
			}
			if mode == "outbox" {
				h.fail = "outbox"
			}
			before := h.snapshot()
			var observed []events.Event
			publish := h.m.service.events
			h.m.service.events = projectPublisher(func(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
				observed = append(observed, e)
				return publish.Publish(ctx, tx, e)
			})
			_, err = h.command(14, func(ctx context.Context, tx *gorm.DB, version int) (ResultView, error) {
				if mode == "stale" {
					version--
				}
				return h.m.acceptResultOn(ctx, tx, 1, 99, h.work, result.ID, 14, 11, version, resultAcceptBody{SeenSeq: new(h.stored().ActivitySeq), Reason: "Delegated evidence decision"})
			})
			if mode != "success" {
				require.Error(t, err)
				if mode == "execution_error" || mode == "settlement_error" || mode == "authority_error" {
					require.ErrorIs(t, err, failure)
				}
				require.Equal(t, before, h.snapshot(), "failed acceptance is atomic")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "ACCEPTED", h.stored().State)
			require.NotEmpty(t, observed)
			require.Equal(t, "AGENT", observed[0].Actor.Kind)
			require.Equal(t, "DELEGATION:901", observed[0].Actor.AuthoritySource)
			_, err = h.accept(result.ID, 14)
			require.Error(t, err, "an accepted claim cannot be accepted twice")
		})
	}
}

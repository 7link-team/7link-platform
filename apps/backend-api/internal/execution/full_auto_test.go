package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func fullAutoFixture(t *testing.T) (*executionFixture, ExecutionView) {
	t.Helper()
	f := executionHarness(t)
	in := validCreate()
	in.Plan.AuthorizationMode = "FULL_AUTO"
	var v ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; v, e = f.m.CreateOn(f.human, tx, 7, in); return e }))
	f.m.reauth = func(_ context.Context, _ *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		if actor != 11 || cookie != "session" || proof != "verified" || purpose != "execution_full_auto" || target != "execution:"+v.ID.String() {
			return errors.New("reauth rejected")
		}
		return nil
	}
	return f, v
}
func fullAutoGrant(t *testing.T, f *executionFixture, id guid.ID) (FullAutoGrant, error) {
	t.Helper()
	var out FullAutoGrant
	err := f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = f.m.GrantFullAutoOn(f.human, tx, 7, id, "session", "verified", "I approve unattended execution of this exact plan")
		return e
	})
	return out, err
}
func TestFullAutoRequiresRecordedHumanGrantAndPreservesFrozenPlan(t *testing.T) {
	f, v := fullAutoFixture(t)
	require.Equal(t, "FULL_AUTO_APPROVAL_REQUIRED", v.StateReason)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		out, e := f.m.PullOn(f.runner, tx, 7, f.fence)
		require.Empty(t, out)
		return e
	}))
	g, err := fullAutoGrant(t, f, v.ID)
	require.NoError(t, err)
	require.Positive(t, g.ID)
	require.Equal(t, v.PlanHash, g.PlanHash)
	g2, err := fullAutoGrant(t, f, v.ID)
	require.NoError(t, err)
	require.Equal(t, g, g2)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		out, e := f.m.PullOn(f.runner, tx, 7, f.fence)
		require.NoError(t, e)
		require.Len(t, out, 1)
		require.Equal(t, v.PlanHash, out[0].PlanHash)
		require.Equal(t, v.Plan, out[0].Plan)
		require.NotNil(t, out[0].FullAutoGrantID)
		require.Equal(t, g.ID, *out[0].FullAutoGrantID)
		return e
	}))
	q := query.BindEvidence(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotContains(t, rows[0].Content, "verified")
	require.NotContains(t, rows[0].Content, "session")
}
func TestFullAutoGrantRechecksHumanPolicyFencesAndAtomicEvidence(t *testing.T) {
	for _, fault := range []string{"proof", "missing_consumer", "human", "permission", "project_policy", "cancel", "leased", "wrong_mode", "audit", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			f, v := fullAutoFixture(t)
			switch fault {
			case "proof":
				f.m.reauth = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error {
					return errors.New("expired or wrong purpose")
				}
			case "missing_consumer":
				f.m.reauth = nil
			case "human":
				f.humanActive = false
			case "permission":
				f.authorized = false
			case "project_policy":
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					_, e := f.m.PutFullAutoPolicyOn(f.human, tx, 7, v.ID, 0, false, "Disallow unattended tools in this project")
					return e
				}))
			case "cancel":
				q := query.BindExecutions(f.db, f.m.names)
				_, e := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.CancelRequestedAt, time.Now())
				require.NoError(t, e)
			case "leased":
				q := query.BindExecutions(f.db, f.m.names)
				_, e := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.LeaseExpiresAt, time.Now())
				require.NoError(t, e)
			case "wrong_mode":
				q := query.BindExecutions(f.db, f.m.names)
				p := v.Plan
				p.AuthorizationMode = "READ_ONLY"
				b, e := json.Marshal(p)
				require.NoError(t, e)
				_, e = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Updates(map[string]any{"plan_json": string(b), "plan_hash": hash(b)})
				require.NoError(t, e)
			case "audit":
				f.m.audit = nil
			case "outbox":
				f.pub.fail = true
			}
			_, err := fullAutoGrant(t, f, v.ID)
			require.Error(t, err)
			q := query.BindEvidence(f.db, f.m.names)
			n, e := q.WithContext(t.Context()).Where(q.Kind.Eq("FULL_AUTO_GRANT")).Count()
			require.NoError(t, e)
			require.Zero(t, n)
			r := f.read(t, v.ID)
			require.Equal(t, "FULL_AUTO_APPROVAL_REQUIRED", r.StateReason)
		})
	}
}
func TestFullAutoPolicyVersionRevocationAndAttemptBinding(t *testing.T) {
	f, v := fullAutoFixture(t)
	_, err := fullAutoGrant(t, f, v.ID)
	require.NoError(t, err)
	put := func(expected int, allow bool) error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			_, e := f.m.PutFullAutoPolicyOn(f.human, tx, 7, v.ID, expected, allow, "Human project policy decision")
			return e
		})
	}
	require.NoError(t, put(0, false))
	require.ErrorIs(t, put(0, true), ErrConflict)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.PullOn(f.runner, tx, 7, f.fence); return e }), ErrFullAutoForbidden)
	require.NoError(t, put(1, true))
	q := query.BindExecutions(f.db, f.m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.DispatchEpoch, 2)
	require.NoError(t, err)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.PullOn(f.runner, tx, 7, f.fence); return e }), ErrApprovalUnavailable)
	f.human = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8, PrincipalID: 11})
	require.Error(t, put(2, false))
}

func TestFullAutoReplayDoesNotConsumeProofTwiceButReauthorizes(t *testing.T) {
	f, v := fullAutoFixture(t)
	consume := f.m.reauth
	calls := 0
	f.m.reauth = func(ctx context.Context, tx *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		calls++
		if calls > 1 {
			return errors.New("single-use proof already consumed")
		}
		return consume(ctx, tx, actor, cookie, proof, purpose, target)
	}
	g, err := fullAutoGrant(t, f, v.ID)
	require.NoError(t, err)
	replay, err := fullAutoGrant(t, f, v.ID)
	require.NoError(t, err)
	require.Equal(t, g, replay)
	require.Equal(t, 1, calls)
	// A lost approval response may be retried after the runner has leased it.
	// Returning immutable evidence must neither consume proof nor create a grant.
	q := query.BindExecutions(f.db, f.m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.LeaseExpiresAt, time.Now())
	require.NoError(t, err)
	replay, err = fullAutoGrant(t, f, v.ID)
	require.NoError(t, err)
	require.Equal(t, g, replay)
	require.Equal(t, 1, calls)
	f.authorized = false
	_, err = fullAutoGrant(t, f, v.ID)
	require.ErrorIs(t, err, ErrForbidden)
	require.Equal(t, 1, calls)
}
func TestFullAutoDispatchRejectsRevokedApprovingHuman(t *testing.T) {
	f, v := fullAutoFixture(t)
	_, err := fullAutoGrant(t, f, v.ID)
	require.NoError(t, err)
	f.humanActive = false
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.PullOn(f.runner, tx, 7, f.fence); return e }), ErrForbidden)
}

func TestFullAutoStorageFailuresPreserveGrantPolicyAndDispatch(t *testing.T) {
	for _, command := range []string{"grant", "policy", "dispatch"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f, v := fullAutoFixture(t)
				if command == "dispatch" {
					_, e := fullAutoGrant(t, f, v.ID)
					require.NoError(t, e)
				}
				return f, func(tx *gorm.DB) error {
					switch command {
					case "grant":
						out, e := f.m.GrantFullAutoOn(f.human, tx, 7, v.ID, "session", "verified", "Approve exact frozen plan")
						if e == nil {
							require.Equal(t, v.PlanHash, out.PlanHash)
						}
						return e
					case "policy":
						out, e := f.m.PutFullAutoPolicyOn(f.human, tx, 7, v.ID, 0, false, "Disable unattended execution")
						if e == nil {
							require.False(t, out.AllowFullAuto)
						}
						return e
					default:
						out, e := f.m.PullOn(f.runner, tx, 7, f.fence)
						if e == nil {
							require.Len(t, out, 1)
							require.NotNil(t, out[0].FullAutoGrantID)
						}
						return e
					}
				}
			})
		})
	}
}

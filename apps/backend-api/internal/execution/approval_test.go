package execution

import (
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func approvalFixture(t *testing.T) (*executionFixture, ExecutionView, ApprovalRequest) {
	t.Helper()
	f := executionHarness(t)
	in := validCreate()
	in.Plan.Runtime = "CLAUDE_CODE"
	in.Plan.AuthorizationMode = "ON_REQUEST"
	var v ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var err error; v, err = f.m.CreateOn(f.human, tx, 7, in); return err }))
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	return f, v, ApprovalRequest{DispatchEpoch: 1, RequestID: "runtime-request-1", Tool: "Bash", Input: json.RawMessage(`{"command":"git status"}`)}
}
func requestApproval(t *testing.T, f *executionFixture, v ExecutionView, in ApprovalRequest) ApprovalView {
	t.Helper()
	var out ApprovalView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = f.m.RequestApprovalOn(f.runner, tx, 7, v.ID, f.fence, in)
		return err
	}))
	return out
}
func TestApprovalHumanDecisionReplayRevocationAndFences(t *testing.T) {
	f, v, in := approvalFixture(t)
	a := requestApproval(t, f, v, in)
	require.Equal(t, "PENDING", a.State)
	require.Equal(t, a.ID, requestApproval(t, f, v, in).ID)
	var decision ApprovalView
	decide := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			var err error
			decision, err = f.m.DecideApprovalOn(f.human, tx, 7, v.ID, a.ID, "ALLOW", "Reviewed exact tool input")
			return err
		})
	}
	require.NoError(t, decide())
	require.Equal(t, "ALLOW", decision.State)
	require.NotNil(t, decision.ActorID)
	require.Equal(t, guid.ID(11), *decision.ActorID)
	require.NoError(t, decide())
	f.authorized = false
	require.ErrorIs(t, decide(), ErrForbidden)
	f.authorized = true
	f.humanActive = false
	require.ErrorIs(t, decide(), ErrForbidden)
	f.humanActive = true
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		r, e := f.m.ReadRunnerApprovalOn(f.runner, tx, 7, v.ID, a.ID, f.fence, 1)
		require.Equal(t, "ALLOW", r.State)
		return e
	}))
	f.sessionValid = false
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.ReadRunnerApprovalOn(f.runner, tx, 7, v.ID, a.ID, f.fence, 1)
		return e
	}))
	f.sessionValid = true
	in.Input = json.RawMessage(`{"command":"git push"}`)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.RequestApprovalOn(f.runner, tx, 7, v.ID, f.fence, in); return e }), ErrConflict)
}
func TestApprovalExpiryCancellationNoAgentDecisionAndAtomicAudit(t *testing.T) {
	for _, fault := range []string{"expiry", "cancel", "epoch", "human", "audit"} {
		t.Run(fault, func(t *testing.T) {
			f, v, in := approvalFixture(t)
			a := requestApproval(t, f, v, in)
			ctx := f.human
			switch fault {
			case "expiry":
				q := query.BindApprovals(f.db, f.m.names)
				_, err := q.WithContext(t.Context()).Where(q.ID.Eq(a.ID)).Update(q.ExpiresAt, time.Now().Add(-time.Second))
				require.NoError(t, err)
			case "cancel":
				q := query.BindExecutions(f.db, f.m.names)
				_, err := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.CancelRequestedAt, time.Now())
				require.NoError(t, err)
			case "epoch":
				q := query.BindExecutions(f.db, f.m.names)
				_, err := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.DispatchEpoch, 2)
				require.NoError(t, err)
			case "human":
				f.humanActive = false
				ctx = authn.WithClaims(context.Background(), &authn.Claims{TenantID: 7, PrincipalID: 21})
			case "audit":
				f.m.audit = nil
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, e := f.m.DecideApprovalOn(ctx, tx, 7, v.ID, a.ID, "ALLOW", "reviewed")
				return e
			}))
			q := query.BindApprovals(f.db, f.m.names)
			r, err := q.WithContext(t.Context()).Where(q.ID.Eq(a.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "PENDING", r.State)
			require.Nil(t, r.ActorID)
		})
	}
}

func TestApprovalEveryStorageFailurePreservesRequestDecisionAndJournal(t *testing.T) {
	for _, command := range []string{"request", "decide", "read"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f, v, in := approvalFixture(t)
				var a ApprovalView
				if command != "request" {
					a = requestApproval(t, f, v, in)
				}
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Executions.String(), f.m.names.Receipts.String(), f.m.names.Logs.String(), f.m.names.Evidence.String(), f.m.names.Approvals.String(), "slots", "effects"} {
						var rows []map[string]any
						require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
						out[table] = rows
					}
					return out
				}
				before := snapshot()
				count := 0
				injected := errors.New("execution transactional storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("execution_transaction_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("execution_transaction_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("execution_transaction_failure", hook))
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					switch command {
					case "request":
						_, e := f.m.RequestApprovalOn(f.runner, tx, 7, v.ID, f.fence, in)
						return e
					case "decide":
						_, e := f.m.DecideApprovalOn(f.human, tx, 7, v.ID, a.ID, "ALLOW", "Reviewed current tool input")
						return e
					default:
						_, e := f.m.ReadRunnerApprovalOn(f.runner, tx, 7, v.ID, a.ID, f.fence, 1)
						return e
					}
				})
				require.NoError(t, f.db.Callback().Query().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("execution_transaction_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				require.Equal(t, before, snapshot(), "failed command must roll back execution, journals, slots and effects")
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}

func TestApprovalCurrentAuthorityAndMalformedRequestsNeverCreateOrChangeDecision(t *testing.T) {
	for _, command := range []string{"request", "decide", "read"} {
		for _, fault := range []string{"lock", "work_missing", "work_source", "runner_source", "wrong_machine", "no_transaction", "missing_execution", "missing_approval", "policy_missing", "policy_source", "policy_denied", "id_error", "id_zero", "input_shape"} {
			// Human decisions authorize the current Human and project, without a runner fence.
			if (command == "decide" && (fault == "runner_source" || fault == "wrong_machine")) || (fault == "missing_approval" && command == "request") || ((strings.HasPrefix(fault, "policy_") || strings.HasPrefix(fault, "id_") || fault == "input_shape") && command != "request") {
				continue
			}
			t.Run(command+"/"+fault, func(t *testing.T) {
				f, v, in := approvalFixture(t)
				var a ApprovalView
				if command != "request" {
					a = requestApproval(t, f, v, in)
				}
				q := query.BindApprovals(f.db, f.m.names)
				before, e := q.WithContext(f.human).Order(q.ID).Find()
				require.NoError(t, e)
				injected := errors.New("approval current owner unavailable")
				switch fault {
				case "lock":
					f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
				case "work_missing":
					f.m.work.AuthorizeOn = nil
				case "work_source":
					f.m.work.AuthorizeOn = func(context.Context, *gorm.DB, WorkLink, string) error { return injected }
				case "runner_source":
					f.m.machines.ValidateSessionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64) error { return injected }
				case "wrong_machine":
					f.fence.MachineID = 99
				case "missing_execution":
					v.ID = 999
				case "missing_approval":
					a.ID = 999
				case "policy_missing":
					f.m.policy = nil
				case "policy_source":
					f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{}, injected
					}
				case "policy_denied":
					f.authorized = false
				case "id_error":
					f.m.ids = failedExecutionIDs{err: injected}
				case "id_zero":
					f.m.ids = failedExecutionIDs{}
				case "input_shape":
					in.Input = json.RawMessage(`[]`)
				}
				invoke := func(tx *gorm.DB) error {
					var out ApprovalView
					var e error
					switch command {
					case "request":
						out, e = f.m.RequestApprovalOn(f.runner, tx, 7, v.ID, f.fence, in)
					case "decide":
						out, e = f.m.DecideApprovalOn(f.human, tx, 7, v.ID, a.ID, "DENY", "Reviewed current request")
					default:
						out, e = f.m.ReadRunnerApprovalOn(f.runner, tx, 7, v.ID, a.ID, f.fence, 1)
					}
					require.Zero(t, out.ID)
					return e
				}
				var err error
				if fault == "no_transaction" {
					err = invoke(f.db)
				} else {
					err = f.db.Transaction(invoke)
				}
				require.Error(t, err)
				after, e := q.WithContext(f.human).Order(q.ID).Find()
				require.NoError(t, e)
				require.Equal(t, before, after)
			})
		}
	}
}

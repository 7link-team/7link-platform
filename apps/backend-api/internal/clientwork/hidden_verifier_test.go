package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type hiddenReconcileCatalog struct{ subscribers []events.Subscriber }

func (c *hiddenReconcileCatalog) AddSubscribers(_ string, s ...events.Subscriber) error {
	c.subscribers = append(c.subscribers, s...)
	return nil
}

func TestHiddenVerificationReconcileUsesExactEventAndHonorsUnavailableAcceptance(t *testing.T) {
	for _, stage := range []string{"invalid", "unavailable", "collect_unavailable", "collect_failure", "success", "bind_failure"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _, _ := harness(t)
			s := module.NewServices()
			catalog := &hiddenReconcileCatalog{}
			require.NoError(t, module.Provide[identity.ServiceAccountValidator](s, identity.ServiceAccountValidator(func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return true, nil })))
			require.NoError(t, module.Provide[core.EntityConstraintOn](s, core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			})))
			require.NoError(t, module.Provide[execution.HiddenSourceFacts](s, execution.HiddenSourceFacts{}))
			failure := errors.New("acceptance collection failed")
			require.NoError(t, module.Provide[delivery.HiddenAcceptanceConnector](s, delivery.HiddenAcceptanceConnector{Bind: func(v delivery.HiddenAcceptanceVerifier) error {
				require.Equal(t, m.config.HiddenVerifier.PrincipalID, v.PrincipalID())
				require.NotNil(t, v.VerdictOn)
				if stage == "bind_failure" {
					return failure
				}
				return nil
			}}))
			calls := 0
			automation := delivery.VerificationAutomation{CollectOn: func(_ context.Context, tx *gorm.DB, tenant, project, work, result guid.ID) error {
				calls++
				require.EqualValues(t, 1, tenant)
				require.EqualValues(t, 3, project)
				require.EqualValues(t, 11, work)
				require.EqualValues(t, 40, result)
				require.NotNil(t, tx)
				if stage == "collect_unavailable" {
					return delivery.ErrHiddenAcceptanceUnavailable
				}
				if stage == "collect_failure" {
					return failure
				}
				return nil
			}}
			if stage == "unavailable" {
				automation.CollectOn = nil
			}
			require.NoError(t, module.Provide[delivery.VerificationAutomation](s, automation))
			require.NoError(t, module.Provide[events.SubscriberCatalog](s, catalog))
			require.NoError(t, s.Seal())
			err := m.injectHiddenAcceptance(s)
			if stage == "bind_failure" {
				require.ErrorIs(t, err, failure)
				require.Empty(t, catalog.subscribers)
				return
			}
			require.NoError(t, err)
			require.Len(t, catalog.subscribers, 1)
			handler := catalog.subscribers[0]
			require.Equal(t, events.Transactional, handler.Mode)
			event := events.Envelope{TenantID: 1, ProjectID: 3, Subject: events.Subject{Kind: "clientwork"}, Data: json.RawMessage(`{"work_item_id":"11","result_id":"40"}`)}
			if stage == "invalid" {
				event.Subject.Kind = "foreign"
			}
			err = db.Transaction(func(tx *gorm.DB) error { return handler.Handle(t.Context(), tx, event) })
			switch stage {
			case "invalid":
				require.ErrorIs(t, err, ErrInvalid)
				require.Zero(t, calls)
			case "unavailable":
				require.ErrorIs(t, err, ErrUnavailable)
				require.Zero(t, calls)
			case "collect_failure":
				require.ErrorIs(t, err, failure)
				require.Equal(t, 1, calls)
			default:
				require.NoError(t, err)
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestHiddenVerifierAuthorityAndPersistenceFailuresNeverCreatePassingReceipt(t *testing.T) {
	for _, stage := range []string{"missing_request", "lock", "service", "permission", "denied_project", "request", "empty_suite", "source_missing", "source_read", "receipt_read", "changed_suite", "create", "missing_audit", "audit"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _, _ := harness(t)
			ctx := testContext(t, 2)
			failure := errors.New("verifier dependency failed")
			m.config.HiddenVerifier = HiddenVerifierConfig{PrincipalID: 99, Model: "local-fixture", Family: "openai"}
			m.hiddenService = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return true, nil }
			m.hiddenPermission = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			in := delivery.HiddenAcceptanceRequest{TenantID: 1, ProjectID: 3, WorkItemID: 11, ResultID: 40, ExecutionID: 50, SuiteHash: strings.Repeat("a", 64), HeadCommit: strings.Repeat("b", 40), Cases: []delivery.RequirementCaseSnapshot{{ID: 55}}}
			m.hiddenRequest = func(context.Context, *gorm.DB, delivery.HiddenAcceptanceRequest) (delivery.HiddenAcceptanceRequest, error) {
				return in, nil
			}
			m.hiddenSource.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (execution.HiddenSource, error) {
				return execution.HiddenSource{PrincipalID: 7, Family: "openai", Model: "same-family-fixture"}, nil
			}
			want := failure
			switch stage {
			case "missing_request":
				m.hiddenRequest = nil
				want = ErrUnavailable
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "service":
				m.hiddenService = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, failure }
			case "permission":
				m.hiddenPermission = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "denied_project":
				m.hiddenPermission = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"3"}}, nil
				}
				want = ErrForbidden
			case "request":
				m.hiddenRequest = func(context.Context, *gorm.DB, delivery.HiddenAcceptanceRequest) (delivery.HiddenAcceptanceRequest, error) {
					return delivery.HiddenAcceptanceRequest{}, failure
				}
			case "empty_suite":
				in.Cases = nil
				want = nil
			case "source_missing":
				m.hiddenSource.ReadOn = nil
				want = ErrUnavailable
			case "source_read":
				m.hiddenSource.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (execution.HiddenSource, error) {
					return execution.HiddenSource{}, failure
				}
			case "receipt_read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_hidden", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.HiddenVerificationReceipt.String() {
						tx.AddError(failure)
					}
				}))
			case "changed_suite":
				calls := 0
				m.hiddenRequest = func(context.Context, *gorm.DB, delivery.HiddenAcceptanceRequest) (delivery.HiddenAcceptanceRequest, error) {
					calls++
					out := in
					if calls > 1 {
						out.SuiteHash = strings.Repeat("c", 64)
					}
					return out, nil
				}
				want = ErrConflict
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("regression_hidden", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.HiddenVerificationReceipt.String() {
						tx.AddError(failure)
					}
				}))
			case "missing_audit":
				m.audit = nil
				want = ErrUnavailable
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			err := m.executeHidden(ctx, in)
			if want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, want)
			}
			require.NoError(t, db.Callback().Query().Remove("regression_hidden"))
			require.NoError(t, db.Callback().Create().Remove("regression_hidden"))
			var rows []model.HiddenVerificationReceipt
			require.NoError(t, db.Table(m.names.HiddenVerificationReceipt.String()).Find(&rows).Error)
			require.Empty(t, rows, "dependency failure or empty suite must not fabricate a verification receipt")
		})
	}
}

// A local protocol adapter, never a configured-model acceptance claim.
func TestHiddenVerifierReceiptFixture(t *testing.T) {
	mode := os.Args[len(os.Args)-1]
	if !strings.HasPrefix(mode, "fixture-receipt-") {
		return
	}
	var in HiddenVerifierInput
	if json.NewDecoder(os.Stdin).Decode(&in) != nil {
		os.Exit(31)
	}
	out := HiddenVerifierOutput{Nonce: in.Nonce, SuiteHash: in.SuiteHash, HeadCommit: in.HeadCommit, Model: in.Model, Family: in.Family, Verdict: "FAILED"}
	switch strings.TrimPrefix(mode, "fixture-receipt-") {
	case "wrong_nonce":
		out.Nonce = "wrong"
	case "unknown_verdict":
		out.Verdict = "APPROVED"
	case "oversized":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 70000))
		os.Exit(0)
	case "trailing":
		_ = json.NewEncoder(os.Stdout).Encode(out)
	case "unknown_field":
		_, _ = os.Stdout.WriteString(`{"private_extra":"untrusted"}`)
		os.Exit(0)
	case "exit":
		os.Exit(32)
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
	os.Exit(0)
}

func TestHiddenVerifierRejectsUnboundAndMalformedCommandReceipts(t *testing.T) {
	repository, head := hiddenRepository(t)
	executable, err := os.Executable()
	require.NoError(t, err)
	m := &Module{}
	m.hiddenIsolation = func(cmd *exec.Cmd, _ HiddenVerifierConfig, root string) (func() error, error) {
		cmd.Env = hiddenEnvironment(os.Environ())
		return func() error { return os.RemoveAll(root) }, nil
	}
	for _, mode := range []string{"valid", "wrong_nonce", "unknown_verdict", "oversized", "trailing", "unknown_field", "exit"} {
		t.Run(mode, func(t *testing.T) {
			config := HiddenVerifierConfig{Executable: executable, Args: []string{"-test.run=^TestHiddenVerifierReceiptFixture$", "--", "fixture-receipt-" + mode}, Model: "local-protocol-fixture", Family: "anthropic", TimeoutSec: 15}
			in := HiddenVerifierInput{HeadCommit: head, Nonce: "fresh-nonce", SuiteHash: strings.Repeat("a", 64), Model: config.Model, Family: config.Family}
			raw, err := json.Marshal(in)
			require.NoError(t, err)
			verdict, reason, hash := m.runHiddenCommand(t.Context(), config, repository, in, raw)
			if mode == "valid" {
				require.Equal(t, "FAILED", verdict)
				require.Equal(t, "VERIFIER_COMMAND_COMPLETED", reason)
			} else {
				require.Equal(t, "INCONCLUSIVE", verdict)
				if mode == "exit" {
					require.Equal(t, "VERIFIER_COMMAND_FAILED", reason)
				} else {
					require.Equal(t, "INVALID_VERIFIER_RECEIPT", reason)
				}
			}
			require.Len(t, hash, 64)
		})
	}
	for _, config := range []HiddenVerifierConfig{{Executable: "relative", TimeoutSec: 1}, {Executable: executable, TimeoutSec: 0}, {Executable: executable, TimeoutSec: 3601}, {Executable: filepath.Join(t.TempDir(), "absent.exe"), TimeoutSec: 1}} {
		verdict, _, _ := m.runHiddenCommand(t.Context(), config, repository, HiddenVerifierInput{HeadCommit: head}, nil)
		require.Equal(t, "UNAVAILABLE", verdict)
	}
	verdict, reason, _ := m.runHiddenCommand(t.Context(), HiddenVerifierConfig{Executable: executable, TimeoutSec: 10}, t.TempDir(), HiddenVerifierInput{HeadCommit: head}, nil)
	require.Equal(t, "UNAVAILABLE", verdict)
	require.Equal(t, "SOURCE_CLONE_UNAVAILABLE", reason)
	verdict, reason, _ = m.runHiddenCommand(t.Context(), HiddenVerifierConfig{Executable: executable, TimeoutSec: 10}, repository, HiddenVerifierInput{HeadCommit: strings.Repeat("f", 40)}, nil)
	require.Equal(t, "UNAVAILABLE", verdict)
	require.Equal(t, "FROZEN_HEAD_UNAVAILABLE", reason)
}

// This test process is a real trusted command adapter. It asserts exact Git
// bytes and frozen secrets from stdin, then echoes only the nonce/hash verdict.
func TestHiddenVerifierCommandFixture(t *testing.T) {
	if os.Args[len(os.Args)-1] != "private-hidden-fixture" {
		return
	}
	var in HiddenVerifierInput
	if json.NewDecoder(os.Stdin).Decode(&in) != nil {
		os.Exit(21)
	}
	actual, err := os.ReadFile("proof.txt")
	if err != nil || string(actual) != "committed proof\n" {
		os.Exit(22)
	}
	if len(in.Cases) != 1 || !strings.Contains(string(in.Cases[0]), "FROZEN-SECRET") || strings.Contains(string(in.Cases[0]), "MUTATED-SECRET") {
		os.Exit(23)
	}
	_ = json.NewEncoder(os.Stdout).Encode(HiddenVerifierOutput{Nonce: in.Nonce, SuiteHash: in.SuiteHash, HeadCommit: in.HeadCommit, Model: in.Model, Family: in.Family, Verdict: "PASSED"})
	os.Exit(0)
}

func hiddenRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		hideHiddenProcess(cmd)
		cmd.Dir = root
		raw, err := cmd.CombinedOutput()
		require.NoError(t, err, string(raw))
		return strings.TrimSpace(string(raw))
	}
	git("init")
	git("config", "user.name", "Verifier Fixture")
	git("config", "user.email", "fixture@example.invalid")
	require.NoError(t, os.WriteFile(filepath.Join(root, "proof.txt"), []byte("committed proof\n"), 0600))
	git("add", "proof.txt")
	git("commit", "-m", "fixture")
	head := git("rev-parse", "HEAD")
	// A dirty executor worktree must never become verifier input.
	require.NoError(t, os.WriteFile(filepath.Join(root, "proof.txt"), []byte("uncommitted executor bytes"), 0600))
	return root, head
}

func TestHiddenVerifierFrozenBytesRealCommandReceiptAndRevocation(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	var created Receipt
	body := TestCaseBody{Title: "Private", Steps: "FROZEN-SECRET", Expected: "Verify committed proof", WorkItemID: 11, IsHiddenAcceptance: true}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = m.writeCaseOn(ctx, tx, 3, 0, 0, body)
		return err
	}))
	var cases []delivery.RequirementCaseSnapshot
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		cases, err = m.requirementCasesOn(ctx, tx, 1, 3, []guid.ID{11})
		return err
	}))
	body.Steps = "MUTATED-SECRET"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.writeCaseOn(ctx, tx, 3, created.ID, 1, body); return err }))
	repository, head := hiddenRepository(t)
	executable, err := os.Executable()
	require.NoError(t, err)

	m.hiddenIsolation = func(cmd *exec.Cmd, _ HiddenVerifierConfig, root string) (func() error, error) {
		cmd.Env = hiddenEnvironment(os.Environ())
		return func() error { return os.RemoveAll(root) }, nil
	}
	m.config.HiddenVerifier = HiddenVerifierConfig{PrincipalID: 99, Executable: executable, Args: []string{"-test.run=^TestHiddenVerifierCommandFixture$", "--", "private-hidden-fixture"}, Model: "actual-command-adapter", Family: "anthropic", TimeoutSec: 15}
	active, allowed := true, true
	m.hiddenService = identity.ServiceAccountValidator(func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID, _ time.Time) (bool, error) {
		return active && tenant == 1 && principal == 99, nil
	})
	m.hiddenPermission = core.EntityConstraintOn(func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, principal guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, "acceptance_suite_verify", permission)
		return authz.DataConstraint{AllowAll: allowed && tenant == 1 && project == 3 && principal == 99}, nil
	})
	m.hiddenSource = execution.HiddenSourceFacts{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (execution.HiddenSource, error) {
		return execution.HiddenSource{PrincipalID: 7, Family: "openai", Model: "source-model", RepositoryPath: repository}, nil
	}}
	in := delivery.HiddenAcceptanceRequest{TenantID: 1, ProjectID: 3, WorkItemID: 11, ResultID: 40, ExecutionID: 50, SuiteHash: strings.Repeat("a", 64), HeadCommit: head, Cases: cases}
	m.hiddenRequest = func(context.Context, *gorm.DB, delivery.HiddenAcceptanceRequest) (delivery.HiddenAcceptanceRequest, error) {
		return in, nil
	}
	queue := &forecastQueueProbe{}
	m.forecastQueue = queue
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.dispatchHiddenOn(ctx, tx, in) }))
	require.Len(t, queue.requests, 1)
	payload, err := json.Marshal(queue.requests[0].Payload)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "SECRET")
	require.NotContains(t, string(payload), cases[0].ContentHash)
	require.NoError(t, m.hiddenVerifierHandler().Run(ctx, jobs.Job{TenantID: 1, Payload: payload}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		receipt, err := m.hiddenReceiptOn(ctx, tx, in)
		require.NoError(t, err)
		require.Equal(t, "PASSED", receipt.Verdict, receipt.Reason)
		require.Equal(t, "VERIFIER_COMMAND_COMPLETED", receipt.Reason)
		raw, _ := json.Marshal(receipt)
		require.NotContains(t, string(raw), "SECRET")
		require.NotContains(t, string(raw), repository)
		passed, err := m.hiddenPassedOn(ctx, tx, in)
		require.NoError(t, err)
		require.True(t, passed)
		verdict, err := m.hiddenVerdictOn(ctx, tx, in)
		require.NoError(t, err)
		require.Equal(t, "PASSED", verdict)
		// Re-dispatch is idempotent and does not reexecute the command.
		return m.dispatchHiddenOn(ctx, tx, in)
	}))
	var outbox []struct {
		Data         string
		RealtimeMode string
	}
	require.NoError(t, db.Table("g_domain_events").Where("event_type = ?", "clientwork.hidden_verified").Find(&outbox).Error)
	require.Len(t, outbox, 1)
	require.NotContains(t, outbox[0].Data, "SECRET")
	require.Equal(t, "NONE", outbox[0].RealtimeMode)
	q := query.BindHiddenVerificationReceipt(db, m.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		changed := in
		changed.HeadCommit = strings.Repeat("b", 40)
		passed, err := m.hiddenPassedOn(ctx, tx, changed)
		require.NoError(t, err)
		require.False(t, passed)
		verdict, err := m.hiddenVerdictOn(ctx, tx, changed)
		require.NoError(t, err)
		require.Equal(t, "UNAVAILABLE", verdict)
		return nil
	}))
	require.NoError(t, db.Table(m.names.HiddenVerificationReceipt.String()).Where("result_id=?", in.ResultID).Update("receipt_hash", strings.Repeat("0", 64)).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		verdict, err := m.hiddenVerdictOn(ctx, tx, in)
		require.NoError(t, err)
		require.Equal(t, "UNAVAILABLE", verdict, "tampered verifier bytes never authorize acceptance")
		passed, err := m.hiddenPassedOn(ctx, tx, in)
		require.NoError(t, err)
		require.False(t, passed)
		return nil
	}))
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_verdict", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.HiddenVerificationReceipt.String() {
			tx.AddError(errors.New("receipt store unavailable"))
		}
	}))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.hiddenVerdictOn(ctx, tx, in); return err }))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.hiddenPassedOn(ctx, tx, in); return err }))
	require.NoError(t, db.Callback().Query().Remove("regression_verdict"))
	allowed = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.hiddenPassedOn(ctx, tx, in); return err }), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.hiddenVerdictOn(ctx, tx, in); return err }), ErrForbidden)
	allowed = true
	active = false
	require.ErrorIs(t, m.executeHidden(ctx, in), ErrForbidden)
}

func TestHiddenVerifierSameFamilyUnavailableAndArchiveTamperRejected(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	m.config.HiddenVerifier = HiddenVerifierConfig{PrincipalID: 99, Executable: "configured", Model: "review", Family: "openai", TimeoutSec: 1}
	m.hiddenService = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return true, nil }
	m.hiddenPermission = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.hiddenSource = execution.HiddenSourceFacts{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (execution.HiddenSource, error) {
		return execution.HiddenSource{PrincipalID: 7, Family: "openai", Model: "source"}, nil
	}}
	in := delivery.HiddenAcceptanceRequest{TenantID: 1, ProjectID: 3, WorkItemID: 11, ResultID: 40, ExecutionID: 50, SuiteHash: strings.Repeat("a", 64), HeadCommit: strings.Repeat("b", 40), Cases: []delivery.RequirementCaseSnapshot{{ID: 55, Version: 1, WorkItemID: 11, IsHiddenAcceptance: true, ContentHash: strings.Repeat("c", 64)}}}
	m.hiddenRequest = func(context.Context, *gorm.DB, delivery.HiddenAcceptanceRequest) (delivery.HiddenAcceptanceRequest, error) {
		return in, nil
	}
	require.NoError(t, m.executeHidden(ctx, in))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		r, err := m.hiddenReceiptOn(ctx, tx, in)
		require.NoError(t, err)
		require.Equal(t, "UNAVAILABLE", r.Verdict)
		require.Equal(t, "DIFFERENT_MODEL_FAMILY_UNAVAILABLE", r.Reason)
		passed, err := m.hiddenPassedOn(ctx, tx, in)
		require.NoError(t, err)
		require.False(t, passed)
		return nil
	}))
	m.config.HiddenVerifier.Family = "anthropic"
	in.ResultID++
	in.Cases = []delivery.RequirementCaseSnapshot{{ID: 55, Version: 1, WorkItemID: 11, IsHiddenAcceptance: true, ContentHash: strings.Repeat("c", 64)}}
	require.ErrorIs(t, m.executeHidden(ctx, in), ErrConflict)
}

func TestHiddenVerifierMissingOSIsolationNeverStartsCommand(t *testing.T) {
	m := &Module{}
	repository, head := hiddenRepository(t)
	executable, err := os.Executable()
	require.NoError(t, err)
	verdict, reason, _ := m.runHiddenCommand(t.Context(), HiddenVerifierConfig{Executable: executable, Args: []string{"-test.run=^TestHiddenVerifierCommandFixture$", "--", "private-hidden-fixture"}, TimeoutSec: 10}, repository, HiddenVerifierInput{HeadCommit: head}, []byte("PRIVATE-SECRET"))
	require.Equal(t, "UNAVAILABLE", verdict)
	require.Equal(t, "OS_ISOLATION_UNAVAILABLE", reason)
}

func TestHiddenVerifierCleanupFailureCannotPass(t *testing.T) {
	repository, head := hiddenRepository(t)
	executable, err := os.Executable()
	require.NoError(t, err)
	m := &Module{}
	var privateRoot string
	m.hiddenIsolation = func(cmd *exec.Cmd, _ HiddenVerifierConfig, root string) (func() error, error) {
		privateRoot = root
		cmd.Env = hiddenEnvironment(os.Environ())
		return func() error { return errors.New("private cleanup failure") }, nil
	}
	defer func() {
		if privateRoot != "" {
			require.NoError(t, os.RemoveAll(privateRoot))
		}
	}()
	input := HiddenVerifierInput{Nonce: strings.Repeat("n", 64), SuiteHash: strings.Repeat("a", 64), HeadCommit: head, Model: "fixture-model", Family: "anthropic", Cases: []json.RawMessage{json.RawMessage(`{"steps":"FROZEN-SECRET"}`)}}
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	verdict, reason, digest := m.runHiddenCommand(t.Context(), HiddenVerifierConfig{Executable: executable, Args: []string{"-test.run=^TestHiddenVerifierCommandFixture$", "--", "private-hidden-fixture"}, Model: input.Model, Family: input.Family, TimeoutSec: 10}, repository, input, raw)
	require.Equal(t, "UNAVAILABLE", verdict)
	require.Equal(t, "PRIVATE_WORKSPACE_CLEANUP_FAILED", reason)
	require.Len(t, digest, 64)
	_, err = os.Stat(privateRoot)
	require.NoError(t, err, "failed authenticated cleanup must not fall back to backend ACL restoration or deletion")
}

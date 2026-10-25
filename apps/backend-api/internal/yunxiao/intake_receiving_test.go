package yunxiao

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/yunxiao/query"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestIntakeCredentialRotationHashOnlyReplayAndVersion(t *testing.T) {
	m, db, ctx := workHarness(t)
	settings := intakeSettingsFixture()
	settings.Enabled = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 0, settings)
		return err
	}))
	in := &intakeCredentialInput{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WriteHeaders: WriteHeaders{IdempotencyKey: "credential-key", IfMatch: `"1"`}}
	first, err := m.rotateIntakeCredentialHTTP(ctx, in)
	require.NoError(t, err)
	secret := first.Body.Data.Credential
	require.Len(t, secret, 64)
	q := query.BindIntakeChannels(db, m.names)
	row, err := q.WithContext(ctx).Where(q.Kind.Eq("API")).Take()
	require.NoError(t, err)
	require.Equal(t, credentialDigest(secret), *row.CredentialHash)
	require.NotEqual(t, secret, *row.CredentialHash)
	var replayBodies []struct{ ResponseBody string }
	require.NoError(t, db.Table("yx_platform_idempotency_keys").Select("response_body").Find(&replayBodies).Error)
	for _, stored := range replayBodies {
		require.NotContains(t, stored.ResponseBody, secret)
	}
	require.Equal(t, "AVAILABLE", first.Body.Data.Channel.Availability)
	replay, err := m.rotateIntakeCredentialHTTP(ctx, in)
	require.NoError(t, err)
	require.Empty(t, replay.Body.Data.Credential)
	require.Equal(t, "true", replay.Replayed)
	old := secret
	in.IdempotencyKey = "rotate-again"
	in.IfMatch = `"2"`
	second, err := m.rotateIntakeCredentialHTTP(ctx, in)
	require.NoError(t, err)
	require.NotEqual(t, old, second.Body.Data.Credential)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.authorizeIntakeReceivingOn(ctx, tx, 1, 99, old) }), ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.authorizeIntakeReceivingOn(ctx, tx, 1, 99, second.Body.Data.Credential)
	}))
}
func TestIntakeReceivingQuotaReplayCredentialAndDisableFence(t *testing.T) {
	m, db, ctx := workHarness(t)
	settings := intakeSettingsFixture()
	settings.Enabled = true
	settings.DailyLimit = 1
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 0, settings)
		return err
	}))
	issued, err := m.rotateIntakeCredentialHTTP(ctx, &intakeCredentialInput{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WriteHeaders: WriteHeaders{IdempotencyKey: "issue-key", IfMatch: `"1"`}})
	require.NoError(t, err)
	calls := 0
	m.intakeReceiving = intake.APIReceiving{ReceiveOn: func(_ context.Context, _ *gorm.DB, tenant, project, channel guid.ID, in intake.APIReceiptInput) (intake.IntakeLedgerEntry, error) {
		calls++
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.Positive(t, channel)
		return intake.IntakeLedgerEntry{ID: 444, ProjectID: project, Title: in.Title, State: "RECEIVED", Kind: "NEW", Version: 1}, nil
	}}
	in := &intakeReceivingInput{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WriteHeaders: WriteHeaders{IdempotencyKey: "submission-key"}, Credential: issued.Body.Data.Credential, Body: intake.APIReceiptInput{ContactID: 1, Title: "Actual source", Content: "source content", Format: "PLAIN"}}
	first, err := m.receiveIntakeHTTP(ctx, in)
	require.NoError(t, err)
	require.EqualValues(t, 444, first.Body.Data.ID)
	replay, err := m.receiveIntakeHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, 1, calls)
	in.IdempotencyKey = "second-submission"
	_, err = m.receiveIntakeHTTP(ctx, in)
	require.Error(t, err)
	require.Equal(t, 1, calls)
	in.IdempotencyKey = "submission-key"
	in.Credential = strings.Repeat("a", 64)
	_, err = m.receiveIntakeHTTP(ctx, in)
	require.Error(t, err)
	in.Credential = issued.Body.Data.Credential
	settings.Enabled = false
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 2, settings)
		return err
	}))
	_, err = m.receiveIntakeHTTP(ctx, in)
	require.Error(t, err)
	require.Equal(t, 1, calls)
	q := query.BindIntakeChannels(db, m.names)
	row, err := q.WithContext(ctx).Where(q.Kind.Eq("API")).Take()
	require.NoError(t, err)
	require.Equal(t, 1, row.ReceivedCount)
	// The policy document is independent of credentials and counters.
	var persisted IntakeChannelSettings
	require.NoError(t, json.Unmarshal([]byte(row.Document), &persisted))
	require.False(t, persisted.Enabled)
	settings.Enabled = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 3, settings)
		return err
	}))
	m.now = func() time.Time { return time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC) }
	m.intakeReceiving.ReceiveOn = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, _ intake.APIReceiptInput) (intake.IntakeLedgerEntry, error) {
		require.NoError(t, tx.Exec("INSERT INTO test_effects(kind) VALUES ('failed-receiving-effect')").Error)
		return intake.IntakeLedgerEntry{}, errors.New("domain/outbox failure")
	}
	in.IdempotencyKey = "next-day-failure"
	_, err = m.receiveIntakeHTTP(ctx, in)
	require.Error(t, err)
	row, err = q.WithContext(ctx).Where(q.Kind.Eq("API")).Take()
	require.NoError(t, err)
	require.Equal(t, 1, row.ReceivedCount)
	require.Equal(t, "2026-10-20", *row.ReceivingDay)
	var failedEffects int64
	require.NoError(t, db.Table("test_effects").Where("kind = ?", "failed-receiving-effect").Count(&failedEffects).Error)
	require.Zero(t, failedEffects)
}

func TestIntakeReceivingAndRotationLockTriageBeforeActorAndRecheckReplay(t *testing.T) {
	m, db, ctx := workHarness(t)
	agent := guid.ID(900)
	settings := intakeSettingsFixture()
	settings.Enabled, settings.TriageAgentID = true, &agent
	m.projects.ValidateTriageAgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 0, settings)
		return err
	}))
	var order []guid.ID
	locked := map[guid.ID]bool{}
	m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error {
		if locked[id] {
			return nil
		}
		if len(order) > 0 && id < order[len(order)-1] {
			return errors.New("participant lock inversion")
		}
		order = append(order, id)
		locked[id] = true
		return nil
	}
	valid := true
	m.projects.ValidateTriageAgentOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
		require.Equal(t, agent, id)
		// The real delivery participant helper locks sorted [agent principal=2,
		// current Human=11]. Rejecting inversion proves the old Human-first
		// receive/rotate sequence cannot pass this assertion.
		for _, principal := range []guid.ID{2, 11} {
			if err := m.principals.Lock(ctx, tx, principal); err != nil {
				return err
			}
		}
		if !valid {
			return ErrForbidden
		}
		return nil
	}
	path := WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}
	issued, err := m.rotateIntakeCredentialHTTP(ctx, &intakeCredentialInput{WorkProjectPath: path, WriteHeaders: WriteHeaders{IdempotencyKey: "lock-order-rotation", IfMatch: `"1"`}})
	require.NoError(t, err)
	require.Equal(t, []guid.ID{2, 11}, order)
	order, locked = nil, map[guid.ID]bool{}
	m.intakeReceiving.ReceiveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, intake.APIReceiptInput) (intake.IntakeLedgerEntry, error) {
		return intake.IntakeLedgerEntry{ID: 456, ProjectID: 99, Version: 1}, nil
	}
	in := &intakeReceivingInput{WorkProjectPath: path, WriteHeaders: WriteHeaders{IdempotencyKey: "lock-order-receiving"}, Credential: issued.Body.Data.Credential, Body: intake.APIReceiptInput{ContactID: 1, Title: "Source", Content: "Original", Format: "PLAIN"}}
	_, err = m.receiveIntakeHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{2, 11}, order)
	order, locked = nil, map[guid.ID]bool{}
	valid = false
	_, err = m.receiveIntakeHTTP(ctx, in)
	require.Error(t, err, "replay must still validate the current triage participant")
	require.Equal(t, []guid.ID{2, 11}, order)
}

func TestIntakeReceivingRejectsPolicyChangedAfterParticipantDiscovery(t *testing.T) {
	m, db, ctx := workHarness(t)
	settings := intakeSettingsFixture()
	settings.Enabled = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 0, settings)
		return err
	}))
	var secret = strings.Repeat("c", 64)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		row, err := m.intakeAPIChannelOn(ctx, tx, 1, 99)
		if err != nil {
			return err
		}
		hash := credentialDigest(secret)
		row.CredentialHash = &hash
		return query.BindIntakeChannels(tx, m.names).WithContext(ctx).Save(row)
	}))
	// Inject the competing policy revision at the exact point between the
	// discovery read and current project/channel fences, without a fake success.
	m.projects.AuthorizeOn = func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _ string) error {
		q := query.BindIntakeChannels(tx, m.names)
		row, err := q.WithContext(ctx).Where(q.Kind.Eq("API")).Take()
		if err != nil {
			return err
		}
		row.Version++
		return q.WithContext(ctx).Save(row)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.authorizeIntakeReceivingOn(ctx, tx, 1, 99, secret) }), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.authorizeIntakeAPIOn(ctx, tx, 1, 99, "intake_channel_manage")
		return err
	}), ErrConflict)
}

func TestIntakeReceivingFailureMappingAndCredentialPersistenceAreAtomic(t *testing.T) {
	for _, mode := range []string{"unbound", "invalid", "forbidden", "unavailable", "credential_save", "stale_rotation"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			settings := intakeSettingsFixture()
			settings.Enabled = true
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 0, settings); return e }))
			rotation := &intakeCredentialInput{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}, WriteHeaders: WriteHeaders{IdempotencyKey: "issue-credential", IfMatch: `"1"`}}
			issued, e := m.rotateIntakeCredentialHTTP(ctx, rotation)
			require.NoError(t, e)
			if mode == "credential_save" {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("credential_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.IntakeChannels.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Create().Remove("credential_storage")
			}
			if mode == "credential_save" || mode == "stale_rotation" {
				rotation.IdempotencyKey = "rotate-second"
				if mode == "credential_save" {
					rotation.IfMatch = `"2"`
				}
				_, e = m.rotateIntakeCredentialHTTP(ctx, rotation)
				require.Error(t, e)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					return m.authorizeIntakeReceivingOn(ctx, tx, 1, 99, issued.Body.Data.Credential)
				}))
				return
			}
			want := 503
			sourceErr := intake.ErrUnavailable
			switch mode {
			case "invalid":
				sourceErr = intake.ErrInvalidArgument
				want = 422
			case "forbidden":
				sourceErr = intake.ErrForbidden
				want = 403
			}
			if mode != "unbound" {
				m.intakeReceiving.ReceiveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, intake.APIReceiptInput) (intake.IntakeLedgerEntry, error) {
					return intake.IntakeLedgerEntry{}, sourceErr
				}
			}
			_, e = m.receiveIntakeHTTP(ctx, &intakeReceivingInput{WorkProjectPath: rotation.WorkProjectPath, WriteHeaders: WriteHeaders{IdempotencyKey: "receive-failed"}, Credential: issued.Body.Data.Credential, Body: intake.APIReceiptInput{ContactID: 1, Title: "Actual", Content: "content", Format: "PLAIN"}})
			requireStatus(t, e, want)
			row, e := query.BindIntakeChannels(db, m.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Zero(t, row.ReceivedCount)
			require.Nil(t, row.ReceivingDay)
		})
	}
	m, db, ctx := workHarness(t)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.authorizeIntakeAPIOn(ctx, tx, 1, 99, "intake_submit"); return e }))
	_, e := m.authorizeIntakeAPIOn(ctx, nil, 1, 99, "intake_submit")
	require.Error(t, e)
	_, e = m.authorizeIntakeAPIOn(ctx, db, 1, 99, "intake_submit")
	require.Error(t, e)
	_, e = m.authorizeIntakeAPIOn(context.Background(), db, 1, 99, "intake_submit")
	require.Error(t, e)
	_, e = m.rotateIntakeCredentialHTTP(ctx, &intakeCredentialInput{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "0"}}})
	require.Error(t, e)
	_, e = m.rotateIntakeCredentialHTTP(ctx, &intakeCredentialInput{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "1"}, ProjectID: "99"}})
	require.Error(t, e)
	_, e = m.receiveIntakeHTTP(ctx, &intakeReceivingInput{WorkProjectPath: WorkProjectPath{TenantInput: TenantInput{TenantID: "0"}}})
	require.Error(t, e)
}

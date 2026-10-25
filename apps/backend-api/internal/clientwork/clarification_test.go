package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestClarificationRequestDependencyFailuresRollbackSourceAndRound(t *testing.T) {
	m, db, _, clients := clarificationHarness(t)
	ctx := testContext(t, 2)
	failure := errors.New("clarification dependency failed")
	for _, stage := range []string{"invalid", "blank_question", "missing_source", "recipient_revoked", "recipient_lookup", "recipient_inactive", "source_error", "count_error", "source_transition", "timing_error", "create_error"} {
		t.Run(stage, func(t *testing.T) {
			copyModule := *m
			body := clarificationRequest().Body
			want := failure
			switch stage {
			case "invalid":
				body.IntakeID = 0
				want = ErrInvalid
			case "blank_question":
				body.Questions = []string{" "}
				want = ErrInvalid
			case "missing_source":
				copyModule.intakeSources.SourceOn = nil
				want = ErrUnavailable
			case "recipient_revoked":
				clients.revoked = true
				defer func() { clients.revoked = false }()
				want = ErrNotFound
			case "recipient_lookup":
				original := copyModule.principals.ActiveHumanByID
				copyModule.principals.ActiveHumanByID = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if id == 4 {
						return identity.Principal{}, false, failure
					}
					return original(ctx, tx, id)
				}
			case "recipient_inactive":
				original := copyModule.principals.ActiveHumanByID
				copyModule.principals.ActiveHumanByID = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if id == 4 {
						return identity.Principal{}, false, nil
					}
					return original(ctx, tx, id)
				}
				want = ErrNotFound
			case "source_error":
				copyModule.intakeSources.SourceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.IntakeLedgerSource, error) {
					return intake.IntakeLedgerSource{}, failure
				}
			case "count_error":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_clarification", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ClarificationRound.String() {
						tx.AddError(failure)
					}
				}))
			case "source_transition":
				copyModule.intakeSources.ClarificationOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, guid.ID, intake.ClarificationAction) (int, error) {
					return 0, failure
				}
			case "timing_error":
				copyModule.intakeSources.TimingOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.ClarificationTiming, bool, error) {
					return intake.ClarificationTiming{}, false, failure
				}
			case "create_error":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("regression_clarification", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ClarificationRound.String() {
						tx.AddError(failure)
					}
				}))
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := copyModule.requestClarificationOn(ctx, tx, 3, body); return err })
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Query().Remove("regression_clarification"))
			require.NoError(t, db.Callback().Create().Remove("regression_clarification"))
			var source struct {
				Status      string
				Version     int
				OpenRoundID *guid.ID
			}
			require.NoError(t, db.Table("g_intake_records").Where("id=50").Take(&source).Error)
			require.Equal(t, "RECEIVED", source.Status)
			require.Equal(t, 1, source.Version)
			require.Nil(t, source.OpenRoundID)
			var count int64
			require.NoError(t, db.Table(m.names.ClarificationRound.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestClarificationAnswerAndCancellationCASFailuresRollbackTogether(t *testing.T) {
	for _, action := range []string{"answer", "cancel"} {
		for _, stage := range []string{"missing_transition", "transition_error", "update_error", "cas"} {
			t.Run(action+"/"+stage, func(t *testing.T) {
				m, db, _, _ := clarificationHarness(t)
				opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
				require.NoError(t, err)
				failure := errors.New("clarification update failed")
				want := failure
				switch stage {
				case "missing_transition":
					m.intakeSources.ClarificationOn = nil
					want = ErrUnavailable
				case "transition_error":
					m.intakeSources.ClarificationOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, guid.ID, intake.ClarificationAction) (int, error) {
						return 0, failure
					}
				case "update_error":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("regression_round_update", func(tx *gorm.DB) {
						if tx.Statement.Table == m.names.ClarificationRound.String() {
							tx.AddError(failure)
						}
					}))
				case "cas":
					require.NoError(t, db.Callback().Update().After("gorm:update").Register("regression_round_update", func(tx *gorm.DB) {
						if tx.Statement.Table == m.names.ClarificationRound.String() {
							tx.RowsAffected = 0
						}
					}))
					want = ErrConflict
				}
				err = db.Transaction(func(tx *gorm.DB) error {
					if action == "cancel" {
						_, err := m.cancelClarificationOn(testContext(t, 2), tx, 3, opened.Body.Data.ID, 1, "Questions withdrawn")
						return err
					}
					_, err := m.answerClarificationOn(testContext(t, 4), tx, 3, opened.Body.Data.ID, 1, ClarificationAnswer{QuestionNo: 1, Answer: "CSV"})
					return err
				})
				require.ErrorIs(t, err, want)
				require.NoError(t, db.Callback().Update().Remove("regression_round_update"))
				var round model.ClarificationRound
				require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", opened.Body.Data.ID).Take(&round).Error)
				require.Equal(t, 1, round.Version)
				require.Equal(t, "OPEN", round.State)
				require.NotContains(t, round.Questions, "CSV")
				var source struct {
					Status  string
					Version int
				}
				require.NoError(t, db.Table("g_intake_records").Where("id=50").Take(&source).Error)
				require.Equal(t, "CLARIFYING", source.Status)
				require.Equal(t, 2, source.Version)
			})
		}
	}
}

func clarificationHarness(t *testing.T) (*Module, *gorm.DB, *testProjects, *testClients) {
	t.Helper()
	m, db, projects, clients := harness(t)
	realIntakeSources(t, m, db)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Updates(map[string]any{"status": "RECEIVED", "kind": "NEW", "version": 1, "raw_content": "private original source"}).Error)
	return m, db, projects, clients
}
func clarificationRequest() *CreateInput[ClarificationRequest] {
	return &CreateInput[ClarificationRequest]{EntityID: "3", Key: "request-clarification", Body: ClarificationRequest{IntakeID: 50, IntakeVersion: 1, RecipientID: 4, Questions: []string{"Which export format?", "Which timezone?"}}}
}

func TestClarificationPartialAnswerCompletionReplayAndPrivacy(t *testing.T) {
	m, db, _, clients := clarificationHarness(t)
	request := clarificationRequest()
	opened, err := m.requestClarificationHTTP(testContext(t, 2), request)
	require.NoError(t, err)
	replay, err := m.requestClarificationHTTP(testContext(t, 2), request)
	require.NoError(t, err)
	require.Equal(t, opened.Body.Data, replay.Body.Data)
	require.Equal(t, "true", replay.Replayed)
	id := opened.Body.Data.ID.String()
	source, err := m.getIntakeLedgerHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "50"})
	require.NoError(t, err)
	require.Equal(t, "CLARIFYING", source.Body.Data.State)
	view, err := m.getClarificationHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: id})
	require.NoError(t, err)
	require.Equal(t, "IN_APP", view.Body.Data.AvailableVia)
	require.Len(t, view.Body.Data.Questions, 2)
	raw, err := json.Marshal(view.Body.Data)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private original")
	require.NotContains(t, string(raw), "raw_content")
	require.NotContains(t, string(raw), "triage")
	_, err = m.getClarificationHTTP(testContext(t, 5), &ObjectInput{EntityID: "3", ID: id})
	require.Error(t, err)
	first := &EditInput[ClarificationAnswer]{EntityID: "3", ID: id, IfMatch: `"1"`, Key: "answer-first", Body: ClarificationAnswer{QuestionNo: 1, Answer: "CSV"}}
	// T3 allows a triage revision while a round is open. Answers belong to the
	// round, so this must not invalidate the recipient's outstanding questions.
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Updates(map[string]any{"version": 3, "client_unresponsive": true}).Error)
	originalAudit := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error {
		return errors.New("answer audit unavailable")
	}
	_, err = m.answerClarificationHTTP(testContext(t, 4), first)
	require.Error(t, err)
	var rolledBack struct {
		Version              int
		ClientUnresponsive   bool
		LastClientActivityAt *time.Time
	}
	require.NoError(t, db.Table("g_intake_records").Select("version,client_unresponsive,last_client_activity_at").Where("id=50").Take(&rolledBack).Error)
	require.Equal(t, 3, rolledBack.Version)
	require.True(t, rolledBack.ClientUnresponsive)
	require.Nil(t, rolledBack.LastClientActivityAt)
	m.audit = originalAudit
	answered, err := m.answerClarificationHTTP(testContext(t, 4), first)
	require.NoError(t, err)
	require.Equal(t, "OPEN", answered.Body.Data.State)
	var activity struct {
		Version              int
		Status               string
		ClientUnresponsive   bool
		LastClientActivityAt *time.Time
	}
	require.NoError(t, db.Table("g_intake_records").Select("version,status,client_unresponsive,last_client_activity_at").Where("id=50").Take(&activity).Error)
	require.Equal(t, 4, activity.Version)
	require.Equal(t, "CLARIFYING", activity.Status)
	require.False(t, activity.ClientUnresponsive)
	require.NotNil(t, activity.LastClientActivityAt)
	replay, err = m.answerClarificationHTTP(testContext(t, 4), first)
	require.NoError(t, err)
	require.Equal(t, answered.Body.Data, replay.Body.Data)
	stale := *first
	stale.Key = "stale-answer"
	stale.Body.QuestionNo = 2
	_, err = m.answerClarificationHTTP(testContext(t, 4), &stale)
	require.Error(t, err)
	last := &EditInput[ClarificationAnswer]{EntityID: "3", ID: id, IfMatch: `"2"`, Key: "answer-last", Body: ClarificationAnswer{QuestionNo: 2, Answer: "UTC"}}
	closed, err := m.answerClarificationHTTP(testContext(t, 4), last)
	require.NoError(t, err)
	require.Equal(t, "ANSWERED", closed.Body.Data.State)
	source, err = m.getIntakeLedgerHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "50"})
	require.NoError(t, err)
	require.Equal(t, "TRIAGED", source.Body.Data.State)
	require.Equal(t, 5, source.Body.Data.Version)
	require.Equal(t, "private original source", *source.Body.Data.RawContent)
	var round *guid.ID
	require.NoError(t, db.Table("g_intake_records").Select("open_round_id").Where("id=50").Scan(&round).Error)
	require.Nil(t, round)
	clients.revoked = true
	_, err = m.answerClarificationHTTP(testContext(t, 4), last)
	require.Error(t, err, "replay cannot restore revoked project membership")
}

func TestClarificationRollbackAndCurrentAuthority(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	original := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.Error(t, err)
	var n int64
	require.NoError(t, db.Table(m.names.ClarificationRound.String()).Count(&n).Error)
	require.Zero(t, n)
	var state string
	require.NoError(t, db.Table("g_intake_records").Select("status").Where("id=50").Scan(&state).Error)
	require.Equal(t, "RECEIVED", state)
	m.audit = original
	opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, err)
	id := opened.Body.Data.ID.String()
	_, err = m.requestClarificationHTTP(testContext(t, 2), &CreateInput[ClarificationRequest]{EntityID: "3", Key: "second-round", Body: clarificationRequest().Body})
	require.Error(t, err, "only one open round")
	last := &EditInput[ClarificationAnswer]{EntityID: "3", ID: id, IfMatch: `"1"`, Key: "first", Body: ClarificationAnswer{QuestionNo: 1, Answer: "CSV"}}
	_, err = m.answerClarificationHTTP(testContext(t, 2), last)
	require.Error(t, err, "team cannot impersonate client answer")
	_, err = m.answerClarificationHTTP(testContext(t, 5), last)
	require.Error(t, err, "other current client cannot answer recipient round")
	_, err = m.answerClarificationHTTP(testContext(t, 4), last)
	require.NoError(t, err)
	last.IfMatch = `"2"`
	last.Key = "final"
	last.Body.QuestionNo = 2
	last.Body.Answer = "UTC"
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err = m.answerClarificationHTTP(testContext(t, 4), last)
	require.Error(t, err)
	require.NoError(t, db.Table("g_intake_records").Select("status").Where("id=50").Scan(&state).Error)
	require.Equal(t, "CLARIFYING", state)
	m.audit = original
	view, err := m.getClarificationHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: id})
	require.NoError(t, err)
	require.Empty(t, view.Body.Data.Questions[1].Answer)
	require.Equal(t, 2, view.Body.Data.Version)
	projects.deny = true
	_, err = m.answerClarificationHTTP(testContext(t, 4), last)
	require.Error(t, err)
	_, err = m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.Error(t, err, "revocation checked before original request replay")
}

func TestClarificationCancellationPreservesAnswersWithdrawsOutstandingAndRechecksReplay(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, err)
	id := opened.Body.Data.ID.String()
	_, err = m.answerClarificationHTTP(testContext(t, 4), &EditInput[ClarificationAnswer]{EntityID: "3", ID: id, IfMatch: `"1"`, Key: "partial-before-cancel", Body: ClarificationAnswer{QuestionNo: 1, Answer: "CSV"}})
	require.NoError(t, err)
	cancel := &EditInput[ClarificationCancellation]{EntityID: "3", ID: id, IfMatch: `"2"`, Key: "cancel-round", Body: ClarificationCancellation{Reason: "The remaining question is no longer needed."}}
	for i, reason := range []string{"", "   ", strings.Repeat("\u754c", 2001)} {
		invalid := *cancel
		invalid.Key = fmt.Sprintf("invalid-cancel-%d", i)
		invalid.Body.Reason = reason
		_, err = m.cancelClarificationHTTP(testContext(t, 2), &invalid)
		require.Error(t, err, "cancellation requires a bounded nonblank reason")
	}
	_, err = m.cancelClarificationHTTP(testContext(t, 4), cancel)
	require.Error(t, err, "client cannot cancel a team round")
	stale := *cancel
	stale.IfMatch = `"1"`
	_, err = m.cancelClarificationHTTP(testContext(t, 2), &stale)
	require.Error(t, err)
	publisher := m.events
	m.events = failPublisher{}
	_, err = m.cancelClarificationHTTP(testContext(t, 2), cancel)
	require.Error(t, err)
	m.events = publisher
	var source struct {
		Status               string
		Version              int
		OpenRoundID          *guid.ID
		LastClientActivityAt *time.Time
	}
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Take(&source).Error)
	require.Equal(t, "CLARIFYING", source.Status)
	require.Equal(t, 3, source.Version)
	require.NotNil(t, source.OpenRoundID)
	clientActivity := source.LastClientActivityAt
	closed, err := m.cancelClarificationHTTP(testContext(t, 2), cancel)
	require.NoError(t, err)
	require.Equal(t, "CANCELLED", closed.Body.Data.State) //nolint:misspell // Frozen domain state.
	replayed, err := m.cancelClarificationHTTP(testContext(t, 2), cancel)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, closed.Body.Data, replayed.Body.Data)
	view, err := m.getClarificationHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: id})
	require.NoError(t, err)
	require.NotNil(t, view.Body.Data.ClosedAt)
	require.Equal(t, cancel.Body.Reason, *view.Body.Data.CloseNote)
	require.Equal(t, "CANCELLED_BY_TEAM", *view.Body.Data.CloseReason) //nolint:misspell // Frozen close reason.
	require.Equal(t, "CSV", view.Body.Data.Questions[0].Answer)
	require.Equal(t, "ANSWERED", view.Body.Data.Questions[0].State)
	require.Equal(t, "WITHDRAWN", view.Body.Data.Questions[1].State)
	source.OpenRoundID = nil
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Take(&source).Error)
	require.Equal(t, "TRIAGED", source.Status)
	require.Equal(t, 4, source.Version)
	require.Nil(t, source.OpenRoundID)
	require.Equal(t, clientActivity, source.LastClientActivityAt, "team cancellation is not client activity")
	_, err = m.answerClarificationHTTP(testContext(t, 4), &EditInput[ClarificationAnswer]{EntityID: "3", ID: id, IfMatch: `"3"`, Key: "late-answer", Body: ClarificationAnswer{QuestionNo: 2, Answer: "UTC"}})
	require.Error(t, err)
	projects.deny = true
	_, err = m.cancelClarificationHTTP(testContext(t, 2), cancel)
	require.Error(t, err, "revoked authority cannot replay cancellation")
}

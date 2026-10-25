package clientwork

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntakeThreadLinkRejectsChangingSourceAndRollsBackPublicationFailure(t *testing.T) {
	for _, stage := range []string{"client_read", "revoked", "source_changed", "stale_version", "existing", "read", "create", "event"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _, clients := clarificationHarness(t)
			ctx := testContext(t, 2)
			require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
			m.intakeConversation.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string) error { return nil }
			in := &EditInput[IntakeThreadLink]{EntityID: "3", ID: "50", IfMatch: `"1"`, Key: "link-failure", Body: IntakeThreadLink{ChannelID: 70, RootMessageID: 71, RecipientID: 4}}
			failure := errors.New("thread link dependency unavailable")
			if stage == "existing" {
				require.NoError(t, db.Table(m.names.IntakeThread.String()).Create(&model.IntakeThread{TenantID: 1, ProjectID: 3, IntakeID: 50, ChannelID: 70, RootMessageID: 71, RecipientID: 4, CreatedBy: 2, CreatedAt: now()}).Error)
			}
			before := collaborationSnapshot(t, m, db)
			switch stage {
			case "client_read":
				clients.clientErr = failure
			case "revoked":
				clients.revoked = true
			case "source_changed":
				original := m.intakeSources.SourceOn
				calls := 0
				m.intakeSources.SourceOn = func(ctx context.Context, tx *gorm.DB, a, b, c guid.ID) (intake.IntakeLedgerSource, error) {
					calls++
					if calls > 1 {
						return intake.IntakeLedgerSource{}, failure
					}
					return original(ctx, tx, a, b, c)
				}
			case "stale_version":
				in.IfMatch = `"99"`
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_link", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.IntakeThread.String() {
						tx.AddError(failure)
					}
				}))
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("regression_link", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.IntakeThread.String() {
						tx.AddError(failure)
					}
				}))
			case "event":
				require.NoError(t, db.Exec("CREATE TRIGGER fail_thread_event BEFORE INSERT ON g_domain_events BEGIN SELECT RAISE(ABORT,'outbox unavailable'); END").Error)
			}
			out, err := m.linkIntakeThreadHTTP(ctx, in)
			require.Error(t, err)
			require.Nil(t, out)
			require.NoError(t, db.Callback().Query().Remove("regression_link"))
			require.NoError(t, db.Callback().Create().Remove("regression_link"))
			require.Equal(t, before, collaborationSnapshot(t, m, db))
		})
	}
}

func TestIntakeConversationHistoryUsesCurrentContactAndPropagatesReadFailure(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	ctx := testContext(t, 2)
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
	require.NoError(t, db.Table(m.names.IntakeThread.String()).Create(&model.IntakeThread{TenantID: 1, ProjectID: 3, IntakeID: 50, ChannelID: 70, RootMessageID: 71, RecipientID: 4, CreatedBy: 2, CreatedAt: now()}).Error)
	m.intakeConversation.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string) error { return nil }
	called := 0
	failRead := false
	m.intakeConversation.ReadOn = func(_ context.Context, tx *gorm.DB, tenant, project, channel, root, recipient guid.ID, hash, cursor string) (collab.CursorPage[collab.MessageView], error) {
		called++
		require.Implements(t, (*gorm.TxCommitter)(nil), tx.Statement.ConnPool)
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		require.EqualValues(t, 70, channel)
		require.EqualValues(t, 71, root)
		require.EqualValues(t, 4, recipient)
		require.Equal(t, strings.Repeat("a", 64), hash)
		require.Equal(t, "actual-thread-cursor", cursor)
		if failRead {
			return collab.CursorPage[collab.MessageView]{}, errors.New("thread read failed")
		}
		return collab.CursorPage[collab.MessageView]{Items: []collab.MessageView{}}, nil
	}
	in := &ObjectInput{EntityID: "3", ID: "50", ReplyCursor: "actual-thread-cursor"}
	view, err := m.listIntakeThreadMessagesHTTP(ctx, in)
	require.NoError(t, err)
	require.Empty(t, view.Body.Data.Items)
	failRead = true
	view, err = m.listIntakeThreadMessagesHTTP(ctx, in)
	require.Error(t, err)
	require.Nil(t, view)
	require.Equal(t, 2, called)
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 5).Error)
	view, err = m.listIntakeThreadMessagesHTTP(ctx, in)
	require.Error(t, err)
	require.Nil(t, view)
	require.Equal(t, 2, called, "revoked original contact must be fenced before invoking channel history")
	_, err = m.getIntakeThreadHTTP(ctx, in)
	require.Error(t, err)
	// A linked source cannot quietly drop its outgoing message capability.
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
	err = db.Transaction(func(tx *gorm.DB) error { return m.postLinkedIntakeMessageOn(ctx, tx, 3, 50, "followup") })
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestIntakeConversationHTTPBoundaryAndUnavailableReads(t *testing.T) {
	m, _, projects, _ := clarificationHarness(t)
	ctx := testContext(t, 2)
	for _, read := range []func(*ObjectInput) error{
		func(in *ObjectInput) error { _, err := m.getIntakeThreadHTTP(ctx, in); return err },
		func(in *ObjectInput) error { _, err := m.listIntakeThreadMessagesHTTP(ctx, in); return err },
		func(in *ObjectInput) error { _, err := m.clientMergeHTTP(ctx, in); return err },
	} {
		for _, in := range []*ObjectInput{{EntityID: "bad", ID: "50"}, {EntityID: "3", ID: "bad"}} {
			var status huma.StatusError
			require.ErrorAs(t, read(in), &status)
			require.Equal(t, 422, status.GetStatus())
		}
		runtime := m.runtime
		m.runtime = nil
		var status huma.StatusError
		require.ErrorAs(t, read(&ObjectInput{EntityID: "3", ID: "50"}), &status)
		require.Equal(t, 503, status.GetStatus())
		m.runtime = runtime
	}
	m.intakeConversation.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string, string) (collab.CursorPage[collab.MessageView], error) {
		return collab.CursorPage[collab.MessageView]{}, nil
	}
	projects.deny = true
	_, err := m.listIntakeThreadMessagesHTTP(ctx, &ObjectInput{EntityID: "3", ID: "50"})
	require.Error(t, err)
	projects.deny = false
	_, err = m.listIntakeThreadMessagesHTTP(ctx, &ObjectInput{EntityID: "3", ID: "50"})
	require.Error(t, err, "unlinked source cannot expose arbitrary channel history")
	_, err = m.getIntakeThreadHTTP(ctx, &ObjectInput{EntityID: "3", ID: "50"})
	require.Error(t, err)
	_, err = m.linkIntakeThreadHTTP(ctx, &EditInput[IntakeThreadLink]{EntityID: "bad"})
	require.Error(t, err)
	_, err = m.replyIntakeThreadHTTP(ctx, &EditInput[IntakeThreadMessage]{EntityID: "bad"})
	require.Error(t, err)
	_, err = m.forwardMergedReplyHTTP(ctx, &EditInput[MergedReplyBody]{EntityID: "bad"})
	require.Error(t, err)
}

func TestIntakeConversationReplyDependencyFailuresLeaveNoSuccessReceipt(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	ctx := testContext(t, 2)
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Updates(map[string]any{"status": "CONVERTED", "requirement_id": 100}).Error)
	require.NoError(t, db.Table(m.names.IntakeThread.String()).Create(&model.IntakeThread{TenantID: 1, ProjectID: 3, IntakeID: 50, ChannelID: 70, RootMessageID: 71, RecipientID: 4, CreatedBy: 2, CreatedAt: now()}).Error)
	failure := errors.New("current conversation dependency failed")
	for _, stage := range []string{"invalid_link", "missing_contact", "contact_error", "source_error", "channel_error", "owner_missing", "owner_error", "requirement_missing", "reply_missing", "reply_error", "stale_source", "blank_body", "fence_missing"} {
		t.Run(stage, func(t *testing.T) {
			copyModule := *m
			work := guid.ID(90)
			in := &EditInput[IntakeThreadMessage]{EntityID: "3", ID: "50", IfMatch: `"1"`, Key: "reply-failure-" + stage, Body: IntakeThreadMessage{Body: "Which format?", WorkItemID: &work, WorkVersion: 1}}
			copyModule.intakeConversation.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string) error { return nil }
			copyModule.intakeConversation.ReplyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string, string) (guid.ID, error) {
				t.Fatal("failed request must not post a message")
				return 0, nil
			}
			copyModule.intakeConversation.BindWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return nil }
			copyModule.intakeWorkFence = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error { return nil }
			copyModule.intakeWorkOwner = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) (guid.ID, error) {
				return 2, nil
			}
			want := 500
			switch stage {
			case "invalid_link":
				_, err := copyModule.validateIntakeThreadOn(ctx, db, 3, 50, IntakeThreadLink{})
				require.ErrorIs(t, err, ErrInvalid)
				return
			case "missing_contact":
				copyModule.intakeSources.RecipientOn = nil
				want = 503
			case "contact_error":
				copyModule.intakeSources.RecipientOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return failure }
			case "source_error":
				copyModule.intakeSources.SourceOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.IntakeLedgerSource, error) {
					return intake.IntakeLedgerSource{}, failure
				}
			case "channel_error":
				copyModule.intakeConversation.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string) error {
					return failure
				}
			case "owner_missing":
				copyModule.intakeWorkOwner = nil
				want = 503
			case "owner_error":
				copyModule.intakeWorkOwner = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) (guid.ID, error) {
					return 0, failure
				}
			case "requirement_missing":
				original := copyModule.intakeSources.SourceOn
				copyModule.intakeSources.SourceOn = func(ctx context.Context, tx *gorm.DB, a, b, c guid.ID) (intake.IntakeLedgerSource, error) {
					row, err := original(ctx, tx, a, b, c)
					row.RequirementID = nil
					return row, err
				}
				want = 409
			case "reply_missing":
				in.Body.WorkItemID = nil
				in.Body.WorkVersion = 0
				copyModule.intakeConversation.ReplyOn = nil
				want = 503
			case "reply_error":
				copyModule.intakeConversation.ReplyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string, string) (guid.ID, error) {
					return 0, failure
				}
			case "stale_source":
				in.IfMatch = `"99"`
				want = 409
			case "blank_body":
				in.Body.Body = " "
				want = 422
			case "fence_missing":
				copyModule.intakeWorkFence = nil
				want = 503
			}
			before := collaborationSnapshot(t, m, db)
			out, err := copyModule.replyIntakeThreadHTTP(ctx, in)
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, want, status.GetStatus())
			require.Nil(t, out)
			require.Equal(t, before, collaborationSnapshot(t, m, db), "failed replies must not publish audit, outbox or idempotency success")
		})
	}
}

type intakeWorkFenceProjects struct {
	*testProjects
	t      *testing.T
	fenced *bool
}

func (p *intakeWorkFenceProjects) LockProjectOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
	require.True(p.t, *p.fenced, "work participant locks must precede the clientwork project lock")
	return p.testProjects.LockProjectOn(ctx, tx, tenant, project, permission)
}

func TestIntakeWorkOwnerQuestionUsesOriginalThreadAndAtomicCanonicalBinding(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Updates(map[string]any{"status": "CONVERTED", "requirement_id": 100}).Error)
	require.NoError(t, db.Table(m.names.IntakeThread.String()).Create(&model.IntakeThread{TenantID: 1, ProjectID: 3, IntakeID: 50, ChannelID: 70, RootMessageID: 71, RecipientID: 4, CreatedBy: 2, CreatedAt: now()}).Error)
	require.NoError(t, db.Exec("CREATE TABLE owner_question_probe(kind TEXT)").Error)
	m.intakeConversation.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string) error { return nil }
	m.intakeConversation.ReplyOn = func(_ context.Context, tx *gorm.DB, tenant, project, channel, root, recipient guid.ID, hash, body string) (guid.ID, error) {
		require.Equal(t, guid.ID(71), root)
		require.Equal(t, guid.ID(4), recipient)
		require.Equal(t, "Which CSV columns should this work include?", body)
		return 81, tx.Exec("INSERT INTO owner_question_probe VALUES ('message')").Error
	}
	owner := guid.ID(2)
	fenced := false
	m.projects = &intakeWorkFenceProjects{testProjects: projects, t: t, fenced: &fenced}
	m.intakeWorkFence = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error { fenced = true; return nil }
	m.intakeWorkOwner = func(_ context.Context, _ *gorm.DB, tenant, project, requirement, work guid.ID, version int) (guid.ID, error) {
		require.True(t, fenced, "canonical participant locks precede source/owner validation")
		require.Equal(t, guid.ID(100), requirement)
		require.Equal(t, guid.ID(90), work)
		require.Equal(t, 3, version)
		return owner, nil
	}
	bindError := errors.New("canonical work binding unavailable")
	failBinding := true
	m.intakeConversation.BindWorkOn = func(_ context.Context, tx *gorm.DB, tenant, project, message, work guid.ID) error {
		if failBinding {
			return bindError
		}
		require.Equal(t, guid.ID(81), message)
		require.Equal(t, guid.ID(90), work)
		return tx.Exec("INSERT INTO owner_question_probe VALUES ('binding')").Error
	}
	work := guid.ID(90)
	in := &EditInput[IntakeThreadMessage]{EntityID: "3", ID: "50", IfMatch: `"1"`, Key: "owner-original-thread-question", Body: IntakeThreadMessage{Body: "Which CSV columns should this work include?", WorkItemID: &work, WorkVersion: 3}}
	_, err := m.replyIntakeThreadHTTP(testContext(t, 2), in)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table("owner_question_probe").Count(&count).Error)
	require.Zero(t, count, "failed binding rolls back the posted question and leaves the retry key usable")
	failBinding = false
	first, err := m.replyIntakeThreadHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, guid.ID(81), first.Body.Data.ID)
	replay, err := m.replyIntakeThreadHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.NoError(t, db.Table("owner_question_probe").Count(&count).Error)
	require.EqualValues(t, 2, count)
	originalAudit := m.audit
	m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		if event.EventType == "clientwork.intake_thread_work_bound" {
			return errors.New("followup binding audit unavailable")
		}
		return originalAudit(ctx, tx, event)
	}
	followup := *in
	followup.Key = "owner-followup-audit-outage"
	_, err = m.replyIntakeThreadHTTP(testContext(t, 2), &followup)
	require.Error(t, err)
	m.audit = originalAudit
	require.NoError(t, db.Table("owner_question_probe").Count(&count).Error)
	require.EqualValues(t, 2, count, "a followup without its durable message/work audit must roll back the entire posted question")
	fenceOutage := errors.New("current work participants unavailable")
	m.intakeWorkFence = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error {
		return fenceOutage
	}
	fenced = false
	_, err = m.replyIntakeThreadHTTP(testContext(t, 2), in)
	require.Error(t, err, "participant source outage must refuse even a previously committed receipt before taking the project lock")
	require.NoError(t, db.Table("owner_question_probe").Count(&count).Error)
	require.EqualValues(t, 2, count, "refused replay cannot post another question or binding")
	m.intakeWorkFence = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error { fenced = true; return nil }
	owner = 5
	_, err = m.replyIntakeThreadHTTP(testContext(t, 2), in)
	require.Error(t, err, "owner change must be checked before replaying an old question receipt")
	in.Body.WorkVersion = 0
	_, err = m.replyIntakeThreadHTTP(testContext(t, 2), in)
	require.Error(t, err, "work binding always names the exact current work version")
}

func TestIntakeThreadLinksActualContactReplaysAndPublishesClarificationAtomically(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
	require.NoError(t, db.Exec("CREATE TABLE source_post_probe (id BIGINT, body TEXT)").Error)
	allowed := true
	m.intakeConversation = collab.IntakeConversation{
		CheckOn: func(_ context.Context, tx *gorm.DB, tenant, project, channel, root, recipient guid.ID, hash string) error {
			require.Equal(t, guid.ID(1), tenant)
			require.Equal(t, guid.ID(3), project)
			require.Equal(t, guid.ID(70), channel)
			require.Equal(t, guid.ID(71), root)
			require.Equal(t, guid.ID(4), recipient)
			require.Equal(t, strings.Repeat("a", 64), hash)
			if !allowed {
				return collab.ErrNotFound
			}
			return nil
		},
		ReplyOn: func(_ context.Context, tx *gorm.DB, tenant, project, channel, root, recipient guid.ID, hash, body string) (guid.ID, error) {
			require.Equal(t, guid.ID(71), root)
			require.Equal(t, guid.ID(4), recipient)
			id, err := m.nextID()
			if err != nil {
				return 0, err
			}
			return id, tx.Exec("INSERT INTO source_post_probe(id,body) VALUES (?,?)", id, body).Error
		},
	}
	in := &EditInput[IntakeThreadLink]{EntityID: "3", ID: "50", IfMatch: `"1"`, Key: "link-original-thread", Body: IntakeThreadLink{ChannelID: 70, RootMessageID: 71, RecipientID: 4}}
	first, err := m.linkIntakeThreadHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "LINKED", first.Body.Data.State)
	replay, err := m.linkIntakeThreadHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	view, err := m.getIntakeThreadHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "50"})
	require.NoError(t, err)
	require.Equal(t, "CLIENT_SHARED", view.Body.Data.Transport)
	allowed = false
	_, err = m.linkIntakeThreadHTTP(testContext(t, 2), in)
	require.Error(t, err, "replay rechecks current channel visibility")
	allowed = true
	request := clarificationRequest()
	originalAudit := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("round audit unavailable") }
	_, err = m.requestClarificationHTTP(testContext(t, 2), request)
	require.Error(t, err)
	m.audit = originalAudit
	opened, err := m.requestClarificationHTTP(testContext(t, 2), request)
	require.NoError(t, err)
	require.Equal(t, "OPEN", opened.Body.Data.State)
	var bodies []string
	require.NoError(t, db.Table("source_post_probe").Order("id").Pluck("body", &bodies).Error)
	require.Equal(t, request.Body.Questions, bodies, "only exact client questions enter the shared thread")
	_, err = m.requestClarificationHTTP(testContext(t, 2), request)
	require.NoError(t, err)
	var n int64
	require.NoError(t, db.Table("source_post_probe").Count(&n).Error)
	require.EqualValues(t, 2, n)
	message := &EditInput[IntakeThreadMessage]{EntityID: "3", ID: "50", IfMatch: `"2"`, Key: "source-reply", Body: IntakeThreadMessage{Body: "Recorded clarification follow-up"}}
	before := collaborationSnapshot(t, m, db)
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error {
		return errors.New("message audit unavailable")
	}
	_, err = m.replyIntakeThreadHTTP(testContext(t, 2), message)
	require.Error(t, err)
	require.Equal(t, before, collaborationSnapshot(t, m, db), "message and channel effects share the outer transaction")
	require.NoError(t, db.Table("source_post_probe").Count(&n).Error)
	require.EqualValues(t, 2, n, "failed reply must roll back the transport write itself")
	m.audit = originalAudit
	posted, err := m.replyIntakeThreadHTTP(testContext(t, 2), message)
	require.NoError(t, err)
	require.Equal(t, "POSTED", posted.Body.Data.State)
	notice := &EditInput[intake.SourceDisposition]{EntityID: "3", ID: "50", IfMatch: `"2"`, Key: "decline-with-source-thread", Body: intake.SourceDisposition{Action: "DECLINE", ReasonCode: "OTHER", Reason: "Human disposition after clarification", ClientMessage: "We cannot take this request", NotifyClient: true}}
	allowed = false
	_, err = m.disposeHTTP(testContext(t, 2), notice, "decline")
	require.Error(t, err, "linked transport failure must preserve the source and open round")
	allowed = true
	closed, err := m.disposeHTTP(testContext(t, 2), notice, "decline")
	require.NoError(t, err)
	require.Equal(t, "DECLINED", closed.Body.Data.State)
	_, err = m.disposeHTTP(testContext(t, 2), notice, "decline")
	require.NoError(t, err)
	require.NoError(t, db.Table("source_post_probe").Order("id").Pluck("body", &bodies).Error)
	require.Equal(t, []string{request.Body.Questions[0], request.Body.Questions[1], message.Body.Body, notice.Body.ClientMessage}, bodies, "original thread receives each exact question, reply and optional notice once")
	projects.deny = true
	_, err = m.replyIntakeThreadHTTP(testContext(t, 2), message)
	require.Error(t, err)
}

func TestIntakeClientMergeProjectionRequiresCurrentExternalProjectAndSourceContact(t *testing.T) {
	m, db, projects, clients := clarificationHarness(t)
	// The real provider owns the active original-contact check.
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
	in := &ObjectInput{EntityID: "3", ID: "50"}
	got, err := m.clientMergeHTTP(testContext(t, 4), in)
	require.NoError(t, err)
	require.False(t, got.Body.Data.Merged)
	_, err = m.clientMergeHTTP(testContext(t, 2), in)
	require.Error(t, err, "internal users use the internal ledger")
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 5).Error)
	_, err = m.clientMergeHTTP(testContext(t, 4), in)
	require.Error(t, err, "another project client is not the original contact")
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
	clients.revoked = true
	_, err = m.clientMergeHTTP(testContext(t, 4), in)
	require.Error(t, err)
	clients.revoked = false
	projects.deny = true
	_, err = m.clientMergeHTTP(testContext(t, 4), in)
	require.Error(t, err)
}

func TestIntakeMergedReplyRoutesExactCurrentTargetAndRollsBackTransport(t *testing.T) {
	m, db, _, _ := clarificationHarness(t)
	require.NoError(t, db.Table("g_intake_contacts").Where("id=1").Update("principal_id", 4).Error)
	require.NoError(t, db.Exec("CREATE TABLE merged_post_probe(id BIGINT)").Error)
	at := now()
	require.NoError(t, db.Table("g_intake_records").Create(map[string]any{"id": 51, "tenant_id": 1, "ref_no": 2, "channel_id": 3, "channel_kind": "SHARED_CHANNEL", "contact_id": 1, "kind": "NEW", "title": "Actual merge target", "raw_content_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "received_at": at, "reply_token": "target-51", "status": "RECEIVED", "project_id": 3, "created_at": at, "updated_at": at}).Error)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Updates(map[string]any{"status": "MERGED", "merged_into_intake_id": 51, "version": 2}).Error)
	for _, source := range []guid.ID{50, 51} {
		row := model.IntakeThread{TenantID: 1, ProjectID: 3, IntakeID: source, ChannelID: 70, RootMessageID: source + 100, RecipientID: 4, CreatedBy: 2, CreatedAt: at}
		require.NoError(t, db.Table(m.names.IntakeThread.String()).Create(&row).Error)
	}
	allowed := true
	m.intakeConversation.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string) error {
		if !allowed {
			return collab.ErrNotFound
		}
		return nil
	}
	m.intakeConversation.ForwardOn = func(_ context.Context, tx *gorm.DB, tenant, project guid.ID, in collab.IntakeReplyForward) (guid.ID, error) {
		require.Equal(t, guid.ID(150), in.SourceRootID)
		require.Equal(t, guid.ID(151), in.TargetRootID)
		require.Equal(t, guid.ID(81), in.MessageID)
		return 82, tx.Exec("INSERT INTO merged_post_probe VALUES (82)").Error
	}
	in := &EditInput[MergedReplyBody]{EntityID: "3", ID: "50", IfMatch: `"2"`, Key: "forward-canonical-reply", Body: MergedReplyBody{MessageID: 81}}
	original := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err := m.forwardMergedReplyHTTP(testContext(t, 2), in)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table("merged_post_probe").Count(&count).Error)
	require.Zero(t, count)
	m.audit = original
	first, err := m.forwardMergedReplyHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, guid.ID(82), first.Body.Data.ID)
	replay, err := m.forwardMergedReplyHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.NoError(t, db.Table("merged_post_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	allowed = false
	_, err = m.forwardMergedReplyHTTP(testContext(t, 2), in)
	require.Error(t, err, "replay must recheck current target channel authority")
	_, err = m.forwardMergedReplyHTTP(testContext(t, 4), in)
	require.Error(t, err, "external source contact cannot discover or write another client's target")
	allowed = true
	require.NoError(t, db.Table("g_intake_records").Where("id=51").Updates(map[string]any{"status": "CONVERTED", "requirement_id": 100}).Error)
	require.NoError(t, db.Exec("CREATE TABLE merged_work_probe(work_id BIGINT)").Error)
	currentOwner := true
	m.intakeWorkFence = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) error { return nil }
	m.intakeWorkOwner = func(_ context.Context, _ *gorm.DB, tenant, project, requirement, work guid.ID, version int) (guid.ID, error) {
		require.Equal(t, guid.ID(100), requirement)
		require.Equal(t, guid.ID(90), work)
		require.Equal(t, 3, version)
		if !currentOwner {
			return 0, ErrForbidden
		}
		return 11, nil
	}
	m.intakeConversation.BindWorkOn = func(_ context.Context, tx *gorm.DB, tenant, project, message, work guid.ID) error {
		return tx.Exec("INSERT INTO merged_work_probe VALUES (?)", work).Error
	}
	work := guid.ID(90)
	in.Key = "forward-bound-reply"
	in.Body.WorkItemID = &work
	in.Body.WorkVersion = 3
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error {
		return errors.New("binding audit unavailable")
	}
	_, err = m.forwardMergedReplyHTTP(testContext(t, 2), in)
	require.Error(t, err)
	require.NoError(t, db.Table("merged_work_probe").Count(&count).Error)
	require.Zero(t, count)
	m.audit = original
	_, err = m.forwardMergedReplyHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.NoError(t, db.Table("merged_work_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	currentOwner = false
	_, err = m.forwardMergedReplyHTTP(testContext(t, 2), in)
	require.Error(t, err, "work owner binding is rechecked before receipt replay")

}

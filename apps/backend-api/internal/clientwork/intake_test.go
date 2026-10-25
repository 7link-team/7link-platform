package clientwork

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntakeChangeFrozenCardCorruptionAndDependencyErrorsCannotApply(t *testing.T) {
	m, db, projects, _ := harness(t)
	realIntakeSources(t, m, db)
	ctx := testContext(t, 2)
	change, hash := confirmedProposal(t, m, db)
	var source Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		source, err = m.draftIntakeChangeOn(ctx, tx, 3, IntakeChangeBody{OriginalIntakeID: 50, ChangeID: change.ID})
		return err
	}))
	var original model.ChangeRequest
	require.NoError(t, db.Table(m.names.ChangeRequest.String()).Where("id=?", change.ID).Take(&original).Error)
	m.intakeAppend.AppendFromIntake = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int, string, delivery.ClientScopeChange) (guid.ID, error) {
		t.Fatal("invalid frozen card must never reach application")
		return 0, nil
	}
	for _, stage := range []string{"missing_change", "unconfirmed", "corrupt_proposal", "different_requirement", "corrupt_impact", "tolerance_lookup", "draft_state", "draft_stale_requirement", "draft_tolerance_lookup", "draft_source_write"} {
		t.Run(stage, func(t *testing.T) {
			copyModule := *m
			failure := errors.New("canonical dependency failed")
			blockQueries := false
			defer func() {
				projects.changed = false
				require.NoError(t, db.Table(m.names.ChangeRequest.String()).Where("id=?", change.ID).Updates(map[string]any{"proposal": original.Proposal, "state": original.State}).Error)
			}()
			originalRead := m.intakeSources.ReadOn
			copyModule.intakeSources.ReadOn = func(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (intake.ChangeSourceView, error) {
				fact, err := originalRead(ctx, tx, tenant, project, id)
				if err != nil {
					return fact, err
				}
				switch stage {
				case "missing_change":
					fact.ChangeID = 999
				case "corrupt_proposal":
					fact.ProposalJSON = "broken"
				case "different_requirement":
					fact.RequirementID = 999
				case "corrupt_impact":
					fact.ImpactJSON = "broken"
				}
				return fact, nil
			}
			switch stage {
			case "unconfirmed", "draft_state":
				require.NoError(t, db.Table(m.names.ChangeRequest.String()).Where("id=?", change.ID).Update("state", "REQUESTED").Error)
			case "corrupt_proposal":
				require.NoError(t, db.Table(m.names.ChangeRequest.String()).Where("id=?", change.ID).Update("proposal", "broken").Error)
			case "draft_stale_requirement":
				projects.changed = true
			case "tolerance_lookup", "draft_tolerance_lookup":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_change_tolerance", func(tx *gorm.DB) {
					if blockQueries && tx.Statement.Table == m.names.Tolerance.String() {
						tx.AddError(failure)
					}
				}))
				defer db.Callback().Query().Remove("regression_change_tolerance")
			case "draft_source_write":
				copyModule.intakeSources.DraftOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, intake.ChangeSourceDraft) (intake.ChangeSourceView, error) {
					return intake.ChangeSourceView{}, failure
				}
			}
			before := collaborationSnapshot(t, m, db)
			blockQueries = true
			err := db.Transaction(func(tx *gorm.DB) error {
				if strings.HasPrefix(stage, "draft_") {
					_, err := copyModule.draftIntakeChangeOn(ctx, tx, 3, IntakeChangeBody{OriginalIntakeID: 50, ChangeID: change.ID})
					return err
				}
				_, err := copyModule.convertIntakeChangeOn(ctx, tx, 3, source.ID, source.Version, "session", IntakeConversionBody{CardRevision: 1, ProposalHash: hash, ReauthProof: "proof"})
				return err
			})
			blockQueries = false
			require.Error(t, err)
			require.Equal(t, before, collaborationSnapshot(t, m, db))
		})
	}
}

func realIntakeSources(t *testing.T, m *Module, db *gorm.DB) {
	t.Helper()
	im := intake.NewModule(m.runtime, intake.Config{})
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, m.ids))
	require.NoError(t, module.Provide[identity.PrincipalStateStore](s, m.principals))
	require.NoError(t, module.Provide[audit.Appender](s, m.audit))
	locks := dlock.NewModule(m.runtime)
	require.NoError(t, locks.ProvideServices(s))
	publisher := events.NewModule(m.runtime)
	require.NoError(t, publisher.ProvideServices(s))
	require.NoError(t, module.Provide[core.AuthorizationExplanation](s, func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true}, nil
	}))
	require.NoError(t, im.ProvideServices(s))
	require.NoError(t, s.Seal())
	require.NoError(t, im.InjectServices(s))
	require.NoError(t, publisher.InjectServices(s))
	require.NoError(t, publisher.Initialize(t.Context()))
	var e error
	m.intakeSources, e = module.Resolve[intake.ChangeSources](s)
	require.NoError(t, e)
	m.submissionEvents, e = module.Resolve[intake.SubmissionEvents](s)
	require.NoError(t, e)
	require.NoError(t, m.injectIntakeConversation(s))
	m.reworkSources, e = module.Resolve[intake.ReworkSources](s)
	require.NoError(t, e)
	raw, e := db.DB()
	require.NoError(t, e)
	source := im.MigrationSource()
	source.Prefix = "g_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	at := time.Now().UTC()
	require.NoError(t, db.Table("g_intake_contacts").Create(map[string]any{"id": 1, "tenant_id": 1, "display_name": "Contact", "created_at": at, "updated_at": at}).Error)
	require.NoError(t, db.Table("g_intake_records").Create(map[string]any{"id": 50, "tenant_id": 1, "ref_no": 1, "channel_id": 3, "channel_kind": "SHARED_CHANNEL", "contact_id": 1, "title": "Original", "raw_content_hash": strings.Repeat("a", 64), "received_at": at, "reply_token": "source-50", "status": "CONVERTED", "project_id": 3, "requirement_id": 10, "created_at": at, "updated_at": at}).Error)
}

func TestIntakeLedgerHTTPUsesCurrentAuthorityBoundCursorAndActualSource(t *testing.T) {
	m, db, _, _ := harness(t)
	realIntakeSources(t, m, db)
	change, _ := confirmedProposal(t, m, db)
	frozen, e := m.draftIntakeHTTP(testContext(t, 2), &CreateInput[IntakeChangeBody]{EntityID: "3", Key: "ledger-freeze", Body: IntakeChangeBody{OriginalIntakeID: 50, ChangeID: change.ID}})
	require.NoError(t, e)
	first, e := m.listIntakeLedgerHTTP(testContext(t, 2), &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, e)
	require.True(t, first.Body.Data.HasMore)
	require.Equal(t, frozen.Body.Data.ID, first.Body.Data.Items[0].ID)
	require.NotEmpty(t, first.Body.Data.NextCursor)
	last, e := m.listIntakeLedgerHTTP(testContext(t, 2), &ListInput{EntityID: "3", Limit: 1, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, e)
	require.False(t, last.Body.Data.HasMore)
	require.Equal(t, guid.ID(50), last.Body.Data.Items[0].ID)
	source, e := m.getIntakeLedgerHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: frozen.Body.Data.ID.String()})
	require.NoError(t, e)
	require.Equal(t, change.ID, source.Body.Data.Card.ChangeID)
	require.Equal(t, guid.ID(50), source.Body.Data.Card.OriginalIntakeID)
	for _, in := range []*ListInput{{EntityID: "bad", Limit: 1}, {EntityID: "3", Limit: 0}, {EntityID: "3", Limit: 101}, {EntityID: "3", Limit: 1, Cursor: "bad"}, {EntityID: "3", Limit: 1, Cursor: cursor(cursorScope(testContext(t, 4), 3, "intakes"), 50)}, {EntityID: "3", Limit: 1, Cursor: cursor(cursorScope(testContext(t, 2), 3, "sheets:false"), 50)}} {
		_, e := m.listIntakeLedgerHTTP(testContext(t, 2), in)
		require.Error(t, e)
	}
	_, e = m.listIntakeLedgerHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, e, "external member cannot browse internal Intake")
	_, e = m.getIntakeLedgerHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: frozen.Body.Data.ID.String()})
	require.Error(t, e)
	for _, in := range []*ObjectInput{{EntityID: "bad", ID: "50"}, {EntityID: "3", ID: "0"}, {EntityID: "3", ID: "999999"}} {
		_, e := m.getIntakeLedgerHTTP(testContext(t, 2), in)
		require.Error(t, e)
	}
	m.intakeSources.ListOn = nil
	_, e = m.listIntakeLedgerHTTP(testContext(t, 2), &ListInput{EntityID: "3", Limit: 1})
	require.Error(t, e)
	m.intakeSources.SourceOn = nil
	_, e = m.getIntakeLedgerHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "50"})
	require.Error(t, e)
}

func TestIntakeTriageCommandReplaysExactAssessmentAndRejectsStaleOrExternalWrites(t *testing.T) {
	m, db, _, _ := harness(t)
	realIntakeSources(t, m, db)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Updates(map[string]any{"kind": "NEW", "status": "RECEIVED", "version": 1, "raw_content": "Unchanged source"}).Error)
	in := &EditInput[intake.TriageDraft]{EntityID: "3", ID: "50", IfMatch: "\"1\"", Key: "triage-first", Body: intake.TriageDraft{Summary: "Export requested", RequirementDraft: "Export current rows", Criteria: []string{"Output matches rows"}, Questions: []string{}, Risks: []string{}, Size: "M"}}
	first, err := m.triageIntakeHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, 2, first.Body.Data.Version)
	require.Equal(t, 1, first.Body.Data.Revision)
	replayed, err := m.triageIntakeHTTP(testContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, first.Body.Data, replayed.Body.Data)
	in.Body.Summary = "Different assessment"
	_, err = m.triageIntakeHTTP(testContext(t, 2), in)
	require.Error(t, err, "same key cannot change the card")
	in.Key = "stale-card"
	_, err = m.triageIntakeHTTP(testContext(t, 2), in)
	require.Error(t, err, "new key cannot bypass source version")
	in.IfMatch = "\"2\""
	_, err = m.triageIntakeHTTP(testContext(t, 4), in)
	require.Error(t, err, "external members cannot author internal triage")
	source, err := m.getIntakeLedgerHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "50"})
	require.NoError(t, err)
	require.Equal(t, "Unchanged source", *source.Body.Data.RawContent)
	require.Equal(t, "Export requested", source.Body.Data.Triage.Draft.Summary)
	pub := m.events
	m.events = failPublisher{}
	// The Intake provider publishes with its own injected publisher. An outer
	// command failure still rolls that provider's card and version back.
	prior := m.intakeSources.TriageOn
	m.intakeSources.TriageOn = func(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int, body intake.TriageDraft) (intake.TriageSource, error) {
		v, e := prior(ctx, tx, tenant, project, id, version, body)
		if e != nil {
			return v, e
		}
		return v, fmt.Errorf("outer effect unavailable")
	}
	_, err = m.triageIntakeHTTP(testContext(t, 2), in)
	require.Error(t, err)
	m.events = pub
	source, err = m.getIntakeLedgerHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: "50"})
	require.NoError(t, err)
	require.Equal(t, 2, source.Body.Data.Version)
	require.Equal(t, 1, source.Body.Data.Triage.Revision)
}

func TestIntakeTriageHTTPInvalidIdentifiersAndUnavailableProviderHaveNoEffects(t *testing.T) {
	m, db, _, _ := harness(t)
	realIntakeSources(t, m, db)
	for _, in := range []*EditInput[intake.TriageDraft]{{EntityID: "invalid", ID: "50", IfMatch: "\"1\""}, {EntityID: "3", ID: "0", IfMatch: "\"1\""}, {EntityID: "3", ID: "50", IfMatch: "1"}} {
		out, err := m.triageIntakeHTTP(testContext(t, 2), in)
		require.Error(t, err)
		require.Nil(t, out)
	}
	m.intakeSources.TriageOn = nil
	out, err := m.triageIntakeHTTP(testContext(t, 2), &EditInput[intake.TriageDraft]{EntityID: "3", ID: "50", IfMatch: "\"1\"", Key: "missing-provider"})
	require.Error(t, err)
	require.Nil(t, out)
	var cards int64
	require.NoError(t, db.Table("g_intake_triage_cards").Count(&cards).Error)
	require.Zero(t, cards)
}

func TestIntakeChangeConversionUsesRealSourceExactClientDecisionAndAtomicAdjustment(t *testing.T) {
	m, db, projects, _ := harness(t)
	realIntakeSources(t, m, db)
	change, hash := confirmedProposal(t, m, db)
	m.intakeAppend = delivery.IntakeAppender{AppendFromIntake: func(ctx context.Context, tx *gorm.DB, tenant, project, requirement, source guid.ID, expected int, cookie string, in delivery.ClientScopeChange) (guid.ID, error) {
		require.Positive(t, int64(source))
		return projects.ApplyClientChangeOn(ctx, tx, tenant, project, requirement, expected, cookie, in)
	}}
	drafted, e := m.draftIntakeHTTP(testContext(t, 2), &CreateInput[IntakeChangeBody]{EntityID: "3", Key: "freeze-original", Body: IntakeChangeBody{OriginalIntakeID: 50, ChangeID: change.ID}})
	require.NoError(t, e)
	source := drafted.Body.Data
	request := &IntakeConversionInput{EditInput: EditInput[IntakeConversionBody]{EntityID: "3", ID: source.ID.String(), IfMatch: fmt.Sprintf("\"%d\"", source.Version), Key: "intake-atomic", Body: IntakeConversionBody{CardRevision: 1, ProposalHash: hash, ReauthProof: "proof"}}, Cookie: "session"}
	invalid := *request
	invalid.Body.ProposalHash = strings.Repeat("0", 64)
	_, e = m.convertIntakeHTTP(testContext(t, 2), &invalid)
	require.Error(t, e)
	before, e := m.getIntakeHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: source.ID.String()})
	require.NoError(t, e)
	pub := m.events
	m.events = failPublisher{}
	_, e = m.convertIntakeHTTP(testContext(t, 2), request)
	require.Error(t, e)
	m.events = pub
	after, e := m.getIntakeHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: source.ID.String()})
	require.NoError(t, e)
	require.Equal(t, before.Body.Data, after.Body.Data)
	var n int64
	require.NoError(t, db.Table("baseline_probe").Where("id=?", 88).Count(&n).Error)
	require.Zero(t, n)
	first, e := m.convertIntakeHTTP(testContext(t, 2), request)
	require.NoError(t, e)
	require.Equal(t, "CONVERTED", first.Body.Data.State)
	replay, e := m.convertIntakeHTTP(testContext(t, 2), request)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	got, e := m.getIntakeHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: source.ID.String()})
	require.NoError(t, e)
	require.Equal(t, 2, *got.Body.Data.ResultVersionNo)
	_, e = m.getIntakeHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: source.ID.String()})
	require.Error(t, e)
	require.NoError(t, db.Table("baseline_probe").Where("id=?", 88).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestIntakeChangeConversionRejectsUnconfirmedCardAndChangedBreach(t *testing.T) {
	m, db, _, _ := harness(t)
	realIntakeSources(t, m, db)
	change, hash := confirmedProposal(t, m, db)
	var source Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		source, e = m.draftIntakeChangeOn(testContext(t, 2), tx, 3, IntakeChangeBody{OriginalIntakeID: 50, ChangeID: change.ID})
		return e
	}))
	m.intakeAppend = delivery.IntakeAppender{AppendFromIntake: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int, string, delivery.ClientScopeChange) (guid.ID, error) {
		t.Fatal("must not append invalid source")
		return 0, nil
	}}
	convert := func(body IntakeConversionBody) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.convertIntakeChangeOn(testContext(t, 2), tx, 3, source.ID, source.Version, "session", body)
			return e
		})
	}
	require.ErrorIs(t, convert(IntakeConversionBody{CardRevision: 2, ProposalHash: hash}), ErrConflict)
	require.NoError(t, db.Table(m.names.ChangeRequest.String()).Where("id=?", change.ID).Update("state", "REJECTED").Error)
	require.ErrorIs(t, convert(IntakeConversionBody{CardRevision: 1, ProposalHash: hash}), ErrConflict)
	require.NoError(t, db.Table(m.names.ChangeRequest.String()).Where("id=?", change.ID).Update("state", "CONFIRMED").Error)
	require.NoError(t, db.Table("g_intake_triage_cards").Where("intake_id=?", source.ID).Update("impact_json", `{"breached":[{"dimension":"TIME","subject_kind":"PROJECT","subject_id":"3"}]}`).Error)
	require.ErrorIs(t, convert(IntakeConversionBody{CardRevision: 1, ProposalHash: hash}), ErrConflict)
}

func TestIntakeSourceFreezeRejectsUnavailableUnassessedAndCorruptProposals(t *testing.T) {
	for _, scenario := range []string{"invalid_origin", "invalid_change", "denied", "missing_capability", "missing_change", "unassessed", "corrupt_proposal"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _, _ := harness(t)
			realIntakeSources(t, m, db)
			change, _ := confirmedProposal(t, m, db)
			in := &CreateInput[IntakeChangeBody]{EntityID: "3", Key: "invalid-freeze", Body: IntakeChangeBody{OriginalIntakeID: 50, ChangeID: change.ID}}
			actor := guid.ID(2)
			switch scenario {
			case "invalid_origin":
				in.Body.OriginalIntakeID = 0
			case "invalid_change":
				in.Body.ChangeID = 0
			case "denied":
				actor = 4
			case "missing_capability":
				m.intakeSources.DraftOn = nil
			case "missing_change":
				in.Body.ChangeID = 999
			case "unassessed":
				require.NoError(t, db.Table(m.names.ChangeRequest.String()).Where("id=?", change.ID).Update("state", "REQUESTED").Error)
			case "corrupt_proposal":
				require.NoError(t, db.Table(m.names.ChangeRequest.String()).Where("id=?", change.ID).Update("proposal", `{}`).Error)
			}
			_, e := m.draftIntakeHTTP(testContext(t, actor), in)
			require.Error(t, e)
			var count int64
			require.NoError(t, db.Table("g_intake_triage_cards").Count(&count).Error)
			require.Zero(t, count)
		})
	}
	for _, in := range []*ObjectInput{{EntityID: "invalid", ID: "1"}, {EntityID: "3", ID: "0"}, {EntityID: "3", ID: "1"}} {
		_, e := (&Module{}).getIntakeHTTP(testContext(t, 2), in)
		require.Error(t, e)
	}
	_, e := (&Module{}).draftIntakeHTTP(testContext(t, 2), &CreateInput[IntakeChangeBody]{EntityID: "bad"})
	require.Error(t, e)
}

// Conversion must stop before canonical scope mutation when current authority
// or the source capability is unavailable, including direct provider callers.
func TestIntakeConversionUnavailableAuthorityOrSourceCannotApplyScope(t *testing.T) {
	for _, scenario := range []string{"current_authority", "missing_capability", "source_error"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _, _ := harness(t)
			realIntakeSources(t, m, db)
			failure := errors.New("intake source unavailable")
			actor := guid.ID(2)
			if scenario == "current_authority" {
				actor = 4
			}
			if scenario == "missing_capability" {
				m.intakeSources.ReadOn = nil
			}
			if scenario == "source_error" {
				m.intakeSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.ChangeSourceView, error) {
					return intake.ChangeSourceView{}, failure
				}
			}
			m.intakeAppend.AppendFromIntake = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int, string, delivery.ClientScopeChange) (guid.ID, error) {
				t.Fatal("unavailable source or authority must never apply canonical scope")
				return 0, nil
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.convertIntakeChangeOn(testContext(t, actor), tx, 3, 50, 1, "session", IntakeConversionBody{CardRevision: 1, ProposalHash: strings.Repeat("a", 64)})
				return err
			})
			require.Error(t, err)
			if scenario == "source_error" {
				require.ErrorIs(t, err, failure)
			}
			if scenario == "missing_capability" {
				require.ErrorIs(t, err, ErrUnavailable)
			}
			var count int64
			require.NoError(t, db.Table("g_intake_triage_cards").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

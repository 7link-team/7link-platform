package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type resultFileFixture struct {
	h         *resultFixture
	pins      []files.ResourceBindingInput
	opened    []guid.ID
	inspected []guid.ID
	fail      error
	closed    int
}

func (f *resultFileFixture) check(ctx context.Context, tx *gorm.DB) error {
	require.NotNil(f.h.t, tx)
	_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
	require.True(f.h.t, ok)
	if f.h.tx != nil {
		require.Same(f.h.t, f.h.tx, tx)
	}
	return f.fail
}
func (f *resultFileFixture) InspectOn(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
	if err := f.check(ctx, tx); err != nil {
		return files.PinnedCandidate{}, err
	}
	f.inspected = append(f.inspected, id)
	return files.PinnedCandidate{PinnedResource: files.PinnedResource{SpaceID: 500, FileID: id, VersionID: 502, Name: "evidence.txt", MIME: "text/plain", Size: 5, SHA256: strings.Repeat("a", 64)}, NodeVersion: 3}, nil
}
func (f *resultFileFixture) PinOn(ctx context.Context, tx *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
	if err := f.check(ctx, tx); err != nil {
		return files.PinnedResource{}, err
	}
	require.Equal(f.h.t, resultEvidencePurpose, in.Purpose)
	require.Equal(f.h.t, "delivery_result", in.ResourceKind)
	require.Equal(f.h.t, authn.PrincipalIDFromContext(ctx), in.ActorID)
	require.NotNil(f.h.t, in.VersionID)
	if err := f.h.m.authorizeResultResource(ctx, tx, in.ResourceID, files.ResourceOwner{Scope: "entity", ID: 99}); err != nil {
		return files.PinnedResource{}, err
	}
	if err := tx.Exec("INSERT INTO provision_probe(step) VALUES (?)", "pin").Error; err != nil {
		return files.PinnedResource{}, err
	}
	f.pins = append(f.pins, in)
	return files.PinnedResource{BindingID: in.ID, SpaceID: in.SpaceID, FileID: in.FileID, VersionID: *in.VersionID, ResourceID: in.ResourceID, Purpose: in.Purpose, ResourceKind: in.ResourceKind, Name: "evidence.txt", SHA256: strings.Repeat("a", 64), MIME: "text/plain", Size: 5}, nil
}

type resultReadCloser struct {
	io.Reader
	close func()
}

func (r resultReadCloser) Close() error { r.close(); return nil }
func (f *resultFileFixture) OpenOn(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
	if err := f.check(ctx, tx); err != nil {
		return files.PinnedResourceRead{}, err
	}
	for _, pin := range f.pins {
		if pin.ID == id {
			if err := f.h.m.authorizeResultResource(ctx, tx, pin.ResourceID, files.ResourceOwner{Scope: "entity", ID: 99}); err != nil {
				return files.PinnedResourceRead{}, err
			}
			f.opened = append(f.opened, id)
			return files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: id, FileID: pin.FileID, VersionID: *pin.VersionID, Name: "evidence.txt", MIME: "text/plain", Size: 5}, Reader: resultReadCloser{strings.NewReader("exact"), func() { f.closed++ }}}, nil
		}
	}
	return files.PinnedResourceRead{}, ErrProjectNotFound
}

type resultFixture struct {
	t               *testing.T
	m               *Module
	db              *gorm.DB
	ctx             context.Context
	work            guid.ID
	input           resultSubmitBody
	files           *resultFileFixture
	tx              *gorm.DB
	fail            string
	deniedAuthority bool
}

func newResultFixture(t *testing.T) *resultFixture {
	t.Helper()
	m, db, ctx, work := assignmentHarness(t, true)
	assignmentClaim(t, m, db, ctx, work, false)
	h := &resultFixture{t: t, m: m, db: db, work: work.ID}
	h.ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	h.files = &resultFileFixture{h: h}
	m.resultFiles = h.files
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, id >= 11 && id <= 13, nil
	}
	m.members = memberCheck(func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (bool, error) {
		return tenant == 1 && id >= 11 && id <= 13, nil
	})
	m.deliveryAuthority = func(ctx context.Context, tx *gorm.DB, in governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		require.Equal(t, "HUMAN", in.ActorKind)
		require.Equal(t, authn.PrincipalIDFromContext(ctx), in.PrincipalID)
		require.NotNil(t, in.Facts)
		_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, ok)
		if h.fail == "authority" {
			return governance.AuthorityDecision{}, errors.New("authority failed")
		}
		return governance.AuthorityDecision{Allowed: !h.deniedAuthority, AuthoritySource: "ROLE:reviewer"}, nil
	}
	m.service.audit = func(_ context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		if err := tx.Exec("INSERT INTO provision_probe(step) VALUES (?)", "audit:"+event.EventType).Error; err != nil {
			return err
		}
		if h.fail == "audit" {
			return errors.New("audit failed")
		}
		return nil
	}
	m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
		if err := tx.Exec("INSERT INTO provision_probe(step) VALUES (?)", "event:"+event.Type).Error; err != nil {
			return 0, err
		}
		if h.fail == "outbox" {
			return 0, errors.New("outbox failed")
		}
		return 1, nil
	})
	h.input = resultSubmitBody{Summary: "Human result", Evidence: []resultEvidenceBody{{SpaceID: 500, FileID: 501, FileVersion: 3, VersionID: 502, CriterionID: &work.Criteria[0].ID, Coverage: "COVERED"}}}
	return h
}
func (h *resultFixture) actor(id guid.ID) context.Context {
	return authn.WithClaims(h.ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: id, SubjectType: authn.SubjectTypePrincipal})
}
func (h *resultFixture) stored() *model.WorkItem {
	h.t.Helper()
	w := query.BindWorkItems(h.db, h.m.service.names)
	row, err := w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).First()
	require.NoError(h.t, err)
	return row
}
func (h *resultFixture) command(actor guid.ID, fn func(context.Context, *gorm.DB, int) (ResultView, error)) (ResultView, error) {
	h.t.Helper()
	version := h.stored().Version
	var result ResultView
	err := h.db.Transaction(func(tx *gorm.DB) error {
		h.tx = tx
		defer func() { h.tx = nil }()
		var e error
		result, e = fn(h.actor(actor), tx, version)
		return e
	})
	return result, err
}
func (h *resultFixture) submit() (ResultView, error) {
	return h.command(11, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.submitResultOn(ctx, tx, 1, 99, h.work, 11, 11, v, h.input)
	})
}
func (h *resultFixture) check(result, actor guid.ID, verdict string) (ResultView, error) {
	return h.command(actor, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.checkResultOn(ctx, tx, 1, 99, h.work, result, actor, 11, v, resultCheckBody{Verdict: verdict, Reason: "Independent examination"})
	})
}
func (h *resultFixture) accept(result, actor guid.ID) (ResultView, error) {
	return h.command(actor, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.acceptResultOn(ctx, tx, 1, 99, h.work, result, actor, 11, v, resultAcceptBody{Reason: "Criteria verified"})
	})
}
func (h *resultFixture) handoff(result guid.ID) (ResultView, error) {
	return h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.handoffResultOn(ctx, tx, 1, 99, h.work, result, 12, 11, v, resultHandoffBody{Summary: "Frozen package"})
	})
}
func (h *resultFixture) receive(result, handoff, actor guid.ID, accepted bool) (ResultView, error) {
	return h.command(actor, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.receiveResultOn(ctx, tx, 1, 99, h.work, result, handoff, actor, 11, v, resultReceiptBody{Accepted: accepted, Reason: "Independent receipt decision"})
	})
}
func (h *resultFixture) snapshot() map[string]string {
	h.t.Helper()
	out := map[string]string{}
	for _, table := range []string{h.m.service.names.Projects.String(), h.m.service.names.WorkItems.String(), h.m.service.names.Assignments.String(), h.m.service.names.ResultVersions.String(), h.m.service.names.ResultEvidence.String(), h.m.service.names.ResultActions.String(), h.m.service.names.ResultHandoffs.String(), h.m.service.names.Deliveries.String(), h.m.service.names.WorkTransitions.String(), h.m.service.names.WorkActivities.String(), "provision_probe"} {
		var rows []map[string]any
		require.NoError(h.t, h.db.Table(table).Find(&rows).Error)
		raw, err := json.Marshal(rows)
		require.NoError(h.t, err)
		out[table] = string(raw)
	}
	return out
}

func TestResultLifecyclePublishesThroughDeclaredEventCatalog(t *testing.T) {
	h := newResultFixture(t)
	declaration := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{Config: module.Config{Enabled: true}})
	services := module.NewServices()
	require.NoError(t, declaration.ProvideServices(services))
	for _, provide := range moduleProviders(t) {
		require.NoError(t, provide(services))
	}
	require.NoError(t, services.Seal())
	require.NoError(t, declaration.InjectServices(services))
	publisher, err := module.Resolve[events.Publisher](services)
	require.NoError(t, err)
	eventModule, ok := publisher.(*events.Module)
	require.True(t, ok)
	require.NoError(t, eventModule.InjectServices(services))
	require.NoError(t, eventModule.Initialize(t.Context()))
	sqlDB, err := h.db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, eventModule.MigrationSource(), h.db.Dialector.Name()))
	h.m.service.events = eventModule
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	result, err := h.submit()
	require.NoError(t, err)
	for attempt := range 3 {
		if attempt > 0 {
			result, err = h.submit()
			require.NoError(t, err)
		}
		_, err = h.check(result.ID, 12, "PASSED")
		require.NoError(t, err)
		_, err = h.accept(result.ID, 12)
		require.NoError(t, err)
		result, err = h.handoff(result.ID)
		require.NoError(t, err)
		_, err = h.receive(result.ID, result.Handoffs[0].ID, 13, false)
		require.NoError(t, err)
	}
	require.Equal(t, 3, h.stored().QualityReworkCount)
	require.NotNil(t, h.stored().PausedAt)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.appendResultFeedbackOn(h.actor(13), tx, 1, 99, h.work, result.ID, 13, 11, resultFeedbackBody{Kind: "COMMENT", Detail: "Received exact evidence"})
		return e
	}))
	var count int64
	require.NoError(t, h.db.Table("domain_events").Where("event_type = ?", "work_item.state_changed").Count(&count).Error)
	require.EqualValues(t, 15, count)
	require.NoError(t, h.db.Table("domain_events").Where("event_type = ? AND subject_kind = ?", "work_item.escalated", "work_item").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, h.db.Table("domain_events").Where("event_type = ? AND subject_kind = ?", "result.feedback_added", "result").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestResultHumanLifecyclePreservesExactVersionsAndRework(t *testing.T) {
	h := newResultFixture(t)
	first, err := h.submit()
	require.NoError(t, err)
	require.Equal(t, 1, first.Sequence)
	require.Nil(t, first.PreviousResultID)
	require.Len(t, first.Evidence, 1)
	require.Equal(t, guid.ID(502), first.Evidence[0].VersionID)
	require.Equal(t, strings.Repeat("a", 64), first.Evidence[0].SHA256)
	require.Len(t, h.files.pins, 1)
	require.Equal(t, int64(3), h.files.pins[0].ExpectedFileVersion)
	require.Equal(t, guid.ID(502), *h.files.pins[0].VersionID)
	row := h.stored()
	require.Equal(t, "NEEDS_HUMAN_REVIEW", row.State)
	require.Nil(t, row.CurrentExecutionID)
	require.Equal(t, first.ID, *row.CurrentClaimID)
	assignment, err := query.BindAssignments(h.db, h.m.service.names).WithContext(h.ctx).First()
	require.NoError(t, err)
	require.Nil(t, assignment.LeaseExpiresAt)
	_, err = h.check(first.ID, 11, "PASSED")
	require.ErrorIs(t, err, ErrResultIndependent)
	_, err = h.accept(first.ID, 11)
	require.ErrorIs(t, err, ErrResultIndependent)
	_, err = h.accept(first.ID, 12)
	require.ErrorIs(t, err, ErrResultCoverage)
	checked, err := h.check(first.ID, 12, "PASSED")
	require.NoError(t, err)
	require.Equal(t, "CHECK", checked.Actions[0].Kind)
	accepted, err := h.accept(first.ID, 13)
	require.NoError(t, err)
	require.Len(t, accepted.Actions, 2)
	acceptance := *h.stored().AcceptedDecisionID
	package1, err := h.handoff(first.ID)
	require.NoError(t, err)
	require.Len(t, package1.Handoffs, 1)
	manifest := package1.Handoffs[0]
	sum := sha256.Sum256([]byte(manifest.Manifest))
	require.Equal(t, hex.EncodeToString(sum[:]), manifest.SHA256)
	require.Contains(t, manifest.Manifest, first.Evidence[0].BindingID.String())
	require.Contains(t, manifest.Manifest, acceptance.String())
	_, err = h.receive(first.ID, manifest.ID, 12, false)
	require.ErrorIs(t, err, ErrResultIndependent)
	returned, err := h.receive(first.ID, manifest.ID, 13, false)
	require.NoError(t, err)
	require.Equal(t, "REWORK", h.stored().State)
	require.Nil(t, h.stored().AcceptedDecisionID)
	require.Nil(t, h.stored().AcceptedAt)
	require.Equal(t, 1, h.stored().QualityReworkCount)
	returnedAction := returned.Actions[len(returned.Actions)-1]
	require.Equal(t, "RETURN", returnedAction.Kind)
	second, err := h.submit()
	require.NoError(t, err)
	require.Equal(t, 2, second.Sequence)
	require.Equal(t, first.ID, *second.PreviousResultID)
	require.Equal(t, returnedAction.ID, *second.ReworkActionID)
	require.Empty(t, second.Actions)
	require.Nil(t, h.stored().AcceptedDecisionID)
	_, err = h.check(first.ID, 12, "PASSED")
	require.ErrorIs(t, err, ErrResultState)
	oldRow, err := h.m.resultOn(h.ctx, h.db, 1, 99, h.work, first.ID)
	require.NoError(t, err)
	old, err := h.m.resultViewOn(h.ctx, h.db, oldRow)
	require.NoError(t, err)
	require.Equal(t, returned, old)
	_, err = h.check(second.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(second.ID, 13)
	require.NoError(t, err)
	package2, err := h.handoff(second.ID)
	require.NoError(t, err)
	received, err := h.receive(second.ID, package2.Handoffs[0].ID, 13, true)
	require.NoError(t, err)
	require.Equal(t, "RECEIPT", received.Actions[len(received.Actions)-1].Kind)
	require.Equal(t, "ACCEPTED", h.stored().State)
	require.Nil(t, h.stored().CurrentExecutionID)
	_, err = h.receive(second.ID, package2.Handoffs[0].ID, 13, true)
	require.ErrorIs(t, err, ErrResultState)
	deliveries, err := query.BindDeliveries(h.db, h.m.service.names).WithContext(h.ctx).Order(query.BindDeliveries(h.db, h.m.service.names).SequenceNo).Find()
	require.NoError(t, err)
	require.Len(t, deliveries, 2)
	require.Equal(t, "REJECTED", deliveries[0].State)
	require.Equal(t, "ACCEPTED", deliveries[1].State)
}

func TestResultAcceptanceUsesEveryReviewersLatestCheck(t *testing.T) {
	for _, verdict := range []string{"FAILED", "INCONCLUSIVE"} {
		t.Run(verdict, func(t *testing.T) {
			h := newResultFixture(t)
			r, err := h.submit()
			require.NoError(t, err)
			_, err = h.check(r.ID, 12, verdict)
			require.NoError(t, err)
			_, err = h.check(r.ID, 13, "PASSED")
			require.NoError(t, err)
			before := h.snapshot()
			_, err = h.accept(r.ID, 13)
			require.ErrorIs(t, err, ErrResultCoverage)
			require.Equal(t, before, h.snapshot())
			_, err = h.check(r.ID, 12, "PASSED")
			require.NoError(t, err)
			_, err = h.accept(r.ID, 13)
			require.NoError(t, err)
		})
	}
}

func TestResultReplacementClearsCurrentAcceptance(t *testing.T) {
	h := newResultFixture(t)
	r, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(r.ID, 13)
	require.NoError(t, err)
	oldAccepted := *h.stored().AcceptedDecisionID
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"type": "BUG", "defect_stage": "VERIFIED"}).Error)
	next, err := h.submit()
	require.NoError(t, err)
	require.Equal(t, r.ID, *next.PreviousResultID)
	require.Nil(t, h.stored().AcceptedAt)
	require.Nil(t, h.stored().AcceptedDecisionID)
	require.Nil(t, h.stored().DefectStage, "a new result cannot retain verification of an older exact version")
	require.NotNil(t, h.stored().FirstAcceptedAt)
	actions, err := query.BindResultActions(h.db, h.m.service.names).WithContext(h.ctx).Find()
	require.NoError(t, err)
	require.Len(t, actions, 2)
	require.Equal(t, oldAccepted, actions[1].ID)
}

func TestResultRejectsInvalidSubmissionsAtomically(t *testing.T) {
	for _, scenario := range []string{"summary", "oversize_summary", "utf8", "no_evidence", "too_many", "file", "space", "file_revision", "version", "criterion", "coverage", "note", "na_reason", "duplicate", "unavailable", "pin", "stale", "lease", "holder", "paused", "container", "execution", "stage", "policy", "human", "member"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			switch scenario {
			case "summary":
				h.input.Summary = " "
			case "oversize_summary":
				h.input.Summary = strings.Repeat("a", 2001)
			case "utf8":
				h.input.Summary = string([]byte{0xff})
			case "no_evidence":
				h.input.Evidence = nil
			case "too_many":
				h.input.Evidence = make([]resultEvidenceBody, 501)
			case "file":
				h.input.Evidence[0].FileID = 0
			case "space":
				h.input.Evidence[0].SpaceID = 0
			case "file_revision":
				h.input.Evidence[0].FileVersion = 0
			case "version":
				h.input.Evidence[0].VersionID = 0
			case "criterion":
				id := guid.ID(999)
				h.input.Evidence[0].CriterionID = &id
			case "coverage":
				h.input.Evidence[0].Coverage = "UNKNOWN"
			case "note":
				h.input.Evidence[0].Note = strings.Repeat("a", 2001)
			case "na_reason":
				h.input.Evidence[0].Coverage = "NOT_APPLICABLE"
			case "duplicate":
				h.input.Evidence = append(h.input.Evidence, h.input.Evidence[0])
			case "unavailable":
				h.m.resultFiles = nil
			case "pin":
				h.files.fail = errors.New("pin denied")
			case "lease":
				require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("tenant_id=1").Update("lease_expires_at", time.Now().Add(-time.Second)).Error)
			case "holder":
				require.NoError(t, h.db.Table(h.m.service.names.Assignments.String()).Where("tenant_id=1").Update("assignee_principal_id", 12).Error)
			case "paused":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("paused_at", time.Now()).Error)
			case "container":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("is_container", true).Error)
			case "execution":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("current_execution_id", 3).Error)
			case "stage":
				require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=99").Update("stage", "PLANNING").Error)
			case "policy":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
			case "human":
				h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "member":
				h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
			}
			before := h.snapshot()
			var err error
			if scenario == "stale" {
				_, err = h.command(11, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
					return h.m.submitResultOn(ctx, tx, 1, 99, h.work, 11, 11, v-1, h.input)
				})
			} else {
				_, err = h.submit()
			}
			require.Error(t, err)
			require.Equal(t, before, h.snapshot())
		})
	}
}

func TestResultCoverageAndAuthorityGates(t *testing.T) {
	for _, scenario := range []string{"partial", "general", "stale_baseline", "changed_baseline", "no_authority", "denied_authority", "authority_error"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			if scenario == "partial" {
				h.input.Evidence[0].Coverage = "PARTIAL"
			}
			if scenario == "general" {
				h.input.Evidence[0].CriterionID = nil
			}
			r, err := h.submit()
			require.NoError(t, err)
			_, err = h.check(r.ID, 12, "PASSED")
			require.NoError(t, err)
			switch scenario {
			case "stale_baseline":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_stale", true).Error)
			case "changed_baseline":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("baseline_version_no", 9).Error)
			case "no_authority":
				h.m.deliveryAuthority = nil
			case "denied_authority":
				h.deniedAuthority = true
			case "authority_error":
				h.fail = "authority"
			}
			before := h.snapshot()
			_, err = h.accept(r.ID, 13)
			require.Error(t, err)
			require.Equal(t, before, h.snapshot())
		})
	}
	h := newResultFixture(t)
	h.input.Evidence[0].Coverage = "NOT_APPLICABLE"
	h.input.Evidence[0].Note = "This clause does not apply"
	r, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(r.ID, 13)
	require.NoError(t, err)
}

func TestResultCommandsRollbackAllTransactionalEffects(t *testing.T) {
	for _, operation := range []string{"submit", "check", "accept", "handoff", "return", "receipt"} {
		for _, failure := range []string{"audit", "outbox"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				h := newResultFixture(t)
				var r ResultView
				var err error
				if operation != "submit" {
					r, err = h.submit()
					require.NoError(t, err)
				}
				if operation == "accept" || operation == "handoff" || operation == "return" || operation == "receipt" {
					_, err = h.check(r.ID, 12, "PASSED")
					require.NoError(t, err)
				}
				if operation == "handoff" || operation == "return" || operation == "receipt" {
					_, err = h.accept(r.ID, 13)
					require.NoError(t, err)
				}
				var handoff guid.ID
				if operation == "return" || operation == "receipt" {
					pack, e := h.handoff(r.ID)
					require.NoError(t, e)
					handoff = pack.Handoffs[0].ID
				}
				before := h.snapshot()
				h.fail = failure
				switch operation {
				case "submit":
					_, err = h.submit()
				case "check":
					_, err = h.check(r.ID, 12, "PASSED")
				case "accept":
					_, err = h.accept(r.ID, 13)
				case "handoff":
					_, err = h.handoff(r.ID)
				case "return":
					_, err = h.receive(r.ID, handoff, 13, false)
				case "receipt":
					_, err = h.receive(r.ID, handoff, 13, true)
				}
				require.ErrorContains(t, err, failure)
				require.Equal(t, before, h.snapshot())
			})
		}
	}
}

func TestResultActivityRecordsCurrentClaimAndDecisionAndRollsBackFailure(t *testing.T) {
	h := newResultFixture(t)
	before := h.stored().ActivitySeq
	result, err := h.submit()
	require.NoError(t, err)
	require.Equal(t, before+1, h.stored().ActivitySeq)
	read := func() []WorkActivityView {
		var rows []WorkActivityView
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			rows, err = h.m.workActivitySinceOn(h.ctx, tx, 1, h.work, before, 100)
			return err
		}))
		return rows
	}
	rows := read()
	require.Len(t, rows, 1)
	require.Equal(t, "CLAIM_SUBMITTED", rows[0].Kind)
	require.Equal(t, WorkActivityActor{Kind: "HUMAN", ID: 11}, rows[0].Actor)
	require.Equal(t, result.ID, rows[0].Ref.ID)
	h.fail = "outbox"
	_, err = h.check(result.ID, 12, "PASSED")
	require.ErrorContains(t, err, "outbox")
	require.Equal(t, before+1, h.stored().ActivitySeq)
	require.Len(t, read(), 1)
	h.fail = ""
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	rows = read()
	require.Len(t, rows, 2)
	require.Equal(t, "DECISION_MADE", rows[1].Kind)
	require.Equal(t, WorkActivityActor{Kind: "HUMAN", ID: 12}, rows[1].Actor)
	require.Equal(t, before+2, h.stored().ActivitySeq)
}

func TestResultEvidenceDeduplicatesPinsAcrossCriteria(t *testing.T) {
	h := newResultFixture(t)
	criteria := query.BindWorkCriteria(h.db, h.m.service.names)
	second, err := criteria.WithContext(h.ctx).First()
	require.NoError(t, err)
	second.ID++
	second.Position++
	second.Text = "Second acceptance clause"
	require.NoError(t, criteria.WithContext(h.ctx).Create(second))
	var frozen []workVerificationStep
	var original model.WorkItem
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).First(&original).Error)
	require.NoError(t, json.Unmarshal([]byte(original.VerificationPlan), &frozen))
	frozen = append(frozen, workVerificationStep{CriterionID: second.ID, Method: "HUMAN", Detail: "Independent check of second criterion"})
	raw, err := json.Marshal(frozen)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("verification_plan", string(raw)).Error)
	extra := h.input.Evidence[0]
	extra.CriterionID = &second.ID
	h.input.Evidence = append(h.input.Evidence, extra)
	r, err := h.submit()
	require.NoError(t, err)
	require.Len(t, r.Evidence, 2)
	require.Len(t, h.files.pins, 1)
	require.Equal(t, r.Evidence[0].BindingID, r.Evidence[1].BindingID)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(r.ID, 13)
	require.NoError(t, err)
}

func TestResultDependencyReadinessIsRecomputedOnAcceptanceAndReplacement(t *testing.T) {
	h := newResultFixture(t)
	var successors []workDetailView
	for _, ready := range []bool{false, true} {
		var work workDetailView
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			work, err = h.m.createWorkOn(h.ctx, tx, 1, 99, 11, workCreateBody{Title: "Dependent task", Type: "TASK", Objective: "Use the predecessor result", AccountablePrincipalID: 11, Criteria: []workCriterionInput{{Text: "Acceptance", VerificationMethod: "Exercise"}}})
			return err
		}))
		if ready {
			estimate, minutes := 30, 60
			money := "1000"
			prep := WorkPreparationBody{EstimateMinutes: &estimate, Risk: "MEDIUM", AutonomyLevel: "MEDIUM", OutOfScope: []string{}, OutOfScopeConfirmed: true, ContextRefs: []workContextRef{{Kind: "URL", Ref: "https://example.test/context"}}, VerificationPlan: []workVerificationStep{{CriterionID: work.Criteria[0].ID, Method: "HUMAN", Detail: "Actual check"}}, Budget: &workBudget{USDMicros: &money, WallClockMinutes: &minutes, MaxInternalRounds: 5}}
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				_, err := h.m.prepareWorkOn(h.ctx, tx, 1, 99, work.ID, 11, 11, work.Version, &prep)
				return err
			}))
			var err error
			work, err = h.m.readWork(h.ctx, h.db, 1, 99, work.ID, 11)
			require.NoError(t, err)
		} else {
			// Keep a known estimate while leaving the other preparation fields
			// incomplete: the dependency clearing must restore DRAFT, not READY.
			require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("estimate_minutes", 30).Error)
		}
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, err := h.m.changeDependencyOn(h.ctx, tx, 1, 99, work.ID, 11, 11, h.work, work.Version, false)
			return err
		}))
		successors = append(successors, work)
	}
	r, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(r.ID, 13)
	require.NoError(t, err)
	for i, successor := range successors {
		row, err := h.m.readWork(h.ctx, h.db, 1, 99, successor.ID, 11)
		require.NoError(t, err)
		require.Equal(t, []string{"DRAFT", "READY"}[i], row.State)
	}
	_, err = h.submit()
	require.NoError(t, err)
	for _, successor := range successors {
		row, err := h.m.readWork(h.ctx, h.db, 1, 99, successor.ID, 11)
		require.NoError(t, err)
		require.Equal(t, "BLOCKED", row.State)
	}
}

func TestResultCommandsRejectMissingReasonsAndInvalidStates(t *testing.T) {
	h := newResultFixture(t)
	r, err := h.submit()
	require.NoError(t, err)
	for _, command := range []func(context.Context, *gorm.DB, int) (ResultView, error){
		func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
			return h.m.checkResultOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, resultCheckBody{Verdict: "PASSED", Reason: " "})
		},
		func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
			return h.m.checkResultOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, resultCheckBody{Verdict: "UNKNOWN", Reason: "Valid reason"})
		},
		func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
			return h.m.acceptResultOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, resultAcceptBody{})
		},
		func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
			return h.m.handoffResultOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, resultHandoffBody{})
		},
		func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
			return h.m.receiveResultOn(ctx, tx, 1, 99, h.work, r.ID, 1, 12, 11, v, resultReceiptBody{})
		},
	} {
		before := h.snapshot()
		_, err := h.command(12, command)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.Equal(t, before, h.snapshot())
	}
	_, err = h.handoff(r.ID)
	require.ErrorIs(t, err, ErrResultState)
	_, err = h.receive(r.ID, 1, 13, true)
	require.ErrorIs(t, err, ErrResultState)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(r.ID, 13)
	require.NoError(t, err)
	_, err = h.check(r.ID, 12, "PASSED")
	require.ErrorIs(t, err, ErrResultState)
	_, err = h.accept(r.ID, 13)
	require.ErrorIs(t, err, ErrResultState)
	_, err = h.receive(r.ID, 1, 13, true)
	require.ErrorIs(t, err, ErrProjectNotFound)
	_, err = h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.checkResultOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v-1, resultCheckBody{Verdict: "PASSED", Reason: "Stale work"})
	})
	require.ErrorIs(t, err, ErrWorkVersion)
}

func TestResultStorageFailuresRollbackWholeCommand(t *testing.T) {
	cases := []struct {
		operation, kind, table string
		skip                   int
	}{
		{"submit", "create", "versions", 0}, {"submit", "query", "criteria", 0}, {"submit", "create", "evidence", 0}, {"submit", "update", "assignments", 0}, {"submit", "update", "work", 0}, {"submit", "create", "transitions", 0},
		{"submit", "query", "evidence", 0}, {"submit", "query", "actions", 0}, {"submit", "query", "handoffs", 0},
		{"check", "query", "versions", 0}, {"check", "create", "actions", 0}, {"check", "update", "work", 0},
		{"accept", "query", "criteria", 0}, {"accept", "query", "evidence", 0}, {"accept", "query", "actions", 0}, {"accept", "update", "assignments", 0}, {"accept", "query", "dependencies", 0},
		{"handoff", "query", "projects", 1}, {"handoff", "create", "deliveries", 0}, {"handoff", "update", "projects", 0}, {"handoff", "create", "handoffs", 0},
		{"handoff", "query", "evidence", 1}, {"handoff", "query", "actions", 1}, {"handoff", "query", "handoffs", 0},
		{"receipt", "query", "handoffs", 0}, {"receipt", "query", "actions", 1}, {"receipt", "create", "actions", 0}, {"receipt", "update", "deliveries", 0},
	}
	for _, test := range cases {
		t.Run(test.operation+"/"+test.kind+"/"+test.table, func(t *testing.T) {
			h := newResultFixture(t)
			var r ResultView
			var err error
			if test.operation != "submit" {
				r, err = h.submit()
				require.NoError(t, err)
			}
			if test.operation == "accept" || test.operation == "handoff" || test.operation == "receipt" {
				_, err = h.check(r.ID, 12, "PASSED")
				require.NoError(t, err)
			}
			if test.operation == "handoff" || test.operation == "receipt" {
				_, err = h.accept(r.ID, 13)
				require.NoError(t, err)
			}
			var handoff guid.ID
			if test.operation == "receipt" {
				pack, e := h.handoff(r.ID)
				require.NoError(t, e)
				handoff = pack.Handoffs[0].ID
			}
			names := h.m.service.names
			tables := map[string]string{"versions": names.ResultVersions.String(), "criteria": names.WorkCriteria.String(), "evidence": names.ResultEvidence.String(), "assignments": names.Assignments.String(), "work": names.WorkItems.String(), "transitions": names.WorkTransitions.String(), "actions": names.ResultActions.String(), "dependencies": names.Dependencies.String(), "projects": names.Projects.String(), "deliveries": names.Deliveries.String(), "handoffs": names.ResultHandoffs.String()}
			before := h.snapshot()
			sentinel := errors.New("injected storage failure")
			fired := false
			skip := test.skip
			hook := func(tx *gorm.DB) {
				if tx.Statement.Table != tables[test.table] || fired {
					return
				}
				if skip > 0 {
					skip--
					return
				}
				fired = true
				_ = tx.AddError(sentinel)
			}
			switch test.kind {
			case "query":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("result_storage_failure", hook))
			case "create":
				require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("result_storage_failure", hook))
			case "update":
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("result_storage_failure", hook))
			}
			switch test.operation {
			case "submit":
				_, err = h.submit()
			case "check":
				_, err = h.check(r.ID, 12, "PASSED")
			case "accept":
				_, err = h.accept(r.ID, 13)
			case "handoff":
				_, err = h.handoff(r.ID)
			case "receipt":
				_, err = h.receive(r.ID, handoff, 13, true)
			}
			require.True(t, fired, "the failure must reach the intended storage operation")
			require.ErrorIs(t, err, sentinel)
			require.Equal(t, before, h.snapshot())
		})
	}
}

func TestResultIDFailuresNeverLeavePartialRows(t *testing.T) {
	for operation, count := range map[string]int{"submit": 4, "check": 2, "accept": 2, "handoff": 3, "receipt": 2} {
		for position := 0; position < count; position++ {
			t.Run(operation+"/"+string(rune('0'+position)), func(t *testing.T) {
				h := newResultFixture(t)
				var r ResultView
				var err error
				if operation != "submit" {
					r, err = h.submit()
					require.NoError(t, err)
				}
				if operation == "accept" || operation == "handoff" || operation == "receipt" {
					_, err = h.check(r.ID, 12, "PASSED")
					require.NoError(t, err)
				}
				if operation == "handoff" || operation == "receipt" {
					_, err = h.accept(r.ID, 13)
					require.NoError(t, err)
				}
				var handoff guid.ID
				if operation == "receipt" {
					pack, e := h.handoff(r.ID)
					require.NoError(t, e)
					handoff = pack.Handoffs[0].ID
				}
				before := h.snapshot()
				original := h.m.ids
				remaining := position
				sentinel := errors.New("id generator failed")
				h.m.ids = draftIDSource(func() (int64, error) {
					if remaining == 0 {
						return 0, sentinel
					}
					remaining--
					return original.Next()
				})
				switch operation {
				case "submit":
					_, err = h.submit()
				case "check":
					_, err = h.check(r.ID, 12, "PASSED")
				case "accept":
					_, err = h.accept(r.ID, 13)
				case "handoff":
					_, err = h.handoff(r.ID)
				case "receipt":
					_, err = h.receive(r.ID, handoff, 13, true)
				}
				require.ErrorIs(t, err, sentinel)
				require.Equal(t, before, h.snapshot())
			})
		}
	}
}

func TestResultResourceBindingRejectsForeignScopeAndRevokedAccess(t *testing.T) {
	h := newResultFixture(t)
	r, err := h.submit()
	require.NoError(t, err)
	for _, owner := range []files.ResourceOwner{{Scope: "tenant", ID: 1}, {Scope: "entity", ID: 98}} {
		require.ErrorIs(t, h.m.authorizeResultResource(h.ctx, h.db, r.ID, owner), ErrWorkForbidden)
	}
	require.Error(t, h.m.authorizeResultResource(h.ctx, h.db, r.ID+1, files.ResourceOwner{Scope: "entity", ID: 99}))
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Error(t, h.m.authorizeResultResource(h.ctx, h.db, r.ID, files.ResourceOwner{Scope: "entity", ID: 99}), "an existing file binding never bypasses current work access")
}

func TestResultDeliveryAuthorityUsesPersistedPackageTargetAndPropagatesFailures(t *testing.T) {
	h := newResultFixture(t)
	r, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(r.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(r.ID, 13)
	require.NoError(t, err)
	pack, err := h.handoff(r.ID)
	require.NoError(t, err)
	q := query.BindResultVersions(h.db, h.m.service.names)
	row, err := q.WithContext(h.ctx).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	work := h.stored()
	h.m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, in governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		require.Equal(t, governance.ResourceRef{Kind: "delivery", ID: pack.Handoffs[0].DeliveryID}, in.Resource)
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delivery_sender"}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		source, e := h.m.resultAuthorityOn(h.actor(12), tx, work, row, 12, "delivery_send")
		require.Equal(t, "ROLE:delivery_sender", source)
		return e
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.resultAuthorityOn(h.actor(12), tx, work, row, 12, "delivery_send", 0)
		require.ErrorIs(t, e, ErrInvalidArgument)
		return nil
	}))
	require.NoError(t, h.db.Exec("DELETE FROM d_delivery_result_handoffs WHERE result_id=?", r.ID).Error)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.resultAuthorityOn(h.actor(12), tx, work, row, 12, "delivery_send")
		return e
	}))
	h.m.deliveryAuthority = nil
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.resultAuthorityOn(h.actor(12), tx, work, row, 12, "delivery_send")
		return e
	}), ErrUnavailable)
}

func TestResultEvidenceProjectionUsesFrozenPlanAndRealSources(t *testing.T) {
	criterion, task, check := guid.ID(41), guid.ID(51), guid.ID(61)
	for _, tc := range []struct {
		name, method, coverage, state, verdict, receipt, wantCoverage, wantReason string
		task, check                                                               bool
	}{
		{name: "missing", method: "COMMAND", wantCoverage: "MISSING", wantReason: "MISSING_EVIDENCE"},
		{name: "partial", method: "COMMAND", coverage: "PARTIAL", wantCoverage: "PARTIAL", wantReason: "PARTIAL_EVIDENCE"},
		{name: "attachment_is_not_verification", method: "COMMAND", coverage: "COVERED", wantCoverage: "COVERED", wantReason: "VERIFICATION_PENDING"},
		{name: "not_applicable_is_not_covered", method: "COMMAND", coverage: "NOT_APPLICABLE", wantCoverage: "MISSING", wantReason: "MISSING_EVIDENCE"},
		{name: "pending_receipt", method: "COMMAND", state: "PENDING", verdict: "PASSED", receipt: strings.Repeat("a", 64), task: true, wantCoverage: "MISSING", wantReason: "MISSING_EVIDENCE"},
		{name: "unsourced_pass", method: "COMMAND", state: "DONE", verdict: "PASSED", wantCoverage: "MISSING", wantReason: "MISSING_EVIDENCE"},
		{name: "real_pass", method: "COMMAND", state: "DONE", verdict: "PASSED", receipt: strings.Repeat("a", 64), task: true, wantCoverage: "COVERED"},
		{name: "real_failure", method: "COMMAND", state: "DONE", verdict: "FAILED", receipt: strings.Repeat("a", 64), task: true, wantCoverage: "COVERED", wantReason: "VERIFICATION_FAILED"},
		{name: "real_inconclusive", method: "COMMAND", state: "DONE", verdict: "INCONCLUSIVE", receipt: strings.Repeat("a", 64), task: true, wantCoverage: "COVERED", wantReason: "VERIFICATION_INCONCLUSIVE"},
		{name: "human_deferred", method: "HUMAN", wantCoverage: "MISSING", wantReason: "MISSING_EVIDENCE"},
		{name: "human_check", method: "HUMAN", state: "DONE", verdict: "PASSED", check: true, wantCoverage: "COVERED"},
		{name: "agent_test", method: "AGENT_JUDGE", state: "DONE", verdict: "PASSED", receipt: strings.Repeat("a", 64), task: true, wantCoverage: "COVERED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _ := json.Marshal([]workVerificationStep{{CriterionID: criterion, Method: tc.method}})
			row := &model.ResultVersion{VerificationPlan: string(plan)}
			v := ResultView{}
			if tc.coverage != "" {
				v.Evidence = []resultEvidenceView{{ID: 71, CriterionID: &criterion, Coverage: tc.coverage}, {ID: 72, Coverage: "COVERED"}}
			}
			if tc.state != "" {
				stage := "AUTOMATED"
				if tc.method == "HUMAN" {
					stage = "HUMAN"
				}
				if tc.method == "AGENT_JUDGE" {
					stage = "AGENT_TEST"
				}
				run := verificationView{ID: 81, CriterionID: criterion, Method: tc.method, Stage: stage, State: tc.state, Verdict: tc.verdict, ReceiptHash: tc.receipt}
				if tc.task {
					run.TaskID = &task
				}
				if tc.check {
					run.CheckID = &check
				}
				v.Verifications = []verificationView{run}
			}
			require.NoError(t, resultEvidenceProjection(row, &v))
			require.Len(t, v.CriteriaMap, 1)
			require.Equal(t, tc.wantCoverage, v.CriteriaMap[0].Coverage)
			if tc.wantReason == "" {
				require.Empty(t, v.Unverified)
			} else {
				require.Equal(t, []resultUnverifiedView{{CriterionID: criterion, Method: tc.method, Reason: tc.wantReason}}, v.Unverified)
			}
			require.NotNil(t, v.Unverified)
			require.NotNil(t, v.CriteriaMap[0].EvidenceIDs)
			require.NotNil(t, v.CriteriaMap[0].VerificationIDs)
		})
	}
	require.Error(t, resultEvidenceProjection(&model.ResultVersion{VerificationPlan: "{"}, &ResultView{}))
	var empty ResultView
	require.NoError(t, resultEvidenceProjection(&model.ResultVersion{}, &empty))
	require.NotNil(t, empty.Unverified)
}

func TestResultEvidenceProjectionLatestAttemptCannotReusePriorPass(t *testing.T) {
	criterion, task := guid.ID(41), guid.ID(51)
	plan, _ := json.Marshal([]workVerificationStep{{CriterionID: criterion, Method: "COMMAND"}})
	v := ResultView{Verifications: []verificationView{
		{ID: 81, CriterionID: criterion, Method: "COMMAND", Stage: "AUTOMATED", State: "DONE", Verdict: "PASSED", TaskID: &task, ReceiptHash: strings.Repeat("a", 64)},
		{ID: 82, CriterionID: criterion, Method: "COMMAND", Stage: "AUTOMATED", State: "PENDING", TaskID: &task},
		{ID: 83, CriterionID: 99, Method: "COMMAND", Stage: "AUTOMATED", State: "DONE", Verdict: "PASSED", TaskID: &task, ReceiptHash: strings.Repeat("a", 64)},
	}}
	require.NoError(t, resultEvidenceProjection(&model.ResultVersion{VerificationPlan: string(plan)}, &v))
	require.Equal(t, "PENDING", v.CriteriaMap[0].Verdict)
	require.Equal(t, "MISSING_EVIDENCE", v.Unverified[0].Reason)
	require.Equal(t, []guid.ID{81, 82}, v.CriteriaMap[0].VerificationIDs)
}

func TestResultAcceptanceEvidenceDefersOnlyHumanAndPreservesOverrideFailure(t *testing.T) {
	h, r, in := verificationFixture(t)
	row, err := h.m.resultOn(h.ctx, h.db, 1, 99, h.work, r.ID)
	require.NoError(t, err)
	require.ErrorIs(t, h.m.claimAcceptanceEvidenceOn(h.ctx, h.db, row), ErrVerificationPending)
	_, err = h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.startVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v, in)
	})
	require.NoError(t, err)
	h.m.verificationExecutor.ReceiptOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) (VerificationReceipt, error) {
		exit := 1
		return VerificationReceipt{TaskID: 900, ResultID: r.ID, ExecutionID: 880, CriterionID: in.CriterionID, ResultVersion: r.Sequence, Method: "COMMAND", Verdict: "FAILED", Reason: "NONZERO_EXIT", ReceiptHash: strings.Repeat("b", 64), PlanHash: strings.Repeat("c", 64), HeadCommit: strings.Repeat("a", 40), ExitCode: &exit}, nil
	}
	failed, err := h.command(12, func(ctx context.Context, tx *gorm.DB, v int) (ResultView, error) {
		return h.m.collectVerificationOn(ctx, tx, 1, 99, h.work, r.ID, 12, 11, v)
	})
	require.NoError(t, err)
	require.Equal(t, "VERIFICATION_FAILED", failed.Unverified[0].Reason)
	require.NoError(t, h.m.claimAcceptanceEvidenceOn(h.ctx, h.db, row), "stage gate, not evidence presence, checks exact Human exceptions")
	require.False(t, failed.Completion.L2)
	human, _ := json.Marshal([]workVerificationStep{{CriterionID: in.CriterionID, Method: "HUMAN"}})
	row.VerificationPlan = string(human)
	require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", row.ID).Update("verification_plan", row.VerificationPlan).Error)
	require.NoError(t, h.m.claimAcceptanceEvidenceOn(h.ctx, h.db, row))
}

func TestResultHandoffFreezesCoverageAndUnverifiedArrays(t *testing.T) {
	h := newResultFixture(t)
	r, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(r.ID, 12, "PASSED")
	require.NoError(t, e)
	_, e = h.accept(r.ID, 12)
	require.NoError(t, e)
	pack, e := h.handoff(r.ID)
	require.NoError(t, e)
	require.Len(t, pack.Handoffs, 1)
	var manifest struct {
		CriteriaMap []resultCriterionView  `json:"criteria_map"`
		Unverified  []resultUnverifiedView `json:"unverified"`
	}
	require.NoError(t, json.Unmarshal([]byte(pack.Handoffs[0].Manifest), &manifest))
	require.NotEmpty(t, manifest.CriteriaMap)
	require.NotNil(t, manifest.Unverified)
	require.Empty(t, manifest.Unverified)
	require.Equal(t, pack.CriteriaMap, manifest.CriteriaMap)
	digest := sha256.Sum256([]byte(pack.Handoffs[0].Manifest))
	require.Equal(t, hex.EncodeToString(digest[:]), pack.Handoffs[0].SHA256)
}

func TestResultDependentRecheckStorageAndCurrentOwnerFailuresRollBackState(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	_, e = h.accept(result.ID, 13)
	require.NoError(t, e)
	source := h.stored()
	successor := *source
	successor.ID = source.ID + 100
	successor.Number += 100
	successor.State = "BLOCKED"
	successor.CurrentClaimID = nil
	successor.AcceptedAt = nil
	successor.AcceptedDecisionID = nil
	successor.VerificationPlan = "[]"
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Create(&successor).Error)
	require.NoError(t, h.db.Table(h.m.service.names.Dependencies.String()).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: successor.ID, DependsOnID: source.ID}).Error)
	failure := errors.New("dependent source unavailable")
	rollback := errors.New("rollback dependent recheck")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("dependent-recheck-storage", probe))
	require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("dependent-recheck-storage", probe))
	require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("dependent-recheck-storage", probe))
	run := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			if e := h.m.recomputeResultDependantsOn(h.ctx, tx, source, 12, time.Now().UTC()); e != nil {
				return e
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 3)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "dependent stage %d", i)
	}
	require.NoError(t, h.db.Callback().Query().Remove("dependent-recheck-storage"))
	require.NoError(t, h.db.Callback().Create().Remove("dependent-recheck-storage"))
	require.NoError(t, h.db.Callback().Update().Remove("dependent-recheck-storage"))
	human := h.m.principals.ActiveHumanByID
	h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, failure
	}
	require.ErrorIs(t, run(), failure)
	h.m.principals.ActiveHumanByID = human
	members := h.m.members
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, failure })
	require.ErrorIs(t, run(), failure)
	h.m.members = members
	var after model.WorkItem
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", successor.ID).First(&after).Error)
	require.Equal(t, successor, after)
}

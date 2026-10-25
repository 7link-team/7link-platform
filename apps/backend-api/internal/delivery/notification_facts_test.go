package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAndonInboxRechecksHumanRowScopeAndClear(t *testing.T) {
	h := newResultFixture(t)
	allow, activeHuman := true, true
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: allow}, nil
	}
	h.m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, activeHuman, nil
	}
	h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	change := func(clearing bool) {
		before := h.stored()
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, err := h.m.changeAndonOn(h.actor(11), tx, 1, 99, h.work, 11, 11, andonBody{Version: before.Version, Reason: "Investigate actual source evidence"}, clearing)
			return err
		}))
	}
	change(false)
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 12, ProjectID: 99, Kind: "ANDON", Limit: 1}
	read := func() []governance.InboxItem {
		rows, err := h.m.andonInbox(h.actor(12), req, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil })
		require.NoError(t, err)
		return rows
	}
	items := read()
	require.Len(t, items, 1)
	require.Equal(t, "CRITICAL", items[0].Priority)
	require.Equal(t, h.work, items[0].ObjectID)
	req.After = &items[0].InboxKey
	require.Empty(t, read(), "keyset excludes current episode")
	req.After = nil
	allow = false
	require.Empty(t, read(), "lead alone cannot bypass current row access")
	allow, activeHuman = true, false
	require.Empty(t, read(), "Agent and inactive Human excluded")
	activeHuman = true
	change(true)
	require.Empty(t, read(), "actual clear closes the inbox item")
	_, err := h.m.andonInbox(h.actor(11), req, nil)
	require.ErrorIs(t, err, governance.ErrForbidden)
}

func TestHomeFactsPropagateEveryFailedStorageRead(t *testing.T) {
	h := newResultFixture(t)
	_, err := h.submit()
	require.NoError(t, err)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	start := time.Now().UTC().Truncate(24 * time.Hour)
	injected := errors.New("home facts unavailable")
	queries, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("home-source-failure", func(tx *gorm.DB) {
		queries++
		if queries == failAt {
			tx.AddError(injected)
		}
	}))
	defer h.db.Callback().Query().Remove("home-source-failure")
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.homeFactsOn(h.ctx, tx, 1, 99, start, start.Add(24*time.Hour))
			return e
		})
	}
	require.NoError(t, read())
	total := queries
	require.Greater(t, total, 5)
	for i := 1; i <= total; i++ {
		queries, failAt = 0, i
		require.ErrorIs(t, read(), injected, "read %d of %d must not fabricate an empty dashboard", i, total)
	}
}

func TestHumanVerificationNoticePreservesEachStorageFailureAndCurrentScope(t *testing.T) {
	h := newResultFixture(t)
	claim, e := h.submit()
	require.NoError(t, e)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	injected := errors.New("verification notice unavailable")
	reads, failAt := 0, 0
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("verification-notice-failure", func(tx *gorm.DB) {
		reads++
		if reads == failAt {
			tx.AddError(injected)
		}
	}))
	defer h.db.Callback().Query().Remove("verification-notice-failure")
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			_, _, _, e := h.m.humanVerificationNotificationOn(h.actor(12), tx, 1, h.work, 12)
			return e
		})
	}
	require.NoError(t, read())
	total := reads
	require.Greater(t, total, 4)
	for i := 1; i <= total; i++ {
		reads, failAt = 0, i
		require.ErrorIs(t, read(), injected, "notice read %d", i)
	}
	failAt = 0
	for _, fault := range []string{"read_denied", "write_denied", "write_error", "hidden", "scope", "malformed"} {
		t.Run(fault, func(t *testing.T) {
			h.m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
				if fault == "read_denied" && permission == "work_item_read" || fault == "write_denied" && permission == "work_item_write" {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
				if permission == "work_item_write" {
					switch fault {
					case "write_error":
						return authz.DataConstraint{}, injected
					case "hidden":
						return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
					case "scope":
						return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
					}
				}
				return authz.DataConstraint{AllowAll: true}, nil
			}
			if fault == "malformed" {
				require.NoError(t, h.db.Table(h.m.service.names.ResultVersions.String()).Where("id=?", claim.ID).Update("verification_plan", "{}").Error)
			}
			var eligible bool
			e := h.db.Transaction(func(tx *gorm.DB) error {
				_, _, yes, e := h.m.humanVerificationNotificationOn(h.actor(12), tx, 1, h.work, 12)
				eligible = yes
				return e
			})
			require.False(t, eligible)
			if fault == "write_error" {
				require.ErrorIs(t, e, injected)
			} else if fault == "scope" || fault == "malformed" {
				require.Error(t, e)
			} else {
				require.NoError(t, e)
			}
		})
	}
}

func TestAndonFactsRequireNamedCurrentPullTransition(t *testing.T) {
	m, db, ctx, work, _ := dependencyHarness(t)
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	read := func(tenant guid.ID) (AndonNotificationFact, bool) {
		f, found, err := m.andonNotificationOn(ctx, db, tenant, work.ID)
		require.NoError(t, err)
		return f, found
	}
	_, found := read(1)
	require.False(t, found)
	change := func(version int, clearing bool) {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.changeAndonOn(ctx, tx, 1, 99, work.ID, 11, 11, andonBody{Version: version, Reason: "Investigate actual source evidence"}, clearing)
			return err
		}))
	}
	change(work.Version, false)
	f, found := read(1)
	require.True(t, found)
	require.Positive(t, f.OccurrenceID)
	require.Equal(t, work.ID, f.WorkItemID)
	_, found = read(2)
	require.False(t, found, "tenant fence")
	change(work.Version+1, true)
	_, found = read(1)
	require.False(t, found, "clear closes current source")
	change(work.Version+2, false)
	next, found := read(1)
	require.True(t, found)
	require.NotEqual(t, f.OccurrenceID, next.OccurrenceID)
	q := query.BindWorkTransitions(db, m.service.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(next.OccurrenceID)).Delete()
	require.NoError(t, err)
	_, found = read(1)
	require.False(t, found, "a named hold without its actual persisted pull cannot notify")
}

func TestStageReadyPublishesActualCriteriaWithGlobalSourceThrottle(t *testing.T) {
	m, db, ctx, _ := stageHarness(t)
	q := query.BindProjects(db, m.service.names)
	_, err := q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).Update(q.Stage, "INTAKE")
	require.NoError(t, err)
	var prior []events.ProjectOccurrence
	history := func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, types []string, since time.Time) ([]events.ProjectOccurrence, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.Equal(t, []string{"project.stage_ready"}, types)
		require.WithinDuration(t, time.Now().Add(-24*time.Hour), since, time.Second)
		return prior, nil
	}
	published, audited := 0, 0
	m.service.events = projectPublisher(func(_ context.Context, _ *gorm.DB, e events.Event) (guid.ID, error) {
		published++
		require.Equal(t, "project.stage_ready", e.Type)
		require.Equal(t, "SYSTEM", e.Actor.Kind)
		data := e.Data.(map[string]any)
		require.Equal(t, "DISCOVERY", data["target"])
		require.NotEmpty(t, data["criteria_passed"])
		return 77, nil
	})
	m.service.audit = func(_ context.Context, _ *gorm.DB, e audit.DomainEvent) error {
		audited++
		require.Zero(t, e.PrincipalID)
		require.Equal(t, "SYSTEM", e.Detail["actor_kind"])
		return nil
	}
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.publishStageReadyOn(t.Context(), tx, 1, 99, history) })
	}
	require.NoError(t, run())
	require.Equal(t, 1, published)
	prior = []events.ProjectOccurrence{{Target: "DISCOVERY", At: time.Now()}}
	require.NoError(t, run())
	require.Equal(t, 1, published, "source receipt suppresses all recipients together")
	require.Equal(t, 1, audited)
	prior = nil
	failure := errors.New("audit failed")
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, run(), failure)
	require.Equal(t, 1, published)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.publishStageReadyOn(ctx, tx, 1, 99, history) }), ErrWorkForbidden)
	row, err := q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).First()
	require.NoError(t, err)
	require.Equal(t, "INTAKE", row.Stage)

	// A later publisher failure must roll back an already appended audit row.
	require.NoError(t, db.Exec("CREATE TABLE regression_stage_atomic_receipts (kind TEXT NOT NULL)").Error)
	m.service.audit = func(_ context.Context, tx *gorm.DB, _ audit.DomainEvent) error {
		return tx.Exec("INSERT INTO regression_stage_atomic_receipts(kind) VALUES ('audit')").Error
	}
	m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, _ events.Event) (guid.ID, error) {
		if err := tx.Exec("INSERT INTO regression_stage_atomic_receipts(kind) VALUES ('outbox')").Error; err != nil {
			return 0, err
		}
		return 0, failure
	})
	require.ErrorIs(t, run(), failure)
	var receipts int64
	require.NoError(t, db.Table("regression_stage_atomic_receipts").Count(&receipts).Error)
	require.Zero(t, receipts)
}

func TestVerificationInboxTracksExactHumanInstructionAndExhaustion(t *testing.T) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	claim, err := h.submit()
	require.NoError(t, err)
	checkNotice := func(actor guid.ID) bool {
		var yes bool
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			id, project, eligible, err := h.m.humanVerificationNotificationOn(h.actor(actor), tx, 1, h.work, actor)
			if eligible {
				require.Equal(t, claim.ID, id)
				require.EqualValues(t, 99, project)
			}
			yes = eligible
			return err
		}))
		return yes
	}
	require.True(t, checkNotice(12))
	require.False(t, checkNotice(11))
	read := func(actor guid.ID, kind string) []governance.InboxItem {
		rows, err := h.m.verificationInboxWithLeads(h.actor(actor), governance.InboxRequest{TenantID: 1, PrincipalID: actor, ProjectID: 99, Kind: kind, Limit: 10}, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return actor == 12, nil })
		require.NoError(t, err)
		return rows
	}
	rows := read(12, "HUMAN_VERIFICATION")
	require.Len(t, rows, 1)
	require.Equal(t, claim.ID, rows[0].ID)
	require.Empty(t, read(11, "HUMAN_VERIFICATION"), "claim author cannot verify own result")
	require.Empty(t, read(12, "ESCALATION_INTERNAL_EXHAUSTED"), "ordinary review is not exhaustion")
	work := h.stored()
	reason := "INTERNAL_ROUNDS_EXHAUSTED"
	work.EscalationReason, work.RequireHumanDecision = &reason, true
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("tenant_id = ? AND id = ?", 1, h.work).Updates(map[string]any{"escalation_reason": reason, "require_human_decision": true}).Error)
	require.Len(t, read(12, "ESCALATION_INTERNAL_EXHAUSTED"), 1)
	_, err = h.check(claim.ID, 12, "PASSED")
	require.NoError(t, err)
	require.False(t, checkNotice(12), "a completed Human instruction cannot remain actionable")
	require.Empty(t, read(12, "HUMAN_VERIFICATION"), "canonical Human CHECK completes the exact instruction")
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Empty(t, read(12, "ESCALATION_INTERNAL_EXHAUSTED"), "current source revocation removes the item")
}

func TestLeaseNotificationRequiresActualExpiredHumanAssignment(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	m.assignmentGuard = func(context.Context, *gorm.DB, string) error { return nil }
	claimed := assignmentClaim(t, m, db, ctx, work, false)
	read := func(tenant guid.ID) bool {
		var found bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			fact, yes, err := m.leaseExpiredNotificationOn(ctx, tx, tenant, claimed.Assignment.ID)
			if yes {
				require.Equal(t, work.ID, fact.WorkItemID)
				require.Equal(t, claimed.Assignment.AssigneePrincipalID, fact.AssigneeID)
			}
			found = yes
			return err
		}))
		return found
	}
	require.False(t, read(1))
	at := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("id=?", claimed.Assignment.ID).Update("lease_expires_at", at.Add(-61*time.Second)).Error)
	require.NoError(t, m.reclaimHumanAssignments(ctx, 1, at))
	require.True(t, read(1))
	require.False(t, read(2))
}

func TestHomeFactsUseFirstHistoricalAcceptanceAndDailyPinnedEvidence(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	start := time.Now().UTC().Truncate(24 * time.Hour)
	for i, state := range []string{"REWORK", "ACCEPTED", "REWORK", "ACCEPTED"} {
		row := model.WorkItemTransition{TenantID: 1, ID: guid.ID(800 + i), ProjectID: 99, WorkItemID: work.ID, ToState: state, Trigger: "COMMAND", ActorKind: "HUMAN", VersionAfter: i + 2, CreatedAt: start.Add(time.Duration(i) * time.Hour)}
		require.NoError(t, db.Table(m.service.names.WorkTransitions.String()).Create(&row).Error)
	}
	for i := range 3 {
		id := guid.ID(900 + i)
		at := start.Add(6 * time.Hour)
		if i == 2 {
			at = at.Add(24 * time.Hour)
		}
		r := model.ResultVersion{TenantID: 1, ID: id, ProjectID: 99, WorkItemID: work.ID, Sequence: i + 1, Summary: "Actual completion claim", VerificationPlan: "[]", ActorID: 11, CreatedAt: at}
		require.NoError(t, db.Table(m.service.names.ResultVersions.String()).Create(&r).Error)
		if i == 1 {
			pin := model.ResultEvidence{TenantID: 1, ID: 1000, ProjectID: 99, WorkItemID: work.ID, ResultID: id, BindingID: 1001, VersionID: 1002, Coverage: "COVERED", SHA256: strings.Repeat("a", 64)}
			require.NoError(t, db.Table(m.service.names.ResultEvidence.String()).Create(&pin).Error)
		}
	}
	var facts HomeProjectFacts
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		facts, err = m.homeFactsOn(ctx, tx, 1, 99, start, start.Add(72*time.Hour))
		return err
	}))
	require.Len(t, facts.FirstAcceptances, 1)
	require.Equal(t, start.Add(time.Hour), facts.FirstAcceptances[0].At)
	require.True(t, facts.FirstAcceptances[0].PriorQualityRework)
	require.Equal(t, 2, facts.ClaimedDays)
	require.Equal(t, 1, facts.EvidencedDays)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		facts, err = m.homeFactsOn(ctx, tx, 1, 99, start.Add(2*time.Hour), start.Add(72*time.Hour))
		return err
	}))
	require.Empty(t, facts.FirstAcceptances, "reaccepting never increases the first-acceptance denominator")
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.homeFactsOn(ctx, tx, 1, 99, start, start.Add(72*time.Hour))
		return err
	}))
}

func TestQualityReviewWindowCountsDistinctTransitionsWithCurrentReadScope(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	allow := true
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: allow}, nil
	}
	start := time.Now().UTC().Truncate(24 * time.Hour)
	for i, at := range []time.Time{start.Add(-time.Millisecond), start, start.Add(time.Hour), start.Add(24 * time.Hour)} {
		row := model.WorkItemTransition{TenantID: 1, ID: guid.ID(5000 + i), ProjectID: 99, WorkItemID: work.ID, ToState: "NEEDS_HUMAN_REVIEW", Trigger: "COMMAND", ActorKind: "HUMAN", VersionAfter: i + 2, CreatedAt: at}
		require.NoError(t, db.Table(m.service.names.WorkTransitions.String()).Create(&row).Error)
	}
	check := func() (governance.QualityReviewWindow, error) {
		var out governance.QualityReviewWindow
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.qualityReviewWindowOn(ctx, tx, 1, 99, start, start.Add(24*time.Hour))
			return e
		})
		return out, err
	}
	out, err := check()
	require.NoError(t, err)
	require.Equal(t, 1, out.Entered)
	require.Equal(t, []guid.ID{work.ID}, out.WorkIDs)
	allow = false
	_, err = check()
	require.Error(t, err)
}

func TestPlanNotificationCurrentSourceAndEligibility(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	allow := true
	readAllowed := true
	var policyErr error
	m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: allow && (permission != "work_item_read" || readAllowed)}, policyErr
	}
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	content := ReviewPlanContent{OutOfScopeConfirmed: true}
	for _, c := range work.Criteria {
		content.CriteriaCoverage = append(content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement criterion", Verification: "Run regression"})
	}
	var plan ReviewPlanView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		plan, err = m.proposeReviewPlanOn(ctx, tx, 1, 99, work.ID, 11, work.AccountablePrincipalID, work.Version, content)
		return err
	}))
	reviewer := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12})
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 12, ProjectID: 99, Kind: "PLAN_APPROVAL", Limit: 1}
	items, err := m.planApprovalInbox(reviewer, req)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, plan.ID, items[0].ObjectID)
	require.Equal(t, "review_plan", items[0].ObjectType)
	req.After = &items[0].InboxKey
	items, err = m.planApprovalInbox(reviewer, req)
	require.NoError(t, err)
	require.Empty(t, items)
	req.After = nil
	_, err = m.planApprovalInbox(ctx, req)
	require.ErrorIs(t, err, ErrWorkForbidden)
	readAllowed = false
	items, err = m.planApprovalInbox(reviewer, req)
	require.NoError(t, err)
	require.Empty(t, items, "approval permission never substitutes for source read authority")
	readAllowed = true
	_, _, err = m.decisionNotificationFactsOn(ctx, db, 1, plan.ID, "plan")
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		f, found, err := m.decisionNotificationFactsOn(ctx, tx, 1, plan.ID, "plan")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, guid.ID(11), f.AuthorID)
		require.Equal(t, work.AccountablePrincipalID, f.AccountableID)
		require.Equal(t, work.ID, f.WorkItemID)
		_, found, err = m.decisionNotificationFactsOn(ctx, tx, 2, plan.ID, "plan")
		require.NoError(t, err)
		require.False(t, found)
		eligible, err := m.planNotificationEligibleOn(ctx, tx, 1, plan.ID, 11)
		require.NoError(t, err)
		require.False(t, eligible, "author cannot review own proposal")
		eligible, err = m.planNotificationEligibleOn(ctx, tx, 1, plan.ID, 12)
		require.NoError(t, err)
		require.True(t, eligible)
		pending, err := m.pendingHumanPlansOn(reviewer, tx, 1, 99)
		require.NoError(t, err)
		require.Len(t, pending, 1)
		require.Equal(t, plan.ID, pending[0].ID)
		pending, err = m.pendingHumanPlansOn(reviewer, tx, 1, 98)
		require.NoError(t, err)
		require.Empty(t, pending, "project boundary")
		_, err = m.pendingHumanPlansOn(reviewer, tx, 2, 99)
		require.ErrorIs(t, err, ErrWorkForbidden)
		pending, err = m.pendingHumanPlansOn(authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 11}), tx, 1, 99)
		require.NoError(t, err)
		require.Empty(t, pending, "independent Human approval")
		allow = false
		eligible, err = m.planNotificationEligibleOn(ctx, tx, 1, plan.ID, 12)
		require.NoError(t, err)
		require.False(t, eligible)
		pending, err = m.pendingHumanPlansOn(reviewer, tx, 1, 99)
		require.NoError(t, err)
		require.Empty(t, pending, "current IAM revocation")
		allow = true
		policyErr = errors.New("IAM unavailable")
		_, err = m.planNotificationEligibleOn(ctx, tx, 1, plan.ID, 12)
		require.ErrorIs(t, err, policyErr)
		policyErr = nil
		q := query.BindReviewPlans(tx, m.service.names)
		_, err = q.WithContext(ctx).Where(q.ID.Eq(plan.ID)).Update(q.State, "SUPERSEDED")
		require.NoError(t, err)
		eligible, err = m.planNotificationEligibleOn(ctx, tx, 1, plan.ID, 12)
		require.NoError(t, err)
		require.False(t, eligible)
		pending, err = m.pendingHumanPlansOn(reviewer, tx, 1, 99)
		require.NoError(t, err)
		require.Empty(t, pending, "superseded source")
		return nil
	}))
}

func TestDecisionBindingPreservesExplicitMissingSourceCommand(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	s := module.NewServices()
	var bound governance.AutomatedDecisionSource
	require.NoError(t, module.Provide[governance.AutomatedDecisionConnector](s, governance.AutomatedDecisionConnector{Bind: func(source governance.AutomatedDecisionSource) error { bound = source; return nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindAutomatedDecisions(s))
	require.NotNil(t, bound.ResolveOn)
	require.Nil(t, bound.ApplyOn, "never impersonate a Human to call canonical Human-only acceptance")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, current, err := bound.ResolveOn(ctx, tx, 1, 99, work.ID, 777)
		require.NoError(t, err)
		require.False(t, current)
		_, _, err = bound.ResolveOn(context.Background(), nil, 1, 99, work.ID, 777)
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
}

func TestHeavyReferencesUseActualScopedWorkAndExecutor(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claimed := assignmentClaim(t, m, db, ctx, work, false)
	allow := true
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: allow}, nil
	}
	check := func(tenant, project, workID, assignment guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error {
			return m.heavyPlanningReferenceOn(ctx, tx, tenant, project, workID, assignment)
		})
	}
	require.NoError(t, check(1, 99, work.ID, claimed.Assignment.ID))
	for _, ids := range [][4]guid.ID{{2, 99, work.ID, claimed.Assignment.ID}, {1, 98, work.ID, claimed.Assignment.ID}, {1, 99, work.ID + 1, claimed.Assignment.ID}, {1, 99, work.ID, claimed.Assignment.ID + 1}} {
		require.Error(t, check(ids[0], ids[1], ids[2], ids[3]))
	}
	allow = false
	require.Error(t, check(1, 99, work.ID, claimed.Assignment.ID))
	allow = true
	q := query.BindAssignments(db, m.service.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(claimed.Assignment.ID)).Update(q.Role, "REVIEWER")
	require.NoError(t, err)
	require.Error(t, check(1, 99, work.ID, claimed.Assignment.ID))
	require.ErrorIs(t, m.heavyPlanningReferenceOn(ctx, db, 1, 99, work.ID, claimed.Assignment.ID), ErrInvalidArgument)
}

func TestReviewNotificationsRequirePendingFrozenReviewerAndCurrentTargetRead(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	var sheet RequirementReviewSheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11, 12}, Minimum: 2})
		return err
	}))
	check := func(actor guid.ID, typ string) bool {
		var yes bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			yes, err = m.reviewSheetNotificationEligibleOn(ctx, tx, 1, sheet.ID, actor, typ)
			return err
		}))
		return yes
	}
	require.True(t, check(12, "review_sheet.opened"))
	require.False(t, check(13, "review_sheet.opened"))
	sheet.Document.Opinions = []RequirementReviewOpinion{{ReviewerID: 12, Decision: "PASS"}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		q := query.BindReviewSheets(tx, m.service.names)
		row, err := q.WithContext(ctx).Where(q.ID.Eq(sheet.ID)).First()
		if err != nil {
			return err
		}
		_, err = m.saveSheetOn(ctx, tx, row, sheet.Document, "review_sheet.opinion_submitted")
		return err
	}))
	require.False(t, check(12, "review_sheet.due"))
	require.True(t, check(11, "review_sheet.due"))
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.False(t, check(11, "review_sheet.opened"))
}

func TestConstitutionNoticeRechecksActualReviewStateAndCurrentHuman(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	proposed := constitutionProposal(t, m, db, ctx)
	check := func() bool {
		var yes bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			project, allowed, err := m.constitutionNotificationEligibleOn(ctx, tx, 1, proposed.ID, 11)
			require.EqualValues(t, 99, project)
			yes = allowed
			return err
		}))
		return yes
	}
	require.False(t, check())
	proposed = constitutionChange(t, m, db, ctx, proposed, nil)
	require.True(t, check())
	proposed = constitutionChange(t, m, db, ctx, proposed, &constitutionDecisionBody{Result: "APPROVE"})
	require.False(t, check())
}

func TestDeadlineFactsUseStateChangeRatherThanMutableUpdateTime(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	at := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Updates(map[string]any{"state": "NEEDS_HUMAN_REVIEW", "state_changed_at": at, "updated_at": at.Add(time.Hour)}).Error)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"stage": "BLOCKED", "blocked_kind": "DEPENDENCY", "blocked_reason": "External prerequisite", "stage_changed_at": at, "updated_at": at.Add(time.Hour)}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, kind := range []string{"review_age", "project_blocked"} {
			id := work.ID
			if kind == "project_blocked" {
				id = 99
			}
			fact, found, err := m.deadlineNotificationOn(ctx, tx, 1, id, kind)
			require.NoError(t, err)
			require.True(t, found)
			require.True(t, fact.Active)
			require.True(t, at.Equal(fact.ChangedAt))
			_, found, err = m.deadlineNotificationOn(ctx, tx, 2, id, kind)
			require.NoError(t, err)
			require.False(t, found)
		}
		return nil
	}))
}

func TestCancelledDependentsRecheckCurrentRelationsAndTenant(t *testing.T) {
	m, db, ctx, _ := assignmentHarness(t, true)
	q := query.BindWorkItems(db, m.service.names)
	work, err := q.WithContext(ctx).First()
	require.NoError(t, err)
	pred := *work
	pred.ID, pred.Number, pred.State = work.ID+1, work.Number+1, "CANCELLED" //nolint:misspell // Canonical domain status.
	require.NoError(t, q.WithContext(ctx).Create(&pred))
	d := query.BindDependencies(db, m.service.names)
	require.NoError(t, d.WithContext(ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, DependsOnID: pred.ID}))
	read := func(tenant guid.ID) []DependencyNotificationFact {
		facts, err := m.cancelledDependentsOn(ctx, db, tenant, pred.ID)
		require.NoError(t, err)
		return facts
	}
	require.Len(t, read(1), 1)
	require.Empty(t, read(2))
	_, err = q.WithContext(ctx).Where(q.ID.Eq(pred.ID)).Update(q.State, "DRAFT")
	require.NoError(t, err)
	require.Empty(t, read(1), "a non-canceled predecessor is not a canceled dependency")
	_, err = q.WithContext(ctx).Where(q.ID.Eq(pred.ID)).Update(q.State, "CANCELLED") //nolint:misspell // Canonical domain status.
	require.NoError(t, err)
	_, err = d.WithContext(ctx).Where(d.WorkItemID.Eq(work.ID)).Delete()
	require.NoError(t, err)
	require.Empty(t, read(1), "manual relation removal resolves the warning")
}

func TestStageSuggestionUsesCanonicalGuardAndPreservesProject(t *testing.T) {
	m, db, ctx, _ := stageHarness(t)
	m.stagePackages.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (ProjectStagePackageFacts, error) {
		return ProjectStagePackageFacts{}, nil
	}
	q := query.BindProjects(db, m.service.names)
	_, err := q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).Update(q.Stage, "INTAKE")
	require.NoError(t, err)
	var target string
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		target, err = m.stageNotificationSuggestionOn(ctx, tx, 1, 99)
		return err
	}))
	require.Equal(t, "DISCOVERY", target, "real work already provides a canonical source")
	row, err := q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).First()
	require.NoError(t, err)
	require.Equal(t, "INTAKE", row.Stage, "suggestions never migrate the project")
	_, err = q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).Update(q.Stage, "ACTIVE")
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		target, err = m.stageNotificationSuggestionOn(ctx, tx, 1, 99)
		return err
	}))
	require.Empty(t, target, "accepted work alone does not satisfy SENT delivery guard")
}

func TestAcceptedCandidatesUseRealAcceptanceAgeAndState(t *testing.T) {
	h := newResultFixture(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	q := query.BindWorkItems(h.db, h.m.service.names)
	check := func(want bool) {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			ids, err := h.m.acceptedUndeliveredOn(t.Context(), tx, 1, 99, at)
			require.NoError(t, err)
			require.Equal(t, want, slices.Contains(ids, h.work))
			return nil
		}))
	}
	check(false)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id = ?", h.work).Updates(map[string]any{"state": "ACCEPTED", "accepted_at": at.Add(-24 * time.Hour)}).Error)
	check(false)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(h.work)).Update(q.AcceptedAt, at.Add(-24*time.Hour-time.Millisecond))
	require.NoError(t, err)
	check(true)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(h.work)).Update(q.State, "REWORK")
	require.NoError(t, err)
	check(false)
}

func TestReportSuperiorRequiresExactlyOneActiveProjectAndCurrentUnexpiredLead(t *testing.T) {
	m, db, ctx, _ := stageHarness(t)
	require.NoError(t, db.Exec("CREATE TABLE d_iam_role_bindings (tenant_id BIGINT,role_id BIGINT,principal_id BIGINT,scope_type TEXT,scope_id BIGINT,effect TEXT,expires_at DATETIME)").Error)
	m.roles.RoleIDOn = func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 90, nil }
	m.projectAccess = core.PublishedAccessTableNames("d_")
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "ACTIVE").Error)
	require.NoError(t, db.Exec("INSERT INTO d_iam_role_bindings VALUES (1,80,11,'entity',99,'allow',NULL),(1,90,12,'entity',99,'allow',NULL)").Error)
	read := func() guid.ID {
		var id guid.ID
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; id, e = m.reportSuperiorOn(ctx, tx, 1, 11); return e }))
		return id
	}
	require.Equal(t, guid.ID(12), read())
	require.NoError(t, db.Exec("UPDATE d_iam_role_bindings SET expires_at=? WHERE principal_id=12", time.Now().UTC().Add(-time.Hour)).Error)
	require.Zero(t, read(), "expired role never remains an upper-level relationship")
	require.NoError(t, db.Exec("UPDATE d_iam_role_bindings SET expires_at=NULL WHERE principal_id=12").Error)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "CLOSED").Error)
	require.Zero(t, read(), "inactive project falls back to the configured team owner")
}

func TestAndonInboxCannotPublishWhenAnyCurrentStorageReadFails(t *testing.T) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	h.m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	before := h.stored()
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeAndonOn(h.actor(11), tx, 1, 99, h.work, 11, 11, andonBody{Version: before.Version, Reason: "Investigate actual source evidence"}, false)
		return e
	}))
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 12, ProjectID: 99, Kind: "ANDON", Limit: 1}
	failure := errors.New("current Andon inbox storage unavailable")
	calls, failAt := 0, 0
	db := h.m.runtime.Database().Write()
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("andon-inbox-storage", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	read := func() error {
		rows, e := h.m.andonInbox(h.actor(12), req, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil })
		if e == nil {
			require.Len(t, rows, 1)
			require.Equal(t, h.work, rows[0].ObjectID)
		} else {
			require.Empty(t, rows)
		}
		return e
	}
	require.NoError(t, read())
	total := calls
	require.Greater(t, total, 3)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, read(), failure, "current inbox source stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("andon-inbox-storage"))
}

func TestNotificationSourceBoundariesRejectUnscopedAndNontransactionalReads(t *testing.T) {
	h := newResultFixture(t)
	at := time.Now().UTC()
	for name, read := range map[string]func() error{
		"stage_projects":      func() error { _, e := h.m.stageNotificationProjectsOn(h.ctx, h.db, 1, -1, 10); return e },
		"stage_readiness":     func() error { _, _, e := h.m.stageNotificationReadinessOn(h.ctx, h.db, 1, 99); return e },
		"canceled_dependency": func() error { _, e := h.m.cancelledDependentsOn(h.ctx, h.db, 0, h.work); return e },
		"sheet":               func() error { _, _, e := h.m.reviewSheetNotificationOn(h.ctx, h.db, 0, 1); return e },
		"deadline":            func() error { _, _, e := h.m.deadlineNotificationOn(h.ctx, h.db, 0, h.work, "WORK"); return e },
		"attention":           func() error { _, _, e := h.m.executionAttentionWorkOn(h.ctx, h.db, 1, h.work); return e },
		"attention_scan":      func() error { _, e := h.m.attentionWorksOn(h.ctx, h.db, 1, 99); return e },
		"accepted_scan":       func() error { _, e := h.m.acceptedUndeliveredOn(h.ctx, h.db, 1, 99, at); return e },
		"superior":            func() error { _, e := h.m.reportSuperiorOn(h.ctx, h.db, 0, 11); return e },
		"pending_plan":        func() error { _, e := h.m.pendingHumanPlansOn(h.ctx, h.db, 1, 99); return e },
	} {
		t.Run(name, func(t *testing.T) { require.ErrorIs(t, read(), ErrInvalidArgument) })
	}
	for _, kind := range []string{"ANDON", "HUMAN_VERIFICATION", "PLAN_APPROVAL"} {
		read := func(req governance.InboxRequest) ([]governance.InboxItem, error) {
			switch kind {
			case "ANDON":
				return h.m.andonInbox(h.actor(12), req, nil)
			case "HUMAN_VERIFICATION":
				return h.m.verificationInboxWithLeads(h.actor(12), req, nil)
			default:
				return h.m.planApprovalInbox(h.actor(12), req)
			}
		}
		req := governance.InboxRequest{TenantID: 1, PrincipalID: 12, ProjectID: 99, Kind: kind, Limit: 10}
		for _, limit := range []int{0, 102} {
			req.Limit = limit
			items, e := read(req)
			require.Error(t, e)
			require.Empty(t, items)
		}
		req.Limit = 10
		req.TenantID = 2
		items, e := read(req)
		require.Error(t, e)
		require.Empty(t, items)
		req.TenantID = 1
		runtime := h.m.runtime
		h.m.runtime = nil
		items, e = read(req)
		require.ErrorIs(t, e, governance.ErrUnavailable)
		require.Empty(t, items)
		h.m.runtime = runtime
	}
}

func TestAndonInboxLeadFallbackRequiresCurrentPositiveLeadFact(t *testing.T) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeAndonOn(h.actor(11), tx, 1, 99, h.work, 11, 11, andonBody{Version: h.stored().Version, Reason: "Investigate actual source evidence"}, false)
		return e
	}))
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 12, ProjectID: 99, Kind: "ANDON", Limit: 10}
	items, e := h.m.andonInbox(h.actor(12), req, nil)
	require.NoError(t, e)
	require.Empty(t, items)
	failure := errors.New("current project lead unavailable")
	items, e = h.m.andonInbox(h.actor(12), req, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, failure })
	require.ErrorIs(t, e, failure)
	require.Empty(t, items)
	items, e = h.m.andonInbox(h.actor(12), req, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, nil })
	require.NoError(t, e)
	require.Empty(t, items)
}

func TestVerificationInboxStorageAndCurrentLeadFailuresCannotExposePendingWork(t *testing.T) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	claim, e := h.submit()
	require.NoError(t, e)
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 12, ProjectID: 99, Kind: "HUMAN_VERIFICATION", Limit: 10}
	failure := errors.New("current verification inbox unavailable")
	db := h.m.runtime.Database().Write()
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("verification-inbox-storage", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	read := func() error {
		items, e := h.m.verificationInboxWithLeads(h.actor(12), req, nil)
		if e == nil {
			require.Len(t, items, 1)
			require.Equal(t, claim.ID, items[0].ID)
		} else {
			require.Empty(t, items)
		}
		return e
	}
	require.NoError(t, read())
	total := calls
	require.Greater(t, total, 3)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, read(), failure, "verification inbox stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("verification-inbox-storage"))
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"escalation_reason": "INTERNAL_ROUNDS_EXHAUSTED", "require_human_decision": true}).Error)
	req.Kind = "ESCALATION_INTERNAL_EXHAUSTED"
	items, e := h.m.verificationInboxWithLeads(h.actor(12), req, nil)
	require.NoError(t, e)
	require.Empty(t, items)
	items, e = h.m.verificationInboxWithLeads(h.actor(12), req, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, failure })
	require.ErrorIs(t, e, failure)
	require.Empty(t, items)
	items, e = h.m.verificationInboxWithLeads(h.actor(12), req, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, nil })
	require.NoError(t, e)
	require.Empty(t, items)
}

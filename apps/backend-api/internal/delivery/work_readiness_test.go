package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Real InnoDB REPEATABLE READ exposes stale reads after waiting on a lock.
// Capability doubles use real locked rows; the business transaction is unmodified.
func TestReadinessCurrentFactsExternalDatabase(t *testing.T) {
	dialect, dsn := os.Getenv("IAM_LIFECYCLE_DATABASE_TYPE"), os.Getenv("IAM_LIFECYCLE_DATABASE_DSN")
	if dialect == "" && dsn == "" {
		t.Skip("external database supplied by database gate")
	}
	require.Contains(t, []string{"mysql", "postgres"}, dialect)
	require.NotEmpty(t, dsn)
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: dialect, DSN: dsn, Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(6)
	for _, scenario := range []string{"revoked_actor", "predecessor_accepted"} {
		t.Run(scenario, func(t *testing.T) {
			m, seed, ctx, detail, input := readinessHarness(t)
			source := m.MigrationSource()
			prefix := fmt.Sprintf("rd_%x_", time.Now().UnixNano())
			source.Prefix = prefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, dialect))
			original := m.service.names
			m.service.names = query.BindNames(prefix)
			var project model.Project
			require.NoError(t, seed.Table(original.Projects.String()).First(&project).Error)
			require.NoError(t, query.BindProjects(db, m.service.names).WithContext(ctx).Create(&project))
			work, err := query.BindWorkItems(seed, original).WithContext(ctx).First()
			require.NoError(t, err)
			require.NoError(t, query.BindWorkItems(db, m.service.names).WithContext(ctx).Create(work))
			criteria, err := query.BindWorkCriteria(seed, original).WithContext(ctx).Find()
			require.NoError(t, err)
			require.NoError(t, query.BindWorkCriteria(db, m.service.names).WithContext(ctx).Create(criteria...))
			contract, err := query.BindWorkContracts(seed, original).WithContext(ctx).First()
			require.NoError(t, err)
			require.NoError(t, query.BindWorkContracts(db, m.service.names).WithContext(ctx).Create(contract))
			probe := prefix + "principal_probe"
			require.NoError(t, db.Exec("CREATE TABLE "+probe+" (id BIGINT PRIMARY KEY, active BOOLEAN NOT NULL)").Error)
			require.NoError(t, db.Exec("INSERT INTO "+probe+" (id,active) VALUES (11,true)").Error)
			reached := make(chan struct{})
			type principalProbe struct {
				ID     guid.ID
				Active bool
			}
			m.principals.Lock = func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
				if scenario == "revoked_actor" {
					close(reached)
				}
				var row principalProbe
				return tx.WithContext(ctx).Table(probe).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&row).Error
			}
			m.principals.ActiveHumanByID = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				var row principalProbe
				err := tx.WithContext(ctx).Table(probe).Where("id=?", id).Take(&row).Error
				return identity.Principal{ID: id}, row.Active, err
			}
			blocker := db.Begin()
			require.NoError(t, blocker.Error)
			defer blocker.Rollback()
			if scenario == "revoked_actor" {
				require.NoError(t, blocker.Table(probe).Where("id=11").Update("active", false).Error)
			} else {
				predecessor := *work
				predecessor.ID, predecessor.Number, predecessor.State = work.ID+1, work.Number+1, "DRAFT"
				require.NoError(t, query.BindWorkItems(db, m.service.names).WithContext(ctx).Create(&predecessor))
				require.NoError(t, query.BindDependencies(db, m.service.names).WithContext(ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, DependsOnID: predecessor.ID}))
				p := query.BindProjects(blocker, m.service.names)
				_, err = p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First()
				require.NoError(t, err)
				w := query.BindWorkItems(blocker, m.service.names)
				_, err = w.WithContext(ctx).Where(w.ID.Eq(predecessor.ID)).Update(w.State, "ACCEPTED")
				require.NoError(t, err)
				m.workPolicy = func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _ string, _ guid.ID) (authz.DataConstraint, error) {
					// Establish the IAM snapshot before waiting for the project lock.
					var n int64
					err := tx.Table(probe).Count(&n).Error
					close(reached)
					return authz.DataConstraint{AllowAll: true}, err
				}
			}
			result := make(chan error, 1)
			go func() {
				owner, e := m.preparationOwner(ctx, db, 1, 99, detail.ID)
				if e == nil {
					e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
						receipt, e := m.prepareWorkOn(ctx, tx, 1, 99, detail.ID, 11, owner, 1, &input)
						if e == nil && receipt.State != "READY" {
							return fmt.Errorf("expected READY after committed predecessor acceptance, got %s", receipt.State)
						}
						return e
					})
				}
				result <- e
			}()
			select {
			case <-reached:
			case <-time.After(10 * time.Second):
				t.Fatal("command did not reach lock boundary")
			}
			require.NoError(t, blocker.Commit().Error)
			select {
			case err = <-result:
			case <-time.After(10 * time.Second):
				t.Fatal("command did not finish")
			}
			if scenario == "revoked_actor" {
				require.ErrorIs(t, err, ErrInvalidMember)
				var checks, transitions int64
				require.NoError(t, db.Table(m.service.names.ReadinessChecks.String()).Count(&checks).Error)
				require.NoError(t, db.Table(m.service.names.WorkTransitions.String()).Count(&transitions).Error)
				require.Zero(t, checks)
				require.Zero(t, transitions)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func readinessHarness(t *testing.T) (*Module, *gorm.DB, context.Context, workDetailView, WorkPreparationBody) {
	t.Helper()
	m, db, ctx, body := workHTTPHarness(t)
	created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	detail, err := m.readWork(ctx, db, 1, 99, created.Body.Data.ID, 11)
	require.NoError(t, err)
	estimate, minutes := 30, 60
	money := "9007199254740993"
	input := WorkPreparationBody{EstimateMinutes: &estimate, Risk: "MEDIUM", AutonomyLevel: "MEDIUM", OutOfScope: []string{}, OutOfScopeConfirmed: true, ContextRefs: []workContextRef{{Kind: "URL", Ref: "https://example.test/specification"}}, VerificationPlan: []workVerificationStep{{CriterionID: detail.Criteria[0].ID, Method: "HUMAN", Detail: "Exercise the actual feature against this criterion"}}, Budget: &workBudget{USDMicros: &money, WallClockMinutes: &minutes, MaxInternalRounds: 5}}
	return m, db, ctx, detail, input
}

func TestPreparationValidationRejectsInvalidDimensionsWithoutWrites(t *testing.T) {
	m, db, ctx, detail, valid := readinessHarness(t)
	intRef := func(n int) *int { return &n }
	strRef := func(s string) *string { return &s }
	cases := map[string]func(*WorkPreparationBody){
		"risk":             func(b *WorkPreparationBody) { b.Risk = "UNKNOWN" },
		"autonomy":         func(b *WorkPreparationBody) { b.AutonomyLevel = "UNKNOWN" },
		"estimate":         func(b *WorkPreparationBody) { b.EstimateMinutes = intRef(14) },
		"exclusions_count": func(b *WorkPreparationBody) { b.OutOfScope = make([]string, 21) },
		"empty_exclusion":  func(b *WorkPreparationBody) { b.OutOfScope = []string{"  "} },
		"long_exclusion":   func(b *WorkPreparationBody) { b.OutOfScope = []string{strings.Repeat("x", 2001)} },
		"contexts_count":   func(b *WorkPreparationBody) { b.ContextRefs = make([]workContextRef, 51) },
		"long_context": func(b *WorkPreparationBody) {
			b.ContextRefs = []workContextRef{{Kind: "URL", Ref: strings.Repeat("x", 2001)}}
		},
		"url_credentials": func(b *WorkPreparationBody) {
			b.ContextRefs = []workContextRef{{Kind: "URL", Ref: "https://user:password@example.test"}}
		},
		"url_scheme": func(b *WorkPreparationBody) {
			b.ContextRefs = []workContextRef{{Kind: "URL", Ref: "javascript:alert(1)"}}
		},
		"url_missing_host":    func(b *WorkPreparationBody) { b.ContextRefs = []workContextRef{{Kind: "URL", Ref: "https://"}} },
		"invalid_work_id":     func(b *WorkPreparationBody) { b.ContextRefs = []workContextRef{{Kind: "WORK_ITEM", Ref: "-1"}} },
		"unsupported_context": func(b *WorkPreparationBody) { b.ContextRefs = []workContextRef{{Kind: "FILE", Ref: "123"}} },
		"verification_count":  func(b *WorkPreparationBody) { b.VerificationPlan = make([]workVerificationStep, 51) },
		"duplicate_clause": func(b *WorkPreparationBody) {
			b.VerificationPlan = append(slices.Clone(b.VerificationPlan), b.VerificationPlan[0])
		},
		"invalid_clause":     func(b *WorkPreparationBody) { b.VerificationPlan[0].CriterionID = 0 },
		"invalid_method":     func(b *WorkPreparationBody) { b.VerificationPlan[0].Method = "UNKNOWN" },
		"empty_verification": func(b *WorkPreparationBody) { b.VerificationPlan[0].Detail = " " },
		"long_verification":  func(b *WorkPreparationBody) { b.VerificationPlan[0].Detail = strings.Repeat("x", 2001) },
		"rounds_negative":    func(b *WorkPreparationBody) { b.Budget.MaxInternalRounds = -1 },
		"rounds_overflow":    func(b *WorkPreparationBody) { b.Budget.MaxInternalRounds = 32768 },
		"time_zero":          func(b *WorkPreparationBody) { b.Budget.WallClockMinutes = intRef(0) },
		"negative_tokens":    func(b *WorkPreparationBody) { b.Budget.Tokens = strRef("-1") },
		"noncanonical_money": func(b *WorkPreparationBody) { b.Budget.USDMicros = strRef("01") },
		"money_overflow":     func(b *WorkPreparationBody) { b.Budget.USDMicros = strRef("9223372036854775808") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(valid)
			require.NoError(t, err)
			var body WorkPreparationBody
			require.NoError(t, json.Unmarshal(encoded, &body))
			mutate(&body)
			_, err = prepareTestWork(t, m, db, ctx, detail.ID, 1, &body)
			require.ErrorIs(t, err, ErrInvalidArgument)
			current, err := m.readWork(ctx, db, 1, 99, detail.ID, 11)
			require.NoError(t, err)
			require.Equal(t, 1, current.Version)
			require.Equal(t, detail.Contract, current.Contract)
			assertReadinessHistoryCount(t, db, 0)
		})
	}
	valid.OutOfScope = []string{"  Outside scope  "}
	valid.Budget.MaxInternalRounds = 0
	valid.ContextRefs = []workContextRef{{Kind: "WORK_ITEM", Ref: detail.ID.String()}, {Kind: "WORK_ITEM", Ref: detail.ID.String()}}
	receipt, err := prepareTestWork(t, m, db, ctx, detail.ID, 1, &valid)
	require.NoError(t, err)
	require.Equal(t, "READY", receipt.State)
	current, err := m.readWork(ctx, db, 1, 99, detail.ID, 11)
	require.NoError(t, err)
	require.Equal(t, []string{"Outside scope"}, current.Preparation.OutOfScope)
	require.Equal(t, 5, current.Preparation.Budget.MaxInternalRounds)
}

func TestPreparationDependencyFailuresAndChangedOwnerAreAtomic(t *testing.T) {
	for _, scenario := range []string{"principal_lock", "human_provider", "membership_provider", "scope", "missing_project", "project_storage", "work_storage", "changed_owner", "stored_budget", "stored_preparation", "criteria_storage", "contract_storage", "contract_json"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, detail, input := readinessHarness(t)
			injected := errors.New("dependency failure")
			switch scenario {
			case "principal_lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "human_provider":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, injected
				}
			case "membership_provider":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected })
			case "scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{ResourceIDs: []string{"100"}}, nil
				}
			case "missing_project":
				require.NoError(t, db.Exec("UPDATE d_delivery_projects SET stage='CLOSED',archived_at=CURRENT_TIMESTAMP").Error)
			case "project_storage":
				require.NoError(t, db.Exec("ALTER TABLE d_delivery_projects RENAME TO hidden_projects").Error)
			case "work_storage":
				m.principals.Lock = func(_ context.Context, tx *gorm.DB, _ guid.ID) error {
					return tx.Exec("ALTER TABLE d_delivery_work_items RENAME TO hidden_work").Error
				}
			case "changed_owner":
				m.principals.Lock = func(_ context.Context, tx *gorm.DB, _ guid.ID) error {
					return tx.Exec("UPDATE d_delivery_work_items SET accountable_principal_id=22").Error
				}
			case "stored_budget":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET budget='{\"tokens\":1}'").Error)
			case "stored_preparation":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET out_of_scope='[1]'").Error)
			case "criteria_storage":
				require.NoError(t, db.Exec("ALTER TABLE d_delivery_work_item_criteria RENAME TO hidden_criteria").Error)
			case "contract_storage":
				require.NoError(t, db.Exec("ALTER TABLE d_delivery_work_item_contracts RENAME TO hidden_contracts").Error)
			case "contract_json":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_item_contracts SET snapshot='{\"criteria\":true}'").Error)
			}
			var body *WorkPreparationBody = &input
			if scenario == "stored_budget" || scenario == "stored_preparation" {
				body = nil
			}
			_, err := prepareTestWork(t, m, db, ctx, detail.ID, 1, body)
			require.Error(t, err, scenario)
			assertReadinessHistoryCount(t, db, 0)
			var version int
			require.NoError(t, db.Table("d_delivery_work_items").Select("version").Scan(&version).Error)
			require.Equal(t, 1, version)
		})
	}
}

func TestReadinessLegacyMissingContractAndContainerStayUnexecutable(t *testing.T) {
	m, db, ctx, detail, input := readinessHarness(t)
	require.NoError(t, db.Exec("DELETE FROM d_delivery_work_item_contracts").Error)
	require.NoError(t, db.Exec("DELETE FROM d_delivery_work_item_criteria").Error)
	require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET objective=NULL,is_container=true").Error)
	receipt, err := prepareTestWork(t, m, db, ctx, detail.ID, 1, &WorkPreparationBody{Risk: input.Risk, AutonomyLevel: input.AutonomyLevel})
	require.NoError(t, err)
	require.False(t, receipt.Check.OK)
	require.Equal(t, "BLOCKED", receipt.State)
	require.ElementsMatch(t, []string{"ESTIMATE_MISSING", "CHILDREN_OPEN"}, receipt.BlockReasons)
	fields := []string{}
	for _, issue := range receipt.Check.Issues {
		fields = append(fields, issue.Field)
	}
	require.ElementsMatch(t, []string{"objective", "out_of_scope", "context_refs", "budget", "acceptance_criteria", "confirmed_contract"}, fields)
}

func TestReadinessUsesCurrentLinkedBaseline(t *testing.T) {
	m, db, ctx, requirement := requirementHarness(t)
	created, err := m.createRequirementHTTP(ctx, &requirementCreateInput{EntityID: "99", Body: requirement})
	require.NoError(t, err)
	baseline, err := m.baselineRequirementHTTP(ctx, &requirementBaselineInput{EntityID: "99", RequirementID: created.Body.Data.ID.String(), IfMatch: fmtETag(1)})
	require.NoError(t, err)
	body := workCreateBody{Title: "Linked work", Type: "TASK", Objective: "Deliver the confirmed scope", AccountablePrincipalID: 11, Criteria: []workCriterionInput{{Text: "Feature works", VerificationMethod: "Exercise the feature"}}, RequirementID: &created.Body.Data.ID, BaselineVersionNo: &baseline.Body.Data.Version.VersionNo}
	work, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	estimate := 30
	input := WorkPreparationBody{Risk: "LOW", AutonomyLevel: "LOW", EstimateMinutes: &estimate, OutOfScope: []string{}, OutOfScopeConfirmed: true, ContextRefs: []workContextRef{{Kind: "WORK_ITEM", Ref: work.Body.Data.ID.String()}}, Budget: &workBudget{MaxInternalRounds: 5}, VerificationPlan: []workVerificationStep{{CriterionID: work.Body.Data.ID + 1, Method: "TEST", Detail: "Run the acceptance test"}}}
	detail, err := m.readWork(ctx, db, 1, 99, work.Body.Data.ID, 11)
	require.NoError(t, err)
	input.VerificationPlan[0].CriterionID = detail.Criteria[0].ID
	ready, err := prepareTestWork(t, m, db, ctx, detail.ID, 1, &input)
	require.NoError(t, err)
	require.Equal(t, "READY", ready.State)
	require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET baseline_stale=true").Error)
	blocked, err := prepareTestWork(t, m, db, ctx, detail.ID, 2, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"REQUIREMENT_DRAFT"}, blocked.BlockReasons)
	require.Equal(t, "BLOCKED", blocked.State)
	// The linked baseline becoming unreadable must abort instead of treating it as absent.
	require.NoError(t, db.Exec("ALTER TABLE d_delivery_requirements RENAME TO hidden_requirements").Error)
	_, err = prepareTestWork(t, m, db, ctx, detail.ID, 3, nil)
	require.Error(t, err)
	assertReadinessHistoryCount(t, db, 2)
}

func TestPreparationRejectsMissingTransactionAndOwnerDiscoveryFailures(t *testing.T) {
	m, db, ctx, detail, input := readinessHarness(t)
	_, _, err := m.preparationWorkOn(ctx, db, 1, 99, detail.ID, 11, 11, "work_item_write")
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = m.preparationWorkOn(ctx, nil, 1, 99, detail.ID, 11, 11, "work_item_write")
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.preparationOwner(ctx, nil, 1, 99, detail.ID)
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err = m.preparationOwner(ctx, tx, 1, 99, detail.ID)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, _, err = m.preparationWorkOn(ctx, tx, 1, 99, detail.ID, 12, 11, "work_item_write")
		require.ErrorIs(t, err, ErrInvalidArgument)
		saved := m.service.events
		m.service.events = nil
		_, _, err = m.preparationWorkOn(ctx, tx, 1, 99, detail.ID, 11, 11, "work_item_write")
		require.ErrorIs(t, err, ErrUnavailable)
		m.service.events = saved
		return nil
	}))
	_, err = m.preparationOwner(ctx, db, 1, 99, detail.ID+100)
	require.ErrorIs(t, err, ErrProjectNotFound)
	_, err = m.prepareWorkHTTP(ctx, &workPrepareInput{EntityID: "bad", WorkItemID: detail.ID.String(), IfMatch: fmtETag(1), Body: input})
	assertWorkStatus(t, err, 422)
	_, err = m.checkWorkReadinessHTTP(ctx, &workCheckInput{EntityID: "99", WorkItemID: detail.ID.String(), IfMatch: "unquoted"})
	assertWorkStatus(t, err, 422)
	m.runtime = nil
	_, err = m.prepareWorkHTTP(ctx, &workPrepareInput{EntityID: "99", WorkItemID: detail.ID.String(), IfMatch: fmtETag(1), Body: input})
	assertWorkStatus(t, err, 503)
	_, err = m.checkWorkReadinessHTTP(ctx, &workCheckInput{EntityID: "99", WorkItemID: detail.ID.String(), IfMatch: fmtETag(1)})
	assertWorkStatus(t, err, 503)
	assertReadinessHistoryCount(t, db, 0)
}
func prepareTestWork(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, id guid.ID, version int, input *WorkPreparationBody) (workPreparationReceipt, error) {
	t.Helper()
	var result workPreparationReceipt
	owner, err := m.preparationOwner(ctx, db, 1, 99, id)
	if err != nil {
		return result, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		var e error
		result, e = m.prepareWorkOn(ctx, tx, 1, 99, id, 11, owner, version, input)
		return e
	})
	return result, err
}
func assertReadinessHistoryCount(t *testing.T, db *gorm.DB, count int64) {
	t.Helper()
	for _, table := range []string{"d_delivery_readiness_checks", "d_delivery_work_item_transitions"} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.Equal(t, count, n, table)
	}
}
func TestWorkPreparationReadinessIsVersionedAndDoesNotChangeContract(t *testing.T) {
	m, db, ctx, original, input := readinessHarness(t)
	first, err := m.prepareWorkHTTP(ctx, &workPrepareInput{EntityID: "99", WorkItemID: original.ID.String(), IfMatch: fmtETag(1), IdempotencyKey: "prepare-once", Body: input})
	require.NoError(t, err)
	require.Equal(t, "READY", first.Body.Data.State)
	require.True(t, first.Body.Data.Check.OK)
	require.Empty(t, first.Body.Data.Check.Issues)
	require.Equal(t, 2, first.Body.Data.Version)
	assertReadinessHistoryCount(t, db, 1)
	replay, err := m.prepareWorkHTTP(ctx, &workPrepareInput{EntityID: "99", WorkItemID: original.ID.String(), IfMatch: fmtETag(1), IdempotencyKey: "prepare-once", Body: input})
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	current, err := m.getWorkHTTP(ctx, &workGetInput{EntityID: "99", WorkItemID: original.ID.String()})
	require.NoError(t, err)
	require.Equal(t, fmtETag(2), current.ETag)
	require.Equal(t, original.Contract, current.Body.Data.Contract)
	require.Nil(t, current.Body.Data.RequirementID)
	require.Equal(t, "9007199254740993", *current.Body.Data.Preparation.Budget.USDMicros)
	require.True(t, current.Body.Data.Preparation.OutOfScopeConfirmed)
	require.Equal(t, first.Body.Data.Check.ID, current.Body.Data.Preparation.LastCheck.ID)
	var requirementCount int64
	require.NoError(t, db.Table("d_delivery_requirements").Count(&requirementCount).Error)
	require.Zero(t, requirementCount)
	manual := &workCheckInput{EntityID: "99", WorkItemID: original.ID.String(), IfMatch: fmtETag(2), IdempotencyKey: "manual-once"}
	check, err := m.checkWorkReadinessHTTP(ctx, manual)
	require.NoError(t, err)
	require.Equal(t, 201, check.Status)
	require.Equal(t, "MANUAL", check.Body.Data.Check.Trigger)
	require.True(t, check.Body.Data.Check.OK)
	again, err := m.checkWorkReadinessHTTP(ctx, manual)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, check.Body.Data, again.Body.Data)
	assertReadinessHistoryCount(t, db, 2)
	input.Budget = nil
	incomplete, err := prepareTestWork(t, m, db, ctx, original.ID, 3, &input)
	require.NoError(t, err)
	require.Equal(t, "DRAFT", incomplete.State)
	require.False(t, incomplete.Check.OK)
	require.Contains(t, incomplete.Check.Issues, workReadinessIssue{Code: "MISSING_FIELD", Field: "budget", MessageKey: "delivery.readiness_missing_field"})
	latest, err := m.readWork(ctx, db, 1, 99, original.ID, 11)
	require.NoError(t, err)
	require.Nil(t, latest.Preparation.Budget)
	require.Equal(t, original.Contract, latest.Contract)
	checks := query.BindReadinessChecks(db, m.service.names)
	stored, err := checks.WithContext(ctx).Where(checks.ID.Eq(first.Body.Data.Check.ID)).First()
	require.NoError(t, err)
	require.True(t, stored.OK)
	require.Equal(t, "[]", stored.Issues)
	require.Equal(t, 2, stored.WorkItemVersion)
	_, err = prepareTestWork(t, m, db, ctx, original.ID, 2, &input)
	require.ErrorIs(t, err, ErrWorkVersion)
	assertReadinessHistoryCount(t, db, 3)
}

func TestReadinessSeparatesPreparationIssuesFromSystemBlockers(t *testing.T) {
	for _, scenario := range []string{"estimate", "scope_confirmation", "context", "method", "high_risk", "manual_block", "paused", "owner"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, original, input := readinessHarness(t)
			wantState, wantOK := "DRAFT", false
			switch scenario {
			case "estimate":
				input.EstimateMinutes = nil
				wantState, wantOK = "BLOCKED", true
			case "scope_confirmation":
				input.OutOfScopeConfirmed = false
			case "context":
				input.ContextRefs = nil
			case "method":
				input.VerificationPlan = nil
			case "high_risk":
				input.Risk = "HIGH"
				input.VerificationPlan[0].Method = "AGENT_JUDGE"
			case "manual_block":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET block_reasons=? WHERE id=?", `["EXTERNAL"]`, original.ID).Error)
				wantState, wantOK = "BLOCKED", true
			case "paused":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET paused_at=CURRENT_TIMESTAMP,pause_reason='MANUAL' WHERE id=?", original.ID).Error)
				wantState, wantOK = "DRAFT", true
			case "owner":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET accountable_principal_id=22 WHERE id=?", original.ID).Error)
				// Reconfirm the fixture with the actual other Human before deactivating them.
				var contract model.WorkItemContract
				require.NoError(t, db.Table("d_delivery_work_item_contracts").Where("work_item_id=?", original.ID).First(&contract).Error)
				snapshot := original.Contract.Snapshot
				snapshot.AccountablePrincipalID = 22
				encoded, hash := testContractEncoding(t, snapshot)
				require.NoError(t, db.Table("d_delivery_work_item_contracts").Where("id=?", contract.ID).Updates(map[string]any{"snapshot": encoded, "content_hash": hash}).Error)
				m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{ID: id}, id != 22, nil
				}
				wantState, wantOK = "BLOCKED", false
			}
			result, err := prepareTestWork(t, m, db, ctx, original.ID, 1, &input)
			require.NoError(t, err)
			require.Equal(t, wantState, result.State)
			require.Equal(t, wantOK, result.Check.OK)
			if scenario == "estimate" {
				require.Equal(t, []string{"ESTIMATE_MISSING"}, result.BlockReasons)
			}
			if scenario == "manual_block" {
				require.Equal(t, []string{"EXTERNAL"}, result.BlockReasons)
			}
			if scenario == "high_risk" {
				input.VerificationPlan[0].Method = "TEST"
				fixed, err := prepareTestWork(t, m, db, ctx, original.ID, 2, &input)
				require.NoError(t, err)
				require.True(t, fixed.Check.OK)
				require.Equal(t, "READY", fixed.State)
			}
		})
	}
}

func TestPreparationAtomicFailuresAndSameKeyRetry(t *testing.T) {
	for _, stage := range []string{"update", "check", "transition", "audit", "ready_event", "state_event"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, original, input := readinessHarness(t)
			var before model.WorkItem
			require.NoError(t, db.Table("d_delivery_work_items").Where("id=?", original.ID).First(&before).Error)
			errInjected := errors.New("readiness write failed")
			var restore func()
			switch stage {
			case "update", "check", "transition":
				table, operation := "d_delivery_work_items", "UPDATE"
				if stage == "check" {
					table, operation = "d_delivery_readiness_checks", "INSERT"
				}
				if stage == "transition" {
					table, operation = "d_delivery_work_item_transitions", "INSERT"
				}
				require.NoError(t, db.Exec("CREATE TRIGGER fail_preparation BEFORE "+operation+" ON "+table+" BEGIN SELECT RAISE(ABORT,'injected readiness failure'); END").Error)
				restore = func() { require.NoError(t, db.Exec("DROP TRIGGER fail_preparation").Error) }
			case "audit":
				previous := m.service.audit
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errInjected }
				restore = func() { m.service.audit = previous }
			default:
				previous := m.service.events
				m.service.events = projectPublisher(func(_ context.Context, _ *gorm.DB, event events.Event) (guid.ID, error) {
					if (stage == "ready_event" && event.Type == "work_item.ready_checked") || (stage == "state_event" && event.Type == "work_item.state_changed") {
						return 0, errInjected
					}
					return 1, nil
				})
				restore = func() { m.service.events = previous }
			}
			in := &workPrepareInput{EntityID: "99", WorkItemID: original.ID.String(), IfMatch: fmtETag(1), IdempotencyKey: "atomic-preparation", Body: input}
			_, err := m.prepareWorkHTTP(ctx, in)
			require.Error(t, err)
			var after model.WorkItem
			require.NoError(t, db.Table("d_delivery_work_items").Where("id=?", original.ID).First(&after).Error)
			require.Equal(t, before, after)
			assertReadinessHistoryCount(t, db, 0)
			restore()
			success, err := m.prepareWorkHTTP(ctx, in)
			require.NoError(t, err)
			require.Equal(t, "READY", success.Body.Data.State)
			assertReadinessHistoryCount(t, db, 1)
		})
	}
}

func TestPreparationRejectsForeignReferencesAndUnauthorizedOrStaleWrites(t *testing.T) {
	for _, scenario := range []string{"criterion", "context", "scope", "policy", "inactive", "stale", "terminal", "claimed", "contract_hash", "criterion_mutation", "missing_dependency_table"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, original, input := readinessHarness(t)
			version := 1
			switch scenario {
			case "criterion":
				input.VerificationPlan[0].CriterionID++
			case "context":
				input.ContextRefs = []workContextRef{{Kind: "WORK_ITEM", Ref: "999999"}}
			case "scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"22"}}, nil
				}
			case "policy":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
			case "inactive":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
			case "stale":
				version = 2
			case "terminal":
				require.NoError(t, db.Exec("UPDATE d_delivery_projects SET stage='CLOSED'").Error)
			case "claimed":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET state='CLAIMED' WHERE id=?", original.ID).Error)
			case "contract_hash":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_item_contracts SET content_hash=?", strings.Repeat("0", 64)).Error)
			case "criterion_mutation":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_item_criteria SET text='Not the confirmed criterion'").Error)
			case "missing_dependency_table":
				require.NoError(t, db.Exec("ALTER TABLE d_delivery_work_item_dependencies RENAME TO hidden_dependencies").Error)
			}
			var before model.WorkItem
			require.NoError(t, db.Table("d_delivery_work_items").Where("id=?", original.ID).First(&before).Error)
			_, err := prepareTestWork(t, m, db, ctx, original.ID, version, &input)
			require.Error(t, err)
			var after model.WorkItem
			require.NoError(t, db.Table("d_delivery_work_items").Where("id=?", original.ID).First(&after).Error)
			require.Equal(t, before, after)
			assertReadinessHistoryCount(t, db, 0)
		})
	}
}

func TestReadinessRequiresRealDependencyAcceptanceAndPreservesQualityFindings(t *testing.T) {
	m, db, ctx, original, input := readinessHarness(t)
	body := workCreateBody{Title: "Predecessor", Type: "TASK", Objective: "Provide the required input", AccountablePrincipalID: 11, Criteria: []workCriterionInput{{Text: "Required input exists", VerificationMethod: "Inspect actual input"}}}
	other, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: body})
	require.NoError(t, err)
	require.NoError(t, query.BindDependencies(db, m.service.names).WithContext(ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: original.ID, DependsOnID: other.Body.Data.ID}))
	blocked, err := prepareTestWork(t, m, db, ctx, original.ID, 1, &input)
	require.NoError(t, err)
	require.True(t, blocked.Check.OK)
	require.Equal(t, "BLOCKED", blocked.State)
	require.Equal(t, []string{"DEPENDENCY"}, blocked.BlockReasons)
	require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET state='ACCEPTED' WHERE id=?", other.Body.Data.ID).Error)
	ready, err := prepareTestWork(t, m, db, ctx, original.ID, 2, nil)
	require.NoError(t, err)
	require.Equal(t, "READY", ready.State)
	issue := `[{"code":"REQUIREMENT_AMBIGUOUS","message_key":"quality.actual_ambiguity"}]`
	require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET readiness_issues=? WHERE id=?", issue, original.ID).Error)
	draft, err := prepareTestWork(t, m, db, ctx, original.ID, 3, nil)
	require.NoError(t, err)
	require.False(t, draft.Check.OK)
	require.Equal(t, "DRAFT", draft.State)
	require.Contains(t, draft.Check.Issues, workReadinessIssue{Code: "REQUIREMENT_AMBIGUOUS", MessageKey: "quality.actual_ambiguity"})
}

func testContractEncoding(t *testing.T, snapshot workContractSnapshot) (string, string) {
	t.Helper()
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	sum := sha256.Sum256(encoded)
	return string(encoded), hex.EncodeToString(sum[:])
}

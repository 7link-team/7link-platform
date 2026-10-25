package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkRecordSerializesUIGateFactsIncludingClearedValues(t *testing.T) {
	pause, stage, execution := "AUTONOMY_REWORK_LIMIT", "VERIFIED", guid.ID(9007199254740993)
	for _, scenario := range []struct {
		name string
		row  model.WorkItem
		want map[string]string
	}{
		{name: "set", row: model.WorkItem{BaselineStale: true, PauseReason: &pause, CurrentExecutionID: &execution, DefectStage: &stage}, want: map[string]string{
			"baseline_stale": "true", "pause_reason": `"AUTONOMY_REWORK_LIMIT"`, "current_execution_id": `"9007199254740993"`, "defect_stage": `"VERIFIED"`,
		}},
		{name: "cleared", row: model.WorkItem{}, want: map[string]string{
			"baseline_stale": "false", "pause_reason": "null", "current_execution_id": "null", "defect_stage": "null",
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			encoded, err := json.Marshal(workRecord(&scenario.row))
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &fields))
			for field, want := range scenario.want {
				require.Contains(t, fields, field, "cleared facts must remain explicit in the API")
				require.JSONEq(t, want, string(fields[field]), field)
			}
			var decoded WorkView
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			require.Equal(t, scenario.row.BaselineStale, decoded.BaselineStale)
			require.Equal(t, scenario.row.PauseReason, decoded.PauseReason)
			require.Equal(t, scenario.row.CurrentExecutionID, decoded.CurrentExecutionID)
			require.Equal(t, scenario.row.DefectStage, decoded.DefectStage)
		})
	}
}

func TestWorkLinkedBaselineMustExistAndIsCaptured(t *testing.T) {
	m, db, ctx, input := workHarness(t)
	require.NoError(t, db.Exec("INSERT INTO d_delivery_requirements (tenant_id,id,project_id,number,title,status,current_version_no,created_at,updated_at) VALUES (1,8,99,1,'Requirement','BASELINED',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)").Error)
	id, version := guid.ID(8), 1
	input.RequirementID, input.BaselineVersionNo = &id, &version
	err := db.Transaction(func(tx *gorm.DB) error { _, e := m.createWorkOn(ctx, tx, 1, 99, 11, input); return e })
	require.ErrorIs(t, err, ErrInvalidArgument, "a status flag alone is not a real baseline")
	require.NoError(t, db.Exec("INSERT INTO d_delivery_requirement_versions (tenant_id,id,project_id,requirement_id,version_no,title,description,priority,criteria,content_hash,created_at) VALUES (1,9,99,8,1,'Requirement','',0,'[]',?,CURRENT_TIMESTAMP)", strings.Repeat("a", 64)).Error)
	var created workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; created, e = m.createWorkOn(ctx, tx, 1, 99, 11, input); return e }))
	require.Equal(t, &id, created.RequirementID)
	require.Equal(t, 1, created.BaselineVersionNo)
	require.Equal(t, &id, created.Contract.Snapshot.RequirementID)
	require.Equal(t, 1, created.Contract.Snapshot.BaselineVersionNo)
	require.NoError(t, db.Exec("UPDATE d_delivery_requirements SET title='Later title' WHERE id=8").Error)
	read, err := m.readWork(ctx, db, 1, 99, created.ID, 11)
	require.NoError(t, err)
	require.Equal(t, created.Contract, read.Contract)
}

func TestWorkDependencyFailuresNeverWritePartialAggregate(t *testing.T) {
	for _, stage := range []string{"lock", "policy", "human", "member", "project_missing", "project_read", "counter_read", "counter_create", "work_create", "bad_owner_scope", "missing_policy"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, input := workHarness(t)
			injected := errors.New("dependency failed")
			switch stage {
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return injected }
			case "policy":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, injected
				}
			case "member":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected })
			case "project_missing":
				require.NoError(t, db.Exec("DELETE FROM d_delivery_projects").Error)
			case "project_read":
				require.NoError(t, db.Exec("ALTER TABLE d_delivery_projects RENAME TO hidden_projects").Error)
			case "counter_read":
				require.NoError(t, db.Exec("ALTER TABLE d_delivery_project_counters RENAME TO hidden_counters").Error)
			case "counter_create":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_counter BEFORE INSERT ON d_delivery_project_counters BEGIN SELECT RAISE(ABORT,'counter failed'); END").Error)
			case "work_create":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_work BEFORE INSERT ON d_delivery_work_items BEGIN SELECT RAISE(ABORT,'work failed'); END").Error)
			case "bad_owner_scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
			case "missing_policy":
				m.workPolicy = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.createWorkOn(ctx, tx, 1, 99, 11, input); return e })
			require.Error(t, err)
			for _, table := range []string{"d_delivery_work_items", "d_delivery_work_item_criteria", "d_delivery_work_item_contracts"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Zero(t, count, table)
			}
		})
	}
}

func TestWorkCreationRequiresTransactionAndMatchingActor(t *testing.T) {
	m, db, ctx, input := workHarness(t)
	_, err := m.createWorkOn(ctx, db, 1, 99, 11, input)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.createWorkOn(ctx, nil, 1, 99, 11, input)
	require.ErrorIs(t, err, ErrInvalidArgument)
	err = db.Transaction(func(tx *gorm.DB) error { _, e := m.createWorkOn(ctx, tx, 1, 99, 12, input); return e })
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func workHarness(t *testing.T) (*Module, *gorm.DB, context.Context, workCreateBody) {
	t.Helper()
	s, db, draft := projectHarness(t)
	draft.project.ProjectID = 99
	require.NoError(t, query.BindProjects(db, s.names).WithContext(t.Context()).Create(&draft.project))
	ids, err := guid.New(1)
	require.NoError(t, err)
	m := &Module{service: s, ids: ids, workPolicy: func(_ context.Context, _ *gorm.DB, tenant, entity guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), entity)
		require.Equal(t, guid.ID(11), actor)
		require.Contains(t, []string{"work_item_write", "work_item_read"}, permission)
		return authz.DataConstraint{AllowAll: true, Revision: 7}, nil
	}, members: memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }), principals: identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }, ActiveHumanByID: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}}}
	s.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }
	s.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 1, nil })
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	return m, db, ctx, workCreateBody{Title: " First task ", Type: "TASK", Objective: " Deliver a working feature ", AccountablePrincipalID: 11, Criteria: []workCriterionInput{{Text: " Acceptance clause ", VerificationMethod: " Run the actual feature "}}}
}

func TestWorkCreatesIndependentContractAndPreservesLegacyNumbers(t *testing.T) {
	m, db, ctx, input := workHarness(t)
	require.NoError(t, db.Exec("INSERT INTO d_delivery_requirements (tenant_id,id,project_id,number,title,created_at,updated_at) VALUES (1,8,99,9,'Legacy requirement',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)").Error)
	require.NoError(t, db.Exec("INSERT INTO d_delivery_work_items (tenant_id,id,project_id,requirement_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES (1,7,99,8,12,'Legacy task','TASK',0,11,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)").Error)
	var created workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = m.createWorkOn(ctx, tx, 1, 99, 11, input)
		return err
	}))
	require.Equal(t, 13, created.Number)
	require.Nil(t, created.RequirementID)
	require.Equal(t, 0, created.BaselineVersionNo)
	require.Equal(t, "First task", created.Title)
	require.Equal(t, "DRAFT", created.State)
	require.Equal(t, 1, created.Contract.VersionNo)
	require.Len(t, created.Criteria, 1)
	require.Positive(t, created.Criteria[0].ID)
	encoded, err := json.Marshal(created.Contract.Snapshot)
	require.NoError(t, err)
	hash := sha256.Sum256(encoded)
	require.Equal(t, hex.EncodeToString(hash[:]), created.Contract.ContentHash)
	read, err := m.readWork(ctx, db, 1, 99, created.ID, 11)
	require.NoError(t, err)
	require.Equal(t, created, read)
	legacy, err := m.readWork(ctx, db, 1, 99, 7, 11)
	require.NoError(t, err)
	require.Nil(t, legacy.Contract)
	require.Empty(t, legacy.Criteria)
	c := query.BindCounters(db, m.service.names)
	counter, err := c.WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, 10, counter.NextRequirementNo)
	require.Equal(t, 14, counter.NextWorkItemNo)
}

func TestWorkCreationRollsBackAggregateAndCounter(t *testing.T) {
	for _, stage := range []string{"criteria", "contract", "audit", "event"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, input := workHarness(t)
			injected := errors.New("injected failure")
			switch stage {
			case "criteria":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_criteria BEFORE INSERT ON d_delivery_work_item_criteria BEGIN SELECT RAISE(ABORT,'injected failure'); END").Error)
			case "contract":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_contract BEFORE INSERT ON d_delivery_work_item_contracts BEGIN SELECT RAISE(ABORT,'injected failure'); END").Error)
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return injected }
			case "event":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, injected })
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.createWorkOn(ctx, tx, 1, 99, 11, input); return e })
			require.Error(t, err)
			for _, table := range []string{"d_delivery_work_items", "d_delivery_work_item_criteria", "d_delivery_work_item_contracts", "d_delivery_project_counters"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Zero(t, count, table)
			}
		})
	}
}

func TestWorkCreationStorageFailuresLeaveNoAggregateOrReservedNumber(t *testing.T) {
	m, db, ctx, input := workHarness(t)
	injected := errors.New("work creation unavailable")
	abort := errors.New("rollback probe")
	reads, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("work-creation-read-failure", func(tx *gorm.DB) {
		reads++
		if reads == failAt {
			tx.AddError(injected)
		}
	}))
	defer db.Callback().Query().Remove("work-creation-read-failure")
	create := func(probe bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.createWorkOn(ctx, tx, 1, 99, 11, input)
			if e == nil && probe {
				return abort
			}
			return e
		})
	}
	require.ErrorIs(t, create(true), abort)
	total := reads
	require.Equal(t, 3, total)
	for i := 1; i <= total; i++ {
		reads, failAt = 0, i
		require.Error(t, create(false), "creation read %d", i)
	}
	failAt = 0
	original := m.ids
	calls, idFailure := 0, 0
	m.ids = draftIDSource(func() (int64, error) {
		calls++
		if calls == idFailure {
			return 0, injected
		}
		return original.Next()
	})
	require.ErrorIs(t, create(true), abort)
	total = calls
	for i := 1; i <= total; i++ {
		calls, idFailure = 0, i
		require.ErrorIs(t, create(false), injected, "creation id %d", i)
	}
	for _, table := range []string{m.service.names.WorkItems.String(), m.service.names.WorkCriteria.String(), m.service.names.WorkContracts.String(), m.service.names.Counters.String()} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestWorkRejectsIncompleteOrUnauthorizedConfirmation(t *testing.T) {
	for _, scenario := range []string{"goal", "criteria", "verification", "owner", "human", "member", "denied", "department_only", "foreign_owner_scope", "requirement_pair", "requirement_missing", "stale_baseline"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, input := workHarness(t)
			switch scenario {
			case "goal":
				input.Objective = " "
			case "criteria":
				input.Criteria = nil
			case "verification":
				input.Criteria[0].VerificationMethod = " "
			case "owner":
				input.AccountablePrincipalID = 0
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "member":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
			case "denied", "department_only", "foreign_owner_scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					if scenario == "denied" {
						return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
					}
					if scenario == "department_only" {
						return authz.DataConstraint{DepartmentIDs: []string{"3"}}, nil
					}
					return authz.DataConstraint{OwnerIDs: []string{"22"}}, nil
				}
			case "requirement_pair":
				id := guid.ID(8)
				input.RequirementID = &id
			case "requirement_missing", "stale_baseline":
				id := guid.ID(8)
				version := 1
				input.RequirementID = &id
				input.BaselineVersionNo = &version
				if scenario == "stale_baseline" {
					require.NoError(t, db.Exec("INSERT INTO d_delivery_requirements (tenant_id,id,project_id,number,title,status,current_version_no,created_at,updated_at) VALUES (1,8,99,1,'R','BASELINED',2,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)").Error)
				}
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createWorkOn(ctx, tx, 1, 99, 11, input); return e }))
			w := query.BindWorkItems(db, m.service.names)
			count, err := w.WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestWorkOwnerScopeAndExplicitDeny(t *testing.T) {
	m, db, ctx, input := workHarness(t)
	var created workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; created, e = m.createWorkOn(ctx, tx, 1, 99, 11, input); return e }))
	for _, c := range []authz.DataConstraint{{OwnerIDs: []string{"22"}}, {AllowAll: true, DeniedIDs: []string{"99"}}, {ResourceIDs: []string{created.ID.String()}}, {DepartmentIDs: []string{"1"}}} {
		m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return c, nil
		}
		_, err := m.readWork(ctx, db, 1, 99, created.ID, 11)
		require.ErrorIs(t, err, ErrProjectNotFound)
	}
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
	}
	_, err := m.readWork(ctx, db, 1, 99, created.ID, 11)
	require.NoError(t, err)
}

func TestDefectCreationRequiresDimensionsAndFreezesThem(t *testing.T) {
	for _, kind := range []string{"BUG", "INCIDENT"} {
		t.Run(kind, func(t *testing.T) {
			m, db, ctx, input := workHarness(t)
			input.Type = kind
			create := func() (workDetailView, error) {
				var v workDetailView
				err := db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.createWorkOn(ctx, tx, 1, 99, 11, input); return e })
				return v, err
			}
			_, err := create()
			require.ErrorIs(t, err, ErrInvalidArgument)
			severity, priority, repro, version := "CRITICAL", "HIGH", "ALWAYS", "build-17"
			input.Severity = &severity
			input.Priority = &priority
			input.Reproducibility = &repro
			input.FoundVersion = &version
			v, err := create()
			require.NoError(t, err)
			require.Equal(t, &severity, v.Severity)
			require.Equal(t, &priority, v.Priority)
			require.Equal(t, &repro, v.Reproducibility)
			require.Equal(t, &version, v.Contract.Snapshot.FoundVersion)
			severity = "UNKNOWN"
			_, err = create()
			require.ErrorIs(t, err, ErrInvalidArgument)
			severity = "MINOR"
			input.Type = "TASK"
			_, err = create()
			require.ErrorIs(t, err, ErrInvalidArgument)
		})
	}
}

func TestDraftContractConfirmationRequiresCurrentBaselineAndAtomicPersistence(t *testing.T) {
	for _, mode := range []string{"success", "existing", "contract_read", "criteria_read", "contract_write", "audit", "id", "invalid_objective", "baseline_read", "baseline_stale", "baseline_success"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, input := workHarness(t)
			var created workDetailView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; created, e = m.createWorkOn(ctx, tx, 1, 99, 11, input); return e }))
			if mode != "existing" {
				require.NoError(t, db.Table(m.service.names.WorkContracts.String()).Where("work_item_id=?", created.ID).Delete(&model.WorkItemContract{}).Error)
			}
			q := query.BindWorkItems(db, m.service.names)
			work, e := q.WithContext(ctx).Where(q.ID.Eq(created.ID)).First()
			require.NoError(t, e)
			failure := errors.New("contract source unavailable")
			if strings.HasPrefix(mode, "baseline_") {
				id := guid.ID(8)
				work.RequirementID = &id
				work.BaselineVersionNo = 1
				require.NoError(t, db.Exec("INSERT INTO d_delivery_requirements (tenant_id,id,project_id,number,title,status,current_version_no,created_at,updated_at) VALUES (1,8,99,1,'Requirement','BASELINED',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)").Error)
				if mode == "baseline_stale" {
					work.BaselineStale = true
				}
			}
			if mode == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			if mode == "id" {
				m.ids = unavailableActivityIDs{err: failure}
			}
			if mode == "invalid_objective" {
				empty := ""
				work.Objective = &empty
			}
			target := ""
			switch mode {
			case "contract_read":
				target = m.service.names.WorkContracts.String()
			case "criteria_read":
				target = m.service.names.WorkCriteria.String()
			case "baseline_read":
				target = m.service.names.Requirements.String()
			}
			hit := false
			if target != "" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("contract-source-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						hit = true
						tx.AddError(failure)
					}
				}))
			}
			if mode == "contract_write" {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("contract-write-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.WorkContracts.String() {
						hit = true
						tx.AddError(failure)
					}
				}))
			}
			e = db.Transaction(func(tx *gorm.DB) error { return m.confirmDraftWorkContractOn(ctx, tx, work, 11) })
			if target != "" {
				require.NoError(t, db.Callback().Query().Remove("contract-source-failure"))
				require.True(t, hit)
			}
			if mode == "contract_write" {
				require.NoError(t, db.Callback().Create().Remove("contract-write-failure"))
				require.True(t, hit)
			}
			if mode == "success" || mode == "baseline_success" {
				require.NoError(t, e)
				r, e := query.BindWorkContracts(db, m.service.names).WithContext(ctx).First()
				require.NoError(t, e)
				require.Equal(t, created.ID, r.WorkItemID)
				require.Equal(t, guid.ID(11), r.ConfirmedBy)
				require.Len(t, r.ContentHash, 64)
			} else {
				require.Error(t, e)
				if target != "" || mode == "contract_write" || mode == "audit" || mode == "id" {
					require.ErrorIs(t, e, failure)
				}
				var count int64
				require.NoError(t, db.Table(m.service.names.WorkContracts.String()).Where("work_item_id=?", created.ID).Count(&count).Error)
				if mode == "existing" {
					require.EqualValues(t, 1, count)
				} else {
					require.Zero(t, count)
				}
			}
		})
	}
}

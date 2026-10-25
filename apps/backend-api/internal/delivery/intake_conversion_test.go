package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestIntakeRequirementUsesCanonicalDraftAndSourceAssociation(t *testing.T) {
	m, db, ctx, _ := requirementHarness(t)
	p := guid.ID(99)
	m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
		return intake.IntakeRef{ID: 50, Kind: "NEW", Status: "TRIAGED", ProjectID: &p}, true, nil
	}
	draft := intake.TriageDraft{Summary: "Export", RequirementDraft: "Export selected rows", Criteria: []string{"CSV includes every selected row"}}
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = m.createIntakeRequirementOn(ctx, tx, 1, 99, 50, draft)
		return err
	}))
	var row model.Requirement
	require.NoError(t, db.Table(m.service.names.Requirements.String()).Where("id=?", id).Take(&row).Error)
	require.Equal(t, "DRAFT", row.Status)
	require.Equal(t, "NEEDS_WORK", row.ReviewState)
	require.Zero(t, row.CurrentVersionNo)
	require.Equal(t, guid.ID(50), *row.SourceIntakeID)
	for _, scenario := range []string{"missing", "wrong_kind", "wrong_state", "foreign_project", "source_error", "invalid_criteria", "current_authority"} {
		t.Run(scenario, func(t *testing.T) {
			fact := intake.IntakeRef{ID: 50, Kind: "NEW", Status: "TRIAGED", ProjectID: &p}
			found := true
			var sourceErr error
			input := draft
			switch scenario {
			case "missing":
				found = false
			case "wrong_kind":
				fact.Kind = "CHANGE"
			case "wrong_state":
				fact.Status = "RECEIVED"
			case "foreign_project":
				other := guid.ID(100)
				fact.ProjectID = &other
			case "source_error":
				sourceErr = errors.New("source unavailable")
			case "invalid_criteria":
				input.Criteria = nil
			case "current_authority":
				m.workPolicy = nil
			}
			m.sources.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
				return fact, found, sourceErr
			}
			// Canonical draft creation permits zero criteria; an invalid title still must
			// fail before writing, independently of the eventual baseline gate.
			if scenario == "invalid_criteria" {
				input.Summary = ""
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.createIntakeRequirementOn(ctx, tx, 1, 99, 50, input); return err }))
		})
	}
}

func TestIntakeWorkOwnerRequiresCanonicalRequirementVersionAndActiveOwner(t *testing.T) {
	m, db, ctx, body := requirementHarness(t)
	var requirement, work guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		r, err := m.createRequirementOn(ctx, tx, 1, 99, 11, body)
		if err != nil {
			return err
		}
		requirement = r.ID
		_, err = m.baselineRequirementOn(ctx, tx, 1, 99, requirement, 11, 1)
		return err
	}))
	version := 1
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		r, err := m.createWorkOn(ctx, tx, 1, 99, 11, workCreateBody{Title: "Canonical merged work", Type: "TASK", Objective: "Follow original requirement", AccountablePrincipalID: 11, RequirementID: &requirement, BaselineVersionNo: &version, Criteria: []workCriterionInput{{Text: "Actual requirement", VerificationMethod: "Human check"}}})
		work = r.ID
		return err
	}))
	var workVersion int
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Select("version").Where("id=?", work).Scan(&workVersion).Error)
	call := func(req guid.ID, v int) (guid.ID, error) {
		var owner guid.ID
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			owner, err = m.intakeWorkOwnerOn(ctx, tx, 1, 99, req, work, v)
			return err
		})
		return owner, err
	}
	owner, err := call(requirement, workVersion)
	require.NoError(t, err)
	require.Equal(t, guid.ID(11), owner)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.intakeWorkFenceOn(ctx, tx, 1, 99, work, workVersion) }))
	_, err = call(requirement+1, workVersion)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = call(requirement, workVersion+1)
	require.ErrorIs(t, err, ErrWorkVersion)
	_, err = m.intakeWorkOwnerOn(ctx, db, 1, 99, requirement, work, workVersion)
	require.Error(t, err, "canonical lock requires an actual transaction")
	policy := m.workPolicy
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = call(requirement, workVersion)
	require.Error(t, err, "a previously valid binding cannot bypass current read policy")
	m.workPolicy = policy
	members := m.members
	m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
	_, err = call(requirement, workVersion)
	require.Error(t, err, "revoked owner or actor must not remain eligible through the stored binding")
	m.members = members
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work).Updates(map[string]any{"state": "CANCELLED", "archived_at": time.Now().UTC()}).Error) //nolint:misspell // Frozen persisted protocol state.
	_, err = call(requirement, workVersion)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func delegatedNewHarness(t *testing.T) (*Module, *gorm.DB, context.Context, intake.TriageSource) {
	t.Helper()
	m, db, ctx, _ := requirementHarness(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Updates(map[string]any{"team_mode": "AUTONOMOUS", "budget_cap_micros": 1000}).Error)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 44, TenantID: 1, PrincipalID: 11, State: "ACTIVE"}, true, nil
	}
	m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, "intake_convert", permission)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	card := intake.TriageSource{ID: 51, IntakeID: 50, ProjectID: 99, ActorID: 12, Version: 2, Revision: 1, Hash: strings.Repeat("a", 64), Draft: intake.TriageDraft{Summary: "Canonical delegated draft", RequirementDraft: "Frozen original proposal", Criteria: []string{"Actual criterion"}, Size: "S"}}
	m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, req governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		require.Equal(t, "AGENT", req.ActorKind)
		require.Equal(t, "intake_convert", req.Code)
		require.Equal(t, guid.ID(50), req.Resource.ID)
		require.Equal(t, "12", req.Facts.TriageCardAuthorID)
		require.Equal(t, "NEW_REQUIREMENT", req.Facts.IntakeConvertMode)
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "DELEGATION:500", DelegationID: 500}, nil
	}
	m.delegatedNewSources = intake.DelegatedNewSources{ReadOn: func(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID) (intake.TriageSource, string, error) {
		authority, err := m.delegatedNewAuthorityOn(ctx, tx, tenant, project, card)
		return card, authority, err
	}, ConvertOn: func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, expected, revision int, hash string, requirement guid.ID) error {
		require.Equal(t, 2, expected)
		require.Equal(t, 1, revision)
		require.Equal(t, card.Hash, hash)
		return tx.Exec("INSERT INTO delegated_source_probe (id) VALUES (?)", requirement).Error
	}}
	require.NoError(t, db.Exec("CREATE TABLE delegated_source_probe (id BIGINT PRIMARY KEY)").Error)
	return m, db, ctx, card
}
func TestDelegatedNewPreviewUsesCurrentAuthorityWithoutCreatingDraft(t *testing.T) {
	for _, scenario := range []string{"success", "revoked", "source_failure", "missing_source"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, card := delegatedNewHarness(t)
			failure := errors.New("source read failed")
			switch scenario {
			case "revoked":
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{}, ErrProjectNonDelegable
				}
			case "source_failure":
				m.delegatedNewSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.TriageSource, string, error) {
					return intake.TriageSource{}, "", failure
				}
			case "missing_source":
				m.delegatedNewSources.ReadOn = nil
			}
			var got intake.TriageSource
			err := db.Transaction(func(tx *gorm.DB) error {
				var e error
				got, e = m.readDelegatedNewCardOn(ctx, tx, 1, 99, 50)
				return e
			})
			if scenario == "success" {
				require.NoError(t, err)
				require.Equal(t, card, got)
			} else {
				require.Error(t, err)
				require.Empty(t, got, "denied previews must not expose the frozen draft")
				if scenario == "source_failure" {
					require.ErrorIs(t, err, failure)
				}
			}
			for _, table := range []string{m.service.names.Requirements.String(), m.service.names.Criteria.String(), m.service.names.Counters.String(), "delegated_source_probe"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Zero(t, count, "preview must not create a requirement, criteria, counter or conversion receipt")
			}
		})
	}
}

func TestDelegatedNewCanonicalDraftAuthorityAndAtomicity(t *testing.T) {
	for _, scenario := range []string{"success", "hybrid", "self_card", "human", "denied_iam", "missing_grant", "grant_error", "missing_authority", "closed", "wrong_revision", "wrong_hash", "wrong_version", "invalid_draft", "audit_failure", "outbox_failure", "source_failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, card := delegatedNewHarness(t)
			version, revision, hash := card.Version, card.Revision, card.Hash
			var observed events.Event
			originalPublisher := m.service.events
			m.service.events = publisherFunc(func(c context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
				observed = event
				return originalPublisher.Publish(c, tx, event)
			})
			failure := errors.New("storage unavailable")
			switch scenario {
			case "hybrid":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("team_mode", "HYBRID").Error)
			case "self_card":
				card.ActorID = 11
				m.delegatedNewSources.ReadOn = func(c context.Context, tx *gorm.DB, t, p, _ guid.ID) (intake.TriageSource, string, error) {
					s, e := m.delegatedNewAuthorityOn(c, tx, t, p, card)
					return card, s, e
				}
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{ID: 11}, true, nil
				}
			case "denied_iam":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "missing_grant":
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:delegated_intake_convert"}, nil
				}
			case "grant_error":
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{}, failure
				}
			case "missing_authority":
				m.deliveryAuthority = nil
			case "closed":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("stage", "CLOSED").Error)
			case "wrong_revision":
				revision++
			case "wrong_hash":
				hash = strings.Repeat("b", 64)
			case "wrong_version":
				version++
			case "invalid_draft":
				card.Draft.Summary = ""
				m.delegatedNewSources.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (intake.TriageSource, string, error) {
					return card, "DELEGATION:500", nil
				}
			case "audit_failure":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox_failure":
				m.service.events = publisherFunc(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			case "source_failure":
				m.delegatedNewSources.ConvertOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, int, string, guid.ID) error {
					return failure
				}
			}
			var id guid.ID
			err := db.Transaction(func(tx *gorm.DB) error {
				var e error
				id, e = m.createDelegatedNewOn(ctx, tx, 1, 99, 50, version, revision, hash)
				return e
			})
			if scenario == "success" {
				require.NoError(t, err)
				var row model.Requirement
				require.NoError(t, db.Table(m.service.names.Requirements.String()).Where("id=?", id).Take(&row).Error)
				require.Equal(t, "DRAFT", row.Status)
				require.Equal(t, "NEEDS_WORK", row.ReviewState)
				require.Zero(t, row.CurrentVersionNo)
				require.EqualValues(t, 50, *row.SourceIntakeID)
				require.EqualValues(t, 11, *row.CreatedBy)
				require.Equal(t, "AGENT", observed.Actor.Kind)
				require.Equal(t, "DELEGATION:500", observed.Actor.AuthoritySource)
			} else {
				require.Error(t, err)
				for _, table := range []string{m.service.names.Requirements.String(), m.service.names.Criteria.String(), m.service.names.Counters.String(), "delegated_source_probe"} {
					var count int64
					require.NoError(t, db.Table(table).Count(&count).Error)
					require.Zero(t, count, table)
				}
			}
			// Ordinary creation still rejects this same Agent without invoking delegation.
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, nil
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.createRequirementOn(ctx, tx, 1, 99, 11, requirementCreateBody{Title: "Unauthorized general write"})
				return e
			}), ErrInvalidMember)
		})
	}
}

type publisherFunc func(context.Context, *gorm.DB, events.Event) (guid.ID, error)

func (f publisherFunc) Publish(c context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	return f(c, tx, e)
}

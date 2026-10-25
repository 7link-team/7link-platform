package governance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEscapeReaderRequiresCurrentPrincipalMembershipAndProjectPermission(t *testing.T) {
	for _, fault := range []string{"principal source", "inactive", "member", "permission source", "permission", "missing source"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _ := escapeHarness(t)
			switch fault {
			case "principal source", "inactive":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					if fault == "principal source" {
						return identity.Principal{}, false, ErrUnavailable
					}
					return identity.Principal{}, false, nil
				}
			case "member":
				m.delegations.members = delegationMembers{false}
			case "permission source", "permission":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					if fault == "permission source" {
						return authz.Explanation{}, ErrUnavailable
					}
					return authz.Explanation{}, nil
				}
			case "missing source":
				m.explain = nil
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.escapeReadAuthorityOn(ctx, tx, 1, 99) }))
		})
	}
}

func TestEscapeDecisionStorageAndAuditFailurePreservesPendingSource(t *testing.T) {
	for _, fault := range []string{"invalid state", "missing row", "missing audit", "audit", "update", "project source"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, id := escapeHarness(t)
			target, state := id, "RECORDED"
			switch fault {
			case "invalid state":
				state = "UNKNOWN"
			case "missing row":
				target = 999
			case "missing audit":
				m.seed.audit = nil
			case "audit":
				m.seed.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
			case "update":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_decision BEFORE UPDATE ON "+m.seed.names.QualityEscapes.String()+" BEGIN SELECT RAISE(ABORT,'decision failed'); END").Error)
			case "project source":
				m.delegations.projects.InspectOn = nil
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.decideEscapeOn(ctx, tx, 1, target, 1, state, ""); return err }))
			q := query.BindQualityEscapes(db, m.seed.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
			require.NoError(t, err)
			require.Equal(t, "PENDING", row.LearningState)
			require.Equal(t, 1, row.Version)
			require.Nil(t, row.DecidedBy)
		})
	}
}

func TestEscapeConsumerRejectsInvalidSourceFactsWithoutNewRows(t *testing.T) {
	for _, fault := range []string{"event identity", "payload", "unknown event", "foreign replay", "missing learning", "long eval", "invalid memory", "missing ids", "failed ids", "zero ids", "invalid gate", "insert", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, pub, _ := escapeHarness(t)
			data := map[string]any{"escape_key": "new:escape", "work_item_id": "45", "stage": "CLIENT_ACCEPTANCE", "category": "FUNCTIONAL_DEFECT", "summary": "Observed source defect", "passed_by": []PassedGate{{Stage: "INTERNAL_QA", PrincipalID: 3, ModelFamily: "observed", VerifierVersion: "v1"}}}
			e := events.Envelope{EventID: 101, TenantID: 1, ProjectID: 99, EventType: "quality.escaped"}
			switch fault {
			case "event identity":
				e.EventID = 0
			case "unknown event":
				e.EventType = "unknown"
			case "foreign replay":
				data["escape_key"] = "work:44:claim:55"
			case "missing learning":
				e.EventType = "quality.escape_learning_proposed"
			case "long eval", "invalid memory":
				e.EventType = "quality.escape_learning_proposed"
				data["escape_key"] = "work:44:claim:55"
				if fault == "long eval" {
					data["eval_case_ref"] = strings.Repeat("x", 201)
				} else {
					data["memory_ids"] = []string{"0"}
				}
			case "missing ids":
				m.seed.ids = nil
			case "failed ids":
				m.seed.ids = seedTestIDs{err: ErrUnavailable}
			case "zero ids":
				m.seed.ids = seedTestIDs{}
			case "invalid gate":
				data["passed_by"] = []PassedGate{{PrincipalID: 3}}
			case "insert":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_escape BEFORE INSERT ON "+m.seed.names.QualityEscapes.String()+" BEGIN SELECT RAISE(ABORT,'escape insert failed'); END").Error)
			case "outbox":
				pub.Fail = true
			}
			e.Data, _ = json.Marshal(data)
			if fault == "payload" {
				e.Data = []byte("invalid")
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeQualityEscape(ctx, tx, e) }))
			require.EqualValues(t, 1, seedCount(t, db, m.seed.names.QualityEscapes.String()))
		})
	}
}

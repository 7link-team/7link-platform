package delivery

import (
	"strings"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVisualReviewAuthorityUsesCurrentResultWithoutExecutorLease(t *testing.T) {
	for _, fault := range []string{"", "version", "head", "execution", "criterion", "self", "stale", "claim", "policy", "project"} {
		t.Run(fault, func(t *testing.T) {
			h := newResultFixture(t)
			result, err := h.submit()
			require.NoError(t, err)
			h.m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
				require.Equal(t, guid.ID(1), tenant)
				require.Equal(t, guid.ID(99), project)
				require.Equal(t, "verification_run", permission)
				require.Contains(t, []guid.ID{11, 12}, actor)
				return authz.DataConstraint{AllowAll: true}, nil
			}
			head := strings.Repeat("b", 40)
			r := query.BindResultVersions(h.db, h.m.service.names)
			_, err = r.WithContext(h.ctx).Where(r.ID.Eq(result.ID)).Updates(map[string]any{"source_execution_id": result.ID, "head_commit": head})
			require.NoError(t, err)
			criteria := query.BindWorkCriteria(h.db, h.m.service.names)
			criterion, err := criteria.WithContext(h.ctx).Where(criteria.WorkItemID.Eq(h.work)).First()
			require.NoError(t, err)
			link := ExecutionWorkLink{TenantID: 1, ProjectID: 99, WorkItemID: h.work, ExecutionID: result.ID, ActorID: 12, InitiatorID: 11}
			version, criterionID := 1, criterion.ID
			switch fault {
			case "version":
				version++
			case "head":
				head = strings.Repeat("c", 40)
			case "execution":
				link.ExecutionID++
			case "criterion":
				criterionID = guid.ID(999)
			case "self":
				link.ActorID = 11
			case "stale", "claim":
				w := query.BindWorkItems(h.db, h.m.service.names)
				changes := map[string]any{"baseline_stale": true}
				if fault == "claim" {
					changes = map[string]any{"current_claim_id": nil}
				}
				_, err = w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).Updates(changes)
				require.NoError(t, err)
			case "policy":
				h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "project":
				p := query.BindProjects(h.db, h.m.service.names)
				_, err = p.WithContext(h.ctx).Where(p.ProjectID.Eq(guid.ID(99))).Update(p.Stage, "CLOSED")
				require.NoError(t, err)
			}
			err = h.db.Transaction(func(tx *gorm.DB) error {
				return h.m.authorizeVisualReviewOn(h.actor(link.ActorID), tx, link, result.ID, version, criterionID, head)
			})
			if fault == "" {
				require.NoError(t, err, "completed execution has no current execution or live executor lease")
			} else {
				require.Error(t, err)
			}
		})
	}
}

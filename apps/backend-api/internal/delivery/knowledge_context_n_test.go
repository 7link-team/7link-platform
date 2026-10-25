package delivery

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestKnowledgeReadRequiresCurrentPrincipalMembershipScopeAndValidStoredReferences(t *testing.T) {
	for _, fault := range []string{"principal read", "inactive", "wrong principal", "membership read", "revoked", "policy read", "scope", "project missing", "project inactive", "work missing", "row scope", "malformed refs", "invalid ref", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			h := newResultFixture(t)
			failure := errors.New("source unavailable")
			h.m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				if fault == "principal read" {
					return identity.Principal{}, false, failure
				}
				if fault == "wrong principal" {
					id++
				}
				return identity.Principal{ID: id}, fault != "inactive", nil
			}
			h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
				if fault == "membership read" {
					return false, failure
				}
				return fault != "revoked", nil
			})
			h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				if fault == "policy read" {
					return authz.DataConstraint{}, failure
				}
				if fault == "scope" {
					return authz.DataConstraint{}, nil
				}
				if fault == "row scope" {
					return authz.DataConstraint{OwnerIDs: []string{"12"}}, nil
				}
				return authz.DataConstraint{AllowAll: true}, nil
			}
			project, work := guid.ID(99), h.work
			ctx := h.actor(12)
			switch fault {
			case "project missing":
				project++
			case "project inactive":
				require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "CLOSED").Error)
			case "work missing":
				work++
			case "malformed refs":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", work).Update("context_refs", "[1]").Error)
			case "invalid ref":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", work).Update("context_refs", `[{"kind":"KNOWLEDGE","ref":"invalid"}]`).Error)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				ids, err := h.m.workKnowledgeContextOn(ctx, tx, 1, project, work)
				require.Empty(t, ids, "an incomplete or unauthorized context is never returned")
				return err
			})
			switch fault {
			case "principal read", "membership read", "policy read":
				require.ErrorIs(t, err, failure)
			case "inactive", "wrong principal", "revoked":
				require.ErrorIs(t, err, ErrWorkForbidden)
			case "project missing", "work missing", "row scope", "scope":
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			case "project inactive":
				require.ErrorIs(t, err, ErrWorkProjectReadOnly)
			case "malformed refs":
				require.ErrorIs(t, err, ErrUnavailable)
			case "invalid ref":
				require.ErrorIs(t, err, ErrInvalidArgument)
			case "canceled":
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}

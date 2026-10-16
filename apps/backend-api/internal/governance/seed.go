package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrUnavailable     = errors.New("business governance unavailable")
	ErrInvalidArgument = errors.New("business governance invalid argument")
)

type roleManifestEntry struct {
	Key, Name, Description string
	Codes                  []string
	Conditions             map[string]json.RawMessage
}

type seedService struct {
	names    query.TableNames
	ids      guid.Source
	roles    core.RoleSeeder
	registry *authz.Registry
	audit    audit.Appender
	now      func() time.Time
	acquire  dlock.TransactionLease
}

// apply serializes one tenant's complete manifest, including audit, using the
// shared lock provider and one SQL transaction. Scheduled reconciliation may
// skip ErrNotAcquired; request handlers must not mistake it for successful seed
// provisioning. Lease release runs after the transaction has ended.
func (s *seedService) apply(ctx context.Context, db *gorm.DB, tenantID guid.ID, version int, manifest []roleManifestEntry) (err error) {
	if s == nil || s.acquire == nil || db == nil {
		return ErrUnavailable
	}
	if tenantID <= 0 {
		return ErrInvalidArgument
	}
	lease, err := s.acquire(ctx, "governance:seed:"+tenantID.String(), time.Minute)
	if err != nil {
		return err
	}
	if lease == nil {
		return ErrUnavailable
	}
	defer func() { err = errors.Join(err, lease.Unlock(context.WithoutCancel(ctx))) }()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lease.FenceOn(ctx, tx); err != nil {
			return err
		}
		return s.applyOn(ctx, tx, tenantID, version, manifest)
	})
}

type seedChange struct {
	RoleKey    string   `json:"role_key"`
	AddedCodes []string `json:"added_codes"`
}

// applyOn runs under the caller's tenant seed lock and SQL transaction. The
// caller must roll back every error. The ledger records historical grants, not
// current IAM permissions: an administrator's revocation must remain revoked.
func (s *seedService) applyOn(ctx context.Context, tx *gorm.DB, tenantID guid.ID, version int, manifest []roleManifestEntry) error {
	if s == nil || s.ids == nil || s.roles.EnsureRoleOn == nil || s.roles.GrantPermissionOn == nil || s.registry == nil || s.audit == nil || s.now == nil || s.names.RoleSeeds.IsZero() {
		return ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenantID <= 0 || version <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	changes := make([]seedChange, 0)
	for _, entry := range manifest {
		change, changed, err := s.applyRoleOn(ctx, tx, tenantID, version, entry)
		if err != nil {
			return err
		}
		if changed {
			changes = append(changes, change)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	event := audit.NewDomainEvent(ctx, tenantID, "governance.role_seed_applied", "tenant", tenantID)
	event.Detail["manifest_version"] = version
	event.Detail["roles"] = changes
	return s.audit(ctx, tx, event)
}

func (s *seedService) applyRoleOn(ctx context.Context, tx *gorm.DB, tenantID guid.ID, version int, entry roleManifestEntry) (seedChange, bool, error) {
	change := seedChange{RoleKey: entry.Key, AddedCodes: []string{}}
	if entry.Key == "" || len(entry.Key) > 48 || strings.TrimSpace(entry.Name) == "" {
		return change, false, ErrInvalidArgument
	}
	table := query.BindRoleSeeds(tx, s.names)
	row, err := table.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(table.TenantID.Eq(tenantID), table.RoleKey.Eq(entry.Key)).First()
	created := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !created {
		return change, false, err
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	var by *guid.ID
	if actor > 0 {
		by = &actor
	}
	if created {
		role, roleErr := s.roles.EnsureRoleOn(ctx, tx, tenantID, strings.ToLower(strings.TrimSpace(entry.Name)), entry.Name, entry.Description)
		conflict := errors.Is(roleErr, core.ErrRoleNameConflict) && role.ID > 0
		if roleErr != nil && !conflict {
			return change, false, roleErr
		}
		if role.ID <= 0 {
			return change, false, ErrUnavailable
		}
		id, idErr := s.ids.Next()
		if idErr != nil {
			return change, false, idErr
		}
		if id <= 0 {
			return change, false, ErrUnavailable
		}
		row = &model.RoleSeed{ID: guid.ID(id), TenantID: tenantID, RoleKey: entry.Key, RoleID: role.ID, ManifestVersion: version, AppliedCodes: "[]", SkippedCodes: "[]", Managed: !conflict, SeedConflict: conflict, Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: by, UpdatedBy: by}
		if conflict {
			return change, true, table.WithContext(ctx).Create(row)
		}
	} else if !row.Managed || row.SeedConflict {
		return change, false, nil
	}
	applied, err := decodeSeedCodes(row.AppliedCodes)
	if err != nil {
		return change, false, err
	}
	codes := append([]string(nil), entry.Codes...)
	slices.Sort(codes)
	codes = slices.Compact(codes)
	skipped := make([]string, 0)
	for _, code := range codes {
		if _, ok := s.registry.Find(code); !ok {
			skipped = append(skipped, code)
			continue
		}
		if slices.Contains(applied, code) {
			continue
		}
		var grantErr error
		if condition, conditional := entry.Conditions[code]; conditional {
			if s.roles.GrantConditionalPermissionOn == nil {
				return change, false, ErrUnavailable
			}
			_, grantErr = s.roles.GrantConditionalPermissionOn(ctx, tx, tenantID, row.RoleID, code, condition)
		} else {
			_, grantErr = s.roles.GrantPermissionOn(ctx, tx, tenantID, row.RoleID, code)
		}
		if grantErr != nil {
			return change, false, grantErr
		}
		applied = append(applied, code)
		change.AddedCodes = append(change.AddedCodes, code)
	}
	slices.Sort(applied)
	applied = slices.Compact(applied)
	appliedJSON, _ := json.Marshal(applied)
	skippedJSON, _ := json.Marshal(skipped)
	changed := created || row.AppliedCodes != string(appliedJSON) || row.SkippedCodes != string(skippedJSON) || row.ManifestVersion != version
	if !changed {
		return change, false, nil
	}
	row.AppliedCodes = string(appliedJSON)
	row.SkippedCodes = string(skippedJSON)
	row.ManifestVersion = version
	row.UpdatedAt = now
	row.UpdatedBy = by
	if created {
		if entry.Key == "team_owner" {
			if s.roles.SeedAdministratorMembersOn == nil {
				return change, false, ErrUnavailable
			}
			if _, err := s.roles.SeedAdministratorMembersOn(ctx, tx, tenantID, row.RoleID); err != nil {
				return change, false, err
			}
		}
		return change, true, table.WithContext(ctx).Create(row)
	}
	row.Version++
	result := tx.WithContext(ctx).Table(s.names.RoleSeeds.String()).Where("tenant_id = ? AND id = ?", tenantID, row.ID).Select("*").Updates(row)
	return change, true, result.Error
}

func decodeSeedCodes(raw string) ([]string, error) {
	var codes []string
	if err := json.Unmarshal([]byte(raw), &codes); err != nil || codes == nil {
		return nil, fmt.Errorf("%w: invalid seed permission ledger", ErrInvalidArgument)
	}
	return codes, nil
}

package governance

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"time"
)

func (m *Module) injectProductionAuthority(s *module.Services) error {
	reader, err := module.Resolve[core.TemporaryGrantReader](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	m.temporaryGrants = reader
	return nil
}

// Production authority is an additional gate. Neither administrator/permanent
// roles nor delegated authority can replace an approved bounded temporary grant.
func (m *Module) checkProductionAuthority(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error) {
	deny := func(reason string) (AuthorityDecision, error) {
		return AuthorityDecision{ReasonCode: reason, Steps: []AuthorityStep{{Step: "S2", Outcome: "DENY"}}}, nil
	}
	if req.ActorKind != "HUMAN" {
		return deny("AGENT_PROHIBITED")
	}
	if m == nil || m.seed == nil || m.explain == nil || m.temporaryGrants == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil {
		return AuthorityDecision{}, ErrUnavailable
	}
	if tx == nil {
		if m.runtime == nil || m.runtime.Database() == nil {
			return AuthorityDecision{}, ErrUnavailable
		}
		var result AuthorityDecision
		err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(actual *gorm.DB) error {
			var e error
			result, e = m.checkProductionAuthority(ctx, actual, req)
			return e
		})
		return result, err
	}
	if tx.Statement == nil {
		return AuthorityDecision{}, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return AuthorityDecision{}, ErrInvalidArgument
	}
	if err := m.principals.Lock(ctx, tx, req.PrincipalID); err != nil {
		return AuthorityDecision{}, err
	}
	p, human, err := m.principals.ActiveHumanByID(ctx, tx, req.PrincipalID)
	if err != nil {
		return AuthorityDecision{}, err
	}
	if !human || p.ID != req.PrincipalID {
		return deny("AGENT_PROHIBITED")
	}
	x, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: req.TenantID, EntityID: req.ProjectID, PrincipalID: req.PrincipalID, Permission: "production_access"})
	if err != nil {
		return AuthorityDecision{}, err
	}
	if !x.Allowed || x.Reason == "explicit_deny" || x.Reason == "administrator_scope_denied" {
		return deny("PRODUCTION_ACCESS_REQUIRED")
	}
	settings, err := m.readSettingsOn(ctx, tx, req.TenantID)
	if err != nil {
		return AuthorityDecision{}, err
	}
	reason := "PRODUCTION_GRANT_NOT_TEMPORARY"
	now := m.seed.now()
	for _, match := range x.Matches {
		if match.Effect != "allow" || match.PermissionCode != "production_access" || match.SourceType != "temporary_role" {
			continue
		}
		id, e := guid.RequireID(match.SourceID)
		if e != nil {
			continue
		}
		grant, found, e := m.temporaryGrants(ctx, tx, req.TenantID, req.PrincipalID, id)
		if e != nil {
			return AuthorityDecision{}, e
		}
		if !found || grant.SourceType != "access_request" || grant.RoleID.String() != match.RoleID || grant.StartsAt.After(now) || !grant.EndsAt.After(now) || !grant.EndsAt.After(grant.StartsAt) {
			continue
		}
		if grant.EndsAt.Sub(grant.StartsAt) > time.Duration(settings.ProductionAccessMaxHours)*time.Hour {
			reason = "PRODUCTION_GRANT_TOO_LONG"
			continue
		}
		return AuthorityDecision{Allowed: true, AuthoritySource: "TEMPORARY_ROLE:" + id.String(), PolicyRevision: x.Revision, Steps: []AuthorityStep{{Step: "S1", Outcome: "PASS"}, {Step: "S2", Outcome: "MATCH"}}}, nil
	}
	return deny(reason)
}

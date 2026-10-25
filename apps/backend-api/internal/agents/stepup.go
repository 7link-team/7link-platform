package agents

import (
	"context"

	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type proofCookieKey struct{}

func (m *Module) injectStepUp(s *module.Services) error {
	consume, err := module.Resolve[authn.ReauthenticationConsumer](s)
	if err != nil {
		return err
	}
	if consume == nil {
		return ErrUnavailable
	}
	if err = m.bindCredentialProof(func(ctx context.Context, tx *gorm.DB, tenant guid.ID, entryKey, proof string) error {
		cookie, _ := ctx.Value(proofCookieKey{}).(string)
		return consume(ctx, tx, actorID(ctx), cookie, proof, iamauthn.ReauthenticationAgentToolCredentialWrite,
			"tenant:"+tenant.String()+":tool:"+entryKey)
	}); err != nil {
		return err
	}
	m.fullResetProof = func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, proof string) error {
		cookie, _ := ctx.Value(proofCookieKey{}).(string)
		return consume(ctx, tx, actorID(ctx), cookie, proof, iamauthn.ReauthenticationAgentFullReset,
			"tenant:"+tenant.String()+":agent:"+agent.String())
	}
	m.forcedRetirementProof = func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, proof string) error {
		cookie, _ := ctx.Value(proofCookieKey{}).(string)
		return consume(ctx, tx, actorID(ctx), cookie, proof, iamauthn.ReauthenticationAgentForcedRetire,
			"tenant:"+tenant.String()+":agent:"+agent.String())
	}
	return nil
}

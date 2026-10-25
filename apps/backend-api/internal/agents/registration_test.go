package agents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type registrationMounter func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) error

func (f registrationMounter) MountOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, skills []guid.ID) error {
	return f(ctx, tx, tenant, agent, skills)
}

func bindRegistrationFixture(t *testing.T, m *Module, fail string) {
	t.Helper()
	m.memoryPolicy = knowledge.MemoryPolicyValidator{Validate: func(_ context.Context, _ guid.ID, raw json.RawMessage) error {
		if string(raw) != `{"profile":"STANDARD"}` {
			return ErrInvalid
		}
		return nil
	}}
	m.registration.identity = identity.AgentIdentityRegistration{
		Prepare: func(context.Context, string, int64) (*identity.PreparedAgentIdentity, error) { return nil, nil },
		CreateOn: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, handle, name string, _ *identity.PreparedAgentIdentity) (identity.ServiceAccountCredentialSecret, error) {
			if e := tx.Exec("INSERT INTO audit_probe VALUES (?)", "identity-avatar-credential").Error; e != nil {
				return identity.ServiceAccountCredentialSecret{}, e
			}
			if fail == "identity" {
				return identity.ServiceAccountCredentialSecret{}, errors.New("identity failure")
			}
			return identity.ServiceAccountCredentialSecret{Credential: identity.ServiceAccountCredential{ID: 123, ServiceAccountID: 7}, Secret: "private-control-plane-secret"}, nil
		},
	}
	m.registration.fleet = fleet.AgentProvisioner{CreateOn: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, in fleet.AgentRegistration, profile fleet.AgentProfilePatch, secret identity.ServiceAccountCredentialSecret) (fleet.RegisteredAgent, error) {
		require.EqualValues(t, 7, in.PrincipalID)
		require.Equal(t, "private-control-plane-secret", secret.Secret)
		require.Equal(t, `{"profile":"STANDARD"}`, string(*profile.MemoryPolicy)) //nolint:testifylint // Serialized bytes are frozen by this regression contract.
		if e := tx.Exec("INSERT INTO audit_probe VALUES (?)", "fleet-roles-config-delivery").Error; e != nil {
			return fleet.RegisteredAgent{}, e
		}
		if fail == "fleet" {
			return fleet.RegisteredAgent{}, errors.New("fleet failure")
		}
		return fleet.RegisteredAgent{ID: 70, PrincipalID: 7, State: "ACTIVE", Version: 2}, nil
	}}
	m.registration.skills = registrationMounter(func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, ids []guid.ID) error {
		require.EqualValues(t, 7, agent)
		require.Equal(t, []guid.ID{55}, ids)
		if e := tx.Exec("INSERT INTO audit_probe VALUES (?)", "skill-mount").Error; e != nil {
			return e
		}
		if fail == "skills" {
			return knowledge.ErrSkillNotMountable
		}
		return nil
	})
}

func atomicCreationInput() AgentCreation {
	return AgentCreation{MachineID: 9, Name: "Atomic executor", Handle: "atomic-executor", Runtime: "CODEX", Model: "gpt-test", Roles: []string{"EXECUTOR"}, SystemPrompt: "Use the frozen plan", AuthorizationMode: "READ_ONLY", AvatarStyle: "PIXEL", AvatarSeed: 42, SkillIDs: []guid.ID{55}, MemoryPolicy: `{"profile":"STANDARD"}`, DefaultChannels: []string{}}
}

func TestAtomicCreationOrchestrationRollsBackEveryParticipant(t *testing.T) {
	for _, failure := range []string{"identity", "fleet", "skills", "state", ""} {
		t.Run("failure_"+failure, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			bindRegistrationFixture(t, m, failure)
			if failure == "state" {
				require.NoError(t, db.Migrator().DropTable(m.names.LiveState.String()))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.createAgentOn(agentContext(t, 2), tx, 1, atomicCreationInput(), nil)
				return e
			})
			var count int64
			require.NoError(t, db.Table("audit_probe").Count(&count).Error)
			if failure != "" {
				require.Error(t, err)
				require.Zero(t, count)
			} else {
				require.NoError(t, err)
				require.EqualValues(t, 3, count)
				q := query.BindLiveState(db, m.names)
				row, e := q.WithContext(t.Context()).First()
				require.NoError(t, e)
				require.EqualValues(t, 7, row.AgentPrincipalID)
			}
		})
	}
}

func TestAtomicCreationHTTPReplayCurrentAuthorityAndNoCredentialDisclosure(t *testing.T) {
	m, db, handler, _ := agentHTTPHarness(t)
	bindRegistrationFixture(t, m, "")
	body, e := json.Marshal(atomicCreationInput())
	require.NoError(t, e)
	path := "/tenants/1/agent-registrations"
	first := callAgent(t, handler, agentContext(t, 2), "POST", path, "atomic-create", string(body))
	require.Equal(t, 201, first.Code, first.Body.String())
	require.NotContains(t, first.Body.String(), "secret")
	second := callAgent(t, handler, agentContext(t, 2), "POST", path, "atomic-create", string(body))
	require.Equal(t, 201, second.Code, second.Body.String())
	require.Equal(t, "true", second.Header().Get("Idempotent-Replayed"))
	var count int64
	require.NoError(t, db.Table("audit_probe").Count(&count).Error)
	require.EqualValues(t, 3, count)
	changed := strings.Replace(string(body), "Atomic executor", "Other executor", 1)
	require.Equal(t, 409, callAgent(t, handler, agentContext(t, 2), "POST", path, "atomic-create", changed).Code)
	for _, actor := range []guid.ID{7, 8, 9} {
		require.Equal(t, 403, callAgent(t, handler, agentContext(t, actor), "POST", path, "blocked", string(body)).Code)
	}
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Equal(t, 403, callAgent(t, handler, agentContext(t, 2), "POST", path, "atomic-create", string(body)).Code)
}

func TestAtomicCreationCurrentAuthorityPreparationAndPolicyFailuresHaveNoSideEffects(t *testing.T) {
	for _, mode := range []string{"missing-capability", "policy", "too-many-skills", "prepare", "principal", "human-source", "policy-source", "missing-authority"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _, _ := agentHTTPHarness(t)
			bindRegistrationFixture(t, m, "")
			ctx := agentContext(t, 2)
			fault := errors.New("registration source failed")
			in := atomicCreationInput()
			switch mode {
			case "missing-capability":
				m.registration.fleet = fleet.AgentProvisioner{}
			case "policy":
				in.MemoryPolicy = `{"profile":"INVALID"}`
			case "too-many-skills":
				in.SkillIDs = make([]guid.ID, 31)
			case "prepare":
				m.registration.identity.Prepare = func(context.Context, string, int64) (*identity.PreparedAgentIdentity, error) { return nil, fault }
			case "principal":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "human-source":
				m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "policy-source":
				m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, fault
				}
			case "missing-authority":
				m.members = nil
			}
			var err error
			if mode == "too-many-skills" {
				err = db.Transaction(func(tx *gorm.DB) error { _, e := m.createAgentOn(ctx, tx, 1, in, nil); return e })
			} else {
				_, err = m.createAgentHTTP(ctx, &AgentCreationInput{TeamPath: TeamPath{TenantID: "1"}, IdempotencyKey: "failed-registration", Body: in})
			}
			require.Error(t, err)
			var count int64
			require.NoError(t, db.Table("audit_probe").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

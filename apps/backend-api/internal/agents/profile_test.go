package agents

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProfileCurrentAuthorityAndSelfRuntimeConfiguration(t *testing.T) {
	m, db, _ := agentHarness(t)
	calls := 0
	m.profiles.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, agent guid.ID) (fleet.AgentProfile, error) {
		calls++
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(7), agent)
		return fleet.AgentProfile{SystemPrompt: "private", ConfigRevision: 3}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, actor := range []guid.ID{2, 7} {
			value, e := m.profileOn(agentContext(t, actor), tx, 1, 7, actor == 7)
			require.NoError(t, e)
			require.Equal(t, "private", value.SystemPrompt)
		}
		_, e := m.profileOn(agentContext(t, 2), tx, 1, 7, true)
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.profileOn(agentContext(t, 7), tx, 1, 7, false)
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.profileOn(agentContext(t, 9), tx, 1, 7, false)
		require.ErrorIs(t, e, ErrForbidden)
		require.Equal(t, 2, calls)
		return nil
	}))
}

func TestProfileUpdateIsHumanOnlyAndRecordsNoPromptBytes(t *testing.T) {
	m, db, _ := agentHarness(t)
	calls := 0
	m.profiles.UpdateOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, in fleet.AgentProfilePatch) (fleet.AgentProfile, error) {
		calls++
		return fleet.AgentProfile{Version: in.Version + 1}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		prompt := "never copy prompt into audit"
		in := fleet.AgentProfilePatch{Version: 1, SystemPrompt: &prompt}
		_, e := m.updateProfileOn(agentContext(t, 7), tx, 1, 7, in)
		require.ErrorIs(t, e, ErrForbidden)
		require.Zero(t, calls)
		value, e := m.updateProfileOn(agentContext(t, 2), tx, 1, 7, in)
		require.NoError(t, e)
		require.Equal(t, 2, value.Version)
		require.Equal(t, 1, calls)
		return nil
	}))
}

func TestRuntimeConfigOnlySelfReceivesCurrentGrantedSkillBytes(t *testing.T) {
	m, db, _ := agentHarness(t)
	m.profiles.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentProfile, error) {
		return fleet.AgentProfile{}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		tool, e := m.createToolOn(agentContext(t, 2), tx, 1, ToolInput{EntryKey: "runtime-review", Kind: "SKILL", Title: "Review", SkillBody: "Review actual code"})
		require.NoError(t, e)
		_, e = m.grantOn(agentContext(t, 2), tx, 1, tool.ID, 7, "ACTIVE")
		require.NoError(t, e)
		out, e := m.runtimeConfigOn(agentContext(t, 7), tx, 1, 7)
		require.NoError(t, e)
		require.Len(t, out.Skills, 1)
		require.Equal(t, "Review actual code", out.Skills[0].Content)
		require.Equal(t, tool.ContentHash, out.Skills[0].SHA256)
		_, e = m.runtimeConfigOn(agentContext(t, 2), tx, 1, 7)
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.grantOn(agentContext(t, 2), tx, 1, tool.ID, 7, "REVOKED")
		require.NoError(t, e)
		out, e = m.runtimeConfigOn(agentContext(t, 7), tx, 1, 7)
		require.NoError(t, e)
		require.Empty(t, out.Skills)
		return nil
	}))
}

func TestProfilePublicHandlersRenderStableAvatarCandidatesAndRecheckReplay(t *testing.T) {
	m, _, h, _ := agentHTTPHarness(t)
	m.profiles.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentProfile, error) {
		return fleet.AgentProfile{AvatarStyle: "PIXEL", AvatarSeed: 42, Version: 1}, nil
	}
	m.profiles.UpdateOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, in fleet.AgentProfilePatch) (fleet.AgentProfile, error) {
		return fleet.AgentProfile{Version: in.Version + 1, AvatarStyle: "PIXEL", AvatarSeed: 42}, nil
	}
	m.profileAvatar.Render = func(context.Context, string, int64) ([]byte, error) { return []byte("rendered pixels"), nil }
	path := "/tenants/1/agents/7"
	response := callAgent(t, h, agentContext(t, 2), "GET", path+"/profile", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), base64.StdEncoding.EncodeToString([]byte("rendered pixels")))
	response = callAgent(t, h, agentContext(t, 7), "GET", path+"/runtime-config", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	first, e := m.avatarCandidatesHTTP(agentContext(t, 2), &AvatarCandidatesInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}, Style: "PIXEL", Batch: 2})
	require.NoError(t, e)
	require.Len(t, first.Body.Data, 6)
	again, e := m.avatarCandidates(agentContext(t, 2), 1, 7, "PIXEL", 2)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data, again)
	other, e := m.avatarCandidates(agentContext(t, 2), 1, 7, "PIXEL", 3)
	require.NoError(t, e)
	require.NotEqual(t, again[0].Seed, other[0].Seed)
	response = callAgent(t, h, agentContext(t, 2), "PATCH", path+"/profile", "profile-write", `{"version":1,"system_prompt":"Reviewed prompt"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = callAgent(t, h, agentContext(t, 2), "PATCH", path+"/profile", "profile-write", `{"version":1,"system_prompt":"Reviewed prompt"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "true", response.Header().Get("Idempotent-Replayed"))
	_, e = m.avatarCandidates(agentContext(t, 2), 1, 7, "PIXEL", -1)
	require.ErrorIs(t, e, ErrInvalid)
	m.profileAvatar.Render = func(context.Context, string, int64) ([]byte, error) { return nil, ErrUnavailable }
	_, e = m.avatarCandidates(agentContext(t, 2), 1, 7, "PIXEL", 2)
	require.ErrorIs(t, e, ErrInvalid)
	_, e = m.profileWithAvatar(agentContext(t, 2), fleet.AgentProfile{})
	require.ErrorIs(t, e, ErrUnavailable)
	m.profileAvatar.Render = nil
	_, e = m.avatarCandidates(agentContext(t, 2), 1, 7, "PIXEL", 2)
	require.ErrorIs(t, e, ErrUnavailable)
}

func TestProfileCurrentProviderFailuresAndPolicyValidationAreExplicit(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.profileOn(ctx, tx, 1, 7, false); return e }), ErrUnavailable)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.updateProfileOn(ctx, tx, 1, 7, fleet.AgentProfilePatch{Version: 1})
		return e
	}), ErrUnavailable)
	for _, tc := range []struct{ source, want error }{{fleet.ErrInvalidArgument, ErrInvalid}, {fleet.ErrVersionConflict, ErrConflict}, {gorm.ErrRecordNotFound, ErrNotFound}, {fleet.ErrUnavailable, ErrUnavailable}} {
		m.profiles.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentProfile, error) {
			return fleet.AgentProfile{}, tc.source
		}
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.profileOn(ctx, tx, 1, 7, false); return e }), tc.want)
		m.profiles.UpdateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, fleet.AgentProfilePatch) (fleet.AgentProfile, error) {
			return fleet.AgentProfile{}, tc.source
		}
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.updateProfileOn(ctx, tx, 1, 7, fleet.AgentProfilePatch{Version: 1})
			return e
		}), tc.want)
	}
	policy := json.RawMessage(`{"profile":"STANDARD"}`)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.updateProfileOn(ctx, tx, 1, 7, fleet.AgentProfilePatch{Version: 1, MemoryPolicy: &policy})
		return e
	}), ErrUnavailable)
	m.memoryPolicy.Validate = func(context.Context, guid.ID, json.RawMessage) error { return ErrInvalid }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.updateProfileOn(ctx, tx, 1, 7, fleet.AgentProfilePatch{Version: 1, MemoryPolicy: &policy})
		return e
	}), ErrInvalid)
	for _, bad := range []AgentPath{{TenantID: "0", AgentPrincipalID: "7"}, {TenantID: "1", AgentPrincipalID: "bad"}} {
		_, e := m.profileHTTP(ctx, &ProfileReadInput{AgentPath: bad})
		require.Error(t, e)
		_, e = m.runtimeConfigHTTP(ctx, &ProfileReadInput{AgentPath: bad})
		require.Error(t, e)
		_, e = m.updateProfileHTTP(ctx, &ProfileUpdateInput{AgentPath: bad})
		require.Error(t, e)
		_, e = m.avatarCandidatesHTTP(ctx, &AvatarCandidatesInput{AgentPath: bad})
		require.Error(t, e)
	}
}

func TestRuntimeConfigurationRejectsCorruptPublishedSkillAndStorageFailures(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	m.profiles.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentProfile, error) {
		return fleet.AgentProfile{PrincipalID: 7, Version: 1}, nil
	}
	var tool ToolView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		tool, e = m.createToolOn(agentContext(t, 2), tx, 1, ToolInput{EntryKey: "runtime-integrity", Kind: "SKILL", Title: "Current", SkillBody: "Only use verified sources"})
		if e != nil {
			return e
		}
		_, e = m.grantOn(agentContext(t, 2), tx, 1, tool.ID, 7, "ACTIVE")
		return e
	}))
	v := query.BindToolVersion(db, m.names)
	_, err := v.WithContext(ctx).Where(v.ToolID.Eq(tool.ID)).Update(v.SkillBody, "corrupt bytes")
	require.NoError(t, err)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.runtimeConfigOn(ctx, tx, 1, 7); return e }), ErrUnavailable)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("runtime-version", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.ToolVersion.String() {
			tx.AddError(ErrUnavailable)
		}
	}))
	defer db.Callback().Query().Remove("runtime-version")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.runtimeConfigOn(ctx, tx, 1, 7); return e }), ErrUnavailable)
}

func TestProfileServiceInjectionBindsActualProcessObservationAndPropagatesRefusal(t *testing.T) {
	m, db, _ := agentHarness(t)
	var observer func(context.Context, *gorm.DB, fleet.AgentProcessTransition) error
	services := module.NewServices()
	require.NoError(t, module.Provide(services, identity.AgentAvatar{}))
	require.NoError(t, module.Provide(services, knowledge.MemoryPolicyValidator{}))
	require.NoError(t, module.Provide(services, fleet.AgentProfiles{BindProcessObserver: func(next func(context.Context, *gorm.DB, fleet.AgentProcessTransition) error) error {
		observer = next
		return nil
	}}))
	require.NoError(t, services.Seal())
	require.NoError(t, m.injectProfiles(services))
	require.NotNil(t, observer)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return observer(t.Context(), tx, fleet.AgentProcessTransition{TenantID: 1, AgentPrincipalID: 7, Kind: "STARTED", OccurredAt: m.now()})
	}))
	var count int64
	require.NoError(t, query.BindProcessEvents(db, m.names).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentInspectionHTTPCurrentAuthorityAndContracts(t *testing.T) {
	m, _, handler, api := agentHTTPHarness(t)
	called := 0
	m.selectedWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, int) (delivery.PersonalWorkPage, error) {
		called++
		return delivery.PersonalWorkPage{Items: []delivery.PersonalWorkView{}}, nil
	}
	m.directReader.ChannelsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) ([]collab.ChannelView, error) {
		called++
		return []collab.ChannelView{}, nil
	}
	m.directReader.MessagesOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) ([]collab.MessageView, error) {
		called++
		return []collab.MessageView{}, nil
	}
	for _, suffix := range []string{"/work", "/direct-channels", "/direct-channels/20/messages"} {
		path := "/tenants/1/agents/7" + suffix
		response := callAgent(t, handler, agentContext(t, 2), "GET", path, "", "")
		require.Equal(t, 200, response.Code, response.Body.String())
		for _, actor := range []guid.ID{7, 8, 9} {
			response = callAgent(t, handler, agentContext(t, actor), "GET", path, "", "")
			require.Equal(t, 403, response.Code, response.Body.String())
		}
		response = callAgent(t, handler, agentContext(t, 2), "GET", path+"?limit=0", "", "")
		require.Equal(t, 422, response.Code, response.Body.String())
		require.Contains(t, api.OpenAPI().Paths, "/tenants/{tenant_id}/agents/{agent_principal_id}"+strings.ReplaceAll(suffix, "/20/", "/{channel_id}/"))
	}
	require.Equal(t, 3, called)
	m.selectedWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, int) (delivery.PersonalWorkPage, error) {
		return delivery.PersonalWorkPage{}, delivery.ErrInvalidArgument
	}
	response := callAgent(t, handler, agentContext(t, 2), "GET", "/tenants/1/agents/7/work?cursor=invalid", "", "")
	require.Equal(t, 422, response.Code, response.Body.String())
}

func TestAgentDirectReaderRequiresCurrentHumanAuthority(t *testing.T) {
	m, _, _ := agentHarness(t)
	called := 0
	m.directReader.ChannelsOn = func(_ context.Context, _ *gorm.DB, tenant, agent, after guid.ID, limit int) ([]collab.ChannelView, error) {
		called++
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(7), agent)
		return []collab.ChannelView{{ID: 10, Name: "Direct"}, {ID: 11, Name: "Next"}}, nil
	}
	in := &AgentReadInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}, Limit: 1}
	page, err := m.agentDirectChannelsHTTP(agentContext(t, 2), in)
	require.NoError(t, err)
	require.True(t, page.Body.Data.HasMore)
	require.Equal(t, "10", page.Body.Data.NextCursor)
	require.Len(t, page.Body.Data.Items, 1)
	for _, actor := range []guid.ID{7, 8, 9} {
		_, err = m.agentDirectChannelsHTTP(agentContext(t, actor), in)
		require.Error(t, err)
	}
	require.Equal(t, 1, called)
	m.tenantPolicy = core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	})
	_, err = m.agentDirectChannelsHTTP(agentContext(t, 3), in)
	require.Error(t, err)
	require.Equal(t, 1, called)
	// Owner relation is current, independent of an unrelated administrator grant.
	_, err = m.agentDirectChannelsHTTP(agentContext(t, 2), in)
	require.NoError(t, err)
	in.Cursor = "bad"
	_, err = m.agentDirectChannelsHTTP(agentContext(t, 2), in)
	require.Error(t, err)
}

func TestAgentMessageReaderPaginationAndFailClosed(t *testing.T) {
	m, _, _ := agentHarness(t)
	in := &AgentDirectMessagesInput{AgentReadInput: AgentReadInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}, Limit: 1}, ChannelID: "20"}
	_, err := m.agentDirectMessagesHTTP(agentContext(t, 2), in)
	require.Error(t, err)
	m.directReader.MessagesOn = func(_ context.Context, _ *gorm.DB, tenant, agent, channel, before guid.ID, limit int) ([]collab.MessageView, error) {
		require.Equal(t, guid.ID(20), channel)
		return []collab.MessageView{{ID: 30, Body: "Latest"}, {ID: 29, Body: "Older"}}, nil
	}
	page, err := m.agentDirectMessagesHTTP(agentContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "30", page.Body.Data.NextCursor)
	require.Len(t, page.Body.Data.Items, 1)
	m.directReader.MessagesOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) ([]collab.MessageView, error) {
		return nil, collab.ErrNotFound
	}
	_, err = m.agentDirectMessagesHTTP(agentContext(t, 2), in)
	require.Error(t, err)
	in.ChannelID = "bad"
	_, err = m.agentDirectMessagesHTTP(agentContext(t, 2), in)
	require.Error(t, err)
}

func TestAgentInspectionProviderFailuresAreTranslatedWithoutContent(t *testing.T) {
	m, _, handler, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	for _, tc := range []struct {
		name   string
		cause  error
		status int
	}{
		{"invalid cursor", delivery.ErrInvalidArgument, 422},
		{"work scope denied", delivery.ErrWorkForbidden, 403},
		{"work unavailable", delivery.ErrUnavailable, 503},
		{"private provider failure", errors.New("private SQL connection details"), 500},
	} {
		t.Run("work/"+tc.name, func(t *testing.T) {
			m.selectedWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, int) (delivery.PersonalWorkPage, error) {
				return delivery.PersonalWorkPage{}, fmt.Errorf("reader: %w", tc.cause)
			}
			response := callAgent(t, handler, ctx, "GET", "/tenants/1/agents/7/work", "", "")
			require.Equal(t, tc.status, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), "private SQL")
		})
	}
	for _, tc := range []struct {
		name   string
		cause  error
		status int
	}{
		{"channel not visible", collab.ErrNotFound, 404},
		{"invalid position", collab.ErrInvalidArgument, 422},
		{"private provider failure", errors.New("private SQL connection details"), 500},
	} {
		t.Run("direct/"+tc.name, func(t *testing.T) {
			m.directReader.ChannelsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) ([]collab.ChannelView, error) {
				return []collab.ChannelView{{ID: 20, Name: "private channel content"}}, fmt.Errorf("reader: %w", tc.cause)
			}
			m.directReader.MessagesOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) ([]collab.MessageView, error) {
				return []collab.MessageView{{ID: 30, Body: "private message content"}}, fmt.Errorf("reader: %w", tc.cause)
			}
			for _, suffix := range []string{"/direct-channels", "/direct-channels/20/messages"} {
				response := callAgent(t, handler, ctx, "GET", "/tenants/1/agents/7"+suffix, "", "")
				require.Equal(t, tc.status, response.Code, response.Body.String())
				require.NotContains(t, response.Body.String(), "private")
			}
		})
	}
	// A missing provider must fail closed rather than report a successful empty collection.
	m.selectedWork.ListOn = nil
	m.directReader.ChannelsOn = nil
	m.directReader.MessagesOn = nil
	for _, suffix := range []string{"/work", "/direct-channels", "/direct-channels/20/messages"} {
		response := callAgent(t, handler, ctx, "GET", "/tenants/1/agents/7"+suffix, "", "")
		require.Equal(t, 503, response.Code, response.Body.String())
	}
}

func TestAgentInspectionRejectsInvalidScopeBeforeReading(t *testing.T) {
	m, _, _ := agentHarness(t)
	called := 0
	m.selectedWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, int) (delivery.PersonalWorkPage, error) {
		called++
		return delivery.PersonalWorkPage{}, nil
	}
	m.directReader.ChannelsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) ([]collab.ChannelView, error) {
		called++
		return nil, nil
	}
	m.directReader.MessagesOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) ([]collab.MessageView, error) {
		called++
		return nil, nil
	}
	assertStatus := func(err error, expected int) {
		t.Helper()
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Equal(t, expected, status.GetStatus())
	}
	for _, tc := range []struct {
		path   AgentPath
		status int
	}{
		{AgentPath{TenantID: "bad", AgentPrincipalID: "7"}, 422},
		{AgentPath{TenantID: "1", AgentPrincipalID: "0"}, 422},
		{AgentPath{TenantID: "2", AgentPrincipalID: "7"}, 403},
	} {
		in := AgentReadInput{AgentPath: tc.path, Limit: 1}
		_, err := m.selectedWorkHTTP(agentContext(t, 2), &in)
		assertStatus(err, tc.status)
		_, err = m.agentDirectChannelsHTTP(agentContext(t, 2), &in)
		assertStatus(err, tc.status)
		_, err = m.agentDirectMessagesHTTP(agentContext(t, 2), &AgentDirectMessagesInput{AgentReadInput: in, ChannelID: "20"})
		assertStatus(err, tc.status)
	}
	for _, tc := range []struct {
		cursor string
		limit  int
	}{
		{"bad", 1}, {"0", 1}, {"9223372036854775808", 1}, {"", 0}, {"", 101},
	} {
		in := AgentReadInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}, Cursor: tc.cursor, Limit: tc.limit}
		_, err := m.agentDirectChannelsHTTP(agentContext(t, 2), &in)
		assertStatus(err, 422)
		_, err = m.agentDirectMessagesHTTP(agentContext(t, 2), &AgentDirectMessagesInput{AgentReadInput: in, ChannelID: "20"})
		assertStatus(err, 422)
	}
	// Current membership, principal and owner/admin checks apply to work and messages too.
	m.tenantPolicy = core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	})
	in := AgentReadInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}, Limit: 1}
	for _, actor := range []guid.ID{3, 7, 8, 9} {
		_, err := m.selectedWorkHTTP(agentContext(t, actor), &in)
		assertStatus(err, 403)
		_, err = m.agentDirectMessagesHTTP(agentContext(t, actor), &AgentDirectMessagesInput{AgentReadInput: in, ChannelID: "20"})
		assertStatus(err, 403)
	}
	require.Zero(t, called, "no collection may be read before all input and current authority checks pass")
}

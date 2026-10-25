package agents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkspaceHTTPAccessAndMachineTransport(t *testing.T) {
	m, _, h, _ := agentHTTPHarness(t)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Equal(t, 200, callAgent(t, h, agentContext(t, 2), "GET", "/tenants/1/agents/7/workspace-access", "", "").Code)
	for _, actor := range []guid.ID{4, 7, 8, 9} {
		require.Equal(t, 403, callAgent(t, h, agentContext(t, actor), "GET", "/tenants/1/agents/7/workspace-access", "", "").Code)
	}
	m.workspaceMachine.BindingOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentWorkspaceFence, error) {
		return fleet.AgentWorkspaceFence{MachineID: 11, SessionID: 12, ConnEpoch: 3, ConfigRevision: 1}, nil
	}
	m.workspaceMachine.BindContext = func(ctx context.Context, id guid.ID) (context.Context, error) {
		if actorID(ctx) != 5 || id != 11 {
			return ctx, ErrForbidden
		}
		return ctx, nil
	}
	m.workspaceMachine.ValidateOn = func(_ context.Context, _ *gorm.DB, tenant, machine, session guid.ID, epoch int64) error {
		if tenant != 1 || machine != 11 || session != 12 || epoch != 3 {
			return ErrForbidden
		}
		return nil
	}
	type response struct {
		Data []workspaceCommand `json:"data"`
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- callAgent(t, h, agentContext(t, 2), "GET", "/tenants/1/agents/7/workspace-entries?path=notes&search=a&limit=1&offset=0", "", "")
	}()
	require.Eventually(t, func() bool { m.workspace.mu.Lock(); defer m.workspace.mu.Unlock(); return len(m.workspace.pending) > 0 }, time.Second, time.Millisecond)
	pull := callAgent(t, h, agentContext(t, 5), "POST", "/machines/11/agent-workspace-pulls", "", `{"session_id":"12","conn_epoch":3}`)
	require.Equal(t, 200, pull.Code, pull.Body.String())
	var command response
	require.NoError(t, json.Unmarshal(pull.Body.Bytes(), &command))
	require.Len(t, command.Data, 1)
	resultPath := "/machines/11/agent-workspace-reads/" + command.Data[0].ID.String() + "/result"
	stale := callAgent(t, h, agentContext(t, 5), "PUT", resultPath, "", `{"session_id":"12","conn_epoch":2,"items":[],"content":""}`)
	require.Equal(t, 403, stale.Code, stale.Body.String())
	result := callAgent(t, h, agentContext(t, 5), "PUT", resultPath, "", `{"session_id":"12","conn_epoch":3,"items":[{"name":"alpha","kind":"FILE","size":4},{"name":"beta","kind":"FILE","size":3}],"content":""}`)
	require.Equal(t, 200, result.Code, result.Body.String())
	page := <-done
	require.Equal(t, 200, page.Code, page.Body.String())
	require.Equal(t, "no-store", page.Header().Get("Cache-Control"))
	require.Contains(t, page.Body.String(), `"total":2`)
	require.Contains(t, page.Body.String(), `"has_more":true`)
	require.Equal(t, 422, callAgent(t, h, agentContext(t, 2), "GET", "/tenants/1/agents/7/workspace-entries?path=../host", "", "").Code)
}

func TestWorkspacePrivatePathValidation(t *testing.T) {
	for _, p := range []string{"", "notes/file.txt", "file.txt"} {
		require.True(t, validWorkspacePath(p), p)
	}
	for _, p := range []string{".", "..", "../secret", "/root", "C:/root", "a\\b", "a/../secret", "a//b", "nul\x00"} {
		require.False(t, validWorkspacePath(p), p)
	}
}
func TestWorkspaceReadRechecksAuthorizationAndFencesAndAuditsHashOnly(t *testing.T) {
	m, _, _, _ := agentHTTPHarness(t)
	fence := fleet.AgentWorkspaceFence{MachineID: 11, SessionID: 12, ConnEpoch: 3, ConfigRevision: 1}
	m.workspaceMachine.BindingOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentWorkspaceFence, error) {
		return fence, nil
	}
	in := &workspaceReadInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}, Path: "private.txt", Limit: 50}
	for _, scenario := range []string{"success", "binding_changed", "permission_revoked"} {
		t.Run(scenario, func(t *testing.T) {
			original := m.tenantPolicy
			defer func() { m.tenantPolicy = original }()
			actor := guid.ID(2)
			if scenario == "permission_revoked" {
				actor = 4
			}
			var recorded *audit.DomainEvent
			m.audit = func(_ context.Context, _ *gorm.DB, event audit.DomainEvent) error { recorded = &event; return nil }
			done := make(chan error, 1)
			go func() {
				result, e := m.workspaceRead(agentContext(t, actor), in, "CONTENT")
				if e == nil && result.Content != "actual" {
					done <- ErrInvalid
					return
				}
				done <- e
			}()
			var pending *workspacePending
			require.Eventually(t, func() bool {
				m.workspace.mu.Lock()
				defer m.workspace.mu.Unlock()
				for _, p := range m.workspace.pending {
					pending = p
					return true
				}
				return false
			}, time.Second, time.Millisecond)
			if scenario == "binding_changed" {
				fence.ConfigRevision++
			}
			if scenario == "permission_revoked" {
				m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			}
			pending.result <- WorkspaceReply{Content: "actual"}
			e := <-done
			if scenario == "success" {
				require.NoError(t, e)
				require.NotNil(t, recorded)
				require.Equal(t, "fleet.agent_workspace_read", recorded.EventType)
				require.Len(t, recorded.Detail["path_sha256"], 64)
				require.NotContains(t, recorded.Detail, "path")
				require.NotContains(t, recorded.Detail, "content")
			} else if scenario == "binding_changed" {
				require.ErrorIs(t, e, ErrConflict)
			} else {
				require.ErrorIs(t, e, ErrForbidden)
			}
		})
	}
}

func TestWorkspaceContentUsesCurrentMachineReplyAndExplicitReadFailures(t *testing.T) {
	m, _, _, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 2)
	m.workspaceMachine.BindingOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentWorkspaceFence, error) {
		return fleet.AgentWorkspaceFence{MachineID: 11, SessionID: 12, ConnEpoch: 3, ConfigRevision: 1}, nil
	}
	in := &workspaceReadInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}, Path: "notes/private.txt", Limit: 50}
	for _, reason := range []string{"", "INVALID_PATH", "TOO_LARGE", "NOT_FOUND", "UNAVAILABLE"} {
		t.Run(reason, func(t *testing.T) {
			delivered := make(chan bool, 1)
			go func() {
				delivered <- assert.Eventually(t, func() bool {
					m.workspace.mu.Lock()
					defer m.workspace.mu.Unlock()
					for _, p := range m.workspace.pending {
						p.result <- WorkspaceReply{Content: "actual machine bytes", Reason: reason}
						return true
					}
					return false
				}, time.Second, time.Millisecond)
			}()
			result, err := m.workspaceContentHTTP(ctx, in)
			require.True(t, <-delivered, "machine reply must reach the pending request")
			if reason == "" {
				require.NoError(t, err)
				require.Equal(t, "actual machine bytes", result.Body.Data.Content)
				require.Equal(t, in.Path, result.Body.Data.Path)
			} else {
				require.Error(t, err)
				require.Nil(t, result)
			}
			m.workspace.mu.Lock()
			require.Empty(t, m.workspace.pending)
			m.workspace.mu.Unlock()
		})
	}
	m.workspaceMachine.BindingOn = nil
	_, err := m.workspaceRead(ctx, in, "CONTENT")
	require.ErrorIs(t, err, ErrUnavailable)
	fault := errors.New("machine binding source failed")
	m.workspaceMachine.BindingOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentWorkspaceFence, error) {
		return fleet.AgentWorkspaceFence{}, fault
	}
	_, err = m.workspaceRead(ctx, in, "CONTENT")
	require.ErrorIs(t, err, fault)
	in.AgentPrincipalID = "bad"
	_, err = m.workspaceRead(ctx, in, "CONTENT")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestWorkspaceMachineRepliesRejectMalformedOversizedAndStaleResults(t *testing.T) {
	m, _, _, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 5)
	pull := &workspaceMachineInput{MachineID: "bad"}
	_, err := m.workspacePullHTTP(ctx, pull)
	require.Error(t, err)
	pull.MachineID = "11"
	_, err = m.workspacePullHTTP(ctx, pull)
	require.Error(t, err)
	result := &workspaceResultInput{MachineID: "bad", RequestID: "91"}
	_, err = m.workspaceResultHTTP(ctx, result)
	require.Error(t, err)
	result.MachineID = "11"
	result.RequestID = "bad"
	_, err = m.workspaceResultHTTP(ctx, result)
	require.Error(t, err)
	result.RequestID = "91"
	for _, reply := range []WorkspaceReply{{Content: strings.Repeat("x", 1048577)}, {Items: []WorkspaceEntry{{Name: "../private", Kind: "FILE"}}}, {Content: string([]byte{255})}} {
		result.Body.WorkspaceReply = reply
		_, err = m.workspaceResultHTTP(ctx, result)
		require.Error(t, err)
	}
	result.Body.WorkspaceReply = WorkspaceReply{}
	_, err = m.workspaceResultHTTP(ctx, result)
	require.Error(t, err)
	m.workspaceMachine.BindContext = func(c context.Context, _ guid.ID) (context.Context, error) { return c, fleet.ErrMachineOffline }
	_, err = m.workspacePullHTTP(ctx, pull)
	require.Error(t, err)
	_, err = m.workspaceResultHTTP(ctx, result)
	require.Error(t, err)
	m.workspaceMachine.BindContext = func(c context.Context, _ guid.ID) (context.Context, error) { return c, nil }
	_, err = m.workspacePullHTTP(ctx, pull)
	require.Error(t, err)
	_, err = m.workspaceResultHTTP(ctx, result)
	require.Error(t, err)
	m.workspaceMachine.ValidateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64) error { return nil }
	_, err = m.workspaceResultHTTP(ctx, result)
	require.Error(t, err)
	pending := &workspacePending{tenant: 1, result: make(chan WorkspaceReply, 1), command: workspaceCommand{ID: 91, AgentWorkspaceFence: fleet.AgentWorkspaceFence{MachineID: 11}, ExpiresAt: m.now().Add(time.Minute)}}
	m.workspace.pending = map[guid.ID]*workspacePending{91: pending}
	accepted, err := m.workspaceResultHTTP(ctx, result)
	require.NoError(t, err)
	require.True(t, accepted.Body.Data)
	_, err = m.workspaceResultHTTP(ctx, result)
	require.Error(t, err)
	require.Len(t, pending.result, 1)
}

package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestToolsCredentialsStaySealedAndGrantsStayScoped(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	m.credentialProof = func(context.Context, *gorm.DB, guid.ID, string, string) error { return nil }
	var tool ToolView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		tool, e = m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "team-docs", Kind: "MCP_SERVER", Title: "Team docs", Transport: "STREAMABLE_HTTP", Endpoint: "https://tools.example.invalid/mcp", Credential: "not-a-real-token", ReauthProof: "verified"})
		return e
	}))
	require.True(t, tool.HasCredential)
	encoded, e := json.Marshal(tool)
	require.NoError(t, e)
	require.NotContains(t, string(encoded), "not-a-real-token")
	require.NotContains(t, string(encoded), "credential_sealed")
	q := query.BindToolEntry(db, m.names)
	row, e := q.WithContext(ctx).First()
	require.NoError(t, e)
	require.NotEqual(t, "not-a-real-token", row.CredentialSealed)
	ring, e := m.keys()
	require.NoError(t, e)
	plain, e := ring.Open("tool_credential", []byte("1:"+tool.ID.String()), row.CredentialSealed)
	require.NoError(t, e)
	require.Equal(t, "not-a-real-token", string(plain))
	_, e = ring.Open("tool_credential", []byte("2:"+tool.ID.String()), row.CredentialSealed)
	require.Error(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, _, e := m.toolsOn(agentContext(t, 7), tx, 1, 7, 0, 50)
		require.NoError(t, e)
		require.Empty(t, rows)
		grant, e := m.grantOn(ctx, tx, 1, tool.ID, 7, "ACTIVE")
		require.NoError(t, e)
		require.Equal(t, 1, grant.Version)
		again, e := m.grantOn(ctx, tx, 1, tool.ID, 7, "ACTIVE")
		require.NoError(t, e)
		require.Equal(t, grant.ID, again.ID)
		rows, _, e = m.toolsOn(agentContext(t, 7), tx, 1, 7, 0, 50)
		require.NoError(t, e)
		require.Len(t, rows, 1)
		grant, e = m.grantOn(ctx, tx, 1, tool.ID, 7, "REVOKED")
		require.NoError(t, e)
		require.Equal(t, 2, grant.Version)
		rows, _, e = m.toolsOn(agentContext(t, 7), tx, 1, 7, 0, 50)
		require.NoError(t, e)
		require.Empty(t, rows)
		_, _, e = m.toolsOn(agentContext(t, 7), tx, 1, 0, 0, 50)
		require.ErrorIs(t, e, ErrForbidden)
		return nil
	}))
}
func TestToolValidationAndHumanOnlyCatalog(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	valid := ToolInput{EntryKey: "team-skill", Kind: "SKILL", Title: "Skill", SkillBody: "Use evidence and report unverified facts."}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.createToolOn(ctx, tx, 1, valid)
		require.NoError(t, e)
		require.Len(t, v.ContentHash, 64)
		for _, change := range []func(*ToolInput){func(v *ToolInput) { v.EntryKey = "../bad" }, func(v *ToolInput) { v.Kind = "OTHER" }, func(v *ToolInput) { v.SkillBody = "" }, func(v *ToolInput) { v.Transport = "STREAMABLE_HTTP" }, func(v *ToolInput) { v.Credential = "plaintext" }, func(v *ToolInput) { v.Title = " " }} {
			in := valid
			change(&in)
			_, e = m.createToolOn(ctx, tx, 1, in)
			require.ErrorIs(t, e, ErrInvalid)
		}
		_, e = m.createToolOn(agentContext(t, 7), tx, 1, valid)
		require.ErrorIs(t, e, ErrForbidden)
		for _, in := range []ToolInput{
			{EntryKey: "http-tool", Kind: "MCP_SERVER", Title: "HTTP", Transport: "STREAMABLE_HTTP", Endpoint: "http://example.invalid"},
			{EntryKey: "http-tool", Kind: "MCP_SERVER", Title: "HTTP", Transport: "STREAMABLE_HTTP", Endpoint: "https://user:secret@example.invalid"},
			{EntryKey: "http-tool", Kind: "MCP_SERVER", Title: "HTTP", Transport: "STREAMABLE_HTTP", Endpoint: "https://example.invalid?token=secret"},
			{EntryKey: "stdio-tool", Kind: "MCP_SERVER", Title: "Stdio", Transport: "STDIO_PACKAGE", Endpoint: "npm:tool@1", ContentHash: "bad"},
			{EntryKey: "stdio-tool", Kind: "MCP_SERVER", Title: "Stdio", Transport: "STDIO_PACKAGE", Endpoint: "npm:tool@1", ContentHash: strings.Repeat("z", 64)},
		} {
			_, e = m.createToolOn(ctx, tx, 1, in)
			require.ErrorIs(t, e, ErrInvalid)
		}
		stdio, e := m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "stdio-tool", Kind: "MCP_SERVER", Title: "Stdio", Transport: "STDIO_PACKAGE", Endpoint: "npm:tool@1.0.0", ContentHash: strings.Repeat("a", 64)})
		require.NoError(t, e)
		require.False(t, stdio.HasCredential)
		rows, more, e := m.toolsOn(ctx, tx, 1, 0, 0, 1)
		require.NoError(t, e)
		require.Len(t, rows, 1)
		require.True(t, more)
		rows, more, e = m.toolsOn(ctx, tx, 1, 0, rows[0].ID, 1)
		require.NoError(t, e)
		require.Len(t, rows, 1)
		require.False(t, more)
		return nil
	}))
}
func TestCredentialsRequireCurrentProofAndConfiguredKeyring(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	in := ToolInput{EntryKey: "protected-tool", Kind: "MCP_SERVER", Title: "Protected", Transport: "STREAMABLE_HTTP", Endpoint: "https://example.invalid", Credential: "not-real", ReauthProof: "proof"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.createToolOn(ctx, tx, 1, in)
		require.ErrorIs(t, e, ErrForbidden)
		return nil
	}))
	m.credentialProof = func(context.Context, *gorm.DB, guid.ID, string, string) error { return errors.New("invalid proof") }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.createToolOn(ctx, tx, 1, in)
		require.ErrorIs(t, e, ErrForbidden)
		return nil
	}))
	m.credentialProof = func(context.Context, *gorm.DB, guid.ID, string, string) error { return nil }
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.createToolOn(ctx, tx, 1, in)
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
	m.config.EncryptionKey = "invalid"
	_, e := m.keys()
	require.ErrorIs(t, e, ErrUnavailable)
	require.ErrorIs(t, m.bindCredentialProof(nil), ErrInvalid)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { require.ErrorIs(t, m.teamToolAuthority(ctx, tx, 1), ErrForbidden); return nil }))
	var count int64
	require.NoError(t, db.Table(m.names.ToolEntry.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestCatalogRejectsUnknownTargetsAndInvalidPages(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.grantOn(ctx, tx, 1, 0, 7, "ACTIVE")
		require.ErrorIs(t, e, ErrInvalid)
		_, e = m.grantOn(ctx, tx, 1, 1, 99, "ACTIVE")
		require.ErrorIs(t, e, ErrNotFound)
		_, _, e = m.toolsOn(ctx, tx, 1, 0, -1, 50)
		require.ErrorIs(t, e, ErrInvalid)
		_, _, e = m.toolsOn(ctx, tx, 1, 99, 0, 50)
		require.ErrorIs(t, e, ErrNotFound)
		_, e = m.inboxOn(agentContext(t, 7), tx, 1, 7, -1, 50)
		require.ErrorIs(t, e, ErrInvalid)
		m.tenantPolicy = nil
		_, _, e = m.toolsOn(ctx, tx, 1, 0, 0, 50)
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
}

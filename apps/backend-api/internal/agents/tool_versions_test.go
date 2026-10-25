package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestToolVersionsPublishRevokeFreezeAndArchive(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	var entry ToolView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		entry, e = m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "review-tools", Kind: "MCP_SERVER", Title: "Review", Transport: "STREAMABLE_HTTP", Endpoint: "https://one.example.invalid/mcp"})
		return e
	}))
	var first ToolVersionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		detail, e := m.toolDetailOn(ctx, tx, 1, entry.ID)
		require.NoError(t, e)
		require.Len(t, detail.Versions, 1)
		first = detail.Versions[0]
		require.Equal(t, 1, detail.CurrentVersionNo)
		_, e = m.publishToolOn(ctx, tx, 1, entry.ID, ToolPublishInput{Version: 1, Transport: first.Transport, Endpoint: first.Endpoint})
		require.ErrorIs(t, e, ErrConflict)
		second, e := m.publishToolOn(ctx, tx, 1, entry.ID, ToolPublishInput{Version: 1, Transport: first.Transport, Endpoint: "https://two.example.invalid/mcp", ChangeNote: "Review endpoint migration"})
		require.NoError(t, e)
		require.Equal(t, 2, second.VersionNo)
		require.NotEqual(t, first.ContentHash, second.ContentHash)
		detail, e = m.toolDetailOn(ctx, tx, 1, entry.ID)
		require.NoError(t, e)
		require.Equal(t, first, detail.Versions[1])
		require.Equal(t, 2, detail.CurrentVersionNo)
		require.Equal(t, 2, detail.Entry.Version)
		_, e = m.publishToolOn(ctx, tx, 1, entry.ID, ToolPublishInput{Version: 1, Transport: first.Transport, Endpoint: "https://stale.example.invalid"})
		require.ErrorIs(t, e, ErrConflict)
		_, e = m.changeToolStateOn(ctx, tx, 1, entry.ID, ToolStateChange{Version: 2, VersionNo: 2, VersionState: "REVOKED", Reason: "The new endpoint is withdrawn"})
		require.NoError(t, e)
		detail, e = m.toolDetailOn(ctx, tx, 1, entry.ID)
		require.NoError(t, e)
		require.Equal(t, 1, detail.CurrentVersionNo)
		require.Equal(t, first.Endpoint, detail.Entry.Endpoint)
		require.Equal(t, "REVOKED", detail.Versions[0].State)
		require.Equal(t, first, detail.Versions[1])
		_, e = m.changeToolStateOn(ctx, tx, 1, entry.ID, ToolStateChange{Version: 3, State: "ARCHIVED", Reason: "Archive directly while active"})
		require.ErrorIs(t, e, ErrConflict)
		_, e = m.changeToolStateOn(ctx, tx, 1, entry.ID, ToolStateChange{Version: 3, VersionNo: 1, VersionState: "REVOKED", Reason: "All available endpoints withdrawn"})
		require.NoError(t, e)
		_, e = m.changeToolStateOn(ctx, tx, 1, entry.ID, ToolStateChange{Version: 4, State: "ACTIVE"})
		require.ErrorIs(t, e, ErrConflict)
		_, e = m.changeToolStateOn(ctx, tx, 1, entry.ID, ToolStateChange{Version: 4, State: "ARCHIVED", Reason: "Retained for historical execution evidence"})
		require.NoError(t, e)
		_, e = m.publishToolOn(ctx, tx, 1, entry.ID, ToolPublishInput{Version: 5, Transport: first.Transport, Endpoint: "https://three.example.invalid"})
		require.ErrorIs(t, e, ErrConflict)
		_, e = m.changeToolStateOn(ctx, tx, 1, entry.ID, ToolStateChange{Version: 5, State: "ACTIVE"})
		require.ErrorIs(t, e, ErrConflict)
		return nil
	}))
}

func TestToolUpstreamReportsDoNotRepublishAndMutationRollsBack(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	var entry ToolView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		entry, e = m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "upstream-tool", Kind: "MCP_SERVER", Title: "Tool", Transport: "STREAMABLE_HTTP", Endpoint: "https://example.invalid/mcp"})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		detail, e := m.reportToolUpstreamOn(ctx, tx, 1, entry.ID, ToolUpstreamInput{Version: 1, ContentHash: strings.Repeat("b", 64), Note: "Observed upstream release published"})
		require.NoError(t, e)
		require.True(t, detail.UpstreamChanged)
		require.Len(t, detail.Upstream, 1)
		require.Len(t, detail.Versions, 1)
		require.Equal(t, entry.ContentHash, detail.Entry.ContentHash)
		_, e = m.reportToolUpstreamOn(agentContext(t, 7), tx, 1, entry.ID, ToolUpstreamInput{Version: 2, ContentHash: strings.Repeat("b", 64), Note: "Observed upstream release published"})
		require.ErrorIs(t, e, ErrForbidden)
		return nil
	}))
	m.events = badPublisher{}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.publishToolOn(ctx, tx, 1, entry.ID, ToolPublishInput{Version: 2, Transport: "STREAMABLE_HTTP", Endpoint: "https://changed.example.invalid"})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.toolDetailOn(ctx, tx, 1, entry.ID)
		require.NoError(t, e)
		require.Len(t, v.Versions, 1)
		require.Equal(t, 2, v.Entry.Version)
		require.Equal(t, entry.Endpoint, v.Entry.Endpoint)
		return nil
	}))
}

func TestToolPresetAndVerifiedMachineImportsStayDisabledAndUngrantable(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	m.toolSourceFacts = fleet.ToolSourceFacts{ReadOn: func(_ context.Context, _ *gorm.DB, tenant, machine guid.ID) (fleet.MachineCapabilities, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 55, machine)
		return fleet.MachineCapabilities{Discoveries: []fleet.InspectionView{{ScopeKey: "home", Discovery: []fleet.RuntimeDiscovery{{Runtime: "CODEX", State: "READY", MCP: []string{"detected-tool"}}}}}}, nil
	}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		preset, e := m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "preset-tool", Kind: "MCP_SERVER", Title: "Preset", PresetKey: "issue-tracker", Endpoint: "https://example.invalid/mcp"})
		require.NoError(t, e)
		require.Equal(t, "DISABLED", preset.State)
		_, e = m.grantOn(ctx, tx, 1, preset.ID, 7, "ACTIVE")
		require.Error(t, e)
		detail, e := m.toolDetailOn(ctx, tx, 1, preset.ID)
		require.NoError(t, e)
		require.Equal(t, "PRESET", detail.Versions[0].Source)
		require.Equal(t, "issue-tracker", detail.Versions[0].SourceRef)
		_, e = m.changeToolStateOn(ctx, tx, 1, preset.ID, ToolStateChange{Version: 1, State: "ACTIVE"})
		require.NoError(t, e)
		imported, e := m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "local-tool", Kind: "MCP_SERVER", Title: "Local", Transport: "STREAMABLE_HTTP", Endpoint: "https://local.example.invalid/mcp", FromLocal: &ToolLocalSource{MachineID: 55, Runtime: "CODEX", Name: "detected-tool", Scope: "home"}})
		require.NoError(t, e)
		require.Equal(t, "DISABLED", imported.State)
		detail, e = m.toolDetailOn(ctx, tx, 1, imported.ID)
		require.NoError(t, e)
		require.Equal(t, "MACHINE", detail.Versions[0].Source)
		require.Equal(t, "55:CODEX:home:detected-tool", detail.Versions[0].SourceRef)
		_, e = m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "unknown-tool", Kind: "MCP_SERVER", Title: "Unknown", Transport: "STREAMABLE_HTTP", Endpoint: "https://local.example.invalid/mcp", FromLocal: &ToolLocalSource{MachineID: 55, Runtime: "CODEX", Name: "made-up"}})
		require.ErrorIs(t, e, ErrNotFound)
		return nil
	}))
}

func TestToolVersionBoundariesAndDisabledGrantVisibility(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		entry, e := m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "skills-frozen", Kind: "SKILL", Title: "Skill", SkillBody: "Keep execution evidence pinned."})
		require.NoError(t, e)
		_, e = m.publishToolOn(ctx, tx, 1, entry.ID, ToolPublishInput{Version: 1, Transport: "STREAMABLE_HTTP", Endpoint: "https://example.invalid"})
		require.ErrorIs(t, e, ErrConflict)
		_, e = m.grantOn(ctx, tx, 1, entry.ID, 7, "ACTIVE")
		require.NoError(t, e)
		_, e = m.changeToolStateOn(ctx, tx, 1, entry.ID, ToolStateChange{Version: 1, State: "DISABLED", Reason: "Maintenance with recorded reason"})
		require.NoError(t, e)
		rows, _, e := m.toolsOn(agentContext(t, 7), tx, 1, 7, 0, 50)
		require.NoError(t, e)
		require.Empty(t, rows)
		q := query.BindToolGrant(tx, m.names)
		grants, e := q.WithContext(ctx).Where(q.ToolID.Eq(entry.ID)).Find()
		require.NoError(t, e)
		require.Len(t, grants, 1)
		require.Equal(t, "ACTIVE", grants[0].State)
		_, e = m.toolDetailOn(ctx, tx, 2, entry.ID)
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.changeToolStateOn(ctx, tx, 1, entry.ID, ToolStateChange{Version: 2, State: "ARCHIVED", Reason: "short"})
		require.ErrorIs(t, e, ErrInvalid)
		detail, e := m.toolDetailOn(ctx, tx, 1, entry.ID)
		require.NoError(t, e)
		encoded, e := json.Marshal(detail)
		require.NoError(t, e)
		require.NotContains(t, string(encoded), "credential_sealed")
		return nil
	}))
}

func TestToolVersionCommandsStorageAndAuditFailuresPreservePublishedRevision(t *testing.T) {
	for _, command := range []string{"publish", "state", "upstream"} {
		for _, failure := range []string{"query", "create", "update", "audit", "outbox", "ids", "zero-id"} {
			if command == "state" && (failure == "create" || failure == "ids" || failure == "zero-id") {
				continue
			}
			t.Run(command+"/"+failure, func(t *testing.T) {
				m, db, _ := agentHarness(t)
				ctx := agentContext(t, 2)
				var tool ToolView
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					tool, e = m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "version-storage", Kind: "MCP_SERVER", Title: "Original", Transport: "STREAMABLE_HTTP", Endpoint: "https://original.example.test"})
					return e
				}))
				fault := errors.New("tool persistence unavailable")
				originalAudit := m.audit
				originalEvents := m.events
				originalIDs := m.ids
				matches := func(tx *gorm.DB) bool {
					return tx.Statement.Table == m.names.ToolEntry.String() || tx.Statement.Table == m.names.ToolVersion.String() || tx.Statement.Table == m.names.ToolUpstream.String()
				}
				switch failure {
				case "query":
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("tool-storage", func(tx *gorm.DB) {
						if matches(tx) {
							tx.AddError(fault)
						}
					}))
					defer db.Callback().Query().Remove("tool-storage")
				case "create":
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("tool-storage", func(tx *gorm.DB) {
						if matches(tx) {
							tx.AddError(fault)
						}
					}))
					defer db.Callback().Create().Remove("tool-storage")
				case "update":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("tool-storage", func(tx *gorm.DB) {
						if matches(tx) {
							tx.AddError(fault)
						}
					}))
					defer db.Callback().Update().Remove("tool-storage")
				case "audit":
					m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
				case "outbox":
					m.events = badPublisher{}
				case "ids":
					m.ids = nil
				case "zero-id":
					m.ids = failedAgentIDs{id: 0}
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					switch command {
					case "publish":
						_, e := m.publishToolOn(ctx, tx, 1, tool.ID, ToolPublishInput{Version: 1, Transport: "STREAMABLE_HTTP", Endpoint: "https://replacement.example.test"})
						return e
					case "state":
						_, e := m.changeToolStateOn(ctx, tx, 1, tool.ID, ToolStateChange{Version: 1, State: "DISABLED"})
						return e
					default:
						_, e := m.reportToolUpstreamOn(ctx, tx, 1, tool.ID, ToolUpstreamInput{Version: 1, ContentHash: strings.Repeat("b", 64), Note: "Observed actual upstream change"})
						return e
					}
				})
				require.Error(t, err)
				if failure == "query" {
					require.NoError(t, db.Callback().Query().Remove("tool-storage"))
				}
				m.audit = originalAudit
				m.events = originalEvents
				m.ids = originalIDs
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					detail, e := m.toolDetailOn(ctx, tx, 1, tool.ID)
					require.NoError(t, e)
					require.Equal(t, 1, detail.Entry.Version)
					require.Equal(t, "ACTIVE", detail.Entry.State)
					require.Len(t, detail.Versions, 1)
					require.Empty(t, detail.Upstream)
					return nil
				}))
			})
		}
	}
}

func TestToolSourceAndVersionValidationRejectsUnreviewedConfiguration(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	for _, in := range []ToolInput{{PresetKey: "issue-tracker", FromLocal: &ToolLocalSource{}}, {PresetKey: "missing", Kind: "MCP_SERVER"}, {PresetKey: "issue-tracker", Kind: "SKILL"}, {Kind: "MCP_SERVER", FromLocal: &ToolLocalSource{MachineID: 55, Runtime: "UNKNOWN", Name: "source"}}, {Kind: "MCP_SERVER", FromLocal: &ToolLocalSource{MachineID: 55, Runtime: "CODEX", Name: "source"}}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, _, e := m.prepareToolSourceOn(ctx, tx, 1, &in); return e }))
	}
	var tool ToolView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		tool, e = m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "invalid-versions", Kind: "MCP_SERVER", Title: "Current", Transport: "STREAMABLE_HTTP", Endpoint: "https://tools.example.test"})
		return e
	}))
	for _, in := range []ToolPublishInput{{Version: 0}, {Version: 1, Transport: "UNKNOWN"}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.publishToolOn(ctx, tx, 1, tool.ID, in); return e }))
	}
	for _, in := range []ToolStateChange{{Version: 0}, {Version: 1, State: "UNKNOWN"}, {Version: 1, VersionNo: 1, VersionState: "REVOKED", Reason: "short"}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.changeToolStateOn(ctx, tx, 1, tool.ID, in); return e }))
	}
	for _, in := range []ToolUpstreamInput{{Version: 0}, {Version: 1, ContentHash: strings.Repeat("z", 64), Note: "Observed upstream release"}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.reportToolUpstreamOn(ctx, tx, 1, tool.ID, in); return e }))
	}
}

// Package knowledge owns scoped knowledge entries and human review of rules.
package knowledge

//go:generate go run ./dbgen

import (
	"context"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/infra/secrets"
	knowledgei18n "7link.com/platform/internal/knowledge/i18n"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
)

const Name = "knowledge"

type Config struct {
	module.Config
	Remote    RemoteConfig              `mapstructure:"remote"`
	Embedding EmbeddingDeploymentConfig `mapstructure:"embedding"`
}
type Module struct {
	runtime      *appenv.Runtime
	config       Config
	names        query.TableNames
	native       query.NativeNames
	ids          guid.Source
	principals   identity.PrincipalStateStore
	members      core.ActiveMemberChecker
	policy       core.EntityConstraintOn
	tenantPolicy core.TenantConstraintOn
	agents       fleet.AgentDirectory
	audit        audit.Appender
	events       events.Publisher
	replay       idempotency.Store
	queue        jobs.Queue
	usage        usageEvidenceBinding
	memory       memoryEvidenceBinding
	metrics      runtimeMetricBinding
	view         executionViewBinding
	injection    injectionBinding
	remote       *remoteConnector
	ingestion    eventSourceBinding
	archiveState projectArchiveState
	distillation distillationBinding
	embedding    embeddingBinding
	registrar    *authz.Registrar
	now          func() time.Time
}

func NewModule(rt *appenv.Runtime, config Config) *Module {
	return &Module{runtime: rt, config: config, names: query.BindNames(rt.TablePrefix()), native: query.BindNativeNames(rt.TablePrefix()), now: func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }}
}
func (*Module) ModuleName() string { return Name }

// Initialize loads translations before any HTTP router is published.
func (m *Module) Initialize(ctx context.Context) error {
	if err := m.initializeNativeSearch(ctx); err != nil {
		return err
	}
	if err := m.initializeRemote(); err != nil {
		return err
	}
	return knowledgei18n.Register()
}
func (m *Module) Enabled() bool { return m != nil && m.config.Enabled }
func (m *Module) ModuleDependencies() []string {
	if m.config.Embedding.Enabled {
		return []string{guid.Name, core.Name, identity.Name, fleet.Name, audit.Name, events.Name, idempotency.Name, jobs.Name, secrets.Name, dlock.Name, iamauthn.Name, platformi18n.Name}
	}
	return []string{guid.Name, core.Name, identity.Name, fleet.Name, audit.Name, events.Name, idempotency.Name, jobs.Name, platformi18n.Name}
}
func (m *Module) InjectServices(s *module.Services) error {
	if err := m.initializeEmbeddingServices(s); err != nil {
		return err
	}
	if err := m.registerEventIngestion(s); err != nil {
		return err
	}
	var err error
	if m.ids, err = module.Resolve[guid.Source](s); err != nil {
		return err
	}
	if m.principals, err = module.Resolve[identity.PrincipalStateStore](s); err != nil {
		return err
	}
	if m.members, err = module.Resolve[core.ActiveMemberChecker](s); err != nil {
		return err
	}
	if m.policy, err = module.Resolve[core.EntityConstraintOn](s); err != nil {
		return err
	}
	if m.tenantPolicy, err = module.Resolve[core.TenantConstraintOn](s); err != nil {
		return err
	}
	if m.agents, err = module.Resolve[fleet.AgentDirectory](s); err != nil {
		return err
	}
	if m.audit, err = module.Resolve[audit.Appender](s); err != nil {
		return err
	}
	if m.events, err = module.Resolve[events.Publisher](s); err != nil {
		return err
	}
	if m.replay, err = module.Resolve[idempotency.Store](s); err != nil {
		return err
	}
	if m.queue, err = module.Resolve[jobs.Queue](s); err != nil {
		return err
	}
	if m.registrar, err = module.Resolve[*authz.Registrar](s); err != nil {
		return err
	}
	if m.registrar == nil || m.registrar.Registry() == nil {
		return ErrUnavailable
	}
	if err = m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "knowledge", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read scoped knowledge"},
		{Resource: "knowledge", Verb: "write", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "write scoped knowledge"},
		{Resource: "knowledge", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "revoke scoped knowledge"},
		{Resource: "memory", Verb: "approve", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "review proposed rule memories"},
	}, nil); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	return catalog.AddTypes(Name, events.TypeSpec{Name: "knowledge.entry_written", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone}, events.TypeSpec{Name: "knowledge.memory_reviewed", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.skill_distillation_requested", SchemaVersion: 1, SubjectKind: "knowledge_skill_distillation", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.skill_distillation_completed", SchemaVersion: 1, SubjectKind: "knowledge_skill_distillation", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.skill_distillation_failed", SchemaVersion: 1, SubjectKind: "knowledge_skill_distillation", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.embedding_configured", SchemaVersion: 1, SubjectKind: "knowledge_embedding", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.searched", SchemaVersion: 1, SubjectKind: "knowledge_search", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.memory_written", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.memory_escalated", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.memory_approved", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.memory_rejected", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.memory_revoked", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.memory_expired", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.memory_edited", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.memory_promoted", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.memory_superseded", SchemaVersion: 1, SubjectKind: "knowledge_entry", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.skill_created", SchemaVersion: 1, SubjectKind: "knowledge_skill", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.skill_version_created", SchemaVersion: 1, SubjectKind: "knowledge_skill", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.skill_state_changed", SchemaVersion: 1, SubjectKind: "knowledge_skill", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.skill_mounted", SchemaVersion: 1, SubjectKind: "knowledge_skill", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.skill_unmounted", SchemaVersion: 1, SubjectKind: "knowledge_skill", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "skill_version.approved", SchemaVersion: 1, SubjectKind: "knowledge_skill", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "skill_version.rejected", SchemaVersion: 1, SubjectKind: "knowledge_skill", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.settings_updated", SchemaVersion: 1, SubjectKind: "knowledge_settings", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.source_bound", SchemaVersion: 1, SubjectKind: "knowledge_source", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.source_metadata_changed", SchemaVersion: 1, SubjectKind: "knowledge_source", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.source_archived", SchemaVersion: 1, SubjectKind: "knowledge_source", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.source_queued", SchemaVersion: 1, SubjectKind: "knowledge_source", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.source_failed", SchemaVersion: 1, SubjectKind: "knowledge_source", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.usage_recorded", SchemaVersion: 1, SubjectKind: "knowledge_usage", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.chunks_purged", SchemaVersion: 1, SubjectKind: "knowledge_source", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.injection_built", SchemaVersion: 1, SubjectKind: "knowledge_injection", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.injection_included", SchemaVersion: 1, SubjectKind: "knowledge_injection", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.injection_cited", SchemaVersion: 1, SubjectKind: "knowledge_injection", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.source_synced", SchemaVersion: 1, SubjectKind: "knowledge_source", Realtime: events.RealtimeNone},
		events.TypeSpec{Name: "knowledge.source_invalidated", SchemaVersion: 1, SubjectKind: "knowledge_source", Realtime: events.RealtimeNone})
}

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)

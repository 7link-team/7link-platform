package templates

//go:generate go run ./dbgen

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/infra/secrets"
	"7link.com/platform/internal/intake"
	templatesi18n "7link.com/platform/internal/templates/i18n"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"net/http"
)

const Name = "templates"

type Config struct{ module.Config }
type Module struct {
	verifierTools        fleet.VerifierTools
	evalAgents           fleet.VerifierAgents
	rubricOwner          core.EntityOwnerResolver
	rubricSubjects       delivery.RubricSubjects
	rubricProof          RubricProof
	videoSource          VideoSource
	teamWork             delivery.TeamWorkOn
	teamHuman            delivery.TeamHumanOn
	teamEvidenceClient   *http.Client
	providerAdapters     map[string]ProviderAdapter
	providerSealer       secrets.Sealer
	providerExecution    ProviderExecutionAuthority
	evalFiles            files.PinnedResources
	evalBindings         *files.ResourceBindings
	calibrationEvaluator CalibrationEvaluator
	calibrationSealed    bool
	loopCosts            RuntimeCosts
	escalate             governance.EscalationRecorder
	runtime              *appenv.Runtime
	config               Config
	names                query.TableNames
	ids                  guid.Source
	policy               core.TenantConstraintOn
	principals           identity.PrincipalStateStore
	scope                delivery.ProjectScope
	delivery             delivery.TemplateInstantiation
	gates                governance.WorkflowDecisions
	briefs               intake.SignedBriefs
	audit                audit.Appender
	events               events.Publisher
	idem                 idempotency.Store
	registrar            *authz.Registrar
}

func NewModule(rt *appenv.Runtime, c Config) *Module {
	return &Module{runtime: rt, config: c, names: query.BindNames(rt.TablePrefix())}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	// Secrets is optional: only provider credential operations require a sealer.
	return []string{fleet.Name, files.Name, jobs.Name, intake.Name, governance.Name, delivery.Name, core.Name, identity.Name, guid.Name, audit.Name, events.Name, idempotency.Name}
}
func (m *Module) InjectServices(s *module.Services) error {
	var toolsErr error
	m.verifierTools, toolsErr = module.Resolve[fleet.VerifierTools](s)
	if toolsErr != nil && !errors.Is(toolsErr, module.ErrServiceMissing) {
		return toolsErr
	}
	m.evalAgents, toolsErr = module.Resolve[fleet.VerifierAgents](s)
	if toolsErr != nil && !errors.Is(toolsErr, module.ErrServiceMissing) {
		return toolsErr
	}
	var teamErr error
	if m.teamWork, teamErr = module.Resolve[delivery.TeamWorkOn](s); teamErr != nil {
		return teamErr
	}
	if m.teamHuman, teamErr = module.Resolve[delivery.TeamHumanOn](s); teamErr != nil {
		return teamErr
	}
	if err := m.injectProviders(s); err != nil {
		return err
	}
	if err := m.injectEval(s); err != nil {
		return err
	}
	var e error
	if m.rubricOwner, e = module.Resolve[core.EntityOwnerResolver](s); e != nil {
		return e
	}
	if m.rubricSubjects, e = module.Resolve[delivery.RubricSubjects](s); e != nil {
		return e
	}
	if m.escalate, e = module.Resolve[governance.EscalationRecorder](s); e != nil {
		return e
	}
	if m.briefs, e = module.Resolve[intake.SignedBriefs](s); e != nil {
		return e
	}
	if m.gates, e = module.Resolve[governance.WorkflowDecisions](s); e != nil {
		return e
	}
	if m.ids, e = module.Resolve[guid.Source](s); e != nil {
		return e
	}
	if m.policy, e = module.Resolve[core.TenantConstraintOn](s); e != nil {
		return e
	}
	if m.principals, e = module.Resolve[identity.PrincipalStateStore](s); e != nil {
		return e
	}
	if m.scope, e = module.Resolve[delivery.ProjectScope](s); e != nil {
		return e
	}
	if m.delivery, e = module.Resolve[delivery.TemplateInstantiation](s); e != nil {
		return e
	}
	if m.audit, e = module.Resolve[audit.Appender](s); e != nil {
		return e
	}
	if m.events, e = module.Resolve[events.Publisher](s); e != nil {
		return e
	}
	if m.idem, e = module.Resolve[idempotency.Store](s); e != nil {
		return e
	}
	if m.registrar, e = module.Resolve[*authz.Registrar](s); e != nil {
		return e
	}
	if e = m.registrar.Registry().Contribute([]authz.Action{{Resource: "template", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Read workflow templates"}, {Resource: "template", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "Manage workflow templates"}}, nil); e != nil {
		return e
	}
	catalog, e := module.Resolve[events.Catalog](s)
	if e != nil {
		return e
	}
	if e = catalog.AddTypes(Name, events.TypeSpec{Name: "template.loop_escalated", SchemaVersion: 1, SubjectKind: "workflow_binding", Realtime: events.RealtimeSignal}); e != nil {
		return e
	}
	for _, name := range []string{"template.binding_changed", "template.overrides_approved"} {
		if e = catalog.AddTypes(Name, events.TypeSpec{Name: name, SchemaVersion: 1, SubjectKind: "workflow_binding", Realtime: events.RealtimeSignal}); e != nil {
			return e
		}
	}
	if e = catalog.AddTypes(Name, events.TypeSpec{Name: "template.studio_changed", SchemaVersion: 1, SubjectKind: "studio_record", Realtime: events.RealtimeSignal}); e != nil {
		return e
	}
	for _, name := range []string{"verifier.calibration_changed", "provider.state_changed", "eval.run_changed", "eval.run_completed", "template.eval_case_changed", "rubric.score_recorded", "rubric.scores_compared", "rubric.divergence_detected", "verifier.tool_availability_changed"} {
		if e = catalog.AddTypes(Name, events.TypeSpec{Name: name, SchemaVersion: 1, SubjectKind: "template_lifecycle", Realtime: events.RealtimeSignal}); e != nil {
			return e
		}
	}
	for _, name := range []string{"template.workflow_proposed", "template.drafted", "template.patched", "template.published", "template.instantiated", "template.advanced", "template.copied", "template.version_deprecated"} {
		if e = catalog.AddTypes(Name, events.TypeSpec{Name: name, SchemaVersion: 1, SubjectKind: "template_version", Realtime: events.RealtimeSignal}); e != nil {
			return e
		}
	}
	for _, name := range []string{"template.calibration_sample_added", "template.calibration_started", "template.calibration_confirmed", "template.calibration_evaluated", "template.calibration_drifted"} {
		if e = catalog.AddTypes(Name, events.TypeSpec{Name: name, SchemaVersion: 1, SubjectKind: "verifier_calibration", Realtime: events.RealtimeSignal}); e != nil {
			return e
		}
	}
	for _, name := range []string{"template.provider_created", "template.provider_changed", "template.provider_health_checked", "template.provider_binding_changed", "template.provider_generation_requested", "template.provider_generation_changed"} {
		subject := "template_provider"
		if name == "template.provider_binding_changed" {
			subject = "template_provider_binding"
		} else if name == "template.provider_generation_requested" || name == "template.provider_generation_changed" {
			subject = "template_provider_generation"
		}
		if e = catalog.AddTypes(Name, events.TypeSpec{Name: name, SchemaVersion: 1, SubjectKind: subject, Realtime: events.RealtimeSignal}); e != nil {
			return e
		}
	}
	return nil
}
func (m *Module) Initialize(context.Context) error {
	if m.calibrationEvaluator == nil {
		m.calibrationEvaluator = m.evaluateInstalledVerifier
	}
	m.calibrationSealed = true
	if m.teamWork == nil || m.teamHuman == nil || m.ids == nil || m.policy == nil || m.principals.Lock == nil || m.scope.AuthorizeOn == nil || m.delivery.CreateWorkOn == nil || m.delivery.InitializeConstitutionOn == nil || m.delivery.WorkStatusOn == nil || m.audit == nil || m.events == nil || m.idem == nil {
		return ErrUnavailable
	}
	if err := m.initializeEval(); err != nil {
		return err
	}
	return templatesi18n.Register()
}

var (
	_ module.Depender                = (*Module)(nil)
	_ module.Enabler                 = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.Starter                 = (*Module)(nil)
	_ module.HTTPRegistrar           = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)

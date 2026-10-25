package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"slices"
)

func (m *Module) bindAutomation(s *module.Services) error {
	if err := m.bindDispatch(s); err != nil {
		return err
	}
	connector, err := module.Resolve[delivery.AutomationTestRunWorkConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil // Minimal compositions do not run the automation dispatcher.
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	return connector.Bind(m.automationTestRunWorkOn)
}

func (m *Module) bindDispatch(s *module.Services) error {
	connector, err := module.Resolve[delivery.AutomationDispatchConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil || connector.WorkOn == nil || connector.ApplyOn == nil {
		return ErrUnavailable
	}
	return connector.Bind(func(ctx context.Context, tx *gorm.DB, tenant guid.ID) ([]guid.ID, error) {
		if m.dispatchFleet.PrincipalsOn == nil {
			return nil, ErrUnavailable
		}
		return m.dispatchFleet.PrincipalsOn(ctx, tx, tenant)
	}, func(ctx context.Context, tx *gorm.DB, in delivery.AutomationDispatch) error {
		return m.dispatchAutomationOn(ctx, tx, in, connector)
	})
}

// Reuse the canonical proposal, scoring, expiry, decision and CAS commands.
// Only their authorization/read and write adapters differ: the rule's current
// creator authorizes reads/application; the stored actor is always SYSTEM.
func (m *Module) dispatchAutomationOn(ctx context.Context, tx *gorm.DB, in delivery.AutomationDispatch, connector delivery.AutomationDispatchConnector) error {
	if tx == nil || tx.Statement == nil || authn.PrincipalIDFromContext(ctx) != 0 || authn.TenantIDFromContext(ctx) != in.TenantID || in.Role != "EXECUTOR" || in.CreatorID <= 0 || in.RuleID <= 0 || in.RunID <= 0 || in.RunAs != "SYSTEM:automation:"+in.RuleID.String() {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	if connector.WorkOn == nil || connector.ApplyOn == nil || m.audit == nil || m.events == nil || m.dispatchFleet.ListOn == nil {
		return ErrUnavailable
	}
	local := *m
	local.dispatch.AutoWorkOn = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID) (delivery.DispatchWorkFacts, error) {
		return connector.WorkOn(ctx, tx, in)
	}
	local.dispatch.AutoAuthorityOn = func(_ context.Context, tx *gorm.DB, _, _, _, selected guid.ID) (string, error) {
		if selected > 0 && !slices.Contains(in.LockedPrincipals, selected) {
			return "", ErrConflict
		}
		if _, err := connector.WorkOn(ctx, tx, in); err != nil {
			return "", err
		}
		return in.RunAs, nil
	}
	local.dispatch.ApplyOn = func(_ context.Context, tx *gorm.DB, _, _ guid.ID, assignment delivery.DispatchAssignmentInput) (guid.ID, error) {
		return connector.ApplyOn(ctx, tx, delivery.AutomationAssignment{AutomationTransition: in.AutomationTransition, AssigneePrincipalID: assignment.AssigneePrincipalID}, assignment.ProposalID, assignment.DecisionID)
	}
	local.audit = func(_ context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		event.PrincipalID = 0
		if event.Detail == nil {
			event.Detail = map[string]any{}
		}
		event.Detail["actor_kind"], event.Detail["authority_source"] = "SYSTEM", in.RunAs
		event.Detail["rule_id"], event.Detail["run_id"], event.Detail["creator_id"] = in.RuleID, in.RunID, in.CreatorID
		return m.audit(ctx, tx, event)
	}
	local.events = dispatchPublisher{publisher: m.events, change: in}
	// The canonical AUTO snapshot requires every current roster member to have
	// been discovered before project locks. A changing roster fails closed.
	commandCtx := context.WithValue(ctx, dispatchAutoKey{}, &dispatchAutoState{principals: slices.Clone(in.LockedPrincipals)})
	facts, err := connector.WorkOn(ctx, tx, in)
	if err != nil {
		return err
	}
	created, err := local.createDispatchOn(commandCtx, tx, in.ProjectID, DispatchBody{WorkItemID: in.WorkItemID, ExpectedWorkVersion: facts.WorkVersion, Trigger: "READY"})
	if err != nil {
		return err
	}
	row, err := local.dispatchOn(commandCtx, tx, in.ProjectID, created.ID)
	if err != nil {
		return err
	}
	view, err := dispatchView(row)
	if err != nil {
		return err
	}
	if view.Snapshot.RequiresHuman || row.RecommendedID == nil {
		return nil
	}
	_, err = local.decideDispatchOn(commandCtx, tx, in.ProjectID, row.ID, row.Version, DispatchDecision{Decision: "AUTO_APPLY", Reason: "Automation: deterministic eligible candidate under current project policy"})
	return err
}

type dispatchPublisher struct {
	publisher events.Publisher
	change    delivery.AutomationDispatch
}

func (p dispatchPublisher) Publish(ctx context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
	event.Actor = events.Actor{Kind: "SYSTEM", AuthoritySource: p.change.RunAs}
	data, ok := event.Data.(map[string]any)
	if !ok {
		data = map[string]any{}
	}
	data["rule_id"], data["run_id"], data["creator_id"] = p.change.RuleID, p.change.RunID, p.change.CreatorID
	event.Data = data
	return p.publisher.Publish(ctx, tx, event)
}

// Resolve a recorded test run through its frozen plan, not the editable case or
// event payload. This is a lock hint only; the dispatcher reauthorizes the work.
func (m *Module) automationTestRunWorkOn(ctx context.Context, tx *gorm.DB, source events.RecordedEvent) (guid.ID, error) {
	if tx == nil || tx.Statement == nil || source.TenantID <= 0 || source.ProjectID <= 0 || source.SubjectID <= 0 {
		return 0, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalid
	}
	if source.Type != "clientwork.test_run_recorded" || source.SubjectKind != "clientwork" {
		return 0, nil
	}
	r := query.BindTestRun(tx, m.names)
	run, err := r.WithContext(ctx).Where(r.TenantID.Eq(source.TenantID), r.ProjectID.Eq(source.ProjectID), r.ID.Eq(source.SubjectID)).First()
	if err != nil {
		return 0, err
	}
	p := query.BindTestPlan(tx, m.names)
	plan, err := p.WithContext(ctx).Where(p.TenantID.Eq(source.TenantID), p.ProjectID.Eq(source.ProjectID), p.ID.Eq(run.PlanID)).First()
	if err != nil {
		return 0, err
	}
	if plan.State != "FROZEN" {
		return 0, ErrConflict
	}
	snapshot, err := planView(plan)
	if err != nil {
		return 0, err
	}
	var work guid.ID
	for _, item := range snapshot.Cases {
		if item.ID == run.CaseID && item.Version == run.CaseVersion {
			if work != 0 || item.WorkItemID <= 0 {
				return 0, ErrConflict
			}
			work = item.WorkItemID
		}
	}
	if work == 0 {
		return 0, ErrConflict
	}
	return work, nil
}

// The automation bridge adds only the general source disposition adapter; structured REWORK
// disposition remains with its existing owner and command.
func (m *Module) registerAutomationBridgeHTTP(api huma.API) error {
	if err := m.registerDispositionHTTP(api); err != nil {
		return err
	}
	for _, item := range []struct{ action, path, verb string }{{"merge", "/merge", "convert"}, {"decline", "/decline", "decline"}} {
		action := item.action
		path := entityBase + "/intakes/{id}" + item.path
		authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake." + action, Method: "POST", Path: path, Summary: "Record a reasoned Intake source " + action + " without claiming external delivery", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "intake", Verb: item.verb}, func(ctx context.Context, in *EditInput[intake.SourceDisposition]) (*CommandOutput, error) {
			return m.disposeHTTP(ctx, in, action)
		})
	}
	return nil
}

func (m *Module) disposeHTTP(ctx context.Context, in *EditInput[intake.SourceDisposition], action string) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	permission, expected := "intake_convert", "MERGE"
	if action == "decline" {
		permission, expected = "intake_decline", "DECLINE"
	}
	if in.Body.Action != expected {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.intake."+action, "POST", entityBase+"/intakes/"+id.String()+"/"+action, in.Key, 200, in, permission, false, nil, func(tx *gorm.DB) (Receipt, error) {
		if m.intakeSources.DisposeOn == nil {
			return Receipt{}, ErrUnavailable
		}
		row, e := m.intakeSources.DisposeOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id, v, in.Body)
		return Receipt{ID: row.ID, Version: row.Version, State: row.State}, e
	})
}

func (m *Module) registerAutomationBridge() error {
	return m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "intake", Verb: "convert", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "merge authorized project Intake sources"},
		{Resource: "intake", Verb: "decline", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "decline project Intake as a Human"},
	}, nil)
}

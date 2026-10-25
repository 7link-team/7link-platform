package clientwork

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

type dispatchAutoKey struct{}
type dispatchAutoState struct{ principals []guid.ID }

func isDispatchAuto(ctx context.Context) bool {
	auto, _ := ctx.Value(dispatchAutoKey{}).(*dispatchAutoState)
	return auto != nil
}

type DispatchAutoBody struct {
	WorkItemID          guid.ID `json:"work_item_id"`
	ExpectedWorkVersion int     `json:"expected_work_version" minimum:"1"`
}
type DispatchAutoInput CreateInput[DispatchAutoBody]

// autoDispatchHTTP is invoked with the current lead Agent's own credential.
// It records a guarded proposal when Human review is required, and otherwise
// applies the deterministic top candidate in the same idempotent transaction.
func (m *Module) autoDispatchHTTP(ctx context.Context, in *DispatchAutoInput) (*CommandOutput, error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil || in.Body.WorkItemID <= 0 || in.Body.ExpectedWorkVersion < 1 {
		return nil, httpError(ctx, ErrInvalid)
	}
	ctx = context.WithValue(ctx, dispatchAutoKey{}, &dispatchAutoState{})
	return m.command(ctx, p, "clientwork.dispatch.auto", "POST", entityBase+"/dispatch-proposals/auto", in.Key, 201, in, "work_item_read", false, nil, func(tx *gorm.DB) (Receipt, error) {
		created, err := m.createDispatchOn(ctx, tx, p, DispatchBody{WorkItemID: in.Body.WorkItemID, ExpectedWorkVersion: in.Body.ExpectedWorkVersion, Trigger: "READY"})
		if err != nil {
			return Receipt{}, err
		}
		row, err := m.dispatchOn(ctx, tx, p, created.ID)
		if err != nil {
			return Receipt{}, err
		}
		var snapshot DispatchSnapshot
		if err = json.Unmarshal([]byte(row.Snapshot), &snapshot); err != nil {
			return Receipt{}, err
		}
		if snapshot.RequiresHuman || row.RecommendedID == nil {
			return created, nil
		}
		return m.decideDispatchOn(ctx, tx, p, row.ID, row.Version, DispatchDecision{Decision: "AUTO_APPLY", Reason: "Deterministic top eligible candidate; measured history or disclosed priors and cost availability frozen in proposal inputs"})
	})
}

func (m *Module) dispatchEventOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, kind string, version int) error {
	if !isDispatchAuto(ctx) {
		return m.event(ctx, tx, project, id, kind, "work_item_assign", version)
	}
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	source := "POLICY:dispatch_auto"
	if kind == "dispatch_proposed" {
		source = "permission:work_item_read"
	}
	a := audit.NewDomainEvent(ctx, tenant, "clientwork."+kind, "clientwork", id)
	a.Detail["project_id"], a.Detail["version"], a.Detail["authority_source"] = project.String(), version, source
	if err := m.audit(ctx, tx, a); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "clientwork." + kind, Actor: events.Actor{Kind: "AGENT", ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "clientwork", ID: id}, SubjectVersion: int64(version), Data: map[string]any{"version": version}})
	return err
}

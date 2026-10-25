package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"slices"
)

type ReworkDispositionBody struct {
	ReceiptID          guid.ID                     `json:"receipt_id"`
	ContentHash        string                      `json:"source_hash" pattern:"^[a-f0-9]{64}$"`
	Items              []ReworkDispositionItem     `json:"items" minItems:"1" maxItems:"100"`
	Draft              *intake.TriageDraft         `json:"requirement_draft,omitempty"`
	RequirementID      guid.ID                     `json:"requirement_id,omitempty"`
	RequirementVersion int                         `json:"requirement_version,omitempty"`
	Append             *delivery.ClientScopeChange `json:"append,omitempty"`
}
type ReworkDispositionItem struct {
	ItemID        string `json:"item_id" minLength:"1" maxLength:"128"`
	Mode          string `json:"mode" enum:"REOPEN_WORK_ITEMS,APPEND_TO_REQUIREMENT,NEW_REQUIREMENT,DECLINE"`
	WorkVersion   int    `json:"work_version,omitempty"`
	Reason        string `json:"reason" minLength:"1" maxLength:"2000"`
	ClientMessage string `json:"client_message" minLength:"1" maxLength:"2000"`
}
type ReworkDispositionInput struct {
	EditInput[ReworkDispositionBody]
	Cookie string `header:"Cookie"`
}

const reworkDispositionPath = entityBase + "/intakes/{id}/dispositions"

func (m *Module) registerReworkDispositionHTTP(api huma.API) error {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.rework_disposition", Method: "POST", Path: reworkDispositionPath, Summary: "Dispose an exact rejected subset with current Human authority", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "decision", Verb: "request_changes"}, m.reworkDispositionHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, reworkDispositionPath)
}
func (m *Module) reworkDispositionHTTP(ctx context.Context, in *ReworkDispositionInput) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return m.command(ctx, p, "clientwork.intake.rework_disposition", "POST", entityBase+"/intakes/"+id.String()+"/dispositions", in.Key, 201, in, "decision_request_changes", false, func(tx *gorm.DB) error {
		if m.reworkDisposition.AuthorizeOn == nil {
			return ErrUnavailable
		}
		return m.reworkDisposition.AuthorizeOn(ctx, tx, authn.TenantIDFromContext(ctx), p)
	}, func(tx *gorm.DB) (Receipt, error) { return m.disposeReworkOn(ctx, tx, p, id, v, in) })
}
func (m *Module) disposeReworkOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in *ReworkDispositionInput) (Receipt, error) {
	b := in.Body
	tenant := authn.TenantIDFromContext(ctx)
	if len(b.Items) < 1 || len(b.Items) > 100 || b.ReceiptID <= 0 || len(b.ContentHash) != 64 {
		return Receipt{}, ErrInvalid
	}
	if m.reworkSources.ReadOn == nil || m.reworkSources.AdvanceOn == nil || m.audit == nil || m.events == nil {
		return Receipt{}, ErrUnavailable
	}
	source, found, err := m.reworkSources.ReadOn(ctx, tx, tenant, p, b.ReceiptID)
	if err != nil {
		return Receipt{}, err
	}
	if !found || source.ID != id || source.Version != v || source.ContentHash != b.ContentHash || !slices.Contains([]string{"RECEIVED", "TRIAGED", "CONVERTED"}, source.State) {
		return Receipt{}, ErrConflict
	}
	rejected := map[string]intake.ReworkItem{}
	for _, item := range source.Rejection.Items {
		rejected[item.ItemID] = item
	}
	q := query.BindReworkDisposition(tx, m.names)
	prior, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.IntakeID.Eq(id)).Find()
	if err != nil {
		return Receipt{}, err
	}
	disposed := map[string]bool{}
	modes := map[string]bool{}
	for _, item := range prior {
		disposed[item.ItemID] = true
		modes[item.ItemID+":"+item.Mode] = true
	}
	// Validate the complete subset before any canonical command can take effect.
	seen := map[string]bool{}
	reopened := map[guid.ID]ReworkDispositionItem{}
	reopenedFacts := map[guid.ID]intake.ReworkItem{}
	for _, item := range b.Items {
		fact, ok := rejected[item.ItemID]
		if !ok || seen[item.ItemID] || modes[item.ItemID+":"+item.Mode] || (disposed[item.ItemID] && item.Mode != "APPEND_TO_REQUIREMENT") || !validText(item.Reason, 2000) || !validText(item.ClientMessage, 2000) || !slices.Contains([]string{"REOPEN_WORK_ITEMS", "APPEND_TO_REQUIREMENT", "NEW_REQUIREMENT", "DECLINE"}, item.Mode) {
			return Receipt{}, ErrInvalid
		}
		seen[item.ItemID] = true
		if item.Mode == "REOPEN_WORK_ITEMS" {
			previous, exists := reopened[fact.WorkItemID]
			previousFact := reopenedFacts[fact.WorkItemID]
			if fact.WorkItemID <= 0 || fact.ResultID <= 0 || fact.EvidenceID <= 0 || item.WorkVersion < 1 || (exists && (previous.WorkVersion != item.WorkVersion || previousFact.ResultID != fact.ResultID || previousFact.RequirementID != fact.RequirementID)) {
				return Receipt{}, ErrInvalid
			}
			reopened[fact.WorkItemID] = item
			reopenedFacts[fact.WorkItemID] = fact
		}
		if item.Mode == "NEW_REQUIREMENT" && b.Draft == nil {
			return Receipt{}, ErrInvalid
		}
		if item.Mode == "APPEND_TO_REQUIREMENT" && (b.Append == nil || b.RequirementID <= 0 || b.RequirementVersion < 1) {
			return Receipt{}, ErrInvalid
		}
	}
	targets := map[string]guid.ID{}
	applied := map[guid.ID]bool{}
	for _, item := range b.Items {
		fact := rejected[item.ItemID]
		target := guid.ID(0)
		switch item.Mode {
		case "REOPEN_WORK_ITEMS":
			if m.reworkDisposition.ReopenOn == nil {
				return Receipt{}, ErrUnavailable
			}
			if !applied[fact.WorkItemID] {
				err = m.reworkDisposition.ReopenOn(ctx, tx, tenant, p, fact, item.WorkVersion, item.Reason)
				applied[fact.WorkItemID] = true
			}
			target = fact.WorkItemID
		case "NEW_REQUIREMENT":
			if m.reworkDisposition.NewRequirementOn == nil {
				return Receipt{}, ErrUnavailable
			}
			target = targets[item.Mode]
			if target == 0 {
				target, err = m.reworkDisposition.NewRequirementOn(ctx, tx, tenant, p, id, *b.Draft)
				targets[item.Mode] = target
			}
		case "APPEND_TO_REQUIREMENT":
			if m.projects == nil {
				return Receipt{}, ErrUnavailable
			}
			target = targets[item.Mode]
			if target == 0 {
				target, err = m.projects.ApplyClientChangeOn(ctx, tx, tenant, p, b.RequirementID, b.RequirementVersion, in.Cookie, *b.Append)
				targets[item.Mode] = target
			}
		}
		if err != nil {
			return Receipt{}, err
		}
		next, err := m.nextID()
		if err != nil {
			return Receipt{}, err
		}
		row := &model.ReworkDisposition{ID: next, TenantID: tenant, ProjectID: p, IntakeID: id, ReceiptID: b.ReceiptID, ItemID: item.ItemID, Mode: item.Mode, Reason: item.Reason, ClientMessage: item.ClientMessage, ActorID: authn.PrincipalIDFromContext(ctx), CreatedAt: now()}
		if target > 0 {
			row.TargetID = &target
		}
		if err = q.WithContext(ctx).Create(row); err != nil {
			return Receipt{}, err
		}
	}
	for _, item := range b.Items {
		disposed[item.ItemID] = true
	}
	complete := len(disposed) == len(rejected)
	version, err := m.reworkSources.AdvanceOn(ctx, tx, tenant, p, id, v, complete)
	if err != nil {
		return Receipt{}, err
	}
	detail := map[string]any{"receipt_id": b.ReceiptID.String(), "source_hash": b.ContentHash, "item_count": len(b.Items), "authority_source": "permission:decision_request_changes"}
	event := audit.NewDomainEvent(ctx, tenant, "clientwork.rework_disposed", Name, id)
	event.Detail = detail
	if err = m.audit(ctx, tx, event); err != nil {
		return Receipt{}, err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: p, Type: "clientwork.rework_disposed", Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "permission:decision_request_changes"}, Subject: events.Subject{Kind: Name, ID: id}, SubjectVersion: int64(version), Data: detail})
	state := source.State
	if complete {
		state = "CONVERTED"
	}
	return Receipt{ID: id, Version: version, State: state}, err
}

package delivery

import (
	"encoding/json"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
)

// projectDraft assembles a validated, defaulted H1 request after authorization
// and reference checks. Request defaults belong to the Huma tags; this function
// only supplies server-owned initial state. It performs no persistence.
func (m *Module) projectDraft(tenant guid.ID, input projectCreateBody, members []projectMember, actor events.Actor, audit collab.ProvisioningAudit, now time.Time) (projectDraft, error) {
	if m == nil || m.ids == nil {
		return projectDraft{}, ErrUnavailable
	}
	if tenant <= 0 || actor.Kind != "HUMAN" || actor.ID <= 0 || actor.AuthoritySource == "" || audit.AuthoritySource != actor.AuthoritySource || now.IsZero() {
		return projectDraft{}, ErrInvalidArgument
	}
	portal := input.PortalSettings
	if portal.ShowAILabel == nil || portal.ShowWorkItemTitles == nil || portal.ShowEvidenceSummary == nil || portal.ShowReleaseNotes == nil || portal.MilestonesVisible == nil {
		return projectDraft{}, ErrInvalidArgument
	}
	portalJSON, err := json.Marshal(portal)
	if err != nil {
		return projectDraft{}, err
	}
	now = now.UTC().Truncate(time.Millisecond)
	actorID := actor.ID
	draft := projectDraft{
		entity: core.Entity{TenantID: tenant, Type: "project", Name: input.Name, Status: core.EntityActive, Metadata: map[string]any{"module": Name}},
		client: input.Client,
		project: model.Project{
			TenantID: tenant, Code: input.Code, Description: input.Description, OriginIntakeID: input.OriginIntakeID,
			Stage: "INTAKE", StageChangedAt: now, TeamMode: "HYBRID", DispatchPolicy: "MANUAL", EnsembleMaxBranches: 3,
			BudgetCapMicros: input.BudgetCapMicros, BudgetCapVersion: 1, BudgetNotifyPct: input.BudgetNotifyPct, BudgetState: "NORMAL",
			Holds: "[]", Health: "GREEN", HealthReasons: "[]", PortalSettings: string(portalJSON),
			AcceptancePolicy: `{"due_days":14,"reminder_days":[3,7,12],"escalate_day":14,"deemed_acceptance_days":null}`,
			NextDeliverySeq:  1, TargetDate: input.TargetDate, ProvisioningIssues: "[]", Version: 1,
			CreatedAt: now, UpdatedAt: now, CreatedBy: &actorID, UpdatedBy: &actorID,
		},
		members: append([]projectMember(nil), members...), actor: actor, audit: audit,
	}
	for _, contact := range input.Contacts {
		if contact.ReceivesDeliveries == nil || contact.CanAccept == nil {
			return projectDraft{}, ErrInvalidArgument
		}
		draft.contacts = append(draft.contacts, model.ProjectContact{
			TenantID: tenant, IntakeContactID: contact.IntakeContactID, Role: contact.Role,
			ReceivesDeliveries: *contact.ReceivesDeliveries, CanAccept: *contact.CanAccept,
			CreatedAt: now, UpdatedAt: now, CreatedBy: &actorID, UpdatedBy: &actorID,
		})
	}
	for _, repository := range input.Repositories {
		branches, err := json.Marshal(repository.ProtectedBranches)
		if err != nil {
			return projectDraft{}, err
		}
		id, err := m.ids.Next()
		if err != nil {
			return projectDraft{}, err
		}
		if id <= 0 {
			return projectDraft{}, ErrUnavailable
		}
		draft.repositories = append(draft.repositories, model.ProjectRepository{
			TenantID: tenant, ID: guid.ID(id), Name: repository.Name, RemoteURL: repository.RemoteURL,
			DefaultBranch: repository.DefaultBranch, ProtectedBranches: string(branches), IsPrimary: repository.IsPrimary,
			Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: &actorID, UpdatedBy: &actorID,
		})
	}
	return draft, nil
}

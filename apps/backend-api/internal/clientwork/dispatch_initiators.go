package clientwork

import (
	"context"
	"encoding/json"
	"errors"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DispatchRequestInitiatorFacts binds a channel request to its immutable
// proposed event. A later deciding Human is never substituted for its initiator.
type DispatchRequestInitiatorFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) (DispatchRequestInitiatorFact, bool, error)

type DispatchRequestInitiatorFact struct {
	ProjectID, WorkItemID, RequesterID guid.ID
}

func (m *Module) provideDispatchInitiators(s *module.Services) error {
	if err := module.Provide[delivery.ClientPackageConfirmationSource](s, m.packageConfirmationSourceOn); err != nil {
		return err
	}
	if err := module.Provide[delivery.ClarificationReplySource](s, m.clarificationReplySourceOn); err != nil {
		return err
	}
	return module.Provide[DispatchRequestInitiatorFacts](s, func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (DispatchRequestInitiatorFact, bool, error) {
		// Resolve at invocation after Wire/Seal, never during provider setup.
		source, err := module.Resolve[events.SubjectHumanActor](s)
		if err != nil {
			return DispatchRequestInitiatorFact{}, false, err
		}
		return m.channelDispatchInitiatorOn(ctx, tx, tenant, id, source)
	})
}

// The active publication source is clientwork_packages, not the unimplemented
// spec07 Delivery composer. Validate only actual existing publication facts.
func (m *Module) packageConfirmationSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, lock bool) (delivery.ClientPackageConfirmationFact, bool, error) {
	var out delivery.ClientPackageConfirmationFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || id <= 0 {
		return out, false, delivery.ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, delivery.ErrInvalidArgument
	}
	q := query.BindDeliveryPackage(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id))
	if lock {
		base = base.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	row, err := base.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out = delivery.ClientPackageConfirmationFact{ProjectID: row.ProjectID, PackageID: row.ID, Version: row.Version, Title: row.Title, Draft: row.State == "DRAFT" && row.ArchivedAt == nil}
	if !out.Draft {
		return out, true, nil
	}
	if m.clients == nil {
		return out, false, delivery.ErrUnavailable
	}
	active, err := m.clients.IsClientOn(ctx, tx, tenant, project, row.RecipientID)
	if err != nil {
		return out, false, err
	}
	if !active {
		return out, false, nil
	}
	if !lock {
		return out, true, nil
	}
	if m.projects == nil {
		return out, false, delivery.ErrUnavailable
	}
	var manifest packageManifest
	if json.Unmarshal([]byte(row.Manifest), &manifest) != nil {
		return out, false, delivery.ErrUnavailable
	}
	if !validText(row.ReleaseNotes, 8000) || !validText(row.Title, 200) || len(manifest.Bindings) == 0 {
		return out, false, delivery.ErrResultState
	}
	current, err := m.projects.AcceptedArtifactsOn(ctx, tx, tenant, project, manifest.Refs)
	if err != nil {
		return out, false, err
	}
	a, err := json.Marshal(current)
	if err != nil {
		return out, false, err
	}
	b, err := json.Marshal(manifest.Content.Artifacts)
	if err != nil {
		return out, false, err
	}
	if string(a) != string(b) {
		return out, false, delivery.ErrResultState
	}
	pins := map[guid.ID]bool{}
	for _, pin := range manifest.Bindings {
		if pin.BindingID <= 0 || pin.VersionID <= 0 {
			return out, false, delivery.ErrResultState
		}
		pins[pin.VersionID] = true
	}
	for _, artifact := range current {
		if !pins[artifact.VersionID] {
			return out, false, delivery.ErrResultState
		}
	}
	return out, true, nil
}

func (m *Module) channelDispatchInitiatorOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, source events.SubjectHumanActor) (DispatchRequestInitiatorFact, bool, error) {
	var out DispatchRequestInitiatorFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalid
	}
	if source == nil {
		return out, false, ErrUnavailable
	}
	q := query.BindDispatchProposal(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if r.Trigger != "CHANNEL_REQUEST" {
		return out, false, nil
	}
	actor, found, err := source(ctx, tx, tenant, r.ProjectID, "clientwork", id, "clientwork.dispatch_proposed")
	if err != nil || !found || actor <= 0 {
		return out, false, err
	}
	return DispatchRequestInitiatorFact{ProjectID: r.ProjectID, WorkItemID: r.WorkItemID, RequesterID: actor}, true, nil
}

// A round permits one answer per question. Counting actual immutable answered
// questions gives a reply revision unaffected by expiry or reminder versions.
func (m *Module) clarificationReplySourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, lock bool) (delivery.ClarificationReplyFact, bool, error) {
	var out delivery.ClarificationReplyFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalid
	}
	q := query.BindClarificationRound(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id))
	if lock {
		base = base.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	row, err := base.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	view, err := clarificationView(row)
	if err != nil {
		return out, false, err
	}
	out.ProjectID, out.RequesterID = row.ProjectID, row.CreatedBy
	for _, question := range view.Questions {
		if question.AnsweredAt != nil {
			out.ReplyVersion++
		}
	}
	return out, out.ReplyVersion > 0, nil
}

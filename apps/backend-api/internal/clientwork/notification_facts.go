package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// ExceptionNotificationFact contains persisted routing facts, never report text
// or caller-supplied recipients. Consumers must check current Human authority.
type ExceptionNotificationFact struct {
	ProjectID guid.ID
	State     string
}
type ExceptionNotificationFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) (ExceptionNotificationFact, bool, error)

func (m *Module) registerNotificationFacts() error {
	// Existing package reads evaluate this data-scoped permission through IAM.
	// Declaring it grants nothing; absent registry metadata makes that evaluation
	// fail before current role and row constraints can be checked.
	if m.registrar == nil || m.registrar.Registry() == nil {
		return ErrUnavailable
	}
	return m.registrar.Registry().Contribute([]authz.Action{{Resource: "delivery", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read authorized project delivery packages"}}, nil)
}

func (m *Module) ProvideServices(s *module.Services) error {
	if err := m.provideAcceptanceCalendar(s); err != nil {
		return err
	}
	if err := module.Provide[AcceptanceReminderFacts](s, m.acceptanceReminderFactsOn); err != nil {
		return err
	}
	if err := m.provideDispatchInitiators(s); err != nil {
		return err
	}
	if err := module.Provide[ClarificationReminderFacts](s, m.clarificationReminderFactsOn); err != nil {
		return err
	}
	if err := module.Provide[ClarificationNotificationFacts](s, m.clarificationNotificationFactsOn); err != nil {
		return err
	}
	if err := module.Provide[HomeReceipts](s, m.homeReceiptsOn); err != nil {
		return err
	}
	if err := module.Provide[CollaborationInbox](s, m.collaborationInboxOn); err != nil {
		return err
	}
	if err := module.Provide[CollaborationNotificationFacts](s, m.collaborationNotificationFactsOn); err != nil {
		return err
	}
	return module.Provide[ExceptionNotificationFacts](s, m.exceptionNotificationFactsOn)
}

// ClarificationNotificationFact exposes persisted routing identities only.
// A client answer is never treated as team authority or copied into a notice.
type ClarificationNotificationFact struct {
	ProjectID, IntakeID, RequesterID guid.ID
	State                            string
	Answered                         bool
	LateAnswer                       bool
}
type ClarificationNotificationFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) (ClarificationNotificationFact, bool, error)

func (m *Module) clarificationNotificationFactsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (ClarificationNotificationFact, bool, error) {
	var out ClarificationNotificationFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalid
	}
	q := query.BindClarificationRound(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	v, err := clarificationView(r)
	if err != nil {
		return out, false, err
	}
	out = ClarificationNotificationFact{ProjectID: r.ProjectID, IntakeID: r.IntakeID, RequesterID: r.CreatedBy, State: r.State}
	for _, question := range v.Questions {
		out.Answered = out.Answered || question.AnsweredAt != nil
		out.LateAnswer = out.LateAnswer || (question.AnsweredAt != nil && r.ClosedAt != nil && question.AnsweredAt.After(*r.ClosedAt))
	}
	return out, true, nil
}

type HomeReceiptEvent struct {
	PackageID, WorkItemID, ActorID guid.ID
	At                             time.Time
	Rejected                       bool
}

type HomeReceiptHistory struct {
	Events   []HomeReceiptEvent
	Complete bool
}

type HomeReceipts func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (HomeReceiptHistory, error)

// homeReceiptsOn maps frozen item outcomes back through the package's private
// immutable manifest. A legacy receipt without item identity is not an empty
// rejection history and must not produce a misleading rework percentile.
func (m *Module) homeReceiptsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, before time.Time) (HomeReceiptHistory, error) {
	out := HomeReceiptHistory{Events: []HomeReceiptEvent{}, Complete: true}
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.TenantID != tenant || tx == nil || before.IsZero() {
		return out, ErrForbidden
	}
	scoped := *claims
	scoped.EntityID = project
	ctx = authn.WithClaims(ctx, &scoped)
	if _, err := m.authorize(ctx, tx, project, "delivery_read", false); err != nil {
		return out, err
	}
	r := query.BindPackageReceipt(tx, m.names)
	receipts, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.CreatedAt.Lt(before.UTC())).Order(r.CreatedAt, r.ID).Find()
	if err != nil {
		return out, err
	}
	p := query.BindDeliveryPackage(tx, m.names)
	for _, receipt := range receipts {
		row, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ID.Eq(receipt.PackageID), p.ContentHash.Eq(receipt.ContentHash)).First()
		if err != nil {
			return out, err
		}
		var manifest packageManifest
		if err := json.Unmarshal([]byte(row.Manifest), &manifest); err != nil {
			return out, err
		}
		items := packageItems(row, manifest.Content)
		if len(items) != len(manifest.Refs) || receipt.ItemOutcomes == nil {
			out.Complete = false
			continue
		}
		workByItem := map[string]guid.ID{}
		for i, item := range items {
			workByItem[item.ItemID] = manifest.Refs[i].WorkItemID
		}
		var outcomes []PackageItemDecision
		if err := json.Unmarshal([]byte(*receipt.ItemOutcomes), &outcomes); err != nil {
			return out, err
		}
		if len(outcomes) != len(items) {
			out.Complete = false
		}
		seen := map[guid.ID]bool{}
		for _, outcome := range outcomes {
			work := workByItem[outcome.ItemID]
			if work <= 0 {
				out.Complete = false
				continue
			}
			// Keep every rejection; accepted sibling artifacts cannot erase one.
			if outcome.Result != "REJECTED" || seen[work] {
				continue
			}
			seen[work] = true
			out.Events = append(out.Events, HomeReceiptEvent{PackageID: row.ID, WorkItemID: work, ActorID: receipt.ActorID, At: receipt.CreatedAt, Rejected: true})
		}
		out.Events = append(out.Events, HomeReceiptEvent{PackageID: row.ID, ActorID: receipt.ActorID, At: receipt.CreatedAt})
	}
	return out, nil
}

// CollaborationInbox returns current, authorized decision sources. Consumers
// sort and page only after this authorization pass.
type CollaborationInbox func(context.Context, *gorm.DB, guid.ID, guid.ID, string) ([]CollaborationInboxItem, error)
type CollaborationInboxItem struct {
	ID, ProjectID guid.ID
	CreatedAt     time.Time
	DueAt         *time.Time
	IntakeID      guid.ID
	Revision      string
}

func (m *Module) collaborationInboxOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, kind string) ([]CollaborationInboxItem, error) {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.TenantID != tenant || claims.PrincipalID <= 0 {
		return nil, ErrForbidden
	}
	if tx == nil || tenant <= 0 || project < 0 {
		return nil, ErrInvalid
	}
	if m == nil {
		return nil, ErrUnavailable
	}
	var candidates []CollaborationInboxItem
	permission := ""
	switch kind {
	case "CLARIFICATION_REPLY":
		permission = "scope_change"
		q := query.BindClarificationRound(tx, m.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("ANSWERED", "EXPIRED"))
		if project > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(project))
		}
		rows, err := stmt.Find()
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			scoped := *claims
			scoped.EntityID = r.ProjectID
			resourceCtx := authn.WithClaims(ctx, &scoped)
			if _, err := m.authorize(resourceCtx, tx, r.ProjectID, "scope_change", false); errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, delivery.ErrWorkForbidden) || errors.Is(err, delivery.ErrProjectNotFound) || errors.Is(err, delivery.ErrWorkProjectReadOnly) {
				continue
			} else if err != nil {
				return nil, err
			}
			if m.intakeSources.SourceOn == nil {
				return nil, ErrUnavailable
			}
			source, err := m.intakeSources.SourceOn(resourceCtx, tx, tenant, r.ProjectID, r.IntakeID)
			if errors.Is(err, intake.ErrForbidden) || errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if (r.CreatedBy != claims.PrincipalID && (source.LastHandlerID == nil || *source.LastHandlerID != claims.PrincipalID)) || source.Version != r.IntakeVersion {
				continue
			}
			candidates = append(candidates, CollaborationInboxItem{ID: r.ID, ProjectID: r.ProjectID, CreatedAt: r.UpdatedAt, IntakeID: r.IntakeID, Revision: fmt.Sprint(r.Version)})
		}
	case "DISPATCH_PROPOSAL":
		permission = "work_item_assign"
		q := query.BindDispatchProposal(tx, m.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Eq("PROPOSED"), q.ExpiresAt.Gt(now()))
		if project > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(project))
		}
		rows, err := stmt.Find()
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			var snapshot DispatchSnapshot
			if err := json.Unmarshal([]byte(r.Snapshot), &snapshot); err != nil {
				return nil, err
			}
			if snapshot.RequiresHuman {
				due := r.ExpiresAt
				candidates = append(candidates, CollaborationInboxItem{ID: r.ID, ProjectID: r.ProjectID, CreatedAt: r.CreatedAt, DueAt: &due})
			}
		}
	case "EXCEPTION_DECISION":
		permission = "exception_decide"
		q := query.BindToleranceException(tx, m.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Eq("REPORTED"), q.Report.IsNotNull())
		if project > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(project))
		}
		rows, err := stmt.Find()
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			candidates = append(candidates, CollaborationInboxItem{ID: r.ID, ProjectID: r.ProjectID, CreatedAt: r.CreatedAt})
		}
	case "PREVIEW_SHARE_APPROVAL":
		permission = "preview_share_approve"
		q := query.BindPreviewShare(tx, m.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Eq("PENDING_APPROVAL"), q.RequestedBy.Neq(claims.PrincipalID))
		if project > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(project))
		}
		rows, err := stmt.Find()
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			candidates = append(candidates, CollaborationInboxItem{ID: r.ID, ProjectID: r.ProjectID, CreatedAt: r.CreatedAt})
		}
	default:
		return nil, ErrInvalid
	}
	out := []CollaborationInboxItem{}
	for _, item := range candidates {
		scoped := *claims
		scoped.EntityID = item.ProjectID
		resourceCtx := authn.WithClaims(ctx, &scoped)
		_, err := m.authorize(resourceCtx, tx, item.ProjectID, permission, false)
		if err == nil && kind == "PREVIEW_SHARE_APPROVAL" {
			err = m.projects.ApprovePreviewShareOn(resourceCtx, tx, tenant, item.ProjectID)
		}
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, delivery.ErrWorkForbidden) || errors.Is(err, delivery.ErrProjectNotFound) || errors.Is(err, delivery.ErrWorkProjectReadOnly) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// CollaborationNotificationFact contains only persisted routing facts. In
// particular, it intentionally exposes no draft creator as a delivery decider.
type CollaborationNotificationFact struct {
	ProjectID        guid.ID
	WorkItemID       guid.ID
	AppliedID        guid.ID
	AssignmentID     guid.ID
	Automatic        bool
	RequesterID      guid.ID
	ApprovalEligible bool
	State, Decision  string
	RequiresHuman    bool
	ExpiresAt        time.Time
}

type CollaborationNotificationFacts func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (CollaborationNotificationFact, bool, error)

func (m *Module) collaborationNotificationFactsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string) (CollaborationNotificationFact, bool, error) {
	var out CollaborationNotificationFact
	if m == nil {
		return out, false, ErrUnavailable
	}
	if tx == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalid
	}
	switch kind {
	case "preview":
		q := query.BindPreviewShare(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		out.ProjectID, out.State, out.RequesterID = r.ProjectID, shareRecord(r).State, r.RequestedBy
		if r.DecidedBy != nil {
			out.Decision = r.State
		}
		if actor := authn.PrincipalIDFromContext(ctx); actor > 0 && actor != r.RequestedBy && out.State == "PENDING_APPROVAL" {
			_, err = m.authorize(ctx, tx, r.ProjectID, "preview_share_approve", false)
			if err == nil {
				err = m.projects.ApprovePreviewShareOn(ctx, tx, tenant, r.ProjectID)
			}
			if err != nil && !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) && !errors.Is(err, delivery.ErrWorkForbidden) && !errors.Is(err, delivery.ErrProjectNotFound) && !errors.Is(err, delivery.ErrWorkProjectReadOnly) {
				return out, false, err
			}
			out.ApprovalEligible = err == nil
		}
		return out, true, nil
	case "tolerance":
		q := query.BindTolerance(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		return CollaborationNotificationFact{ProjectID: r.ProjectID, State: r.State}, true, nil
	case "package":
		q := query.BindDeliveryPackage(tx, m.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.ArchivedAt.IsNull()).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		out.ProjectID, out.State = row.ProjectID, row.State
		r := query.BindPackageReceipt(tx, m.names)
		receipt, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(row.ProjectID), r.PackageID.Eq(id), r.ContentHash.Eq(row.ContentHash)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, true, nil
		}
		if err != nil {
			return out, false, err
		}
		out.Decision = receipt.Decision
		return out, true, nil
	case "dispatch":
		q := query.BindDispatchProposal(tx, m.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		var snapshot DispatchSnapshot
		if err = json.Unmarshal([]byte(row.Snapshot), &snapshot); err != nil {
			return out, false, err
		}
		out = CollaborationNotificationFact{ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, State: row.State, RequiresHuman: snapshot.RequiresHuman, ExpiresAt: row.ExpiresAt}
		if row.AppliedID != nil {
			out.AppliedID = *row.AppliedID
		}
		if row.AssignmentID != nil {
			out.AssignmentID = *row.AssignmentID
		}
		if out.State == "APPLIED" && out.AssignmentID > 0 && out.AppliedID > 0 {
			a := query.BindDispatchAction(tx, m.names)
			count, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(row.ProjectID), a.ProposalID.Eq(id), a.WorkItemID.Eq(row.WorkItemID), a.Action.Eq("AUTO_APPLY"), a.AssignmentID.Eq(out.AssignmentID), a.SelectedID.Eq(out.AppliedID)).Count()
			if err != nil {
				return out, false, err
			}
			out.Automatic = count > 0
		}
		return out, true, nil
	default:
		return out, false, ErrInvalid
	}
}

func (m *Module) exceptionNotificationFactsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (ExceptionNotificationFact, bool, error) {
	var out ExceptionNotificationFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalid
	}
	if m == nil {
		return out, false, ErrUnavailable
	}
	q := query.BindToleranceException(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	return ExceptionNotificationFact{ProjectID: row.ProjectID, State: row.State}, true, nil
}

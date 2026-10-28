package clientwork

import (
	activityi18n "7link.com/platform/internal/clientwork/i18n"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

func (m *Module) injectDisposition(s *module.Services) error {
	merge, err := module.Resolve[intake.AgentMerge](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.agentMerge = merge
	notice, err := module.Resolve[collab.IntakeDispositionNotice](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.dispositionNotice = notice
	receipts, err := module.Resolve[collab.IntakeDispositionReceipts](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.dispositionReceipts = receipts
	routes, err := module.Resolve[intake.MergedReplyRoutes](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.mergedReplyRoutes = routes
	routing, err := module.Resolve[collab.IntakeReplyRouting](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.intakeReplyRouting = routing
	fence, err := module.Resolve[delivery.IntakeReplyRouteFence](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.intakeReplyFence = fence
	if routes == nil || routing.ReadOn == nil || routing.ForwardOn == nil || fence == nil {
		return nil
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	return catalog.AddSubscribers(Name, events.Subscriber{DisplayNameKey: activityi18n.Namespace + ".activity_subscriber_clientwork_merged_source_replies_name", DescriptionKey: activityi18n.Namespace + ".activity_subscriber_clientwork_merged_source_replies_description", Name: "clientwork.merged_source_replies", EventTypes: []string{"collab.message_posted"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: m.routeMergedReplyOn})
}

func (m *Module) routeMergedReplyOn(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
	if e.EventType != "collab.message_posted" || e.TenantID <= 0 || e.ProjectID <= 0 || e.Subject.Kind != "message" || e.Subject.ID <= 0 {
		return ErrInvalid
	}
	if m.intakeReplyRouting.ReadOn == nil || m.intakeReplyRouting.ForwardOn == nil || m.mergedReplyRoutes == nil || m.intakeReplyFence == nil || m.clients == nil {
		return ErrUnavailable
	}
	fact, err := m.intakeReplyRouting.ReadOn(ctx, tx, e.TenantID, e.ProjectID, e.Subject.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, collab.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	q := query.BindIntakeThread(tx, m.names)
	sources, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ProjectID.Eq(e.ProjectID), q.ChannelID.Eq(fact.ChannelID), q.RootMessageID.Eq(fact.RootID), q.RecipientID.Eq(fact.RecipientID)).Limit(2).Find()
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}
	if len(sources) != 1 {
		return ErrConflict
	}
	source := sources[0]
	route, err := m.mergedReplyRoutes(ctx, tx, e.TenantID, e.ProjectID, source.IntakeID, fact.RecipientID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := m.intakeReplyFence(ctx, tx, e.TenantID, e.ProjectID, []guid.ID{fact.RecipientID, route.TargetRecipientID}); err != nil {
		return err
	}
	// The initial chain is only a hint until the principal/project fences hold.
	current, err := m.mergedReplyRoutes(ctx, tx, e.TenantID, e.ProjectID, source.IntakeID, fact.RecipientID)
	if err != nil {
		return err
	}
	if current.TargetID != route.TargetID || current.TargetRecipientID != route.TargetRecipientID {
		return ErrConflict
	}
	for _, recipient := range []guid.ID{fact.RecipientID, route.TargetRecipientID} {
		client, err := m.clients.IsClientOn(ctx, tx, e.TenantID, e.ProjectID, recipient)
		if err != nil {
			return err
		}
		if !client {
			return ErrNotFound
		}
	}
	target, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ProjectID.Eq(e.ProjectID), q.IntakeID.Eq(route.TargetID), q.RecipientID.Eq(route.TargetRecipientID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = m.intakeReplyRouting.ForwardOn(ctx, tx, e.TenantID, e.ProjectID, collab.IntakeReplyForward{SourceChannelID: fact.ChannelID, SourceRootID: fact.RootID, SourceRecipientID: fact.RecipientID, MessageID: e.Subject.ID, SourceHash: route.SourceHash, SourceRef: route.SourceRef, TargetChannelID: target.ChannelID, TargetRootID: target.RootMessageID, TargetRecipientID: target.RecipientID, TargetHash: route.TargetHash})
	return err
}

// A distinct request selects the narrow, current-delegation path before replay.
type AgentMergeInput struct {
	EditInput[intake.SourceDisposition]
}

func (m *Module) registerDispositionHTTP(api huma.API) error {
	path := entityBase + "/intakes/{id}/delegated-merge"
	owner := authz.ResourceOwnerResolver(m.owner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.delegated_merge", Method: "POST", Path: path, Summary: "Merge an Intake under current independent Agent delegation", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "intake", Verb: "convert"}, m.agentMergeHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.disposition_receipt", Method: "GET", Path: entityBase + "/intakes/{id}/disposition-receipt", Summary: "Read the actual original-thread disposition message and client read receipt", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "scope", Verb: "change"}, m.dispositionReceiptHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, path)
}

func (m *Module) agentMergeHTTP(ctx context.Context, in *AgentMergeInput) (*CommandOutput, error) {
	p, id, version, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if in.Body.Action != "MERGE" {
		return nil, httpError(ctx, ErrForbidden)
	}
	return m.command(ctx, p, "clientwork.intake.delegated_merge", "POST", entityBase+"/intakes/"+id.String()+"/delegated-merge", in.Key, 200, in, "intake_convert", false, nil, func(tx *gorm.DB) (Receipt, error) {
		if m.agentMerge.MergeOn == nil {
			return Receipt{}, ErrUnavailable
		}
		out, err := m.agentMerge.MergeOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id, version, in.Body)
		return Receipt{ID: out.ID, Version: out.Version, State: out.State}, err
	})
}

func (m *Module) dispositionReceiptHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[intake.DispositionDelivery], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.intakeSources.SourceOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out intake.DispositionDelivery
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := m.authorize(ctx, tx, p, "scope_change", false); err != nil {
			return err
		}
		source, err := m.intakeSources.SourceOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id)
		if err != nil {
			return err
		}
		out = source.DispositionDelivery
		if out.State == "" {
			out.State = "NOT_REQUESTED"
		}
		if out.State != "POSTED" {
			return nil
		}
		if out.MessageID == nil || m.dispositionReceipts == nil {
			return ErrUnavailable
		}
		thread, err := m.intakeThreadOn(ctx, tx, p, id)
		if err != nil {
			return err
		}
		hash, err := m.validateIntakeThreadOn(ctx, tx, p, id, threadLink(thread))
		if err != nil {
			return err
		}
		read, err := m.dispositionReceipts(ctx, tx, thread.TenantID, p, thread.ChannelID, thread.RootMessageID, thread.RecipientID, *out.MessageID, hash)
		if read {
			out.State = "READ"
		}
		return err
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) bindPipeline(s *module.Services) error {
	c, err := module.Resolve[delivery.PipelineClientConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(m.pipelineStagesOn)
}

// Pipeline reads the owner's exact receipts without exporting private case
// bodies, command output, sender identity or client-only package contents.
func (m *Module) pipelineStagesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, works []guid.ID, links []delivery.PipelineDeliveryLink) ([]delivery.PipelineStageRun, error) {
	out := []delivery.PipelineStageRun{}
	if tx == nil || tenant <= 0 || project <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return out, delivery.ErrWorkForbidden
	}
	if _, err := m.authorize(ctx, tx, project, "work_item_read", false); err != nil {
		if errors.Is(err, ErrForbidden) || errors.Is(err, delivery.ErrWorkForbidden) {
			return out, delivery.ErrWorkForbidden
		}
		if errors.Is(err, ErrNotFound) {
			return out, delivery.ErrProjectNotFound
		}
		return out, delivery.ErrUnavailable
	}
	if len(works) == 0 {
		return out, nil
	}
	if len(works) > 10000 || len(links) > 10000 {
		return out, delivery.ErrUnavailable
	}
	ids := make([]driver.Valuer, len(works))
	for i, id := range works {
		ids[i] = id
	}
	h := query.BindHiddenVerificationReceipt(tx, m.names)
	hidden, err := h.WithContext(ctx).Where(h.TenantID.Eq(tenant), h.ProjectID.Eq(project), h.WorkItemID.In(ids...)).Limit(10001).Find()
	if err != nil {
		return out, err
	}
	if len(hidden) > 10000 {
		return out, delivery.ErrUnavailable
	}
	for _, r := range hidden {
		state := r.Verdict
		if !slices.Contains([]string{"PASSED", "FAILED", "INCONCLUSIVE"}, state) || len(r.ReceiptHash) != 64 {
			state = "INCONCLUSIVE"
		}
		out = append(out, delivery.PipelineStageRun{ID: "hidden:" + r.ID.String(), ProjectID: project, WorkItemID: r.WorkItemID, ResultID: r.ResultID, Stage: "HIDDEN_SUITE", State: state, CreatedAt: r.CreatedAt, EndedAt: &r.CreatedAt, VerificationID: &r.ID, EvidenceRefs: []string{"receipt-sha256:" + r.ReceiptHash}})
	}
	// Package composition can originate directly in Clientwork, without a
	// legacy result handoff. Discover only exact frozen work/result references.
	p := query.BindDeliveryPackage(tx, m.names)
	packages, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).Limit(10001).Find()
	if err != nil {
		return nil, err
	}
	if len(packages) > 10000 {
		return nil, delivery.ErrUnavailable
	}
	packageByID := map[guid.ID]model.DeliveryPackage{}
	frozenLinks := map[delivery.PipelineDeliveryLink]bool{}
	for _, pkg := range packages {
		packageByID[pkg.ID] = *pkg
		var manifest packageManifest
		if json.Unmarshal([]byte(pkg.Manifest), &manifest) != nil {
			return nil, delivery.ErrUnavailable
		}
		for _, ref := range manifest.Refs {
			if slices.Contains(works, ref.WorkItemID) {
				link := delivery.PipelineDeliveryLink{DeliveryID: pkg.ID, WorkItemID: ref.WorkItemID, ResultID: ref.ResultID}
				frozenLinks[link] = true
				links = append(links, link)
			}
		}
	}
	seenLinks := map[delivery.PipelineDeliveryLink]bool{}
	if len(links) > 20000 {
		return nil, delivery.ErrUnavailable
	}
	for _, link := range links {
		if !frozenLinks[link] {
			continue
		}
		if seenLinks[link] {
			continue
		}
		seenLinks[link] = true
		if !slices.Contains(works, link.WorkItemID) {
			return nil, delivery.ErrUnavailable
		}
		t := query.BindPackageTimeline(tx, m.names)
		timeline, err := t.WithContext(ctx).Where(t.TenantID.Eq(tenant), t.ProjectID.Eq(project), t.PackageID.Eq(link.DeliveryID)).Limit(1001).Find()
		if err != nil {
			return out, err
		}
		if len(timeline) > 1000 {
			return out, delivery.ErrUnavailable
		}
		for _, r := range timeline {
			if pkg, ok := packageByID[link.DeliveryID]; !ok || r.ContentHash != pkg.ContentHash {
				continue
			}
			state := "INCONCLUSIVE"
			switch r.State {
			case "DRAFT":
				state = "PENDING"
			case "PUBLISHED":
				state = "RUNNING"
			case "WITHDRAWN", "ARCHIVED":
				state = "BLOCKED"
			}
			out = append(out, delivery.PipelineStageRun{ID: "package-transition:" + r.ID.String() + ":" + link.WorkItemID.String() + ":" + link.ResultID.String(), ProjectID: project, WorkItemID: link.WorkItemID, ResultID: link.ResultID, Stage: "DELIVERY", State: state, CreatedAt: r.CreatedAt, DeliveryID: &link.DeliveryID, EvidenceRefs: []string{"package-version:" + strconv.Itoa(r.Version), "manifest-sha256:" + r.ContentHash}})
		}
		rq := query.BindPackageReceipt(tx, m.names)
		receipts, err := rq.WithContext(ctx).Where(rq.TenantID.Eq(tenant), rq.ProjectID.Eq(project), rq.PackageID.Eq(link.DeliveryID)).Limit(1001).Find()
		if err != nil {
			return out, err
		}
		if len(receipts) > 1000 {
			return out, delivery.ErrUnavailable
		}
		for _, r := range receipts {
			state := "INCONCLUSIVE"
			if r.ItemOutcomes == nil && r.ContentHash == packageByID[link.DeliveryID].ContentHash {
				switch r.Decision {
				case "ACCEPT":
					state = "PASSED"
				case "REJECT":
					state = "FAILED"
				}
			} else if r.ItemOutcomes != nil {
				state = pipelinePackageOutcome(packageByID[link.DeliveryID], *r, link)
			}
			out = append(out, delivery.PipelineStageRun{ID: "package-receipt:" + r.ID.String() + ":" + link.WorkItemID.String() + ":" + link.ResultID.String(), ProjectID: project, WorkItemID: link.WorkItemID, ResultID: link.ResultID, Stage: "DELIVERY", State: state, CreatedAt: r.CreatedAt, EndedAt: &r.CreatedAt, DecisionID: &r.ID, DeliveryID: &link.DeliveryID, EvidenceRefs: []string{"manifest-sha256:" + r.ContentHash}})
		}
		if len(out) > 20000 {
			return nil, delivery.ErrUnavailable
		}
	}
	return out, nil
}

// Match item identities to their exact frozen work/result references. An old
// manifest or malformed/incomplete outcome cannot become a successful stage.
func pipelinePackageOutcome(pkg model.DeliveryPackage, receipt model.PackageReceipt, link delivery.PipelineDeliveryLink) string {
	if pkg.ContentHash != receipt.ContentHash || receipt.ItemOutcomes == nil {
		return "INCONCLUSIVE"
	}
	var manifest packageManifest
	var outcomes []PackageItemDecision
	if json.Unmarshal([]byte(pkg.Manifest), &manifest) != nil || json.Unmarshal([]byte(*receipt.ItemOutcomes), &outcomes) != nil || len(manifest.Refs) != len(manifest.Content.Artifacts) {
		return "INCONCLUSIVE"
	}
	items := packageItems(&pkg, manifest.Content)
	byID := map[string]string{}
	for _, o := range outcomes {
		if byID[o.ItemID] != "" || (o.Result != "ACCEPTED" && o.Result != "REJECTED") {
			return "INCONCLUSIVE"
		}
		byID[o.ItemID] = o.Result
	}
	if len(byID) != len(items) {
		return "INCONCLUSIVE"
	}
	found, rejected := false, false
	for i, item := range items {
		if byID[item.ItemID] == "" {
			return "INCONCLUSIVE"
		}
		ref := manifest.Refs[i]
		if ref.WorkItemID == link.WorkItemID && ref.ResultID == link.ResultID {
			found = true
			rejected = rejected || byID[item.ItemID] == "REJECTED"
		}
	}
	if !found {
		return "INCONCLUSIVE"
	}
	if rejected {
		return "FAILED"
	}
	return "PASSED"
}

package clientwork

import (
	"7link.com/platform/internal/agents"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) bindKnowledgeAcceptance(s *module.Services) error {
	if err := m.bindRetirementDispatch(s); err != nil {
		return err
	}
	if err := m.bindAgentPerformance(s); err != nil {
		return err
	}
	if c, e := module.Resolve[agents.AgentRosterAnalyticsConnector](s); e == nil {
		if e = c.Bind(m.agentRosterAnalyticsOn); e != nil {
			return e
		}
	} else if !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	connector, err := module.Resolve[knowledge.ClientAcceptanceReaderConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = connector.Bind(knowledge.ClientAcceptanceReader{ReadOn: m.knowledgeAcceptedWorkOn}); err != nil {
		return err
	}
	documents, err := module.Resolve[knowledge.EventSourceReaderConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return documents.Bind(knowledge.EventSourceReader{ReadOn: m.knowledgeCancellationDocumentsOn})
}

func (m *Module) bindRetirementDispatch(s *module.Services) error {
	connector, err := module.Resolve[delivery.RetirementDispatchConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return connector.Bind(delivery.RetirementDispatch{TransferOn: m.retirementDispatchOn})
}

func (m *Module) retirementDispatchOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, assignment delivery.RetirementAssignment, in delivery.RetirementTransferInput) (guid.ID, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant || assignment.ProjectID <= 0 || assignment.WorkItemID <= 0 || assignment.WorkVersion < 1 || in.RetirementID <= 0 || in.ArtifactID <= 0 || in.AssigneePrincipalID <= 0 || !slices.Contains([]string{"HUMAN", "AGENT"}, in.AssigneeKind) {
		return 0, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalid
	}
	proposal, err := m.createDispatchOn(ctx, tx, assignment.ProjectID, DispatchBody{WorkItemID: assignment.WorkItemID, ExpectedWorkVersion: assignment.WorkVersion, Trigger: "REDISPATCH"})
	if err != nil {
		return 0, err
	}
	reason := fmt.Sprintf("Agent retirement handover: %s; artifact: %s", in.RetirementID, in.ArtifactID)
	_, err = m.decideDispatchOn(ctx, tx, assignment.ProjectID, proposal.ID, proposal.Version, DispatchDecision{Decision: "OVERRIDE", AssigneePrincipalID: &in.AssigneePrincipalID, Reason: reason})
	if err != nil {
		return 0, err
	}
	q := query.BindDispatchProposal(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(assignment.ProjectID), q.ID.Eq(proposal.ID)).First()
	if err != nil {
		return 0, err
	}
	if row.AssignmentID == nil || *row.AssignmentID <= 0 {
		return 0, ErrConflict
	}
	return *row.AssignmentID, nil
}

func (m *Module) agentRosterAnalyticsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, ids []guid.ID) (map[guid.ID]agents.RosterAnalytics, error) {
	if len(ids) > 200 {
		return nil, ErrInvalid
	}
	out := map[guid.ID]agents.RosterAnalytics{}
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrInvalid
		}
		out[id] = agents.RosterAnalytics{}
	}
	at := time.Now().UTC()
	for _, kind := range []string{"FEATURE", "BUG", "CHORE", "DOC", "RESEARCH", "DESIGN", "TEST", "TASK", "RISK", "INCIDENT", "CONTENT"} {
		facts, _, _, e := m.measuredDispatchWindowOn(ctx, tx, tenant, kind, at, 30*24*time.Hour)
		if e != nil {
			return nil, e
		}
		// Manual claims carry their actual Human actor; dispatch scoring excludes
		// these because that score only ranks Agent executions.
		works, e := m.dispatchDecisions.ReadOn(ctx, tx, tenant, kind, at)
		if e != nil {
			return nil, e
		}
		for _, work := range works {
			if len(work.Decisions) == 0 {
				continue
			}
			first := work.Decisions[0]
			if first.CreatedAt.Before(at.Add(-30*24*time.Hour)) || first.CreatedAt.After(at) {
				continue
			}
			counted := map[guid.ID]bool{}
			for _, claim := range work.Claims {
				if claim.ExecutionID != nil || claim.ActorID <= 0 || claim.CreatedAt.After(first.CreatedAt) || counted[claim.ActorID] {
					continue
				}
				if _, wanted := out[claim.ActorID]; !wanted {
					continue
				}
				counted[claim.ActorID] = true
				value := out[claim.ActorID]
				value.FirstDecisions++
				if first.Action == "APPROVE" {
					value.FirstPassAccepts++
				}
				out[claim.ActorID] = value
			}
		}
		for _, id := range ids {
			if f := facts[id]; f != nil {
				v := out[id]
				v.FirstDecisions += f.FirstDecisions
				v.FirstPassAccepts += f.FirstPassAccepts
				out[id] = v
			}
		}
	}
	return out, nil
}

func (m *Module) knowledgeCancellationDocumentsOn(ctx context.Context, tx *gorm.DB, event events.Envelope) ([]knowledge.EventSourceDocument, error) {
	if event.EventType != "work_item.state_changed" && event.EventType != "project.stage_changed" {
		return nil, nil
	}
	var payload struct {
		To         string  `json:"to"`
		ToStage    string  `json:"to_stage"`
		DecisionID guid.ID `json:"decision_id"`
	}
	if json.Unmarshal(event.Data, &payload) != nil {
		return nil, ErrInvalid
	}
	if (payload.To != "CANCELLED" && payload.ToStage != "CANCELLED") || payload.DecisionID <= 0 { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return nil, nil
	}
	if tx == nil || tx.Statement == nil || event.TenantID <= 0 || event.ProjectID <= 0 || event.Subject.ID <= 0 {
		return nil, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalid
	}
	q := query.BindCommandDecision(tx, m.names)
	decision, err := q.WithContext(ctx).Where(q.TenantID.Eq(event.TenantID), q.ProjectID.Eq(event.ProjectID), q.ID.Eq(payload.DecisionID), q.Action.Eq("CANCEL"), q.TargetKind.Eq("PROJECT"), q.TargetID.Eq(event.ProjectID), q.ActorID.Eq(event.Actor.ID), q.CreatedAt.Lte(event.OccurredAt)).First()
	if err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(decision.Reason) < 20 {
		return nil, nil
	}
	return []knowledge.EventSourceDocument{{Kind: "DECISION", Origin: "decision", OriginID: decision.ID, Version: 1, Title: "Project cancellation", Content: fmt.Sprintf("## CANCEL\n\n%s\n\n- Project [%s]\n- Exception [%s]\n", decision.Reason, decision.ProjectID, decision.SourceID)}}, nil
}
func (m *Module) knowledgeAcceptedWorkOn(ctx context.Context, tx *gorm.DB, event events.Envelope) ([]guid.ID, error) {
	if tx == nil || tx.Statement == nil || event.TenantID <= 0 || event.ProjectID <= 0 || event.Subject.ID <= 0 {
		return nil, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalid
	}
	var payload struct {
		ReceiptSHA256 string `json:"receipt_sha256"`
	}
	if json.Unmarshal(event.Data, &payload) != nil {
		return nil, ErrInvalid
	}
	// Historical package events predate signed receipt metadata. They cannot
	// establish client acceptance and must not park the ingestion stream.
	if payload.ReceiptSHA256 == "" {
		return []guid.ID{}, nil
	}
	if len(payload.ReceiptSHA256) != 64 {
		return nil, ErrInvalid
	}
	q := query.BindPackageReceipt(tx, m.names)
	receipt, err := q.WithContext(ctx).Where(q.TenantID.Eq(event.TenantID), q.ProjectID.Eq(event.ProjectID), q.PackageID.Eq(event.Subject.ID), q.ReceiptSHA256.Eq(payload.ReceiptSHA256)).First()
	if err != nil {
		return nil, err
	}
	p := query.BindDeliveryPackage(tx, m.names)
	pack, err := p.WithContext(ctx).Where(p.TenantID.Eq(event.TenantID), p.ProjectID.Eq(event.ProjectID), p.ID.Eq(event.Subject.ID)).First()
	if err != nil {
		return nil, err
	}
	return acceptedWorkFromReceipt(pack, receipt)
}
func acceptedWorkFromReceipt(pack *model.DeliveryPackage, receipt *model.PackageReceipt) ([]guid.ID, error) {
	if pack == nil || receipt == nil || pack.TenantID != receipt.TenantID || pack.ProjectID != receipt.ProjectID || pack.ID != receipt.PackageID || pack.ContentHash != receipt.ContentHash {
		return nil, ErrInvalid
	}
	if receipt.Decision != "ACCEPT" && receipt.Decision != "PARTIALLY_ACCEPTED" {
		return []guid.ID{}, nil
	}
	var manifest packageManifest
	if json.Unmarshal([]byte(pack.Manifest), &manifest) != nil || len(manifest.Refs) != len(manifest.Content.Artifacts) || len(manifest.Refs) == 0 {
		return nil, ErrInvalid
	}
	if receipt.ItemOutcomes == nil {
		return nil, ErrInvalid
	}
	var outcomes []PackageItemDecision
	if json.Unmarshal([]byte(*receipt.ItemOutcomes), &outcomes) != nil {
		return nil, ErrInvalid
	}
	items := packageItems(pack, manifest.Content)
	results := map[string]string{}
	for _, outcome := range outcomes {
		if results[outcome.ItemID] != "" {
			return nil, ErrInvalid
		}
		results[outcome.ItemID] = outcome.Result
	}
	if len(results) != len(items) {
		return nil, ErrInvalid
	}
	accepted, rejected := map[guid.ID]bool{}, map[guid.ID]bool{}
	for i, item := range items {
		work := manifest.Refs[i].WorkItemID
		if work <= 0 {
			return nil, ErrInvalid
		}
		switch results[item.ItemID] {
		case "ACCEPTED":
			accepted[work] = true
		case "REJECTED":
			rejected[work] = true
		default:
			return nil, ErrInvalid
		}
	}
	out := []guid.ID{}
	for work := range accepted {
		if !rejected[work] {
			out = append(out, work)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (m *Module) bindChannelDispatch(s *module.Services) error {
	connector, e := module.Resolve[collab.ChannelDispatchConnector](s)
	if e != nil {
		return e
	}
	poster, e := module.Resolve[collab.ChannelPoster](s)
	if e != nil {
		return e
	}
	if e = connector.BindFacts(func(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (map[string]any, error) {
		q := query.BindDispatchProposal(tx, m.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
		if err != nil {
			return nil, err
		}
		var snapshot DispatchSnapshot
		if err = json.Unmarshal([]byte(row.Snapshot), &snapshot); err != nil {
			return nil, err
		}
		facts := map[string]any{"proposal_id": row.ID.String(), "work_item_id": row.WorkItemID.String(), "project_id": project.String(), "status": row.State, "result": row.Result, "requires_human": snapshot.RequiresHuman, "guard_reasons": snapshot.GuardReasons, "proposal_version": row.Version}
		if row.RecommendedID != nil {
			facts["recommended_id"] = row.RecommendedID.String()
		}
		if row.AppliedID != nil {
			facts["applied_id"] = row.AppliedID.String()
		}
		return facts, nil
	}); e != nil {
		return e
	}
	return connector.Bind(func(ctx context.Context, tx *gorm.DB, in collab.ChannelDispatchRequest) (collab.ChannelDispatchResult, error) {
		var empty collab.ChannelDispatchResult
		if m.dispatchFleet.ListOn == nil {
			return empty, ErrUnavailable
		}
		agents, e := m.dispatchFleet.ListOn(ctx, tx, in.TenantID)
		if e != nil {
			return empty, e
		}
		lead := false
		for _, agent := range agents {
			if agent.PrincipalID == authn.PrincipalIDFromContext(ctx) && agent.State == "ACTIVE" && slices.Contains(agent.Roles, "LEAD") {
				lead = true
			}
		}
		if !lead {
			return empty, collab.ErrForbidden
		}
		if in.CheckOnly {
			if _, e := m.authorize(ctx, tx, in.ProjectID, "work_item_assign", false); e != nil {
				return empty, e
			}
			if m.dispatch.WorkOn == nil {
				return empty, ErrUnavailable
			}
			_, e := m.dispatch.WorkOn(ctx, tx, in.TenantID, in.ProjectID, in.WorkItemID)
			return empty, e
		}
		receipt, e := m.createDispatchOn(ctx, tx, in.ProjectID, DispatchBody{WorkItemID: in.WorkItemID, ExpectedWorkVersion: in.ExpectedWorkVersion, Trigger: "CHANNEL_REQUEST"})
		if e != nil {
			return empty, e
		}
		q := query.BindDispatchProposal(tx, m.names)
		row, e := q.WithContext(ctx).Where(q.ID.Eq(receipt.ID), q.TenantID.Eq(in.TenantID)).First()
		if e != nil {
			return empty, e
		}
		var snapshot DispatchSnapshot
		if e = json.Unmarshal([]byte(row.Snapshot), &snapshot); e != nil {
			return empty, e
		}
		snapshot.RequiresHuman = true
		encoded, digest, e := encodeDispatchSnapshot(snapshot)
		if e != nil {
			return empty, e
		}
		if _, e = q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.TenantID.Eq(in.TenantID)).Updates(map[string]any{"snapshot": encoded, "inputs_digest": digest}); e != nil {
			return empty, e
		}
		payload := map[string]any{"proposal_id": row.ID.String(), "work_item_id": row.WorkItemID.String(), "project_id": in.ProjectID.String(), "requested_by": in.RequestedBy.String(), "source_message_id": in.SourceMessageID.String(), "status": "PROPOSED", "requires_human": true, "guard_reasons": snapshot.GuardReasons, "result": row.Result}
		if row.RecommendedID != nil {
			payload["recommended_id"] = row.RecommendedID.String()
		}
		_, e = poster.PostSystemOn(ctx, tx, collab.SystemCardInput{TenantID: in.TenantID, ProjectID: in.ProjectID, ChannelID: in.ChannelID, SourceID: row.ID, Kind: "DISPATCH_CARD", Body: "CHANNEL_REQUEST", FoldKey: "dispatch:" + row.ID.String(), Attention: row.Result == "NO_ELIGIBLE", Payload: payload})
		if e != nil {
			return empty, e
		}
		return collab.ChannelDispatchResult{ProposalID: row.ID, State: row.State, RequiresHuman: true}, nil
	})
}

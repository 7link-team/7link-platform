package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrDefectAgentAccept = errors.New("delivery: Agent cannot accept a defect")
var ErrDefectDeployment = errors.New("delivery: deployment does not match trusted merged artifact")
var ErrDefectReopen = errors.New("delivery: defect reopen source is not terminal")

// DeploymentEvent is our pipeline contract. Commit is the accepted source
// commit contained in the deployed artifact; the producer attests that mapping.
// Neither a file attachment nor an ordinary Human/Agent claim is a deployment.
type DeploymentEvent struct {
	ResultID     guid.ID   `json:"result_id"`
	EventKey     string    `json:"event_key" minLength:"1" maxLength:"128"`
	Environment  string    `json:"environment" minLength:"1" maxLength:"128"`
	Commit       string    `json:"commit" minLength:"40" maxLength:"64"`
	ArtifactHash string    `json:"artifact_hash" minLength:"64" maxLength:"64"`
	FixedVersion string    `json:"fixed_version" minLength:"1" maxLength:"64"`
	OccurredAt   time.Time `json:"occurred_at"`
}
type DeploymentReceipt struct {
	ID         guid.ID `json:"id"`
	WorkItemID guid.ID `json:"work_item_id"`
	ResultID   guid.ID `json:"result_id"`
	Version    int     `json:"version"`
	Stage      string  `json:"stage"`
}

func digestDeployment(in DeploymentEvent) string {
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Service credentials carry only current project work_item_write authority.
// This path deliberately cannot use Human/Agent result-decision authority.
func (m *Module) deploymentWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (*model.WorkItem, error) {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims.SubjectType != authn.SubjectTypeServiceAccount || claims.PrincipalID <= 0 || claims.TenantID != tenant || claims.EntityID != project {
		return nil, ErrWorkForbidden
	}
	if !isWorkActivityTransaction(tx) || m.workPolicy == nil || m.principals.Lock == nil || m.principals.ActiveRow == nil {
		return nil, ErrUnavailable
	}
	actor := claims.PrincipalID
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return nil, err
	}
	principal, active, err := m.principals.ActiveRow(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	if !active || principal.ID != actor {
		return nil, ErrWorkForbidden
	}
	c, err := m.workPolicy(ctx, tx, tenant, project, "work_item_write", actor)
	if err != nil {
		return nil, err
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(c, project, w.CreatedBy)
	if err != nil {
		return nil, err
	}
	p := query.BindProjects(tx, m.service.names)
	row, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ArchivedAt.IsNull()).First()
	if err != nil {
		return nil, err
	}
	if row.Stage == "CLOSED" || row.Stage == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return nil, ErrWorkProjectReadOnly
	}
	statement := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(id), w.ArchivedAt.IsNull())
	if scope != nil {
		statement = statement.Where(scope)
	}
	return statement.First()
}

func (m *Module) recordDeploymentOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, in DeploymentEvent) (DeploymentReceipt, error) {
	var out DeploymentReceipt
	claims, ok := authn.FromContext(ctx)
	if !ok || claims.SubjectType != authn.SubjectTypeServiceAccount || claims.TenantID != tenant || claims.EntityID != project || claims.PrincipalID <= 0 {
		return out, ErrWorkForbidden
	}
	// Discover and lock all future verifier participants before the project,
	// including the authenticated pipeline principal in the same sorted set.
	_, _, reviewers, err := m.lockAutomaticVerificationOn(ctx, tx, tenant, project, id, in.ResultID, claims.PrincipalID)
	if err != nil {
		return out, err
	}
	work, err := m.deploymentWorkOn(ctx, tx, tenant, project, id)
	if err != nil {
		return out, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if in.ResultID <= 0 || strings.TrimSpace(in.EventKey) == "" || len(in.EventKey) > 128 || strings.TrimSpace(in.Environment) == "" || len(in.Environment) > 128 || strings.TrimSpace(in.FixedVersion) == "" || len(in.FixedVersion) > 64 || in.OccurredAt.IsZero() || in.OccurredAt.After(time.Now().Add(time.Minute)) {
		return out, ErrInvalidArgument
	}
	artifact, e := hex.DecodeString(in.ArtifactHash)
	if e != nil || len(artifact) != sha256.Size || strings.ToLower(in.ArtifactHash) != in.ArtifactHash {
		return out, ErrInvalidArgument
	}
	d := query.BindDeployments(tx, m.service.names)
	prior, e := d.WithContext(ctx).Where(d.TenantID.Eq(tenant), d.ProjectID.Eq(project), d.ProducerID.Eq(actor), d.EventKey.Eq(in.EventKey)).First()
	if e == nil {
		if prior.WorkItemID != id || prior.PayloadHash != digestDeployment(in) {
			return out, ErrDefectDeployment
		}
		return DeploymentReceipt{ID: prior.ID, WorkItemID: id, ResultID: prior.ResultID, Version: work.Version, Stage: "DEPLOYED"}, nil
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return out, e
	}
	result, err := m.resultOn(ctx, tx, tenant, project, id, in.ResultID)
	if err != nil {
		return out, err
	}
	if work.CurrentClaimID == nil || *work.CurrentClaimID != result.ID || work.PausedAt != nil || work.BaselineStale || work.BaselineVersionNo != result.BaselineVersionNo || !isRuntimeDefect(work, result) || work.DefectStage == nil || *work.DefectStage != "MERGED" || in.Commit != result.HeadCommit {
		return out, ErrDefectDeployment
	}
	if err = m.requireNoAndonOn(ctx, tx, work); err != nil {
		return out, err
	}
	fact, err := m.trustedDefectMergeOn(ctx, tx, work, result)
	if err != nil {
		return out, err
	}
	next, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	record := &model.Deployment{TenantID: tenant, ID: guid.ID(next), ProjectID: project, WorkItemID: id, ResultID: result.ID, ProducerID: actor, EventKey: in.EventKey, Environment: in.Environment, Commit: in.Commit, ArtifactHash: in.ArtifactHash, MergeReceiptHash: fact.ReceiptHash, FixedVersion: in.FixedVersion, PayloadHash: digestDeployment(in), OccurredAt: in.OccurredAt.UTC(), CreatedAt: now}
	if err = d.WithContext(ctx).Create(record); err != nil {
		return out, err
	}
	before := work.State
	stage := "DEPLOYED"
	work.DefectStage = &stage
	work.FixedVersion = &in.FixedVersion
	w := query.BindWorkItems(tx, m.service.names)
	if _, err = w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id)).UpdateSimple(w.FixedVersion.Value(in.FixedVersion)); err != nil {
		return out, err
	}
	if work.State == "VERIFYING" {
		readyErr := m.verificationStageReadyOn(ctx, tx, work, result, "AGENT_TEST")
		if readyErr == nil {
			work.State = "INTERNAL_QA"
		} else if !errors.Is(readyErr, ErrVerificationPending) {
			return out, readyErr
		}
	}
	if err = m.resultWorkChangedOn(ctx, tx, work, 0, before, "DEFECT_DEPLOYED", "SYSTEM:deployment", result.ID, now); err != nil {
		return out, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.deployment_recorded", "work_item", id)
	event.Detail["deployment_id"] = record.ID.String()
	event.Detail["producer_id"] = actor.String()
	event.Detail["environment"] = in.Environment
	event.Detail["artifact_hash"] = in.ArtifactHash
	event.Detail["commit"] = in.Commit
	event.Detail["merge_receipt_hash"] = fact.ReceiptHash
	if err = m.service.audit(ctx, tx, event); err != nil {
		return out, err
	}
	if err = m.queueAutomaticAgentStagesOn(ctx, tx, work, result, reviewers); err != nil {
		return out, err
	}
	return DeploymentReceipt{ID: record.ID, WorkItemID: id, ResultID: result.ID, Version: work.Version, Stage: stage}, nil
}

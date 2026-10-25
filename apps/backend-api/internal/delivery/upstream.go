package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"io"
	"time"
)

var ErrUpstreamStale = errors.Join(ErrResultState, errors.New("delivery: upstream artifact is stale or inconclusive"))

type consumptionBody struct {
	Version            int      `json:"version" minimum:"1"`
	AcceptedArtifactID guid.ID  `json:"accepted_artifact_id"`
	ReplacesID         *guid.ID `json:"replaces_id,omitempty"`
	Reason             string   `json:"reason,omitempty" maxLength:"2000"`
}

func (m *Module) consumeArtifactOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner guid.ID, in consumptionBody) (model.ArtifactConsumption, error) {
	var out model.ArtifactConsumption
	_, work, _, err := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write")
	if err != nil {
		return out, err
	}
	if work.Version != in.Version {
		return out, ErrWorkVersion
	}
	e := query.BindResultEvidence(tx, m.service.names)
	evidence, err := e.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(e.TenantID.Eq(tenant), e.ProjectID.Eq(project), e.ID.Eq(in.AcceptedArtifactID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrProjectNotFound
	}
	if err != nil {
		return out, err
	}
	upstream, err := m.readWork(ctx, tx, tenant, project, evidence.WorkItemID, actor)
	if err != nil {
		return out, err
	}
	u := query.BindWorkItems(tx, m.service.names)
	upstreamRow, err := u.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(u.TenantID.Eq(tenant), u.ProjectID.Eq(project), u.ID.Eq(upstream.ID)).First()
	if err != nil {
		return out, err
	}
	upstream.WorkView = workRecord(upstreamRow)
	if upstream.State != "ACCEPTED" || upstream.CurrentClaimID == nil || *upstream.CurrentClaimID != evidence.ResultID || upstream.AcceptedDecisionID == nil || upstream.ID == id {
		return out, ErrResultState
	}
	d := query.BindDependencies(tx, m.service.names)
	count, err := d.WithContext(ctx).Where(d.TenantID.Eq(tenant), d.ProjectID.Eq(project), d.WorkItemID.Eq(id), d.DependsOnID.Eq(upstream.ID)).Count()
	if err != nil {
		return out, err
	}
	if count != 1 {
		return out, ErrInvalidArgument
	}
	table := query.BindArtifactConsumptions(tx, m.service.names).WithContext(ctx)
	err = table.Where("tenant_id = ? AND work_item_id = ? AND accepted_artifact_id = ?", tenant, id, evidence.ID).First(&out).Error
	if err == nil {
		return out, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	next, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	out = model.ArtifactConsumption{TenantID: tenant, ID: guid.ID(next), ProjectID: project, WorkItemID: id, UpstreamWorkItemID: upstream.ID, AcceptedArtifactID: evidence.ID, ResultID: evidence.ResultID, FileID: evidence.FileID, VersionID: evidence.VersionID, SHA256: evidence.SHA256, State: "CURRENT", CreatedBy: actor, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	state, _, err := m.consumptionApplicabilityOn(ctx, tx, &out)
	if err != nil {
		return out, err
	}
	if state != "CURRENT" {
		return out, ErrUpstreamStale
	}
	if in.ReplacesID != nil {
		reason, e := resultText(in.Reason)
		if e != nil || len([]rune(reason)) < 5 {
			return out, ErrInvalidArgument
		}
		var old model.ArtifactConsumption
		if e = table.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND project_id = ? AND work_item_id = ? AND id = ?", tenant, project, id, *in.ReplacesID).First(&old).Error; e != nil {
			return out, e
		}
		if old.UpstreamWorkItemID != out.UpstreamWorkItemID || old.AcceptedArtifactID == out.AcceptedArtifactID || old.State == "SUPERSEDED" {
			return out, ErrInvalidArgument
		}
		if e = table.Where("tenant_id = ? AND id = ?", tenant, old.ID).Update("state", "SUPERSEDED").Error; e != nil {
			return out, e
		}
		event := audit.NewDomainEvent(ctx, tenant, "delivery.upstream_consumption_superseded", "work_item", id)
		event.Detail["old_consumption_id"] = old.ID.String()
		event.Detail["replacement_consumption_id"] = out.ID.String()
		event.Detail["reason"] = reason
		if e = m.service.audit(ctx, tx, event); e != nil {
			return out, e
		}
	}
	if err = table.Create(&out).Error; err != nil {
		return out, err
	}
	if err = m.upstreamEffectOn(ctx, tx, &out, actor, "HUMAN", "CONSUMED"); err != nil {
		return out, err
	}
	return out, nil
}

func (m *Module) upstreamEffectOn(ctx context.Context, tx *gorm.DB, row *model.ArtifactConsumption, actor guid.ID, kind, state string) error {
	ev := audit.NewDomainEvent(ctx, row.TenantID, "delivery.upstream_artifact_changed", "work_item", row.WorkItemID)
	ev.Detail["consumption_id"] = row.ID.String()
	ev.Detail["state"] = state
	ev.Detail["actor_kind"] = kind
	if kind == "SYSTEM" {
		ev.PrincipalID = 0
	}
	if err := m.service.audit(ctx, tx, ev); err != nil {
		return err
	}
	_, err := m.service.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: "work_item.upstream_changed", Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: "artifact:reconciliation"}, Subject: events.Subject{Kind: "work_item", ID: row.WorkItemID}, Data: map[string]any{"consumption_id": row.ID.String(), "state": state}})
	return err
}

// Reconcile both the immutable stored bytes and the live head. A failed read is
// inconclusive, never a fabricated passing hash. Identical outcomes emit nothing.
func (m *Module) reconcileConsumptionOn(ctx context.Context, tx *gorm.DB, row *model.ArtifactConsumption, actor guid.ID, kind string) error {
	w := query.BindWorkItems(tx, m.service.names)
	if _, err := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(row.TenantID), w.ProjectID.Eq(row.ProjectID), w.ID.Eq(row.WorkItemID)).First(); err != nil {
		return err
	}
	var current model.ArtifactConsumption
	table := query.BindArtifactConsumptions(tx, m.service.names).WithContext(ctx)
	if err := table.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", row.TenantID, row.ID).First(&current).Error; err != nil {
		return err
	}
	if current.State == "SUPERSEDED" {
		return nil
	}
	state, observed, err := m.consumptionApplicabilityOn(ctx, tx, &current)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := table.Where("tenant_id = ? AND id = ?", row.TenantID, row.ID).Updates(map[string]any{"state": state, "observed_hash": observed, "checked_at": now}).Error; err != nil {
		return err
	}
	if state != current.State || observed != current.ObservedHash {
		current.State, current.ObservedHash = state, observed
		if err := m.upstreamEffectOn(ctx, tx, &current, actor, kind, state); err != nil {
			return err
		}
	}
	return nil
}

// Current-read traversal makes overlapping independent stale sources effective
// across every downstream edge without rewriting historical acceptance decisions.
func (m *Module) requireCurrentUpstreamOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem) error {
	seen := map[guid.ID]bool{}
	pending := []guid.ID{work.ID}
	d := query.BindDependencies(tx, m.service.names)
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		var rows []model.ArtifactConsumption
		if err := query.BindArtifactConsumptions(tx, m.service.names).WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND project_id = ? AND work_item_id = ?", work.TenantID, work.ProjectID, id).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if row.State == "SUPERSEDED" {
				continue
			}
			if row.State != "CURRENT" {
				return ErrUpstreamStale
			}
			state, _, e := m.consumptionApplicabilityOn(ctx, tx, &row)
			if e != nil {
				return e
			}
			if state != "CURRENT" {
				return ErrUpstreamStale
			}
		}
		edges, err := d.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(d.TenantID.Eq(work.TenantID), d.ProjectID.Eq(work.ProjectID), d.WorkItemID.Eq(id)).Find()
		if err != nil {
			return err
		}
		for _, edge := range edges {
			pending = append(pending, edge.DependsOnID)
		}
	}
	return nil
}

const artifactReconcileJob = "delivery.artifact_reconcile"

func (m *Module) artifactReconcileHandler() jobs.Handler {
	return jobs.Handler{Kind: artifactReconcileJob, Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalidArgument)
		}
		if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
			return ErrUnavailable
		}
		db := m.runtime.Database().Write()
		var rows []model.ArtifactConsumption
		if err := query.BindArtifactConsumptions(db, m.service.names).WithContext(ctx).Where("tenant_id = ? AND state <> ?", job.TenantID, "SUPERSEDED").Order("checked_at, id").Limit(100).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			// File access still runs under the original consumer's current authority;
			// revocation makes this verification inconclusive rather than bypassing IAM.
			claims := authn.WithClaims(ctx, &authn.Claims{TenantID: row.TenantID, EntityID: row.ProjectID, PrincipalID: row.CreatedBy, SubjectType: authn.SubjectTypePrincipal})
			err := db.WithContext(claims).Transaction(func(tx *gorm.DB) error {
				if m.principals.Lock == nil {
					return ErrUnavailable
				}
				if e := m.principals.Lock(claims, tx, row.CreatedBy); e != nil {
					return e
				}
				p := query.BindProjects(tx, m.service.names)
				if _, e := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(row.TenantID), p.ProjectID.Eq(row.ProjectID)).First(); e != nil {
					return e
				}
				return m.reconcileConsumptionOn(claims, tx, &row, 0, "SYSTEM")
			})
			if err != nil {
				return err
			}
		}
		return nil
	}}
}

func (m *Module) consumptionApplicabilityOn(ctx context.Context, tx *gorm.DB, current *model.ArtifactConsumption) (string, string, error) {
	e := query.BindResultEvidence(tx, m.service.names)
	evidence, err := e.WithContext(ctx).Where(e.TenantID.Eq(current.TenantID), e.ProjectID.Eq(current.ProjectID), e.ID.Eq(current.AcceptedArtifactID)).First()
	if err != nil {
		return "", "", err
	}
	state, observed := "INCONCLUSIVE", ""
	if m.resultFiles != nil {
		head, headErr := m.resultFiles.InspectOn(ctx, tx, current.FileID)
		pinned, openErr := m.resultFiles.OpenOn(ctx, tx, evidence.BindingID)
		if openErr == nil && pinned.Reader != nil {
			h := sha256.New()
			_, readErr := io.Copy(h, pinned.Reader)
			closeErr := pinned.Reader.Close()
			if readErr == nil && closeErr == nil && headErr == nil {
				observed = head.SHA256
				state = "CURRENT"
				if hex.EncodeToString(h.Sum(nil)) != current.SHA256 || head.SHA256 != current.SHA256 {
					state = "STALE"
				}
			}
		}
	}

	return state, observed, nil
}

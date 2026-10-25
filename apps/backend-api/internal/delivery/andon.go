package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAndonBlocked = errors.Join(ErrResultState, errors.New("delivery: ANDON_UPSTREAM"))

type andonBody struct {
	Version int    `json:"version" minimum:"1"`
	Reason  string `json:"reason" minLength:"5" maxLength:"2000"`
}

type andonView struct {
	Work WorkView `json:"work"`
	// A durable control-plane hold is not a runner acknowledgement.
	Executions  []ExecutionHoldFact `json:"executions"`
	RunnerPause string              `json:"runner_pause" enum:"NOT_APPLICABLE,UNAVAILABLE,REQUESTED,ACKNOWLEDGED"`
}

// Project locking serializes dependency edits, pull/clear and execution starts.
// Traverse current upstream rows rather than materializing a second pause on
// dependents: overlapping pulls and unrelated holds then clear independently.
func (m *Module) requireNoAndonOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem) error {
	seen := map[guid.ID]bool{}
	pending := []guid.ID{work.ID}
	w := query.BindWorkItems(tx, m.service.names)
	d := query.BindDependencies(tx, m.service.names)
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		row, err := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(work.TenantID), w.ProjectID.Eq(work.ProjectID), w.ID.Eq(id)).First()
		if err != nil {
			return err
		}
		if andonActive(row) {
			return ErrAndonBlocked
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

func (m *Module) andonAuthorityOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, actor guid.ID, release bool) (string, error) {
	check := m.deliveryAuthority
	if m.andonAuthority != nil {
		check = governance.DeliveryAuthority(m.andonAuthority)
	}
	if check == nil {
		return "", ErrUnavailable
	}
	code := "andon_pull"
	if release {
		code = "andon_clear"
	}
	kind, err := m.resultActorKindOn(ctx, tx, actor, "")
	if err != nil {
		return "", err
	}
	if release && kind != "HUMAN" {
		return "", ErrWorkForbidden
	}
	a, err := check(ctx, tx, governance.AuthorityRequest{TenantID: work.TenantID, ProjectID: work.ProjectID, PrincipalID: actor, ActorKind: kind, Code: code, Resource: governance.ResourceRef{Kind: "work_item", ID: work.ID}, Facts: &governance.WorkItemFacts{Type: work.Type, Risk: work.Risk, Paused: work.PausedAt != nil}})
	if err != nil {
		return "", err
	}
	if !a.Allowed || a.AuthoritySource == "" {
		return "", ErrWorkForbidden
	}
	return a.AuthoritySource, nil
}

func (m *Module) changeAndonOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner guid.ID, in andonBody, release bool) (andonView, error) {
	var out andonView
	reason := strings.TrimSpace(in.Reason)
	if !utf8.ValidString(reason) || utf8.RuneCountInString(reason) < 5 || utf8.RuneCountInString(reason) > 2000 || in.Version < 1 {
		return out, ErrInvalidArgument
	}
	verifyFence, err := m.holdPrincipalFenceOn(ctx, tx, tenant, project, id, actor, owner)
	if err != nil {
		return out, err
	}
	_, work, _, err := m.lockedWorkActorOn(ctx, tx, tenant, project, id, actor, owner, "work_item_read", !release)
	if err != nil {
		return out, err
	}
	if err = verifyFence(); err != nil {
		return out, err
	}
	source, err := m.andonAuthorityOn(ctx, tx, work, actor, release)
	if err != nil {
		return out, err
	}
	if work.Version != in.Version {
		return out, ErrWorkVersion
	}
	if work.State == "ACCEPTED" || work.State == "CANCELLED" { //nolint:misspell // Frozen protocol state.
		return out, ErrResultState
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	action := "ANDON_PULLED"
	var blocks []string
	if json.Unmarshal([]byte(work.BlockReasons), &blocks) != nil {
		return out, ErrUnavailable
	}
	if release {
		if !andonActive(work) {
			return out, ErrResultState
		}
		work.PausedAt, work.PauseReason = nil, nil
		blocks = slices.DeleteFunc(blocks, func(v string) bool { return v == "ANDON" })
		action = "ANDON_CLEARED"
	} else {
		if work.PausedAt != nil {
			return out, ErrResultState
		}
		hold := "MANUAL"
		work.PausedAt, work.PauseReason = &now, &hold
		blocks = append(blocks, "ANDON")
	}
	data, _ := json.Marshal(blocks)
	work.BlockReasons = string(data)
	// No work-state transition or runtime restart is implied by clearing the hold.
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, work.State, action, source, 0, now); err != nil {
		return out, err
	}
	out = andonView{Work: workRecord(work), RunnerPause: "NOT_APPLICABLE"}
	if work.CurrentExecutionID != nil {
		out.RunnerPause = "UNAVAILABLE"
	}
	if m.executionHolds.ChangeOn != nil {
		ids, e := m.affectedAndonWorksOn(ctx, tx, tenant, project, id)
		if e != nil {
			return andonView{}, e
		}
		q := query.BindWorkItems(tx, m.service.names)
		for _, affected := range ids {
			row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(affected)).First()
			if e != nil {
				return andonView{}, e
			}
			releaseRuntime := release && row.PausedAt == nil && m.noProjectAndonOn(ctx, tx, tenant, project)
			if releaseRuntime {
				if e = m.requireNoAndonOn(ctx, tx, row); e != nil {
					if !errors.Is(e, ErrAndonBlocked) {
						return andonView{}, e
					}
					releaseRuntime = false
				}
			}
			facts, e := m.executionHolds.ChangeOn(ctx, tx, tenant, project, affected, releaseRuntime)
			if e != nil {
				return andonView{}, e
			}
			out.Executions = append(out.Executions, facts...)
		}
		if len(out.Executions) > 0 {
			out.RunnerPause = "REQUESTED"
			allPaused := true
			for _, f := range out.Executions {
				if f.Control != "PAUSED" {
					allPaused = false
				}
			}
			if allPaused {
				out.RunnerPause = "ACKNOWLEDGED"
			}
		}
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.andon_disposition", "work_item", id)
	event.Detail["reason"], event.Detail["action"], event.Detail["runner_pause"] = reason, action, out.RunnerPause
	if err = m.service.audit(ctx, tx, event); err != nil {
		return andonView{}, err
	}
	// Link the public lifecycle to the immutable transition that opened this
	// pull, including on clear. Free text never substitutes for episode identity.
	pulls := query.BindWorkTransitions(tx, m.service.names)
	pull, err := pulls.WithContext(ctx).Where(pulls.TenantID.Eq(tenant), pulls.ProjectID.Eq(project), pulls.WorkItemID.Eq(id), pulls.ReasonCode.Eq("ANDON_PULLED")).Order(pulls.VersionAfter.Desc()).First()
	if err != nil {
		return andonView{}, err
	}
	typ := "andon.pulled"
	if release {
		typ = "andon.cleared"
	}
	kind, err := m.resultActorKindOn(ctx, tx, actor, source)
	if err != nil {
		return andonView{}, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: typ, Subject: events.Subject{Kind: "work_item", ID: id}, SubjectVersion: int64(work.Version), Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: source}, Data: map[string]any{"pull_id": pull.ID.String(), "target_kind": "WORK_ITEM", "target_id": id.String(), "reason": reason, "runner_pause": out.RunnerPause}})
	if err != nil {
		return andonView{}, err
	}
	return out, nil
}

// Holds propagate through current dependency edges; no duplicated pause flags
// are written onto downstream work, so overlapping pulls clear independently.
func (m *Module) affectedAndonWorksOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) ([]guid.ID, error) {
	q := query.BindDependencies(tx, m.service.names)
	edges, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Find()
	if err != nil {
		return nil, err
	}
	seen := map[guid.ID]bool{id: true}
	pending := []guid.ID{id}
	for i := 0; i < len(pending); i++ {
		for _, edge := range edges {
			if edge.DependsOnID == pending[i] && !seen[edge.WorkItemID] {
				seen[edge.WorkItemID] = true
				pending = append(pending, edge.WorkItemID)
			}
		}
	}
	slices.Sort(pending)
	return pending, nil
}

// Andon is a named manual hold; the existing extensible block list stores its
// subtype without inventing a new pause enum or a decision/runner receipt.
func andonActive(work *model.WorkItem) bool {
	var blocks []string
	return work.PausedAt != nil && work.PauseReason != nil && *work.PauseReason == "MANUAL" && json.Unmarshal([]byte(work.BlockReasons), &blocks) == nil && slices.Contains(blocks, "ANDON")
}

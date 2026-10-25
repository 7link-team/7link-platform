package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrConstitutionTooLarge  = errors.New("delivery: constitution exceeds64KiB")
	ErrConstitutionState     = errors.New("delivery: constitution state does not allow this command")
	ErrConstitutionVersion   = errors.New("delivery: constitution version conflict")
	ErrConstitutionHumanOnly = errors.New("delivery: constitution review requires a Human")
)

type constitutionProposalBody struct {
	Body          string `json:"body" minLength:"1" doc:"Markdown, at most 65536 UTF-8 bytes after LF normalization and surrounding whitespace removal."`
	ChangeSummary string `json:"change_summary" minLength:"1" maxLength:"2000"`
}
type constitutionDecisionBody struct {
	Result  string `json:"result" enum:"APPROVE,REJECT"`
	Comment string `json:"comment,omitempty" maxLength:"2000"`
}

// ConstitutionView excludes the document body so list/replay responses stay bounded.
type ConstitutionView struct {
	ID             guid.ID    `json:"id"`
	ProjectID      guid.ID    `json:"project_id"`
	VersionNo      int        `json:"version_no"`
	BodySHA256     string     `json:"body_sha256"`
	State          string     `json:"state"`
	ChangeSummary  string     `json:"change_summary"`
	ProposedByKind string     `json:"proposed_by_kind"`
	ProposedBy     guid.ID    `json:"proposed_by"`
	ReviewedBy     *guid.ID   `json:"reviewed_by"`
	ReviewedAt     *time.Time `json:"reviewed_at"`
	ReviewComment  *string    `json:"review_comment"`
	Version        int        `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
type constitutionDetailView struct {
	ConstitutionView
	Body string `json:"body"`
}

func constitutionRecord(row *model.ProjectConstitution) ConstitutionView {
	return ConstitutionView{ID: row.ID, ProjectID: row.ProjectID, VersionNo: row.VersionNo, BodySHA256: row.BodySHA256, State: row.State, ChangeSummary: row.ChangeSummary, ProposedByKind: row.ProposedByKind, ProposedBy: row.ProposedBy, ReviewedBy: row.ReviewedBy, ReviewedAt: row.ReviewedAt, ReviewComment: row.ReviewComment, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func validateConstitutionProposal(body constitutionProposalBody) (constitutionProposalBody, error) {
	body.Body = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(body.Body, "\r\n", "\n"), "\r", "\n"))
	body.ChangeSummary = strings.TrimSpace(body.ChangeSummary)
	if len(body.Body) > 65536 {
		return body, ErrConstitutionTooLarge
	}
	if !utf8.ValidString(body.Body) || body.Body == "" || body.ChangeSummary == "" || !utf8.ValidString(body.ChangeSummary) || utf8.RuneCountInString(body.ChangeSummary) > 2000 {
		return body, ErrInvalidArgument
	}
	return body, nil
}

// The actor lock and IAM fence precede every business row read. Project scope
// belongs to the project creator, not the proposer of a particular document.
func (m *Module) constitutionProjectOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, permission string) (*model.Project, string, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || actor <= 0 || authn.PrincipalIDFromContext(ctx) != actor {
		return nil, "", ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, "", ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.workPolicy == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.members == nil || m.ids == nil || m.service.audit == nil || m.service.events == nil {
		return nil, "", ErrUnavailable
	}
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return nil, "", err
	}
	c, err := m.workPolicy(ctx, tx, tenant, project, permission, actor)
	if err != nil {
		return nil, "", err
	}
	if !c.AllowsEntityRows(project) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, project.String()) && len(c.OwnerIDs) == 0) {
		return nil, "", ErrWorkForbidden
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return nil, "", err
	}
	kind := "HUMAN"
	if !active || human.ID != actor {
		if permission == "project_manage" {
			return nil, "", ErrConstitutionHumanOnly
		}
		if m.agents.ByPrincipalOn == nil {
			return nil, "", ErrUnavailable
		}
		agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
		if e != nil {
			return nil, "", e
		}
		if !found || agent.TenantID != tenant || agent.PrincipalID != actor || agent.ID <= 0 || agent.State != "ACTIVE" {
			return nil, "", ErrInvalidMember
		}
		kind = "AGENT"
	}
	member, err := m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return nil, "", err
	}
	if !member {
		return nil, "", ErrInvalidMember
	}
	p := query.BindProjects(tx, m.service.names)
	scope, err := workScope(c, project, p.CreatedBy)
	if err != nil {
		return nil, "", err
	}
	base := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	row, err := base.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", ErrProjectNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if row.Stage == "CLOSED" || row.Stage == "CANCELLED" { //nolint:misspell // Frozen business state.
		return nil, "", ErrWorkProjectReadOnly
	}
	return row, kind, nil
}

func (m *Module) proposeConstitutionOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, input constitutionProposalBody) (ConstitutionView, error) {
	var empty ConstitutionView
	input, err := validateConstitutionProposal(input)
	if err != nil {
		return empty, err
	}
	_, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "project_read")
	if err != nil {
		return empty, err
	}
	q := query.BindConstitutions(tx, m.service.names)
	latest, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Order(q.VersionNo.Desc()).First()
	versionNo := 1
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, err
	}
	if err == nil {
		if latest.VersionNo >= 2147483647 {
			return empty, ErrConstitutionVersion
		}
		versionNo = latest.VersionNo + 1
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC()
	hash := sha256.Sum256([]byte(input.Body))
	row := &model.ProjectConstitution{TenantID: tenant, ID: guid.ID(id), ProjectID: project, VersionNo: versionNo, Body: input.Body, BodySHA256: hex.EncodeToString(hash[:]), State: "DRAFT", ChangeSummary: input.ChangeSummary, ProposedByKind: kind, ProposedBy: actor, CreatedAt: now, UpdatedAt: now, Version: 1}
	if err = q.WithContext(ctx).Create(row); err != nil {
		return empty, err
	}
	if err = m.constitutionEventOn(ctx, tx, row, actor, kind, "proposed", "permission:project_read"); err != nil {
		return empty, err
	}
	return constitutionRecord(row), nil
}

func (m *Module) changeConstitutionOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor guid.ID, expected int, decision *constitutionDecisionBody) (ConstitutionView, error) {
	var empty ConstitutionView
	permission := "project_read"
	if expected < 1 {
		return empty, ErrInvalidArgument
	}
	if decision != nil {
		v := *decision
		v.Comment = strings.TrimSpace(v.Comment)
		decision = &v
		if !slices.Contains([]string{"APPROVE", "REJECT"}, v.Result) || !utf8.ValidString(v.Comment) || utf8.RuneCountInString(v.Comment) > 2000 || (v.Result == "REJECT" && v.Comment == "") {
			return empty, ErrInvalidArgument
		}
		permission = "project_manage"
	}
	projectRow, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, permission)
	if err != nil {
		return empty, err
	}
	q := query.BindConstitutions(tx, m.service.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrProjectNotFound
	}
	if err != nil {
		return empty, err
	}
	if row.Version != expected {
		return empty, ErrConstitutionVersion
	}
	now := time.Now().UTC()
	event := "submitted"
	if decision == nil {
		if row.ProposedBy != actor || row.ProposedByKind != kind {
			return empty, ErrWorkForbidden
		}
		if row.State != "DRAFT" {
			return empty, ErrConstitutionState
		}
		row.State = "IN_REVIEW"
	} else {
		if row.State != "IN_REVIEW" {
			return empty, ErrConstitutionState
		}
		row.ReviewedBy = &actor
		row.ReviewedAt = &now
		row.ReviewComment = &decision.Comment
		row.State = "REJECTED"
		event = "rejected"
		if decision.Result == "APPROVE" {
			// The project lock serializes all its proposals/reviews. Releasing the
			// previous unique slot and allocating the new one share this transaction.
			if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("ACTIVE")).UpdateSimple(q.State.Value("SUPERSEDED"), q.ActiveKey.Null(), q.Version.Add(1), q.UpdatedAt.Value(now)); err != nil {
				return empty, err
			}
			row.State = "ACTIVE"
			row.ActiveKey = &project
			event = "activated"
			p := query.BindProjects(tx, m.service.names)
			result, e := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.Version.Eq(projectRow.Version)).UpdateSimple(p.ConstitutionVersionNo.Value(row.VersionNo), p.Version.Add(1), p.UpdatedAt.Value(now), p.UpdatedBy.Value(actor))
			if e != nil {
				return empty, e
			}
			if result.RowsAffected != 1 {
				return empty, ErrConstitutionVersion
			}
		}
	}
	row.Version++
	row.UpdatedAt = now
	updates := []field.AssignExpr{q.State.Value(row.State), q.Version.Value(row.Version), q.UpdatedAt.Value(now)}
	if decision != nil {
		updates = append(updates, q.ReviewedBy.Value(actor), q.ReviewedAt.Value(now), q.ReviewComment.Value(decision.Comment))
	}
	if row.ActiveKey != nil {
		updates = append(updates, q.ActiveKey.Value(*row.ActiveKey))
	} else {
		updates = append(updates, q.ActiveKey.Null())
	}
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id), q.Version.Eq(expected)).UpdateSimple(updates...)
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrConstitutionVersion
	}
	if err = m.constitutionEventOn(ctx, tx, row, actor, kind, event, "permission:"+permission); err != nil {
		return empty, err
	}
	return constitutionRecord(row), nil
}

func (m *Module) constitutionEventOn(ctx context.Context, tx *gorm.DB, row *model.ProjectConstitution, actor guid.ID, kind, event, source string) error {
	a := audit.NewDomainEvent(ctx, row.TenantID, "delivery.constitution_"+event, "project_constitution", row.ID)
	a.Detail["project_id"] = row.ProjectID.String()
	a.Detail["version_no"] = row.VersionNo
	a.Detail["body_sha256"] = row.BodySHA256
	a.Detail["authority_source"] = source
	if err := m.service.audit(ctx, tx, a); err != nil {
		return err
	}
	_, err := m.service.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: "project.constitution_" + event, Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "project_constitution", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"version_no": row.VersionNo, "sha256": row.BodySHA256, "state": row.State}})
	return err
}

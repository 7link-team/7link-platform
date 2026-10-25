package performance

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/performance/model"
	"7link.com/platform/internal/performance/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("performance: invalid configuration")
	ErrForbidden   = errors.New("performance: owner Human required")
	ErrUnavailable = errors.New("performance: unavailable")
	ErrConflict    = errors.New("performance: revision conflict")
	ErrNotFound    = errors.New("performance: scheme not found")
)

type Condition struct {
	Metric    string `json:"metric" enum:"QUALITY_SCORE,ON_TIME_RATIO,PROGRESS_RATIO,REWORK_COUNT,ADOPTION_SCORE,DOWNSTREAM_VALUE_SCORE,RISK_RESPONSE_SCORE,REVIEW_SCORE"`
	Operator  string `json:"operator" enum:"GTE,LTE,EQ"`
	Threshold int64  `json:"threshold" minimum:"0" maximum:"1000000000"`
}
type Rule struct {
	Key           string    `json:"key" minLength:"1" maxLength:"64"`
	Description   string    `json:"description" minLength:"1" maxLength:"1000"`
	Condition     Condition `json:"condition"`
	Effect        string    `json:"effect" enum:"ADD,SUBTRACT"`
	Points        int64     `json:"points" minimum:"1" maximum:"1000000000"`
	EvidenceKinds []string  `json:"evidence_kinds" minItems:"1" maxItems:"8"`
}
type Dimension struct {
	Key           string `json:"key" minLength:"1" maxLength:"64"`
	Title         string `json:"title" minLength:"1" maxLength:"200"`
	WeightBPS     int    `json:"weight_bps" minimum:"1" maximum:"10000" doc:"Exact weight in basis points; dimension weights sum to 10000."`
	MaximumPoints int64  `json:"maximum_points" minimum:"1" maximum:"1000000000"`
	Rules         []Rule `json:"rules" minItems:"1" maxItems:"50"`
}
type ReportTemplate struct {
	Title          string   `json:"title" minLength:"1" maxLength:"200"`
	Summary        string   `json:"summary" maxLength:"2000"`
	DimensionOrder []string `json:"dimension_order" minItems:"1" maxItems:"30"`
}
type SchemeDefinition struct {
	Schedule    *GenerationSchedule `json:"schedule,omitempty"`
	Name        string              `json:"name" minLength:"1" maxLength:"200"`
	SubjectKind string              `json:"subject_kind" enum:"HUMAN,AGENT,PROJECT,TEAM"`
	Scope       string              `json:"scope" minLength:"1" maxLength:"1000" doc:"Explicit applicability and role/cohort criteria; report generation must resolve and validate actual subjects separately."`
	SubjectIDs  []guid.ID           `json:"subject_ids" maxItems:"200" doc:"Optional explicit subject selection; empty selects the declared cohort. Generation must resolve current subject kind, tenant and scope before collecting any evidence."`
	Enabled     bool                `json:"enabled"`
	Dimensions  []Dimension         `json:"dimensions" minItems:"1" maxItems:"30"`
	Template    ReportTemplate      `json:"template"`
}
type RevisionView struct {
	ID         guid.ID          `json:"id"`
	SchemeID   guid.ID          `json:"scheme_id"`
	Version    int              `json:"version"`
	Definition SchemeDefinition `json:"definition"`
	SHA256     string           `json:"sha256"`
	Reason     string           `json:"reason"`
	CreatedBy  guid.ID          `json:"created_by"`
	CreatedAt  time.Time        `json:"created_at"`
}

func textOK(value string, maximum int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= maximum
}
func (d SchemeDefinition) validate() error {
	if d.Schedule != nil {
		if err := d.Schedule.validate(); err != nil {
			return err
		}
		if len(d.SubjectIDs) == 0 {
			return ErrInvalid
		}
	}
	if !textOK(d.Name, 200) || !textOK(d.Scope, 1000) || !slices.Contains([]string{"HUMAN", "AGENT", "PROJECT", "TEAM"}, d.SubjectKind) || len(d.Dimensions) < 1 || len(d.Dimensions) > 30 || !textOK(d.Template.Title, 200) || utf8.RuneCountInString(d.Template.Summary) > 2000 {
		return ErrInvalid
	}
	if len(d.SubjectIDs) > 200 {
		return ErrInvalid
	}
	subjects := map[guid.ID]bool{}
	for _, id := range d.SubjectIDs {
		if id <= 0 || subjects[id] {
			return ErrInvalid
		}
		subjects[id] = true
	}

	keys := map[string]bool{}
	weights := 0
	for _, dim := range d.Dimensions {
		if !textOK(dim.Key, 64) || keys[dim.Key] || !textOK(dim.Title, 200) || dim.WeightBPS < 1 || dim.WeightBPS > 10000 || dim.MaximumPoints < 1 || dim.MaximumPoints > 1000000000 || len(dim.Rules) < 1 || len(dim.Rules) > 50 {
			return ErrInvalid
		}
		keys[dim.Key] = true
		weights += dim.WeightBPS
		rules := map[string]bool{}
		for _, rule := range dim.Rules {
			if !textOK(rule.Key, 64) || rules[rule.Key] || !textOK(rule.Description, 1000) || !slices.Contains([]string{"ADD", "SUBTRACT"}, rule.Effect) || rule.Points < 1 || rule.Points > 1000000000 || !slices.Contains([]string{"QUALITY_SCORE", "ON_TIME_RATIO", "PROGRESS_RATIO", "REWORK_COUNT", "ADOPTION_SCORE", "DOWNSTREAM_VALUE_SCORE", "RISK_RESPONSE_SCORE", "REVIEW_SCORE"}, rule.Condition.Metric) || !slices.Contains([]string{"GTE", "LTE", "EQ"}, rule.Condition.Operator) || rule.Condition.Threshold < 0 || rule.Condition.Threshold > 1000000000 || len(rule.EvidenceKinds) < 1 || len(rule.EvidenceKinds) > 8 {
				return ErrInvalid
			}
			rules[rule.Key] = true
			seen := map[string]bool{}
			for _, kind := range rule.EvidenceKinds {
				if seen[kind] || !slices.Contains([]string{"WORK_ITEM", "RESULT", "PLAN", "GOAL", "DECISION", "REPORT", "HUMAN_REVIEW", "DOWNSTREAM_RECEIPT"}, kind) {
					return ErrInvalid
				}
				seen[kind] = true
			}
		}
	}
	if weights != 10000 || len(d.Template.DimensionOrder) != len(keys) {
		return ErrInvalid
	}
	for _, key := range d.Template.DimensionOrder {
		if !keys[key] {
			return ErrInvalid
		}
		delete(keys, key)
	}
	return nil
}
func (m *Module) authorizeOn(ctx context.Context, tx *gorm.DB) error {
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	if tenant <= 0 || actor <= 0 {
		return ErrForbidden
	}
	if tx == nil || tx.Statement == nil {
		return ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	if m.policy == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil {
		return ErrUnavailable
	}
	if e := m.principals.Lock(ctx, tx, actor); e != nil {
		return e
	}
	human, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return e
	}
	if !active || human.ID != actor {
		return ErrForbidden
	}
	// tenant_administer is the canonical current tenant-owner authority. A grant
	// of performance_manage alone cannot confer owner configuration rights.
	c, e := m.policy(ctx, tx, tenant, "tenant_administer", actor)
	if e != nil {
		return e
	}
	if !c.AllowAll {
		return ErrForbidden
	}
	return nil
}
func revisionView(r *model.Revision) (RevisionView, error) {
	var d SchemeDefinition
	if e := json.Unmarshal([]byte(r.Definition), &d); e != nil {
		return RevisionView{}, ErrUnavailable
	}
	sum := sha256.Sum256([]byte(r.Definition))
	if hex.EncodeToString(sum[:]) != r.SHA256 {
		return RevisionView{}, ErrUnavailable
	}
	return RevisionView{ID: r.ID, SchemeID: r.SchemeID, Version: r.Version, Definition: d, SHA256: r.SHA256, Reason: r.Reason, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}, nil
}
func (m *Module) schemeOn(ctx context.Context, tx *gorm.DB, id guid.ID) (*model.Scheme, error) {
	q := query.BindSchemes(tx, m.names)
	row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return row, e
}
func (m *Module) reviseOn(ctx context.Context, tx *gorm.DB, id guid.ID, base int, d SchemeDefinition, reason string) (RevisionView, error) {
	if e := m.authorizeOn(ctx, tx); e != nil {
		return RevisionView{}, e
	}
	if e := d.validate(); e != nil {
		return RevisionView{}, e
	}
	if !textOK(reason, 2000) || base < 0 || base >= 2147483647 || id < 0 || (id == 0) != (base == 0) {
		return RevisionView{}, ErrInvalid
	}
	if m.ids == nil || m.audit == nil || m.events == nil {
		return RevisionView{}, ErrUnavailable
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	now := time.Now().UTC().Truncate(time.Millisecond)
	q := query.BindSchemes(tx, m.names)
	if id == 0 {
		next, e := m.ids.Next()
		if e != nil {
			return RevisionView{}, e
		}
		id = guid.ID(next)
		if e = q.WithContext(ctx).Create(&model.Scheme{ID: id, TenantID: tenant, Version: 1, Enabled: d.Enabled, CreatedAt: now, UpdatedAt: now}); e != nil {
			return RevisionView{}, e
		}
	} else {
		current, e := m.schemeOn(ctx, tx, id)
		if e != nil {
			return RevisionView{}, e
		}
		if current.Version != base {
			return RevisionView{}, ErrConflict
		}
		info, e := q.WithContext(ctx).Where(q.ID.Eq(id), q.TenantID.Eq(tenant), q.Version.Eq(base)).UpdateSimple(q.Version.Value(base+1), q.Enabled.Value(d.Enabled), q.UpdatedAt.Value(now))
		if e != nil {
			return RevisionView{}, e
		}
		if info.RowsAffected != 1 {
			return RevisionView{}, ErrConflict
		}
	}
	next, e := m.ids.Next()
	if e != nil {
		return RevisionView{}, e
	}
	body, e := json.Marshal(d)
	if e != nil {
		return RevisionView{}, e
	}
	sum := sha256.Sum256(body)
	row := model.Revision{ID: guid.ID(next), TenantID: tenant, SchemeID: id, Version: base + 1, Definition: string(body), SHA256: hex.EncodeToString(sum[:]), Reason: reason, CreatedBy: actor, CreatedAt: now}
	if e = query.BindRevisions(tx, m.names).WithContext(ctx).Create(&row); e != nil {
		return RevisionView{}, e
	}
	detail := map[string]any{"reason": reason, "authority_source": "ROLE:tenant_administrator", "evidence_refs": []string{}, "revision_id": row.ID.String(), "sha256": row.SHA256, "version": row.Version, "enabled": d.Enabled, "subject_kind": d.SubjectKind}
	event := audit.NewDomainEvent(ctx, tenant, "performance.scheme_revised", "performance_scheme", id)
	for key, value := range detail {
		event.Detail[key] = value
	}
	if e = m.audit(ctx, tx, event); e != nil {
		return RevisionView{}, e
	}
	_, e = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "performance.scheme_revised", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "ROLE:tenant_administrator"}, Subject: events.Subject{Kind: "performance_scheme", ID: id}, SubjectVersion: int64(row.Version), Data: detail})
	if e != nil {
		return RevisionView{}, e
	}
	return revisionView(&row)
}

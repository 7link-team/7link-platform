package governance

import (
	governancei18n "7link.com/platform/internal/governance/i18n"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"time"
)

type escapeSummaryInput struct {
	TenantID  string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ProjectID string `query:"project_id" required:"true" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type QualityEscapeSummary struct {
	ProjectID          guid.ID   `json:"project_id"`
	WindowStart        time.Time `json:"window_start"`
	WindowEnd          time.Time `json:"window_end"`
	EscapedProblems    int64     `json:"escaped_problems"`
	EnteredHumanReview int       `json:"entered_human_review"`
	Rate               *float64  `json:"rate" doc:"Escaped problems divided by distinct readable work entering Human review in the same UTC calendar week. Null when the denominator is zero."`
}

func (m *Module) qualityEscapeSummaryHTTP(ctx context.Context, in *escapeSummaryInput) (*respx.Body[QualityEscapeSummary], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, escapeHTTPError(ctx, ErrInvalidArgument)
	}
	project, err := guid.RequireID(in.ProjectID)
	if err != nil {
		return nil, escapeHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, escapeHTTPError(ctx, err)
	}
	now := m.seed.now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	start := day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	out := QualityEscapeSummary{ProjectID: project, WindowStart: start, WindowEnd: start.AddDate(0, 0, 7)}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.escapeReadAuthorityOn(ctx, tx, tenant, project); e != nil {
			return e
		}
		if m.qualityReviews == nil {
			return ErrUnavailable
		}
		reviews, e := m.qualityReviews(ctx, tx, tenant, project, out.WindowStart, out.WindowEnd)
		if e != nil {
			return e
		}
		if reviews.Entered < 0 {
			return ErrUnavailable
		}
		out.EnteredHumanReview = reviews.Entered
		if len(reviews.WorkIDs) == 0 {
			return nil
		}
		ids := make([]driver.Valuer, 0, len(reviews.WorkIDs))
		for _, id := range reviews.WorkIDs {
			ids = append(ids, id)
		}
		q := query.BindQualityEscapes(tx, m.seed.names)
		out.EscapedProblems, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.In(ids...), q.CreatedAt.Gte(out.WindowStart), q.CreatedAt.Lt(out.WindowEnd)).Count()
		if e == nil && out.EnteredHumanReview > 0 {
			rate := float64(out.EscapedProblems) / float64(out.EnteredHumanReview)
			out.Rate = &rate
		}
		return e
	})
	if err != nil {
		return nil, escapeHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

type escapeListInput struct {
	TenantID      string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ProjectID     string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Stage         string `query:"stage" enum:"HUMAN_REVIEW,CLIENT_ACCEPTANCE"`
	LearningState string `query:"learning_state" enum:"PENDING,RECORDED,DISMISSED"`
	Category      string `query:"category" maxLength:"64"`
	Limit         int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset        int    `query:"offset" default:"0" minimum:"0"`
}
type escapeRefInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"escape_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type escapeDecisionInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"escape_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch  string `header:"If-Match" required:"true"`
	Body     struct {
		State     string                       `json:"state" enum:"RECORDED,DISMISSED"`
		Reason    string                       `json:"reason,omitempty" maxLength:"2000"`
		RuleDraft *knowledge.LearningRuleDraft `json:"rule_draft,omitempty" doc:"Optional human-authored rule; creates a pending Knowledge review, never activates it."`
	}
}
type escapeDecisionOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[QualityEscapeView]
}

func (m *Module) registerQualityEscapeHTTP(api huma.API) {
	// The domain reader checks current tenant/project audit authority. Retain the
	// route's interface scope for PATs before the registrar authenticates them.
	readOp := func(op huma.Operation) huma.Operation {
		op.Middlewares = append(op.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
			tenant, _ := guid.Parse(ctx.Param("tenant_id"))
			next(huma.WithContext(ctx, authn.WithPersonalTokenScope(ctx.Context(), tenant, "audit_read")))
		})
		return op
	}
	authz.RegisterTenantMember(m.registrar, api, readOp(huma.Operation{OperationID: "governance.quality-escapes.summary", Method: http.MethodGet, Path: "/quality-escape-summary", Summary: "Read the current UTC week project escape rate from source facts", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}), m.qualityEscapeSummaryHTTP)

	authz.RegisterTenantMember(m.registrar, api, readOp(huma.Operation{OperationID: "governance.quality-escapes.list", Method: http.MethodGet, Path: "/quality-escapes", Summary: "List source-backed quality escapes", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}), m.listQualityEscapesHTTP)
	authz.RegisterTenantMember(m.registrar, api, readOp(huma.Operation{OperationID: "governance.quality-escape.get", Method: http.MethodGet, Path: "/quality-escapes/{escape_id}", Summary: "Read immutable gate facts and learning draft references", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}), m.getQualityEscapeHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.quality-escape.learning", Method: http.MethodPut, Path: "/quality-escapes/{escape_id}/learning", Summary: "Confirm or dismiss escape learning as a human", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "policy", Verb: "manage"}, m.decideQualityEscapeHTTP)
}
func (m *Module) listQualityEscapesHTTP(ctx context.Context, in *escapeListInput) (*respx.Body[[]QualityEscapeView], error) {
	if in.Limit < 1 || in.Limit > 200 || in.Offset < 0 {
		return nil, escapeHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, escapeHTTPError(ctx, ErrInvalidArgument)
	}
	var project guid.ID
	if in.ProjectID != "" {
		project, e = guid.RequireID(in.ProjectID)
		if e != nil {
			return nil, escapeHTTPError(ctx, ErrInvalidArgument)
		}
	}
	db, e := m.writer()
	if e != nil {
		return nil, escapeHTTPError(ctx, e)
	}
	items := []QualityEscapeView{}
	var total int64
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.escapeReadAuthorityOn(ctx, tx, tenant, project); e != nil {
			return e
		}
		q := query.BindQualityEscapes(tx, m.seed.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(tenant))
		if project > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(project))
		}
		if in.Stage != "" {
			stmt = stmt.Where(q.Stage.Eq(in.Stage))
		}
		if in.LearningState != "" {
			stmt = stmt.Where(q.LearningState.Eq(in.LearningState))
		}
		if in.Category != "" {
			stmt = stmt.Where(q.Category.Eq(in.Category))
		}
		var e error
		total, e = stmt.Count()
		if e != nil {
			return e
		}
		rows, e := stmt.Order(q.ID.Desc()).Limit(in.Limit).Offset(in.Offset).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			v, e := escapeView(r)
			if e != nil {
				return e
			}
			items = append(items, v)
		}
		return nil
	})
	if e != nil {
		return nil, escapeHTTPError(ctx, e)
	}
	return respx.List(ctx, items, respx.Page{Total: total, Limit: in.Limit, Offset: in.Offset}), nil
}
func (m *Module) getQualityEscapeHTTP(ctx context.Context, in *escapeRefInput) (*respx.Body[QualityEscapeView], error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, escapeHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, escapeHTTPError(ctx, ErrInvalidArgument)
	}
	db, e := m.writer()
	if e != nil {
		return nil, escapeHTTPError(ctx, e)
	}
	var out QualityEscapeView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindQualityEscapes(tx, m.seed.names)
		r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if e = m.escapeReadAuthorityOn(ctx, tx, tenant, r.ProjectID); e != nil {
			return e
		}
		out, e = escapeView(r)
		return e
	})
	if e != nil {
		return nil, escapeHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) decideQualityEscapeHTTP(ctx context.Context, in *escapeDecisionInput) (*escapeDecisionOutput, error) {
	if in.Body.RuleDraft != nil && in.Body.State != "RECORDED" {
		return nil, escapeHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, escapeHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, escapeHTTPError(ctx, ErrInvalidArgument)
	}
	version, e := parseGovernanceVersion(in.IfMatch)
	if e != nil {
		return nil, escapeHTTPError(ctx, e)
	}
	db, e := m.writer()
	if e != nil {
		return nil, escapeHTTPError(ctx, e)
	}
	if m.delegations.replay == nil {
		return nil, escapeHTTPError(ctx, ErrUnavailable)
	}
	payload, _ := json.Marshal(struct {
		Version int
		Body    any
	}{version, in.Body})
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "quality_escape.learning:" + id.String(), Value: in.Key, RequestHash: sha256.Sum256(payload)}
	if e = key.Validate(); e != nil {
		return nil, escapeHTTPError(ctx, e)
	}
	var result idempotency.Result
	var replayed bool
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock/recheck current human and project authority before cached responses too.
		q := query.BindQualityEscapes(tx, m.seed.names)
		r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if e = m.escapeDecisionAuthorityOn(ctx, tx, tenant, r.ProjectID); e != nil {
			return e
		}
		if in.Body.RuleDraft != nil {
			if m.learningRules.AuthorizeOn == nil || m.learningRules.ProposeOn == nil {
				return ErrUnavailable
			}
			if e = m.learningRules.AuthorizeOn(ctx, tx, tenant, r.ProjectID); e != nil {
				return learningRuleError(e)
			}
		}
		result, replayed, e = m.delegations.replay.Do(ctx, tx, key, func(work *gorm.DB) (idempotency.Result, error) {
			v, e := m.decideEscapeOn(ctx, work, tenant, id, version, in.Body.State, in.Body.Reason)
			if e != nil {
				return idempotency.Result{}, e
			}
			if in.Body.RuleDraft != nil {
				rule, e := m.learningRules.ProposeOn(ctx, work, tenant, r.ProjectID, id, *in.Body.RuleDraft)
				if e != nil {
					return idempotency.Result{}, learningRuleError(e)
				}
				v.MemoryIDs = append(v.MemoryIDs, rule.ID)
				refs, _ := json.Marshal(v.MemoryIDs)
				q := query.BindQualityEscapes(work, m.seed.names)
				if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(v.Version)).Update(q.MemoryIDs, string(refs)); e != nil {
					return idempotency.Result{}, e
				}
			}
			body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, v).Body)
			return idempotency.Result{StatusCode: 200, Body: body, ResourceKind: "quality_escape", ResourceID: id}, e
		})
		return e
	})
	if e != nil {
		return nil, escapeHTTPError(ctx, e)
	}
	out := escapeDecisionOutput{Status: result.StatusCode}
	if replayed {
		out.Replayed = "true"
	}
	if e = json.Unmarshal(result.Body, &out.Body); e != nil {
		return nil, escapeHTTPError(ctx, e)
	}
	return &out, nil
}

func learningRuleError(err error) error {
	switch {
	case errors.Is(err, knowledge.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, knowledge.ErrInvalid):
		return ErrInvalidArgument
	case errors.Is(err, knowledge.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, knowledge.ErrConflict):
		return ErrVersion
	default:
		return err
	}
}

func escapeHTTPError(ctx context.Context, err error) error {
	if errors.Is(err, ErrNotFound) {
		return respx.ErrStatus(ctx, 404, 160480, governancei18n.EscapeNotFound, nil)
	}
	if errors.Is(err, ErrVersion) {
		return respx.ErrStatus(ctx, 409, 160481, governancei18n.EscapeDecided, nil)
	}
	return delegationHTTPError(ctx, err)
}

package execution

import (
	"context"
	"encoding/json"
	"net/http"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const artifactCommentsPath = "/executions/{execution_id}/artifact-comments"

type artifactCommentInput struct {
	ExecutionInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           ArtifactCommentCreate
}
type artifactCommentListInput struct {
	ExecutionInput
	Cursor string `query:"cursor" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type ArtifactCommentPage struct {
	Items      []ArtifactCommentView `json:"items"`
	HasMore    bool                  `json:"has_more"`
	NextCursor string                `json:"next_cursor,omitempty"`
}
type artifactCommentOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[ArtifactCommentView]
}

func (m *Module) artifactCommentExecutionOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.writer()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	var row model.Execution
	err = db.WithContext(ctx).Table(m.names.Executions.String()).Select("tenant_id", "project_id").Where("id = ?", id).First(&row).Error
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: row.TenantID, EntityID: row.ProjectID}, true, nil
}
func (m *Module) registerArtifactCommentsHTTP(api huma.API) error {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "artifact_comment.create", Method: http.MethodPost, Path: artifactCommentsPath, Summary: "Comment on an immutable pinned artifact location", Tags: []string{"executions"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 503}}, "execution_id", m.artifactCommentExecutionOwner, authz.Guard{Resource: "artifact_comment", Verb: "write"}, m.createArtifactCommentHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "artifact_comment.list", Method: http.MethodGet, Path: artifactCommentsPath, Summary: "Read internal artifact comments with immutable anchors", Tags: []string{"executions"}, Errors: []int{401, 403, 404, 409, 422, 503}}, "execution_id", m.artifactCommentExecutionOwner, authz.Guard{Resource: "execution", Verb: "read"}, m.listArtifactCommentsHTTP)
	if err := m.registerArtifactCommentRepliesHTTP(api); err != nil {
		return err
	}
	if err := m.registerArtifactCommentLifecycleHTTP(api); err != nil {
		return err
	}
	return authz.RegisterRequestContract(api, artifactCommentsPath)
}
func (m *Module) createArtifactCommentHTTP(ctx context.Context, in *artifactCommentInput) (*artifactCommentOutput, error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.commentIdempotency == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	tenant := authn.TenantIDFromContext(ctx)
	digest, err := idempotency.HashRequest(http.MethodPost, artifactCommentsPath, map[string]string{"execution_id": id.String()}, in.Body)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "artifact_comment.create", Value: in.IdempotencyKey, RequestHash: digest}
	if err = key.Validate(); err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	var result idempotency.Result
	var replay bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, e := m.rowOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if _, e = m.commentAuthority(ctx, tx, tenant, row.ProjectID, "artifact_comment_write"); e != nil {
			return e
		}
		result, replay, e = m.commentIdempotency.Do(ctx, tx, key, func(tx *gorm.DB) (idempotency.Result, error) {
			view, e := m.CreateArtifactCommentOn(ctx, tx, tenant, id, in.Body)
			if e != nil {
				return idempotency.Result{}, e
			}
			body, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, view).Body)
			return idempotency.Result{StatusCode: 201, Body: body, ResourceKind: "artifact_comment", ResourceID: view.ID}, e
		})
		return e
	})
	if err != nil {
		return nil, writeContextHTTPError(ctx, err, "artifact_comment.create")
	}
	out := &artifactCommentOutput{Status: 201}
	if err = json.Unmarshal(result.Body, &out.Body); err != nil {
		return nil, httpError(ctx, err)
	}
	if replay {
		out.Replayed = "true"
	}
	return out, nil
}
func (m *Module) listArtifactCommentsHTTP(ctx context.Context, in *artifactCommentListInput) (*respx.Body[ArtifactCommentPage], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	var cursor guid.ID
	if in.Cursor != "" {
		cursor, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, httpError(ctx, ErrInvalidArgument)
		}
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	out := ArtifactCommentPage{Items: []ArtifactCommentView{}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		row, e := m.rowOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if _, e = m.commentAuthority(ctx, tx, tenant, row.ProjectID, "execution_read"); e != nil {
			return e
		}
		rows := []model.ArtifactCommentThread{}
		if e = tx.WithContext(ctx).Table(m.names.CommentThreads.String()).Where("tenant_id = ? AND execution_id = ? AND visibility = ? AND id > ?", tenant, id, "INTERNAL", cursor).Order("id ASC").Limit(in.Limit + 1).Find(&rows).Error; e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
		}
		for _, r := range rows {
			v := ArtifactCommentView{LastSeq: int64(r.Version), ID: r.ID, ExecutionID: r.ExecutionID, WorkItemID: r.WorkItemID, EvidenceID: r.EvidenceID, Visibility: r.Visibility, State: r.State, FirstBody: r.FirstBody, CreatedAt: r.CreatedAt}
			v.CreatedBy = r.CreatedBy
			var plan ExecutionPlan
			if json.Unmarshal([]byte(row.PlanJSON), &plan) == nil {
				v.ExecutorID = plan.ExecutorPrincipalID
			}
			var first model.ArtifactCommentMessage
			if e = tx.WithContext(ctx).Table(m.names.CommentMessages.String()).Where("tenant_id = ? AND thread_id = ? AND seq = ?", tenant, r.ID, 1).Limit(1).Find(&first).Error; e != nil {
				return e
			}
			if first.ID > 0 {
				v.FirstBody = first.Body
				v.FirstRetracted = first.RetractedAt != nil
				if v.FirstRetracted {
					v.FirstBody = ""
				}
			}
			if e = decodeRelocatedAnchor(r, &v); e != nil {
				return e
			}
			if json.Unmarshal([]byte(r.AnchorSnapshot), &v.Anchor) != nil {
				return ErrUnavailable
			}
			var c model.ArtifactCommentConversion
			e = tx.WithContext(ctx).Table(m.names.CommentConversions.String()).Where("tenant_id = ? AND thread_id = ? AND target_kind = ?", tenant, r.ID, "DEFECT").Limit(1).Find(&c).Error
			if e != nil {
				return e
			}
			v.DefectID = c.TargetID
			c = model.ArtifactCommentConversion{}
			if e = tx.WithContext(ctx).Table(m.names.CommentConversions.String()).Where("tenant_id = ? AND thread_id = ? AND target_kind = ?", tenant, r.ID, "FEEDBACK").Limit(1).Find(&c).Error; e != nil {
				return e
			}
			v.FeedbackID = c.TargetID
			out.Items = append(out.Items, v)
		}
		if out.HasMore && len(rows) > 0 {
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

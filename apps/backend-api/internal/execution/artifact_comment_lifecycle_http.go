package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const artifactCommentThreadPath = "/comment-threads/{thread_id}"
const artifactCommentMessagePath = "/comments/{comment_id}"

type artifactCommentMutationInput struct {
	ThreadID       string `path:"thread_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           ArtifactCommentMutation
}

type artifactCommentMessageMutationInput struct {
	CommentID      string `path:"comment_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           ArtifactCommentMutation
}

type ArtifactCommentMutationReceipt struct {
	ThreadID     guid.ID                     `json:"thread_id"`
	LastSeq      int64                       `json:"last_seq"`
	Message      *ArtifactCommentMessageView `json:"message,omitempty"`
	AnchorStatus string                      `json:"anchor_status,omitempty"`
}

type artifactCommentSourceInput struct {
	ExecutionInput
	EvidenceID string `query:"evidence_id" required:"true" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type ArtifactCommentSourceContent struct {
	Name      string  `json:"name"`
	Text      string  `json:"text"`
	VersionID guid.ID `json:"version_id"`
	SHA256    string  `json:"sha256"`
}

type artifactCommentMutationOutput struct {
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[ArtifactCommentMutationReceipt]
}

func (m *Module) commentMessageThread(ctx context.Context, tx *gorm.DB, id guid.ID) (model.ArtifactCommentMessage, error) {
	var message model.ArtifactCommentMessage
	err := tx.WithContext(ctx).Table(m.names.CommentMessages.String()).Where("tenant_id = ? AND id = ?", authn.TenantIDFromContext(ctx), id).Take(&message).Error
	return message, err
}

func (m *Module) artifactCommentMessageOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.writer()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	message, err := m.commentMessageThread(ctx, db, id)
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return m.artifactCommentThreadOwner(ctx, message.ThreadID)
}

func (m *Module) registerArtifactCommentLifecycleHTTP(api huma.API) error {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "artifact_comment.source", Method: http.MethodGet, Path: "/executions/{execution_id}/artifact-source", Summary: "Read verified pinned artifact text for an internal comment viewer", Tags: []string{"executions"}, Errors: []int{400, 401, 403, 404, 409, 422, 503}}, "execution_id", m.artifactCommentExecutionOwner, authz.Guard{Resource: "execution", Verb: "read"}, m.artifactCommentSourceHTTP)
	guard := authz.Guard{Resource: "execution", Verb: "read"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "artifact_comment.state", Method: http.MethodPatch, Path: artifactCommentThreadPath, Summary: "Resolve or reopen an internal comment thread", Tags: []string{"executions"}, Errors: []int{400, 401, 403, 404, 409, 422, 503}}, "thread_id", m.artifactCommentThreadOwner, guard, m.artifactCommentStateHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "artifact_comment.reanchor", Method: http.MethodPost, Path: artifactCommentThreadPath + "/reanchor", Summary: "Check an immutable comment anchor against a pinned artifact version", Tags: []string{"executions"}, Errors: []int{400, 401, 403, 404, 409, 422, 503}}, "thread_id", m.artifactCommentThreadOwner, guard, m.artifactCommentReanchorHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "artifact_comment.edit", Method: http.MethodPatch, Path: artifactCommentMessagePath, Summary: "Edit an owned comment within its editing window", Tags: []string{"executions"}, Errors: []int{400, 401, 403, 404, 409, 422, 503}}, "comment_id", m.artifactCommentMessageOwner, guard, m.artifactCommentEditHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "artifact_comment.withdraw", Method: http.MethodDelete, Path: artifactCommentMessagePath, Summary: "Retract a comment while retaining its chronology", Tags: []string{"executions"}, Errors: []int{400, 401, 403, 404, 409, 422, 503}}, "comment_id", m.artifactCommentMessageOwner, guard, m.artifactCommentWithdrawHTTP)
	for _, path := range []string{artifactCommentThreadPath, artifactCommentThreadPath + "/reanchor", artifactCommentMessagePath} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) artifactCommentSourceHTTP(ctx context.Context, in *artifactCommentSourceInput) (*respx.Body[ArtifactCommentSourceContent], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	evidenceID, err := guid.RequireID(in.EvidenceID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out ArtifactCommentSourceContent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		row, e := m.rowOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if _, e = m.commentAuthority(ctx, tx, tenant, row.ProjectID, "execution_read"); e != nil {
			return e
		}
		q := query.BindEvidence(tx, m.names)
		evidence, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(id), q.ProjectID.Eq(row.ProjectID), q.ID.Eq(evidenceID), q.Kind.Eq("FILE")).First()
		if e != nil {
			return e
		}
		var fact ArtifactFact
		if json.Unmarshal([]byte(evidence.Content), &fact) != nil || m.commentFiles == nil {
			return ErrUnavailable
		}
		readCtx := context.WithValue(ctx, artifactCommentReadKey{}, artifactCommentRead{tenant, row.ProjectID, evidence.ID})
		pin, e := m.commentFiles.OpenOn(readCtx, tx, fact.BindingID)
		if e != nil {
			return e
		}
		if pin.Reader == nil {
			return ErrUnavailable
		}
		if pin.Purpose != "execution_evidence" || pin.ResourceID != evidence.ID {
			return errors.Join(ErrForbidden, pin.Reader.Close())
		}
		data, e := io.ReadAll(io.LimitReader(pin.Reader, 4*1024*1024+1))
		if e = errors.Join(e, pin.Reader.Close()); e != nil {
			return e
		}
		if len(data) > 4*1024*1024 || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
			return &ClientAnchorError{Field: "artifact_ref"}
		}
		if hash(data) != fact.SHA256 || pin.SHA256 != fact.SHA256 || pin.FileID != fact.FileID || pin.VersionID != fact.VersionID || pin.BindingID != fact.BindingID {
			return ErrConflict
		}
		out = ArtifactCommentSourceContent{Name: pin.Name, Text: string(data), VersionID: pin.VersionID, SHA256: pin.SHA256}
		return nil
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) runCommentMutationHTTP(ctx context.Context, id guid.ID, message bool, method, path, operation, ifMatch, keyValue string, body ArtifactCommentMutation, run func(*gorm.DB, guid.ID, int64) (ArtifactCommentMutationReceipt, error)) (*artifactCommentMutationOutput, error) {
	expected, err := strconv.ParseInt(strings.Trim(ifMatch, "\""), 10, 32)
	if err != nil || expected < 1 {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.commentIdempotency == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	digest, err := idempotency.HashRequest(method, path, map[string]string{"id": id.String(), "if_match": ifMatch}, body)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: keyValue, RequestHash: digest}
	if err = key.Validate(); err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	var result idempotency.Result
	var replay bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		threadID := id
		if message {
			record, e := m.commentMessageThread(ctx, tx, id)
			if e != nil {
				return e
			}
			threadID = record.ThreadID
		}
		thread, e := m.commentThreadOn(ctx, tx, tenant, threadID, false)
		if e != nil {
			return e
		}
		if _, e = m.commentAuthority(ctx, tx, tenant, thread.ProjectID, "execution_read"); e != nil {
			return e
		}
		// Replay rechecks current author/privileged authority as well as membership.
		if message {
			record, e := m.commentMessageThread(ctx, tx, id)
			if e != nil {
				return e
			}
			if record.CreatedBy != authn.PrincipalIDFromContext(ctx) {
				if operation != "artifact_comment.withdraw" {
					return ErrForbidden
				}
				if _, e = m.commentAuthority(ctx, tx, tenant, thread.ProjectID, "work_item_write"); e != nil {
					return e
				}
			}
		} else if operation == "artifact_comment.state" && thread.CreatedBy != authn.PrincipalIDFromContext(ctx) {
			row, e := m.rowOn(ctx, tx, tenant, thread.ExecutionID, false)
			if e != nil {
				return e
			}
			if !commentExecutor(row, authn.PrincipalIDFromContext(ctx)) {
				if _, e = m.commentAuthority(ctx, tx, tenant, thread.ProjectID, "work_item_write"); e != nil {
					return e
				}
			}
		}
		result, replay, e = m.commentIdempotency.Do(ctx, tx, key, func(tx *gorm.DB) (idempotency.Result, error) {
			out, e := run(tx, threadID, expected)
			if e != nil {
				return idempotency.Result{}, e
			}
			encoded, e := respx.MarshalEnvelope(ctx, respx.OK(ctx, out).Body)
			return idempotency.Result{StatusCode: 200, Body: encoded, ResourceKind: "artifact_comment", ResourceID: threadID}, e
		})
		return e
	})
	if err != nil {
		return nil, writeContextHTTPError(ctx, err, operation)
	}
	out := &artifactCommentMutationOutput{}
	if replay {
		out.Replayed = "true"
	}
	if err = json.Unmarshal(result.Body, &out.Body); err != nil {
		return nil, httpError(ctx, err)
	}
	return out, nil
}

func (m *Module) artifactCommentStateHTTP(ctx context.Context, in *artifactCommentMutationInput) (*artifactCommentMutationOutput, error) {
	id, err := guid.RequireID(in.ThreadID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	return m.runCommentMutationHTTP(ctx, id, false, http.MethodPatch, artifactCommentThreadPath, "artifact_comment.state", in.IfMatch, in.IdempotencyKey, in.Body, func(tx *gorm.DB, thread guid.ID, expected int64) (ArtifactCommentMutationReceipt, error) {
		row, e := m.commentThreadOn(ctx, tx, authn.TenantIDFromContext(ctx), thread, false)
		if e != nil {
			return ArtifactCommentMutationReceipt{}, e
		}
		if int64(row.Version) != expected {
			return ArtifactCommentMutationReceipt{}, ErrConflict
		}
		out, e := m.ChangeArtifactCommentStateOn(ctx, tx, authn.TenantIDFromContext(ctx), thread, expected, in.Body)
		return ArtifactCommentMutationReceipt{ThreadID: thread, LastSeq: out.LastSeq}, e
	})
}
func (m *Module) artifactCommentEditHTTP(ctx context.Context, in *artifactCommentMessageMutationInput) (*artifactCommentMutationOutput, error) {
	return m.mutateCommentMessageHTTP(ctx, in, false)
}
func (m *Module) artifactCommentWithdrawHTTP(ctx context.Context, in *artifactCommentMessageMutationInput) (*artifactCommentMutationOutput, error) {
	return m.mutateCommentMessageHTTP(ctx, in, true)
}
func (m *Module) mutateCommentMessageHTTP(ctx context.Context, in *artifactCommentMessageMutationInput, retract bool) (*artifactCommentMutationOutput, error) {
	id, err := guid.RequireID(in.CommentID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	method, op := http.MethodPatch, "artifact_comment.edit"
	if retract {
		method, op = http.MethodDelete, "artifact_comment.withdraw"
	}
	return m.runCommentMutationHTTP(ctx, id, true, method, artifactCommentMessagePath, op, in.IfMatch, in.IdempotencyKey, in.Body, func(tx *gorm.DB, thread guid.ID, expected int64) (ArtifactCommentMutationReceipt, error) {
		out, e := m.MutateArtifactCommentMessageOn(ctx, tx, authn.TenantIDFromContext(ctx), thread, id, expected, retract, in.Body)
		return ArtifactCommentMutationReceipt{ThreadID: thread, LastSeq: expected + 1, Message: &out}, e
	})
}
func (m *Module) artifactCommentReanchorHTTP(ctx context.Context, in *artifactCommentMutationInput) (*artifactCommentMutationOutput, error) {
	id, err := guid.RequireID(in.ThreadID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	return m.runCommentMutationHTTP(ctx, id, false, http.MethodPost, artifactCommentThreadPath+"/reanchor", "artifact_comment.reanchor", in.IfMatch, in.IdempotencyKey, in.Body, func(tx *gorm.DB, thread guid.ID, expected int64) (ArtifactCommentMutationReceipt, error) {
		return m.ReanchorArtifactCommentOn(ctx, tx, authn.TenantIDFromContext(ctx), thread, expected, in.Body)
	})
}

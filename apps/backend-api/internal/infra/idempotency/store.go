package idempotency

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency/model"
	"7link.com/platform/internal/infra/idempotency/query"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrUnavailable   = errors.New("idempotency: store unavailable")
	ErrInvalidKey    = errors.New("idempotency: invalid request key")
	ErrKeyReused     = errors.New("idempotency: key reused for a different request")
	ErrInProgress    = errors.New("idempotency: request in progress")
	ErrInvalidResult = errors.New("idempotency: invalid response")
)

type Key struct {
	TenantID         guid.ID
	SubjectKind      string
	SubjectID        guid.ID
	Operation, Value string
	RequestHash      [32]byte
}
type Result struct {
	StatusCode   int
	Body         json.RawMessage
	ResourceKind string
	ResourceID   guid.ID
}

// Store currently exposes the strong transactional mode. The separate weak
// middleware mode must not be substituted for an atomic business command.
type Store interface {
	Do(context.Context, *gorm.DB, Key, func(*gorm.DB) (Result, error)) (Result, bool, error)
}

func validKey(k Key) bool {
	if k.TenantID < 0 || k.SubjectID <= 0 || (k.SubjectKind != "PRINCIPAL" && k.SubjectKind != "CONTACT") || k.Operation == "" || !utf8.ValidString(k.Operation) || utf8.RuneCountInString(k.Operation) > 128 || len(k.Value) < 1 || len(k.Value) > 64 {
		return false
	}
	for _, b := range []byte(k.Value) {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}

// Validate checks the shared key contract without touching storage.
func (key Key) Validate() error {
	if !validKey(key) {
		return ErrInvalidKey
	}
	return nil
}

// Do must be called inside the business transaction, before any business reads
// establish a repeatable-read snapshot. Every error requires caller rollback.
// Cacheable error responses roll back the callback savepoint while preserving
// the key/result; noncacheable responses remove the key and roll back effects.
func (m *Module) Do(ctx context.Context, tx *gorm.DB, key Key, fn func(*gorm.DB) (Result, error)) (Result, bool, error) {
	if key.Validate() != nil || tx == nil || tx.Statement == nil || fn == nil {
		return Result{}, false, ErrInvalidKey
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return Result{}, false, ErrInvalidKey
	}
	if m == nil || m.ids == nil || m.now == nil || m.names.Keys.IsZero() {
		return Result{}, false, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return Result{}, false, err
	}
	if id <= 0 {
		return Result{}, false, ErrUnavailable
	}
	now := m.now().UTC().Truncate(time.Millisecond)
	hash := hex.EncodeToString(key.RequestHash[:])
	candidate := model.KeyRow{ID: guid.ID(id), TenantID: key.TenantID, SubjectKind: key.SubjectKind, SubjectID: key.SubjectID, Operation: key.Operation, Key: key.Value, RequestHash: hash, State: "IN_PROGRESS", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	table := query.Bind(tx, m.names)
	if err := table.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate); err != nil {
		return Result{}, false, err
	}
	read := table.WithContext(ctx).Where(table.TenantID.Eq(key.TenantID), table.SubjectKind.Eq(key.SubjectKind), table.SubjectID.Eq(key.SubjectID), table.Operation.Eq(key.Operation), table.Key.Eq(key.Value))
	if tx.Dialector.Name() != "sqlite" {
		read = read.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	row, err := read.Take()
	if err != nil {
		return Result{}, false, err
	}
	if row.ID != candidate.ID && now.Before(row.ExpiresAt) {
		if row.RequestHash != hash {
			return Result{}, false, ErrKeyReused
		}
		if row.State == "IN_PROGRESS" {
			return Result{}, false, ErrInProgress
		}
		if row.State != "COMPLETED" || row.StatusCode == nil {
			return Result{}, false, ErrInvalidResult
		}
		result := Result{StatusCode: *row.StatusCode, ResourceKind: row.ResourceKind}
		if row.ResponseBody != nil {
			result.Body = json.RawMessage(*row.ResponseBody)
		}
		if row.ResourceID != nil {
			result.ResourceID = *row.ResourceID
		}
		return result, true, nil
	}
	if row.ID != candidate.ID {
		_, err = table.WithContext(ctx).Where(table.ID.Eq(row.ID)).UpdateSimple(table.RequestHash.Value(hash), table.State.Value("IN_PROGRESS"), table.StatusCode.Null(), table.ResponseBody.Null(), table.ResourceKind.Value(""), table.ResourceID.Null(), table.CreatedAt.Value(now), table.ExpiresAt.Value(candidate.ExpiresAt))
		if err != nil {
			return Result{}, false, err
		}
	}
	// Explicit savepoint rollback checks its own error; a failed rollback must
	// never be converted into a successfully cached business error.
	point := "idempotency_" + candidate.ID.String()
	work := tx.WithContext(ctx)
	if err := work.SavePoint(point).Error; err != nil {
		return Result{}, false, err
	}
	result, callErr := fn(work)
	if callErr == nil {
		if result.StatusCode < 200 || result.StatusCode > 599 || (!json.Valid(result.Body) && !(result.StatusCode == http.StatusNoContent && len(result.Body) == 0)) || !utf8.ValidString(result.ResourceKind) || utf8.RuneCountInString(result.ResourceKind) > 32 || result.ResourceID < 0 || (result.ResourceKind == "") != (result.ResourceID == 0) || (len(result.Body) > 65536 && result.ResourceID == 0) {
			callErr = ErrInvalidResult
		}
	}
	if callErr != nil || result.StatusCode >= 300 {
		if err := work.RollbackTo(point).Error; err != nil {
			return Result{}, false, err
		}
		if callErr != nil {
			return Result{}, false, callErr
		}
	}
	if !cacheable(result) {
		if _, err = table.WithContext(ctx).Where(table.ID.Eq(row.ID)).Delete(); err != nil {
			return Result{}, false, err
		}
		return result, false, nil
	}
	var response *string
	if len(result.Body) <= 65536 {
		value := string(result.Body)
		response = &value
	}
	var resource *guid.ID
	if result.ResourceID > 0 {
		value := result.ResourceID
		resource = &value
	}
	_, err = table.WithContext(ctx).Where(table.ID.Eq(row.ID)).Updates(map[string]any{"state": "COMPLETED", "status_code": result.StatusCode, "response_body": response, "resource_kind": result.ResourceKind, "resource_id": resource})
	if err != nil {
		return Result{}, false, err
	}
	return result, false, nil
}

func cacheable(result Result) bool {
	if result.StatusCode >= 200 && result.StatusCode < 300 {
		return true
	}
	switch result.StatusCode {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity:
		return true
	case http.StatusConflict:
		var body struct {
			Code int `json:"code"`
			Data struct {
				State  string `json:"state"`
				Reason string `json:"reason_code"`
			} `json:"data"`
		}
		if json.Unmarshal(result.Body, &body) != nil {
			return false
		}
		return body.Code != 120409 && body.Data.State != "interrupted" && !strings.EqualFold(body.Data.Reason, "VERSION_CONFLICT")
	default:
		return false
	}
}

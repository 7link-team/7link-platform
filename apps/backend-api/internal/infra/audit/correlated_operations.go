package audit

import (
	"context"
	"errors"
	"regexp"
	"time"

	"7link.com/platform/internal/infra/audit/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var ErrInvalidCorrelation = errors.New("audit: invalid correlation filter")
var correlationPattern = regexp.MustCompile(`^[A-Za-z0-9:.-]{1,64}$`)

// OperationSummary deliberately excludes request/response bodies, metadata,
// errors and client identity. A correlation drilldown never exposes payloads.
type OperationSummary struct {
	ID           guid.ID   `json:"id"`
	TenantID     guid.ID   `json:"tenant_id"`
	OccurredAt   time.Time `json:"occurred_at"`
	Operation    string    `json:"operation"`
	Outcome      string    `json:"outcome"`
	ResourceType string    `json:"resource_type"`
	ResourceID   guid.ID   `json:"resource_id"`
	TraceID      string    `json:"trace_id"`
}

type CorrelatedOperations func(context.Context, string, int, int) ([]OperationSummary, int64, error)

func readCorrelatedOperations(ctx context.Context, db *gorm.DB, prefix, correlation string, offset, limit int) ([]OperationSummary, int64, error) {
	if !correlationPattern.MatchString(correlation) || offset < 0 || offset > 1000000 || limit < 1 || limit > 200 {
		return nil, 0, ErrInvalidCorrelation
	}
	if db == nil {
		return nil, 0, ErrDatabaseSinkUnavailable
	}
	q := query.Bind(db, prefix)
	// fromEvent marshals a flat map[string]string with encoding/json. Matching
	// the complete canonical member also finds jobs executed in a fresh trace.
	// The bounded alphabet excludes LIKE metacharacters and JSON escaping.
	base := q.WithContext(ctx).Where(q.WithContext(ctx).Where(q.TraceID.Eq(correlation)).Or(q.Metadata.Like(`%"correlation_id":"` + correlation + `"%`)))
	total, err := base.Count()
	if err != nil {
		return nil, 0, err
	}
	rows, err := base.Select(q.ID, q.TenantID, q.OccurredAt, q.Operation, q.Outcome, q.ResourceType, q.ResourceID, q.TraceID).Order(q.OccurredAt.Desc(), q.ID.Desc()).Offset(offset).Limit(limit).Find()
	if err != nil {
		return nil, 0, err
	}
	out := make([]OperationSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, OperationSummary{ID: r.ID, TenantID: r.TenantID, OccurredAt: r.OccurredAt, Operation: r.Operation, Outcome: r.Outcome, ResourceType: r.ResourceType, ResourceID: r.ResourceID, TraceID: r.TraceID})
	}
	return out, total, nil
}

func (m *Module) correlatedOperations(ctx context.Context, correlation string, offset, limit int) ([]OperationSummary, int64, error) {
	if m == nil || !m.cfg.Enabled || !m.cfg.Database.Enabled || m.runtime == nil || m.runtime.Database() == nil {
		return nil, 0, ErrDatabaseSinkUnavailable
	}
	return readCorrelatedOperations(ctx, m.runtime.Database().Write(), m.runtime.TablePrefix(), correlation, offset, limit)
}

// RedactAdministrativeJSON applies the operation log's credential redaction to
// administrative JSON views. Invalid JSON is refused without exposing input.
func RedactAdministrativeJSON(raw []byte) (string, error) {
	return newRedactor(nil).RedactJSON(raw)
}

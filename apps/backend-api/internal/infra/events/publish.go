package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
	"weak"

	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var eventPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

func (m *Module) Publish(ctx context.Context, tx *gorm.DB, e Event) (guid.ID, error) {
	return m.publish(ctx, tx, e, true)
}

// PublishEphemeral stores only the temporary cross-replica realtime transport.
func (m *Module) PublishEphemeral(ctx context.Context, tx *gorm.DB, e Event) (guid.ID, error) {
	return m.publish(ctx, tx, e, false)
}

func (m *Module) publish(ctx context.Context, tx *gorm.DB, e Event, durable bool) (guid.ID, error) {
	if tx == nil || tx.Statement == nil {
		return 0, ErrInvalidEvent
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidEvent
	}
	m.mu.RLock()
	spec, known := m.types[e.Type]
	ready := m.sealed && m.ids != nil
	m.mu.RUnlock()
	if !ready {
		return 0, ErrUnavailable
	}
	if !durable {
		if !eventPattern.MatchString(e.Type) || len(e.Type) > 96 {
			return 0, ErrInvalidEvent
		}
		spec = TypeSpec{Name: e.Type, SchemaVersion: 1, SubjectKind: e.Subject.Kind, Realtime: RealtimeFull}
		known = kindPattern.MatchString(e.Subject.Kind)
	}
	if !known {
		return 0, ErrUnknownEventType
	}
	if e.TenantID < 0 || e.ProjectID < 0 || e.Subject.ID <= 0 || e.Subject.Kind != spec.SubjectKind || e.SubjectVersion < 0 || e.CausationID < 0 || e.Actor.ID < 0 || utf8.RuneCountInString(e.Actor.AuthoritySource) > 160 || utf8.RuneCountInString(e.CorrelationID) > 64 {
		return 0, ErrInvalidEvent
	}
	switch e.Actor.Kind {
	case "SYSTEM":
		if e.Actor.ID != 0 {
			return 0, ErrInvalidEvent
		}
	case "HUMAN", "AGENT", "MACHINE", "CLIENT":
		if e.Actor.ID == 0 {
			return 0, ErrInvalidEvent
		}
	default:
		return 0, ErrInvalidEvent
	}
	payload, err := json.Marshal(e.Data)
	if err != nil {
		return 0, fmt.Errorf("%w: data cannot be encoded", ErrInvalidEvent)
	}
	if len(payload) > 16<<10 {
		return 0, ErrInvalidEvent
	}
	topics, err := eventTopics(e)
	if err != nil {
		return 0, err
	}
	topicJSON, err := json.Marshal(topics)
	if err != nil {
		return 0, err
	}
	budget, err := m.transactionBudget(tx)
	if err != nil {
		return 0, err
	}
	if budget.Add(1) > 100 {
		budget.Add(-1)
		return 0, ErrInvalidEvent
	}
	id, err := m.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	now := m.now().UTC().Truncate(time.Millisecond)
	row := model.EventRow{ID: guid.ID(id), TenantID: e.TenantID, ProjectID: optionalID(e.ProjectID), EventType: e.Type, SchemaVersion: spec.SchemaVersion, Durable: durable, OccurredAt: now, ActorKind: e.Actor.Kind, ActorID: optionalID(e.Actor.ID), AuthoritySource: e.Actor.AuthoritySource, SubjectKind: e.Subject.Kind, SubjectID: e.Subject.ID, SubjectVersion: e.SubjectVersion, Topics: string(topicJSON), RealtimeMode: string(spec.Realtime), Data: string(payload), CorrelationID: e.CorrelationID, CausationID: optionalID(e.CausationID), CreatedAt: now}
	if err := query.BindWithNames(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return 0, err
	}
	if err := query.Notify(ctx, tx, m.names); err != nil {
		return 0, err
	}
	return row.ID, nil
}
func optionalID(id guid.ID) *guid.ID {
	if id == 0 {
		return nil
	}
	return &id
}
func eventTopics(e Event) ([]string, error) {
	prefix := "tenant/" + strconv.FormatInt(int64(e.TenantID), 10) + "/"
	topics := []string{prefix + e.Subject.Kind + "/" + e.Subject.ID.String()}
	if e.ProjectID > 0 {
		topics = append(topics, prefix+"project/"+e.ProjectID.String())
	}
	topics = append(topics, e.ExtraTopics...)
	unique := make([]string, 0, len(topics))
	seen := map[string]bool{}
	for _, topic := range topics {
		if !strings.HasPrefix(topic, prefix) {
			return nil, ErrInvalidEvent
		}
		segments := strings.Split(strings.TrimPrefix(topic, prefix), "/")
		if len(segments) < 1 || len(segments) > 3 || !kindPattern.MatchString(segments[0]) {
			return nil, ErrInvalidEvent
		}
		if len(segments) >= 2 {
			id, err := strconv.ParseInt(segments[1], 10, 64)
			if err != nil || id <= 0 || strconv.FormatInt(id, 10) != segments[1] {
				return nil, ErrInvalidEvent
			}
		}
		if len(segments) == 3 && !kindPattern.MatchString(segments[2]) {
			return nil, ErrInvalidEvent
		}
		if !seen[topic] {
			seen[topic] = true
			unique = append(unique, topic)
		}
	}
	if len(unique) > 8 {
		return nil, ErrInvalidEvent
	}
	return unique, nil
}

// Session and WithContext clone GORM statements, but keep the same SQL
// transaction. Weak keys and cleanup keep finished transactions collectible.
func (m *Module) transactionBudget(tx *gorm.DB) (*atomic.Int64, error) {
	pool := tx.Statement.ConnPool
	for {
		prepared, ok := pool.(*gorm.PreparedStmtTX)
		if !ok {
			break
		}
		pool = prepared.Tx
	}
	if wrapped, ok := pool.(interface{ SQLTx() *sql.Tx }); ok {
		pool = wrapped.SQLTx()
	}
	transaction, ok := pool.(*sql.Tx)
	if !ok {
		return nil, ErrInvalidEvent
	}
	key := weak.Make(transaction)
	value, loaded := m.budgets.LoadOrStore(key, &atomic.Int64{})
	if !loaded {
		runtime.AddCleanup(transaction, func(key weak.Pointer[sql.Tx]) { m.budgets.Delete(key) }, key)
	}
	runtime.KeepAlive(transaction)
	budget, ok := value.(*atomic.Int64)
	if !ok {
		return nil, ErrUnavailable
	}
	return budget, nil
}

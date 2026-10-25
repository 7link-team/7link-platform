package events

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SubjectHumanActor resolves the actual Human actor of a durable source event.
// It never substitutes a subject's creator for the actor of a later command.
type SubjectHumanActor func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID, string) (guid.ID, bool, error)

// ProjectOccurrences returns durable routing facts in a bounded time window.
// Callers still serialize their source aggregate before making a decision.
type ProjectOccurrence struct {
	ID     guid.ID
	Type   string
	At     time.Time
	Target string
}
type ProjectOccurrences func(context.Context, *gorm.DB, guid.ID, guid.ID, []string, time.Time) ([]ProjectOccurrence, error)

func (m *Module) provideProjectOccurrences(s *module.Services) error {
	if err := module.Provide[ProjectOccurrences](s, m.projectOccurrencesOn); err != nil {
		return err
	}
	return module.Provide[SubjectHumanActor](s, m.subjectHumanActorOn)
}

func (m *Module) projectOccurrencesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, types []string, since time.Time) ([]ProjectOccurrence, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || len(types) == 0 || since.IsZero() {
		return nil, ErrInvalidEvent
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidEvent
	}
	q := query.BindWithNames(tx, m.names)
	out := []ProjectOccurrence{}
	var after guid.ID
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.EventType.In(types...), q.Durable.Is(true), q.OccurredAt.Gte(since), q.ID.Gt(after)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(q.ID).Limit(100).Find()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			after = row.ID
			var data struct {
				Target string `json:"target"`
			}
			if err := json.Unmarshal([]byte(row.Data), &data); err != nil {
				return nil, err
			}
			out = append(out, ProjectOccurrence{ID: row.ID, Type: row.EventType, At: row.OccurredAt, Target: data.Target})
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
}

func (m *Module) subjectHumanActorOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, kind string, id guid.ID, typ string) (guid.ID, bool, error) {
	if m == nil || tx == nil || tenant <= 0 || project <= 0 || id <= 0 || kind == "" || typ == "" {
		return 0, false, ErrInvalidEvent
	}
	q := query.BindWithNames(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.SubjectKind.Eq(kind), q.SubjectID.Eq(id), q.EventType.Eq(typ), q.Durable.Is(true), q.ActorKind.Eq("HUMAN")).Order(q.SubjectVersion.Desc(), q.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if row.ActorID == nil || *row.ActorID <= 0 {
		return 0, false, nil
	}
	return *row.ActorID, true, nil
}

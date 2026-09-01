package todo

import (
	"context"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/todo/model"
	"7link.com/platform/internal/todo/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/huma/respx"
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

// service holds the use cases. It is the only place this package reaches the
// query package, so the soft-delete predicate and the table prefix cannot be
// forgotten at some other call site.
type service struct {
	runtime *appenv.Runtime
	ids     guid.Source
	now     func() time.Time
}

func newService(rt *appenv.Runtime, ids guid.Source) *service {
	return &service{runtime: rt, ids: ids, now: func() time.Time { return time.Now().UTC() }}
}

// writer resolves the writable datasource at operation time rather than
// retaining one, matching how every other module uses the router.
func (s *service) writer() *gorm.DB {
	if s == nil || s.runtime == nil || s.runtime.Database() == nil {
		return nil
	}
	return s.runtime.Database().Write()
}

// scope is the only constructor of a task query in this package. Passing
// includeDeleted explicitly at every call site means excluding soft-deleted
// rows is never an accident, and the table prefix is applied in exactly one
// place.
func (s *service) scope(ctx context.Context, includeDeleted bool) (query.ITodoRowDo, *query.Todos, error) {
	db := s.writer()
	if db == nil {
		return nil, nil, ErrUnavailable
	}
	todos := query.Bind(db, s.runtime.TablePrefix())
	q := todos.WithContext(ctx)
	if !includeDeleted {
		q = q.Where(todos.DeletedAt.IsNull())
	}
	return q, todos, nil
}

// actor is the acting principal, taken from the identity contract audit already
// defines. No middleware populates it yet, so it is zero today; when one lands,
// these columns start carrying values with no change here.
func actor(ctx context.Context) guid.ID {
	id, _ := guid.Parse(audit.ActorFromContext(ctx).ID)
	return id
}

// nullableTime assigns a nullable time column. gen models such a column as
// field.Time, whose Value takes a time.Time, so "set it" and "clear it" are two
// different calls; this keeps that branch in one place.
func nullableTime(column field.Time, value *time.Time) field.AssignExpr {
	if value == nil {
		return column.Null()
	}
	return column.Value(*value)
}

// Create stores a validated draft.
func (s *service) Create(ctx context.Context, draft Draft) (*model.TodoRow, error) {
	_, todos, err := s.scope(ctx, false)
	if err != nil {
		return nil, err
	}

	next, err := s.ids.Next()
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}

	now := s.now()
	who := actor(ctx)
	row := &model.TodoRow{
		ID:          guid.ID(next),
		Title:       draft.Title,
		Description: draft.Description,
		Status:      string(draft.Status),
		Priority:    draft.Priority,
		DueAt:       draft.DueAt,
		CompletedAt: completionFor("", draft.Status, nil, now),
		Version:     1,
		CreatedAt:   now,
		CreatedBy:   who,
		UpdatedAt:   now,
		UpdatedBy:   who,
	}
	if err := todos.WithContext(ctx).Create(row); err != nil {
		return nil, err
	}
	return row, nil
}

// Get returns one live task.
func (s *service) Get(ctx context.Context, id guid.ID) (*model.TodoRow, error) {
	return s.find(ctx, id, false)
}

// GetIncludingDeleted returns a task whether or not it is soft-deleted, for the
// restore path and for list queries that opt in.
func (s *service) GetIncludingDeleted(ctx context.Context, id guid.ID) (*model.TodoRow, error) {
	return s.find(ctx, id, true)
}

func (s *service) find(ctx context.Context, id guid.ID, includeDeleted bool) (*model.TodoRow, error) {
	q, todos, err := s.scope(ctx, includeDeleted)
	if err != nil {
		return nil, err
	}
	row, err := q.Where(todos.ID.Eq(id)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// Update applies a patch under an optimistic lock. version is the caller's view
// of the row; a mismatch means someone else wrote first.
func (s *service) Update(ctx context.Context, id guid.ID, version int, patch Patch) (*model.TodoRow, error) {
	current, err := s.find(ctx, id, false)
	if err != nil {
		return nil, err
	}
	if current.Version != version {
		return nil, ErrVersionConflict
	}

	now := s.now()
	previous := Status(current.Status)
	next := previous
	if patch.Status != nil {
		next = *patch.Status
	}

	q, todos, err := s.scope(ctx, false)
	if err != nil {
		return nil, err
	}

	assignments := []field.AssignExpr{
		todos.Version.Value(current.Version + 1),
		todos.UpdatedAt.Value(now),
		todos.UpdatedBy.Value(actor(ctx)),
		nullableTime(todos.CompletedAt, completionFor(previous, next, current.CompletedAt, now)),
	}
	if patch.Title != nil {
		assignments = append(assignments, todos.Title.Value(*patch.Title))
	}
	if patch.Description != nil {
		assignments = append(assignments, todos.Description.Value(*patch.Description))
	}
	if patch.Status != nil {
		assignments = append(assignments, todos.Status.Value(string(*patch.Status)))
	}
	if patch.Priority != nil {
		assignments = append(assignments, todos.Priority.Value(*patch.Priority))
	}
	if patch.DueAt != nil {
		assignments = append(assignments, nullableTime(todos.DueAt, *patch.DueAt))
	}

	// The version predicate is what makes this a compare-and-swap: another
	// writer that landed between the read above and this write leaves zero rows
	// affected rather than silently losing an update.
	info, err := q.Where(todos.ID.Eq(id), todos.Version.Eq(version)).UpdateSimple(assignments...)
	if err != nil {
		return nil, err
	}
	if info.RowsAffected != 1 {
		return nil, ErrVersionConflict
	}
	return s.find(ctx, id, false)
}

// Delete soft-deletes a live task under the same optimistic lock as Update:
// deleting a stale view is the same lost-update problem as writing one.
func (s *service) Delete(ctx context.Context, id guid.ID, version int) error {
	q, todos, err := s.scope(ctx, false)
	if err != nil {
		return err
	}
	now := s.now()
	info, err := q.Where(todos.ID.Eq(id), todos.Version.Eq(version)).UpdateSimple(
		nullableTime(todos.DeletedAt, &now),
		todos.DeletedBy.Value(actor(ctx)),
		todos.Version.Value(version+1),
		todos.UpdatedAt.Value(now),
		todos.UpdatedBy.Value(actor(ctx)),
	)
	if err != nil {
		return err
	}
	if info.RowsAffected == 1 {
		return nil
	}
	// Zero rows means the row is gone, already deleted, or the version moved.
	// Re-read to tell the caller which, rather than returning one vague error.
	if _, err := s.find(ctx, id, false); err != nil {
		return err
	}
	return ErrVersionConflict
}

// Restore clears the soft delete. It requires the caller to have seen the
// deleted row, so a task cannot be restored from its ID alone.
func (s *service) Restore(ctx context.Context, id guid.ID, version int) (*model.TodoRow, error) {
	current, err := s.find(ctx, id, true)
	if err != nil {
		return nil, err
	}
	if current.DeletedAt == nil {
		return nil, ErrNotDeleted
	}
	if current.Version != version {
		return nil, ErrVersionConflict
	}

	q, todos, err := s.scope(ctx, true)
	if err != nil {
		return nil, err
	}
	now := s.now()
	info, err := q.Where(todos.ID.Eq(id), todos.Version.Eq(version), todos.DeletedAt.IsNotNull()).
		UpdateSimple(
			todos.DeletedAt.Null(),
			todos.DeletedBy.Value(guid.ID(0)),
			todos.Version.Value(version+1),
			todos.UpdatedAt.Value(now),
			todos.UpdatedBy.Value(actor(ctx)),
		)
	if err != nil {
		return nil, err
	}
	if info.RowsAffected != 1 {
		return nil, ErrVersionConflict
	}
	return s.find(ctx, id, false)
}

// conditions adapts typed field expressions to the query builder's wider
// condition interface. Keeping predicates in terms of field.Expr means they
// stay compile-checked against the generated columns.
func conditions(exprs []field.Expr) []gen.Condition {
	out := make([]gen.Condition, len(exprs))
	for index, expr := range exprs {
		out[index] = expr
	}
	return out
}

// Page is one slice of a listing plus the total the caller needs to paginate.
type Page struct {
	Rows  []*model.TodoRow
	Total int64
	// Rendered names the columns the caller asked for, so the transport can omit
	// what was not selected instead of rendering it as a zero value.
	Rendered map[string]bool
	// Capabilities describes the filter contract, for a caller that asked for it.
	Capabilities *respx.Capabilities
}

// List returns a filtered, ordered, paginated slice of tasks.
//
// The count runs before ordering and paging: the total describes the filter,
// not the page, and ordering a count is wasted work. It is deliberately not in
// one transaction with the page — a concurrent write can leave the two
// momentarily inconsistent, which is the standard trade-off for offset paging
// and cheaper than holding a snapshot for every listing.
func (s *service) List(ctx context.Context, in ListInput) (Page, error) {
	_, todos, err := s.scope(ctx, in.Deleted)
	if err != nil {
		return Page{}, err
	}
	contract := filters(todos)

	where, err := contract.Parse(in.RawQuery)
	if err != nil {
		return Page{}, err
	}
	if term := strings.TrimSpace(in.Q); term != "" {
		where = append(where, searchTerm(todos, term))
	}
	order, err := contract.Ordering(in.Sort, maxSortKeys, todos.ID.Desc())
	if err != nil {
		return Page{}, err
	}
	// version is read whatever the caller selected, because the ETag is built
	// from it; id is read because a row without one cannot be acted on.
	columns, rendered, err := contract.Projection(in.Fields, "id", "version")
	if err != nil {
		return Page{}, err
	}
	rendered["id"] = true

	counting, _, err := s.scope(ctx, in.Deleted)
	if err != nil {
		return Page{}, err
	}
	total, err := counting.Where(conditions(where)...).Count()
	if err != nil {
		return Page{}, err
	}

	paging, _, err := s.scope(ctx, in.Deleted)
	if err != nil {
		return Page{}, err
	}
	q := paging.Where(conditions(where)...).Order(order...).Offset(in.Offset).Limit(in.Limit)
	if len(columns) > 0 {
		q = q.Select(columns...)
	}
	rows, err := q.Find()
	if err != nil {
		return Page{}, err
	}
	return Page{Rows: rows, Total: total, Rendered: rendered, Capabilities: contract.Describe()}, nil
}

// ImportAll writes every draft in one transaction.
//
// The all-or-nothing guarantee is what makes a rejected import safe to retry:
// a partial write would leave the user unable to tell whether to re-upload the
// whole file or only the failures, and re-uploading would duplicate rows
// because a task has no business key to match on.
func (s *service) ImportAll(ctx context.Context, drafts []Draft) (int, error) {
	if len(drafts) == 0 {
		return 0, nil
	}
	db := s.writer()
	if db == nil {
		return 0, ErrUnavailable
	}

	now := s.now()
	who := actor(ctx)
	rows := make([]*model.TodoRow, 0, len(drafts))
	for _, d := range drafts {
		next, err := s.ids.Next()
		if err != nil {
			return 0, errors.Join(ErrUnavailable, err)
		}
		rows = append(rows, &model.TodoRow{
			ID:          guid.ID(next),
			Title:       d.Title,
			Description: d.Description,
			Status:      string(d.Status),
			Priority:    d.Priority,
			DueAt:       d.DueAt,
			CompletedAt: completionFor("", d.Status, nil, now),
			Version:     1,
			CreatedAt:   now,
			CreatedBy:   who,
			UpdatedAt:   now,
			UpdatedBy:   who,
		})
	}

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return query.Bind(tx, s.runtime.TablePrefix()).WithContext(ctx).CreateInBatches(rows, importBatchSize)
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// importBatchSize bounds one INSERT's parameter count. Every dialect caps how
// many placeholders a statement may carry, and a batch of this size stays well
// under the lowest of them while keeping the round trips few.
const importBatchSize = 200

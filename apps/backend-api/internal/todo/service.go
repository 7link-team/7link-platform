package todo

import (
	"context"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/todo/model"
	"7link.com/platform/internal/todo/query"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformquery "7link.com/platform/pkg/query"
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

// service holds the use cases. It is the only place this package reaches the
// query package, so the soft-delete predicate and the table prefix cannot be
// forgotten at some other call site.
type service struct {
	runtime     *appenv.Runtime
	ids         guid.Source
	now         func() time.Time
	checker     authz.EntityDataPermissionChecker
	fields      authz.FieldPermissionChecker
	departments core.MemberDepartmentReader
	entities    core.EntityOwnerResolver
}

func (s *service) resolveOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db := s.writer()
	if db == nil {
		return authz.ResourceOwner{}, false, ErrUnavailable
	}
	var row model.TodoRow
	err := db.WithContext(ctx).Table(s.runtime.TablePrefix()+model.TodoRow{}.TableName()).
		Select("tenant_id, entity_id").Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	if row.TenantID == nil || row.EntityID == nil {
		return authz.ResourceOwner{}, false, nil
	}
	return authz.ResourceOwner{TenantID: *row.TenantID, EntityID: *row.EntityID}, true, nil
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
func (s *service) scope(ctx context.Context, includeDeleted bool, verb string) (query.ITodoRowDo, *query.Todos, error) {
	db := s.writer()
	if db == nil {
		return nil, nil, ErrUnavailable
	}
	todos := query.Bind(db, s.runtime.TablePrefix())
	q := todos.WithContext(ctx)
	if !includeDeleted {
		q = q.Where(todos.DeletedAt.IsNull())
	}
	q, err := s.constrain(ctx, q, todos, verb)
	return q, todos, err
}

// contract returns the filter declaration for description alone. It builds no
// statement, but the generated column set cannot be constructed without a
// datasource, so this reports ErrUnavailable like every other task operation
// when there is none. Route registration treats that as "nothing to document"
// rather than as a failure.
func (s *service) contract() (*platformquery.Fields, error) {
	db := s.writer()
	if db == nil {
		return nil, ErrUnavailable
	}
	return filters(query.Bind(db, s.runtime.TablePrefix())), nil
}

// actor uses the same trusted principal as the data authorization boundary.
func actor(ctx context.Context) guid.ID {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil {
		return 0
	}
	return claims.PrincipalID
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
	claims, departmentID, err := s.creationScope(ctx, "create")
	if err != nil {
		return nil, err
	}
	constraint, err := s.fieldConstraint(ctx)
	if err != nil {
		return nil, err
	}
	if err := checkFieldWrites(constraint, draft.submitted(), true); err != nil {
		return nil, err
	}

	next, err := s.ids.Next()
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}

	now := s.now()
	who := actor(ctx)
	row := &model.TodoRow{
		ID:       guid.ID(next),
		TenantID: &claims.TenantID, EntityID: &claims.EntityID, DepartmentID: departmentID,
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
	if err := query.Bind(s.writer(), s.runtime.TablePrefix()).WithContext(ctx).Create(row); err != nil {
		return nil, err
	}
	return row, nil
}

// Get returns one live task.
func (s *service) Get(ctx context.Context, id guid.ID) (*model.TodoRow, error) {
	return s.find(ctx, id, false, "view")
}

// GetIncludingDeleted returns a task whether or not it is soft-deleted, for the
// restore path and for list queries that opt in.
func (s *service) GetIncludingDeleted(ctx context.Context, id guid.ID) (*model.TodoRow, error) {
	return s.find(ctx, id, true, "view")
}

func (s *service) find(ctx context.Context, id guid.ID, includeDeleted bool, verb string) (*model.TodoRow, error) {
	q, todos, err := s.scope(ctx, includeDeleted, verb)
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
	constraint, err := s.fieldConstraint(ctx)
	if err != nil {
		return nil, err
	}
	if err := checkFieldWrites(constraint, patch.submitted(), false); err != nil {
		return nil, err
	}
	current, err := s.find(ctx, id, false, "update")
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

	q, todos, err := s.scope(ctx, false, "update")
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
	return s.find(ctx, id, false, "update")
}

// Delete soft-deletes a live task under the same optimistic lock as Update:
// deleting a stale view is the same lost-update problem as writing one.
func (s *service) Delete(ctx context.Context, id guid.ID, version int) error {
	q, todos, err := s.scope(ctx, false, "delete")
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
	if _, err := s.find(ctx, id, false, "delete"); err != nil {
		return err
	}
	return ErrVersionConflict
}

// Restore clears the soft delete. It requires the caller to have seen the
// deleted row, so a task cannot be restored from its ID alone.
func (s *service) Restore(ctx context.Context, id guid.ID, version int) (*model.TodoRow, error) {
	current, err := s.find(ctx, id, true, "update")
	if err != nil {
		return nil, err
	}
	if current.DeletedAt == nil {
		return nil, ErrNotDeleted
	}
	if current.Version != version {
		return nil, ErrVersionConflict
	}

	q, todos, err := s.scope(ctx, true, "update")
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
	return s.find(ctx, id, false, "update")
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
	return s.list(ctx, in, "view")
}

// Export applies the export permission independently of interactive viewing.
func (s *service) Export(ctx context.Context, in ListInput) (Page, error) {
	return s.list(ctx, in, "export")
}

func (s *service) list(ctx context.Context, in ListInput, verb string) (Page, error) {
	_, todos, err := s.scope(ctx, in.Deleted, verb)
	if err != nil {
		return Page{}, err
	}
	constraint, err := s.fieldConstraint(ctx)
	if err != nil {
		return Page{}, err
	}
	readable := readableFields(constraint)
	if in.Deleted && !readable["deleted_at"] {
		return Page{}, ErrForbidden
	}
	fullContract := filters(todos)
	contract := fullContract.Readable(readable)

	where, err := contract.Parse(in.RawQuery)
	if err != nil {
		return Page{}, err
	}
	if term := strings.TrimSpace(in.Q); term != "" {
		if !readable["title"] || !readable["description"] {
			return Page{}, ErrForbidden
		}
		if match := searchTerm(todos, term); match != nil {
			where = append(where, match)
		}
	}
	sortBy := in.Sort
	if sortBy == "" && readable["created_at"] {
		sortBy = "-created_at"
	}
	order, err := contract.Ordering(sortBy, maxSortKeys, todos.ID.Desc())
	if err != nil {
		return Page{}, err
	}
	// version is read whatever the caller selected, because the ETag is built
	// from it; id is read because a row without one cannot be acted on.
	_, requested, err := fullContract.Projection(in.Fields, "id", "version")
	if err != nil {
		return Page{}, err
	}
	rendered := renderedFields(requested, constraint)
	rendered["id"] = true
	selected := []string{"id", "version"}
	for _, name := range contract.SelectableNames() {
		if rendered[name] && name != "id" && name != "version" {
			selected = append(selected, name)
		}
	}
	columns, _, err := contract.Projection(strings.Join(selected, ","))
	if err != nil {
		return Page{}, err
	}

	counting, _, err := s.scope(ctx, in.Deleted, verb)
	if err != nil {
		return Page{}, err
	}
	total, err := counting.Where(conditions(where)...).Count()
	if err != nil {
		return Page{}, err
	}

	paging, _, err := s.scope(ctx, in.Deleted, verb)
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
	for index, row := range rows {
		rows[index] = maskedRow(row, constraint)
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
	claims, departmentID, err := s.creationScope(ctx, "import")
	if err != nil {
		return 0, err
	}
	if len(drafts) == 0 {
		return 0, nil
	}
	constraint, err := s.fieldConstraint(ctx)
	if err != nil {
		return 0, err
	}
	for _, draft := range drafts {
		if err := checkFieldWrites(constraint, draft.submitted(), true); err != nil {
			return 0, err
		}
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
			ID:       guid.ID(next),
			TenantID: &claims.TenantID, EntityID: &claims.EntityID, DepartmentID: departmentID,
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

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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

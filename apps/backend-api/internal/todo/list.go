package todo

import (
	"database/sql/driver"
	"net/url"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/todo/query"
	platformquery "7link.com/platform/pkg/query"
	"gorm.io/gen/field"
)

// maxSortKeys bounds how much ordering a caller can ask for. Beyond a few keys
// no index can serve the query, and the identifier tiebreaker already makes the
// order total.
const maxSortKeys = 3

// transportParams are consumed by the handler rather than by a filter. They are
// declared so an unrecognized parameter can be rejected: silently ignoring one
// is how a typo returns unfiltered data and an injected parameter goes unseen.
var transportParams = []string{"search", "deleted", "sort", "fields", "offset", "limit", "meta", "lang", "format"}

// filters is this module's query contract. It drives parsing, ordering, field
// selection and the capability descriptor alike, so the three cannot disagree.
func filters(cols *query.Todos) *platformquery.Fields {
	return platformquery.New(
		platformquery.String("title", cols.Title).Ops(platformquery.Eq, platformquery.Like),
		// description is the one column whose empty value is both reachable and
		// meaningful: it is NOT NULL and defaults to "", so "no description" is
		// an ordinary state rather than a missing row. Like alone cannot ask for
		// it — a pattern of nothing is rejected — so isEmpty is the only way.
		platformquery.String("description", cols.Description).Ops(platformquery.Like).Empty(),
		platformquery.Enum("status", cols.Status, StatusStrings()).
			Ops(platformquery.Eq, platformquery.NotEq, platformquery.In),
		platformquery.Int("priority", cols.Priority).
			Ops(platformquery.Eq, platformquery.Gte, platformquery.Lte).
			Range(MinPriority, MaxPriority),
		platformquery.Time("due_at", cols.DueAt).
			Ops(platformquery.Gte, platformquery.Gt, platformquery.Lte, platformquery.Lt).Nullable(),
		platformquery.Time("completed_at", cols.CompletedAt).
			Ops(platformquery.Gte, platformquery.Gt, platformquery.Lte, platformquery.Lt).Nullable(),
		platformquery.Time("created_at", cols.CreatedAt).
			Ops(platformquery.Gte, platformquery.Gt, platformquery.Lte, platformquery.Lt),
		platformquery.Time("updated_at", cols.UpdatedAt).
			Ops(platformquery.Gte, platformquery.Gt, platformquery.Lte, platformquery.Lt),
		platformquery.ID("created_by", cols.CreatedBy, parseActorID).
			Ops(platformquery.Eq, platformquery.In),
	).Sortable(map[string]platformquery.SortKey{
		"created_at":   {Column: cols.CreatedAt},
		"updated_at":   {Column: cols.UpdatedAt},
		"priority":     {Column: cols.Priority},
		"title":        {Column: cols.Title},
		"status":       {Column: cols.Status},
		"due_at":       {Column: cols.DueAt, Nullable: true},
		"completed_at": {Column: cols.CompletedAt, Nullable: true},
	}).Selectable(map[string]field.Expr{
		"id": cols.ID, "title": cols.Title, "description": cols.Description,
		"status": cols.Status, "priority": cols.Priority,
		"due_at": cols.DueAt, "completed_at": cols.CompletedAt, "version": cols.Version,
		"created_at": cols.CreatedAt, "created_by": cols.CreatedBy,
		"updated_at": cols.UpdatedAt, "updated_by": cols.UpdatedBy,
		"deleted_at": cols.DeletedAt, "deleted_by": cols.DeletedBy,
	}).Reserve(transportParams...)
}

func parseActorID(raw string) (driver.Valuer, error) {
	id, err := guid.Parse(raw)
	if err != nil || id.Zero() {
		return nil, err
	}
	return id, nil
}

// searchTerm builds the cross-field match behind the search parameter. It is not a
// field filter — there is no field to name and no operator to choose — so it
// stays a plain parameter rather than acquiring an operator prefix.
//
// It returns nil for a term that escaping empties out. EscapeLike removes the
// wildcards, so a search of nothing but them survives the caller's blank check and
// then builds LIKE '%%' on both columns — a full scan that ORs to every row.
// Dropping the predicate returns that same set without asking the database to
// prove it. Unlike a filter this is not an error: search is a search box, and a
// search for nothing is the blank search the caller already handles.
func searchTerm(cols *query.Todos, term string) field.Expr {
	escaped := platformquery.EscapeLike(term)
	if escaped == "" {
		return nil
	}
	pattern := "%" + escaped + "%"
	return field.Or(cols.Title.Like(pattern), cols.Description.Like(pattern))
}

// ListInput carries the parameters the transport itself consumes. The filters
// are not declared here: they are parsed from the raw query against the
// contract above, which is what lets one declaration serve parsing, ordering,
// selection and self-description at once.
//
// huma is told about the filter parameters through Operation.Parameters, so
// they still appear in the generated documentation.
type ListInput struct {
	Search  string `query:"search" maxLength:"128" doc:"matches title or description"`
	Deleted bool   `query:"deleted" doc:"include soft-deleted tasks"`
	Fields  string `query:"fields" doc:"comma-separated subset of columns to return"`
	Sort    string `query:"sort" doc:"comma-separated keys; - prefixes a descending key"`
	Offset  int    `query:"offset" default:"0" minimum:"0"`
	Limit   int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Meta    string `query:"meta" enum:"capabilities" doc:"request the filter and sort contract alongside the page"`

	// RawQuery carries the filter parameters. huma binds only declared fields,
	// so the middleware stores the request's query on the context and the
	// handler reads it from there.
	//
	// It carries no tag on purpose. huma reads a parameter tag as a literal
	// name, not as a struct-tag sentinel: `query:"-"` does not skip the field,
	// it declares a parameter actually named "-" that appears in OpenAPI and
	// fails to bind. A field with no parameter tag is the way to opt out.
	RawQuery url.Values
}

// wantsCapabilities reports whether the caller asked for the contract. It is
// off by default: the descriptor is static per endpoint, so repeating it on
// every page would be pure waste.
func (in ListInput) wantsCapabilities() bool { return in.Meta == "capabilities" }

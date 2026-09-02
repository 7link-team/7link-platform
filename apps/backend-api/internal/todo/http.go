package todo

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	todoi18n "7link.com/platform/internal/todo/i18n"
	"7link.com/platform/internal/todo/model"
	"7link.com/platform/pkg/extension/huma/respx"
	platformquery "7link.com/platform/pkg/query"
	"github.com/danielgtaylor/huma/v2"
)

const basePath = "/todos"

// View is the read shape. Every field except ID is a pointer so a future
// sparse-fieldset response can omit what was not selected; today every field is
// populated.
type View struct {
	ID          guid.ID    `json:"id"`
	Title       *string    `json:"title,omitempty"`
	Description *string    `json:"description,omitempty"`
	Status      *string    `json:"status,omitempty"`
	Priority    *int       `json:"priority,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Version     *int       `json:"version,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	CreatedBy   *guid.ID   `json:"created_by,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	UpdatedBy   *guid.ID   `json:"updated_by,omitempty"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
	DeletedBy   *guid.ID   `json:"deleted_by,omitempty"`
}

// allFields renders every column, for the single-task responses that have no
// field selection.
var allFields = func() map[string]bool {
	rendered := map[string]bool{}
	for _, name := range []string{
		"id", "title", "description", "status", "priority", "due_at", "completed_at",
		"version", "created_at", "created_by", "updated_at", "updated_by",
		"deleted_at", "deleted_by",
	} {
		rendered[name] = true
	}
	return rendered
}()

func viewOf(row *model.TodoRow) View { return viewOfSelected(row, allFields) }

// viewOfSelected renders only the columns the caller asked for. An unselected
// column stays a nil pointer and is omitted.
//
// The two empty columns are not written the same way, and the difference is
// deliberate. An identifier is a value on the row, so a selected one is always
// present and a zero renders as null — an actor no authentication middleware
// has supplied. A nullable timestamp is already a pointer, so an empty one is
// indistinguishable from an unselected one and drops out of the response; that
// is the shape a cleared due date has, and it is asserted as such.
func viewOfSelected(row *model.TodoRow, rendered map[string]bool) View {
	view := View{ID: row.ID}
	if rendered["title"] {
		view.Title = &row.Title
	}
	if rendered["description"] {
		view.Description = &row.Description
	}
	if rendered["status"] {
		view.Status = &row.Status
	}
	if rendered["priority"] {
		view.Priority = &row.Priority
	}
	if rendered["due_at"] {
		view.DueAt = row.DueAt
	}
	if rendered["completed_at"] {
		view.CompletedAt = row.CompletedAt
	}
	if rendered["version"] {
		view.Version = &row.Version
	}
	if rendered["created_at"] {
		view.CreatedAt = &row.CreatedAt
	}
	if rendered["created_by"] {
		view.CreatedBy = &row.CreatedBy
	}
	if rendered["updated_at"] {
		view.UpdatedAt = &row.UpdatedAt
	}
	if rendered["updated_by"] {
		view.UpdatedBy = &row.UpdatedBy
	}
	if rendered["deleted_at"] {
		view.DeletedAt = row.DeletedAt
	}
	if rendered["deleted_by"] {
		view.DeletedBy = &row.DeletedBy
	}
	return view
}

// The path identifier is bound as a string rather than as guid.ID. huma binds
// parameters by Go kind, so it parses that int64 as a number and then validates
// the number against guid.ID's string schema, rejecting every value with 422.
// Parsing here keeps the OpenAPI contract honest and yields a clean 404 for an
// identifier that cannot name a stored task.
//
// The embedded types are exported on purpose: huma cannot reach fields of an
// unexported embedded struct and skips them without error, which leaves every
// such parameter and header silently empty.

// IDInput carries the path identifier shared by every single-task operation.
type IDInput struct {
	ID string `path:"id" doc:"task identifier"`
}

// VersionedInput adds the If-Match precondition, so a caller that has not read
// the task cannot overwrite it.
type VersionedInput struct {
	IDInput
	IfMatch string `header:"If-Match" doc:"version of the task being replaced, as returned in ETag"`
}

// taskID parses a path identifier. An unparsable or non-positive value can
// never name a stored task, so it is reported as absent rather than as a
// malformed request.
func taskID(raw string) (guid.ID, error) {
	id, err := guid.Parse(raw)
	if err != nil || id.Zero() {
		return 0, huma.Error404NotFound(todoi18n.NotFound)
	}
	return id, nil
}

type createInput struct {
	Body struct {
		Title       string     `json:"title" minLength:"1" maxLength:"255" doc:"task title"`
		Description string     `json:"description,omitempty" doc:"free-form details"`
		Status      string     `json:"status,omitempty" enum:"open,doing,done" default:"open" doc:"lifecycle label"`
		Priority    int        `json:"priority,omitempty" minimum:"1" maximum:"5" default:"3" doc:"1 is most urgent"`
		DueAt       *time.Time `json:"due_at,omitempty" doc:"when the task is due"`
	}
}

type patchInput struct {
	VersionedInput
	Body struct {
		Title       *string    `json:"title,omitempty" minLength:"1" maxLength:"255"`
		Description *string    `json:"description,omitempty"`
		Status      *string    `json:"status,omitempty" enum:"open,doing,done"`
		Priority    *int       `json:"priority,omitempty" minimum:"1" maximum:"5"`
		DueAt       dueAtPatch `json:"due_at,omitempty" doc:"omit to leave unchanged; null to clear"`
	}
}

// dueAtPatch distinguishes the three states JSON allows for an optional
// nullable field: absent, null, and a value.
//
// It must be a value field, not a pointer. encoding/json sets a pointer field
// to nil when the JSON is null and never calls the custom unmarshaller, which
// collapses "clear it" into "leave it alone" — the very distinction this type
// exists to preserve. As a value field the unmarshaller runs for null too, and
// Set records that the client mentioned the field at all.
type dueAtPatch struct {
	Set   bool
	Value *time.Time
}

func (d *dueAtPatch) UnmarshalJSON(raw []byte) error {
	d.Set = true
	if string(raw) == "null" {
		d.Value = nil
		return nil
	}
	var parsed time.Time
	if err := parsed.UnmarshalJSON(raw); err != nil {
		return err
	}
	d.Value = &parsed
	return nil
}

func (d dueAtPatch) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Format: "date-time", Nullable: true}
}

// etag renders a version as an entity tag. Versions are opaque to the client:
// it reads one from a response and sends the same value back.
func etag(version int) string { return `"` + strconv.Itoa(version) + `"` }

// parseIfMatch accepts both the quoted entity-tag form and a bare number, since
// hand-written clients frequently send the latter.
func parseIfMatch(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, huma.Error428PreconditionRequired(todoi18n.VersionRequired)
	}
	trimmed = strings.TrimPrefix(trimmed, "W/")
	trimmed = strings.Trim(trimmed, `"`)
	version, err := strconv.Atoi(trimmed)
	if err != nil || version < 1 {
		return 0, huma.Error400BadRequest(todoi18n.VersionInvalid)
	}
	return version, nil
}

// httpError maps a domain error to its HTTP status. Every case here is standard
// HTTP semantics, so the envelope's code mirrors the status and this module
// defines no business codes of its own.
func httpError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound(todoi18n.NotFound)
	case errors.Is(err, ErrDeleted):
		return huma.Error410Gone(todoi18n.Deleted)
	case errors.Is(err, ErrNotDeleted):
		return huma.Error409Conflict(todoi18n.NotDeleted)
	case errors.Is(err, ErrVersionConflict):
		return huma.Error409Conflict(todoi18n.VersionConflict)
	case errors.Is(err, ErrUnavailable):
		return huma.Error503ServiceUnavailable(todoi18n.Unavailable)
	}
	var fieldErr FieldError
	if errors.As(err, &fieldErr) {
		return huma.Error422UnprocessableEntity(fieldErr.Error())
	}
	// A query-contract violation names the offending parameter and never echoes
	// the caller's value, so it is safe to return verbatim.
	var queryErr platformquery.Error
	if errors.As(err, &queryErr) {
		return huma.Error422UnprocessableEntity(queryErr.Error())
	}
	return err
}

// taskBody carries the envelope plus the ETag header, so a caller always has
// the version it needs for its next write.
type taskBody struct {
	ETag string `header:"ETag"`
	Body respx.Envelope[View]
}

func taskResponse(ctx context.Context, row *model.TodoRow) *taskBody {
	return &taskBody{ETag: etag(row.Version), Body: respx.OK(ctx, viewOf(row)).Body}
}

func registerHTTP(api huma.API, svc *service, filterParams []*huma.Param) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-todo",
		Method:        http.MethodPost,
		Path:          basePath,
		Summary:       "Create a task",
		Tags:          []string{"Todo"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createInput) (*taskBody, error) {
		draft, err := draftOf(in)
		if err != nil {
			return nil, httpError(err)
		}
		row, err := svc.Create(ctx, draft)
		if err != nil {
			return nil, httpError(err)
		}
		return taskResponse(ctx, row), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-todos",
		Method:      http.MethodGet,
		Path:        basePath,
		Summary:     "List tasks",
		Description: "Filters are AND-combined. Sorting accepts up to three keys; the identifier is " +
			"always appended so paging stays stable when sort values tie.",
		Tags:       []string{"Todo"},
		Parameters: filterParams,
	}, func(ctx context.Context, in *ListInput) (*respx.Body[[]View], error) {
		in.RawQuery = rawQueryFrom(ctx)
		page, err := svc.List(ctx, *in)
		if err != nil {
			return nil, httpError(err)
		}
		views := make([]View, len(page.Rows))
		for index, row := range page.Rows {
			views[index] = viewOfSelected(row, page.Rendered)
		}
		body := respx.List(ctx, views, respx.Page{
			Offset: in.Offset, Limit: in.Limit, Count: len(views), Total: page.Total,
		})
		if in.wantsCapabilities() {
			body.Body.Meta.Capabilities = page.Capabilities
		}
		return body, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-todo",
		Method:      http.MethodGet,
		Path:        basePath + "/{id}",
		Summary:     "Read a task",
		Tags:        []string{"Todo"},
	}, func(ctx context.Context, in *IDInput) (*taskBody, error) {
		id, err := taskID(in.ID)
		if err != nil {
			return nil, err
		}
		row, err := svc.Get(ctx, id)
		if err != nil {
			return nil, httpError(err)
		}
		return taskResponse(ctx, row), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-todo",
		Method:      http.MethodPatch,
		Path:        basePath + "/{id}",
		Summary:     "Update a task",
		Description: "Requires If-Match carrying the version last read, so a concurrent write cannot be lost.",
		Tags:        []string{"Todo"},
	}, func(ctx context.Context, in *patchInput) (*taskBody, error) {
		version, err := parseIfMatch(in.IfMatch)
		if err != nil {
			return nil, err
		}
		id, err := taskID(in.ID)
		if err != nil {
			return nil, err
		}
		patch, err := patchOf(in)
		if err != nil {
			return nil, httpError(err)
		}
		row, err := svc.Update(ctx, id, version, patch)
		if err != nil {
			return nil, httpError(err)
		}
		return taskResponse(ctx, row), nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-todo",
		Method:        http.MethodDelete,
		Path:          basePath + "/{id}",
		Summary:       "Delete a task",
		Description:   "Soft delete. Requires If-Match: deleting a stale view loses an update just as writing one does.",
		Tags:          []string{"Todo"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *VersionedInput) (*struct{}, error) {
		version, err := parseIfMatch(in.IfMatch)
		if err != nil {
			return nil, err
		}
		id, err := taskID(in.ID)
		if err != nil {
			return nil, err
		}
		if err := svc.Delete(ctx, id, version); err != nil {
			return nil, httpError(err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "restore-todo",
		Method:      http.MethodPost,
		Path:        basePath + "/{id}/restore",
		Summary:     "Restore a deleted task",
		Description: "Requires If-Match, so a task cannot be restored from its identifier alone.",
		Tags:        []string{"Todo"},
	}, func(ctx context.Context, in *VersionedInput) (*taskBody, error) {
		version, err := parseIfMatch(in.IfMatch)
		if err != nil {
			return nil, err
		}
		id, err := taskID(in.ID)
		if err != nil {
			return nil, err
		}
		row, err := svc.Restore(ctx, id, version)
		if err != nil {
			return nil, httpError(err)
		}
		return taskResponse(ctx, row), nil
	})
}

// draftOf validates the create body. huma has already enforced the tag-level
// constraints; this adds the rules the tags cannot express.
func draftOf(in *createInput) (Draft, error) {
	title, err := validateTitle(in.Body.Title)
	if err != nil {
		return Draft{}, err
	}
	status := Status(in.Body.Status)
	if status == "" {
		status = StatusOpen
	}
	if err := validateStatus(status); err != nil {
		return Draft{}, err
	}
	priority := in.Body.Priority
	if priority == 0 {
		priority = DefaultPriority
	}
	if err := validatePriority(priority); err != nil {
		return Draft{}, err
	}
	return Draft{
		Title:       title,
		Description: in.Body.Description,
		Status:      status,
		Priority:    priority,
		DueAt:       in.Body.DueAt,
	}, nil
}

func patchOf(in *patchInput) (Patch, error) {
	var patch Patch
	if in.Body.Title != nil {
		title, err := validateTitle(*in.Body.Title)
		if err != nil {
			return Patch{}, err
		}
		patch.Title = &title
	}
	if in.Body.Description != nil {
		patch.Description = in.Body.Description
	}
	if in.Body.Status != nil {
		status := Status(*in.Body.Status)
		if err := validateStatus(status); err != nil {
			return Patch{}, err
		}
		patch.Status = &status
	}
	if in.Body.Priority != nil {
		if err := validatePriority(*in.Body.Priority); err != nil {
			return Patch{}, err
		}
		patch.Priority = in.Body.Priority
	}
	if in.Body.DueAt.Set {
		value := in.Body.DueAt.Value
		patch.DueAt = &value
	}
	return patch, nil
}

// TemplateRequest asks for a blank import file.
type TemplateRequest struct {
	Format string `query:"format" enum:"csv,xlsx" default:"csv"`
	Sample bool   `query:"sample" doc:"include an example row showing each column's accepted form"`
}

// ExportRequest reuses the list filter, so an export contains exactly what the
// same filter shows on screen.
type ExportRequest struct {
	ListInput
	Format string `query:"format" enum:"csv,xlsx" default:"csv"`
}

// ImportRequest carries the uploaded file. huma parses the multipart body and
// reports the declared size, which is checked before the rows are read.
type ImportRequest struct {
	RawBody huma.MultipartFormFiles[struct {
		// application/octet-stream is huma's wildcard: its validator accepts
		// any upload when the list contains it. That is deliberate here —
		// browsers, curl and spreadsheet tools label a CSV as text/csv,
		// application/vnd.ms-excel, text/plain or octet-stream
		// interchangeably, and rejecting on that label would turn a correct
		// file into a support ticket. The format parameter decides how the
		// bytes are read, and a file that does not parse is reported then.
		//
		// Omitting the tag does not work: huma splits an empty tag into one
		// empty string rather than falling back to a default, so nothing
		// matches and every upload is rejected.
		File huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true"`
	}]
	Format string `query:"format" enum:"csv,xlsx" default:"csv"`
}

// fileBody streams a generated file. huma.StreamResponse keeps the transfer out
// of the JSON envelope while staying inside the framework, so the operation is
// still described in OpenAPI.
func fileResponse(name string, format Format, payload []byte) *huma.StreamResponse {
	return &huma.StreamResponse{
		Body: func(ctx huma.Context) {
			ctx.SetHeader("Content-Type", format.contentType())
			ctx.SetHeader("Content-Disposition", `attachment; filename="`+name+"."+string(format)+`"`)
			_, _ = ctx.BodyWriter().Write(payload)
		},
	}
}

func parseFormat(raw string) (Format, error) {
	format := Format(strings.TrimSpace(strings.ToLower(raw)))
	if format == "" {
		format = FormatCSV
	}
	if !format.valid() {
		return "", huma.Error422UnprocessableEntity(todoi18n.FormatUnsupported)
	}
	return format, nil
}

func registerTransferHTTP(api huma.API, svc *service, filterParams []*huma.Param) {
	huma.Register(api, huma.Operation{
		OperationID: "download-todo-import-template",
		Method:      http.MethodGet,
		Path:        basePath + "/import-template",
		Summary:     "Download the import template",
		Description: "Headers follow the request locale, and the importer accepts a template filled in " +
			"under any supported locale.",
		Tags: []string{"Todo"},
	}, func(ctx context.Context, in *TemplateRequest) (*huma.StreamResponse, error) {
		format, err := parseFormat(in.Format)
		if err != nil {
			return nil, err
		}
		payload, err := templateGrid(ctx, in.Sample).encode(format)
		if err != nil {
			return nil, err
		}
		return fileResponse("todo-import-template", format, payload), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "export-todos",
		Method:      http.MethodGet,
		Path:        basePath + "/export",
		Summary:     "Export tasks",
		Description: "Accepts every list filter. The result is not paginated but is capped; a filter " +
			"matching more rows is rejected rather than silently truncated.",
		Tags:       []string{"Todo"},
		Parameters: filterParams,
	}, func(ctx context.Context, in *ExportRequest) (*huma.StreamResponse, error) {
		format, err := parseFormat(in.Format)
		if err != nil {
			return nil, err
		}
		cols, err := exportColumns(in.Fields)
		if err != nil {
			return nil, httpError(err)
		}

		limits := in.Limits()
		// The export reads whole rows: the fields parameter selects which
		// columns are written, not which are read, so every renderer has its
		// value.
		query := in.ListInput
		query.RawQuery = rawQueryFrom(ctx)
		query.Fields = ""
		query.Offset = 0
		query.Limit = limits.MaxRows + 1

		page, err := svc.List(ctx, query)
		if err != nil {
			return nil, httpError(err)
		}
		if len(page.Rows) > limits.MaxRows {
			return nil, huma.Error422UnprocessableEntity(todoi18n.ExportTooManyRows)
		}

		payload, err := exportGrid(ctx, cols, page.Rows).encode(format)
		if err != nil {
			return nil, err
		}
		return fileResponse("todos", format, payload), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "import-todos",
		Method:      http.MethodPost,
		Path:        basePath + "/import",
		Summary:     "Import tasks",
		Description: "Every row is validated first. If any row is invalid nothing is written and each " +
			"problem is reported, so a corrected file can be re-uploaded whole without duplicating rows.",
		Tags: []string{"Todo"},
	}, func(ctx context.Context, in *ImportRequest) (*respx.Body[ImportReport], error) {
		format, err := parseFormat(in.Format)
		if err != nil {
			return nil, err
		}
		limits := in.Limits()
		file := in.RawBody.Data().File
		if limits.MaxBytes > 0 && file.Size > limits.MaxBytes {
			return nil, huma.Error422UnprocessableEntity(todoi18n.ImportTooLarge)
		}

		table, err := decodeGrid(file, format)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(todoi18n.FileUnreadable)
		}
		drafts, rowErrors, err := parseImport(ctx, table, limits)
		if err != nil {
			return nil, err
		}
		if len(rowErrors) > 0 {
			return nil, huma.Error422UnprocessableEntity(
				todoi18n.ImportRowsInvalid, &rowErrorDetail{Errors: rowErrors})
		}

		imported, err := svc.ImportAll(ctx, drafts)
		if err != nil {
			return nil, httpError(err)
		}
		return respx.OK(ctx, ImportReport{Imported: imported}), nil
	})
}

// rowErrorDetail carries the per-row failures through huma's error detail
// channel, so the response keeps the platform envelope and still names every
// problem.
type rowErrorDetail struct {
	Errors []RowError `json:"errors"`
}

func (d *rowErrorDetail) Error() string { return "import rows are invalid" }

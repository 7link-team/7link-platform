package todo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	todoi18n "7link.com/platform/internal/todo/i18n"
	"7link.com/platform/internal/todo/model"
	platformi18n "7link.com/platform/pkg/i18n"
)

// column is one field of the transfer format. The same definition drives the
// template, the importer and the exporter, so a column can never mean one thing
// in the file a user downloads and another in the file they upload back.
type column struct {
	key       string
	headerKey string

	// importable marks a column the importer accepts. Server-maintained
	// columns are exported for reference and ignored on the way back in, which
	// is what lets an exported file be re-imported unchanged.
	importable bool
	required   bool

	// sample is the value the template shows when asked for one. It doubles as
	// format documentation: a date column shows a date in the accepted layout.
	sample string

	parse  func(raw string, draft *Draft) error
	render func(row *model.TodoRow) string
}

// transferTimeLayout is the layout the template advertises and the importer
// accepts first. RFC 3339 is also accepted, so a file exported by this service
// re-imports without the user reformatting anything.
const transferTimeLayout = "2006-01-02 15:04:05"

var columns = []column{
	{
		key: "id", headerKey: todoi18n.ColumnID,
		render: func(row *model.TodoRow) string { return renderID(row.ID) },
	},
	{
		key: "title", headerKey: todoi18n.ColumnTitle,
		importable: true, required: true, sample: "Quarterly report",
		parse: func(raw string, draft *Draft) error {
			title, err := validateTitle(raw)
			if err != nil {
				return err
			}
			draft.Title = title
			return nil
		},
		render: func(row *model.TodoRow) string { return row.Title },
	},
	{
		key: "description", headerKey: todoi18n.ColumnDescription,
		importable: true, sample: "Collect receipts from the team",
		parse: func(raw string, draft *Draft) error {
			draft.Description = strings.TrimSpace(raw)
			return nil
		},
		render: func(row *model.TodoRow) string { return row.Description },
	},
	{
		key: "status", headerKey: todoi18n.ColumnStatus,
		importable: true, sample: string(StatusOpen),
		parse: func(raw string, draft *Draft) error {
			value := strings.TrimSpace(raw)
			if value == "" {
				draft.Status = StatusOpen
				return nil
			}
			status := Status(strings.ToLower(value))
			if err := validateStatus(status); err != nil {
				return err
			}
			draft.Status = status
			return nil
		},
		render: func(row *model.TodoRow) string { return row.Status },
	},
	{
		key: "priority", headerKey: todoi18n.ColumnPriority,
		importable: true, sample: strconv.Itoa(DefaultPriority),
		parse: func(raw string, draft *Draft) error {
			value := strings.TrimSpace(raw)
			if value == "" {
				draft.Priority = DefaultPriority
				return nil
			}
			priority, err := strconv.Atoi(value)
			if err != nil {
				return FieldError{Field: "priority", Message: "must be a whole number between 1 and 5"}
			}
			if err := validatePriority(priority); err != nil {
				return err
			}
			draft.Priority = priority
			return nil
		},
		render: func(row *model.TodoRow) string { return strconv.Itoa(row.Priority) },
	},
	{
		key: "due_at", headerKey: todoi18n.ColumnDueAt,
		importable: true, sample: "2026-12-31 18:00:00",
		parse: func(raw string, draft *Draft) error {
			value := strings.TrimSpace(raw)
			if value == "" {
				draft.DueAt = nil
				return nil
			}
			parsed, err := parseTransferTime(value)
			if err != nil {
				return FieldError{
					Field:   "due_at",
					Message: "must look like " + transferTimeLayout,
				}
			}
			draft.DueAt = &parsed
			return nil
		},
		render: func(row *model.TodoRow) string { return renderTime(row.DueAt) },
	},
	{
		key: "completed_at", headerKey: todoi18n.ColumnCompletedAt,
		render: func(row *model.TodoRow) string { return renderTime(row.CompletedAt) },
	},
	{
		key: "version", headerKey: todoi18n.ColumnVersion,
		render: func(row *model.TodoRow) string { return strconv.Itoa(row.Version) },
	},
	{
		key: "created_at", headerKey: todoi18n.ColumnCreatedAt,
		render: func(row *model.TodoRow) string { return renderTime(&row.CreatedAt) },
	},
	{
		key: "created_by", headerKey: todoi18n.ColumnCreatedBy,
		render: func(row *model.TodoRow) string { return renderID(row.CreatedBy) },
	},
	{
		key: "updated_at", headerKey: todoi18n.ColumnUpdatedAt,
		render: func(row *model.TodoRow) string { return renderTime(&row.UpdatedAt) },
	},
	{
		key: "updated_by", headerKey: todoi18n.ColumnUpdatedBy,
		render: func(row *model.TodoRow) string { return renderID(row.UpdatedBy) },
	},
}

func renderTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(transferTimeLayout)
}

func renderID(id interface {
	Zero() bool
	String() string
}) string {
	if id.Zero() {
		return ""
	}
	return id.String()
}

// parseTransferTime accepts the layout the template advertises and RFC 3339, so
// a file this service exported re-imports without reformatting.
func parseTransferTime(value string) (time.Time, error) {
	for _, layout := range []string{transferTimeLayout, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("todo: unrecognized time %q", value)
}

// importableColumns are the columns the template offers and the importer reads.
func importableColumns() []column {
	out := make([]column, 0, len(columns))
	for _, c := range columns {
		if c.importable {
			out = append(out, c)
		}
	}
	return out
}

// exportColumns resolves the columns an export writes. An empty selection means
// every column; a selection is validated against the same whitelist the list
// endpoint uses, so the two agree on what a field name means.
func exportColumns(fields string) ([]column, error) {
	if strings.TrimSpace(fields) == "" {
		return columns, nil
	}
	wanted := map[string]bool{}
	for _, raw := range strings.Split(fields, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		wanted[name] = true
	}
	if len(wanted) == 0 {
		return nil, FieldError{Field: "fields", Message: "must name at least one field"}
	}

	out := make([]column, 0, len(wanted))
	for _, c := range columns {
		if wanted[c.key] {
			out = append(out, c)
			delete(wanted, c.key)
		}
	}
	for name := range wanted {
		return nil, FieldError{
			Field:   "fields",
			Message: "unknown field " + name + "; accepts " + strings.Join(transferFieldNames(), ", "),
		}
	}
	return out, nil
}

func transferFieldNames() []string {
	names := make([]string, len(columns))
	for index, c := range columns {
		names[index] = c.key
	}
	return names
}

// headerIndex maps a header cell to a column key.
//
// It carries every registered locale's rendering of every header, plus the raw
// key, so a user who downloads the template in one language can upload it
// unchanged — and a hand-written file using the bare keys works too.
type headerIndex map[string]string

// normalizeHeader folds a header cell to its comparison form. Spreadsheet
// editors introduce case and spacing differences that carry no meaning.
func normalizeHeader(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch r {
		case ' ', '\t', ' ', '　', '_', '-':
			continue
		case '（':
			b.WriteRune('(')
		case '）':
			b.WriteRune(')')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// buildHeaderIndex resolves every column header in every registered locale.
// A collision means two columns are indistinguishable in some language, which
// would silently import values into the wrong field, so it is reported rather
// than resolved by ordering.
func buildHeaderIndex() (headerIndex, error) {
	index := headerIndex{}
	add := func(raw, key string) error {
		normalized := normalizeHeader(raw)
		if normalized == "" {
			return nil
		}
		if existing, taken := index[normalized]; taken && existing != key {
			return fmt.Errorf(
				"todo: header %q maps to both %q and %q; the two columns are indistinguishable in at least one locale",
				raw, existing, key)
		}
		index[normalized] = key
		return nil
	}

	for _, c := range columns {
		if err := add(c.key, c.key); err != nil {
			return nil, err
		}
		// i18n resolves through the context, so each locale is visited by
		// building a context for it rather than with a per-locale lookup.
		for _, locale := range platformi18n.Supported() {
			ctx := platformi18n.WithLocale(context.Background(), locale.Code)
			if err := add(platformi18n.TContext(ctx, c.headerKey), c.key); err != nil {
				return nil, err
			}
		}
	}
	return index, nil
}

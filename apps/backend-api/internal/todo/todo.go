// Package todo is the platform's task module: create, read, update, soft-delete
// and restore tasks, with optimistic concurrency on every write.
package todo

//go:generate go run ./dbgen

import (
	"errors"
	"strings"
	"time"
)

const Name = "todo"

// Status is a task's lifecycle label. Transitions are unrestricted: the status
// describes a task rather than guarding an invariant, so forcing a state
// machine would only produce rejections without protecting anything.
type Status string

const (
	StatusOpen  Status = "open"
	StatusDoing Status = "doing"
	StatusDone  Status = "done"
)

// Statuses is the accepted set, in the order they appear in documentation and
// generated enums.
var Statuses = []Status{StatusOpen, StatusDoing, StatusDone}

func (s Status) Valid() bool {
	for _, candidate := range Statuses {
		if s == candidate {
			return true
		}
	}
	return false
}

// StatusStrings renders Statuses for huma's enum tag and for import validation.
func StatusStrings() []string {
	out := make([]string, len(Statuses))
	for index, status := range Statuses {
		out[index] = string(status)
	}
	return out
}

// Priority bounds. 1 is the most urgent; 3 is the default.
const (
	MinPriority     = 1
	MaxPriority     = 5
	DefaultPriority = 3
)

// TitleMaxLength matches the column width, so a value that validates always
// stores.
const TitleMaxLength = 255

var (
	ErrForbidden = errors.New("todo: forbidden")
	// ErrNotFound covers both "never existed" and "soft-deleted", which are
	// indistinguishable to a caller without the deleted filter.
	ErrNotFound = errors.New("todo: task not found")
	// ErrDeleted is returned where a caller may act on a deleted task, so it can
	// be told the task is recoverable rather than absent.
	ErrDeleted = errors.New("todo: task is deleted")
	// ErrNotDeleted is returned when restoring a task that is not deleted.
	ErrNotDeleted = errors.New("todo: task is not deleted")
	// ErrVersionConflict reports a lost-update attempt: the caller's version no
	// longer matches the stored one.
	ErrVersionConflict = errors.New("todo: version conflict")
	// ErrUnavailable reports that the module cannot reach its datasource.
	ErrUnavailable = errors.New("todo: task service unavailable")
)

// Draft is a validated creation request. Constructing one through newDraft is
// the only way to reach the store, so no unvalidated task can be written.
type Draft struct {
	provided    []string
	Title       string
	Description string
	Status      Status
	Priority    int
	DueAt       *time.Time
}

// Patch is a validated partial update. A nil field means "leave unchanged",
// which is why every field is a pointer: for Description and DueAt, "clear it"
// and "do not touch it" are different requests.
type Patch struct {
	provided    []string
	Title       *string
	Description *string
	Status      *Status
	Priority    *int
	DueAt       **time.Time
}

// FieldError names the offending field so the transport can map it to a
// per-field response rather than one opaque message.
type FieldError struct {
	Field   string
	Message string
}

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// normalizeTitle trims surrounding whitespace. A title of only whitespace is
// empty, not valid, so trimming happens before the emptiness check.
func normalizeTitle(raw string) string { return strings.TrimSpace(raw) }

func validateTitle(raw string) (string, error) {
	title := normalizeTitle(raw)
	switch {
	case title == "":
		return "", FieldError{Field: "title", Message: "must not be empty"}
	case len(title) > TitleMaxLength:
		return "", FieldError{Field: "title", Message: "must be at most 255 bytes"}
	}
	return title, nil
}

func validateStatus(status Status) error {
	if !status.Valid() {
		return FieldError{Field: "status", Message: "must be one of open, doing, done"}
	}
	return nil
}

func validatePriority(priority int) error {
	if priority < MinPriority || priority > MaxPriority {
		return FieldError{Field: "priority", Message: "must be between 1 and 5"}
	}
	return nil
}

// completionFor derives completed_at from a status change. The field is owned
// by the server: entering done stamps it, leaving done clears it, and a status
// that does not change leaves it alone.
func completionFor(previous, next Status, existing *time.Time, now time.Time) *time.Time {
	switch {
	case previous == next:
		return existing
	case next == StatusDone:
		return &now
	default:
		return nil
	}
}

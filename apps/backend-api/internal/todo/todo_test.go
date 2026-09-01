package todo

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/todo/model"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusValidity(t *testing.T) {
	for _, status := range Statuses {
		assert.True(t, status.Valid(), "%q is a declared status", status)
	}
	assert.False(t, Status("archived").Valid())
	assert.False(t, Status("").Valid())
	assert.Equal(t, []string{"open", "doing", "done"}, StatusStrings(),
		"the enum tag and the import validator both read this order")
}

func TestValidateTitle(t *testing.T) {
	title, err := validateTitle("  spaced  ")
	require.NoError(t, err)
	assert.Equal(t, "spaced", title, "surrounding whitespace is not part of a title")

	for _, raw := range []string{"", "   ", "\t\n"} {
		_, err := validateTitle(raw)
		var fieldErr FieldError
		require.ErrorAs(t, err, &fieldErr, "%q must be rejected", raw)
		assert.Equal(t, "title", fieldErr.Field)
	}

	// The bound matches the column width, so a value that validates always
	// stores.
	_, err = validateTitle(strings.Repeat("a", TitleMaxLength))
	assert.NoError(t, err)
	_, err = validateTitle(strings.Repeat("a", TitleMaxLength+1))
	assert.Error(t, err)
}

func TestValidateStatusAndPriority(t *testing.T) {
	assert.NoError(t, validateStatus(StatusDoing))
	assert.Error(t, validateStatus(Status("archived")))

	for _, priority := range []int{MinPriority, DefaultPriority, MaxPriority} {
		assert.NoError(t, validatePriority(priority))
	}
	for _, priority := range []int{0, -1, MaxPriority + 1} {
		var fieldErr FieldError
		require.ErrorAs(t, validatePriority(priority), &fieldErr)
		assert.Equal(t, "priority", fieldErr.Field)
	}
}

func TestFieldErrorNamesTheField(t *testing.T) {
	err := FieldError{Field: "title", Message: "must not be empty"}
	assert.Equal(t, "title: must not be empty", err.Error())
}

func TestCompletionFollowsStatusChange(t *testing.T) {
	now := time.Now().UTC()
	existing := now.Add(-time.Hour)

	// An unchanged status leaves the timestamp exactly as it was, whether set
	// or not, so an unrelated patch never disturbs it.
	assert.Equal(t, &existing, completionFor(StatusDone, StatusDone, &existing, now))
	assert.Nil(t, completionFor(StatusOpen, StatusOpen, nil, now))

	assert.Equal(t, &now, completionFor(StatusOpen, StatusDone, nil, now))
	assert.Nil(t, completionFor(StatusDone, StatusOpen, &existing, now))
	assert.Nil(t, completionFor(StatusDone, StatusDoing, &existing, now))
}

func TestHTTPErrorMapsEveryDomainError(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{ErrNotFound, http.StatusNotFound},
		{ErrDeleted, http.StatusGone},
		{ErrNotDeleted, http.StatusConflict},
		{ErrVersionConflict, http.StatusConflict},
		{ErrUnavailable, http.StatusServiceUnavailable},
		{FieldError{Field: "title", Message: "bad"}, http.StatusUnprocessableEntity},
	} {
		var status huma.StatusError
		require.ErrorAs(t, httpError(tc.err), &status, "%v must map to a status", tc.err)
		assert.Equal(t, tc.status, status.GetStatus(), "for %v", tc.err)
	}

	// An error the module does not recognize passes through rather than being
	// flattened into a misleading 500 message here.
	other := errors.New("boom")
	assert.Equal(t, other, httpError(other))
}

func TestParseIfMatch(t *testing.T) {
	for _, raw := range []string{`"3"`, "3", ` "3" `, `W/"3"`} {
		version, err := parseIfMatch(raw)
		require.NoError(t, err, "%q is an acceptable precondition", raw)
		assert.Equal(t, 3, version)
	}

	missing, err := parseIfMatch("")
	assert.Zero(t, missing)
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusPreconditionRequired, status.GetStatus(),
		"a missing precondition is not a malformed one")

	for _, raw := range []string{"abc", `"0"`, "-1", `""`} {
		_, err := parseIfMatch(raw)
		require.ErrorAs(t, err, &status, "%q must be rejected", raw)
		assert.Equal(t, http.StatusBadRequest, status.GetStatus(), "for %q", raw)
	}
}

func TestTaskIDRejectsUnusableIdentifiers(t *testing.T) {
	id, err := taskID("2366488506482438144")
	require.NoError(t, err)
	assert.Equal(t, "2366488506482438144", id.String())

	// An identifier that cannot name a stored task is reported as absent, not
	// as a malformed request: the caller cannot tell the difference and 404 is
	// the honest answer.
	for _, raw := range []string{"", "0", "-1", "abc", "12x"} {
		_, err := taskID(raw)
		var status huma.StatusError
		require.ErrorAs(t, err, &status, "%q must be rejected", raw)
		assert.Equal(t, http.StatusNotFound, status.GetStatus(), "for %q", raw)
	}
}

func TestDueAtPatchDistinguishesAbsentNullAndValue(t *testing.T) {
	// Absent: the unmarshaller never runs, so Set stays false.
	var absent dueAtPatch
	assert.False(t, absent.Set)
	assert.Nil(t, absent.Value)

	var cleared dueAtPatch
	require.NoError(t, cleared.UnmarshalJSON([]byte("null")))
	assert.True(t, cleared.Set, "an explicit null is still a mention")
	assert.Nil(t, cleared.Value)

	var set dueAtPatch
	require.NoError(t, set.UnmarshalJSON([]byte(`"2026-10-01T00:00:00Z"`)))
	assert.True(t, set.Set)
	require.NotNil(t, set.Value)
	assert.Equal(t, 2026, set.Value.Year())

	var invalid dueAtPatch
	assert.Error(t, invalid.UnmarshalJSON([]byte(`"not-a-time"`)))
}

func TestUnknownActorRendersAsNull(t *testing.T) {
	// A zero actor means no authentication middleware has run. guid.ID renders
	// it as null rather than "0", which it would refuse to parse back, so the
	// field stays present and explicitly unknown.
	encoded, err := json.Marshal(viewOf(&model.TodoRow{Title: "task"}))
	require.NoError(t, err)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	require.Contains(t, fields, "created_by")
	assert.JSONEq(t, "null", string(fields["created_by"]))
}

func TestEtagRendersVersion(t *testing.T) {
	assert.Equal(t, `"1"`, etag(1))
	assert.Equal(t, `"42"`, etag(42))
}

func TestDraftOfAppliesDefaults(t *testing.T) {
	// An omitted status and priority take the documented defaults rather than
	// the Go zero values, which are not valid for either field.
	var in createInput
	in.Body.Title = "  task  "
	draft, err := draftOf(&in)
	require.NoError(t, err)
	assert.Equal(t, "task", draft.Title)
	assert.Equal(t, StatusOpen, draft.Status)
	assert.Equal(t, DefaultPriority, draft.Priority)

	in.Body.Status = "done"
	in.Body.Priority = 1
	draft, err = draftOf(&in)
	require.NoError(t, err)
	assert.Equal(t, StatusDone, draft.Status)
	assert.Equal(t, 1, draft.Priority)
}

func TestDraftOfRejectsInvalidFields(t *testing.T) {
	for name, mutate := range map[string]func(*createInput){
		"empty title":    func(in *createInput) { in.Body.Title = "   " },
		"unknown status": func(in *createInput) { in.Body.Status = "archived" },
		"priority range": func(in *createInput) { in.Body.Priority = 9 },
	} {
		t.Run(name, func(t *testing.T) {
			in := createInput{}
			in.Body.Title = "task"
			mutate(&in)
			_, err := draftOf(&in)
			var fieldErr FieldError
			assert.ErrorAs(t, err, &fieldErr)
		})
	}
}

func TestPatchOfCarriesOnlyMentionedFields(t *testing.T) {
	var in patchInput
	patch, err := patchOf(&in)
	require.NoError(t, err)
	assert.Nil(t, patch.Title)
	assert.Nil(t, patch.Description)
	assert.Nil(t, patch.Status)
	assert.Nil(t, patch.Priority)
	assert.Nil(t, patch.DueAt, "an unmentioned due_at must stay unmentioned")

	title, description, status, priority := "  renamed  ", "details", "doing", 2
	in.Body.Title = &title
	in.Body.Description = &description
	in.Body.Status = &status
	in.Body.Priority = &priority
	in.Body.DueAt = dueAtPatch{Set: true}

	patch, err = patchOf(&in)
	require.NoError(t, err)
	require.NotNil(t, patch.Title)
	assert.Equal(t, "renamed", *patch.Title, "a patched title is trimmed too")
	assert.Equal(t, StatusDoing, *patch.Status)
	assert.Equal(t, 2, *patch.Priority)
	require.NotNil(t, patch.DueAt)
	assert.Nil(t, *patch.DueAt, "an explicit null reaches the service as a clear")
}

func TestPatchOfRejectsInvalidFields(t *testing.T) {
	blank, unknown, outOfRange := "  ", "archived", 0
	for name, mutate := range map[string]func(*patchInput){
		"empty title":    func(in *patchInput) { in.Body.Title = &blank },
		"unknown status": func(in *patchInput) { in.Body.Status = &unknown },
		"priority range": func(in *patchInput) { in.Body.Priority = &outOfRange },
	} {
		t.Run(name, func(t *testing.T) {
			in := patchInput{}
			mutate(&in)
			_, err := patchOf(&in)
			var fieldErr FieldError
			assert.ErrorAs(t, err, &fieldErr)
		})
	}
}

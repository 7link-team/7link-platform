package guid

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// ID crosses JSON as a decimal string so JavaScript clients do not lose
// precision above 2^53. SQL storage remains BIGINT-compatible.
type ID int64

// MarshalJSON renders the ID as a decimal string, or as null when it is zero.
//
// Zero means "no identifier" — an unset optional reference, or an actor no
// authentication middleware has supplied. Rendering it as "0" would emit a
// value this type refuses to read back: Parse rejects non-positive input, while
// it already accepts null as zero. null is therefore the representation that
// round-trips.
func (id ID) MarshalJSON() ([]byte, error) {
	if id.Zero() {
		return []byte("null"), nil
	}
	return []byte(`"` + id.String() + `"`), nil
}

func (id *ID) UnmarshalJSON(value []byte) error {
	parsed, err := Parse(string(value))
	*id = parsed
	return err
}

// MarshalText renders the ID for the text paths — map keys, XML, form encoding
// — and is empty when the ID is zero. It matches MarshalJSON rather than
// String: "0" is a value Parse refuses to read back, so emitting it would give
// the text form a representation that cannot round-trip. Empty parses as zero.
func (id ID) MarshalText() ([]byte, error) {
	if id.Zero() {
		return nil, nil
	}
	return []byte(id.String()), nil
}

func (id *ID) UnmarshalText(value []byte) error {
	parsed, err := Parse(string(value))
	*id = parsed
	return err
}

// Schema declares the ID as a nullable decimal string. The string form keeps
// snowflake identifiers exact for JavaScript clients, whose numbers lose
// precision above 2^53; nullable covers the zero value, which marshals as null.
//
// Do not declare a path or query parameter as ID. huma binds parameters by Go
// kind, so it parses this int64 as a number and then validates that number
// against this string schema, rejecting every value with 422. Declare such a
// parameter as a string with the snowflake pattern and call RequireID:
//
//	type input struct{ ID string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`}
//	id, err := guid.RequireID(in.ID)
//
// Request and response bodies are unaffected: they go through encoding/json,
// which uses MarshalJSON and UnmarshalJSON.
func (ID) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Pattern: `^[1-9][0-9]*$`, Nullable: true}
}

func (id ID) Value() (driver.Value, error) { return int64(id), nil }

func (id *ID) Scan(source any) error {
	switch value := source.(type) {
	case nil:
		*id = 0
		return nil
	case int64:
		*id = ID(value)
		return nil
	case []byte:
		return id.parse(string(value))
	case string:
		return id.parse(value)
	default:
		return fmt.Errorf("guid.ID: cannot scan %T", source)
	}
}

func (id *ID) parse(value string) error {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return err
	}
	*id = ID(parsed)
	return nil
}

func (id ID) Int64() int64 { return int64(id) }

func (id ID) String() string { return strconv.FormatInt(int64(id), 10) }

func (id ID) Zero() bool { return id == 0 }

func Parse(value string) (ID, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "null" || value == `""` {
		return 0, nil
	}
	value = strings.TrimSpace(strings.Trim(value, `"`))
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		if err == nil {
			err = errors.New("must be positive")
		}
		return 0, fmt.Errorf("guid.ID: invalid ID %q: %w", value, err)
	}
	return ID(parsed), nil
}

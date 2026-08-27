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

func (id ID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + id.String() + `"`), nil
}

func (id *ID) UnmarshalJSON(value []byte) error {
	parsed, err := Parse(string(value))
	*id = parsed
	return err
}

func (id ID) MarshalText() ([]byte, error) { return []byte(id.String()), nil }

func (id *ID) UnmarshalText(value []byte) error {
	parsed, err := Parse(string(value))
	*id = parsed
	return err
}

func (ID) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Pattern: `^[1-9][0-9]*$`}
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

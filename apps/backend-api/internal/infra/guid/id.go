package guid

import platformguid "7link.com/platform/pkg/guid"

// ID is the platform snowflake identifier. It is defined in pkg/guid so the
// shared extension packages under pkg/ can carry it without importing this
// module; the alias keeps every existing guid.ID in internal/ the same type.
type ID = platformguid.ID

// Parse reads a decimal snowflake identifier. See pkg/guid.Parse.
func Parse(value string) (ID, error) { return platformguid.Parse(value) }

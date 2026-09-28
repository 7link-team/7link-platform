package files

import (
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var ErrName = errors.New("files: name is not portable")

// PortableName returns the NFC display name and case-insensitive collision key.
// This does not produce a storage key or a local filesystem path.
func PortableName(input string) (string, string, error) {
	if !utf8.ValidString(input) {
		return "", "", ErrName
	}
	name := norm.NFC.String(input)
	if name == "" || name == "." || name == ".." || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || len(name) > 255 || len(utf16.Encode([]rune(name))) > 255 {
		return "", "", ErrName
	}
	for _, c := range name {
		if unicode.IsControl(c) || strings.ContainsRune(`/\:*?"<>|`, c) {
			return "", "", ErrName
		}
	}
	base := strings.ToUpper(strings.TrimRight(strings.SplitN(name, ".", 2)[0], " "))
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CLOCK$" || base == "CONIN$" || base == "CONOUT$" {
		return "", "", ErrName
	}
	if len([]rune(base)) == 4 {
		runes := []rune(base)
		if (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && strings.ContainsRune("123456789¹²³", runes[3]) {
			return "", "", ErrName
		}
	}
	return name, norm.NFC.String(cases.Fold().String(name)), nil
}

// Extension belongs to a virtual name, not the shared physical object.
func Extension(name string) string { return strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")) }

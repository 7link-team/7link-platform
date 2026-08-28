package respx

import (
	"mime"
	"strconv"
	"strings"
)

// PrefersHTML reports whether an Accept header explicitly gives HTML at least
// as much weight as JSON. Empty and generic API requests default to JSON.
func PrefersHTML(accept string) bool {
	htmlQuality := -1.0
	jsonQuality := 0.0
	for _, value := range strings.Split(accept, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		quality := 1.0
		if raw := parameters["q"]; raw != "" {
			quality, err = strconv.ParseFloat(raw, 64)
			if err != nil || quality < 0 || quality > 1 {
				continue
			}
		}
		switch {
		case mediaType == "text/html" || mediaType == "application/xhtml+xml":
			htmlQuality = max(htmlQuality, quality)
		case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") || mediaType == "*/*":
			jsonQuality = max(jsonQuality, quality)
		}
	}
	return htmlQuality > 0 && htmlQuality >= jsonQuality
}

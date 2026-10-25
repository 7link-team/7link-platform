package execution

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"golang.org/x/net/html"
)

type ClientAnchorRect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}
type ClientAnchorViewport struct {
	W      float64 `json:"w"`
	H      float64 `json:"h"`
	DPR    float64 `json:"dpr"`
	Device string  `json:"device" enum:"DESKTOP,TABLET,PHONE"`
}
type ClientAnchorPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type ClientAnchorError struct {
	Field       string
	Unavailable bool
}

func (e *ClientAnchorError) Error() string        { return "execution: invalid client anchor: " + e.Field }
func (e *ClientAnchorError) Is(target error) bool { return target == ErrInvalidArgument }

type clientAnchorSnapshot struct {
	delivery.ArtifactAnchorSnapshot
	Anchor json.RawMessage `json:"anchor"`
}

var clientParagraphBreak = regexp.MustCompile(`\n[\t ]*\n`)

func clientArtifactAnchor(pin files.PinnedResourceRead, source ClientDeliveryItem, in ClientThreadCreate) (clientAnchorSnapshot, error) {
	var out clientAnchorSnapshot
	invalid := func(field string) (clientAnchorSnapshot, error) { return out, &ClientAnchorError{Field: field} }
	if pin.Reader == nil {
		return out, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(pin.Reader, 4*1024*1024+1))
	if err = errors.Join(err, pin.Reader.Close()); err != nil {
		return out, err
	}
	if len(data) > 4*1024*1024 || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return invalid("artifact_ref")
	}
	if pin.VersionID != source.VersionID || pin.SHA256 != source.SHA256 || hash(data) != source.SHA256 || pin.FileID <= 0 || pin.BindingID <= 0 {
		return out, ErrConflict
	}
	kind := in.AnchorKind
	if kind == "" {
		kind = "CODE_LINES"
	}
	if kind == "MEDIA_TIME" {
		return out, &ClientAnchorError{Field: "anchor_kind", Unavailable: true}
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	base := delivery.ArtifactAnchorSnapshot{Kind: kind, Path: pin.Name, VersionRef: fmt.Sprintf("file-version:%s sha256:%s", source.VersionID.String(), source.SHA256), FileID: pin.FileID, VersionID: pin.VersionID, SHA256: source.SHA256}
	var locator any
	switch kind {
	case "CODE_LINES":
		if in.ViewerKind != "" && in.ViewerKind != "CODE" && in.ViewerKind != "DOCUMENT" {
			return invalid("viewer_kind")
		}
		lines := strings.Split(text, "\n")
		if in.StartLine < 1 || in.EndLine < in.StartLine || in.EndLine > len(lines) || in.EndLine-in.StartLine >= 500 {
			return invalid("anchor.end_line")
		}
		base.StartLine, base.EndLine = in.StartLine, in.EndLine
		base.Excerpt = strings.Join(lines[in.StartLine-1:in.EndLine], "\n")
		locator = map[string]any{"path": pin.Name, "start_line": in.StartLine, "end_line": in.EndLine}
	case "TABLE_ROW":
		if in.ViewerKind != "" && in.ViewerKind != "DOCUMENT" && in.ViewerKind != "REPORT" {
			return invalid("viewer_kind")
		}
		if in.RowIndex == nil || *in.RowIndex < 0 {
			return invalid("anchor.row_index")
		}
		var rows []json.RawMessage
		ext := strings.ToLower(filepath.Ext(pin.Name))
		if ext == ".json" || pin.MIME == "application/json" {
			if json.Unmarshal(data, &rows) != nil {
				return invalid("artifact_ref")
			}
			for i, row := range rows {
				var value any
				decoder := json.NewDecoder(strings.NewReader(string(row)))
				decoder.UseNumber()
				if decoder.Decode(&value) != nil {
					return invalid("artifact_ref")
				}
				rows[i], err = json.Marshal(value)
				if err != nil {
					return out, err
				}
			}
		} else {
			if ext != ".csv" && ext != ".tsv" && pin.MIME != "text/csv" && pin.MIME != "text/tab-separated-values" {
				return invalid("viewer_kind")
			}
			r := csv.NewReader(strings.NewReader(text))
			if ext == ".tsv" || pin.MIME == "text/tab-separated-values" {
				r.Comma = '\t'
			}
			table, err := r.ReadAll()
			if err != nil || len(table) < 2 {
				return invalid("artifact_ref")
			}
			for _, row := range table[1:] {
				raw, err := json.Marshal(row)
				if err != nil {
					return out, err
				}
				rows = append(rows, raw)
			}
		}
		if *in.RowIndex >= len(rows) {
			return invalid("anchor.row_index")
		}
		row := rows[*in.RowIndex]
		digest := hash(row)
		if in.RowSHA256 != "" && in.RowSHA256 != digest {
			return invalid("anchor.row_sha256")
		}
		base.Excerpt = string(row)
		locator = map[string]any{"path": pin.Name, "row_index": *in.RowIndex, "row_sha256": digest}
	case "TEXT_PARAGRAPH":
		if in.ViewerKind != "" && in.ViewerKind != "DOCUMENT" {
			return invalid("viewer_kind")
		}
		if in.ParagraphIndex == nil || *in.ParagraphIndex < 0 {
			return invalid("anchor.paragraph_index")
		}
		paragraphs := clientParagraphBreak.Split(strings.TrimSpace(text), -1)
		if *in.ParagraphIndex >= len(paragraphs) {
			return invalid("anchor.paragraph_index")
		}
		paragraph := paragraphs[*in.ParagraphIndex]
		digest := hash([]byte(paragraph))
		if in.ParagraphSHA256 != "" && in.ParagraphSHA256 != digest {
			return invalid("anchor.paragraph_sha256")
		}
		quote := in.TextQuote
		if quote == "" {
			r := []rune(paragraph)
			if len(r) > 500 {
				r = r[:500]
			}
			quote = string(r)
		}
		if len([]rune(quote)) > 500 || !strings.Contains(paragraph, quote) {
			return invalid("anchor.quote")
		}
		base.Excerpt = quote
		locator = map[string]any{"path": pin.Name, "paragraph_index": *in.ParagraphIndex, "paragraph_sha256": digest, "quote": map[string]string{"exact": quote}}
	case "HTML_REGION":
		if in.ViewerKind != "" && in.ViewerKind != "WEB_DEVICE" && in.ViewerKind != "REPORT" {
			return invalid("viewer_kind")
		}
		if strings.ToLower(filepath.Ext(pin.Name)) != ".html" && strings.ToLower(filepath.Ext(pin.Name)) != ".htm" && pin.MIME != "text/html" {
			return invalid("viewer_kind")
		}
		if in.Rect == nil || in.Viewport == nil || in.Scroll == nil {
			return invalid("anchor.rect")
		}
		r, v, s := in.Rect, in.Viewport, in.Scroll
		for _, n := range []float64{r.X, r.Y, r.W, r.H, v.W, v.H, v.DPR, s.X, s.Y} {
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return invalid("anchor.rect")
			}
		}
		if r.W <= 0 || r.H <= 0 || v.W <= 0 || v.H <= 0 || v.W > 10000 || v.H > 10000 || v.DPR <= 0 || v.DPR > 8 || s.X < 0 || s.Y < 0 || r.X < s.X || r.Y < s.Y || r.X+r.W > s.X+v.W || r.Y+r.H > s.Y+v.H {
			return invalid("anchor.rect")
		}
		if v.Device != "DESKTOP" && v.Device != "TABLET" && v.Device != "PHONE" {
			return invalid("anchor.viewport")
		}
		if len(in.Selector) > 512 || len([]rune(in.TextQuote)) == 0 || len([]rune(in.TextQuote)) > 200 {
			return invalid("anchor.text_quote")
		}
		doc, err := html.Parse(strings.NewReader(text))
		if err != nil {
			return invalid("artifact_ref")
		}
		var visible strings.Builder
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
				return
			}
			if n.Type == html.TextNode {
				visible.WriteString(n.Data)
				visible.WriteByte(' ')
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		if !strings.Contains(strings.Join(strings.Fields(visible.String()), " "), strings.Join(strings.Fields(in.TextQuote), " ")) {
			return invalid("anchor.text_quote")
		}
		base.Excerpt = in.TextQuote
		locator = map[string]any{"url_path": pin.Name, "selector": in.Selector, "rect": r, "viewport": v, "scroll": s, "text_quote": in.TextQuote, "precision": "RECT_ONLY"}
	default:
		return invalid("anchor_kind")
	}
	if len(base.Excerpt) > 12000 {
		return invalid("anchor_excerpt")
	}
	raw, err := json.Marshal(locator)
	if err != nil {
		return out, err
	}
	return clientAnchorSnapshot{ArtifactAnchorSnapshot: base, Anchor: raw}, nil
}

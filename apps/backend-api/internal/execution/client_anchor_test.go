package execution

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"7link.com/platform/internal/files"
	"github.com/stretchr/testify/require"
)

func TestClientAnchorFreezesFourMVPViewerLocationsFromPinnedBytes(t *testing.T) {
	zero := 0
	tests := []struct {
		name, file, data, excerpt string
		input                     ClientThreadCreate
	}{
		{"code", "source.go", "first\nrejected\n", "rejected", ClientThreadCreate{AnchorKind: "CODE_LINES", ViewerKind: "CODE", StartLine: 2, EndLine: 2}},
		{"table", "report.csv", "name,result\nBuild,failed\n", `["Build","failed"]`, ClientThreadCreate{AnchorKind: "TABLE_ROW", ViewerKind: "REPORT", RowIndex: &zero}},
		{"table exact numbers", "report.json", `[{"count":9007199254740993}]`, `{"count":9007199254740993}`, ClientThreadCreate{AnchorKind: "TABLE_ROW", ViewerKind: "REPORT", RowIndex: &zero}},
		{"paragraph", "document.txt", "Rejected paragraph\n\nSecond paragraph", "Rejected", ClientThreadCreate{AnchorKind: "TEXT_PARAGRAPH", ViewerKind: "DOCUMENT", ParagraphIndex: &zero, TextQuote: "Rejected"}},
		{"html", "preview.html", "<main><p>Rejected output</p><script>secret script</script></main>", "Rejected output", ClientThreadCreate{AnchorKind: "HTML_REGION", ViewerKind: "WEB_DEVICE", TextQuote: "Rejected output", Rect: &ClientAnchorRect{X: 10, Y: 10, W: 100, H: 20}, Viewport: &ClientAnchorViewport{W: 800, H: 600, DPR: 1, Device: "DESKTOP"}, Scroll: &ClientAnchorPoint{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &artifactAnchorReader{Reader: strings.NewReader(tt.data)}
			digest := hash([]byte(tt.data))
			pin := files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 3, Name: tt.file, SHA256: digest}, Reader: reader}
			got, err := clientArtifactAnchor(pin, ClientDeliveryItem{VersionID: 3, SHA256: digest}, tt.input)
			require.NoError(t, err)
			require.Equal(t, 1, reader.closed)
			require.Equal(t, tt.excerpt, got.Excerpt)
			require.Equal(t, tt.input.AnchorKind, got.Kind)
			require.Equal(t, digest, got.SHA256)
			require.EqualValues(t, 3, got.VersionID)
			require.True(t, json.Valid(got.Anchor))
			require.Contains(t, got.VersionRef, digest)
		})
	}
}

func TestClientAnchorRejectsForgedHashesLocationsAndUnavailableMedia(t *testing.T) {
	zero := 0
	tests := []struct {
		name, file, data string
		input            ClientThreadCreate
	}{
		{"row hash", "report.csv", "name\nvalue", ClientThreadCreate{AnchorKind: "TABLE_ROW", RowIndex: &zero, RowSHA256: strings.Repeat("a", 64)}},
		{"paragraph hash", "document.txt", "source", ClientThreadCreate{AnchorKind: "TEXT_PARAGRAPH", ParagraphIndex: &zero, ParagraphSHA256: strings.Repeat("a", 64)}},
		{"forged quote", "document.txt", "source", ClientThreadCreate{AnchorKind: "TEXT_PARAGRAPH", ParagraphIndex: &zero, TextQuote: "fabricated"}},
		{"lines outside file", "source.go", "one", ClientThreadCreate{StartLine: 1, EndLine: 2}},
		{"binary", "source.go", "one\x00two", ClientThreadCreate{StartLine: 1, EndLine: 1}},
		{"unsupported media", "source.txt", "source", ClientThreadCreate{AnchorKind: "MEDIA_TIME"}},
		{"html script quote", "preview.html", "<main>visible</main><script>secret</script>", ClientThreadCreate{AnchorKind: "HTML_REGION", TextQuote: "secret", Rect: &ClientAnchorRect{W: 10, H: 10}, Viewport: &ClientAnchorViewport{W: 100, H: 100, DPR: 1, Device: "PHONE"}, Scroll: &ClientAnchorPoint{}}},
		{"html outside viewport", "preview.html", "<main>visible</main>", ClientThreadCreate{AnchorKind: "HTML_REGION", TextQuote: "visible", Rect: &ClientAnchorRect{X: 99, W: 10, H: 10}, Viewport: &ClientAnchorViewport{W: 100, H: 100, DPR: 1, Device: "PHONE"}, Scroll: &ClientAnchorPoint{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &artifactAnchorReader{Reader: strings.NewReader(tt.data)}
			digest := hash([]byte(tt.data))
			_, err := clientArtifactAnchor(files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 3, Name: tt.file, SHA256: digest}, Reader: reader}, ClientDeliveryItem{VersionID: 3, SHA256: digest}, tt.input)
			require.Error(t, err)
			require.Equal(t, 1, reader.closed)
		})
	}
}

func TestClientAnchorBoundaryFailuresRetainPreciseFieldAndCloseSource(t *testing.T) {
	zero, negative, large := 0, -1, 10
	cases := []struct {
		name, file, data, field string
		in                      ClientThreadCreate
	}{
		{"code viewer", "a.go", "source", "viewer_kind", ClientThreadCreate{ViewerKind: "REPORT", StartLine: 1, EndLine: 1}},
		{"row viewer", "a.csv", "h\na", "viewer_kind", ClientThreadCreate{AnchorKind: "TABLE_ROW", ViewerKind: "CODE", RowIndex: &zero}},
		{"row missing", "a.csv", "h\na", "anchor.row_index", ClientThreadCreate{AnchorKind: "TABLE_ROW"}},
		{"row negative", "a.csv", "h\na", "anchor.row_index", ClientThreadCreate{AnchorKind: "TABLE_ROW", RowIndex: &negative}},
		{"row beyond", "a.csv", "h\na", "anchor.row_index", ClientThreadCreate{AnchorKind: "TABLE_ROW", RowIndex: &large}},
		{"row unsupported", "a.txt", "h\na", "viewer_kind", ClientThreadCreate{AnchorKind: "TABLE_ROW", RowIndex: &zero}},
		{"row corrupt json", "a.json", "{", "artifact_ref", ClientThreadCreate{AnchorKind: "TABLE_ROW", RowIndex: &zero}},
		{"row corrupt csv", "a.csv", "h\n\"unterminated", "artifact_ref", ClientThreadCreate{AnchorKind: "TABLE_ROW", RowIndex: &zero}},
		{"paragraph viewer", "a.txt", "source", "viewer_kind", ClientThreadCreate{AnchorKind: "TEXT_PARAGRAPH", ViewerKind: "CODE", ParagraphIndex: &zero}},
		{"paragraph missing", "a.txt", "source", "anchor.paragraph_index", ClientThreadCreate{AnchorKind: "TEXT_PARAGRAPH"}},
		{"paragraph beyond", "a.txt", "source", "anchor.paragraph_index", ClientThreadCreate{AnchorKind: "TEXT_PARAGRAPH", ParagraphIndex: &large}},
		{"html viewer", "a.html", "<p>source</p>", "viewer_kind", ClientThreadCreate{AnchorKind: "HTML_REGION", ViewerKind: "CODE"}},
		{"html extension", "a.txt", "source", "viewer_kind", ClientThreadCreate{AnchorKind: "HTML_REGION"}},
		{"html missing rect", "a.html", "<p>source</p>", "anchor.rect", ClientThreadCreate{AnchorKind: "HTML_REGION"}},
		{"unknown kind", "a.txt", "source", "anchor_kind", ClientThreadCreate{AnchorKind: "UNKNOWN"}},
		{"huge excerpt", "a.go", strings.Repeat("a", 12001), "anchor_excerpt", ClientThreadCreate{StartLine: 1, EndLine: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &artifactAnchorReader{Reader: strings.NewReader(tc.data)}
			digest := hash([]byte(tc.data))
			_, e := clientArtifactAnchor(files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 3, SHA256: digest, Name: tc.file}, Reader: r}, ClientDeliveryItem{VersionID: 3, SHA256: digest}, tc.in)
			var anchorErr *ClientAnchorError
			require.ErrorAs(t, e, &anchorErr)
			require.Equal(t, tc.field, anchorErr.Field)
			require.Equal(t, 1, r.closed)
		})
	}
	for _, fault := range []string{"nan", "device", "quote"} {
		t.Run(fault, func(t *testing.T) {
			data := "<p>source</p>"
			r := &artifactAnchorReader{Reader: strings.NewReader(data)}
			in := ClientThreadCreate{AnchorKind: "HTML_REGION", TextQuote: "source", Rect: &ClientAnchorRect{W: 10, H: 10}, Viewport: &ClientAnchorViewport{W: 100, H: 100, DPR: 1, Device: "PHONE"}, Scroll: &ClientAnchorPoint{}}
			field := "anchor.rect"
			switch fault {
			case "nan":
				in.Rect.X = math.NaN()
			case "device":
				in.Viewport.Device = "UNKNOWN"
				field = "anchor.viewport"
			case "quote":
				in.TextQuote = ""
				field = "anchor.text_quote"
			}
			_, e := clientArtifactAnchor(files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 3, SHA256: hash([]byte(data)), Name: "a.html"}, Reader: r}, ClientDeliveryItem{VersionID: 3, SHA256: hash([]byte(data))}, in)
			var anchorErr *ClientAnchorError
			require.ErrorAs(t, e, &anchorErr)
			require.Equal(t, field, anchorErr.Field)
			require.Equal(t, 1, r.closed)
		})
	}
	data := strings.Repeat("?", 501)
	r := &artifactAnchorReader{Reader: strings.NewReader(data)}
	out, e := clientArtifactAnchor(files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 3, SHA256: hash([]byte(data)), Name: "a.txt"}, Reader: r}, ClientDeliveryItem{VersionID: 3, SHA256: hash([]byte(data))}, ClientThreadCreate{AnchorKind: "TEXT_PARAGRAPH", ParagraphIndex: &zero})
	require.NoError(t, e)
	require.Len(t, []rune(out.Excerpt), 500)
}

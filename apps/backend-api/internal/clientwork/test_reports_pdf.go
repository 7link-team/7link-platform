package clientwork

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Build a bounded PDF 1.4 document using standard Adobe GB1 CID fonts. All
// offsets and lengths are byte offsets; source text cannot inject PDF operators.
func testReportPDF(lines []string) []byte {
	wrapped := []string{}
	for _, line := range lines {
		runes := []rune(strings.Map(func(r rune) rune {
			if r < ' ' {
				return ' '
			}
			return r
		}, line))
		for len(runes) > 0 {
			n := min(48, len(runes))
			wrapped = append(wrapped, string(runes[:n]))
			runes = runes[n:]
		}
	}
	if len(wrapped) == 0 {
		wrapped = append(wrapped, "")
	}
	pages := (len(wrapped) + 47) / 48
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "", "<< /Type /Font /Subtype /Type0 /BaseFont /STSong-Light /Encoding /UniGB-UCS2-H /DescendantFonts [4 0 R] >>", "<< /Type /Font /Subtype /CIDFontType0 /BaseFont /STSong-Light /CIDSystemInfo << /Registry (Adobe) /Ordering (GB1) /Supplement 4 >> >>"}
	kids := []string{}
	for page := 0; page < pages; page++ {
		pageID := len(objects) + 1
		contentID := pageID + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", pageID))
		content := strings.Builder{}
		content.WriteString("BT /F1 11 Tf 40 800 Td 15 TL\n")
		for _, line := range wrapped[page*48 : min((page+1)*48, len(wrapped))] {
			content.WriteByte('<')
			for _, unit := range utf16.Encode([]rune(line)) {
				fmt.Fprintf(&content, "%04X", unit)
			}
			content.WriteString("> Tj T*\n")
		}
		content.WriteString("ET\n")
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", contentID), fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", pages, strings.Join(kids, " "))
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	start := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), start)
	return out.Bytes()
}

package clientwork

import (
	"bytes"
	"fmt"
	"github.com/stretchr/testify/require"
	"strconv"
	"strings"
	"testing"
)

func TestTestReportPDFPreservesUnicodeAndValidObjectOffsetsAcrossPages(t *testing.T) {
	lines := []string{"测试报告 — laporan (keputusan) \\ <inject>"}
	for i := 0; i < 110; i++ {
		lines = append(lines, fmt.Sprintf("Frozen result %d", i))
	}
	pdf := testReportPDF(lines)
	require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-1.4")))
	require.Contains(t, string(pdf), "6D4B8BD562A5544A")
	require.Contains(t, string(pdf), "/Count 3")
	require.NotContains(t, string(pdf), "<inject>")
	xref := bytes.Index(pdf, []byte("xref\n"))
	require.Positive(t, xref)
	fields := strings.Split(string(pdf[xref:]), "\n")
	var count int
	_, err := fmt.Sscanf(fields[1], "0 %d", &count)
	require.NoError(t, err)
	for id := 1; id < count; id++ {
		offset, err := strconv.Atoi(fields[2+id][:10])
		require.NoError(t, err)
		require.True(t, bytes.HasPrefix(pdf[offset:], []byte(fmt.Sprintf("%d 0 obj\n", id))))
	}
	require.Contains(t, string(pdf), fmt.Sprintf("startxref\n%d\n%%%%EOF", xref))
	require.Equal(t, pdf, testReportPDF(lines))
}

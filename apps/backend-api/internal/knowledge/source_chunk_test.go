package knowledge

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestSourceNormalizationAndChunkFingerprintsAreDeterministic(t *testing.T) {
	input := "\ufeff---\r\ntitle: metadata\r\n---\r\n# Guide\r\n\r\nCafe\u0301  \r\n\r\nRules\r\n-----\r\n\r\nKeep evidence.\t\r\n"
	text := normalizeSource(input, "MARKDOWN")
	require.Equal(t, "# Guide\n\nCafé\n\nRules\n-----\n\nKeep evidence.", text)
	a, reason := chunkSource(text, "MARKDOWN")
	require.Empty(t, reason)
	b, reason := chunkSource(text, "MARKDOWN")
	require.Empty(t, reason)
	require.Equal(t, a, b)
	require.Len(t, a, 2)
	require.Equal(t, "Guide", a[0].Heading)
	require.Equal(t, "Guide › Rules", a[1].Heading)
	for _, f := range a {
		require.Equal(t, sourceHash(f.Heading+"\n"+f.Content), f.Hash)
		require.Equal(t, utf8.RuneCountInString(f.Content), f.Chars)
		require.Equal(t, (len(f.Content)+2)/3, f.Tokens)
	}
	level, title := sourceHeading("# Write C#")
	require.Equal(t, 1, level)
	require.Equal(t, "Write C#", title)
	_, title = sourceHeading("# Write code ###")
	require.Equal(t, "Write code", title)
	code := normalizeSource("    indented first line\r\n        nested second line\r\n", "CODE")
	chunks, reason := chunkSource(code, "CODE")
	require.Empty(t, reason)
	require.Equal(t, "    indented first line\n        nested second line", chunks[0].Content)
	chunks, reason = chunkSource("first line\n\nlast line\n", "CODE")
	require.Empty(t, reason)
	require.Equal(t, "first line\n\nlast line", chunks[0].Content)
}

func TestSourceChunksRespectRuneByteBoundsWithoutLosingUnicode(t *testing.T) {
	for _, text := range []string{strings.Repeat("knowledge ", 1000), strings.Repeat("迁移权限", 800), strings.Repeat("🧪", 2001), strings.Repeat("a", 2001)} {
		chunks, reason := chunkSource(text, "TEXT")
		require.Empty(t, reason)
		require.Greater(t, len(chunks), 1)
		for _, f := range chunks {
			require.True(t, utf8.ValidString(f.Content))
			require.LessOrEqual(t, len(f.Content), 1800)
			require.LessOrEqual(t, f.Tokens, 600)
			require.LessOrEqual(t, f.Chars, 2000)
		}
	}
	chunks, reason := chunkSource(strings.Repeat("权", 2001), "TEXT")
	require.Empty(t, reason)
	for _, f := range chunks {
		require.LessOrEqual(t, f.Chars, 800)
	}
}

func TestSourceChunkTablesRepeatHeadersAndCodePreservesWholeLines(t *testing.T) {
	table := "| Name | Value |\n| --- | --- |\n" + strings.Repeat("| evidence | recorded value |\n", 120)
	chunks, reason := chunkSource(table, "MARKDOWN")
	require.Empty(t, reason)
	require.Greater(t, len(chunks), 1)
	for _, f := range chunks {
		require.True(t, strings.HasPrefix(f.Content, "| Name | Value |\n| --- | --- |\n"))
		require.LessOrEqual(t, f.Tokens, 600)
	}
	code := "```go\n" + strings.Repeat("var evidence = \"source\"\n", 250) // deliberately unclosed
	chunks, reason = chunkSource(code, "MARKDOWN")
	require.Empty(t, reason)
	require.Greater(t, len(chunks), 1)
	for _, f := range chunks {
		require.Equal(t, "CODE", f.Kind)
		require.True(t, strings.HasPrefix(f.Content, "```go\n"))
		require.True(t, strings.HasSuffix(f.Content, "\n```"))
		require.LessOrEqual(t, f.Tokens, 900)
		for _, line := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(f.Content, "```go\n"), "\n```"), "\n") {
			require.Equal(t, "var evidence = \"source\"", line)
		}
	}
	for _, tc := range []struct{ body, format, reason string }{{"", "TEXT", "TEXT_NOT_EXTRACTABLE"}, {"\x00", "TEXT", "TEXT_NOT_EXTRACTABLE"}, {string([]byte{0xff}), "TEXT", "TEXT_NOT_EXTRACTABLE"}, {strings.Repeat("x", 2701), "CODE", "CODE_LINE_TOO_LONG"}, {"| " + strings.Repeat("x", 1900) + " |\n| --- |\n| row |", "MARKDOWN", "TABLE_ROW_TOO_LONG"}, {strings.Repeat("x", 2000001), "TEXT", "TOO_LARGE"}} {
		chunks, reason = chunkSource(tc.body, tc.format)
		require.Equal(t, tc.reason, reason)
		require.Nil(t, chunks)
	}
}

func TestSourceChunkBoundaryFailuresAndDuplicateParagraphs(t *testing.T) {
	title := strings.Repeat("x", 310)
	chunks, reason := chunkSource("# "+title+"\nbody\n# "+title+"\nbody", "MARKDOWN")
	require.Empty(t, reason)
	require.Len(t, chunks, 1)
	require.Equal(t, 300, utf8.RuneCountInString(chunks[0].Heading))
	require.True(t, strings.HasSuffix(chunks[0].Heading, strings.Repeat("x", 299)))
	require.Equal(t, "body", chunks[0].Content)
	chunks, reason = chunkSource("| short |\n| --- |", "MARKDOWN")
	require.Empty(t, reason)
	require.Len(t, chunks, 1)
	require.Equal(t, "| short |\n| --- |", chunks[0].Content)
	for _, tc := range []struct{ text, format, want string }{
		{"| a |\n| --- |\n| " + strings.Repeat("x", 1800) + " |", "MARKDOWN", "TABLE_ROW_TOO_LONG"},
		{" \n ", "CODE", "TEXT_NOT_EXTRACTABLE"},
	} {
		chunks, reason = chunkSource(tc.text, tc.format)
		require.Equal(t, tc.want, reason)
		require.Nil(t, chunks)
	}
	var body strings.Builder
	for i := 0; i < 2001; i++ {
		body.WriteString("# " + strings.Repeat("x", i%40+1) + "\n" + strings.Repeat("a", i/40+1) + "\n")
	}
	chunks, reason = chunkSource(body.String(), "MARKDOWN")
	require.Equal(t, "TOO_MANY_CHUNKS", reason)
	require.Nil(t, chunks)
}

func TestSourceChunkParagraphPackingAndEmptyCodeDoNotManufactureContent(t *testing.T) {
	chunks, reason := chunkSource(strings.Repeat("a", 1000)+"\n\n"+strings.Repeat("b", 1000), "TEXT")
	require.Empty(t, reason)
	require.Len(t, chunks, 2)
	require.Equal(t, strings.Repeat("a", 1000), chunks[0].Content)
	require.Equal(t, strings.Repeat("b", 1000), chunks[1].Content)
	chunks, reason = chunkSource("\n\n", "CODE")
	require.Nil(t, chunks)
	require.Equal(t, "TEXT_NOT_EXTRACTABLE", reason)
	chunks, reason = chunkSource("# Policy\n\n```\n```\n\nBody remains visible.", "MARKDOWN")
	require.Empty(t, reason)
	require.Len(t, chunks, 1)
	require.Equal(t, "Body remains visible.", chunks[0].Content)
}

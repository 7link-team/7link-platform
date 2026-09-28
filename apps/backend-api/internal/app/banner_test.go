package app

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteBannerRendersFigureAndRuntimeInfo(t *testing.T) {
	var output bytes.Buffer
	err := writeBanner(&output, BannerConfig{
		Enabled: true,
		Text:    "SEVEN",
		Type:    "standard",
		Theme:   "plain",
	}, "test-api", "1.2.3", false)
	require.NoError(t, err)

	assert.NotContains(t, output.String(), "\x1b[")
	assert.Contains(t, output.String(), "Application:  test-api")
	assert.Contains(t, output.String(), "Version:      1.2.3")
	assert.Contains(t, output.String(), "Runtime:      "+runtime.Version())
	assert.Contains(t, output.String(), "System:       "+runtime.GOOS+"/"+runtime.GOARCH)
	assert.Contains(t, output.String(), "Process:")
}

func TestWriteBannerUsesConfiguredTheme(t *testing.T) {
	var spring bytes.Buffer
	require.NoError(t, writeBanner(&spring, BannerConfig{
		Enabled: true,
		Text:    "APP",
		Type:    "small",
		Theme:   "spring",
	}, "test-api", "dev", true))
	assert.Contains(t, spring.String(), "\x1b[92m")

	var rainbow bytes.Buffer
	require.NoError(t, writeBanner(&rainbow, BannerConfig{
		Enabled: true,
		Text:    "APP",
		Type:    "small",
		Theme:   "rainbow",
	}, "test-api", "dev", true))
	assert.Contains(t, rainbow.String(), "\x1b[38;5;")
}

func TestWriteBannerDefaultsToFigurineGradient(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, writeBanner(&output, BannerConfig{
		Enabled: true,
		Text:    "APP",
		Type:    "small",
	}, "test-api", "dev", true))
	assert.Contains(t, output.String(), "\x1b[38;5;")
}

func TestWriteBannerCanBeDisabled(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, writeBanner(&output, BannerConfig{}, "test-api", "dev", false))
	assert.Empty(t, output.String())
}

func TestWriteBannerRejectsInvalidInput(t *testing.T) {
	assert.ErrorContains(t, writeBanner(&bytes.Buffer{}, BannerConfig{
		Enabled: true,
		Text:    "APP",
		Type:    "missing-font",
		Theme:   "plain",
	}, "test-api", "dev", false), `unknown type "missing-font"`)

	assert.EqualError(t, writeBanner(&bytes.Buffer{}, BannerConfig{
		Enabled: true,
		Text:    "APP\nBAD",
		Type:    "standard",
		Theme:   "plain",
	}, "test-api", "dev", false), "banner: text cannot contain control characters")
}

func TestANSIWriterStripsSequencesAcrossWrites(t *testing.T) {
	var output bytes.Buffer
	writer := &ansiStripWriter{writer: &output}
	_, err := writer.Write([]byte("\x1b[38;"))
	require.NoError(t, err)
	_, err = writer.Write([]byte("5;42mgreen\x1b[0m text"))
	require.NoError(t, err)
	assert.Equal(t, "green text", output.String())
}

func TestBannerFontCandidatesResolveNamesAndPassPathsThrough(t *testing.T) {
	// A bare name is tried as given and then title-cased, so both "slant" and
	// "Slant" resolve without the operator having to know the file's casing.
	assert.Equal(t, []string{"slant.flf", "Slant.flf"}, bannerFontCandidates("slant"))
	assert.Equal(t, []string{"slant.flf", "Slant.flf"}, bannerFontCandidates("slant.flf"))
	assert.Equal(t, []string{"slant.flf", "Slant.flf"}, bannerFontCandidates("  slant  "))
	// An already-title-cased name must not be offered twice.
	assert.Equal(t, []string{"Slant.flf"}, bannerFontCandidates("Slant"))

	// Anything that looks like a path is handed back untouched rather than
	// having an extension appended or being title-cased. Rewriting it could
	// turn an explicit path into a different file on disk.
	for name, font := range map[string]string{
		"absolute":  "/usr/share/figlet/slant.flf",
		"relative":  "fonts/slant.flf",
		"traversal": "../../etc/passwd",
	} {
		assert.Equal(t, []string{font}, bannerFontCandidates(font), name)
	}
}

func TestTitleBannerFontCapitalizesEachWord(t *testing.T) {
	for input, want := range map[string]string{
		"slant":      "Slant",
		"big money":  "Big Money",
		"big-money":  "Big-Money",
		"big_money":  "Big_Money",
		"3d":         "3d",
		"":           "",
		"alreadyCap": "AlreadyCap",
	} {
		assert.Equal(t, want, titleBannerFont(input), input)
	}
}

func TestSingleLineFlattensWhitespaceThatWouldBreakTheInfoTable(t *testing.T) {
	// The info block is tab-aligned, so an embedded tab or newline coming from
	// a configured name would misalign or split the table.
	assert.Equal(t, "a b c", singleLine("a\tb\nc"))
	assert.Equal(t, "a b", singleLine("a\rb"))
	assert.Equal(t, "plain", singleLine("plain"))
	assert.Empty(t, singleLine(""))
}

func TestValidateBannerTextRejectsEmptyAndControlCharacters(t *testing.T) {
	require.NoError(t, validateBannerText("platform"))

	for name, text := range map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"tab":        "platform\t",
		"newline":    "platform\n",
		"escape":     "platform\x1b[31m",
	} {
		assert.Error(t, validateBannerText(text), name)
	}
}

func TestANSIStripWriterRemovesEscapeSequencesAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	writer := &ansiStripWriter{writer: &out}

	n, err := writer.Write([]byte("\x1b[31mred\x1b[0m plain"))
	require.NoError(t, err)
	assert.Equal(t, len("\x1b[31mred\x1b[0m plain"), n, "the caller must see every byte accepted")
	assert.Equal(t, "red plain", out.String())

	// A sequence split across two writes must still be stripped; the state
	// machine carries across calls, otherwise a flushed banner could emit raw
	// escape bytes into a log file.
	out.Reset()
	split := &ansiStripWriter{writer: &out}
	_, err = split.Write([]byte("a\x1b"))
	require.NoError(t, err)
	_, err = split.Write([]byte("[1mb"))
	require.NoError(t, err)
	assert.Equal(t, "ab", out.String())

	// An escape that is not a control sequence introducer leaves the following
	// byte intact.
	out.Reset()
	lone := &ansiStripWriter{writer: &out}
	_, err = lone.Write([]byte("a\x1bZb"))
	require.NoError(t, err)
	assert.Equal(t, "ab", out.String())
}

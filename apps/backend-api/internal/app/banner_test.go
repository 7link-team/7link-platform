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
	}, "7link-api", "1.2.3", false)
	require.NoError(t, err)

	assert.NotContains(t, output.String(), "\x1b[")
	assert.Contains(t, output.String(), "Application:  7link-api")
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
	}, "7link-api", "dev", true))
	assert.Contains(t, spring.String(), "\x1b[92m")

	var rainbow bytes.Buffer
	require.NoError(t, writeBanner(&rainbow, BannerConfig{
		Enabled: true,
		Text:    "APP",
		Type:    "small",
		Theme:   "rainbow",
	}, "7link-api", "dev", true))
	assert.Contains(t, rainbow.String(), "\x1b[38;5;")
}

func TestWriteBannerDefaultsToFigurineGradient(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, writeBanner(&output, BannerConfig{
		Enabled: true,
		Text:    "APP",
		Type:    "small",
	}, "7link-api", "dev", true))
	assert.Contains(t, output.String(), "\x1b[38;5;")
}

func TestWriteBannerCanBeDisabled(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, writeBanner(&output, BannerConfig{}, "7link-api", "dev", false))
	assert.Empty(t, output.String())
}

func TestWriteBannerRejectsInvalidInput(t *testing.T) {
	assert.ErrorContains(t, writeBanner(&bytes.Buffer{}, BannerConfig{
		Enabled: true,
		Text:    "APP",
		Type:    "missing-font",
		Theme:   "plain",
	}, "7link-api", "dev", false), `unknown type "missing-font"`)

	assert.EqualError(t, writeBanner(&bytes.Buffer{}, BannerConfig{
		Enabled: true,
		Text:    "APP\nBAD",
		Type:    "standard",
		Theme:   "plain",
	}, "7link-api", "dev", false), "banner: text cannot contain control characters")
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

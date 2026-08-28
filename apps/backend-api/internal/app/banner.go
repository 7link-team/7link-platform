package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/arsham/figurine/v2/figurine"
)

const bannerReset = "\x1b[0m"

var bannerThemes = map[string][]string{
	"spring":  {"\x1b[92m"},
	"ocean":   {"\x1b[38;5;39m", "\x1b[38;5;45m", "\x1b[38;5;51m"},
	"sunset":  {"\x1b[38;5;213m", "\x1b[38;5;209m", "\x1b[38;5;203m"},
	"fire":    {"\x1b[38;5;220m", "\x1b[38;5;208m", "\x1b[38;5;196m"},
	"rainbow": nil,
	"plain":   nil,
}

func writeBanner(writer io.Writer, config BannerConfig, fallback, version string, colorsEnabled bool) (err error) {
	if !config.Enabled {
		return nil
	}

	application := fallback
	if application == "" {
		application = filepath.Base(os.Args[0])
	}
	text := config.Text
	if text == "" {
		text = application
	}
	if err = validateBannerText(text); err != nil {
		return err
	}

	font := config.Type
	if font == "" {
		font = "standard"
	}
	theme := config.Theme
	if theme == "" {
		theme = "rainbow"
	}
	colors, ok := bannerThemes[theme]
	if !ok {
		return fmt.Errorf("banner: unknown theme %q", theme)
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("banner: render type %q: %v", font, recovered)
		}
	}()
	preserveFigurineColors := theme == "rainbow" && colorsEnabled
	rows, err := renderBanner(text, font, preserveFigurineColors)
	if err != nil {
		return err
	}
	useColor := len(colors) > 0 && colorsEnabled
	for index, row := range rows {
		if useColor {
			colorIndex := index * len(colors) / len(rows)
			_, err = fmt.Fprintf(writer, "%s%s%s\n", colors[colorIndex], row, bannerReset)
		} else {
			_, err = fmt.Fprintln(writer, row)
		}
		if err != nil {
			return fmt.Errorf("banner: write: %w", err)
		}
	}
	if err = writeBannerInfo(writer, application, version); err != nil {
		return err
	}
	return nil
}

func renderBanner(text, font string, preserveColors bool) ([]string, error) {
	var rendered bytes.Buffer
	var target io.Writer = &rendered
	var stripper *ansiStripWriter
	if !preserveColors {
		stripper = &ansiStripWriter{writer: &rendered}
		target = stripper
	}

	var renderErr error
	for _, candidate := range bannerFontCandidates(font) {
		rendered.Reset()
		if stripper != nil {
			stripper.state = 0
		}
		renderErr = figurine.Write(target, text, candidate)
		if renderErr == nil {
			output := strings.TrimSuffix(rendered.String(), "\n")
			return strings.Split(output, "\n"), nil
		}
	}
	return nil, fmt.Errorf("banner: unknown type %q: %w", font, renderErr)
}

func bannerFontCandidates(font string) []string {
	font = strings.TrimSpace(font)
	if strings.ContainsAny(font, `/\\`) || strings.Contains(font, "..") {
		return []string{font}
	}
	if strings.HasSuffix(strings.ToLower(font), ".flf") {
		font = font[:len(font)-len(".flf")]
	}
	candidates := []string{font + ".flf"}
	titled := titleBannerFont(font) + ".flf"
	if titled != candidates[0] {
		candidates = append(candidates, titled)
	}
	return candidates
}

func titleBannerFont(font string) string {
	start := true
	return strings.Map(func(char rune) rune {
		if start && unicode.IsLetter(char) {
			start = false
			return unicode.ToUpper(char)
		}
		start = char == ' ' || char == '-' || char == '_'
		return char
	}, font)
}

func writeBannerInfo(writer io.Writer, name, version string) error {
	if version == "" {
		version = "unknown"
	}
	info := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(info, "Application:\t%s\n", singleLine(name))
	_, _ = fmt.Fprintf(info, "Version:\t%s\n", singleLine(version))
	_, _ = fmt.Fprintf(info, "Runtime:\t%s\n", runtime.Version())
	_, _ = fmt.Fprintf(info, "System:\t%s/%s (%d CPUs)\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	_, _ = fmt.Fprintf(info, "Process:\t%s\n", strconv.Itoa(os.Getpid()))
	_, _ = fmt.Fprintln(info)
	if err := info.Flush(); err != nil {
		return fmt.Errorf("banner: write info: %w", err)
	}
	return nil
}

func singleLine(value string) string {
	return strings.Map(func(char rune) rune {
		if char == '\t' || char == '\r' || char == '\n' {
			return ' '
		}
		return char
	}, value)
}

func validateBannerText(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("banner: text cannot be empty")
	}
	for _, char := range text {
		if unicode.IsControl(char) {
			return fmt.Errorf("banner: text cannot contain control characters")
		}
	}
	return nil
}

type ansiStripWriter struct {
	writer io.Writer
	state  uint8
}

func (writer *ansiStripWriter) Write(data []byte) (int, error) {
	plain := make([]byte, 0, len(data))
	for _, char := range data {
		switch writer.state {
		case 0:
			if char == 0x1b {
				writer.state = 1
			} else {
				plain = append(plain, char)
			}
		case 1:
			if char == '[' {
				writer.state = 2
			} else {
				writer.state = 0
			}
		case 2:
			if char >= 0x40 && char <= 0x7e {
				writer.state = 0
			}
		}
	}
	if len(plain) > 0 {
		written, err := writer.writer.Write(plain)
		if err != nil {
			return 0, err
		}
		if written != len(plain) {
			return 0, io.ErrShortWrite
		}
	}
	return len(data), nil
}

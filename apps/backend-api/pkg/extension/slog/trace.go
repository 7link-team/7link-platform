package slogx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/lmittmann/tint"
	"go.opentelemetry.io/otel/trace"
)

// NewText creates a context-aware text logger. The writer may be an
// io.MultiWriter, including a rolling-file writer, without changing this layer.
func NewText(writer io.Writer, options *slog.HandlerOptions) *slog.Logger {
	return slog.New(NewTraceHandler(slog.NewTextHandler(writer, options)))
}

// NewConsole creates a text logger with colors enabled in debug mode.
func NewConsole(writer io.Writer, debug bool, label string, options *slog.HandlerOptions) *slog.Logger {
	if !debug {
		return NewText(writer, options)
	}
	label = sanitizeConsoleLabel(label)

	tintOptions := &tint.Options{
		AddSource:   true,
		Level:       slog.LevelDebug,
		ReplaceAttr: consoleReplaceAttr(label, nil),
	}
	if options != nil {
		if options.Level != nil {
			tintOptions.Level = options.Level
		}
		tintOptions.ReplaceAttr = consoleReplaceAttr(label, options.ReplaceAttr)
	}
	return slog.New(NewTraceHandler(tint.NewTextHandler(writer, tintOptions)))
}

const (
	ansiReset                 = "\x1b[0m"
	ansiFaint                 = "\x1b[2m"
	ansiNormalIntensity       = "\x1b[22m"
	ansiBrightCyan            = "\x1b[96m"
	ansiBrightGreen           = "\x1b[92m"
	ansiBrightYellow          = "\x1b[93m"
	ansiBrightRed             = "\x1b[91m"
	ansiBoldMagenta           = "\x1b[22;1;95m"
	ansiCyan                  = "\x1b[36m"
	consoleRed          uint8 = 9
)

func consoleReplaceAttr(label string, next func([]string, slog.Attr) slog.Attr) func([]string, slog.Attr) slog.Attr {
	return func(groups []string, attr slog.Attr) slog.Attr {
		if next != nil {
			attr = next(groups, attr)
		}
		if attr.Key == "" || attr.Value.Kind() == slog.KindLogValuer {
			return attr
		}

		switch attr.Key {
		case slog.TimeKey:
			return slog.String(attr.Key,
				ansiNormalIntensity+ansiBrightYellow+attr.Value.Time().Format("2006-01-02T15:04:05.000Z07:00")+ansiReset)
		case slog.LevelKey:
			return slog.String(attr.Key, formatConsoleLevel(attr.Value))
		case slog.SourceKey:
			return slog.String(attr.Key, formatConsoleSource(attr.Value, label))
		case slog.MessageKey:
			return attr
		}

		if attr.Value.Kind() != slog.KindGroup {
			attr.Key = ansiNormalIntensity + attr.Key
		}
		if strings.TrimPrefix(attr.Key, ansiNormalIntensity) == "error" {
			return tint.Attr(consoleRed, attr)
		}
		if attr.Value.Kind() == slog.KindAny {
			if _, ok := attr.Value.Any().(error); ok {
				return tint.Attr(consoleRed, attr)
			}
		}
		return attr
	}
}

func sanitizeConsoleLabel(label string) string {
	label = strings.TrimSpace(strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return -1
		}
		return char
	}, label))
	if label == "" {
		label = filepath.Base(os.Args[0])
	}
	if label == "" {
		return "process"
	}
	return label
}

func formatConsoleLevel(value slog.Value) string {
	level, ok := value.Any().(slog.Level)
	if !ok {
		return fmt.Sprintf("%-5s", value.String())
	}
	var color, name string
	switch {
	case level < slog.LevelInfo:
		color, name = ansiBrightCyan, "DEBUG"
	case level < slog.LevelWarn:
		color, name = ansiBrightGreen, "INFO"
	case level < slog.LevelError:
		color, name = ansiBrightYellow, "WARN"
	default:
		color, name = ansiBrightRed, "ERROR"
	}
	return color + fmt.Sprintf("%-5s", name) + ansiReset
}

func formatConsoleSource(value slog.Value, label string) string {
	contextLabel := ansiBoldMagenta + "[" + label + "]" + ansiReset
	source, ok := value.Any().(*slog.Source)
	if !ok || source == nil {
		return contextLabel + " " + ansiFaint + "-->*" + ansiReset
	}
	caller := fmt.Sprintf("(%s:%d)", filepath.Base(source.File), source.Line)
	return contextLabel + " " + ansiNormalIntensity + ansiCyan + caller + ansiReset + " " + ansiFaint + "-->*" + ansiReset
}

// NewTraceHandler decorates any slog handler with OpenTelemetry trace fields.
func NewTraceHandler(next slog.Handler) slog.Handler {
	return &traceHandler{next: next}
}

type traceHandler struct {
	next slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, record slog.Record) error {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		record.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}
	return h.next.Handle(ctx, record)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{next: h.next.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{next: h.next.WithGroup(name)}
}

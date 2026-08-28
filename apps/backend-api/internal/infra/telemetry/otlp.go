package telemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	OTLPProtocolGRPC = "grpc"
	OTLPProtocolHTTP = "http/protobuf"
)

// Config controls optional telemetry exporters. Local tracing and context
// propagation remain available when every exporter is disabled.
type Config struct {
	OTLP OTLPConfig `mapstructure:"otlp" group:"otlp"`
}

// OTLPConfig contains transport settings for an OTLP trace exporter. An empty
// endpoint deliberately leaves endpoint resolution to the OpenTelemetry
// exporter, including its standard environment variables and defaults.
type OTLPConfig struct {
	Enabled  bool              `mapstructure:"enabled" description:"export traces to an OTLP collector" default:"false"`
	Protocol string            `mapstructure:"protocol" description:"OTLP transport: grpc | http/protobuf" default:"grpc" trim:"true"`
	Endpoint string            `mapstructure:"endpoint" description:"OTLP collector host and port" trim:"true"`
	Insecure bool              `mapstructure:"insecure" description:"disable TLS for the OTLP connection" default:"false"`
	Headers  map[string]string `mapstructure:"headers" group:"headers" mapkey:"<header>" description:"OTLP request headers" secret:"true"`
	Timeout  time.Duration     `mapstructure:"timeout" description:"OTLP export timeout" default:"10s"`
}

func newOTLPExporter(ctx context.Context, settings OTLPConfig) (sdktrace.SpanExporter, error) {
	if settings.Timeout < 0 {
		return nil, fmt.Errorf("otel: OTLP timeout cannot be negative: %s", settings.Timeout)
	}
	switch strings.TrimSpace(settings.Protocol) {
	case OTLPProtocolGRPC:
		options := make([]otlptracegrpc.Option, 0, 4)
		if endpoint := strings.TrimSpace(settings.Endpoint); endpoint != "" {
			options = append(options, otlptracegrpc.WithEndpoint(endpoint))
		}
		if settings.Insecure {
			options = append(options, otlptracegrpc.WithInsecure())
		}
		if len(settings.Headers) > 0 {
			options = append(options, otlptracegrpc.WithHeaders(settings.Headers))
		}
		if settings.Timeout > 0 {
			options = append(options, otlptracegrpc.WithTimeout(settings.Timeout))
		}
		exporter, err := otlptracegrpc.New(ctx, options...)
		if err != nil {
			return nil, fmt.Errorf("otel: create OTLP gRPC trace exporter: %w", err)
		}
		return exporter, nil

	case OTLPProtocolHTTP:
		options := make([]otlptracehttp.Option, 0, 4)
		if endpoint := strings.TrimSpace(settings.Endpoint); endpoint != "" {
			options = append(options, otlptracehttp.WithEndpoint(endpoint))
		}
		if settings.Insecure {
			options = append(options, otlptracehttp.WithInsecure())
		}
		if len(settings.Headers) > 0 {
			options = append(options, otlptracehttp.WithHeaders(settings.Headers))
		}
		if settings.Timeout > 0 {
			options = append(options, otlptracehttp.WithTimeout(settings.Timeout))
		}
		exporter, err := otlptracehttp.New(ctx, options...)
		if err != nil {
			return nil, fmt.Errorf("otel: create OTLP HTTP trace exporter: %w", err)
		}
		return exporter, nil

	default:
		return nil, fmt.Errorf("otel: unsupported OTLP protocol %q", settings.Protocol)
	}
}

package otelexport

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// otlpBridge converts spanData batches into OTel SDK ReadOnlySpan values and
// exports them via an OTLP SpanExporter.
type otlpBridge struct {
	exporter sdktrace.SpanExporter
	resource *resource.Resource
}

// newOTLPBridge creates an otlpBridge configured from cfg.
// It selects a gRPC or HTTP OTLP exporter based on cfg.Protocol.
func newOTLPBridge(ctx context.Context, cfg Config) (*otlpBridge, error) {
	var exporter sdktrace.SpanExporter
	var err error

	switch cfg.Protocol {
	case GRPC:
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpointURL(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		exporter, err = otlptracegrpc.New(ctx, opts...)
	case HTTP:
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpointURL(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		exporter, err = otlptracehttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("otelexport: unknown protocol %d", cfg.Protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("otelexport: create OTLP exporter: %w", err)
	}

	res := resource.NewSchemaless(
		attribute.String("service.name", cfg.ServiceName),
	)

	return &otlpBridge{
		exporter: exporter,
		resource: res,
	}, nil
}

// export converts a batch of spanData into ReadOnlySpan values via SpanStub
// snapshots and sends them to the underlying OTLP exporter.
func (b *otlpBridge) export(ctx context.Context, batch []spanData) error {
	spans := make([]sdktrace.ReadOnlySpan, len(batch))
	for i, sd := range batch {
		spans[i] = b.toReadOnlySpan(sd)
	}
	return b.exporter.ExportSpans(ctx, spans)
}

// shutdown gracefully stops the underlying exporter.
func (b *otlpBridge) shutdown(ctx context.Context) error {
	return b.exporter.Shutdown(ctx)
}

// toReadOnlySpan converts a spanData into a ReadOnlySpan using tracetest.SpanStub.
func (b *otlpBridge) toReadOnlySpan(sd spanData) sdktrace.ReadOnlySpan {
	// Build span context (this span).
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    sd.traceID,
		SpanID:     sd.spanID,
		TraceFlags: oteltrace.FlagsSampled,
	})

	// Build parent span context.
	var parent oteltrace.SpanContext
	if sd.parentID.IsValid() {
		parent = oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
			TraceID:    sd.traceID,
			SpanID:     sd.parentID,
			TraceFlags: oteltrace.FlagsSampled,
		})
	}

	// Convert attributes map to OTel key-value pairs.
	attrs := make([]attribute.KeyValue, 0, len(sd.attributes))
	for k, v := range sd.attributes {
		attrs = append(attrs, attribute.String(k, v))
	}

	// Convert extra causal parents to links.
	links := make([]sdktrace.Link, len(sd.links))
	for i, linkSpanID := range sd.links {
		links[i] = sdktrace.Link{
			SpanContext: oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
				TraceID:    sd.traceID,
				SpanID:     linkSpanID,
				TraceFlags: oteltrace.FlagsSampled,
			}),
		}
	}

	// Events are point-in-time; EndTime == StartTime for events.
	endTime := sd.startTime
	if endTime.IsZero() {
		endTime = time.Now()
	}

	stub := tracetest.SpanStub{
		Name:        sd.name,
		SpanContext: sc,
		Parent:      parent,
		SpanKind:    oteltrace.SpanKindInternal,
		StartTime:   sd.startTime,
		EndTime:     endTime,
		Attributes:  attrs,
		Links:       links,
		Status: sdktrace.Status{
			Code: 0, // Unset
		},
		Resource: b.resource,
	}

	return stub.Snapshot()
}

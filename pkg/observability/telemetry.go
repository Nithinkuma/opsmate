// Package observability sets up OpenTelemetry tracing and metrics, structured
// logging via slog, and context helpers for propagating intent IDs.
package observability

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

type contextKey string

const intentIDKey contextKey = "intent_id"

// WithIntentID returns a context carrying the given intent ID.
// All spans started from this context will have the intent_id attribute.
func WithIntentID(ctx context.Context, intentID string) context.Context {
	return context.WithValue(ctx, intentIDKey, intentID)
}

// IntentIDFromContext retrieves the intent ID, or empty string if absent.
func IntentIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(intentIDKey).(string)
	return v
}

// Start begins an OTEL span, automatically attaching intent_id if present.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if id := IntentIDFromContext(ctx); id != "" {
		opts = append(opts, trace.WithAttributes(attribute.String("intent_id", id)))
	}
	return otel.Tracer("opsmate").Start(ctx, name, opts...)
}

// Config holds OTEL configuration.
type Config struct {
	OTLPEndpoint string
	LogLevel     string
	ServiceName  string
}

// Setup initialises the global tracer provider and returns a shutdown function.
// Returns a no-op shutdown and nil error when OTLPEndpoint is empty (dev mode).
func Setup(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "opsmate"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, err
	}

	if cfg.OTLPEndpoint == "" {
		// Dev mode: no-op exporter
		otel.SetTracerProvider(sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
		))
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// NewLogger returns a structured slog.Logger at the given level.
func NewLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

// LogLLMCall emits a structured log line for an LLM call with cost/token data.
func LogLLMCall(ctx context.Context, log *slog.Logger, model, operation string, inputTok, outputTok int, costUSD float64) {
	log.InfoContext(ctx, "llm_call",
		"intent_id", IntentIDFromContext(ctx),
		"model", model,
		"operation", operation,
		"input_tokens", inputTok,
		"output_tokens", outputTok,
		"cost_usd", costUSD,
	)
}

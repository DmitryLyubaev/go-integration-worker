package telemetry

import (
	"context"
	"errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"io"
	"log/slog"
	"net/http"
	"sync"
)

func Start(ctx context.Context, writer io.Writer) (func(context.Context) error, error) {
	exporter, e := stdouttrace.New(stdouttrace.WithWriter(writer))
	if e != nil {
		return nil, e
	}
	previous := otel.GetTracerProvider()
	previousHandler := otel.GetErrorHandler()
	var mu sync.Mutex
	var exportError error
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(e error) { mu.Lock(); exportError = e; mu.Unlock(); slog.Error("trace.export_failure") }))
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return func(ctx context.Context) error {
		e := provider.Shutdown(ctx)
		otel.SetTracerProvider(previous)
		otel.SetErrorHandler(previousHandler)
		mu.Lock()
		defer mu.Unlock()
		return errors.Join(e, exportError)
	}, nil
}
func Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		name := "receiver.request"
		if r.URL.Path == "/exports" {
			name = "receiver.export"
		}
		ctx, span := otel.Tracer("p27").Start(ctx, name)
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

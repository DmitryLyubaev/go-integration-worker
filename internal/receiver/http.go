package receiver

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"net/http"
	"sync/atomic"
)

func NewHandler(s *Store, f *FaultController) http.Handler {
	var active atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /exports", func(w http.ResponseWriter, r *http.Request) {
		p, e := domain.Decode(r.Body)
		if e != nil {
			http.Error(w, "invalid event", 400)
			return
		}
		n := active.Add(1)
		trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("event_id", p.Event.EventID))
		defer active.Add(-1)
		slog.Info("receiver.request", "event_id", p.Event.EventID, "inflight", n)
		fault, ordinal := f.next(p.Event.EventID)
		if fault.Status != 0 && ordinal <= fault.Failures {
			w.WriteHeader(fault.Status)
			slog.Info("receiver.reject", "event_id", p.Event.EventID, "status", fault.Status)
			return
		}
		ctx := r.Context()
		if fault.GatePath != "" && ordinal == 1 {
			if e = waitGate(fault.GatePath); e != nil {
				http.Error(w, "gate expired", 503)
				return
			}
			ctx = context.WithoutCancel(r.Context())
		}
		receipt, created, e := s.Apply(ctx, p)
		if errors.Is(e, ErrConflict) {
			http.Error(w, "conflicting identity", 409)
			return
		}
		if e != nil {
			slog.Error("receiver.storage_failure", "event_id", p.Event.EventID)
			closeResponse(w)
			return
		}
		slog.Info("receiver.commit", "event_id", p.Event.EventID, "created", created, "receipt_id", receipt.ReceiptID)
		if fault.DropResponses < 0 || ordinal <= fault.DropResponses {
			slog.Info("receiver.drop", "event_id", p.Event.EventID)
			closeResponse(w)
			return
		}
		status := 200
		if created {
			status = 201
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(receipt)
	})
	mux.HandleFunc("GET /receipts/{id}", func(w http.ResponseWriter, r *http.Request) {
		slog.Info("receiver.lookup", "event_id", r.PathValue("id"))
		receipt, e := s.Receipt(r.Context(), r.PathValue("id"))
		if errors.Is(e, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if e != nil {
			http.Error(w, "receipt unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(receipt)
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, c := context.WithTimeout(r.Context(), 1000000000)
		defer c()
		if e := s.pool.Ping(ctx); e != nil {
			http.Error(w, "not ready", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"protocol":"p27-receiver-v1"}`))
	})
	return telemetry.Handler(mux)
}
func closeResponse(w http.ResponseWriter) {
	if hijacker, ok := w.(http.Hijacker); ok {
		connection, _, e := hijacker.Hijack()
		if e == nil {
			connection.Close()
		}
	}
}

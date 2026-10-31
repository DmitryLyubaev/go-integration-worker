package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/delivery"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/queue"
	"github.com/DmitryLyubaev/go-integration-worker/internal/receiver"
	"github.com/DmitryLyubaev/go-integration-worker/internal/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"time"
)

func Run(ctx context.Context, args []string, out, stderr io.Writer) (code int) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(stderr, nil)))
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: p27 enqueue FILE | worker | receiver | status [ID] | reconcile ID")
		return 2
	}
	traceWriter := io.Discard
	var file *os.File
	if path := os.Getenv("P27_TRACE_FILE"); path != "" {
		var e error
		file, e = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			fmt.Fprintln(stderr, "cannot open trace file")
			return 2
		}
		defer file.Close()
		traceWriter = file
	}
	shutdown, e := telemetry.Start(ctx, traceWriter)
	if e != nil {
		fmt.Fprintln(stderr, "cannot initialize tracing")
		return 2
	}
	defer func() {
		flush, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		if e := shutdown(flush); e != nil {
			fmt.Fprintln(stderr, "trace export/flush failed")
			code = 1
		}
	}()
	role := "P27_WORKER_DSN"
	if args[0] == "receiver" {
		role = "P27_RECEIVER_DSN"
	}
	dsn := os.Getenv(role)
	if dsn == "" {
		fmt.Fprintln(stderr, "database configuration is required")
		return 2
	}
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		fmt.Fprintln(stderr, "database configuration is invalid")
		return 2
	}
	defer pool.Close()
	store := queue.New(pool)
	fail := func(reason string) int { fmt.Fprintln(stderr, reason); return 1 }
	emit := func(value any) int {
		if e := json.NewEncoder(out).Encode(value); e != nil {
			return fail("cannot write command result")
		}
		return 0
	}
	switch args[0] {
	case "enqueue":
		if len(args) != 2 {
			return fail("enqueue requires one fixture")
		}
		f, e := os.Open(args[1])
		if e != nil {
			return fail("cannot read fixture")
		}
		p, e := domain.Decode(f)
		f.Close()
		if e != nil {
			return fail("invalid event fixture")
		}
		spanCtx, span := otel.Tracer("p27").Start(ctx, "enqueue")
		j, created, e := store.Enqueue(spanCtx, p)
		span.End()
		if e != nil {
			return fail("enqueue rejected or database unavailable")
		}
		return emit(map[string]any{"created": created, "job": j})
	case "status":
		if len(args) > 2 {
			return fail("status takes zero or one ID")
		}
		id := ""
		if len(args) == 2 {
			id = args[1]
		}
		jobs, e := store.Status(ctx, id)
		if e != nil {
			return fail("status unavailable")
		}
		return emit(jobs)
	case "reconcile":
		if len(args) != 2 {
			return fail("reconcile requires one ID")
		}
		client, e := configuredClient()
		if e != nil {
			return fail("invalid receiver configuration")
		}
		receipt, e := client.Receipt(ctx, args[1])
		if e != nil {
			return fail("receipt missing or unavailable; job unchanged")
		}
		if e = store.Reconcile(ctx, args[1], receipt); e != nil {
			return fail("receipt mismatch or job not terminal; job unchanged")
		}
		return emit(receipt)
	case "worker":
		if len(args) != 1 {
			return fail("worker takes no arguments")
		}
		client, e := configuredClient()
		if e != nil {
			return fail("invalid receiver configuration")
		}
		config := delivery.Config{ShutdownTimeout: 3 * time.Second}
		if gate := os.Getenv("P27_CLAIM_GATE"); gate != "" {
			if os.Getenv("P27_TEST_MODE") != "1" {
				return fail("claim gates require local test mode")
			}
			config.BeforeSend = func(ctx context.Context, claim queue.Claim) error {
				raw, _ := json.Marshal(claim)
				if e := os.WriteFile(gate+".claim", raw, 0600); e != nil {
					return e
				}
				deadline := time.NewTimer(10 * time.Second)
				defer deadline.Stop()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-deadline.C:
						return errors.New("claim gate expired")
					case <-ticker.C:
						if _, e := os.Stat(gate + ".release"); e == nil {
							return nil
						}
					}
				}
			}
		}
		slog.Info("worker.start", "goroutines", runtime.NumGoroutine())
		e = delivery.Run(ctx, store, client, config)
		slog.Info("worker.stop", "goroutines", runtime.NumGoroutine(), "joined", e == nil)
		if e != nil {
			return fail("worker failed; inspect retained leases and logs")
		}
		return 0
	case "receiver":
		if len(args) != 1 {
			return fail("receiver takes no arguments")
		}
		var rules map[string]receiver.Fault
		if path := os.Getenv("P27_FAULTS"); path != "" {
			if os.Getenv("P27_TEST_MODE") != "1" {
				return fail("faults require local test mode")
			}
			raw, e := os.ReadFile(path)
			if e != nil || json.Unmarshal(raw, &rules) != nil {
				return fail("invalid local faults file")
			}
		}
		faults, e := receiver.NewFaultController(os.Getenv("P27_TEST_MODE") == "1", rules)
		if e != nil {
			return fail("invalid local faults")
		}
		address := os.Getenv("P27_LISTEN")
		if address == "" {
			address = "127.0.0.1:8127"
		}
		return serve(ctx, address, receiver.NewHandler(receiver.NewStore(pool), faults), stderr)
	default:
		return fail("unknown command")
	}
}
func configuredClient() (*delivery.Client, error) {
	base := os.Getenv("P27_RECEIVER_URL")
	if base == "" {
		base = "http://127.0.0.1:8127"
	}
	return delivery.NewClient(base)
}
func serve(ctx context.Context, address string, handler http.Handler, stderr io.Writer) int {
	if e := validateListen(address); e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case e := <-done:
		if !errors.Is(e, http.ErrServerClosed) {
			fmt.Fprintln(stderr, "receiver listener failed")
			return 1
		}
		return 0
	case <-ctx.Done():
	}
	shutdown, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	if e := server.Shutdown(shutdown); e != nil {
		server.Close()
		fmt.Fprintln(stderr, "receiver shutdown failed")
		return 1
	}
	return 0
}

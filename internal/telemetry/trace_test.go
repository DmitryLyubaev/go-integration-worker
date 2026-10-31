package telemetry_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/DmitryLyubaev/go-integration-worker/internal/delivery"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/queue"
	"github.com/DmitryLyubaev/go-integration-worker/internal/receiver"
	"github.com/DmitryLyubaev/go-integration-worker/internal/telemetry"
	"github.com/DmitryLyubaev/go-integration-worker/internal/testutil"
	"go.opentelemetry.io/otel"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) {
	return 0, errors.New("controlled trace write failure")
}
func TestTraceLinkAndFlush(t *testing.T) {
	db := testutil.PostgreSQL(t)
	var capture bytes.Buffer
	stop, e := telemetry.Start(context.Background(), &capture)
	if e != nil {
		t.Fatal(e)
	}
	ctx, span := otel.Tracer("p27").Start(context.Background(), "enqueue")
	p, _ := domain.Decode(strings.NewReader(`{"event_id":"00000000-0000-0000-0000-000000000001","sale_reference":"trace_1","currency":"AUD","amount_minor":1000}`))
	s := queue.New(db.Worker)
	job, _, e := s.Enqueue(ctx, p)
	span.End()
	if e != nil || job.Traceparent == "" {
		t.Fatalf("durable trace missing: %v", e)
	}
	server := httptest.NewServer(receiver.NewHandler(receiver.NewStore(db.Receiver), nil))
	client, _ := delivery.NewClient(server.URL)
	claim, e := s.ClaimDue(context.Background())
	if e != nil || claim == nil {
		t.Fatal(e)
	} // delivery.Run exercises restart-parent extraction, not a test-side recreation
	s.Complete(context.Background(), *claim, domain.Outcome{Kind: domain.Retryable})
	db.Admin.Exec(context.Background(), "UPDATE worker.jobs SET due_at=clock_timestamp()-interval '1 second'")
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- delivery.Run(runCtx, s, client, delivery.Config{}) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ := s.Status(context.Background(), p.Event.EventID)
		if len(jobs) == 1 && jobs[0].State == "delivered" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	server.Close()
	if e = stop(context.Background()); e != nil {
		t.Fatal(e)
	}
	raw := capture.String()
	for _, name := range []string{"enqueue", "attempt", "receiver.export"} {
		if !strings.Contains(raw, name) {
			t.Fatalf("missing %s span", name)
		}
	}
	traceID := strings.Split(job.Traceparent, "-")[1]
	if strings.Count(strings.ToLower(raw), strings.ToLower(traceID)) < 3 {
		t.Fatal("trace identity not propagated across durable attempt/receiver")
	}
	stop, e = telemetry.Start(context.Background(), failedWriter{})
	if e != nil {
		t.Fatal(e)
	}
	_, span = otel.Tracer("p27").Start(context.Background(), "failure")
	span.End()
	bounded, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	if e = stop(bounded); e == nil {
		t.Fatal("trace exporter failure hidden")
	}
	_ = io.Discard
}

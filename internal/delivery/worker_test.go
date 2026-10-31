package delivery

import (
	"context"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/queue"
	"github.com/DmitryLyubaev/go-integration-worker/internal/testutil"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTwoWorkersCancelAndJoin(t *testing.T) {
	db := testutil.PostgreSQL(t)
	s := queue.New(db.Worker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 1; i <= 4; i++ {
		p, _ := domain.Decode(strings.NewReader(strings.Replace(string(event(t).Bytes), "000000000001", fmt.Sprintf("%012d", i), 1)))
		if _, _, e := s.Enqueue(ctx, p); e != nil {
			t.Fatal(e)
		}
	}
	entered := make(chan struct{}, 4)
	var active, max atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		n := active.Add(1)
		defer active.Add(-1)
		for old := max.Load(); n > old && !max.CompareAndSwap(old, n); old = max.Load() {
		}
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, s, client, Config{}) }()
	for range 2 {
		select {
		case <-entered:
		case e := <-done:
			t.Fatalf("worker exited early: %v", e)
		case <-time.After(3 * time.Second):
			t.Fatal("two concurrent requests not observed")
		}
	}
	start := time.Now()
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join")
	}
	if time.Since(start) > 5*time.Second || max.Load() != 2 {
		t.Fatal("concurrency/shutdown violation")
	}
	var claims, ambiguous int
	db.Admin.QueryRow(context.Background(), "SELECT sum(attempt_count),count(*) FILTER(WHERE ambiguous) FROM worker.jobs").Scan(&claims, &ambiguous)
	if claims != 2 || ambiguous != 2 {
		t.Fatalf("claims=%d ambiguous=%d", claims, ambiguous)
	}
}
func TestFinalBookkeepingFailureLeavesLease(t *testing.T) {
	db := testutil.PostgreSQL(t)
	s := queue.New(db.Worker)
	p := event(t)
	s.Enqueue(context.Background(), p)
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(entered)
		<-release
		w.WriteHeader(503)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, s, client, Config{}) }()
	select {
	case <-entered:
	case e := <-done:
		t.Fatalf("early exit %v", e)
	case <-time.After(3 * time.Second):
		t.Fatal("send not observed")
	}
	tx, e := db.Admin.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	tx.Exec(context.Background(), "LOCK TABLE worker.jobs IN ACCESS EXCLUSIVE MODE")
	cancel()
	close(release)
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("bookkeeping failure hidden")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bookkeeping exceeded shutdown")
	}
	tx.Rollback(context.Background())
	var state string
	db.Admin.QueryRow(context.Background(), "SELECT state FROM worker.jobs").Scan(&state)
	if state != "processing" {
		t.Fatal("lease not recoverable: " + state)
	}
}

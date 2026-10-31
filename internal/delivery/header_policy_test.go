package delivery

import (
	"context"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/queue"
	"github.com/DmitryLyubaev/go-integration-worker/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStatusHeadersSurviveInvalidBodies(t *testing.T) {
	for _, status := range []int{401, 302, 422, 429} {
		for _, bodyMode := range []string{"oversized", "truncated"} {
			t.Run(fmt.Sprintf("%d_%s", status, bodyMode), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if status == 429 {
						w.Header().Set("Retry-After", "120")
					}
					if bodyMode == "truncated" {
						w.Header().Set("Content-Length", "50")
					}
					w.WriteHeader(status)
					if bodyMode == "oversized" {
						w.Write([]byte(strings.Repeat("x", 4097)))
					} else {
						w.Write([]byte("x"))
					}
				}))
				defer server.Close()
				client, _ := NewClient(server.URL)
				outcome := client.Send(context.Background(), event(t))
				switch status {
				case 401, 302:
					if outcome.Kind != domain.Unrecognized || !outcome.Ambiguous {
						t.Fatalf("terminal status overridden: %+v", outcome)
					}
				case 422:
					if outcome.Kind != domain.Permanent || outcome.Ambiguous {
						t.Fatalf("permanent status overridden: %+v", outcome)
					}
				case 429:
					if outcome.Kind != domain.Retryable || outcome.Ambiguous || outcome.RetryAfter <= time.Second {
						t.Fatalf("Retry-After stopping limit lost: %+v", outcome)
					}
				}
			})
		}
	}
}

func TestHeaderDecisionsStopFurtherSendsRealDB(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		retryAfter, state string
	}{{"unauthorized", 401, "", "uncertain"}, {"redirect", 302, "", "uncertain"}, {"rejected", 422, "", "failed"}, {"long-delay", 429, "120", "failed"}, {"overflow-delay", 429, "9223372036854775808", "failed"}} {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.PostgreSQL(t)
			store := queue.New(db.Worker)
			p := event(t)
			if _, _, e := store.Enqueue(context.Background(), p); e != nil {
				t.Fatal(e)
			}
			var sends atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
				if tc.name != "overflow-delay" {
					w.Write([]byte(strings.Repeat("x", 4097)))
				}
			}))
			defer server.Close()
			client, _ := NewClient(server.URL)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- Run(ctx, store, client, Config{}) }()
			deadline := time.Now().Add(3 * time.Second)
			var jobs []queue.Job
			for time.Now().Before(deadline) {
				jobs, _ = store.Status(context.Background(), p.Event.EventID)
				if len(jobs) == 1 && (jobs[0].State == "failed" || jobs[0].State == "uncertain") {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if len(jobs) != 1 || jobs[0].State != tc.state || jobs[0].AttemptCount != 1 || sends.Load() != 1 {
				t.Fatalf("policy allowed extra sends: state=%+v wire=%d", jobs, sends.Load())
			}
		})
	}
}

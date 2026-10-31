package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func event(t *testing.T) domain.Payload {
	t.Helper()
	p, e := domain.Decode(strings.NewReader(`{"event_id":"00000000-0000-0000-0000-000000000001","sale_reference":"delivery_1","currency":"AUD","amount_minor":1000}`))
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestHTTPClassificationAndBounds(t *testing.T) {
	p := event(t)
	for _, tc := range []struct {
		status int
		kind   domain.OutcomeKind
		amb    bool
	}{{200, domain.Success, false}, {201, domain.Success, false}, {400, domain.Permanent, false}, {409, domain.Permanent, false}, {422, domain.Permanent, false}, {408, domain.Retryable, false}, {429, domain.Retryable, false}, {500, domain.Retryable, false}, {502, domain.Retryable, false}, {503, domain.Retryable, false}, {504, domain.Retryable, false}, {401, domain.Unrecognized, true}, {302, domain.Unrecognized, true}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 429 {
					w.Header().Set("Retry-After", "1")
				}
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(domain.Receipt{EventID: p.Event.EventID, PayloadHash: p.Hash, ReceiptID: "receipt"})
			}))
			defer server.Close()
			c, _ := NewClient(server.URL)
			o := c.Send(context.Background(), p)
			if o.Kind != tc.kind || o.Ambiguous != tc.amb {
				t.Fatalf("outcome=%+v", o)
			}
			if tc.status == 429 && o.RetryAfter != time.Second {
				t.Fatal("Retry-After ignored")
			}
		})
	}
	for name, body := range map[string]string{"truncated": "{", "wronghash": `{"event_id":"00000000-0000-0000-0000-000000000001","payload_hash":"wrong","receipt_id":"r"}`, "huge": strings.Repeat("x", 4097)} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer server.Close()
			c, _ := NewClient(server.URL)
			o := c.Send(context.Background(), p)
			if o.Kind != domain.Retryable || !o.Ambiguous {
				t.Fatalf("bad success=%+v", o)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer server.Close()
	c, _ := NewClient(server.URL)
	start := time.Now()
	o := c.Send(context.Background(), p)
	elapsed := time.Since(start)
	if !o.Ambiguous || elapsed < 1800*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("deadline=%v outcome=%+v", elapsed, o)
	}
	for _, url := range []string{"https://example.org", "http://user:password@127.0.0.1", "http://127.0.0.1/other"} {
		if _, e := NewClient(url); e == nil {
			t.Fatalf("unsafe URL accepted %s", url)
		}
	}
}
func TestRetryAfterBounds(t *testing.T) {
	p := event(t)
	for _, value := range []string{"2", "1", "-1", "garbage", time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", value); w.WriteHeader(429) }))
		c, _ := NewClient(server.URL)
		o := c.Send(context.Background(), p)
		server.Close()
		if value == "2" && o.RetryAfter <= time.Second {
			t.Fatal("large retry delay lost")
		}
		if value == "1" && o.RetryAfter != time.Second {
			t.Fatal("one second lost")
		}
		if o.RetryAfter < 0 {
			t.Fatal("negative retry delay")
		}
	}
}
func TestWireSendCountAndRedirectDenied(t *testing.T) {
	p := event(t)
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method == "POST" {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		}
	}))
	server.Config.ConnState = func(c net.Conn, s http.ConnState) {}
	server.Start()
	defer server.Close()
	c, _ := NewClient(server.URL)
	c.Send(context.Background(), p)
	if requests.Load() != 1 {
		t.Fatalf("wire posts=%d", requests.Load())
	}
	var foreign atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreign.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	c, _ = NewClient(redirect.URL)
	o := c.Send(context.Background(), p)
	if foreign.Load() != 0 || o.Kind != domain.Unrecognized {
		t.Fatalf("redirect followed: %d %+v", foreign.Load(), o)
	}
}

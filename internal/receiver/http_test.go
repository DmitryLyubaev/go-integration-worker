package receiver

import (
	"context"
	"encoding/json"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/testutil"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPContract(t *testing.T) {
	db := testutil.PostgreSQL(t)
	s := NewStore(db.Receiver)
	server := httptest.NewServer(NewHandler(s, nil))
	defer server.Close()
	p := fixture(t)
	for i, want := range []int{201, 200, 409} {
		body := string(p.Bytes)
		if i == 2 {
			body = strings.Replace(body, "1000", "1001", 1)
		}
		r, e := http.Post(server.URL+"/exports", "application/json", strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("status=%d want=%d", r.StatusCode, want)
		}
	}
	for _, body := range []string{"{}", strings.Repeat(" ", 4097)} {
		r, e := http.Post(server.URL+"/exports", "application/json", strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatal("bad event accepted")
		}
	}
	r, e := http.Get(server.URL + "/receipts/" + p.Event.EventID)
	if e != nil {
		t.Fatal(e)
	}
	var receipt domain.Receipt
	json.NewDecoder(r.Body).Decode(&receipt)
	r.Body.Close()
	if r.StatusCode != 200 || receipt.PayloadHash != p.Hash {
		t.Fatal("receipt mismatch")
	}
	r, e = http.Get(server.URL + "/receipts/missing")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal("missing receipt not 404")
	}
	r, e = http.Get(server.URL + "/ready")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(raw), "p27-receiver-v1") {
		t.Fatal("readiness missing")
	}
	r, e = http.Get(server.URL + "/exports")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 405 || r.Header.Get("Allow") != "POST" {
		t.Fatal("method contract missing")
	}
}
func TestFaultsRequireTestMode(t *testing.T) {
	if _, e := NewFaultController(false, map[string]Fault{"x": {Status: 503, Failures: 1}}); e == nil {
		t.Fatal("faults enabled outside test mode")
	}
	db := testutil.PostgreSQL(t)
	f, e := NewFaultController(true, map[string]Fault{fixture(t).Event.EventID: {Status: 503, Failures: 1}})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(NewHandler(NewStore(db.Receiver), f))
	defer server.Close()
	r, e := http.Post(server.URL+"/exports", "application/json", strings.NewReader(string(fixture(t).Bytes)))
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	var n int
	db.Admin.QueryRow(context.Background(), "SELECT count(*) FROM receiver.effects").Scan(&n)
	if r.StatusCode != 503 || n != 0 {
		t.Fatal("fault caused effect")
	}
}
func TestCommitThenCloseRealHTTP(t *testing.T) {
	db := testutil.PostgreSQL(t)
	s := NewStore(db.Receiver)
	p := fixture(t)
	f, _ := NewFaultController(true, map[string]Fault{p.Event.EventID: {DropResponses: 1}})
	server := httptest.NewServer(NewHandler(s, f))
	defer server.Close()
	if r, e := http.Post(server.URL+"/exports", "application/json", strings.NewReader(string(p.Bytes))); e == nil {
		r.Body.Close()
		t.Fatal("response not lost")
	}
	receipt, e := s.Receipt(context.Background(), p.Event.EventID)
	if e != nil || receipt.PayloadHash != p.Hash {
		t.Fatalf("commit missing: %v", e)
	}
	r, e := http.Post(server.URL+"/exports", "application/json", strings.NewReader(string(p.Bytes)))
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("replay did not return original")
	}
}

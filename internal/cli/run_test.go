package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/DmitryLyubaev/go-integration-worker/internal/testutil"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCLIEnqueueStatusAndExitCodes(t *testing.T) {
	db := testutil.PostgreSQL(t)
	dir := t.TempDir()
	binary := filepath.Join(dir, "p27")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/p27")
	if raw, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %s %v", raw, e)
	}
	fixture := filepath.Join(dir, "event.json")
	raw := `{"event_id":"00000000-0000-0000-0000-000000000001","sale_reference":"cli_1","currency":"AUD","amount_minor":1000}`
	receiverURL := ""
	os.WriteFile(fixture, []byte(raw), 0600)
	run := func(ok bool, args ...string) string {
		t.Helper()
		c := exec.Command(binary, args...)
		c.Env = append(os.Environ(), "P27_WORKER_DSN="+db.WorkerDSN, "P27_TRACE_FILE="+filepath.Join(dir, "trace.jsonl"), "P27_RECEIVER_URL="+receiverURL)
		var out, stderr bytes.Buffer
		c.Stdout = &out
		c.Stderr = &stderr
		e := c.Run()
		if (e == nil) != ok {
			t.Fatalf("args=%v exit=%v stderr=%s", args, e, stderr.String())
		}
		if strings.Contains(stderr.String(), "p27-local-worker") || strings.Contains(stderr.String(), "postgres://") {
			t.Fatal("DSN leaked")
		}
		return out.String()
	}
	first := run(true, "enqueue", fixture)
	if !strings.Contains(first, `"created":true`) {
		t.Fatalf("enqueue output %s", first)
	}
	duplicate := run(true, "enqueue", fixture)
	if !strings.Contains(duplicate, `"created":false`) {
		t.Fatal("duplicate not recognized")
	}
	status := run(true, "status")
	var jobs []json.RawMessage
	if e := json.Unmarshal([]byte(status), &jobs); e != nil || len(jobs) != 1 {
		t.Fatalf("status=%s", status)
	}
	os.WriteFile(fixture, []byte(strings.Replace(raw, "1000", "1001", 1)), 0600)
	run(false, "enqueue", fixture)
	run(false, "unknown")
	if _, e := db.Admin.Exec(context.Background(), "UPDATE worker.jobs SET state='uncertain',ambiguous=true"); e != nil {
		t.Fatal(e)
	}
	var posts atomic.Int32
	for _, mode := range []string{"missing", "unavailable", "mismatch", "matching"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				posts.Add(1)
			}
			switch mode {
			case "missing":
				w.WriteHeader(404)
			case "unavailable":
				w.WriteHeader(503)
			default:
				var hash string
				db.Admin.QueryRow(context.Background(), "SELECT hash FROM worker.jobs").Scan(&hash)
				if mode == "mismatch" {
					hash = "wrong"
				}
				json.NewEncoder(w).Encode(map[string]string{"event_id": "00000000-0000-0000-0000-000000000001", "payload_hash": hash, "receipt_id": "receipt"})
			}
		}))
		receiverURL = server.URL
		run(mode == "matching", "reconcile", "00000000-0000-0000-0000-000000000001")
		server.Close()
		var state string
		db.Admin.QueryRow(context.Background(), "SELECT state FROM worker.jobs").Scan(&state)
		want := "uncertain"
		if mode == "matching" {
			want = "delivered"
		}
		if state != want {
			t.Fatalf("reconcile %s changed state=%s", mode, state)
		}
	}
	if posts.Load() != 0 {
		t.Fatal("reconciliation resent event")
	}
	missing := exec.Command(binary, "status")
	missing.Env = []string{"P27_WORKER_DSN="}
	if e := missing.Run(); e == nil {
		t.Fatal("missing configuration accepted")
	}
}

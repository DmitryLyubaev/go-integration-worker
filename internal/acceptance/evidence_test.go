package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/queue"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureResult(t *testing.T) Result {
	t.Helper()
	root := t.TempDir()
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	r := Result{Root: root, SourceCommit: strings.Repeat("a", 40), Started: base, Finished: base.Add(30 * time.Second), Cleaned: true}
	write := func(path string, value any) {
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(root, path), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := os.ReadFile("../../fixtures/canonical.json")
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "fixture.json"), raw, 0600)
	write("source-manifest.json", map[string]any{"SourceCommit": r.SourceCommit})
	write("compose.yaml", "synthetic config")
	write("p27", "synthetic binary")
	write("source.tar", "synthetic archive")
	var fixture struct {
		Events []domain.Event `json:"events"`
	}
	json.Unmarshal(raw, &fixture)
	// Literal expectations are independent of verifier implementation and runtime observations.
	table := []struct {
		name           string
		first, last    int
		attempts       []int
		posts, effects int
	}{{"A1", 1, 4, []int{1, 1, 1, 1}, 4, 4}, {"A2", 5, 5, []int{1}, 1, 1}, {"A3", 6, 7, []int{2, 1}, 3, 1}, {"A4", 8, 8, []int{3}, 3, 0}, {"A5", 9, 9, []int{2}, 2, 1}, {"A6", 10, 10, []int{2}, 1, 1}, {"A7", 11, 11, []int{2}, 2, 1}, {"A8", 12, 12, []int{3}, 3, 1}}
	pid := 100
	for _, tc := range table {
		snapshot := Snapshot{At: base.Add(20 * time.Second)}
		for n := tc.first; n <= tc.last; n++ {
			event := fixture.Events[n-1]
			raw, _ := json.Marshal(event)
			p, e := domain.Decode(strings.NewReader(string(raw)))
			if e != nil {
				t.Fatal(e)
			}
			state := "delivered"
			if n == 7 || n == 8 {
				state = "failed"
			}
			receipt := domain.Receipt{EventID: event.EventID, PayloadHash: p.Hash, ReceiptID: fmt.Sprintf("receipt%d", n)}
			j := queue.Job{Payload: p, State: state, AttemptCount: tc.attempts[n-tc.first], Traceparent: "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-1111111111111111-01"}
			if state == "delivered" {
				j.Receipt = &receipt
				snapshot.Receipts = append(snapshot.Receipts, receipt)
				snapshot.Effects = append(snapshot.Effects, event)
			}
			snapshot.Jobs = append(snapshot.Jobs, j)
			for a := 1; a <= j.AttemptCount; a++ {
				snapshot.Attempts = append(snapshot.Attempts, Attempt{EventID: event.EventID, Number: a, Token: fmt.Sprint(a), Phase: "reserved"})
				phase := "outcome"
				amb := (tc.name == "A5" || tc.name == "A7" || tc.name == "A8") && a == 1
				if tc.name == "A6" && a == 1 {
					phase = "abandoned"
					amb = true
				}
				snapshot.Attempts = append(snapshot.Attempts, Attempt{EventID: event.EventID, Number: a, Token: fmt.Sprint(a), Phase: phase, Ambiguous: amb})
			}
		}
		c := CaseResult{Name: tc.name, Snapshot: tc.name + "-snapshot.json", ReceiverLog: tc.name + "-receiver.jsonl", Checkpoints: map[string]string{}}
		write(c.Snapshot, snapshot)
		workers := 1
		if tc.name == "A5" || tc.name == "A6" || tc.name == "A8" {
			workers = 2
		}
		for w := 0; w < workers; w++ {
			path := fmt.Sprintf("%s-worker%d.jsonl", tc.name, w)
			c.WorkerLogs = append(c.WorkerLogs, path)
			posts := tc.posts
			if workers == 2 {
				posts = 0
				if tc.name == "A5" {
					posts = 1
				}
				if tc.name == "A6" && w == 1 {
					posts = 1
				}
				if tc.name == "A8" && w == 0 {
					posts = 3
				}
			}
			var lines []string
			for range posts {
				lines = append(lines, `{"msg":"worker.send"}`)
			}
			lines = append(lines, `{"msg":"worker.stop","goroutines":4,"joined":true}`)
			os.WriteFile(filepath.Join(root, path), []byte(strings.Join(lines, "\n")+"\n"), 0600)
			pid++
			process := ProcessResult{Case: tc.name, Role: "worker", PID: pid, Started: base, Stopped: base.Add(5 * time.Second), Signal: "TERM", ShutdownSeconds: 1}
			if tc.name == "A5" && w == 0 {
				process.Stopped = base.Add(time.Second)
			}
			if tc.name == "A6" && w == 0 {
				process.Signal = "KILL"
				process.ExitCode = -1
				process.Stopped = base.Add(time.Second)
			}
			r.Processes = append(r.Processes, process)
		}
		var records []string
		requests := tc.posts
		if tc.name == "A2" {
			requests += 3
		}
		for range requests {
			records = append(records, `{"msg":"receiver.request","inflight":2}`)
		}
		commitTime := base.Add(2 * time.Second)
		if tc.name == "A6" {
			commitTime = base.Add(11 * time.Second)
		}
		for range tc.effects {
			record, _ := json.Marshal(map[string]any{"msg": "receiver.commit", "time": commitTime})
			records = append(records, string(record))
		}
		if tc.name == "A7" {
			records = append(records, `{"msg":"receiver.drop"}`)
		}
		if tc.name == "A8" {
			records = append(records, `{"msg":"receiver.drop"}`, `{"msg":"receiver.drop"}`, `{"msg":"receiver.drop"}`, `{"msg":"receiver.lookup"}`)
		}
		os.WriteFile(filepath.Join(root, c.ReceiverLog), []byte(strings.Join(records, "\n")+"\n"), 0600)
		pid++
		r.Processes = append(r.Processes, ProcessResult{Case: tc.name, Role: "receiver", PID: pid, Started: base, Stopped: base.Add(25 * time.Second), Signal: "TERM", ShutdownSeconds: 1})
		if tc.name == "A5" {
			checkpoint := snapshot
			checkpoint.Jobs = append([]queue.Job(nil), snapshot.Jobs...)
			checkpoint.Jobs[0].State = "processing"
			checkpoint.Jobs[0].AttemptCount = 1
			checkpoint.Effects = nil
			checkpoint.Receipts = nil
			for _, key := range []string{"held", "stopped"} {
				path := tc.name + "-" + key + ".json"
				write(path, checkpoint)
				c.Checkpoints[key] = path
			}
		}
		if tc.name == "A6" {
			checkpoint := snapshot
			checkpoint.Jobs = append([]queue.Job(nil), snapshot.Jobs...)
			checkpoint.Jobs[0].State = "processing"
			checkpoint.Jobs[0].AttemptCount = 1
			checkpoint.Jobs[0].LeaseUntil = base.Add(10 * time.Second)
			checkpoint.Effects = nil
			checkpoint.Receipts = nil
			path := "A6-claimed.json"
			write(path, checkpoint)
			c.Checkpoints["claimed"] = path
		}
		if tc.name == "A8" {
			checkpoint := snapshot
			checkpoint.Jobs = append([]queue.Job(nil), snapshot.Jobs...)
			checkpoint.Jobs[0].State = "uncertain"
			checkpoint.Jobs[0].Receipt = nil
			for _, key := range []string{"uncertain", "restarted"} {
				path := "A8-" + key + ".json"
				write(path, checkpoint)
				c.Checkpoints[key] = path
			}
		}
		r.Cases = append(r.Cases, c)
	}
	r.TraceFiles = []string{"trace.jsonl"}
	trace := `{"Name":"enqueue","SpanContext":{"TraceID":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","SpanID":"1111111111111111"},"Parent":{"SpanID":"0000000000000000"}}` + "\n" + `{"Name":"attempt","SpanContext":{"TraceID":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","SpanID":"2222222222222222"},"Parent":{"SpanID":"1111111111111111"}}` + "\n" + `{"Name":"receiver.export","SpanContext":{"TraceID":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","SpanID":"3333333333333333"},"Parent":{"SpanID":"2222222222222222"}}` + "\n"
	os.WriteFile(filepath.Join(root, "trace.jsonl"), []byte(trace), 0600)
	hash := func(path string) string {
		b, e := os.ReadFile(filepath.Join(root, path))
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	r.SourceTreeHash = hash("source-manifest.json")
	r.FixtureHash = hash("fixture.json")
	r.BinaryHash = hash("p27")
	r.ConfigHash = hash("compose.yaml")
	files, _ := os.ReadDir(root)
	for _, file := range files {
		r.Inventory = append(r.Inventory, FileEntry{file.Name(), hash(file.Name())})
	}
	return r
}
func TestVerifyRejectsMissingAndChangedEvidence(t *testing.T) {
	r := fixtureResult(t)
	if e := Verify(r); e != nil {
		t.Fatalf("valid software-only fixture rejected: %v", e)
	}
	for _, kind := range []string{"missing-case", "process-exit", "changed-fixture", "missing-trace", "missing-cleanup", "changed-file", "changed-effect"} {
		t.Run(kind, func(t *testing.T) {
			candidate := fixtureResult(t)
			switch kind {
			case "missing-case":
				candidate.Cases = candidate.Cases[:7]
			case "process-exit":
				candidate.Processes[0].Stopped = time.Time{}
			case "changed-fixture":
				candidate.FixtureHash = strings.Repeat("0", 64)
			case "missing-trace":
				candidate.TraceFiles = nil
			case "missing-cleanup":
				candidate.Cleaned = false
			case "changed-file":
				os.WriteFile(filepath.Join(candidate.Root, candidate.Cases[0].Snapshot), []byte("{}"), 0600)
			case "changed-effect":
				path := candidate.Cases[0].Snapshot
				raw, _ := os.ReadFile(filepath.Join(candidate.Root, path))
				var snapshot Snapshot
				json.Unmarshal(raw, &snapshot)
				snapshot.Effects = snapshot.Effects[:3]
				raw, _ = json.Marshal(snapshot)
				os.WriteFile(filepath.Join(candidate.Root, path), raw, 0600)
				sum := sha256.Sum256(raw)
				for i := range candidate.Inventory {
					if candidate.Inventory[i].Path == path {
						candidate.Inventory[i].SHA256 = hex.EncodeToString(sum[:])
					}
				}
			}
			if e := Verify(candidate); e == nil {
				t.Fatal("missing/altered evidence accepted")
			}
		})
	}
}

package acceptance

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type expectation struct {
	first, last, reservations, posts, effects int
	attempts                                  []int
}

var expected = map[string]expectation{"A1": {1, 4, 4, 4, 4, []int{1, 1, 1, 1}}, "A2": {5, 5, 1, 1, 1, []int{1}}, "A3": {6, 7, 3, 3, 1, []int{2, 1}}, "A4": {8, 8, 3, 3, 0, []int{3}}, "A5": {9, 9, 2, 2, 1, []int{2}}, "A6": {10, 10, 2, 1, 1, []int{2}}, "A7": {11, 11, 2, 2, 1, []int{2}}, "A8": {12, 12, 3, 3, 1, []int{3}}}

type logRecord struct {
	Message              string    `json:"msg"`
	Time                 time.Time `json:"time"`
	EventID              string    `json:"event_id"`
	Inflight, Goroutines int
	Joined               bool
}

func hashBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func safePath(root, path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return "", errors.New("invalid evidence path")
	}
	target := filepath.Join(root, path)
	relative, e := filepath.Rel(root, target)
	if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("evidence escapes root")
	}
	return target, nil
}
func readJSON(root, path string, value any) error {
	full, e := safePath(root, path)
	if e != nil {
		return e
	}
	raw, e := os.ReadFile(full)
	if e != nil {
		return e
	}
	return json.Unmarshal(raw, value)
}
func logs(root, path string) ([]logRecord, error) {
	full, e := safePath(root, path)
	if e != nil {
		return nil, e
	}
	f, e := os.Open(full)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	var records []logRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var record logRecord
		if e = json.Unmarshal(scanner.Bytes(), &record); e != nil {
			return nil, e
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}
func count(records []logRecord, message string) int {
	n := 0
	for _, r := range records {
		if r.Message == message {
			n++
		}
	}
	return n
}
func Verify(r Result) error {
	if r.Failure != "" || !r.Cleaned || r.Started.IsZero() || r.Finished.Before(r.Started) || r.Finished.Sub(r.Started) > 300*time.Second || len(r.Cases) != 8 || len(r.SourceCommit) != 40 {
		return errors.New("incomplete/over-bound canonical result")
	}
	if _, e := hex.DecodeString(r.SourceCommit); e != nil {
		return e
	}
	inventory := map[string]string{}
	for _, entry := range r.Inventory {
		if _, exists := inventory[entry.Path]; exists {
			return errors.New("duplicate inventory entry")
		}
		full, e := safePath(r.Root, entry.Path)
		if e != nil {
			return e
		}
		raw, e := os.ReadFile(full)
		if e != nil {
			return e
		}
		if hashBytes(raw) != entry.SHA256 {
			return fmt.Errorf("evidence hash mismatch: %s", entry.Path)
		}
		inventory[entry.Path] = entry.SHA256
	}
	for path, want := range map[string]string{"fixture.json": r.FixtureHash, "source-manifest.json": r.SourceTreeHash, "p27": r.BinaryHash, "compose.yaml": r.ConfigHash} {
		if len(want) != 64 || inventory[path] != want {
			return fmt.Errorf("missing/mismatched source binding %s", path)
		}
	}
	if inventory["source.tar"] == "" {
		return errors.New("source archive missing")
	}
	var source struct{ SourceCommit string }
	if e := readJSON(r.Root, "source-manifest.json", &source); e != nil || source.SourceCommit != r.SourceCommit {
		return errors.New("source manifest identity mismatch")
	}
	var fixture struct {
		Events []domain.Event `json:"events"`
	}
	if e := readJSON(r.Root, "fixture.json", &fixture); e != nil || len(fixture.Events) != 12 {
		return errors.New("fixture must contain twelve events")
	}
	payloads := map[string]domain.Payload{}
	for i, event := range fixture.Events {
		if event.EventID != fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1) || event.AmountMinor != 1000 || event.Currency != "AUD" {
			return errors.New("frozen event identity/value changed")
		}
		raw, _ := json.Marshal(event)
		p, e := domain.Decode(strings.NewReader(string(raw)))
		if e != nil {
			return e
		}
		payloads[event.EventID] = p
	}
	seen := map[string]bool{}
	var traceParent string
	for _, c := range r.Cases {
		want, ok := expected[c.Name]
		if !ok || seen[c.Name] {
			return errors.New("unknown/duplicate acceptance group")
		}
		seen[c.Name] = true
		if inventory[c.Snapshot] == "" || inventory[c.ReceiverLog] == "" || len(c.WorkerLogs) == 0 {
			return errors.New("missing case evidence")
		}
		var snapshot Snapshot
		if e := readJSON(r.Root, c.Snapshot, &snapshot); e != nil {
			return e
		}
		if len(snapshot.Jobs) != want.last-want.first+1 || len(snapshot.Effects) != want.effects || len(snapshot.Receipts) != want.effects {
			return fmt.Errorf("%s SQL state/effect/receipt counts differ", c.Name)
		}
		reservations := 0
		for _, attempt := range snapshot.Attempts {
			if attempt.Phase == "reserved" {
				reservations++
			}
		}
		if reservations != want.reservations {
			return fmt.Errorf("%s reservations=%d", c.Name, reservations)
		}
		for i, job := range snapshot.Jobs {
			id := fmt.Sprintf("00000000-0000-0000-0000-%012d", want.first+i)
			if job.Payload.Event.EventID != id || job.Payload.Hash != payloads[id].Hash || string(job.Payload.Bytes) != string(payloads[id].Bytes) || job.AttemptCount != want.attempts[i] {
				return fmt.Errorf("%s job identity/attempt mismatch", c.Name)
			}
			state := "delivered"
			if id == fixture.Events[6].EventID || id == fixture.Events[7].EventID {
				state = "failed"
			}
			if job.State != state {
				return fmt.Errorf("%s unexpected final state %s", c.Name, job.State)
			}
			if state == "delivered" && (job.Receipt == nil || job.Receipt.EventID != id || job.Receipt.PayloadHash != job.Payload.Hash || job.Receipt.ReceiptID == "") {
				return errors.New("delivered without matching receipt")
			}
			for number := 1; number <= job.AttemptCount; number++ {
				n := 0
				for _, a := range snapshot.Attempts {
					if a.EventID == id && a.Number == number && a.Phase == "reserved" {
						n++
					}
				}
				if n != 1 {
					return errors.New("reservation inventory mismatch")
				}
			}
			if c.Name == "A1" && i == 0 {
				traceParent = job.Traceparent
			}
		}
		for _, effect := range snapshot.Effects {
			p, exists := payloads[effect.EventID]
			if !exists || effect != p.Event {
				return errors.New("receiver effect differs from frozen event")
			}
			matched := false
			for _, receipt := range snapshot.Receipts {
				if receipt.EventID == effect.EventID && receipt.PayloadHash == p.Hash && receipt.ReceiptID != "" {
					for _, job := range snapshot.Jobs {
						if job.Receipt != nil && *job.Receipt == receipt {
							matched = true
						}
					}
				}
			}
			if !matched {
				return errors.New("effect/receipt/job identity mismatch")
			}
		}
		receiverRecords, e := logs(r.Root, c.ReceiverLog)
		if e != nil {
			return e
		}
		posts := 0
		for _, path := range c.WorkerLogs {
			if inventory[path] == "" {
				return errors.New("worker log absent from inventory")
			}
			records, e := logs(r.Root, path)
			if e != nil {
				return e
			}
			posts += count(records, "worker.send")
		}
		if posts != want.posts {
			return fmt.Errorf("%s worker sends=%d", c.Name, posts)
		}
		probes := 0
		if c.Name == "A2" {
			probes = 3
		}
		if count(receiverRecords, "receiver.request") != posts+probes {
			return fmt.Errorf("%s independent HTTP count mismatch", c.Name)
		}
		lookups := 0
		if c.Name == "A8" {
			lookups = 1
		}
		if count(receiverRecords, "receiver.lookup") != lookups {
			return errors.New("unexpected receipt lookup count")
		}
		var workers []ProcessResult
		receivers := 0
		for _, p := range r.Processes {
			if p.Case != c.Name {
				continue
			}
			if p.PID <= 0 || p.Stopped.IsZero() || p.Stopped.Before(p.Started) {
				return errors.New("process exit observation missing")
			}
			if p.Signal == "KILL" {
				if c.Name != "A6" || p.Role != "worker" || p.ExitCode != -1 {
					return errors.New("unexpected killed process")
				}
			} else if p.ExitCode != 0 || p.ShutdownSeconds > 5 {
				return errors.New("process shutdown failed or exceeded bound")
			}
			if p.Role == "worker" {
				workers = append(workers, p)
			} else if p.Role == "receiver" {
				receivers++
			}
		}
		minimumWorkers := 1
		if c.Name == "A5" || c.Name == "A6" || c.Name == "A8" {
			minimumWorkers = 2
		}
		if len(workers) != minimumWorkers || len(c.WorkerLogs) != minimumWorkers || receivers != 1 {
			return errors.New("process/restart inventory incomplete")
		}
		for key, path := range c.Checkpoints {
			if inventory[path] == "" {
				return fmt.Errorf("missing checkpoint %s", key)
			}
		}
		checkpoint := func(key string) (Snapshot, error) {
			var s Snapshot
			path := c.Checkpoints[key]
			if inventory[path] == "" {
				return s, errors.New("required checkpoint absent")
			}
			e := readJSON(r.Root, path, &s)
			return s, e
		}
		switch c.Name {
		case "A1":
			maximum := 0
			for _, record := range receiverRecords {
				if record.Message == "receiver.request" && record.Inflight > maximum {
					maximum = record.Inflight
				}
			}
			if maximum != 2 {
				return errors.New("maximum in-flight work was not exactly two")
			}
		case "A5":
			for _, key := range []string{"held", "stopped"} {
				s, e := checkpoint(key)
				if e != nil || len(s.Effects) != 0 || len(s.Jobs) != 1 || s.Jobs[0].AttemptCount != 1 {
					return errors.New("cancellation checkpoints invalid")
				}
			}
			committedLate := false
			for _, record := range receiverRecords {
				if record.Message == "receiver.commit" && record.Time.After(workers[0].Stopped) {
					committedLate = true
				}
			}
			if !committedLate {
				return errors.New("late commit after worker exit unproven")
			}
		case "A6":
			s, e := checkpoint("claimed")
			if e != nil || len(s.Jobs) != 1 || s.Jobs[0].State != "processing" || s.Jobs[0].AttemptCount != 1 || len(s.Effects) != 0 || workers[0].Signal != "KILL" || !workers[0].Stopped.Before(s.Jobs[0].LeaseUntil) {
				return errors.New("actual pre-send kill/claim proof missing")
			}
			committedAfterExpiry := false
			for _, record := range receiverRecords {
				if record.Message == "receiver.commit" && !record.Time.Before(s.Jobs[0].LeaseUntil) {
					committedAfterExpiry = true
				}
			}
			if !committedAfterExpiry {
				return errors.New("genuine lease-expiry recovery unproven")
			}
			records, e := logs(r.Root, c.WorkerLogs[0])
			if e != nil || count(records, "worker.send") != 0 {
				return errors.New("pre-send kill already sent")
			}
		case "A7":
			if count(receiverRecords, "receiver.drop") != 1 {
				return errors.New("lost-response observation missing")
			}
			amb := false
			for _, a := range snapshot.Attempts {
				if a.Number == 1 && a.Phase == "outcome" && a.Ambiguous {
					amb = true
				}
			}
			if !amb {
				return errors.New("transport ambiguity was not recorded")
			}
		case "A8":
			if count(receiverRecords, "receiver.drop") != 3 {
				return errors.New("three lost responses unproven")
			}
			for _, key := range []string{"uncertain", "restarted"} {
				s, e := checkpoint(key)
				if e != nil || len(s.Jobs) != 1 || s.Jobs[0].State != "uncertain" || s.Jobs[0].AttemptCount != 3 || s.Jobs[0].Receipt != nil {
					return errors.New("terminal ambiguity/restart checkpoint invalid")
				}
			}
			records, e := logs(r.Root, c.WorkerLogs[1])
			if e != nil || count(records, "worker.send") != 0 {
				return errors.New("restart sent beyond budget")
			}
		}
	}
	if !linkedTrace(r, inventory, traceParent) {
		return errors.New("required enqueue/attempt/receiver trace chain missing")
	}
	return nil
}
func linkedTrace(r Result, inventory map[string]string, parent string) bool {
	parts := strings.Split(parent, "-")
	if len(parts) != 4 || len(r.TraceFiles) == 0 {
		return false
	}
	type span struct {
		Name        string
		SpanContext struct{ TraceID, SpanID string }
		Parent      struct{ SpanID string }
	}
	var spans []span
	for _, path := range r.TraceFiles {
		if inventory[path] == "" {
			return false
		}
		full, e := safePath(r.Root, path)
		if e != nil {
			return false
		}
		f, e := os.Open(full)
		if e != nil {
			return false
		}
		decoder := json.NewDecoder(f)
		for decoder.More() {
			var value span
			if e = decoder.Decode(&value); e != nil {
				f.Close()
				return false
			}
			spans = append(spans, value)
		}
		f.Close()
	}
	enqueue := false
	attempts := map[string]bool{}
	for _, s := range spans {
		if s.SpanContext.TraceID != parts[1] {
			continue
		}
		if s.Name == "enqueue" && s.SpanContext.SpanID == parts[2] {
			enqueue = true
		}
		if s.Name == "attempt" && s.Parent.SpanID == parts[2] {
			attempts[s.SpanContext.SpanID] = true
		}
	}
	if !enqueue {
		return false
	}
	for _, s := range spans {
		if s.Name == "receiver.export" && s.SpanContext.TraceID == parts[1] && attempts[s.Parent.SpanID] {
			return true
		}
	}
	return false
}

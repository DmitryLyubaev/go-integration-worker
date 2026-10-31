package acceptance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/receiver"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func (s *runner) scenario(name string) error {
	c := CaseResult{Name: name, Checkpoints: map[string]string{}}
	s.result.Cases = append(s.result.Cases, c)
	index := len(s.result.Cases) - 1
	defer func() { s.result.Cases[index] = c }()
	ids := caseIDs(name)
	rules := map[string]receiver.Fault{}
	gate := filepath.Join(s.cfg.Output, name+"-gate")
	switch name {
	case "A1":
		for _, id := range ids {
			rules[id] = receiver.Fault{GatePath: gate}
		}
	case "A3":
		rules[ids[0]] = receiver.Fault{Status: 503, Failures: 1}
		rules[ids[1]] = receiver.Fault{Status: 422, Failures: 1}
	case "A4":
		rules[ids[0]] = receiver.Fault{Status: 503, Failures: 3}
	case "A5":
		rules[ids[0]] = receiver.Fault{GatePath: gate}
	case "A7":
		rules[ids[0]] = receiver.Fault{DropResponses: 1}
	case "A8":
		rules[ids[0]] = receiver.Fault{DropResponses: -1}
	}
	receiverProcess, e := s.startReceiver(name, rules)
	if e != nil {
		return e
	}
	c.ReceiverLog = receiverProcess.log
	for i, id := range ids {
		if _, e = s.enqueue(name, fmt.Sprintf("enqueue%d", i+1), id, false); e != nil {
			return e
		}
	}
	if name == "A2" {
		raw, e := s.enqueue(name, "duplicate", ids[0], false)
		if e != nil || !bytes.Contains(raw, []byte(`"created":false`)) {
			return errors.New("exact duplicate enqueue did not return original")
		}
		if _, e = s.enqueue(name, "conflict", ids[0], true); e == nil {
			return errors.New("conflicting enqueue accepted")
		} else {
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 1 {
				return errors.New("conflicting enqueue failed for unexpected reason")
			}
		}
	}
	claimGate := ""
	if name == "A6" {
		claimGate = gate
	}
	worker, e := s.startWorker(name, "worker1", claimGate)
	if e != nil {
		return e
	}
	c.WorkerLogs = append(c.WorkerLogs, worker.log)
	if name == "A1" {
		if e = s.waitUntil(func() bool {
			records, e := logs(s.cfg.Output, receiverProcess.log)
			return e == nil && count(records, "receiver.request") == 2
		}); e != nil {
			return e
		}
		if e = os.WriteFile(gate+".release", []byte("release"), 0600); e != nil {
			return e
		}
	}
	if name == "A5" {
		if e = s.waitUntil(func() bool { _, e := os.Stat(gate + ".entered"); return e == nil }); e != nil {
			return e
		}
		c.Checkpoints["held"], _, e = s.snapshot(name, "held")
		if e != nil {
			return e
		}
		if e = worker.stop("TERM"); e != nil {
			return e
		}
		c.Checkpoints["stopped"], _, e = s.snapshot(name, "stopped")
		if e != nil {
			return e
		}
		if e = os.WriteFile(gate+".release", []byte("release"), 0600); e != nil {
			return e
		}
		if e = s.waitUntil(func() bool {
			var n int
			return s.db.QueryRow(s.ctx, "SELECT count(*) FROM receiver.effects WHERE event_id=$1", ids[0]).Scan(&n) == nil && n == 1
		}); e != nil {
			return e
		}
		worker, e = s.startWorker(name, "worker2", "")
		if e != nil {
			return e
		}
		c.WorkerLogs = append(c.WorkerLogs, worker.log)
	}
	if name == "A6" {
		if e = s.waitUntil(func() bool { _, e := os.Stat(gate + ".claim"); return e == nil }); e != nil {
			return e
		}
		c.Checkpoints["claimed"], _, e = s.snapshot(name, "claimed")
		if e != nil {
			return e
		}
		if e = worker.stop("KILL"); e != nil {
			return e
		}
		worker, e = s.startWorker(name, "worker2", "")
		if e != nil {
			return e
		}
		c.WorkerLogs = append(c.WorkerLogs, worker.log)
	}
	states := map[string]string{}
	for _, id := range ids {
		state := "delivered"
		if name == "A4" || (name == "A3" && id == ids[1]) {
			state = "failed"
		}
		if name == "A8" {
			state = "uncertain"
		}
		states[id] = state
	}
	if e = s.waitStates(states); e != nil {
		return e
	}
	if e = worker.stop("TERM"); e != nil {
		return e
	}
	if name == "A2" {
		_, snapshot, e := s.snapshot(name, "before-probes")
		if e != nil || len(snapshot.Receipts) != 1 {
			return errors.New("original receiver receipt missing")
		}
		receipt := snapshot.Receipts[0]
		var wg sync.WaitGroup
		failures := make(chan error, 2)
		for ordinal := 1; ordinal <= 2; ordinal++ {
			wg.Add(1)
			go func(n int) { defer wg.Done(); failures <- s.probe(name, ids[0], n, false, receipt) }(ordinal)
		}
		wg.Wait()
		close(failures)
		for e := range failures {
			if e != nil {
				return e
			}
		}
		if e = s.probe(name, ids[0], 3, true, receipt); e != nil {
			return e
		}
	}
	if name == "A8" {
		c.Checkpoints["uncertain"], _, e = s.snapshot(name, "uncertain")
		if e != nil {
			return e
		}
		worker, e = s.startWorker(name, "worker2", "")
		if e != nil {
			return e
		}
		c.WorkerLogs = append(c.WorkerLogs, worker.log)
		select {
		case <-time.After(500 * time.Millisecond):
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
		c.Checkpoints["restarted"], _, e = s.snapshot(name, "restarted")
		if e != nil {
			return e
		}
		if e = worker.stop("TERM"); e != nil {
			return e
		}
		if _, e = s.command(name+"-reconcile", "reconcile", ids[0]); e != nil {
			return e
		}
		if e = s.waitStates(map[string]string{ids[0]: "delivered"}); e != nil {
			return e
		}
	}
	c.Snapshot, _, e = s.snapshot(name, "final")
	if e != nil {
		return e
	}
	if e = receiverProcess.stop("TERM"); e != nil {
		return e
	}
	fmt.Printf("%s runtime observations captured\n", name)
	return nil
}
func (s *runner) enqueue(group, label, id string, conflict bool) ([]byte, error) {
	raw := s.events[id]
	if conflict {
		raw = bytes.Replace(raw, []byte(`"amount_minor":1000`), []byte(`"amount_minor":1001`), 1)
	}
	path := filepath.Join(s.cfg.Output, group+"-"+label+".event.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		return nil, e
	}
	return s.command(group+"-"+label, "enqueue", path)
}
func (s *runner) startReceiver(group string, rules map[string]receiver.Fault) (*child, error) {
	raw, _ := json.Marshal(rules)
	path := filepath.Join(s.cfg.Output, group+"-faults.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		return nil, e
	}
	child, e := s.start("receiver", group, "receiver", []string{"P27_TEST_MODE=1", "P27_FAULTS=" + path}, "receiver")
	if e != nil {
		return nil, e
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	defer client.CloseIdleConnections()
	e = s.waitUntil(func() bool {
		response, e := client.Get("http://127.0.0.1:8127/ready")
		if e != nil {
			return false
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 4097))
		return response.StatusCode == 200 && strings.Contains(string(raw), `"protocol":"p27-receiver-v1"`)
	})
	return child, e
}
func (s *runner) startWorker(group, name, gate string) (*child, error) {
	var env []string
	if gate != "" {
		env = []string{"P27_TEST_MODE=1", "P27_CLAIM_GATE=" + gate}
	}
	return s.start("worker", group, name, env, "worker")
}
func (s *runner) probe(group, id string, ordinal int, conflict bool, original domain.Receipt) error {
	raw := s.events[id]
	want := 200
	if conflict {
		raw = bytes.Replace(raw, []byte(`"amount_minor":1000`), []byte(`"amount_minor":1001`), 1)
		want = 409
	}
	request, e := http.NewRequestWithContext(s.ctx, "POST", "http://127.0.0.1:8127/exports", bytes.NewReader(raw))
	if e != nil {
		return e
	}
	request.GetBody = nil
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, e := client.Do(request)
	if e != nil {
		return e
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, 4097))
	response.Body.Close()
	if e != nil || response.StatusCode != want {
		return fmt.Errorf("duplicate/conflicting probe status %d", response.StatusCode)
	}
	if !conflict {
		var receipt domain.Receipt
		if json.Unmarshal(body, &receipt) != nil || receipt != original {
			return errors.New("duplicate probe receipt changed")
		}
	}
	record, _ := json.Marshal(map[string]any{"status": response.StatusCode, "body": string(body)})
	return os.WriteFile(filepath.Join(s.cfg.Output, fmt.Sprintf("%s-probe%d.json", group, ordinal)), record, 0600)
}

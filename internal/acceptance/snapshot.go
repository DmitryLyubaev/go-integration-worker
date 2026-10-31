package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/queue"
	"os"
	"path/filepath"
	"time"
)

func caseIDs(name string) []string {
	e := expected[name]
	var ids []string
	for n := e.first; n <= e.last; n++ {
		ids = append(ids, fmt.Sprintf("00000000-0000-0000-0000-%012d", n))
	}
	return ids
}
func (s *runner) snapshot(name, label string) (string, Snapshot, error) {
	ctx, c := context.WithTimeout(s.ctx, 2*time.Second)
	defer c()
	snapshot := Snapshot{At: time.Now().UTC()}
	ids := caseIDs(name)
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	jobs, e := queue.New(s.db).Status(ctx, "")
	if e != nil {
		return "", snapshot, e
	}
	for _, j := range jobs {
		if wanted[j.Payload.Event.EventID] {
			snapshot.Jobs = append(snapshot.Jobs, j)
		}
	}
	rows, e := s.db.Query(ctx, "SELECT event_id,number,token,phase,reason,ambiguous,recorded_at FROM worker.attempts WHERE event_id=ANY($1) ORDER BY event_id,number,recorded_at", ids)
	if e != nil {
		return "", snapshot, e
	}
	for rows.Next() {
		var a Attempt
		if e = rows.Scan(&a.EventID, &a.Number, &a.Token, &a.Phase, &a.Reason, &a.Ambiguous, &a.At); e != nil {
			rows.Close()
			return "", snapshot, e
		}
		snapshot.Attempts = append(snapshot.Attempts, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return "", snapshot, e
	}
	rows, e = s.db.Query(ctx, "SELECT event_id,hash,receipt_id FROM receiver.receipts WHERE event_id=ANY($1) ORDER BY event_id", ids)
	if e != nil {
		return "", snapshot, e
	}
	for rows.Next() {
		var r domain.Receipt
		if e = rows.Scan(&r.EventID, &r.PayloadHash, &r.ReceiptID); e != nil {
			rows.Close()
			return "", snapshot, e
		}
		snapshot.Receipts = append(snapshot.Receipts, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return "", snapshot, e
	}
	rows, e = s.db.Query(ctx, "SELECT event_id,sale_reference,currency,amount_minor FROM receiver.effects WHERE event_id=ANY($1) ORDER BY event_id", ids)
	if e != nil {
		return "", snapshot, e
	}
	for rows.Next() {
		var event domain.Event
		if e = rows.Scan(&event.EventID, &event.SaleReference, &event.Currency, &event.AmountMinor); e != nil {
			rows.Close()
			return "", snapshot, e
		}
		snapshot.Effects = append(snapshot.Effects, event)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return "", snapshot, e
	}
	path := name + "-" + label + ".json"
	raw, e := json.MarshalIndent(snapshot, "", "  ")
	if e != nil {
		return "", snapshot, e
	}
	e = os.WriteFile(filepath.Join(s.cfg.Output, path), raw, 0600)
	return path, snapshot, e
}
func (s *runner) waitUntil(condition func() bool) error {
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return nil
		}
		for _, child := range s.children {
			select {
			case <-child.done:
				if !child.requested {
					return fmt.Errorf("owned %s exited unexpectedly (%d)", child.info.Role, child.info.ExitCode)
				}
			default:
			}
		}
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-ticker.C:
		}
	}
}
func (s *runner) waitStates(states map[string]string) error {
	return s.waitUntil(func() bool {
		jobs, e := queue.New(s.db).Status(s.ctx, "")
		if e != nil {
			return false
		}
		matched := 0
		for _, j := range jobs {
			if want, ok := states[j.Payload.Event.EventID]; ok && j.State == want {
				matched++
			}
		}
		return matched == len(states)
	})
}

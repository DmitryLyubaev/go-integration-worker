package receiver

import (
	"context"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/testutil"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T) domain.Payload {
	t.Helper()
	p, e := domain.Decode(strings.NewReader(`{"event_id":"00000000-0000-0000-0000-000000000001","sale_reference":"receiver_1","currency":"AUD","amount_minor":1000}`))
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestConcurrentApplyRealDB(t *testing.T) {
	db := testutil.PostgreSQL(t)
	s := NewStore(db.Receiver)
	ctx := context.Background()
	p := fixture(t)
	receipts := make(chan domain.Receipt, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _, e := s.Apply(ctx, p)
			if e != nil {
				t.Error(e)
				return
			}
			receipts <- r
		}()
	}
	wg.Wait()
	close(receipts)
	var first domain.Receipt
	for r := range receipts {
		if first.ReceiptID == "" {
			first = r
		}
		if r != first || r.PayloadHash != p.Hash || r.ReceiptID == "" {
			t.Fatal("receipt diverged")
		}
	}
	var n int
	db.Admin.QueryRow(ctx, "SELECT count(*) FROM receiver.effects").Scan(&n)
	if n != 1 {
		t.Fatalf("effects=%d", n)
	}
	conflict, _ := domain.Decode(strings.NewReader(strings.Replace(string(p.Bytes), "1000", "1001", 1)))
	if _, _, e := s.Apply(ctx, conflict); e != ErrConflict {
		t.Fatalf("conflicting reuse=%v", e)
	}
	got, e := s.Receipt(ctx, p.Event.EventID)
	if e != nil || got != first {
		t.Fatalf("receipt lookup %v", e)
	}
	if _, e = db.Receiver.Exec(ctx, "UPDATE worker.jobs SET state='delivered'"); e == nil {
		t.Fatal("receiver can write worker schema")
	}
}
func TestApplyAtomicRollback(t *testing.T) {
	db := testutil.PostgreSQL(t)
	s := NewStore(db.Receiver)
	ctx := context.Background()
	healthy, _ := domain.Decode(strings.NewReader(strings.Replace(string(fixture(t).Bytes), "1000", "1001", 1)))
	if _, _, e := s.Apply(ctx, healthy); e != nil {
		t.Fatal(e)
	}
	_, e := db.Admin.Exec(ctx, "ALTER TABLE receiver.effects ADD CONSTRAINT test_failure CHECK (amount_minor <> 1000)")
	if e != nil {
		t.Fatal(e)
	}
	different, _ := domain.Decode(strings.NewReader(strings.Replace(string(fixture(t).Bytes), "000000000001", "000000000002", 1)))
	if _, _, e = s.Apply(ctx, different); e == nil {
		t.Fatal("forced insert failure not propagated")
	}
	var receipts, effects int
	db.Admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM receiver.receipts),(SELECT count(*) FROM receiver.effects)").Scan(&receipts, &effects)
	if receipts != 1 || effects != 1 {
		t.Fatalf("non-atomic receipt/effect: %d/%d", receipts, effects)
	}
}

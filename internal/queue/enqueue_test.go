package queue

import (
	"context"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/testutil"
	"strings"
	"sync"
	"testing"
)

func payload(t *testing.T) domain.Payload {
	t.Helper()
	p, e := domain.Decode(strings.NewReader(`{"event_id":"00000000-0000-0000-0000-000000000001","sale_reference":"test_1","currency":"AUD","amount_minor":1000}`))
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestEnqueueIdentityRealDB(t *testing.T) {
	db := testutil.PostgreSQL(t)
	s := New(db.Worker)
	ctx := context.Background()
	p := payload(t)
	job, created, e := s.Enqueue(ctx, p)
	if e != nil || !created || job.State != "queued" {
		t.Fatalf("enqueue created=%v state=%s error=%v", created, job.State, e)
	}
	again, created, e := s.Enqueue(ctx, p)
	if e != nil || created || again.Payload.Hash != p.Hash {
		t.Fatalf("duplicate changed identity %v", e)
	}
	bad, _ := domain.Decode(strings.NewReader(strings.Replace(string(p.Bytes), "1000", "1001", 1)))
	if _, _, e = s.Enqueue(ctx, bad); e != ErrConflict {
		t.Fatalf("conflict=%v", e)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, created, e := s.Enqueue(ctx, p); e != nil || created {
				t.Errorf("concurrent duplicate created=%v err=%v", created, e)
			}
		}()
	}
	wg.Wait()
	var count int
	var raw []byte
	if e = db.Admin.QueryRow(ctx, "SELECT count(*) FROM worker.jobs").Scan(&count); e != nil || count != 1 {
		t.Fatalf("jobs=%d error=%v", count, e)
	}
	db.Admin.QueryRow(ctx, "SELECT payload FROM worker.jobs").Scan(&raw)
	if string(raw) != string(p.Bytes) {
		t.Fatal("original overwritten")
	}
	if _, e = db.Worker.Exec(ctx, "INSERT INTO receiver.receipts VALUES ('x','y','z')"); e == nil {
		t.Fatal("worker can write receiver data")
	}
}

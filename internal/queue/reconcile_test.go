package queue

import (
	"context"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/testutil"
	"testing"
)

func TestReconcileReadOnlyAndRaceRealDB(t *testing.T) {
	db := testutil.PostgreSQL(t)
	s := New(db.Worker)
	ctx := context.Background()
	p := payload(t)
	s.Enqueue(ctx, p)
	claim, e := s.ClaimDue(ctx)
	if e != nil {
		t.Fatal(e)
	}
	s.Complete(ctx, *claim, domain.Outcome{Kind: domain.Unrecognized, Ambiguous: true})
	var before int
	db.Admin.QueryRow(ctx, "SELECT count(*) FROM worker.attempts").Scan(&before)
	bad := domain.Receipt{EventID: p.Event.EventID, PayloadHash: "bad", ReceiptID: "receipt"}
	if e = s.Reconcile(ctx, p.Event.EventID, bad); e == nil {
		t.Fatal("mismatch accepted")
	}
	jobs, e := s.Status(ctx, p.Event.EventID)
	if e != nil || len(jobs) != 1 || jobs[0].State != "uncertain" {
		t.Fatalf("status=%+v %v", jobs, e)
	}
	good := bad
	good.PayloadHash = p.Hash
	if e = s.Reconcile(ctx, p.Event.EventID, good); e != nil {
		t.Fatal(e)
	}
	if e = s.Complete(ctx, *claim, domain.Outcome{Kind: domain.Permanent}); e != ErrStale {
		t.Fatal("stale completion overwrote reconcile")
	}
	jobs, e = s.Status(ctx, "")
	if e != nil || len(jobs) != 1 || jobs[0].State != "delivered" || jobs[0].Receipt == nil || *jobs[0].Receipt != good {
		t.Fatalf("reconcile state=%+v %v", jobs, e)
	}
	var after int
	db.Admin.QueryRow(ctx, "SELECT count(*) FROM worker.attempts").Scan(&after)
	if before != after {
		t.Fatal("reconcile rewrote attempt history")
	}
}

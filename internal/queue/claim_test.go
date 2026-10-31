package queue

import (
	"context"
	"errors"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/testutil"
	"sync"
	"testing"
	"time"
)

func TestClaimReservationsRealDB(t *testing.T) {
	db := testutil.PostgreSQL(t)
	s := New(db.Worker)
	ctx := context.Background()
	p := payload(t)
	s.Enqueue(ctx, p)
	var wg sync.WaitGroup
	claims := make(chan *Claim, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := s.ClaimDue(ctx)
			if e != nil {
				t.Error(e)
			}
			if c != nil {
				claims <- c
			}
		}()
	}
	wg.Wait()
	close(claims)
	if len(claims) != 1 {
		t.Fatalf("claims=%d", len(claims))
	}
	c := <-claims
	var attempts, idle int
	var leaseSeconds float64
	db.Admin.QueryRow(ctx, "SELECT count(*) FROM worker.attempts WHERE phase='reserved'").Scan(&attempts)
	db.Admin.QueryRow(ctx, "SELECT extract(epoch from lease_until-clock_timestamp()) FROM worker.jobs").Scan(&leaseSeconds)
	db.Admin.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND state='idle in transaction'").Scan(&idle)
	if attempts != 1 || c.Attempt != 1 || leaseSeconds < 8 || leaseSeconds > 10 || idle != 0 {
		t.Fatalf("reservation=%d attempt=%d lease=%f idle=%d", attempts, c.Attempt, leaseSeconds, idle)
	}
	wrong := *c
	wrong.Token = "wrong"
	if e := s.Complete(ctx, wrong, domain.Outcome{Kind: domain.Retryable}); !errors.Is(e, ErrStale) {
		t.Fatalf("stale=%v", e)
	}
	for i := 1; i <= 3; i++ {
		if i > 1 {
			db.Admin.Exec(ctx, "UPDATE worker.jobs SET due_at=clock_timestamp()-interval '1 second'")
			var e error
			c, e = s.ClaimDue(ctx)
			if e != nil || c == nil || c.Attempt != i {
				t.Fatalf("claim %d: %v", i, e)
			}
		}
		if e := s.Complete(ctx, *c, domain.Outcome{Kind: domain.Retryable, Reason: "503"}); e != nil {
			t.Fatal(e)
		}
		if i < 3 {
			var delay float64
			db.Admin.QueryRow(ctx, "SELECT extract(epoch from due_at-clock_timestamp()) FROM worker.jobs").Scan(&delay)
			if delay <= 0 || delay > 0.21 {
				t.Fatalf("backoff=%f", delay)
			}
		}
	}
	c, e := s.ClaimDue(ctx)
	if e != nil || c != nil {
		t.Fatalf("fourth claim=%v error=%v", c, e)
	}
	var state string
	db.Admin.QueryRow(ctx, "SELECT state FROM worker.jobs").Scan(&state)
	if state != "failed" {
		t.Fatal(state)
	}
}
func TestRecoveryAndStickyAmbiguityRealDB(t *testing.T) {
	for _, mode := range []string{"abandoned", "third_abandoned", "sticky", "success", "permanent"} {
		t.Run(mode, func(t *testing.T) {
			db := testutil.PostgreSQL(t)
			s := New(db.Worker)
			ctx := context.Background()
			p := payload(t)
			s.Enqueue(ctx, p)
			if mode == "third_abandoned" {
				db.Admin.Exec(ctx, "UPDATE worker.jobs SET attempt_count=2")
			}
			c, e := s.ClaimDue(ctx)
			if e != nil || c == nil {
				t.Fatalf("claim=%v", e)
			}
			switch mode {
			case "abandoned", "third_abandoned":
				db.Admin.Exec(ctx, "UPDATE worker.jobs SET lease_until=clock_timestamp()-interval '1 second'")
				n, e := s.RecoverExpired(ctx)
				if e != nil || n != 1 {
					t.Fatalf("recovery=%d/%v", n, e)
				}
				if e = s.Complete(ctx, *c, domain.Outcome{Kind: domain.Permanent}); !errors.Is(e, ErrStale) {
					t.Fatal("recovered claim overwritten")
				}
				var state string
				var count int
				db.Admin.QueryRow(ctx, "SELECT state,attempt_count FROM worker.jobs").Scan(&state, &count)
				want := "retry_wait"
				if mode == "third_abandoned" {
					want = "uncertain"
				}
				if state != want || count != c.Attempt {
					t.Fatalf("state/count=%s/%d", state, count)
				}
			case "sticky":
				for i := 1; i <= 3; i++ {
					if i > 1 {
						db.Admin.Exec(ctx, "UPDATE worker.jobs SET due_at=clock_timestamp()-interval '1 second'")
						c, e = s.ClaimDue(ctx)
						if e != nil || c == nil {
							t.Fatal(e)
						}
					}
					if e = s.Complete(ctx, *c, domain.Outcome{Kind: domain.Retryable, Ambiguous: i == 1}); e != nil {
						t.Fatal(e)
					}
				}
				var state string
				db.Admin.QueryRow(ctx, "SELECT state FROM worker.jobs").Scan(&state)
				if state != "uncertain" {
					t.Fatal("ambiguity erased: " + state)
				}
			case "success":
				bad := domain.Receipt{EventID: p.Event.EventID, PayloadHash: "wrong", ReceiptID: "receipt"}
				if e = s.Complete(ctx, *c, domain.Outcome{Kind: domain.Success, Receipt: &bad}); e == nil {
					t.Fatal("mismatched receipt accepted")
				}
				bad.PayloadHash = p.Hash
				if e = s.Complete(ctx, *c, domain.Outcome{Kind: domain.Success, Receipt: &bad}); e != nil {
					t.Fatal(e)
				}
			case "permanent":
				if e = s.Complete(ctx, *c, domain.Outcome{Kind: domain.Permanent}); e != nil {
					t.Fatal(e)
				}
				if c, e = s.ClaimDue(ctx); e != nil || c != nil {
					t.Fatal("terminal claimed")
				}
			}
		})
	}
}
func TestDatabaseDeadlineRealDB(t *testing.T) {
	db := testutil.PostgreSQL(t)
	s := New(db.Worker)
	ctx := context.Background()
	p := payload(t)
	s.Enqueue(ctx, p)
	c, e := s.ClaimDue(ctx)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.Admin.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	tx.Exec(ctx, "LOCK TABLE worker.jobs IN ACCESS EXCLUSIVE MODE")
	start := time.Now()
	if e = s.Complete(ctx, *c, domain.Outcome{Kind: domain.Permanent}); e == nil {
		t.Fatal("blocked DB ignored deadline")
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("DB deadline=%v", elapsed)
	}
}

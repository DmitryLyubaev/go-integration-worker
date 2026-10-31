package queue

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s *Store) Complete(ctx context.Context, claim Claim, o domain.Outcome) error {
	ctx, c := context.WithTimeout(ctx, time.Second)
	defer c()
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer rollback(tx)
	j, e := scan(tx.QueryRow(ctx, "SELECT "+columns+" FROM worker.jobs WHERE event_id=$1 AND state='processing' AND claim_token=$2 FOR UPDATE", claim.Job.Payload.Event.EventID, claim.Token))
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrStale
	}
	if e != nil {
		return e
	}
	if j.AttemptCount != claim.Attempt {
		return ErrStale
	}
	ambiguous := j.Ambiguous || o.Ambiguous
	state := "failed"
	delay := 100 * time.Millisecond
	if j.AttemptCount == 2 {
		delay = 200 * time.Millisecond
	}
	if o.RetryAfter > delay {
		delay = o.RetryAfter
	}
	var receipt []byte
	switch o.Kind {
	case domain.Success:
		if o.Receipt == nil || o.Receipt.EventID != j.Payload.Event.EventID || o.Receipt.PayloadHash != j.Payload.Hash || o.Receipt.ReceiptID == "" {
			return errors.New("receipt does not match claimed event")
		}
		state = "delivered"
		receipt, _ = json.Marshal(o.Receipt)
	case domain.Permanent:
		if ambiguous {
			state = "uncertain"
		}
	case domain.Retryable, domain.Canceled:
		if o.Kind == domain.Canceled {
			ambiguous = true
		}
		if j.AttemptCount < 3 && o.RetryAfter <= time.Second {
			state = "retry_wait"
		} else if ambiguous {
			state = "uncertain"
		}
	case domain.Unrecognized:
		ambiguous = true
		state = "uncertain"
	default:
		return errors.New("invalid delivery outcome")
	}
	if _, e = tx.Exec(ctx, "INSERT INTO worker.attempts(event_id,number,token,phase,reason,ambiguous) VALUES($1,$2,$3,'outcome',$4,$5)", j.Payload.Event.EventID, j.AttemptCount, claim.Token, o.Reason, o.Ambiguous || o.Kind == domain.Canceled); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE worker.jobs SET state=$2,ambiguous=$3,due_at=clock_timestamp()+$4*interval '1 millisecond',claim_token='',lease_until=NULL,receipt=coalesce($5::jsonb,receipt),last_reason=$6 WHERE event_id=$1", j.Payload.Event.EventID, state, ambiguous, delay.Milliseconds(), receipt, o.Reason); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

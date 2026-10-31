package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type Claim struct {
	Job     Job
	Token   string
	Attempt int
}

var ErrStale = errors.New("claim is no longer owned")

func rollback(tx pgx.Tx) {
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	tx.Rollback(ctx)
}
func (s *Store) ClaimDue(ctx context.Context) (*Claim, error) {
	ctx, c := context.WithTimeout(ctx, time.Second)
	defer c()
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer rollback(tx)
	j, e := scan(tx.QueryRow(ctx, "SELECT "+columns+" FROM worker.jobs WHERE state IN ('queued','retry_wait') AND due_at<=clock_timestamp() AND attempt_count<3 ORDER BY due_at,event_id FOR UPDATE SKIP LOCKED LIMIT 1"))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	raw := make([]byte, 16)
	if _, e = rand.Read(raw); e != nil {
		return nil, e
	}
	token := hex.EncodeToString(raw)
	j.AttemptCount++
	e = tx.QueryRow(ctx, "UPDATE worker.jobs SET state='processing',attempt_count=$2,claim_token=$3,lease_until=clock_timestamp()+interval '10 seconds' WHERE event_id=$1 RETURNING lease_until", j.Payload.Event.EventID, j.AttemptCount, token).Scan(&j.LeaseUntil)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO worker.attempts(event_id,number,token,phase) VALUES($1,$2,$3,'reserved')", j.Payload.Event.EventID, j.AttemptCount, token); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	j.State = "processing"
	j.ClaimToken = token
	return &Claim{j, token, j.AttemptCount}, nil
}
func (s *Store) RecoverExpired(ctx context.Context) (int, error) {
	ctx, c := context.WithTimeout(ctx, time.Second)
	defer c()
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer rollback(tx)
	rows, e := tx.Query(ctx, "SELECT event_id,attempt_count,claim_token FROM worker.jobs WHERE state='processing' AND lease_until<=clock_timestamp() FOR UPDATE SKIP LOCKED")
	if e != nil {
		return 0, e
	}
	type abandoned struct {
		id      string
		attempt int
		token   string
	}
	var items []abandoned
	for rows.Next() {
		var item abandoned
		if e = rows.Scan(&item.id, &item.attempt, &item.token); e != nil {
			rows.Close()
			return 0, e
		}
		items = append(items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	for _, item := range items {
		state := "retry_wait"
		if item.attempt >= 3 {
			state = "uncertain"
		}
		if _, e = tx.Exec(ctx, "INSERT INTO worker.attempts(event_id,number,token,phase,reason,ambiguous) VALUES($1,$2,$3,'abandoned','expired claim',true)", item.id, item.attempt, item.token); e != nil {
			return 0, e
		}
		if _, e = tx.Exec(ctx, "UPDATE worker.jobs SET state=$2,ambiguous=true,due_at=clock_timestamp(),claim_token='',lease_until=NULL,last_reason='expired claim' WHERE event_id=$1", item.id, state); e != nil {
			return 0, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return 0, e
	}
	return len(items), nil
}

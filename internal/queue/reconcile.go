package queue

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s *Store) Status(ctx context.Context, id string) ([]Job, error) {
	ctx, c := context.WithTimeout(ctx, time.Second)
	defer c()
	rows, e := s.pool.Query(ctx, "SELECT "+columns+" FROM worker.jobs WHERE $1='' OR event_id=$1 ORDER BY event_id", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		j, e := scan(rows)
		if e != nil {
			return nil, e
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
func (s *Store) Reconcile(ctx context.Context, id string, r domain.Receipt) error {
	ctx, c := context.WithTimeout(ctx, time.Second)
	defer c()
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer rollback(tx)
	j, e := scan(tx.QueryRow(ctx, "SELECT "+columns+" FROM worker.jobs WHERE event_id=$1 FOR UPDATE", id))
	if errors.Is(e, pgx.ErrNoRows) {
		return errors.New("job not found")
	}
	if e != nil {
		return e
	}
	if r.EventID != id || r.PayloadHash != j.Payload.Hash || r.ReceiptID == "" {
		return ErrConflict
	}
	if j.State == "delivered" && j.Receipt != nil && *j.Receipt == r {
		return tx.Commit(ctx)
	}
	if j.State != "failed" && j.State != "uncertain" {
		return errors.New("only terminal unresolved jobs can be reconciled")
	}
	raw, _ := json.Marshal(r)
	if _, e = tx.Exec(ctx, "UPDATE worker.jobs SET state='delivered',receipt=$2::jsonb,last_reason='receipt reconciliation' WHERE event_id=$1", id, raw); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

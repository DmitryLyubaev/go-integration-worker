package queue

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"strings"
	"time"
)

var ErrConflict = errors.New("conflicting identity")

type Job struct {
	Payload      domain.Payload
	State        string
	AttemptCount int
	Ambiguous    bool
	ClaimToken   string
	LeaseUntil   time.Time
	Receipt      *domain.Receipt
	Traceparent  string
}
type Store struct{ pool *pgxpool.Pool }

func New(p *pgxpool.Pool) *Store { return &Store{p} }

const columns = "payload,hash,state,attempt_count,ambiguous,claim_token,coalesce(lease_until,'epoch'::timestamptz),receipt,traceparent"

func scan(row pgx.Row) (Job, error) {
	var j Job
	var raw, receipt []byte
	var hash string
	err := row.Scan(&raw, &hash, &j.State, &j.AttemptCount, &j.Ambiguous, &j.ClaimToken, &j.LeaseUntil, &receipt, &j.Traceparent)
	if err != nil {
		return j, err
	}
	j.Payload, err = domain.Decode(strings.NewReader(string(raw)))
	if err != nil || j.Payload.Hash != hash {
		return j, errors.New("stored payload identity is invalid")
	}
	if len(receipt) > 0 {
		if err = json.Unmarshal(receipt, &j.Receipt); err != nil {
			return j, errors.New("stored receipt invalid")
		}
	}
	return j, nil
}
func (s *Store) Enqueue(ctx context.Context, p domain.Payload) (Job, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	tag, err := s.pool.Exec(ctx, "INSERT INTO worker.jobs(event_id,payload,hash,traceparent) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", p.Event.EventID, p.Bytes, p.Hash, carrier.Get("traceparent"))
	if err != nil {
		return Job{}, false, err
	}
	j, err := scan(s.pool.QueryRow(ctx, "SELECT "+columns+" FROM worker.jobs WHERE event_id=$1", p.Event.EventID))
	if err != nil {
		return Job{}, false, err
	}
	if j.Payload.Hash != p.Hash || string(j.Payload.Bytes) != string(p.Bytes) {
		return Job{}, false, ErrConflict
	}
	return j, tag.RowsAffected() == 1, nil
}

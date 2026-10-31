package receiver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

var ErrConflict = errors.New("conflicting receiver identity")

type Store struct{ pool *pgxpool.Pool }

func NewStore(p *pgxpool.Pool) *Store { return &Store{p} }
func (s *Store) Apply(ctx context.Context, p domain.Payload) (domain.Receipt, bool, error) {
	ctx, c := context.WithTimeout(ctx, time.Second)
	defer c()
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return domain.Receipt{}, false, e
	}
	defer tx.Rollback(context.Background())
	token := make([]byte, 16)
	if _, e = rand.Read(token); e != nil {
		return domain.Receipt{}, false, e
	}
	tag, e := tx.Exec(ctx, "INSERT INTO receiver.receipts(event_id,hash,receipt_id) VALUES($1,$2,$3) ON CONFLICT(event_id) DO NOTHING", p.Event.EventID, p.Hash, hex.EncodeToString(token))
	if e != nil {
		return domain.Receipt{}, false, e
	}
	var r domain.Receipt
	e = tx.QueryRow(ctx, "SELECT event_id,hash,receipt_id FROM receiver.receipts WHERE event_id=$1", p.Event.EventID).Scan(&r.EventID, &r.PayloadHash, &r.ReceiptID)
	if e != nil {
		return r, false, e
	}
	if r.PayloadHash != p.Hash {
		return r, false, ErrConflict
	}
	created := tag.RowsAffected() == 1
	if created {
		_, e = tx.Exec(ctx, "INSERT INTO receiver.effects VALUES($1,$2,$3,$4)", p.Event.EventID, p.Event.SaleReference, p.Event.Currency, p.Event.AmountMinor)
		if e != nil {
			return r, false, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return r, false, e
	}
	return r, created, nil
}
func (s *Store) Receipt(ctx context.Context, id string) (domain.Receipt, error) {
	ctx, c := context.WithTimeout(ctx, time.Second)
	defer c()
	var r domain.Receipt
	e := s.pool.QueryRow(ctx, "SELECT event_id,hash,receipt_id FROM receiver.receipts WHERE event_id=$1", id).Scan(&r.EventID, &r.PayloadHash, &r.ReceiptID)
	return r, e
}

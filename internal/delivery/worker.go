package delivery

import (
	"context"
	"errors"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/queue"
	"log/slog"
	"sync"
	"time"
)

type Config struct {
	Concurrency                   int
	DrainTimeout, ShutdownTimeout time.Duration
	BeforeSend                    func(context.Context, queue.Claim) error
}

func Run(ctx context.Context, store *queue.Store, client *Client, config Config) error {
	if config.Concurrency == 0 {
		config.Concurrency = 2
	}
	if config.DrainTimeout == 0 {
		config.DrainTimeout = time.Second
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = 4 * time.Second
	}
	if config.Concurrency != 2 || config.DrainTimeout < 0 || config.DrainTimeout > time.Second || config.ShutdownTimeout < 0 || config.ShutdownTimeout > 4*time.Second {
		return errors.New("worker configuration exceeds demonstration bounds")
	}
	claimCtx, stopClaims := context.WithCancel(ctx)
	defer stopClaims()
	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	defer client.http.CloseIdleConnections()
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := loop(claimCtx, workCtx, store, client, config); e != nil {
				failures <- e
				stopClaims()
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return firstError(failures)
	case <-claimCtx.Done():
	}
	drain := time.NewTimer(config.DrainTimeout)
	defer drain.Stop()
	select {
	case <-done:
		return firstError(failures)
	case <-drain.C:
		cancelWork()
	}
	shutdown := time.NewTimer(config.ShutdownTimeout)
	defer shutdown.Stop()
	select {
	case <-done:
		return firstError(failures)
	case <-shutdown.C:
		return errors.New("worker shutdown exceeded five-second bound")
	}
}
func firstError(failures <-chan error) error {
	select {
	case e := <-failures:
		return e
	default:
		return nil
	}
}
func loop(claimCtx, workCtx context.Context, store *queue.Store, client *Client, config Config) error {
	for {
		if claimCtx.Err() != nil {
			return nil
		}
		if _, e := store.RecoverExpired(claimCtx); e != nil {
			if claimCtx.Err() != nil {
				return nil
			}
			return fmt.Errorf("recovery failed: %w", e)
		}
		claim, e := store.ClaimDue(claimCtx)
		if e != nil {
			if claimCtx.Err() != nil {
				return nil
			}
			return fmt.Errorf("claim failed: %w", e)
		}
		if claim == nil {
			timer := time.NewTimer(25 * time.Millisecond)
			select {
			case <-claimCtx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
				continue
			}
		}
		outcome := domain.Outcome{Kind: domain.Canceled, Ambiguous: true, Reason: "stop before send"}
		if claimCtx.Err() == nil {
			if config.BeforeSend != nil {
				if e = config.BeforeSend(workCtx, *claim); e != nil {
					outcome.Reason = "claim barrier canceled"
				} else {
					outcome = send(workCtx, client, *claim)
				}
			} else {
				outcome = send(workCtx, client, *claim)
			}
		}
		if e = store.Complete(context.Background(), *claim, outcome); e != nil {
			return fmt.Errorf("completion failed; lease retained: %w", e)
		}
		slog.Info("worker.outcome", "event_id", claim.Job.Payload.Event.EventID, "attempt", claim.Attempt, "kind", outcome.Kind, "ambiguous", outcome.Ambiguous)
	}
}
func send(ctx context.Context, client *Client, claim queue.Claim) domain.Outcome {
	slog.Info("worker.send", "event_id", claim.Job.Payload.Event.EventID, "attempt", claim.Attempt)
	return client.Send(ctx, claim.Job.Payload)
}

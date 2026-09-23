package server

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type Collector interface{ Collect(context.Context) error }
type Dependency interface{ Ping(context.Context) error }
type Worker struct {
	collector  Collector
	dependency Dependency
	readiness  *Readiness
	logger     *slog.Logger
	interval   time.Duration
	mu         sync.Mutex
	cancel     context.CancelFunc
	done       chan struct{}
}

func NewWorker(collector Collector, dependency Dependency, readiness *Readiness, logger *slog.Logger, interval time.Duration) *Worker {
	return &Worker{collector: collector, dependency: dependency, readiness: readiness, logger: logger, interval: interval}
}
func (w *Worker) Start(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan struct{})
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			probe, stop := context.WithTimeout(ctx, 3*time.Second)
			err := w.dependency.Ping(probe)
			stop()
			w.readiness.Set(err == nil && ctx.Err() == nil)
			if err == nil {
				collect, stop := context.WithTimeout(ctx, 30*time.Second)
				err = w.collector.Collect(collect)
				stop()
				if err != nil && ctx.Err() == nil {
					w.logger.Warn("provider collection will retry", "reason", "COLLECTION_FAILED")
				}
			}
			select {
			case <-ctx.Done():
				w.readiness.Set(false)
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}
func (w *Worker) Stop(ctx context.Context) error {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	w.readiness.Set(false)
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

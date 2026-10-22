package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/zhangzhe-ctrl/ani-accelerator-service/internal/biz"
)

type InitializationWorker struct {
	usecase  *biz.InitializationUsecase
	logger   *slog.Logger
	interval time.Duration
	workerID string
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewInitializationWorker(usecase *biz.InitializationUsecase, logger *slog.Logger, interval time.Duration) *InitializationWorker {
	return &InitializationWorker{usecase: usecase, logger: logger, interval: interval, workerID: biz.NewID()}
}

func (worker *InitializationWorker) Start(context.Context) error {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.usecase == nil || worker.interval <= 0 {
		return biz.Invalid("INITIALIZATION_WORKER_CONFIG_INVALID")
	}
	if worker.cancel != nil {
		return biz.Conflict("INITIALIZATION_WORKER_STARTED")
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker.cancel = cancel
	worker.done = make(chan struct{})
	go func() {
		defer close(worker.done)
		ticker := time.NewTicker(worker.interval)
		defer ticker.Stop()
		for {
			work, stop := context.WithTimeout(ctx, 5*time.Minute)
			err := worker.usecase.Work(work, worker.workerID, 16)
			stop()
			if err != nil && ctx.Err() == nil && worker.logger != nil {
				worker.logger.Warn("initialization worker will retry pending work", "reason", "INITIALIZATION_WORK_FAILED")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (worker *InitializationWorker) Stop(ctx context.Context) error {
	worker.mu.Lock()
	cancel, done := worker.cancel, worker.done
	worker.mu.Unlock()
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

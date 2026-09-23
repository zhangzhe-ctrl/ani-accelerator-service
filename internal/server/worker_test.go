package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type workerProbe struct {
	failed   atomic.Bool
	attempts atomic.Int32
}

func (p *workerProbe) Ping(context.Context) error {
	if p.failed.Load() {
		return errors.New("controlled dependency failure")
	}
	return nil
}
func (p *workerProbe) Collect(context.Context) error {
	p.attempts.Add(1)
	return errors.New("controlled provider failure")
}
func TestWorkerDependencyReadinessRetryAndStop(t *testing.T) {
	p := &workerProbe{}
	p.failed.Store(true)
	r := NewReadiness()
	w := NewWorker(p, p, r, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Millisecond)
	if err := w.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Stop(context.Background()) })
	wait := func(fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if fn() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("lifecycle transition timed out")
	}
	if r.Ready() {
		t.Fatal("failed dependency ready")
	}
	p.failed.Store(false)
	wait(func() bool { return r.Ready() && p.attempts.Load() >= 2 })
	p.failed.Store(true)
	wait(func() bool { return !r.Ready() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if r.Ready() {
		t.Fatal("stopped worker ready")
	}
}

package ling

import (
	"context"
	"sync"
)

type activity struct {
	mu    sync.Mutex
	count int
	idle  chan struct{}
}

func newActivity() *activity {
	idle := make(chan struct{})
	close(idle)

	return &activity{
		mu:    sync.Mutex{},
		count: 0,
		idle:  idle,
	}
}

func (work *activity) begin() {
	work.mu.Lock()
	defer work.mu.Unlock()

	if work.count == 0 {
		work.idle = make(chan struct{})
	}

	work.count++
}

func (work *activity) end() {
	work.mu.Lock()
	defer work.mu.Unlock()

	work.count--

	if work.count == 0 {
		close(work.idle)
	}
}

func (work *activity) wait(ctx context.Context) {
	work.mu.Lock()
	idle := work.idle
	work.mu.Unlock()

	select {
	case <-idle:
	case <-ctx.Done():
	}
}

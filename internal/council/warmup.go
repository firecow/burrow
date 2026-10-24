package council

import (
	"context"
	"time"
)

// Warmup tracks the period after startup in which the council rebuilds state from member reports before serving it.
type Warmup struct {
	readyAt time.Time
}

// NewWarmup returns a Warmup that ends after the given duration.
func NewWarmup(duration time.Duration) *Warmup {
	return &Warmup{readyAt: time.Now().Add(duration)}
}

func (warmup *Warmup) active() bool {
	return time.Now().Before(warmup.readyAt)
}

func (warmup *Warmup) wait(ctx context.Context) {
	timer := time.NewTimer(time.Until(warmup.readyAt))
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

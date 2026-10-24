package state

import "context"

// TestFetchState exposes fetchState for external tests.
func (w *Watcher) TestFetchState(ctx context.Context) {
	w.fetchState(ctx)
}

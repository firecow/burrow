package state_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/firecow/burrow/internal/state"
)

func TestReplay_RedeliversUnchangedState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{"revision":7}`))
		}),
	)
	defer server.Close()

	var revisions []int

	watcher := state.NewWatcher(server.URL, func(snapshot *state.State) {
		revisions = append(revisions, snapshot.Revision)
	})

	watcher.TestFetchState(t.Context())
	watcher.TestFetchState(t.Context())
	watcher.Replay()

	if len(revisions) != 2 || revisions[0] != 7 || revisions[1] != 7 {
		t.Fatalf("expected revision 7 delivered twice, got %v", revisions)
	}
}

package state

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	pollInterval      = 5 * time.Second
	waitCheckInterval = 100 * time.Millisecond
	reconnectDelay    = 2 * time.Second
	httpPrefix        = "http://"
	httpsPrefix       = "https://"
	wsPrefix          = "ws://"
	wssPrefix         = "wss://"
)

// Watcher monitors council state via polling and WebSocket events.
type Watcher struct {
	councilHost   string
	state         *State
	mu            sync.Mutex
	onChangeMu    sync.Mutex
	onChange      func(*State)
	httpTransport http.RoundTripper
}

// NewWatcher creates a new Watcher that polls the given council host for state changes.
func NewWatcher(councilHost string, onChange func(*State)) *Watcher {
	return &Watcher{
		councilHost:   councilHost,
		state:         nil,
		mu:            sync.Mutex{},
		onChangeMu:    sync.Mutex{},
		onChange:      onChange,
		httpTransport: http.DefaultTransport,
	}
}

// Run starts polling and WebSocket listening for state changes.
func (w *Watcher) Run(ctx context.Context) {
	go w.listenWebSocket(ctx)

	w.fetchState(ctx)

	ticker := time.NewTicker(pollInterval)

	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.fetchState(ctx)
		}
	}
}

// WaitForState blocks until the watcher has received its first state or the context is cancelled.
func (w *Watcher) WaitForState(ctx context.Context) error {
	ticker := time.NewTicker(waitCheckInterval)

	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for state: %w", ctx.Err())
		case <-ticker.C:
			w.mu.Lock()
			hasState := w.state != nil
			w.mu.Unlock()

			if hasState {
				return nil
			}
		}
	}
}

// Replay invokes the change callback with the latest state.
func (w *Watcher) Replay() {
	w.onChangeMu.Lock()
	defer w.onChangeMu.Unlock()

	w.mu.Lock()
	latest := w.state
	w.mu.Unlock()

	w.onChange(latest)
}

func buildStateURL(councilHost string) (string, error) {
	baseURL, err := url.Parse(councilHost)
	if err != nil {
		return "", fmt.Errorf("parsing council host URL: %w", err)
	}

	return baseURL.JoinPath("/state").String(), nil
}

func (w *Watcher) fetchState(ctx context.Context) {
	stateURL, err := buildStateURL(w.councilHost)
	if err != nil {
		slog.Error("Failed to build state URL", "error", err)

		return
	}

	w.doFetchState(ctx, stateURL)
}

func (w *Watcher) doFetchState(ctx context.Context, stateURL string) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, stateURL, nil,
	)
	if err != nil {
		slog.Error("Failed to create state request", "error", err)

		return
	}

	resp, err := w.httpTransport.RoundTrip(req)
	if err != nil {
		slog.Error("Failed to fetch state from council", "error", err)

		return
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Failed to fetch state from council")

		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("Failed to read state response", "error", err)

		return
	}

	var newState State

	unmarshalErr := json.Unmarshal(body, &newState)
	if unmarshalErr != nil {
		slog.Error("Failed to parse state response", "error", unmarshalErr)

		return
	}

	w.mu.Lock()

	if w.state == nil || w.state.Revision != newState.Revision {
		w.state = &newState
		w.mu.Unlock()
		w.Replay()

		return
	}

	w.mu.Unlock()
}

func (w *Watcher) listenWebSocket(ctx context.Context) {
	wsURL := httpToWS(w.councilHost) + "/ws"

	for {
		if ctx.Err() != nil {
			return
		}

		conn, resp, dialErr := websocket.Dial(ctx, wsURL, nil)

		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}

		if dialErr != nil {
			slog.Error("WebSocket connection failed", "error", dialErr)
			w.waitForReconnect(ctx)

			continue
		}

		slog.Info("WebSocket connected")
		w.readWebSocketMessages(ctx, conn)

		_ = conn.CloseNow()
	}
}

func (w *Watcher) waitForReconnect(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(reconnectDelay):
	}
}

func (w *Watcher) readWebSocketMessages(
	ctx context.Context,
	conn *websocket.Conn,
) {
	for {
		_, message, err := conn.Read(ctx)
		if err != nil {
			slog.Info("WebSocket disconnected", "error", err)

			break
		}

		if string(message) == "state-changed" {
			slog.Info("State changed event received, force fetching")
			w.fetchState(ctx)
		}
	}
}

func httpToWS(rawURL string) string {
	if after, found := strings.CutPrefix(rawURL, httpsPrefix); found {
		return wssPrefix + after
	}

	if after, found := strings.CutPrefix(rawURL, httpPrefix); found {
		return wsPrefix + after
	}

	return wsPrefix + rawURL
}

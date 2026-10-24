package ling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"time"
)

const (
	syncInterval = 1 * time.Second
)

var errUnsupportedURLScheme = errors.New("unsupported URL scheme")

type syncPayload struct {
	LingID            string       `json:"lingId"`
	ShuttingDown      bool         `json:"shuttingDown"`
	Tunnels           []syncTunnel `json:"tunnels"`
	ReadyServiceIDs   []string     `json:"readyServiceIds"`
	PreferredLocation string       `json:"preferredLocation"`
}

type syncTunnel struct {
	Name       string  `json:"name"`
	ServiceID  string  `json:"serviceId,omitempty"`
	Token      string  `json:"token,omitempty"`
	Host       *string `json:"host,omitempty"`
	BindPort   *int    `json:"bindPort,omitempty"`
	RemotePort *int    `json:"remotePort,omitempty"`
}

type lingSyncer struct {
	councilHost       string
	lingID            string
	preferredLocation string
	report            func() ([]syncTunnel, []string)
	shuttingDown      func() bool
	notify            chan struct{}
	httpTransport     http.RoundTripper
}

func (syncer *lingSyncer) run(ctx context.Context) {
	syncer.sync(ctx)

	ticker := time.NewTicker(syncInterval)

	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncer.sync(ctx)
		case <-syncer.notify:
			syncer.sync(ctx)
		}
	}
}

func (syncer *lingSyncer) triggerSync() {
	select {
	case syncer.notify <- struct{}{}:
	default:
	}
}

func validateCouncilHost(councilHost string) (*url.URL, error) {
	baseURL, err := url.Parse(councilHost)
	if err != nil {
		return nil, fmt.Errorf("parsing council host URL: %w", err)
	}

	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf(
			"scheme %q: %w", baseURL.Scheme, errUnsupportedURLScheme,
		)
	}

	return baseURL, nil
}

func buildSyncURL(baseURL *url.URL) *url.URL {
	return &url.URL{
		Scheme:      baseURL.Scheme,
		Opaque:      "",
		User:        baseURL.User,
		Host:        baseURL.Host,
		Path:        path.Join(baseURL.Path, "/ling"),
		RawPath:     "",
		OmitHost:    false,
		ForceQuery:  false,
		RawQuery:    "",
		Fragment:    "",
		RawFragment: "",
	}
}

func (syncer *lingSyncer) sync(ctx context.Context) {
	tunnels, readyIDs := syncer.report()

	payload := syncPayload{
		LingID:            syncer.lingID,
		ShuttingDown:      syncer.shuttingDown(),
		Tunnels:           tunnels,
		ReadyServiceIDs:   readyIDs,
		PreferredLocation: syncer.preferredLocation,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal ling sync payload", "error", err)

		return
	}

	baseURL, err := validateCouncilHost(syncer.councilHost)
	if err != nil {
		slog.Error("Failed to validate council host", "error", err)

		return
	}

	syncURL := buildSyncURL(baseURL)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPut,
		syncURL.String(), bytes.NewReader(body),
	)
	if err != nil {
		slog.Error("Failed to build sync request", "error", err)

		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := syncer.httpTransport.RoundTrip(req)
	if err != nil {
		slog.Error("Failed to sync with council", "error", err)

		return
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Council sync returned non-OK status")
	}
}

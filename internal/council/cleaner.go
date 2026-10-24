package council

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/firecow/burrow/internal/state"
)

const staleThresholdMillis = 10000

// StartCleaner starts the background cleaner loop that removes stale kings,
// lings, and orphaned services from state.
func StartCleaner(
	ctx context.Context,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
	warmup *Warmup,
	stop <-chan struct{},
) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if !warmup.active() {
				RunCleaner(ctx, currentState, stateMutex, hub)
			}
		}
	}
}

// RunCleaner performs a single cleaner pass, removing stale kings, lings,
// and orphaned services from state.
func RunCleaner(
	ctx context.Context,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	hub *WSHub,
) {
	stateMutex.Lock()

	stateChanged := false
	now := time.Now().UnixMilli()
	threshold := now - staleThresholdMillis

	stateChanged = cleanStaleKings(currentState, threshold) || stateChanged
	stateChanged = cleanStaleLings(currentState, threshold) || stateChanged
	stateChanged = cleanOrphanedServices(currentState) || stateChanged

	if stateChanged {
		currentState.Revision++
		stateMutex.Unlock()
		hub.Broadcast(ctx)

		return
	}

	stateMutex.Unlock()
}

func cleanStaleKings(currentState *state.State, threshold int64) bool {
	changed := false
	kings := currentState.Kings[:0]

	for _, king := range currentState.Kings {
		if king.Beat <= threshold {
			slog.Info("Removing stale king", "host", king.Host, "bind_port", king.BindPort)

			changed = true

			continue
		}

		kings = append(kings, king)
	}

	currentState.Kings = kings

	return changed
}

func cleanStaleLings(currentState *state.State, threshold int64) bool {
	changed := false
	lings := currentState.Lings[:0]

	for _, ling := range currentState.Lings {
		if ling.Beat <= threshold {
			slog.Info("Removing stale ling", "ling_id", ling.LingID)

			changed = true

			continue
		}

		lings = append(lings, ling)
	}

	currentState.Lings = lings

	return changed
}

func cleanOrphanedServices(currentState *state.State) bool {
	changed := false
	services := currentState.Services[:0]

	for _, svc := range currentState.Services {
		if svc.BindPort == nil {
			if hasLing(currentState, svc.LingID) {
				services = append(services, svc)
			} else {
				slog.Info(
					"Removing orphaned service (ling missing)",
					"name", svc.Name,
					"service_id", svc.ServiceID,
				)

				changed = true
			}

			continue
		}

		kingFound := hasKingForService(currentState, &svc)
		lingFound := hasLing(currentState, svc.LingID)

		if kingFound && lingFound {
			services = append(services, svc)
		} else {
			slog.Info(
				"Removing orphaned service",
				"name", svc.Name,
				"service_id", svc.ServiceID,
				"king_found", kingFound,
				"ling_found", lingFound,
			)

			changed = true
		}
	}

	currentState.Services = services

	return changed
}

func hasKingForService(currentState *state.State, svc *state.Service) bool {
	for _, king := range currentState.Kings {
		if svc.Host != nil &&
			svc.BindPort != nil &&
			king.Host == *svc.Host &&
			king.BindPort == *svc.BindPort {
			return true
		}
	}

	return false
}

func hasLing(currentState *state.State, lingID string) bool {
	for _, ling := range currentState.Lings {
		if ling.LingID == lingID {
			return true
		}
	}

	return false
}

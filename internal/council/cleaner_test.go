package council_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/firecow/burrow/internal/council"
	"github.com/firecow/burrow/internal/state"
)

const (
	testHostA       = "1.2.3.4"
	testHostB       = "5.6.7.8"
	testLingID      = "ling-1"
	testServiceID   = "svc-1"
	testServiceName = "alpha"
	testPorts       = "5000-5001"
	testLocation    = "CPH"
)

func TestRunCleaner_RemovesStaleKing(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Services: nil,
		Kings: []state.King{
			{
				BindPort:     2333,
				Host:         testHostA,
				Ports:        "",
				ShuttingDown: false,
				Beat:         time.Now().UnixMilli() - 11000,
				Location:     "",
				CertPEM:      "",
			},
		},
		Lings: nil,
	}

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()

	council.RunCleaner(context.Background(), currentState, &stateMutex, hub)

	if len(currentState.Kings) != 0 {
		t.Fatalf("expected 0 kings, got %d", len(currentState.Kings))
	}

	if currentState.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", currentState.Revision)
	}
}

func TestRunCleaner_KeepsFreshKing(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Services: nil,
		Kings: []state.King{
			{
				BindPort:     2333,
				Host:         testHostA,
				Ports:        "",
				ShuttingDown: false,
				Beat:         time.Now().UnixMilli(),
				Location:     "",
				CertPEM:      "",
			},
		},
		Lings: nil,
	}

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()

	council.RunCleaner(context.Background(), currentState, &stateMutex, hub)

	if len(currentState.Kings) != 1 {
		t.Fatalf("expected 1 king, got %d", len(currentState.Kings))
	}

	if currentState.Revision != 0 {
		t.Fatalf("expected revision 0, got %d", currentState.Revision)
	}
}

func TestRunCleaner_RemovesStaleLing(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Services: nil,
		Kings:    nil,
		Lings: []state.Ling{
			{
				LingID:       testLingID,
				ShuttingDown: false,
				Beat:         time.Now().UnixMilli() - 11000,
			},
		},
	}

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()

	council.RunCleaner(context.Background(), currentState, &stateMutex, hub)

	if len(currentState.Lings) != 0 {
		t.Fatalf("expected 0 lings, got %d", len(currentState.Lings))
	}
}

func TestRunCleaner_RemovesOrphanedService(t *testing.T) {
	t.Parallel()

	host := testHostA
	bindPort := 2333
	remotePort := 5000
	currentState := &state.State{
		Revision: 0,
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              "",
				Token:             "",
				LingID:            testLingID,
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              &host,
				BindPort:          &bindPort,
				RemotePort:        &remotePort,
			},
		},
		Kings: nil,
		Lings: nil,
	}

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()

	council.RunCleaner(context.Background(), currentState, &stateMutex, hub)

	if len(currentState.Services) != 0 {
		t.Fatalf(
			"expected 0 services (orphaned), got %d",
			len(currentState.Services),
		)
	}
}

func TestRunCleaner_KeepsServiceWithKingAndLing(t *testing.T) {
	t.Parallel()

	host := testHostA
	bindPort := 2333
	remotePort := 5000
	currentState := &state.State{
		Revision: 0,
		Kings: []state.King{
			{
				BindPort:     2333,
				Host:         testHostA,
				Ports:        "",
				ShuttingDown: false,
				Beat:         time.Now().UnixMilli(),
				Location:     "",
				CertPEM:      "",
			},
		},
		Lings: []state.Ling{
			{
				LingID:       testLingID,
				ShuttingDown: false,
				Beat:         time.Now().UnixMilli(),
			},
		},
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              "",
				Token:             "",
				LingID:            testLingID,
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              &host,
				BindPort:          &bindPort,
				RemotePort:        &remotePort,
			},
		},
	}

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()

	council.RunCleaner(context.Background(), currentState, &stateMutex, hub)

	if len(currentState.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(currentState.Services))
	}
}

func TestRunCleaner_KeepsUnprovisionedServiceWithLing(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Kings:    nil,
		Lings: []state.Ling{
			{
				LingID:       testLingID,
				ShuttingDown: false,
				Beat:         time.Now().UnixMilli(),
			},
		},
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              "",
				Token:             "",
				LingID:            testLingID,
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              nil,
				BindPort:          nil,
				RemotePort:        nil,
			},
		},
	}

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()

	council.RunCleaner(context.Background(), currentState, &stateMutex, hub)

	if len(currentState.Services) != 1 {
		t.Fatalf(
			"expected 1 service (unprovisioned but ling exists), got %d",
			len(currentState.Services),
		)
	}
}

func TestRunCleaner_RemovesUnprovisionedServiceWithoutLing(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Kings:    nil,
		Lings:    nil,
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              "",
				Token:             "",
				LingID:            testLingID,
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              nil,
				BindPort:          nil,
				RemotePort:        nil,
			},
		},
	}

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()

	council.RunCleaner(context.Background(), currentState, &stateMutex, hub)

	if len(currentState.Services) != 0 {
		t.Fatalf(
			"expected 0 services (ling missing), got %d",
			len(currentState.Services),
		)
	}
}

package council_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/firecow/burrow/internal/council"
	"github.com/firecow/burrow/internal/state"
)

const (
	testAdoptedServiceID = "svc-adopted"
	testAdoptedToken     = "tok-adopted"
	testBindPort         = 2333
	testFirstPort        = 5000
)

func registerTestKing(
	t *testing.T,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	warmup *council.Warmup,
) {
	t.Helper()

	registerKing(
		t,
		council.HandlePutKing(currentState, stateMutex, council.NewWSHub(), warmup),
		council.PutKingRequest{
			Host:            testHostA,
			ShuttingDown:    false,
			Tunnels:         []council.PutKingTunnel{{BindPort: testBindPort, Ports: testPorts}},
			ReadyServiceIDs: []string{},
			Location:        testLocation,
			CertPEM:         "",
		},
	)
}

func registerTestLing(
	t *testing.T,
	currentState *state.State,
	stateMutex *sync.RWMutex,
	warmup *council.Warmup,
	tunnel council.PutLingTunnel,
) {
	t.Helper()

	registerLing(
		t,
		council.HandlePutLing(currentState, stateMutex, council.NewWSHub(), warmup),
		council.PutLingRequest{
			LingID:            testLingID,
			ShuttingDown:      false,
			Tunnels:           []council.PutLingTunnel{tunnel},
			ReadyServiceIDs:   []string{},
			PreferredLocation: testLocation,
		},
	)
}

func adoptedTunnel() council.PutLingTunnel {
	host := testHostA
	bindPort := testBindPort
	remotePort := testFirstPort

	return council.PutLingTunnel{
		Name:       testServiceName,
		ServiceID:  testAdoptedServiceID,
		Token:      testAdoptedToken,
		Host:       &host,
		BindPort:   &bindPort,
		RemotePort: &remotePort,
	}
}

func TestPutLing_AdoptsReportedIdentity(t *testing.T) {
	t.Parallel()

	currentState := newTestState()

	var stateMutex sync.RWMutex

	warmup := council.NewWarmup(0)

	registerTestKing(t, currentState, &stateMutex, warmup)
	registerTestLing(t, currentState, &stateMutex, warmup, adoptedTunnel())

	svc := currentState.Services[0]
	if svc.ServiceID != testAdoptedServiceID || svc.Token != testAdoptedToken {
		t.Fatalf("expected adopted identity, got %s/%s", svc.ServiceID, svc.Token)
	}

	if svc.RemotePort == nil || *svc.RemotePort != testFirstPort {
		t.Fatalf("expected adopted remote port %d, got %v", testFirstPort, svc.RemotePort)
	}
}

func TestPutLing_ReprovisionsAdoptedServiceWhenPortTaken(t *testing.T) {
	t.Parallel()

	currentState := newTestState()
	host := testHostA
	bindPort := testBindPort
	remotePort := testFirstPort
	currentState.Services = append(currentState.Services, state.Service{
		Name:              "other",
		Token:             "",
		ServiceID:         "svc-other",
		LingID:            "ling-other",
		PreferredLocation: "",
		LingReady:         false,
		KingReady:         false,
		Host:              &host,
		BindPort:          &bindPort,
		RemotePort:        &remotePort,
	})

	var stateMutex sync.RWMutex

	warmup := council.NewWarmup(0)

	registerTestKing(t, currentState, &stateMutex, warmup)
	registerTestLing(t, currentState, &stateMutex, warmup, adoptedTunnel())

	adopted := council.FindService(currentState, testAdoptedServiceID)
	if adopted == nil {
		t.Fatal("expected adopted service ID to be kept")
	}

	if adopted.RemotePort == nil || *adopted.RemotePort == testFirstPort {
		t.Fatalf("expected adopted service on a free port, got %v", adopted.RemotePort)
	}
}

func TestPutLing_DefersProvisioningDuringWarmup(t *testing.T) {
	t.Parallel()

	currentState := newTestState()

	var stateMutex sync.RWMutex

	warmup := council.NewWarmup(time.Hour)

	registerTestKing(t, currentState, &stateMutex, warmup)
	registerTestLing(t, currentState, &stateMutex, warmup, council.PutLingTunnel{
		Name: testServiceName, ServiceID: "", Token: "", Host: nil, BindPort: nil, RemotePort: nil,
	})

	if currentState.Services[0].BindPort != nil {
		t.Fatal("expected no provisioning during warmup")
	}
}

func TestGetState_UnavailableDuringWarmup(t *testing.T) {
	t.Parallel()

	var stateMutex sync.RWMutex

	handler := council.HandleGetState(newTestState(), &stateMutex, council.NewWarmup(time.Hour))
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/state", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 during warmup, got %d", recorder.Code)
	}
}

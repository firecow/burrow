package council_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/firecow/burrow/internal/council"
	"github.com/firecow/burrow/internal/state"
)

func newTestState() *state.State {
	return &state.State{
		Revision: 0,
		Services: []state.Service{},
		Kings:    []state.King{},
		Lings:    []state.Ling{},
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}

	return data
}

func TestGetState_ReturnsJSON(t *testing.T) {
	t.Parallel()

	currentState := newTestState()

	var stateMutex sync.RWMutex

	handler := council.HandleGetState(currentState, &stateMutex)

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/state", nil)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	resp := recorder.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)

	var result state.State

	err := json.Unmarshal(body, &result)
	if err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if result.Revision != 0 {
		t.Fatalf("expected revision 0, got %d", result.Revision)
	}
}

func TestPutKing_CreatesNewKing(t *testing.T) {
	t.Parallel()

	currentState := newTestState()

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()
	handler := council.HandlePutKing(currentState, &stateMutex, hub)

	payload := council.PutKingRequest{
		Host:         testHostA,
		ShuttingDown: false,
		Tunnels: []council.PutKingTunnel{
			{BindPort: 2333, Ports: testPorts},
		},
		ReadyServiceIDs: []string{},
		Location:        testLocation,
		CertPEM:         "",
	}
	body := mustMarshal(t, payload)

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/king", bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	resp := recorder.Result()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)

		t.Fatalf(
			"expected 200, got %d: %s",
			resp.StatusCode,
			string(bodyBytes),
		)
	}

	if len(currentState.Kings) != 1 {
		t.Fatalf("expected 1 king, got %d", len(currentState.Kings))
	}

	if currentState.Kings[0].Host != testHostA {
		t.Fatalf(
			"expected host %s, got %s",
			testHostA,
			currentState.Kings[0].Host,
		)
	}

	if currentState.Kings[0].Location != testLocation {
		t.Fatalf(
			"expected location CPH, got %s",
			currentState.Kings[0].Location,
		)
	}

	if currentState.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", currentState.Revision)
	}
}

func TestPutKing_UpdatesExistingKing(t *testing.T) {
	t.Parallel()

	currentState := newTestState()
	currentState.Kings = append(currentState.Kings, state.King{
		BindPort:     2333,
		Host:         testHostA,
		Ports:        testPorts,
		ShuttingDown: false,
		Beat:         1000,
		Location:     testLocation,
		CertPEM:      "",
	})

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()
	handler := council.HandlePutKing(currentState, &stateMutex, hub)

	payload := council.PutKingRequest{
		Host:         testHostA,
		ShuttingDown: false,
		Tunnels: []council.PutKingTunnel{
			{BindPort: 2333, Ports: testPorts},
		},
		ReadyServiceIDs: []string{},
		Location:        testLocation,
		CertPEM:         "",
	}
	body := mustMarshal(t, payload)

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/king", bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	if len(currentState.Kings) != 1 {
		t.Fatalf(
			"expected 1 king (updated), got %d",
			len(currentState.Kings),
		)
	}

	if currentState.Kings[0].Beat == 1000 {
		t.Fatalf("expected beat to be updated")
	}
}

func TestPutKing_SetsReadyServiceIDs(t *testing.T) {
	t.Parallel()

	currentState := newTestState()
	currentState.Kings = append(currentState.Kings, state.King{
		BindPort:     2333,
		Host:         testHostA,
		Ports:        testPorts,
		ShuttingDown: false,
		Beat:         0,
		Location:     testLocation,
		CertPEM:      "",
	})
	currentState.Services = append(currentState.Services, state.Service{
		ServiceID:         testServiceID,
		Name:              testServiceName,
		Token:             "",
		LingID:            "",
		PreferredLocation: "",
		LingReady:         false,
		KingReady:         false,
		Host:              nil,
		BindPort:          nil,
		RemotePort:        nil,
	})

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()
	handler := council.HandlePutKing(currentState, &stateMutex, hub)

	payload := council.PutKingRequest{
		Host:         testHostA,
		ShuttingDown: false,
		Tunnels: []council.PutKingTunnel{
			{BindPort: 2333, Ports: testPorts},
		},
		ReadyServiceIDs: []string{testServiceID},
		Location:        testLocation,
		CertPEM:         "",
	}
	body := mustMarshal(t, payload)

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/king", bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	if !currentState.Services[0].KingReady {
		t.Fatalf("expected king_ready to be true")
	}
}

func assertLingAndServiceCreated(
	t *testing.T,
	currentState *state.State,
) {
	t.Helper()

	if len(currentState.Lings) != 1 {
		t.Fatalf("expected 1 ling, got %d", len(currentState.Lings))
	}

	if currentState.Lings[0].LingID != testLingID {
		t.Fatalf("expected ling_id ling-1, got %s", currentState.Lings[0].LingID)
	}

	if len(currentState.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(currentState.Services))
	}

	if currentState.Services[0].Name != testServiceName {
		t.Fatalf("expected service name alpha, got %s", currentState.Services[0].Name)
	}

	if currentState.Services[0].LingID != testLingID {
		t.Fatalf("expected ling_id ling-1, got %s", currentState.Services[0].LingID)
	}

	if currentState.Services[0].Token == "" {
		t.Fatalf("expected non-empty token")
	}

	if currentState.Services[0].ServiceID == "" {
		t.Fatalf("expected non-empty service_id")
	}
}

func TestPutLing_CreatesNewLingAndService(t *testing.T) {
	t.Parallel()

	currentState := newTestState()

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()
	handler := council.HandlePutLing(currentState, &stateMutex, hub)

	payload := council.PutLingRequest{
		LingID:       testLingID,
		ShuttingDown: false,
		Tunnels: []council.PutLingTunnel{
			{Name: testServiceName},
		},
		ReadyServiceIDs:   []string{},
		PreferredLocation: testLocation,
	}
	body := mustMarshal(t, payload)

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/ling", bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	resp := recorder.Result()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)

		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	assertLingAndServiceCreated(t, currentState)
}

func TestPutLing_DoesNotDuplicateService(t *testing.T) {
	t.Parallel()

	currentState := newTestState()
	currentState.Lings = append(currentState.Lings, state.Ling{
		LingID:       testLingID,
		ShuttingDown: false,
		Beat:         1000,
	})
	currentState.Services = append(currentState.Services, state.Service{
		ServiceID:         testServiceID,
		Name:              testServiceName,
		Token:             "",
		LingID:            testLingID,
		PreferredLocation: "",
		LingReady:         false,
		KingReady:         false,
		Host:              nil,
		BindPort:          nil,
		RemotePort:        nil,
	})

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()
	handler := council.HandlePutLing(currentState, &stateMutex, hub)

	payload := council.PutLingRequest{
		LingID:       testLingID,
		ShuttingDown: false,
		Tunnels: []council.PutLingTunnel{
			{Name: testServiceName},
		},
		ReadyServiceIDs:   []string{},
		PreferredLocation: testLocation,
	}
	body := mustMarshal(t, payload)

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/ling", bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	if len(currentState.Services) != 1 {
		t.Fatalf(
			"expected 1 service (no duplicate), got %d",
			len(currentState.Services),
		)
	}
}

func TestPutLing_SetsReadyServiceIDs(t *testing.T) {
	t.Parallel()

	currentState := newTestState()
	currentState.Services = append(currentState.Services, state.Service{
		ServiceID:         testServiceID,
		Name:              testServiceName,
		Token:             "",
		LingID:            testLingID,
		PreferredLocation: "",
		LingReady:         false,
		KingReady:         false,
		Host:              nil,
		BindPort:          nil,
		RemotePort:        nil,
	})

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()
	handler := council.HandlePutLing(currentState, &stateMutex, hub)

	payload := council.PutLingRequest{
		LingID:       testLingID,
		ShuttingDown: false,
		Tunnels: []council.PutLingTunnel{
			{Name: testServiceName},
		},
		ReadyServiceIDs:   []string{testServiceID},
		PreferredLocation: testLocation,
	}
	body := mustMarshal(t, payload)

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/ling", bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	if !currentState.Services[0].LingReady {
		t.Fatalf("expected ling_ready to be true")
	}
}

func TestPutKing_MissingHost(t *testing.T) {
	t.Parallel()

	currentState := newTestState()

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()
	handler := council.HandlePutKing(currentState, &stateMutex, hub)

	payload := council.PutKingRequest{
		Host:         "",
		ShuttingDown: false,
		Tunnels: []council.PutKingTunnel{
			{BindPort: 2333, Ports: testPorts},
		},
		ReadyServiceIDs: []string{},
		Location:        testLocation,
		CertPEM:         "",
	}
	body := mustMarshal(t, payload)

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/king", bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	if recorder.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"expected 400 for missing host, got %d",
			recorder.Result().StatusCode,
		)
	}
}

func TestPutLing_MissingLingID(t *testing.T) {
	t.Parallel()

	currentState := newTestState()

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()
	handler := council.HandlePutLing(currentState, &stateMutex, hub)

	payload := council.PutLingRequest{
		LingID:       "",
		ShuttingDown: false,
		Tunnels: []council.PutLingTunnel{
			{Name: testServiceName},
		},
		ReadyServiceIDs:   []string{},
		PreferredLocation: testLocation,
	}
	body := mustMarshal(t, payload)

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/ling", bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	if recorder.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"expected 400 for missing ling_id, got %d",
			recorder.Result().StatusCode,
		)
	}
}

func TestPutKing_ShuttingDownUpdatesState(t *testing.T) {
	t.Parallel()

	currentState := newTestState()
	currentState.Kings = append(currentState.Kings, state.King{
		BindPort:     2333,
		Host:         testHostA,
		Ports:        testPorts,
		ShuttingDown: false,
		Beat:         1000,
		Location:     testLocation,
		CertPEM:      "",
	})

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()
	handler := council.HandlePutKing(currentState, &stateMutex, hub)

	payload := council.PutKingRequest{
		Host:         testHostA,
		ShuttingDown: true,
		Tunnels: []council.PutKingTunnel{
			{BindPort: 2333, Ports: testPorts},
		},
		ReadyServiceIDs: []string{},
		Location:        testLocation,
		CertPEM:         "",
	}
	body := mustMarshal(t, payload)

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/king", bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	if !currentState.Kings[0].ShuttingDown {
		t.Fatalf("expected shutting_down to be true")
	}
}

func registerKing(
	t *testing.T,
	handler http.HandlerFunc,
	payload council.PutKingRequest,
) {
	t.Helper()

	body := mustMarshal(t, payload)
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/king",
		bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)
}

func registerLing(
	t *testing.T,
	handler http.HandlerFunc,
	payload council.PutLingRequest,
) {
	t.Helper()

	body := mustMarshal(t, payload)
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/ling",
		bytes.NewReader(body),
	)
	recorder := httptest.NewRecorder()
	handler(recorder, request)
}

func TestFullProvisioningFlow(t *testing.T) {
	t.Parallel()

	currentState := newTestState()

	var stateMutex sync.RWMutex

	hub := council.NewWSHub()

	registerKing(t, council.HandlePutKing(currentState, &stateMutex, hub), council.PutKingRequest{
		Host:            testHostA,
		ShuttingDown:    false,
		Tunnels:         []council.PutKingTunnel{{BindPort: 2333, Ports: testPorts}},
		ReadyServiceIDs: []string{},
		Location:        testLocation,
		CertPEM:         "",
	})

	registerLing(t, council.HandlePutLing(currentState, &stateMutex, hub), council.PutLingRequest{
		LingID:            testLingID,
		ShuttingDown:      false,
		Tunnels:           []council.PutLingTunnel{{Name: testServiceName}},
		ReadyServiceIDs:   []string{},
		PreferredLocation: testLocation,
	})

	if len(currentState.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(currentState.Services))
	}

	svc := currentState.Services[0]

	if svc.Host == nil || *svc.Host != testHostA {
		t.Fatalf("expected provisioned to host %s", testHostA)
	}

	if svc.RemotePort == nil || *svc.RemotePort != 5000 {
		t.Fatalf("expected remote_port 5000, got %v", svc.RemotePort)
	}
}

package king_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/firecow/burrow/internal/king"
	"github.com/firecow/burrow/internal/state"
)

const (
	testHost          = "1.2.3.4"
	testDifferentHost = "9.9.9.9"
	testBindPort      = 2333
	testBindPort2     = 2334
	testNonMatchPort  = 9999
	testPortRange1    = "5000-5100"
	testPortRange2    = "6000-6050"
	testPortRange3    = "39701-39710"
	testPortRange4    = "39711-39720"
	testPortRange5    = "39721-39730"
	testPortRange6    = "39731-39740"
	testPortRange7    = "39741-39750"
	testPortRange8    = "39751-39760"
	testPortRange9    = "39761-39770"
	testPortRange10   = "39771-39780"
	testRemotePort1   = 39701
	testRemotePort2   = 39711
	testRemotePort3   = 39721
	testRemotePort4   = 39741
	testRemotePort5   = 39751
	testRemotePort6   = 39761
	testRemotePort7   = 39771
)

func newTestService(
	serviceID, lingID string,
	host *string,
	bindPort *int,
) state.Service {
	return state.Service{
		Name:              "",
		Token:             "",
		ServiceID:         serviceID,
		LingID:            lingID,
		PreferredLocation: "",
		LingReady:         false,
		KingReady:         false,
		Host:              host,
		BindPort:          bindPort,
		RemotePort:        nil,
	}
}

func newTestServiceWithToken(
	serviceID, lingID, token string,
	host *string,
	bindPort, remotePort *int,
) state.Service {
	return state.Service{
		Name:              "",
		Token:             token,
		ServiceID:         serviceID,
		LingID:            lingID,
		PreferredLocation: "",
		LingReady:         false,
		KingReady:         false,
		Host:              host,
		BindPort:          bindPort,
		RemotePort:        remotePort,
	}
}

func newTestLing(
	lingID string, shuttingDown bool,
) state.Ling {
	return state.Ling{
		LingID:       lingID,
		ShuttingDown: shuttingDown,
		Beat:         0,
	}
}

func newTestState(
	services []state.Service,
	lings []state.Ling,
) *state.State {
	return &state.State{
		Revision: 0,
		Services: services,
		Kings:    nil,
		Lings:    lings,
	}
}

// --- ParseTunnelArgs ---

func TestParseTunnelArgs_ValidSingle(t *testing.T) {
	t.Parallel()

	configs, err := king.ParseTunnelArgs(
		[]string{"bind_port=2333 ports=5000-5100"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}

	if configs[0].BindPort != testBindPort {
		t.Fatalf(
			"expected bind_port 2333, got %d",
			configs[0].BindPort,
		)
	}

	if configs[0].Ports != testPortRange1 {
		t.Fatalf(
			"expected ports 5000-5100, got %s",
			configs[0].Ports,
		)
	}
}

func TestParseTunnelArgs_ValidMultiple(t *testing.T) {
	t.Parallel()

	configs, err := king.ParseTunnelArgs([]string{
		"bind_port=2333 ports=5000-5100",
		"bind_port=2334 ports=6000-6050",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}

	if configs[0].BindPort != testBindPort {
		t.Fatalf(
			"expected first bind_port 2333, got %d",
			configs[0].BindPort,
		)
	}

	if configs[1].BindPort != testBindPort2 {
		t.Fatalf(
			"expected second bind_port 2334, got %d",
			configs[1].BindPort,
		)
	}

	if configs[1].Ports != testPortRange2 {
		t.Fatalf(
			"expected second ports 6000-6050, got %s",
			configs[1].Ports,
		)
	}
}

func TestParseTunnelArgs_Empty(t *testing.T) {
	t.Parallel()

	configs, err := king.ParseTunnelArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(configs) != 0 {
		t.Fatalf("expected 0 configs, got %d", len(configs))
	}
}

func TestParseTunnelArgs_MissingBindPort(t *testing.T) {
	t.Parallel()

	_, err := king.ParseTunnelArgs(
		[]string{"ports=5000-5100"},
	)
	if err == nil {
		t.Fatal("expected error for missing bind_port")
	}
}

func TestParseTunnelArgs_MissingPorts(t *testing.T) {
	t.Parallel()

	_, err := king.ParseTunnelArgs(
		[]string{"bind_port=2333"},
	)
	if err == nil {
		t.Fatal("expected error for missing ports")
	}
}

func TestParseTunnelArgs_InvalidBindPort(t *testing.T) {
	t.Parallel()

	_, err := king.ParseTunnelArgs(
		[]string{"bind_port=abc ports=5000-5100"},
	)
	if err == nil {
		t.Fatal("expected error for invalid bind_port")
	}
}

// --- BuildSyncTunnels ---

func TestBuildSyncTunnels_Empty(t *testing.T) {
	t.Parallel()

	result := king.BuildSyncTunnels(nil)
	if len(result) != 0 {
		t.Fatalf(
			"expected 0 sync tunnels, got %d", len(result),
		)
	}
}

func TestBuildSyncTunnels_Single(t *testing.T) {
	t.Parallel()

	result := king.BuildSyncTunnels([]king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
	})

	if len(result) != 1 {
		t.Fatalf(
			"expected 1 sync tunnel, got %d", len(result),
		)
	}

	if result[0].BindPort != testBindPort {
		t.Fatalf(
			"expected bind_port 2333, got %d",
			result[0].BindPort,
		)
	}

	if result[0].Ports != testPortRange1 {
		t.Fatalf(
			"expected ports 5000-5100, got %s",
			result[0].Ports,
		)
	}
}

func TestBuildSyncTunnels_Multiple(t *testing.T) {
	t.Parallel()

	result := king.BuildSyncTunnels([]king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
		{BindPort: testBindPort2, Ports: testPortRange2},
	})

	if len(result) != 2 {
		t.Fatalf(
			"expected 2 sync tunnels, got %d", len(result),
		)
	}

	if result[1].BindPort != testBindPort2 {
		t.Fatalf(
			"expected second bind_port 2334, got %d",
			result[1].BindPort,
		)
	}
}

// --- ComputeReadyServiceIDs ---

func TestComputeReadyServiceIDs_MatchingService(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnelSrv.SetQUICConn("svc-1", nil)

	currentState := newTestState(
		[]state.Service{
			newTestService(
				"svc-1", "ling-1",
				new(string), new(int),
			),
		},
		[]state.Ling{newTestLing("ling-1", false)},
	)

	*currentState.Services[0].Host = testHost
	*currentState.Services[0].BindPort = testBindPort

	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
	}

	ids := king.ComputeReadyServiceIDs(
		currentState, tunnels, tunnelConfigs, testHost,
	)

	if len(ids) != 1 {
		t.Fatalf(
			"expected 1 ready service, got %d", len(ids),
		)
	}

	if ids[0] != "svc-1" {
		t.Fatalf("expected svc-1, got %s", ids[0])
	}
}

func TestComputeReadyServiceIDs_DifferentHost(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnelSrv.SetQUICConn("svc-1", nil)

	currentState := newTestState(
		[]state.Service{
			newTestService(
				"svc-1", "ling-1",
				new(string), new(int),
			),
		},
		[]state.Ling{newTestLing("ling-1", false)},
	)

	*currentState.Services[0].Host = testDifferentHost
	*currentState.Services[0].BindPort = testBindPort

	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
	}

	ids := king.ComputeReadyServiceIDs(
		currentState, tunnels, tunnelConfigs, testHost,
	)

	if len(ids) != 0 {
		t.Fatalf(
			"expected 0 ready services (different host), got %d",
			len(ids),
		)
	}
}

func TestComputeReadyServiceIDs_NilHostAndBindPort(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)

	currentState := newTestState(
		[]state.Service{
			newTestService(
				"svc-1", "ling-1", nil, nil,
			),
		},
		[]state.Ling{newTestLing("ling-1", false)},
	)

	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
	}

	ids := king.ComputeReadyServiceIDs(
		currentState, tunnels, tunnelConfigs, testHost,
	)

	if len(ids) != 0 {
		t.Fatalf(
			"expected 0 ready services (nil host/bind_port), got %d",
			len(ids),
		)
	}
}

func TestComputeReadyServiceIDs_MissingLing(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnelSrv.SetQUICConn("svc-1", nil)

	currentState := newTestState(
		[]state.Service{
			newTestService(
				"svc-1", "ling-1",
				new(string), new(int),
			),
		},
		[]state.Ling{},
	)

	*currentState.Services[0].Host = testHost
	*currentState.Services[0].BindPort = testBindPort

	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
	}

	ids := king.ComputeReadyServiceIDs(
		currentState, tunnels, tunnelConfigs, testHost,
	)

	if len(ids) != 0 {
		t.Fatalf(
			"expected 0 ready services (missing ling), got %d",
			len(ids),
		)
	}
}

func TestComputeReadyServiceIDs_ShuttingDownLing(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnelSrv.SetQUICConn("svc-1", nil)

	currentState := newTestState(
		[]state.Service{
			newTestService(
				"svc-1", "ling-1",
				new(string), new(int),
			),
		},
		[]state.Ling{newTestLing("ling-1", true)},
	)

	*currentState.Services[0].Host = testHost
	*currentState.Services[0].BindPort = testBindPort

	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
	}

	ids := king.ComputeReadyServiceIDs(
		currentState, tunnels, tunnelConfigs, testHost,
	)

	if len(ids) != 0 {
		t.Fatalf(
			"expected 0 ready services (shutting down ling), got %d",
			len(ids),
		)
	}
}

func TestComputeReadyServiceIDs_NoQUICConnection(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)

	currentState := newTestState(
		[]state.Service{
			newTestService(
				"svc-1", "ling-1",
				new(string), new(int),
			),
		},
		[]state.Ling{newTestLing("ling-1", false)},
	)

	*currentState.Services[0].Host = testHost
	*currentState.Services[0].BindPort = testBindPort

	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
	}

	ids := king.ComputeReadyServiceIDs(
		currentState, tunnels, tunnelConfigs, testHost,
	)

	if len(ids) != 0 {
		t.Fatalf(
			"expected 0 ready services (no QUIC connection), got %d",
			len(ids),
		)
	}
}

func TestComputeReadyServiceIDs_NonMatchingTunnel(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnelSrv.SetQUICConn("svc-1", nil)

	currentState := newTestState(
		[]state.Service{
			newTestService(
				"svc-1", "ling-1",
				new(string), new(int),
			),
		},
		[]state.Ling{newTestLing("ling-1", false)},
	)

	*currentState.Services[0].Host = testHost
	*currentState.Services[0].BindPort = testNonMatchPort

	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
	}

	ids := king.ComputeReadyServiceIDs(
		currentState, tunnels, tunnelConfigs, testHost,
	)

	if len(ids) != 0 {
		t.Fatalf(
			"expected 0 ready services (bind_port mismatch), got %d",
			len(ids),
		)
	}
}

func TestComputeReadyServiceIDs_MultipleServicesPartialReady(
	t *testing.T,
) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnelSrv.SetQUICConn("svc-1", nil)

	currentState := newTestState(
		[]state.Service{
			newTestService(
				"svc-1", "ling-1",
				new(string), new(int),
			),
			newTestService(
				"svc-2", "ling-2",
				new(string), new(int),
			),
		},
		[]state.Ling{
			newTestLing("ling-1", false),
			newTestLing("ling-2", false),
		},
	)

	*currentState.Services[0].Host = testHost
	*currentState.Services[0].BindPort = testBindPort
	*currentState.Services[1].Host = testHost
	*currentState.Services[1].BindPort = testBindPort

	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
	}

	ids := king.ComputeReadyServiceIDs(
		currentState, tunnels, tunnelConfigs, testHost,
	)

	if len(ids) != 1 {
		t.Fatalf(
			"expected 1 ready service, got %d", len(ids),
		)
	}

	if ids[0] != "svc-1" {
		t.Fatalf("expected svc-1, got %s", ids[0])
	}
}

func TestComputeReadyServiceIDs_EmptyState(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	currentState := newTestState(nil, nil)

	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange1},
	}

	ids := king.ComputeReadyServiceIDs(
		currentState, tunnels, tunnelConfigs, testHost,
	)

	if len(ids) != 0 {
		t.Fatalf(
			"expected 0 ready services (empty state), got %d",
			len(ids),
		)
	}
}

// --- OnStateChanged ---

func TestOnStateChanged_UpdatesServicesMap(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange3},
	}

	currentState := newTestState(
		[]state.Service{
			newTestServiceWithToken(
				"svc-1", "ling-1", "tok-1",
				new(string), new(int), new(int),
			),
		},
		[]state.Ling{newTestLing("ling-1", false)},
	)

	*currentState.Services[0].Host = testHost
	*currentState.Services[0].BindPort = testBindPort
	*currentState.Services[0].RemotePort = testRemotePort1

	king.OnStateChanged(
		t.Context(), currentState,
		tunnels, tunnelConfigs, testHost,
	)

	auth, exists := tunnelSrv.GetServiceAuth("svc-1")
	if !exists {
		t.Fatal("expected svc-1 in services map")
	}

	if auth.Token != "tok-1" {
		t.Fatalf("expected token tok-1, got %s", auth.Token)
	}
}

func TestOnStateChanged_ReassignsReusedListenerPort(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange10},
	}

	for _, serviceID := range []string{"svc-old", "svc-new"} {
		host := testHost
		bindPort := testBindPort
		remotePort := testRemotePort7

		currentState := newTestState(
			[]state.Service{
				newTestServiceWithToken(
					serviceID, "ling-1", "tok-1",
					&host, &bindPort, &remotePort,
				),
			},
			[]state.Ling{newTestLing("ling-1", false)},
		)

		king.OnStateChanged(
			t.Context(), currentState,
			tunnels, tunnelConfigs, testHost,
		)
	}

	if tunnelSrv.TCPServiceID(testRemotePort7) != "svc-new" {
		t.Fatalf(
			"expected port to route to svc-new, got %s",
			tunnelSrv.TCPServiceID(testRemotePort7),
		)
	}
}

func TestOnStateChanged_ExcludesShuttingDownLing(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange4},
	}

	currentState := newTestState(
		[]state.Service{
			newTestServiceWithToken(
				"svc-1", "ling-1", "tok-1",
				new(string), new(int), new(int),
			),
		},
		[]state.Ling{newTestLing("ling-1", true)},
	)

	*currentState.Services[0].Host = testHost
	*currentState.Services[0].BindPort = testBindPort
	*currentState.Services[0].RemotePort = testRemotePort2

	king.OnStateChanged(
		t.Context(), currentState,
		tunnels, tunnelConfigs, testHost,
	)

	serviceCount := tunnelSrv.ServiceCount()
	if serviceCount != 0 {
		t.Fatalf(
			"expected 0 services (ling shutting down), got %d",
			serviceCount,
		)
	}
}

func TestOnStateChanged_ExcludesDifferentHost(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange5},
	}

	currentState := newTestState(
		[]state.Service{
			newTestServiceWithToken(
				"svc-1", "ling-1", "tok-1",
				new(string), new(int), new(int),
			),
		},
		[]state.Ling{newTestLing("ling-1", false)},
	)

	*currentState.Services[0].Host = testDifferentHost
	*currentState.Services[0].BindPort = testBindPort
	*currentState.Services[0].RemotePort = testRemotePort3

	king.OnStateChanged(
		t.Context(), currentState,
		tunnels, tunnelConfigs, testHost,
	)

	serviceCount := tunnelSrv.ServiceCount()
	if serviceCount != 0 {
		t.Fatalf(
			"expected 0 services (different host), got %d",
			serviceCount,
		)
	}
}

func TestOnStateChanged_ExcludesNilFields(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange6},
	}

	currentState := newTestState(
		[]state.Service{
			newTestServiceWithToken(
				"svc-1", "ling-1", "tok-1",
				nil, nil, nil,
			),
		},
		[]state.Ling{newTestLing("ling-1", false)},
	)

	king.OnStateChanged(
		t.Context(), currentState,
		tunnels, tunnelConfigs, testHost,
	)

	serviceCount := tunnelSrv.ServiceCount()
	if serviceCount != 0 {
		t.Fatalf(
			"expected 0 services (nil fields), got %d",
			serviceCount,
		)
	}
}

func buildMultiTunnelState() *state.State {
	currentState := newTestState(
		[]state.Service{
			newTestServiceWithToken(
				"svc-1", "ling-1", "tok-1",
				new(string), new(int), new(int),
			),
			newTestServiceWithToken(
				"svc-2", "ling-2", "tok-2",
				new(string), new(int), new(int),
			),
		},
		[]state.Ling{
			newTestLing("ling-1", false),
			newTestLing("ling-2", false),
		},
	)

	*currentState.Services[0].Host = testHost
	*currentState.Services[0].BindPort = testBindPort
	*currentState.Services[0].RemotePort = testRemotePort4
	*currentState.Services[1].Host = testHost
	*currentState.Services[1].BindPort = testBindPort2
	*currentState.Services[1].RemotePort = testRemotePort5

	return currentState
}

func TestOnStateChanged_MultipleTunnels(t *testing.T) {
	t.Parallel()

	tunnelSrv1 := king.NewTunnelServer(testBindPort, nil)
	tunnelSrv2 := king.NewTunnelServer(testBindPort2, nil)

	tunnels := map[int]*king.TunnelServer{
		testBindPort:  tunnelSrv1,
		testBindPort2: tunnelSrv2,
	}

	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange7},
		{BindPort: testBindPort2, Ports: testPortRange8},
	}

	king.OnStateChanged(
		t.Context(), buildMultiTunnelState(),
		tunnels, tunnelConfigs, testHost,
	)

	_, hasSvc1 := tunnelSrv1.GetServiceAuth("svc-1")
	_, hasSvc2InTs1 := tunnelSrv1.GetServiceAuth("svc-2")
	_, hasSvc2 := tunnelSrv2.GetServiceAuth("svc-2")
	_, hasSvc1InTs2 := tunnelSrv2.GetServiceAuth("svc-1")

	if !hasSvc1 {
		t.Fatal("expected svc-1 in ts1 services")
	}

	if hasSvc2InTs1 {
		t.Fatal("svc-2 should not be in ts1")
	}

	if !hasSvc2 {
		t.Fatal("expected svc-2 in ts2 services")
	}

	if hasSvc1InTs2 {
		t.Fatal("svc-1 should not be in ts2")
	}
}

func TestOnStateChanged_ClearsServicesOnEmpty(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnelSrv.SetServiceAuth(
		"svc-old", king.ServiceAuth{Token: "old-tok"},
	)

	tunnels := map[int]*king.TunnelServer{
		testBindPort: tunnelSrv,
	}
	tunnelConfigs := []king.TunnelConfig{
		{BindPort: testBindPort, Ports: testPortRange9},
	}

	currentState := newTestState(
		[]state.Service{
			newTestServiceWithToken(
				"svc-1", "ling-1", "tok-1",
				new(string), new(int), new(int),
			),
		},
		[]state.Ling{newTestLing("ling-1", false)},
	)

	*currentState.Services[0].Host = testHost
	*currentState.Services[0].BindPort = testBindPort
	*currentState.Services[0].RemotePort = testRemotePort6

	king.OnStateChanged(
		t.Context(), currentState,
		tunnels, tunnelConfigs, testHost,
	)

	serviceCount := tunnelSrv.ServiceCount()
	if serviceCount != 1 {
		t.Fatalf(
			"expected 1 service after first call, got %d",
			serviceCount,
		)
	}

	emptyState := newTestState(nil, nil)

	king.OnStateChanged(
		t.Context(), emptyState,
		tunnels, tunnelConfigs, testHost,
	)

	serviceCount = tunnelSrv.ServiceCount()
	if serviceCount != 0 {
		t.Fatalf(
			"expected 0 services after clearing, got %d",
			serviceCount,
		)
	}
}

// --- WaitForDrain ---

func TestWaitForDrain_EmptyImmediate(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)

	done := make(chan struct{})

	go func() {
		tunnelSrv.WaitForDrain(t.Context())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waitForDrain should return immediately when no connections")
	}
}

func TestWaitForDrain_WaitsForRemoval(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnelSrv.SetQUICConn("svc-1", nil)

	done := make(chan struct{})

	go func() {
		tunnelSrv.WaitForDrain(t.Context())
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("waitForDrain should not return while connections exist")
	case <-time.After(50 * time.Millisecond):
	}

	tunnelSrv.RemoveQUICConn("svc-1")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waitForDrain should return after connection removed")
	}
}

func TestWaitForDrain_RespectsContextTimeout(t *testing.T) {
	t.Parallel()

	tunnelSrv := king.NewTunnelServer(testBindPort, nil)
	tunnelSrv.SetQUICConn("svc-1", nil)

	ctx, cancel := context.WithTimeout(
		t.Context(), 100*time.Millisecond,
	)
	defer cancel()

	done := make(chan struct{})

	go func() {
		tunnelSrv.WaitForDrain(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waitForDrain should return on context cancellation")
	}

	if tunnelSrv.QUICConnCount() != 1 {
		t.Fatal("connection should still exist after timeout")
	}
}

// --- Syncer.TriggerSync ---

func TestTriggerSync_SendsNotification(t *testing.T) {
	t.Parallel()

	syncerInstance := king.NewSyncer(
		"", "", "", "",
		nil,
		make(chan struct{}, 1),
		&sync.Mutex{},
		nil, nil,
	)

	syncerInstance.TriggerSync()

	select {
	case <-syncerInstance.Notify():
	default:
		t.Fatal(
			"expected notification on notify channel",
		)
	}
}

func TestTriggerSync_DoesNotBlockWhenFull(t *testing.T) {
	t.Parallel()

	notifyChan := make(chan struct{}, 1)

	syncerInstance := king.NewSyncer(
		"", "", "", "",
		nil,
		notifyChan,
		&sync.Mutex{},
		nil, nil,
	)

	notifyChan <- struct{}{}

	syncerInstance.TriggerSync()

	<-notifyChan

	select {
	case <-notifyChan:
		t.Fatal(
			"expected only one notification in channel",
		)
	default:
	}
}

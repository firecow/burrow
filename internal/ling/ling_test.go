package ling_test

import (
	"context"
	"testing"

	"github.com/firecow/burrow/internal/ling"
	"github.com/firecow/burrow/internal/state"
)

const (
	testKingHost     = "10.0.0.1"
	testKingBindPort = 5000
	testRemotePort   = 12345
	testLocalAddr    = "127.0.0.1:8080"
	testProxyName    = "myproxy"
	testServiceID    = "svc-1"
	testTunnelName   = "web"
	testLingID       = "ling-a"
)

func TestParseTunnelArgs_Valid(t *testing.T) {
	t.Parallel()

	args := []string{"name=myservice local_addr=127.0.0.1:8080"}

	configs, err := ling.ParseTunnelArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}

	if configs[0].Name != "myservice" {
		t.Errorf("expected name=myservice, got %s", configs[0].Name)
	}

	if configs[0].LocalAddr != testLocalAddr {
		t.Errorf("expected local_addr=127.0.0.1:8080, got %s", configs[0].LocalAddr)
	}
}

func TestParseTunnelArgs_Multiple(t *testing.T) {
	t.Parallel()

	args := []string{
		"name=svc1 local_addr=127.0.0.1:8080",
		"name=svc2 local_addr=127.0.0.1:9090",
	}

	configs, err := ling.ParseTunnelArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}

	if configs[0].Name != "svc1" || configs[1].Name != "svc2" {
		t.Errorf("unexpected names: %s, %s", configs[0].Name, configs[1].Name)
	}
}

func TestParseTunnelArgs_MissingName(t *testing.T) {
	t.Parallel()

	args := []string{"local_addr=127.0.0.1:8080"}

	_, err := ling.ParseTunnelArgs(args)
	if err == nil {
		t.Fatal("expected error for missing name")
	}

	expected := "--tunnel must have 'name' field"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestParseTunnelArgs_MissingLocalAddr(t *testing.T) {
	t.Parallel()

	args := []string{"name=myservice"}

	_, err := ling.ParseTunnelArgs(args)
	if err == nil {
		t.Fatal("expected error for missing local_addr")
	}

	expected := "--tunnel must have 'local_addr' field"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestParseTunnelArgs_Empty(t *testing.T) {
	t.Parallel()

	configs, err := ling.ParseTunnelArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if configs != nil {
		t.Errorf("expected nil configs for nil args, got %v", configs)
	}
}

func TestParseProxyArgs_Valid(t *testing.T) {
	t.Parallel()

	args := []string{"name=myproxy bind_port=3306"}

	configs, err := ling.ParseProxyArgs(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}

	if configs[0].Name != testProxyName {
		t.Errorf("expected name=myproxy, got %s", configs[0].Name)
	}

	if configs[0].BindPort != 3306 {
		t.Errorf("expected bind_port=3306, got %d", configs[0].BindPort)
	}
}

func TestParseProxyArgs_MissingName(t *testing.T) {
	t.Parallel()

	args := []string{"bind_port=3306"}

	_, err := ling.ParseProxyArgs(args)
	if err == nil {
		t.Fatal("expected error for missing name")
	}

	expected := "--proxy must have 'name' field"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestParseProxyArgs_MissingBindPort(t *testing.T) {
	t.Parallel()

	args := []string{"name=myproxy"}

	_, err := ling.ParseProxyArgs(args)
	if err == nil {
		t.Fatal("expected error for missing bind_port")
	}

	expected := "--proxy must have 'bind_port' field"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestParseProxyArgs_InvalidBindPort(t *testing.T) {
	t.Parallel()

	args := []string{"name=myproxy bind_port=abc"}

	_, err := ling.ParseProxyArgs(args)
	if err == nil {
		t.Fatal("expected error for invalid bind_port")
	}
}

func TestParseProxyArgs_Empty(t *testing.T) {
	t.Parallel()

	configs, err := ling.ParseProxyArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if configs != nil {
		t.Errorf("expected nil configs for nil args, got %v", configs)
	}
}

func TestComputeReadyServiceIDs_MatchingLing(t *testing.T) {
	t.Parallel()

	stateSnapshot := &state.State{
		Revision: 0,
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              testTunnelName,
				Token:             "",
				LingID:            testLingID,
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         true,
				Host:              nil,
				BindPort:          nil,
				RemotePort:        nil,
			},
		},
		Kings: nil,
		Lings: nil,
	}
	tunnelMap := map[string]string{testTunnelName: testLocalAddr}

	ids := ling.ComputeReadyServiceIDs(stateSnapshot, testLingID, tunnelMap)
	if len(ids) != 1 || ids[0] != testServiceID {
		t.Errorf("expected [svc-1], got %v", ids)
	}
}

func TestComputeReadyServiceIDs_NonMatchingLing(t *testing.T) {
	t.Parallel()

	stateSnapshot := &state.State{
		Revision: 0,
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              testTunnelName,
				Token:             "",
				LingID:            "ling-b",
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         true,
				Host:              nil,
				BindPort:          nil,
				RemotePort:        nil,
			},
		},
		Kings: nil,
		Lings: nil,
	}
	tunnelMap := map[string]string{testTunnelName: testLocalAddr}

	ids := ling.ComputeReadyServiceIDs(stateSnapshot, testLingID, tunnelMap)
	if len(ids) != 0 {
		t.Errorf("expected empty, got %v", ids)
	}
}

func TestComputeReadyServiceIDs_MissingTunnelEntry(t *testing.T) {
	t.Parallel()

	stateSnapshot := &state.State{
		Revision: 0,
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              "db",
				Token:             "",
				LingID:            testLingID,
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         true,
				Host:              nil,
				BindPort:          nil,
				RemotePort:        nil,
			},
		},
		Kings: nil,
		Lings: nil,
	}
	tunnelMap := map[string]string{testTunnelName: testLocalAddr}

	ids := ling.ComputeReadyServiceIDs(stateSnapshot, testLingID, tunnelMap)
	if len(ids) != 0 {
		t.Errorf("expected empty, got %v", ids)
	}
}

func TestComputeReadyServiceIDs_KingNotReady(t *testing.T) {
	t.Parallel()

	stateSnapshot := &state.State{
		Revision: 0,
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              testTunnelName,
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
		Kings: nil,
		Lings: nil,
	}
	tunnelMap := map[string]string{testTunnelName: testLocalAddr}

	ids := ling.ComputeReadyServiceIDs(stateSnapshot, testLingID, tunnelMap)
	if len(ids) != 0 {
		t.Errorf("expected empty, got %v", ids)
	}
}

func newTestService(
	serviceID, name, lingID string,
	kingReady bool,
) state.Service {
	return state.Service{
		ServiceID:         serviceID,
		Name:              name,
		Token:             "",
		LingID:            lingID,
		PreferredLocation: "",
		LingReady:         false,
		KingReady:         kingReady,
		Host:              nil,
		BindPort:          nil,
		RemotePort:        nil,
	}
}

func buildMultipleServicesState() *state.State {
	return &state.State{
		Revision: 0,
		Services: []state.Service{
			newTestService(testServiceID, testTunnelName, testLingID, true),
			newTestService("svc-2", "api", testLingID, true),
			newTestService("svc-3", testTunnelName, "ling-b", true),
			newTestService("svc-4", "db", testLingID, true),
			newTestService("svc-5", testTunnelName, testLingID, false),
		},
		Kings: nil,
		Lings: nil,
	}
}

func TestComputeReadyServiceIDs_MultipleServices(t *testing.T) {
	t.Parallel()

	stateSnapshot := buildMultipleServicesState()

	tunnelMap := map[string]string{
		testTunnelName: testLocalAddr,
		"api":          "127.0.0.1:9090",
	}

	ids := ling.ComputeReadyServiceIDs(stateSnapshot, testLingID, tunnelMap)
	if len(ids) != 2 {
		t.Fatalf("expected 2 ready IDs, got %d: %v", len(ids), ids)
	}

	idSet := map[string]bool{}

	for _, identifier := range ids {
		idSet[identifier] = true
	}

	if !idSet[testServiceID] || !idSet["svc-2"] {
		t.Errorf("expected svc-1 and svc-2, got %v", ids)
	}
}

type stateChangedTestCase struct {
	name             string
	kingShuttingDown bool
	lingShuttingDown bool
	lingReady        bool
	kingReady        bool
	hostNil          bool
	expectedTargets  int
}

func buildStateChangedState(testCase stateChangedTestCase) *state.State {
	kingHost := testKingHost
	kingBindPort := testKingBindPort
	remotePort := testRemotePort

	var hostPtr *string

	var bindPortPtr *int

	var remotePortPtr *int

	if !testCase.hostNil {
		hostPtr = &kingHost
		bindPortPtr = &kingBindPort
		remotePortPtr = &remotePort
	}

	return &state.State{
		Revision: 0,
		Kings: []state.King{
			{
				BindPort:     kingBindPort,
				Host:         kingHost,
				Ports:        "",
				ShuttingDown: testCase.kingShuttingDown,
				Beat:         0,
				Location:     "",
				CertPEM:      "",
			},
		},
		Lings: []state.Ling{
			{
				LingID:       testLingID,
				ShuttingDown: testCase.lingShuttingDown,
				Beat:         0,
			},
		},
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              testProxyName,
				Token:             "",
				LingID:            testLingID,
				PreferredLocation: "",
				LingReady:         testCase.lingReady,
				KingReady:         testCase.kingReady,
				Host:              hostPtr,
				BindPort:          bindPortPtr,
				RemotePort:        remotePortPtr,
			},
		},
	}
}

func TestOnStateChanged_UpdatesProxyTargets(t *testing.T) {
	t.Parallel()

	stateSnapshot := buildStateChangedState(stateChangedTestCase{
		name:             "updates proxy targets",
		kingShuttingDown: false,
		lingShuttingDown: false,
		lingReady:        true,
		kingReady:        true,
		hostNil:          false,
		expectedTargets:  1,
	})

	proxy := ling.NewTCPProxy(testProxyName, 0)
	tcpProxies := map[string]*ling.TCPProxy{testProxyName: proxy}
	tunnelCli := ling.NewTunnelClient()

	ling.OnStateChanged(
		context.Background(), stateSnapshot,
		"other-ling", "", map[string]string{}, tunnelCli, tcpProxies,
	)

	targets := proxy.ReadTargets()

	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
}

func TestOnStateChanged_IncludesShuttingDownLing(t *testing.T) {
	t.Parallel()

	stateSnapshot := buildStateChangedState(stateChangedTestCase{
		name:             "includes shutting down ling with ready state",
		kingShuttingDown: false,
		lingShuttingDown: true,
		lingReady:        true,
		kingReady:        true,
		hostNil:          false,
		expectedTargets:  1,
	})

	proxy := ling.NewTCPProxy(testProxyName, 0)
	tcpProxies := map[string]*ling.TCPProxy{testProxyName: proxy}
	tunnelCli := ling.NewTunnelClient()

	ling.OnStateChanged(
		context.Background(), stateSnapshot,
		"other-ling", "", map[string]string{}, tunnelCli, tcpProxies,
	)

	targets := proxy.ReadTargets()

	if len(targets) != 1 {
		t.Errorf("expected 1 target (ling shutting down but still ready), got %d", len(targets))
	}
}

func TestOnStateChanged_ExcludesShuttingDownKing(t *testing.T) {
	t.Parallel()

	stateSnapshot := buildStateChangedState(stateChangedTestCase{
		name:             "excludes shutting down king",
		kingShuttingDown: true,
		lingShuttingDown: false,
		lingReady:        true,
		kingReady:        true,
		hostNil:          false,
		expectedTargets:  0,
	})

	proxy := ling.NewTCPProxy(testProxyName, 0)
	tcpProxies := map[string]*ling.TCPProxy{testProxyName: proxy}
	tunnelCli := ling.NewTunnelClient()

	ling.OnStateChanged(
		context.Background(), stateSnapshot,
		"other-ling", "", map[string]string{}, tunnelCli, tcpProxies,
	)

	targets := proxy.ReadTargets()

	if len(targets) != 0 {
		t.Errorf("expected 0 targets (king shutting down), got %d", len(targets))
	}
}

func TestOnStateChanged_ExcludesNotReady(t *testing.T) {
	t.Parallel()

	stateSnapshot := buildStateChangedState(stateChangedTestCase{
		name:             "excludes not ready",
		kingShuttingDown: false,
		lingShuttingDown: false,
		lingReady:        false,
		kingReady:        true,
		hostNil:          false,
		expectedTargets:  0,
	})

	proxy := ling.NewTCPProxy(testProxyName, 0)
	tcpProxies := map[string]*ling.TCPProxy{testProxyName: proxy}
	tunnelCli := ling.NewTunnelClient()

	ling.OnStateChanged(
		context.Background(), stateSnapshot,
		"other-ling", "", map[string]string{}, tunnelCli, tcpProxies,
	)

	targets := proxy.ReadTargets()

	if len(targets) != 0 {
		t.Errorf("expected 0 targets (ling not ready), got %d", len(targets))
	}
}

func TestOnStateChanged_ExcludesMissingHostOrPort(t *testing.T) {
	t.Parallel()

	stateSnapshot := buildStateChangedState(stateChangedTestCase{
		name:             "excludes missing host/port",
		kingShuttingDown: false,
		lingShuttingDown: false,
		lingReady:        true,
		kingReady:        true,
		hostNil:          true,
		expectedTargets:  0,
	})

	proxy := ling.NewTCPProxy(testProxyName, 0)
	tcpProxies := map[string]*ling.TCPProxy{testProxyName: proxy}
	tunnelCli := ling.NewTunnelClient()

	ling.OnStateChanged(
		context.Background(), stateSnapshot,
		"other-ling", "", map[string]string{}, tunnelCli, tcpProxies,
	)

	targets := proxy.ReadTargets()

	if len(targets) != 0 {
		t.Errorf("expected 0 targets (nil host/port), got %d", len(targets))
	}
}

func buildLocationState() *state.State {
	hostA := "10.0.0.1"
	hostB := "10.0.0.2"
	bindPortA := 5000
	bindPortB := 5001
	remotePortA := 12345
	remotePortB := 12346

	return &state.State{
		Revision: 0,
		Kings: []state.King{
			{
				BindPort:     bindPortA,
				Host:         hostA,
				Ports:        "",
				ShuttingDown: false,
				Beat:         0,
				Location:     "eu-west",
				CertPEM:      "",
			},
			{
				BindPort:     bindPortB,
				Host:         hostB,
				Ports:        "",
				ShuttingDown: false,
				Beat:         0,
				Location:     "us-east",
				CertPEM:      "",
			},
		},
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              testProxyName,
				Token:             "",
				LingID:            testLingID,
				PreferredLocation: "",
				LingReady:         true,
				KingReady:         true,
				Host:              &hostA,
				BindPort:          &bindPortA,
				RemotePort:        &remotePortA,
			},
			{
				ServiceID:         "svc-2",
				Name:              testProxyName,
				Token:             "",
				LingID:            "ling-b",
				PreferredLocation: "",
				LingReady:         true,
				KingReady:         true,
				Host:              &hostB,
				BindPort:          &bindPortB,
				RemotePort:        &remotePortB,
			},
		},
		Lings: nil,
	}
}

func TestOnStateChanged_PrefersLocationTargets(t *testing.T) {
	t.Parallel()

	stateSnapshot := buildLocationState()

	proxy := ling.NewTCPProxy(testProxyName, 0)
	tcpProxies := map[string]*ling.TCPProxy{testProxyName: proxy}
	tunnelCli := ling.NewTunnelClient()

	ling.OnStateChanged(
		context.Background(), stateSnapshot,
		"other-ling", "eu-west", map[string]string{}, tunnelCli, tcpProxies,
	)

	targets := proxy.ReadTargets()

	if len(targets) != 1 {
		t.Fatalf("expected 1 target (preferred location), got %d", len(targets))
	}
}

func TestOnStateChanged_FallsBackWhenNoPreferredLocationTargets(t *testing.T) {
	t.Parallel()

	stateSnapshot := buildLocationState()

	proxy := ling.NewTCPProxy(testProxyName, 0)
	tcpProxies := map[string]*ling.TCPProxy{testProxyName: proxy}
	tunnelCli := ling.NewTunnelClient()

	ling.OnStateChanged(
		context.Background(), stateSnapshot,
		"other-ling", "ap-south", map[string]string{}, tunnelCli, tcpProxies,
	)

	targets := proxy.ReadTargets()

	if len(targets) != 2 {
		t.Fatalf("expected 2 targets (fallback to all), got %d", len(targets))
	}
}

func TestBuildSyncTunnels_ReportsHeldIdentity(t *testing.T) {
	t.Parallel()

	tunnelConfigs := []ling.TunnelConfig{
		{Name: testTunnelName, LocalAddr: testLocalAddr},
		{Name: "unassigned", LocalAddr: testLocalAddr},
	}
	stateSnapshot := &state.State{
		Revision: 1,
		Services: []state.Service{
			{
				Name:              testTunnelName,
				Token:             "tok",
				ServiceID:         testServiceID,
				LingID:            testLingID,
				PreferredLocation: "",
				LingReady:         true,
				KingReady:         true,
				Host:              nil,
				BindPort:          nil,
				RemotePort:        nil,
			},
		},
		Kings: []state.King{},
		Lings: []state.Ling{},
	}

	reported := ling.SyncTunnelServiceIDs(tunnelConfigs, stateSnapshot, testLingID)

	if reported[testTunnelName] != testServiceID || reported["unassigned"] != "" {
		t.Fatalf("expected only %s to carry its service ID, got %v", testTunnelName, reported)
	}
}

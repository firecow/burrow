package council_test

import (
	"testing"

	"github.com/firecow/burrow/internal/council"
	"github.com/firecow/burrow/internal/state"
)

func newTestKing(host, ports string, bindPort int, shuttingDown bool) state.King {
	return state.King{
		BindPort:     bindPort,
		Host:         host,
		Ports:        ports,
		ShuttingDown: shuttingDown,
		Beat:         0,
		Location:     "",
		CertPEM:      "",
	}
}

func newEmptyService(serviceID, name string) state.Service {
	return state.Service{
		ServiceID:         serviceID,
		Name:              name,
		Token:             "",
		LingID:            "",
		PreferredLocation: "",
		LingReady:         false,
		KingReady:         false,
		Host:              nil,
		BindPort:          nil,
		RemotePort:        nil,
	}
}

func TestAvailableKingPorts_NoKings(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Services: nil,
		Kings:    nil,
		Lings:    nil,
	}

	result := council.AvailableKingPorts(currentState)

	if len(result) != 0 {
		t.Fatalf("expected 0 available king ports, got %d", len(result))
	}
}

func TestAvailableKingPorts_ShuttingDownExcluded(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Services: nil,
		Kings: []state.King{
			{
				BindPort:     2333,
				Host:         testHostA,
				Ports:        testPorts,
				ShuttingDown: true,
				Beat:         0,
				Location:     "",
				CertPEM:      "",
			},
		},
		Lings: nil,
	}

	result := council.AvailableKingPorts(currentState)

	if len(result) != 0 {
		t.Fatalf(
			"expected 0 (shutting down king excluded), got %d",
			len(result),
		)
	}
}

func TestAvailableKingPorts_AllPortsFree(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Services: nil,
		Kings: []state.King{
			{
				BindPort:     2333,
				Host:         testHostA,
				Ports:        "5000-5002",
				ShuttingDown: false,
				Beat:         0,
				Location:     "",
				CertPEM:      "",
			},
		},
		Lings: nil,
	}

	result := council.AvailableKingPorts(currentState)

	if len(result) != 1 {
		t.Fatalf("expected 1 king, got %d", len(result))
	}

	if len(result[0].Ports) != 3 {
		t.Fatalf("expected 3 ports, got %d", len(result[0].Ports))
	}

	if result[0].Ports[0] != 5000 ||
		result[0].Ports[1] != 5001 ||
		result[0].Ports[2] != 5002 {
		t.Fatalf("unexpected ports: %v", result[0].Ports)
	}
}

func TestAvailableKingPorts_SomePortsUsed(t *testing.T) {
	t.Parallel()

	host := testHostA
	bindPort := 2333
	remotePort := 5000
	currentState := &state.State{
		Revision: 0,
		Kings: []state.King{
			{
				BindPort:     bindPort,
				Host:         host,
				Ports:        "5000-5002",
				ShuttingDown: false,
				Beat:         0,
				Location:     "",
				CertPEM:      "",
			},
		},
		Services: []state.Service{
			{
				ServiceID:         "",
				Name:              "",
				Token:             "",
				LingID:            "",
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              &host,
				BindPort:          &bindPort,
				RemotePort:        &remotePort,
			},
		},
		Lings: nil,
	}

	result := council.AvailableKingPorts(currentState)

	if len(result) != 1 {
		t.Fatalf("expected 1 king, got %d", len(result))
	}

	if len(result[0].Ports) != 2 {
		t.Fatalf("expected 2 free ports, got %d", len(result[0].Ports))
	}

	if result[0].Ports[0] != 5001 || result[0].Ports[1] != 5002 {
		t.Fatalf("unexpected ports: %v", result[0].Ports)
	}
}

func TestAvailableKingPorts_AllPortsUsed(t *testing.T) {
	t.Parallel()

	host := testHostA
	bindPort := 2333
	port5000 := 5000
	currentState := &state.State{
		Revision: 0,
		Kings: []state.King{
			{
				BindPort:     bindPort,
				Host:         host,
				Ports:        "5000-5000",
				ShuttingDown: false,
				Beat:         0,
				Location:     "",
				CertPEM:      "",
			},
		},
		Services: []state.Service{
			{
				ServiceID:         "",
				Name:              "",
				Token:             "",
				LingID:            "",
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              &host,
				BindPort:          &bindPort,
				RemotePort:        &port5000,
			},
		},
		Lings: nil,
	}

	result := council.AvailableKingPorts(currentState)

	if len(result) != 0 {
		t.Fatalf("expected 0 (all ports used), got %d", len(result))
	}
}

func TestProvisionService_AssignsFirstAvailable(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Kings: []state.King{
			{
				BindPort:     2333,
				Host:         testHostA,
				Ports:        testPorts,
				ShuttingDown: false,
				Beat:         0,
				Location:     "",
				CertPEM:      "",
			},
		},
		Services: []state.Service{
			{
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
			},
		},
		Lings: nil,
	}

	council.ProvisionService(currentState, &currentState.Services[0])

	svc := currentState.Services[0]

	if svc.Host == nil || *svc.Host != testHostA {
		t.Fatalf("expected host %s, got %v", testHostA, svc.Host)
	}

	if svc.BindPort == nil || *svc.BindPort != 2333 {
		t.Fatalf("expected bind_port 2333, got %v", svc.BindPort)
	}

	if svc.RemotePort == nil || *svc.RemotePort != 5000 {
		t.Fatalf("expected remote_port 5000, got %v", svc.RemotePort)
	}

	if currentState.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", currentState.Revision)
	}
}

func TestProvision_MultipleUnprovisioned(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Kings:    []state.King{newTestKing(testHostA, testPorts, 2333, false)},
		Services: []state.Service{
			newEmptyService(testServiceID, testServiceName),
			newEmptyService("svc-2", "beta"),
		},
		Lings: nil,
	}

	council.Provision(currentState)

	if currentState.Services[0].RemotePort == nil ||
		*currentState.Services[0].RemotePort != 5000 {
		t.Fatalf(
			"svc-1: expected remote_port 5000, got %v",
			currentState.Services[0].RemotePort,
		)
	}

	if currentState.Services[1].RemotePort == nil ||
		*currentState.Services[1].RemotePort != 5001 {
		t.Fatalf(
			"svc-2: expected remote_port 5001, got %v",
			currentState.Services[1].RemotePort,
		)
	}
}

func TestProvision_NoKingsNoAssignment(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Kings:    nil,
		Lings:    nil,
		Services: []state.Service{
			{
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
			},
		},
	}

	council.Provision(currentState)

	if currentState.Services[0].Host != nil {
		t.Fatalf("expected nil host, got %v", currentState.Services[0].Host)
	}
}

func TestProvision_SkipsAlreadyProvisioned(t *testing.T) {
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
				Ports:        testPorts,
				ShuttingDown: false,
				Beat:         0,
				Location:     "",
				CertPEM:      "",
			},
		},
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              testServiceName,
				Token:             "",
				LingID:            "",
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              &host,
				BindPort:          &bindPort,
				RemotePort:        &remotePort,
			},
			{
				ServiceID:         "svc-2",
				Name:              "beta",
				Token:             "",
				LingID:            "",
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              nil,
				BindPort:          nil,
				RemotePort:        nil,
			},
		},
		Lings: nil,
	}

	council.Provision(currentState)

	if *currentState.Services[0].RemotePort != 5000 {
		t.Fatalf("svc-1 should still be on 5000")
	}

	if currentState.Services[1].RemotePort == nil ||
		*currentState.Services[1].RemotePort != 5001 {
		t.Fatalf(
			"svc-2: expected remote_port 5001, got %v",
			currentState.Services[1].RemotePort,
		)
	}
}

func TestProvision_DeprovisionsFromShuttingDownKing(t *testing.T) {
	t.Parallel()

	host := testHostA
	bindPort := 2333
	remotePort := 5000
	currentState := &state.State{
		Revision: 0,
		Kings: []state.King{
			newTestKing(testHostA, testPorts, 2333, true),
			newTestKing(testHostB, "6000-6001", 2334, false),
		},
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              testServiceName,
				Token:             "",
				LingID:            "",
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         true,
				Host:              &host,
				BindPort:          &bindPort,
				RemotePort:        &remotePort,
			},
		},
		Lings: nil,
	}

	council.Provision(currentState)

	svc := currentState.Services[0]
	assertServiceHost(t, svc, testHostB)
	assertServiceBindPort(t, svc, 2334)
	assertServiceRemotePort(t, svc, 6000)

	if svc.KingReady {
		t.Fatalf("expected KingReady to be false after deprovisioning")
	}
}

func assertServiceHost(t *testing.T, svc state.Service, expected string) {
	t.Helper()

	if svc.Host == nil || *svc.Host != expected {
		t.Fatalf("expected host %s, got %v", expected, svc.Host)
	}
}

func assertServiceBindPort(t *testing.T, svc state.Service, expected int) {
	t.Helper()

	if svc.BindPort == nil || *svc.BindPort != expected {
		t.Fatalf("expected bind_port %d, got %v", expected, svc.BindPort)
	}
}

func assertServiceRemotePort(t *testing.T, svc state.Service, expected int) {
	t.Helper()

	if svc.RemotePort == nil || *svc.RemotePort != expected {
		t.Fatalf("expected remote_port %d, got %v", expected, svc.RemotePort)
	}
}

func TestProvision_DeprovisionsAndReprovisions(t *testing.T) {
	t.Parallel()

	hostA := testHostA
	bindPortA := 2333
	remotePortA := 5000
	hostB := testHostB
	bindPortB := 2334
	remotePortB := 6000
	currentState := &state.State{
		Revision: 0,
		Kings: []state.King{
			newTestKing(testHostA, testPorts, 2333, true),
			newTestKing(testHostB, "6000-6001", 2334, false),
		},
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              testServiceName,
				Token:             "",
				LingID:            "",
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              &hostA,
				BindPort:          &bindPortA,
				RemotePort:        &remotePortA,
			},
			{
				ServiceID:         "svc-2",
				Name:              "beta",
				Token:             "",
				LingID:            "",
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              &hostB,
				BindPort:          &bindPortB,
				RemotePort:        &remotePortB,
			},
		},
		Lings: nil,
	}

	council.Provision(currentState)

	svc1 := currentState.Services[0]
	assertServiceHost(t, svc1, testHostB)
	assertServiceBindPort(t, svc1, 2334)
	assertServiceRemotePort(t, svc1, 6001)

	svc2 := currentState.Services[1]
	assertServiceHost(t, svc2, testHostB)
	assertServiceRemotePort(t, svc2, 6000)
}

func TestProvision_NoReprovisioning_WhenNoAvailableKing(t *testing.T) {
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
				Ports:        testPorts,
				ShuttingDown: true,
				Beat:         0,
				Location:     "",
				CertPEM:      "",
			},
		},
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              testServiceName,
				Token:             "",
				LingID:            "",
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         true,
				Host:              &host,
				BindPort:          &bindPort,
				RemotePort:        &remotePort,
			},
		},
		Lings: nil,
	}

	council.Provision(currentState)

	svc := currentState.Services[0]

	if svc.Host != nil {
		t.Fatalf("expected nil host (unprovisioned), got %v", svc.Host)
	}

	if svc.BindPort != nil {
		t.Fatalf("expected nil bind_port, got %v", svc.BindPort)
	}

	if svc.RemotePort != nil {
		t.Fatalf("expected nil remote_port, got %v", svc.RemotePort)
	}

	if svc.KingReady {
		t.Fatalf("expected KingReady to be false")
	}
}

func newTestKingWithLocation(
	host, ports string, bindPort int, location string,
) state.King {
	return state.King{
		BindPort:     bindPort,
		Host:         host,
		Ports:        ports,
		ShuttingDown: false,
		Beat:         0,
		Location:     location,
		CertPEM:      "",
	}
}

func TestProvisionService_PrefersMatchingLocation(t *testing.T) {
	t.Parallel()

	currentState := &state.State{
		Revision: 0,
		Kings: []state.King{
			newTestKingWithLocation(testHostA, testPorts, 2333, "us-east"),
			newTestKingWithLocation(testHostB, "6000-6001", 2334, "eu-west"),
		},
		Services: []state.Service{
			{
				ServiceID:         testServiceID,
				Name:              testServiceName,
				Token:             "",
				LingID:            "",
				PreferredLocation: "eu-west",
				LingReady:         false,
				KingReady:         false,
				Host:              nil,
				BindPort:          nil,
				RemotePort:        nil,
			},
		},
		Lings: nil,
	}

	council.ProvisionService(currentState, &currentState.Services[0])

	svc := currentState.Services[0]
	assertServiceHost(t, svc, testHostB)
	assertServiceBindPort(t, svc, 2334)
	assertServiceRemotePort(t, svc, 6000)
}

func TestProvisionService_FallsBackWhenPreferredLocationFull(t *testing.T) {
	t.Parallel()

	hostB := testHostB
	bindPortB := 2334
	port6000 := 6000

	currentState := &state.State{
		Revision: 0,
		Kings: []state.King{
			newTestKingWithLocation(testHostA, testPorts, 2333, "us-east"),
			newTestKingWithLocation(testHostB, "6000-6000", 2334, "eu-west"),
		},
		Services: []state.Service{
			{
				ServiceID:         "existing",
				Name:              "existing",
				Token:             "",
				LingID:            "",
				PreferredLocation: "",
				LingReady:         false,
				KingReady:         false,
				Host:              &hostB,
				BindPort:          &bindPortB,
				RemotePort:        &port6000,
			},
			{
				ServiceID:         testServiceID,
				Name:              testServiceName,
				Token:             "",
				LingID:            "",
				PreferredLocation: "eu-west",
				LingReady:         false,
				KingReady:         false,
				Host:              nil,
				BindPort:          nil,
				RemotePort:        nil,
			},
		},
		Lings: nil,
	}

	council.ProvisionService(currentState, &currentState.Services[1])

	svc := currentState.Services[1]
	assertServiceHost(t, svc, testHostA)
	assertServiceBindPort(t, svc, 2333)
	assertServiceRemotePort(t, svc, 5000)
}

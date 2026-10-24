// Package king implements the king node that manages QUIC tunnels and syncs state with the council.
package king

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/firecow/burrow/internal/state"
	"github.com/spf13/cobra"
)

const (
	defaultCouncilHost = "http://localhost:8080"
	kvPairCount        = 2
	finalSyncTimeout   = 2 * time.Second
	quicDrainTimeout   = 5 * time.Second
	reconcileInterval  = 1 * time.Second
	certValidityYears  = 10 * 365 * 24 * time.Hour
)

var (
	errNoTunnelsSpecified = errors.New(
		"at least one --tunnel must be specified",
	)
	errMissingBindPort = errors.New(
		"--tunnel must have 'bind_port' field",
	)
	errMissingPorts = errors.New(
		"--tunnel must have 'ports' field",
	)
)

// TunnelConfig holds the configuration for a single QUIC tunnel.
type TunnelConfig struct {
	BindPort int
	Ports    string
}

// Command returns the cobra command for starting a king node.
func Command() *cobra.Command {
	var (
		councilHost string
		host        string
		location    string
		tunnelArgs  []string
	)

	cmd := &cobra.Command{
		Use:   "king",
		Short: "Start king",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tunnelConfigs, err := ParseTunnelArgs(tunnelArgs)
			if err != nil {
				return err
			}

			if len(tunnelConfigs) == 0 {
				return errNoTunnelsSpecified
			}

			return run(
				cmd.Context(), councilHost,
				host, location, tunnelConfigs,
			)
		},
	}

	cmd.Flags().StringVar(
		&councilHost, "council-host",
		defaultCouncilHost, "Council host to synchronize from",
	)
	cmd.Flags().StringVar(
		&host, "host", "", "Host (domain or IP)",
	)
	cmd.Flags().StringVar(
		&location, "location", "", "Location identifier",
	)
	cmd.Flags().StringArrayVar(
		&tunnelArgs, "tunnel", nil,
		"Tunnel servers (bind_port=N ports=M-M)",
	)

	_ = cmd.MarkFlagRequired("host")
	_ = cmd.MarkFlagRequired("location")

	return cmd
}

// ParseTunnelArgs parses CLI tunnel arguments into TunnelConfig slices.
func ParseTunnelArgs(args []string) ([]TunnelConfig, error) {
	configs := make([]TunnelConfig, 0, len(args))

	for _, arg := range args {
		config, err := parseSingleTunnelArg(arg)
		if err != nil {
			return nil, err
		}

		configs = append(configs, config)
	}

	return configs, nil
}

func parseSingleTunnelArg(arg string) (TunnelConfig, error) {
	pairs := make(map[string]string)

	for pair := range strings.SplitSeq(arg, " ") {
		parts := strings.SplitN(pair, "=", kvPairCount)
		if len(parts) == kvPairCount {
			pairs[parts[0]] = parts[1]
		}
	}

	bindPortStr, hasBindPort := pairs["bind_port"]
	if !hasBindPort {
		return TunnelConfig{
			BindPort: 0, Ports: "",
		}, errMissingBindPort
	}

	portsStr, hasPorts := pairs["ports"]
	if !hasPorts {
		return TunnelConfig{
			BindPort: 0, Ports: "",
		}, errMissingPorts
	}

	var bindPort int

	_, err := fmt.Sscanf(bindPortStr, "%d", &bindPort)
	if err != nil {
		return TunnelConfig{
			BindPort: 0, Ports: "",
		}, fmt.Errorf(
			"invalid bind_port %s: %w", bindPortStr, err,
		)
	}

	return TunnelConfig{
		BindPort: bindPort, Ports: portsStr,
	}, nil
}

func run(
	ctx context.Context,
	councilHost, host, location string,
	tunnelConfigs []TunnelConfig,
) error {
	tlsCert, certPEM, err := generateSelfSignedCert()
	if err != nil {
		return fmt.Errorf(
			"failed to generate TLS certificate: %w", err,
		)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"burrow"},
		MinVersion:   tls.VersionTLS13,
	}

	tunnels := makeTunnelServers(tunnelConfigs, tlsConfig)
	syncerInstance := buildSyncer(
		councilHost, host, location, certPEM,
		tunnelConfigs, tunnels,
	)

	for _, tunnelSrv := range tunnels {
		tunnelSrv.onLingConnected = syncerInstance.triggerSync
	}

	watcher := state.NewWatcher(
		councilHost,
		func(newState *state.State) {
			syncerInstance.mutex.Lock()
			syncerInstance.currentState = newState
			syncerInstance.mutex.Unlock()

			OnStateChanged(
				ctx, newState, tunnels, tunnelConfigs, host,
			)
		},
	)

	return runEventLoop(
		ctx, tunnels, watcher, syncerInstance,
	)
}

func makeTunnelServers(
	tunnelConfigs []TunnelConfig,
	tlsConfig *tls.Config,
) map[int]*TunnelServer {
	tunnels := make(map[int]*TunnelServer)

	for _, tunnelCfg := range tunnelConfigs {
		tunnels[tunnelCfg.BindPort] = NewTunnelServer(
			tunnelCfg.BindPort, tlsConfig,
		)
	}

	return tunnels
}

func buildSyncer(
	councilHost, host, location, certPEM string,
	tunnelConfigs []TunnelConfig,
	tunnels map[int]*TunnelServer,
) *Syncer {
	mutex := &sync.Mutex{}

	return NewSyncer(
		councilHost, host, location, certPEM,
		BuildSyncTunnels(tunnelConfigs),
		make(chan struct{}, 1),
		mutex,
		tunnels,
		tunnelConfigs,
	)
}

func runEventLoop(
	ctx context.Context,
	tunnels map[int]*TunnelServer,
	watcher *state.Watcher,
	syncerInstance *Syncer,
) error {
	startTunnelServers(ctx, tunnels)

	watcherCtx, watcherCancel := context.WithCancel(ctx)
	defer watcherCancel()

	go watcher.Run(watcherCtx)

	err := watcher.WaitForState(ctx)
	if err != nil {
		return fmt.Errorf("waiting for initial state: %w", err)
	}

	syncerCtx, syncerCancel := context.WithCancel(ctx)
	defer syncerCancel()

	go syncerInstance.run(syncerCtx)

	slog.Info("Ready")

	reconcileTicker := time.NewTicker(reconcileInterval)
	defer reconcileTicker.Stop()

	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-reconcileTicker.C:
			watcher.Replay()
		}
	}

	performShutdown(
		ctx, tunnels,
		watcherCancel, syncerCancel,
		syncerInstance,
	)

	return nil
}

func performShutdown(
	ctx context.Context,
	tunnels map[int]*TunnelServer,
	watcherCancel, syncerCancel context.CancelFunc,
	syncerInstance *Syncer,
) {
	slog.Info("Shutdown sequence initiated")

	syncerInstance.mutex.Lock()
	syncerInstance.isShuttingDown = true
	syncerInstance.mutex.Unlock()

	watcherCancel()
	syncerCancel()

	finalCtx, finalCancel := context.WithTimeout(
		context.WithoutCancel(ctx), finalSyncTimeout,
	)
	defer finalCancel()

	syncerInstance.sync(finalCtx)

	drainCtx, drainCancel := context.WithTimeout(
		context.WithoutCancel(ctx), quicDrainTimeout,
	)
	defer drainCancel()

	var drainWg sync.WaitGroup

	for _, tunnelSrv := range tunnels {
		drainWg.Add(1)

		go func(srv *TunnelServer) {
			defer drainWg.Done()

			srv.waitForDrain(drainCtx)
		}(tunnelSrv)
	}

	drainWg.Wait()

	for _, tunnelSrv := range tunnels {
		tunnelSrv.close()
	}
}

func startTunnelServers(
	ctx context.Context,
	tunnels map[int]*TunnelServer,
) {
	for _, tunnelSrv := range tunnels {
		go func(server *TunnelServer) {
			runErr := server.run(ctx)
			if runErr != nil {
				slog.Error(
					"Tunnel server failed",
					"bind_port", server.bindPort,
					"error", runErr,
				)
			}
		}(tunnelSrv)
	}
}

// BuildSyncTunnels converts TunnelConfig slices to SyncTunnel slices.
func BuildSyncTunnels(
	tunnelConfigs []TunnelConfig,
) []SyncTunnel {
	result := make([]SyncTunnel, 0, len(tunnelConfigs))

	for _, tunnelCfg := range tunnelConfigs {
		result = append(result, SyncTunnel(tunnelCfg))
	}

	return result
}

// ComputeReadyServiceIDs returns service IDs that are ready based on current state.
func ComputeReadyServiceIDs(
	currentState *state.State,
	tunnels map[int]*TunnelServer,
	tunnelConfigs []TunnelConfig,
	host string,
) []string {
	ids := make([]string, 0, len(currentState.Services))

	for _, svc := range currentState.Services {
		if isServiceReady(
			svc, tunnels, tunnelConfigs,
			host, currentState.Lings,
		) {
			ids = append(ids, svc.ServiceID)
		}
	}

	return ids
}

func isServiceReady(
	svc state.Service,
	tunnels map[int]*TunnelServer,
	tunnelConfigs []TunnelConfig,
	host string,
	lings []state.Ling,
) bool {
	if svc.BindPort == nil || svc.Host == nil {
		return false
	}

	if *svc.Host != host {
		return false
	}

	if !matchesTunnelConfig(svc, tunnelConfigs) {
		return false
	}

	if !lingIsActive(svc.LingID, lings) {
		return false
	}

	return hasQUICConnection(svc, tunnels)
}

func matchesTunnelConfig(
	svc state.Service,
	tunnelConfigs []TunnelConfig,
) bool {
	for _, tunnelCfg := range tunnelConfigs {
		if *svc.BindPort == tunnelCfg.BindPort {
			return true
		}
	}

	return false
}

func lingIsActive(
	lingID string, lings []state.Ling,
) bool {
	for _, ling := range lings {
		if ling.LingID == lingID && !ling.ShuttingDown {
			return true
		}
	}

	return false
}

func hasQUICConnection(
	svc state.Service,
	tunnels map[int]*TunnelServer,
) bool {
	tunnelSrv := tunnels[*svc.BindPort]
	if tunnelSrv == nil {
		return false
	}

	tunnelSrv.mu.RLock()
	_, hasConn := tunnelSrv.quicConns[svc.ServiceID]
	tunnelSrv.mu.RUnlock()

	return hasConn
}

// OnStateChanged updates tunnel servers based on new state.
func OnStateChanged(
	ctx context.Context,
	currentState *state.State,
	tunnels map[int]*TunnelServer,
	tunnelConfigs []TunnelConfig,
	host string,
) {
	for _, tunnelCfg := range tunnelConfigs {
		tunnelSrv := tunnels[tunnelCfg.BindPort]

		services, desiredPorts := filterServicesForTunnel(
			currentState, tunnelCfg, host,
		)

		tunnelSrv.updateServices(services)
		ensureDesiredListeners(ctx, tunnelSrv, desiredPorts)
		removeStaleListeners(tunnelSrv, desiredPorts)
	}
}

func filterServicesForTunnel(
	currentState *state.State,
	tunnelCfg TunnelConfig,
	host string,
) (map[string]ServiceAuth, map[int]string) {
	services := make(map[string]ServiceAuth)
	desiredPorts := make(map[int]string)

	for _, svc := range currentState.Services {
		if svc.BindPort == nil ||
			svc.Host == nil ||
			svc.RemotePort == nil {
			continue
		}

		if *svc.BindPort != tunnelCfg.BindPort ||
			*svc.Host != host {
			continue
		}

		if isLingShuttingDown(svc.LingID, currentState.Lings) {
			continue
		}

		services[svc.ServiceID] = ServiceAuth{Token: svc.Token}
		desiredPorts[*svc.RemotePort] = svc.ServiceID
	}

	return services, desiredPorts
}

func isLingShuttingDown(
	lingID string, lings []state.Ling,
) bool {
	for _, ling := range lings {
		if ling.LingID == lingID {
			return ling.ShuttingDown
		}
	}

	return false
}

func ensureDesiredListeners(
	ctx context.Context,
	tunnelSrv *TunnelServer,
	desiredPorts map[int]string,
) {
	for port, serviceID := range desiredPorts {
		tunnelSrv.ensureTCPListener(ctx, port, serviceID)
	}
}

func removeStaleListeners(
	tunnelSrv *TunnelServer,
	desiredPorts map[int]string,
) {
	tunnelSrv.mu.RLock()

	var removePorts []int

	for port := range tunnelSrv.tcpListeners {
		if _, needed := desiredPorts[port]; !needed {
			removePorts = append(removePorts, port)
		}
	}

	tunnelSrv.mu.RUnlock()

	for _, port := range removePorts {
		tunnelSrv.removeTCPListener(port)
	}
}

func generateSelfSignedCert() (tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf(
			"generating ECDSA key: %w", err,
		)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{"burrow"},
		NotBefore:    time.Now(),
		NotAfter: time.Now().Add(
			certValidityYears,
		),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(
		rand.Reader, template, template,
		&key.PublicKey, key,
	)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf(
			"creating certificate: %w", err,
		)
	}

	certPEMBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, string(certPEMBytes), nil
}

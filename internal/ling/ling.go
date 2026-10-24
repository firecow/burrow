// Package ling provides the ling command and its supporting tunnel/proxy infrastructure.
package ling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/firecow/burrow/internal/state"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const (
	keyValueParts           = 2
	finalSyncTimeout        = 2 * time.Second
	streamDrainTimeout      = 5 * time.Second
	reconcileInterval       = 1 * time.Second
	quicErrorCodeCloseClean = 0
)

// TunnelConfig holds the configuration for a single tunnel.
type TunnelConfig struct {
	Name      string
	LocalAddr string
}

// ProxyConfig holds the configuration for a single TCP proxy.
type ProxyConfig struct {
	Name     string
	BindPort int
}

var (
	errTunnelMissingName      = errors.New("--tunnel must have 'name' field")
	errTunnelMissingLocalAddr = errors.New("--tunnel must have 'local_addr' field")
	errProxyMissingName       = errors.New("--proxy must have 'name' field")
	errProxyMissingBindPort   = errors.New("--proxy must have 'bind_port' field")
)

// Command returns the cobra command for the ling subcommand.
func Command() *cobra.Command {
	var (
		councilHost       string
		lingID            string
		tunnelArgs        []string
		proxyArgs         []string
		preferredLocation string
	)

	cmd := newLingCommand(
		&councilHost, &lingID, &tunnelArgs,
		&proxyArgs, &preferredLocation,
	)

	cmd.Flags().StringVar(
		&councilHost, "council-host",
		"http://localhost:8080", "Council host to synchronize from",
	)
	cmd.Flags().StringVar(
		&lingID, "ling-id", "",
		"Unique ID of this ling instance (auto-generates UUID if omitted)",
	)
	cmd.Flags().StringArrayVar(
		&tunnelArgs, "tunnel", nil,
		"Tunnel clients (name=STR local_addr=ADDR)",
	)
	cmd.Flags().StringArrayVar(
		&proxyArgs, "proxy", nil,
		"TCP proxies (name=STR bind_port=N)",
	)
	cmd.Flags().StringVar(
		&preferredLocation, "location", "default",
		"Preferred location identifier",
	)

	return cmd
}

func newLingCommand(
	councilHost, lingID *string,
	tunnelArgs, proxyArgs *[]string,
	preferredLocation *string,
) *cobra.Command {
	return &cobra.Command{
		Use:   "ling",
		Short: "Start ling",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tunnelConfigs, err := ParseTunnelArgs(*tunnelArgs)
			if err != nil {
				return err
			}

			proxies, err := ParseProxyArgs(*proxyArgs)
			if err != nil {
				return err
			}

			if *lingID == "" {
				*lingID = uuid.New().String()
			}

			return run(
				cmd.Context(), *councilHost, *lingID,
				*preferredLocation, tunnelConfigs, proxies,
			)
		},
	}
}

// ParseTunnelArgs parses raw tunnel argument strings into TunnelConfig slices.
func ParseTunnelArgs(args []string) ([]TunnelConfig, error) {
	if len(args) == 0 {
		return nil, nil
	}

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
		parts := strings.SplitN(pair, "=", keyValueParts)

		if len(parts) == keyValueParts {
			pairs[parts[0]] = parts[1]
		}
	}

	name, nameFound := pairs["name"]
	if !nameFound {
		return TunnelConfig{Name: "", LocalAddr: ""}, errTunnelMissingName
	}

	localAddr, addrFound := pairs["local_addr"]
	if !addrFound {
		return TunnelConfig{Name: "", LocalAddr: ""}, errTunnelMissingLocalAddr
	}

	return TunnelConfig{Name: name, LocalAddr: localAddr}, nil
}

// ParseProxyArgs parses raw proxy argument strings into ProxyConfig slices.
func ParseProxyArgs(args []string) ([]ProxyConfig, error) {
	if len(args) == 0 {
		return nil, nil
	}

	configs := make([]ProxyConfig, 0, len(args))

	for _, arg := range args {
		config, err := parseSingleProxyArg(arg)
		if err != nil {
			return nil, err
		}

		configs = append(configs, config)
	}

	return configs, nil
}

func parseSingleProxyArg(arg string) (ProxyConfig, error) {
	pairs := make(map[string]string)

	for pair := range strings.SplitSeq(arg, " ") {
		parts := strings.SplitN(pair, "=", keyValueParts)

		if len(parts) == keyValueParts {
			pairs[parts[0]] = parts[1]
		}
	}

	name, nameFound := pairs["name"]
	if !nameFound {
		return ProxyConfig{Name: "", BindPort: 0}, errProxyMissingName
	}

	bindPortStr, portFound := pairs["bind_port"]
	if !portFound {
		return ProxyConfig{Name: "", BindPort: 0}, errProxyMissingBindPort
	}

	bindPort, err := strconv.Atoi(bindPortStr)
	if err != nil {
		return ProxyConfig{Name: "", BindPort: 0}, fmt.Errorf("invalid bind_port: %w", err)
	}

	return ProxyConfig{Name: name, BindPort: bindPort}, nil
}

func run(
	ctx context.Context,
	councilHost, lingID, preferredLocation string,
	tunnelConfigs []TunnelConfig,
	proxies []ProxyConfig,
) error {
	tunnelMap := buildTunnelMap(tunnelConfigs)
	syncTunnels := buildSyncTunnels(tunnelConfigs)
	tunnelCli := NewTunnelClient()

	tcpProxies, err := startProxies(ctx, proxies)
	if err != nil {
		return err
	}

	var stateMutex sync.Mutex

	var currentState *state.State

	shuttingDown := false

	readyServiceIDs := func() []string {
		stateMutex.Lock()
		defer stateMutex.Unlock()

		if currentState == nil || shuttingDown {
			return []string{}
		}

		return ComputeReadyServiceIDs(currentState, lingID, tunnelMap)
	}

	syncerInstance := buildSyncer(
		councilHost, lingID, preferredLocation,
		syncTunnels, readyServiceIDs, &stateMutex, &shuttingDown,
	)

	tunnelCli.onConnected = syncerInstance.triggerSync

	watcher := state.NewWatcher(
		councilHost,
		func(stateSnapshot *state.State) {
			stateMutex.Lock()
			currentState = stateSnapshot
			stateMutex.Unlock()

			OnStateChanged(
				ctx, stateSnapshot, lingID, preferredLocation,
				tunnelMap, tunnelCli, tcpProxies,
			)
		},
	)

	return runEventLoop(
		ctx, watcher, syncerInstance,
		&stateMutex, &shuttingDown, tunnelCli, tcpProxies,
	)
}

func startProxies(
	ctx context.Context,
	proxies []ProxyConfig,
) (map[string]*TCPProxy, error) {
	tcpProxies := make(map[string]*TCPProxy)

	for _, proxyConf := range proxies {
		proxy := NewTCPProxy(proxyConf.Name, proxyConf.BindPort)
		tcpProxies[proxyConf.Name] = proxy

		err := proxy.start(ctx)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to start proxy %s: %w",
				proxyConf.Name, err,
			)
		}
	}

	return tcpProxies, nil
}

func buildSyncer(
	councilHost, lingID, preferredLocation string,
	syncTunnels []syncTunnel,
	readyServiceIDs func() []string,
	stateMutex *sync.Mutex,
	shuttingDown *bool,
) *lingSyncer {
	return &lingSyncer{
		councilHost:       councilHost,
		lingID:            lingID,
		preferredLocation: preferredLocation,
		tunnels:           syncTunnels,
		readyServiceIDs:   readyServiceIDs,
		notify:            make(chan struct{}, 1),
		shuttingDown: func() bool {
			stateMutex.Lock()
			defer stateMutex.Unlock()

			return *shuttingDown
		},
		httpTransport: http.DefaultTransport,
	}
}

func runEventLoop(
	ctx context.Context,
	watcher *state.Watcher,
	syncerInstance *lingSyncer,
	stateMutex *sync.Mutex,
	shuttingDown *bool,
	tunnelCli *TunnelClient,
	tcpProxies map[string]*TCPProxy,
) error {
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
		ctx, syncerInstance, stateMutex,
		shuttingDown, watcherCancel, syncerCancel,
		tunnelCli, tcpProxies,
	)

	return nil
}

func performShutdown(
	ctx context.Context,
	syncerInstance *lingSyncer,
	stateMutex *sync.Mutex,
	shuttingDown *bool,
	watcherCancel, syncerCancel context.CancelFunc,
	tunnelCli *TunnelClient,
	tcpProxies map[string]*TCPProxy,
) {
	slog.Info("Shutdown sequence initiated")

	stateMutex.Lock()
	*shuttingDown = true
	stateMutex.Unlock()

	watcherCancel()
	syncerCancel()

	finalCtx, finalCancel := context.WithTimeout(
		context.WithoutCancel(ctx), finalSyncTimeout,
	)
	defer finalCancel()

	syncerInstance.sync(finalCtx)

	drainCtx, drainCancel := context.WithTimeout(
		context.WithoutCancel(ctx), streamDrainTimeout,
	)
	defer drainCancel()

	var drainWg sync.WaitGroup

	drainWg.Go(func() {
		tunnelCli.closeAll(drainCtx)
	})

	for _, proxy := range tcpProxies {
		drainWg.Go(func() {
			proxy.close(drainCtx)
		})
	}

	drainWg.Wait()
}

func buildTunnelMap(tunnelConfigs []TunnelConfig) map[string]string {
	tunnelMap := make(map[string]string)

	for _, tunnelCfg := range tunnelConfigs {
		tunnelMap[tunnelCfg.Name] = tunnelCfg.LocalAddr
	}

	return tunnelMap
}

func buildSyncTunnels(tunnelConfigs []TunnelConfig) []syncTunnel {
	syncTunnels := make([]syncTunnel, 0, len(tunnelConfigs))

	for _, tunnelCfg := range tunnelConfigs {
		syncTunnels = append(syncTunnels, syncTunnel{Name: tunnelCfg.Name})
	}

	if len(syncTunnels) == 0 {
		syncTunnels = []syncTunnel{}
	}

	return syncTunnels
}

// ComputeReadyServiceIDs returns service IDs that are ready for the given ling.
func ComputeReadyServiceIDs(
	stateSnapshot *state.State,
	lingID string,
	tunnelMap map[string]string,
) []string {
	ids := make([]string, 0, len(stateSnapshot.Services))

	for _, svc := range stateSnapshot.Services {
		if svc.LingID != lingID {
			continue
		}

		_, hasTunnel := tunnelMap[svc.Name]
		if !hasTunnel {
			continue
		}

		if svc.KingReady {
			ids = append(ids, svc.ServiceID)
		}
	}

	return ids
}

// OnStateChanged handles a state change by updating tunnel connections and proxy targets.
func OnStateChanged(
	ctx context.Context,
	stateSnapshot *state.State,
	lingID string,
	preferredLocation string,
	tunnelMap map[string]string,
	tunnelCli *TunnelClient,
	tcpProxies map[string]*TCPProxy,
) {
	kings := buildKingIndex(stateSnapshot)
	updateTunnelConnections(ctx, stateSnapshot, lingID, tunnelMap, tunnelCli, kings)
	updateProxyTargets(stateSnapshot, preferredLocation, tcpProxies, kings)
}

// KingGroup holds the connection details for a group of services on a single king.
type KingGroup struct {
	host     string
	bindPort int
	certPEM  string
	services []TunnelService
}

// KingIndex stores health and certificate info for a king address.
type KingIndex struct {
	healthy  bool
	certPEM  string
	location string
}

func buildKingIndex(stateSnapshot *state.State) map[string]KingIndex {
	index := make(map[string]KingIndex, len(stateSnapshot.Kings))

	for _, king := range stateSnapshot.Kings {
		addr := net.JoinHostPort(king.Host, strconv.Itoa(king.BindPort))
		index[addr] = KingIndex{
			healthy:  !king.ShuttingDown,
			certPEM:  king.CertPEM,
			location: king.Location,
		}
	}

	return index
}

func buildTunnelGroups(
	stateSnapshot *state.State,
	lingID string,
	tunnelMap map[string]string,
	kings map[string]KingIndex,
) (map[string]*KingGroup, map[string]string) {
	groups := make(map[string]*KingGroup)
	localAddrs := make(map[string]string)

	for _, svc := range stateSnapshot.Services {
		if svc.LingID != lingID {
			continue
		}

		localAddr, isTunnel := tunnelMap[svc.Name]
		if !isTunnel || svc.BindPort == nil || svc.Host == nil {
			continue
		}

		kingAddr := net.JoinHostPort(*svc.Host, strconv.Itoa(*svc.BindPort))

		king, exists := kings[kingAddr]
		if !exists || !king.healthy {
			continue
		}

		if groups[kingAddr] == nil {
			groups[kingAddr] = &KingGroup{
				host:     *svc.Host,
				bindPort: *svc.BindPort,
				certPEM:  king.certPEM,
				services: nil,
			}
		}

		groups[kingAddr].services = append(
			groups[kingAddr].services,
			TunnelService{
				serviceID: svc.ServiceID,
				token:     svc.Token,
				localAddr: localAddr,
			},
		)
		localAddrs[svc.ServiceID] = localAddr
	}

	return groups, localAddrs
}

func updateTunnelConnections(
	ctx context.Context,
	stateSnapshot *state.State,
	lingID string,
	tunnelMap map[string]string,
	tunnelCli *TunnelClient,
	kings map[string]KingIndex,
) {
	groups, localAddrs := buildTunnelGroups(
		stateSnapshot, lingID, tunnelMap, kings,
	)

	for _, group := range groups {
		tunnelCli.ensureConnection(ctx, group, localAddrs)
	}

	tunnelCli.retireUnneeded(ctx, groups)
}

func updateProxyTargets(
	stateSnapshot *state.State,
	preferredLocation string,
	tcpProxies map[string]*TCPProxy,
	kings map[string]KingIndex,
) {
	for proxyName, proxy := range tcpProxies {
		targets := collectProxyTargets(
			stateSnapshot, proxyName, preferredLocation, kings,
		)
		proxy.updateTargets(targets)
	}
}

func isServiceEligibleForProxy(
	svc state.Service,
	proxyName string,
	kings map[string]KingIndex,
) bool {
	if svc.Name != proxyName || svc.Host == nil || svc.RemotePort == nil {
		return false
	}

	if !svc.LingReady || !svc.KingReady {
		return false
	}

	if svc.BindPort == nil {
		return false
	}

	kingAddr := net.JoinHostPort(*svc.Host, strconv.Itoa(*svc.BindPort))

	king, exists := kings[kingAddr]

	return exists && king.healthy
}

func collectProxyTargets(
	stateSnapshot *state.State,
	proxyName string,
	preferredLocation string,
	kings map[string]KingIndex,
) []ProxyTarget {
	var allTargets []ProxyTarget

	var preferredTargets []ProxyTarget

	for _, svc := range stateSnapshot.Services {
		if !isServiceEligibleForProxy(svc, proxyName, kings) {
			continue
		}

		target := ProxyTarget{
			host:       *svc.Host,
			remotePort: *svc.RemotePort,
		}

		allTargets = append(allTargets, target)

		if preferredLocation != "" {
			kingAddr := net.JoinHostPort(*svc.Host, strconv.Itoa(*svc.BindPort))
			if king, exists := kings[kingAddr]; exists && king.location == preferredLocation {
				preferredTargets = append(preferredTargets, target)
			}
		}
	}

	if len(preferredTargets) > 0 {
		return preferredTargets
	}

	return allTargets
}

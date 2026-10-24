# Burrow

[![QA](https://github.com/firecow/burrow/actions/workflows/quality-assurance.yml/badge.svg)](https://github.com/firecow/burrow/actions/workflows/quality-assurance.yml)

Distributed service mesh with native QUIC tunneling. Single static Go binary with zero runtime dependencies.

Consists of three components:

### council
Service discovery and state coordination. Used by kings and lings.

### king
QUIC tunnel server. Must be reachable by all lings.

### ling
QUIC tunnel client and TCP proxy. Can be completely isolated — only needs outbound connectivity.

## Design principles

1. **Redeploying never drops traffic.** Restarting or upgrading any council, king or ling, one at a time, causes no failed or stalled connections. The integration test enforces this.
2. **The data plane outlives the control plane.** Kings and lings keep tunneling with their last known state while the council is unavailable.
3. **Members are the source of truth.** The council holds no state of its own. After a restart it rebuilds the cluster from what kings and lings report, serving state only after a warm-up of a few heartbeat intervals. Members that report later rejoin with their existing identities. A council restart is never worse for the mesh than a council that stayed up.
4. **Identities are stable.** Service IDs, tokens and port assignments survive restarts of any component, so a restart never forces a reconnect.
5. **Leave gracefully.** A component announces its shutdown, stops receiving new work and drains in-flight work before closing. Waits are event-driven, with timeouts only as a safety net.
6. **Always converge.** Every component reconciles towards the latest state on a fixed interval, not only on change events, so a missed or rejected event never leaves the mesh broken.

## Install

Binaries for Linux, macOS, Windows and FreeBSD are attached to each [release](https://github.com/firecow/burrow/releases), and the container image is `ghcr.io/firecow/burrow`.

## Quickstart

Install `docker` and run `docker swarm init`

See [stack.yml](./examples/docker-swarm/stack.yml) for deployment configuration

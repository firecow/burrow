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

## Install

Binaries for Linux, macOS, Windows and FreeBSD are attached to each [release](https://github.com/firecow/burrow/releases), and the container image is `ghcr.io/firecow/burrow`.

## Quickstart

Install `docker` and run `docker swarm init`

See [stack.yml](./examples/docker-swarm/stack.yml) for deployment configuration

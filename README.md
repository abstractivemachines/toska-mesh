# ToskaMesh Control Plane

Go control plane for the [ToskaMesh](https://github.com/abstractivemachines) service mesh.

## Components

- **Gateway** — Reverse proxy (port 5000). Dynamic route discovery from Consul, JWT auth, rate limiting, CORS, retry with exponential backoff, per-service circuit breakers.
- **Discovery** — gRPC service registry (port 8080). Backed by Consul. Publishes events to RabbitMQ in MassTransit-compatible format for C# interop.
- **HealthMonitor** — Concurrent health probe worker with circuit breakers. Exposes status API (port 8081).
- **Router** — Load balancing library: round-robin, least-connections, random, weighted round-robin, IP hash.

## Quick Start

```bash
make build      # build all binaries → bin/
make test       # go test -race ./...

./bin/gateway
./bin/discovery
./bin/healthmonitor
```

## Prerequisites

- Go 1.25+
- Consul (for service discovery)
- RabbitMQ (optional, for event publishing)

## Configuration

All services are configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `CONSUL_ADDRESS` | `http://localhost:8500` | Consul agent address |
| `GATEWAY_PORT` | `5000` | Gateway listen port |
| `GATEWAY_ROUTE_PREFIX` | `/api/` | URL prefix for service routing |
| `JWT_SECRET_KEY` | _(empty, auth disabled)_ | JWT signing key |
| `RABBITMQ_URL` | _(empty, no-op publisher)_ | AMQP connection string |
| `HEALTHMONITOR_PORT` | `8081` | HealthMonitor API port |
| `HEALTHMONITOR_PROBE_INTERVAL_SECONDS` | `30` | Seconds between probe cycles |

## Architecture

```
              ┌─────────┐
  Client ───▶│ Gateway  │──▶ Backend Services
              └────┬────┘
                   │ routes from
              ┌────▼────┐
              │ Consul   │◀── Discovery (gRPC)
              └────┬────┘
                   │ health
              ┌────▼─────────┐
              │ HealthMonitor │
              └──────────────┘
```

Services register via the [Go SDK](https://github.com/abstractivemachines/toska-mesh-go) or [C# SDK](https://github.com/abstractivemachines/toska-mesh-cs). Protobuf definitions live in [toska-mesh-proto](https://github.com/abstractivemachines/toska-mesh-proto).

## License

Apache License 2.0 — see [LICENSE](LICENSE).

# CLAUDE.md — toska-mesh (Go Control Plane)

This directory contains the Go control plane for the ToskaMesh service mesh.

## Build, Test, and Run

All commands run from this directory (`toska-mesh/`).

```bash
make generate   # regenerate Go protobuf from ../toska-mesh-proto/
make build      # build all binaries → bin/
make test       # go test ./...
make lint       # golangci-lint (skipped if not installed)
make clean      # remove bin/

# Run a single binary
./bin/gateway
./bin/discovery
./bin/healthmonitor
```

### Prerequisites

- Go 1.26+
- `protoc` (libprotoc 33+)
- `protoc-gen-go` and `protoc-gen-go-grpc` (install with `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`)
- Ensure `~/go/bin` is in `$PATH`

## Module

```
module github.com/toska-mesh/toska-mesh
```

## Directory Layout

```
toska-mesh/
├── cmd/
│   ├── gateway/main.go          # reverse proxy entry point
│   ├── discovery/main.go        # gRPC registry entry point
│   └── healthmonitor/main.go    # health probe worker entry point
├── internal/
│   ├── gateway/                  # reverse proxy, rate limiting, CORS, JWT
│   ├── discovery/                # gRPC server, Consul integration, events
│   ├── healthmonitor/            # concurrent probe workers, circuit breakers
│   ├── router/                   # load balancing algorithms (library, no binary)
│   └── consul/                   # Consul client wrapper
├── pkg/
│   └── meshpb/                   # generated protobuf Go code (do not edit)
├── tests/                        # integration tests
├── Makefile
└── go.mod
```

## Protobuf

The canonical proto definitions live in `../toska-mesh-proto/`. Both this Go control plane and the C# SDK (`toska-mesh-cs/`) generate from the same source. Run `make generate` after modifying any `.proto` file.

## Coding Conventions

- Standard Go style: `gofmt`/`goimports` formatting, exported names in PascalCase, unexported in camelCase.
- Errors are values — return `error`, don't panic. Wrap errors with `fmt.Errorf("context: %w", err)`.
- Use `context.Context` as the first parameter on functions that do I/O or may be cancelled.
- Prefer table-driven tests. Use `testing.T` directly (no assertion libraries).
- Keep `internal/` for implementation details, `pkg/` for code importable by other modules.
- No global mutable state. Pass dependencies explicitly (constructor injection or function parameters).

## Testing

```bash
go test ./...                          # all tests
go test ./internal/router/...          # specific package
go test -run TestRoundRobin ./...      # specific test
go test -race ./...                    # race detector
go test -bench=. ./internal/router/    # benchmarks
```

## Interop with C# Services

This Go control plane communicates with C# services (in `toska-mesh-cs/`) via:
- **gRPC** — `discovery.proto` defines the service registry contract
- **HTTP** — health check endpoints (`GET /health`)
- **Consul** — shared service metadata (`scheme`, `health_check_endpoint`, `lb_strategy`, `weight`)
- **RabbitMQ** — event publishing in MassTransit-compatible envelope format

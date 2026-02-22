# ToskaMesh Protobuf Definitions

Shared protobuf definitions for the [ToskaMesh](https://github.com/abstractivemachines) service mesh. Single source of truth for both Go and C# code generation.

## Contents

- **`discovery.proto`** — Service registry contract: registration, deregistration, instance lookup, health reporting.

## Code Generation

### Go (control plane & SDK)

```bash
# From toska-mesh/ or toska-mesh-go/
make generate
```

### C# (runtime SDK)

The `ToskaMesh.Grpc` project references this proto via `Grpc.Tools` — rebuild the C# solution to regenerate.

## Related

- [toska-mesh](https://github.com/abstractivemachines/toska-mesh) — Go control plane
- [toska-mesh-go](https://github.com/abstractivemachines/toska-mesh-go) — Go runtime SDK
- [toska-mesh-cs](https://github.com/abstractivemachines/toska-mesh-cs) — C# runtime SDK

## License

Apache License 2.0 — see [LICENSE](LICENSE).

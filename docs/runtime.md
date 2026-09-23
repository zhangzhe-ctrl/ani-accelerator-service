# Generic Runtime Contract

- Status: **FROZEN CONTRACT**
- Scope: runtime shell emitted by LAYOUT-0

This is the minimum runtime contract shared by newly generated ANI services.
It standardizes application mechanics while leaving business ownership to the
service.

## Process topology

One Kratos application owns two listeners:

| Listener | Purpose | Default local bind | Public business API |
| --- | --- | --- | --- |
| gRPC | future service APIs plus standard gRPC health | `127.0.0.1:19090` | no API supplied by the layout |
| admin HTTP | process health, readiness, and metrics | `127.0.0.1:19091` | no |

The committed defaults are safe for local execution. A service deployment may
override an address to an unspecified IP such as `0.0.0.0`; that choice belongs
to the service deployment, not to the template default.

## Configuration

- configuration is typed from `internal/conf/v1/conf.proto`;
- the process reads a configuration file selected by `-conf`;
- environment overrides use the `ANI` prefix;
- server configuration contains gRPC, admin HTTP, request timeout, and graceful
  shutdown timeout only;
- validation runs before listeners start;
- supported network value is `tcp`;
- listener addresses must use a literal loopback or unspecified IP plus a valid
  non-zero port;
- gRPC and admin must use distinct non-zero ports; and
- timeout values must be positive and bounded by the validation contract.

No database, message broker, cache, identity endpoint, or provider configuration
is present in LAYOUT-0.

## Composition and lifecycle

- `cmd/ani-accelerator-service/main.go` owns process concerns: configuration loading, logger
  construction, signal handling, and process metadata.
- `cmd/ani-accelerator-service/app.go` is the explicit composition root.
- Kratos `App` owns server start and stop.
- graceful shutdown uses the configured timeout and returns a non-zero result
  when startup or shutdown fails.
- `automaxprocs` is integrated into the same structured logger rather than
  writing an unrelated log format.
- Wire and generated DI code are absent.

The layout assumes one Kratos app per process. A service that changes this model
must review global telemetry-provider ownership explicitly.

## Logging and request correlation

The process emits Kratos structured JSON logs with at least service name,
version, instance ID, timestamp, caller, and trace/span identifiers when a span
is active. Sensitive values must be filtered at the logger boundary. The layout
does not define a business audit log.

## Transport middleware

The frozen gRPC middleware order is:

1. recovery;
2. metadata;
3. tracing;
4. logging;
5. metrics; and
6. validation.

Changing the order is a runtime-contract change because it affects whether
panics, request metadata, trace context, validation failures, latency, and status
are observable consistently.

Kratos error and codec handlers remain the transport boundary. gRPC reflection
is disabled by default. The standard gRPC health service is enabled.

## Administrative endpoints

| Endpoint | Meaning | Must not imply |
| --- | --- | --- |
| `GET /healthz` | the process and admin server can answer | downstream dependencies are healthy |
| `GET /readyz` | the generic process reached its running lifecycle state | business dependency readiness |
| `GET /metrics` | Prometheus exposition for registered runtime metrics | telemetry has been scraped or exported |

Administrative handlers use the same Kratos error, codec, and middleware path
rather than an unrelated ad-hoc HTTP stack.

`/readyz` is intentionally process-only in this template. When a service owns a
database, broker, provider, or other mandatory dependency, the service must add
an explicit readiness contribution and tests. It must not reinterpret the
generic result silently.

## Telemetry

- Prometheus exposes process and Kratos transport metrics on the admin listener;
- OpenTelemetry provides trace propagation and an export integration seam;
- the generic readiness gauge is named `ani_runtime_ready`;
- telemetry initialization and shutdown are owned by the application lifecycle;
  and
- absence of an external collector must not invent successful export evidence.

Local metric exposition can be verified without proving a production scrape,
dashboard, alert, or trace backend.

## Extension seams

A generated service adds its own API, use cases, repositories, providers, and
dependency-specific probes below the explicit composition root. The empty
`internal/biz`, `internal/data`, and `internal/service` packages are navigation
seams, not permission boundaries and not a mandate to reproduce ANI's historical
Core/Service split.

## Verification boundary

The executable local checks are described in
[runtime-verification.md](runtime-verification.md). The layout release records
its own acceptance evidence separately; each generated service must record its
own results. Contract text alone is not runtime evidence.

# Runtime verification

The [2026-10-27 retirement](releases/2026-10-27-observe-release-retirement.md) removes
the public `ObserveRelease` method. The 22-RPC results below are historical and
do not assert acceptance of the remaining API or consumer migration after that change.

The current process regression entry point is
`tests/contract/TestPublicRPCsMTLSAndRecovery`. Its cases cover the remaining public
RPCs, five certificate identities invoking the retired path and receiving
`Unimplemented`, non-Gov Resolve rejection, the Gov Resolve path, and persisted
`ListBindings` `RELEASE_CONFIRMED` facts with capacity recovery. The test must run
against the candidate on Fedora with the required PostgreSQL fixture; this
description is not a test result or real workload cleanup acceptance.

ACC-V12-01 supersedes the scaffold-only evidence described below. Its recorded
acceptance includes PostgreSQL, the then-registered RPCs over mTLS, process interruption,
provider replay and populated restore; see [execution status](execution/status.md)
and [the runtime runbook](runbooks/accelerator-runtime.md). All task execution is
on Fedora. Real GPU and external integration remain `not_verified`.

The generated service keeps executable runtime checks beside the source. From a
clean checkout, run:

```bash
make tools
make verify
```

`make verify` checks that typed configuration and the GPU protobuf/gRPC bindings
regenerate without a content change, rejects stale formatting or module metadata,
and runs `go test`, `go vet`, `go build`, `go mod verify`, and the imported v1.2
Python design-model tests. The model tests are pure computation, not GPU or
database integration checks.

The runtime integration tests start real loopback gRPC and admin HTTP listeners.
They check gRPC health, disabled reflection, health/readiness/metrics responses,
Kratos error encoding, middleware recovery/metadata/validation, request metrics,
trace-correlated structured logging, and graceful application stop. These tests
prove only the generic local runtime. They do not prove a database, broker,
provider, external telemetry backend, container, Kubernetes deployment, or
business contract.

For vulnerability and secret scans, a reproducible CycloneDX JSON runtime SBOM,
and notice/license-inventory consistency checks (with `jq` available):

```bash
make supply-chain-tools
make audit
```

Vulnerability results are valid only for the scanner and database state at the
time of execution. The pinned Gitleaks module scans all local Git refs with its
embedded detector set and redacts any finding in command output; CI requests a
full-history checkout before running it. The SBOM
generator requires a committed revision, ignores only its own output while
checking cleanliness, and builds an isolated synthetic Git snapshot from all
tracked files except the prior SBOM. This prevents both a self-reference and
accidental discovery of a parent Git repository through `TMPDIR`.

The committed SBOM targets the Linux/amd64 runtime graph. Source identity excludes
the BOM itself. The generator removes the tool binary hashes from portable BOM
identity and records the actual tool hash separately in its command output.

For a newly generated repository, first commit the generated source, then run
`make supply-chain-tools` and `make audit`, review the result, and commit
`docs/scaffold/bom.cdx.json`. The CI gate rejects a missing or stale SBOM and
reruns the scanner and notice/license-evidence checks. The retained upstream
notice is not a project license; choose the service's license before publication.

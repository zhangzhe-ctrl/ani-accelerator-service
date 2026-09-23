# Service repository guidance

This service owns its generated runtime source. It has no runtime dependency on
the layout that created it.

## Boundaries

- `cmd/<service>` is the explicit composition root.
- `internal/service` adapts inbound transport contracts to use cases.
- `internal/biz` owns domain concepts, behavior, use cases, and required ports;
  it must not import Kratos, protobuf, transports, or storage drivers.
- `internal/data` implements outbound ports and owns infrastructure-specific
  code. Add an adapter only when a vertical slice needs it.
- `internal/server` owns transport and cross-cutting runtime wiring, not
  business rules.

Prefer framework capabilities over local replacements. Do not introduce a
shared ANI runtime module, generalized YAML exception registry, or technology
switches unrelated to a real slice. Generate protobuf output through the pinned
workflow and run `make verify` before committing.

## Accelerator scope

- The active API sources live in `api/protos/`; generated Go files live in
  `api/gen/go/`. Use `make api`; do not edit generated files.
- The imported v1.2 source design is under `docs/design/v1.2/`. It takes priority
  over older GPU design versions: Governance synchronizes usage asynchronously;
  Inference does not register a Consumer before creating workloads.
- This service owns GPU supply, profiles, inventory, bindings and capacity views.
  Governance owns quota and usage synchronization; Inference owns its workloads,
  model readiness, deletion and quota release facts. Do not copy their business
  writes into this service.
- Usage ENDED does not erase a live binding. Exit/restart is not instance deletion.
  Keep the Inference deletion-completed release contract defined by the design.
- SQL under the design tree is a proposal. Governance SQL stays in its integration
  reference directory. Add reviewed executable migrations only for an actual
  persistence slice; never migrate automatically on service startup.
- The Python model is a design fixture, not the Go scheduler/provider implementation.
  Historical source-validation results do not prove this service is integrated.

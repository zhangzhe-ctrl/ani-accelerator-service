# GPU absolute-memory consumer contract

ACC supplies immutable GPU execution data; Governance owns identity, original quota charges and durable dispatch; Resource owns its workload and complete-release facts. This contract replaces the previous full Plan and owner attachments without a runtime conversion path.

## Resolve and execution

`ResolveGpuRequest` returns `ResolvedGpuRequest` with `execution`, `quota_items` (Governance only), and `selection` (display only). `GpuExecution` contains exactly scheduler, queue, RuntimeClass, node selector, Pod annotations, resources and execution digest. Number, memory and cores appear once, in resources. The only supported resource keys are `volcano.sh/vgpu-number`, `volcano.sh/vgpu-memory`, `volcano.sh/vgpu-cores`; memory is integer MiB with actual device-plugin F=1. Percentage and unknown resource keys are rejected.

RuntimeClass comes from ACC's managed baseline, with no owner fallback. Shared memory is positive MiB and cores 1..99. Whole mode requests the actual complete per-device usable memory and 100 cores, with K=1. Profile parameters are immutable; changing them requires a new Profile ID.

`pkg/gpuprotocol` exports `GpuExecutionDigest` and `ValidateGpuExecution`. The digest is SHA256 of `acc-execution-v1\n` plus canonical UTF-8 JSON of the explicit six execution fields, excluding the digest. Keys sort lexically; resource values are decimal strings; HTML escaping and trailing newlines are absent. Display and mutable catalog state do not enter this digest.

The common persisted-command ACK lives in `accelerator.command.v1`. Deleted protobuf field numbers and names are reserved. Retired Plan, Encoding, Totals and GPU owner attachment messages are absent from the generated API.

## Catalog and physical inventory

Management RPCs are CreateProfile, PreviewProfile, SetProfileState, DeleteProfile, AdminListProfiles. Profiles have ACTIVE/DISABLED/DELETED state and a revision. Mutations use idempotency and CAS; deletion is soft. The authorized catalog retains inactive entries so consumers can show an unavailable reason. New Resolve requires ACTIVE; previously accepted execution data is independent of subsequent catalog state.

Multiple profiles may share a group. Their capacities are alternative views of one physical ledger and cannot be added. Per-card usable memory is the minimum of actual registered memory and quantized NVML memory. Missing or conflicting automatic sources produce Unknown and retain occupancy. No test report, verification reference, artificial qualification state or expiry gates catalog creation, admission, resolution or collection.

## Ownership, Usage and release

Resource signs actual cluster/namespace/Pod UID/container, tenant/owner/resource/create-operation and execution digest using the `acc-owner-pod-v2\n` domain. ACC verifies the managed owner key; a label or digest alone does not authenticate ownership. Resource and Inference keys remain separate.

Governance asynchronously sends the thin Usage projection: reference, profile/group/configuration references, cluster/container, execution digest, source creation time, revision/state/end reason/fact reference and payload digest. ACC checks immutable reference identity without recalculating execution or quota from today's directory. ENDED does not erase a live binding. Workload creation does not await Usage delivery.

Complete resource release is reported to Governance through its quota API `ReportResourceReleased`: release event, original create operation, resource and reason only. Resource neither receives nor persists charges. It seals creation, resolves unknown writes and persists release outbox before reporting; only a matching terminal ACK completes that outbox. Stop/restart/ordinary exit do not cause full quota return. Governance refunds the authenticated owner's original remaining charges exactly once.

## Deployment and verification boundary

Explicit migration 0004 removes the single-profile constraint, updates new-write identity checks and protects immutable Profile parameters. Startup never migrates. Cutover requires old workloads, in-flight writes and release delivery to be closed; old audit facts are preserved and are not interpreted as new execution payloads. New services require coordinated provider/consumer versions and actual F=1 plugin configuration.

Fedora required `make verify` passed for this implementation, including generation/sqlc consistency, PostgreSQL, mTLS/process recovery, race, tenant mutations, transactional restore, vulnerability and secret scans. The production executable's no-test-proof flow also passed with explicit external infrastructure substitutes. These results do not constitute real GPU acceptance. Deployment, shared/whole CUDA, physical release and official scheduler validation are tracked by the execution Goal and remain required before Goal completion.

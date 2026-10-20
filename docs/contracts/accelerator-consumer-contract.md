# GPU absolute-memory consumer contract

ACC supplies immutable GPU execution data; Governance owns identity, original quota charges and durable dispatch; Resource owns its workload and complete-release facts. This contract replaces the previous full Plan and owner attachments without a runtime conversion path.

## Service authorization and tenant isolation

Governance is the authorization authority for user identity, account state, roles, platform administration, subscription modules and quota admission. It derives the actor and resource-tenant UUID from its authenticated principal and persisted tenant mapping, and reconstructs outbound metadata rather than forwarding public caller identity fields. New users and tenants require no ACC configuration change or restart. ACC has no per-user or per-tenant deployment allowlist.

ACC requires TLS 1.3, a verified managed CA chain and exactly one DNS SAN on each caller certificate, with no URI, IP, email or additional SAN entry. `security.governance_dns_name`, `security.inference_dns_name` and `security.resource_dns_name` are the fixed service identities `ani-governance`, `ani-inference-service` and `ani-resource-service`. CN, SPIFFE URI aliases, wildcard names and alternate identities are not accepted. The Inference transport identity maps to the existing persisted owner identifier `ani-inference`; historical usage and release references are unchanged.

| Caller | Exact RPC allowlist |
|---|---|
| Governance | The generated full method names of AcceleratorAdminService, AcceleratorCatalogService, and the usage reads `GetGpuUsage`, `ListGpuUsages`, `ListBindings`; background `SyncGpuUsage`; gRPC Health `Check`, `List`, `Watch` |
| Resource and Inference | Only `/accelerator.v1.AcceleratorUsageService/ObserveRelease`, for the authenticated service's own resource reference |
| Other identity or method | Denied, including a known short method name under another service/package, reflection and other streaming methods |

Adding a generated RPC does not automatically authorize it: ACC explicitly registers each complete method path. Health access is also a service permission, not a user grant. Resource and Inference cannot use ACC health methods or directory/management RPCs.

Delegated Gov calls retain `x-ani-action`, `x-ani-actor-type`, `x-ani-actor-id`, and tenant-scoped calls retain `x-ani-tenant-id`. Metadata must be unique and match the typed request context; admin requests must not carry a tenant scope. ACC retains the actor for audit, cursor binding and idempotency rather than looking it up in a deployment list. Usage and Binding reads retain tenant predicates and full tenant-preserving resource references; conflicting tenant context and cross-tenant cursors or associations are rejected. Shared Profile/Pool/SupplyGroup catalog entries remain platform data, scoped by their managed cluster and parent relations.

`SyncGpuUsage` is a durable Gov background fact and carries no delegated user or tenant metadata; an optional action must be `SyncGpuUsage`. `ObserveRelease` carries the tenant, action and the authenticated owner's durable deletion scope. Lifecycle synchronization, physical release and original-charge refund do not depend on the initiating user still holding a role or an active subscription.

ACC's infrastructure scope comes directly from the existing `provider.json` `connections[].ref` values and the persisted cluster `connection_ref`. `RegisterCluster` can reference only a configured connection. Directory, resolution, capacity and management operations verify that stored cluster relations remain in this provider domain. Unknown or removed connections, unknown clusters and conflicting entity parents are rejected. There is no second service/cluster list and no user/tenant-to-cluster mapping. Connection credentials and endpoints cannot be supplied through public RPCs; the [provider configuration contract](../runbooks/provider-sources.md#受管连接配置) owns them.

Migration removes `security.grants` and the previous `*_uri` fields; their protobuf field names and numbers remain reserved. Pre-cutover cursor tokens fail their changed scope hash and callers must restart pagination; persisted actor-based mutation idempotency is unchanged. Old fields are rejected rather than ignored as a permissive default. Replace service certificates and configuration together, preserving a matching previous binary/configuration/certificate set for recovery. A user permission or subscription denial is a Gov decision; an ACC service identity or RPC-policy denial is a dependency authorization failure, not a request for the user to obtain another role.

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

`ObserveRelease` requires the authenticated owner's reference, delete operation, cluster, persisted `sealed_at` and known historical Pod references (which may be empty). Resource uses its distinct exact DNS SAN and client certificate, authorized only for this method. ACC supplements the scope from durable owner claims and rejects cross-cluster conflicts. Every managed GPU node must have complete, fresh Kubernetes, PodResources and CRI watermarks at or after the seal. Known allocations require positive runtime-exit, allocation-absence and online-node evidence; durable release facts do not expire while current source health is checked. Missing sources or unassociated allocations remain Unknown. A sealed scope with no observed allocation can complete only after this full source check.

Complete resource release is reported to Governance through its quota API `ReportResourceReleased`: release event, original create operation, resource and reason only. Resource neither receives nor persists charges. It seals creation, resolves unknown writes and confirms physical release before creating the release outbox; only a matching terminal ACK completes that outbox. Stop/restart/ordinary exit do not cause full quota return. Governance refunds the authenticated owner's original remaining charges exactly once.

## Deployment and verification boundary

Explicit migration 0004 removes the single-profile constraint, updates new-write identity checks and protects immutable Profile parameters. Startup never migrates. Cutover requires old workloads, in-flight writes and release delivery to be closed; old audit facts are preserved and are not interpreted as new execution payloads. New services require coordinated provider/consumer versions and actual F=1 plugin configuration.

Existing software acceptance records cover generation/sqlc consistency, PostgreSQL, mTLS/process recovery, race, tenant mutations, transactional restore, vulnerability and secret scans, and the ordinary production executable with explicit external infrastructure substitutes. Each record applies to its recorded source snapshot. The authorization change requires directed Fedora checks for the real Gov→ACC catalog path, new principals without ACC onboarding, service/RPC rejection, tenant isolation and existing background/release behavior. Local work is source editing only; generation, builds and tests run on Fedora. This change does not authorize deployment. Software checks do not constitute real GPU acceptance; shared/whole CUDA, physical release and official scheduler acceptance remain separate.

# GOV-ACC-V12-01 isolated software helpers

These entry points compile only into Go test binaries. They are absent from the
production registry, OpenAPI and production image. Run them only on the authorized
Fedora host with task-local caches, `GOWORK=off`, `GOMAXPROCS=2`, `GOFLAGS=-p=2`.
Use separate A/B databases, CAs, certificates and evidence directories. Never copy
fixture private keys into production or repository evidence.

## B: persistent contract runtime

Create an isolated database through explicit migrations 0001 and 0002, and grant
its own non-owner, non-superuser, NOBYPASSRLS role only the privileges documented
in `scripts/test-pg-grants.sql`. Revoke PUBLIC CONNECT for the isolated B database.
Store its DSN in a task-owned mode-0600 file.

Build `go test -c -o <task>/joint-b/acc-contract.test ./tests/contract`. Run:

```sh
ACC_TEST_DSN_FILE=<task>/joint-b/acc.dsn \
ACC_JOINT_ROOT=<task>/joint-b \
ACC_JOINT_ADDR=127.0.0.1:29092 \
<task>/joint-b/acc-contract.test \
  -test.run='^TestJointPersistentRuntime$' -test.timeout=0 -test.v
```

This explicitly creates a new task-local test CA and server/client certificates,
then invokes the real management gRPC API over mTLS to register/adopt/publish/open
one **software fixture** catalog. It substitutes only controlled inspection and
consumption evidence in the test process; its PG repository, pgx/sqlc paths,
service, transport and service/context authorization middleware are production code. It never
registers a real owner or claims hardware acceptance.

`ready.json` contains paths and connection parameters, not credential bytes.
The Acc certificate has DNS `ani-accelerator` and `accelerator.test`; Gov and owner
certificates carry exactly one DNS SAN, respectively `ani-governance` and
`ani-inference-service`, with no URI or other SAN identity. The latter maps to
the existing persisted owner ID `ani-inference`. Private keys and other files
are mode 0600. The CA signing key
and hardware/owner assertion signing private keys are not persisted.

The seed actor is `user:1`; the default tenant UUID is
`11111111-1111-4111-8111-111111111111`. Gov owns user roles and subscriptions;
ACC does not configure actor/tenant grants or a second cluster allowlist.
Its transport authorizes the exact service and complete RPC method, while
business code verifies actor/tenant consistency and the configured provider
connection domain. Additional Gov-authorized actors and tenants need no ACC
configuration change or restart. `seed.json` contains catalog objects, a valid request and
`requests[RPC]`, the original five management requests including idempotency keys.
BFF tests can replay the original requests with the same trusted actor.
`SyncGpuUsage` and `ObserveRelease` use their direct certificate identities.

Once Gov has durably accepted and synchronized a real test operation, write a
fixture request containing the **Gov-generated original ref**:

```json
{"ref":{"tenant_id":"...","owner_service":"ani-inference","resource_id":"...","create_operation_id":"..."},"state":"ACTIVE"}
```

Invoke `TestJointObservationFixture` in the same test binary with
`ACC_TEST_DSN_FILE` and `ACC_JOINT_OBSERVATION_FILE=<request.json>`. It reads the
persisted usage and frozen plan, writes inventory/three source claims through
actual `SaveObservation`, then proves a nonempty sensitive binding is queryable
for the original tenant/ref. `request.json.result.json` retains its exact Pod and
device scope. Feed that result back with `state=RELEASED` to persist a controlled
positive release observation. Owner then calls the real `ObserveRelease` over
mTLS using the complete returned Pod scope and its explicit DELETE operation.
The helper does not issue quota refunds or replace owner cleanup responsibility.

Gov BFF redaction must be asserted against the resulting **nonempty** binding.
Neither an empty response nor helper completion proves BFF redaction. The source
is a software hardware-port fixture; no real GPU allocation/cleanup is claimed.

Stop the persistent B fixture by creating `<task>/joint-b/stop`; the parent test
stops the server child. Remove private material only after consumers have stopped.

To resume a previously seeded B database, do not rerun
`TestJointPersistentRuntime`. Read the preserved `ready.json` and launch the
existing or freshly verified contract binary with `ACC_CONTRACT_CHILD=1`,
`ACC_CONTRACT_CONFIG=<config_file>`, `ACC_CONTRACT_CONNECTION=<connection_ref>`,
and a task-owned `ACC_CONTRACT_MARKER`, using
`-test.run='^TestContractServerProcess$' -test.timeout=0 -test.v`. This starts only
the transport/usecase runtime and preserves all catalog IDs, original keys, CA
and database records. It does not read the wrapper's `stop` file. Record its
exact PID and new log; stop that direct child with SIGINT. Check certificate
expiry and database readiness before resuming. A new binary must be built from
the verified candidate; never label an older running helper as the final runtime.

## A: production reader source with no consumption evidence

`TestFormalProductionConsumptionGate` performs the complete A-layer gate. Supply
`ACC_FORMAL_DSN_FILE` pointing to a separately migrated database and restricted
role; `make verify` requires it, and CI creates its own `accelerator_formal`
database. The test builds and launches the ordinary production executable,
creates an independent service CA and an empty hardware proof directory, starts
the controlled source below in another process, and calls real management RPCs.
It asserts Publish reaches `CONSUMPTION_NOT_VERIFIED`, OPEN returns
`PROFILE_NOT_ACTIVE`, and Resolve independently returns `NOT_FOUND` because no
profile exists. It checks zero profiles and durable CLOSED/NOT_VERIFIED/version 1
after restart, then records `formal-a.json` and production/source logs in
`ACC_TEST_EVIDENCE_DIR`. No B fixture or proof key is consumed by this test.

For a formal Gov→Acc integration, the same test accepts an explicit absolute
`ACC_FORMAL_HOLD_DIR`. After all A assertions pass it writes `ready.json` there
with the ordinary production executable's hash/PID, endpoint, independent CA,
Gov certificate/key paths and catalog IDs, and stays alive until `<hold>/stop`
exists. The test uses platform actor `user:1` without configuring a per-actor
ACC grant, and the Gov certificate has the sole DNS SAN `ani-governance` with
no URI or alternate SAN; the Acc server supports DNS
`ani-accelerator`. Run the test binary with `-test.timeout=0`. The referenced
ephemeral files remain valid only while this helper is running. This optional
hold does not change production composition or provide consumption evidence.

Build `go test -c -o <task>/joint-a/source/source.test ./internal/data/provider`.
Run it with `ACC_FORMAL_SOURCE_DIR=<task>/joint-a/source` and
`-test.run='^TestJointFormalProviderSource$' -test.timeout=0 -test.v`.

Read `source.json`. Configure a **formal production Acc binary** with that
`provider_config_file`, an independent A database and service CA, a separate
hardware public key, and an empty attestation directory. The source endpoint
requires a verified client certificate and presents a verified TLS server
certificate; the formal Acc reader loads those credentials from provider config.
The helper advertises a constructed provider configuration only. It writes no
profile, no VERIFIED state and no hardware attestation.

Use source.json's baseline, node, connection ref, model and queue in real
RegisterCluster/CreatePool/AdoptSupplyGroup requests. Adoption starts CLOSED and
NOT_VERIFIED. Write the returned actual GroupID to `group_id_file` so the source
labels satisfy the real Publish inspection. Publish with an absent evidence ref
must then reach `CONSUMPTION_NOT_VERIFIED`. Verify no profile was inserted and the
group did not become VERIFIED. Record OPEN and Resolve's own actual preconditions
separately; a missing profile is not a substitute for reaching the proof gate.

Stop the source by creating its `stop` file. Its ephemeral source CA files remain
valid only for that running test process.

## C and regression evidence

`make verify` executes the real PG/mTLS 22-RPC positive and wrong-service tests,
process kill/restart cases, and production reader TLS HTTP/Unix gRPC replay tests.
The provider replay covers normalized units, duplicate sources, local conflicts,
stale/offline UNKNOWN, exact-scope release and recovered capacity. The NVML data
and attestations in that separate C replay are explicitly constructed software
fixtures. A, B and C results must remain separate from real GPU and real owner
acceptance claims.

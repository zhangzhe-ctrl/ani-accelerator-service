# Accelerator persistence

`Postgres` owns pgx transaction lifetimes. Every production database statement,
including startup security/version checks and advisory locks, is defined in
`queries/accelerator.sql` and executed through sqlc-generated `accdb.Queries`.
A `pgStore` holds only the generated queries bound to its single `pgx.Tx`.
Domain interfaces and JSON snapshots remain in `internal/biz`; sqlc and pgx types
do not cross that boundary. No arbitrary SQL execution helper is exposed.

The only schema authority is the ordered `migrations/` chain. sqlc v1.30.0 reads
that chain directly. `make sqlc` regenerates; `make verify-sqlc` checks the exact
module identity/checksum, compiles queries, regenerates without changes, rejects
inline production/test SQL, and emits input/output hashes. The pinned checksum is
`h1:H4HrNwPc0hntxGWzAbhlfplPRN4bQpXFx+CaEMcKz6c=` in `scripts/verify-sqlc`.

## Scope and constraints

- Clusters, pools, groups, physical devices and unassociated provider facts are
  platform/cluster-scoped supply inventory. They do not manufacture a tenant.
  The business layer checks trusted cluster grants before these reads.
- Usage reads, revisions, writes, conflicts and tenant binding pages use explicit
  tenant/owner/resource/create identities. Composite foreign keys preserve the
  tenant. Binding references are either fully absent (unassociated physical
  occupancy) or complete and refer to the same tenant usage.
- Cryptographically verified claims/release proofs may arrive before usage.
  Their generated relation columns expose the complete owner scope derived from
  JSON. They deliberately have no usage FK because pre-projection evidence is a
  valid global inventory fact; tenant-visible bindings require a matching usage.
- Migration 0002 makes required JSON comparisons `IS TRUE`, preventing missing
  keys/JSON null from bypassing CHECK constraints. It disables RLS/forced RLS and
  removes any policies in the owned schema. Startup reads reject table owners,
  superusers, BYPASSRLS, DDL/TEMP privileges and any residual RLS/policy.
- Reconciliation scans are explicitly cluster-scoped inventory processing, not
  tenant request APIs. No global tenant-list business query exists.

## Upgrade and recovery

Stop new admission and provider polling, back up the populated database, then
explicitly run `scripts/migrate 0002` as the migration owner. It validates existing
rows transactionally and aborts on inconsistent legacy JSON. Repair invalid facts
from authoritative records; do not coerce, delete or invent tenant identity.
Fresh databases require 0001 then 0002. Runtime startup never migrates or seeds.

The new runtime requires schema version 2. Do not roll the old binary back over
new writes: stop writers and restore the retained pre-upgrade backup into an
isolated database, verify it, then choose a compatible binary. There is no
automatic destructive DOWN migration.

`make verify` requires isolated `ACC_TEST_DSN_FILE` and `ACC_FORMAL_DSN_FILE`
references with different database names, and runs real restricted-role tests.
The latter is reserved for the ordinary production-process A-layer gate;
software source/consumption fixtures use the former. `make test-race` adds
concurrent transaction/association checks.
`scripts/test-postgres-recovery` requires a labeled task container and an evidence
location, dumps populated data, checks failed transactional DDL rollback,
restores to a separate database, compares every data table and reruns repository
and contract tests there. Test SQL resides under `testdata/` and operational SQL
under `scripts/`; it is never a second schema definition.

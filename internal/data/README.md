# Accelerator persistence

`Postgres` owns pgx transaction lifetimes. Production SQL is defined under
`queries/` and executed through sqlc-generated `accdb.Queries`. A `pgStore`
contains only queries bound to its transaction. The domain never imports pgx or
sqlc types, and no arbitrary SQL helper is exposed.

The ordered `migrations/` chain is the only schema authority. sqlc v1.30.0 reads
that chain; `make sqlc` regenerates its output. Application startup only checks
schema version and runtime-role privileges; it never migrates or seeds data.

## Current projection

A complete Kubernetes Pod list and its current Volcano assignments replace the
current occupancy set. Binding identity remains cluster/Pod UID/container/device;
ended rows remain available to paginated resource lists, but current reads use
partial indexes and never load historical claims, attempts or monitoring events.

Collection reads the cluster observation revision before provider I/O. The write
transaction takes the cluster lock, compares that revision, updates devices and
bindings, and advances the revision atomically. A competing collection retries
only at the next normal poll. A failed list preserves the corresponding current
set and marks its observation incomplete; it is not an empty list.

`devices.inventory_present` tracks the current registered inventory. Complete
Node and registration lists may retire missing identities. Failed registration
preserves current identities as unknown. Optional physical-memory bytes have no
capacity role and are zero when not freshly available.

Signed owner candidates may precede Usage. The candidate is private metadata,
not a tenant association. A matching immutable Usage can link pending bindings,
including ended rows, without changing allocation state or observations. Usage
replays only touch still-pending matches. Existing verified owner columns,
composite Usage foreign keys and tenant JSON checks remain in force.

## Upgrade and recovery

Schema 9 is an explicit one-window cutover: stop every schema-8 writer and its
connections, back up the database, then run `scripts/migrate 0009` as the migration
owner. Apply the updated runtime grants, including UPDATE on clusters. Existing
binding IDs and quantities remain; current observations start incomplete until
the first successful complete collection. The seven obsolete proof, telemetry
and monitoring-history tables are dropped. Fresh databases run migrations 1–9.

Do not mix schema-8 and schema-9 writers. Rollback requires the coordinated
schema-8 backup and matching old binaries/config after handling any new business
writes; changing only the image cannot restore removed tables.

Required tests use isolated restricted-role PostgreSQL databases. Operational
and test SQL stays in scripts/testdata, separate from the migration authority.
`scripts/test-postgres-recovery` restores into a separate task database and
compares the remaining business relations. Never run it against production.

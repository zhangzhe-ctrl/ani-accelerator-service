# Biz

Catalog mutations arbitrate idempotency in short PostgreSQL transactions. Adopt,
Publish and OPEN read the original catalog snapshot and release that transaction
before calling provider inspection or consumption verification. The final
mutation arbitrates the key again (an already committed same-request winner has
priority), checks the complete original snapshot and expected version, checks
inspection freshness again, and atomically writes the catalog and replay result.
Group/shared-vector locks remain in the final mutation. External I/O never holds
either those locks or a read-only database transaction.

Resolve similarly reads, verifies outside PostgreSQL, then compares the catalog
snapshot and computes capacity in a fresh consistent view. A concurrent catalog
change returns VERSION_CONFLICT; it cannot apply evidence to a changed group.
Capacity remains a reference, not a reservation. Collector I/O also occurs
between its catalog-read and observation-write transactions.

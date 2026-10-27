# GPU monitoring ownership scopes

The monitoring methods are internal Gov mTLS reads. Platform callers use
`AcceleratorAdminService.ListAdminGpuMetricScopes`, action
`ListAdminGpuMetricScopes`, an `AdminRead`, and no tenant metadata. Tenant callers
use `AcceleratorUsageService.ListTenantGpuMetricScopes`, action
`ListTenantGpuMetricScopes`, and matching `TenantContext`/tenant metadata. Gov
checks resource readability before passing an optional complete original
`GpuUsageRef`. Tenant aggregate requests omit the ref and include retained
deleted-resource history and all three trusted owner services. Infrastructure
selectors never enter public tenant responses.

Apply `scripts/migrate 0008` with the explicit migration-role DSN, then grant the
existing runtime role SELECT on `gpu_metric_index`, SELECT/INSERT on
`gpu_metric_history` (the existing task grant script also covers these tables).
Revoke UPDATE/DELETE/TRUNCATE on both monitoring tables from the runtime role;
the history and its original activation time are immutable to runtime reads.
The identity sequence requires no separately exposed sequence privilege. Start
the new binary only after migration; runtime checks require schema version 8.
No automatic migration, historical backfill, deletion, or pruning is installed.
For rollback stop the new binary, retain both monitoring tables, restore the
previous binary's schema-version compatibility in a reviewed forward fix; do
not erase migration history or drop monitoring evidence to force an older
binary to start.

The index begins at migration time. Each binding reconciliation persists the
first monitoring association and changes of identity, runtime selector,
ownership confidence, or positive release state, within the existing binding
transaction. Poll timestamps alone do not append events. A later nil verified
ref closes the previous period and preserves it. `valid_from` is the first
persisted monitoring evidence time, never a retroactive claim about earlier
samples; `evidence_at` is the underlying source observation time.

The raw source key uses cluster, trusted node **name**, namespace, Pod name,
container, normalized vendor GPU UUID and available vdevice. Node UID, Pod UID,
ACC DeviceID and binding IDs remain evidence. UUID matching lowercases and strips
`GPU-`; it never changes the ACC DeviceID. A missing vdevice dimension is an
unconstrained selector, not an invented device identity.

All recorded candidates for a selected raw key are loaded before tenant/ref
output filtering. Equal complete UsageRefs across Pod UIDs share a business
lifecycle. Different tenants/resources/original create operations remain
`OWNERSHIP_AMBIGUOUS`; retired and older-than-30-day conflict evidence does not
expire automatically. Unknown candidate UIDs without a trusted association
produce `OWNERSHIP_UNKNOWN`. An ownership conflict cannot expose the other
candidate identity to a tenant. No exclusion/cache invalidation proof protocol
is invented here, so known cross-resource reuse remains unresolved until a
future trusted proof contract is implemented.

A singleton from before index activation is not proof that no old cache exists.
If the trusted original operation creation time precedes `index_started_at`,
the scope remains `OWNERSHIP_UNKNOWN/LIFECYCLE_PRECEDES_INDEX`. The normal
complete path starts with a controlled fresh lifecycle after index activation,
the owner-signed actual Pod identity, and no conflicting or incomplete candidate
history. Existing legacy lifecycles require a future trusted cache-exclusion
proof; current KSM UID, fixed waiting periods and old business end times cannot
upgrade them automatically.

Windows are bounded to the last 30 days. Page sizes are 1–200 (default 50).
Tokens bind the authenticated caller, all filters, and `snapshot_id`. Every page
uses a Repeatable Read transaction. If any append changes the index between
pages, `METRIC_SNAPSHOT_CHANGED` explicitly rejects the page and the caller must
restart the complete bounded query. It never returns a silently inconsistent
page. The count watermark detects late commits with lower identity sequence
values. A request loads at most 8192 relevant candidate events; larger scopes
fail `METRIC_SCOPE_BUDGET_EXCEEDED` rather than truncating. Gov/Obs must apply
their independent 128-source and query-wide budgets across all scope pages.

`index_started_at` and `index_complete=false/HISTORY_UNAVAILABLE` disclose
pre-index windows. Source freshness and sample/caching validity remain Obs
responsibilities; fresh scrapes alone do not prove cache incarnation. Current
allocation and quota semantics are unchanged. Query support does not establish
30 days of actual historical data or Prometheus retention.

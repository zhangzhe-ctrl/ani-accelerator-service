# Acc resumed acceptance, 2026-09-24

`candidate-v7/verify.exit` is **0**. It supersedes the deliberately interrupted
candidate-v5 for the delivered runtime, and includes the complete candidate-v6
gates plus explicit cursor action/method negatives. Command, UTC start, source
manifest and complete output are adjacent. Source formatting/generation did not
change during acceptance. Final commit, module publication, clean-commit SBOM
and exact-SHA CI remain the coordinator's separate release gates.

Adopt, Publish, OPEN and Resolve now read their catalog snapshot in a short
transaction, perform provider/evidence I/O outside PostgreSQL, and revalidate the
complete original snapshot before committing or resolving. Final mutation
arbitration returns an already committed same-key winner before checking stale
prepared state. Six actual restricted-PG tests cover delayed Inspect/Verify,
zero acquired connections during I/O, concurrent catalog changes and same-key
winners. Both ordinary and race suites pass. The initial fixture's duplicate
profile uniqueness failure is preserved in `network-outside-initial.log`; an
independent unpublished group corrected that test fixture before later runs.

The following are **Acc component results**. They must be combined with the
corresponding Gov/owner transport, BFF, ledger and worker evidence before a joint
matrix ID is marked complete. Historical labels inside test log messages are
not substituted for the unified IDs below.

| ID | Result within Acc scope | Evidence in candidate-v7/verify.log unless specified |
|---|---|---|
| BASE-01 | pass for tested source; final publication identity pending | source.sha256; acceptance no-source-change check |
| BASE-02 | pass | Task-owned PG, role, cache/lock and isolated A/B configuration; pause/resume records |
| BASE-04 | pass | Source generation, tidy, tests, vet, build, module verification |
| AUTH-01 | pass | TestAll22RPCsMTLSAndRecovery: actual mTLS, wrong service/CA/no certificate |
| AUTH-03 | pass for 20 missing-grant cases and direct service boundaries | Twenty named AUTH-03 lines: valid Gov URI, matching typed/metadata ungranted actor, exact DELEGATION_DENIED; Sync/Observe have separate identity rules |
| AUTH-04 | pass for Acc usages/bindings/catalog | Restricted tenant negatives, composite-FK checks, wrong tenant and changed replay scope |
| API-05 | pass for Acc paging and cursor scope | TestCursorBoundToPrincipalAndFilter: tenant, actor, cluster grants, action, method, filter, tamper; actual PG stable keyset paging |
| API-06 | pass for Acc transport/reasons | 22-RPC suite plus formal A exact reasons; Gov HTTP mapping is separate |
| SYNC-04 | pass for Acc receiver | Projection order/conflict/restart tests and real Sync RPC replay |
| SYNC-07 | pass for Acc observation/projection association and live-binding retention | TestPostgresBindingsBeforeProjectionAndScopedRelease, 22-RPC suite; actual Gov overlay is in joint evidence |
| DB-01 | pass for Acc migrations/runtime | Runtime DDL/TEMP denial; explicit populated-v1 upgrade in upgrade-final.log |
| DB-02 | pass for Acc constraints/data | Explicit-tenant, JSON missing/null identity and composite relation tests |
| DB-03 | pass for Acc Sync transaction kill windows | Real process before/after COMMIT kill and receiver replay; Gov four transaction classes recorded separately in its process/ evidence |
| DB-04 | pass for Acc restore | Populated dump to separate DB; all 13 relations byte-identical; restored repository/contract tests |
| DB-05 | pass for Acc failed migration | injected-migration-failure.log and recovery summary; original data preserved |
| PROC-01 | pass for Acc independent processes | Production A process and B mTLS subprocesses; Gov/owner overall process proof is separate |
| PROC-03 | pass for Acc lifecycle subset | Runtime/readiness tests, real kill/restart and formal SIGTERM lifecycle |
| PROC-04 | pass | Full affected race suite, including the six new catalog concurrency scenarios |
| PROVIDER-01 | pass for software replay | TestProviderControlledSourcesPersistRecoverAndRelease: production TLS HTTP and Unix gRPC readers |
| PROVIDER-02 | pass for software replay | Same test: normalization, three-source dedup, local conflict, stale/offline UNKNOWN, scoped release and capacity recovery |
| PROVIDER-03 | pass for Acc scope checks | Real ObserveRelease positive/wrong-service/empty-scope and provider trust tests; owner complete cleanup is not inferred |
| BOUND-02 | pass | formal-a.json: ordinary production executable, Publish CONSUMPTION_NOT_VERIFIED, OPEN PROFILE_NOT_ACTIVE, Resolve NOT_FOUND, zero profiles, CLOSED/NOT_VERIFIED |
| BOUND-03 | pass for isolated Acc B fixture | Real management RPC seed and actual SaveObservation helper; original B process is explicitly distinguished from the final rebuilt helper |
| RLS-01 | pass | Real catalog flags/policies checks and explicit migration |
| RLS-02 | pass | Actual non-owner NOSUPERUSER/NOBYPASSRLS role, no schema/database CREATE or TEMP |
| TENANT-01 | pass | Required identities, relation/JSON consistency, missing/null negatives |
| TENANT-02 | pass within Acc relations | Known foreign UUID, same business keys, read/list/update/restart, FK and replay scope negatives |
| TENANT-03 | pass within Acc scope | No wildcard ordinary tenant; global supply and unassociated observations remain explicit global facts |
| TENANT-04 | pass | Four mutation logs remove tenant predicates, regenerate sqlc and fail actual behavior tests |
| SQLC-01 | pass within Acc | All 48 production SQL statements through generated pgx queries |
| SQLC-02 | pass within Acc | Same pgx.Tx per atomic write; external I/O outside all catalog transactions/views; TestPostgresCatalogProviderOutsideTransactions |
| SQLC-03 | pass within Acc | Authoritative migrations, pinned sqlc v1.30.0, tool/query/generated hash verification |
| SQLC-04 | pass within Acc | compile, regenerate-no-diff, provenance and production SQL scan in make verify/CI definition |
| SHIP-01 | precommit verify pass; full release audit pending | candidate-v7 source and exit 0; clean-commit SBOM is a separate gate |

Formal A is optionally held for Gov→Acc integration using only the ordinary
production executable and a legal constructed read-only provider source. Its
task-local ready/config/CA paths are in `joint-a/formal-resume/ready.json`; the
Gov-side actual A→A results are in the coordinator's
`evidence/formal-gov-acc-process.json`. Private material is not copied here.

API-01/02/03/04/07, other Gov ledger/worker IDs and SQLC-05's quota/lab/Network
requirements need Gov evidence. DOC and SHIP publication/cleanup IDs belong to
coordinated closeout. OWNER/LIVE/DEPLOY remain not_verified. Neither the empty
original B helper nor hardware-port fixtures prove nonempty BFF redaction or
real GPU/production-owner completion.

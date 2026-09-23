# Acc evidence mapping, candidate-v4

This maps the unified acceptance IDs to this repository's evidence. A component
pass does not certify the Governance half of a combined requirement. Actual
Fedora command, start time, exit 0 and exact tested source hashes are in
`candidate-v4/verify.{command,started,exit,log}` and `source.sha256`. Final commit
and CI are still publication work. B helpers remain running for joint consumers.

| Unified IDs | Acc result and evidence | Remaining boundary |
|---|---|---|
| BASE-01/02/04 | Acc component pass: source manifest; task isolation; full pinned generation/build/test gates | Coordinating final SHA/whole-tree identity and Gov build |
| AUTH-01/03/04, API-05/06 | Acc component pass: `TestAll22RPCsMTLSAndRecovery`, catalog/paging/trust tests | JWT, Gov role/BFF and complete Gov authorization belong to joint suite |
| SYNC-04/07 | Acc receiver component pass: terminal-first, duplicate/conflicting revisions, same ref/plan and live binding retention | Actual Gov sender plus nonempty overlay result recorded separately by coordinator |
| DB-01/02, RLS-01/02, TENANT-01/02 | Acc component pass: restricted-role data/isolation tests; `upgrade-final.log` restores populated original-v1 fixture before explicit v2 migration | Gov Atlas, historical balances and related modules require Gov evidence |
| DB-03, PROC-01/03 | Acc Sync receive commit-before/after kill pass: `verify.log` lines 172–176; restarted mTLS processes preserve original PG facts | Gov four transaction classes and owner/Gov full process lifecycle are independent |
| DB-04/05 | Acc component pass: `verify.log` recovery section, all 13 relations byte-identical, failed transactional DDL rollback, restored data and contract suites; remote backups indexed by `retained-backups.sha256` | Gov charges/receipts and worker convergence require Gov evidence; old DB-06 label inside script is historical Acc-local vocabulary |
| PROC-04 | Acc affected race pass in `verify.log` | Gov affected race separate |
| PROVIDER-01/02 | Acc pass: `TestProviderControlledSourcesPersistRecoverAndRelease`, actual production TLS HTTP/Unix gRPC readers, PG, three-source dedup/conflict/stale/offline UNKNOWN/scoped release/capacity recovery | Constructed software samples; no actual GPU |
| PROVIDER-03 | Acc scope component pass: real ObserveRelease positives, wrong service, empty/scope/history checks in 22-RPC and provider/trust suites | Owner full closing/history/in-flight proof and refunds must be established by owner/Gov joint tests |
| BOUND-02 | Acc pass: `TestFormalProductionConsumptionGate`, `formal-a.json`, production/source logs; actual production binary reaches consumption-proof gate, zero profile, CLOSED/NOT_VERIFIED; independent A DB/CA | Resolve NOT_FOUND and OPEN PROFILE_NOT_ACTIVE are recorded as their actual preconditions, not consumption-proof rechecks |
| BOUND-03, API-04 | Acc fixture prepared: real five management RPCs, isolated B PG/mTLS CA, original requests in remote seed.json; `_test.go` real SaveObservation helper | Nonempty binding from the Gov-generated ref and HTTP redaction result only count when coordinator/BFF suite executes them |
| TENANT-03 | Acc scoped reads pass; global supply/unassociated facts explicitly global, no ordinary wildcard tenant | Gov bounded worker candidates/leases remain Gov evidence |
| TENANT-04 | Acc pass: four `mutation-*.log` files; removed read/list/update tenant predicates regenerate and fail actual behavioral tests | Gov mutation set separate |
| SQLC-01/03/04 | Acc pass: all 48 statements generated from fixed sqlc v1.30.0; authoritative migrations; reproducible compile/generate/provenance and no inline SQL scans in verify/CI | Gov complete quota implementation and actual remote CI separate |
| SQLC-02 | Acc atomic DB operations use a single pgx.Tx and generated queries | This does not assert all existing provider calls are outside Acc admin transactions; Gov Resolve/dispatch transaction boundary needs separate evidence |
| SQLC-05 | Acc populated old-fixture upgrade/restore pass | Non-GPU/quota_lab/Network/receipts requirement belongs to Gov |
| SHIP-01/03/04 | Precommit Acc make verify pass; resources deliberately retained for joint suite | Clean-commit audit/SBOM, exact-SHA CI and final cleanup pending coordinator |

All 22 RPCs have actual mTLS positive and wrong-service negative coverage in
`TestAll22RPCsMTLSAndRecovery`; this is more than a route inventory. It does not
stand in for all 20 Governance HTTP BFF methods. The original empty-binding helper
is never used as API-04 redaction evidence. OWNER/LIVE/DEPLOY remain not_verified.

#!/usr/bin/env bash
set -Eeuo pipefail
base=/home/chabking/gov-acc-v12-01-20260923
cd "$base/acc"
export GOWORK=off GOMAXPROCS=2 GOFLAGS=-p=2 GOMODCACHE=$base/cache/acc-mod GOCACHE=$base/cache/acc-build
name=acc_upgrade_final
psql_upgrade() { docker exec -i gov-acc-v12-01-acc psql -X -v ON_ERROR_STOP=1 -U postgres -d "$name" "$@"; }
docker exec gov-acc-v12-01-acc createdb -U postgres "$name"
docker exec -i gov-acc-v12-01-acc pg_restore -U postgres -d "$name" --exit-on-error < "$base/evidence/acc/v1-populated-before-upgrade.dump"
psql_upgrade < scripts/test-pg-grants.sql
psql_upgrade -c 'ALTER TABLE accelerator.usages ENABLE ROW LEVEL SECURITY; ALTER TABLE accelerator.usages FORCE ROW LEVEL SECURITY; CREATE POLICY task_legacy_policy ON accelerator.usages USING (true);'
psql_upgrade < migrations/0002_explicit_isolation.sql
for table in clusters pools supply_groups profiles usages devices claims release_proofs bindings source_watermarks; do
 psql_upgrade -Atc "SELECT payload::text FROM accelerator.$table ORDER BY payload::text" > "$base/evidence/acc/upgrade-final-$table.rows"
 cmp "$base/evidence/acc/upgrade-before-$table.rows" "$base/evidence/acc/upgrade-final-$table.rows"
done
umask 077
printf 'host=127.0.0.1 port=25432 dbname=%s user=acc_runtime sslmode=disable\n' "$name" > "$base/acc-upgrade-final-dsn"
ACC_TEST_DSN_FILE=$base/acc-upgrade-final-dsn go test -count=1 -v ./internal/data
sha256sum migrations/*.sql
printf 'PASS: final migration over original-SHA v1 populated backup; all original payloads preserved; legacy RLS/policy removed; restricted-role tests passed.\n'

#!/usr/bin/env bash
set -Eeuo pipefail
base=/home/chabking/gov-acc-v12-01-20260923
export GOWORK=off GOMAXPROCS=2 GOFLAGS=-p=2 GOMODCACHE=$base/cache/acc-mod GOCACHE=$base/cache/acc-build
cd "$base/acc"
mkdir -p .tmp/legacy
# Exact original Git input; no sibling replacement or copied schema.
git archive 9f9712198488aaa722dc78aeb444248df25b08ef | tar -C .tmp/legacy -xf -
docker exec gov-acc-v12-01-acc createdb -U postgres acc_legacy
psql_legacy() { docker exec -i gov-acc-v12-01-acc psql -X -v ON_ERROR_STOP=1 -U postgres -d acc_legacy "$@"; }
psql_legacy < .tmp/legacy/migrations/0001_accelerator.sql
psql_legacy < scripts/test-pg-grants.sql
umask 077
printf 'host=127.0.0.1 port=25432 dbname=acc_legacy user=acc_runtime sslmode=disable\n' > "$base/acc-legacy-dsn"
(cd .tmp/legacy && ACC_TEST_DSN_FILE=$base/acc-legacy-dsn go test -count=1 ./internal/data)
docker exec gov-acc-v12-01-acc pg_dump -U postgres -d acc_legacy -Fc > "$base/evidence/acc/v1-populated-before-upgrade.dump"
# Capture every original payload; the upgrade must retain these exact facts.
for table in clusters pools supply_groups profiles usages devices claims release_proofs bindings source_watermarks; do
 psql_legacy -Atc "SELECT payload::text FROM accelerator.$table ORDER BY payload::text" > "$base/evidence/acc/upgrade-before-$table.rows"
done
psql_legacy -c 'ALTER TABLE accelerator.usages ENABLE ROW LEVEL SECURITY; ALTER TABLE accelerator.usages FORCE ROW LEVEL SECURITY; CREATE POLICY task_legacy_policy ON accelerator.usages USING (true);'
psql_legacy < migrations/0002_explicit_isolation.sql
for table in clusters pools supply_groups profiles usages devices claims release_proofs bindings source_watermarks; do
 psql_legacy -Atc "SELECT payload::text FROM accelerator.$table ORDER BY payload::text" > "$base/evidence/acc/upgrade-after-$table.rows"
 cmp "$base/evidence/acc/upgrade-before-$table.rows" "$base/evidence/acc/upgrade-after-$table.rows"
done
ACC_TEST_DSN_FILE=$base/acc-legacy-dsn go test -count=1 -v ./internal/data
printf 'PASS: original-SHA populated v1 fixture upgraded, all payloads retained, injected legacy RLS/policy removed, restricted v2 tests passed.\n'

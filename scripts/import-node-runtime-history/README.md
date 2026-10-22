# Task-only legacy node history import

This offline tool is for the node-agent B/C maintenance window, not a new RPC or startup migration. It reads the original legacy runtime JSON and uses the production PostgreSQL repository/schema checks plus existing generated queries. It never creates an Agent identity, Report, epoch, log ID, sequence, ACK receipt, source observation or owner assertion. Existing Claims, Bindings, signed owner facts and ledgers are untouched; normal production owner verification still applies after collection.

The old collector must have desired/status replicas zero and no remaining Pods in `ani-accelerator`. All existing groups containing the source Node must be CLOSED. The new Agent must already have genuinely authenticated and persisted a current snapshot/receipt for the same Node UID/name. Group advisory locks and the node row lock protect import. Existing container IDs require identical Pod namespace/name/UID/container identity and are never overwritten, even by an old positive EXITED. New attempts preserve the exact original observation/finish times and carry the original file SHA256 in an explicit legacy-history issue. Static-Pod runtime UIDs remain their original references, including 32-hex UIDs.

Each commit inserts only absent attempts and advances fact revision once if needed. It does not advance consumed revision or change source freshness/gaps. Repeating the same file is a no-op. Any identity conflict rolls back the whole import. Preserve the source JSON and JSON receipt under the task's 0700 directory/0600 files. A live preflight copy is not the final stopped-writer source.

On Fedora, build from the matching ACC checkout:

```sh
GOTOOLCHAIN=go1.26.10 GOWORK=off go build -o "$PRIVATE/import-node-runtime-history" ./scripts/import-node-runtime-history
GOTOOLCHAIN=go1.26.10 GOWORK=off go test -count=1 -v ./scripts/import-node-runtime-history
```

Before the window, `--validate-source-only` validates a file without DB/cluster access and cannot be combined with `--apply`. In the window, stop the old writer first, then capture the final original host file through the task Agent's temporary read-only legacy-history mount. Do not return the host mount to the center. Remove the temporary Agent mount after import, retaining its normal durable state directory.

```sh
umask 077
kubectl -n ani-node-system exec "$AGENT_POD" -- sh -c 'cat /migration/legacy-runtime-events/*.json' > "$FINAL_SOURCE"
FINAL_SHA=$(sha256sum "$FINAL_SOURCE" | cut -d ' ' -f 1)
```

Set only nonsecret scope values and a private DSN **file path**. Obtain `SOURCE_SOCKET` from the original file's `Socket` field, never from a command supplied by a remote request. Use the same reviewed arguments for preview and apply:

```sh
"$PRIVATE/import-node-runtime-history" --dsn-file "$ACC_RUNTIME_DSN_FILE" \
  --source "$FINAL_SOURCE" --sha256 "$FINAL_SHA" --source-socket "$SOURCE_SOCKET" \
  --cluster-id "$EXISTING_CLUSTER_ID" --connection-ref managed:ani-lab-formal \
  --node-uid af992ed9-cbe1-4f64-8fac-7433808bfab2 --node-name ani-03 > "$PRIVATE/history-preview.json"
```

Preview performs the same transaction but rolls back (`Applied=false`). Only after reviewing counts/digest, repeat with `--apply` and save a different receipt path. A failed or ambiguous commit requires a repeat preview to inspect actual dedup coverage; do not reset telemetry state or manufacture a receipt. Verify original IDs/admission states, unchanged historical business row counts, imported/current attempts, current facts and the original owner release path before reopening only the previously Open group. Never reopen a group that was originally Closed.

# Outbound SCIM operations and recovery

SCIM targets use durable database claims to exclude concurrent outbound changes across API instances. These claims **do not expire**. A timeout, disconnection, HTTP 202/408/5xx response, process exit, or failed local completion cannot prove that a remote write has stopped. A gateway may return 502/503/504 while its provider continues the PUT or DELETE. Automatic retries must not clear a claim or permit a later write to overtake it.

## Normal operation

`SyncTarget` processes at most 50 live mappings and has a 30-second batch deadline. Its persisted `(sync_resource_type, sync_resource_id)` cursor continues on the next call. It returns the successful count and any partial failures. This service method currently has no dedicated HTTP endpoint. The periodic worker visits at most eight targets per tick, ordered by target ID, with a 30-second total deadline; it retains its target cursor between ticks. Target failures persist a retry delay of 2 seconds, doubling up to 1,024 seconds. An individual HTTP request retains the configured `http_timeout` (default 10 seconds).

Retirement remains a synchronous HTTP operation which reads mappings in pages. It persists `retiring = true`, blocks new pushes and configuration changes, and preserves credentials until every live mapping is deprovisioned. A definitive remote error allows a later retirement call to resume. An unfinished operation returns conflict instead of starting a competing write. Explicit manual provisioning after an ordinary deprovision remains supported; stale automatic synchronization cannot revive a tombstone.

Before a first PUT or explicit reprovision, a live mapping intent is committed. This keeps principal erasure and target deletion from losing remote accountability even if the process dies before it learns the remote identifier. A rejected first attempt may therefore retain a mapping; successful deprovision (including remote 404) resolves that responsibility.

## Diagnose an unfinished operation

Only explicit rejection responses (400, 401, 403, 404, 405, 409, 410, 412, 413, 414, 415, 422, 429) allow automatic release; DELETE 404 instead completes the tombstone normally. HTTP 408, all 5xx, redirects and unknown statuses preserve the claim, even when the error body arrived completely. A provider returning 5xx repeatedly therefore remains blocked after the first uncertain operation rather than automatically retrying writes. Investigate `iam_scim_unfinished_operations` and `iam_scim_oldest_operation_age_seconds` alongside `iam_scim_failed_targets`; restoring provider health alone is not recovery evidence.

Run these read-only queries against the **writer** database. Replace `<prefix>` with the configured, validated table prefix, and bind `:tenant_id` and `:target_id` using the database client's parameter syntax. Do not select the bearer-token ciphertext or log user payloads.

```sql
SELECT id, tenant_id, status, retiring, operation_token, operation_kind,
       operation_resource_type, operation_resource_id, operation_started_at,
       sync_resource_type, sync_resource_id, sync_failures, sync_after_ms
FROM <prefix>iam_scim_targets
WHERE tenant_id = :tenant_id AND id = :target_id;

SELECT r.resource_type, r.resource_id, r.external_id, r.deleted_at, r.version
FROM <prefix>iam_scim_target_resources AS r
JOIN <prefix>iam_scim_targets AS t ON t.id = r.target_id
WHERE t.tenant_id = :tenant_id AND t.id = :target_id
  AND r.resource_type = t.operation_resource_type
  AND r.resource_id = t.operation_resource_id;
```

`operation_token = 0` means no outstanding claim. A nonzero token identifies one attempt; `operation_kind` distinguishes push, deprovision, retirement preparation, replacement, and deletion. The resource fields identify the exact user/group for outbound HTTP operations. `operation_started_at` is diagnostic information, never a lease expiry. `sync_after_ms` is a Unix timestamp in milliseconds.

## Recovery requirements

### Migration maintenance boundary

Both upgrading to migration 6 and rolling it back require a maintenance window. Stop admission of **all** SCIM writes across every API instance, disable every SCIM worker, and stop or otherwise fence every old writer process so a paused pre-dispatch operation cannot resume later. Drain local operations and establish through provider-side evidence that accepted remote requests, including asynchronous work, have ended. A local shutdown alone does not cancel an already accepted remote write.

Only then inspect claims/retirement and perform the migration. The rollback COUNT check is a refusal guard, **not** an online exclusion lock: a running writer can claim a target after COUNT returns zero and before ALTER removes coordination columns. A one-time zero count is therefore insufficient. An upgrade must likewise not mix old writers, which do not participate in claims, with new writers. Keep writes stopped until schema changes have completed and all restarted instances run the matching implementation. If remote completion remains unknown, retain the existing schema and blocked state; do not proceed with either migration as a recovery shortcut.

### Resolving an individual operation

1. Keep the claim and credentials intact. Identify the originating process and correlate the target/resource and time with remote audit or request records. A restarted process must not take over the claim.
2. Establish that the original remote request has definitively completed or can no longer execute. This requires provider-side evidence or a provider-supported cancellation/drain guarantee, including any asynchronous queue. Stopping the local process, waiting longer than `http_timeout`, or observing one GET/404 is **not sufficient**: an already accepted PUT may still execute later.
3. Reconcile the provider's final resource identifier and outcome with the preserved mapping intent. If completion cannot be established, leave the claim blocked and escalate to the provider; do not report retirement or erasure complete.
4. Only after that evidence exists, prepare a reviewed, provider-specific repair transaction which checks the exact tenant, target, operation token, and mapping version; reconciles the mapping; records the recovery evidence through the deployment's audit/change process; and clears the operation fields atomically. There is deliberately no generic unlock endpoint or automatic SQL reset in this runbook. A token mismatch means stop and re-read current state.
5. Retry deprovision/retirement through the normal API and verify both the remote outcome and local tombstone. Keep `retiring` set until retirement completes. Do not bypass the live-mapping erasure guard or downgrade migration 6 while claims or retirement are outstanding.

## Independent worker monitoring

These metrics use the existing module Prometheus collector. They do not change API/login readiness when an external directory fails. Enable the deployment's metrics endpoint and configure its actual alert receiver; the examples below are acceptance criteria, not evidence that production alert routing exists.

| Signal | Suggested investigation / alert condition |
|---|---|
| `iam_scim_unfinished_operations`, `iam_scim_oldest_operation_age_seconds` | A claim older than the normal request budget needs investigation; age never authorizes unlocking. For example, oldest age above 60 seconds for 5 minutes. |
| `iam_scim_failed_targets` | A nonzero value persisting 15 minutes indicates failed batches requiring recovery. |
| `iam_scim_sync_failed_batches_total` / `iam_scim_sync_batches_total` | A sustained failure ratio over 15 minutes indicates worker or provider degradation. |
| `iam_scim_sync_pending_mappings` | Live mappings after the stored cursor on due active targets. Compare with observed throughput and directory size; it is a work estimate, not a promise of remote drift. |
| `iam_scim_sync_enabled`, `iam_scim_sync_last_success_timestamp_seconds` | Apply stale-worker alerts only when enabled, using a threshold greater than the configured sync interval and batch budget. An empty successful batch does not clear `iam_scim_failed_targets` or unfinished claims. |
| `iam_scim_metrics_database_success` | Zero means operational state could not be read; do not interpret other zero gauges as recovery. |
| `iam_scim_operation_release_failures_total` | Any increase requires checking durable claims and writer database availability. |

Before production acceptance, exercise sustained provider 5xx, a delayed PUT that outlives its client, multi-page directories, and a real routed alert. Verify recovery with two independently connected instances on each supported production database engine. SQLite unit/race results alone are not PostgreSQL/MySQL HA evidence.

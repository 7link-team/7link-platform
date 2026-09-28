# Migration And Recovery Runbook

Rehearsed procedures for rolling migrations back and forward, backing the IAM
schema up, restoring it, and reconciling erasures a restore has undone.

Every command below was executed against a pinned MySQL 8.4.11 container on
2026-09-08 and the observed result is recorded with it. Where a number is an
observation from that rehearsal rather than an agreed target, it says so.

## What This Runbook Does Not Set

**RTO and RPO are not defined here.** The rehearsal timings below are what one
isolated database did on one machine; they are evidence that the procedure
works, not a commitment about production. Whoever owns the service decides how
much downtime and how much data loss is acceptable, and that decision belongs in
the service description rather than in this file.

## Preparing A Drill Database

The gate transport is whatever `make iam-preflight` resolved for this machine,
which may be a Docker daemon on another host. Nothing below assumes a local one.

```sh
make iam-preflight                      # resolves DOCKER_CMD / DOCKER_TEST_HOST
```

Start an isolated MySQL 8.4 with the same image and SQL mode the release gate
uses, publish its port, and point a config at it. `scripts/test-mysql8.sh` shows
the exact flags; a drill uses the same ones so it rehearses the engine that
production runs, not a compatible substitute.

## Migration Status Is The First Thing To Read

```sh
7link-api migrate status   --config <config>
7link-api migrate validate --config <config>
```

`status` reports every module's current and target version. `validate` additionally
refuses a state the embedded migrations cannot explain. Read one of them before
and after every step below; a step that leaves a module `pending`, `ahead` or `diverged` has
not finished, whatever else looked successful.

Rehearsed: 13 modules, all `clean`, after each of the three drills.

## Drill 1 — Rolling One Module Back And Forward

The narrow case: a release added migrations and they have to come out again.

```sh
7link-api migrate down-to --module iam --to 14 --config <config>
7link-api migrate up-to   --module iam --to 15 --config <config>
```

Rehearsed on `iam` 15 -> 14 -> 15. Status returned to `clean` and the seeded
principal and platform administrator were still present afterwards.

Data loss is per migration, not general: rolling past a migration that dropped a
column loses that column's contents. `docs/iam-local-uat.md` records which of the
menu-translation migrations are lossy in which direction. **Back up before any
schema rollback** rather than reasoning about which ones are safe.

## Drill 2 — Rolling Everything Back And Forward

The broad case: a deployment has to be taken back to nothing and rebuilt.

Order matters and is not alphabetical. A module must come down before the module
it depends on, or the dependency guard refuses -- correctly, because rolling a
dependency back first would orphan a milestone that is still applied.

```sh
for module in federation authn oauth governance provisioning organization \
              iam identity todo iam-audit audit wuid dlock; do
  7link-api migrate down-to --module "$module" --to 0 --config <config>
done
7link-api migrate auto --config <config>
```

Rehearsed: all 13 modules reached 0, then `migrate auto` returned all 13 to
`clean`. Thirteen tables survived the rollback -- one goose version table per
module, which is by design; the tables holding data were gone, and the data with
them. Restoring it is Drill 3.

## Drill 3 — Backup And Restore

```sh
# Backup
<docker> exec -i <container> mysqldump --user=<user> --password=<password> \
  --single-transaction --no-tablespaces --routines --triggers --events \
  --hex-blob <database> > backup.sql

# Restore
<docker> exec -i <container> mysql --user=<user> --password=<password> <database> < backup.sql
```

`--triggers` is not optional. The principal tombstone guards are triggers, so a
dump taken without them restores a schema that no longer refuses to bring erased
data back. `--no-tablespaces` avoids requiring the application backup identity
to hold MySQL's global `PROCESS` privilege.

Inventory trigger definers before the incident. The restore identity needs
`SET_ANY_DEFINER` (or the corresponding tightly controlled administrative
capability) to recreate a trigger carrying another account's definer, and every
named definer must exist with permission to execute its trigger afterwards. In
the rehearsal, root could restore all 58 triggers, but the first application
write then failed with MySQL error 1449 until the original `iam_uat@%` definer
identity was recreated and granted access to the restored database. A successful
import alone is therefore not proof that the restored schema is operable.

Rehearsed: a 219 KB dump of 88 tables restored in **1 second** (observation, not
an RTO). Afterwards the seeded principal and platform administrator were present,
`information_schema.triggers` reported 58 triggers, and `migrate validate`
reported all 13 modules `clean`.

Verification SQL:

```sql
SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = DATABASE();
SELECT COUNT(*) FROM <prefix>iam_principals;
SELECT COUNT(*) FROM <prefix>iam_platform_administrators;
```

## Erasure Reconciliation After A Restore

**A restore can undo an erasure, and by itself leaves no sign that it did.**

Three things record that a principal was erased, and a database restore rolls
back all three together:

| What | Where it lives |
|---|---|
| The redacted rows | the owner tables |
| The tombstone (`anonymized_at`) | `iam_principal_lifecycles` |
| The `principal_anonymized` event | `iam_audit_events` |

Verified against the rehearsal dump: `iam_principals`,
`iam_principal_lifecycles` and `iam_audit_events` are all inside the same backup.
So restoring a backup taken before an erasure returns the person's data *and*
removes the record saying it should have been removed. Nothing inside the
restored database can then tell you an erasure is owed.

Accepted erasures now first create a database-external, COMPLIANCE-locked
obligation under `audit/erasures/{principal_id}/{lifecycle_version}.json`. The
database transaction cannot commit an erasure unless that exact object version
has been read back and its content, SHA-256 and retention verified. A database
failure after the object write may leave an obligation for work that did not
commit; that is intentional and must be reconciled rather than deleted.

When offering permanent erasure, the deployment must set `iam_audit.erasure_ledger.enabled: true` on every replica
that accepts erasure. Its `iam_audit.anchor.retention_days` must exceed the age
of the oldest database backup that can be restored plus the operational recovery
window. Do not reopen access if that coverage relationship is false or unknown.

Enabled ledgers expose a separate optional `iam-audit-erasure-ledger` health check.
It performs a bounded, read-only Object Lock configuration request and requires
`s3:GetBucketObjectLockConfiguration`. An unhealthy ledger must alert the erasure
operator; it does not remove otherwise healthy login servers from readiness.
This probe does not prove write permission: each actual erasure still requires
the immutable obligation write and verified read-back before database erasure.

Reconcile every restore, including one believed not to cross an erasure:

1. Keep admission and background writers disabled. Record the backup point,
   recovery point, object-store bucket and configured retention period.
2. Read and verify the external ledger, not the restored audit table:

   ```sh
   7link-api iam list-erasure-obligations --config <config> \
     > erasure-obligations.ndjson
   ```

   Each NDJSON line contains `principal_id`, `lifecycle_version`, `object_key`,
   exact `version_id`, `sha256` and `retain_until`. The command reads all object
   versions, so a delete marker does not hide a locked obligation. It does not
   initialize application modules or connect to the restored database. Any
   list, decode, key, hash, version or retention failure stops recovery.
3. For each listed principal whose lifecycle in the restored database is not
   already anonymized at that version or later, re-run the platform erasure:
   `POST /platform/iam/principals/{principal_id}/lifecycle/anonymization`.
   The retention policy and suspension it requires were also rolled back, so
   they have to be re-recorded first -- the endpoint refuses otherwise, which is
   the intended behaviour rather than an obstacle.
4. Run the external list again and retain its output with the recovery evidence.
   Confirm every listed obligation has a matching lifecycle tombstone at the
   same or later version, the principal and all owner copies contain no PII, and
   a `principal_anonymized` audit event exists. Only then re-enable admission.

The historical audit archive remains useful evidence but is not a substitute:
its age policy can omit recent erasures. An empty ledger is evidence only when
the ledger was enabled throughout the entire interval covered by every
restorable backup and its retention was long enough.

Rehearsed on 2026-09-09 with an isolated MySQL 8.4.11 database and a disposable
MinIO S3-compatible Object Lock service:

- A 354,263-byte backup was taken after creating a synthetic principal but
  before its retention decision and suspension.
- The first erasure reached lifecycle version 2 and wrote one verified external
  obligation. The principal and membership copies were redacted, the credential
  was removed, and one tombstone and one `principal_anonymized` event remained.
- Restoring the older backup made the login, email, active state and credential
  return while both the tombstone and erasure event returned to zero. All 58
  triggers were present and all 13 migration modules still validated `clean`.
- `iam list-erasure-obligations` still returned the obligation with its original
  object VersionID and SHA-256 despite the database rollback. The restored
  database was not configured or consulted by that command.
- Re-recording the expired retention decision, suspending and erasing again
  restored the version-2 tombstone and audit event and removed both principal
  and membership PII plus the credential. The ledger still contained exactly
  the original object version and digest: reconciliation verified it rather
  than replacing it.

The delete-marker retry case was separately reverified against MinIO
RELEASE.2025-09-07T16-13-09Z (container image digest
`sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e`):
after an unversioned delete, a retry found the first locked version and did not
publish a second obligation version.

The drill used a one-day object retention only because both containers were
disposable. Production must use the longer backup-coverage relationship stated
above. The drill containers and temporary dumps were deleted; the existing local
UAT database was only read to seed the isolated copy.

## Known Gaps

- RTO and RPO are undecided (see above).
- These drills used one isolated database. They do not cover replication,
  failover, or a multi-node deployment.

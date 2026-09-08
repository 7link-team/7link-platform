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
and after every step below; a step that leaves a module `pending` or `dirty` has
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
  --single-transaction --routines --triggers --events --hex-blob <database> > backup.sql

# Restore
<docker> exec -i <container> mysql --user=<user> --password=<password> <database> < backup.sql
```

`--triggers` is not optional. The principal tombstone guards are triggers, so a
dump taken without them restores a schema that no longer refuses to bring erased
data back.

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

The record that survives is the one kept outside the database: the audit archive
(`docs` and the archive workflow), which is append-only and stored externally.
After any restore that crosses an erasure:

1. List `principal_anonymized` events from the **archive**, not from the restored
   `iam_audit_events`, for the window between the backup and the restore.
2. For each principal named, re-run the platform erasure:
   `POST /platform/iam/principals/{id}/lifecycle/anonymization`.
   The retention policy and suspension it requires were also rolled back, so
   they have to be re-recorded first -- the endpoint refuses otherwise, which is
   the intended behaviour rather than an obstacle.
3. Confirm each principal's lifecycle reports `anonymized`, and that
   `GET /platform/iam/principals/{id}/lifecycle` lists `already_anonymized`.

**Not yet rehearsed.** Drills 1-3 above were executed; this reconciliation
procedure is derived from where the three records are stored and has not been
run end to end. It needs an environment with the audit archive configured
against an object store, which the drill database did not have.

## Known Gaps

- RTO and RPO are undecided (see above).
- Erasure reconciliation is written but not rehearsed.
- These drills used one isolated database. They do not cover replication,
  failover, or a multi-node deployment.

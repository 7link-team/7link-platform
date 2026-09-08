# Local IAM UAT

This is an isolated, single-machine acceptance environment, not a production
deployment or an agreed RTO/RPO. It runs the real API, IAM console and pinned
MySQL 8.4 image. It does not use API mocks or an existing business database.

## Prerequisites

- macOS or Linux, Node.js 22+, the repository's Go toolchain, Make and Bun.
- A running local Docker daemon selected by a Unix-socket Docker context.
  Remote contexts and `DOCKER_HOST` / `DOCKER_CONTEXT` overrides are refused.
- Frontend dependencies installed from `apps/frontend-web-admin` with
  `bun install --frozen-lockfile`; network access for uncached Go modules/images.
- Run commands below from `apps/backend-api`. Do not use `sudo`.

## Initialize And Start

```sh
node scripts/iam-uat.mjs init
node scripts/iam-uat.mjs start
node scripts/iam-uat.mjs verify
node scripts/iam-uat.mjs status
```

`init` builds a separate API binary, runs the existing `migrate auto` and
`iam bootstrap-platform-admin` commands, and records the binary SHA-256 and Git
revision. It never overwrites an existing initialization. Interrupted initialization
is retained for inspection; use the preserving reset below before retrying.

`start` supervises the API and console in the background, waits for readiness,
and reuses `bootstrap-audit-environment.mjs` to create the UAT tenant through the
real API. Repeating start/tenant initialization does not recreate the tenant.
The server never automatically migrates on restart. Run `init` in a fresh UAT
when testing a new build rather than replacing a running binary.

Default console: `http://127.0.0.1:18091`; API: `http://127.0.0.1:18080`.
The initializer chooses alternate free ports if those are occupied. Always use
the URLs printed by `start` or `status`. All listeners, including MySQL and gRPC,
bind to loopback. Selected ports are stable across restarts; a later collision
fails rather than connecting to another service.

## Accounts And Secrets

| Persona | Login | Password File | Expected Access |
| --- | --- | --- | --- |
| Platform/tenant administrator | `admin@example.test` | `.temp/iam-uat/admin-password` | Platform administration and UAT tenant ownership |
| Unprivileged tenant member | `member@example.test` | `.temp/iam-uat/member-password` | Sign-in and own session, no assigned roles or administrative access |

The member is created by the first `verify` run through the principal API.
Passwords and four independent encryption/signing keys are randomly generated,
stored under a mode-0700 directory in mode-0600 files, and never printed by the
script. The configuration and MySQL environment file also contain secrets and
must not be attached to tickets, copied into chat, or committed. Existing
`.gitignore` rules exclude the entire `.temp/` directory.

Read the password file locally when signing in. Deliver formal UAT credentials
through the organization's approved secret channel, not this document. These
accounts are local synthetic fixtures, not real people. Do not use production
passwords or copy production data into this environment.

HTTP-only session cookies are enabled solely because this environment is
loopback HTTP. Do not expose it through a tunnel or change its bind addresses.
Use HTTPS and secure cookies for a separately designed shared UAT environment.

## Stop, Restart And Reset

```sh
node scripts/iam-uat.mjs stop
node scripts/iam-uat.mjs start
node scripts/iam-uat.mjs verify
```

Stop sends a graceful signal to the verified UAT supervisor, waits for its API
and console children to exit, then stops only the workspace-labelled MySQL
container. Database data, credentials and the binary remain available.

```sh
node scripts/iam-uat.mjs reset --preserve-old
node scripts/iam-uat.mjs init
node scripts/iam-uat.mjs start
node scripts/iam-uat.mjs verify
```

Reset stops the environment, renames the old container and moves the old state
directory to names ending in the same `-saved-<timestamp>` suffix. It does not
drop tables, remove volumes, or delete secrets. The next init creates a new
database and new credentials. Saved containers remain stopped and consume disk
space; cleanup is a separate, explicitly targeted operator action.

Preservation is not a backup/restore drill. Saved configurations contain absolute
key paths and the original database binding. To recover a saved environment,
first stop and preserve any newer environment, then restore the selected saved
directory and container to their original names together. Inspect its port binding
before starting; never start saved and current containers on the same port.

## Evidence And Acceptance

`verify` uses the console's real `/api` proxy and records
`.temp/iam-uat/verification.json`. It verifies readiness, both logins, zero member
roles, denied tenant administration, denied platform lifecycle access, rejected
cross-origin writes, and invalid sessions after logout. It does not alter existing
roles or reset a password to make a test pass. A modified fixture fails visibly.
Recorded tenant/member IDs must survive subsequent verification; a missing member
is not silently recreated after the initial persona setup.

Operational logs: `.temp/iam-uat/uat.log`. Build identity and selected ports:
`.temp/iam-uat/state.json`. These are local evidence, not an immutable release
candidate or shared-environment sign-off.

Before formal acceptance, complete and record these separately:

- Desktop and mobile browser login, tenant selection, navigation and logout.
- Administrator lifecycle workflow and unprivileged-member denial in the UI.
- Seven-owner populated erasure and both writer orderings on the release build.
- Backup/restore with post-backup erasure reconciliation before reopening access.
- Audit archive/object-store recovery and integrity verification.
- Coordinated reverse-dependency rollback and roll-forward on a disposable clone.
- Measured recovery time and recovery point, compared with approved business
  RTO/RPO targets; designated product/security/QA/operations sign-off.

The local environment does not configure external IdPs, outbound SCIM providers,
mail delivery, or an S3 object-lock store. Their acceptance remains open.
HTTP verification must not be reported as browser acceptance.

## Local Verification Record (2026-09-08)

- Real MySQL initialization, backend build/migration/bootstrap, console startup,
  repeated start/init, graceful stop/restart and preserving reset: PASS.
- Nine proxy/API checks, including stable tenant/member IDs after restart: PASS.
- Mode-0700 directory, eight mode-0600 secret/config files, and generated-secret
  value scan of the UAT log: PASS.
- Reset without the preservation flag and Docker endpoint overrides: refused as
  expected. Old stopped container, data volume and secret files remain present.
- Initial random Docker host-port binding changed on restart. The implementation
  now selects an available port and publishes it explicitly; restart retest PASS.
- Browser workflows, recovery drills and formal production acceptance: NOT RUN /
  NOT READY. See the remaining checklist above.

# Local IAM UAT

This is an isolated, single-machine acceptance environment, not a production
deployment or an agreed RTO/RPO. It runs the real API, IAM console and pinned
MySQL 8.4 image. It does not use API mocks or an existing business database.

## Prerequisites

- macOS or Linux, Node.js 22+, the repository's Go toolchain, Make and Bun.
- A running local Docker daemon selected by a Unix-socket Docker context.
  Remote contexts and `DOCKER_HOST` / `DOCKER_CONTEXT` overrides are refused.
- Frontend dependencies installed from `apps/frontend-web` with
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
The isolated configuration enables registration and passkeys for browser UAT.
Its captcha test answer is randomly generated per initialization, and its local
notification receiver listens only on loopback and records only the most recent
synthetic notification under `.temp/iam-uat/`.

`start` supervises the API and console in the background, waits for readiness,
and reuses `bootstrap-audit-environment.mjs` to create the UAT tenant through the
real API. Repeating start/tenant initialization does not recreate the tenant.
The server never automatically migrates on restart. Run `init` in a fresh UAT
when testing a new build rather than replacing a running binary.

Default console: `http://localhost:18091`; API: `http://127.0.0.1:18080`.
The initializer chooses alternate free ports if those are occupied. Always use
the URLs printed by `start` or `status`. All listeners, including MySQL and gRPC,
bind to loopback. Selected ports are stable across restarts; a later collision
fails rather than connecting to another service.

The console uses `localhost` because WebAuthn permits that development domain as
an RP ID but does not permit an IP address to act as `localhost`. The Vite listener
still binds to `127.0.0.1`, and the generated allowlists contain both loopback
origins for compatibility; neither listener is exposed beyond the local machine.

## Existing Menu Translation Upgrade

Core migration v14 adds `iam_menus.translations`; v15 backfills the 15 built-in
menu routes in every tenant during the normal `migrate auto` workflow. No browser
editing or local backfill script is required. Only exact built-in route/name pairs
with `translations = '{}'` qualify (including the legacy accounts menu name).
Custom labels/routes and nonempty translation maps, even partial maps, are preserved.
New tenant initialization writes translations through `DefaultMenus()` as before.

Rolling v15 back to v14 retains translated data, including subsequent edits;
reapplying v15 leaves those values intact. Rolling v14 back removes the column
and therefore its translations. Back up before any schema rollback.

`TestIAMDefaultMenuTranslationUpgrade` covers old-version data, two tenants,
customizations, prefix variants, repeated migration and rollback/re-upgrade.
The real MySQL gate includes this test using isolated tables, not the UAT data.

## Accounts And Secrets

| Persona | Login | Password File | Expected Access |
| --- | --- | --- | --- |
| Platform/tenant administrator | `admin@example.test` | `.temp/iam-uat/admin-password` | Platform administration and UAT tenant ownership |
| Unprivileged tenant member | `member@example.test` | `.temp/iam-uat/member-password` | Sign-in and own session, no assigned roles or administrative access |

The member is created by the first `verify` run through the principal API.
Passwords, the captcha test answer and four independent encryption/signing keys are randomly generated,
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

Browser verification uses the frontend's existing Playwright dependency and an
installed Chrome; it does not require the agent's embedded browser tool. From
`apps/backend-api`, with UAT running and `verify` already completed:

```sh
node ../frontend-web/apps/web/tests/local-uat-browser-audit.mjs
```

This drives actual UI login, tenant selection, navigation, read-only lifecycle
guards, member access denial and logout/protected-route redirect for both local
personas at 1440x1000 and 390x844. It uses fresh browser contexts, no API mocks,
and logs out its sessions. Report and screenshots are under
`.temp/iam-uat/browser/`. Screenshots are captured with animations completed.
This script does not execute permanent erasure or alter retention policy.

The complete workflow audit also exercises the directory, authorization,
governance, provisioning, registration and passkey screens. It uses `localhost`
for the WebAuthn RP ID while retaining the same loopback listener:

```sh
IAM_CONSOLE_URL="http://localhost:$(node -p "require('./.temp/iam-uat/state.json').webPort")" \
IAM_BROWSER_PASSWORD_FILE="$PWD/.temp/iam-uat/admin-password" \
IAM_BROWSER_TENANT="$(tr -d '\r\n' < .temp/iam-uat/tenant-id)" \
IAM_NOTIFICATION_FILE="$PWD/.temp/iam-uat/notification-last.json" \
IAM_CAPTCHA_TEST_ANSWER_FILE="$PWD/.temp/iam-uat/captcha-test-answer" \
IAM_DATABASE_CONTAINER="$(node -p "require('./.temp/iam-uat/state.json').container")" \
IAM_DATABASE_TABLE_PREFIX=uat_ \
node ../frontend-web/apps/web/tests/run-browser-audits.mjs
```

The registration audit reads its randomly generated test answer from a file and
uses a read-only query inside the owned MySQL container to verify email,
activation and zero-membership state. It does not print database credentials.
The workflow creates synthetic global accounts and completed governance records;
use the preserving reset before a new formal run rather than reusing a database
that contains failed or partial acceptance fixtures.

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
real mail delivery, or an S3 object-lock store. Its loopback notification receiver
only supports synthetic registration verification. External acceptance remains open.
HTTP verification must not be reported as browser acceptance.

Permanent-erasure browser acceptance must use a separate disposable database and
an S3-compatible object-lock store. It must never target the retained local UAT
personas. After starting that isolated deployment, run the frontend audit with
all paths and identifiers explicit:

```sh
IAM_ERASURE_ACCEPT_DISPOSABLE_DATABASE=true \
IAM_CONSOLE_URL=http://127.0.0.1:<console-port> \
IAM_BROWSER_TENANT=<disposable-tenant-id> \
IAM_BROWSER_PASSWORD_FILE=<disposable-admin-password-file> \
IAM_ERASURE_REPORT_DIR=<private-report-directory> \
bun --cwd ../frontend-web/apps/web run audit:permanent-erasure
```

The safety acknowledgement and loopback URL check are mandatory. The audit creates
a random synthetic principal, disables it, records an expired retention decision,
types the login confirmation, performs permanent erasure, and reads the versioned
tombstone back through the API. It writes a mode-0600 JSON report and before/after
screenshots. Complete acceptance also requires independent reconciliation of the
external erasure obligation and the database state; the browser report alone is
not sufficient.

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
- Browser login/tenant/navigation/read-only lifecycle guard/access denial/logout
  on desktop and mobile: PASS for all four persona/viewport combinations using
  Playwright 1.58.2 and Chrome 152.0.7977.76. Screenshots visually inspected.
- Existing session-tenant browser audit: PASS for unique membership adoption,
  multiple-membership ambiguity, explicit tenant persistence and revocation. That
  audit created a synthetic member and second tenant; it does not modify the two
  documented UAT personas.
- Permanent-erasure UI execution, recovery drills and formal production
  acceptance remain NOT RUN / NOT READY. See the remaining checklist above.

## Local Verification Record (2026-09-09)

- A fresh environment generated from the current working tree enabled the local
  registration receiver and WebAuthn RP configuration; nine proxy/API checks:
  PASS.
- Complete browser workflow aggregator: 13/13 PASS. This includes department,
  position, static/dynamic group, business entity, tenant, role, invitation,
  access request, access review, service account, SCIM, registration and passkey.
- The run found a real MySQL 8.4 defect in access-review creation: the dynamic
  group snapshot query used reserved word `groups` as an unquoted alias. The
  alias was replaced and the real MySQL browser workflow then completed.
- Administrator/member by desktop/mobile: 4/4 PASS. Session tenant behavior,
  desktop-redact/mobile-omit profile policy, partial PATCH, account navigation,
  and the three-language by two-viewport workspace matrix also passed.
- Permanent-erasure browser acceptance on a disposable MySQL 8.4.11 and MinIO
  object-lock deployment: PASS. A random synthetic account completed UI disable,
  expired-retention save, typed confirmation, erasure, tombstone rendering and API
  read-back at lifecycle version 2. Both 1440x1000 screenshots were inspected.
- Independent reconciliation found exactly one verified COMPLIANCE-locked external
  obligation for that synthetic account. The principal and tenant-member rows were
  anonymized, the credential count was zero, the version-2 tombstone and one
  `principal_anonymized` event remained, and all four MySQL tombstone triggers were
  installed. Direct attempts to revive the principal or clear `anonymized_at` were
  rejected and the stored tombstone remained intact.
- External notification delivery, external IdP/SCIM integration, approved RTO/RPO,
  designated sign-off and formal production acceptance remain NOT READY.

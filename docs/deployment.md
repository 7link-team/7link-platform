# Deployment and releases

The server ships as two GHCR images, `<owner>/<repository>-api` and
`<owner>/<repository>-web`. Owner and repository are derived from GitHub and
lowercased for GHCR. The runner ships as native binaries, not a container: it
needs access to the user's local tools, credentials and workspace. Its existing
npm launcher remains available for local packaging; this workflow does not
publish the private npm package. The Tauri desktop server launcher also ships as
a Windows x64 NSIS installer and portable ZIP, macOS Intel/Apple Silicon DMGs,
and Linux amd64/arm64 AppImages.

## Cut a release

1. Merge a candidate with green CI and complete deployment acceptance.
2. Create and push a tag such as `v0.1.0` or `v0.1.0-rc.1` using your normal
   release approval process. The release workflow also accepts an **existing**
   tag through `workflow_dispatch`; it does not create tags.
3. Wait for both multi-platform images, all six runner builds and all five
   desktop platform builds to succeed.
   The final job creates a GitHub Release with generated notes and assets.

The exact git tag is the image tag, including its leading `v` and prerelease
suffix. For example, set `IMAGE_PREFIX=ghcr.io/<owner>/<repository>` and
`RELEASE_TAG=v0.1.0`, then pull `${IMAGE_PREFIX}-api:${RELEASE_TAG}` and
`${IMAGE_PREFIX}-web:${RELEASE_TAG}`. Only stable tags update `latest`.
Tags must be Docker-compatible `vMAJOR.MINOR.PATCH[-prerelease]`, at most 128
characters; SemVer `+build` metadata is rejected because Docker forbids `+`.
Use a version tag or digest in deployments. Concurrent releases are serialized
per tag; release different stable tags in order if you rely on `latest`.

Manual dispatch also provides three mutually exclusive modes:

- `desktop_only`: build and verify desktop bundles without publishing a Release.
- `images_only`: verify native images under unique `verify-<run>-<attempt>` tags.
  Application version labels still identify the requested source tag. This mode
  does not update version tags or `latest`, or build Runner/desktop assets.
- `append_desktop`: add missing macOS/Linux desktop bundles to an existing
  Release. The tag, images, Windows/Runner binaries and existing checksum lists
  remain unchanged. New Unix bundles receive a separate desktop Unix checksum
  list. Retry a failed publication job with its original build artifacts after
  an interrupted upload; conflicting unlisted existing binaries are rejected.

Image and desktop verification have separate concurrency groups. Normal Release
publication and desktop append share the same per-tag publication group.

Images support `linux/amd64` and `linux/arm64`. Both embed the tag in
`org.opencontainers.image.version`, with source, revision and creation labels.
The API's existing build-version ldflag also supplies its reported application
version unless an operator explicitly sets `version` in configuration.
BuildKit publishes SBOM and maximum provenance attestations; cosign signs the
image index digest with GitHub OIDC (no stored signing key). Verify against the
repository's release workflow identity and the issuer
`https://token.actions.githubusercontent.com` using `cosign verify`.

Each component/architecture builds on its matching native Linux runner, with
separate cache scopes. ARM64 uses `ubuntu-24.04-arm`; no QEMU compiler emulation
is used. Each image must execute its native smoke check before exporting its
digest. The merge jobs require both architectures and their attestation
references before publishing and signing the final indexes.

Native release assets are named:

```text
<repository>-runner_<tag>_windows_amd64.zip
<repository>-runner_<tag>_windows_arm64.zip
<repository>-runner_<tag>_linux_amd64.tar.gz
<repository>-runner_<tag>_linux_arm64.tar.gz
<repository>-runner_<tag>_macos_amd64.tar.gz
<repository>-runner_<tag>_macos_arm64.tar.gz
<repository>-runner_<tag>_checksums.txt
<repository>-desktop_<tag>_windows_amd64-setup.exe
<repository>-desktop_<tag>_windows_amd64_portable.zip
<repository>-desktop_<tag>_macos_amd64.dmg
<repository>-desktop_<tag>_macos_arm64.dmg
<repository>-desktop_<tag>_linux_amd64.AppImage
<repository>-desktop_<tag>_linux_arm64.AppImage
<repository>-desktop_<tag>_checksums.txt
```

Extract `machine-runner` (or `machine-runner.exe`) and run `--version`; it reports
the exact tag injected through `-X main.version=<tag>`. On Unix, verify downloaded
archives with `sha256sum --ignore-missing -c <repository>-runner_<tag>_checksums.txt`.
Use `Get-FileHash -Algorithm SHA256` to compare the selected archive on Windows.
The Runner checksum list covers all six Runner archives; the separate desktop
checksum list covers its installer and portable ZIP.
For a complete new release, the desktop list also covers both DMGs and both
AppImages. When appending Unix bundles to an older Windows-only release, the
original desktop list is preserved and the four new bundles are listed in
`<repository>-desktop_<tag>_unix_checksums.txt`.
Checksums protect download integrity; runner archives are not separately signed,
and Windows desktop code signing is not configured.

macOS DMGs are built on native Intel and Apple Silicon runners. The mounted app's
version, architecture, ad-hoc signature and startup are checked. These builds
have no Apple Developer ID signature or notarization, so Gatekeeper may require
explicit approval. Linux AppImages use an Ubuntu 22.04 build baseline for both
architectures; verification extracts the bundle and starts it in a graphical
session. Downloaded AppImages require executable permission (`chmod +x`). These
startup checks do not replace UI or login acceptance on the target desktop.

The NSIS installer offers **Only for me / All users**. Tauri's `installMode=both`
requires Administrator access for either selection; the choice controls the
installation scope. The portable ZIP contains the actual `frontend-app.exe`
from that same build and can be extracted and run without installing the app.
It requires Microsoft Edge WebView2 Runtime to be present; it does not install
WebView2 or promise that application/WebView state remains beside the executable.
This packages a server chooser that loads the deployed Web, not a completed native integration
or an accepted native login flow. See `apps/frontend-app/README.md` for its scope.

Desktop releases do not embed a server or API address and need no `VITE_API_URL`
repository variable. At runtime users save and select multiple HTTP(S) server
origins (scheme, host and optional port, without a path, credentials or query).
The WebView navigates to that server's Web page; its relative `/api` requests
use the same nginx origin. Deploy Web with the existing same-origin API proxy,
without a baked absolute `VITE_API_URL`. Server aliases still need corresponding
server-side cookie and login callback configuration.

Every launch opens the bundled chooser. The native **Connection -> Servers**
menu (`Ctrl+Shift+S`) returns there even if the remote page is unavailable.
Saved origins live in local WebView storage; remote Web storage is isolated by
origin. Cookies follow browser Domain/Path/Secure rules: different ports on the
same host do not isolate cookies. Removing or switching an address does not log out another
server. This is selection, not automatic failover. The launcher needs a running
Web deployment; no shared Web assets are embedded for offline use. The app
version is injected from the tag through a temporary Tauri config.

## Repository settings

- Enable Actions and grant this repository's `GITHUB_TOKEN` permission to publish
  packages and create Releases. No PAT is required. For an existing GHCR package,
  grant this repository Actions access in that package's settings.
- Image build and merge jobs request `packages: write`; only index signing jobs
  request `id-token: write`. Release creation and desktop append jobs request
  `contents: write`; CI has `contents: read`.
- Allow the SHA-pinned actions used by the workflows and OIDC access to Sigstore.
  This distribution requires public GHCR packages. After the first image push,
  open each package's **Package settings → Change visibility → Public** and
  confirm the change. New packages default to private; a public source repository
  or OCI source link does not automatically make a package public. The release
  workflow does not change package visibility. Before announcing the release,
  verify both image tags can be pulled anonymously using an empty Docker config
  directory (without `docker login`), for example
  `docker --config <empty-directory> pull ${IMAGE_PREFIX}-api:${RELEASE_TAG}`
  and the equivalent `-web` image. A successful authenticated CI push or pull
  does not prove anonymous access.
- No repository secrets are required by the workflow. `GITHUB_TOKEN` is supplied
  by GitHub. Application/database keys belong in the deployment secret store,
  never in source, image build arguments, or workflow files.
- The workflow can publish partial image results if another build fails. The
  Release is created only after **all** builds and image signatures succeed.
  An existing Release is not overwritten on a manual rerun; inspect failed runs
  before retrying. No release is published by local verification.

## Build from a clean checkout

```sh
docker build --build-arg VERSION=v0.1.0 -t local-api:v0.1.0 apps/backend-api
docker build --build-arg VERSION=v0.1.0 -t local-web:v0.1.0 apps/frontend-web
```

Base images are pinned to multi-platform index digests. Go modules and Bun
dependencies come from lockfiles (`bun install --frozen-lockfile`). No local
binaries, node_modules, dev config or generated untracked build output is needed.
The Go toolchain directive is honored; builds need network access to fetch its
pinned toolchain, Go modules and Alpine build packages. Upstream package indexes
and archive timestamps mean these are repeatable clean-source builds, not a
claim of bit-identical images. Update base digests deliberately for security fixes.

The API uses `gorm.io/driver/sqlite` / `mattn/go-sqlite3`, so it is built with
`CGO_ENABLED=1`, then runs on matching Alpine/musl. It includes CA certificates
and timezone data, embedded migrations/locales/API docs, and runs as UID/GID
10001. `/data` is writable; mounted data/config/secret paths must be readable by
that UID. The web image uses unprivileged nginx (UID/GID 101), listens on 8080,
and needs only a writable `/tmp`; it supports a read-only root filesystem.

## Runtime configuration

The API reuses its existing precedence: flags > environment > config file >
defaults. Use `api serve --config /etc/service/config.json` to mount a config,
or uppercase the dotted config key with `_` separators for environment variables.
Examples: `HTTP_SERVER_PORT`, `DATABASE_MAIN_TYPE`, `DATABASE_MAIN_DSN`,
`DATABASE_MAIN_WRITABLE`, `AUTHN_ISSUER`, `AUTHN_SIGNING_KEY_FILE`.
For map-valued settings such as `database.main`, declare the map entry and its
fields in the config file first; the current configurator does not discover a
new map entry from underscore-only environment variables. Environment variables
can then override those declared fields. Alternatively pass explicit dotted
database flags. A config skeleton contains no credentials:

```json
{
  "database": {
    "main": {"type": "mysql", "dsn": "", "writable": true, "readable": true}
  }
}
```

Database alternatives (supply the DSN through a secret store):

| Type | `DATABASE_MAIN_DSN` format |
| --- | --- |
| MySQL | `<user>:<password>@tcp(mysql:3306)/<database>?parseTime=true&charset=utf8mb4&multiStatements=true` |
| PostgreSQL | `host=postgres port=5432 user=<user> password=<password> dbname=<database> sslmode=verify-full` |
| SQLite | `/data/application.sqlite?_foreign_keys=on` |

Set `DATABASE_MAIN_WRITABLE=true`. Use persistent storage for SQLite and uploaded
files. SQLite is suitable for a single deployment with its existing serialized
writer; choose MySQL/PostgreSQL for shared-database replicas. Configure database
TLS for production networks; the Compose example below uses a private local
network for illustration.

`CMD ["serve"]` does **not** run migrations. Back up first and either run
`api migrate up --config /etc/service/config.json` as a controlled one-off job or
explicitly opt into `api serve --migrate --config /etc/service/config.json`.
Migrations are embedded in the binary; no host SQL directory is needed.
Respect irreversible migration warnings. See the backend README for initial
platform-administrator provisioning and key rotation. `/healthz` is the image
health check; `/readyz` additionally reports application readiness, which requires
the configured database, IAM bootstrap and enabled capabilities to be ready.

For a usable authenticated deployment, enable `authn`, configure its public
issuer and browser URLs/origins, mount signing/MFA keys, provision the initial
administrator, and configure keys for every enabled encrypted capability
(federation, webhooks, business secrets, connector/gateway integrations).
Read `api serve --help` and the backend config documentation for those existing
options. Do not copy development keys or enable demo accounts in production.
Leave `version` unset to retain the image's release version.

The web browser always calls same-origin `/api`. At **container startup**, set
`API_UPSTREAM=http://api:8080` (an absolute HTTP(S) origin with no trailing slash).
nginx strips `/api/` before proxying to the API. Changing this environment value
and recreating the container selects another API without rebuilding JavaScript.
It must be reachable from nginx, not from the user's browser. SPA deep links
such as `/login` fall back to `index.html`; static assets have immutable caching,
while HTML is revalidated. API streaming disables buffering. The web health check
tests the static server; it does not assert API readiness.

Terminate HTTPS at your ingress and configure the application's canonical public
URLs consistently. The supplied nginx forwards its observed scheme; for TLS
termination upstream, align trusted proxy/forwarded-header policy at the ingress
and API, and verify secure cookies/redirects before exposing the deployment.
Restrict ingress to required routes; do not publish diagnostic or gRPC ports by
default. Deployment names and organization labels remain runtime API config.

## Compose example (documentation only)

Create your own protected `.env` or supply variables from a secret manager.
Required values are referenced below without committing credentials. Prepare
`./config.json` and `./secrets/` with the authenticated deployment settings above;
ensure UID 10001 can read them. This example explicitly opts into migrations and
assumes the database user has the required migration privileges.

```yaml
services:
  mysql:
    image: mysql:8.4
    environment:
      MYSQL_DATABASE: application
      MYSQL_USER: application
      MYSQL_PASSWORD: ${DATABASE_PASSWORD:?provide through secret management}
      MYSQL_ROOT_PASSWORD: ${DATABASE_ROOT_PASSWORD:?provide through secret management}
    volumes:
      - database:/var/lib/mysql
    healthcheck:
      test: [CMD, mysqladmin, ping, --silent]
      interval: 10s
      timeout: 5s
      retries: 30
  api:
    image: ${IMAGE_PREFIX:?ghcr.io/owner/repository}-api:${RELEASE_TAG:?exact git tag}
    command: [serve, --config, /etc/service/config.json, --migrate]
    environment:
      DATABASE_MAIN_TYPE: mysql
      DATABASE_MAIN_WRITABLE: 'true'
      DATABASE_MAIN_DSN: application:${DATABASE_PASSWORD:?required}@tcp(mysql:3306)/application?parseTime=true&charset=utf8mb4&multiStatements=true
    volumes:
      - ./config.json:/etc/service/config.json:ro
      - ./secrets:/run/secrets:ro
      - files:/data
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    depends_on:
      mysql:
        condition: service_healthy
  web:
    image: ${IMAGE_PREFIX:?ghcr.io/owner/repository}-web:${RELEASE_TAG:?exact git tag}
    environment:
      API_UPSTREAM: http://api:8080
    ports: ['127.0.0.1:8099:8080']
    read_only: true
    tmpfs: [/tmp]
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    depends_on:
      api:
        condition: service_healthy
volumes:
  database:
  files:
```

Use a DSN-safe database password (or the configuration driver's supported
escaping), and replace this illustrative volume/ingress setup with your backup,
TLS and retention policy. A named volume initially inherits the image's `/data`
ownership; bind mounts require explicit host permissions.

## CI scope

CI runs the gates from `make ci` (format, pinned lint, schema lock, coverage floors,
embedded-engine integration, build, OpenAPI/client drift) in independent backend
checks, coverage and integration jobs. The nested integration suite includes
architecture tests. CI also runs real MySQL 8 migration tests and lifecycle/archive tests against both
MySQL and PostgreSQL service containers. Lifecycle/archive fixtures use a separate
`lifecycle` database so migration fixture data in `integration` cannot leak into
application bootstrap. Existing integration helpers still use
their embedded PostgreSQL instance for isolated per-test databases. Services use
password-free ephemeral CI authentication; do not copy that configuration into
a deployment. Frontend typecheck/lint/test/build and runner build/test/vet plus
npm-launcher tests run separately, with Go/Bun caches and superseded-run cancellation.
Coverage retains the full cross-package atomic instrumentation, test set and
thresholds. On Linux, Go's merged profile flows through a dedicated pipe into
the coverage tool, which sums repeated blocks as they arrive and writes one
record per block. This avoids storing the large duplicate aggregate profile;
Go still creates and cleans its own per-test fragments. Other operating systems
collect into a temporary regular profile and compact it before writing
`coverage.raw.out`. Module floors still include generated blocks, while the
total uses the existing generated-code exclusions. Test/build/collection
failures stop the gate, and normal test output remains separate from profile data.
The release workflow additionally builds the Windows NSIS installer/portable
ZIP, macOS DMGs and Linux AppImages on native runners using locked dependencies
and tag-derived app versioning. Windows verifies both installation scopes and
uninstall cleanup; Unix jobs verify bundle architecture/version or extraction
and native startup. CI configuration checks do not substitute for these hosted
builds and runtime checks.
`GOFLAGS=-p=1` bounds Go package parallelism. Performance, whole-tree race,
external Redis/TLS, deployment acceptance and browser UAT from `release-check`
remain explicit release-owner gates; they are not silently claimed by CI.

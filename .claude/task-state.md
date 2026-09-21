# Task State
## REST URI, Swagger Grouping, And App/IAM Boundary - 2026-09-21

- Current phase: CHECKPOINT DELIVERY.
- Objective: normalize the complete public API around recognizable resources and
  standard protocol endpoints; make every Huma registration own its correct
  Swagger tag; remove generated-document tag inference; audit URI-derived IAM,
  menu, and permission logic; reduce IAM ownership inside `internal/app`.
- Confirmed contract: all first-class internal IDs are globally unique snowflake
  IDs. Create/list collections are nested under their direct owner; existing
  resources use short global-ID paths (`/entities/{entity_id}` and
  `/departments/{department_id}`). Current-principal projections exist at the
  account, tenant, entity and department contexts. Scope never comes from a
  header/query inference. WebAuthn is an authentication mechanism, not a
  distinct session type. OAuth/OIDC/SAML/SCIM and probes retain protocol paths.
- Completed: removed OpenAPI post-processing tag inference; changed Passkey
  credential management tags to `me`; replaced the rejected `passkey-sessions`
  design with `/login/passkey/options` and `/login/passkey/verify`; moved current
  session, session inventory, step-up, and reauthentication paths below `/me`.
- Active work: commit and push the verified app-boundary and Files REST checkpoint;
  contextual menu resource work remains a later implementation phase.
- Known risk: `internal/app` currently contains IAM-specific operations, route
  manifests, direct internal-service/table tests, and several thousand lines of
  IAM integration fixtures. Module composition belongs there; IAM domain and
  route ownership do not.
- Verification: authentication, app, affected IAM packages, OpenAPI generation,
  frontend API tests and typecheck passed before D66 resource-path migration.
- Remaining work: route inventory and migration matrix; owner-resolving authz
  middleware; all backend/frontend URI changes; contextual menu persistence and
  projection; generated contracts; deep audit; runtime verification; commit and
  push branch IAM.
- Completed D66 implementation: domain-neutral `RegisterOwned` resolves trusted
  tenant/entity owners, scopes personal tokens, returns 404 for absent/non-member
  resources, and injects immutable request claims. Core entity and role items and
  direct subresources now use short global-ID paths and resource-root tags; their
  focused OpenAPI, behavior, pagination, and cross-tenant tests pass.
- Latest checkpoint: commit `bc27c2f` is pushed to `origin/IAM`. Uncommitted work
  contains generated OpenAPI, Todo REST cleanup, and the entity-owner resolver
  correction. Focused Todo and core tests and OpenAPI checks pass; the large app
  Todo/IAM integration test currently exposes a 403 after entity ownership was
  corrected and must not be patched in place.
- Storage finding: the Files default `data/files` is cwd-relative. The shared app
  integration fixture did not inject a test root, so Go package tests wrote
  ignored avatar objects under `internal/app/data/files`. Tests must use isolated
  temporary roots; existing uploaded files must not be deleted without an
  explicit migration/cleanup decision.
- Completed app-boundary cleanup: the shared IAM fixture now uses `t.TempDir()`
  for Files; its bootstrap regression passes and `internal/app/data/files` is
  empty. The duplicated 241-line `bootstrap_todo_test.go` was removed; Todo
  behavior remains covered by 2,600+ lines of module-local authorization, field
  policy, HTTP, service, and transfer tests, while app retains module bootstrap
  smoke coverage. Focused Todo and app tests pass.
- Full app regression finding: the prior REST migration was not atomic. Multiple
  app fixtures still pass tenant IDs for globally addressed role, SCIM, service
  account, and governance item paths, causing the helper to prepend obsolete
  `/tenants/{tenant_id}` segments. IAM route/guard manifests also still classify
  owner-resolved item routes as tenant-selector routes. Do not restore old paths
  or add a resource-name whitelist to the helper.
- Authorization test direction: owner-resolved operations are a distinct
  `owned-resource` class; tests must verify the repository resolver marker rather
  than infer tenant/entity authorization from URI segments. Initial classifier
  support and SCIM credential fixture corrections are implemented; remaining
  manifest/call-site migration is incomplete.
- Files design constraint: replace `RegisterTenantEntityOrAuthenticated` for
  Files with explicit collections `/me/files`, `/tenants/{tenant_id}/files`,
  `/entities/{entity_id}/files`, and `/departments/{department_id}/files`.
  Existing items stay globally addressed under `/files/{file_id}` and resolve
  trusted authorization owner from persisted root bindings. `listRoots` must
  accept the verified collection scope/owner; it currently collapses entity
  claims to tenant roots.
- Completed app production boundary: platform-admin bootstrap/reset, tenant-owner
  repair, secret rewrap, and erasure-ledger listing now live in
  `internal/maintenance`. `internal/app` exposes only the shared synchronous
  maintenance lifecycle runner; it owns configuration, composition, migration
  validation, module start/stop, and runtime resource resolution. The rejected
  `MaintenanceHost` abstraction is absent. Principal anonymization is owned by
  `internal/iam/lifecycle`; app only wires its capability. CLI command and test
  references use maintenance-owned request, report, and error types.
- Completed app test boundary: IAM profile, SCIM actor, tenant isolation,
  principal closure/erasure, audit archive, load, performance, and external
  database suites moved from `internal/app` to `internal/integration/iam`, along
  with their shared fixture and performance baseline. App retains only
  lifecycle, composition/OpenAPI, and migration-status tests. The fixture uses
  direct lifecycle methods (`New`, `Bootstrap`, `RegisterHTTP`, `Shutdown`) and
  read-only runtime/module accessors; no host abstraction or test-only business
  proxy was introduced.
- Completed route fixture cleanup: authorization manifests recognize explicit
  `owned-resource` registrar metadata, and app integration callers use canonical
  short item paths for roles, groups, positions, entities, departments,
  governance items, SCIM directories/targets, and service accounts. No helper
  whitelist or URI-derived authorization inference was added.
- Verification: `go test ./internal/todo -count=1` PASS;
  `go test ./internal/maintenance -count=1` PASS; full
  `go test ./internal/app -count=1` PASS; full
  `go test ./internal/integration/iam -count=1` PASS; full
  `go test ./cmd/7link-api/cmd -count=1` PASS; combined app/integration/
  maintenance/CLI regression PASS; `git diff --check` PASS.
- Completed Files REST checkpoint: explicit `/me/files`, tenant, entity, and
  department owner collections; globally addressed `/files/{file_id}` items;
  persisted owner resolution for active and trashed nodes; exact root filtering;
  batched recycle-bin ownership lookup; updated integration contracts; regenerated
  OpenAPI. No legacy nested item aliases were restored.
- Verification: `go test ./internal/files ./internal/iam/core
  ./internal/iam/organization ./pkg/extension/authz ./internal/app
  ./internal/integration/iam ./internal/maintenance ./cmd/7link-api/cmd
  ./internal/todo` PASS; `make openapi-check` PASS; `git diff --check` PASS.
- Next action: commit and push this checkpoint to `origin/IAM`; after that,
  continue the remaining contextual menu resource work and complete the broader
  REST deep audit in a separate checkpoint.

## Review, Security Audit, And Regression - 2026-09-20

- Current phase: ACCEPTANCE COMPLETE for the review/audit; release readiness is
  NOT READY because documented dependency and coverage gates remain open.
- Objective: review and audit the current repository, fix confirmed defects that
  do not require a product decision, and run proportionate regression gates.
- Completed implementation: cross-tenant file destinations now return the same
  not-found result as absent IDs; Todo imports accept the documented multipart
  size and contain Excel parser panics; reachable x/image advisories are removed;
  Excelize is pinned to the upstream negative-index fix; authz unused-symbol and
  orphan architecture-test failures are fixed. File conflict retries retain one
  idempotency key across ambiguous response loss, restart stale confirmation as
  fresh preflight, preserve still-valid choices, disable policy buttons while
  busy, and use action-neutral rename wording. File mutations no longer trigger
  global menu-catalog invalidation. MySQL Files migrations now use table-level
  cross-column checks and compatible FK collations; Organization migration 10
  applies and rolls back its composite department/entity constraints on MySQL
  8.4 without losing the pre-existing department foreign keys.
- Verification: full frontend tests, lint, typecheck, and production build PASS;
  backend format, pinned lint, build, OpenAPI check, all main-module tests, full
  integration matrix, and affected-package race tests PASS. Coverage tests PASS
  but the gate FAILS at 88.3% versus 90.0%; Files is 76.3%. Root Bun audit and
  tests/tools `govulncheck` report zero vulnerabilities. Main `govulncheck`
  reports only GO-2026-6452 because its advisory has no fixed version; the pinned
  Excelize source contains both negative-index guards and the malicious-workbook
  no-panic regression passes. One authn concurrency test timed out during the
  first coverage run, then passed 5 focused repetitions and the complete rerun.
  Remote Docker at 10.0.0.100 was reached over the configured SSH transport:
  both Redis security matrices PASS across standalone mTLS/ACL, Cluster, and
  Sentinel; the full MySQL 8.4 gate PASSes fresh migration, rollback/replay,
  lifecycle/concurrency, performance, federation, provisioning, audit, and
  schema-lock checks. The gate's disposable container cleanup was verified.
  Stoplight, openapi-ui and Stoplight's Mermaid runtime are disabled in source
  and absent from the rebuilt embedded site; their pages/assets return 404 and
  the Huma extension tests PASS. The docs audit is reduced from 29 findings to 8.
- Known issues: the retained ReDoc dependency graph has 2 high js-yaml CPU-DoS
  findings, and Scalar has 6 low AI SDK resource-consumption findings; the user
  explicitly chose to retain these renderers. Federation callback URL trust is
  deployment-sensitive and unresolved. Full coverage is 88.3% against a 90%
  gate. Two older remote MySQL gate containers remain on 10.0.0.100; this review
  did not delete pre-existing remote resources.
- Remaining work: approve a trusted federation callback-origin contract and
  separately raise total and Files coverage to the existing 90% gate.
- Next action: add behavior-focused Files tests until the coverage gate passes.
  Define and approve a trusted public federation callback-origin contract before
  changing callback reconstruction.

## File Copy Conflict Negotiation - 2026-09-20

- Current phase: ACCEPTANCE COMPLETE.
- Objective: same-directory and same-name copy/move conflicts return a user choice
  instead of 403 or silently applying a fixed rename policy.
- Contract: initial operations send `ask` for file, directory, and type policies;
  Web renders only each conflict's server-provided policies (skip, rename,
  overwrite/replace, or merge) as direct, wrapping buttons, then retries with
  `observed_revision` and accumulated per-key decisions. Nested directory
  conflicts may require repeated negotiation.
- Authorization: fix only the invalid tenant-only claims predicate. Do not broaden
  file permissions, add business IDs to headers, or add query-token auth.
- Execution: Direct because backend protocol types, API errors, page state, and
  dialog behavior form one tightly coupled workflow.
- Verification: backend Files package and `-race` PASS; frontend focused 52 tests,
  full 203 tests, typecheck, lint, and production build PASS. Real Chrome tenant
  same-directory copy PASS: the dialog exposed only the server-provided rename
  policy, retry created `name (1)`, and `/file-operations` returned no 403.
  Temporary active test items were moved to recycle bin.
- Runtime: current API source is serving at `http://127.0.0.1:61827`; Vite remains
  at `http://127.0.0.1:61828`.
- Next action: self-review the final diff, commit, push `IAM`, and verify local and
  remote heads.

## Canonical Web And API Context Routes - 2026-09-20

- Current phase: TESTING. The user approved a breaking cleanup with no ambiguous
  legacy Web aliases and no public `/iam` or `/authn` API prefixes.
- Canonical Web contract: `/me/*` for the signed-in principal;
  `/tenants/:tenantId/*` for tenant scope;
  `/tenants/:tenantId/entities/:entityId/*` for entity scope; optional department
  resource views nest below that entity; `/platform/*` is platform administration.
- Context rule: Web URL params are authoritative. Local storage may remember the
  last workspace but cannot override a URL. API tenant/entity context will be
  derived from canonical path params with parent-child validation; callers must
  not choose authorization scope solely through `X-Tenant-Id`/`X-Entity-Id`.
- Known failure: department persona-created `666` directories landed in account
  roots because flat `/files` lacked complete entity context. First Business and
  UAT tenant spaces are distinct in SQLite; the UI made them appear identical by
  falling back to the same account view.
- Existing local changes rename account navigation to localized `My files`; keep
  them. Previous commit `79760da` contains file previews and demo personas.
- Execution: Direct. Router, authz registrar, generated OpenAPI/client, menus and
  persona E2E share one route contract and must change atomically.
- Completed implementation: Web router/sidebar/API calls use canonical scope
  paths; tenant/entity headers were removed from runtime API calls; OAuth tenant
  input is a path parameter; session display cannot be overridden by a header;
  file new-tab opening navigates directly to a cookie-authorized streaming URL
  with explicit inline disposition instead of buffering an unbounded Blob.
- Contextual navigation permissions are now explicit too: platform discovery uses
  `/me/permissions`, tenant navigation uses `/tenants/:tenant_id/me/permissions`,
  and entity navigation uses
  `/tenants/:tenant_id/entities/:entity_id/me/permissions`. Tenant results include
  data-scoped actions only when their constraint is tenant-wide; entity results
  include department/entity grants only when they can reach that entity.
- File scope correction: the client distinguishes personal (`null`), tenant
  (`undefined`) and entity (ID) scope, and the backend publishes all three URI
  forms. Tenant-level constraints allow only tenant-wide grants; entity and
  department grants cannot escalate to the tenant root.
- Verification: frontend API tests 39/39 and typecheck PASS. Live five-persona
  browser E2E PASS on 61828/61827: platform/system/tenant admins see tenant,
  entity and both department roots; entity account sees entity and both departments;
  department account sees Finance only. Tenant file navigation is hidden from the
  entity/department personas while their entity file navigation remains available.
  Canonical-route package tests for core, todo, identity, organization,
  federation, governance, provisioning and audit PASS. Platform archive inputs
  are now separate from tenant DTOs, fixing the runtime 422 and removing the
  OpenAPI mutation workaround. App composition and todo-IAM integration PASS.
  `go test ./...` PASS before deprecated selector constants were removed; the
  affected cleanup packages also PASS afterward. Full acceptance is NOT_RUN.
- Remaining work: migrate executable browser scripts; update current docs while
  retaining explicitly historical migration examples; regenerate OpenAPI/client;
  run complete tests/build/lint and five-persona isolation E2E; start services;
  self-review; commit and push.
- Next action: migrate browser scripts in three groups: personal scripts remove
  obsolete tenant selection, tenant management scripts prefix `/tenants/:id`,
  and entity scripts prefix `/tenants/:id/entities/:id`; syntax-check every MJS,
  then update docs and regenerate OpenAPI/client.

## Office Preview Readability - 2026-09-20

- Current phase: ACCEPTANCE COMPLETE. DOCX previews use page-width fitting and
  retain distinct page frames; Office zoom supports 50%-600% in 25% steps.
  Modern XLSX files are classified for the installed local Office renderer.
- PPTX previews provide a localized page list on the left at desktop sizes and
  a horizontal list on mobile. Selecting a page displays that slide reliably
  despite the renderer's asynchronous slide-node replacement; print restores
  every slide.
- Existing Office URL/DOM/CSS sanitization remains in place. Legacy XLS is not
  claimed as a high-fidelity preview format, and streaming/new-tab token design
  remains a separate scope.
- Verification: focused Office/file tests 17/17 PASS; full frontend tests PASS
  (UI 85, IAM 113); full typecheck PASS; full lint PASS; production build PASS;
  `git diff --check` PASS. Real browser acceptance on 61828/61827 PASS with a
  generated 3-page DOCX (1324 px page width), XLSX workbook, and 3-slide PPTX;
  zoom reached 600%, PPT page 2 selection worked, desktop had no horizontal
  overflow, and mobile navigation rendered horizontally. Screenshots are under
  `.local/screenshots/office-preview/` and are not tracked.
- Runtime: API remains at `http://127.0.0.1:61827`; Web remains at
  `http://127.0.0.1:61828`.
- Next action: user accepts Office previews at the running Web URL; no source
  implementation remains for this slice.

## Local File Persona Demo Accounts - 2026-09-20

- Current phase: ACCEPTANCE COMPLETE. Local development can explicitly enable
  five disposable login personas: platform super administrator, restricted
  system administrator, tenant administrator, entity account, and Finance
  department account. The login selector and optional first-account prefill are
  gated by both Vite development mode and explicit environment flags; production
  builds expose no credentials.
- Command contract: `node scripts/iam-uat.mjs start --demo-accounts` initializes
  personas and exposes the selector. Add `--prefill-demo-account` to prefill the
  first persona; that flag is rejected unless demo accounts are enabled. With no
  flags, the existing login behavior is unchanged.
- Demo data bootstrap is idempotent and reuses existing IAM APIs, roles,
  permissions, entity bindings, departments, and generated local password files.
  The entity persona uses a tenant-level discovery-only role plus an entity-bound
  file role so Web navigation works without exposing the tenant file root.
- Verification: dev parser tests 3/3 PASS; full frontend typecheck PASS; full
  frontend lint PASS; production build PASS; credential scan of `dist` PASS;
  script syntax checks and `git diff --check` PASS.
- Real browser acceptance on Web `http://127.0.0.1:61828` and API
  `http://127.0.0.1:61827`: all five personas selected from the login dropdown,
  autofill and login PASS. Super/system/tenant administrators see tenant, entity,
  Finance, and Operations roots; entity account sees entity plus both departments;
  department account sees Finance only. Unexpected HTTP/console errors: none.
- Navigation follow-up: tenant view now has a permission-gated `/files` entry and
  loads tenant-scope roots without requiring an entity selection; entity view
  retains its `/files?entity_id=...` entry. Browser acceptance verifies both menu
  links and both pages for all five personas. Sidebar tests 13/13, typecheck,
  lint, production build, and diff check PASS.
- File layout follow-up: file pages now fill the shell's available height instead
  of nesting `100svh` calculations inside padded content. Empty folders no longer
  create page scroll, and the recycle-bin action remains fully visible. Browser
  measurements PASS at 1440x900 and 390x844 with body/main scroll dimensions
  equal to their client dimensions; the 44px recycle action stays inside both
  viewports. Navigation labels are now `文件管理`, `File management`, and
  `Pengurusan fail`. Focused UI tests 25/25, typecheck, lint, build, five-persona
  E2E, and diff check PASS.
- Open-in-new-tab finding: current implementation downloads the whole file into
  an unbounded Blob before opening a `blob:` URL; only dialog preview has the
  32 MiB cap. Browser sessions are opaque HttpOnly server-side sessions, OAuth
  access tokens are short-lived JWTs, and personal `pat_` tokens are opaque
  scoped bearer credentials. General query-token authentication is not supported
  and should not be added. Recommended next slice is a streaming inline-content
  gateway with Range support and a one-resource short-lived grant exchanged from
  URL fragment to HttpOnly cookie; persistent/revocable shares remain separate.
- Runtime note: acceptance uses a current-source SQLite environment because fresh
  MySQL initialization is blocked by pre-existing Files migration 2 and
  Organization migration 10 compatibility failures. The prior MySQL UAT data was
  preserved under `iam-uat-saved-2026-09-20T03-46-16-195Z`.
- Next action: user accepts the file-management views at
  `http://127.0.0.1:61828/login`; no implementation work remains for this slice.

## Global REST Resource Routing And Swagger Grouping - 2026-09-19

- Current phase: IMPLEMENTATION. The user approved a breaking pre-production
  migration with no legacy aliases and requested a complete route audit.
- Contract (D66): public paths expose resource nouns, never internal module
  prefixes. Create/list collections nest under their direct owner; globally
  identified existing resources use short item paths and nest direct
  subresources there. The owning module resolves trusted tenant/entity scope;
  scope headers and query parameters are not authorization selectors.
- Swagger: every application operation has one lowercase kebab-case tag equal to
  its first public path segment. Package tags (`iam`, `authn`, `identity`,
  `organization`, `governance`, `federation`, `provisioning`) are forbidden.
- Avatar contract: `/me/avatar` for current metadata/configuration,
  `/principals/{principal_id}/avatar` for current bytes, and
  `/me/avatar-candidates/{candidate_id}` for private expiring candidate bytes.
  Storage filenames are not public identifiers and no table is added.
- Runtime compatibility: avatar storage-name parsing accepts both historical
  `/api/iam/avatars/{principal_id}/{file}` and intermediate
  `/api/avatars/{principal_id}/{file}` references while continuing to reject
  public current-avatar URLs and malformed/wrong-principal references. This
  keeps startup migration and reads compatible without rewriting database data.
- Execution: Direct; routes, generated contract, Web calls, menu paths and
  acceptance are strongly coupled. No subagent was requested.
- D66 implementation checkpoint 2026-09-21: generic `RegisterOwned` and its
  failure semantics are tested; core entity/role/menu and organization
  department/position/group/invitation item routes are migrated. Identity
  service-account item/credential routes now resolve `owner_tenant_id` in the
  identity repository and consume trusted context. Identity compiles; its full
  suite currently stops on stale route-manifest assertions that still expect
  nested service-account items.
- D66 checkpoint 2026-09-21 (continued): identity service-account focused
  lifecycle/isolation tests pass; federation identity-provider and SAML-SP,
  OAuth client, provisioning directory/target, and governance request/review
  item routes now use module-owned owner resolvers and short paths. Added and
  tested `RegisterOwnedTenantMember` for owned self-service resources without
  inventing a permission. OAuth, provisioning, and governance focused route
  contracts pass. Full module regressions and generated artifacts remain to be
  run after the remaining resource migrations.
- D66 checkpoint 2026-09-21 (todo): core now publishes a narrow
  `EntityOwnerResolver` capability; todo consumes it without reading IAM tables.
  Todo collections/import/export are under `/entities/{entity_id}/todos...` and
  existing items are `/todos/{id}` with owner scope read from the todo row. Todo
  full tests PASS. Backend `go test ./... -run '^$'` PASS after clearing 9.5 GB
  of reproducible Go build cache that had filled the system temp volume.
- Remaining critical design/implementation: menu definition/context placement
  separation and account/tenant/entity/department effective projections; files
  contextual collection mounts; stale callers/tests/generated contracts; full
  regression, deep audit, live Swagger/UAT, review, commit, and push.
- Verification 2026-09-19: frontend full tests PASS (UI 85, IAM 98), full lint
  PASS, production build PASS, backend build PASS, generated OpenAPI check PASS.
  Backend `go test ./...` remains FAIL because route migration fixtures and
  behavior tests still expect the removed action-style routes.
- Runtime recovery verification: identity legacy-avatar tests PASS; focused
  authn HTTP tests PASS; source build PASS. API PID 25424 serves 18280;
  `/sessions/current` returns the expected anonymous 401 directly and through
  the 18291 proxy. Fresh browser admin login and `/account/files` load PASS with
  zero post-login failed responses or console errors.
- Port 18081 was still serving the pre-migration `.temp/files-preview/api.exe`
  built on 2026-09-18, whose OpenAPI exposed 109 `/iam` and `/platform/iam`
  paths. It was replaced with a current source build (`api-current.exe`, PID
  15124). Live 18081 OpenAPI now has 183 enabled paths, zero IAM-prefixed paths,
  and zero first-path-segment/tag mismatches; `/iam/tenants` is 404 while
  `/tenants`, `/sessions/current`, and `/files` reach the new routes and return
  the expected anonymous 401. Health, readiness, and OpenAPI probes return 200.
- Broader migration gates remain red for stale expectations, not the file
  navigation fix: composed mapping, SCIM profile/service-account, tenant
  isolation, principal closure, identity closure and OAuth tag tests still use
  superseded route contracts.
- Next action: migrate remaining globally addressed federation, OAuth,
  provisioning, governance, todo, and applicable audit resources to
  module-owned owner resolvers; implement the four approved contextual menu
  projections; then update route tests/callers, regenerate contracts, and run
  the full deep-audit gates and live Swagger/UAT acceptance.

## File Preview, Thumbnails, Blank Files, And Editors - 2026-09-19

- Current phase: ACCEPTANCE COMPLETE for the navigation/new-tab slice. Phase 1
  safe preview plus the user's image thumbnail and blank-file follow-ups are
  implemented and browser-accepted.
- Completed follow-up: icon surfaces open entries; directory navigation is URL
  history-backed; refreshed/direct child-directory loads fall back to the
  authorized parent path; files opened in a new tab render direct authorized
  Blob content without the admin shell or preview dialog; folders open their
  file-manager URL. Same-browser drag-to-directory uses the existing synchronous
  `POST /file-operations`; sanitized local OOXML DOCX/PPTX preview uses the
  installed Office plugin. No new API, table, route, background task, or public
  storage URL was added.
- Implemented: MIME/extension category icons; dynamic `open-file-viewer` for
  image/text/PDF/bounded media; 32 MiB authorized Blob limit; 8 MiB
  near-viewport image thumbnails with abort/revoke/icon fallback; generic ZIP
  refinement for OOXML/ODF/container formats; current-folder `New` submenu for
  zero-byte `.txt` and `.md` files through existing `POST /files`.
- Thumbnail and file-type visuals now share the same fixed square at every file
  browser density: 36 px compact/details and 64 px large. Images, loading/error
  fallbacks, directories, and known-type SVG icons cannot resize the row/card.
- Real acceptance: account login on current API 18280 and restarted Web 18291;
  actual upload and render of text/image/PDF; PDF canvas nonblank; Office safe
  fallback; viewer and worker chunks load only on demand; thumbnails remain in
  details/small/large views; `.txt` and `.md` creation succeeds; account raw
  requests omit tenant/entity headers; desktop and 390px mobile have no overflow,
  authenticated console errors, or failed responses. Test artifacts were moved
  to recycle bin. Screenshots are `.local/screenshots/file-preview/`.
- Verification: focused 23 browser presentation tests PASS; IAM typecheck PASS;
  IAM lint PASS. Earlier full frontend 181 tests and production build PASS before
  the thumbnail/new-file follow-up; rerun full gates after this slice.
- Size-consistency follow-up verification: file-browser presentation tests 24/24
  PASS; IAM typecheck PASS; focused ESLint PASS. A browser-mounted real
  `FileVisual` measured image/SVG pairs at 36x36 compact and 64x64 large.
- Navigation/new-tab verification: focused file-browser tests 25/25 PASS; full
  frontend tests PASS (UI 85, IAM 98); typecheck, full lint and production build
  PASS. Live 18291/18280 browser acceptance PASS for clicked-directory browser
  Back, direct-directory file-manager Back to its authorized parent, and image
  `Open in new tab` reaching a `blob:` content page with no admin preview dialog
  or server 5xx response.
- Viewer decisions: user wants the best default per file type and a Windows-like
  `Open with` choice. PPTist, Univer OSS and FortuneSheet are excluded because no
  open-source option reliably covers PPT/PPTX, DOC/DOCX and XLS/XLSX together.
  Microsoft Office Viewer is the default external Office handler and Google is
  the alternate, only through an explicit short-lived read-only public token URL.
- Next action after the active global route migration: implement the temporary
  public file gateway and external Office handler registry, then versioned raw
  saving and lazy Monaco.

## File Icons, Preview, Editing And Version History - 2026-09-19

- Current phase: DESIGN. User requested known-type file icons, broad file opening
  through `open-file-viewer`, and a path to Monaco text/code editing with history.
- Existing reuse: browser metadata already contains MIME/extension/size/version/
  revision; authenticated raw download exists; immutable Objects, Versions,
  `current_version_id`, object staging and `appendVersion` already support the
  correct save foundation. No version-history table is needed.
- Viewer review: `@open-file-viewer/core/react` 0.1.47 are MIT and React-compatible;
  core is about 6.8 MB unpacked and the audited install resolves 211 production
  dependencies with zero currently reported npm vulnerabilities. The package is
  pre-1.0 and exposes one bundled core entry.
- Security gates: Office is blocked by open issue #140 (unsafe DOCX hyperlinks and
  unsandboxed srcdoc); archive decompression has no expansion/entry/ratio budget;
  optional LibreDWG is GPL-3.0. These plugins are not approved for Phase 1.
- Proposed Phase 1: one MIME-first classifier and Lucide icon component; dynamically
  loaded local viewer adapter; Blob source from the authorized raw client; image,
  text/code/Markdown, PDF, bounded native audio/video; 32 MiB initial in-memory
  limit and explicit unsupported/oversized states.
- Proposed Phase 2: lazy Monaco full-file editor; `PUT /files/{file_id}/raw` with
  `If-Match`; immutable Object+Version append, node CAS, transaction audit; bounded
  version list/read/restore UI. Existing compact form CodeMirror remains unchanged.
- Design: `apps/frontend-web-admin/docs/file-preview-and-editing-design.md`.
- Execution: Direct. The preview UI and its API/context behavior are tightly
  coupled; no subagents requested. No product source or dependency has changed.
- Verification: repository/source review PASS; npm production dependency audit
  PASS (0 known vulnerabilities). Implementation tests NOT_RUN because design is
  awaiting confirmation.
- Next action: obtain approval or corrections for the phased plugin gates, 32 MiB
  initial limit, existing download permission for preview, and Monaco Phase 2;
  then implement Phase 1 before adding any write API.

## Recycle Bin Restore Location And Rename Conflict - 2026-09-19

- Current phase: ACCEPTANCE COMPLETE. User requested visible original restore paths
  and a rename prompt when restoring into an occupied original name.
- Contract: recycle-bin `FileEntryView` may include structured `restore_path`
  segments. The field is omitted unless the original parent and every segment
  from the owning file root remain active and the caller can traverse all of
  them. This prevents ancestor-name disclosure through trash read permission.
- Restore keeps the original name by default. A true destination-name collision
  returns `file_restore_name_conflict`; Web then prompts for a new name and
  retries the same `PATCH /files/{file_id}` with `state`, `revision`, and `name`.
  Version/tree conflicts remain `file_state_changed`.
- Performance: restore paths use generated `TrashRoot`, `NodeClosure`, and `Node`
  batch queries only; no handwritten SQL and no per-entry path query. Measured
  trash-list SELECTs are 11 for one item and 12 for 100 items.
- Execution: Direct because persistence, HTTP contract and Web interaction are
  tightly coupled. No subagents, new tables, routes, or dependencies.
- Verification: full Files package PASS; backend build PASS; OpenAPI regenerated
  and check PASS; Web 93 tests plus shared UI 85 tests PASS; full lint, typecheck,
  and production build PASS; `git diff --check` PASS.
- Real acceptance: authenticated 18280/18291 desktop and 390px mobile lifecycle
  PASS for create/upload/trash, localized full restore path, name-conflict prompt,
  renamed retry, and both restored files visible. No console/server errors or
  horizontal overflow. Screenshots are under `.local/screenshots/file-browser/`.
- Preview: API PID 10912 (`api-restore-path-v2.exe`) on 18280; existing Web PID 12496
  on 18291. Temporary browser audit script was removed.
- Next action: nothing remains for this slice. Broader ACL editor/sharing and
  production-readiness work remains separately tracked below.

## File Clipboard, Navigation And Recycle Bin - 2026-09-19

- Current phase: ACCEPTANCE. Added synchronous `POST /file-operations` without
  public `space_id`; the service derives the destination space and enforces
  same-space live IAM + ACL authorization. Account and tenant contexts are both
  supported. Copy/move remain synchronous; no conflict/task table was added.
- Web now exposes copy/cut in item context and details menus, batch copy/cut in
  selection mode, paste in the current-directory toolbar and blank-area context
  menu. Cut clears only after confirmed success; copy remains reusable.
- Directories open on one click with no arrow. Independent directory back/forward
  history is implemented and does not reuse pagination history.
- `GET /files?state=trashed` lists authorized deletion roots; `PATCH /files/{id}`
  with `{state:"active",revision}` restores the original batch atomically. The
  Web sidebar has a recycle-bin view with single and bulk restore. No permanent
  deletion behavior was invented.
- Known root scopes render through Web i18n: account/personal roots are `My files`
  rather than the backend's compatibility name `Account files`; tenant/entity/
  department roots also use translated scope names in sidebar and breadcrumb.
- Verification: full Files package PASS; focused account HTTP test covers copy,
  move, trash listing and ordered restore; Web typecheck/lint and 19 focused tests
  PASS; mocked browser audit PASS on desktop/tablet/mobile plus zh-CN/ms-MY; real
  18280/18291 browser lifecycle PASS for copy/paste, cut/move, navigation, upload,
  exact download, rename, trash and restore. Services remain on 18280/18291.
- Paste 404 follow-up: the API log contained no `POST /file-operations` 404 and
  every received authenticated paste returned 201. Real Web-proxied checks also
  passed for file copy/move, directory copy with descendants and two-item batch
  copy. The long-running Vite process predated the final API client update, so it
  was restarted on 18291 with an explicit 18280 proxy target. The newly served
  module calls `/api/file-operations`; its anonymous route probe returns the
  expected 401 rather than 404.
- Recycle-bin follow-up: a node deleted, restored and deleted again retained two
  historical `file_trash_roots`; the list query joined both and returned a
  duplicate file ID, which the Web correctly rejected as an invalid page. Trash
  listing now joins only the node's current `trash_batch_id`; historical batches
  remain stored for audit. A regression covers restore/retrash and asserts one
  visible entry with two retained placement records. Web uses a dedicated empty
  recycle-bin message, never shows the missing-root hint there, excludes trashed
  directories from folder navigation, and keeps the recycle entry in a separate
  visible sidebar-bottom area. Real desktop/mobile Web against the pre-existing
  duplicate data PASS; API 18280 PID 20316 and Web 18291 remain running.
- Verification: Files full package PASS; Web 92 tests and shared UI 85 tests PASS;
  Web typecheck, lint and production build PASS; desktop/mobile live recycle-bin
  layout and data loading PASS.
- Next action: user refreshes `http://127.0.0.1:18291/account/files` and inspects
  the corrected recycle bin. Remaining sharing/ACL editor work stays outside this
  completed slice.

## Hierarchical File Roots And Large-Scale Authorization - 2026-09-19

- Current phase: IMPLEMENTATION/REVIEW. User approved the reviewed design before
  execution. The organization-root and batched-browser foundation is implemented,
  but the complete file-management design is not production-ready.
- Goal: initialize secure Tenant/Entity/Department file roots in the resource
  creation transaction, lazily initialize account-private and tenant-personal
  roots, expose unambiguous root ownership, and remove identified read-path
  serialization/N+1 behavior without changing the approved public `/files`
  resource routes.
- Security model: organizational roots belong to Tenant/Entity/Department, not
  their creator. IAM role permissions at that organization scope define the
  whole-tree action ceiling; file ACLs refine child directory/file access and
  cannot grant an IAM-denied action. The creator is audit metadata only. Only
  account-private and tenant-personal roots follow a principal. Upper-level
  `file_read_all` remains read-only and never grants write/delete/share/manage_acl.
- Data prerequisite: every new Department must belong to exactly one Entity and
  its parent must belong to that same Entity. Add nullable `entity_id` in a new
  three-dialect migration for compatibility; legacy NULL rows fail closed until
  explicitly repaired/backfilled.
- Lifecycle: Tenant/Entity/Department roots are synchronous and atomic with IAM
  resource creation. Tenant-personal roots are lazy and idempotent; account-private
  root persistence is defined but its account-context API is not implemented.
  Root identity is `(space_id, root_scope, root_owner_id)`; there is
  no Workspace/Root table or synthetic workspace key. Existing roots are never
  rebound. Organization-wide management comes from scoped IAM roles, not a
  creator-specific root ACL.
- Scale corrections in scope: read paths must not lock Tenant-wide `file_spaces`;
  list authorization/path/ACL/object metadata must be batched; write fencing
  moves toward workspace-root revisions with sorted locking for cross-root writes;
  root authorization uses targeted ancestor/EXISTS checks instead of materializing
  entire Entity/Department subtrees.
- Existing evidence: indexed SQLite directory query plans PASS and browser page
  authorization is fixed-query batched (18 SELECTs for 2 files, 19 for 100).
  No real PostgreSQL/MySQL `EXPLAIN ANALYZE` has been run.
- Confirmed exclusions: no conflict/task/cleanup table, no eager personal-root
  fan-out, no menu initialization per Entity/Department, no global `/iam` or
  `/authn` route redesign in this phase, and no claim of production readiness
  until real lifecycle and multi-dialect scale verification pass.
- Completed this phase: migration `00010` adds Department `entity_id` for all
  dialects; PostgreSQL/MySQL use composite foreign keys and SQLite uses equivalent
  constraint triggers. New Departments require an existing same-tenant Entity,
  parent creation/moves are same-Entity only, and Entity ownership is immutable.
  OpenAPI, generated TypeScript, and the Department Web page now require/select
  an Entity and filter parent choices to that Entity.
- Verification: organization package PASS; SQLite/MySQL/PostgreSQL plain+prefixed
  organization migration and model tests PASS; OpenAPI check PASS; IAM API client
  36 tests PASS; full frontend typecheck and lint PASS. The migration suite also
  exposed and corrected its stale Identity milestone expectation (`avatars`).
- Completed lifecycle work: `file_nodes` uses `root_scope + root_owner_id` with
  three-dialect uniqueness. Tenant/Entity/Department roots are provisioned in
  the organization creation transaction; failures roll back the organization.
  Organization roots have no creator ACL. Tenant-personal roots remain lazy.
- Completed ownership contract: `/files` root views return direct Tenant/Entity/
  Department/Principal IDs and the Web validates and groups these fields instead
  of inferring ownership. Entity/Department deletion rejects roots with retained
  descendants in the same transaction and locks IAM revision before file space.
- Scale correction completed: `loadNodePermissions` now performs a read-only
  `file_spaces` lookup. It no longer executes SQLite's lock UPDATE or emits
  `FOR UPDATE` on PostgreSQL/MySQL. `replaceNodeACL` explicitly takes the write
  lock at its mutation boundary, preserving stale-write detection. A regression
  test asserts permission reads execute zero GORM update callbacks.
- Additional verification: full Files package PASS; Files+Organization race PASS
  (`files` 29.983s, `organization` 8.503s); backend `go build ./...` PASS;
  `git diff --check` PASS.
- Completed scale work: one browser page batch-loads paths, ACLs, Versions and
  Objects; IAM constraints and root reference validation are cached per request.
  Query-budget regression records 18 SELECTs for 2 files and 19 for 100 files.
  ACL reads are SQL-bounded at `unique ancestors * 256 + 1` and fail closed.
- Verification 2026-09-19: Files full package PASS; Files/Core/Organization race
  PASS; `internal/app` full package PASS; backend `go build ./...` PASS;
  `make openapi-check` PASS; Web lint, forced tests (169 PASS), typecheck and
  production build PASS; `git diff --check` PASS before the final doc checkpoint.
- Remaining work: account-private lazy root/API; audited organization-root
  archive/recovery and legacy repair; root-level revisions and sorted cross-root
  locks; real MySQL 8.4/PostgreSQL `EXPLAIN ANALYZE` and concurrency gates; ACL
  management API/Web; four sharing modes and public gateway; full storage/browser
  E2E. Sharing and production readiness remain explicitly unaccepted.
- Next action: design and implement the independent account-context `/files`
  authorization/provisioning path without tenant claims or tenant-admin exposure,
  then add root-level write revisions before real dual-database scale testing.

## Unified Files Public Resource Routes - 2026-09-18

- IMPLEMENTATION/TESTING: user rejected public `node`, separate `/directories`,
  `/file-roots`, and `/personal-file-root`; a directory is one file entry with
  `kind=directory`.
- Current public Files contract is `GET/POST /files`, `GET/PATCH/DELETE
  /files/{file_id}`, `GET /files/{file_id}/items`, `GET /files/{file_id}/raw`,
  and root-relative `GET /files/{root_file_id}/-/{path}`. The runtime path accepts
  multiple segments and authorizes the selected root plus every traversed entry.
- Root entries use `kind=directory` and a separate `scope` field. Unified
  multipart `POST /files` handles personal-root provisioning, child-directory
  creation, and file upload. Web mutations no longer dispatch by entry kind.
- Internal `Node`, workspace, space, tree, IAM+ACL gates and synchronous operation
  behavior remain unchanged. No table or persistent conflict state was added.
- FINAL PASS: full Files package tests and final race; backend build and
  `make openapi-check`; OpenAPI and TypeScript generation; 49 focused Web tests,
  typecheck, lint and production build; desktop/tablet/mobile/three-language route
  audit; `git diff --check`.
- Real isolated lifecycle PASS: existing personal root, directory creation,
  27-byte upload, ID metadata, multi-segment root-relative lookup, exact raw
  download, file/directory rename and trash. Test entries 37868046718599169 and
  37868046768930817 are trashed.
- Real authenticated Web PASS with requests limited to `/api/files?limit=100`
  and `/api/files/{file_id}/items?limit=100`, no console errors or overflow.
  Screenshot: `.local/screenshots/file-browser/unified-resource-routes.png`.
- Isolated API 18081 runs the verified binary as PID 20332; Web 18092 remains PID
  11600. Existing 18080/18191 remained untouched.
- Global `/iam` and `/authn` routing migration remains design-only.

## Global HTTP Resource Routing Audit - 2026-09-18

- User requires a strict, precise, unambiguous REST resource convention across
  every public URL and explicitly rejects implementation prefixes/segments such
  as `/iam` and `/files/nodes`. User requested reference to the archived Spring
  Boot project and previously required design before execution.
- DESIGN phase. Generated OpenAPI currently contains 202 paths: 111 under
  `/iam`, 35 under `/authn`, 7 under `/files`, plus protocol/infra/example paths.
- Read-only archive review confirms the useful convention of plural top-level
  resources, `/{id}` identity, nested owned resources, and explicit content/info
  representations. Do not copy its `batch` or `recycler` command paths.
- Added proposed `docs/http-resource-routing-design.md`. It covers every current
  top-level family, removes module/internal names from application routes,
  preserves standards/probes, specifies noun-based state transitions, defines
  final file/directory routes, and proposes a global OpenAPI convention gate.
- No route or runtime code changed in this design checkpoint. Existing isolated
  preview remains on API 18081 and Web 18092; do not restart it until a contract
  is approved and implemented. Existing 18080/18191 services remain untouched.
- Approval required before the breaking implementation: remove both `/iam` and
  `/authn`; expose no deprecated aliases; retain mandatory tenant/entity headers
  rather than nesting all globally identified resources in URL paths.
- Next action: obtain approval/corrections for the three routing decisions, then
  implement the file route slice first with type-specific ID validation and the
  global OpenAPI path convention test.

## Basic File API Priority Correction - 2026-09-18

- User correctly rejected copy/move-first delivery while create directory,
  upload, download, rename and delete are missing. Copy/move public exposure is
  paused; its internal implementation/tests remain intact.
- IMPLEMENTATION plan: external API uses authorized `roots` and global node IDs;
  `space` remains internal. First usable slice is an idempotently provisioned
  personal root for the current principal in the selected tenant/entity, browser,
  directory creation, single-file streaming upload/download, rename and trash.
- Personal-root provisioning is explicit/idempotent and owner-only at creation.
  It must lock the tenant space, validate current transaction-bound IAM references
  and `file_create` scope, create owner ACLs, and append audit atomically. A GET
  must not create data. Organization roots and ACL editor follow this usable slice.
- Reuse existing immutable Driver, object preparation/checksum/metadata, file
  Version, directory closure, trash, node ACL, IAM policy fence and audit appender.
  No conflict/task tables, alternate permission system or upload spool.
- Next action: add caller-transaction node primitives and basic service tests,
  implement root/directory/upload/download/rename/trash HTTP, regenerate OpenAPI
  and Web client, then connect the live Web interactions and restart only the
  isolated 18081/18092 preview.

## Isolated File Preview And Public API Gap - 2026-09-18

- Isolated current API is running on 127.0.0.1:18081 (PID 19948) and Web on
  127.0.0.1:18092 (exec session 94997). Existing 18080/18191 services remain
  untouched. Fresh SQLite only; no UAT/MySQL writes.
- Bootstrap administrator `admin@example.test` was created. Real Web-proxied
  password login and current-session requests both returned HTTP 200.
- Runtime readiness, Web proxy, Swagger and OpenAPI all return HTTP 200. Runtime
  OpenAPI contains only workspace listing, child listing and synchronous copy/move.
- Browser audit PASS on desktop/tablet/mobile, zh-CN/ms-MY, anonymous redirect and
  authenticated generated-client paths. Audit now permits the one extra identical
  GET caused by React Strict Mode while rejecting unexpected paths or missing
  tenant/entity headers.
- User correctly identified the public product gap: upload is NOT exposed or
  usable. Upload tables/models are existing persistence groundwork only; there is
  no current upload service, authorized HTTP lifecycle or Web upload workflow.
- `Space` is currently an internal tenant/account namespace and revision fence.
  `Workspace` is a directory node marked as an organization/personal ACL root.
  Fresh preview has no public workspace provisioning route, so it has no file
  roots to browse. Do not call this file product production-ready.
- Current step: obtain confirmation before materially simplifying the public API:
  keep space internal, expose authorized roots and globally identified nodes, then
  implement upload/download/directory mutations with IAM+ACL and real Web flows.

## Live IAM To File ACL Integration Checkpoint - 2026-09-18

- Latest user says continue. Direct; preserve dirty work, no agents, commit/push,
  original DB changes or existing service restarts. Full product NOT READY.
- IMPLEMENTATION checkpoint: core AuthorizationReferencesOn now validates active
  entity/department/principal tenant anchors in the caller transaction under the
  policy revision fence. Focused core tests PASS, including rollback visibility,
  inactive/missing refs, invalid/unwired calls and stable module capability.
- Files registers file_view,13 independent file actions and file_read_all in the
  shared IAM catalog during module injection. operationService no longer accepts
  a test-only arbitrary operationAccess: it resolves current IAM subjects, checks
  each action-specific constraint, validates workspace references and evaluates
  inherited node ACL. Copy source normalizes to read.
- file_read_all bypasses ordinary ACL only for list/traverse/read/download and
  only within its IAM workspace scope. It does not grant create/update/move/
  delete/share/manage_acl. Owner ACL identity is added only when actor equals the
  validated personal-workspace owner. Tenant/entity/department/personal scope
  mapping fails closed; entity-bound workspace must match selected entity.
- Focused files tests PASS for service audit/rollback/replay, permission catalog,
  ACL denial, management-read exception, reference failure, entity mismatch,
  owner spoofing and revision mismatch. Full `go test -race ./internal/files/...
  ./internal/iam/core -count=1` PASS (files33.8s/core63.6s).
- Next action: build authorized bounded workspace/children/path read service and
  synchronous copy/move HTTP/OpenAPI, then regenerate frontend API types and wire
  `/files` to real data. ACL edit delegation and sharing remain subsequent work.

## Transaction-Bound IAM Subjects Checkpoint - 2026-09-18

- Latest user says continue. Direct execution; preserve dirty work, no agents,
  commit/push/original DB writes/existing service restarts. Full product NOT READY.
- IMPLEMENTATION checkpoint: core now publishes caller-transaction functions for
  entity constraints and current authorization subjects. Both lock the tenant
  policy revision before IAM reads and reject stale/changed snapshots.
- AuthorizationSubjectsOn verifies active tenant membership and selected entity,
  then resolves the current principal, active department, active static/dynamic
  groups, and effective direct/temporary/group/position/entity roles. It grants
  no action and is only trusted input for a business-owned ACL.
- Focused tests PASS for uncommitted revocation visibility+rollback, policy fence,
  inactive/invalid/unwired/cancelled failures, department/group/role resolution,
  expired memberships, stable pre-wire capabilities and duplicate registration.
  Command: `go test ./internal/iam/core -run 'TestAuthorizationSubjectsOn|TestEntityConstraintOn|TestModuleProvidesStable|TestModuleProvidesTransaction|TestFrozenIAMCapabilities|TestIAMCapabilities' -count=1`.
- Next action: add transaction-bound validation of workspace IAM references, then
  wire file action constraints + verified subjects + management-read exception
  into the operation service and replace its test-only operationAccess callback.
  Do not expose HTTP until this real chain and audit remain atomic and tested.

## Persistent Workspace And Node ACL Checkpoint - 2026-09-18

- Latest user continues approved section21. Direct; preserve dirty work, no agents,
  commit/push/original DB changes/existing service restarts. Full product NOT READY.
- IMPLEMENTATION checkpoint: migration9 adds workspace kind/entity/department/
  principal + inheritance stop to Node, and durable single-action NodeACL rules.
  Update model/dbgen/prefix resolver; regenerate typed Node/NodeACL/Query files.
- Private persistence methods initialize only unbound directory affiliations,
  load nearest validated workspace via existing closure/path, and replace direct
  ACL sets with node+space revision fences in caller transaction. No parallel org
  mirror, automatic rebind, default grant, conflicts/tasks or original DB migration.
- Account workspace is account-root-only; tenant personal is explicitly owned.
  Workspace root is mandatory ACL boundary; child stop excludes old ancestors.
  Limit256 expanded rules/node, refuse over-budget reads/edits, no truncation.
- Tests snapshot ACL+nodes+space too: inherited/direct deny, interruptions,5 valid
  bindings, account isolation, unbound/hidden/foreign scopes, stale edits, duplicates,
  limits, late SQL/ID/caller audit rollback, concurrent edits (one success/one conflict).
- Verification: full files race+coverage PASS (86.3%, BELOW90%); added concurrent
  edit race PASS. files scoped pinned lint/vet, backend build, architecture and
  diff check PASS. SQLite, MySQL dialect memory engine, embedded PostgreSQL upgrade/
  down/up and FK/CHECK/uniqueness tests PASS plain+prefixed (6 combinations, no skips).
  Does not establish real MySQL8 performance or full live IAM/HTTP/Web acceptance.
- Private helpers only persist; services must validate live IAM identities,
  workspace relationships, scope, delegation ceilings and actual audit first.
- Next action: inspect core authorization snapshot/current role/group resolution
  and organization-owned affiliation capabilities; add a narrow transaction-bound
  current subject/affiliation contract, wire read-all vs ordinary ACL access, then
  register actual files OpenAPI and generated client for Web permissions/sharing.

## ACL Core And Actual Audit Checkpoint - 2026-09-18

- Latest user says continue after design delivery; implement the three recommended
  choices: management-read exception to ordinary ACL only, separate account-private
  versus tenant-personal ownership, anonymous read/download-only link sharing.
- Phase IMPLEMENTATION checkpoint. New node ACL evaluator/tests cover5 subject kinds,13 separate
  actions, inheritance/scope, interruption, explicit deny priority, source tracking
  and malformed input fail-closed. Not wired to database/HTTP/Web/current subjects.
- operationService now rejects empty/explicitly denied transaction IAM constraint
  before business reads; mandatory per-node access remains. Immediate engine no
  longer persists cost in PlanJSON. Legacy schema/internal preparation untouched.
- Actual audit appender SQLite service tests: actor, tenant/account rejection,
  missing claims/dependencies, denial/cancellation/deadline, transient conflicts,
  replay read checks, audit-once concurrent replay, late audit/commit rollback.
  Policy/access are test doubles; not end-to-end IAM or public-route acceptance.
- Verification: files+core race/coverage PASS; final files race/coverage rerun
  including commit rollback PASS. files85.9% BELOW90%, core90.1%. Focused service+
  ACL tests PASS; files scoped lint, vet files/core, full backend build, architecture
  tests and diff check PASS. No original DB writes/service changes/commit/push.
- Next: persistent workspace
  ownership+ACL migrations/query and transaction-bound live IAM subject resolution.
  Do not expose placeholder file_view routes or allow-all access capabilities.

## Hierarchical Files ACL And Sharing Design - 2026-09-18

- Latest user requests design BEFORE implementation. Phase DESIGN, awaiting
  confirmation. No business code, database or existing service changes this turn.
- Added design section21 to existing docs/file-management-design.md, superseding
  old public-sharing exclusion without altering previous implementation evidence.
- Confirmed requirement: platform-wide, tenant/subtree, entity/subtree management
  visibility plus fine-grained account/department/directory/file permissions;
  temporary/permanent password/token sharing; synchronous copy/move constraints.
- Proposed reuse: account/tenant physical namespace, workspace roots with explicit
  entity/department/owner affiliation, existing IAM scope/action ceiling, node ACL
  inheritance+deny rules, separate bounded management-read capability, durable
  ACL/share configuration (not durable conflicts), generated HTTP and real Web.
- Pending product confirmation: management-read bypasses ordinary child ACL;
  distinguish global account-private versus tenant-personal files (tenant admin
  only latter); anonymous read/download sharing allowed, no anonymous writes.
- Inspection confirms no current node ownership/workspace ACL or share model;
  production Web remains unavailable. Transaction service claims do not support
  platform/account requests yet; IAM constraints identify entities, not node IDs.
- Verification this turn: documentation review only; new feature tests NOT_RUN.
  Prior files85.3% coverage remains below90%; prior core lint reports3 unrelated
  S1016 in core/http.go. No production readiness or visual screenshot acceptance.
- Next action: obtain user confirmation of section21.10, then complete isolated
  real-audit/transaction IAM tests before workspace/ACL schema or HTTP changes.
- Preserve dirty work; no agents/commit/push/original DB migrations/service restarts.

## Transaction IAM And File Audit Continuation - 2026-09-18

- Latest user says continue implementing, after read-only archived reference.
  Direct; preserve existing dirty work, no agents/commit/push/original DB writes/
  existing service restarts. D46/D47 and archive cautions remain authoritative.
- Phase IMPLEMENTATION. Extend core with a narrow EntityConstraintOn function
  capability, reusing EntityConstraint with caller transaction and tenant policy
  lock. Compare current locking policy revision with snapshot revision to refuse
  stale MySQL repeatable-read snapshots. Do not change ordinary evaluator behavior
  or create a parallel authorization algorithm/interface. Extend exact capability
  ownership inventory, retaining frozen interface gate.
- Next: add transaction/live policy/rollback/concurrent revocation tests, publish
  capability through existing module services, then files transaction runner with
  audit-once replay and authorized workspace mapping before HTTP/OpenAPI/Web.
- New edits UNVERIFIED; do not claim real file API or production readiness.

## Archived File Management Reference Review - 2026-09-18

- Latest user requests read-only review of
  D:/Projects/Archives/Synron/StoreV3/SynronCloud for useful file-management logic.
  Inspected actual spring-cloud-file service/controllers/strategy/mapper/schema,
  storage/recycler code, store MaterialController and Vue material/blockSystem UI.
  No archived files changed; no Gradle/server/tests executed or old DB touched.
- Useful reference: synchronous source-ID batch request + destination + per-kind
  strategy; directory merge mapping to existing target IDs before closure rewrite;
  new virtual nodes share physical storage_id; per-source/category result counts;
  copy/cut stores source selection, paste/drag sends a real operation, refresh after
  acknowledged success. Existing current engine already covers core semantics.
- Actual old SQL uses fixed t_file_batch/t_file_closure_batch staging tables with
  isolation tokens; tmp_* strings are NOT dynamically created database tables.
  dropTable actually DELETEs token rows. This is not a pending decision workflow,
  nor a requirement for batch support. Do not import staging/conflict tables.
- Do not port: overrideDuplicate hard-deletes target nodes/closures (no retained
  versions); renameDuplicate only appends second-resolution timestamps; staging
  PRIMARY KEY(id) is not isolation-qualified and reFlagDuplicateFolder4Rename lacks
  isolation predicates; browser Number(el.id) loses precision for large int64 IDs;
  strategy defaults to RENAME and has no ask/per-item observed-revision confirmation.
  batch path merely forwards requester creator/owner IDs; no node-level IAM check
  visible in inspected path, so it is not evidence of current transaction security.
- Reference assessment STATIC ONLY, not runtime/production acceptance. No product
  decisions or current business code changed. Prior engine evidence remains valid.
- Next action remains transaction-bound core IAM/audit capability and approved
  workspace mapping, then generated HTTP/OpenAPI and real Web. Apply clipboard/
  drag interaction ideas within React/shared UI, keep GUID strings and mandatory
  current IAM, atomic rollback, version retention and transient conflict semantics.

## Synchronous Batch Engine Continuation - 2026-09-18

- User requests continuation under D46: real-time copy/move, transient conflicts,
  no new conflict/decision tables or pending tasks. Direct; preserve prior dirty
  work, no agents/commit/push/original DB changes/existing service restarts.
- Phase IMPLEMENTATION. Reuse canonical validation, space write fence, bounded
  prospective overlay, policy walker, version/trash/closure writes and existing
  success result journal ONLY for scoped idempotent replay. A caller transaction
  simulates first and returns transient ask frontier; on explicit confirmation
  it checks observed space revision and exact per-conflict keys, then executes
  all changes with mandatory live access checks and cost parity, recording final
  success atomically. No preparation items/pins, operation row on conflict/failure,
  queued/ready/awaiting states, leases or conflict cleanup in this new entrypoint.
- Source protection uses normalized roots in request memory rather than persistent
  snapshot items; no whole-tree arrays. Nested ask after a chosen merge can return
  a subsequent transient frontier; no partial publication. Stable conflict keys
  use real IDs or created-from-source aliases and effective origin content versions,
  never transient negative IDs or newly minted rolled-back version/node IDs.
- Files: extend operation.go canonical helper, operation_execute.go source guards/
  transient policy hooks and overlay parity; add synchronous runner/conflict types
  and focused tests. Existing async engine retained as prior internal work; no
  schema/query changes. HTTP/current IAM+audit wiring and real Web API are later.
- Engine checkpoint IMPLEMENTED / TESTED (not full product acceptance):
  executeImmediateOperation now runs bounded simulation and actual publication in
  the caller's transaction, returning transient conflict frontiers with stable
  real-node/provenance/content identities. Exact per-item strategies, observed
  space revision, allowed policies, source overlap and final cost/revision parity
  are checked. Unknown/stale/duplicate decisions fail; self-copy ask only renames.
  Whole moves reauthorize every descendant including old trash; source protection
  releases adopted/recycled input roots. Simulation uses cached created nodes so
  content revision changes match real writes. Legacy replay reuses the same helper.
- Added operation_immediate_test.go: 11 top-level tests including nested frontiers,
  prospective files/directories, distinct per-item choices, copy/move origin keys,
  self-copy, stale/unknown/invalid decisions, descendant/old-trash denial, late SQL
  journal failure, injected audit failure, new-node IAM denial, budget rollback,
  parent/child root normalization, old-trash moves, type replacement and concurrent
  replay over four independent SQLite pooled connections. Database snapshots prove
  no persistent changes on conflicts/failure; no OperationItem rows on success.
- Final verification: files race+coverage PASS (85.3%, BELOW required90%);
  files vet PASS; pinned scoped golangci-lint PASS; go build ./... PASS;
  architecture tests PASS; gofmt and git diff --check PASS. No diagnostic process
  remains running. No new schema/query changes or original database writes.
- Self-review PASS for this engine checkpoint: scope/reuse/architecture/rollback/
  bounded traversal/durable state. Acceptance: synchronous internal engine tested;
  full file-management product NOT READY. Audit failure tests inject a transaction
  error; actual audit appender and IAM integration are not wired or accepted.
- Next action: inspect core.AuthorizationEvaluator/Authorizer/revision.go and
  directory-source contracts to add transaction-bound current IAM evaluation,
  approved department/entity workspace mapping and audit-once result replay; then
  register files HTTP/OpenAPI and connect the existing real Web loader/mutations.
  Current evaluator only exposes independently pinned writer methods; do not use
  those as if they were bound to the file transaction, and do not publish an
  allow-all API. Frontend files/page.tsx remains intentionally unavailable.

## Real-Time File Operation Contract Correction - 2026-09-18

- User explicitly rejects persistent conflict/decision tables: copy/move are
  real-time operations. This newest contract supersedes earlier queued/planned/
  awaiting-decision product design and historical next actions below. Direct;
  preserve all prior work, no agents/commit/push/original DB/service changes.
- Phase ACCEPTANCE for draft withdrawal/contract correction only. Removed ONLY
  this continuation's newly introduced conflict and
  decision model types, ConflictCount field, generator/resolver additions, three
  migration9 drafts and two orphan generated query files. Regenerated files queries
  from retained inputs. Prior migration8 cost, operation engine and all Web work
  remain unchanged. No migration9 was applied to any original database.
- Updated design12/13/14/15: detect conflicts within the request and return them;
  confirmation is a fresh explicit-strategy request with observed versions;
  reauthorize/revalidate under one atomic transaction and return final success.
  No conflict/decision persistence, user-waiting transaction, pending task, leases,
  cleanup workflow or implicit background queue for ordinary copy/move. Upload
  sessions/transfer tasks are separate. Do not weaken scope/permission/revision/
  budget/idempotency/audit/rollback checks to make operations synchronous.
- New durable conflict flow was never implemented; removed schema was drafts only.
  The synchronous replacement runner/HTTP/Web mutation integration is NOT yet
  implemented. Existing internal operation/snapshot tables require scoped review
  for reuse/replacement; do not delete prior migrations or user data automatically.
- Verification: files generator PASS; new-table/model references absent in files
  sources, dbgen/resolver/generated registry match their prior contents, retained
  operation model differs only by the earlier migration8 PlanJSON field.
  `go test -race ./internal/files/... -count=1` PASS; `go build ./...` PASS;
  `git diff --check` PASS. No diagnostic session remains running.
- Self-review PASS for minimal draft-only removal, prior changes preserved, query
  regeneration, no database mutation, accurate new contract and durable state.
  Acceptance PASS for requested correction, full file product remains NOT READY.
- Next action: implement the
  synchronous request runner reusing bounded traversal and caller transaction,
  transient ask responses/confirmed-version validation, existing IAM/audit and
  idempotency capabilities; connect generated HTTP types to real Web operations.
  Do NOT resume old durable conflict model or async decisions/cancel endpoints.

## File Management Web Continuation - 2026-09-18

- Latest user explicitly requires the Web page too; Direct execution, no agents,
  commit/push, original DB writes or existing preview restarts. Approved design13
  governs UI; backend conflict/decision implementation remains pending, unchanged.
- Phase ACCEPTANCE for Web presentation checkpoint ONLY, NOT full product.
  Added authenticated /files lazy route and shared-token browser
  component: bounded100-item pages, current-page search/type/sort,3views, ID-based
  multi/range selection, breadcrumbs/folder navigation, responsive spaces drawer,
  metadata sidebar/sheet, distinct empty/loading/forbidden/unavailable/error states.
- Production page intentionally unavailable until authorized files HTTP/OpenAPI
  exists; no fake tenant files, provisional HTTP calls, public navigation bypass,
  mutation, uploads or success simulation. Effective menus still own navigation.
- Isolated browser fixture outside production router and contract audit added.
  Frozen lockfile install restored missing react-easy-crop dependency; no version
  or lockfile changes. Final frontend typecheck/lint/build PASS; forced tests163
  PASS (78 IAM incl11 new,85 shared), git diff --check PASS. Existing Vite config
  future native-loader warning remains unchanged; not a build failure.
- Browser contract audit PASS desktop1440/tablet768/mobile390 plus zh-CN/ms-MY:
  ID/range/keyboard selection, search/no-results/type filters,3views, sort,
  properties, pagination/folder/breadcrumb navigation, responsive drawer/layout,
  metadata revocation, failure isolation, Escape and focus restoration. Route
  fixtures verify anonymous login redirect and authenticated unavailable page
  with effective menu. No provisional files API calls, runtime page errors, or
  fixture name markers in production build. All browser cases use isolated
  snapshots/auth fixtures, NOT real database/storage/IAM acceptance.
- Browser test caught native checkbox preventDefault reverting range endpoint;
  fixed via native-event modifiers in onChange. Keyboard test caught autofocus
  on tooltip button swallowing Escape; properties title now receives initial
  focus. Screenshot review removed duplicate mobile close buttons. Closed-dialog
  animation completion is awaited before keyboard reopen/viewport assertions.
- Reviewed desktop/mobile details, properties and mobile large-view screenshots.
  Evidence: .local/screenshots/file-browser (ignored). New owned Vite is running
  on http://127.0.0.1:18191, listenerPID6756, launcherPID4624; no existing service
  restarted. /files is authenticated/unavailable; interactive fixture at
  /scripts/fixtures/file-browser.html?locale=zh-CN is not a production route.
  Generated diagnostics under app .temp are ignored; no staging/commit/push.
- Self-review PASS for scoped reuse/ownership/theme/security/complexity/state;
  presentation-stage acceptance PASS. Original file-management request NOT READY.
  This is NOT a lazy whole-space tree, space search, path resolution/deep link,
  live API, upload/download/preview, mutation, clipboard/drag/context menus,
  versions/trash/task/conflict UI, or real E2E. Earlier backend changes preserved.
- Next action: implement exact conflict identities and revision-bound ask decisions
  in files-owned models/migrations/shared walker; wire current IAM/audit runner,
  publish files HTTP/OpenAPI and generate the client types; then connect the
  /files authorized loader and complete transfers/mutation/task/conflict UI and
  real end-to-end acceptance. Existing approved design suffices; no user decision
  needed for those scoped implementation steps. Full90% and production gates stay.

## File Management Continuation - 2026-09-18

- User requests continuing the approved file-management implementation. Execution
  Direct; existing design explicitly excludes subagents. No new commit/push,
  production database changes or preview restart authorized by this request.
- Phase ACCEPTANCE for this continuation checkpoint, NOT full product acceptance.
  Local Windows read/write tests restored. Shared operation walker
  now simulates bounded prospective name slots/tree state, including recursive
  batch collisions, replacement, skipped-source preservation and old-trash moves.
  Migration8 persists exact cumulative node/closure/result-count costs; execution
  rejects missing/legacy/corrupt costs and rolls back mismatched actual results.
- MySQL no-op UPDATE changed-row portability defect found in operation fences.
  Replaced non-SQLite fences with typed SELECT FOR UPDATE; SQLite takes a no-op
  write lock BEFORE reading, without using changed rows as an existence check.
  Positive/absent-space regression with simulated zero changed rows PASS.
- Fresh native Windows baseline: `go test ./internal/files/... -count=1 -cover`
  FAIL (73.3% files coverage). Independent JSON rerun confirms 13 failed tests:
  directory fsync returns Access denied; symlink fixture lacks Windows privilege.
- Keep immutable publication, file fsync, exact lengths, root confinement and
  cancellation. Windows directory fsync is unavailable; explicitly document its
  metadata power-loss durability limitation instead of hiding arbitrary I/O errors.
  Replace privileged symlink fixture with an actual Windows junction when needed;
  confinement assertions remain mandatory, not skipped.
- Final verification: `go test -race ./internal/files/... ./internal/iam/identity
  -count=1 -cover` PASS; files85.1%, identity85.0%, BOTH below90% gate. Two-run
  operation planning/race regression PASS. Architecture -count1 PASS; fresh
  identity/app regressions PASS; go build ./... PASS; affected vet PASS.
  New files migrations all8 up/down and cost roundtrip PASS in SQLite, embedded
  PostgreSQL16 and in-process MySQL dialect, with/without deployment prefix.
  In-process MySQL is NOT real MySQL8 acceptance. Scoped linter PASS after fixing
  existing unchecked closes/type assertions and preserving frozen cancellation
  states. Final files+identity linter PASS. Full backend linter FAIL on three
  pre-existing gosimple DTO conversion findings in iam/core/http.go:1291,1315,
  1372, deliberately unchanged as unrelated to file-management work. No full CI
  or production acceptance claim; public API, cloud/transfer/browser acceptance
  remain NOT_RUN / NOT READY. Query generator rerun and git diff --check PASS.
- Remaining approved scope: complete conflict decisions/plan persistence, IAM/audit
  runner, transfers/S3, account creation tracking, public API/UI and fresh real
  SQLite/MySQL/Postgres/storage/browser/fault/resource acceptance; 90% gate unchanged.
- New prospective nodes have no IAM identity; preflight defers their checks, while
  real execution retains mandatory current checks. Cost plan does NOT implement
  revision-bound per-conflict decisions or bind exact conflict identities yet.
- Avatar read now joins storage close errors with read errors; success, read,
  close, combined failure and oversized-input tests PASS. Read-only resource
  handle/rollback cleanup in files explicitly discards close results; successful
  publication still reports file fsync/publication/close errors. No gate weakened.
- Self-review PASS for scoped ownership/reuse/atomic rollback/generator/index/
  regression/complexity. Stage acceptance PASS: native Windows functionality,
  bounded batch reservations/cost refusal/persistence/result validation, zero
  changed-row fencing, read-only preparation and failure pin release verified.
- Next action: persist exact per-conflict identities and revision-bound decisions
  in owned operation models/migrations, extend the shared walker with resolved
  ask policies, then wire the IAM/audit service runner before exposing file HTTP.
  Continue approved transfers/S3/account creation/UI and fresh full acceptance;
  no additional approval needed for that design, but commit/push are not implied.
- No staging/commit/push, original DB changes, preview/service restart or subagents.
  Last checkpoint: final verified continuation state, 2026-09-18.

## Authorized Checkpoint Delivery - 2026-09-18

- User explicitly authorized committing and pushing ALL local deliverable changes
  on IAM. This supersedes earlier no-staging/commit/push constraints; ignored
  credentials, runtime files/databases, previews and diagnostic outputs stay local.
- Delivering file-management foundation, avatar storage integration, generated
  queries/OpenAPI types, approved design and tests as an implementation checkpoint,
  NOT full product acceptance. Remaining work and83.4% coverage gate gap stay recorded.
- Reviewed tracked/untracked manifest, storage ignore rules and scoped changes;
  no new credential markers in new module/design/tests. git fetch origin and diff
  check PASS. Last functional verification is recorded below; no full CI/E2E claim.
- Next: commit explicitly reviewed paths, push IAM to origin/IAM, verify equal
  local/remote heads and clean worktree. Continue the remaining approved design
  only after this delivery request is handled; original DB/previews remain untouched.

## Existing-Target Preparation Preflight - 2026-09-18

- Phase IMPLEMENTATION, Direct. operation_preflight.go adds read-only target
  checks before preparing -> ready: existing recursive merge collisions, target
  read/update permissions, paged replacement-subtree delete permissions, per-target
  node bounds, selected-source/ancestor overlap and replacement containing inputs.
  No node/version/closure mutation; failed seal atomically releases source pins.
  Execution retains its independent current authorization and revision checks.
- New fresh SQLite tests cover nested update/create/read denial, descendant delete
  denial, second page denial, target bounds, successful105-node target inspection,
  skip without update/delete rights, self-merge and source-in-replacement rejection.
- Worker expiry fixture used an unexecutable self-merge; changed its policy to
  legal same-parent copy+rename. All expiry/pin release assertions retained.
- PASS: files-target-preflight-final-race.log, files-target-preflight-coverage.log
  (83.4%, BELOW90%; no gate lowered), files-target-preflight-architecture.log,
  files-target-preflight-identity.log, go vet files and git diff --check.
- Self-review: existing-target traversal stays indexed/paged and no storage I/O;
  failed preparation releases pins; no new schema/API/configuration/dependency.
  Original-request acceptance NOT READY. This is NOT a complete cost/conflict plan:
  intra-batch virtual name reservations, cumulative target/depth/closure costs,
  ask/per-conflict revision-bound decisions and real IAM/audit runner still missing.
- Next: implement bounded virtual name reservations and cost simulation matching
  execution; persist exact conflict identities/plan revision and decisions before
  exposing HTTP. Transfers/cloud/account creation/UI/fresh full E2E still pending.
- No staging/commit/push/delegation, original DB changes or preview restart.

## Atomic Operation Engine Checkpoint - 2026-09-18

- Phase IMPLEMENTATION, Direct. Migration7/model/resolver/generator now add
  operations and paged snapshot items with nullable live node/version/object pins.
  Pins constrain exact same-space node/version/content; terminal release retains
  audit snapshots. Account object collector checks operation content pins.
- operation.go prepares source snapshots in short pages100 with unchanged-space
  revision fence each page/seal, normalized roots, actor+space idempotency identity,
  canonical request hash, durable failure/cancel pin release. Explicit space input
  makes the write lock precede reads (including raw deferred SQLite harness).
- operation_execute.go internal caller-transaction engine: same-space copy/move,
  explicit overwrite/merge/replace/skip/rename policies, immutable content references,
  directory replacement trash-before-placement, merge retains skipped source items,
  keyset traversal, cumulative budgets, precommit source validation/required access
  callback, stored success/result in same transaction and succeeded replay.
- Verified fresh SQLite recursive copy/move merge, file overwrite retains target ID,
  directory/type replacement recycles target before placement, skip retains source,
  rename incl same-parent copy, source physical-object count unchanged, success/result
  durable replay without duplicate versions/tree revision. Live exact-content pins
  prohibit deleting the source version; cancel/expiry/failure release pins atomically.
- Preparation page100 fault tests: generator failure/page budget/revision change
  after initial fence all preserve earlier page snapshots but release all live pins,
  never mutate user tree. Tests include normalized nested sources, actor/scoped key
  identity, binary case-sensitive keys, stale plan and mandatory access callback.
- Expiry added to existing owned cleanup worker before account GC, cursor resets
  on exhausted sweep so older/later-expiring plans aren't starved; actual worker
  expiry and shutdown tested. Plan TTL supplied explicitly, no new retention default.
- Added indexed child-ID cursor in migration7; source pages order closure descendant
  key. SQLite EXPLAIN source-page JOIN, child cursor, item-page and object-pin lookup
  all SEARCH without SCAN/temp sort (expiry filter uses owned expiry index).
- Tests PASS: .temp/files-operation-tests.log, files-operation-race.log,
  files-operation-final-race.log (latest includes result-root current-access replay),
  files-operation-identity.log, files-operation-architecture.log (-count=1),
  files-operation-compile.log (compile-only), files-operation-atomic-boundary.log
  (-race count3 strengthened late SQL/permission/cumulative-budget rollback tests),
  files-operation-app.log (Bootstrap/Module/Config), go vet files and
  git diff --check. Coverage82.9%, BELOW90%; gate not lowered.
- All owned diagnostic sessions finished. Self-review fixed byte-sensitive keys,
  expiry cursor starvation, missing active-file heads, request hash/root validation,
  succeeded replay result-root current access and source/target overlap protection.
  Strengthened final-result failure test to include target recycling+placement;
  cumulative-budget failure occurs after clone+overwrite, not before first mutation.
- Two reviews: scope/reuse/ownership/transaction/index/regression checked for this
  foundation; full original requirement review remains NOT READY for missing items.
- Original-request acceptance NOT READY. No public file API/UI/cloud acceptance.
  Ask/decision UI, accurate preparation-time full cost/conflict estimate, service
  runner/audit+IAM transaction capability wiring/cancel-during-commit remain pending.
  Reconcile version provenance references before implementing trash purge: current
  immutable FK-backed provenance can retain a source version after a logical copy.
- Next: implement full preflight conflict/cost plan with
  revision-bound decisions; wire operation runner into IAM/audit capabilities before
  exposing HTTP. File transfers/cloud adapters/legacy creation/UI/E2E still pending.
- No staging/commit/push/delegation; original databases/previews preserved.

## Trash And Closure-Safe Move Checkpoint - 2026-09-18

- Phase IMPLEMENTATION, Direct. Added migration6 in all3dialects: trash batch,
  normalized root snapshots, indexed node batch association with same-space guard.
  Extended generator inputs/model/table resolver and regenerated successfully.
- Added internal trash.go: transaction-required space CAS, bounded selection and
  subtree budget, normalize parent/child roots, tag only currently active nodes,
  original-placement restore with whole-batch conflict/parent checks; SQL chunks100
  share caller transaction. No file I/O; previously trashed descendants retain batch.
- Added move.go: pinned space/source/destination revisions, active directory target,
  portable rename, scoped collision/cycle/depth checks, explicit node/closure budgets.
  Replace only external ancestor edges; preserve internal closure, versions, objects
  and old trashed descendants. Chunk SQL100 in caller transaction, no storage I/O.
- Added fresh SQLite tests: normalized roots/name release/whole restore conflict,
  old deletion doesn't revive/deleted parent rejection, budgets/scope/CAS/invalid
  inputs/caller audit rollback, delete and restore second SQL chunk failures,
  populated migration6 rollback/up, batch query EXPLAIN SEARCH index; real stored
  old/current content survives GC with all account refs revoked and file trashed.
  Moves test paths/internal rows/old trash restore, cycle/depth including trash,
  stale pins/foreign scopes/invalid file parent, file ext vs immutable content,
  node write failure after closure mutation, composed operations rollback and
  concurrent pins allowing exactly one commit (also repeated20).
- PASS: .temp/files-trash-move-final-race.log, files-trash-identity.log,
  files-trash-move-concurrency.log, files-trash-move-compile.log (compile-only),
  go vet ./internal/files/..., git diff --check. Architecture -count=1 verified
  separately from tests module, files-trash-move-architecture.log.
- Self-review found/fixed MySQL down order: drop FK before its supporting index.
  MySQL/Postgres runtime NOT_RUN; no cloud/browser/public file E2E or full CI yet.
  Coverage remains below90% gate (see files-trash-move-coverage.log); gate not lowered.
  Original-request acceptance NOT READY. Public API/authz, alternate-target restore,
  merge/overwrite/copy/purge/operations persistence, cloud/transfers/UI outstanding.
- Next: build operation plan/result persistence plus atomic conflict engine over
  these primitives, including file overwrite by version and recursive directory
  merge/copy; no partial commits or waiting on user decisions inside transactions.
- No staging/commit/push/delegation; old databases and previews untouched.

## Immutable File Versions Checkpoint - 2026-09-18

- Phase IMPLEMENTATION, execution Direct; full request acceptance NOT READY.
- Recovered prior version-schema run: .temp/files-version-schema.log PASS.
- Migration5/model/generated queries/current head and appendVersion primitive
  present for SQLite/Postgres/MySQL; no storage I/O inside caller-owned transaction.
- Added version_test.go: initial/overwrite retain node ID, stale revision/sequence
  rollback including space revision, immutable history, rename preserves detected
  physical MIME/history ext, directory/wrong-node heads rejected, wrong-space and
  staged-object append rejected. Historical-only physical reference GC protection
  tested with distinct replacement object. Populated SQLite migration5 down/up tested.
- PASS: go test -race ./internal/files/... (final result below), go vet
  ./internal/files/..., git diff --check. PostgreSQL/MySQL runtime NOT_RUN.
- Self-review: strengthened GC test to use separate historical/current objects;
  transaction ownership, DB scope constraints and generated output ownership checked.
- Remaining: trash batches/atomic restore, closure-safe moves/copies/merge, full
  account creation tracking, cloud driver/transfers/authz/Web UI and whole-chain E2E.
- Next: implement owned trash batch/root association schema and transaction-bound
  soft delete/restore preserving internal closure, releasing active name slots;
  test nested selections, restore conflicts, deleted-parent and whole-batch rollback.
- No staging/commit/push, delegation, original database or preview changes.

## Durable Account Files / Indexed Closure Tree - 2026-09-18

- Phase IMPLEMENTATION, Direct. User asks whether closure/path children use
  indexes: schema and real SQLite EXPLAIN now implemented/tested, not just planned.
- Added owned account preparation table (migration3, all3dialects) reserving key
  BEFORE I/O. Stage publishes verified metadata into staged object after ACK;
  Register consumes ready intent + object + reference in caller transaction.
  It rejects a nontransaction caller; Stage rejects nested writer transactions.
- Cleanup claims object before deletion, checks active references, expires
  candidates, retries failed deletes. Uncertain abandoned writes remain durable
  tombstones with repeated cleanup (not asserted remotely fenced/released).
  Local pending keys are deterministic for NEVER-reused immutable generated keys;
  reclaim removes publication source first. Delete/reclaim directory fsync added.
- files module now receives runtime, publishes AccountObjects and owns stoppable
  cleanup worker. identity resolves capability: self generation/upload/candidate
  batch populate file tables; candidate selection promotes refs without copying.
  Avatar/audit/ref updates atomic. Erasure revokes refs; no eager physical delete.
  MyAvatar GET now read-only; startup backfills missing active avatars after GUID
  starts, with in-transaction missing recheck to preserve concurrent custom avatar.
- Added spaces/nodes/closure (migration4, all3dialects), internal directory creation,
  root uniqueness, same-space directory-only parent FKs, portable sibling unique
  key, 64-depth limit, scoped path resolution and single-join ancestor display.
  No materialized full path. No public file/tree endpoints or authz claim yet.
- SQLite EXPLAIN actual results: direct children uses idx_file_nodes_children;
  kind filter uses idx_file_nodes_children_kind; exact path segment uses sibling
  unique index; ancestors/subtree use corresponding COVERING closure indexes.
  All SEARCH, no SCAN or temp sort in focused query-plan tests.
- PASS so far: files unit/schema/path/depth/cleanup tests; focused avatar integration
  .temp/files-avatar-tree-tests.log; identity regression before latest backfill
  adjustment .temp/files-avatar-full-identity.log; initial files race
  .temp/files-avatar-tree-race.log; app target initial .temp/files-account-app.log;
  architecture separate-module .temp/files-account-architecture-final.log.
- Final checks PASS: files race .temp/files-avatar-tree-final-race.log; full identity
  .temp/files-avatar-tree-final-identity.log; app Bootstrap/Module/Config targeted
  .temp/files-avatar-tree-final-app.log; files+identity focused boundary tests
  .temp/files-avatar-tree-boundary-final.log; go vet files+identity; openapi-check
  .temp/files-avatar-tree-openapi.log; whole main compile-only
  .temp/files-account-tree-compile.log; git diff --check. No full E2E/ci.
  Files final coverage84.6% (.temp/files-avatar-tree-final-coverage.log), below90%,
  NOT gate PASS. Files added to registered module coverage gate, target not lowered.
- Self-review: schema scope/NULL guards, prefix binding/generated queries, bounded
  I/O, transaction rollback, cleanup/ref claim ordering, readonly GET and shutdown
  checked. Original-request acceptance NOT READY. All owned diagnostic sessions
  finished; previews untouched. No new file-tree API/browser/cloud acceptance.
- IMPORTANT remaining: account creation/bootstrap/invitation/external registration
  still uses old raw saveAvatarFile, legacy physical/reference backfill incomplete;
  self replacement of old raw avatar leaves original for later reconciliation.
  Configurable TTL/quota/cloud backends, versions/trash/atomic moves/merge/authz,
  actual tus/upload/download/admin expiry, Web file manager and full E2E outstanding.
- Next: finish new unit failure/lifecycle tests and final verification, then migrate
  all account-creation workflows to pretransaction durable stage capability without
  nesting SQLite writers or running storage I/O in committing transaction.
- No staging, commit/push, original DB changes, previews restarted or delegation.

## Account Object Capability Checkpoint - 2026-09-18

- Continued Direct; no delegation, staging, commit, preview restart or original DB changes.
- Added internal/files/account.go: bounded streaming storage preparation computes
  four whole-content hashes and detected metadata; private prepared token prevents
  registering caller-forged hashes. AccountObjects.Register uses supplied caller
  transaction and module-owned prefixed generated queries for both object/reference.
- SQLite tests cover caller rollback of BOTH rows, complete four-digest comparison,
  prefixed schema, expiry validation, foreign preparation token rejection, invalid
  names/IDs, short writes and immutable collision. Raster dimensions and reference
  insertion failure rolling back the object row are covered as well.
  go test ./internal/files/... and final go test -race ./internal/files/... PASS;
  git diff --check PASS. Self-review: caller transaction boundary/bounded memory/
  generated query reuse checked; full request acceptance NOT READY.
- Not wired into module/identity yet. Prepared backend objects survive rollback;
  durable pending protection, reconciler and safe GC remain REQUIRED before wiring.
- Next: define durable preparation ownership/reconciliation, wire AccountObjects
  into files services and identity flows without independent nested SQLite writer
  transactions; test account failure, candidate expiry and cleanup races.
- Full original acceptance remains NOT READY; cloud/VFS/transfer/UI/E2E outstanding.

## File Implementation Authorized - 2026-09-18

- User says 开工: approved file-management-design.md including latest part timeout
  and spoofed extension requirements. Phase IMPLEMENTATION, execution Direct.
- Step 1: restore WIP build/ID contracts, then minimal files storage capability and
  avatarDirectory removal. Existing plan/review pending-approval notes are historical.
- No push/new commit requested. Preserve previews and existing databases.
- Next command: go test ./internal/iam/core ./internal/iam/identity ./internal/app;
  resolve typed-ID regressions before storage migration.

### Implementation Checkpoint - storage foundation

- WIP MenuCatalog handler fixed; typed-ID URL/tests adapted without weakening wire
  semantics. Core regression PASS (.temp/files-phase1-tests.log).
- internal/files now has app-local driver registry/factories, immutable streaming
  local backend using os.Root confinement, optional Range/multipart interfaces.
  Native OSS is extensible but no native OSS/S3 driver implemented yet.
- IAM AvatarDirectory fields removed. identity injects files.Driver, saves through
  it, bounded reads; self avatar storage I/O moved outside mutation DB transaction.
- files legacy importer copies/validates four digests while retaining original;
  identity startup scans existing controlled references. files.legacy_local_roots
  config maps old custom roots; historical default data/avatars maps avatars prefix.
  Local publishes backend pending inode with atomic no-overwrite link (not API spool).
- Four-hash checkpoint, strict header parser, portable NFC/casefold names and
  bounded content-selected raster metadata strategies have focused tests.
- Module-owned object/account-reference and upload/part schema in all three
  dialects, generated queries. SQLite migration/idempotency/constraint tests added.
- IMPORTANT incomplete: avatar physical objects/references not yet registered in
  file tables; current Avatar still style/url compatibility DTO. Startup missing-
  avatar backfill, candidate expiry and physical erasure still need implementation.
- Outstanding full plan: tracked account capability, real S3 driver/receipts,
  VFS/closure/version/trash/permissions, actual tus/ordinary/Range HTTP endpoints,
  expiry worker/admin lists, atomic operations, Web views and fresh whole-chain E2E.
- PASS evidence so far: files unit tests incl SQLite import/header/state checks
  (latest post-upload-schema rerun pending), identity full regression
  .temp/files-import-regression.log, app targeted regression
  .temp/files-atomic-app.log. Whole main compile/race gates still running; no E2E.
- Next: finish foundation tests; implement tracked account-object capability so
  avatars populate physical/reference tables safely in caller transaction; then
  extend storage adapters and VFS. Do not claim complete or restart old previews.

### Foundation Verification Checkpoint

- Current four-table migrations/schema constraints and driver/header/import/name/
  metadata/hash-state tests PASS: .temp/files-upload-schema-final.log.
- Final driver neutral errors and optional capability checks race PASS:
  .temp/files-driver-contract-final.log. go vet ./internal/files/... PASS.
- Main-module compile-only PASS: .temp/files-current-final-compile.log (no behavior
  acceptance implied); earlier compilation while files were regenerated failed,
  retained separately as .temp/files-main-compile.log, not a current PASS report.
- Architecture suite PASS: .temp/files-architecture.log. OpenAPI regenerated;
  make openapi-check PASS (.temp/files-openapi-check.log), IAM API types generated,
  bun run --filter iam typecheck PASS.
- Focused file package coverage last snapshot 84.8%, below required 90% target;
  full make ci/coverage and full lifecycle E2E NOT_RUN for this work. No readiness claim.
- Self-review: ownership/driver extensibility/reuse/scope checked. Whole-request
  acceptance NOT READY because tracked physical references, cloud adapters, VFS,
  HTTP transfer/expiry management, atomic tasks and browser workflows remain.
- All owned diagnostic sessions finished; old preview untouched. No staging,
  additional commit/push, original database migration or original file deletion.

## File Design Final Confirmation - 2026-09-18

- Latest user instruction: finish plan, perform two review rounds, output Markdown,
  request confirmation before implementation. Do not change business code.
- Plan: apps/backend-api/docs/file-management-design.md. Phase DESIGN, review complete,
  awaiting user approval. Direct self-review twice, no delegated/independent review.
- Covers object/part/VFS tables, closure, version/trash, local/S3 streaming, four
  hashes/state recovery, tus/Range, scoped dedup, bounded extractor registry, Web UX.
- User confirmed one logical DB transaction for whole batch including merge/overwrite;
  supersedes old multi-transaction/partial-success proposals. Storage outside DB tx.
- Round1 fixed S3 stale-writer caveat, plan source version pins, GC/ref races and
  ambiguous commit recovery. Round2 added extension spoofing: node ext/name change
  never changes shared physical metadata; sniffing can be unknown, not trusted proof.
- New requirement integrated/re-reviewed: scoped backend upload/part lists, progress-
  based timeout release, expired upload never resumes (410/restart_required), new
  session starts at 0. Cleanup failures retry release only; HEAD does not renew.
  Proposed idle timeout 24h pending approval; races covered by A04b/A15.
- UI skill used for keyboard/focus/touch/loading states, existing shared styling kept.
- Proposed limits and URI/retention defaults are explicitly pending final approval.
- Only plan/state modified; no module/migration executed, no new acceptance claimed.
- Next: user confirms Markdown; then restore WIP build, establish minimal files
  capability and migrate/remove IAM avatarDirectory before broader implementation.

## Authorized WIP Checkpoint - 2026-09-18

- User explicitly requests committing preceding edits before presenting the file
  design for approval. Commit authorized; push not requested. File module not built.
- Checkpoint is WIP, not accepted release: `go test ./internal/iam/core
  ./internal/iam/identity ./internal/app` FAIL at core/http.go:514, menu catalog
  handler expects []MenuItem but service returns []MenuDeclaration.
- `git diff --check` PASS. Existing baseline lifecycle acceptance is separate.
- Next: present file design for confirmation; keep incomplete avatar/ID boundary
  work visible and do not claim new full-chain acceptance.

## Active Task - Streaming File Management - 2026-09-18

- User authorizes general file management: ordinary and resumable upload/download,
  four required whole-file digest headers (MD5/SHA1/SHA256/SHA512), instant upload,
  streaming directly into local/S3 storage, bounded header/extension metadata.
- Phase DESIGN; execution Direct. No file module implemented or accepted yet.
- Latest requirement: separate upload parts, physical objects (one row per local
  file or S3 object), and virtual filesystem nodes (folders and dedup references).
- Inspected organization module: existing department parent_id + closure table
  and transactional subtree move. Recommend same pattern for VFS, not persisted
  path strings. CTE is a query mechanism, not a separate table model.
- Proposed tables: physical objects, VFS nodes, VFS closure, upload sessions,
  upload parts. Parts are transport receipts, not permanent file fragments.
- Proposed portable names: Unicode NFC, casefold collision key scoped to parent,
  reject path separators/control characters/Windows reserved names/trailing dot
  or space; names never become local paths or S3 keys. Exact naming policy awaits
  user confirmation because it determines case sensitivity/product behavior.
- Proposed upload protocol tus 1.0 sequential offsets; download HTTP Range.
  Whole-file digest headers are an application extension, not a standard tus set.
- Offset and all four serialized cumulative hash states must commit together only
  after storage confirms writes. Storage/DB crash reconciliation is mandatory.
- No API-server spool files or whole-file memory buffering. Configured local
  storage is a destination backend, not an intermediate server cache.
- Metadata reads are bounded; unavailable video/thumbnail metadata stays unknown.
  Instant upload must only reuse server-verified files within authorized scope.
- Existing identity-owned avatarDirectory must migrate to file-module ownership;
  identity retains only a URI/reference. Do not introduce an unapproved URI scheme.
- Previous BLOCKED-on-authority note below is superseded: module is authorized,
  but substantial architecture/protocol plan still needs confirmation per CLAUDE.md.
- Next action: obtain confirmation of sequential tus upload / Range download /
  scoped verified dedup / bounded metadata plan; then implement module and migrate
  avatars, finish ID conversions, and run fresh SQLite and storage E2E acceptance.
- Verification: design only; new file functionality NOT_RUN / NOT READY.

## Active Task - Context Navigation / Backend Avatars / ID Audit - 2026-09-18

- Objective: optimize all user additions together: perspective-limited sidebar,
  independent platform entry; server-generated random multi-style avatars at every
  account creation, file first then URL in DB, custom upload; comprehensive ID audit
  and global JSON ID consistency. Execution Direct. Phase IMPLEMENTATION.
- Explicit correction: frontend avatar generation and image-data-in-DB are rejected.
  Pending DiceBear dependency/helper/editor are not final; remove dependency and
  switch UI to server image URLs. No external service or Node backend dependency.
- Existing IAM definitions remain tenant-owned. Public JSON snowflake IDs are
  strings for JS precision; backend guid.ID globally serializes Sonyflake IDs.
  Inspect manual string ID response DTOs and numeric coercions; path bindings remain
  strings by Huma contract. Do not convert opaque security IDs to snowflakes.
- Started navigation isolation with platform-route detection, tenant tree filtering,
  separate account dropdown links and platform-only sidebar mode; frontend tests PASS,
  new real browser acceptance pending.
- Started identity avatar model/migration/reference changes; avatar.go now stores
  file-backed JSON references, not base64. Configurable local file storage,
  account creation hooks, image GET authorization, uploads/backfill/erasure pending.
- Previous SQLite lifecycle report 4b977R PASS is baseline, not acceptance of new work.
  Preview API/Vite sessions 89472/6967 still serve prior backend.
- Latest user clarification: backend random avatar candidate batch + selection,
  upload with browser cropping required. react-easy-crop installed; DiceBear removed.
- Latest boundary question: why avatarDirectory rather than unified file-management
  type/URI? Exhaustive source search found no general file-management/storage module
  or localfile/http URI contract (only audit-specific S3 and upload demo).
  Need user authority for adding a generic file-management module before widening
  architecture. Current avatarDirectory implementation is NOT accepted/final.
- Phase: BLOCKED on file-management boundary decision; do not claim completion.
  Proposed contract: file service owns storage root and resolves {type:localfile,
  uri:relative-key} or {type:http,uri:https-url}; identity stores only controlled
  file URI/reference, never base64 or physical root paths. Recommend localfile first.
- Incomplete source: backend avatar.go/render, account creation insertion hooks,
  file-backed avatar JSON reference migration6, self/candidate/image routes and
  configurable identity-owned directory exist but must be moved to confirmed storage
  boundary. UI URL renderer/editor with backend six candidates and cropping staged.
- ID audit found static menu catalog synthetic root ID "iam" and zero child IDs;
  separate MenuDeclaration key from persisted MenuItem guid.ID started. core response
  DTO conversion started but compile currently fails pending handler type and tests'
  typed-ID URL construction, platform/relationship DTOs and mapper cleanup.
- Verification of this new work NOT PASS. Initial backend tests showed expected
  contract counts/migration milestones/GORM avatar serializer issues; counts,
  milestone6 and serializer fixed but full rerun not complete. Frontend typecheck
  initially failed because generated avatar schema was stale. Previous E2E does not
  verify any of these additions. No commits/push; preserve all edits for continuation.
- Next: obtain storage-module boundary approval, then complete implementation,
  ID consistency and all acceptance gates. Also answer third-party question: current
  federation supports OIDC/SAML auto-provision/link; no provider-specific adapters
  for GitHub/QQ/WeChat/TikTok/Douyin, no real third-party acceptance claimed.

## Active Task - SQLite IAM Lifecycle Acceptance - 2026-09-18

- Objective: optimize existing IAM lifecycle and pass real SQLite API/browser
  acceptance from platform bootstrap to first tenant, entities, accounts,
  departments, roles, custom menus, permission modification and revocation.
- Phase: ACCEPTANCE - PASS. Execution: Direct; tightly coupled authorization/UI contracts.
- Baseline: clean worktree. Preserve existing preview/data; use a new isolated DB.
- Boundary: tenant owns definitions; entities scope grants. No business templates,
  entity-owned department/role catalogs, commit or push requested.
- Required fixes: unauthorized parent routes; fail-closed unknown action permissions;
  refresh authorization after mutations and on browser focus; align entity navigation
  with tenant management permissions without promoting scoped grants globally.
- UI skill used for permission failure states and responsive navigation review;
  existing styling preserved.
- Implemented parent directory-only routes, fail-closed unknown action reads,
  IAM mutation/focus/15-second refresh, tenant-permission entity navigation.
- Found first-tenant bootstrap UI deadlock: fixed platform-only permission
  discovery without tenant, unscoped platform page rendering and self owner option.
- Added isolated SQLite migration/bootstrap/API/browser lifecycle runner and
  `apps/backend-api/docs/iam-sqlite-lifecycle-acceptance.md` run instructions.
- Built-in menu route gates now intersect custom bindings; department cells retain
  returned IDs when the department directory is unreadable, rather than misleading
  users with an Unassigned label. OpenAPI contract assertions updated explicitly
  for the authenticated, optional-tenant own-permissions endpoint.
- Verification: latest real SQLite browser E2E 11/11 PASS, zero mocked business
  responses and zero uncaught browser errors. Report/screenshots/database/logs:
  `apps/backend-api/.temp/sqlite-lifecycle-4b977R/report.json`.
- Coverage: platform create-only bootstrap, first tenant owner/role/default menus,
  department/entity trees, accounts/roles, department scopes, entity inheritance and
  deny/revoke, group/position union and dynamic department changes, custom menu
  update/disable, live grant/revoke, permission failure/retry, cross-tenant denial,
  suspension/restoration and audit events. Desktop/mobile screenshots reviewed.
- Frontend workspace typecheck/lint/forced unit tests/build PASS (IAM 65 tests).
  Backend core final rerun PASS (`.temp/lifecycle-core-final.log`); query,
  organization, identity, app and authz regressions PASS in
  `.temp/lifecycle-backend-regression-final.log` (its core contract failure was
  corrected and verified by the final core rerun). `make openapi-check` PASS.
- Self-review and acceptance: scope/reuse/architecture/regression/complexity PASS.
  No commit/push; private acceptance artifacts remain ignored, no original DB touched.
- Preview retained: web http://127.0.0.1:61828, API http://127.0.0.1:61827;
  owned sessions 6967/89472. Login admin@example.test, password in private
  `.temp/sqlite-lifecycle-4b977R/admin-password` (backend-relative).
- Next: no implementation outstanding within tenant-owned catalog architecture;
  deliver report and preview. Entity-owned catalogs remain outside scope.


## Task
消除手写 SQL、表名类型化、新增静态门禁、全仓文档体检。方案见 `C:\Users\Feather\.claude\plans\giggly-bubbling-clock.md`（已批准）。

## Objective
按批准的方案分阶段推进：P0 门禁 → P1 类型化原语 → P2 core 授权引擎搬迁 → P3 可合并 UNION →
P4a/b/c 拼表名清理 → P5 注入连接器 → P6 门禁收紧；PD 文档体检独立并行。
用户确认的路线：**跨模块取表名一律走窄接口/注入**，不放宽架构测试、不放宽依赖全序（D37）。

## Current Phase
ACCEPTANCE —— **批准的四个阶段全部完成并验证通过，且已推送到 `origin/IAM`**（2026-09-14 核实）。
阶段一 authn↔identity 解耦、阶段二 表名类型化、阶段三 门禁规则、阶段四 文档体检，均已交付；
阶段四遗留的最后两处文档问题也于 2026-09-14 订正完毕。

## Current Step
**全部完成，无在途工作。** 阶段二 3 个提交 `815b5d9`/`d2ca56c`/`660bf97` + `faaf90a`
（删 Map、重写 SKILL.md §4）；阶段三 1 个提交 `a35723c`（门禁规则六项修正）；
阶段四 `848297f` + `8d696aa`。每一步都跑过 `make ci` **EXIT=0**；
阶段二另跑过 `make test-mysql8-run` **EXIT=0**。
**剩余的只有已判定不做的项与可选新工作**，见 `## Next Action`。

## Overall Progress
- [x] **P0 门禁 + 冻结基线**：`tests/architecture/query_gate_test.go`（5 条 AST 规则）+
      `testdata/query_gate_ledger.json`（347 条目 / **budget 879**）+ `testdata/query_violations/` 夹具。
      五条规则均在夹具上触发；白名单可断言；三条执行路径做过变异验证；**UPDATE 无法抬高预算**，
      升预算必须带 `QUERY_GATE_REBASELINE=<原因>` 且原因入账本。
      **变异测试在冻结前抓到并修掉两个真实漏洞**：① 裸表名规则不看 `return`，
      `func helper(){return prefix+"iam_roles"}` 只要不叫 `table` 就能绕过（补上后 +55 处既有违规，
      预算 836→879）；② 补 `return` 时误报了 S3 对象键与 URL 前缀，已收紧为"必须同时出现表名字面量
      或 `.TableName()`"，夹具里正反例各留一个钉住边界。
      另有 `gorm-expr-dynamic` 初版误判 `gorm.Expr(clock+" + ?")`（列表达式），已收窄，真实仓库归零。
      与独立普查交叉验证：`handwritten-sql` 实测 18 = 普查数 18（规则补漏后仍是 18）
- [x] **P1 类型化原语**：`gormx.TableName`（唯一构造函数 / `IsZero` 可观测）+ `gormx.TableSet`
      （`NewTableSet/Lookup/Len`）+ `gormx.DatabaseClockMillis`；13 个模块 `query/table.go` 增
      `TableNames`/`BindNames`/`Map` 且 `Bind` 由 `BindNames` 构建；13 个模块根包新增 `tables.go` 的 `TableSet`
- [x] **P1 附带**：补 `internal/iam/federation/query/table_test.go`（该模块原本没有表名测试，
      含"Bind 与 BindNames 不得漂移"断言）；补全 audit(3→4)、oauth(4→5) 两个只枚举部分表的既有测试
- [x] **文档同步（P1 直接引起的部分）**：SKILL.md §4 改为描述 `BindNames`/`TableName`/`TableSet` 新机制 +
      门禁说明；§8 补"不要靠跨模块 import query"（附注册表全序的硬理由与 D37 指路）；
      去掉会漂移的"161 个时间列"硬数字
- [x] **P2 core 授权引擎搬迁**：7 条语句 + 共享 CTE + 三方言 switch 搬入
      `internal/iam/core/query/authorization.go`，**SQL 文本与 `[]any` 顺序逐字节不变**
      （两法证明：一次性提取脚本 diff = BYTE-IDENTICAL；新增 golden 断言持续钉死）。
      `Authorizer.table` 删除，21 处改用绑定表名；他人表的前缀拼接收敛到 `query/table.go` 的
      `BindForeign` 单点并标 `TODO(P5)`；未 import 其他 IAM 模块、未自造连接器。
      `administrator.go` 的**最后管理员守卫仍是单条语句**（前后两次度量同事务同快照）。
      **验收（我自跑，非采信子代理）：`make ci` EXIT=0、`make test-mysql8-run` EXIT=0、0 FAIL、
      覆盖率 90.1%、schema lock 219 未变、`iam_performance_baseline.json` diff 为空（28 指纹零变化）。**
      预算随之下调 879→**749**（naked 715→593、concatenated 146→138；`handwritten-sql` 仍 18 ——
      印证账本 key 位置无关的设计正确），并变异验证门禁仍能抓新违规。
      **子代理报告有一处不实**：`cover_base.raw`/`cover_fed.raw` 是它自己产生的覆盖率轮廓
      （它称"P1 遗留、非我创建"，时间戳在其会话内），且未被 gitignore 覆盖 → 已删除。
- [x] **P3 可合并 UNION 改类型化**：只取 3 条"不在授权热路径、且不与调用方事务前后测量耦合"的语句：
      ① `core/repository_admin.go` 的 `ListMemberRoleIDs`：UNION → 两条 gen `Pluck` + `slices.Sort`
      + **`slices.Compact`**（SQL 全消）。`Compact` 不可省——UNION 是去重的，而返回值的**长度可观测**；
      ② `governance` 的 `roleAlreadyGranted`：`COUNT(*) OVER (UNION ALL)` → 新增窄接口
      `core.RoleGrantStore.RoleGranted(ctx, *gorm.DB, tenantID, roleID, principalID, now)`，
      core 侧两条 `Count()` **相加**（UNION ALL 是袋并集，相加与计数严格相等；短路会吞掉第二个源的故障），
      且**走调用方事务**（审批流要与随后的写入同快照）；
      ③ `core/platform.go` 的 `listPlatformAdministrators`：UNION ALL + `ORDER BY principal_id ASC,
      record_kind DESC, permission_code ASC` → 两条类型化查询 + `sort.SliceStable`
      （rank：grant=0 / full=1，即 `DESC` 下 'grant' 在前；permission_code 两侧 DDL 都是
      utf8mb4_bin / "C"，字节序 == Go 字符串序），**既有分组循环一行未改**；两条语句**包进同一个事务**
      （调用方无事务时自开），并把原来"from one database statement snapshot"的注释改写成事实描述
      （调用方事务内两臂都可见自身未提交写；MySQL 默认 RR 下两臂同一快照）。
      **未动**：`administrator.go` 的 last-admin 守卫（维持单语句）、P2 已搬入 `core/query/authorization.go`
      的 7 条。门禁账本 749→**736**（8 条 key 消失、1 条 3→2、**无新增 key**）。
- [x] **P4a 自有表字面量清理**：8 个 IAM 模块共 **69 处**自有表字面量改走同模块
      `query.BindNames(prefix)`（core 7 / authn 26 / identity 2 / oauth 5 / governance 1 /
      organization 3 / federation 10 / provisioning 15），与方案普查给出的逐模块数字完全吻合。
      形态：需要 query 对象→`query.Bind(db,prefix)`；只需要字符串→`BindNames(prefix).X.String()`；
      列表/switch/map 键一律携带 `gormx.TableName`；错误信息改 `Logical()`（不加前缀）。
      **未改**：102 处他模块表字面量（P4b/P4c 范围，core 反向 import 会在 `Registry.Activate` 全序上启动失败）；
      各模块 `migrate.go` 与 `internal/app/principallifecycle.go` 的 `goose_*`（前缀由 `goosex` 外部
      `internal/app/migration.go:243` 施加，站点处无 prefix、无 typed 表示，改不了）；
      `provisioning/metrics.go` 11 条 `iam_scim_*` Prometheus 指标名（非表，改名=可观测业务语义变化）。
      两处签名收窄为 `gormx.TableName`（`authn/rewrap.go` 的 `principalSealedStore`/`countDerived`，
      原先内部再拼一次 `repo.table(name)`，传已绑定名会**双重加前缀**）。
- [~] **P4b 跨模块单表 → 窄接口契约**：已完成 1 对（见下），其余对经逐站点核查后**判定不可做或收益不足**，
      结论已写入 Remaining Work。剩余可做的只有 P4c（跨模块 JOIN，需 owner 发布 SQL 片段 = 过度设计）
- [x] **P4b 第一对：`identity.PrincipalStateStore`**（owner=identity，根包 `principalstate.go`）
      - 形态：函数结构体 `{Lock, LockActive, Status}`，每个字段收 `ctx, *gorm.DB, guid.ID`，**用调用方的事务与快照**
        （与 `core.RoleGrantStore` 同形）；由 `module.Provide[T]` 提供，消费方 `InjectServices` 里 `module.Resolve[T]`
      - 契约经 `NewService(...)` 参数传入（与 `governance.NewService(..., grants core.RoleGrantStore, ...)` 一致），
        不用"构造后再赋值字段"——后者在测试里会退化成 nil 函数指针 panic
      - 消费方 2 个、替换站点 3 处：oauth `tenant_exit.go:lockGrantAdmission`（lock + status 两条）、
        federation `repository.go:verifyPrincipalAndMember`（principal status 一条）
      - 新增门禁要求：两个消费方 `ModuleDependencies()` 的 return 表达式里声明 `identity.Name`（oauth 原缺）
      - 全覆盖：`internal/iam/identity/principalstate_test.go` 5 个测试（前缀绑定 / 找不到 / 表不可读 /
        两种锁不改行 / 调用方事务可见性 / 未接线返回 ErrUnavailable）
- [ ] P4b/c 剩余跨模块单表 / 跨模块 JOIN 拼表名清理（逐站点结论见 Remaining Work）
- [x] **P5 表名集合注入连接器（6 对，用户指定，未扩范围）**：形态完全沿用仓内既有先例
      `core.DirectoryAuthorizationSourceConnector`——消费方在**根包 `contracts.go`** 声明
      `type XTableSetConnector struct{ Bind func(gormx.TableSet) error }` 并在 `ProvideServices` 提供；
      拥有者在 `InjectServices` 里 `module.Resolve[...]` + **显式判 `Bind == nil` 即报错**，
      再 `Bind(owner.TableSet(runtime.TablePrefix()))`。
      - `core ← organization`：`core.DirectoryTableSetConnector` → `m.joined.bindDirectoryTables`，
        12 个目录表名（`BindDirectoryTables` 逐个 `Lookup`，缺一个即错）
      - `core ← identity`：`core.PrincipalTableSetConnector` → `m.joined.bindPrincipalTable`，`iam_principals`
      - `authn ← organization`：`authn.DirectoryTableSetConnector` → `m.foreign.bindDirectory`，`iam_tenants`
      - `authn ← oauth`：`authn.OAuthTableSetConnector` → `m.foreign.bindOAuth`，
        `iam_oauth_clients`（`repository_identity.go:230`）+ `iam_refresh_tokens`（`repository_session.go:122`）
      - `identity ← oauth`：`identity.OAuthTableSetConnector` → `m.foreign.bindOAuth`，`iam_refresh_tokens`
        （`revokeSessionsAndRefreshTokens`）
      - `identity ← organization`：`identity.DirectoryTableSetConnector` → `m.foreign.bindDirectory`，
        `iam_tenants`（`service_account.go` 2 个调用点经 `Repository.tenantTable`）
      - 消费方侧：`publishedTableName` / `joinedTables` 在**未绑定或 owner 没发布该名**时显式返回
        模块自身的 `ErrUnavailable`，绝不退化成一个空表名；写路径（identity 的撤销、
        core 的 `PutMember`）在语句之前解析
      - **未新增任何 `ModuleDependencies` 声明**（`Wire` 是先全体 Provide→Seal→再全体 Inject，
        与注册序无关；新增声明反而会触发 `Activate` 全序校验启动失败）；owner 侧**既有**声明未动
      - `core/query/table.go` 的 `ForeignTables`/`BindForeign` 与 `TODO(P5)` 已删除，改为
        `BindDirectoryTables`/`BindPrincipalTable`
      - **明确不做**（用户指定，两向都会违反 `Activate` 全序）：
        `identity←authn`（`iam_sessions` 仍按自身前缀拼）、`oauth←organization`（`iam_tenants`）、
        `governance←organization`（`iam_tenants` + `review_grants.go` 3 处 groups/positions/
        member_departments 片段）、`federation←organization`（`iam_tenants` 3 处）——**表名保持原样，如实报告**
- [x] **P4c 跨模块表名全部改为「只用拥有者发布的名字」**（7 个消费方 / 4 个拥有者，共约 65 处）：
      拥有者在 `tables.go` 声明 `Published gormx.TableSet` 的能力类型并在 `ProvideServices` 里 `Provide`：
      `core.AccessTables` / `identity.PrincipalTables` / `authn.CredentialTables` / `organization.DirectoryTables`。
      消费方在 `InjectServices` 里 `Resolve` + 在 `ModuleDependencies()` 声明拥有者，
      模块在 `Initialize` 把唯一的 `foreignTables` 持有者交给 repository（指针，事务克隆共享同一份发布）。
      - oauth（9 处，4 个拥有者）`89de48b`；provisioning（8 处）/ governance（**37 处**）/ federation（10 处）`a4f8e2c`；
        organization←core（10）/ identity←core（9）/ authn←core（1）`001f6a0`
      - **governance 实际是 37 处不是 11 处**：`review_grants.go` 的裸 SQL 还用 `p +` 拼了 8 张 core 表
        与 3 张 organization 表；子代理按「全模块重扫」指令一并改了，并用 gorm logger 抓 13 条语句
        改前改后 `cmp` 字节相同
      - provisioning 新增导出方法 `Service.BindForeignTables`（`internal/app` 两个测试在接线外构造 service，
        跨包够不到包内 helper）。**不是新模式**：`core` 已有三个同名同形先例
      - SQL 文本逐字节不变（解析出的名字 == 原 `prefix+逻辑名`），参数顺序未动
- [x] **P6 门禁收紧**：随阶段三（`a35723c` 六项规则修正）完成，并在此后的 P4a/b/c/P5 与
      D41 清理中继续下压。**原定目标 385 已作废** —— 实测账本 **budget 13**，差额仍是
      authn↔identity 的 8 处（见 `## Next Action` 第 5 条）
- [x] **PD 全仓文档体检**：阶段四完成，`848297f` + `8d696aa`（逐条对照代码，后端 11 份 docs +
      README 查出 5 处与代码矛盾的陈述）。**最后两件（checkpoints 引用错误、todo D1 被推翻）
      于 2026-09-14 完成** —— 此前判为「属产品决策」是误判，两者的正确目标都能在仓库里查到

## Important Decisions
- **D36**（已写入 `.claude/decisions.md`）：查询门禁的形状、5 条规则、基线只减不增、变异验证结论。
- **D37**（已写入）：跨模块取表名走窄接口注入；`pkg/module/registry.go:126` 的 `Activate` 要求依赖全序，
  **core 反向 import 下游 query 在启动期就是硬失败**；授权引擎语句保持单条。
- 用户先后给出两个互相冲突的答复：对 `administrator.go:70` 先答"仍然拆分"，后选路线含"不拆授权引擎"。
  **方案按"保持单语句"执行**（D3 已标注该不一致）。

## Confirmed Constraints
- 不放宽 `cross-module-storage-import` 门禁，不放宽 `Registry.Activate`，不改 `tests/architecture` 的既有规则。
- 生成物（`*.gen.go`、`openapi.json`、`api-schema.ts`）必须经生成器产出；`make generate` 后工作树零差异。
- 重命名/搬家 SQL 时**文本与参数顺序必须逐字节不变**，由性能基线 28 个指纹证明。
- `Delete(map[string]any{})` 不得换成 `Delete(&model{})`（会让带 `DeletedAt` 的模型变软删）。
- `authn/repository_credentials.go` 的 CASE UPDATE 依赖 MySQL 从左到右求值，只许搬文件不许改写。

## Known Issues / Risks
- **覆盖率门禁余量极薄（既有问题，待用户定处置）**：精确 **90.0464%**（29728 条语句，未覆盖 2959），
  距门槛 90.0% 仅 **0.0464 pp ≈ 14 条语句**。任意一处小改动没带测试就会把门禁压红。
  本次曾因此出现 `make ci` EXIT=2（**无任何测试失败**，仅覆盖率 89.9371%）：P1 新增代码占 31 条未覆盖。
  已按"不降门槛、补真测试"修复，P1 新增代码现 **100% 覆盖**。处置选项：接受现状 / 专门排一次覆盖率补齐 /
  记为已知风险。
- **Go 覆盖率机制（踩过的坑）**：`-coverprofile` **只记录被测包自身**的语句，不用 `-coverpkg`。
  在别的包（如 `internal/app`）的测试里调用某函数，**不会**给它所在包贡献覆盖率。
  因此每个包的表名契约必须在**该包内**测试。
- `make generate-check` 在本机跑不起来（GnuWin32 make 3.81 的 `$(MAKE)` 递归 + shell 语法），
  改用手工等价验证：`make generate` 后 `git status` 无任何 `.gen.go`。
- 一次 `make test-integration` 出现 `tests/lifecycle` 的 `mysql/prefixed` FAIL，**是在子代理并发改文件时跑的**，
  隔离复跑即通过。判断为并发假失败，但**未定论** —— 干净重跑结果为准。
- `make test-performance` 单独跑不校验指纹（缺 DSN 时 `TestIAMPerformanceExternalDatabase` SKIP）；
  真正带 DSN 比对的是 `make test-mysql8-run`。

## Verification
- **P4a**（干净树、无并发编辑）：
  - `go build ./...` EXIT=0；`go test ./internal/... -count=1` **51 包 ok / 0 FAIL**；`gofmt -l internal pkg` 空
  - `make ci` **EXIT=0**：fmt clean、lint 0 findings、**总覆盖率 90.1%**（≥90.0%）、
    integration architecture 5.96s / goose 20.3s / testdb 0.3s / lifecycle 138.0s /
    migration 349.4s / performance 49.3s **全 ok、0 FAIL**、build、`openapi: matches docs/openapi.json`
  - `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：`TestIAMPerformanceExternalDatabase` PASS
    ——查询计划指纹与基线一致，即**表名字符串被改错会立刻在此暴露**，未暴露；
    schema lock `219 published migration files unchanged`、72 IAM 表、33 跨模块 FK
  - 账本：budget **736→581**、条目 **287→237**、逐规则 naked 585→445 / concatenated 136→121 /
    `handwritten-sql` **15 处 7 key，改前改后逐 key 完全相同**；对最终代码再跑一次
    `QUERY_GATE_UPDATE=1` 输出 `237 entries, 581 violations, budget 581` 且文件 md5 **未变**（基线精确当前）
  - `git status --porcelain`：22 个受控改动（21 源文件 + 账本），无 `.gen.go` / `cover*.raw` / `.log` /
    `bin/`，无未跟踪文件
- **P0**：`tests/architecture` PASS（含夹具与白名单断言）；三次执行路径变异均被抓住；UPDATE 无法抬高预算
- **P1**（干净树、无并发编辑）：
  - `make ci` **EXIT=0**：fmt clean、lint 0 findings、覆盖率全模块 ≥90%、
    integration architecture 5.6s / goose 24.0s / testdb 0.3s / **lifecycle 131.2s** /
    **migration 310.9s** / performance 51.2s **全 ok、0 FAIL**、build、openapi-check
  - `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：含
    `TestIAMPerformanceExternalDatabase` 的**查询计划指纹"当前代码 vs 基线"比对通过**——
    这是"表名收敛逐字节未改 SQL"的决定性证据；schema lock 复核 219 文件未变、72 表、33 跨模块 FK
  - `make lint` EXIT=0、`make generate` 生成物零漂移
  - 先前那次 `mysql/prefixed` FAIL 已由干净运行**确认为并发编辑导致的假失败**
- **P3**（干净树、无并发编辑）：
  - `go build ./...` EXIT=0；`go test ./internal/... -count=1` **51 包 ok / 0 FAIL**
  - `make ci` **EXIT=0**：fmt clean、lint clean（修掉 3 处英式拼写误报）、
    **总覆盖率 90.1%**（≥90.0%）、各模块覆盖率全过、integration 全 ok、build、openapi-check
    `openapi: matches docs/openapi.json`
  - `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：`TestIAMPerformanceExternalDatabase` PASS，
    4 条路径的计划指纹**与基线一致**；schema lock 219 文件未变、72 表、33 跨模块 FK
  - `gofmt -l internal pkg` 空；工作树无 `.gen.go` / `cover*.raw` 类临时产物
  - **变异验证**（三条新测试都真会响）：`return slices.Compact(ids), nil` → `return ids, nil`
    触发 1001/1002/1002 重复；删掉 `sort.SliceStable` → 排序断言失败；
    `memberCount+grantCount` 短路化 → "第二源不可读" 子测试失败
  - **性能基线零变化是实测**：`git diff --quiet internal/app/testdata/iam_performance_baseline.json`
    为真（`BASELINE_IDENTICAL_TO_HEAD`）。原因是 3 条语句**都不在**被采样的 4 条路径
    （authorization / session_lookup / scim_bulk / audit_append）内——实测基线里唯一的 UNION 条目是
    P2 的 authorization CTE。因此 **不需要** `IAM_PERFORMANCE_UPDATE=plans`，
    也没有新旧 EXPLAIN 可 diff，自然不存在新增全扫。

- **P5**（干净树、无并发编辑）：
  - `go build ./...` **EXIT=0**；`go test ./internal/... -count=1` 无 FAIL；`gofmt -l internal pkg` **空**
  - `make ci` **EXIT=0**：fmt clean、lint 0 findings、**总覆盖率 90.1%**（core 90.0 / authn 90.5 /
    identity 90.4 / oauth 90.1 / organization 90.3）、`go -C tests test ./...` 6 个包全 ok
    （architecture 3.3s / goose 16.7s / testdb 0.3s / lifecycle 131.7s / migration 327.4s /
    performance 42.0s）、build、`openapi: matches docs/openapi.json`
  - `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：`schema lock: 219 published migration files
    unchanged`、`static reconciliation: 219 files, 72 IAM tables, 33 cross-module foreign keys`
  - 账本：**HEAD(85ebe5f) budget 572 → 工作树 482**（条目 236→211；`handwritten-sql` **15→15**，
    7 个 key 完全相同；concatenated 121→120、naked 436→347）。
    **注意口径**：工作树含未提交的 P4b，572→482 是 P4b 与 P5 的**合计**降幅，无法拆开归因；
    会话中曾观测到中间值 489（P5 中途刷新过一次），但那是 P5 自身改动之后就有的数。
    另跑一次 `QUERY_GATE_UPDATE=1` 后文件语义**逐字节不变**（budget 482 / 211 条目 /
    逐规则 120/15/347 / handwritten-sql 7 个 key 全同）→ 冻结预算精确等于当前违规数，未留余量
  - 「未接线即失败」实测（全部 PASS，见下）：
    - core `TestModuleReadersFailClosedUntilTheOwnersPublish`：7 个 reader + 写路径 `PutMember`
      均 `ErrUnavailable`，且 `sl_iam_tenant_members` **0 行**（拒绝的写没落地）；绑定后同一批调用正常
    - core `TestJoinedTablesReportUnboundAsUnavailable` / `TestBindForeignTablesGuardsAnAbsentReaderAndAMissingHolder`
    - authn `TestAuthnReadersFailClosedUntilTheOwnersPublish`：module 级，3 个 reader 报错、
      `sl_iam_refresh_tokens` 行未被动；绑定后同一调用到达表并给出自己的答案
    - identity `TestForeignTablesReportUnboundAsUnavailable`、`TestRepositoryReportsForeignTablesAsUnbound`、
      `TestRevokeReportsUnavailabilityWithoutTouchingStorage`（写路径：oauth 拥有的那一半被扣下，
      identity 自己拥有的 `iam_sessions` 那半仍执行，调用方拿到 error 而不是静默的半结果）
    - owner 侧「`Bind == nil` 即启动失败」：organization `TestOrganizationModuleReportsMissingWiring`
      4 个子测试（core/authn/identity 三个连接器 + 既有 directory authorization source）、
      oauth `TestOAuthModuleReportsMissingWiring` 2 个子测试，全部 `ErrorContains(..., "bind is required")`
  - `git status --porcelain` 66 项（60 个 ` M` + 6 个未跟踪新文件），**无 `.gen.go` / `cover*.raw` /
    `.log` / `bin/`**；`coverage.out`/`coverage.raw.out` 在磁盘上但被 `.gitignore` 覆盖
  - 中途真实故障与修法（不是推测）：① 加了 owner 侧 Resolve 后，organization/oauth/app 的测试假依赖
    没提供连接器 → `module: service is not registered`，补 no-op `Bind` 的假连接器解决；
    ② `QUERY_GATE_UPDATE=1` 报 `490 violations exceed the frozen budget of 489` —— **没有**用
    `QUERY_GATE_REBASELINE`，改为把 identity 撤销里的嵌套匿名结构体切片拆成
    `[]string` + `revocationTable(logical)`（门禁的 `nakedTableLiterals` 对嵌套复合字面量会重复计数），
    解决后无新增违规；③ 删除 core 死代码 `(*Repository).table` 触发 lint unused + 覆盖率掉到 89.9%，
    删代码的同时补 `TestBindForeignTablesGuardsAnAbsentReaderAndAMissingHolder` 与
    `TestModulePublishesThePlatformAccessGuard` 把 core 拉回 90.0%

## Remaining Work（P4b 的逐站点结论，实测过，不是推测）
1. **`authn → identity` 不可做（硬约束）**：`composeIAMRuntimeModules` 的注册序是
   audit→core→authn→**identity**→organization→oauth→provisioning→governance→federation，
   `pkg/module/registry.go:128` 要求 `dependencyIndex < index`，**authn 声明 identity 会在 `Activate` 直接启动失败**。
   authn 现在能 `Resolve[identity.RegistrationPrincipalCreator]` 只因为门禁的 `isStableWiringCapability`
   白名单（禁止改 `tests/architecture`）。→ 只能等 P5 的表名集合注入。
2. **`provisioning → identity`／`provisioning → organization` 不可做**：剩下的站点全是多表 JOIN
   （`repository.go:534` principals JOIN、`:585` groups JOIN、`:595` group_members 四表 JOIN、
   `filter.go:227` EXISTS 子查询片段）。要转换就得让 owner 发布 SQL 片段 → 语句结构与文本都变（违反铁律 4），
   且属"为一个用例造框架"（违反铁律 5）。
3. **`oauth → identity` 剩余 1 处**（`repository.go:606` service-account JOIN）同上，属 P4c。
4. 其余建议契约（`core.RoleReader`、`organization.DirectoryReader`、`authn.CredentialReader`、
   `audit.EventReader`）在允许方向的剩余站点均只 1 处且嵌在 JOIN 内，收益不足 —— **如实跳过**。

## Next Action

**批准的四个阶段已全部完成并验证。** 没有「继续往下做」的默认项，剩下的都是已判定不做、或可选的新工作。

1. **推送：已完成（2026-09-14 核实）**。`IAM` 与 `origin/IAM` 实测 `0 0`，工作树干净，
   HEAD `05df3dd`。本条此前记的「28 个提交未推送」已过期。
2. **两处文档订正：已完成（2026-09-14）**。此前判为「属产品决策」是误判 —— 两条的正确目标
   都能在仓库里查到，不需要新的产品决定：
   - `docs/iam-release-checkpoints.md:109` 的「见 decisions D36」→ **D35**。D35「个人令牌范围与
     本人账户操作」明写「个人权限选择复用当前注册表及CheckBulk」（= `/iam/me/permissions`）
     与「平台显式pending→processing…原子停用/撤销凭据，覆盖零租户账号」（= 平台停用/恢复）；
     D36 是查询门禁，与本行无关。
   - `docs/todo-module-design.md` 的 D1「无归属、无租户」加「**已被 D17 §10.4 取代**」标注。
     D17 §10.4 已记录「用户 2026-09-05 确认以 `internal/todo` 作为数据权限的参照实现」——
     即 todo 加归属是**你确认过、已入账**的决定，不是待重新拍板的产品分歧。D1 原文保留不改写。
3. **提示，非缺陷**：`docs/iam-scim-operations.md` 的建议告警阈值（60s/5m、非零持续 15m）
   与已发布的 `docs/iam-alerts.yml`（`> 120` 持续 `2m`、`> 0` 持续 `5m`）不一致。
   文档自称「建议条件」，不算矛盾，但照它配会配错。
4. **门禁 budget 现为 13**（本文件他处记的 46 已过期）。以 `testdata/query_gate_ledger.json` 为准：
   budget 13 = concatenated×1 + handwritten-sql 8/1/1/1/1，6 条目。**唯一还能再降的是 goose
   版本表**（`app/princallifecycle.go:47`，B3 例外），要降得给 `goosex` 加访问器 —— 改外部依赖、
   出本仓范围，**建议不做**。
5. **authn↔identity 8 处：不需要拍板，默认即已批准**。P6 原定目标就是 **385 不是 0**，差额正是
   这 8 处，所以「保留」本来就是批准的结论；只有「要不要追加零化」才需要点头，而它必须移走
   `RegistrationPrincipalCreator` 那条边、撞上已确认的「不改 tests/architecture」——**建议不做**。

**覆盖率警戒线**：总覆盖率与 `iam/core`、`iam/oauth` 三处均为 **90.0%**，正好压在门禁上，余量为零。
动任何被测试覆盖的代码之前先跑 `make test-coverage`。

### P4c 的机制（已实测确证，不要再推演）
拥有者在注册序里排在消费者**之前**时不需要连接器：拥有者 `Provide` 自己的表名集合，
消费者 `Resolve` 并在 `ModuleDependencies()` 声明拥有者即可。
连接器只在**拥有者排在消费者之后**（无法被声明为依赖）时才必需，P5 的六对正是那种情形。
注册序：`audit(0) core(1) authn(2) identity(3) organization(4) oauth(5) provisioning(6) governance(7) federation(8)`；
`Registry.Activate` 要求 `dependencyIndex < index`，架构门禁 `module-dependency` 要求
「Resolve 了谁的类型就必须声明谁」——合起来即**拥有者 index 必须小于消费者 index**。

### 唯一受阻：authn ↔ identity，共 8 处，如实保留在基线里
authn 已 import identity（`identity.RegistrationPrincipalCreator`，靠架构门禁
`isStableWiringCapability`（`tests/architecture/architecture_test.go:554`）白名单放行）。于是：
- identity 读 authn 的表 → identity 要 import authn → **Go 包环**；
- authn 读 identity 的表 → authn 要声明 `identity.Name`，而 identity(3) ≥ authn(2) → `Activate` 启动失败；
- 连接器方向同样撞上这两条之一；白名单只列了那一个类型，且**不许改 tests/architecture**。
站点：identity 读 authn 的 `iam_credentials`×2、`iam_sessions`、`iam_email_verification_tokens`、
`iam_password_recovery_tokens`（5）；authn 读 identity 的 `iam_principals`×2、`iam_service_accounts`（3）。
**唯一的解法是把 `RegistrationPrincipalCreator` 这条 authn→identity 的边移走**，属独立议题，需用户决定。

### 已知的同类缺陷，未修（D 类，超出本次范围，需用户决定）
`core.NewRevisionStore(runtime.TablePrefix())`：governance / identity / organization 把**自己的前缀**
交给 core，由 core 在 `core/revision.go` 内部拼到 core 拥有的 `iam_policy_revisions` 上。
同一缺陷类，但拼接发生在 core 的构造函数里，改动会波及 4 个模块共用的 core API。

### 用户约束（未变）
- **不要把带 SQL 的东西放进 `pkg/`。** 若某对必须走中立位置，优先考虑业务模块下的独立子目录
  （前提：两侧 import 它不构成环），而不是 `pkg/`。

### 教训（写给下一个上下文）
- 这一层的正确性只能由 `Registry.Activate` 的启动期检查（`tests/lifecycle`）确证，不要用推理代替。
  本会话此前在这个边界上推演错过三次。
- **站点数要用脚本普查，不要目测**：governance 目测 11 处，实际 37 处。
- 并发子代理共用一个工作树时**不要 `git stash`**（本会话有代理这么做了，连带暂存了同伴的在途改动，
  pop 成功但属高风险操作）。

## 事后修正（2026-09-11，用户质疑后核实，优先级高于上面的记述）

### 1. P4c 的理由我写错了，已改提交说明
`table_prefix` 是**全局唯一一个配置项**（`internal/app/config.go:34`，装在 runtime 上，整个程序一份）。
因此消费方自己拼出的 `prefix+逻辑名` 与拥有者拼出的**永远是同一个字符串**。
我在三条提交说明里写的「某个部署里拥有者前缀不同就会查到不存在的表」**场景不可能发生**。
提交（未推送）已重写为真实理由：**表名没有主人**——拥有者改名时，消费方照样编译通过、只在运行时炸；
而且跨模块读表这件事以前在依赖图上看不见。

### 2. 真正优雅的做法：让拥有者干活，而不是把表名递出去
仓内**已经有这个样板且在用**：
- `internal/app/principallifecycle.go`：app 作为编排者，依次调用每个拥有者**自己**的
  `XXX.ErasePrincipalData(ctx, tx, prefix, id, now)`，各自清自己的表。没有任何模块读别人的表，
  也就没有任何循环依赖。
- `authn/module.go:69` 已经 `Provide[identity.PrincipalCredentialsRevoker]` ——委派通道早就存在。

**设计规则（建议写进 SKILL.md）**：
- 跨模块的**写**、或**单表读** → **委派给拥有者**（表名不过界，不产生依赖边）；
- 跨模块的**单条语句 JOIN** → 名字必须过界，才用「拥有者发布表名」。
按这条规则，P4c 已做的 65 处里属于 JOIN 的那部分是对的；而下面第 3 条那些是**该委派却没委派**的。

### 3. 「authn↔identity 受阻 8 处」这个判断是错的，而且规模远大于 8
受阻只是针对「发布表名」这一种机制。按委派走不产生任何 import 边，**不受阻**。
而且真实规模被我的普查低估了（普查只认字面量，没认常量）：
- **identity 写 authn 的表**：`createCredential`（建凭据）、`incrementCredentialVersion`（改版本号）、
  `revokeAllSecurityState`（吊销 `iam_sessions` + 消费 `iam_password_recovery_tokens` /
  `iam_email_verification_tokens`）。authn 已经提供了 `PrincipalCredentialsRevoker`，
  这些本就该委派。
- **authn 操作 identity 的表**：`internal/iam/authn/repository_identity.go` 用
  `principalTableName`/`serviceAccountTableName` 两个常量，**约 15 条语句**——建 principal、改 principal、
  读 principal、JOIN service_accounts。不是 3 处。
→ 这是**双向的数据所有权破裂**，不是命名问题。修法是把两侧的跨界操作改成互相委派，
  规模是一块独立的工程，需要用户决定是否排期。**不要再试图用发布表名解决它。**

### 4. 一处注释是错的（未修，下次顺手改）
`internal/iam/identity/repository.go` 的 `revocationTable` 注释写「The sessions belong to identity,
so its own prefix names them」——**`iam_sessions` 是 authn 拥有的**
（`internal/iam/authn/model/credential.go:41`）。这条注释会把下一个读者引到错的所有权认知上。

### 5. 两个「本来可以更简单」的发现
- **gorm 的命名策略完全没用**（`pkg/extension/gorm` 里没有 `NamingStrategy`/`TablePrefix:`）。
  如果建连接时配一次 `schema.NamingStrategy{TablePrefix: ...}`，基于模型的查询就自动带前缀，
  业务代码里一个 `prefix +` 都不用写——P1~P6 这条路线的大半不会存在。现在改的代价是裸 SQL 与
  `Table()` 调用绕过命名策略，享受不到。
- **跨模块传表名应该用「带类型的字段」而不是「字符串查 map」**。现在是
  `accessTable("iam_tenant_members")`：魔法字符串还在、编译器帮不上忙、每处多一个永不触发的错误分支。
  改成拥有者导出带字段的结构体（`tables.TenantMembers`）则三者全解，且拥有者那边
  `query/table.go` 的 `TableNames` **已经是这个形状**。属返工（约 65 处），收益是代码变少。

## 收尾计划的执行进度（2026-09-12）

计划文件：`~/.claude/plans/flickering-toasting-neumann.md`（用户已批准）。

### 阶段一 authn↔identity 解耦 —— 完成，**全仓自拼他模块表名 65 → 0**
提交 `9e89c5e..501efc0`（未推送）：
- `9e89c5e` S0 冻结 9 条语句的 SQL 文本 + 补链路特征测试（零生产改动）
- `906df4e` S1 删 `EnsureBootstrapPrincipal` 等死码（净 −196 行）
- `7fbf266` S2 identity 推表名给 authn（`PrincipalTableSetReceiver`，方向反转）
- `7d41726` S3 扩 `PrincipalStateStore` 六个 reader（纯增量，SQL 等价实测）
- `c7555db` S4 authn 七条语句换方法体（签名/错误/调用点不变）
- `d374ef8` S5 authn 提供 `PrincipalCredentialStore`（四个闭包钉在调用方事务）
- `61f2b35` S6 identity 五条越界写换成能力
- `501efc0` S7/S8 保留 sessions 双写 + 计数不变量、修 authn 持有者注释、写 D39

**决定性验证**：`make ci` **EXIT=0**（总覆盖率 90.1%、六个集成套件全 ok、openapi 指纹一致）；
`make test-mysql8-run` **EXIT=0**，`TestIAMPerformanceExternalDatabase` **PASS**、
`iam_performance_baseline.json` `git diff --quiet` 为真、schema lock 219 文件未变、72 表、33 跨模块 FK。
门禁账本 **385 → 372**（四次手动下调，全程未用 `QUERY_GATE_REBASELINE`）。

详细决策见 `.claude/decisions.md` 的 **D38**（死码删除）与 **D39**（双向委派、方向反转、
`LockActiveRow` 的安全差异、sessions 双写为何保留、identity 测试为何只能用替身）。

### 阶段二 带类型字段 —— 拥有者侧已完成（`1478189`），**消费方侧未开工**

#### 已完成
五个拥有者各导出一个类型化结构体 + 构造函数 + `Bound()`，并各带一个**漂移门禁**
（`TestXCoversEveryTableTheModuleOwns`，用反射比对 `query.BindNames` 的字段集；
已做变异验证：删掉一个字段立刻失败）：

| 模块 | 类型 | 构造函数 | 字段数 |
|---|---|---|---|
| core | `AccessTableNames` | `PublishedAccessTableNames(prefix)` | 14 |
| identity | `PrincipalTableNames` | `PublishedPrincipalTableNames(prefix)` | 5 |
| authn | `CredentialTableNames` | `PublishedCredentialTableNames(prefix)` | 15 |
| organization | `DirectoryTableNames` | `PublishedDirectoryTableNames(prefix)` | 14 |
| oauth | `GrantTableNames` | `PublishedGrantTableNames(prefix)` | 5 |

**旧的不具名形式仍在并存**（`XTables{Published gormx.TableSet}` 能力 + `TableSet(prefix)` 函数），
这是为了让消费方能一个模块一个模块地迁移。**它们在阶段二结束时必须删除。**

#### 剩余工作：消费方迁移（71 处生产 + 52 处测试）

**唯一的一条规则**（由 Go 的 import 方向决定，不可选）：
> 跨界的类型化结构体，声明在**被对方 import 的那一侧**。

**A 类 —— 消费方 import 拥有者 ⇒ 直接用拥有者导出的类型**（66 处生产）：

| 消费方 | 拥有者(槽位) | 生产处 | 测试处 |
|---|---|---|---|
| authn | core(access) | 1 | 4 |
| authn | identity(principal) | 2 | 5 |
| identity | core(access) | 1 | 0 |
| organization | core(access) | 8 | 5 |
| oauth | core(access) / identity(principal) / authn(credential) / organization(directory) | 3/4/1/1 | 2/1/1/1 |
| provisioning | core(access) / identity(principal) / organization(directory) | 1/1/7 | 2/1/4 |
| governance | core(access) / organization(directory) | 17/10 | 3/2 |
| federation | core(access) / organization(directory) | 6/3 | 1/1 |

做法：消费方的 `foreignTables` 槽位从 `gormx.TableSet` 换成拥有者的类型化结构体；
`module.Resolve[core.AccessTables]` 换成 `module.Resolve[core.AccessTableNames]`；
调用点 `r.foreign.accessTable("iam_tenant_members")` 换成 `r.foreign.access.TenantMembers.String()`；
**「是否已绑定」的检查从每处一条收成每对一条**，放在 `Initialize`（Registry 相位保证那时所有
Provide/Bind 都已完成），任一槽位 `!Bound()` 即启动失败。P4c 写的那批「未发布即报错」的
运行时用例随之收敛成每模块一条启动期用例。

**B 类 —— 拥有者 import 消费方 ⇒ 消费方声明自己需要的字段，拥有者填满**（5 处生产）：

| 消费方 | 拥有者 | 现有连接器 | 生产处 |
|---|---|---|---|
| core | organization | `core.DirectoryTableSetConnector` | 走 `query.BindDirectoryTables`，已是类型化 |
| core | identity | `core.PrincipalTableSetConnector` | 同上 |
| authn | organization | `authn.DirectoryTableSetConnector` | 1 |
| authn | oauth | `authn.OAuthTableSetConnector` | 2 |
| identity | organization | `identity.DirectoryTableSetConnector` | 1 |
| identity | oauth | `identity.OAuthTableSetConnector` | 1 |

做法：把连接器的载荷 `Bind func(gormx.TableSet) error` 换成
`Bind func(消费方声明的类型化结构体) error`；拥有者（organization/oauth，它们 import 消费方）
在填充处逐字段构造。**这一侧比 A 类更强**：消费方加一个字段，拥有者的填充处立刻编译不过。
core 的两对已经是这个形状（`joined_tables.go` 在绑定时转成 `query.DirectoryTables`），
可直接照抄；只是要把中间那个不具名集合去掉。

**收尾（0 遗憾的判据）**：
1. 全仓 `grep -rn 'Table("iam_' --include=*.go internal/` 除拥有者自己的 `query/table.go` 外无命中；
2. `gormx.TableSet` / `NewTableSet` / `Lookup` 若已无人使用即整体删除（`make lint` 的 unused 判定）；
3. 各模块的 `XTables{Published ...}` 与 `TableSet(prefix)` 全部删除；
4. SQL 文本不变——由 `internal/iam/authn/repository_identity_test.go` 的 9 条冻结断言、
   `internal/iam/identity/principalstate_test.go` 的 8 条、以及 `make test-mysql8-run` 的
   查询计划指纹共同证明。

**并行建议**：A 类按消费方模块切，彼此不共享文件，可并行下放；B 类涉及 organization/oauth 的
填充处（会被多个消费方同时触碰），必须串行、由主代理自做。门禁账本由主代理统一刷新。

### 阶段二 方案 C 的来由（保留）

**用户的要求（2026-09-12）**：必须强类型约束写法，全仓统一，0 遗憾。
并明确指出一处我解释失败：**本次不改任何表名**，「改名就编译不过」只是在比较两种写法的安全性。

**实测规模**：生产调用点 **71 处**（不是计划估的 ~65），测试调用点 52 处。

**被推翻的原计划**：原本要「拥有者导出类型化结构体、消费方引用它」。实测有 4 对做不到——
拥有者激活在消费方之后时，消费方 import 拥有者会成包环（已验证 `organization` 确实 import 了 `core`，
3 个文件）。那会导致 66/71 强类型、5 处留字符串，两种形态并存。

**最终方案 C（一条规则，由 Go 的 import 方向决定，不可选）**：
> 跨界的类型化结构体，声明在**被对方 import 的那一侧**。

- **消费方 import 拥有者** → 拥有者导出自己的类型化结构体，消费方直接持有它。
  适用：authn←core、identity←core、organization←core、oauth←{core,identity,authn,organization}、
  provisioning←{core,identity,organization}、governance←{core,organization}、
  federation←{core,organization}、authn←identity（S2 的 receiver 形状）。
- **拥有者 import 消费方** → 消费方声明它需要的字段，拥有者负责填满（连接器的现有方向，
  只是载荷从 `gormx.TableSet` 换成消费方声明的类型化结构体）。
  适用：core←organization、core←identity、authn←organization、authn←oauth、
  identity←organization、identity←oauth。
  **这一侧更强**：消费方加字段 → 拥有者的填充处编译不过 → 被迫提供。

两种情形下消费方手里都是类型化结构体，**71 处调用点全是字段访问**。

**交付标准（0 遗憾）**：全仓无一处字符串查表、无不具名集合；`gormx.TableSet` 若无人使用即删除；
每处 SQL 文本不变（由既有的冻结断言 + MySQL 查询计划指纹证明）。

**仓内已有的先例**：`internal/iam/core/joined_tables.go` 已经在绑定时把集合转成
`query.DirectoryTables`，调用点用字段——阶段二是把这件事做成全仓唯一形态，并去掉中间那个不具名集合。

### 阶段二 原定描述（已作废，保留以便追溯）
拥有者在根包导出字段为 `gormx.TableName` 的结构体（镜像 `query.TableNames`，带漂移测试）；
把「是否已绑定」校验集中到 `Initialize`（任一字段为零值即启动失败），业务代码错误分支从约 65 降到每消费方 1 个；
P4c 写的约 30 个运行时用例会收敛成每模块 1 个。**顺序在阶段三之前**——计划里纠正过用户最初的选择：
返工之后那 142 个「查表键」从代码里消失，门禁就不需要加「查表键不算违规」这条特例。

### 阶段三 门禁规则 / 阶段四 文档体检 —— 待开始
阶段三五项修正（排除 Prometheus 指标名误报、只认前缀加法、按 file:line:col 去重、补常量洞、
补 `sql-in-format-string` 的局部变量洞）与阶段四 36 份文档清单见计划文件。

### 阶段二执行进度（2026-09-12，本会话）

**已提交 `815b5d9`——federation 参考实现，其余模块一律照抄这个形状：**
1. 拥有者在 `ProvideServices` 里**并行**多 `Provide` 一个类型化结构体
   （`core.AccessTableNames` / `organization.DirectoryTableNames` / `identity.PrincipalTableNames` /
   `authn.CredentialTableNames`；oauth 无需——它只被 B 类连接器消费）。旧的 `XTables{Published}` 暂留。
2. 消费方 `foreignTables` 的槽位换成拥有者的类型化结构体；`bind` 收类型化结构体；新增 `bound() bool`；
   删掉 `xxxTable(logical string)` 查表方法与 `publishedTableName` 帮手。
3. 调用点 `x, err := r.foreign.accessTable("iam_roles")` + `if err != nil` 三行 → `x := r.foreign.access.Roles.String()` 一行。
   **保留局部变量与变量名**，构造 SQL 的语句行一个字节都不动（这是 SQL 文本不变的施工保证）。
   删掉 `:=` 后若后文 `err = ...` 变成未声明，把那一处改成 `err :=`。
4. `module.go` 改 `module.Resolve[core.AccessTableNames]`；`Initialize` 里加一条 `!m.foreign.bound()` 启动检查。
5. 测试：per-statement「未发布即报错」用例**收敛**成 (a) 一条 `bound()` 单测（零值 / 各拥有者缺一 / nil）
   + (b) 一条启动期用例（`Initialize` 报错）。「发布了但不全」的用例随类型消失——结构体没有部分发布这回事。

**已做的一处修正**：federation 的 `badKey` 用例原先绕过依赖直接置字段，新增的启动检查排在
加密密钥检查之前，导致它读到新错误。修法是给该夹具补上 `foreign.bind`（补全前置条件），
不是把检查挪后——检查属于「必需依赖」那一组。

**federation 实测**：`go test ./internal/iam/federation/ -count=1` **ok**；
`go test ./internal/iam/... ./internal/app/... -count=1` **0 FAIL**；
`go -C tests test ./architecture/ -count=1` **ok**；门禁账本 **372 → 350**（手动下调，未用 REBASELINE）。

### 阶段三 门禁规则修正（2026-09-12，提交 `a35723c`）

**规则从「量 AST 形状」改成「量真实缺陷」，账本 218 → 46，手动下调，未用 REBASELINE。**
218 次计数只落在 163 个物理位置上——这两个数字的差、以及 163 里的大部分，都不是缺陷。

六项改动，每项都有夹具且都做过变异验证：
1. **真实表名集合**：从各模块 `model` 的 `TableName()` 读出真表（与迁移、schema lock 同源）。
   只靠形状会把 11 个 `iam_scim_*` Prometheus 指标名当成表。
2. **位置收紧**（用户选的「两者都做」= 真表集合 **且** 查询位置）：只有查询方法实参、
   模块自己的前缀帮手实参、以及 return 出去的名字才算表位置。
3. **补常量洞**：标识符绑定到字符串会被解析，把表名塞进常量再传给 `Table()` 不再能绕过。
4. **只认前缀加法**：加法某一侧必须是前缀来源。「任何加法」此前把 `"version": version + 1`、
   一处 `sync.Map` 键、以及 90 多处「已解析名 + `" AS 别名"`」全算成违规。
5. **按 file:line:col 去重**：五种宿主节点嵌套，同一字面量被算 2–3 次。
6. **`sql-in-format-string` 跟一跳赋值** + **修 `UPDATE\s` 永不匹配 `UPDATE %s`**（计划里没有这条，
   验证时发现：只补局部变量洞仍抓不到那唯一一处 Sprintf，边界写法才是真凶）。

**我自己两处实现被验证推翻，已不在结果里**（写下来是因为它们都很容易再犯）：
- 给 `Table()` 实参加「结果得像张表」的守卫 → 丢掉 `Table(prefix+owner.versions)` 这个真违规
  （表名来自结构体字段）。`Table()` 的实参按定义就是表名；该守卫只该留在 return 路径。
- 换成真表集合后 return 路径钻进嵌套调用 → 误报 `fileResponse("todos", …)`（`todos` 是真表名，
  但那是文件名）。改为不下钻嵌套调用，由被调方自己判定。

**剩余 46 处全部可指认**：13 处 `goose_<模块>` 版本表（前缀由 `goosex` 外部施加，站点处改不了）、
7 条 core 授权语句 + 8 处 `core/relationship.go` 递归 CTE（P2 冻结，有 golden 断言）、
6 个模块的 `func (r *Repository) table(name string)` 帮手、5 处 governance SQL 片段、
`core/revision.go:28`（D 类已知项）、`app/principallifecycle.go:47`（goose 版本查询）、
1 处 `sql-in-format-string`（`authn/repository_credentials.go`）。
**继续压需要改生产代码，不是改门禁。**

**账本 `rebaseline` 字段未动**，仍写着 `initial freeze at the P0 commit`。budget 46 的真实出处是
「规则语义变更后重新量测，不是抬高」——用户明确要求不动 rebaseline 机制，故字段保持原样，
出处记录在此与提交 `a35723c` 的说明里。**若日后觉得字段该如实，改字符串即可，不触发抬预算机制。**

## Last Checkpoint
2026-09-12 **手写 SQL 复查：15 → 13**（用户质疑「没清零」后逐条复查发现）。
`identity/lifecycle.go:lifecycleNow` 与 `federation/repository.go:replayNow` 是在**手抄
`gormx.DatabaseClockMillis`**（P1 就加了这个原语，行为等价，原语还多一个 nil 守卫）。已改为调用原语。
门禁账本 **46 → 44**，手动下调。

**这条教训比那两行代码重要**：我此前把「门禁命中 = 已在账本 = 已知 = 可指认」当成了「有理由保留」。
**账本记录的是存在，不是正当。** 复查方式应当是逐条问「这一条为什么还在」，而不是看它是否已被记录。
剩余 13 处已逐条核过，全部是 ORM/gen 表达不了的语句形态：
7 条 core 授权递归 CTE（P2 冻结 + golden + 指纹）、5 条 governance 复核图查询、
1 条 authn 登录失败计数 UPDATE（两个 CASE 必须观察旧计数，依赖 MySQL 从左到右求值赋值顺序）。

2026-09-12 **形状统一收尾**（用户在验收提问后要求「改，要到位」），提交 `01c14cf`：
八个模块「不属于自己的表」这一层现在只有一种形状。
- core 的 `joinedTables` → `foreignTables`（文件与配对测试同时改名），
  `bindDirectoryTables`/`bindPrincipalTable` → `bindDirectory`/`bindPrincipal`；
  删掉 `directoryBound`/`principalBound` 两个布尔——状态从名字本身读，布尔会与它描述的名字不一致
- `organization.bindAccess` → `bind`（与同为一次性 Resolve 的四个模块一致）
- **六个「消费方声明的载荷」补上 `Bound()`**（拥有者发布的五个本就有）。这是统一的关键一步：
  八个模块的 `bound()` 现在都是「逐槽位问 `Bound()`」
- authn 的 oauth 绑定器改为逐字段报名，断言随之**变严**（必须报出具体字段）
**验证**：`make ci` **EXIT=0**；`make test-mysql8-run` **EXIT=0**，
`TestIAMPerformanceExternalDatabase` **PASS**、性能基线 diff 为空、schema lock 219 未变
——core 动了持有者类型与授权语句组装路径，指纹一致是「只换形状未改 SQL」的证据。
**过程中的错误（写下来防再犯）**：做 `joined` → `foreign` 改名时用了全词正则，
改到了英文注释与一个局部变量（`never joined` → `never foreign`、
`consent joined to the client` → `consent foreign to`）。三个文件已还原。
**机械替换必须先看命中列表再落笔**。

2026-09-12 **阶段四（文档体检）完成**，提交 `848297f`（skill 补「何时不该让表名过界」+ D39/D37 加注）
与 `8d696aa`（README 3 处、runbook 3 处事实订正）。
审计方式是**逐条对照代码验证**，不是通读：后端 11 份 docs + README 共查出 5 处与代码矛盾的陈述。
**刻意未改**：带日期的演练记录（触发器 58→今 66、密钥文件 8→今 9、浏览器审计 13→今 14），
它们标注了观测日期且后续提交能解释差额，改成今天的数字等于把记录伪造成对当下的断言。
**两件需用户决定的事见 `## Next Action`。**

2026-09-12 **阶段三完成**，提交 `a35723c`，`make ci` **EXIT=0**（在最终状态上重跑确认过一次，
因为第一次 CI 启动时还差一段注释修改）。总覆盖率仍为 90.0%。

2026-09-12 **阶段二完成并全量验证通过**，3 个提交 `815b5d9`（federation 参考实现）、
`d2ca56c`（四个 A 类消费方）、`660bf97`（B 类六对 + 清理 + 测试跟进），**未推送**。

**决定性验证（我自跑，非采信子代理）**
- `make ci` **EXIT=0**：fmt clean、lint 0 findings、六个集成套件全 ok
  （architecture 3.4s / goose 16.5s / testdb 0.3s / lifecycle 114.3s / migration 272.6s /
  performance 44.5s）、build、`openapi: matches docs/openapi.json`
- `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：`TestIAMPerformanceExternalDatabase` **PASS**
  ——查询计划指纹与基线一致；`iam_performance_baseline.json` `git diff` 为空；
  `schema lock: 219 published migration files unchanged`；72 表、33 跨模块外键
- 门禁账本 **372 → 350 → 236 → 218**，三次均**手动下调**，全程未用 `QUERY_GATE_REBASELINE`
- 三条收尾判据全部实测通过：生产代码 `Table("iam_` 零命中；`gormx.TableSet`/`NewTableSet`
  在 `internal/`+`pkg/`+`tests/` 零命中；`TableSet(prefix)` 与 `XTables{Published}` 全部消失

**必须记住的两件事**
1. **总覆盖率 90.1% → 90.0%，正好压在门禁线上，余量为零**（core 与 oauth 两个包同样是 90.0%）。
   原因是删掉了大量被测试覆盖的错误分支（每个调用点三行 `if err != nil`），分母缩小得比分子慢。
   **下一次任何删减都可能把它压到线下**，改动前先跑 `make test-coverage`。
2. **`make ci` 抓到了我 grep 没抓到的东西**：`tests/` 是独立 Go module，
   我只 grep 了 `gormx.TableSet` 而没 grep `<模块>.TableSet(`，漏了
   `tests/migration/iam_core_test.go` 两处，编译失败。**普查必须覆盖 `tests/` 模块**。

**本会话推翻的一个计划结论**：计划称 B 类「消费方加字段 → 拥有者编译不过」。**这是错的**，
Go 的带键结构体字面量在新增字段时照样编译。真正的强制是运行期逐字段 `IsZero()` + 启动期
`bound()`，两者都做过变异验证。详见 D40。

2026-09-11 **P4c 全部完成，全量门禁绿**（4 个提交 `3cb34f1..001f6a0`，未推送）：
- `make ci` **EXIT=0**：fmt clean、lint 0 findings、**总覆盖率 90.1%**（全模块 ≥90.0：organization 90.0 /
  identity 90.2 / authn 90.5 / oauth 90.2 / provisioning 90.4 / governance 93.6 / federation 91.1）、
  architecture 3.6s / goose 15.4s / testdb 0.3s / lifecycle 112.2s / migration 265.3s / performance 41.0s
  **全 ok、0 FAIL**、build、`openapi: matches docs/openapi.json`
- `make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4）：`TestIAMPerformanceExternalDatabase` **PASS**
  ——查询计划指纹与基线一致（表名被改错会立刻在此暴露）；`iam_performance_baseline.json`
  `git diff --quiet` 为真；`schema lock: 219 published migration files unchanged`；
  `static reconciliation: 219 files, 72 IAM tables, 33 cross-module foreign keys`
- 门禁账本 **482 → 473 → 412 → 385**，三次均**手动下调**，全程未用 `QUERY_GATE_REBASELINE`
- 脚本普查复核：全仓「消费方用自己前缀拼他模块表名」**从约 65 处降到 8 处**，
  剩下的 8 处全部落在受阻的 authn↔identity 一对内
- `git status` 仅受控改动，无 `.gen.go` / `cover*.raw` / `.log` / `bin/` / 临时产物

2026-09-11 **P4c 第一段（oauth）完成并提交 `89de48b`**：新增四个拥有者能力
（`core.AccessTables` / `identity.PrincipalTables` / `authn.CredentialTables` / `organization.DirectoryTables`），
oauth 的 9 处他模块表名改为解析而来，`ModuleDependencies` 增 `core`+`organization`。
实测：`go build ./...` EXIT=0；`go test ./internal/... -count=1` 0 FAIL；
`go test ./tests/architecture/ -count=1` **ok**（门禁账本 **482→473**，手动下调，未用 REBASELINE）；
`go test ./tests/lifecycle/ -count=1` **ok 99.194s**（= `Registry.Activate` 接受新依赖声明的决定性证据）；
`make test-coverage` 总覆盖率 **90.1%**、oauth **90.2%**；`gofmt -l internal pkg tests` 空；
`make lint` 0 findings；`git status` 仅受控改动、无临时产物。
**尚未跑**：`make ci` 全量与 `make test-mysql8-run`（留到 P4c 全部改完一次性跑）。

2026-09-11 P0–P5 已推送（`0da7f6e..9295570`，9 个提交）；P4c 受阻范围修正为约 4 处。
2026-09-11 **P5 六对连接器全部完成并实测**：`go build ./...` EXIT=0、`go test ./internal/... -count=1`
无 FAIL、`make ci` **EXIT=0**（总覆盖率 **90.1%**，全模块 ≥90.0%）、`make test-mysql8-run` **EXIT=0**
（schema lock 219 文件未变、72 IAM 表、33 跨模块 FK）、`gofmt -l internal pkg` 空、
账本 **HEAD 572 → 482**（`handwritten-sql` **15 不变**，7 个 key 逐字相同）、
`git status --porcelain` 66 项且无 `.gen.go`/`cover*.raw`/`.log`/`bin/`；
「未接线即失败」在 core/authn/identity 三处消费方 + organization/oauth 两处 owner 侧全部实测 PASS。
**未做**的 4 对（`identity←authn`、`oauth←organization`、`governance←organization`、
`federation←organization`）表名保持原样，已逐站点列出。

2026-09-11 **P4b 第一对（`identity.PrincipalStateStore`）完成并实测**：`go build ./...` EXIT=0、
`go test ./internal/... -count=1` **51 包 ok / 0 FAIL**、`make ci` **EXIT=0**（总覆盖率 **90.1%**，
oauth 90.2 / federation 91.0 / identity 90.5）、`make test-mysql8-run` **EXIT=0**（真实 MySQL 8.4，
`TestIAMPerformanceExternalDatabase` 指纹 PASS、schema lock 219 文件未变）、`gofmt -l internal pkg` 空、
账本 budget **581→572**（`handwritten-sql` 仍 7 key/15 处）、`git status` 仅本次受控改动、无临时产物。
逐语句 SQL 实测（gorm DryRun + callback 钩子）：oauth LOCK/STATUS 与 HEAD **逐字符相同**；
federation PRINCIPAL 由 `Scan` 改为 `Take`，**多一个 `LIMIT 1`**（主键等值查找，语义等价，已如实报告）。

2026-09-11 P4a 完成并实测：`go build ./...` 0、`go test ./internal/... -count=1` 51 ok/0 FAIL、
`make ci` **EXIT=0**（总覆盖率 **90.1%**）、`make test-mysql8-run` **EXIT=0**（`TestIAMPerformanceExternalDatabase`
指纹比对 PASS、schema lock 219 文件未变）、`gofmt -l internal pkg` 空、账本 budget **736→581**
（条目 287→237；`handwritten-sql` 7 key/15 处**逐 key 相同**）；`git status` 仅 22 个受控改动、无临时产物

## 2026-09-13 5 处账本层修复 + SQL 零手写普查（用户「your call」后执行）

**5 处修复**：core/relationship.go 两处语句的表名（`iam_relationships`/`iam_entities`/
`iam_resource_relationships`）与 organization/tenant.go:112（`iam_tenants`）改走各自模块
`query.BindNames(prefix)`（`Relationships/Entities/ResourceRelationships/Tenants`）。
SQL 文本逐字节不变（恒等式替换）。**账本 29→20**（12 条目/20 计数单位；9 计数单位消失，
手工下调、未用 REBASELINE、`QUERY_GATE_UPDATE=1` 幂等 md5 不变）。架构门禁绿。
`make ci` 在本沙箱跑不起来（fmt-check 首步 Git-bash `CreateFileMapping` 被拒——环境限制，
与既有 GnuWin32/bash 记录同类），改为逐门禁直接验证：gofmt 整树 clean、go vet 两包 0、
`go build ./...` 0、core+organization 模块测试 ok、架构门禁绿且幂等；全量 `go test ./internal/...`
与 `go -C tests test ./...` 后台验证中。

**交付 `apps/backend-api/docs/sql-zero-census.md`**（359 SQL 站点 = B1 类 262 / B2 77 / B3 20；
另 B-NA ≈51、迁移物料另计）。口径=片段层（一切 SQL 字符串字面量），与账本层（表名位置，
budget 20）分离。含 B2 决策清单 D1-D10、B3 冻结清单（20 站带锚点）、里程碑 M1-M4、
扫描器模式集与盲区记录（**点在上行行尾、方法名在下行行首的续行形态**，21 模式扫描器
系统性漏抓，oauth 实测漏 23/24）。

**普查新发现（需拍板）**：① governance `reviewCounts`（SUM CASE）可 gen 化（GROUP BY+Go 折叠）
→ 账本 key `query` 2→1、budget 20→19 预判（推翻了旧"13 条表达不了"原判，与 goose/5 处同类）；
② identity 4 站可经 `core/query` 降 B1（core(1)<identity(3) 无反向边）；③ organization/oauth
方向不可（注册序 + 既有反向 Go import）；④ authn↔identity 死角维持（D37/P5）。

**提交计划（未推送）**：census 文档一个提交 + 5 处修复与账本一个提交。

## 2026-09-13（续）recordLoginFailure 模板化试点 —— 账本 20→19

背景：用户连发四轮 gorm 能力文档（DAO 查询 / TablePrefix / Associations / CLI Typed Raw SQL），逐轮核对了钉死源码 v0.3.29：
- **DAO 文档 → §7 回扫**（普查文档新增）：B2 过度归类修正——`LeftJoin(table, conds...)`多条件 ON、`As`、`Distinct`、`gen.Exists`、`Count/Sum/As`、`Between` 全在 v0.3.29；据此 15-20 站 B2→B1-check（候选口径 B2 77→约55-60、B1类 262→约280）。
- **TablePrefix（连接级 naming）**：不适用——只作用于 model 派生路径，裸 SQL/Table()/Raw 不经过 naming；且前缀是运行期部署配置（runtime.TablePrefix），不该埋进数据源层。
- **Associations/Preload**：不适用——零关联字段（model 层刻意扁平），跨模块引用 model 触 `cross-module-*` 门禁，双向引用=Go 包环（已实验证实）；Preload 是多语句非同一快照，与 B3 单语句约束冲突。
- **CLI vs Gen / CLI Typed Raw SQL**：新 CLI（v1.31.2 的 `G[T]`/`schema.Interface[T]` 已存在，仓库 0 用）— Where/Select/Order 是字符串接口，换过去"0 手写"不缩反退；Typed Raw SQL 的 `@@table/@param/{{if}}` 是治理形态（SQL 进注释=门禁看不见，限局部用）。

**试点落地**：`authn/repository_credentials.go:113` `recordLoginFailure` 从 `fmt.Sprintf(\`UPDATE %s …\`)` 改为包级常量 `loginFailureStatement`（`@@table` 占位）+ `strings.Replace(…, "@@table", tables.Credentials.TableName(), 1)`。最终 SQL 文本逐字节不变；账本 `sql-in-format-string:339e855787a58a02` 键消失、**20→19**（手工下调未用 REBASELINE）；handwritten-sql 无新增键（模板占位不被计数）。验证：gofmt clean、`go build ./...` 0、authn 全包测试 ok、门禁绿且 QUERY_GATE_UPDATE=1 幂等 md5 不变。

**边界**：SQL 模板化只用于"文本冻结、表名已类型化"的语句，未推广全仓（SQL 进注释/占位后 AST 门禁需配套才能继续看见表名）。
**待用户拍板（未动）**：governance reviewCounts（budget 19→18 预判）、B1 262 站迁移批次、B2 决策 D1-D10、是否把四轮结论写成 census 选型备注。

## 2026-09-13（续）D41 Resolver Isolation 三件套落盘

用户拍板"现在就做、一次定死、不动 tests/architecture"。已提交（未推送）：
- **`decisions.md` D41**：Logical Table ≠ Physical Table；判定指标 =「0 个未经授权的 physical-table-name 决策」而非「0 TablePrefix()」；6 条 invariants + Allowed/Forbidden + Non-goals + enforcement 顺序（先清理后机械化、最后才动 architecture tests）。采纳了外部草稿的核心（指标措辞、invariant、non-goal、账本重标记），校准两处：①NewRevisionStore 是 resolver 泄漏不是跨 namespace 混淆（全仓共享同一 table_prefix）；②账本链路 19→15（4 helpers）→14（goose）。
- **SKILL「十、Resolver Isolation」**：执行规则（动库代码前先分类表名；MUST NOT 清单；解析路径优先级；改完自检；不扩大范围）。
- **census §5.4 账本重标记 + §8 门禁草案**：9 prefix()（leakage）+ 4 table()（reconstruction）+ NewRevisionStore（propagation）+ 1 goose（B3）分裂定性；resolver-isolation 规则形态 + allowlist + 迁移期机制，明确"未实施"。
- 实测 40 处 TablePrefix() 全量分三类：~24 合法 / 9+4+NewRevisionStore 违规面 / goose B3 + S3-URL 不误伤。

## 2026-09-13（续）D41 第 1 步：4×table() 帮手清零 —— 账本 19→15

4 模块并行子代理 + 主代理收尾，已提交（未推送）：
- **authn**（6 站 reauthentication.go，删 `table()`、去无关 `query` import、测试改 `prefix()+...` 形态）、**federation**（26 站，新增 `tables()`——原模块缺；resolver 补 **ReplayClaims** 字段+漂移门禁 5→6 表）、**organization**（49 站代理转 + 主代理收尾 2 个动态循环站点：`deleteGroup`/`deletePosition` 的 checks 循环拆成逐表 typed 块，错误消息保留逻辑名；删 `table()`）、**provisioning**（33 站，新增 `tables()`、删 `table()`）。
- 生产代码 `.table(` 残留 **0**；SQL 文本逐字节不变（`tables.X.TableName()` == `prefix+logical`，各模块 `table_test` 漂移门禁 + frozen 断言佐证）；`prefix()` 保留（仅 resolver 入口用途：`tables()` 与 `query.BindNames(repo.prefix())`——D41 Allowed，属下一步 9×prefix() 范围）。
- 验证：gofmt 全树空、`go build ./...` 0、4 模块测试全 ok、全量 `go test ./internal/...` 后台跑；账本 **19→15**（QUERY_GATE_UPDATE 移除 4 键 955a3f/6520c/802198/e85793，手工下调 budget、幂等 md5 不变）、门禁绿。
- 提交计划：4 模块改动 + 账本一笔；census/state 文档另一笔。

## 2026-09-13（续）D41 第 2 步 Part A：NewRevisionStore 归位 —— 账本 15→14

已提交（未推送）`f039051`（11 文件）：
- `NewRevisionStore(prefix string)` → `NewRevisionStore(runtime *appenv.Runtime)`，core 内部 `query.BindNames(runtime.TablePrefix()).PolicyRevisions.String()` 自绑表名（消掉 `prefix + logical` 拼接 → 账本键 `1c5dc7` 消失）。
- 跨模块调用者（governance/identity/organization repository.go）改传 runtime，不再把部署前缀当业务参数递进 core（D41 明确的泄漏点）。
- 保留内部 `newRevisionStoreForPrefix(prefix)` 供 app 层统一擦除路径（`internal/app/principallifecycle.go:31` 的 `erase func(ctx, tx, prefix string, id, now)` 是 8 模块共享类型，签名零涟漪）。
- 验证：core/gov/identity/org/authn 串行测试全 ok、门禁绿、语义幂等 True。账本 15→14（手工下调）。
- 注意：本沙箱 `go test` 多 pattern 并发行会 setup failed（环境假象），单 pattern 串行正常；账本幂等用「语义比对」（budget+entries）而非 md5（门禁写入器自身格式化不同）。

## 2026-09-13（续）D41 第 2 步 Part B：9×prefix() 归位 —— 账本保持 14

6 子代理并行 + 主代理集成，已提交（未推送，Part B 随本文档一笔）：
- **9 个模块 Repository 的 `prefix()` 方法全部删除**（audit/authn/core/federation/governance/identity/oauth/organization/provisioning），改为构造期一次绑定：`names query.TableNames` 字段 + `query.BindNames(runtime.TablePrefix())`；`query/table.go` 新增 `BindWithNames(db, names)`（`Bind(db, prefix)` 委托它），`tables()`/resolver 入口从 `r.names` 取已绑定名。core 的 `MembershipChecker.prefix()`、`Authorizer.prefix`、`tenantAdministratorGuard.prefix` 一并替换为 names。
- **集成修复（主代理）**：`names query.TableNames` 是存储类型，直接做导出结构体字段触发 public-storage-type 门禁 → 嵌入**非导出** `repositoryTables{names query.TableNames}`（core 共享一份，其余模块各一份），`r.names` 经字段提升访问；构造函数 `repositoryTables: repositoryTables{names: query.BindNames(runtime.TablePrefix())}`。
- **验证（最终状态全绿）**：gofmt 全树 **clean**（先 `gofmt -w` 8 个未对齐文件）、`go build ./...` exit **0**、9 个 IAM 模块 `-count=1` 串行测试 **AGGREGATE=0**、`tests/architecture` 门禁 **ok 3.6s**。
- **账本 14 不变**（SQL 文本逐字节不变）：1 concatenated（c71210×1）+ 5 handwritten-sql（61b570×8 / 62c6ad×2 / 19680d / 8f8cec / 9b0891），6 条目。剩余唯一可降项是 goose 版本表（principallifecycle.go:47，B3 例外，需 goosex 访问器）。
- 文档同步：census §5.4 清理链路改写为已全部执行 + 第 2 步记录（本文档同笔）。

## 2026-09-13（续）D41 第 2 步 Part B 后续：governance reviewCounts 改写 —— 账本 14→13

用户拍板继续清理，唯一可再降项 `reviewCounts` 已改写（未提交）：
- 原语句：Raw + `FROM prefix+name` 拼接 + 3×`SUM(CASE WHEN decision=...)`（条件聚合 gen 表达不了）。改为 gen DO：`Select(ReviewID, Decision, ReviewID.Count().As("n")).Group(ReviewID, Decision)` + **Go 侧 fold**：每 (review, decision) 桶的 `N` 累加进 total、按 decision 分桶 pending/kept/revoked。`COUNT(review_id)` ≡ `COUNT(*)`（迁移里 `review_id BIGINT NOT NULL` 钉死）。返回 map 形状与调用方（`ListReviews` 的 `counts[row.ID]` + 零值兜底）完全不变。
- 踩坑：`accessReviewItemDo.Scan` 返回 `(err error)`（裸 error，非 gen.ResultInfo 链）——第一版写成 `.Scan(&rows).Error` 编译报 "err is a func value, not called"（`.Error` 命中 error 接口方法值），改为直接 `if err := ...Scan(&rows); err != nil`。
- **验证（实测）**：`go build ./...` 0、`go test -count=1 ./internal/iam/governance/` ok、架构门禁全绿（4.2s 全包）；语义等价由既有行为断言证明（review_test.go 经 `ListReviews` 断言 Total/Pending/Kept/Revoked），未新增测试。
- 账本：`QUERY_GATE_UPDATE=1` 重算 → `handwritten-sql:62c6ad` **2→1**，budget 手工下调 **14→13** = 6 条目和（c71210×1 + 61b570×8 + 62c6ad×1 + 19680d/8f8cec/9b0891 各×1），幂等、门禁绿。
- 剩余唯一可降项：goose 版本表（principallifecycle.go:47，B3 例外，需 goosex 访问器）。

## 2026-09-14 收尾：两处文档订正 + 状态文件失真订正

用户问「还差什么」，追问「为什么需要我拍板」后复查发现：**此前挂在 `## Next Action` 的两件
「需用户决定」，一件是误判、一件是没查**。两者都不是产品分歧，正确目标都能在仓库里查到：

- `iam-release-checkpoints.md:109` 的「见 decisions D36」→ **D35**。D35（`decisions.md:1179`）
  正是记 `/iam/me/permissions`（「个人权限选择复用当前注册表及CheckBulk」）与平台停用/恢复
  （「平台显式pending→processing…原子停用/撤销凭据，覆盖零租户账号」）的那条决策。
  **此前记的「查不出原意」是没查** —— 原意就在相邻的那条决策里。
- `todo-module-design.md` D1「无归属、无租户」→ 加「**已被 D17 §10.4 取代**」标注。
  **此前记的「属产品决策」是误判**：D17 §10.4（`decisions.md:508`）已白纸黑字记着
  「用户 2026-09-05 确认以 `internal/todo` 作为数据权限的参照实现」。决定早就做过并入账，
  只是那份设计文档没加被取代的标注。D1 原文保留不改写。
- 同时订正状态文件自身三处失真：①「28 个提交未推送」—— 实测 `IAM`/`origin/IAM` 为 `0 0`；
  ② `Overall Progress` 的 P6/PD 两个复选框没勾（均已交付），且 P6 的「目标 385」早被
  后续清理甩开（实测 budget **13**）；③ `Next Action` 记的 budget 46 已过期。

**验证**：本轮是纯文档改动，`docs/*.md` **无任何门禁覆盖**（已核实 Makefile 无 md lint 目标，
`iam-preflight` 与各测试均不引用这两个文件名），故未跑 `make ci` —— 它不消费 markdown。
实际验证方式：diff 逐行审查（2 文件 / 9 增 2 删，行尾 LF 未变）+ 新增陈述逐条对照源码：
`internal/todo/model` 确有 `tenant_id`/`entity_id`/`department_id` 三列、`http.go` 确有
9 处 `authz.RegisterEntity`、D35 与 D17 §10.4 原文均已核。

## 2026-09-14 压测任务:先跑基准,第一步就撞出已推送的编译中断

用户要求「压测一波」,先跑现成基准(`TestIAMPerformanceExternalDatabase`,仓里唯一现成的性能
机制;真实并发压测仓库里没有,是第二阶段的新工作量)。

**第一次跑直接 FAIL —— 不是环境问题,是测试包编译不过:**

```
internal\app\principallifecycle_test.go:138:52: cannot use f.prefix (variable of type string)
  as *appenv.Runtime value in argument to core.NewRevisionStore
```

**根因**:`f039051`(D41 第 2 步 Part A,NewRevisionStore 改收 `*appenv.Runtime`)改了 11 个文件,
**漏了两个测试调用点**,两处都把 `string` 当 `*appenv.Runtime` 传:

| 文件 | 后果 |
|---|---|
| `internal/app/principallifecycle_test.go:138` | `internal/app` 测试包编译不过 → **性能测试就在这个包里,基准根本跑不起来** |
| `tests/migration/iam_core_test.go:184` | `tests/migration` 编译不过 → 迁移门禁跑不了 |

**当前 HEAD(`05df3dd`)上 `make ci` 与 `make test-mysql8-run` 都是红的。已推送到 origin。**
之所以漏掉:`f039051` 记录的验证范围是「core/gov/identity/org/authn 串行测试全 ok」——
`internal/app` 与 `tests/migration` 不在其中,而查询门禁只查 SQL 账本、不编译测试包。
**因此本文件此前「make ci 在最终状态上绿过」的说法对 HEAD 不成立**,最后一次全绿早于 `f039051`。

**已修**(最小改动,沿用仓内既有范式 `core/revision_test.go:28`):
两处改为 `NewRevisionStore(appenv.NewRuntime(appenv.NewIdentity("<name>", "1"), appenv.WithTablePrefix(<prefix>)))`,
app 侧补 `pkg/appenv` import。`ValidatePrefix("")` 放行空串,故 fixture 的 `prefix = ""` 路径不受影响。

**验证**:`go vet` 两个包均 OK、gofmt 干净;`TestComposedPrincipalErasure`(sqlite 路径,
正好覆盖 138 行)**跑过 ok 5.7s**。`tests/migration` 的 `TestIAMCoreServiceBehaviorOnPostgres`
走 embedded-postgres(本地二进制,非 Docker),**尚未跑**,待基准结束后补。

**教训(与「账本记录的是存在,不是正当」同类)**:改签名的机械替换,验证集必须是
**能编译该符号的全部包**,不是「我改过的那些包」。`go build ./...` 不编译测试文件,
所以它绿不代表测试包能编译;要抓这个必须跑 `go vet ./...` 或 `go test`。

### 基准结果(修复后重跑,PASS)

真实 MySQL 8.4,5 样本中位数,对比 2026-09-04 冻结基线:

| 热路径 | 基线 | 现在 | 变化 | 阈值 | 余量 |
|---|---|---|---|---|---|
| audit_append | 3.12 ms | 3.12 ms | +0.1% | 11 ms | 3.5x |
| authorization | 2.15 ms | 2.08 ms | −3.4% | 7 ms | 3.4x |
| session_lookup | 3.14 ms | 2.93 ms | −6.6% | 10 ms | 3.4x |
| **scim_bulk_25** | **50.77 ms** | **78.03 ms** | **+53.7%** | 154 ms | 2.0x |

14 条 SQL 计划指纹与基线**逐条一致**(所以语句形态没变)。前三条稳定。
**scim_bulk_25 慢 54% 是真漂移不是噪声**:基线 5 样本 48.0–51.3ms、现在 73.3–83.2ms,两组不重叠。
**根因未查,不猜。** 候选方向:同一组语句形状下执行次数变多(需比对 SQL capture 的次数,或 profile)。

### 新发现:延迟阈值根本没在执行

`enforceIAMPerformanceThresholds` 只在 `IAM_PERFORMANCE_UPDATE=plans` 或
`IAM_PERFORMANCE_ENFORCE_THRESHOLDS=1` 时才调用(`bootstrap_iam_performance_integration_test.go:220`),
而官方门禁 `scripts/test-mysql8.sh` **两个都没设** —— 它只设 `IAM_PERFORMANCE_DATABASE_TYPE/DSN`。
**所以 `make test-mysql8-run` 实际只校验 SQL 计划指纹,完全不校验延迟。**
scim 这 54% 之所以没拦住,不是阈值松,是阈值没跑。**待定:要不要在门禁里打开 enforce。**

### 第二阶段:真并发压测(用户已定方向)

**用户选定:指向远程 MySQL 8.4**(不是 SQLite)。已确认 `make dev` 在 `--debug` 下用 SQLite
(`internal/app/debug.go:11-23`,无 database 配置时落到 `sqlite.db`),压它没有生产参考价值。

待建:压测载荷 + 驱动。设计要点(未开工):
- 走 HTTP 全栈(routing/middleware/auth 都要真实),复用既有 fixture 造数 + `httptest.Server`
- 外部库走既有 env 契约(同 `TestComposedPrincipalErasureExternalDatabase` 的形态)
- 输出 RPS、p50/p95/p99、错误率

### 第二阶段完成:HTTP 并发压测已建成并跑通

**新增**(均为增量,未改任何生产代码):
- `internal/app/bootstrap_iam_load_integration_test.go` —— 走 `httptest` 的真实全栈
  (路由/中间件/认证/授权/数据库),复用既有 fixture 造数。4 条场景。**不设延迟断言**:
  共享机器上的延迟不是稳定断言,会抖的门禁最后都会被关掉;只断言「无传输错误、无 5xx」,
  以及 warm-up 必须命中预期 2xx(否则会静默地压一个 404 路径还当作容量)。
  **不设环境变量则 skip**,不影响 `make ci`。
- `scripts/load-mysql8.sh` —— 照 `test-mysql8.sh` 形态起一次性 MySQL 8.4 容器并驱动。
  容器每次新建销毁,保证可重复:复用的库会带着上一轮留下的东西。
- `Makefile` 加 `test-load`。**刻意不进 `ci` 与 `release-check`** —— 它是量测不是门禁,
  且数字依赖机器。`IAM_LOAD_CONCURRENCY` / `IAM_LOAD_REQUESTS` 可调。

**实测(真实 MySQL 8.4,16 并发 × 300 请求 × 4 场景 = 每场景 4800 请求,全部 200,零失败):**

| 场景 | 吞吐 | p50 | p95 | p99 | max |
|---|---|---|---|---|---|
| authorization_check | 705 req/s | 21 ms | 29 ms | 58.1 ms | 154.7 ms |
| roles_list | 1581 req/s | 9.0 ms | 12.5 ms | 27.5 ms | 97.0 ms |
| entities_list | 1572 req/s | 9.0 ms | 12 ms | 32.5 ms | 103.5 ms |
| tenants_list | 4190 req/s | 3.5 ms | 4.5 ms | 11.5 ms | 40.2 ms |

**必须随数字一起讲的局限**:压测进程与服务同进程(`httptest`),生成器与服务抢同一份
CPU,所以这些是**下界**,不是容量上限;且库在局域网远端的 10.0.0.100 上。
要绝对容量得把服务独立部署出去再压,那是另一件事。

**过程中的两个错误(都靠实跑才暴露)**:
① `iamLoadIssue` 原本收 `t` 并调 `t.Helper()` —— 它在工作协程里跑,Go 的 `*testing.T`
不允许在非测试协程调用。改为完全不收 `t`,错误以返回值交回测试协程断言。
② `load-mysql8.sh` 抄 `test-mysql8.sh` 时漏了 `--log-bin-trust-function-creators=ON`,
迁移建函数直接 1419 失败。**抄脚本要逐行对,不能凭印象。**

## 2026-09-15 时延门禁打开(用户拍板)

**改动一行**:`scripts/test-mysql8.sh` 的性能步骤加 `IAM_PERFORMANCE_ENFORCE_THRESHOLDS=1`(+6 行注释)。

**这不是新主张,是补上 D13 早就定的事。** D13 的 Tradeoffs 白纸黑字写着「发布机需要显式启用
时延门禁」,但那个变量从来没人设过 —— 所以 `make test-mysql8-run`(`release-check` 的一环)
长期**只校验 SQL 集合与计划指纹,时延量而不判**。我此前把它讲成「我的建议」,不准确。

**验证(两步,缺一不可)**:
1. **开启后 PASS**:真实 MySQL 8.4,`--- PASS (33.92s)`。
2. **变异验证(关键)**:把 `scim_bulk_25` 阈值临时降到 60ms → 门禁如期 **FAIL**,
   报 `"83006957" is not less than or equal to "60000000" / scim_bulk_25 exceeded the
   same-machine frozen threshold`,5 个样本逐条报;基线由 trap `git checkout` 还原,
   `git status` 已确认干净。**证明它是活的开关,不是设了个没用的变量。**

**作用域已确认不泄漏**:`VAR=x cmd` 是前缀赋值不 export;`make ci` 里 `tests/performance`
的 PostgreSQL 那条用 `os.Environ()` 继承,拿不到它。与 D13「跨机器默认运行不因纯时延失败」
一致 —— PostgreSQL 阈值是亚毫秒级(authorization 0.8ms),在 CI 机器上开必然抖,**刻意不动**。

**当前余量(开启后的真实位置)**:authorization 3.53x / audit_append 3.47x /
session_lookup 2.83x / **scim_bulk_25 仅 2.04x**。D13 设计的是「最大值 3 倍」,
scim 这条已被真实变化吃掉近三分之一。详见 decisions.md 的 D13 Amendment。

**SCIM 那 54% 的根因(已查清,未处置)**:不是性能退化,是 `c7a02b1` 给 `Bulk` 加了鉴权
(`authorizeSCIM`:读凭据行 + 校验租户/目录/主体 + 服务账号校验,每次调用多 3~4 次数据库往返)。
`bulk.go` 两版之间只多了那 3 行。A/B 实测:基线提交 `3277e5e` 的旧代码今天同机重跑 58.56ms、
HEAD 78.03ms;同轮四条路径里另外两条更快、一条持平,故非全局退化。**这是安全的成本,不是缺陷,
建议不动。** 未做隔离实验(临时旁路鉴权再测),故「100% 由它造成」未证,只证了量级相符。

## 2026-09-15 覆盖率门禁:当前 HEAD 的 make ci 是红的 —— 两个独立问题

用户问「IAM 生产就绪了吗(只说 Go 代码功能逻辑)」。在当前 HEAD 实跑 `make ci`:
**CI_EXIT=2,零测试失败**,卡在 `total coverage: 89.8% (minimum 90.0%)`;后面的
test-integration / build / openapi-check 根本没跑到。

**注意:后台任务报的 "exit code 0" 是包装 echo 的退出码,不是 make 的。**
与之前 `tee` 掩盖退出码同一个坑 —— 判定必须读日志里的 CI_EXIT。

### 问题 A:总覆盖率失败,是门禁自己的 bug(已修)
`internal/*/query/gen.go` 由 gorm.io/gen 生成(头部 `DO NOT EDIT`),与同目录 14 个
`.gen.go` 同一生成器。但过滤器写的是 `/\.gen\.go:/` —— `gen.go` 前面是斜杠不是点,
**匹配不上,于是 297 条纯生成语句(覆盖率 4.4%)被算进了总覆盖率**。
- 修法:再排除 `/\/query\/gen\.go:/`。**按全路径而非文件名排除** ——
  `pkg/extension/gorm/gen/gen.go` 是**手写的构建工具**(差点误伤,已核实其头部注释)。
- 验证(用真 awk 跑现有 profile,非推理):总覆盖率 **89.8% → 90.6%**,
  `make -n test-coverage` 解析正常,手写的 gorm/gen 仍在 profile 里未被排除。
- 注释按房屋风格放在 target 上方(recipe 内夹注释在 Make 里有风险)。

### 问题 B:两个模块门禁是真缺口(子代理处理中)
模块门禁用**未过滤**的 profile 且按目录精确匹配,`query/` 是另一个目录 ——
**A 的修复对它们零影响**,已用门禁自己的 awk 实测确认仍是 88.9% / 89.5%。

| 模块 | 现状 | 需补 | 未覆盖构成 |
|---|---|---|---|
| organization | 88.9% | +26 条 | 错误传播 131 / **真行为 121** |
| provisioning | 89.5% | +15 条 | 错误传播 143 / **真行为 144** |

**真行为那半里有该测没测的东西**(这才是补测试的理由,不是凑数):
- provisioning:`filter.go:46` SCIM 过滤器 `depth>16||nodes>64` **DoS 防护**、
  `field_policy.go:14` `actor<=0→ErrUnauthorized` **授权守卫**、`bulk.go` 7 处
  `strictDecode→ErrInvalidSCIM` 畸形载荷校验、`bulk.go:157` DELETE 带 body 必须拒。
- organization:**乐观锁冲突路径全线没测**(`group_repository.go:90/127`、
  `group_service.go:106/174`、position 侧对应处)—— 即「并发编辑必须可检测」这个
  设计目标在 group/position 上没有验证;另有唯一名/岗位编码冲突、邀请状态守卫。

**错误传播那半(两模块合计 274 条)不主动追**:全是 `if err != nil { return err }`。
仓里有现成套路(`governance/review_test.go:398-414` 的 `DropTable` + `ErrorContains`,
authn/governance 正是靠它到的 90.3%/92.9%),它能抓「吞掉 DB 错误返回空列表」这类真 bug,
**但纯按它凑数就是为覆盖率写测试**。仅在 P0 补完仍不够线时才用。

### 执行方式
Hybrid:两个包互相独立(不同目录/不同文件),两个子代理并行补测试;
主代理自己改门禁 filter,并保留集成与最终验收。

### 结果:make ci 全绿(2026-09-15,我自己跑的,非采信子代理)

```
gofmt: clean
total coverage: 90.8% (minimum 90.0%)
module coverage: internal/iam/organization    90.3% (minimum 90.0%)
module coverage: internal/iam/provisioning    90.6% (minimum 90.0%)
openapi: matches docs/openapi.json
CI_EXIT=0
```

三笔提交:`7b9a233` 门禁 filter / `e611be2` provisioning 测试 / `5f96ae7` organization 测试。
**生产代码零改动**(`git status` 核过,改的全是 `_test.go` 加一个 Makefile)。

**复核方式(没有只看覆盖率数字)**:
- 逐个读了关键测试。SCIM 复杂度上限用两组互相隔离的输入(17 层 vs 127 节点)分别证明两半守卫,
  且各配限内反例防止退化成一律拒绝;授权守卫断言「拒绝 + 根本不去问下游 checker」;
  乐观锁测试断言「赢家值幸存 + version 未变 + **policy revision 未被推进**」。
- organization 子代理自己做了变异验证(5 个守卫逐个改坏 → 5/5 变红 → 还原),已在报告中给出证据。
- 两个包 `-shuffle=on` 均 ok,新测试不依赖执行顺序。
- 核实了子代理声称的「不可达分支」:`scim2/filter-parser` 的 `Expression` 确为密封接口
  (`ast.go:109` 的未导出 `exprNode()`,包内恰 4 个实现),其 type switch 的 default 确实到不了。
  **声明属实,未被糊弄。**

**遗留(如实记录,未处置)**:四条不可达的防御分支仍计入分母
(`provisioning/bulk.go:204`、`provisioning/field_policy.go:143`、`organization/invitation.go:191/236`)。
两模块合计仍有约 500 条未覆盖,绝大多数是数据库错误传播;按既定判断不追。

## 2026-09-16 release-check:7 关跑了 5 关全绿,2 关被环境挡住

用户问「IAM 生产就绪了吗」后要求跑发布门禁。**`make release-check` 无法整体执行**
(其组成里有本机跑不了的关),故逐关执行。HEAD `d98cea3`,工作树全程干净。

| 关卡 | 结果 |
|---|---|
| `ci` | ✅ CI_EXIT=0(总覆盖 90.8% / org 90.3% / prov 90.6% / openapi matches) |
| `frontend-check` | ✅ FRONTEND_EXIT=0(lint/typecheck/**148 测试 0 fail**/build) |
| `test-race` | ✅ RACE_EXIT=0,**零 DATA RACE,零 FAIL** |
| `test-mysql8-run` | ✅ MYSQL8_EXIT=0,**schema lock 219 个迁移文件未变** |
| `generate-check`(手工等价) | ✅ `make generate` 后 `git status` 全空,连未跟踪生成物都没有 |
| `test-alerts` | ❌ **本机无 `promtool`** |
| `test-redis-security` | ❌ **脚本结构性不支持本机** |

**时延门禁首次在真门禁里执行并通过**(我 2026-09-15 加的 `IAM_PERFORMANCE_ENFORCE_THRESHOLDS=1`
生效了):scim_bulk_25 中位数 **84.9ms** / 阈值 154ms —— 注意**比前几次(75–78ms)高**,
余量收窄到约 1.8 倍。样本间波动比想象的大,D13 Amendment 记的 2.04x 应理解为「那一次的值」。

**`test-redis-security` 是结构性跑不了,不是主机故障**:`scripts/test-redis-security.sh:5` 用
`command -v docker` 要求**本机 PATH 上有 docker**,且全程裸 `docker` 调用,不接受 `DOCKER_CMD`。
本机是走 SSH 驱动 10.0.0.100 的远程 docker。要跑就得让该脚本像 `test-mysql8.sh` 一样接受
docker 命令参数 —— **属新工作,未做,未擅自改脚本**。

**过程中的环境插曲**:10.0.0.100 一度 ping 通但 22 端口超时(sshd 未接受连接),用户恢复后正常。
期间**未**使用 `make iam-preflight --bootstrap-docker` 去救 —— Makefile 明写
「release verification must not bootstrap or change a remote Docker installation」。

## 2026-09-16 release-check 首次端到端通过 —— RELEASE_EXIT=0

用户要求打通此前卡住的两关。做完后 **`make release-check` 七关首次能整体执行并全绿**:

```
gofmt: clean
total coverage: 90.8%   organization 90.3%   provisioning 90.6%
openapi: matches docs/openapi.json
race: 162 包 ok / DATA RACE 0 / FAIL 0
promtool test rules: SUCCESS
schema lock: 219 published migration files unchanged
Redis mTLS/ACL + Cluster + Sentinel: PASS ×2(明文轮 + 全 mTLS 轮)
RELEASE_EXIT=0
```

### promtool(未改仓库)
官方 3.14.0 二进制,SHA256 与发布的 `sha256sums.txt` 逐字节吻合,放进 GOPATH/bin。
**刻意不进 `tools/go.mod`**:实测 `go get` 会拖进 Kubernetes 客户端库(go.sum +367 行),
为一条告警规则单测不值;且 Makefile 的 `PROMTOOL ?= promtool` 本就设计为来自环境
(buf/golangci-lint 才在 tools 模块里),按既有约定办。

### test-redis-security:四个独立的移植缺陷,逐个实测才暴露
原估「让脚本接 DOCKER_CMD,约一小时」**严重低估**。实际:
1. **bind mount 送不到远程 daemon**(`src=` 在 daemon 那台机器解析)→ `tar -cf - . | docker cp -`,fixture 走 stdin
2. **70 行内联脚本被 SSH 中间 shell 重切** → `sh -eu` 从 heredoc 读 stdin(正是 `test-mysql8.sh:76-80` 记录过的洞见)
3. **MSYS 把 `-subj /CN=x` 改写成 `C:/Program Files/Git/CN=x`** → `MSYS2_ARG_CONV_EXCL='/CN='`
   (不能用 `'*'`:那会连文件路径一起排除,原生 openssl.exe 就写不了 MSYS 路径了)
4. **Windows openssl 写 CRLF**(重定向流与 `-out` 文件都是)→ 容器里 `\r` 留在密码中 → `WRONGPASS`;
   在**构建二进制之前**归一化文本 fixture(用 `cat >` 回写以保留 `chmod 600`)
5. **Windows 无执行位** → bind mount 时 Docker 补 0755,tar 忠实保留「没有」→ 容器内 `chmod +x`

**容器内那 70 行测试逻辑逐字节未改**(已用 diff 证明),只动传输层;无参数时仍默认 `docker`,
本地 docker 用户路径不变。

### 顺带修好 generate-check(此前被记为「本机跑不了」)
根因不是玄学:`$(MAKE)` 未加引号,展开成 `C:/Program Files (x86)/GnuWin32/bin/make`,
shell 遇到空格与括号直接语法错误。同文件 `test-mysql8` 那处本就写作 `@"$(MAKE)"` ——
按同文件既有写法加引号即可。**本文件此前记录的「generate-check 在本机跑不起来」现已作废。**

### 我自己犯的错(写下来防再犯)
用 heredoc 写 `tr -d '\r'` 时反斜杠被吞一层,**写进脚本的是一个不可见的裸 CR 字节**。
它看起来像能用,实际失效 —— WRONGPASS 正是它造成的,换成正规 `\r` 才通。
**经 heredoc 写含反斜杠的内容后,必须用 `cat -A` 之类验证真实字节**,不能看渲染结果。

## 2026-09-16 Swagger/OpenAPI 契约审计(用户此前问「接口审计没问题了」)

推送完成(远端 `refs/heads/IAM` = `602c6a5`,0/0)后开始。**这是契约层审计,不是全仓 deep-audit。**
范围:192 paths / 274 operations / 358 schemas。

### 干净的(实测,非印象)
- **零个** operation 缺 `operationId` / `summary` / `tags`
- 31 条非信封 2xx 响应**全部有外部规范依据**:OIDC discovery、JWKS、SAML metadata/SSO/SLO、
  文件下载(审计导出/归档)、RFC 7662 introspect —— 包信封反而是错的
- 26 个匿名端点**逐条核过**:`.well-known`、SAML/OIDC 回调、登录前的 captcha/注册/找回密码等,
  全部是合理的免鉴权面
- 分页命名两套是**规范要求**,不是分歧:IAM `limit/offset`,SCIM `startIndex/count`(RFC 7644)
- 错误码使用面一致(401/403 各 ~250、500/503 各 ~262)

### 发现 1:DELETE 状态码,参照模块与主体不一致
- IAM 全部 **36 条 DELETE 返 200 + 信封体**(如 `EnvelopeMapStringBool`)
- **`/todos/{id}` 返 204 无体**;SCIM 两条返 204 是 **RFC 7644 强制,正确**
- 问题在于 **todo 是"打样"模块**(其设计文档自述用途是给 catalog/order 照抄),
  照它写的新模块会与 IAM 主体分歧。两种都是合法 REST,**但参照实现与主体不该不一致**。

### 发现 2:31 个列表端点无分页,随租户数据无限增长
53 个返回数组且无分页参数的 GET,分类后:
- A 设计上有界 14(健康检查、静态权限/菜单目录、由路径参数限定)
- B 限定在调用者自身 9(`/authn/me`、`/iam/me/*`、自己的会话与 passkey)
- **C 随数据增长 31** —— 真正的发现。含 `/iam/tenants`(平台级)、
  `/iam/roles/{role_id}/members`、`/iam/groups/{id}/members`、`/iam/positions/{id}/members`
  (成员列表随租户规模增长)、`/iam/departments`、`/iam/groups`、`/iam/invitations` 等

**已实证到实现层**:`/iam/roles/{role_id}/members` → `core.ListMembers` →
`repository.go:460` 的 `.Find()`,**无 `.Limit()`**,返回全部行。
而 `/iam/roles`、`/iam/entities` 是有 `limit/offset` 的,且全仓用了 **317 处 `.Limit()`** ——
**所以这是既有模式没被用上,不是缺少模式。** 性能基线的工作负载正是 64 租户 × 64 成员。

### 方法论教训(与门禁那次同源)
第一版检测器报「0 个无分页列表端点」,**是假的**:schema 里 `data` 的 type 是 `["array","null"]`,
我按 `== "array"` 判断,永远为假。**先用已知的分页端点自检检测器**才发现。
「检测不出来」与「没有问题」是两回事 —— 任何扫描在报告结论前必须先证明它能检出已知实例。

### 未做
信封内 `code` 字段的语义一致性、各端点错误码与业务语义的逐条对应、schema 命名语义、
向后兼容策略。这些属 `deep-audit` 的范围,未展开。

## 2026-09-16/17 列表端点有界分页铺开(D42)

用户选定「有界默认 + 前端配套」并要求「不需要拍板的别停,全部做完」。样板 `aabee75` 之后铺开。

### 后端:25 条端点,7 个模块
core 8(含 `/iam/roles`、`/iam/entities` 由 `default:"0"` 订正为有界)、organization 7、
provisioning 3、governance 3、federation 2、oauth 1、audit 1。
一律**新增 `XxxPage` 变体,既有非分页方法逐字节未动**(内部调用方不受影响)。

### 契约变更:严格验证为纯增量
`operation 274 → 274`、`schema 358 → 358`、**响应体被改 0 处、响应码被删 0 处**。
印证样板的发现:`respx.OK → respx.List` 不改 schema(同一个 `Envelope[T]`,`List` 只填 `meta.page`)。

### 前端:统一翻页,行为不变
`iam-api.ts` 抽出 `requestAllPages`,改造 **19 个消费点**。服务端有界、客户端翻完,
**需要完整集合的界面行为零变化**,无 UI 改造。契约断言更新 12 处。

### 三个子代理,一个失败
- **core 的子代理中途 503 挂掉**,停在"准备做变异验证"——**代码写了没验证,且编译不过**
  (`http.go:798 declared and not used: total`)。主代理接手修完。
- **它误判了一个端点**:`/iam/roles/{role_id}/data-scope` 返回**单个对象**(角色数据范围配置),
  不是列表。对它分页 = 分页嵌套的 `department_ids` = **返回一份错误的配置**。已撤销该改动、
  其 Page 方法、测试用例与孤儿辅助函数。
  **根源是我最初审计的检测器把「data 里含数组字段」当成列表** —— 是我的误判,不是子代理的。
- **补做了失败代理没做的变异验证**:旁路 6 处 limit 守卫 → **5 个测试变红**,还原后恢复。

### 子代理顺带发现的两个真问题(均已被 Page 变体取代,原方法未动)
- `governance.List` 硬上限 `Limit(200)`、`ListReviews` 硬上限 `Limit(100)`,**且无 offset ——
  调用方根本够不到第 201 行**。
- `organization.InvitationService.List` 有个**静默的硬编码 `Limit(200)`**,且不返回 total。

### 已知限制(如实记录)
- **两条端点只有「响应有界」,数据库读取仍无界**:`/iam/departments`(树序与 `Depth` 依赖全部祖先)
  与动态用户组成员(成员是 Go 侧规则判定,窗口和总数在规则跑完前不存在)。二者在计算后分页。
- **8 个非分页方法现已无生产调用方**(仅测试在用),因为我们采取"新增变体"而非"改既有方法"。
  **未删除** —— 删除公开方法本身是一次独立的 API 变更,且会牵动覆盖率。留作后续。
- `/iam/audit/governance` 不是列表端点,子代理对它的 `Anchors` 链做了有界化并保留了
  `report.Anchored` 的整链语义。形态特殊,已复核。

### 验证
`make ci` **EXIT=0**:gofmt clean、总覆盖率 **90.8%**、全部 30 个门禁模块 ≥90%、openapi 匹配。
前端 typecheck 通过、**148 测试 0 fail**。lint 首轮红(英式拼写 6 处 + 我删测试后的孤儿辅助 1 处),
已修,复跑绿。

## E2E Verification - 2026-09-17

- Request: run E2E tests. Execution: Direct; no business code or assertions changed.
- Revision: `20a8c785de7abb88b07ea3ffab526c44e1518a9f`.
- Build: PASS, `go build -trimpath -o .temp/e2e-current/api.exe ./cmd/7link-api`
  from `apps/backend-api`. The existing UAT binary/state were not overwritten.
- Real-backend E2E: BLOCKED. Existing isolated UAT MySQL endpoint
  `10.0.0.100:32769` actively refused TCP connections on two checks.
  Docker CLI is unavailable on this Windows host; the documented initializer
  requires macOS/Linux and a local Unix-socket Docker context. No database reset,
  migration, remote container change, or production acceptance was performed.
- Frontend browser regression: 60 distinct scenarios PASS, 1 FAIL; real Chrome
  and Vite, mocked API responses. Command from `apps/frontend-web-admin/apps/iam`:
  `node scripts/release-regression-browser-audit.mjs`. Full run passed 19 scenarios
  before failing. Separate runs with `IAM_REGRESSION_FILTER` completed remaining
  groups: `list states` (24 PASS), `personal` (13 PASS), `language switch` (1 PASS),
  `nested error` (3 PASS). `nullable OAuth` independently reproduced the failure.
- Failure: `release-regression-browser-audit.mjs:835` times out waiting for
  `Edit Machine client`. The mock has no `/iam/me/permissions` handler and its
  default response is `[]`; the real page correctly gates edit controls on
  `oauth_client_update` (`src/app/oauth-clients/page.tsx`). This identifies a
  missing test permission fixture, not a verified backend or product defect.
- Self-review: scope preserved; failures not suppressed; no test weakened.
  Acceptance: NOT READY for real-backend E2E or production sign-off.
- Next action: restore the isolated UAT MySQL endpoint, then run HTTP verification
  and `local-uat-browser-audit.mjs --release`; recommend adding explicit OAuth
  update permission to the affected mock scenario after user approval to fix it.

### Authorized E2E Follow-up - 2026-09-17

- User authorized fixes and environment work; MUST NOT commit or push.
- Phase: ACCEPTANCE. All selected E2E suites PASS. No business code changed.
- Fixed the regression mock: explicit `/iam/me/permissions` response, empty
  permissions by default, `oauth_client_update` only for the OAuth edit scenario.
  Full browser regression rerun: 61/61 PASS (exit 0).
- Added optional `IAM_UAT_DIRECTORY` to local UAT and route-matrix browser scripts
  so current-build evidence stays separate from previous UAT state/artifacts.
- Confirmed the remote container name, pinned image, UAT label and existing
  volume before starting only `7link-iam-console-uat-mysql` through the existing
  dedicated SSH key. Restart changed its dynamic port from 32769 to 32768.
  Only `.temp/e2e-current/config.yaml` was updated; original UAT config/state,
  keys, passwords and binaries were preserved. No reset or migration performed.
- Current backend SHA-256:
  `504fdc947267cdf12847946b024ce44e409b2ec972c0154b70877bc74e34fa84`.
- Fixed local UAT's single/default-tenant assumption: discover the configured
  accessible tenant and explicitly choose it through the real workspace UI;
  preserve tenant assertions and all existing checks.
- Real API: 9 checks PASS via `.temp/e2e-current/verify-http.mjs` (exit 0).
- Real browser: `local-uat-browser-audit.mjs --release` PASS (exit 0), 4
  persona/device combinations and 6 additional child/OAuth flow results.
  Reports and screenshots: `apps/backend-api/.temp/e2e-current/browser/`.
- Full real-backend route matrix: 792/792 PASS (exit 0): 22 routes x 3 locales x
  2 widths x 3 personas x 2 tenants. Both fixture cleanup results PASS: owned
  synthetic tenant fixtures deleted and synthetic global principals disabled.
- Frontend `bun run typecheck`, final `bun run lint`, `bun run test --force`,
  `bun run build`: PASS (exit 0). Unit tests executed without cache: 148 PASS,
  0 FAIL (ui 85, iam 63). Script syntax, Prettier and `git diff --check`: PASS.
- Reviewed desktop failure evidence and successful mobile dashboard screenshot;
  all route-matrix cells assert no page errors and no horizontal overflow.
- Self-review and final acceptance: PASS for requested local E2E verification.
  No production sign-off, permanent erasure, reset, migration, commit or push.
- Processes started for this run: backend PID 17768 (loopback 18080), Vite
  consoles 18091 and 18094. Both Vite consoles stopped; backend stopped after
  executable-path ownership verification. UAT MySQL stopped to restore its
  initial stopped state; database volume, current-build reports and screenshots
  are retained. No user or unrelated service was stopped.
- Next action: none for requested tests. Changes remain uncommitted in three
  browser scripts plus this task-state record, as requested. To rerun against
  this build, start the owned UAT container, refresh its dynamic port with
  `.temp/e2e-current/prepare.mjs`, start the current API and proxy console, and
  set `IAM_UAT_DIRECTORY` to `apps/backend-api/.temp/e2e-current`.

## Business Workflow E2E - 2026-09-17

- Request: run directory/authorization, governance, SCIM, registration/email
  verification and Passkey workflow audits. Fix failures minimally; no commit
  or push. Execution: Direct, sequential audits sharing the isolated UAT tenant.
- Phase: TESTING. Existing UAT container restarted with preserved volume;
  current backend PID 16588, Vite 18091, notification receiver 18100.
  Build SHA-256 remains `504fdc947267cdf12847946b024ce44e409b2ec972c0154b70877bc74e34fa84`.
- Initial department run failed on a missing create button. Runner only seeded
  tenant ID, not its session subject; fixed by resolving subject through real
  authenticated API and setting both existing storage keys together.
- Directory group running: department then failed during list reload on a
  transient missing row; position/group failed clicking create before effective
  permissions resolved. Added bounded readiness waits, preserved assertions.
- Registration needs remote MySQL readback: added optional existing-convention
  `IAM_DOCKER_SSH` / `IAM_DOCKER_SSH_KEY` support, shell-quotes every remote arg,
  requires batch key authentication and bounds the read-only query to 15 seconds.
- Existing runner now optionally records reports and failure screenshots under
  `IAM_BROWSER_AUDIT_OUTPUT`. No business source changed so far.
- Next action: finish initial directory group, rerun failed audits, then governance
  and protocol groups; inspect evidence, rerun checks and restore stopped services.
  Permanent erasure, production sign-off and database reset remain out of scope.

### Workflow Checkpoint - scope race reproduced and fixed

- Department, position, static/dynamic group, entity, tenant and member
  authorization-source audits have PASS results after bounded readiness waits
  and correct tenant/subject binding on each test account switch.
- Real product defect reproduced: UI role scope returns to "all" after saving
  selected departments, while PUT payload and independent backend GET confirm
  selected departments persisted. Older in-flight detail reads overwrite draft.
- Added controlled delayed-read browser regression in the existing release
  regression script. Before fix: FAIL (`All data` vs `Selected departments`).
  After fix: PASS. Roles page now versions scope edits/saves; read responses
  only initialize the draft if no newer scope edit/save occurred.
- Role workflow progressed through scope persistence, conditional permission,
  direct and directory grants; its next failure was screenshot sampling while
  details showed loading counters. Added readiness wait for the existing expected
  1 permission / 3 assignments, without changing screenshot assertions.
- Frontend typecheck and lint after product fix: PASS.
- Next action: run all 14 audits together with complete member, notification,
  captcha and SSH readback configuration; fix remaining failures and rerun.

### Workflow Checkpoint - integrated rerun in progress

- Initial combined run passed 10/14 workflows; position, access-request and
  access-review passed on retry after readiness/tenant-binding fixes.
- Registration verified email activation and zero tenant memberships through
  real DB readback. Corrected obsolete dashboard expectation to the established
  no-active-workspace screen; added authenticated-session, zero-membership and
  logout checks. Fixed browser Response.ok property use in those checks.
- Full mocked browser regression after scope fix: 64/64 PASS (exit 0), including
  delayed read versus unsaved/saved edits and legitimate untouched refresh.
- Final frontend typecheck, lint, forced unit tests (148) and build: PASS.
- Full 14-workflow final run active (exec session 16466), evidence under
  `.temp/e2e-current/browser/business-workflows-final`; first five workflows PASS.
- Standalone SCIM actor/target audits now bind session subject and test tenant
  using existing storage keys. Not yet verified. For outbound receiver, only
  owned test API may be restarted with CLI flags
  `--provisioning.outbound-allow-plaintext` and
  `--provisioning.outbound-allow-networks 127.0.0.1/32`; production defaults and
  original UAT configuration MUST remain unchanged.
- Next action: finish integrated run, run standalone SCIM audits and roles
  route matrix, review evidence/diff, then stop verified owned services and
  restore initially stopped UAT container. No commit/push/reset/erasure.

### Workflow Checkpoint - Passkey and SCIM safety expectations

- Combined final run passed 13/14; registration and all business workflows PASS,
  Passkey rename failed because refresh still held its button disabled.
- Passkey now waits for enabled controls and uses unique credential names to
  prevent touching earlier failed fixtures. Retry PASS: register, rename,
  authenticate, delete, reject deleted credential; no console errors/overflow.
- Independent SCIM actor audit PASS after explicit zh-CN cookie and session
  subject binding: create/preserve/rebind/conflict reload/clear/tenant switch.
- Roles real route matrix PASS 36/36, cleanup PASS for both test tenants;
  durable report `browser/route-matrix/roles-report.json`. Existing matrix
  `report.json` is latest-run output (36 cells), not the earlier 792-cell run.
- Owned backend restarted from verified executable path, now PID 1480 with
  test-only loopback SCIM CLI allowlist. Original config remains unchanged.
- Outbound audit found obsolete expectation: a 503 intentionally preserves
  the durable uncertain-operation claim and subsequent push returns 409.
  Backend tests/implementation document this safety invariant. Test now covers
  definite rejection (429) followed by safe retry AND 503 followed by no resend
  and no claim deletion. No backend code or DB records manually altered.
- Outbound retry active session 17974. Full integrated green run active session
  20538, output `browser/business-workflows-green`. Final readiness still pending.
- Removed owned temporary roles probe (not a deliverable); retained evidence.
- Next action: finish outbound and integrated suites, inspect screenshots,
  final script/static checks, append actual outcomes, stop owned services and
  UAT container while preserving its data volume and unfinished-operation claim.

### Business Workflow Final Acceptance - 2026-09-17

- Phase: ACCEPTANCE. Final integrated real-backend run PASS 14/14 (exit 0):
  department, position, group, entity, tenant, role, member-authorization,
  invitation, access-request, access-review, service-account, inbound SCIM,
  registration/email verification and Passkey.
- Report: `apps/backend-api/.temp/e2e-current/browser/business-workflows-green/report.json`.
  Earlier failed-run evidence is retained separately, not presented as green.
- Standalone SCIM actor PASS (exit 0); outbound SCIM target PASS (exit 0),
  including wire payloads, ordinary tenant admin permissions, token lifecycle,
  field projection, denials, safe retry after 429, 503 uncertain-operation
  exclusion, version conflict reload, deletion and tenant isolation.
- Roles route matrix PASS 36/36, both tenant cleanup results PASS. Report:
  `apps/backend-api/.temp/e2e-current/browser/route-matrix/roles-report.json`.
- Mocked regression PASS 64/64; frontend typecheck, lint, forced unit tests
  (148 PASS, 0 FAIL), build, all changed script syntax, Prettier and diff checks
  PASS. Final lint/format rerun includes standalone SCIM and Passkey edits.
- Inspected role-scope and outbound-editor mobile screenshots: no overlap or
  overflow; persisted selected department is visible, stored bearer token is
  absent from editor. Browser audit assertions pass. Vite dev-server logs also
  contain non-fatal ResizeObserver notification-loop warnings during resizing;
  these are not counted as business failures or production acceptance.
- Product change is limited to roles page scope-draft version guard. Added
  three focused regression scenarios; test scripts fixed stale assumptions,
  real session/tenant/locale setup, disabled-button races and fixture ownership.
- Self-review PASS: scope/reuse/architecture/complexity/regression/durable state.
  Original-request acceptance PASS for local E2E testing; execution Direct.
- Owned API PID 1480 stopped after executable-path verification; Vite session
  13553 and notification session 26913 stopped. Ports 18080/18091/18100 have no
  remaining listeners. Owned labeled MySQL stopped, final status `exited`,
  restoring initial state while retaining its volume.
- Deliberately retained isolated synthetic records: standalone audits do not
  clean their tenants/principals/service accounts; uncertain 503 targets keep
  their protective operation claims, including the earlier failed test target.
  No manual claim release, permanent erasure, reset, migration, production
  configuration change, staging, commit or push performed.
- Final Git diff check PASS; staged diff empty. No further test or implementation
  work remains; local changes await user review, not auto-commit.

### Interactive Preview - 2026-09-17

- User requested running the app to inspect it in the browser. Execution Direct;
  reused current UAT binary/config and preserved database, no commit or push.
- Verified isolated MySQL container label before starting it; refreshed dynamic
  port using existing ignored prepare helper. Original UAT configuration remains
  unchanged. API restarted without outbound SCIM test-only network overrides.
- Running owned API PID 18712, notification receiver PID 7064, Vite exec session
  93672 on 18091. Browser helper PID 6312 opened visible dedicated Chrome,
  authenticated existing admin and bound existing test tenant; no secrets logged.
- Preview URL: `http://localhost:18091/`. Frontend GET and proxied `/api/readyz`
  both HTTP 200; browser helper confirmed authenticated homepage and saved local
  `apps/backend-api/.temp/e2e-current/preview-desktop.png` screenshot.
- Added only ignored local browser-launch helper `open-preview.mjs`; no business
  source changed. Services intentionally left running for the user's inspection.
- Next action: user inspects browser. Stop only these verified owned services
  and restore stopped container when preview is no longer needed; retain DB data.

### Account Display Fix And Authorized Delivery - 2026-09-17

- User requested fixing the numeric account label, then explicitly authorized
  committing and pushing ALL local deliverable changes. This supersedes the
  earlier no-commit/no-push constraint; ignored credentials/artifacts stay local.
- Reproduced: unscoped real session exposes subject only; same session with the
  authorized UAT tenant exposes `preferred_username: admin@example.test`.
- Regression reproduced before fix (username absent), then PASS after fix.
- Added explicit `sessionForTenant` client query, while global authentication
  stays unscoped. UserMenu loads authorized profile keyed by tenant and subject,
  rejects mismatched subjects and ignores late reads after context changes.
  Empty/omitted/denied/unavailable attributes safely fall back to account ID.
- Both workspace and personal account headers use the display query; no backend
  policy bypass or authentication mutation. Existing personal-token test now
  explicitly allows only read-only session projection with selected t1; all
  other personal requests retain strict absent-tenant assertions.
- Ten focused browser regressions PASS: username, cross-tenant late response,
  omitted fields, 403/503/network failures preserving login, no workspaces,
  mobile email fallback, subject mismatch and personal account navigation.
- Real Chrome/current backend username readback PASS on desktop and mobile;
  no uncaught page errors or horizontal overflow. Screenshots are ignored local
  `username-desktop.png` and `username-mobile.png` under `.temp/e2e-current`.
- Frontend typecheck/lint/forced tests (149 PASS, 0 FAIL)/build PASS.
  Final full browser regression rerun PASS 74/74 (session 6634, exit 0).
- `git fetch origin` PASS, current IAM and origin/IAM had no divergence.
  All 22 modified paths reviewed; no untracked deliverables, credentials,
  runtime configuration, browser profiles or generated reports to stage.
- Final personal lifecycle six locale/device combinations PASS; only the
  read-only display projection may carry t1/t2, while every personal lifecycle
  and platform mutation retains strict absent-tenant assertions.
- Final Prettier, script syntax and staged diff checks PASS. All 22 deliverable
  paths explicitly staged; private-key marker and local-artifact checks clear.
- Self-review and original-request acceptance PASS: username fixed, global
  authentication preserved, field policy respected, delayed reads isolated,
  existing E2E improvements retained and verified. Execution Direct.
- Delivery authorized on branch IAM to origin/IAM, commit message:
  `fix(iam): harden browser workflows and tenant-aware account display`.
  Exact commit hash and remote synchronization are authoritative in Git.
- Next action: execute authorized commit/push and verify equal local/remote heads
  and clean worktree; then user continues inspection at localhost:18091.
  Preview API/notification/Vite and owned UAT database remain running intentionally.

## Account File Entry Checkpoint - 2026-09-19

- Phase: TESTING. Same `/files` contract now supports strict account and
  tenant/entity contexts; no Workspace/Space/Node/Root public resource was added.
- Account root is lazy and idempotent. GET does not create it. Account and tenant
  spaces reject each other's globally valid file IDs; partial organization headers
  return `incomplete_resource_context` before the handler.
- Added full account HTTP lifecycle coverage: create root/directory, upload,
  download raw bytes, rename and delete. PASS; all 7 audit rows have `tenant_id=0`
  and no organization audit row is created.
- Backend PASS: `go test ./internal/files -count=1` and
  `go test -race ./internal/files ./pkg/extension/authz -count=1`.
- Frontend production build PASS. Mocked real-Chrome file browser audit PASS for
  component desktop/tablet/mobile, zh-CN/ms-MY mobile, organization route
  anonymous/desktop/mobile, and account route desktop/mobile. Account requests
  carry neither organization selector; organization requests carry both.
- Account desktop/mobile screenshots inspected: no overlap or horizontal overflow.
  Isolated Vite remains running on `127.0.0.1:18291`, proxying the pre-existing API
  on `127.0.0.1:18080`; existing listeners on 18080/18191 were not restarted.
- Still not production ready: organization root archive/restore and old-root repair,
  root-level revision/ordered cross-root locking, real MySQL 8.4/PostgreSQL plans
  and concurrency, ACL management API/Web, four sharing modes, and real storage E2E.
- Next action: run final formatting/static/generated checks and inspect the complete
  diff for authorization regressions; keep the isolated preview available for user
  inspection and report its URL without exposing or inventing credentials.

### Account File Real Preview Acceptance

- Built the current source to ignored `.temp/files-preview/api.exe`; migrated a new
  isolated SQLite database to files 9/9 and organization 10/10. Existing MySQL and
  listeners on 18080/18191 were not stopped, migrated or modified.
- Current-source API runs on `127.0.0.1:18280`; Web runs on `127.0.0.1:18291` and
  proxies only to that API. `/readyz`, `/docs`, and `/openapi.json` return 200.
  Runtime OpenAPI exposes exactly `/files`, `/files/{file_id}`,
  `/files/{file_id}/items`, `/files/{file_id}/raw`, and
  `/files/{root_file_id}/-/{path}` for file management.
- Real Chrome + real API/storage PASS: login, lazy account-root creation, directory
  creation, multipart upload, byte-exact download, rename, file trash and directory
  trash. Test-created visible entries were moved to trash; final root is empty.
- Final screenshot inspected at ignored
  `.temp/files-preview/account-files-real.png`: no overlap or horizontal overflow.
- Login is `admin@example.test`; the password remains in ignored local file
  `.temp/iam-uat/admin-password` and must not be printed into chat or committed.
- Next action: finish final repository checks, then leave 18280/18291 running for
  user inspection. Production-readiness gaps listed above remain unchanged.

### Account File Entry Final Review

- Phase: ACCEPTANCE for the account entry increment; overall file management remains
  TESTING and is not production ready because the documented remaining features and
  database/concurrency gates are not complete.
- Final PASS: backend Files tests, authz tests, Files/authz race, app composition,
  whole Go build, OpenAPI check, generate check, changed-Go gofmt, Web typecheck,
  lint, 170 forced tests, production build, Prettier, browser route audit, real
  account lifecycle, screenshot/layout inspection and `git diff --check`.
- Self-review PASS for this increment: same `/files` resource model, strict account
  versus organization context, no implicit GET writes, no public internal Node/
  Space/Workspace concept, owner-only account ACL, cross-space denial, and no
  unrelated existing service/database mutation.
- Final acceptance PASS for requested account-specific files and Web entry. No
  commit or push was requested. Preview API/Web intentionally remain running at
  18280/18291; only ignored isolated SQLite/storage/test artifacts were created.
- Next action: user inspects `http://127.0.0.1:18291/account/files` and Swagger at
  `http://127.0.0.1:18280/docs`. Password is read locally from
  `apps/backend-api/.temp/iam-uat/admin-password`. Subsequent implementation should
  start with organization-root archive/restore and root-level concurrency fences,
  then real MySQL/PostgreSQL plans before ACL management and sharing.

### File Browser Desktop Interaction Follow-up - 2026-09-19

- Phase: IMPLEMENTATION. User requested a full-width account file workspace,
  removal of the redundant page title, file/folder drag-and-drop upload, native
  item context menus, clean icon views without persistent overflow buttons, and
  an optional selection mode with actual bulk actions.
- Design: only `/account/files` escapes the account form max width. File commands
  live in the browser toolbar. Selection mode defaults off and exposes download,
  trash and clear-selection actions when active. Existing file create/upload/
  download/trash APIs are reused sequentially and the page refreshes once per
  successful batch. Directory drops are recursively materialized with bounded
  depth/item counts; no backend contract or dependency changes.
- Execution: Direct; interactions are tightly coupled in one file-browser surface.
- Next action: implement the bounded drop parser and UI/page wiring, add focused
  unit/browser assertions, then run frontend typecheck, lint, tests, build and
  real 1440/390 browser acceptance against the isolated 18280/18291 preview.

### File Browser Desktop Interaction Acceptance - 2026-09-19

- Phase: ACCEPTANCE. Account files alone now use the full account work area;
  other account forms retain `max-w-5xl`. The redundant page heading is removed;
  the current-folder breadcrumb remains as file-manager location context.
- File browser now accepts multi-file drops and Chromium/Edge directory entries,
  repeatedly drains directory reader batches, preserves empty directories, and
  rejects drops beyond depth 64 or 1,000 entries. Recursive creation is ordered
  parent-first and a mutation batch refreshes once, including partial-failure
  refresh. Existing `/files` APIs and account/organization selectors are reused.
- Native Base UI right-click/long-press menus provide open/properties/download/
  rename/trash. Icon views have no persistent overflow button; details view keeps
  it for touch/keyboard fallback. Raw root owner IDs are no longer displayed.
- Selection mode defaults off. Enabling it exposes checkboxes, select-visible,
  selected-file download, selected-item trash with one confirmation, and clear.
  Closing selection mode clears selection.
- PASS: frontend typecheck, lint, all 175 tests (85 shared UI + 90 IAM), production
  build, `git diff --check`, and real Chrome browser audit on desktop/tablet/mobile
  plus zh-CN/ms-MY. Browser audit covers multi-file drag highlight/drop, right-click
  properties, hidden icon overflow controls, selection actions, paging cleanup,
  layout and account/organization request-header isolation.
- PASS against isolated real API/storage on 18280/18291: login, root, folder,
  two-file drag upload, byte-exact download, rename, selecting two files plus one
  folder, batch trash, and restoration of the exact pre-run user-file ID baseline.
  Existing user items `234` and `grayscale-test.png` were preserved.
- Recursive folder parsing/materialization PASS with nested files, an empty folder,
  multiple `readEntries()` batches, depth/item limits and exact API call order.
  Playwright cannot synthesize a native OS directory drag: an attempted synthetic
  entry was correctly excluded from acceptance and its four exact test-owned
  artifacts were removed; no user file was deleted.
- Self-review PASS: requested interaction scope only, existing APIs and UI library
  reused, no new dependency/backend contract/concept, desktop/mobile regression
  checked. Overall file management production-readiness gaps remain unchanged.
- Next action: user inspects `http://127.0.0.1:18291/account/files`; isolated API,
  Web and Swagger remain running at 18280/18291. No commit or push requested.

### DOCX Embedded Image Follow-up - 2026-09-19

- Phase: ACCEPTANCE. User reports that DOCX text renders but embedded images do not.
- Current local Office adapter permits `data:image/*`, `data:font/*` and Blob URLs,
  while `docx-preview` 0.3.7 is configured by `open-file-viewer` 0.1.47 to convert
  package resources to data URLs. An isolated active `printtest.docx` contains
  three embedded PNG files and is available for a real browser reproduction.
- Scope: restore DOCX embedded-image preview without weakening the existing Office
  active-content sanitization or changing file APIs. Add focused sanitizer coverage
  and real-browser acceptance; then resume the global REST test migration.
- Execution: Direct because the sanitizer and preview adapter are one tightly
  coupled surface. No subagent is requested.
- Real browser reproduction confirmed all three image elements had their `src`
  removed and decoded to 0x0. The rejected values are PNG payloads emitted as
  `data:application/octet-stream;base64,...` by `docx-preview`.
- Implementation signature-checks generic package resources and rewrites only
  verified PNG/JPEG/GIF/BMP/WebP payloads used by `<img src>`. Unknown binary data
  remains rejected; the existing active-content and external-URL rules remain.
- PASS: six focused signature tests, all 104 IAM tests, full IAM ESLint,
  TypeScript typecheck, production build and `git diff --check`.
- PASS in real Chrome against the isolated current API/Web: all three embedded PNGs
  retain `data:image/png` sources and decode at 2480x1748, 2000x500 and 2000x700;
  no browser console errors. Screenshot `docx-images-fixed.png` was inspected and
  shows the document page and embedded color/grayscale test images without blank
  placeholders, overlap or horizontal overflow.
- Self-review and requirement acceptance PASS. The fix does not broaden generic
  octet-stream access: only known raster signatures used by `<img src>` normalize;
  unknown/malformed payloads and active/external resources remain blocked.

### System-wide File Governance Design - 2026-09-19

- Phase: IMPLEMENTATION. User requires every durable business upload to use the file core,
  system/tenant/entity/department/personal/account ownership at the lowest valid
  granularity, and prevention of unowned files.
- Existing Object and Node/Version layers are reused. Existing tenant spaces and
  entity/department/personal roots remain; proposal adds only system scope and a
  registered-purpose logical resource binding ledger. Department and account scope
  remain necessary. “Instance” is interpreted as the existing Entity resource.
- Platform-wide logical/physical viewing is explicitly deferred. The current phase
  has no global file API, Web page, or placeholder platform permissions. Ordinary
  account `/files` never becomes global because of Principal privilege.
- User-managed files may be unbound; resource-managed files must atomically bind
  a verified domain resource and are hidden from ordinary folders by default.
  Unknown/missing bindings are quarantined and audited, never auto-assigned upward.
- Detailed design is section 23 of `docs/file-management-design.md`; the user
  reviewed it and explicitly approved implementation.
- Binding foundation completed: migration 00010 adds Node management mode and the
  single `file_resource_bindings` ledger in all three dialects; generated queries
  and DI publish one `ResourceBindings` capability. Purpose registration rejects
  unknown/duplicate contracts and requires a transaction-bound business-resource
  validator. Bind derives the verified root owner, validates the exact resource,
  requires a real head/pinned Version, serializes on the Space and atomically marks
  the Node resource-managed. Unbind is Space-fenced and leaves the Node quarantined
  for orphan detection. Ordinary ACL authorization and browser pages reject/hide
  resource-managed files.
- Expiring bindings are no longer declarative-only: the existing bounded cleanup
  worker keyset-scans due active bindings, applies the same per-Space mutation
  fence, transitions them to `expired`, and leaves their resource-managed Node as
  an explicit logical orphan for policy-driven reconciliation. Concurrent manual
  unbind/expiry is idempotent at the worker boundary.
- The earlier unused global inventory/content permission placeholders were removed
  when the user deferred platform-wide viewing; no ordinary `/files` behavior was
  changed.
- System scope completed: `file_spaces.system_slot=1` is a cross-dialect singleton;
  system rows require NULL Tenant/Principal ownership, and the physical root is
  recognized without inventing a `root_owner_id`. SQLite uses one connection,
  foreign keys disabled around an explicit `BEGIN IMMEDIATE` table rebuild; the
  old rows are copied atomically and foreign keys are restored. Downgrade performs
  a preflight guard and refuses while system data exists. PostgreSQL/MySQL replace
  their old checks with named constraints and the same singleton invariant.
- Verification 2026-09-19 after system scope: Files package PASS; Files race PASS
  (38.673s after binding expiry coverage); backend `go build ./...` PASS; `make generate-check` PASS;
  SQLite/MySQL/PostgreSQL migration matrix PASS with plain and prefixed schemas
  (18.015s); `git diff
  --check` PASS. Tests cover invalid Purpose contracts, exact owner validation,
  head/pinned version scope, duplicate slots, node-transition rollback, optimistic
  unbind, hidden ordinary access, orphan classification and populated rollback.
- Remaining: avatar migration, business upload guardrails, stock-file
  reconciliation, and storage/browser end-to-end acceptance. Platform-wide views
  and cross-scope content access are deferred outside this phase.
- Next action: migrate avatars with a single lifecycle: new writes create
  account-scoped resource-managed Node/Version/Binding rows atomically; startup
  backfill converts legacy account references idempotently before the old model is
  retired. Preserve candidate expiry, selection, account erasure and storage
  cleanup semantics throughout. Do not add a global route, global Web page, or
  placeholder platform permission while completing this work.

### REST Cleanup And Governed Avatar Acceptance - 2026-09-19

- Phase: ACCEPTANCE. Public application paths contain no `/iam`, `/authn`, or
  `/platform/iam` resource prefix. Swagger tags are lowercase kebab-case first
  path segments. Core migration 16 upgrades existing built-in menu routes to the
  new resource paths; custom labels/translations remain intact.
- Avatar lifecycle now uses account-owned Object -> Version -> Node ->
  ResourceBinding records. Current/candidate purposes are registered, ordinary
  file browsing hides resource-managed Nodes, promotion reuses the object,
  candidate expiry is bounded, erasure revokes bindings, and orphan cleanup is
  Space-fenced before existing physical-object reclamation.
- Legacy account references and direct-storage avatars migrate idempotently.
  Existing object bytes are adopted and checksummed without a copy. New accounts
  do not create a durable default avatar; the empty-avatar presentation fallback
  remains until an avatar is explicitly set.
- DOCX embedded images accept only signature-verified PNG/JPEG/GIF/BMP/WebP
  octet-stream data URLs. Unknown binary, active content and external URLs remain
  blocked.
- PASS: migration matrix across SQLite/PostgreSQL/MySQL with plain/prefixed
  schemas (315.302s); `go test ./... -count=1`; Files/Identity/Core race;
  related package rerun after lint cleanup; `go build ./...`; backend fmt/lint;
  OpenAPI and generate checks; schema-lock tool/tests (219 frozen unchanged,
  228 files, 72 tables, 34 cross-module FKs); forced frontend lint/typecheck,
  85 UI + 104 IAM tests and production build; `git diff --check`.
- Scope review: no global file inventory/API/Web page, no public Node/Space/
  Workspace abstraction, no conflict/task/cleanup table, no legacy route aliases,
  and no storage key exposure. Real external MySQL 8 and deployment/browser UAT
  were not rerun in this final commit cycle; the embedded three-engine matrix and
  earlier live browser acceptance are the available evidence.
- Delivery: implementation commit `ef03aa4` was accepted by `origin/IAM`. Codeup
  emitted a non-blocking warning that the commit email `gankcode@gmail.com` differs
  from the authenticated account email; the branch update itself succeeded.
- Next action: none for this delivery after the state-only follow-up commit is
  pushed and the remote tracking reference is verified. Real MySQL 8 and deployment
  UAT remain separate release-readiness gates, not hidden completion claims.

### Canonical Scoped Routing And File Acceptance - 2026-09-20

- Phase: ACCEPTANCE. Public API and Web routes use `/me`,
  `/tenants/{tenant_id}`, `/tenants/{tenant_id}/entities/{entity_id}`, and
  `/platform`; no business-resource selector is constructed in request headers.
- Files use canonical personal, tenant, and entity paths. Direct new-tab opening
  uses cookie-authorized inline streaming URLs instead of unbounded Blob URLs.
  Tenant/entity permission discovery and menu visibility are scope-specific.
- Demo bootstrap provides super, system, tenant, entity, and department personas.
  Entity/department personas now include create/delete file permissions so the
  requested mutation and inheritance flow can be exercised.
- PASS: generated OpenAPI check; `go test ./...`; `go build ./...`; frontend API
  tests 39/39; full frontend tests 199/199; typecheck; lint; production build;
  all browser-audit script syntax checks; `git diff --check`.
- PASS real browser: all five personas log in from the development account picker
  with expected tenant/entity/department roots and no unexpected browser errors.
  Department persona created `666` in UAT Finance and the entity persona observed
  it. First Business `37707464115748864` and UAT Files `38001505780891648`
  returned disjoint root IDs.
- Runtime acceptance remains available at `http://127.0.0.1:61828/login` with API
  `http://127.0.0.1:61827` and the isolated SQLite fixture
  `.temp/sqlite-lifecycle-4b977R`.
- Next action: commit the reviewed tracked changes, push `IAM`, verify the remote
  tracking reference, and leave the acceptance services running.

### Personal View Workspace Return Fix - 2026-09-20

- Phase: ACCEPTANCE. Root navigation no longer captures an empty tenant at module
  startup. Each visit to `/` resolves the authenticated account's active tenants,
  preserves a valid prior selection, or selects the first active workspace.
- `/me/*` uses `/` as the return entry so first-login users without local tenant
  state can leave the personal view.
- PASS: focused navigation/account tests 5/5, typecheck, lint, production build,
  and real Chrome login -> `/me/profile` -> workspace navigation.
- Next action: push the focused fix and keep the existing acceptance URL running.

### Scoped File Menu And Department Labels - 2026-09-20

- Phase: ACCEPTANCE. Entity and department demo accounts now select the UAT
  tenant at login and receive a tenant-sidebar File management entry that links
  to their authorized entity route, never to the tenant-wide file route.
- Department file roots resolve only their already-visible department IDs to
  readable names. The entity persona has a separate tenant-level read-only role
  limited to the two selected UAT departments; its file rights remain entity-bound.
- PASS: 44 focused tests, typecheck, lint, production build, `git diff --check`,
  and real Chrome E2E for all five personas. Entity sees entity + UAT Finance +
  UAT Operations roots; department sees only UAT Finance. Both see the correct
  entity-scoped File management menu link from the tenant view.
- Runtime remains available at `http://127.0.0.1:61828/login`, backed by API
  `http://127.0.0.1:61827` and the isolated SQLite fixture.
- Delivered in `f2f2fe7`; `origin/IAM` accepted the push. Acceptance services
  remain running for user inspection. No implementation work remains.

### File Root Owner Name Consistency - 2026-09-20

- Phase: ACCEPTANCE. Tenant, entity and department file roots now all display
  their actual owner names; multiple roots at any organization level are
  distinguishable. Generic localized scope labels remain fallback-only.
- Reused the layout's authenticated tenant/entity catalogs and the scoped
  department lookup; no additional broad directory permission or API contract.
- PASS: 27 focused tests, typecheck, lint, production build, diff check and
  five-persona real Chrome E2E. Verified labels: UAT Files, UAT File Company,
  UAT Finance and UAT Operations.
- Delivered in `8115aa0`; `origin/IAM` accepted the push. Acceptance services
  remain running and no implementation work remains.

### Directory Selection Interaction - 2026-09-20

- Phase: ACCEPTANCE. Active directories now single-click to select and
  double-click to enter. Enter opens the focused directory and the context-menu
  Open command is unchanged. File preview and recycle-bin behavior are unchanged.
- Navigating clears selections and the range anchor from the prior directory but
  preserves checkbox selection mode, preventing cross-directory bulk actions.
- PASS: 27 focused tests, typecheck, lint, production build, diff check and real
  Chrome against department folder `666` for single click, checkbox selection,
  double-click navigation and selection-mode persistence.
- Delivered in `726a2e2`; `origin/IAM` accepted the push. Acceptance services
  remain running and no implementation work remains.

### Two-Row File Toolbar And Persistent Selection Mode - 2026-09-20

- Phase: ACCEPTANCE. The former three toolbar bands are now two: navigation,
  path, search and type filter on the first; selection/bulk controls plus view,
  sort and mutation actions on the second, with actions aligned to the right.
- The stable FileBrowser wrapper now owns selection-mode state. Directory/page
  body remounts still clear item selections and metadata, while checkbox mode
  remains enabled after the destination fully loads.
- PASS: component tests 12/12, typecheck, lint, desktop/mobile real Chrome,
  exactly two toolbar bands, zero horizontal overflow at 1440px and 390px, and
  department-folder navigation with persistent mode plus zero inherited checks.
- Follow-up: all file actions, including the selection-mode toggle, select-all,
  bulk actions, create/upload/paste and refresh, are grouped on the left. Only
  view and sort controls remain right-aligned. Tenant file refresh now receives
  the same read-only refresh callback as account/entity views instead of staying
  permanently disabled.
- Next action: verify the final left/right grouping and tenant refresh in real
  Chrome, run build/diff checks, then commit and push IAM.

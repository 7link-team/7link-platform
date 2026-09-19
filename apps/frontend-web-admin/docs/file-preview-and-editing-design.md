# File Icons, Preview, Editing, And Version History

Status: Phase 1 implemented and under final acceptance. Date: 2026-09-19.

## Goal

The file manager should:

1. show a recognizable icon for every known file category instead of one generic
   file icon;
2. open supported files in an in-product preview;
3. support text and source-code editing later without replacing old content; and
4. expose the resulting immutable version history.

Directories remain files with `kind=directory`. No public Node, Space, Workspace,
preview-job, conflict, or editor-session resource is introduced.

## Existing Implementation And Reuse

- The browser already receives `name`, `extension`, `detected_type`, `size`,
  `version`, and `revision` for each file. These are sufficient for icon
  classification and bounded preview decisions.
- `GET /files/{file_id}/raw` already streams the current content and enforces the
  `download` action. The Web client already fetches it with the authenticated
  account or tenant/entity request context.
- `file_objects` are immutable, `file_versions` already stores immutable history,
  and `file_nodes.current_version_id` selects the current version. The existing
  object staging and `appendVersion` transaction primitive should be reused for
  editing saves.
- The shared UI already uses Lucide icons. File-category icons should use that
  library rather than custom SVG assets.
- CodeMirror 6 is already used for compact JSON form fields. It remains there.
  A future full-file code editor may use Monaco when IDE-grade language behavior
  is required; it must be lazy-loaded and must not replace the form control.

## File Presentation Classification

One pure classifier owns icon category, accent, display label, preview tier, and
future editor eligibility. It considers the server-detected MIME type first and
the normalized extension second. `application/octet-stream` may fall back to the
extension. A filename never changes authorization or backend format validation.

| Category           | Representative formats                 | Presentation                   |
| ------------------ | -------------------------------------- | ------------------------------ |
| PDF                | pdf                                    | red document icon, `PDF` badge |
| Document           | doc, docx, odt, rtf, wps               | blue text-document icon        |
| Spreadsheet        | xls, xlsx, ods, csv, et                | green spreadsheet icon         |
| Presentation       | ppt, pptx, odp, dps                    | orange presentation icon       |
| Image              | jpg, png, gif, webp, svg, tiff, heic   | image icon                     |
| Video              | mp4, webm, mov, mkv                    | video icon                     |
| Audio              | mp3, wav, flac, ogg, m4a               | audio icon                     |
| Archive            | zip, 7z, rar, tar, gz, bz2, xz         | archive icon                   |
| Text/code          | txt, md, json, yaml, xml, source files | text/code icon                 |
| Email/book         | eml, msg, mbox, epub                   | mail/book icon                 |
| Data               | sqlite, parquet, avro                  | database icon                  |
| Drawing/CAD/3D/GIS | drawio, dwg, dxf, glb, geojson, shp    | matching drawing/box/map icon  |
| Font               | ttf, otf, woff                         | typography icon                |
| Unknown            | everything else                        | neutral file icon              |

The same component is used by details, small-icon, large-icon, and properties
views. The large icon view may show a short extension badge, but never renders an
unbounded filename inside the icon.

Browser-renderable images use an authorized thumbnail presentation in file rows
instead of the category icon. Thumbnail bytes are loaded through the same
account or tenant/entity-aware raw client, never through an unscoped raw URL.
Loading begins only near the viewport, is aborted when the row leaves the React
tree, and is bounded to 8 MiB before buffering. The row keeps a stable frame and
falls back to the image file-type icon when loading, authorization, size, format,
or browser decoding fails. This initial client thumbnail does not claim to
replace a future server-generated, dimension-bounded thumbnail representation.

## Preview Architecture

`open-file-viewer` is isolated behind one local `FilePreview` component. The file
browser depends on this local contract, not directly on third-party plugin APIs.
The component owns loading, unsupported, oversized, error, close, and download
states and revokes every object URL on replacement or close.

Opening a file performs the existing authorized blob request and passes the Blob,
the original filename, and `detected_type` to the viewer. A raw authenticated URL
is not passed to the library: its URL fetches cannot add the required tenant and
entity headers. Blob loading also prevents PDF iframe fallback from navigating to
an application-authenticated raw URL.

The preview opens in a responsive near-full-screen dialog. A file click opens the
preview; directory click continues to navigate. Properties remain available from
the details/context menus. The dialog header contains the file identity and clear
commands, while renderer controls stay in the content toolbar.

### Dependency Decision

Pin exact versions because the viewer is pre-1.0:

- `@open-file-viewer/core@0.1.47`
- `@open-file-viewer/react@0.1.47`
- `pdfjs-dist@4.10.38`, matching the version tested by the project

The core npm artifact is about 6.8 MB unpacked and resolves roughly 211 production
dependencies with PDF support. The package exposes one bundled entry rather than
per-plugin subpath exports. Production chunk size and initial-route loading must
therefore be measured; the entire preview module must be dynamically imported.

### Enabled Renderer Tiers

Tier 1 is enabled for the first release:

- raster/SVG images;
- plain text, source code, JSON, XML, YAML, CSV, and sanitized Markdown;
- PDF through PDF.js;
- browser-supported audio and video within the in-memory size limit; and
- DOCX and PPTX through the existing Office plugin behind the local sanitizing
  adapter described below; and
- a safe unsupported/download fallback.

Tier 2 is not enabled until the named gate is satisfied:

- Other Office formats: legacy DOC/PPT and XLS/XLSX are not accepted by the
  local renderer. They remain download-only until the external gateway below is
  implemented and accepted.
- Archives: current decompression paths have no compressed-size, expanded-size,
  entry-count, or compression-ratio budget. Enable only after all four limits and
  cancellation are enforced.
- Email, EPUB, drawings, data files, GIS, and 3D: enable individually after
  malicious-input tests, resource budgets, and browser acceptance.
- CAD/DWG: do not add the optional GPL-3.0 LibreDWG path without an explicit
  licensing decision. Proprietary-format preview quality is best effort.

PPTist, Univer, and FortuneSheet are not enabled. None of their open-source
offerings reliably covers all required Office families (`ppt`, `pptx`, `doc`,
`docx`, `xls`, and `xlsx`): PPTist is an AGPL-3.0 Vue application focused on
presentations, Univer places the needed Office conversion and mature Slides
capabilities in Pro, and FortuneSheet covers spreadsheets only.

The enabled DOCX/PPTX adapter addresses upstream 0.1.47 issue #140 locally. It
renders into a detached staging element, removes iframe/srcdoc, script, object,
embed and form content, strips event-handler attributes, rejects unsafe or
external embedded resource URLs, sanitizes CSS imports/URLs, and observes later
mutations before attaching output to the preview surface. `docx-preview` labels
package images as generic octet streams, so the adapter signature-checks common
raster formats and rewrites only validated `<img src>` data URLs to their image
MIME type. Unknown binary data remains blocked. No other Office extension may
enter this adapter.

Office formats without an accepted local renderer use external online viewers:
Microsoft Office Viewer is the default and Google Viewer is an alternate `Open
with` handler. Both require a publicly reachable HTTPS URL, so the authenticated
`/files/{file_id}/content` route must never be passed directly. The handler
requests an explicit, short-lived, read-only, file-version-bound, revocable and
audited token URL from the shared public-file gateway. Tenant policy can disable
external data transfer, and the UI discloses the selected third party before
opening it. Until that gateway exists, these formats remain download-only.

## Blank File Creation

The current-directory context menu has one conventional `New` submenu. It
reuses `POST /files` rather than adding a command route: folders use the existing
directory form, while `.txt` and `.md` create zero-byte uploads with
`text/plain` and `text/markdown`. The filename dialog enforces the selected
extension when omitted. More templates are added only when their initial bytes
and editing workflow are defined; an empty file with a misleading Office
extension is not created.

The npm audit performed on 2026-09-19 reports no known vulnerabilities, but that
does not supersede parser isolation, content sanitization, or input budgets.

### Resource Limits

The first implementation uses a single documented in-memory preview limit and
shows an oversized fallback without fetching content. Recommended initial value:
32 MiB for mobile-safe behavior. This value must live in one configuration point
and have boundary tests. Decoded image dimensions and parser-specific expansion
limits remain separate gates; a compressed archive is never made safe merely by
being smaller than 32 MiB.

Large PDF/audio/video support is a later backend slice. It should add standards-
compliant byte ranges to the existing current-content representation and an
authorized delivery mechanism usable by PDF.js/media elements without exposing
storage keys. It must not create permanent signed URLs or bypass file ACL checks.

## Authorization

Previewing reveals the original bytes and cannot technically prevent a browser
user from saving them. The first implementation therefore uses the existing
`download` permission; it does not invent a misleading weaker `preview`
permission. Account and organization request contexts remain distinct.

Editing requires the existing `file_update` IAM ceiling and effective node ACL.
Version history metadata requires read access; reading any historical bytes also
requires download access. Authorization is evaluated on every request, including
after a preview dialog has been open for a long time.

## Text And Code Editing With Monaco

Monaco is a viable full-file editor, but it is not part of `open-file-viewer` and
does not create server history by itself. It is loaded only after the user selects
Edit on an eligible text/code file. Preview and editor are two modes of the same
file-open surface, sharing file identity, loading, errors, and close behavior.

The first editor slice is explicit-save only. It has dirty-state close protection,
UTF-8 encoding, a configured size limit, language selection from the classifier,
and no collaborative editing, autosave, drafts, or executable code path.

### Save Contract

Use `PUT /files/{file_id}/raw` for replacement of the current content
representation. Send the bytes as the request body with `Content-Type` and an
`If-Match` value derived from the file revision observed when editing began.

On success, the backend:

1. authorizes update against live IAM and file ACL state;
2. stages and verifies a new immutable `file_object`;
3. locks and rechecks the file identity, active state, kind, and revision;
4. appends the next immutable `file_version` using the existing primitive;
5. atomically advances `current_version_id` and the file revision;
6. appends one audit event in the same transaction; and
7. returns refreshed file metadata including the new version and revision.

A stale `If-Match` returns `409`; the UI keeps the unsaved buffer and offers
reload or download of the local text. It never silently overwrites a newer
version. A failed transaction never exposes the staged object as a file version;
existing object cleanup rules reclaim it.

### History Contract

Saving creates durable history immediately, but history is not user-visible until
these resources and UI are implemented:

- `GET /files/{file_id}/versions` lists bounded metadata pages;
- `GET /files/{file_id}/versions/{version}/raw` streams one historical version;
- restoring an old version creates a new head version referencing or copying the
  selected immutable content; it never deletes or mutates intervening history.

Retention, permanent version deletion, legal hold, and quota charging are product
policies and remain outside this slice until explicitly approved.

## Implementation Plan

### Phase 1: icons and safe preview

- Add and test the shared file-presentation classifier and icon component.
- Lazily render bounded authorized image thumbnails with icon fallback.
- Add blank `.txt` and `.md` creation to the current-directory context menu.
- Replace generic file icons in every file-browser view.
- Add exact pinned preview dependencies and a dynamically loaded adapter.
- Add the responsive preview surface, open/context actions, i18n, cancellation,
  object-URL cleanup, size guard, and error handling.
- Enable only Tier 1 plugins and verify bundle/chunk behavior.
- Test desktop and mobile with real image, text, PDF, unsupported, oversized,
  unauthorized, and corrupt samples.

### Phase 2: Monaco save and visible history

- Add the raw-content `PUT` contract, service method, immutable version append,
  optimistic conflict handling, authorization, audit, and tests.
- Add the lazy Monaco editor for classifier-approved text/code formats.
- Add bounded version list/read/restore APIs and the history UI.
- Test concurrent saves, revocation while editing, corrupt encodings, rollback,
  historical authorization, and real browser edit/save/reopen/history flows.

## Files Expected To Change

- `apps/frontend-web-admin/apps/iam/src/lib/file-browser.ts`
- `apps/frontend-web-admin/apps/iam/src/components/file-browser.tsx`
- one local preview adapter/component under the existing IAM app
- file message bundles, focused tests, app package manifest, and lockfile
- Phase 2 only: `internal/files` service/HTTP/OpenAPI/generated client/tests

No database migration is expected for versioned text saves because the immutable
object/version model already exists. No handwritten SQL is required.

## Acceptance

- Known types have stable, consistent icons in all views; unknown types retain a
  neutral fallback.
- Opening a supported file uses the correct renderer without leaking storage URLs
  or bypassing account/tenant/entity authorization.
- Unsupported, oversized, corrupt, and forbidden files fail clearly and do not
  leave object URLs, workers, or stale content mounted.
- Initial file-manager loading does not include the preview/editor implementation
  chunk.
- In Phase 2, every successful save increments file version and revision, retains
  prior bytes, records audit, and rejects stale concurrent saves.
- Frontend tests, lint, typecheck, production build, backend tests/OpenAPI checks
  when applicable, and real desktop/mobile browser flows pass.

## Deliberately Out Of Scope

- Office/CAD fidelity claims, arbitrary binary editing, collaborative editing,
  autosave/draft tables, preview task tables, server-side conversion, OCR,
  thumbnail generation, and search indexing.

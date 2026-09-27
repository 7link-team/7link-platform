# Menu Tree Acceptance - 2026-09-26

## Result

Tree operations and recorded technical checks: PASS. User acceptance reopened
navigation correspondence: built-in sidebar entries are not yet represented in
the editable tenant tree. That requirement is NOT READY. Current menu changes
are not committed/pushed.

## Running Demo

- Web: http://127.0.0.1:8091/login
- Swagger: http://127.0.0.1:8080/docs
- Menu manager: http://127.0.0.1:8091/tenants/38895061949218816/menus
- Choose one of the five development accounts on the login page; the selector
  fills credentials. No password needs to be copied.
- Existing demo menu IDs/routes/grants are retained under four persisted groups:
  Organization and members, Access management, External integrations, Audit.

## Actual Browser Evidence

Run from `apps/frontend-web-admin`:

```powershell
node apps/iam/tests/menu-tree-browser-audit.mjs
```

Final real Chrome run: PASS, zero mocked business responses, zero page errors.
Report and screenshots: `apps/backend-api/.temp/dev/browser/menu-tree/`.
Audit restores original menu placement and removes its temporary fixtures;
cleanup failure causes test failure.

| Role | Widths | Verified views | Menu management |
| --- | --- | --- | --- |
| super-admin | 1440, 390 | Tenant, platform, personal | Tree, arrows, persisted reload |
| system-admin | 1440, 390 | Tenant, platform, personal | Hidden; actual PATCH403 |
| tenant-admin | 1440, 390 | Tenant, personal | Tree, arrows, persisted reload |
| entity-account | 1440, 390 | Tenant, scoped entity, personal | Hidden; actual PATCH403 |
| department-account | 1440, 390 | Tenant, personal | Hidden; actual PATCH403 |

All ten English role/viewport combinations pass navigation collapse/expand and
document width checks. Ten additional Chinese role/viewport combinations verify
tenant sidebar labels, permission-correct menu links, authorized menu trees and
localized direct-URL denial. Total20 combinations PASS. Desktop/mobile
menu-manager screenshots were inspected.

User acceptance exposed a gap in the original English-only audit: four Chinese
demo branch translations had been written as question marks by a PowerShell
pipe. All four were repaired. The audit now sets the actual `NEXT_LOCALE` cookie
and asserts document language, label integrity and menu-link visibility.
System-admin has no menu-management link; directly visiting its protected URL
correctly denies access. The denied page no longer exposes the preview-language
control. Frontend typecheck/lint/build were rerun after this correction and pass.

Admin business flow verifies root showing all descendants, branch showing direct
children, real mouse dragging before/inside/root, leaf becoming a branch,
moving entire subtrees, up/down entering branches and exiting parents,
descendant-cycle refusal in UI and actual HTTP422, reload preserving hierarchy
and order, and actual creation form inheriting the selected branch.

## Technical Gates

- Backend `make ci`: PASS, native exit0; overall coverage91.1%, core90.9%.
  Architecture, migration, lifecycle, performance, build and OpenAPI drift pass.
  Evidence: `apps/backend-api/.temp/menu-ci.log`.
- Focused core menu movement/authorization race checks: PASS.
  Evidence: `apps/backend-api/.temp/menu-race.log`.
- Frontend `bun run typecheck`, `bun run lint`, `bun run test`,
  `bun run build`: PASS on final frontend state.
- IAM `bun run check:api-types`: PASS. `git diff --check`: PASS.

## Manual Acceptance

1. Select super-admin or tenant-admin. Platform accounts initially open platform
   tenant management; use the top workspace switcher to enter the tenant. Open
   tenant menu management. The left directory
   tree shows branches only. Root initially lists every level; selecting a branch
   initially shows its immediate children on the right. Right-side branch arrows
   expand/collapse descendants in place, preserving the left selection.
2. Drag a leaf or branch before, into, or after another node, or onto All menus
   to move to root. Refresh and confirm descendants and order are preserved.
3. Use up/down: an adjacent branch receives the node; reaching a parent's edge
   moves it out before/after that parent. Moving a branch keeps its subtree.
4. Switch through all five accounts and their available views. Expand/collapse
   the grouped sidebars. Restricted accounts cannot manage menu metadata.

The existing permission model, policy transaction and audit path are reused.
No new endpoint, permission, dependency or database migration was introduced.

## Navigation Consistency Correction

The workspace switcher now includes authorized platform navigation and personal
account access. Default home uses global effective permissions, not role names:
platform tenant viewers enter `/platform/tenants`, platform administrators without
that view permission enter platform audit, and other callers enter tenant/personal
workspaces. Explicit login return URLs remain intact.

Platform view displays platform context, loads global permissions, and does not
load tenant menu/field catalogs. Its shared catalog exposes existing tenant,
audit and closure-tracking pages under their existing independent guards.
The tenant sidebar renders configured hierarchy without an extra Current tenant
wrapper; configured access requests keep their exact location. Missing
self-service links are supplied once under System entries.

The lower read-only system-navigation list was removed on user authorization.
Tenant menu management currently contains its editable tenant tree only; shared
fixed catalogs still render platform/entity/personal and supplemental tenant
navigation. Built-in correspondence/editability is being clarified; removing
the list does not resolve that requirement. No new authority was introduced.

Regression covers permission-based default home, configured sidebar paths and
parentage, absent duplicate wrappers/links, actual switcher
platform/tenant round trips, existing platform audit/closure page HTTP200 and
system-admin closure HTTP403. Frontend typecheck/lint/test(142 IAM tests)/build
PASS. Expanded20-case browser run PASS with zero mocks/page errors. Four final
Chinese clean-demo platform-switcher cases PASS after the last display correction.
Platform switcher does not show an unresolved entity loader or incorrectly mark
tenant view selected. Platform page assertions await rendered data, correct
active links and dismissed mobile drawers before taking screenshots.

Recorded screenshots include `final-*-zh-CN-switcher.png`,
historical `final-*-zh-CN-system-entries.png`, `final-*-zh-CN-tenant.png` and real platform
audit/closure screenshots alongside the report. All temporary menu nodes are
removed; the retained demo has18 nodes under four real branches.
Latest acceptance found Branch fixtures during a live audit. Mutation E2E must
use isolated test data/runtime rather than the tenant the user is inspecting.
Next action: verify fixture cleanup, decide built-in editability, and implement
full menu/sidebar correspondence. Prior technical PASS is not final acceptance.

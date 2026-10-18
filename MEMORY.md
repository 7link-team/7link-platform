# Task State
## Objective
Deliver H2 production project list with real counts/API/AutoTable and make all AGENTS.md neutral/general. User authorized commit and push.
## Current Phase
ACCEPTANCE
## Current Step
H2 slice and AGENTS neutrality accepted after all final gates PASS. This checkpoint accompanies the delivery commit; Git remains the authority for commit/push and clean-tree status.
## Completed Work
- Baseline origin/chaosplus efebae4: exact list record budget projection; previous commits 3d4f483 (all-page AutoTable/AutoForm), a6054bf (H3 detail), a22ee69 (activity storage) delivered.
- Current changes implement tenant/permission-filtered GET /tenants/{tenant_id}/projects with same-snapshot page-scoped grouped activity queries, no fanout/N+1.
- Production ProjectsPage reuses AutoForm/AutoTable, server filters/sort/pagination, fixed sequence/actions, bounded scrolling, manual/automatic refresh, current-page CSV, details and exact BigInt micros formatting. Three locales added.
- Failure/retry retains query/offset/total; literal name/code all remains a real filter; cancelled requests cannot overwrite new data. Short viewport filter area scrolls without displacing all table rows.
- Nullable client Huma panic repaired via TransformSchema; full exact OpenAPI field/null/string gate added. Regenerated OpenAPI and API types.
- Both AGENTS.md files generalized; project conventions moved intact to docs/development.md and backend README; source/path/brand scan has zero matches in agent instructions.
## Confirmed Decisions / Constraints
- Approved counts: open excludes archived/ACCEPTED/CANCELLED/SKIPPED, includes containers; review=nonarchived NEEDS_HUMAN_REVIEW; waiting=nonarchived SENT; project-wide, not per-approver.
- Per specification project.list uses default50/max100; frontend explicitly20. Generic pagination contract gate asserts exact max100 only for this operation, all other max200 checks preserved.
- Reuse typed queries, authz FilterData, existing DSL/respx/generated types. Preserve existing detail numeric amount contract and all authorization semantics.
- Preserve original Web4303/API8080, demo selector, unrelated8091/8092; no original service restart or business-data migration. No fabricated production counts or Todo substitution.
## Verification
- PASS focused delivery/list tests and composed HTTP/OpenAPI tests, including 401,422,precise amounts, field set, nullable client and pagination.
- PASS independent SQL, HTTP and UI reviews. HTTP registration panic repaired; reviewer confirmed pagination exception against spec07 lines774/1515.
- PASS independent agent-browser fixture: literal all filters, page2 failure/retry, numbered pager, fixed columns/header and fullheight 2D scrolling, actual5s auto-refresh, exact max-int64 micros and detail route.
- PASS main-agent real isolated API/browser: persisted activity yields3/2/2, actual AutoTable renders project, default50 and limit101=>422, nullable client/budget strings, search works; 1262x624 row/footer visible and390x844 no horizontal document overflow.
- PASS four count mutations (tenant removal, accepted inclusion, container exclusion, wrong delivery state), source restored byte-for-byte.
- PASS final frontend bun run typecheck/lint/test/build (sessions70901/18938/64465/53572 exit0). Web212 tests; UI package also passes. Only unrelated autoformat hunks restored afterward, no semantic changes.
- PASS make test-performance (50.474s; existing baseline, not new H2 SLO proof).
- PASS final make ci, session35311 exit0: formatting, lint0issues, schema lock255 unchanged, unit/coverage (delivery91.9%), architecture213.453s, lifecycle181.061s, migration180.643s, performance49.887s, build, OpenAPI check and frontend contract. First-run pagination mismatch fixed according to explicit spec and independently reviewed.
- PASS final make test-mysql8, session64447 exit0; TEMP/h2-mysql8-final.log. Real MySQL8.4 thresholds/storage and existing external database gates passed; schema lock255 unchanged.
## Active Acceptance Environment
- Isolated http://127.0.0.1:4317/admin/login, demo account selector enabled; use tenant-admin. API8087 runs final binary; original ports remain listening.
- Source SQLite online backup copied to ignored apps/backend-api/.temp/dev/h2-acceptance/acceptance.sqlite. Original database untouched.
- One project H2-UAT-1 labeled H2 acceptance demo (isolated copy), created through real API; explicitly isolated test activity fixtures persisted for counts3/2/2. Not production records or full workflow evidence.
- API exec session85611; Web session36359. Config/binaries/credentials/screenshots ignored and excluded from staging. Browser sessions closed.
- Browser evidence: ignored TEMP/h2-independent-browser-result.json and TEMP/h2-projects-live.png. Gate logs in OS TEMP.
## Reviews / Acceptance
- Self-review PASS: requested scope only, existing components/query/authz/schema mechanisms reused, no new dependencies or migrations, old API preserved, all gates executed, no artifacts/secrets staged.
- Final acceptance PASS for H2 list and neutral AGENTS requirements; not acceptance of the entire prototype. Main-agent real HTTP/browser acceptance retained independently of delegated reviews.
- Explicit24-file staging reviewed; unrelated formatter-only edits restored. Both AGENTS files contain zero project-specific path/product/port matches; original conventions preserved in project docs.
## Remaining Work
- Commit/push the reviewed delivery and verify remote HEAD/clean tree using Git. No implementation or validation remains for this slice.
## Notes / Scope Limits
- Entire prototype is not finished. H2 list slice is implemented; project create/edit UI and remaining work-item/delivery command workflows remain future slices.
- Existing module coverage and architecture/schema gates remain enabled; do not weaken them.
- New app scaffolds remain scaffolds; no runner daemon/task execution added. Prior frontend exceptions inventory remains docs/myrmidon/frontend-web-migration-checklist.md.
## Next Action
After the delivery commit, verify origin/chaosplus points to HEAD and git status is clean, then provide the user the isolated4317 login URL and demo-account instructions for acceptance.
## Last Checkpoint
All H2/frontend/live/browser/mutation/backend/MySQL gates and both final reviews PASS. No mutation active, no validation process outstanding. Original services unchanged; isolated acceptance services remain running.

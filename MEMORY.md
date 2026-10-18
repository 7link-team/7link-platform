# Task State - H2 continuation 2026-10-18
## Objective
Deliver production H2 project list with real persisted counts, permission filtering, exact budget transport and AutoTable/AutoForm interactions.
## Current Phase
ACCEPTANCE
## Current Step
Private list projection accepted after full CI PASS. This checkpoint accompanies its delivery commit; full H2 awaits count semantics and remains NOT READY.
## Completed Work
- Recovery verified clean starting tree at a22ee69, matching origin/chaosplus; previous storage delivery remains accepted below.
- Existing permission-filtered query now uses the narrow list projection with exact decimal-string budget cap/spent; no SQL/schema changes. Seven DB-to-JSON cases and old detail compatibility verified.
## Important Decisions / Confirmed Constraints
- Existing detail ProjectView stays wire-compatible. New list budget cap/spent use decimal integer strings in USD micros; pct is display-only numeric and null without a cap.
- No fake counts, Todo substitution, permission changes, live service restart or business-data migration.
- Direct implementation; two bounded independent precision and OpenAPI/compatibility reviews completed per deep-review.
## Reviews
- Independent precision and OpenAPI/compatibility reviews: no findings. No fresh API operation exists; final CI checks existing generated OpenAPI.
- A1 staging/A2 bulk/A3 transitions/A6 idempotency/A8 jobs: not touched. A4 isolation: existing permission/filter/pagination regression PASS and SQL unchanged. A5 precision: exact amount boundary assertions plus rejected float64 mutation. A7 compatibility: existing detail cap/spent numeric bytes and unchanged schema verified.
- Self-review of this slice: scope/reuse/architecture/regression/complexity PASS; whole H2 acceptance NOT READY until genuine aggregates/HTTP/UI/browser verification.
## Active Work
No running validation or active mutation. Implemented files: delivery/project_list.go and existing test file; ADR decision and compact MEMORY updated. No HTTP route until real activity summaries exist.
## Remaining Work
1. Await user count-semantics answer; this verified private projection is the current delivery slice.
2. Await user answer: recommended open = all nonarchived work items excluding ACCEPTED/CANCELLED/SKIPPED including summaries; review = NEEDS_HUMAN_REVIEW; waiting = SENT deliveries. Alternatives leaf-only open/current-approver review. This is undefined product semantics, not an implementation permission request.
3. Implement real same-snapshot aggregates, HTTP contracts, generated API types, frontend page/navigation/i18n, AutoTable/AutoForm controls and browser acceptance.
4. Deep review, meaningful mutation checks, required CI, explicit staging/commit/push.
## Verification
PASS focused TestProjectList including seven DB-to-JSON amount/NULL cases, existing permission/filter/pagination regression, full delivery+app tests, and go build ./.... Lossy float64 amount mutation rejected by no-cap >2^53 case; source restored and focused tests PASS. Two independent precision/contract reviews: no findings. Initial CI stopped at testifylint encoded-compare: JSONEq would lose exact numeric precision, so raw-byte assertions retain narrowly documented lint exceptions (matches existing test convention). Final full make ci PASS session39798 exit0, TEMP/h2-projection-ci-final.log: formatting, lint zero issues, schema lock, main tests and coverage, integration, build, OpenAPI and frontend contracts/typecheck. No SQL/schema/public operation changes. Previous accepted slice evidence follows.
## Next Action
Await explicit user count-semantics answer before implementing aggregates; recommendation and alternatives are listed above. Then add same-transaction aggregates, GET list, generated API types and AutoTable/AutoForm page, and verify browser behavior. No new endpoint/page acceptance claim.
## Last Checkpoint
2026-10-18 private projection acceptance: two independent reviews, mutation rejection, full CI PASS. No new endpoint exposed; H2 implementation is incomplete.

# Task State - production prototype continuation 2026-10-18
## Objective
User authorized commit/push and continuing the prototype in production; latest continuation includes minimal repairs for three existing CI blockers.
## Current Phase
ACCEPTANCE
## Current Step
Work-item/delivery root storage slice accepted. This checkpoint accompanies its delivery commit. Whole prototype and H2 remain incomplete.
## Completed Work
- Previously pushed frontend migration3d4f483 and H3 project detaila6054bf; remote hashes verified. Prior H3 frontend typecheck/lint/tests/build and55 browser cases pass.
- Full persisted WorkItem78-column and Delivery44-column models; generated queries and typed deployment table bindings reuse delivery module conventions.
- New three-dialect00003 migration: tenant/project-qualified references, unique sequence numbers, enums, JSON shapes/limits, numeric bounds, archive rules and indexes. Published00001/00002 unchanged.
- Migration tests preserve seeded v2 projects/requirements across v3 upgrade/downgrade/reupgrade, reject tenant/project cross references, check defaults and exact activity_seq9007199254740993 roundtrip.
- MySQL enum fields use ascii_bin. In-process MySQL explicitly matches real gate's case-insensitive database collation. Nullable budget preserves SQL NULL.
- Approved CI repairs: gofmt-only huma_test.go; explicit23-module migration report inventory plus rollback ordering; project error contract tests check status/code/message/reason and private-error exclusion. No threshold or production behavior changes.
## Verification
- PASS full make ci, session82245 exit0, TEMP/prototype-gates-ci.log: formatting, lint0issues, schema lock255 published migrations unchanged, main unit tests/coverage, integration, build, OpenAPI check and frontend contract.
- Coverage overall91.3%, delivery91.6%; all registered module gates>=90%.
- Full integration packages PASS: architecture177.306s, goose10.759s, testdb0.282s, lifecycle166.811s, migration166.289s, performance47.623s.
- PASS real MySQL8.4 gate session2569 exit0; new storage mysql8 case0.53s, storage suite12.86s. TEMP/prototype-storage-mysql8-final.log.
- PASS focused model/requirements/storage21.859s; migration-status three engines/two prefixes81.627s; focused delivery/huma tests.
- PASS make generate-check; all delivery query fingerprints stable around focused regeneration, including new files.
- PASS existing make test-performance66.748s. This is an existing baseline, not proof of new H2 performance SLOs.
- Mutation sensitivity PASS: removed channel enum, weakened project FK, missing MySQL binary collation, wrong project conflict status all caught; every mutation restored.
## Reviews
- Three independent storage reviews (SQL/spec/compatibility) complete; supported findings fixed and rechecked.
- Independent gate-test review PASS: explicit inventory/assertions retained, rollback order covers all owners, error contracts meaningful, formatting only.
- Self-review scope/reuse/architecture/regression/complexity/durable state PASS. Storage-slice acceptance PASS; no claim of complete work-item/delivery workflows.
## Confirmed Constraints
- No fake H2 counters, mock production persistence, Todo-as-business-task, new authorization semantics, or budget UI before lossless int64 transport.
- Preserve managed Web4303/API8080, demo account selector, unrelated8091/8092; no automatic live service restart or business-data migration.
- No new HTTP API contract in this slice; no business state machine or subtables implemented.
## Deferred Dependencies / Remaining Work
- Same-module FKs to add with owning tables: work_item pause_decision_id/current_claim_id/accepted_decision_id/decomposition_id/current_plan_id/active_exception_id; delivery release_notes_draft_id/deliver_decision_id/acceptance_id; previous requirement_version change_record_id.
- Cross-module principal/agent/execution/intake/files references intentionally have no FK per spec.
- H2 real project activity aggregates/list API and production list page, then remaining create/edit and work-item/delivery workflows.
## Next Action
Implement H2 project list from existing project_list.go and spec07 API2, using persisted activity sources and permission filtering before count/pagination. Inspect precise count semantics and reuse project record query/transport/AutoTable; do not fabricate counters or silently decide undefined product semantics.
## Last Checkpoint
2026-10-18 storage and approved baseline repairs accepted after full CI PASS; all validation processes collected, no mutation active. Explicitly reviewed source/tests/docs only are eligible for the delivery commit; generated outputs/logs/secrets remain excluded.

## Existing frontend acceptance and services
- Frontend AutoTable/AutoForm migration, sticky sides/header, bounded full-height center scrolling, split manual/automatic refresh and numbered pagination delivered in 3d4f483. Detailed page inventory/exceptions/browser evidence: docs/myrmidon/frontend-web-migration-checklist.md. H3 project detail delivered in a6054bf.
- Managed Web4303/API8080 stay running. Login http://127.0.0.1:4303/admin/login has demo account selector; tenant-admin fills credentials. Never print passwords. Latest availability check: login HTTP200; ports4303/8080/8091/8092 listening. Do not restart/migrate demo or stop unrelated8091/8092.
- Existing IAM bulk import lacks endpoint/field-conflict semantics; do not add fake import buttons. Exports use displayed page and preserve existing export permissions. Preserve existing loaded-row caps and full hierarchy behavior.
- Whole prototype remains incomplete. ADR.md retains architecture decisions; Git/source/actual verification take precedence over historical state. Earlier repeated task transcripts removed here to keep recovery state compact.

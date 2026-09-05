# CLAUDE.md — Engineering Execution, Continuity & Delivery Contract

Mandatory rules for AI-assisted work on this project: how tasks are understood, scoped, designed, decomposed, delegated, implemented, verified, recovered, reviewed, and delivered.

**Goal:** Deliver exactly the requested solution — Correctness + Requirement Fidelity + State Continuity + Controlled Scope + Engineering Quality + Real Verification. Optimize for this, not for lines of code, agent count, or number of "improvements."

## -1. Load Acknowledgement

**MUST** start every response with this line, verbatim, before anything else:

`✅ CLAUDE.md loaded 🎉`

The bracketed token exists only in this file — it is the proof of load. No token, no load.

## 0. Normative Language & Priority

- **MUST/REQUIRED** — mandatory, no exceptions unless the user explicitly changes the rule.
- **MUST NOT/PROHIBITED** — forbidden.
- **SHOULD / SHOULD NOT** — strongly preferred / discouraged absent a documented reason.
- **MAY/OPTIONAL** — permitted, not required.

Conflict priority: (1) higher-priority system/developer/user instructions → (2) explicit user requirements & confirmed decisions → (3) this CLAUDE.md → (4) existing project architecture/conventions → (5) general engineering preference. Do not reinterpret an explicit user decision just because another solution seems technically preferable.

## 1. Core Non-Negotiable Rules

1. Understand before coding. 2. Design before implementation. 3. Inspect existing code before creating new code. 4. Reuse before creating. 5. Follow the project's existing architecture and framework conventions. 6. Implement exactly what was requested. 7. Do not silently expand scope. 8. Do not over-engineer. 9. Do not create unnecessary files or abstractions. 10. Do not duplicate existing logic. 11. Do not introduce avoidable bugs or regressions. 12. Better ideas require user confirmation when they change scope, behavior, or design. 13. Real testing is mandatory. 14. Passing tests alone does not mean the task is complete. 15. After testing, self-review against this document, then verify acceptance against the original requirement. 16. Do not claim completion or verification without evidence. 17. Conversation history is not durable task state; anything needed for correct continuation MUST be persisted outside it. 18. Long tasks MUST be assessed for decomposition/Subagent delegation before large-scale execution. 19. Context compression is not proof that prior execution state is correctly remembered — after context loss, recover and reconcile before continuing. 20. Actual repository, Git, and executed verification results outrank remembered conversation state.

**Decision order:** User Requirement → Confirmed Decisions → Existing Architecture & Framework Conventions → Understand Existing Code → Assess Task/Context → Decompose When Beneficial → Reuse Existing Logic → Simplest Correct Design → Minimal Necessary Change → Durable Checkpoint → Real Testing → Self-Review → Final Acceptance → Delivery. Optional improvements sit outside this flow until approved.

## 2. Requirements and Scope

**Requirements are the source of truth.** Implement exactly what was requested; do not silently reinterpret it. Do not independently add features, business rules, workflows, validations, permissions, retries, fallbacks, notifications, integrations, background jobs, configuration, abstractions, optimizations, or architectural changes unless: (1) explicitly requested; (2) already required by existing architecture; (3) technically necessary for correctness; (4) necessary for compatibility; or (5) necessary to prevent a regression.

**Change boundary.** Before any change, classify it:
- **A. Required** — directly requested → implement.
- **B. Technically necessary** — needed for the requested behavior to work correctly/safely/compatibly → implement, minimally.
- **C. Optional improvement** — useful, not required → propose; do not implement without confirmation when it changes scope, behavior, or design.
- **D. Unrelated issue** — discovered but outside the request → do not modify; report separately.

Only A and B proceed without extra confirmation. If uncertain about a material change: **stop and ask.**

**Surgical changes.** Touch only what's necessary. Don't refactor, rename, reorganize, "modernize," upgrade deps, rewrite, or reformat anything unrelated. Every changed line must trace back to the requirement, a technically necessary supporting change, correctness, compatibility, or regression prevention. Never use a requested task as an excuse for "while I'm here" cleanup — propose it separately.

**Preserve existing behavior.** Presume it is intentional. Before modifying existing logic, understand current behavior, inspect callers/dependencies/tests, and identify invariants and side effects. If existing behavior looks wrong but is out of scope, report it — don't silently fix it.

**Drift prevention.** Long tasks drift. Continuously compare current work against the original goal, requirements, scope, acceptance criteria, and confirmed decisions. "I noticed this could also be improved" must never become "I implemented the improvement." If a discovery materially changes design/behavior/architecture/scope: **stop**, explain the discovery, impact, and proposed change, and get confirmation.

**Better ideas are suggestions, not permission.** Identifying a better design is encouraged; implementing it unasked is not. Explain the idea, why it may be better, tradeoffs, and scope impact → get confirmation → then implement. Never silently substitute your preferred solution for the user's.

**No unauthorized product or business decisions.** Don't silently decide product behavior, business rules, authorization semantics, data retention, API compatibility policy, user-facing behavior, workflow, or operational policy when it isn't already defined and materially affects the outcome — follow an established safe default from existing architecture or explicit requirements, otherwise **stop and ask.**

**Stop when satisfied.** Once the requirement is implemented, acceptance criteria met, tests pass, regression reviewed, architecture respected, durable state consistent, and both reviews pass — **stop.** Don't keep changing the project because other improvements are possible.

## 3. Think and Design Before Coding

Before changing anything: read the request carefully; identify the actual goal, explicit requirements, constraints, and acceptance criteria; inspect the codebase for related implementations and reusable functionality; understand the existing architecture and framework conventions; identify affected code paths and compatibility/regression risks; assess task size and context cost; decide whether decomposition/Subagents are appropriate; design; only then implement. Don't use coding as a substitute for analysis.

Non-trivial work requires a design first: what changes, why, where; what already exists and can be reused; what stays unchanged; dependencies and side effects; how it will be tested and accepted; whether it can be safely decomposed. For substantial tasks, present a concise plan before touching code — and for substantial architectural or behavioral changes, get confirmation before implementing:

```
## Implementation Plan
### Goal / ### Requirements / ### Existing Implementation / ### Reuse
### Task Decomposition / ### Execution Mode (Direct/Subagent/Hybrid) / ### Design
### Files to Change / ### Files to Add / ### Files Not to Change
### Testing / ### Acceptance Criteria / ### Risks / ### Out of Scope
```

## 4. Reuse, Architecture and Dependencies

**Inspect before creating.** Before adding any function, method, type, interface, service, repository, utility, middleware, validator, component, adapter, abstraction, config mechanism, or file: ask "does this already exist?", then "can I reuse or extend it?" Order of preference: reuse → extend → refactor an existing abstraction if genuinely necessary → create new code only when nothing existing can reasonably satisfy the requirement. Don't reimplement business logic, validation, conversion, serialization, authz, DB access, error handling, config, constants, utilities, or framework integration that already exists.

**Follow existing architecture.** Module boundaries, dependency direction, service boundaries, framework integration, lifecycle, config, logging, error handling, testing, naming, package structure, and extension points are authoritative unless there's a justified, approved reason to change them. Don't introduce a second pattern for a problem already solved one way.

**Follow framework conventions.** Use the framework's own routing/middleware/validation/DI/serialization/config/lifecycle/error-handling/persistence/testing/plugin mechanisms rather than inventing custom ones, even if a custom solution looks easier.

**Consistency over preference.** When touching existing code, match its naming, formatting, structure, error handling, logging, testing style, and framework usage. Project consistency beats personal preference.

**Dependencies.** Before adding one: check whether the project or an existing dependency already provides it; consider whether it's simple enough to implement directly; weigh maintenance and operational cost; confirm it's in scope. Convenience alone is not a reason.

**Deployment identity is configuration, not code.** A product name, organization, logo, domain, or contact address describes a deployment, not the software. Never hardcode one into source, tests, fixtures, or generated output — read it from configuration and fall back to neutral wording, not to whichever brand happened to be first. The rule extends to identifiers that merely embed a brand (storage keys, downloaded filenames, environment variables, scopes): name them for what they hold, so the same build serves any deployment.

## 5. Complexity and Abstraction Discipline

**No abstraction explosion.** No interfaces with a single implementation absent real architectural need, no factories for trivial construction, no managers for simple logic, no wrappers around simple APIs, no generic frameworks for one use case, no config systems for fixed behavior, no extra DI/service/adapter layers without a real problem to solve. "Enterprise-grade" is not a reason.

**No file explosion.** New files must represent a meaningful responsibility or boundary. Avoid one-function files and unnecessary services, repositories, interfaces, wrappers, adapters, DTO layers, utility packages, and config files. Ask "does this genuinely need to be a separate unit?" — otherwise use the existing location.

**No speculative engineering.** Don't build for imagined future needs. Build the simple, correct, maintainable implementation for the actual current requirement.

**Quality over cleverness.** Prefer simple, clear, cohesive, predictable, readable, testable, idiomatic code. Avoid clever tricks, excessive indirection, unnecessary generics, hidden magic, metaprogramming, and extra framework layers.

## 6. Task Sizing, Decomposition and Subagents

**Sizing.** Assess long tasks before large-scale execution — not to predict exact token cost, but to catch work likely to cause excessive main-session context growth. Cost ≈ requirement complexity + exploration + implementation + tests + debugging + context retention + integration. A task can be small in code but high in context cost.

**Execution modes.**
- **Direct** — small, low-exploration, tightly coupled work (localized bug fix, small API change, adding a field, a few tests).
- **Subagent** — independently executable, clearly bounded, low-coupling, locally verifiable work with substantial exploration/implementation cost.
- **Hybrid** — large tasks: Main Agent owns requirement, architecture, boundaries, decomposition, integration, and final acceptance; Subagents investigate/implement/test bounded units.

**Mandatory evaluation.** When a task has multiple independently executable units, the Main Agent MUST evaluate whether delegation would reduce context growth, isolate high-context work, reduce repeated exploration, enable independent verification, or improve throughput without added integration risk. Evaluating is not delegating — never delegate mechanically because a task is large.

**Decomposition.** Split into units with explicit objective, scope, inputs, outputs, dependencies, files/modules, acceptance criteria, verification requirements, and integration requirements; prefer independently verifiable units and track ordering. Good candidates: independent modules, independent investigations, independent test suites, repetitive work across independent components, documentation analysis, isolated bug reproduction. Poor candidates: tightly coupled sequential logic, core architectural or domain-model decisions, shared interfaces still being designed, work where every step depends on the prior decision, changes with high concurrent-edit conflict risk.

**Responsibilities.** The Main Agent owns requirement interpretation, scope governance, architecture, decomposition, delegation decisions, important design decisions, integration, cross-subtask consistency, final verification, regression review, self-review, and acceptance — it MUST NOT outsource final acceptance. A Subagent is a bounded execution worker: it MAY investigate, implement bounded changes, run local tests, debug its scope, report findings, and update task-specific state when instructed; it MUST stay in scope, follow this contract, report actual verification results, distinguish facts from assumptions, and surface incomplete work and integration-relevant info; it MUST NOT expand overall scope or impose a material architectural/product decision outside its authority.

**Context isolation.** Send a Subagent only the context its bounded task needs, not the conversation history. Main context holds global requirements, architecture, decisions, task graph, and compact results; subagent context holds its task, relevant code, constraints, and required verification. Retain outcomes, not transcripts.

**Result contract.** Subagents return structured results, never full transcripts:

```
## Subagent Result
### Objective / ### Completed / ### Files Changed / ### Important Findings
### Decisions / ### Tests / ### Verification Evidence
### Known Issues / ### Remaining Work / ### Integration Notes / ### Scope Exceptions
```

**Parallelism.** Parallelize only when units are sufficiently independent. First check shared files, interfaces, generated code, DB/schema changes, config, dependency ordering, and merge risk. If two Subagents would touch the same critical files or shared contract, design the shared contract first, then delegate independent implementations, then integrate. Correctness beats parallelism.

## 7. Durable Task State and Continuity

**Conversation is ephemeral; state must be persisted.** Anything whose loss could cause incorrect continuation MUST live in project state: current phase/task, completed/remaining work, important decisions, confirmed constraints, scope boundaries (In Scope / Out of Scope / Known Unrelated Issues), known issues and blockers, test and verification results, next action, Subagent results, integration status.

Use `.claude/task-state.md` for long/multi-stage tasks (skip for trivial ones), and `.claude/decisions.md` when a decision record is needed. Don't create more state files than that.

**`task-state.md` is a compact state record, not a transcript.** No reasoning history, no command-by-command narrative — small enough to be useful for recovery. Format (adapt to an established project format if one exists, but keep the semantics):

```markdown
# Task State
## Task / ## Objective
## Current Phase          ANALYSIS | DESIGN | IMPLEMENTATION | TESTING | REVIEW | ACCEPTANCE | BLOCKED
## Current Step           [exact current step]
## Overall Progress       - [x] / - [ ]
## Active Work            [what is currently being executed]
## Completed Work         [verified completed work]
## Remaining Work         1. ...
## Important Decisions / ## Confirmed Constraints / ## Known Issues / Risks
## Verification           Build / Tests / Other: NOT_RUN | PASS | FAIL
## Next Action            [the next concrete action]
## Last Checkpoint        [timestamp or meaningful checkpoint identifier]
```

**Next action must be concrete.** "Run integration tests for auth middleware. If PASS, continue to regression tests. If FAIL, investigate before modifying unrelated authentication code" — not "continue implementation."

**Decision persistence.** Persist decisions that constrain future implementation: selected architecture, framework mechanism, API contract, database strategy, rejected alternatives, compatibility requirements, explicit user constraints, scope exclusions, and anything future work must not reverse without approval. Record as `Decision / Reason / Alternatives Considered / Tradeoffs / Scope / Status`. Trivial decisions don't need an ADR.

**Checkpoint protocol.** Checkpoint after analysis, after important design decisions, after each meaningful subtask, before/after major implementation phases, after significant test results, when a blocker appears, before a long execution sequence, before handing off to another context, after integrating Subagent results, and before final review. A checkpoint must answer: What is the task? What's verified complete? What remains? What decisions constrain continuation? What's next? What verification actually occurred?

**Update rules.** Update state when the phase changes, a major subtask completes, a major decision is made, a blocker appears or resolves, verification status changes, a Subagent returns a material result, integration changes overall state, or the next action changes materially. Don't update after every trivial command; don't let state go materially stale.

**Context safety.** Don't retain large amounts of unnecessary command output, repeated source, test logs, exploratory discussion, failed approaches, or Subagent transcripts — prefer compact durable summaries. Don't guess remaining tokens; use semantic triggers: once substantial exploration, implementation, debugging, or delegated work has accumulated, persist state before continuing. The goal is not to avoid compression but to make it safe.

**State authority.** When reconstructing actual state, priority is: (1) source code and filesystem → (2) Git state and diff → (3) actually executed test/build/verification results → (4) durable task state → (5) conversation history. If `task-state.md` claims something is implemented and the code doesn't contain it, reconcile before continuing. Never let a stale state file override observable repository reality.

## 8. Recovery After Context Loss

Enter **Recovery Mode** if context compression is indicated, earlier history is unavailable, the task seems inconsistent with remembered history, a long task resumes after interruption, or you're uncertain what's already implemented:

1. Stop making speculative changes. 2. Read CLAUDE.md. 3. Read `.claude/task-state.md`. 4. Read `.claude/decisions.md`. 5. Inspect Git status and relevant diff. 6. Inspect the actual source files involved. 7. Inspect relevant tests and actual verification results. 8. Compare durable state against repository reality. 9. Resolve discrepancies. 10. Reconstruct the current phase and remaining task graph. 11. Identify the exact next safe action. 12. Update durable state if necessary. 13. Only then resume implementation.

Recovery is never "read state → continue." If state and repository disagree, investigate — don't blindly trust either. Never continue on a vague memory of the previous conversation.

**Context loss must not cause duplicate work.** Before reimplementing anything, verify via Git diff, changed files, source, tests, and generated artifacts that the work isn't already present. Don't recreate existing files, repeat completed refactors, duplicate implemented logic, rerun destructive operations, or overwrite correct work.

## 9. Implementation Protocol

**Feature:** understand requirement → inspect existing implementation → identify reusable functionality → assess task size → decompose if beneficial → design → confirm design when required → persist state → implement or delegate → integrate → build → test → verify actual behavior → checkpoint → self-review → acceptance review → deliver.

**Bug fix:** reproduce → understand → create/identify a regression test → implement the minimal fix → run the regression test → run related tests → verify acceptance criteria → checkpoint → self-review → acceptance review → deliver. No speculative changes during bug fixing.

**Long-running tasks** must be staged, not run as one uncontrolled sequence: Analysis → Task Sizing → Decomposition → Design → User Confirmation (when required) → Durable State Initialization → Implementation/Delegation → Integration → Build → Testing → Checkpoint → Self-Review → Acceptance Review → Delivery. After each significant stage, compare against the original goal; if drifting, **stop and reassess.**

**When blocked:** record the blocker, what's already verified, what remains unresolved, and the exact next decision or action in durable state. Don't silently work around a blocker by changing scope. Ask the user when input is required; if it can be resolved safely within scope, resolve it and checkpoint before continuing. Record `Attempt / Result / Cause / Resolution` concisely — keep the evidence, not the debugging transcript.

**Preserve project health.** After implementation, clean up artifacts your change introduced: unused imports/variables/functions, dead branches, orphaned files, duplicate code, obsolete config, unnecessary dependencies, temporary debugging code. Don't remove unrelated pre-existing code unless asked.

**Before committing, MUST clean or exclude non-deliverable artifacts.** Check `git status` before staging; never blind-stage with `git add -A`/`git add .`. Build outputs, compiled binaries, coverage/test reports, logs, caches, temporary scratch files, editor/OS files, and local-only config MUST NOT be committed — delete them or add them to `.gitignore` (prefer `.gitignore` for anything the toolchain regenerates). Never commit a file you cannot justify as part of the requested change.

## 10. Testing Discipline

**Real testing is mandatory.** Code isn't complete because it looks correct, compiles, passes static analysis, or "should work." Before delivery: build the affected code; run relevant automated tests; add tests when coverage is insufficient; execute the actual functionality when practical; test important success paths, failure paths, and edge cases; verify acceptance criteria.

**Test the actual requirement, not just the code.** Technical verification (build succeeds, relevant and regression tests pass, static checks pass, no obvious runtime errors) AND requirement verification (every requested behavior exists with the intended semantics, important edge cases handled, required existing behavior preserved, acceptance criteria satisfied).

**Compilation proves compile-time constraints only** — not business correctness, API correctness, or runtime/integration/edge-case behavior. Compiles ≠ works; works on one path ≠ fully verified.

**Never weaken tests to pass.** Don't delete failing tests, weaken assertions without justification, change expected behavior to match a broken implementation, skip relevant tests without explanation, or suppress failures. On a failing test: investigate → determine cause → fix the implementation (or correct a genuinely incorrect test) → rerun → verify.

**Claims must be earned.** Never say "tests pass" unless they were executed; never say "it works" or "verified" without evidence. If testing can't be performed, state what couldn't be tested and why, do all feasible verification, and separate tested from untested behavior. Never declare completion on assumptions — no "this should work," "probably correct," "effectively done." Implemented, Built, Tested, Verified, and Accepted are different states; don't conflate them.

## 11. Review, Delivery Gate and Final Response

Nothing is complete until two review passes run — **Self-Review** against this document, then **Final Acceptance Review** against the *original user request* (not just the tests). Tests Passed ≠ Requirement Accepted. Neither pass may be skipped; for trivial tasks they may be brief. If a review finds a violation, fix it before delivery — don't mention it and keep claiming completion.

```
[ ] Requirement — implemented exactly what was requested; nothing missed; nothing unrequested added.
[ ] Scope — no silent expansion, no unauthorized business/product decision, no unapproved optional improvement,
    no substitution of a preferred design for the requested one.
[ ] Reuse — searched for existing functionality, reused it where appropriate, did not duplicate logic.
[ ] Architecture — followed existing architecture and framework conventions; no unnecessary new pattern.
[ ] Complexity — no unnecessary files, abstractions, or dependencies; could not be materially simpler.
[ ] Regression — existing required behavior preserved; regression risk considered and verified.
[ ] Delegation — task size assessed; Subagents used only with clear boundaries and actual verification;
    Main Agent kept ownership of architecture and acceptance.
[ ] Durable state — current and accurate (decisions, remaining work, next action); repo reality matches
    recorded state; another context could safely resume from it.
[ ] Testing — actually built; relevant tests actually executed and passed; failure paths and edge cases
    covered; tests actually prove the requirement.
[ ] Commit hygiene — reviewed `git status`/staged diff; no build outputs, logs, caches, temp files,
    or other junk artifacts are being committed.
[ ] Acceptance — every acceptance criterion of the original request verified.
### Final Result — PASS / NOT READY
```

Only PASS is eligible for delivery. If any important item is false, do not claim full completion.

**Required final response format:**

```
## Completed
### Implemented / ### Changed / ### Reused
### Delegation — Direct / Subagent / Hybrid; subtasks delegated: ...
### Tests — Command/test: ... ; Result: PASS
### Acceptance — Requirement 1/2/3: PASS
### Self-Review — Scope / Reuse / Architecture / Regression / Complexity / Durable State: PASS
### Notes — ...
```

If something couldn't be tested or verified, state it explicitly — don't hide limitations.

## 12. Repository Conventions

**Workspace layout.** `apps/backend-api` is Go. `apps/frontend-web-admin` is a bun + Turborepo workspace (`apps/*` for deployables, `packages/*` for shared code). Run frontend checks from `apps/frontend-web-admin` — `bun run typecheck`, `bun run lint`, `bun run test` fan out to every package. Invoking `tsc` directly from the wrong directory emits `.js` beside the sources and pollutes the tree; if that happens, delete the output before staging.

**Line endings.** Each area declares its own `.gitattributes` (`text eol=lf`) and `core.autocrlf` is `false`. `apps/frontend-web-admin` has no such file yet, so it is checked out and committed as CRLF. Until that is resolved, be aware that `sed -i`, `awk`, and `cat >` silently rewrite a file to LF and turn a three-line change into a whole-file diff. After any such edit, restore the endings and confirm the diff is the size you expect before staging.

**Generated files.** Anything marked AUTO-GENERATED is derived output. Change the generator or its input and regenerate; never hand-edit the result. A generator must produce the same bytes on every machine — normalize platform-dependent input rather than copying it through.

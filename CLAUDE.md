# CLAUDE.md — Engineering Execution, Continuity & Delivery Contract

Mandatory rules for AI-assisted work on this project: how tasks are understood, scoped, designed, decomposed, delegated, implemented, verified, recovered, reviewed, and delivered.

**Goal:** Deliver exactly the requested solution — Correctness + Requirement Fidelity + State Continuity + Controlled Scope + Engineering Quality + Real Verification. Optimize for this, not for lines of code, agent count, or number of "improvements."

## 0. Normative Language & Priority

- **MUST/REQUIRED** — mandatory, no exceptions unless the user explicitly changes the rule.
- **MUST NOT/PROHIBITED** — forbidden.
- **SHOULD** — strongly preferred absent a documented reason otherwise.
- **SHOULD NOT** — strongly discouraged absent a documented reason.
- **MAY/OPTIONAL** — permitted, not required.

Conflict priority: (1) higher-priority system/developer/user instructions → (2) explicit user requirements & confirmed decisions → (3) this CLAUDE.md → (4) existing project architecture/conventions → (5) general engineering preference. Do not reinterpret an explicit user decision just because another solution seems technically preferable.

## 1. Core Non-Negotiable Rules

1. Understand before coding. 2. Design before implementation. 3. Inspect existing code before creating new code. 4. Reuse existing functionality before creating new functionality. 5. Follow the project's existing architecture and framework conventions. 6. Implement exactly what the user requested. 7. Do not silently expand scope. 8. Do not over-engineer. 9. Do not create unnecessary files or abstractions. 10. Do not duplicate existing logic. 11. Do not introduce avoidable bugs or regressions. 12. Better ideas require user confirmation when they change scope, behavior, or design. 13. Real testing is mandatory. 14. Passing tests alone does not mean the task is complete. 15. After testing, perform an independent self-review against this document. 16. After self-review, perform final acceptance verification against the original requirements. 17. Do not claim completion or verification without evidence. 18. Conversation history is not durable task state. 19. Information required for correct continuation MUST be persisted outside the conversation. 20. Long tasks MUST be assessed for decomposition/Subagent delegation before large-scale execution. 21. Context compression MUST NOT be treated as proof that prior execution state is still correctly remembered. 22. After context loss or suspected compression, recover and reconcile state before continuing. 23. Actual repository state, source code, filesystem, Git state, and executed verification results take precedence over remembered conversation state.

## 2. Requirements Are the Source of Truth

Implement exactly what was requested — do not silently reinterpret it. Do not independently add features, business rules, workflows, validations, permissions, retries, fallbacks, notifications, integrations, background jobs, configuration, abstractions, optimizations, or architectural changes unless: (1) explicitly requested; (2) already required by existing architecture; (3) technically necessary for correctness; (4) necessary for compatibility; or (5) necessary to prevent a regression. Anything merely "useful but not required" is an optional improvement — propose it and get confirmation before implementing if it changes scope, behavior, or design.

## 3. Scope Governance

Every proposed change falls into one bucket:
- **A. Required** — directly requested → implement.
- **B. Technically necessary** — needed for the requested behavior to work correctly/safely/compatibly → implement, minimally.
- **C. Optional improvement** — useful, not required → propose, don't implement without confirmation when it changes scope/behavior/design.
- **D. Unrelated issue** — discovered but outside the request → don't modify; report separately.

Before any change ask: Required? Technically necessary? Optional? Unrelated? If uncertain about a material change: **stop and ask.**

## 4. Scope Drift Prevention

Long tasks are especially prone to drift. Continuously compare current work against the Original Goal, Requirements, Scope, Acceptance Criteria, and Confirmed Decisions. Repository discoveries must not silently expand the task — "I noticed this could also be improved" must never become "I implemented the improvement." If a discovery materially changes design/behavior/architecture/scope: **stop**, explain the discovery + impact + proposed change, and get confirmation before proceeding.

## 5. Better Ideas Are Suggestions, Not Permission

Identifying a better design/architecture/algorithm/performance strategy is encouraged, but it is not permission to implement it. When an improvement exceeds the explicit requirement or materially changes the implementation: explain the idea, why it may be better, tradeoffs, and scope impact → ask for confirmation → implement only after confirmation. Never silently substitute your preferred solution for the user's requested/confirmed one.

## 6. Think Before Coding

Before changing anything: read the request carefully; identify the actual goal, explicit requirements, constraints, and acceptance criteria; inspect the relevant codebase for related implementations and reusable functionality; understand the existing architecture and framework conventions; identify affected code paths and compatibility/regression risks; assess task size and context cost; decide whether decomposition/Subagents are appropriate; design the solution; only then implement. Do not use coding as a substitute for analysis, and do not create files merely because a feature looks easy.

## 7. Design Before Implementation

Non-trivial work requires a design first: what exactly needs to change, why, where; what already exists and can be reused; what stays unchanged; dependencies and side effects; how it will be tested and how acceptance is determined; whether it can be safely decomposed/delegated. For substantial tasks, present a concise plan before touching code:

```
## Implementation Plan
### Goal / ### Requirements / ### Existing Implementation / ### Reuse
### Task Decomposition / ### Execution Mode (Direct/Subagent/Hybrid) / ### Design
### Files to Change / ### Files to Add / ### Files Not to Change
### Testing / ### Acceptance Criteria / ### Risks / ### Out of Scope
```

For substantial architectural/behavioral changes, get user confirmation before implementing.

## 8. Existing Code and Architecture

**Inspect before creating.** Before adding any function, method, type, interface, service, repository, utility, middleware, validator, component, adapter, abstraction, config mechanism, or file: ask "does this already exist?", then "can I reuse or extend it?" Order of preference: reuse existing implementation → extend it → refactor an existing abstraction if genuinely necessary → create new code only when nothing existing can reasonably satisfy the requirement.

**No duplicate wheels.** Don't reimplement the same problem twice (business logic, validation, conversion, serialization, authz, DB access, error handling, config, constants, utilities, framework integration) — investigate existing similar code first.

**Follow existing architecture.** Module boundaries, dependency direction, service boundaries, framework integration, lifecycle, config, logging, error handling, testing, naming, package structure, and extension points are authoritative unless there's a justified, approved reason to change them. Don't introduce a second pattern for a problem already solved one way.

**Follow framework conventions.** Use the framework's own routing/middleware/validation/DI/serialization/config/lifecycle/error-handling/persistence/testing/plugin mechanisms rather than inventing custom ones, even if a custom solution looks easier.

**Consistency over preference.** When touching existing code, match its naming, formatting, module structure, error handling, logging, testing style, and framework usage. Don't rewrite working code for stylistic reasons — project consistency beats personal preference.

## 9. Surgical Changes

Touch only what's necessary. Don't refactor, rename, reorganize, "modernize," upgrade deps, rewrite, or reformat anything unrelated to the task. Every changed line must trace back to the requirement, a technically necessary supporting change, correctness, compatibility, or regression prevention.

## 10. Preserve Existing Behavior

Presume existing behavior is intentional unless proven otherwise. Before modifying existing logic: understand current behavior, inspect callers/dependencies/tests, identify invariants and side effects. Don't casually replace working logic. If existing behavior looks problematic but is out of scope: don't silently fix it — report it separately.

## 11. Complexity and Abstraction Discipline

**Avoid abstraction explosion.** No interfaces with a single implementation absent real architectural need, no factories for trivial construction, no managers for simple logic, no wrappers around simple APIs, no generic frameworks for one use case, no config systems for fixed behavior, no extra DI layers/service layers/adapters without a real problem to solve. "Enterprise-grade" is not a reason.

**Avoid file explosion.** New files must represent a meaningful responsibility/boundary. Avoid one-function files, unnecessary services/repositories/interfaces/wrappers/adapters/DTO layers/utility packages/config files. Ask "does this genuinely need to be a separate unit?" before creating one; otherwise use the existing location.

**Quality over cleverness.** Aim for simple, clear, cohesive, predictable, readable, testable, maintainable, idiomatic, consistent code. Avoid clever tricks, excessive indirection, unnecessary generics, hidden behavior/magic, metaprogramming, and extra framework layers. Prefer boring, obvious, high-quality engineering.

**No speculative engineering.** Don't build for imagined future needs ("we may need it later," "more flexible/scalable/enterprise-grade," "helps future development"). Build the simple, correct, maintainable implementation for the actual current requirement.

## 12. Dependency Discipline

Before adding a dependency: check whether the project or an existing dependency already provides the functionality; consider whether it's simple enough to implement directly; weigh maintenance/operational cost; confirm it's in scope. Don't add a dependency just because it's convenient.

## 13. Task Sizing and Execution Mode

Assess long tasks before large-scale execution — not to predict exact token cost, but to catch work likely to cause excessive main-session context growth. Task cost ≈ requirement complexity + codebase exploration cost + implementation cost + test cost + debugging cost + context retention cost + integration cost. A task can be small in code but high in context cost if it requires reading many files/APIs/deps/logs/tests/unrelated modules.

Execution modes:
- **Direct** — small, low-complexity, low-exploration, low-context, tightly coupled work (localized bug fix, small API change, adding a field, a few tests).
- **Subagent** — independently executable, clearly bounded, low-coupling, locally verifiable work likely to consume substantial exploration/implementation context (independent module investigation, isolated implementation/tests, repetitive analysis across independent components).
- **Hybrid** — for large tasks: Main Agent understands the requirement, designs architecture, defines boundaries, decomposes work, delegates independent work, integrates results, and performs final verification/acceptance; Subagents investigate/implement/test bounded work units.

**Mandatory evaluation:** when a task has multiple independently executable units, the Main Agent MUST evaluate whether Subagent delegation would reduce context growth, isolate high-context work, reduce repeated exploration, enable independent verification, or improve throughput without added integration risk. Evaluating doesn't mean delegating — never delegate mechanically just because a task is large.

## 14. Task Decomposition Protocol

When decomposition is appropriate, split into work units with explicit objective, scope, inputs, outputs, dependencies, files/modules involved, acceptance criteria, verification requirements, and integration requirements. Prefer independently verifiable units and track dependency ordering between them. Don't decompose merely to spawn more agents.

Good candidates: independent modules, independent codebase investigations, independent test suites, repetitive work across independent components, documentation analysis, isolated bug reproduction, isolated implementation tasks.

Poor candidates: tightly coupled sequential logic, core architectural decisions, domain model decisions needing full-task context, shared interfaces still being designed, work where every step depends on the prior decision, changes where concurrent edits create high merge/integration risk.

## 15. Main Agent and Subagent Responsibilities

**Main Agent owns:** requirement interpretation, scope governance, architecture, task decomposition, dependency graph, delegation decisions, important design decisions, integration, cross-subtask consistency, final verification, regression review, self-review, acceptance, final delivery. It must not outsource final acceptance responsibility just because implementation was delegated.

**Subagent** is an execution-plane worker for a bounded task. MAY investigate, inspect code, implement bounded changes, run local tests, debug its assigned scope, report findings, update task-specific state when instructed. MUST stay within assigned scope, avoid unrelated refactoring, follow project architecture and this contract, report actual verification results, distinguish facts from assumptions, and identify incomplete work/known issues/integration-relevant info. MUST NOT expand overall task scope or silently impose a material architectural/product decision outside its authority.

## 16. Subagent Context Isolation

Subagents are also a context-isolation mechanism: send only the context a bounded task actually needs, not the whole conversation history. Main context should hold global requirements, architecture, important decisions, task graph, and compact results; subagent context should hold its bounded task, relevant code, relevant constraints, and required verification. Retain outcomes in the Main Agent, not full execution transcripts.

## 17. Subagent Result Contract

Subagents must return concise, structured results, not full execution transcripts:

```
## Subagent Result
### Objective / ### Completed / ### Files Changed / ### Important Findings
### Decisions / ### Tests / ### Verification Evidence
### Known Issues / ### Remaining Work / ### Integration Notes / ### Scope Exceptions
```

Never claim a task complete merely because code was changed — verification status must be explicit.

## 18. Parallelism and Conflict Control

Parallelize only when work units are sufficiently independent. Before parallel delegation, check shared files, shared interfaces, shared generated code, shared DB/schema changes, shared config, dependency ordering, merge/conflict risk, and integration complexity. If two Subagents would likely touch the same critical files or shared contract simultaneously, don't parallelize blindly — design the shared contract first, then delegate independent implementations, then integrate. Correctness beats parallelism.

## 19. Durable Task State

Conversation is ephemeral execution context, not authoritative state — anything whose loss could cause incorrect continuation MUST be persisted in project state: current phase/task, completed/remaining work, important decisions, confirmed constraints, scope boundaries, known issues/blockers, test results, verification status, next action, Subagent results, integration status.

For long/multi-stage tasks, use `.claude/task-state.md` as the default durable state file (skip for trivial tasks). For complex tasks needing a decision record, add `.claude/decisions.md`. Don't create more state files than that.

`task-state.md` is a compact state record, not a transcript — it should contain Current State, Completed, Remaining, Important Decisions, Constraints, Risks/Known Issues, Verification, Next Action. It should not contain full reasoning history or a command-by-command narrative. Keep it small enough to be useful for recovery.

## 20. Task State Format

```markdown
# Task State
## Task
[Task name]
## Objective
[Required outcome]
## Current Phase
ANALYSIS | DESIGN | IMPLEMENTATION | TESTING | REVIEW | ACCEPTANCE | BLOCKED
## Current Step
[Exact current step]
## Overall Progress
- [ ] ...
- [x] ...
## Active Work
[What is currently being executed]
## Completed Work
[Verified completed work]
## Remaining Work
1. ...
## Important Decisions
- ...
## Confirmed Constraints
- ...
## Known Issues / Risks
- ...
## Verification
Build: NOT_RUN / PASS / FAIL
Tests: NOT_RUN / PASS / FAIL
Other verification: ...
## Next Action
[The next concrete action]
## Last Checkpoint
[Timestamp or meaningful checkpoint identifier]
```

Adapt the exact format to an established project format if one exists, but keep this semantic information available.

## 21. State Authority and Reconciliation

Priority when reconstructing actual task state: (1) actual source code/filesystem state → (2) actual Git state and diff → (3) actual executed test/build/verification results → (4) durable task state → (5) conversation history. The repository is evidence of what actually exists; the state file is evidence of intended execution state; conversation memory is only contextual assistance. If `task-state.md` claims a feature is implemented but the source code doesn't contain it, don't assume it's implemented — reconcile the discrepancy before continuing. Never let a stale state file override observable repository reality.

## 22. Checkpoint Protocol

Long-running tasks must checkpoint — after analysis, after important design decisions, after completing a meaningful subtask, before/after major implementation phases, after significant test results, when a blocker is discovered, before a long execution sequence, before handing off to another execution context, after integrating Subagent results, and before final review. A checkpoint must update durable state enough to answer: What is the task? What's verified complete? What remains? What decisions constrain continuation? What's the next action? What verification actually occurred? Don't wait until the end of a large task to record state.

## 23. Context Safety and Compression

Treat conversation context as a finite working resource — don't intentionally retain large amounts of unnecessary command output, repeated source code, test logs, exploratory discussion, failed approaches, redundant explanations, or Subagent transcripts. Prefer compact durable summaries for information needed later.

Don't rely on guessing remaining tokens — use semantic checkpoint triggers: once a task has accumulated substantial exploration, implementation, debugging, or delegated work, persist current state before continuing. The goal isn't to prevent every compression event, but to make compression safe.

When a task can be safely isolated, use Subagents to keep the Main Agent's context focused on requirements, architecture, task graph, important decisions, compact results, integration state, and acceptance — but don't use Subagents just to save context if doing so raises correctness/integration risk.

## 24. Context Compression Recovery Protocol

If context compression is indicated, earlier task history is no longer fully available, you can't reliably reconstruct previous execution state, the current task seems inconsistent with remembered history, a long task resumes after interruption, or you're uncertain what's already implemented — enter **Recovery Mode**:

1. Stop making speculative changes. 2. Read CLAUDE.md. 3. Read `.claude/task-state.md` if it exists. 4. Read `.claude/decisions.md` if it exists. 5. Inspect Git status and relevant diff. 6. Inspect the actual source files involved. 7. Inspect relevant tests and actual verification results where available. 8. Compare durable state against repository reality. 9. Identify and resolve discrepancies. 10. Reconstruct the current task phase. 11. Reconstruct the remaining task graph. 12. Identify the exact next safe action. 13. Update durable state if necessary. 14. Only then resume implementation.

Recovery is never just "read state → continue." It is: read durable state → inspect repository reality → inspect Git → inspect verification evidence → compare → resolve discrepancies → determine current state → continue. If state and repository disagree, investigate — don't blindly trust either one. Never continue merely on a vague memory of the previous conversation.

## 25. Context Loss Must Not Cause Duplicate Work

After recovery, explicitly verify previously reported work is actually present — inspect Git diff, changed files, relevant source code, tests, generated artifacts, and durable task state before reimplementing anything. Don't recreate existing files, repeat completed refactors, duplicate implemented logic, rerun destructive operations unnecessarily, or overwrite correct work just because conversation history was lost.

## 26. Decision Persistence

Persist decisions that affect future implementation: selected architecture, selected framework mechanism, API contract, database strategy, rejected alternatives, compatibility requirements, explicit user constraints, scope exclusions, and decisions future work must not reverse without approval.

```
Decision / Reason / Alternatives Considered / Tradeoffs / Scope / Status
```

Not every trivial decision needs a separate ADR; use `.claude/decisions.md` or the project's established ADR mechanism for substantial ones.

## 27. Scope Persistence

Preserve explicit scope boundaries in durable state: In Scope, Out of Scope, Confirmed Decisions, Known Unrelated Issues. This prevents context compression from letting an old discovery be reinterpreted as a new requirement.

## 28. Next Action Must Be Concrete

State the next safe action specifically enough that a recovered context can continue without reconstructing the whole conversation. Prefer "Run integration tests for auth middleware. If PASS, continue to regression tests. If FAIL, investigate before modifying unrelated authentication code." over "Continue implementation."

## 29. State Update Rules

Update durable state when a meaningful task phase changes, a major subtask completes, a major decision is made, a blocker appears/resolves, verification status changes, a Subagent returns a material result, integration changes overall state, or the next action changes materially. Don't update after every trivial command, and don't let state go materially stale during a long execution.

## 30. Implementation Protocol

**Feature:** understand requirement → inspect existing implementation → identify reusable functionality → assess task size → decompose if beneficial → design → confirm design when required → persist important state → implement directly or delegate bounded work → integrate → build → test → verify actual behavior → checkpoint → self-review → final acceptance review → deliver.

**Bug fix:** reproduce → understand → create/identify a regression test → implement minimal fix → run regression test → run related tests → verify acceptance criteria → checkpoint → self-review → final acceptance review → deliver. No speculative changes during bug fixing.

## 31. No "While I'm Here" Refactoring

Don't use a requested task as an excuse to refactor unrelated code, however tempting the cleanup. If a refactor is genuinely valuable, propose it separately — don't include it silently.

## 32. Preserve Project Health

After implementation, check for and clean up artifacts your change introduced: unused imports/variables/functions, dead branches, orphaned files, duplicate code, obsolete config, unnecessary dependencies, temporary debugging code, generated artifacts that shouldn't be committed. Don't remove unrelated pre-existing code unless explicitly requested.

## 33. Testing Discipline

**Real testing is mandatory.** Code isn't complete because it looks correct, compiles, passes static analysis, or "should work." Before delivery: build/compile the affected code; run relevant automated tests; add tests when coverage is insufficient; execute the actual functionality when practical; test important success paths, failure paths, and edge cases; verify acceptance criteria.

**Claims must be earned.** Never say "tests pass" unless they were actually executed. Never say "it works" or "verified" without evidence. If testing can't be performed, state what couldn't be tested, why, do all feasible verification, and clearly separate tested from untested behavior.

**Test the actual requirement**, not just the code: technical verification (build succeeds, relevant/regression tests pass, static checks pass, no obvious runtime errors) AND requirement verification (every requested behavior exists and matches intended semantics, important edge cases handled, required existing behavior preserved, acceptance criteria satisfied).

**Never weaken tests to pass:** don't delete failing tests, weaken assertions without justification, change expected behavior to match a broken implementation, skip relevant tests without explanation, or suppress/hide failures. On a failing test: investigate → determine cause → fix the implementation or correct a genuinely incorrect test → rerun → verify.

**Compilation proves compile-time constraints only** — not business correctness, API correctness, runtime/integration/edge-case behavior, or acceptance criteria. Compiles ≠ works; works on one path ≠ fully verified.

## 34. Mandatory Post-Implementation Self-Review

After implementation and testing, before reporting success, run a second-pass review:

```
## Self-Review
### Requirement Compliance — implemented exactly what was requested? missed anything? added anything unrequested?
### Scope Compliance — stayed in scope? silently expanded? implemented an optional improvement without approval?
### Reuse Compliance — searched for existing functionality? reused it where appropriate? duplicated logic?
### Architecture Compliance — followed existing architecture/framework conventions? introduced a new pattern unnecessarily?
### Complexity Review — unnecessary files/abstractions/dependencies? could it be materially simpler?
### Task Decomposition Review — was decomposition appropriate? Subagents used only with clear boundaries? did delegation reduce context cost without extra integration risk? did Main Agent keep ownership of architecture/acceptance?
### Durable State Review — state current? decisions persisted? completed/remaining accurate? next action clear? scope/constraints persisted?
### Recovery Safety Review — could another context safely resume from durable state? does repo reality match recorded state? are Git/verification results consistent?
### Regression Review — altered existing behavior? could this regress? verified affected behavior?
### Testing Review — actually built? actually ran relevant tests? tested failure paths and edge cases? do tests actually prove the requirement?
### Unauthorized Change Review — added anything unrequested? made a product/business decision without confirmation? substituted your preferred design for the requested one?
### Final Judgment — actually ready to deliver?
```

If the self-review finds a violation, fix it before delivery — don't just mention it and keep claiming completion.

## 35. Mandatory Post-Test Acceptance Review

Testing is not the final step. After tests pass, compare the final implementation against the *original user request* (not just the tests):

```
## Final Acceptance Review
### Original Requirement — [restate the actual requested outcome]
### Implemented — [what was actually implemented]
### Verified — [what was actually tested]
### Acceptance Criteria — [ ] Requirement 1 / 2 / 3 satisfied
### Scope — [ ] no unauthorized feature / [ ] no unnecessary behavior / [ ] no unapproved architectural change
### Regression — [ ] existing required behavior preserved / [ ] relevant regression tests pass
### Engineering Quality — [ ] existing functionality reused / [ ] conventions followed / [ ] no unnecessary files, abstractions, duplicate logic, or obvious unnecessary complexity
### State Continuity — [ ] durable state current / [ ] decisions persisted / [ ] remaining work accurate / [ ] no unresolved state/repo discrepancy
### Final Result — PASS / NOT READY
```

Only PASS is eligible for delivery. Tests Passed ≠ Requirement Accepted: the delivery-ready state requires Tests Passed + Requirement Verified + Scope Verified + Architecture Verified + Regression Reviewed + State Verified.

## 36. No False Completion

Never declare completion on assumptions — avoid "this should work," "it should be fine," "I believe the tests would pass," "probably correct," "effectively done" unless actual verification supports it. Use precise, non-conflated status language: Implemented, Built, Tested, Verified, Accepted are different states.

## 37. Long-Running Task Protocol

For large tasks, don't run as one uncontrolled sequence. Stage it: Analysis → Task Sizing → Decomposition → Design → User Confirmation (when required) → Durable State Initialization → Implementation/Delegation → Integration → Build → Testing → Checkpoint → Self-Review → Acceptance Review → Delivery. After each significant stage, compare current state against the original goal; if drifting, **stop and reassess.**

## 38. Change Boundary Rule

Before any change: Required? Technically necessary? Optional improvement? Unrelated? Only the first two can normally proceed without extra confirmation (see §3). If uncertain about a material change: **stop and ask.**

## 39. Failure, Blocking and Recovery

When execution is blocked: record the blocker in durable state; record what's already verified and what remains unresolved; record the exact next decision/action needed; don't silently work around the blocker by changing scope; ask the user if input is required; if it can be resolved safely within scope, resolve it and update state; checkpoint after resolution before continuing. Don't erase evidence of a failed attempt if it's relevant to future decisions — record Attempt / Result / Cause / Resolution concisely, without retaining unnecessary debugging transcript.

## 40. No Unauthorized Product or Business Decisions

Don't silently decide product behavior, business rules, authorization semantics, data retention policy, API compatibility policy, user-facing behavior, workflow policy, or operational policy when it isn't already defined and materially affects the requested outcome — **stop and ask.** Follow an already-established safe default from existing architecture or explicit requirements when one exists.

## 41. Stop When the Requirement Is Satisfied

Once requested functionality is implemented, acceptance criteria are satisfied, tests pass, regression risk is reviewed, architecture is respected, durable state is consistent, self-review passes, and final acceptance passes — **stop.** Don't keep modifying the project just because other improvements are possible.

## 42. Final Delivery Gate

Nothing is complete until it passes: 1. Requirement Review 2. Scope Review 3. Task Decomposition Review 4. Durable State Review 5. Reuse Review 6. Architecture Review 7. Complexity Review 8. Regression Review 9. Real Test Verification 10. Self-Review 11. Final Acceptance Review 12. Delivery. Don't skip the gate for small tasks — for trivial ones the review may be brief, but it must still occur.

## 43. Final Delivery Checklist

Before reporting completion, confirm all of the following are true:

```
[ ] Understood the original requirement; implemented exactly the requested behavior.
[ ] Did not silently expand scope, add unauthorized business logic, or implement optional improvements without approval.
[ ] Inspected existing code; searched for and reused existing functionality where appropriate; did not duplicate logic.
[ ] Followed existing architecture and framework conventions.
[ ] Did not create unnecessary files, abstractions, or dependencies.
[ ] Preserved existing required behavior; considered and mitigated regression risk; no known avoidable bug introduced.
[ ] Assessed task size for substantial work; evaluated Subagent delegation where appropriate; did not delegate work requiring centralized architectural ownership.
[ ] Subagent work stayed in scope and was actually verified.
[ ] Durable task state is current: decisions, remaining work, and next action are accurate and recorded.
[ ] Code builds successfully; relevant tests were actually executed and passed; important edge cases and failure paths verified; acceptance criteria verified.
[ ] Repository state is consistent with durable task state.
[ ] Performed post-test self-review and final acceptance review.
[ ] Final implementation is within original scope and ready for delivery.
```

If any important item is false, do not claim full completion.

## 44. Required Final Response Format

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

## 45. Engineering Decision Priority

When deciding how to implement something, follow this order: User Requirement → Confirmed Decisions → Existing Architecture & Framework Conventions → Understand Existing Code → Assess Task/Context → Decompose When Beneficial → Reuse Existing Logic → Simplest Correct Design → Minimal Necessary Change → Durable Checkpoint → Real Testing → Self-Review → Final Acceptance → Delivery. Optional improvements sit outside this flow until the user approves them.

## 46. Core Principles (Summary)

Think first. Understand the existing system first. Design before implementation. Assess task size before large-scale execution; decompose when it improves isolation, context efficiency, or verifiability without compromising correctness. Use Subagents as bounded execution workers and context-isolation tools, not a substitute for Main Agent ownership of architecture and acceptance. Persist important task state outside the conversation; treat conversation as ephemeral and repository state as durable evidence. Checkpoint during long tasks; after context compression or uncertain continuity, recover from durable state and reconcile with actual repository state before continuing — never rely on remembered conversation state when correctness depends on it. Reuse before reinventing; follow existing architecture and framework; implement exactly what was requested without silently expanding scope or over-engineering; avoid unnecessary files, abstractions, and duplicate logic; don't introduce new bugs; preserve existing behavior. Propose better ideas and get confirmation before they change scope, behavior, or design. Test the actual implementation for real; never claim tests passed unless they did; never stop at compilation. Run the mandatory self-review, then the mandatory final acceptance review — passing tests is not the same as acceptance. Never declare completion without verification. Deliver exactly the right solution, not the largest one.

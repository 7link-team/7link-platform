# CLAUDE.md

# Engineering Execution & Delivery Contract

This document defines the mandatory engineering rules for working on this project.

These rules are not suggestions.

They define how tasks must be analyzed, designed, implemented, tested, reviewed, and delivered.

The primary objective is:

> **Deliver exactly the requested solution, with high engineering quality, minimal unnecessary change, no avoidable regression, and real verification.**

The AI must optimize for **correctness and requirement fidelity**, not for the amount of code produced or the number of improvements made.

---

# 0. Non-Negotiable Rules

The following rules have the highest priority.

1. **Understand before coding.**
2. **Design before implementation.**
3. **Inspect existing code before creating new code.**
4. **Reuse existing functionality before creating new functionality.**
5. **Follow the project's existing architecture and framework conventions.**
6. **Implement exactly what the user requested.**
7. **Do not silently expand the scope.**
8. **Do not over-engineer.**
9. **Do not create unnecessary files or abstractions.**
10. **Do not duplicate existing logic.**
11. **Do not introduce avoidable bugs or regressions.**
12. **Better ideas require user confirmation when they change scope, behavior, or design.**
13. **Real testing is mandatory.**
14. **Passing tests alone does not mean the task is complete.**
15. **After testing, perform an independent self-review against these rules.**
16. **After self-review, perform final acceptance verification against the original requirements.**
17. **Do not claim completion or verification without evidence.**

---

# 1. Think Before Coding

**Do not start coding before understanding the problem.**

Before making changes:

1. Read the user's request carefully.
2. Identify the actual goal.
3. Identify explicit requirements.
4. Identify constraints.
5. Identify acceptance criteria.
6. Inspect the relevant codebase.
7. Find related implementations.
8. Find reusable functionality.
9. Understand the existing architecture.
10. Understand framework conventions.
11. Identify affected code paths.
12. Consider compatibility and regression risks.
13. Design the solution.
14. Only then implement.

Do not use coding as a substitute for analysis.

Do not start creating files simply because the requested feature appears easy.

---

# 2. Design Before Implementation

**Non-trivial work requires a design before implementation.**

Before coding, determine:

* What exactly needs to change?
* Why does it need to change?
* Where should it change?
* What already exists?
* What can be reused?
* What should remain unchanged?
* What dependencies are involved?
* What side effects are possible?
* How will the result be tested?
* How will acceptance be determined?

For substantial tasks, provide a concise implementation plan before modifying the code.

Use:

```text
## Implementation Plan

### Goal
What exactly must be delivered?

### Requirements
What must the implementation do?

### Existing Implementation
What relevant code already exists?

### Reuse
What existing functionality can be reused or extended?

### Design
What is the proposed implementation?

### Files to Change
Which existing files need modification?

### Files to Add
Which new files are genuinely necessary?

### Files Not to Change
Which areas are intentionally outside the scope?

### Testing
How will the implementation be verified?

### Acceptance Criteria
What must be true for the task to be considered complete?

### Risks
What could break or require special attention?

### Out of Scope
What will explicitly NOT be implemented?
```

For substantial architectural or behavioral changes, obtain user confirmation before implementation.

---

# 3. Requirements Are the Source of Truth

**The user's explicit requirements define the scope.**

When the requirement is clear:

> Implement exactly what was requested.

Do not silently reinterpret the requirement.

Do not independently add:

* features
* business rules
* workflows
* validations
* permissions
* retries
* fallbacks
* notifications
* integrations
* background jobs
* configuration
* abstractions
* optimizations
* architectural changes

unless they are:

1. explicitly requested,
2. already required by the existing architecture,
3. technically necessary for correctness,
4. necessary for compatibility, or
5. necessary to prevent a regression.

---

# 4. Better Ideas Are Suggestions, Not Permission

**A better idea is not permission to implement it.**

You are encouraged to identify better:

* designs
* architectures
* algorithms
* implementations
* performance strategies
* maintainability improvements
* security improvements

However, if the improvement goes beyond the user's explicit requirement or materially changes the implementation:

1. Explain the proposed improvement.
2. Explain why it may be better.
3. Explain the tradeoffs.
4. Explain the scope impact.
5. Ask the user for confirmation.
6. Only implement after confirmation.

Do not silently substitute your preferred solution for the user's requested solution.

---

# 5. Strict Scope Control

Every change belongs to one of four categories.

### A. Required

Directly required by the user's request.

→ Implement.

### B. Technically Necessary

Required to make the requested behavior work correctly.

→ Implement, but keep it minimal.

### C. Optional Improvement

Useful but not required.

→ Propose it. Do not implement without confirmation.

### D. Unrelated Issue

Discovered during implementation but outside the request.

→ Do not modify it.

Report it separately if relevant.

---

# 6. Scope Drift Prevention

Long tasks are particularly vulnerable to scope drift.

During implementation, continuously compare the work against:

```text
Original Goal
Original Requirements
Original Scope
Acceptance Criteria
```

Do not allow discoveries in the repository to silently expand the task.

Do not turn:

> "I noticed this could also be improved"

into:

> "I implemented the improvement."

If a discovery requires materially changing the original design:

**STOP.**

Explain the discovery and ask for confirmation.

---

# 7. Reuse Before Reinventing

**Always search for existing functionality before creating new functionality.**

Before creating a:

* function
* method
* type
* interface
* service
* repository
* utility
* helper
* middleware
* validator
* component
* adapter
* abstraction
* configuration mechanism
* file

search the existing codebase.

Ask:

> "Does this already exist?"

Then:

> "Can I reuse or extend it?"

Preferred order:

```text
1. Reuse existing implementation
2. Extend existing implementation
3. Refactor an existing abstraction if genuinely necessary
4. Create new code only when existing code cannot reasonably satisfy the requirement
```

---

# 8. No Duplicate Wheels

Do not implement the same problem multiple times.

Avoid duplicate:

* business logic
* validation
* conversion
* serialization
* authorization
* database access
* error handling
* configuration
* constants
* utilities
* framework integration

If similar code exists, investigate it before creating another implementation.

---

# 9. Avoid File Explosion

**Do not create new files by default.**

A new file should represent a meaningful responsibility or architectural boundary.

Do not create files merely to make the implementation look organized.

Avoid:

* one-function files
* one-type files without meaningful boundaries
* unnecessary service files
* unnecessary repository files
* unnecessary interfaces
* unnecessary wrappers
* unnecessary adapters
* unnecessary DTO layers
* unnecessary utility packages
* unnecessary configuration files

Before creating a file:

> "Does this genuinely need to be a separate unit?"

If not, use the existing appropriate location.

---

# 10. Avoid Abstraction Explosion

Do not create abstractions without a real reason.

Avoid:

* interfaces with only one implementation without an actual architectural need
* factories for trivial construction
* managers for simple logic
* wrappers around simple APIs
* generic frameworks for one use case
* configuration systems for fixed behavior
* additional dependency-injection layers
* unnecessary service layers
* unnecessary adapters

Abstraction must solve a real problem.

Do not create abstractions merely because they appear "enterprise-grade."

---

# 11. Follow Existing Architecture

**The existing project architecture is authoritative unless there is a justified reason to change it.**

Before implementation, understand:

* module boundaries
* dependency direction
* service boundaries
* framework integration
* lifecycle
* configuration
* logging
* error handling
* testing
* naming
* package structure
* extension points

Follow established project patterns.

Do not introduce a second pattern for solving the same problem.

---

# 12. Follow Framework Conventions

**Do not fight the framework.**

If the framework provides a standard mechanism, use it.

Do not invent custom mechanisms when the framework already provides:

* routing
* middleware
* validation
* dependency injection
* serialization
* configuration
* lifecycle management
* error handling
* persistence integration
* testing
* plugin mechanisms
* extension points

Follow the framework's intended usage.

Do not bypass the framework merely because a custom solution appears easier.

---

# 13. Consistency Over Personal Preference

When modifying existing code:

* match naming conventions,
* match formatting,
* match module structure,
* match error handling,
* match logging,
* match testing style,
* match framework usage.

Do not rewrite working code simply because you prefer another style.

Project consistency is more important than personal preference.

---

# 14. Surgical Changes

**Touch only what is necessary.**

Do not:

* refactor unrelated code,
* rename unrelated symbols,
* reorganize unrelated directories,
* modernize unrelated modules,
* upgrade dependencies unnecessarily,
* rewrite unrelated functions,
* modify unrelated comments,
* change formatting unnecessarily.

Every changed line should be traceable to:

* the requirement,
* a technically necessary supporting change,
* correctness,
* compatibility,
* or regression prevention.

---

# 15. Preserve Existing Behavior

Existing behavior should be presumed intentional unless there is evidence otherwise.

Before modifying existing logic:

* understand current behavior,
* inspect callers,
* inspect dependencies,
* inspect tests,
* identify invariants,
* identify side effects.

Do not casually replace working logic.

If existing behavior appears problematic but is outside the current requirement:

> Do not silently fix it.

Report it separately.

---

# 16. Do Not Introduce New Bugs

**Every implementation must consider regression risk.**

The objective is not:

```text
Original bug fixed
+
New bug introduced
```

The objective is:

```text
Original requirement satisfied
+
Existing required behavior preserved
+
No avoidable regression introduced
```

Before changing existing code, consider:

* callers
* dependencies
* state transitions
* concurrency
* error paths
* edge cases
* data integrity
* API compatibility
* backward compatibility
* side effects

After changing it, verify the affected behavior.

---

# 17. Real Testing Is Mandatory

**Code is not complete until it has actually been tested.**

Do not consider code verified merely because:

* it looks correct,
* it compiles,
* static analysis passes,
* the implementation appears logically correct,
* an AI model believes it should work.

Before delivery:

1. Build/compile the affected code.
2. Run relevant automated tests.
3. Add tests when coverage is insufficient.
4. Execute the actual functionality when practical.
5. Test important success paths.
6. Test relevant failure paths.
7. Test important edge cases.
8. Verify the acceptance criteria.

---

# 18. Testing Must Be Real

Never claim:

> "Tests pass"

unless the tests were actually executed.

Never claim:

> "It works"

unless the relevant behavior was actually verified.

Never claim:

> "Verified"

without evidence.

If testing cannot be performed:

* state what could not be tested,
* state why,
* perform all feasible verification,
* clearly distinguish tested and untested behavior.

---

# 19. Test the Actual Requirement

Passing tests does not automatically mean the requirement is satisfied.

Verify both:

### Technical Verification

* Build succeeds.
* Relevant tests pass.
* Regression tests pass.
* Relevant static checks pass when available.
* No obvious runtime errors are present.

### Requirement Verification

* Every requested behavior exists.
* Behavior matches the intended semantics.
* Important edge cases are handled.
* Existing behavior is preserved where required.
* Acceptance criteria are satisfied.

---

# 20. Never Weaken Tests to Make Them Pass

Do not:

* delete failing tests to get green results,
* weaken assertions without justification,
* change expected behavior merely to match a broken implementation,
* skip relevant tests without explanation,
* suppress errors,
* hide failures.

When a test fails:

```text
Investigate
    ↓
Determine cause
    ↓
Fix implementation OR correct the test if genuinely incorrect
    ↓
Run again
    ↓
Verify
```

---

# 21. Bug Fix Workflow

For bugs:

```text
Reproduce
    ↓
Understand
    ↓
Create / identify regression test
    ↓
Implement minimal fix
    ↓
Run regression test
    ↓
Run related tests
    ↓
Verify acceptance criteria
    ↓
Perform final self-review
```

Do not make speculative changes during bug fixing.

---

# 22. Feature Development Workflow

For new features:

```text
Understand requirement
    ↓
Inspect existing implementation
    ↓
Identify reusable functionality
    ↓
Design
    ↓
Confirm design when required
    ↓
Implement
    ↓
Build
    ↓
Test
    ↓
Verify actual behavior
    ↓
Self-review against engineering rules
    ↓
Final acceptance review
    ↓
Deliver
```

---

# 23. Mandatory Post-Implementation Self-Review

**After implementation and testing, perform a mandatory second-pass review.**

Do not immediately report success after tests pass.

The AI must explicitly re-check its own work against this document.

This is a separate phase from implementation.

Use:

```text
## Self-Review

### Requirement Compliance
- Did I implement exactly what was requested?
- Did I miss any requirement?
- Did I add anything that was not requested?

### Scope Compliance
- Did the implementation remain within scope?
- Did I silently expand the task?
- Did I implement any optional improvement without approval?

### Reuse Compliance
- Did I search for existing functionality?
- Did I reuse existing code where appropriate?
- Did I duplicate existing logic?

### Architecture Compliance
- Did I follow the existing architecture?
- Did I follow framework conventions?
- Did I introduce a new pattern unnecessarily?

### Complexity Review
- Did I create unnecessary files?
- Did I create unnecessary abstractions?
- Did I introduce unnecessary dependencies?
- Can the implementation be materially simpler?

### Regression Review
- Did I alter existing behavior?
- Could the change introduce a regression?
- Did I verify affected existing behavior?

### Testing Review
- Did I actually build the code?
- Did I actually run the relevant tests?
- Did I test important failure paths?
- Did I test important edge cases?
- Do the tests actually prove the requirement?

### Unauthorized Change Review
- Did I add any behavior the user did not request?
- Did I make any product or business decision without confirmation?
- Did I substitute my preferred design for the requested design?

### Final Judgment
- Is the implementation actually ready to deliver?
```

If the self-review discovers a violation:

**Fix the violation before delivery.**

Do not merely mention it and continue claiming completion.

---

# 24. Mandatory Post-Test Acceptance Review

**Testing is not the final step.**

After tests pass, perform a separate acceptance review.

The acceptance review must compare the final implementation against the **original user request**, not merely against the tests.

Use:

```text
## Final Acceptance Review

### Original Requirement
[Restate the actual requested outcome]

### Implemented
[What was actually implemented]

### Verified
[What was actually tested]

### Acceptance Criteria
- [ ] Requirement 1 satisfied
- [ ] Requirement 2 satisfied
- [ ] Requirement 3 satisfied

### Scope
- [ ] No unauthorized feature added
- [ ] No unnecessary behavior added
- [ ] No unapproved architectural change

### Regression
- [ ] Existing required behavior preserved
- [ ] Relevant regression tests pass

### Engineering Quality
- [ ] Existing functionality reused where appropriate
- [ ] Existing conventions followed
- [ ] No unnecessary files
- [ ] No unnecessary abstractions
- [ ] No duplicate logic
- [ ] No obvious unnecessary complexity

### Final Result
PASS / NOT READY
```

Only `PASS` is eligible for delivery.

---

# 25. Acceptance Is Independent From Test Results

A test suite can pass while the implementation is still wrong.

Therefore:

```text
Tests Passed
    ≠
Requirement Accepted
```

The final state must be:

```text
Tests Passed
    +
Requirement Verified
    +
Scope Verified
    +
Architecture Verified
    +
Regression Reviewed
    =
READY FOR DELIVERY
```

---

# 26. No False Completion

Never declare a task complete based on assumptions.

Do not say:

* "This should work."
* "It should be fine."
* "I believe the tests would pass."
* "The implementation is probably correct."
* "This is effectively done."

unless actual verification supports the statement.

Use precise status language:

```text
Implemented
Built
Tested
Verified
Accepted
```

These are different states.

Do not conflate them.

---

# 27. No Completion Based on Compilation Alone

Compilation proves only that the code satisfies certain compile-time constraints.

It does not prove:

* business correctness,
* API correctness,
* runtime behavior,
* edge-case behavior,
* integration behavior,
* acceptance criteria.

Therefore:

```text
Compiles
    ≠
Works
```

and:

```text
Works in one path
    ≠
Fully Verified
```

---

# 28. Long-Running Task Execution Protocol

For large tasks, do not operate as one uncontrolled sequence.

Break the task into meaningful stages:

```text
Stage 1 — Analysis
Stage 2 — Design
Stage 3 — User Confirmation (when required)
Stage 4 — Implementation
Stage 5 — Build
Stage 6 — Testing
Stage 7 — Self-Review
Stage 8 — Acceptance Review
Stage 9 — Delivery
```

After each significant stage, compare the current state against the original goal.

If the implementation begins drifting from the original requirements:

**STOP and reassess.**

---

# 29. Change Boundary Rule

Before making a change, determine which category it belongs to:

```text
Required?
Technically necessary?
Optional improvement?
Unrelated?
```

Only the first two categories can normally be implemented without additional confirmation.

If uncertain:

> Stop and ask.

---

# 30. No "While I'm Here" Refactoring

Do not use a requested task as an excuse to refactor unrelated code.

Avoid:

> "While I'm here, I also cleaned up..."

unless the cleanup is necessary for the requested change.

If a refactor is genuinely valuable:

> Propose it separately.

Do not silently include it.

---

# 31. No Speculative Engineering

Do not build imaginary future requirements.

Do not add something because:

* "we may need it later,"
* "this will make it more flexible,"
* "this is more scalable,"
* "this is more enterprise-grade,"
* "this will help future development."

Future extensibility is not automatically a requirement.

Prefer:

> Simple, correct, maintainable implementation for the actual current requirement.

---

# 32. Quality Over Cleverness

Good code should be:

* simple
* clear
* cohesive
* predictable
* readable
* testable
* maintainable
* idiomatic
* consistent

Avoid:

* clever tricks
* excessive indirection
* unnecessary generic abstractions
* hidden behavior
* magic
* unnecessary metaprogramming
* excessive framework layers

Prefer boring, obvious, high-quality engineering.

---

# 33. Dependency Discipline

Do not introduce dependencies without justification.

Before adding a dependency:

1. Check whether the project already provides the functionality.
2. Check whether an existing dependency provides it.
3. Consider whether the functionality is simple enough to implement directly.
4. Consider maintenance and operational costs.
5. Confirm that it is within scope.

Do not add a dependency simply because it is convenient.

---

# 34. Preserve Project Health

After implementation, check for artifacts introduced by the change:

* unused imports
* unused variables
* unused functions
* dead branches
* orphaned files
* duplicate code
* obsolete configuration
* unnecessary dependencies
* temporary debugging code
* generated artifacts that should not be committed

Clean up artifacts caused by your own changes.

Do not remove unrelated pre-existing code unless explicitly requested.

---

# 35. Stop When the Requirement Is Satisfied

Once:

* the requested functionality is implemented,
* acceptance criteria are satisfied,
* tests pass,
* regression risk has been reviewed,
* the architecture is respected,
* the self-review passes,

stop.

Do not continue modifying the project just because other improvements are possible.

---

# 36. Final Delivery Gate

**Nothing is considered complete until it passes the final delivery gate.**

Before delivering, execute all of the following:

```text
┌─────────────────────────────────────────────┐
│           FINAL DELIVERY GATE               │
├─────────────────────────────────────────────┤
│ 1. Requirement Review                       │
│ 2. Scope Review                             │
│ 3. Reuse Review                             │
│ 4. Architecture Review                     │
│ 5. Complexity Review                        │
│ 6. Regression Review                        │
│ 7. Real Test Verification                   │
│ 8. Self-Review                              │
│ 9. Final Acceptance Review                  │
│ 10. Delivery                                │
└─────────────────────────────────────────────┘
```

Do not skip the gate merely because the task is small.

For trivial tasks, the review may be brief, but it must still occur.

---

# 37. Final Delivery Checklist

Before reporting completion:

```text
[ ] I understand the original requirement.
[ ] I implemented exactly the requested behavior.
[ ] I did not silently expand the scope.
[ ] I did not add unauthorized business logic.
[ ] I did not implement optional improvements without approval.
[ ] I inspected the existing code.
[ ] I searched for reusable functionality.
[ ] I reused existing functionality where appropriate.
[ ] I did not duplicate existing logic.
[ ] I followed existing architecture.
[ ] I followed existing framework conventions.
[ ] I did not create unnecessary files.
[ ] I did not create unnecessary abstractions.
[ ] I did not introduce unnecessary dependencies.
[ ] I preserved existing required behavior.
[ ] I considered regression risks.
[ ] I did not introduce any known avoidable bug.
[ ] The affected code builds successfully.
[ ] Relevant tests were actually executed.
[ ] Relevant tests actually passed.
[ ] Important edge cases were verified.
[ ] Important failure paths were verified.
[ ] Acceptance criteria were verified.
[ ] I performed a post-test self-review.
[ ] I performed a final acceptance review.
[ ] The final implementation is within the original scope.
[ ] The implementation is ready for delivery.
```

If any important item is false:

> **Do not claim full completion.**

---

# 38. Required Final Response Format

When reporting a completed task, provide concise evidence.

Use:

```text
## Completed

### Implemented
- ...

### Changed
- ...

### Reused
- ...

### Tests
- Command/test: ...
- Result: PASS

### Acceptance
- Requirement 1: PASS
- Requirement 2: PASS
- Requirement 3: PASS

### Self-Review
- Scope: PASS
- Reuse: PASS
- Architecture: PASS
- Regression: PASS
- Complexity: PASS

### Notes
- ...
```

If something could not be tested or verified, state it explicitly.

Do not hide limitations.

---

# 39. Engineering Decision Priority

When deciding how to implement something, use this priority:

```text
                    ┌──────────────────┐
                    │ User Requirement │
                    └────────┬─────────┘
                             ↓
              ┌──────────────────────────┐
              │ Existing Architecture    │
              │ & Framework Conventions  │
              └────────────┬─────────────┘
                           ↓
              ┌──────────────────────────┐
              │ Understand Existing Code │
              └────────────┬─────────────┘
                           ↓
              ┌──────────────────────────┐
              │ Reuse Existing Logic     │
              └────────────┬─────────────┘
                           ↓
              ┌──────────────────────────┐
              │ Simplest Correct Design  │
              └────────────┬─────────────┘
                           ↓
              ┌──────────────────────────┐
              │ Minimal Necessary Change │
              └────────────┬─────────────┘
                           ↓
              ┌──────────────────────────┐
              │ Real Testing             │
              └────────────┬─────────────┘
                           ↓
              ┌──────────────────────────┐
              │ Self-Review              │
              └────────────┬─────────────┘
                           ↓
              ┌──────────────────────────┐
              │ Final Acceptance         │
              └────────────┬─────────────┘
                           ↓
                    ┌──────────────┐
                    │   Delivery   │
                    └──────────────┘
```

Optional improvements are outside this flow until the user approves them.

---

# 40. Core Principle

> **Think first.**
>
> **Understand the existing system first.**
>
> **Design before implementation.**
>
> **Reuse before reinventing.**
>
> **Follow the existing architecture and framework.**
>
> **Implement exactly what the user requested.**
>
> **Do not silently expand scope.**
>
> **Do not over-engineer.**
>
> **Do not create unnecessary files.**
>
> **Do not create unnecessary abstractions.**
>
> **Do not duplicate existing logic.**
>
> **Do not introduce new bugs.**
>
> **Preserve existing behavior.**
>
> **If you have a better idea, propose it first and obtain confirmation when it changes scope, behavior, or design.**
>
> **Test the actual implementation.**
>
> **Do not claim tests passed unless they actually passed.**
>
> **Do not stop at compilation.**
>
> **After testing, perform a mandatory self-review.**
>
> **After self-review, perform a mandatory final acceptance review against the original requirements.**
>
> **Passing tests is not the same as acceptance.**
>
> **Do not declare completion without verification.**
>
> **Deliver exactly the right solution — not the largest solution.**

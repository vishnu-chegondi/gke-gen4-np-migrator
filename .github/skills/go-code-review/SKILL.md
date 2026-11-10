---
name: go-code-review
description: Review Go code with make lint, resolve actionable lint issues, and assess function documentation and maintainability using practical SOLID principles. Use when asked to review or audit Go code.
---

# Go Code Review

Review the requested Go files or change set, resolve actionable issues identified by lint, and return a concise evidence-based summary. Focus on bugs and regressions first, then maintainability and documentation. Keep fixes scoped to the request; do not make unrelated refactors.

## Workflow

1. Establish scope from the user's request and the diff. Read the surrounding code, callers, tests, and relevant repository guidance before judging behavior. If no scope is specified, review the current uncommitted diff; if there is no diff, ask what to review or review the repository only when that is clearly intended.
2. Trace behavior through callers and state changes. For controller code, inspect reconcile transitions, event predicates, cache/API-reader use, retries, object updates, ownership, annotations, and the effects of partial failure.
3. Run `make lint` from the module root to use the repository's configured golangci-lint suite, including its project-specific checks. Review each diagnostic in context; resolve actionable code issues within scope, and rerun `make lint` after the edits. Do not use `make lint-fix` as a substitute for reviewing changes: if using an autofix is appropriate, inspect every resulting diff. Do not run tests or generators unless requested.
4. Review function documentation. Exported functions, methods, types, and package declarations should have useful Go doc comments beginning with the declared name where applicable. Comments should explain purpose and non-obvious contracts, side effects, or error behavior; flag missing, misleading, or stale comments. Avoid demanding parameter/return inventories that merely restate Go signatures.
5. Evaluate readability and maintainability with practical SOLID questions:
   - **Single Responsibility:** Does a function or type combine unrelated reasons to change? Is a cohesive operation split into needless tiny helpers?
   - **Open/Closed:** Are repeated conditionals or duplicated workflows making each new case risky? Prefer a small, clear extension point only when multiple real variants exist.
   - **Liskov Substitution:** Do implementations honor the behavior and error contracts implied by their interfaces? Do not invent inheritance patterns in Go.
   - **Interface Segregation:** Are consumers forced to depend on oversized interfaces? Prefer narrow interfaces at the point of use when they improve decoupling or tests.
   - **Dependency Inversion:** Is core behavior coupled to globals, environment reads, clocks, or concrete infrastructure in a way that harms correctness or tests? Suggest injection only when the seam has practical value.

Treat SOLID as a lens, not a checklist. Do not recommend abstractions merely to satisfy a principle. Prefer straightforward Go: small cohesive functions, explicit dependencies, clear errors, and standard library or existing project patterns.

## Finding standard

Report an issue only when you can identify a concrete bug, lint violation, missing contract, or meaningful maintenance risk. Fix actionable issues within the user's scope. For anything that cannot be safely resolved within scope, give each remaining finding:

- A severity (`P1` urgent, `P2` should fix, `P3` lower impact).
- A precise file and line location.
- The scenario and consequence, grounded in the code.
- A concise suggested fix.

Order remaining findings by severity. Avoid speculative style preferences and generic SOLID advice. If all issues are resolved, say so plainly and report the final `make lint` result. Mention important coverage limits, such as excluded build-tagged packages, only when they matter.

## Output

Summarize the issues fixed, any remaining findings, and the final `make lint` result. Include useful documentation or design suggestions that remain outside the lint scope. Do not claim tests were run unless they were explicitly requested and executed.

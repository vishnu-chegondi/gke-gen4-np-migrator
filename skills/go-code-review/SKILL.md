---
name: go-code-review
description: Review Go code for correctness, function documentation, go vet findings, and maintainability using practical SOLID principles. Use when asked to review or audit Go code; do not make edits unless the user also requests fixes.
---

# Go Code Review

Review the requested Go files or change set and return a prioritized, evidence-based review. Focus on bugs and regressions first, then maintainability and documentation. Keep the review read-only unless the user explicitly asks for fixes.

## Workflow

1. Establish scope from the user's request and the diff. Read the surrounding code, callers, tests, and relevant repository guidance before judging behavior. If no scope is specified, review the current uncommitted diff; if there is no diff, ask what to review or review the repository only when that is clearly intended.
2. Trace behavior through callers and state changes. For controller code, inspect reconcile transitions, event predicates, cache/API-reader use, retries, object updates, ownership, annotations, and the effects of partial failure.
3. Run `go vet ./...` from the module root. Include useful diagnostics as findings only when they are relevant and actionable. Distinguish vet findings from issues found by code inspection. Do not run tests, formatters, generators, or autofix tools unless requested.
4. Review function documentation. Exported functions, methods, types, and package declarations should have useful Go doc comments beginning with the declared name where applicable. Comments should explain purpose and non-obvious contracts, side effects, or error behavior; flag missing, misleading, or stale comments. Avoid demanding parameter/return inventories that merely restate Go signatures.
5. Evaluate readability and maintainability with practical SOLID questions:
   - **Single Responsibility:** Does a function or type combine unrelated reasons to change? Is a cohesive operation split into needless tiny helpers?
   - **Open/Closed:** Are repeated conditionals or duplicated workflows making each new case risky? Prefer a small, clear extension point only when multiple real variants exist.
   - **Liskov Substitution:** Do implementations honor the behavior and error contracts implied by their interfaces? Do not invent inheritance patterns in Go.
   - **Interface Segregation:** Are consumers forced to depend on oversized interfaces? Prefer narrow interfaces at the point of use when they improve decoupling or tests.
   - **Dependency Inversion:** Is core behavior coupled to globals, environment reads, clocks, or concrete infrastructure in a way that harms correctness or tests? Suggest injection only when the seam has practical value.

Treat SOLID as a lens, not a checklist. Do not recommend abstractions merely to satisfy a principle. Prefer straightforward Go: small cohesive functions, explicit dependencies, clear errors, and standard library or existing project patterns.

## Finding standard

Report a finding only when you can identify a concrete bug, likely regression, missing contract, or meaningful maintenance risk. Give each finding:

- A severity (`P1` urgent, `P2` should fix, `P3` lower impact).
- A precise file and line location.
- The scenario and consequence, grounded in the code.
- A concise suggested fix.

Order findings by severity. Avoid speculative style preferences and generic SOLID advice. If there are no findings, say so plainly and note the scope reviewed plus the `go vet ./...` result. Mention important coverage limits, such as excluded build-tagged packages, only when they matter.

## Output

Start with findings. If there are none, use a short “No findings” statement. Then summarize documentation gaps or useful suggestions that do not rise to findings, and report whether `go vet ./...` passed. Do not claim tests were run unless they were explicitly requested and executed.

---
kind: task
status: todo
depends: [01-dependency-and-licence]
---

# Assess security and privacy delta

## Task

Produce one bounded differential exposure assessment of the exact B/C client delta
for the [parent stage](../plan.md), using the accepted
[dependency inventory](../01-dependency-and-licence/plan.md), protocol and integrated
patch assessments. Reuse their secret-stdin, peer/FD, interface-binding, captcha and
TURN findings and source limits. Research only remaining changed client integration
and diagnostic behavior relevant to secrets, log content, network destinations and
activation conditions, permissions, persistence and exposed process interfaces.

The starting scope is changed client diagnostic/integration paths in `rust-client/`
(`main.rs`, `gooby.rs`, `stream_diagnostics.rs`, `traffic_diagnostics.rs` and directly
changed callers), mapped to existing `internal/coreproc`, supervisor and CLI
consumers where necessary. A coordinator and independent non-coding source reviewer
bound inspection by the changed-file inventory and accepted facts. Assess the new
dependency/proxy exposure at the client integration boundary; preserve unknown
external routes and opaque internals rather than undertaking a standalone proxy,
native-runtime, archive/Lua sandbox or whole-project vulnerability audit. No feature,
logging policy, permission or security contract is selected or changed.

## Done when

- A differential exposure matrix covers secret handling, diagnostics/logging,
  network behavior, permissions/persistence and process interfaces, either through
  accepted evidence, narrowly verified B/C changes or an explicit evidence gap.
  Activation/default conditions and affected local consumers are stated.
- Demonstrated source changes, unchanged safeguards required by the shell, existing
  limitations and plausible unverified risks have distinct labels. Prior bounded
  logging findings are retained; deeper diagnostic claims require new exact-ref
  evidence. Neither no discovered issue nor a disabled runtime option proves safety.
- Host/proxy-route unknowns, unresolved local Gooby label, stale DNS wording in both
  architecture and guard reason, independent captcha timers/no request ID and the
  bounded local UDP completion contract remain accurately represented. Prior patch
  and socket audits are referenced without repeating them or authorizing repairs.
- An independent non-coding reviewer verifies new source claims and their threat
  conditions. An ignored record at `doc/local/core-253-security-review.md` and a
  sanitized Outcome give `lead` concrete exposure/benefit findings, confidence,
  migration gates and limits without source snippets or retold implementation.
- The parent's read-only boundary and future-only test constraints are preserved;
  no programmer, implementation change, full external proxy audit, dependency
  download, executable/build/test/resolver/vendor command, live check, external
  write or new security-policy decision is part of the task.

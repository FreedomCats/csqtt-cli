---
kind: task
status: in-progress
detailed: true
base: 0b3ecdfbf8bfc5c5ff9b69194c58560ceed4070d
---

# Assess captcha and TURN runtime patches

## Task

Classify local P5's manual/selected captcha timeout adjustment, P7's
`ONLY_PRINT_URL` solver switch, and the local TURN UDP outbound-drain correction
against pinned 2.5.0 commit `d02da9bd9d8926969b766e197c434d830a83dbd8` (baseline)
and exact csqttr source commit `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`
(candidate). Use architecture decision Р7 and §§04, 07, and 08,
`doc/upstream-proposals.md` item 4, its P7 note under the checked-but-not-proposed
items, and proposal 28, plus the accepted
[core-CI correction Outcome](../../../09-backlog/01-small-defects/08-core-ci-stability/plan.md).
Return one runtime-patch assessment for the [parent stage](../plan.md), with the
existing captcha policy, process contract, and 2.5.0 pin as fixed requirements.

Only the coordinator and an independent non-coding reviewer may inspect incompatible
external implementation source; do not assign it to a planner or programmer. Return
behavioral facts in original wording without source, snippets, or a retelling of
implementation structure to an executor. This is read-only assessment: no code,
test, script, configuration, or pin changes, vendor generation/checks, builds, test
runs, captcha windows, live checks, or external writes are authorized.

## Steps

1. Establish the evidence boundary from `core/CORE_VERSION`, the documents above,
   the accepted [provenance](../../01-upstream-provenance/plan.md) and
   [protocol](../../02-protocol-compatibility/plan.md) Outcomes, and the
   [P3 Outcome](../03-socket-binding-p3/plan.md). Use only the two immutable commits
   named in this task. Reuse the accepted wire compatibility and direct-substitution
   blockers; do not repeat the arguments/secrets/`LISTEN` review or infer captcha
   behavior from startup failing for unrelated local flags. The generated local
   `core/` contains patches and establishes the intended local contract, not either
   unpatched upstream ref. Reuse the core-CI Outcome as historical evidence for the
   local correction, not as validation of the candidate.
2. Inventory the relevant entries in `scripts/vendorcore/rules.go`, using local
   file/line references and behavioral anchor identities rather than copied regex or
   Rust text. The current local inventory is:

   | Local block | Target | Entries | Required matches |
   |---|---|---:|---:|
   | P5, starting at `rules.go:305` | `rust-client/captcha.rs` | 4 | 4 |
   | P7, starting at `rules.go:335` | `rust-client/captcha.rs` | 1 | 1 |
   | TURN drain, starting at `rules.go:400` | `rust-client/turn.rs` | 9 | 11 |
   | TURN test helpers, starting at `rules.go:445` | `rust-client/turn_integration_tests.rs` | 2 | 2 |

   P5 covers the timeout declaration, manual wait, selected/WebView wait, and added
   test-module connection. P7 covers the early solver-disable guard. TURN covers
   queue/completion state, writer and admission accounting, bounded draining,
   cleanup, and regression-test wiring. All entries require one match except the
   TURN queue-admission entry at `rules.go:425`, which requires three. For all 16
   entries, record target, local rule identity, anchor meaning, required count,
   observed count and status independently at each ref, and exact-source references.
   Inspect/count anchors without executing rules or the vendor pipeline; distinguish
   raw upstream anchors from any earlier-rule dependency, leaving an unestablished
   effective count unknown. Separately check upstream presence/collision of the
   added targets `rust-client/captcha_window_tests.rs` and
   `rust-client/turn_outbound_tests.rs`. The eleven TURN entries are already included
   in P3's broad rule-range inventory in `doc/local/core-253-patch-review.md`; cite
   those rows, reconcile any disagreement explicitly, and keep their identities so
   [child 05](../05-vendor-rules-and-crate/plan.md) can count each rule once. Do not
   revise the accepted P3 findings or repeat its socket/crate audit.
3. Have the coordinator compare captcha behavior at both exact refs, primarily in
   `rust-client/captcha.rs`, following only directly necessary callers. Assess P5
   and P7 separately: manual waiting in the default/auto chain, selected requests in
   `wv`, actual wait budgets, immediate handoff versus prior core solver attempts,
   and whether `ONLY_PRINT_URL` prevents core solver requests before touching the
   session. Record whether that switch depends on presence or value and what happens
   when it is absent or ignored. Check result serialization, expiry, cancellation,
   and replacement-request handling only as needed to assess the shell's no-late-reply
   assumption. Keep the normal `wv` path distinct from P7's defensive role if the
   solver path is reached; a missing switch alone does not prove that normal `wv`
   runs the solver. Compare behavior with the local 180-second requirement without
   changing it, and separate baseline behavior from any candidate delta.
4. Have the coordinator compare the TURN UDP drain contract at both exact refs in
   `rust-client/turn.rs` and the directly relevant tests. Trace the boundary between
   successful admission, queued packets, writer-owned packets, completion of a local
   send attempt or discard, and shutdown. Cover async/fast admission, queue-full
   ownership return, closing admission, encoding/socket-write failure, cancellation
   or writer abort, pool-lease release, and the drain wait/notification condition.
   Determine whether the last accepted packet can still be outstanding when normal
   cleanup clears the channel or sends the zero-lifetime Refresh, and whether an
   equivalent correction accounts for both queued and writer-held packets. Compare
   the existing 250 ms best-effort flush budget and forced cleanup separately from
   the later writer-join/abort timeout. Map the two integration-test helper rules and
   added regression module to this same behavior. Do not infer equivalence from an
   unchanged anchor or from the presence of an ordering test; distinguish local
   socket-write completion from remote receipt or acknowledgement.
5. Map consequences to the concrete local shell consumers, reading only the relevant
   blocks. For captcha, follow `internal/config/profile.go`'s fixed `wv` selection,
   `internal/coreproc/config.go`'s argument, `captcha.go`'s mode/switch/no-reply
   contract, `process.go`'s environment, and `message.go`'s request parsing into
   `internal/captcha/request.go`, `bridge.go`, and
   `internal/supervisor/lifecycle.go`. Explain the independent 180-second bridge
   timer, manual/selected handling, rejected auto requests, replaced/same-session
   windows, and suppression of expired or superseded replies via `ErrCaptchaNoReply`.
   Record the observable consequences of a shorter core wait or delayed handoff,
   including a lost or misattributed late result where supported by source evidence.
   For TURN, map `TurnTransport` through the profile/core arguments and bounded
   process stop in `internal/coreproc/process.go` and the supervisor lifecycle.
   Distinguish the UDP correction from TCP/TLS, the shell's process-stop deadline,
   and `auto_js` call cleanup; none of those proves that an admitted UDP packet was
   written. Name any required future rule/helper or consumer adaptation as migration
   work, or state that none was identified; do not implement it.
6. Inspect the existing local tests as contracts, without running them. Cover
   `scripts/vendorcore/files/rust-client/captcha_window_tests.rs` (virtual-time
   acceptance beyond the old manual/selected deadlines and expiry at 180 seconds),
   `internal/captcha/corewindow_test.go`,
   `internal/coreproc/captcha_test.go`'s `TestCoreSolverSwitchInCore`,
   `internal/config/profile_test.go`'s `TestCoreCaptchaModeWebView`, and the
   environment guard in `internal/coreproc/process_test.go`. Inspect the relevant
   cases in `internal/captcha/bridge_test.go` for selected/manual, unsupported auto,
   timeout, replacement and same-session handover, and
   `internal/supervisor/lifecycle_test.go` for replies, no-reply and stop cancellation.
   For TURN, inspect all four regressions in
   `scripts/vendorcore/files/rust-client/turn_outbound_tests.rs`, their helper
   dependencies in `core/rust-client/turn_integration_tests.rs`, and
   `scripts/vendorcore/turn_outbound_test.go`'s rule selection, substitutions and
   missing/duplicate-anchor guards. Identify held/queued FIFO, fast/async ownership,
   encoding failure and forced-deadline/pool recovery coverage, the preserved
   original ordering test, and any remaining gaps. Separate source-shape guards,
   local behavioral regressions, mocks, and prior accepted run results; none is a
   newly executed exact-ref or live-site check.
7. Record a compact English matrix in ignored
   `doc/local/core-253-runtime-review.md`, leaving the existing P1/P2/P3 and TUN
   evidence files intact. Include the per-rule inventory, added-target status, a
   separate P5/P7/TURN behavior and classification matrix, local consumers, inspected
   tests and gaps, exact-ref source URLs with line references, local `file:line`
   references, confidence, and bounded migration work/dependencies for child 05.
   Classify each patch as still needed, equivalent upstream, conflicting, or
   requiring investigation on behavioral evidence; report mechanical anchor/carry
   status separately. State whether equivalence was already present at baseline or
   introduced by the candidate. Have an independent non-coding reviewer verify all
   counts, source behavior, consumer effects, test scopes, and classifications
   against both exact refs and the local artifacts, not merely endorse the matrix.
   Record corrections and unresolved evidence limits in that matrix. The coordinator
   then writes a concise sanitized public Outcome in this task plan, with a pointer
   to the ignored evidence for subsequent assessment work; no source snippets,
   private values, dates, or environment details belong in it.

## Done when

- The ignored runtime matrix inventories all four P5, one P7, and eleven TURN
  drain/test-helper entries: 16 entries requiring 18 matches in total. Each has its
  target, local identity, behavioral anchor, required count, observed count/status
  at both exact refs, and source evidence. Added captcha/TURN test-file collisions
  are checked separately; overlap with P3 is explicit for child 05's reconciliation.
- P5 compares manual and selected/WebView waits at both refs with the fixed `wv`
  launch and independent 180-second shell timer. P7 compares switch handling and
  whether/when core solver work precedes shell handoff, distinguishing the ordinary
  `wv` path from the defensive switch and any supported late-result consequences.
- The TURN finding covers admitted, queued and writer-held UDP packets, completion
  or discard, ownership/lease recovery, and shutdown before the zero-lifetime
  Refresh. The 250 ms best-effort flush, later writer timeout, and shell process-stop
  boundary are distinguished. Classification states whether the local correction
  remains needed without asserting a remote-delivery guarantee.
- P5, P7, and TURN are each classified as still needed, equivalent upstream,
  conflicting, or requiring investigation, with baseline-equivalent behavior kept
  separate from the candidate delta and anchor applicability. Concrete local
  consumers and any hypothetical migration work are named.
- The specified existing tests, added Rust modules and TURN helper/rule tests are
  inspected, not run. The matrix states what they protect and what they cannot
  establish about either unpatched exact ref, runtime behavior, or a live site.
- An independent non-coding reviewer verifies the evidence and classifications.
  Exact refs, local `file:line` references, confidence, corrections and unresolved
  gaps are recorded; existing P1/P2/P3 evidence is preserved. The coordinator's
  public Outcome contains only sanitized findings and evidence limits.
- No code, tests, scripts, configuration, pin, or generated core is changed; no
  build, test run, vendor generation/check, captcha window, live check, external
  write, or legal conclusion is produced. The user's captcha policy and migration
  decision are not reopened.

## Risks and limits

- A matching anchor establishes only a mechanical insertion/replacement location.
  Conversely, a missing anchor can require adaptation without establishing whether
  the protected behavior is absent. Keep patch behavior and carry status separate.
- The shell does not receive a core timeout notification or a request identifier in
  `CaptchaRequest`; its timer and ownership rules depend on the documented result
  lifecycle. Report any evidence gap rather than choosing a new timeout, solver
  policy, event format, or protocol. Historical captcha-session failures do not
  establish current live-site behavior at the candidate ref.
- Queue admission is not send completion, and send completion is not remote receipt.
  Source review and local mocked tests cannot prove race freedom under all schedules
  or delivery through a deployed TURN service. Keep the accepted bounded contract.
- No user input or live access is needed for this source-only task. Any later test
  authorization must retain `doc/local/resume.md`'s memory/no-swap limits and timeout,
  plus Xvfb with `-noreset` for window tests; those rules do not authorize tests here.
  Licensing, platform changes and the final migration choice remain outside this task.

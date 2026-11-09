---
kind: task
status: in-progress
detailed: true
base: c0ded0414f9dc891667122f0ffc2e68b77a89ebb
---

# Assess process and loopback safeguards

## Task

Map local P1 and P2 behavior to the exact csqttr 2.5.3 source and its Go consumers,
using task 02's accepted findings rather than repeating its process-contract review.
Classify each patch as still needed, equivalent upstream, conflicting, or requiring
investigation, explicitly distinguishing behavior already present in the 2.5.0
baseline from changes introduced by 2.5.3. The local pin remains
`d02da9bd9d8926969b766e197c434d830a83dbd8`; compare it with immutable source commit
`84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806` only.

Use architecture §§04, 05, 07, and 11, `doc/upstream-proposals.md` items 1–2, and
the [accepted process-contract assessment](../../02-protocol-compatibility/plan.md).
This is read-only source analysis. Only a coordinator or separate non-coding
reviewer may read incompatible external implementation source; pass behavioral
findings in original wording and do not give implementation source, snippets, or
structure to an executor.

## Steps

1. Establish the comparison boundary from `core/CORE_VERSION`, the architecture patch
   inventory, and task 02's Outcome. Record its accepted facts for P1, P2, and the
   shell process contract as prior evidence; do not re-litigate wire compatibility,
   migration approval, or the direct-substitution conclusion.
2. For P1, trace the exact-ref secret and hash-check behavior against the local rule
   and added module in `scripts/vendorcore/rules.go` and
   `scripts/vendorcore/files/rust-client/secrets_stdin.rs`. Map consequences to
   `internal/coreproc/config.go`, `process.go`, and `hashcheck.go`, including the
   distinct normal-start and `--validate-vk-hashes` input paths. Reuse task 02's
   finding that direct substitution does not preserve the shell's secret contract.
   Record every P1 rule's target path, behaviorally described anchor, required match
   count, and exact-ref anchor status without copying source text.
3. For P2, use task 02's accepted findings about peer filtering and `LISTEN`; map the
   local rule and added module to `internal/coreproc/config.go` and `message.go`,
   `internal/supervisor/lifecycle.go`, and `internal/datapath/link.go`. State which
   Link/UDP datapaths depend on each behavior and keep the Linux TUN-FD path distinct.
   Record every P2 rule's target path, behaviorally described anchor, required match
   count, and exact-ref anchor status.
4. Record a sanitized evidence matrix in ignored
   `doc/local/core-253-patch-review.md`: patch, baseline status, exact-ref status,
   classification, affected local consumer files, operational/platform impact,
   source references, and confidence. Inspect relevant existing contract tests only
   to identify what they protect; do not run them. State evidence gaps rather than
   inferring behavior.

## Done when

- The matrix classifies P1 and P2 separately and distinguishes current 2.5.0 behavior
  from the exact 2.5.3 delta.
- Every `rules.go` entry assigned to P1 or P2 is inventoried with its target, anchor
  identity, required match count, and exact-ref anchor status.
- P1 covers both normal connection startup and hash validation, including the
  password/hash source and the corresponding `coreproc` consumers. P2 covers the
  peer restriction, listener-port event, Link consumer, and applicable datapaths.
- The Linux TUN-FD path is kept outside the P1/P2 conclusions.
- Each classification cites exact-ref evidence and local files, separates source
  facts from inference, and names any unresolved point without revisiting task 02's
  accepted findings.
- No code, test, configuration, pin, generated core, build, vendor command, live
  check, external write, or legal conclusion is produced.

## Risks and limits

- Task 02 already confirmed P1/P2 command-line, secret-input, `LISTEN`, and peer
  findings. A repeated source review is not a substitute for mapping the local
  consumers and patch status required here.
- Do not conflate loopback sender filtering with Linux TUN-FD descriptor ownership.
- The existing root-LICENSE treatment remains unchanged; this task does not determine
  legal compatibility or approve migration.

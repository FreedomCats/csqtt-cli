---
kind: task
status: todo
---

# Assess captcha and TURN runtime patches

## Task

Classify local P5's manual/selected captcha timeout adjustment, P7's
`ONLY_PRINT_URL` solver switch, and the local TURN UDP outbound-drain correction
against exact csqttr source commit
`84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806` and pinned 2.5.0 commit
`d02da9bd9d8926969b766e197c434d830a83dbd8`. Use architecture §§04, 07, and 08,
`doc/upstream-proposals.md` items 4–5 and 28, the referenced core-CI backlog outcome,
and the established shell mode and captcha handling. This is a single runtime-patch
assessment, not a choice to alter captcha policy or approve migration.

Only a coordinator or separate non-coding reviewer may inspect incompatible external
implementation source. Return behavior in original wording without source snippets or
implementation structure to an executor. Do not run captcha windows, builds, tests,
or vendor tools.

## Done when

- Each P5 and P7 vendor rule has a target, anchor identity, required match count, and
  exact-ref anchor status. The review names the relevant `captcha.rs`, `rules.go`,
  added captcha test, and `internal/captcha` contract files without reproducing
  external source text.
- Each TURN outbound-drain rule has a target, anchor identity, required match count,
  and exact-ref anchor status; the added regression test and proposal 28 are mapped
  to the same behavior. The review names `core/rust-client/turn.rs`,
  `scripts/vendorcore/rules.go`, and
  `scripts/vendorcore/files/rust-client/turn_outbound_tests.rs` as applicable local
  artifacts.
- P5's timeout behavior is compared at both exact refs for manual and selected/WebView
  requests; the shell's `-captcha-mode wv`, 180-second bridge handling, and affected
  `internal/captcha` consumer are mapped to observable wait/timeout effects.
- P7's solver-disable behavior is compared at both refs, including the shell's
  `ONLY_PRINT_URL` environment setting and the consequence if the candidate ignores
  it or changes when captcha work is handed to the shell.
- The TURN fix is compared at the boundary between accepted UDP writes, queued or
  writer-owned packets, and shutdown; local socket-write completion is distinguished
  from remote receipt or acknowledgement. Its migration classification names whether
  the local correction remains necessary.
- Each patch is classified as still needed, equivalent upstream, conflicting, or
  requiring investigation, with baseline-equivalent behavior separated from changes
  introduced by 2.5.3. Existing local tests and the added TURN regression test are
  identified only as evidence of protected contracts and are not run.
- The evidence identifies source refs and local consumer paths, records confidence and
  unresolved behavior, and does not revisit the user's captcha decision or assert
  live-site behavior.
- No code, test, configuration, pin, generated core, build, vendor command, live
  check, external write, or legal conclusion is produced.

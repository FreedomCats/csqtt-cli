---
kind: task
status: todo
depends: [01-process-and-loopback-guards, 02-linux-tun-safeguards, 03-socket-binding-p3, 04-captcha-patches]
---

# Reconcile vendor rules and crate inputs

## Task

Reconcile the anchor inventories from children 01–04 and assess how the
`scripts/vendorcore` pipeline carries or rejects non-rule inputs at the exact csqttr
2.5.3 source ref. The current `core/CORE_VERSION` records 59 rules, nine added files, and
`primp-hyper-util` 0.1.22; verify those current-baseline facts from repository files
and compare the relevant inputs with immutable source commit
`84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`. Do not run vendor generation or checks,
or change `core/`.

Use architecture §§02, 06, and 07 and the fixed-ref source evidence from the preceding
children. Only a coordinator or separate non-coding reviewer may inspect incompatible
external implementation source. The matrix may name local rule identifiers, file
paths, counts, and source references, but must not pass source snippets or a retelling
of external implementation structure to an executor. Do not infer legal permission
from Cargo metadata; the existing root-LICENSE treatment remains unchanged.

## Done when

- A complete matrix enumerates all 59 current entries in `scripts/vendorcore/rules.go`,
  recording each rule's domain or local change, target path, behaviorally described
  anchor identity, required match count, and exact-ref status. Use the anchor
  inventories from children 01–04 for the patch domains, classify every remaining
  generator rule rather than silently omitting it, and identify any unassigned entry.
  Preserve P6's explicit status as already upstream in the pinned baseline with no
  current carried rule.
- The generator audit accounts for added files under `scripts/vendorcore/files`,
  excluded upstream files, and generator behavior affecting `CORE_VERSION` counts and
  the generated `core/` output set.
- The pinned `primp-hyper-util` archive, Cargo lock/version/checksum contract, local
  connector change, crate-specific skip/input rules, and candidate-ref dependency
  state are mapped as vendor facts. Behavioral P3 socket conclusions remain in child
  03 and are referenced, not substituted with a crate-presence check.
- Resolve the assignment label "Gooby" only against the authoritative local inventory
  (`doc/architecture.md` §07, `doc/upstream-proposals.md`, and `scripts/vendorcore`).
  If it does not identify a repository artifact or patch, record it as unresolved for
  `lead`; do not silently omit it, guess its meaning, or expand the roadmap scope.
- Reconciled findings identify which anchors, crate inputs, added files, or socket
  guard expectations block safe carry and in what order. State evidence limits; do
  not claim vendor generation or build success.
- No generator or `-check` is run; no code, test, configuration, pin, generated core,
  build, live check, external write, or legal conclusion is produced.

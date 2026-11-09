---
kind: task
status: todo
---

# Assess socket binding (P3)

## Task

Assess whether the exact csqttr 2.5.3 source preserves, changes, or makes unnecessary
local P3's socket-by-socket interface binding. Compare immutable source commit
`84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806` with the pinned 2.5.0 commit
`d02da9bd9d8926969b766e197c434d830a83dbd8`. Use architecture §§04, 06, 07, and 09b,
`doc/upstream-proposals.md` item 5, and task 02's accepted fact that the candidate
does not expose the shell's `--bind-if` or `--mark` arguments. Missing CLI flags alone
do not establish the effect on every socket.

Only a coordinator or separate non-coding reviewer may inspect incompatible external
implementation source. Report behavioral facts and references, not source, snippets,
or a retelling of source structure to an executor. Do not change the shell contract,
platform support, local pin, or migration decision.

## Done when

- Every P3 rule entry in `rules.go` has an anchor identity, target path, required
  match count, and exact-ref status.
- All eight logical P3 socket sites registered in `scripts/vendorcore/sockets.go` are
  assessed individually against the exact source: DNS for VK, TURN UDP, TURN TCP/TLS,
  the two `primp` HTTP client paths (including their connector binding), and the three
  conditional direct-routing sockets (TCP, UDP, and direct-domain DNS). State whether
  each exists at the candidate ref, how it is created/bound, and what is known or
  unknown about its route through a full-system TUN.
- The five always-used and three routing-conditional paths are distinguished; profile
  routing (`routing.template`, `routing.server`) and the supervisor's
  `ErrDirectUnbound` guard are traced rather than treating direct routing as always
  active.
- Shell impact is mapped to `internal/coreproc/config.go`, the P3 preparation and
  rebind path in `internal/supervisor`, and the relevant `internal/netguard`,
  `internal/linuxsys`, and `internal/winsys` consumers. Windows interface binding and
  Linux interface binding plus `SO_MARK` are separately assessed.
- The `primp-hyper-util` connector is included as part of P3 behavior, but its archive,
  Cargo, and vendor-generation consequences are reconciled in child 05. Relevant
  contract tests are inspected, not run; no socket or platform behavior is inferred
  from argument absence alone.
- Findings cite the exact source ref and local consumer paths, separate facts from
  inference, identify platform-specific work and confidence, and do not claim live
  validation or compatibility with a deployed server.
- No code, test, configuration, pin, generated core, build, vendor command, live
  check, external write, or legal conclusion is produced.

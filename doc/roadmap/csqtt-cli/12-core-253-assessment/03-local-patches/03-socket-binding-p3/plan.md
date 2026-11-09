---
kind: task
status: in-progress
detailed: true
base: 8e2e0cd84f0bc976e35609a9228260d00054ae1c
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

## Steps

1. Fix the evidence boundary from `core/CORE_VERSION`, architecture §§04, 06, 07,
   and 09b, `doc/upstream-proposals.md` item 5, task 02's accepted Outcome, and
   `doc/local/core-253-protocol-review.md`. Compare only baseline commit
   `d02da9bd9d8926969b766e197c434d830a83dbd8` and candidate commit
   `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`. Reuse only the prior fact that the
   candidate lacks the shell's `--bind-if` and `--mark` arguments and that socket
   consequences were left open. Do not repeat wire, server, or general process-contract
   review, follow moving refs, or infer socket behavior from missing flags.
2. Inventory every `rules.go` entry assigned to P3: give each its target path, a
   behavioral description of its anchor, required match count, and observed
   count/status at both exact refs. Keep these anchor results separate from whether a
   socket is actually bound or bypasses TUN; an anchor match alone is not behavioral
   evidence. Do not expand this into child 05's full vendor/crate audit.
3. Have the coordinator, or a separate non-coding participant, inspect the exact
   source implementation at both refs and assess all eight logical socket sites
   individually, using `scripts/vendorcore/sockets.go` and architecture §06 as the
   inventory: VK DNS; TURN UDP; TURN TCP/TLS; the `auth.rs` and `vk_js_calls.rs`
   `primp` HTTP paths, including the shared connector's per-connection socket
   binding; direct TCP; direct UDP; and direct-domain DNS. For each site record
   whether it exists at each ref, when it is used, whether it is explicitly bound
   to the selected physical interface, what route through a full-system TUN is
   established by source evidence, and what remains unknown. Distinguish the two
   `primp` clients from their shared connector; do not count the connector as a ninth
   logical site. State source file/line references and confidence without copying
   source or recounting implementation structure.
4. Separate the five always-used sites from the three direct-routing sites. Trace
   activation through `routing.template` and `routing.server` in
   `internal/coreproc/config.go` and the profile/routing contract in architecture
   §§04 and 10. Trace the full-TUN guard through `internal/supervisor/lifecycle.go`:
   `MayRouteDirect`, `datapath.Persistent`, `Prepare`'s interface index, and
   `ErrDirectUnbound`. Distinguish `socks` from full-system `tun`, and the Linux
   TUN-FD path from Windows' Wintun/Link path; do not describe direct routing as
   always active.
5. Map the shell-side P3 path and platform differences using
   `internal/cli/fulltunnel.go`, `netguard.go`, `internal/coreproc/config.go`,
   `internal/supervisor/lifecycle.go`, `internal/netguard/netguard.go`,
   `netguard_linux.go` and `netguard_windows.go`, plus the relevant Linux and
   Windows DNS-guard consumers (`internal/cli/dnsguard_linux.go`,
   `internal/linuxsys/dnsguard_linux.go`, `internal/cli/dnsguard_windows.go`, and
   `internal/winsys/dnsguard_windows.go`). Record Linux `SO_BINDTOIFINDEX` and
   `SO_MARK` roles and conditions separately from Windows `IP_UNICAST_IF` /
   `IPV6_UNICAST_IF`, which has no `SO_MARK`; identify the shell's source of the
   interface index and mark, and what the guards allow. Do not infer candidate
   kernel behavior from local shell behavior.
6. Inspect, but do not run, only the relevant local contract tests: P3 anchor/socket
   coverage in `scripts/vendorcore/files/rust-client/bind_if_tests.rs` and
   `direct_relay_bind_if_tests.rs`; argument and route guard coverage in
   `internal/coreproc/config_test.go` and
   `internal/supervisor/lifecycle_test.go`; platform binding and mark coverage in
   `internal/netguard/netguard_linux_test.go` and
   `netguard_windows_test.go`; and the Linux direct-TUN integration test
   `internal/datapath/direct_netns_linux_test.go`. Note what these tests do and do
   not establish about the candidate; no test result is claimed.
7. Record a P3 section in the existing ignored
   `doc/local/core-253-patch-review.md`, preserving its P1/P2 findings. Include the
   per-site baseline/candidate behavior matrix, direct-routing conditions, platform
   shell mapping, P3 rule-anchor inventory, classification, references, confidence,
   and evidence gaps. Have the reviewer independently verify every socket assertion,
   P3 anchor count/status, routing condition, and platform claim against the two
   exact refs and local consumers, rather than relying on the coordinator's matrix.
   The reviewer must not run checks or provide source snippets/structure to an
   executor.

## Done when

- The ignored P3 evidence section records all eight logical socket sites at baseline
  and candidate refs, including per-site use conditions, physical-interface binding,
  full-system TUN route evidence or explicit uncertainty, exact references, and
  confidence. The two `primp` clients and their shared connector are all covered
  without treating the connector as a ninth site.
- Every P3 rule entry in `scripts/vendorcore/rules.go` has a target, behavioral anchor
  identity, required match count, and observed count/status at both exact refs. This
  inventory is explicitly independent of the socket behavior findings.
- The five always-used and three conditional sites are distinguished. The matrix
  traces `routing.template`, `routing.server`, `MayRouteDirect`, full-TUN detection,
  `Prepare`, and `ErrDirectUnbound`, and keeps `socks`, Windows Wintun/Link, and Linux
  TUN-FD paths distinct.
- Local shell impact is mapped to concrete consumers on both platforms, including
  interface selection, P3 startup/rebind, Windows unicast-interface options, Linux
  interface binding, and the conditional `SO_MARK`/DNS-guard path. Candidate behavior
  is not inferred from the local contract or missing CLI arguments.
- Relevant existing tests are inspected but not run. An independent reviewer verifies
  all socket, anchor, route-condition, and platform assertions against the exact refs
  and local consumers; gaps and unknowns remain explicit. Child 05, not this task,
  reconciles archive, Cargo, and vendor-generation consequences.
- The work remains read-only: no code, tests, configuration, pin, generated core,
  builds, test runs, vendor commands, live checks, or external writes; no legal
  conclusion or claim of deployed-server compatibility is made.

## Risks and limits

- The missing CLI flags are accepted prior evidence, not proof of what any one socket
  does. Interface binding, socket route selection, and mark-based filtering must be
  assessed separately and per site.
- Source inspection can establish explicit socket options and conditional code paths,
  but not necessarily the effective route on a particular host. Mark route behavior
  as unknown where source plus the documented full-TUN setup is insufficient; no live
  check is permitted.
- Only a coordinator or non-coding reviewer may inspect incompatible external source.
  No source snippets or retold implementation structure may be passed to an executor.
- This is a source-level migration assessment only. It does not alter the pinned
  2.5.0 core, the current release, the migration decision, or existing licence
  treatment, and makes no legal determination.

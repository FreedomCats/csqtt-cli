---
kind: task
status: in-progress
detailed: true
base: bc743c8bf133c06cee03421bd04513291a76876f
---

# Assess Linux TUN safeguards

## Task

Classify P4's same-user TUN descriptor check and P6's already-upstream TUN write
behavior against the pinned 2.5.0 commit `d02da9bd9d8926969b766e197c434d830a83dbd8`
and exact csqttr 2.5.3 source commit `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`.
Use architecture §§04, 05, and 07, `doc/upstream-proposals.md` item 3, and the Linux
`-tun-uds` shell contract. Keep P4 distinct from P2 loopback peer filtering and P6
distinct from any carried local rule. This is read-only source analysis and does not
change Linux support or approve migration.

## Steps

1. Fix the evidence boundary from `core/CORE_VERSION`, architecture §§04, 05, and
   07, `doc/upstream-proposals.md` item 3 and its P6 note, and task 02's accepted
   Outcome. Compare only pinned 2.5.0 source commit
   `d02da9bd9d8926969b766e197c434d830a83dbd8` with assessed 2.5.3 source commit
   `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`. Limit this task to the Linux shell's
   TUN-FD (`-tun-uds`) path; do not generalize to Windows or redo the accepted P2
   loopback or P3 socket-binding reviews. Do not change the pin or use moving refs.
2. Inventory every P4 entry in `scripts/vendorcore/rules.go`. For each of its four
   rules targeting `rust-client/tun.rs`, record a behavioral anchor description, the
   required match count (one), and the observed count/status independently at both
   exact source refs. Also check whether the added-file target
   `rust-client/tun_peer.rs` already exists at either ref, recording this separately
   from the four rule anchors. Anchor presence or count is not evidence that the
   corresponding safeguard is implemented.
3. Have the coordinator or a separate non-coding reviewer compare P4 behavior at
   both exact refs with the local rule's intended guard: obtain the connecting
   process credentials, admit only the same effective uid as the core, fail closed
   when credentials cannot be obtained, reject before consuming the descriptor,
   and continue accepting a subsequent valid connection without waiting for another
   attempt. Report behavioral facts in original wording, not foreign-source
   snippets or a retelling of its implementation structure. Classify P4 as still
   needed, equivalent upstream, conflicting, or requiring investigation based on
   behavior, separately from the per-anchor statuses.
4. Trace the shell-side Linux contract through `internal/coreproc/config.go`,
   `internal/datapath/tun_linux.go`, `internal/datapath/tunfd_linux.go`, and
   `internal/supervisor/lifecycle.go`: the per-attempt `-tun-uds` name, `PROCESS`
   pid, descriptor duplication and handoff, shell verification of the listening
   process pid and effective uid before sending, and resend/failure handling after
   `TUN_LOST`. Distinguish that shell-side ownership check from P4's core-side
   sender-uid guard. Inspect, but do not run, the relevant local checks in
   `scripts/vendorcore/files/rust-client/tun_peer.rs`,
   `internal/datapath/tunfd_linux_test.go`, `internal/supervisor/tunfd_test.go`,
   `internal/supervisor/tunlost_test.go`, and
   `internal/supervisor/real_core_linux_test.go`; note what each covers and any gap.
5. Have the coordinator or separate non-coding reviewer compare P6's Linux TUN-write
   behavior at both exact refs, including rejected/non-IP packets, zero-byte writes,
   drop accounting, and whether writing continues after a dropped packet. Separate
   behavior already present at the 2.5.0 baseline from any 2.5.3 delta. Confirm from
   `scripts/vendorcore/rules.go` that no P6 rule is currently carried; do not infer
   upstream behavior from the locally patched `core/` copy or treat the absence of
   a rule as proof of equivalence. Inspect relevant tests at the refs and locally
   only as evidence; do not run them.
6. Hand `lead` a concise evidence matrix with the exact source ref, P4 anchor target,
   behavioral description, expected and observed match counts, anchor status,
   separate behavior-equivalence finding, P4/P6 classification, local consumers,
   inspected tests, confidence, and evidence gaps. Keep source facts distinct from
   inference; record that this is source-only assessment, with no build, vendor
   command, live check, migration, legal conclusion, or platform-generalization.

## Done when

- P4 is classified as still needed, equivalent upstream, conflicting, or requiring
  investigation, with uid-check and replacement/handoff behavior mapped to
  `internal/datapath/tunfd_linux.go` and the supervisor lifecycle. Windows and
  loopback behavior are not generalized from this Linux-only finding.
- P6 is compared at both refs and explicitly recorded as having no current carried
  rule because the behavior was already present in the pinned 2.5.0 baseline; any
  2.5.3 delta is separated from that fact.
- Every P4 rule entry has a target, behaviorally described anchor, required match
  count, and exact-ref anchor status. Relevant tests are inspected, not run.
- Findings cite exact source refs and local consumers, separate facts from inference,
  name confidence and gaps, and avoid legal conclusions or live validation.
- No code, test, configuration, pin, generated core, build, vendor command, live
  check, or external write is performed.

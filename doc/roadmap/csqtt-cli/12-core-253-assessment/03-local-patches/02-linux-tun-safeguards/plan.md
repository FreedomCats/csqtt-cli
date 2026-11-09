---
kind: task
status: todo
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

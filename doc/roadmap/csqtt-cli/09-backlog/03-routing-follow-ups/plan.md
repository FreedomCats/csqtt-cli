---
kind: stage
status: in-progress
base: 2c3dfec
---

# Tunnel bypass routing follow-ups

## Execution order

The user chose this group and its work order before work started: proposal 16, then
proposal 14, then proposal 15. The numbers are the numbers of the candidates in
`doc/upstream-proposals.md`; the order is a work order, not a priority claim.

## Goal

Assess and deliver the tunnel-bypass routing follow-ups collected from
`doc/upstream-proposals.md`. Preserve all three candidates; their execution order is
recorded above.

## Candidates

- Separate direct-traffic counters (16) — direct traffic is counted together with
  tunnel traffic; split it into its own fields.
- Direct traffic in Linux `tun` without workers (14) — the core drops it.
- Fall back into the tunnel when a direct connection fails (15).

## Done when

- Every candidate is implemented and verified, or has an explicit outcome explaining
  why it is deferred or dropped.
- Implemented behaviour passes the relevant Go and Rust builds and tests, including
  regression tests for each changed routing behaviour.

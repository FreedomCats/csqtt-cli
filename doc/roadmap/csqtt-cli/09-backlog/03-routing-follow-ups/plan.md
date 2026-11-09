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

## Children

In the chosen work order:

1. [Direct-traffic counters](01-direct-traffic-counters/plan.md) — stage for candidate
   16: [Core counters](01-direct-traffic-counters/01-core-counters/plan.md) fixes the
   `STATS` contract and the path attribution in the core;
   [Shell consumption](01-direct-traffic-counters/02-shell-consumption/plan.md)
   parses and displays the fields and simplifies the supervisor.
2. [Direct traffic in `-tun-uds` without workers](02-tun-direct-without-workers/plan.md)
   — remove the early drop in `read_tun` so the direct path works while no worker is
   registered. Candidate 14.
3. [Failed direct TCP fallback](03-failed-direct-fallback/plan.md) — candidate 15,
   split into resets and address memory because the two results are accepted
   independently.

## Done when

- Every candidate is implemented and verified, or has an explicit outcome explaining
  why it is deferred or dropped.
- Implemented behaviour passes the relevant Go and Rust builds and tests, including
  regression tests for each changed routing behaviour.

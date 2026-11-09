---
kind: stage
status: done
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

## Outcome

All three candidates are implemented and verified on the patched 2.5.0 core. The
`STATS` event is split by path (П8): tunnel-only `bytes_up`/`bytes_down`, new
`direct_up`/`direct_down`; the shell shows direct traffic separately and the supervisor
clears `NETWORK_SUSPECT` by tunnel downlink again. `read_tun` no longer drops packets
while no worker is registered (П9), so the direct path works in `-tun-uds` during
reconnects, restarts and short network losses. A failed or timed-out direct TCP connect
is reset with the recorded SYN-ACK sequence plus one (П10), and the destination IP is
remembered for five minutes, so later TCP SYNs to it use the tunnel.

The group acceptance over `2c3dfec..2bfa5e0` found one documentation defect: the README
still described mixed counters and said a failed direct connection is not returned to
the tunnel; fixed in `de04e7f`. The engineering checks passed: the paired `STATS`
contract, `vendorcore -check` with idempotent generation from the pinned `d02da9b`,
`buildcore -test` (417 passed, 0 failed, 13 ignored), Rust regressions 7/7 and 6/6,
`go build`, `go vet`, and the full Go suite under Xvfb. The pin, protocol, dependencies
and platforms are unchanged.

Known limits and real problems, recorded rather than fixed: the address memory is keyed
by IP, so other sites on the same address use the tunnel for five minutes; connections
opened during the five-second attempt still go direct; on a multithreaded runtime the
RST can be queued before the SYN-ACK, returning the old “established then closed”
symptom for that one connection; a successful direct connect has no deterministic
recording test, its absence is pinned structurally by the Go rule test; a pre-existing
upstream `sync_domain_routing` behavior clears `active_tcp`/`udp_sessions` on
`UPDATE_DOMAINS` when only IP routing is enabled, breaking established flows — it needs
a separate backlog task; the П8 downlink counts queue-accepted bytes and lost fragments
count as tunnel uplink, with mixed shell/core versions unsupported; live
browser/provider checks and the Windows core build remain unavailable and unclaimed.

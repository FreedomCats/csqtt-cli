---
kind: stage
status: todo
---

# Tunnel bypass routing follow-ups

## Goal

Assess and deliver the tunnel-bypass routing follow-ups collected from
`doc/upstream-proposals.md`. Preserve all three candidates; the user chooses their
execution order before work starts.

## Candidates

- Direct traffic in Linux `tun` without workers — the core drops it.
- Fall back into the tunnel when a direct connection fails.
- A separate counter of direct traffic.

## Done when

- Every candidate is implemented and verified, or has an explicit outcome explaining
  why it is deferred or dropped.
- Implemented behaviour passes the relevant Go and Rust builds and tests, including
  regression tests for each changed routing behaviour.

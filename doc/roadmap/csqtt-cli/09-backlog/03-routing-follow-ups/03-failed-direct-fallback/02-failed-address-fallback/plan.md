---
kind: task
status: todo
depends: [01-failed-connection-reset]
---

# Remember failed direct addresses

## Task

After a direct TCP connect fails, remember the destination address for a bounded period
and route new TCP connections to it through the tunnel instead of attempting the direct
path again. This is the second task of [`Failed direct TCP fallback`](../plan.md) and
the fallback half of proposal 15 in `doc/upstream-proposals.md`.

Use a memory keyed by destination IP with an expiry of a few minutes (one constant, for
example five minutes), checked in the new-connection branch of
`DirectRelay::try_handle` in `core/rust-client/direct_relay.rs` after the DNS handling
and before the direct verdict. Existing direct flows stay direct; only new SYNs to a
remembered address are sent to the tunnel. The memory is cleared when the routing
configuration is disabled, and expired entries are pruned lazily or by the existing
cleanup task. Keep the direct traffic counters correct: a SYN routed to the tunnel is
tunnel traffic, not direct traffic.

## Done when

- A direct connect failure records the destination address for the configured period;
  a new TCP SYN to that address is not accepted by the direct path and reaches
  `dispatch` as tunnel traffic. After expiry a new SYN is allowed direct again.
- Established direct flows, UDP sessions, DNS and domain-routing decisions are
  unchanged. Disabling the routing configuration clears the memory.
- A deterministic Rust test covers the record, the tunnel decision, expiry (with an
  injected or shortened time, not a real wait), clearing on disable, and that a
  successful direct connection is not recorded. It uses documentation addresses and
  no live service.
- The change is authored as counted vendor rules and added files, regenerated, and
  `vendorcore -check` plus idempotence pass against the pinned upstream commit. The
  core build/tests and the relevant Go build/vet/tests pass under the required scope.
- The failed-direct row in the §07 patch table of `doc/architecture.md` is extended
  with the address memory, and item 15 in `doc/upstream-proposals.md` records the
  fallback as implemented locally; the Part 2 introduction is corrected to say that
  items 14–16 are now patched locally.
- No live provider-filtering or browser check is claimed.

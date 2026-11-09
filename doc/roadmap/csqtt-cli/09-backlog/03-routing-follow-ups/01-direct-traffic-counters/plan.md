---
kind: stage
status: in-progress
base: 2353a19
---

# Direct-traffic counters in `STATS`

## Goal

Split the `STATS` traffic counters by path: `bytes_up` and `bytes_down` must count the
tunnel only, while new `direct_up` and `direct_down` count traffic handled by the
tunnel-bypass path in `core/rust-client/direct_relay.rs`. The Go shell consumes the new
fields: the status line shows direct traffic separately, and the supervisor can again
use tunnel downlink growth to clear `NETWORK_SUSPECT` when routing bypass is active.

This is proposal 16 in [`doc/upstream-proposals.md`](../../../../../upstream-proposals.md)
and the first child of [`Tunnel bypass routing follow-ups`](../plan.md). It does not
change the server protocol or the event's existing field names. The shell currently
works around the mixed counters by trusting only `READY` after a suspicion while
routing bypass is on; that workaround becomes obsolete and is removed here.

The work is split at the `STATS` event boundary: the core owns the event contract and
the counter attribution, the shell owns parsing, display and the supervisor. Neither
child changes the other's layer, so each diff can be reviewed with its own checks.

## Children

1. [Core counters](01-core-counters/plan.md) — author the `STATS` fields and the up/down
   path attribution in the core, with deterministic Rust regressions, the peer-gate
   fixture update, vendor rules and the §04/§07 documents. It does not edit the Go
   shell or the supervisor.
2. [Shell consumption](02-shell-consumption/plan.md) — parse `direct_up`/`direct_down`,
   show direct traffic separately, simplify the supervisor now that `bytes_down` is
   tunnel-only, and update the §09 document. It does not edit the core or vendor rules;
   it depends on the core child.

## Done when

- `STATS` carries `direct_up` and `direct_down`; `bytes_up`/`bytes_down` count only
  the tunnel path, and no packet is credited to both paths or twice within one
  path. The exact JSON keys and the counter attribution are covered by a
  deterministic Rust test.
- The shell parses the new fields, the status line shows direct traffic separately,
  and the supervisor clears a network suspicion by tunnel `bytes_down` growth even
  when routing bypass is active. Go tests cover the parser, the display, and the
  supervisor transitions.
- The peer-gate fixture still proves that a foreign datagram neither enters the
  core nor redirects replies, without relying on the old mixed `bytes_up`
  semantics.
- The core changes exist only as authored vendor rules and added files;
  regeneration is idempotent and `vendorcore -check` passes against the pinned
  upstream commit. The upstream pin, protocol, dependencies and supported
  platforms are unchanged.
- Focused vendor/build-script tests, the new Rust regression, the full core build
  and suite, real-core `coreproc`/`supervisor` tests where applicable, `go build
  ./...`, `go vet ./...`, and the full Go suite pass under the required scope and
  Xvfb. Hosted Linux/Windows checks and any push remain pending.
- The architecture and proposals documents describe the new counter split and the
  local patch; no personal or environment data is added to the repository.
- No live core connection or live routing run is needed for this stage, and none is
  claimed.

## Risks and limits

- Downlink counters move from the device write to the packet's origin (tunnel
  worker or direct stack). Bytes accepted into the inbound queue are counted even
  if the queue later evicts the packet or the device write fails. This is
  intentional for tunnel liveness and for separating paths, but it is not an exact
  delivered-to-device count. An exact count would need a per-packet path tag and is
  out of scope.
- Removing the supervisor's direct guard changes when `Degraded` ends: tunnel
  downlink growth now clears the suspicion with routing bypass active. The change
  is covered by tests, but it has no live verification in this stage.
- The event addition is backward-compatible in both directions: an older shell
  ignores the new keys, and a shell reading an older core sees zero direct
  counters. Do not rename the existing keys. The supervisor change, however,
  assumes the paired core: a new shell with an old core would still receive mixed
  `bytes_down` and could clear a suspicion from direct traffic. The shell and core
  are shipped together; do not claim mixed versions are supported, and keep the
  release pairing unchanged.
- The peer-gate fixture starts using a direct test packet; the security assertions
  must stay, only the observation mechanism changes.
- The core and shell children are sequential (`02-shell-consumption` depends on
  `01-core-counters`); the counter contract is fixed before the shell tests are
  rewritten.

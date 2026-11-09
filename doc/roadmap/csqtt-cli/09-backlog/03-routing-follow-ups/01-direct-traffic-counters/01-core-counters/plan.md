---
kind: task
status: todo
detailed: true
---

# Core counters for `STATS`

## Task

Author the core half of proposal 16: split `STATS` into tunnel-only
`bytes_up`/`bytes_down` and new `direct_up`/`direct_down`, with the path attribution in
`core/rust-client/dispatcher.rs`. This is the first task of
[`Direct-traffic counters`](../plan.md); the shell parser, display and supervisor
belong to the sibling [`Shell consumption`](../02-shell-consumption/plan.md) task.
Do not edit the Go shell or the supervisor here.

The server protocol and the event's existing field names do not change. The direct
path is `core/rust-client/direct_relay.rs`; the tunnel path is the worker send and the
downlink from workers/reassembly. The counters must be credited once, by the path that
accepted the packet.

## Steps

1. Add the counters in the core. In `core/rust-client/stats.rs` add
   `direct_bytes_up` and `direct_bytes_down` atomics beside the existing totals, load
   them in `Stats::run`, and pass them to the event. In `core/rust-client/events.rs`
   extend `Events::stats` with the two values and emit `direct_up` and `direct_down`
   in the same `STATS` JSON. Keep `bytes_up`/`bytes_down` as the tunnel-only names.
   The periodic `[СТАТИСТИКА]` log line may show both paths, but must not rename the
   event keys. Add a unit test for the exact `STATS` JSON keys.

2. Attribute uplink in the dispatcher. In `core/rust-client/dispatcher.rs` make
   `Dispatcher::dispatch` return a small outcome: `Direct` when
   `direct_relay.try_handle` accepted the packet, `Dropped` when no worker is
   registered, and `Tunnel` when the packet is handed to a worker. Keep the direct
   call before the worker check. In `read_tun` and `read_udp`, capture the packet
   length before dispatch and credit `direct_bytes_up` for `Direct`,
   `bytes_up` for `Tunnel`, and nothing for `Dropped`; remove the old
   `total_bytes_up` increments from both read loops. Factor that outcome-to-counter
   mapping into a small helper so the unit test can call it directly. The `-listen`
   peer gate still runs before dispatch and is unchanged.

3. Attribute downlink at the packet's origin instead of at the device write.
   Give `Dispatcher` an `Arc<Stats>` field (set it in `start_with_domain_routing`,
   the test `start`, `start_test_tun`, and dispatcher test literals). In
   `Dispatcher::return_packet` add the packet length after stripping the legacy
   downlink prefix to `bytes_down`; this is the tunnel downlink from workers and
   reassembly. Add a `push_direct_inbound` method that adds `direct_bytes_down` and
   then calls `push_inbound`; the direct-relay inbound pump in
   `start_with_domain_routing` must use it. Remove the `total_bytes_down` increments
   from `write_tun`, `write_udp` and `write_udp_packet` and drop their now-unused
   `stats` parameters (or rename them to `_stats` only where a signature must stay
   stable), updating the call sites in `core/rust-client/dispatcher.rs`. A packet is
   counted once, by the path that accepted it; a later queue eviction or device-write
   failure does not adjust the counters. Say so in the field comments.

4. Add a deterministic Rust regression as a new module under
   `scripts/vendorcore/files/rust-client/` (for example
   `direct_stats_tests.rs`), connected to `dispatcher.rs` by a counted rule. Cover:
   a direct TCP SYN returns `Direct` from `dispatch` and, after the same uplink
   accounting the read loops use, increases only `direct_bytes_up`; a tunnel-bound
   packet with no worker returns `Dropped` and increases neither tunnel nor direct
   uplink; the uplink accounting helper credits `Tunnel` only to `bytes_up`;
   `return_packet` increases `bytes_down`; `push_direct_inbound` increases
   `direct_bytes_down`; and no packet is counted twice. Use the existing
   `Dispatcher` test construction and a synthetic direct route; do not use a live
   socket or the network.

5. Update the peer-gate fixture in
   `scripts/vendorcore/files/rust-client/listen_peer.rs`. Because `bytes_up` no
   longer credits a datagram dropped for lack of a worker, observe admission with
   the direct path instead: configure the fixture's dispatcher with an IP rule
   matching a documentation address (`192.0.2.0/24`), send a valid direct TCP SYN
   there, and assert `direct_bytes_up` grows only for the pinned or admitted
   sender. Make the fixture's reply reader ignore the SYN-ACK IP packet emitted by
   the direct stack, so the reply-routing assertions still see only the synthetic
   `0x80` reply. Keep every foreign/pinned/reply assertion and the security
   property of the test; update the comments that describe
   `stats.total_bytes_up`.

6. Author the core changes as counted rules in `scripts/vendorcore/rules.go` and
   added files under `scripts/vendorcore/files/rust-client/`; do not edit generated
   `core/` by hand. Add or extend vendor-rule tests for the new anchors and keep the
   socket coverage guard green. Regenerate `core/` from a clean checkout of the
   pinned upstream commit and check idempotence and `-check`.

7. Record the change in the project documents (Russian, as those documents are):
   the `STATS` event row in §04 of `doc/architecture.md` and a new row in the §07
   local-patch table with the next free number (`П8` for this task). The `Degraded`
   paragraph in §09 belongs to the shell task. Add a short "Как сделано у нас" note
   to item 16 in `doc/upstream-proposals.md` without removing the proposal text.

8. Validate. Every Go test and test binary runs in the mandatory scope; the focused
   Rust checks use the same scope. From the repository root:

   ```sh
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go test -count=1 ./scripts/vendorcore ./scripts/buildcore
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go run ./scripts/vendorcore -upstream <clean checkout of the pinned commit>
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go run ./scripts/buildcore
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go run ./scripts/buildcore -test
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     cargo test --locked --manifest-path core/rust-client/Cargo.toml \
       --target x86_64-unknown-linux-gnu --target-dir build/cargo --bin client \
       direct_stats_tests
   ```

   Repeat the generation to confirm identical output and run
   `go run ./scripts/vendorcore -check -upstream <clean checkout of the pinned
   commit>`. Review the host workflows as references; do not change workflows to
   hide a failure. No live core connection is needed for this task, and no push or
   hosted run is authorized by it.

## Done when

- `STATS` carries `direct_up` and `direct_down`; `bytes_up`/`bytes_down` count only
  the tunnel path, and no packet is credited to both paths or twice within one
  path. The exact JSON keys and the counter attribution are covered by a
  deterministic Rust test.
- The peer-gate fixture still proves that a foreign datagram neither enters the
  core nor redirects replies, without relying on the old mixed `bytes_up`
  semantics.
- The core changes exist only as authored vendor rules and added files;
  regeneration is idempotent and `vendorcore -check` passes against the pinned
  upstream commit. The upstream pin, protocol, dependencies and supported
  platforms are unchanged.
- Focused vendor/build-script tests, the new Rust regression, the full core build
  and suite pass under the required scope. Hosted checks and any push remain
  pending.
- The §04 `STATS` row, the §07 `П8` row and the Russian note to item 16 describe
  the change; no personal or environment data is added to the repository.
- No Go or supervisor file is edited, and `core/` is changed only through vendorcore
  generation; the shell consumption is left to the dependent task.

## Risks and limits

- Downlink counters move from the device write to the packet's origin (tunnel
  worker or direct stack). Bytes accepted into the inbound queue are counted even
  if the queue later evicts the packet or the device write fails. This is
  intentional for tunnel liveness and for separating paths, but it is not an exact
  delivered-to-device count. An exact count would need a per-packet path tag and is
  out of scope.
- The peer-gate fixture starts using a direct test packet; the security assertions
  must stay, only the observation mechanism changes.
- A new shell reading an old core would still receive mixed `bytes_down`; this task
  does not change the shell, so the mixed-version assumption is recorded in the
  stage risks and must not be claimed as supported.
- The direct and tunnel counters are not written to the server or to any persistent
  state; they remain local `STATS` fields.

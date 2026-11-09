---
kind: task
status: todo
depends: [01-direct-traffic-counters]
---

# Direct traffic in `-tun-uds` without workers

## Task

Stop `read_tun` from discarding every packet while no worker is registered. In the
`-tun-uds` mode the direct path does not need a worker: `Dispatcher::dispatch` already
runs `direct_relay.try_handle` before its worker check, so removing the early check
restores direct traffic during reconnects, server restarts and short network losses.
Cover proposal 14 in
[`doc/upstream-proposals.md`](../../../../../upstream-proposals.md); this is the second
child of [`Tunnel bypass routing follow-ups`](../plan.md) and follows the
[`Direct-traffic counters`](../01-direct-traffic-counters/plan.md) stage, whose core
child defines the counter behavior.

The current `read_tun` check is in `core/rust-client/dispatcher.rs`; `read_udp` in the
same file has no such check, so `-listen` already behaves as intended. Keep
`is_forwardable_packet`, all read/retry/EOF handling, and the worker check inside
`dispatch` unchanged.

## Done when

- With `-tun-uds` and zero registered workers, a direct TCP SYN read from the TUN file
  reaches the direct stack and its SYN-ACK is written back; a deterministic Rust
  regression using a Unix datagram pair fails before the change (no SYN-ACK) and
  passes after it. The test uses a documentation address and a cancelled/never-awaited
  outer connect, not a live service; its test-only dispatcher construction must wire
  the direct-relay inbound pump and the TUN reader, or reuse the production start path
  with a test TUN file.
- A tunnel-bound packet read with zero workers is still dropped: `dispatch` returns
  `Dropped` before the worker send, and `bytes_up` does not credit it. The counter
  behavior established by the previous child does not shift: direct packets are
  credited to `direct_up`, tunnel packets only to `bytes_up` when a worker accepts
  them.
- The change is authored as a counted rule in `scripts/vendorcore/rules.go` (with
  rule coverage) and regenerated; `vendorcore -check` and idempotence pass against the
  pinned upstream commit.
- The focused Rust regression, the core build and full core suite, the vendor and
  build-script tests, `go build ./...`, `go vet ./...`, and the Go suite pass under the
  mandatory 4 GiB/no-swap scope (window tests with Xvfb `-noreset`).
- The §07 patch table in `doc/architecture.md` gets the next local-patch row, and
  item 14 in `doc/upstream-proposals.md` gets a short Russian "Как сделано у нас"
  note. The upstream pin, protocol and supported platforms stay unchanged.
- No live check is claimed; a live `-tun-uds` run with a real application remains
  unverified until the user selects live verification.

## Risks and limits

- The regression's outer direct `connect` is allowed to remain pending for its
  five-second timeout; the test only needs the immediate SYN-ACK from the stack and
  must cancel the dispatcher instead of waiting for the connect or for the tunnel.
  Do not turn this into a network test.
- Removing the early check makes `read_tun` call `dispatch` for every forwardable
  packet while no worker exists. Direct handling may spawn the direct stack or DNS
  work, but it is bounded by the existing direct-relay limits; tunnel-bound packets
  still return immediately at the worker check.
- The change is Unix-only (`read_tun` is `cfg(unix)`); Windows builds and the
  `-listen` mode must keep their current behavior.

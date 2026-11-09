---
kind: task
status: todo
---

# Reset a failed direct connection

## Task

When the outer `TcpStream::connect` for a direct TCP flow fails or reaches its
five-second timeout, make the application see a reset instead of a connection that
silently closes. The netstack in `core/rust-client/vendor/netstack-smoltcp` answers the
application's SYN with SYN-ACK immediately, so the flow record must keep the
server-side sequence from that SYN-ACK. The stack egress task inside
`DirectRelay::new` in `core/rust-client/direct_relay.rs` sees every frame before it
enters the dispatcher pump, which is a suitable place to record it. On failure,
`direct_relay.rs` sends an RST built from the destination and the recorded sequence
(reuse `craft_tcp_packet_into` or `build_tcp_packet` in the same file), then releases
the stack stream.

This is the first task of [`Failed direct TCP fallback`](../plan.md) and the first half
of proposal 15 in `doc/upstream-proposals.md`. Keep the successful direct path, active
direct flows, UDP, DNS and domain routing untouched. The memory of failed addresses
belongs to the next task; do not add it here beyond what is needed to report a failure.

## Done when

- A direct TCP flow whose outer connect fails or times out emits an RST to the
  application with the recorded server sequence (and the expected direction and
  ports), rather than only dropping the stack stream. A deterministic Rust test
  exercises the failure handler and checks the emitted IP/TCP packet fields.
- The SYN-ACK sequence is captured per flow from the packets leaving the direct stack;
  if it is not available within a small bounded wait, the code still closes the flow
  without inventing a sequence. The wait is bounded and does not block other flows.
- Successful connects, already established direct flows, UDP, DNS and domain-routing
  decisions are unchanged; only the failed-connect path is affected.
- The change is authored as counted vendor rules and added files, regenerated, and
  `vendorcore -check` plus idempotence pass against the pinned upstream commit. The
  core build/tests and the relevant Go build/vet/tests pass under the required scope.
- The §07 patch table in `doc/architecture.md` gets one row for the failed-direct
  fallback with the next free number after П9 (this task adds the reset part; the next
  task extends the same row), and item 15 in `doc/upstream-proposals.md` gets a short
  Russian note.
- No live application or browser check is claimed.

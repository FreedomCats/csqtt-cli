---
kind: stage
status: in-progress
base: 425bcaf
---

# Failed direct TCP fallback

## Goal

Make a failed outer direct TCP connection visible to the application and stop retrying
the same destination directly for a bounded period, so later connections use the
tunnel. Today the netstack answers the application's SYN itself with SYN-ACK, then the
outer `TcpStream::connect` in `core/rust-client/direct_relay.rs` fails or times out and
the application's connection is closed without a useful signal; the next connection to
the same address repeats the failed direct attempt. Cover proposal 15 in
[`doc/upstream-proposals.md`](../../../../../upstream-proposals.md); this is the third
child of [`Tunnel bypass routing follow-ups`](../plan.md) and follows the
[`Direct-traffic counters`](../01-direct-traffic-counters/plan.md) stage and the
`-tun-uds` worker fix.

The proposal has two independently acceptable results: the reset the application sees
and the address memory that changes the next routing decision. They are split into two
tasks below so each can be implemented and reviewed separately.

## Children

1. [Reset a failed direct connection](01-failed-connection-reset/plan.md) — record the
   sequence from the stack's SYN-ACK for each direct TCP flow and, when the outer
   connect fails or times out, send the application an RST instead of a silent close.
2. [Remember failed direct addresses](02-failed-address-fallback/plan.md) — keep the
   destination address as "does not open directly" for a bounded period and route new
   TCP connections to it through the tunnel; expiry restores direct attempts.

## Done when

- A failed outer direct TCP connect that the application saw as established delivers
  an RST, with a sequence derived from the SYN-ACK the stack already sent; when that
  record is unavailable, the handler closes the flow without inventing a sequence. A
  deterministic Rust test checks the emitted packet for the recorded case.
- The destination address of a failed direct connect is remembered for a bounded
  period; new TCP connections to that address go through the tunnel, expired entries
  allow direct attempts again, and already established direct flows keep working.
- UDP, DNS and domain-routing decisions are unchanged; a failed address only affects
  new TCP connections. The routing config being disabled clears the memory.
- The core changes exist only as authored vendor rules and added files;
  `vendorcore -check`, regeneration idempotence, the core build and tests, and the
  relevant Go build/vet/tests pass under the required scope. The architecture §07
  patch table and the Russian note in `doc/upstream-proposals.md` are updated.
- No live browser/application check is claimed. The RST's effect on a real application
  and the fallback under provider filtering remain unverified until the user selects
  live verification.

## Design notes and risks

- The outer connect timeout is five seconds. Both a connection error and the timeout
  are failures for this behaviour; a successful connect followed by a broken copy loop
  is not, because the direct path did open.
- Delaying the application's SYN-ACK until the outer connect succeeds (the option the
  proposal calls best) is not chosen: it would change the netstack wrapper's accept
  contract and the timing of every direct connection. The reset and the address memory
  deliver the user-visible result without that redesign.
- The application-facing RST needs the server-side sequence number. The stack emits
  SYN-ACK before the outer connect starts, so the flow record must capture the SYN-ACK
  sequence while it leaves the direct stack. A bounded wait for that record avoids a
  race when the connect fails immediately; do not spin forever and do not send an RST
  with a made-up sequence.
- The address memory is keyed by destination IP, not by port or domain, as the
  proposal says. That intentionally makes other sites on the same address use the
  tunnel for the memory period; this trade-off is already inherent in the address-based
  routing decision (proposal 17) and is not widened here.
- A memory period of a few minutes is intended; choose one constant (for example five
  minutes), keep it next to the direct-routing constants, and make it injectable or
  short in tests rather than sleeping for minutes.
- No live check is available in this work; a test can prove the emitted packet and the
  routing decision, but not what a particular browser does with the RST.

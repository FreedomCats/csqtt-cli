---
kind: stage
status: done
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

## Outcome

A failed or timed-out outer direct TCP connect is now visible and sticky. The relay
records the stack's SYN-ACK sequence per flow at the egress and sends an
application-facing RST with sequence `S+1` (flags exactly `0x04`) through the existing
inbound channel; without the record within the 500 ms bounded wait it closes the flow
without inventing a sequence. The destination IP is remembered for five minutes at the
failure-handler entry, before the SYN-ACK wait and the reset, so a failure is remembered
even when no RST is sent. New TCP SYNs to a remembered IP are rejected by `try_handle`
before `evaluate_route` and go to the tunnel; `active_tcp` flows stay direct; UDP, DNS
and routing decisions are unchanged. Expiry is exact against an injected instant and
pruned by the existing 30-second task; the memory clears only when the whole routing
configuration is disabled.

The change is eleven plus five counted vendor rules and one extended added test file;
`CORE_VERSION` ends at 128 files, 110 rules, 12 added. §07 carries a single `П10` row,
proposal 15 a single Russian note, and the Part 2 introduction states that items 14–16
are patched locally.

Integrated acceptance over `425bcaf..c1710c4` found one issue and fixed it in `f4a792b`:
the П9 direct-SYN regression assumed the SYN-ACK was the first inbound frame, but a
failed outer connect can now add a RST earlier; the test now skips an optional RST and
waits for `direct_bytes_down` at least the SYN-ACK length while keeping the exact uplink
assertions. One finding was rejected as the recorded project risk: on a multithreaded
runtime the RST can be queued before the SYN-ACK; a queue-marker redesign is out of
scope. Full checks passed: `buildcore -test` 417/0/13, Rust `failure_tests` 7/7 and
`direct_stats_tests` 6/6, generation idempotence with `-check`, build, vet, and the full
Go suite under Xvfb.

Real limitations, recorded rather than fixed: the memory is keyed by IP, so other sites
on that address use the tunnel for five minutes; connections opened during the
five-second attempt still go direct; a successful direct connect has no deterministic
recording test (its absence is pinned structurally by the Go rule test); a pre-existing
upstream `sync_domain_routing` behavior clears `active_tcp`/`udp_sessions` on
`UPDATE_DOMAINS` when only IP routing is enabled, which breaks established flows and
needs a separate backlog task. Live provider filtering and browser behavior remain
unverified.

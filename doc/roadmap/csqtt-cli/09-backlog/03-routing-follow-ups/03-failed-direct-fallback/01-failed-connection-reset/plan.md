---
kind: task
status: done
detailed: true
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

## Steps

1. **Author the per-flow SYN-ACK record** in `core/rust-client/direct_relay.rs` as
   counted rules in `scripts/vendorcore/rules.go` (`core/` is never edited by hand):
   - add a private constant beside `DIRECT_RELAY_INBOUND_CAPACITY`:
     `const SYN_ACK_RECORD_WAIT: Duration = Duration::from_millis(500);` with an English
     doc comment saying it covers only the accept-versus-egress task-start race — the
     stack's frame normally arrives first;
   - add to `DirectRelay` a field
     `syn_ack_seqs: Arc<RwLock<HashMap<(SocketAddr, SocketAddr), u32>>>`. The key is
     `(local_addr, remote_addr)`, exactly the `active_tcp` key, the listener tuple and
     `try_handle`'s `(src, dst)`: application address first, destination second. The
     value is the 32-bit sequence the stack put into its SYN-ACK;
   - in `DirectRelay::new`, create the map before the egress task spawns
     (`let syn_ack_seqs = Arc::new(RwLock::new(HashMap::new()));` plus an egress
     clone) and store the `Arc` in the `Self` literal. In the egress loop, record the
     frame after `let frame = ...` and **before** `pool_egress.try_acquire()`: the
     record must not depend on pool availability and must exist before the SYN-ACK can
     reach the pump. The rest of the egress frame path (`extend_tail`,
     `copy_from_slice`, `send`) stays as it is;
   - record with two new private free functions:
     `fn syn_ack_flow(frame: &[u8]) -> Option<((SocketAddr, SocketAddr), u32)>` and
     `fn record_syn_ack(map: &RwLock<HashMap<...>>, frame: &[u8])`. Parse IPv4 and
     IPv6, require protocol 6, enough bytes for the IP header plus 20 TCP bytes, and
     flags `(tcp[13] & 0x12) == 0x12` (SYN and ACK set, i.e. the stack's SYN-ACK).
     Read the sequence at `tcp[4..8]`. Take addresses from
     `crate::sni::extract_src_addr` / `extract_dst_addr` and store the key as
     `(dst, src)`, because a SYN-ACK travels from the destination to the application.
     Anything else — a data or plain-ACK frame, a SYN without ACK, a truncated or
     non-TCP frame — returns `None`; never guess. Insert under a short write lock and
     do not hold the guard across an `await`.

2. **Emit the reset on a failed or timed-out outer connect** in the same file:
   - `async fn wait_for_syn_ack_seq(map: &RwLock<HashMap<...>>, key: (SocketAddr, SocketAddr), wait: Duration) -> Option<u32>`:
     check once, then poll every 10 ms until `wait` expires. The wait is a parameter;
     production passes `SYN_ACK_RECORD_WAIT` (500 ms), tests pass a short duration. Do
     not spin without a deadline and do not hold the read guard across the sleep;
   - `async fn abort_failed_direct_tcp(pool: &PacketPool, inbound_tx: &mpsc::Sender<PacketBuf>, map: &RwLock<...>, key: (SocketAddr, SocketAddr), wait: Duration)`:
     on `Some(seq)` build the application-facing packet with
     `build_tcp_packet(key.1, key.0, seq.wrapping_add(1), 0, 0x04, &[])` — source is
     the destination address/port, target is the application, acknowledgement 0,
     flags RST only. The sequence is the recorded SYN-ACK sequence **plus one**: the
     SYN-ACK consumed one sequence number, so the application's `RCV.NXT` is `S+1`,
     and RFC 5961 stacks accept a RST only at or inside the receive window — a RST at
     `S` is outside on the left and would be dropped or answered with a challenge ACK.
     Copy the packet into a `PacketBuf` acquired from the pool and send it through
     `inbound_tx` exactly as the egress task does. Remove the map entry after the send
     attempt, success or not, so a second caller cannot reuse it. On `None` return
     without sending anything and without inventing a sequence;
   - wire the call into the listener task in `DirectRelay::new`. Clone `pool`,
     `inbound_tx` and the map once before the `if let Some(mut listener) = tcp_listener`
     block, and clone those three again per accepted connection beside
     `active_tcp_conn`. Turn the existing
     `if let Ok(Ok(mut server_stream)) = connect_res { ... }` into an `if let ... { ... }
     else { abort_failed_direct_tcp(...).await; }`. Both `Ok(Err(_))` (connect error)
     and `Err(_)` (the five-second timeout) take the `else`; the success branch, its
     socket options and `copy_bidirectional_with_sizes` loop stay byte-for-byte as they
     are, so a successful connect followed by a broken copy loop does **not** reset;
   - keep `drop(client_stream)` and the `active_tcp` removal after the branch. Add
     removal of the same key from `syn_ack_seqs` there as well: a long successful flow
     and a failed flow must both leave no stale record, and the handler's own removal
     only prevents a second send;
   - no counter code: the RST enters the same `direct_inbound_tx` channel as every
     stack frame, so `Dispatcher::push_direct_inbound` credits it as `direct_down` under
     the П8 contract. `sync_domain_routing`, `active_tcp` semantics, UDP, DNS,
     `evaluate_route` and `try_handle` are unchanged. Dropping the stack stream after
     the reset closes the smoltcp side; the vendored stack's close sends no RST of its
     own, so the application sees only the packet generated here.

3. **Add the deterministic Rust regression** as an added file
   `scripts/vendorcore/files/rust-client/direct_relay_failure_tests.rs`, connected as
   a child module `#[cfg(test)]` / `#[path = "direct_relay_failure_tests.rs"]` /
   `mod failure_tests;` after the `bind_if_tests` module. As a child of
   `direct_relay` it can read the relay's private fields and call the private handler,
   like the file's own `mod tests`. Use documentation addresses only
   (`192.0.2.0/24`, `2001:db8::/32`), no live service, no real minutes:
   - `syn_ack_record_reaches_the_failure_path`: build the production
     `DirectRelay::new` with `DomainRoutingConfig { enabled: false,
     ip_routing_enabled: true, ip_is_whitelist: false,
     ip_rules: [IpRule::Cidr(192.0.2.0, 24)] }` and an empty `direct_ips`, so no
     preresolve or DNS runs. Send a SYN `10.66.67.2:54321 → 192.0.2.10:80` through
     `try_handle`. Read frames from the relay's `inbound_tx` with a two-second bound in
     a loop until both a TCP SYN+ACK and a TCP RST have been seen. When the SYN+ACK
     arrives, parse its sequence `S` and, if the map still holds
     `(10.66.67.2:54321, 192.0.2.10:80) → S`, call `abort_failed_direct_tcp` with a
     100 ms injected wait before continuing to read; otherwise the production handler
     already took the record and sent the RST (the RST may arrive before or after the
     SYN-ACK, because the production connect task can send it as soon as the record
     exists). Assert both frames: the SYN+ACK
     goes from `192.0.2.10:80` to `10.66.67.2:54321` with flags `0x12`; the RST uses
     the same address pair, flags exactly `0x04`, sequence `S+1`, acknowledgement 0
     and valid checksums; the map entry is gone. Never await the outer connect and
     never assert on its result: it may stay pending for its five-second timeout and
     is dropped with the test runtime;
   - `failed_connect_emits_reset_with_recorded_sequence`: no relay needed. Create
     `PacketPool::new(16)`, an `mpsc::channel(64)` and a map with
     `(10.66.67.2:54321, 192.0.2.10:80) → 0x1122_3344`; call the handler with a 10 ms
     wait and take one packet. Assert IPv4, protocol 6, total length 40; source
     `192.0.2.10:80`, destination `10.66.67.2:54321`; TCP sequence `0x1122_3345`
     (`S+1`), acknowledgement 0, data offset 5, flags exactly `0x04`;
     `ip_checksum(&packet[..20]) == 0` and
     `tcp_checksum_v4(src_ip, dst_ip, &packet[20..]) == 0`; the map is empty after the
     call. Repeat the call for an IPv6 pair
     (`[2001:db8::1]:443` → `[2001:db8::2]:12345`): total length 60, the same
     direction, ports, `S+1`, flags and `tcp_checksum_v6(...) == 0`;
   - `missing_record_closes_without_reset`: an empty map; call the handler with a
     10 ms wait inside a one-second `tokio::time::timeout` so a hang fails the test;
     assert it returns and that `tokio::time::timeout(Duration::from_millis(50),
     rx.recv())` times out — no packet, no invented sequence;
   - `syn_ack_flow_accepts_only_syn_ack`: frames built with `build_tcp_packet` — an
     IPv4 SYN-ACK (`0x12`) from `192.0.2.10:80` to `10.66.67.2:54321` gives the key
     and the exact sequence; an IPv6 SYN-ACK from `[2001:db8::1]:443` to
     `[2001:db8::2]:12345` gives the same; a plain ACK (`0x10`), a SYN only (`0x02`),
     a truncated frame and a UDP frame give `None`.

   The full-path test exercises the stack egress, `try_handle` and the relay, but does
   not assert that the production task, rather than the test, sent the packet; the
   call site is pinned by the Go rule test in step 5. Do not add a five-second wait or
   a live target to make the production branch send it.

4. **Author the rules and regenerate**:
   - the change is eleven counted rules for `rust-client/direct_relay.rs`: the
     constant; the struct field; the `Self` literal field; the map creation and egress
     clone; the egress record call; the outer clones; the per-connection clones; the
     `else` branch; the flow-end removal; the free functions; and the test-module
     connection. Every anchor is unique with `Count: 1`. The `else` pattern includes
     the success branch tail (`drop(server_stream);`, its closing brace and
     `drop(client_stream);`) so it cannot match another block. The module rule must
     come after the П3 rule that inserts the `bind_if_tests` block and must anchor on
     that generated block — not on `mod tests`, because two rules already share that
     anchor. Rules apply in order (`scripts/vendorcore/apply.go:8`); new Rust comments
     are English and name `П10`;
   - regenerate `core/` from a clean checkout of the pinned commit
     `d02da9bd9d8926969b766e197c434d830a83dbd8` (as recorded in `core/CORE_VERSION`),
     run generation twice to prove idempotence, and run `-check`. `CORE_VERSION` must
     change only through generation: files 127→128, rules 94→105, added 11→12. Do not
     edit `CORE_VERSION` or anything under `core/` by hand;
   - the diff must contain only `scripts/vendorcore/rules.go`, the new Go rule test,
     the new added Rust file, the regenerated `core/` (the changed `direct_relay.rs`,
     the new test file and `CORE_VERSION`), and the two documents of step 6.

5. **Pin the rules with Go tests** in a new `scripts/vendorcore/direct_fallback_test.go`
   (model: `tun_direct_test.go`):
   - select each of the new rules by its anchor, exactly once, through a helper with a
     count check, so a future upstream change fails the test instead of silently
     skipping a rule. Keep the P8 selection of 26 rules in `direct_stats_test.go` and
     the П9 selection in `tun_direct_test.go` untouched;
   - apply the `else` rule to a representative post-П9 listener snippet (the success
     `if let`, socket options, copy loop, `drop(server_stream);`, `drop(client_stream);`);
     assert the handler call appears once in an `else`, the success branch and both
     drops remain, and a missing or duplicated anchor fails;
   - apply the egress record rule to a representative egress snippet; assert
     `record_syn_ack` lands before `pool_egress.try_acquire()` and the forwarding code
     is unchanged; fail closed;
   - apply the module rule to a snippet containing the generated `bind_if_tests` block;
     assert one `mod failure_tests;` with `#[cfg(test)]`, the `bind_if_tests` block
     intact, and a missing anchor fails;
   - cover the remaining rules (constant, struct field, literal field, clones, flow-end
     removal, free functions) at least by selection, so every anchor is exercised.

6. **Update the Russian documents** in place (do not retranslate):
   - `doc/architecture.md` §07: one new table row `П10` after `П9` for the whole
     fallback, with the reset part now: the SYN-ACK sequence already sent to the
     application is recorded per flow; on an outer `connect` error or timeout the
     application gets an RST with the next expected sequence, and without the record
     within a small bounded wait the flow is closed without a reset. “Зачем” — the
     application saw an established and silently closed connection and there was no
     fallback to the tunnel, so a filtered destination never opened (proposal 15).
     “Этап” — `09-backlog: follow-ups обхода туннеля`. The next task extends this same
     row with the address memory; do not add a second row;
   - `doc/upstream-proposals.md` item 15: a short
     `**Как сделано у нас (правка П10).**` note — the sequence is taken from the
     stack's SYN-ACK at the egress; on the connect error or the five-second timeout
     the application gets an RST with the next expected sequence (the recorded one
     plus one, because the SYN consumes a number and a reset at the recorded value
     would be outside the application's window); without the record within a small
     bounded wait the flow is closed and no sequence is invented; successful direct
     connections, established direct flows, UDP, DNS and routing decisions are
     unchanged; the regression is `direct_relay_failure_tests.rs` without a live
     service; the address memory is the next part. Keep the historical “Проверено”
     paragraph;
   - no personal or environment data.

7. **Run the checks** from the repository root. Every Go test/test binary and every
   Rust build/test runs in the mandatory scope
   (`systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600`),
   the full Go suite additionally under Xvfb `-noreset`:

   ```sh
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go test -count=1 ./scripts/vendorcore ./scripts/buildcore
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go run ./scripts/vendorcore -upstream <clean checkout of d02da9bd…>
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go run ./scripts/vendorcore -check -upstream <clean checkout of d02da9bd…>
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go run ./scripts/buildcore
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     cargo test --locked --manifest-path core/rust-client/Cargo.toml \
       --target x86_64-unknown-linux-gnu --target-dir build/cargo --bin client failure_tests
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     cargo test --locked --manifest-path core/rust-client/Cargo.toml \
       --target x86_64-unknown-linux-gnu --target-dir build/cargo --bin client direct_stats_tests
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go run ./scripts/buildcore -test
   go build ./...
   go vet ./...
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 \
     xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test -count=1 ./...
   ```

   Generation runs twice. The known upstream integration flake at
   `turn_integration_tests.rs:291` / `:571` is recorded if it appears and never
   suppressed. Hosted checks and any push remain pending. No live core connection,
   browser, provider or Windows run is required or claimed.

## Done when

- A direct TCP flow whose outer connect returns an error or reaches the five-second
  timeout emits an application-facing RST: source is the destination address and port,
  destination is the application's address and port, sequence is the recorded SYN-ACK
  sequence plus one (the application's next expected sequence), acknowledgement 0,
  flags exactly `0x04`, a 20-byte TCP header and valid IPv4/IPv6 checksums.
  `direct_relay_failure_tests.rs` proves this deterministically, with no live service,
  and checks the exact IP/TCP fields and the removal of the record.
- The SYN-ACK sequence is captured per flow from frames leaving the direct stack:
  only TCP SYN+ACK frames (IPv4 and IPv6) are recorded, keyed by
  `(application, destination)`; non-SYN-ACK, truncated and non-TCP frames are ignored.
  The record is written before the frame enters the dispatcher pump and does not
  depend on pool availability.
- The failure path waits for the record for a bounded injected duration
  (`SYN_ACK_RECORD_WAIT`, 500 ms in production, short in tests). When the record is
  absent it returns and the flow is closed without a packet, never with an invented
  sequence; a test with an empty map proves the silent close and the bounded return.
- The reset is emitted only from the connect-failure `else` branch: `Ok(Err(_))` and
  `Err(_)` take it, while a successful connect and a later broken copy loop do not.
  The success branch, socket options, copy loop, `active_tcp`, `sync_domain_routing`,
  UDP, DNS, `evaluate_route` and `try_handle` are unchanged. The RST goes through the
  existing `direct_inbound_tx`, so the П8 counter contract is unchanged.
- The change exists only as counted vendor rules in `scripts/vendorcore/rules.go`
  (eleven new rules) plus one added Rust test file. Two regenerations from the pinned
  `d02da9bd…` checkout are idempotent, `vendorcore -check` passes, and `CORE_VERSION`
  changes only through generation: 127→128 files, 94→105 rules, 11→12 added files.
  `core/` is not hand-edited.
- The new Go rule test selects every new rule by its anchor and fails closed on a
  missing or duplicated anchor; the P8 (26 rules) and П9 selections are unaffected.
  `go test ./scripts/vendorcore ./scripts/buildcore`, `buildcore`, the focused
  `failure_tests` and `direct_stats_tests` Rust runs, `buildcore -test`,
  `go build ./...`, `go vet ./...` and the full Go suite under Xvfb `-noreset` pass in
  the mandatory 4 GiB/no-swap scope. The known upstream turn flake is recorded if it
  appears, not suppressed.
- `doc/architecture.md` §07 has the single `П10` row (the reset part; the next task
  extends it), and item 15 of `doc/upstream-proposals.md` has the short Russian
  “Как сделано у нас (правка П10)” note; both are edited in Russian and the rest of
  the documents is not retranslated.
- No live browser/application or provider check is claimed. The Windows core build is
  not available here; the change is platform-neutral Rust and no Windows behaviour is
  claimed.

## Risks and limits

- The record is keyed by the four-tuple. If the application retransmits a SYN so the
  stack issues a second SYN-ACK for the same tuple, the later sequence wins; this is
  the same tuple `active_tcp` already uses, and the ambiguity is not widened here.
- The 500 ms wait covers only the accept-versus-egress task-start race. If the stack
  frame is lost before the egress parse, there is no record and the flow closes
  silently, as specified. Do not lengthen the wait to cover network conditions and do
  not send a guessed sequence.
- The application validates the RST against its receive window: `S+1` is the exact
  `RCV.NXT` after the SYN-ACK, while `S` is outside the window. Do not “simplify” the
  sequence to `S`; the test's assertion and the real behaviour both depend on `S+1`.
- The full-path test starts the production connect to a reserved documentation
  address. It may stay pending until its five-second timeout and is dropped with the
  test runtime; never await it and never use a live target to make the failure faster.
- The reset is counted as `direct_down` by the existing pump, which matches the П8
  contract (a packet from the direct path entering the inbound queue). No counter test
  changes, but the added packet must not be credited anywhere else.
- Only the Linux core build and tests run here; the Windows core build is not
  available on this host. No live browser/application check is possible in this work:
  whether a particular browser turns the reset into a useful error or retries another
  address stays unverified, and the address memory is the next task.
- `doc/architecture.md` and `doc/upstream-proposals.md` are Russian; edit them in
  Russian, keep one `П10` row for the whole fallback, and do not add environment or
  personal data.

## Outcome

A direct TCP flow whose outer `TcpStream::connect` returns an error or reaches its
five-second timeout now reaches the application as a reset instead of a silent close.
The egress task of `DirectRelay::new` records the sequence of every TCP SYN+ACK frame
(IPv4 and IPv6) under the same `(application, destination)` key as `active_tcp`, after
the frame is read and before the packet pool is asked for a buffer, so the record does
not depend on pool availability. On failure the listener's `else` branch
(`Ok(Err(_))` and `Err(_)`) calls `abort_failed_direct_tcp`: it waits up to
`SYN_ACK_RECORD_WAIT` (500 ms in production, polled every 10 ms) for the record, sends
`build_tcp_packet(key.1, key.0, S.wrapping_add(1), 0, 0x04, &[])` through the existing
`inbound_tx`, and removes the record; without a record the flow closes with no packet
and no invented sequence. The reset enters the same channel as every stack frame, so
`Dispatcher::push_direct_inbound` credits it as `direct_down` under the П8 contract; no
counter code was added. A successful connect, its socket options and copy loop, a
broken copy loop after a successful connect, `active_tcp`, UDP, DNS,
`sync_domain_routing`, `evaluate_route` and `try_handle` are unchanged.

The change is eleven counted rules for `rust-client/direct_relay.rs` plus one added test
file; `core/` and `CORE_VERSION` were produced only by generation from the pinned
`d02da9b`: 128 files, 105 rules, 12 added files. Two generations gave an identical
`core/` tree and `vendorcore -check` passes. Three implementation details differ from
the plan's wording: the handler takes `&Arc<PacketPool>` because `try_acquire` is a
method on `&Arc<Self>`; the struct-field rule anchors on the `active_tcp` field because
`inbound_tx` appears twice in the file; the field is
`#[cfg_attr(not(test), allow(dead_code))]` because in non-test builds only the clone
made before construction is read. `syn_ack_flow` takes the IPv4 header length from the
IHL, as `extract_src_addr`/`extract_dst_addr` do.

`direct_relay_failure_tests.rs` proves the behaviour deterministically on documentation
addresses and no live service: the full path records the SYN-ACK and delivers the reset
with `S+1`, flags `0x04`, acknowledgement 0 and valid checksums; the handler alone
produces the same reset for IPv4 and IPv6; an empty record closes silently within the
injected wait; only SYN+ACK frames are accepted. The full-path test never awaits the
outer connect and lets it expire with the test runtime. `direct_fallback_test.go` pins
each of the eleven rules by a unique anchor and applies the `else`, egress-record and
module rules to representative snippets, failing closed on a missing or duplicated
anchor; the П8 (26 rules) and П9 selections are untouched. Architecture §07 gained the
single `П10` row (the reset part; the address memory extends it in the next task) and
proposal 15 the Russian “Как сделано у нас (правка П10)” note, both without
retranslating the documents.

Checks passed under the mandatory scope: `go test ./scripts/vendorcore
./scripts/buildcore`, two generations plus `-check`, `buildcore`, the focused Rust
`failure_tests` (4) and `direct_stats_tests` (6), `buildcore -test` (414 passed, 0
failed, 13 ignored), `go build ./...`, `go vet ./...` and the full Go suite under Xvfb
`-noreset`; the known upstream turn flake did not appear. Review found no findings and
nothing was left unfixed because of cost.

Limits: no live browser/application or provider check is claimed — whether a particular
application turns the reset into a useful error or retries another address stays
unverified; the Windows core build is not available here and the change is
platform-neutral Rust, so no Windows behaviour is claimed; the address memory is the
next task.

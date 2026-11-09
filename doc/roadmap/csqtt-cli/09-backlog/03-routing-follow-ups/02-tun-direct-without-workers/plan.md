---
kind: task
status: in-progress
detailed: true
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
`dispatch` unchanged. The regression starts the production `-tun-uds` path
(`Dispatcher::start_with_domain_routing` with a test TUN file passed over the abstract
UDS); `start_test_tun` cannot be reused because it builds no direct relay.

## Steps

1. Add the Rust regression as a new added file
   `scripts/vendorcore/files/rust-client/tun_direct_tests.rs` (vendorcore copies it to
   `core/rust-client/`), connected as a child module of `dispatcher` so it can use the
   private test seams like `direct_stats_tests.rs`. Build the packets with
   `crate::direct_relay::build_tcp_packet`: a direct SYN `10.0.0.2:54321` →
   `192.0.2.10:80` (flags `0x02`) and a tunnel-bound SYN to `198.51.100.10:80`.
   Configure `DomainRoutingConfig` with `enabled: false`, `ip_routing_enabled: true`,
   `ip_is_whitelist: false` and one `IpRule::Cidr(192.0.2.0, 24)` rule, so only the
   documentation prefix is direct. Start the production path with
   `Dispatcher::start_with_domain_routing("127.0.0.1:0", None, Some(name), pool, stats,
   cancel, routing, worker_count)` and register no worker; assert
   `active_count() == 0`.

2. Build the test TUN without a real device or network:
   - take a `std::os::unix::net::UnixDatagram::pair()`, set both ends nonblocking and
     turn the dispatcher end into `File::from_raw_fd` (as `turn_integration_tests.rs`
     does);
   - create a unique abstract UDS name (`csqtt-tun-direct-test-<pid>-<random>`) and pass
     it as `tun_uds`; send the `File` to the core's `-tun-uds` listener with
     `SCM_RIGHTS` using the same `socket`/`connect`/`sendmsg` shape as the tests in
     `core/rust-client/tun.rs` and `tun_peer.rs`; keep the helper local to the module
     and do not modify `tun.rs`;
   - read the one-byte ack that `FdReceiver::receive` sends (blocking helper through
     `spawn_blocking`, with a read timeout) so a broken FD transport is reported as
     such, not as a missing SYN-ACK;
   - convert the test end to `tokio::net::UnixDatagram`, send the tunnel-bound SYN and
     then the direct SYN, and wait up to about five seconds for the SYN-ACK.

3. Assert the result in that single scenario:
   - the datagram read from the test TUN is IPv4/TCP, `192.0.2.10:80` →
     `10.0.0.2:54321`, with SYN-ACK flags `0x12`;
   - `direct_bytes_up` equals the direct SYN length; poll it with a short bounded wait
     (about two seconds) instead of assuming the read loop credited it before the
     SYN-ACK was written; `total_bytes_up` is zero (the tunnel-bound packet returned
     `Dropped` at the worker check and credited nothing);
   - `direct_bytes_down` equals the SYN-ACK length and `total_bytes_down` is zero;
   - finish with `dispatcher.shutdown().await`. Never await the outer direct `connect`
     and never assert on it: the test ends at the SYN-ACK. Use only documentation
     addresses; no live server, service, real TUN or network assertion.

4. Connect the module with a counted rule in `scripts/vendorcore/rules.go`, placed after
   the P8 module rule so it can anchor on its generated block: after
   `#[path = "direct_stats_tests.rs"]` / `mod direct_stats_tests;` insert
   `#[cfg(all(test, unix))]` / `#[path = "tun_direct_tests.rs"]` /
   `mod tun_direct_tests;`, `Count: 1`. The module rule must not use the
   `#[cfg(test)] mod tests` anchor, otherwise `directStatsRules` in
   `direct_stats_test.go` would select it and its expected count of 26 would break.

5. Record RED before the fix: regenerate `core/` from the clean pinned checkout, run the
   focused `tun_direct_tests` run and confirm it fails because no SYN-ACK arrives while
   the early check is still there. Keep the failure output for the task Outcome; do not
   lengthen the timeout or weaken the assertion.

6. Author the behavior as a counted rule in `scripts/vendorcore/rules.go` (`Count: 1`,
   after the P8 uplink-accounting rule): in `read_tun` only, replace
   `if self.workers.load().is_empty() { continue; }` before
   `let outcome = self.dispatch(packet).await;` with a short English comment naming
   `П9` and saying the direct path needs no worker while `dispatch` drops tunnel-bound
   packets itself, then the `let outcome` line. Include the `let outcome` line in the
   anchor so `read_udp` and any duplicated check cannot match. Update the P8 comment in
   `rules.go` that currently says this early exit "stays".

7. Add `scripts/vendorcore/tun_direct_test.go` with rule coverage:
   - select the behavior rule by its `workers.load().is_empty`/`let outcome` anchor,
     apply it to a representative post-P8 `read_tun`/`read_udp` snippet (the shape with
     `let outcome` and `credit_uplink`), assert the early check is gone only from
     `read_tun`, `self.dispatch(packet).await` and
     `credit_uplink(&stats, outcome, length)` still appear twice, and `read_udp` is
     unchanged; fail closed on zero or duplicated anchors;
   - select the module rule by its `tun_direct_tests` insertion, apply it to a snippet
     containing the P8 `direct_stats_tests` block, assert the new module appears once
     with `#[cfg(all(test, unix))]`; fail closed without the anchor;
   - keep the existing `direct_stats_test.go` selection and its 26 rules untouched.

8. Regenerate and check: repeat `go run ./scripts/vendorcore -upstream <clean pinned
   checkout>` to prove idempotence and run `-check`. `core/` is never edited by hand;
   `CORE_VERSION` must change only through generation (`rules` 92→94, `added` 10→11,
   `files` 126→127; the intermediate RED state has 93 rules). Run the focused
   `tun_direct_tests` and `direct_stats_tests` Rust runs and confirm GREEN.

9. Record the change in the Russian documents (and one stale test comment), without
   retranslating the documents:
   - `doc/architecture.md` §07: add the next patch row `П9` — `read_tun` no longer
     drops packets while no worker is registered; the early check is gone and `dispatch`
     drops only tunnel-bound packets, so the direct path works in `-tun-uds`; the “why”
     column — direct traffic died on every reconnect, server restart and short network
     loss even though the direct path needs no workers (proposal 14); the stage column —
     `09-backlog: follow-ups обхода туннеля`;
   - `doc/upstream-proposals.md`: add a short “Как сделано у нас (правка П9)” note to
     item 14 saying the early check is removed, the direct SYN gets its SYN-ACK back even
     with zero workers, a tunnel-bound packet is `Dropped` without counters, and
     `read_udp`/`-listen`/Windows are unchanged (`read_tun` is Unix-only); the regression
     is `tun_direct_tests.rs` on a test TUN, and live verification with an application is
     still separate. Keep the historical “Проверено” paragraph;
   - replace the now-false sentence in item 16 (“Прямой путь в `-tun-uds` без воркеров
     по-прежнему не работает — это отдельное предложение 14.”) with a reference to the
     implemented `П9`;
   - update the stale comment in `internal/datapath/direct_netns_linux_test.go` that says
     `-tun-uds` drops everything while no worker exists; the test keeps its `-listen`
     Link path and its behavior is unchanged.

10. Validate from the repository root. Every Go test and test binary runs in the
    mandatory scope; Rust builds and tests use the same scope:

    ```sh
    systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
      go test -count=1 ./scripts/vendorcore ./scripts/buildcore
    systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
      go run ./scripts/vendorcore -upstream <clean checkout of the pinned commit>
    systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
      go run ./scripts/vendorcore -check -upstream <clean checkout of the pinned commit>
    systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
      go run ./scripts/buildcore
    systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
      cargo test --locked --manifest-path core/rust-client/Cargo.toml \
        --target x86_64-unknown-linux-gnu --target-dir build/cargo --bin client \
        tun_direct_tests
    systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
      cargo test --locked --manifest-path core/rust-client/Cargo.toml \
        --target x86_64-unknown-linux-gnu --target-dir build/cargo --bin client \
        direct_stats_tests
    systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
      go run ./scripts/buildcore -test
    go build ./...
    go vet ./...
    systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 \
      xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test -count=1 ./...
    ```

    Run generation twice to prove idempotence. The known upstream integration flake at
    `turn_integration_tests.rs:291` / `:571` is recorded if it appears and never
    suppressed. Hosted checks and any push remain pending. No live core connection,
    `-tun-uds` run with a real application, or container netns run is required or
    claimed.

## Done when

- With `-tun-uds` and zero registered workers, a direct TCP SYN read from the TUN file
  reaches the direct stack and its SYN-ACK is written back. `tun_direct_tests.rs` proves
  this deterministically: it fails before the fix (no SYN-ACK within the timeout) and
  passes after it. The test starts the production
  `Dispatcher::start_with_domain_routing` path, passes one end of a Unix datagram pair
  by `SCM_RIGHTS` into the abstract UDS, keeps the other end, uses documentation
  addresses only, and never awaits the outer direct connect, a live service or the
  network.
- In the same test a tunnel-bound packet read with zero workers is still dropped:
  `dispatch` returns `Dropped` before the worker send and neither `bytes_up` nor
  `direct_up` grows. After the direct SYN is answered, `direct_up` equals the direct SYN
  length and `bytes_up` is zero; `direct_down` equals the SYN-ACK length and `bytes_down`
  is zero. The counter contract of the previous child does not shift.
- `is_forwardable_packet`, the read/retry/EOF handling, the worker check inside
  `dispatch`, `read_udp` and `-listen` are unchanged; the existing `listen_peer` tests
  (9) and the malformed-packet dispatcher safety test still pass.
- The behavior and the test-module connection exist only as counted rules in
  `scripts/vendorcore/rules.go` plus the added `tun_direct_tests.rs`; the new Go rule
  tests fail closed on anchor drift or a duplicated check; regeneration from the clean
  pinned checkout (`d02da9b` in `core/CORE_VERSION`) is idempotent, `vendorcore -check`
  passes, and `CORE_VERSION` changes only through generation (92→94 rules, 10→11 added
  files, 126→127 files).
- `doc/architecture.md` §07 has the `П9` row and item 14 of
  `doc/upstream-proposals.md` has a short Russian “Как сделано у нас” note; the stale
  sentences in item 16 and in `internal/datapath/direct_netns_linux_test.go` no longer
  say that `-tun-uds` drops direct traffic without workers. The upstream pin, protocol,
  dependencies and supported platforms stay unchanged; no personal or environment data
  is added.
- `go test ./scripts/vendorcore ./scripts/buildcore`, `buildcore`, the focused
  `tun_direct_tests` and `direct_stats_tests` runs, the full core suite
  (`buildcore -test`), `go build ./...`, `go vet ./...` and the full Go suite with Xvfb
  `-noreset` pass under the mandatory 4 GiB/no-swap scope. The known upstream
  integration flake is recorded if it appears, not suppressed.
- No live `-tun-uds` run with a real application is claimed; the container-only netns
  direct test keeps its current behavior. The Windows core build is not available on
  this host; the diff touches only `cfg(unix)` code and the new test module is Unix-gated.

## Risks and limits

- The regression's outer direct `connect` is allowed to remain pending for its
  five-second timeout. The SYN-ACK comes from smoltcp before the connect result; the test
  must shut the dispatcher down after the SYN-ACK instead of awaiting the connect, the
  tunnel or the socket. Do not turn this into a network test.
- The test starts the production `-tun-uds` branch, including the `П4` `SO_PEERCRED`
  check: the sender is the test process itself, so it passes. The abstract UDS name must
  be unique per run (pid + random), otherwise `FdReceiver::bind` retries on
  `EADDRINUSE`. `start_test_tun` is unusable here because it builds `direct_relay: None`;
  do not change it, and do not hand-roll the direct-relay pump instead of using the
  production start path.
- The FD helper is test-local and follows the shape already used by the `tun.rs` and
  `tun_peer.rs` tests; do not refactor production `tun.rs` for it. Abstract Unix sockets
  exist on Linux/Android; the module follows `read_tun`'s `cfg(unix)` and the project's
  test matrix.
- Removing the early check makes `read_tun` call `dispatch` for every forwardable packet
  while no worker exists. `inspect_domain_routing` and `try_handle` may do bounded work
  (direct stack, DNS), and DNS/SNI learning from TUN packets now happens without
  workers exactly as it already does in `-listen`; the existing direct-relay limits
  still apply and tunnel-bound packets return immediately at the worker check. Do not
  remove the worker check from `dispatch`.
- `read_udp` in `-listen` and every Windows path must stay unchanged: the rule's anchor is
  limited to `read_tun`, the test module is `cfg(all(test, unix))`, and the comment update
  in the Linux-only netns test changes no behavior.
- If a future change moves the direct inbound pump, the TUN writer or the `-tun-uds`
  receiver, the regression must be updated with the code, not weakened.
- The two documents are Russian; edit them in Russian and do not retranslate the rest.
  The “Проверено” paragraph in item 14 stays historical; the new note records the
  implementation.

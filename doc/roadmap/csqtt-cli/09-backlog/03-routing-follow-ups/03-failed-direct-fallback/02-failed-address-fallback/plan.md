---
kind: task
status: in-progress
detailed: true
depends: [01-failed-connection-reset]
---

# Remember failed direct addresses

## Task

After a direct TCP connect fails, remember the destination address for a bounded period
and route new TCP connections to it through the tunnel instead of attempting the direct
path again. This is the second task of [`Failed direct TCP fallback`](../plan.md) and
the fallback half of proposal 15 in `doc/upstream-proposals.md`.

Use a memory keyed by destination IP with an expiry of five minutes (one constant,
`FAILED_DIRECT_MEMORY_TTL`, next to the other direct-routing constants). Record it at
the start of the existing failure handler in `core/rust-client/direct_relay.rs`, before
the bounded wait for the SYN-ACK record and before the RST: `Ok(Err(_))` and the
five-second `Err(_)` timeout both take that path, and the fallback must not depend on
the reset record. Check it in `DirectRelay::try_handle` after the DNS handling and
before `evaluate_route`, only for protocol 6: a new SYN to a remembered address returns
false, so `dispatch` sends it to the tunnel. Non-SYN packets of an established flow
return through the existing `active_tcp` branch before the check, so open direct flows
stay direct; UDP, DNS, `evaluate_route` and the direct verdict are otherwise unchanged.
The memory is cleared when the whole routing configuration is disabled (the existing
`if !config.enabled` / `if !config.ip_routing_enabled` path of `sync_domain_routing`)
and pruned by the existing 30-second cleanup task; expiry is checked against an injected
instant, so tests do not sleep for minutes. A SYN routed to the tunnel is tunnel traffic
under the П8 contract and needs no counter code.

## Steps

1. **Add the address memory and record it in the existing failure handler** in
   `core/rust-client/direct_relay.rs` (as vendor rules, never by editing the generated
   file):
   - add `const FAILED_DIRECT_MEMORY_TTL: Duration = Duration::from_secs(300);` beside
     `SYN_ACK_RECORD_WAIT` with an English doc comment: the period during which a
     destination whose direct connect failed is not tried directly; production uses
     five minutes, tests inject a shorter one;
   - add `failed_direct_ips: Arc<RwLock<HashMap<IpAddr, std::time::Instant>>>` to
     `DirectRelay` beside `syn_ack_seqs`. The key is the destination IP only, the value
     is the instant after which a direct attempt is allowed again. `IpAddr` is already
     imported; no `allow(dead_code)` is needed because `try_handle` reads the field in
     production;
   - create the map in `DirectRelay::new` after
     `let active_tcp = Arc::new(RwLock::new(HashMap::new()));`, before the listener and
     cleanup tasks spawn, and give the listener and the cleanup task their own clones;
   - add two free helpers beside the existing П10 helpers, with the time injected:
     `fn remember_failed_direct(map, ip, now: std::time::Instant, ttl: Duration)`
     inserts `ip → now + ttl`, and
     `fn failed_direct_active(map, ip, now: std::time::Instant) -> bool` returns true
     only while `deadline > now`. Use `std::time::Instant`, not `tokio::time::Instant`,
     so the map matches `active_tcp`; do not hold a guard across an `await`;
   - extend `abort_failed_direct_tcp` with `failed: &RwLock<HashMap<IpAddr,
     std::time::Instant>>` and `ttl: Duration` and call `remember_failed_direct` with
     `key.1.ip()` and `std::time::Instant::now()` **at the top of the handler**, before
     `wait_for_syn_ack_seq` and before the reset is built. This covers a connect error
     and the five-second timeout, and records the address even when the SYN-ACK record
     is missing and no RST is sent. Do not add a second recording call anywhere else,
     and do not touch the successful branch: a successful outer connect followed by a
     broken copy loop is still not a failure for this behaviour;
   - in the listener's failure `else`, pass `&abort_failed_direct_ips_conn` and
     `FAILED_DIRECT_MEMORY_TTL` to the handler; clone `failed_direct_ips` once before
     the `if let Some(mut listener) = tcp_listener` block and once per accepted
     connection beside the other П10 clones. Keep `active_tcp` insertion/removal, the
     success branch, its socket options, the copy loop and the `syn_ack_seqs` removal as
     they are.

2. **Use the memory in the new-connection decision, clear it and prune it** in the same
   file:
   - in `try_handle`, after the DNS handling and the TCP SYN parsing, immediately
     before `let domain = self.dns_domain_map.get(&dst_ip);`, add a check
     `protocol == 6 && failed_direct_active(&self.failed_direct_ips, &dst_ip,
     std::time::Instant::now())` that returns false. That is the tunnel branch of
     `dispatch`; it must come before `evaluate_route` and must not insert anything into
     `active_tcp`. Every non-SYN TCP packet already returned above (active flow → true,
     otherwise false), so established direct flows keep working; the explicit
     `protocol == 6` guard keeps UDP and other protocols out of the check. Do not change
     `evaluate_route`, `is_candidate_direct`, `is_direct_ip`, the DNS path or the UDP
     path;
   - in `sync_domain_routing`, inside the existing `if !config.enabled` branch and its
     nested `if !config.ip_routing_enabled` block (the whole routing configuration is
     disabled), clear `self.failed_direct_ips` next to the `direct_ips` clear, before
     the early return. Do not clear it while either routing mode stays enabled: a
     failed address is still relevant when IP routing alone can send traffic directly,
     and clearing on every `UPDATE_DOMAINS` in that mode would drop the fallback. This
     matches the shell's meaning of a disabled routing configuration (`Domains || IPs`
     is false);
   - in the existing cleanup task (30-second interval), clone `failed_direct_ips`
     before the task and, after the `active_tcp` retain, prune entries with
     `guard.retain(|_, deadline| *deadline > now)` using the same `now`. Lookup already
     ignores an expired entry, so routing is exact between ticks and the prune only
     keeps the map small;
   - no counter code: a SYN rejected by `try_handle` is never `Direct`; with workers
     `dispatch` returns `Tunnel` and `bytes_up` grows, without workers it returns
     `Dropped` and no counter grows; `direct_up` does not grow either way. Do not credit
     `direct_down` or change the П8 contract.

3. **Extend the deterministic Rust regression** in
   `scripts/vendorcore/files/rust-client/direct_relay_failure_tests.rs` (the existing
   П10 added file; do not add a second test file). Update the file's top comment to
   mention the address memory as well. Use documentation addresses only (`192.0.2.0/24`,
   `2001:db8::/32`), no live service and no real minutes:
   - update the existing calls of `abort_failed_direct_tcp` for the new arguments. In
     `failed_connect_emits_reset_with_recorded_sequence`, assert after the IPv4 and the
     IPv6 call that `failed` holds `192.0.2.10` and `2001:db8::1` respectively; in
     `missing_record_closes_without_reset`, assert that the address is recorded even
     though no RST was sent (a failure is remembered independently of the SYN-ACK
     record); in `syn_ack_record_reaches_the_failure_path`, pass
     `&relay.failed_direct_ips` and assert after the RST that the destination is
     remembered;
   - `failed_direct_memory_routes_new_syns_to_the_tunnel`: build the production
     `DirectRelay::new` with `DomainRoutingConfig { enabled: false,
     ip_routing_enabled: true, ip_is_whitelist: false, ip_rules:
     [IpRule::Cidr(192.0.2.0, 24)] }` and an empty `direct_ips`, so no preresolve runs.
     Send a SYN `10.66.67.2:54321 → 192.0.2.10:80` and assert it is accepted directly
     (the contrast: without the memory this address is direct). Then remember a
     different address, `192.0.2.11` — a separate IP so the production connect started
     for the contrast cannot race the assertions — and send a new SYN from another
     source port to `192.0.2.11:443`; assert `try_handle` returns false, so the SYNs to
     the same remembered IP but another port are tunnel-bound too. Insert an
     `active_tcp` entry for `(application, 192.0.2.11:443)` and send a non-SYN packet to
     that pair; assert `try_handle` returns true, proving an established direct flow
     stays direct. Replace the entry with `Instant::now() - Duration::from_secs(1)` and
     send a third SYN to `192.0.2.11:443`; assert it is accepted directly, so expiry
     restores direct attempts. Keep the relay's inbound receiver alive; never await the
     outer connects, which may stay pending for their five-second timeout and are
     dropped with the test runtime;
   - `failed_direct_memory_expires_with_injected_time`: with an empty map, call
     `remember_failed_direct(&map, ip, now, Duration::from_secs(300))` with an explicit
     `now`; assert `failed_direct_active` is true at `now` and just before the deadline
     and false at/after `now + ttl`; assert another IP is false. No sleep and no real
     wait;
   - `disabling_routing_clears_the_failed_address_memory`: insert an active entry, call
     `relay.sync_domain_routing` with `enabled: false, ip_routing_enabled: true` and
     assert the entry is still there (turning domain routing off alone does not disable
     direct IP routing); then call it with `enabled: false, ip_routing_enabled: false`
     and assert the map is empty. A `tokio` test, no packets, no connect;
   - a successful direct connection is not recorded: the only recording call is inside
     `abort_failed_direct_tcp`, and the handler is called only from the listener's
     failure `else`. A deterministic successful outer connect would need a live target
     or a new injection seam in `crate::bind_if::connect_tcp`, which this task does not
     introduce; the Go rule test in step 5 pins both the single call site and the
     absence of a recording call in the success branch. Do not start a connect that can
     succeed and do not assert a race on the production task.

4. **Author the rules and regenerate**:
   - `scripts/vendorcore/rules.go`: five new rule entries and the existing П10 entries
     extended:
     - extend the `DIRECT_RELAY_INBOUND_CAPACITY` rule with `FAILED_DIRECT_MEMORY_TTL`;
     - extend the `active_tcp: Arc<RwLock<HashMap…>>` field rule with the
       `failed_direct_ips` field, and the `active_tcp,` literal rule with
       `failed_direct_ips,`;
     - new rule after `let active_tcp = Arc::new(RwLock::new(HashMap::new()));` for the
       memory map creation;
     - extend the `if let Some(mut listener) = tcp_listener` rule with the
       `abort_failed_direct_ips` clone and the `let active_tcp_conn =
       active_tcp_c.clone();` rule with `abort_failed_direct_ips_conn`;
     - extend the `drop(server_stream) … else` rule with the two new handler arguments;
     - extend `synAckRecordCode` with `remember_failed_direct`, `failed_direct_active`,
       the new handler parameters and the record call at the handler entry; keep the
       existing `wait_for_syn_ack_seq` and reset code intact;
     - new rule before `let domain = self.dns_domain_map.get(&dst_ip);` for the
       `try_handle` check;
     - new rule after the `if !config.ip_routing_enabled` / `self.direct_ips.store(…)`
       block inside the `!config.enabled` branch of `sync_domain_routing` for the memory
       clear;
     - new rule after `let active_tcp_cleanup = active_tcp.clone();` for the cleanup
       clone, and new rule after the `active_tcp_cleanup` retain block for the prune.
     Every anchor must be unique with `Count: 1`. New comments are English and name
     П10. The plan expects exactly five new rule entries, so `CORE_VERSION` rules
     105→110; files stay 128 and added stays 12. Do not hand-edit `core/` or
     `CORE_VERSION`;
   - regenerate `core/` from a clean checkout of the pinned commit
     `d02da9bd9d8926969b766e197c434d830a83dbd8` (as recorded in `core/CORE_VERSION`),
     run generation twice to prove idempotence and run `-check`. `CORE_VERSION` changes
     only through generation: files 128, rules 110, added 12;
   - the diff contains only `scripts/vendorcore/rules.go`, the updated
     `scripts/vendorcore/direct_fallback_test.go`, the updated added Rust test file,
     the regenerated `core/` (the changed `direct_relay.rs`, the changed test file and
     `CORE_VERSION`), and the two documents of step 6.

5. **Update the Go rule pinning** in `scripts/vendorcore/direct_fallback_test.go`
   (model: the current П10 tests):
   - extend `directFallbackAnchors` with the five new rule anchors
     (`let active_tcp = Arc::new`, `let active_tcp_cleanup = active_tcp`,
     `let domain = self\.dns_domain_map\.get`, `if !config\.ip_routing_enabled`,
     `active_tcp_cleanup\.write`) and change the expected distinct count from 11 to 16;
   - extend `TestDirectFallbackElseRule` with `&abort_failed_direct_ips_conn,` and
     `FAILED_DIRECT_MEMORY_TTL,`, and assert that `remember_failed_direct(` does not
     appear in the generated listener snippet: only the failure handler records;
   - add `TestDirectFallbackMemoryCheckRule`: apply the `try_handle` rule to a snippet
     with `let domain = self.dns_domain_map.get(&dst_ip);` and `evaluate_route`; assert
     the check appears once before `evaluate_route`, contains `failed_direct_active`,
     `&self.failed_direct_ips` and `return false`, and fails closed on a missing or
     duplicated anchor;
   - add `TestDirectFallbackMemoryClearRule` for the `sync_domain_routing` snippet:
     apply the rule to a snippet with the `if !config.enabled` branch and its nested
     `if !config.ip_routing_enabled` / `self.direct_ips.store(…)` block; assert the
     inserted `self.failed_direct_ips.write()` clear is inside the nested block and
     fails closed on drift;
   - add `TestDirectFallbackCleanupRules` for the cleanup snippet: apply the clone and
     prune rules together, assert `failed_direct_cleanup` and
     `retain(|_, deadline| *deadline > now)` appear once, and fail closed on drift;
   - optionally apply the extended `synAckRecordCode` rule to a
     `pub fn craft_tcp_packet_into(` snippet and assert the recording call
     `remember_failed_direct(failed, key.1.ip()` appears exactly once and
     `failed_direct_active` is defined, so the helper text and the call at the handler
     entry are pinned too. Keep the П8 selection of 26 rules in `direct_stats_test.go`
     and the П9 selection in `tun_direct_test.go` untouched.

6. **Update the Russian documents** in place (do not retranslate):
   - `doc/architecture.md` §07: extend the existing single `П10` row, do not add a
     second one. After the reset part, say that the destination address is remembered
     by IP for five minutes as “напрямую не открывается” (the memory does not depend on
     the reset record: a failed connect without it still remembers the address); new
     TCP connections to it go to the tunnel and count as tunnel traffic; expiry
     restores direct attempts and disabling the routing configuration clears the
     memory; established direct flows, UDP, DNS and domain-routing decisions are
     unchanged. Keep the reset wording and the “Зачем” and “Этап” cells;
   - `doc/upstream-proposals.md` item 15: extend the existing
     `**Как сделано у нас (правка П10).**` note, do not add a second one. Replace the
     final sentence about the next part with the implemented description: the
     destination IP is remembered for five minutes even when the SYN-ACK record is
     missing and no RST is sent; new TCP connections to it use the tunnel and are
     tunnel traffic; expiry allows direct attempts again; disabling the routing
     configuration clears the memory; successful direct connections, established
     direct flows, UDP, DNS and routing decisions are unchanged; the regression is
     `direct_relay_failure_tests.rs` without a live service. Keep the historical
     “Проверено” paragraph;
   - `doc/upstream-proposals.md`, Part 2 introduction, second paragraph: say that items
     14–16 are now patched locally (П9, П10 and П8; §07 of the architecture) and that
     the other items are not. Keep the sentence about items 8–20 and the rest of the
     introduction;
   - both documents stay Russian; no personal or environment data.

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

- A direct TCP connect failure (`Ok(Err(_))` and the five-second `Err(_)` timeout, both
  in the existing failure `else`) records the destination IP in `failed_direct_ips`
  before the SYN-ACK wait and before the RST. The record is independent of the SYN-ACK
  record: a missing record closes the flow without a reset and still remembers the
  address. The key is the destination IP only, so another port or another application
  address to the same IP is affected too.
- A new TCP SYN to a remembered address is not accepted by `DirectRelay::try_handle`
  (it returns false), so `dispatch` never returns `Direct` for it: with workers it is
  tunnel traffic credited to `bytes_up`, without workers it is `Dropped` and credits
  nothing. No counter code changed. Packets of an established direct flow (a key in
  `active_tcp`) still return true and stay direct.
- Expiry is exact and injectable: `failed_direct_active` treats an entry as absent at
  and after its deadline, `try_handle` then allows the direct verdict again, and the
  30-second cleanup task prunes expired entries. Tests use an injected or shortened
  time and never sleep for minutes.
- Disabling the whole routing configuration (`sync_domain_routing` with
  `!config.enabled && !config.ip_routing_enabled`) clears the memory; turning only
  domain routing off while IP routing stays enabled keeps it, because direct attempts
  are still possible. UDP sessions, DNS handling, `evaluate_route`,
  `is_candidate_direct` and the direct verdict are otherwise unchanged; the check is
  guarded by `protocol == 6`.
- The deterministic Rust regression in the existing added file covers the record on
  failure (with and without a SYN-ACK record, IPv4 and IPv6), the tunnel decision for a
  new SYN keyed by IP, the established-flow exemption, expiry with injected time and
  clearing on disable. It uses documentation addresses and no live service. A successful
  direct connection is not recorded: the only recording helper call is in the failure
  handler, and the handler is called only from the listener's failure `else`; the Go
  rule test pins both, because a deterministic successful outer connect would need a
  live target or a new injection seam.
- The change exists only as counted vendor rules in `scripts/vendorcore/rules.go`
  (five new entries plus the extended П10 entries) and the extended added Rust test
  file. Two regenerations from the pinned `d02da9bd…` checkout are idempotent,
  `vendorcore -check` passes, and `CORE_VERSION` changes only through generation:
  128 files, 110 rules, 12 added files. `core/` is not hand-edited.
- The new Go rule test selects every П10 rule by a unique anchor and fails closed on a
  missing or duplicated anchor; it pins the `try_handle` check before `evaluate_route`,
  the clear inside the nested `!config.ip_routing_enabled` block, the cleanup prune, the
  new `else` arguments and the absence of a recording call in the success branch. The П8
  (26 rules) and П9 selections are unaffected.
- `go test ./scripts/vendorcore ./scripts/buildcore`, two generations plus `-check`,
  `buildcore`, the focused Rust `failure_tests` and `direct_stats_tests`,
  `buildcore -test`, `go build ./...`, `go vet ./...` and the full Go suite under Xvfb
  `-noreset` pass in the mandatory 4 GiB/no-swap scope. The known upstream turn flake is
  recorded if it appears, not suppressed.
- `doc/architecture.md` §07 has the single extended `П10` row (not a second row), item
  15 of `doc/upstream-proposals.md` extends its existing Russian “Как сделано у нас
  (правка П10)” note (not a second note), and the Part 2 introduction says items 14–16
  are now patched locally. Both documents are edited in Russian, are not retranslated
  and contain no personal or environment data.
- No live provider-filtering, browser/application or Windows core check is claimed. The
  Windows core build is not available on this host; the change is platform-neutral Rust
  and no Windows behaviour is claimed.

## Risks and limits

- The memory is keyed by destination IP, not by port or domain, as the proposal says.
  Other sites on the same address use the tunnel for the memory period; this is the
  same trade-off as the address-based routing decision and is not widened here.
- The failure is known only after the outer connect fails, so connections opened during
  the five-second attempt still go direct. Do not record the address on SYN: the memory
  must mean a known failure.
- Place the check after the TCP block and before `evaluate_route`; moving it before the
  non-SYN `active_tcp` branch would cut established direct flows, and applying it to
  UDP would change DNS/routing behaviour. Both are pinned by the tests.
- Record at the handler entry, not after the RST: the address is remembered even when
  the SYN-ACK record is missing and no reset is sent, and a SYN arriving during the
  bounded wait already goes to the tunnel. Do not reuse the `syn_ack_seqs` map and do
  not remove the memory entry when the RST is sent.
- The map is bounded by the failure rate and the TTL: each entry needs a failed connect
  (up to five seconds), and the cleanup task removes expired entries. Do not add a size
  cap or a second TTL constant.
- `std::time::Instant` is monotonic and matches `active_tcp`; do not mix it with
  `tokio::time::Instant`. Tests must inject the instant or use a short TTL, never wait
  for the production five minutes.
- Clearing the memory on every `UPDATE_DOMAINS` would drop the fallback on ordinary
  server rule updates; clear it only when the whole routing configuration goes off
  (`!config.enabled && !config.ip_routing_enabled`). Clearing it whenever domain
  routing alone is off would also drop it in the IP-only mode, where direct attempts
  continue.
- A failed connect that lands just after the clear can leave one entry until its TTL.
  It is harmless while routing is off (`try_handle` returns false at the top) and
  expires; do not add a config check to the failure handler for this race.
- `core/` and `CORE_VERSION` are produced only by `scripts/vendorcore`; a hand edit
  would fail `-check` on the next run. The plan fixes the split at five new rule
  entries, so the generated count must be 110; if the implementation needs a different
  split, amend the plan instead of forcing the number or hand-editing `CORE_VERSION`.
- No live browser/application or provider check is possible in this work: whether a
  provider actually filters the address stays unverified, and the Windows core build is
  unavailable on this host.
- `doc/architecture.md` and `doc/upstream-proposals.md` are Russian; edit them in
  Russian, keep one `П10` row and one note for the whole fallback, and do not add
  environment or personal data.

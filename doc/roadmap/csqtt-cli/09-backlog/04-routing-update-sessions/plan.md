---
kind: task
status: done
detailed: true
depends: [03-routing-follow-ups]
---

# Keep direct sessions across routing updates

## Task

Fix the pre-existing upstream `sync_domain_routing` behaviour that tears down
established direct connections on every `UPDATE_DOMAINS` when domain routing is off
but IP routing is on. The defect was found by the acceptance of
[`Tunnel bypass routing follow-ups`](../03-routing-follow-ups/plan.md) and recorded in
its Outcome as a known limit that needs a separate backlog task.

In `core/rust-client/direct_relay.rs`, `sync_domain_routing` (the `!config.enabled`
branch, currently around lines 519-536) clears `dns_domain_map`, `active_tcp` and
`udp_sessions` whenever domain routing is disabled, while `direct_ips` is cleared
only in the nested `if !config.ip_routing_enabled`. So in the IP-only configuration
(which the server rule `enabled=0;ip_enabled=1` produces, see
`internal/coreproc/routing.go:209-211` and `:237-242`) every rule update:

- clears `active_tcp`, after which non-SYN packets of an established direct TCP flow
  no longer return through the established-flow branch of `try_handle` and go to the
  tunnel or are dropped, so the application's connection breaks;
- clears `udp_sessions`, after which the next UDP packet creates a new socket with a
  new source port and the replies to the old one are lost, so the session breaks.

This is upstream 2.5.0 behaviour at the pinned `d02da9b`, not a consequence of
П8–П10. The П10 failed-address memory was deliberately made to clear only when the
whole routing configuration is off (`!enabled && !ip_routing_enabled`); the old
clearing is aligned with that semantics, with one deliberate split (below).

The result is a new local core patch, П11. The pin, the protocol, the dependencies
and the supported platforms do not change; the 2.5.3 migration stays unauthorized.

**Clearing semantics chosen here.** Only live state of the direct path moves into the
nested whole-configuration block:

- `active_tcp` and `udp_sessions` — live flows and sockets of the direct path; clear
  them only when both routing modes are off (`!config.enabled &&
  !config.ip_routing_enabled`);
- `direct_ips` and `failed_direct_ips` (П10) — already cleared only in that block;
  unchanged;
- `dns_domain_map` — rule state of domain routing, not a live flow. It is read only
  by `evaluate_route` when domain routing is enabled, so it stays cleared in the
  `!config.enabled` branch as today. Moving it would let associations learned while
  IP-only mode was active influence the first verdicts of a later domain-enabled rule,
  which is a routing change not needed for this defect.

The `config.enabled` path is not touched: a rule update there already never clears
these maps, so established flows survive there today. Do not change the direct-route
decisions, the direct verdict, the DNS path or the П8 counters.

## Steps

1. **Author two counted rules in `scripts/vendorcore/rules.go`.** Put both
   immediately before the existing П10 memory-clear rule (the entry whose pattern
   starts with `(?m)^( +)if !config\.ip_routing_enabled \{\n...` and inserts
   `failed_direct_ips`). Rules are applied in slice order
   (`scripts/vendorcore/apply.go:10-38`), so both П11 rules must run before that
   П10 rule; the П10 rule must still see the unchanged
   `self.direct_ips` / `.store(...)` lines of the nested block.

   - Rule 1 — remove the live-state clears from the outer branch. Pattern: exactly
     the two consecutive blocks of the upstream `!config.enabled` branch,
     `if let Ok(mut guard) = self.active_tcp.write() { guard.clear(); }` followed by
     `if let Ok(mut guard) = self.udp_sessions.write() { guard.clear(); }`; capture
     the indentation as the existing rules do; `Count: 1`; `Replace: to("")`. Do not
     match or remove `self.dns_domain_map.clear();`, `return;`, `direct_ips` or the
     nested `if`.

   - Rule 2 — insert the same two blocks inside the nested
     `if !config.ip_routing_enabled` block. Pattern: the `self.direct_ips` line, the
     `.store(Arc::new(std::collections::HashSet::new()));` line and the immediately
     following closing brace of the nested block; capture the store indentation and the
     brace indentation. Replace with the same two lines, an English П11 comment, the
     `active_tcp` and `udp_sessions` clears at the brace indentation, and the closing
     brace. `Count: 1`.

   Keep Rule 1 before Rule 2: after the insert, the same two clear blocks exist twice
   (outer and nested), and the remove rule's `Count: 1` would fail. Both rules run
   before the П10 memory-clear rule.

   The rule order and the resulting code must be exactly:

   ```rust
           if !config.enabled {
               if !config.ip_routing_enabled {
                   self.direct_ips
                       .store(Arc::new(std::collections::HashSet::new()));
                   // П10: the whole routing configuration is off, so the failed-address
                   // memory goes with the direct addresses.
                   if let Ok(mut guard) = self.failed_direct_ips.write() {
                       guard.clear();
                   }
                   // П11: domain routing off alone leaves the direct IP path on, so an
                   // update must not break established direct TCP flows or UDP sessions.
                   if let Ok(mut guard) = self.active_tcp.write() {
                       guard.clear();
                   }
                   if let Ok(mut guard) = self.udp_sessions.write() {
                       guard.clear();
                   }
               }
               self.dns_domain_map.clear();
               return;
           }
   ```

   The П10 block lands before the П11 block because its rule inserts right after
   `.store(...)`; keep it that way. Do not include the literal
   `if !config\.ip_routing_enabled` in either П11 pattern: the П10 Go test selects its
   clear rule by that anchor (`scripts/vendorcore/direct_fallback_test.go:47`), and a
   second matching pattern would break the selection.

2. **Add the deterministic Rust regression** as a new added file
   `scripts/vendorcore/files/rust-client/direct_relay_routing_tests.rs` (П11; do not
   extend the П10 `direct_relay_failure_tests.rs`). Add one module rule after the
   existing П10 module rule (the rule anchored on the generated
   `direct_relay_failure_tests` block) that inserts:

   ```rust
   #[cfg(test)]
   #[path = "direct_relay_routing_tests.rs"]
   mod routing_tests;
   ```

   The test file is a child module of `direct_relay`, so `use super::*;` reaches the
   private fields, as the П10 tests already do. Use documentation addresses only
   (`192.0.2.0/24`, `10.66.67.0/24` for the application side), no live service and no
   external traffic. Header comment: П11 regression, what it pins.

   - `ip_only_update_keeps_established_direct_state`:
     - build the production `DirectRelay::new` with
       `DomainRoutingConfig { enabled: false, ip_routing_enabled: true,
       ip_is_whitelist: false, ip_rules: Arc::new(vec![crate::sni::IpRule::Cidr(
       Ipv4Addr::new(192, 0, 2, 0), 24)]) }`, empty `direct_ips`,
       `PacketPool::new(16)` and an inbound receiver kept alive;
     - insert an `active_tcp` entry for
       `(10.66.67.2:54321, 192.0.2.10:80)`, an `udp_sessions` entry for
       `10.66.67.2:54322` holding an `Arc<tokio::net::UdpSocket>` bound to
       `127.0.0.1:0` (loopback only), `dns_domain_map` `192.0.2.10 → "example.com"`,
       `failed_direct_ips` `192.0.2.20 → now + FAILED_DIRECT_MEMORY_TTL` and
       `direct_ips` `{192.0.2.30}`;
     - call `relay.sync_domain_routing` with the same IP-only configuration;
     - assert `active_tcp` still holds the flow, `udp_sessions` still maps the
       application address to the same `Arc` (`Arc::ptr_eq`), `failed_direct_ips`
       still holds `192.0.2.20`, `direct_ips` still holds `192.0.2.30`, and
       `dns_domain_map.get(&192.0.2.10)` is `None` (domain routing is off, so the
       domain map stays with the domain mode);
     - send a non-SYN TCP packet of the flow (`flags 0x10`) through `try_handle` and
       assert `true`, proving the established flow still takes the direct path after
       the update. The non-SYN path only forwards to the egress channel; no outer
       connect starts and no packet leaves the host.

   - `full_disable_clears_learned_direct_state`: same inserts, call
     `sync_domain_routing` with `{enabled: false, ip_routing_enabled: false}`; assert
     `active_tcp`, `udp_sessions`, `failed_direct_ips` and `direct_ips` are empty and
     `dns_domain_map.get(...)` is `None`; a non-SYN packet of the same flow now
     returns `false` (both modes off).

   Do not start an outer direct connect in either test; do not assert on
   `failed_direct_ips` after a real connect race. No sleeps: the memory uses
   `std::time::Instant`.

3. **Pin the two rules in a new Go test**
   `scripts/vendorcore/routing_cleanup_test.go` (model: the П10 tests in
   `direct_fallback_test.go`):

   - select the two П11 rules by unique anchors (for the remove rule and for the
     insert rule; the insert anchor must include the store line plus the following
     closing brace, not a fragment shared with the П10 rule), assert exactly two and
     assert neither pattern contains `if !config\.ip_routing_enabled`, so the П10
     selection stays sixteen rules;
   - `TestRoutingCleanupRemoveRule`: apply the remove rule to a representative
     `sync_domain_routing` snippet; assert both clear blocks are gone and
     `self.dns_domain_map.clear();`, `return;` and the `direct_ips` store remain;
     fail closed on a missing or duplicated anchor;
   - `TestRoutingCleanupNestedRule`: apply remove then insert; assert the two blocks
     appear exactly once at the nested indentation, after the `direct_ips` store,
     before the nested closing brace and before the outer `return;`, and that
     `dns_domain_map.clear()` is still in the outer branch; fail closed on drift;
   - optionally apply the П10 memory-clear rule after the two П11 rules to the same
     snippet and assert the `failed_direct_ips` clear lands inside the nested block
     before the П11 blocks, pinning the rule order; if included, select the П10 rule
     by its existing anchor.

   Keep `direct_fallback_test.go` (16 П10 rules), `direct_stats_test.go` (26 П8 rules)
   and `tun_direct_test.go` (П9) untouched; the full Go suite runs them all.

4. **Regenerate and verify `core/`** from a clean checkout of the pinned commit
   `d02da9bd9d8926969b766e197c434d830a83dbd8`:
   - run `go run ./scripts/vendorcore -upstream <clean checkout>` twice and check the
     second tree is identical (generation is idempotent);
   - run `go run ./scripts/vendorcore -check -upstream <same checkout>`;
   - `CORE_VERSION` changes only through generation: `files = 129`, `rules = 113`,
     `added = 13`; `commit`, `version` and the `crate` line stay as they are (three
     new rules: two for the clear and one for the test module; one new added test
     file). If the implementation needs a different split, amend this plan; never
     hand-edit `core/` or `CORE_VERSION`;
   - the generated `core/rust-client/direct_relay.rs` has the block from step 1 and
     the `mod routing_tests;` insertion; the new added test file is copied as is.
     No other generated change is expected.

5. **Update the Russian documents in place** (do not retranslate):
   - `doc/architecture.md` §07: add one `П11` row after П10 (do not modify the П10
     row): the patch changes only the clearing of live direct state, established
     direct TCP flows and UDP sessions survive `UPDATE_DOMAINS` while IP routing stays
     on, and the learned state clears only when the whole routing configuration is
     off; the `dns_domain_map` stays with domain routing; this is upstream 2.5.0
     behaviour, not П8–П10. The stage cell is `09-backlog:
     04-routing-update-sessions`;
   - `doc/architecture.md` §04, the `UPDATE_DOMAINS` paragraph (the sentence about
     “забывает выученные прямые адреса и соединения”): make it precise — with domain
     routing off the core forgets learned domains; with the whole routing
     configuration off it also forgets learned direct addresses and established direct
     connections;
   - `doc/upstream-proposals.md` item 17 (the item that describes
     `sync_domain_routing` and `UPDATE_DOMAINS`): add one Russian
     `**Связанное исправление у нас (правка П11).**` note, not a second proposal:
     the pre-existing clear of `dns_domain_map`, `active_tcp` and `udp_sessions` in
     the `!enabled` branch broke established direct flows in the IP-only mode; live
     state now clears only on the whole-configuration disable like the П10 memory,
     the domain map keeps its old behaviour, direct routing decisions do not change;
     the regression is `direct_relay_routing_tests.rs`; the item's own proposal
     (rebuild `direct_ips` on `UPDATE_DOMAINS`) remains unimplemented;
   - both documents stay Russian and contain no personal or environment data.

6. **Run the checks** from the repository root. Every Go test, test binary and Rust
   run goes in the mandatory scope
   (`systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600`);
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
       --target x86_64-unknown-linux-gnu --target-dir build/cargo --bin client \
       routing_tests
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     cargo test --locked --manifest-path core/rust-client/Cargo.toml \
       --target x86_64-unknown-linux-gnu --target-dir build/cargo --bin client \
       failure_tests
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

   Generation runs twice. The known upstream integration flake at
   `turn_integration_tests.rs:291`/`:571` is recorded if it appears and never
   suppressed. Hosted checks, push and any live core connection, browser, provider or
   Windows run remain pending and unclaimed.

## Done when

- With `!config.enabled && config.ip_routing_enabled`, `UPDATE_DOMAINS` leaves
  `active_tcp` and `udp_sessions` intact: an established direct TCP flow keeps its
  `active_tcp` entry and a non-SYN packet still returns `true` from `try_handle`, and
  a direct UDP session keeps the same socket. `direct_ips` and the П10
  `failed_direct_ips` memory are untouched. `dns_domain_map` is still cleared when
  domain routing is off, as before.
- With `!config.enabled && !config.ip_routing_enabled`, `dns_domain_map`,
  `active_tcp`, `udp_sessions`, `direct_ips` and `failed_direct_ips` are all cleared,
  and a non-SYN packet returns `false`.
- The `config.enabled` path is unchanged: a rule update never clears these maps, as
  before. Direct routing decisions, the direct verdict, the DNS path and the П8
  counters are unchanged.
- The change exists only as three counted vendor rules (two for the clearing, one for
  the test module) and one added Rust test file. `core/` and `CORE_VERSION` are
  produced only by generation from the pinned `d02da9b`: two generations are
  idempotent, `vendorcore -check` passes, and `CORE_VERSION` is `files = 129`,
  `rules = 113`, `added = 13`; `commit`, `version` and `crate` are unchanged.
- The new Rust regressions in `direct_relay_routing_tests.rs` cover both modes
  deterministically on documentation addresses, without a live service or external
  traffic. The П10 `failure_tests`, the П8 `direct_stats_tests` and the full core
  suite still pass; the П10, П8 and П9 Go rule selections are unchanged.
- The new Go test `routing_cleanup_test.go` selects both П11 rules by unique anchors,
  pins the placement inside the nested `if !config.ip_routing_enabled` block, the
  unchanged `dns_domain_map.clear()` and `return;` in the outer branch, and fails
  closed on a missing or duplicated anchor.
- `go test ./scripts/vendorcore ./scripts/buildcore`, two generations plus `-check`,
  `buildcore`, the focused Rust `routing_tests`, `failure_tests` and
  `direct_stats_tests`, `buildcore -test`, `go build ./...`, `go vet ./...` and the
  full Go suite under Xvfb `-noreset` pass in the mandatory 4 GiB/no-swap scope. The
  known upstream turn flake is recorded if it appears, not suppressed.
- `doc/architecture.md` §07 has one `П11` row, §04 has the precise `UPDATE_DOMAINS`
  wording, and item 17 of `doc/upstream-proposals.md` has the single Russian П11
  note. Both documents stay Russian and contain no personal or environment data.
- No live check is claimed; live verification remains blocked. The upstream pin
  `d02da9b`, the protocol, the dependencies and the platforms are unchanged; the 2.5.3
  migration is not used; `core/` is read-only outside `scripts/vendorcore`; there is
  no Jira, and commits are English and carry no personal data.

## Risks and limits

- Rule order is part of the change: both П11 rules must run before the П10
  memory-clear rule, and the test-module rule after the П10 module rule. Moving them
  changes the generated code silently; the Go test pins the order.
- The insert rule anchors on the store line and the nested closing brace. If upstream
  changes that small block, generation must fail; do not loosen the anchor to a
  generic pattern that would overwrite an upstream change silently.
- Clearing `udp_sessions` only forgets the map entry; the session task holds its own
  `Arc` and the socket lives until the 120-second idle timeout. Do not add a socket
  shutdown here: the full-off behaviour stays as it is, only the map entry is
  cleared.
- Established direct flows survive a rule update even if the new rule would route the
  destination differently; that is the point of not breaking open flows. They end by
  their own idle timeout. Do not re-evaluate active flows during a rule change.
- Keeping `dns_domain_map.clear()` in the `!config.enabled` branch is deliberate:
  it is rule state read only when domain routing is enabled, and carrying entries
  learned in the IP-only mode into a later enabled rule would change first-connection
  verdicts. Moving it is a one-line change, but it is not needed for this defect and
  is not part of П11.
- The test binds a loopback UDP socket and forwards a non-SYN packet to the egress
  channel; no external datagram is sent. Do not use live addresses or a real service.
- `core/` is generated: no hand edits, and the `CORE_VERSION` numbers come from
  generation. If the split needs another number of rules or files, amend the plan.
- The Windows core build is unavailable on this host; the change is
  platform-neutral Rust and no Windows behaviour is claimed.

## Outcome

`UPDATE_DOMAINS` no longer tears down the live state of the direct path when domain
routing is off and IP routing is on. In `sync_domain_routing` the `active_tcp` and
`udp_sessions` clears moved from the outer `!config.enabled` branch into the nested
`if !config.ip_routing_enabled` block, after the `direct_ips` store and the П10
failed-address clear; `dns_domain_map` keeps its old clearing in the outer branch, and
the `config.enabled` path is untouched. In the IP-only configuration an established
direct TCP flow keeps its `active_tcp` entry and a non-SYN packet still returns `true`
from `try_handle`, and a direct UDP session keeps the same socket; with the whole
routing configuration off all five collections are cleared as before. Direct routing
decisions, the direct verdict, the DNS path and the П8 counters do not change.

The change is three counted vendor rules (remove, insert, test module) and one added
Rust test file. Regeneration from the pinned `d02da9b` is idempotent and
`vendorcore -check` passes; `CORE_VERSION` moved to 129 files, 113 rules and 13 added
files; `commit`, `version` and `crate` are unchanged, and no other generated file
changed.

`direct_relay_routing_tests.rs` covers both modes deterministically on documentation
addresses, with a loopback UDP socket that sends no datagram and a non-SYN packet that
is only forwarded to the egress channel: the IP-only update keeps the TCP flow, the
same `Arc` in the UDP map, the П10 memory and the learned direct address while the
domain map is cleared; the full disable clears everything and the same packet then
returns `false`. A mutation run in a copy confirmed each rule is pinned by its own
test: without the insert the full-disable test fails, without both the IP-only test
fails. `routing_cleanup_test.go` selects both П11 rules by unique anchors, rejects a
pattern carrying the П10 `if !config.ip_routing_enabled` anchor, applies remove then
insert to a representative snippet, pins the placement and the outer
`dns_domain_map.clear()`/`return;`, fails closed on missing or duplicated anchors and
pins the rule order with the П10 memory clear; the П10 sixteen-rule, П8 and П9
selections are untouched.

Architecture §07 has one `П11` row, §04 has the precise `UPDATE_DOMAINS` wording, and
item 17 of `doc/upstream-proposals.md` has the single Russian П11 note; both documents
stay Russian. Checks passed under the mandatory 4 GiB/no-swap scope: vendorcore and
buildcore Go tests, two generations plus `-check`, the core build, focused Rust
`routing_tests` (2), `failure_tests` (7) and `direct_stats_tests` (6), `buildcore
-test` (419 passed, 0 failed, 13 ignored), `go build ./...`, `go vet ./...` and the
full Go suite under Xvfb `-noreset`; the known upstream turn flake did not appear.
Review found no defects.

Limits: no live check is claimed and live verification remains blocked; the Windows
core build is unavailable here, and the change is platform-neutral Rust, so no Windows
behaviour is claimed. As before, clearing `udp_sessions` only forgets the map entry —
the session task holds its own `Arc` and the socket lives until its idle timeout — and
an established direct flow survives a rule update even if the new rule would route its
destination differently, ending by its own idle timeout. No real problem was deferred
because of cost.

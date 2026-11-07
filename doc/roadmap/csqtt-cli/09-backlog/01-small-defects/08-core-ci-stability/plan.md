---
kind: task
status: in-progress
detailed: true
---

# Core CI stability

## Task

Fix the reported Linux CI failure before continuing the remaining small defects.
The core builds successfully, but `buildcore -test` fails in
`turn::integration_tests::data_packets_preserve_channel_order_and_release_idle_pool_leases`:
the mock server receives a zero-lifetime TURN Refresh where it expects the last
ChannelData packet, then deallocation times out because the mock server has panicked.

Code inspection identifies a matching shutdown race in `core/rust-client/turn.rs`:
UDP `send()` acknowledges enqueueing, the writer notifies queue space immediately after
`pop`, and `deallocate()` treats an empty queue as a completed flush. Cleanup can then
clear the channel or set `closing` while the writer still owns the final packet. This
can discard that packet before the zero-lifetime Refresh. Confirm this mechanism with
a controlled regression, then correct the existing bounded flush; do not replace the
ordering assertion with a control-packet filter or a server acknowledgement wait.

Follow architecture decision Р2 and §07 of
[`doc/architecture.md`](../../../../../architecture.md): changes to `core/` must be
generated through `scripts/vendorcore`, not applied only to the vendored copy. Keep the
pinned upstream version, production protocol, and supported platforms. This is one
UDP shutdown correction within the [Small defects](../plan.md) stage; resume
[CLI guidance](../03-cli-guidance/plan.md) after its acceptance.

## Steps

1. Add a deterministic Rust regression in
   `scripts/vendorcore/files/rust-client/turn_outbound_tests.rs`, connected as a
   `#[cfg(test)]` child of `turn.rs` by a counted rule in
   `scripts/vendorcore/rules.go`. Add only the per-allocation test synchronization
   needed to park the actual UDP writer after it has popped the last queued packet
   and before it checks the closing/channel state. With that packet held and the
   flush budget still available, poll `deallocate()` and assert that it waits without
   setting `closing` or clearing the channel. Release the writer, verify the packet
   reaches the loopback mock before Refresh with `LIFETIME=0`, acknowledge Refresh,
   join the tasks, and check that every pool lease returns. Establish that this
   regression fails with the existing flush implementation before applying the fix.
   Use explicit gates, not scheduling sleeps, global hooks, or a live service.
2. Extend the counted rules in `scripts/vendorcore/rules.go` for the affected
   `OutboundQueue` state/initialization, `outbound_writer_loop`, and `deallocate()`;
   cover `send()` and `try_send_fast()` if completion accounting is attached to
   enqueueing. A drained writer must mean both an empty queue and no packet still
   owned by the writer. Publish any in-flight state before removing the packet, or
   account for accepted packets before publishing them to the queue, so there is no
   new untracked interval. Release completion accounting exactly once on success,
   encoding/send failure, closing, cancellation, and abort; do not leak accounting
   on a failed enqueue or a returned fast-path packet. Keep queue-space notification
   for backpressure separate from actual send completion, and return the packet lease
   before reporting completion. Register completion waits and recheck their condition
   so a notification between the check and the await is not lost. Preserve the queue
   capacity, FIFO data order, fast path, the existing `OUTBOUND_FLUSH_WAIT` of 250 ms,
   and forced cleanup after that deadline. Do not introduce a peer-delivery guarantee,
   a new public API, or changes to TURN framing, timers, authentication, TCP/TLS, or
   unrelated shutdown paths.
3. Complete the Rust coverage in the same added module: exercise queued plus
   writer-held packets, async and fast enqueueing, and queue-full ownership return;
   verify ordered sends and full pool recovery. Also hold the writer past the existing
   flush deadline and verify bounded cleanup and lease recovery, so waiting for an
   in-flight packet cannot turn shutdown into an unbounded wait. Keep the original
   `data_packets_preserve_channel_order_and_release_idle_pool_leases` test's immediate
   deallocation, all eight outbound payload comparisons, all sixteen inbound
   comparisons, both idle/final pool assertions, and Refresh authentication/lifetime
   checks. Test hooks and their state must compile out of release builds and must not
   require a platform-specific exclusion.
4. Add focused vendor-rule coverage in `scripts/vendorcore/turn_outbound_test.go`
   using the real affected rules and a small fixture of their upstream anchors. Check
   the intended substitutions and fail-closed behavior for missing or duplicated
   anchors; retain the existing rule-engine and socket-coverage tests. Keep the patch
   local to the queue lifecycle and test-module connection rather than replacing an
   entire upstream file or weakening the socket guard.
5. Generate `core/` with `go run ./scripts/vendorcore -upstream <clean-upstream>`
   from a clean checkout of the commit pinned in `core/CORE_VERSION`. Expected generated
   changes are `core/rust-client/turn.rs`,
   `core/rust-client/turn_outbound_tests.rs`, and the rule/file counts in
   `core/CORE_VERSION`; upstream commit, version, crate identity, and lockfile stay
   unchanged. Run generation again to establish idempotence, then
   `go run ./scripts/vendorcore -check -upstream <clean-upstream>`. Format the authored
   Go/Rust changes at their source; do not commit a formatter-only change made solely
   in `core/` or reformat unrelated upstream code.
6. Validate in order: the controlled regression, the original failing test, the nearby
   authenticated-flow and TCP transport integration tests, then the complete core
   build and suite. For focused runs, use Cargo's name filter with `--locked`,
   `--target x86_64-unknown-linux-gnu`, and `--target-dir build/cargo`, or the matching
   test executable produced by `buildcore`; no change to `scripts/buildcore` is needed
   to add test filtering. After fixing the mechanism, run the original test 30
   consecutive times as a declared stability check, stopping on the first failure
   rather than retrying until green. Every test invocation, including Cargo and direct
   Rust/Go test executables, uses the scope and timeout below. Run these checks from
   the repository root; repeat the exact-filter command for the declared 30-run check:

   ```sh
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     cargo test --locked --manifest-path core/rust-client/Cargo.toml \
       --target x86_64-unknown-linux-gnu --target-dir build/cargo --bin client \
       turn::integration_tests::data_packets_preserve_channel_order_and_release_idle_pool_leases -- --exact
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test -count=1 ./scripts/vendorcore ./scripts/buildcore
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go run ./scripts/buildcore
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go run ./scripts/buildcore -test
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 env CSQTT_TEST_CORE="$PWD/build/out/csqtt-core" go test -count=1 -run TestRealCore -v ./internal/coreproc ./internal/supervisor
   go build ./...
   go vet ./...
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 \
     xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test -count=1 ./...
   ```

   Review `.github/workflows/linux.yml` and `.github/workflows/windows.yml` as
   acceptance references, without changing workflows to suppress the failure. Record
   which local checks ran and which hosted checks remain pending, including the
   downstream license, real-core container, and glibc steps skipped after the original
   failure. Do not infer native Windows or hosted Linux success from local checks.
   A push or workflow rerun requires separate authorization.

## Done when

- A controlled regression fails before the production correction and passes after it:
  an empty queue with a writer-held packet does not start normal cleanup while the
  existing flush budget remains, and releasing the writer sends the data before the
  zero-lifetime Refresh. All packet leases return after the tasks finish.
- Async/fast enqueueing, full-queue ownership return, and cleanup after the flush
  deadline are covered; no unbounded wait or new data-delivery guarantee is introduced.
- The original ordering/idle-pool test retains its assertions and immediate shutdown,
  passes all 30 declared consecutive runs, and the related integration tests and full
  `buildcore` / `buildcore -test` pass. No retries-as-success, new ignores/skips, weakened
  assertions, blanket control filtering, or enlarged deadlines hide the failure.
- The vendor-rule tests pass; regeneration is idempotent and `vendorcore -check`
  passes against a clean checkout of the pinned commit. All generated changes have
  an authored source in `scripts/vendorcore`; the upstream pin, protocol, dependencies,
  and supported platforms remain unchanged.
- Focused vendor/build-script tests, real-core `coreproc`/`supervisor` tests, the full
  Go suite, `go build ./...`, and `go vet ./...` pass. Every test uses the required
  4 GiB/no-swap scope and timeout; browser tests also use Xvfb with `-noreset`.
- The outcome explains the mechanism without private log contents and distinguishes
  local evidence from pending hosted Linux/Windows and downstream CI checks. No
  external write is performed under this task's current authorization.

## Risks and limits

- The queue can be empty before its last packet has been sent. Moving only
  `space.notify_one()` or checking queue length again does not close that interval;
  the regression must control the actual writer, not a separate accounting model.
- The 250 ms flush is best-effort and does not promise remote receipt. Keep its
  expiry behavior; extending the deadline or adding a mock-server acknowledgement
  wait to the original test would conceal the identified race.
- The existing log and source support this narrow production correction, but the
  controlled regression has not yet been run. If it does not confirm the mechanism,
  or fixing it requires a wider TURN/transport redesign or a changed shutdown contract,
  stop and return the evidence to lead for replanning.
- No credentials, live server, or user interaction are needed for local regressions.
  Hosted validation and native Windows checks remain subject to available execution
  and separately authorized external actions.

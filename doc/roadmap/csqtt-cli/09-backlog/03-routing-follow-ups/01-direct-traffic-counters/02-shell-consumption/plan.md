---
kind: task
status: todo
detailed: true
depends: [01-core-counters]
---

# Shell consumption of `STATS` counters

## Task

Consume the split `STATS` counters in the Go shell: parse `direct_up`/`direct_down`,
show direct traffic separately, and use tunnel-only `bytes_down` for the supervisor's
network liveness again. This is the second task of
[`Direct-traffic counters`](../plan.md) and depends on
[`Core counters`](../01-core-counters/plan.md), which defines the event contract. Do
not edit `core/` or `scripts/vendorcore` here.

The status line should show direct traffic only when a direct counter is non-zero;
the verbose `STATS` line should include both paths. The supervisor's current
`direct`/`directRule` workaround becomes obsolete: with tunnel-only `bytes_down`,
tunnel downlink growth can clear `NETWORK_SUSPECT` even when routing bypass is active,
while growth of only direct traffic cannot.

## Steps

1. In `internal/coreproc/message.go` add `DirectUp` and `DirectDown` to `StatsEvent`
   and parse `direct_up`/`direct_down` from the event JSON. Add a case to
   `internal/coreproc/message_test.go` covering both keys and the zero values for an
   older core's event.

2. In `internal/cli/output.go` extend `statusModel` with per-attempt direct counters:
   add the current and previous-attempt fields, reset them in `moved` exactly like the
   tunnel counters, and update them in `stats`. Include direct totals in the verbose
   `STATS` line and append a compact `прямо ↑ … ↓ …` part to the status line only
   when `DirectUp` or `DirectDown` is non-zero. Update `internal/cli/output_test.go`
   and, if its event sequence is affected, `internal/cli/main_test.go`.

3. Simplify the supervisor in `internal/supervisor/state.go`: remove the `direct`
   and `directRule` fields, `RuleDirect`, and the `!m.direct && !m.directRule` guard;
   a growing `StatsEvent.BytesDown` clears a network suspicion when workers are
   present, as before routing bypass existed. In `internal/supervisor/lifecycle.go`
   remove the `machine.direct` assignment and the `update.Direct()` block that only
   set the rule flag. Update the comments that describe `bytesDown`.

4. Rewrite the direct-routing expectations in
   `internal/supervisor/state_test.go`, `internal/supervisor/lifecycle_test.go` and
   `internal/supervisor/routing_test.go`: tunnel `BytesDown` growth clears the
   suspicion, growth of only `DirectDown` does not, and `READY` still clears it.
   Keep every other degradation, restart and bind-if behavior unchanged; in
   particular, do not change `MayRouteDirect` or the bind-if requirement.

5. Record the shell-side behavior in §09 of `doc/architecture.md` (Russian): remove
   the statement that `bytes_down` counts direct replies and that accepted traffic
   cannot end `Degraded` under routing bypass; say that tunnel downlink growth clears
   the suspicion and direct traffic does not. Keep the §04/§07 changes in the core
   task.

6. Validate. Every Go test and test binary runs in the mandatory scope; window tests
   additionally use Xvfb with `-noreset`. From the repository root:

   ```sh
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go run ./scripts/buildcore
   go build ./...
   go vet ./...
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     env CSQTT_TEST_CORE="$PWD/build/out/csqtt-core" \
     go test -count=1 -run TestRealCore -v ./internal/coreproc ./internal/supervisor
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     go test -count=1 ./internal/coreproc ./internal/cli ./internal/supervisor
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 \
     xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test -count=1 ./...
   ```

   No core generation or vendor check is required here because this task does not
   change `core/`; do not edit core files or vendor rules.

## Done when

- The shell parses `direct_up` and `direct_down`; the status line shows direct traffic
  separately and the verbose line includes both paths. Go tests cover the parser and
  the display.
- The supervisor clears a network suspicion by tunnel `BytesDown` growth even when
  routing bypass is active; growth of only `DirectDown` does not clear it. The
  supervisor state, lifecycle and routing test sets pass.
- §09 of `doc/architecture.md` no longer says that `bytes_down` includes direct
  replies or that accepted traffic is ignored under routing bypass.
- `go build ./...`, `go vet ./...`, the focused coreproc/CLI/supervisor tests, the
  real-core `coreproc`/`supervisor` tests and the full Go suite with Xvfb pass under
  the mandatory 4 GiB/no-swap scope. No core file or vendor rule is edited.
- No live core connection or live routing run is claimed; hosted checks and any push
  remain pending.

## Risks and limits

- Removing the supervisor's direct guard changes when `Degraded` ends: tunnel
  downlink growth now clears the suspicion with routing bypass active. The change is
  covered by tests, but it has no live verification in this task.
- The new shell logic assumes the paired core from the previous task. A new shell
  with an old core would still receive mixed `bytes_down` and could clear a suspicion
  from direct traffic; mixed versions are not supported and the release pairing stays
  unchanged.
- The status line gains a direct part only when direct counters are non-zero; do not
  make the quiet path print an empty or zero direct section.
- The §09 text is edited in Russian because that document is Russian; do not
  retranslate the document.

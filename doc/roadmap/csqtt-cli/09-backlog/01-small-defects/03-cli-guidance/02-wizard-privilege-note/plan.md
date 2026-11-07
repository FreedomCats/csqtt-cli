---
kind: task
status: todo
---

# Wizard privilege note

## Task

Omit the wizard's rights-needed note when the process already has the required
privileges; keep it for a process that still needs elevation. The unconditional
note is in `internal/cli/wizard.go:359` (`askMode`); platform wording lives in
`internal/cli/elevated_windows.go:16` and `internal/cli/elevated_other.go:17`.
Preserve mode choices, defaults, unsupported-mode handling, and the existing
elevation flow described in §10 of the
[architecture](../../../../../../architecture.md). This is the second independent
hint in the [parent stage](../plan.md).

## Done when

- When `tun` is offered, the rights-needed note is absent for an already privileged
  process and remains present for a process lacking the required rights, with the
  existing platform-specific meaning.
- Deterministic CLI regressions cover both privilege states through the existing
  test seams, without depending on the test runner's real account. Unsupported
  `tun` and missing-guard cases retain their existing choices and explanation.
- Mode numbering, Enter defaults, persisted mode, and elevation behavior are
  unchanged. Relevant CLI tests, `go build ./...`, and `go vet ./...` pass.
- The outcome distinguishes deterministic condition tests from any native Windows
  wizard observation. A Windows cross-build proves compilation only; if native
  elevation/console behavior is not exercised, it remains explicitly unverified.

## Verification constraints

Every `go test` and test binary must use the memory-limited scope. Programmer and
reviewer must retain both templates; the second applies if a full/browser suite is run:

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./internal/cli
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test ./...
```

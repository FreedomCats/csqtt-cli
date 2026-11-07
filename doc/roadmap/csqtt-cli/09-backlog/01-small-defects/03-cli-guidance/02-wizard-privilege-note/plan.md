---
kind: task
status: in-progress
detailed: true
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

## Steps

1. In `askMode` in `internal/cli/wizard.go:333`, keep the existing first-option
   label and append a space plus `adminNote` only when `req.Admin && !isRoot()`.
   Use the existing injectable `isRoot` check from `internal/cli/connect.go:305`,
   which delegates to the platform's `isElevated` in production. Keep this rendering
   after the unsupported/missing-guard branch, and print the label without a trailing
   separator when the note is omitted. Reuse `adminNote` verbatim: Windows names
   administrator rights, and other platforms name root and `sudo`. Do not change the
   platform constants, mode-selection loop, privilege detection, or relaunch code.
   Write any new comments and test messages in English.
2. Add `TestWizardModePrivilegeNote` in `internal/cli/menu_test.go`, using serial
   table subtests for privileged/unprivileged processes and mode replies Enter,
   `1`, and `2`. In each subtest call `tunSupport(t, nil)` and
   `runAsRoot(t, privileged)` (`internal/cli/menu_test.go:37` and
   `internal/cli/menu_test.go:48`); drive the wizard with
   `newMenuEnv`, the existing synthetic `testLink`, a default profile name, and
   the mode reply, then exit the menu without connecting. Check the actual mode
   option lines: `tun` stays item 1, SOCKS5 stays item 2 with
   `datapath.DefaultSocksListen`, and the platform-selected `adminNote` appears
   exactly once only when rights are lacking. Check the omitted-note line has no
   trailing space, the saved profile's `Datapath` is `tun` for Enter/`1` and
   `socks` for `2`, and the first profile remains the default. Check both output
   streams with `noSecrets`, including the supplied link and its synthetic password.
   Do not derive expectations from the real account or call `isElevated` directly.
3. Extend `TestWizardModeWithoutGuard` and `TestWizardModeWithoutTun` in
   `internal/cli/menu_test.go:688` and `internal/cli/menu_test.go:711` to cover
   both injected privilege states. Keep `tunSupport` setup inside each serial
   subtest, then apply `withTunGuard` for the missing-guard case: `tunSupport`
   resets that seam.
   Require SOCKS5 as the only offered item, rejection of item `2`, Enter saving
   `socks`, the existing unavailability reason, and absence of `adminNote` in
   either state. Retain the missing-guard explanation and
   `csqtt-cli connect -datapath tun -allow-leak` command; elevated rights must not
   bypass this branch. Reuse the helpers' cleanup of package globals and do not
   add `t.Parallel` or a new production test seam.
4. Format the two changed Go files and run the focused and complete CLI checks
   below, then `go build ./...` and `go vet ./...`. The existing
   `internal/cli/elevate_test.go` regressions must still cover relaunch arguments,
   successful/failed elevation, detached-window exit, and prevention of repeated
   elevation in `menu -connect`; the existing menu fallback tests must still pass.
   Review the diff to keep this a rendering/test change in `wizard.go` and
   `menu_test.go`. Profile saving, password handling, connection/core behavior,
   and the separate PowerShell-help task retain their existing contracts.

## Done when

- When `tun` is supported, guarded, and requires administrative rights, its option
  has the existing platform-specific `adminNote` exactly once for an unprivileged
  process and no note or dangling separator for an already privileged process.
- Serial deterministic tests inject both privilege states through `runAsRoot` and
  `tunSupport`. In each state, the displayed numbering remains `1` for `tun` and
  `2` for SOCKS5; Enter/`1` persist `tun`, `2` persists `socks`, and default-profile
  selection is preserved. The synthetic link and secrets are absent from output.
- Unsupported `tun` and missing-guard regressions pass in both privilege states:
  only SOCKS5 is offered, item `2` is rejected, Enter persists `socks`, and the
  existing explanation remains, including `-allow-leak` guidance for missing
  protection. Neither branch prints the rights-needed note.
- The focused regressions, complete CLI suite (including existing elevation and
  fallback tests), `go build ./...`, and `go vet ./...` pass. The new and extended
  regressions do not depend on the real account, invoke real elevation, or use a
  server or network tunnel.
- The outcome distinguishes deterministic condition tests from any native Windows
  wizard observation. A Windows cross-build proves compilation only; if native
  elevation/console behavior is not exercised, it remains explicitly unverified.

## Verification constraints

Every `go test` and test binary must use the memory-limited scope. Programmer and
reviewer use these focused and package checks:

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./internal/cli -run '^(TestWizard|TestMenuConnect|TestMenuTun|TestRelaunch)' -count=1
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./internal/cli
```

Retain both general templates below. If running a full or browser suite, use the
second command with Xvfb and `-noreset`; this task does not require running both:

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./...
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test ./...
```

## Risks and user input

`isRoot` is a platform-aware seam despite its name: its production implementation
checks the current Windows process token or the effective user ID elsewhere. Reuse
that meaning and `Requirements.Admin`; do not introduce a capability probe or change
which accounts can elevate. Package-global test seams require serial subtests and
cleanup to avoid leaking one privilege state into another test.

The shared rendering condition and saved mode are accepted through deterministic
tests. Those tests do not establish native Windows token detection, UAC, console
rendering, or window lifecycle. If a native wizard observation is available, record
it separately; otherwise identify that evidence as unverified in the outcome. A
Windows cross-build cannot supply it. No native elevation experiment, credentials,
user decision, or external action is required for this scoped correction.

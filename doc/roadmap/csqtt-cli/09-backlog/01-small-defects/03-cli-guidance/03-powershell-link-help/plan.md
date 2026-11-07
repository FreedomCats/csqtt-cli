---
kind: task
status: in-progress
detailed: true
---

# PowerShell link help

## Task

Show a PowerShell-compatible file-to-stdin pipe form in the help for both
`profile add -link` and `profile set -link`, alongside the existing hidden-input
explanation. Their descriptions are in `internal/cli/profile.go:190` and
`internal/cli/profile.go:358`. Preserve the existing POSIX redirection examples,
the input path in `internal/cli/prompt.go:37`, and all profile/share-link semantics.
See §10 of the
[architecture](../../../../../../architecture.md) and the
[parent stage](../plan.md). This help correction is independent of the other hints.

## Steps

1. Append a short English PowerShell paragraph to the descriptions returned by
   `profileAddCommand` and `profileSetCommand` in `internal/cli/profile.go:185`
   and `internal/cli/profile.go:346`. Use the same file-reading form, with the
   corresponding subcommand and `app.Name`, in both descriptions. Validate these
   examples in the runtime check below:

   ```powershell
   Get-Content -LiteralPath 'link.txt' -Encoding UTF8 | csqtt-cli profile add home -link
   Get-Content -LiteralPath 'link.txt' -Encoding UTF8 | csqtt-cli profile set home -link
   ```

   Explain that `link.txt` is a placeholder for a file already containing the link;
   the link itself must not be pasted into a command. Keep the examples consistent
   with the existing assumption that the executable is in `PATH`. Use the full
   `Get-Content` name and quote the literal filename. Keep all existing description
   text, including hidden input, POSIX `< файл`, missing-field handling, token/manual
   transitions, and fields retained by `set`. Change no flag, prompt, input reader,
   link parser, or persistence code. Adding the text and passing Go tests alone do
   not establish that these examples work in PowerShell.
2. Add a focused `TestProfileLinkHelp` table in `internal/cli/root_test.go`, next to
   `TestSubcommandHelp` (`internal/cli/root_test.go:78`). Use the existing `runCLI`
   helper (`internal/cli/root_test.go:13`) to exercise each of `profile add` and
   `profile set` with `-h`, `-help`, and `--help`: six successful invocations without
   a profile, configuration, or secret-store fixture. For each command, require
   `exitOK`, empty stderr, identical stdout for all three flags, the matching
   PowerShell pipeline and `PowerShell` label, the retained command-specific POSIX
   example, and the existing explanation that the link is read with hidden input and
   missing passwords use the next stdin line. Assert stable meaningful fragments
   rather than a snapshot of the whole Russian description. Keep test names,
   comments, and new failure messages in English. Preserve the existing top-level
   `TestHelpCommandMatchesHelpFlag` (`internal/cli/root_test.go:106`); do not add
   routing for `help profile add` or `help profile set`.
3. Run the focused help checks and the complete CLI suite under the limits below,
   then `go build ./...`, `go vet ./...`, and `git diff --check`. Retain the existing
   `TestHelpCommandMatchesHelpFlag` for supported top-level commands. Existing
   `TestProfileAddFromLink`, `TestProfileAddFromLinkWithoutHashes`,
   `TestProfileAddFromPipe`, `TestProfileRefusesSecretArgs`, `TestProfileSetLink*`,
   `TestPipeStripsByteOrderMark`, and prompt regressions supply coverage of the
   unchanged terminal/stdin and import behavior; do not rewrite those contracts or
   add another input mechanism for this documentation correction.
4. Obtain an official portable PowerShell solely for this check, as authorized by
   the user. Follow Microsoft's
   [binary archive instructions](https://learn.microsoft.com/en-us/powershell/scripting/install/alternate-install-methods#install-from-binary-archives)
   for the archive format, but extract into a private temporary directory outside
   the repository rather than performing the documented system installation. Use
   the [official stable release metadata](https://api.github.com/repos/PowerShell/PowerShell/releases/latest)
   to select one non-draft, non-prerelease tag and the Linux archive matching the
   runner architecture. Fix that tag for the whole check. Download the archive and
   `hashes.sha256` from that same release under
   `https://github.com/PowerShell/PowerShell/releases/`; use HTTPS and fail on HTTP
   errors. Arrange an outer cleanup handler before downloading, so failures and
   timeout also remove the temporary area after its child processes stop.
   Detect the checksum file's encoding (the official file can use UTF-16LE with a
   BOM), decode it before parsing, and require exactly one valid SHA-256 entry for
   the selected archive filename. Compare the locally computed digest with that
   entry before extracting or executing the archive. A missing entry or mismatch
   stops the check. Make only the extracted `pwsh` launcher executable if needed;
   create no system symlink, package installation, or repository dependency. If a
   required runtime library is absent, report the actual error to the lead instead
   of installing dependencies or bypassing the check.
5. Prepare a private, reviewable harness outside tracked files and build the real
   CLI into the same temporary area. Run the portable runtime with
   `-NoLogo -NoProfile -NonInteractive -File` and the harness path, rather than a
   secret-bearing `-Command` string. Make the temporary CLI available only to this
   child process. Put one complete single-line UTF-8 synthetic share link in each
   of two fixture files: use distinct documentation-network peers and made-up
   passwords, with one made-up call hash in the first and two in the second. Create
   fixture contents programmatically without printing them or entering them in an
   interactive command. Pass only paths and non-secret options in process arguments.
   No real server, credentials, connection, browser, or core process is needed.
6. In that PowerShell process, run the two documented file-to-stdin pipelines in
   order: `add home`, then `set home` from the second fixture. Keep `Get-Content
   -LiteralPath ... -Encoding UTF8 |` and the corresponding CLI `-link` operation;
   only the executable/fixture paths and isolation arguments differ from help. Pass
   the same explicit temporary `-config` path to both CLI invocations; an optional
   non-secret `-datapath socks` on `add` can establish an unrelated setting to retain.
   Capture stdout and stderr separately and check `$LASTEXITCODE` immediately after
   each invocation. Require both exits to be zero, the first profile to exist, and
   the second import to replace its peer and hash count while retaining its
   `device_id` and unrelated settings. Run `profile show` with the same `-config`
   after each import, check its exit code and readable secret references, and verify
   the expected counts without dumping secret storage. Complete fixtures must need
   no extra password or hash input.
7. Check every captured output stream, including `profile show`, for both fixture
   links, their passwords, and every synthetic hash before displaying any diagnostic.
   Use generic failure messages without interpolating values, captured output, or
   secret-store data. Before the first portable-runtime launch, enter the required
   systemd scope and isolate its child environment: clear `DBUS_SESSION_BUS_ADDRESS`,
   `XDG_RUNTIME_DIR`, and every inherited `SUDO_*` variable. Point `XDG_CONFIG_HOME`,
   `XDG_CACHE_HOME`, and `XDG_DATA_HOME` at private subdirectories in the temporary
   area. Set `POWERSHELL_TELEMETRY_OPTOUT=1` and `POWERSHELL_UPDATECHECK=Off` for that
   child process. A temporary `-config` alone does not prevent session-keyring writes
   (`internal/config/secret.go:61`,
   `internal/secretservice/service.go:139`, `internal/secretservice/helper.go:27`).
   Keep this isolation for the harness and all CLI children, leaving the parent
   environment unchanged. Read `$PSVersionTable.PSVersion` inside the limited harness
   to establish the version actually executed. Record only the runtime
   product/version, checksum match, pass/fail results, and verification limits. After
   inspection, the cleanup handler must remove the portable runtime, archive,
   checksum, CLI binary, fixtures,
   temporary config and secret store, harness, captures, and runtime state, including
   on failure. Record cleanup success. PowerShell on Linux and native Windows
   PowerShell are separate evidence; compilation and Go stdin tests cannot replace
   either runtime observation.

## Done when

- Help for both commands shows a file-reading PowerShell pipeline that supplies the
  link through stdin. The examples use a placeholder file and `home` or a placeholder
  profile; no secret literal is put in command arguments or shell history.
- Focused help regressions cover the pipe form and hidden-input explanation for both
  commands through `-h`, `-help`, and `--help`, with identical stdout and empty stderr
  for each command's flags. Existing top-level `help` parity is preserved without
  extending subcommand help routing.
- Both documented pipe forms succeed in an official portable PowerShell run on Linux
  using an isolated temporary configuration and synthetic complete share links.
  Output does not echo the supplied secrets. The outcome identifies the PowerShell
  product/version checked; a cross-build or a Go stdin test alone is not proof that
  the example works in PowerShell. The downloaded archive matches its official release
  checksum, and the runtime and all temporary artifacts are removed after inspection.
  If acquisition, integrity, runtime startup, or execution fails, report it and leave
  this criterion unmet instead of treating Go checks as a substitute.
- Existing hidden-terminal and line-by-line stdin behavior, missing-field handling,
  and add/set profile semantics are unchanged; relevant CLI tests, `go build ./...`,
  and `go vet ./...` pass. Any unperformed native Windows/Windows PowerShell check is
  recorded as unverified rather than inferred from another platform's result.
- The implementation diff contains only the two help-description additions and their
  focused regression tests. No input, profile, link-format, help-routing, or
  configuration interface is changed by this help correction.

## Verification constraints

Every `go test` and test binary must use the memory-limited scope. Programmer and
reviewer use these focused and package checks:

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./internal/cli -run '^(TestProfileLinkHelp|TestSubcommandHelp|TestHelpCommandMatchesHelpFlag)$' -count=1
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./internal/cli
```

Retain both general templates below. If running a full or browser suite, use the
second command with Xvfb and `-noreset`; this task does not require running both:

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./...
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test ./...
```

Manual PowerShell/CLI execution, including version inspection, the harness, and every
child CLI process, must also have a 4 GiB memory limit and a 600-second time limit.
Wrap the entire PowerShell harness in the same memory-limited systemd scope and
`timeout`, so the producer and consumer of the pipeline share the limits. Enter the
scope before applying the child's private environment isolation. Use this template,
where the wrapper applies that isolation and invokes the verified portable runtime:

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 <private-isolation-wrapper>
```

The outer cleanup handler runs after that scope stops; keep all invocation paths
private. Downloading the official portable archive for this check is authorized;
system installation, additional dependencies, image pulls, hosted execution, push,
rerun, dispatch, PR, tag, release, and other external writes are not. No full,
browser, Rust, network-connection, or native Windows suite is required for this
help-only change.

## Risks and user input

The user selected the existing help-flag entry points (`-h`, `-help`, and `--help`)
without extending subcommand help dispatch. `runHelp` continues to accept one
top-level command name (`internal/cli/root.go:555`), and `lookup` continues to search
only top-level commands (`internal/cli/root.go:146`). The existing parity test
(`internal/cli/root_test.go:106`) stays in place. Adding `help profile add/set` is
outside the selected help-only change.

The user authorized obtaining an official portable PowerShell in a temporary
directory, executing the synthetic pipeline checks, and removing that runtime
afterward. The release version is a routine verification choice, fixed for one run
and reported in the outcome; it is not a project dependency or a system installation.
Verify the official checksum before execution. A missing runtime library requires
reporting to the lead, not an unapproved dependency installation or an altered
runtime setup. No further user decision is currently pending.

PowerShell on Linux establishes only the checked runtime's file-to-native-stdin
behavior. It does not establish Windows PowerShell 5.1 encoding, native Windows
process input, or Windows console behavior. Those native observations remain
explicitly unverified; a Windows cross-build cannot supply them. This task does not
start the separate Windows-only backlog nodes.

Reading a link from a file avoids placing its contents in command arguments or shell
history; it does not make the file non-secret. Use placeholder filenames in help and
only synthetic data in checks, never an inline share-link literal, `echo` example,
transcript, or failure message containing its value. Treat unexpected follow-up input
or encoding failures as evidence to investigate, not permission to change prompt,
newline, BOM, parsing, or storage semantics in this task.

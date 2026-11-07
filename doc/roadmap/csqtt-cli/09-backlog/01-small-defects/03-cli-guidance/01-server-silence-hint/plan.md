---
kind: task
status: done
detailed: true
---

# Server-silence hint

## Task

Explain that an incorrect connection password can also produce server silence. This
is a correction to the diagnostic in `internal/cli/connect.go:634` (`silenceHint`),
not a change to silence detection or authentication. Keep the existing version,
address/port, and server-availability guidance. See the [parent stage](../plan.md)
and §09 of the [architecture](../../../../../../architecture.md) for the existing
silence contract. The other two hints remain in their separate sibling tasks.

## Steps

1. Update `silenceHint` in `internal/cli/connect.go:634` to name an incorrect connection
   password as another possible cause and point to the existing
   `csqtt-cli profile set <profile> -password` command. Keep the actual profile name in
   both that command and `profile show`, the actual `csqttcli.CoreVersion`, and the
   released/development/missing CLI-version handling. Preserve the other diagnostic
   alternatives without presenting any cause as established by silence alone. Write
   new or replaced output and comments in English, per `AGENTS.md`; keep the change
   confined to this hint and its explanatory comment.
2. Strengthen `TestConnectServerSilent` in `internal/cli/connect_test.go:229` to require
   the password possibility and password-update command in stderr alongside the
   retained guidance. Adjust assertions for any replaced hint wording. Retain the
   existing `exitError`, no-reconnect, and core-stopped assertions, and check stdout
   and stderr with the existing `noSecrets` helper. Reuse the current `silent` fake
   core and short injected silence timeout; no real server or new fake-core mode is
   needed.
3. In the same test file, add a focused `TestSilenceHint` table for a released CLI
   version, `dev`, and an empty version. Verify the embedded core version, usable
   version fallback, password guidance, and both commands using the supplied
   synthetic profile name, including a name other than `home`. Check meaningful
   fragments rather than snapshotting the entire paragraph.
4. Format the changed Go files, run the focused regression and the CLI suite with the
   limits below, then run `go build ./...` and `go vet ./...`. Review the diff for
   unintended edits to connection/error handling. No supervisor, core, profile
   persistence, or input changes belong to this task.

## Done when

- The server-silence diagnostic says that a wrong connection password is possible and
  includes the existing password-update command for the selected profile. It still
  identifies the embedded core version and the version, address/port, and server
  availability alternatives.
- Focused tests cover released, development, and missing CLI versions and profile-name
  substitution in both diagnostic commands.
- The fake-core connection regression exits with an error, stops the core, does not
  reconnect, and prints the revised guidance without exposing supplied secrets.
- The focused regressions, CLI suite, `go build ./...`, and `go vet ./...` pass.

## Verification constraints

Every `go test` and test binary, including fake-core subprocesses, must run inside the
memory-limited scope. Programmer and reviewer use these focused and package checks:

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./internal/cli -run '^(TestSilenceHint|TestConnectServerSilent)$' -count=1
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./internal/cli
```

If a full or browser suite is run, the required template is:

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test ./...
```

## Risks and user input

Silence does not identify its cause. The fake core proves presentation and existing
termination behavior, not how a live server handles an incorrect password. Do not
add a password probe, retry, timeout change, or secret value to the diagnostic. No
live credentials, platform-specific runtime check, or external action is required
for this shared CLI-text change.

## Outcome

`silenceHint` now lists an incorrect connection password as a possible cause and
shows `csqtt-cli profile set <profile> -password` for the selected profile. The
replaced hint and its comment are in English and explicitly state that silence
alone does not identify the cause. Core/CLI version guidance, released/development/
missing CLI-version handling, address/port inspection, and server availability
guidance are preserved.

`TestConnectServerSilent` verifies the revised guidance, checks both output streams
with `noSecrets`, and retains the error exit, core-stop, and no-reconnect assertions.
`TestSilenceHint` covers released, development, and missing versions, including
synthetic non-`home` profiles in both diagnostic commands. Detection, timeouts,
connection handling, profile persistence, and the sibling hints are unchanged.

Focused regressions, the complete CLI suite, `go build ./...`, and `go vet ./...`
passed. Independent review repeated those checks and also passed the complete Go
suite. All Go tests used the required memory limits; the full suite also used Xvfb.
Formatting and `git diff --check` passed. Review found no defects, required no
revisions, and identified no incidental old problems or cost-based deferrals.

No live server or credentials were used. Fake-core tests establish presentation and
termination behavior, not the cause of silence from a live server. Rust, real-core,
and container checks were not repeated for this CLI-only change.

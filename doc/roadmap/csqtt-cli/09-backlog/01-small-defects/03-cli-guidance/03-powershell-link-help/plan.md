---
kind: task
status: todo
---

# PowerShell link help

## Task

Show a PowerShell-compatible file-to-stdin pipe form in the help for both
`profile add -link` and `profile set -link`, alongside the existing hidden-input
explanation. Their descriptions are in `internal/cli/profile.go:190` and
`internal/cli/profile.go:358`. Keep the existing input path in
`internal/cli/prompt.go:37` and all profile/share-link semantics. See §10 of the
[architecture](../../../../../../architecture.md) and the
[parent stage](../plan.md). This help correction is independent of the other hints.

## Done when

- Help for both commands shows a file-reading PowerShell pipeline that supplies the
  link through stdin. The examples use a placeholder file and `home` or a placeholder
  profile; no secret literal is put in command arguments or shell history.
- Focused help regressions cover the pipe form and hidden-input explanation for both
  commands, including the equivalent `help` and `-h` entry points.
- Both documented pipe forms succeed in a real PowerShell run using an isolated
  temporary configuration and synthetic complete share links. Output does not echo
  the supplied secrets. The outcome identifies which PowerShell runtime was checked;
  a cross-build or a Go stdin test alone is not proof that the example works in
  PowerShell. If no suitable runtime is available, this acceptance remains pending
  for the lead to arrange, without an unapproved external action.
- Existing hidden-terminal and line-by-line stdin behavior, missing-field handling,
  and add/set profile semantics are unchanged; relevant CLI tests, `go build ./...`,
  and `go vet ./...` pass. Any unperformed native Windows/Windows PowerShell check is
  recorded as unverified rather than inferred from another platform's result.

## Verification constraints

Every `go test` and test binary must use the memory-limited scope. Programmer and
reviewer must retain both templates; the second applies if a full/browser suite is run:

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./internal/cli
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test ./...
```

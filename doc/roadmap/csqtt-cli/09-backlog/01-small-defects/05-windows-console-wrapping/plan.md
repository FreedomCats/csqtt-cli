---
kind: task
status: todo
---

# Windows console word wrapping

## Task

Ensure long user-facing lines are wrapped at word boundaries in the Windows console
instead of breaking words mid-line. Inspect the shared CLI output paths, starting with
`internal/cli/output.go`, and keep captured or redirected output behavior unchanged.

## Done when

- Long user-facing text in the affected Windows console output is wrapped between words;
  no text is lost, duplicated, or split inside a word.
- Regression tests cover wrapping at a narrow width, including non-ASCII text, and verify
  that redirected output retains its existing line-oriented form.
- Windows builds and relevant CLI tests pass.

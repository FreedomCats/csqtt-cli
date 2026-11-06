---
kind: task
status: todo
---

# 32-bit Windows vet

## Task

Fix the `GOARCH=386 go vet` failure in `monitorDPIAt` in
`internal/captcha/dpi_windows.go` without changing DPI selection behavior on supported
Windows architectures.

## Done when

- `GOOS=windows GOARCH=386 go vet ./internal/captcha` passes.
- Existing DPI tests pass on a runnable Windows target, and Windows amd64 builds remain
  successful.

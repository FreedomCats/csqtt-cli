---
kind: stage
status: done
base: 5c7585f
---

# Foundation

Repository assembles, the core is vendored and built, transferred packages pass
their tests. No network or server needed yet.

Goal: a Go module that builds on both systems, `scripts/vendorcore` that lays out
`core/` from an upstream snapshot and verifies every deviation as a rule, and
`scripts/buildcore` that builds `csqtt-core` for Windows and Linux.

## Done when

- `go build ./...` and `go vet ./...` pass; `go test ./...` passes on Windows and
  Linux, `-race` too.
- `go run ./scripts/vendorcore` lays out the core from a fresh clone, `-check`
  passes, and a manual edit of any file under `core/` fails it.
- `go run ./scripts/buildcore` builds `csqtt-core` for Windows and Linux `amd64`,
  and `cargo test` without network tests passes.
- Both CI workflows are green on `main`.

## Outcome

Tasks F1–F5 of `doc/plan.md` §Э0. The Go module, the rule engine of `vendorcore`
with its socket watcher, `buildcore` with toolchain checks, the packages transferred
from qwdtt-cli without any qWDTT-derived code, and two CI workflows (Windows, Linux).
Detailed record: `doc/plan.md` §Э0, journal `doc/local/plan-e0.md`.

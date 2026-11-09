---
kind: task
status: todo
depends: [01-dependency-and-licence]
---

# Compare platform and build constraints

## Task

Produce one source-based platform feasibility matrix comparing exact baseline B and
candidate C from the [parent stage](../plan.md), using the accepted
[dependency inventory](../01-dependency-and-licence/plan.md). Cover the current
release targets only: `x86_64-pc-windows-msvc`, `x86_64-unknown-linux-gnu` and
`aarch64-unknown-linux-gnu`. Compare declared Rust/C/native tool requirements,
target/feature constraints and runtime/packaging requirements with architecture
§§09a/09b, `scripts/internal/rusttarget/rusttarget.go`, `scripts/buildcore/main.go`
and the existing Linux/Windows/release workflow metadata. Reuse stage 11's accepted
2.5.0 release evidence, not as candidate validation.

Focus new research on changed dependency/build metadata, including the new vendored
LuaJIT and bundled SQLite declarations, and any resulting MSVC, static-CRT, Linux
cross-compilation, glibc or runtime-library uncertainty. Distinguish unconditional
build inputs from runtime opt-in behavior. This is metadata/source research by a
coordinator with an independent non-coding reviewer, under the parent's limits;
no platform expansion, tool installation, dependency download or execution is part
of the result.

## Done when

- Each existing release target has a B/C requirements-and-gaps row covering declared
  toolchain/native inputs, target conditions, build and runtime constraints, with
  exact-version/ref evidence and current local build/release consumer references.
- Existing MSVC/static-CRT and Linux GNU/glibc/cross-build requirements are separated
  from candidate changes. Android/iOS/WSL build metadata is not treated as desktop
  support; runtime-disabled optional behavior is not assumed to remove build inputs.
- Known blockers, metadata-supported feasibility and unknowns are distinguished.
  Source support claims and past 2.5.0 CI never stand in for a successful C build,
  binary inspection, startup, native Windows check or deployed behavior.
- An independently reviewed ignored record at `doc/local/core-253-platform-review.md`
  and a sanitized Outcome hand bounded platform work and verification gaps to
  `lead`. The dependency/licence inventory and accepted patch assessments are reused.
- The parent's read-only boundary and future-only test constraints are preserved;
  no build, test, resolver/vendor/notice command, binary execution, download,
  platform decision or external write is performed.

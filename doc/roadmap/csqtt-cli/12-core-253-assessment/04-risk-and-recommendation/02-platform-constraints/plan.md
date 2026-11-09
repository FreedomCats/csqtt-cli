---
kind: task
status: done
detailed: true
base: dbc2805885d326a8a19f0ada71a00911005f216a
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

## Steps

1. Fix the comparison boundary at baseline B `d02da9bd9d8926969b766e197c434d830a83dbd8`, candidate C `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`, and the separately patched 2.5.0 product in `core/CORE_VERSION`. Reuse the accepted dependency inventory, especially `doc/local/core-253-dependency-review.md:374-382`; do not repeat package/licence reconciliation or treat the unpatched source refs as the released product.
2. Establish the existing product contract from `doc/architecture.md:46`, `:1599-1671`, `scripts/internal/rusttarget/rusttarget.go:11-23`, and `scripts/buildcore/main.go:11-35,98-170,244-287,291-338`. Record each release target's Rust target and minimum version, native C toolchain, supported host/target pairing, linker/sysroot inputs, CRT or glibc constraints, and which properties are enforced by the current build wrapper. Cross-check the declared release and core checks in `.github/workflows/linux.yml`, `.github/workflows/windows.yml`, `.github/workflows/release.yml`, and the package/runtime contract in `scripts/release/main.go` and `README.md`. These are current-product requirements, not evidence about C.
3. For the exact B/C client manifests and lockfiles, inspect only the changed build-relevant declarations and consume the accepted target/host dependency map. Focus on Gooby's unconditional normal edge and its declared `mlua` `luajit,vendored,send` and `rusqlite` `bundled` features; trace only the relevant `mlua-sys`, LuaJIT/SQLite native-source, `cc`, `pkg-config`, `vcpkg`, and host-helper metadata. Keep target-conditional and opt-in runtime behavior distinct: runtime opt-in does not make the dependency/build inputs optional. Do not redo the package/licence or notice audit and do not inspect the security sibling's runtime behavior.
4. Build a B/C row for each of `x86_64-pc-windows-msvc`, `x86_64-unknown-linux-gnu`, and `aarch64-unknown-linux-gnu`. Compare declared Rust/MSRV and native compilation constraints with the existing target contract. Explicitly separate Windows MSVC/C++ availability from the existing `+crt-static` and `/MT` contract; Linux amd64 native GNU compilation from arm64 cross-GCC, target sysroot/linker and host build helpers; and both Linux targets' glibc/runtime packaging limits from the current CI ceiling. Assess whether available exact-version native metadata establishes compiler, assembler, CMake, cross-compilation, static-link/runtime, or target-conditional requirements. Do not infer these from a wrapper crate's feature name, lock membership, a workflow's target declaration, or past CI. Use only already available local/primary metadata; do not acquire crate archives or dependency sources. If exact-version native requirements or shipped library composition cannot be established within this limit, mark them unknown and cite the missing evidence.
5. In every row, classify each conclusion as an existing baseline/product fact, candidate metadata fact, metadata-supported feasibility, known blocker, or unknown. State the evidence and inference separately, including whether a requirement concerns the build host or output target. Do not claim C supports any target, a native input is actually linked/shipped, CRT or system-library linkage is compatible, or a runtime dependency is absent without direct evidence. Record what later authorized target-specific verification would still need to establish without running or scheduling it here.
6. Record the compact three-target matrix in ignored `doc/local/core-253-platform-review.md`, with exact B/C refs, local `file:line` references, primary exact-version metadata references where used, confidence, unknowns and the bounded handoff to `lead`. An independent non-coding reviewer must reconstruct the target rows and check the compiler/host-target classifications, candidate claims, and limits against those references. Relay the future-only Go test requirements (4 GiB memory limit, no swap, 600-second timeout; Xvfb `-noreset` for window tests) as constraints, not permission or planned work. Correct any findings in the ignored record, then write a sanitized Outcome in this node. Preserve all accepted evidence and keep security/privacy research to its sibling task.

## Done when

- Each of the three named release targets has a reviewed B/C requirements-and-gaps
  row covering Rust/MSRV, host and target toolchains, declared native inputs, target
  conditions, cross-build/runtime constraints, and packaging references. Claims cite
  exact-version/ref metadata and current local build/release consumers; unavailable
  evidence is an explicit unknown.
- Existing MSVC/C++ and static-CRT requirements, Linux GNU/glibc ceiling, and Linux
  arm64 cross-toolchain/sysroot requirements are distinguished from candidate deltas.
  Runtime opt-in is not treated as removing the unconditional dependency/build graph;
  Android/iOS/WSL metadata is not treated as desktop support.
- Known blockers, metadata-supported feasibility and unknowns are distinguished.
  Source support claims and past 2.5.0 CI never stand in for a successful C build,
  binary inspection, startup, native Windows check or deployed behavior.
- An independent non-coding review reconstructed the matrix and accepted the ignored
  record at `doc/local/core-253-platform-review.md`; the sanitized Outcome hands
  bounded platform conclusions and verification gaps to `lead`. The accepted
  dependency/licence inventory and patch assessments are reused without repetition.
- The parent's read-only boundary and future-only test constraints are preserved;
  no build, test, resolver/vendor/notice command, binary execution, download,
  platform decision or external write is performed.

## Outcome

The ignored source-based matrix at `doc/local/core-253-platform-review.md` compares
all three current release targets against exact baseline B and candidate C metadata
and the separately patched 2.5.0 product. Candidate C adds unconditional vendored
LuaJIT and bundled SQLite build inputs. Existing target mappings and build workflows
provide bounded feasibility context, not candidate support evidence. No candidate
blocker was demonstrated from the permitted metadata; successful native compilation,
arm64 cross-build compatibility, CRT/runtime linkage, GLIBC ceiling, and final
packaged native composition remain unknown pending separately authorized target
verification.

Independent non-coding review reconstructed the three rows. It corrected the arm64
`AR_<target>` classification to cross-ar, narrowed the README citation, and required
the current Rust minimum and wrapper preflight limits to be explicit; all findings
were incorporated. The assessment reused the accepted dependency/licence inventory
and did not repeat it. No build, Cargo command, test, source acquisition, tool
installation, vendor/notice action, binary inspection, migration, platform decision,
or external write was performed. Any later Go tests retain the 4 GiB, no-swap,
600-second limit; window tests additionally require Xvfb with `-noreset`.

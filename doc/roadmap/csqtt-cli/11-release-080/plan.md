---
kind: stage
status: done
base: 234c430
---

# Release v0.8.0 on the current core

## Goal

Verify and publish the accumulated changes since v0.7.0 as v0.8.0, retaining the
existing vendored CSQTT 2.5.0 core, local patches, supported platforms, and licensing.
This stage takes priority over the remaining backlog. The user requested release
publication, including the necessary branch push, lightweight tag, release workflow,
and publication of the verified assets.

Release notes should describe user-visible fixes, server compatibility, and actual
verification limits. Unrelated Windows and live-verification backlog is not a
prerequisite for this release. No new dependencies, platforms, or core version are
introduced.

## Children

1. [Release readiness and notes](01-release-readiness/plan.md) — reconcile the accepted
   changes with the release scope, review publication safety, and prepare the English
   release-note draft and verification checklist.
2. [Build, verify, and publish v0.8.0](02-publish-release/plan.md) — obtain green CI on
   the final main commit, build the tagged draft, verify the downloaded assets, and
   publish the release with its actual verification limits.

The second task depends on the first. Candidate review is independently acceptable;
building and publishing remain one release operation, with asset acceptance before
public publication. `lead` owns notes, commits, pushes, tagging, and publication.

## Release process and verification boundaries

- Use the existing tag-push route in `.github/workflows/release.yml`. It calls the
  complete Linux and Windows workflows and separately builds the three release cores;
  packaging waits for all of them. Core-build jobs can run alongside the tests. A
  manual dispatch or a `release/**` branch build produces artifacts without creating
  a release draft.
- The hosted workflows cover Go build/vet/tests, the Linux race and network-namespace
  checks, pinned-vendor verification, core builds/tests, real-core integration checks,
  and the existing license and glibc checks. Release-core jobs additionally check the
  Windows PE/static-CRT requirements and Wintun signature, both Linux cores' glibc
  ceiling, and offline core startup. Windows and Linux amd64 core probes are native;
  the Linux arm64 probe uses QEMU.
- Packaging uses `scripts/release`, `scripts/distcheck`, `scripts/sha256sums`, and
  `scripts/releasenotes`, then uploads three archives and `SHA256SUMS.txt` to a draft.
  The tag supplies `-version v0.8.0`; `scripts/release` injects `main.version` and
  `main.commit` through `-ldflags`. Core identity comes from `core/CORE_VERSION` and
  the pinned build inputs. A version-constant edit is not part of this release.
- The packaging job runs on Linux: it probes the staged Linux amd64 CLI/core, and
  `distcheck` checks every archive's required contents and Linux executable modes.
  It does not execute the final Windows ZIP or the archived Linux arm64 CLI. Native
  Windows release-core checks use `release -nozip`, not the final ZIP. Keep this
  distinction in the verification report.
- Local acceptance uses the downloaded draft assets: verify the original checksum
  manifest, rerun `distcheck`, and extract and smoke-test the Linux archives. Run
  Linux amd64 natively and Linux arm64 through QEMU with the matching runtime. Check
  `csqtt-cli version` for v0.8.0, the tagged commit without `-dirty`, and core 2.5.0;
  invoke the packaged core with `--csqtt-cli-release-probe`, expecting exit 2 and
  `Usage:` before any connection attempt. The core probe establishes startup, not a
  separate core-version query; source identity also needs the tagged pin and artifact
  provenance.
- Inspect the Windows ZIP and CLI Go build information, and match its packaged core
  to the release workflow's native-checked core artifact. Record whether the final
  Windows archive was executed. A new native Windows session is not a prerequisite;
  hosted native checks, archive inspection, and local/emulated execution establish
  different things. No real server, credentials, VK interaction, or tunnel setup is
  required. Repeating the previously accepted full local suites is unnecessary for
  unchanged product code; the final hosted workflows remain required.
- Every locally launched `go test`, Cargo test, or executable test binary must use
  the 4 GiB/no-swap systemd scope and `timeout 600`. Browser/window tests and the
  full Go suite also require Xvfb with `-noreset`. The standard commands are:

  ```sh
  systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test <packages>
  systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 \
    xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test -count=1 ./...
  ```

## Risks and limits

- Finalize pre-release documentation and its commits before selecting the release
  SHA. Earlier green runs do not certify a later commit, even if the difference is
  documentation only. Main and the lightweight tag must identify the exact green
  candidate at tagging; subsequent outcome-only commits do not move the tag.
- Describe profile-save rollback within its accepted scope. Cross-process write and
  rollback races, the Secret Service snapshot-to-`Put` race, and the pre-existing
  partial-`Put` failure remain; this release does not provide a serializable storage
  transaction or repair those limitations.
- If the known Rust authenticated-flow timeout recurs, preserve the first failure
  and any diagnostic repeat as separate results. A passing retry does not establish
  a new fix. Diagnose any actual release blocker and return it to `lead` before
  publication; do not suppress tests or broaden the release into unrelated repairs.
- Both hosted core workflows fetch the upstream commit pinned in
  `core/CORE_VERSION` before `vendorcore -check`. If that source becomes unavailable,
  report the concrete build blocker while retaining the pin; switching to the new
  fork is not an authorized release workaround.
- The existing user authorization covers the necessary main push, lightweight
  v0.8.0 tag, normal workflow execution, and publication of verified assets. Core
  migration, dependency/license/platform changes, and unrelated external writes are
  outside it. Deferred Windows-only work and the dropped W6 measurement remain
  outside this release. Stage 12 starts after publication and is not detailed here.
- Keep execution logs, run identifiers, machine details, and work timestamps in the
  ignored journal. Public notes and outcomes contain only sanitized conclusions and
  verification limits, following `AGENTS.md`.

## Done when

- The release candidate is reviewed against the changes since v0.7.0, with no
  unresolved release-blocking regression, privacy, licensing, or packaging issue.
- Linux and Windows CI pass on the exact commit used for the lightweight v0.8.0
  tag, and the release workflow succeeds for that tag.
- The existing Windows amd64, Linux amd64, and Linux arm64 archives and
  SHA256SUMS.txt are verified. Archive contents pass the existing distribution
  checks; executable smoke checks establish v0.8.0 and core 2.5.0 without using real
  credentials or a live connection. Native and emulated checks are distinguished.
- The public v0.8.0 release contains the verified assets and accurate release notes.
- The roadmap records the release result and any remaining verification limits;
  local journals, secrets, environment details, and work timestamps are not published.

## Outcome

Published v0.8.0 with the current patched CSQTT 2.5.0 core. Both normal CI workflows
and the tag-push release workflow passed on the exact release commit. All three
platform archives and `SHA256SUMS.txt` were downloaded, checksum-verified, accepted
by `distcheck`, inspected, and matched to same-run core artifacts. Linux amd64
CLI/core and QEMU ARM64 CLI smokes passed locally. A local ARM64 core smoke was not
run because a matching runtime was unavailable; hosted ARM64 QEMU/glibc evidence
passed. The Windows ZIP was inspected but not run locally; hosted native Windows
checks passed. No live server was used. The public release notes record the scoped
profile-save guarantees, remaining platform limits, and licensing. No migration to
csqttr 2.5.3 occurred; the separate assessment is next.

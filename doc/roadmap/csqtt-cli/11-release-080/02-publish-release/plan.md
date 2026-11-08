---
kind: task
status: done
detailed: true
depends: [01-release-readiness]
---

# Build, verify, and publish v0.8.0

## Task

Release the candidate accepted by [release readiness](../01-release-readiness/plan.md)
through the existing `.github/workflows/release.yml` tag-push route. After `lead`
commits the pre-release documentation, obtain complete Linux and Windows CI success
on the final main SHA, tag that exact commit with a lightweight v0.8.0 tag, verify
the downloaded draft assets, and publish the ordinary public release. Apply the
accepted English notes with actual verification results and record the outcome.

Use the [parent verification contract](../plan.md), the unchanged packaging scripts,
the accepted readiness outcome and English draft in `doc/local/release-v0.8.0.md`,
and architecture decision Р2 and §§09б/11 in
[`doc/architecture.md`](../../../../architecture.md). Historical I3b/I4 and release
outcomes remain in [`doc/plan.md`](../../../../plan.md), which is frozen.

`coordinator` and `reviewer` perform bounded technical review and verification.
They may download/extract artifacts and keep results in ignored scratch space, but
do not change tracked product files, documentation, the index, or git history.
`lead` owns documentation, commits, pushes, tagging, and publication. The handoffs
below request those already authorized actions from `lead`; they do not request
new user permission. No code, dependency, workflow, or version-constant change is
required by this task.

## Steps

1. **Review the release operation and prerequisites.** Read the readiness outcome,
   the prepared notes, the parent verification boundaries, and the three workflows.
   Use `git status --short`, `git log --oneline v0.7.0..HEAD`,
   `git diff --stat v0.7.0..HEAD`, and the pending documentation diff to establish the
   candidate. Have `reviewer` assess the current release range for integration,
   version/pin, packaging, licensing, and publication regressions, using the accepted
   implementation outcomes as evidence. Inspect relevant code only where that
   release review exposes a discrepancy; do not restart each accepted implementation
   review or repeat full local suites. Confirm that release tooling, dependency and
   license files, supported archives, and the upstream/core/crate pins retain the
   readiness assumptions; `core/CORE_VERSION` count changes belong to the accepted
   local patch, not a core upgrade. Report any genuine blocker to `lead` without
   implementing a repair in this verification task.

   Check available `gh`, Go, checksum/archive/ELF tools, and `qemu-aarch64` before
   promising local checks. QEMU alone does not provide the ARM64 dynamic loader or
   libraries. Look for a coherent matching runtime in existing ignored artifacts
   and `build/out`; do not assume the workflow's `/usr/aarch64-linux-gnu` sysroot
   exists locally. The `core-*` workflow artifacts contain executables, not a
   sysroot. The runtime decision and missing prerequisites must be reported as
   described in step 7; a system package installation is not a release gate.

2. **Handoff to `lead`: finish documentation, commit, and push main.** Return the
   reviewed revision, technical review result, pending document paths, and concrete
   next action. `lead` commits all intended pre-release documentation, including
   roadmap state, following `AGENTS.md`: review `git diff --cached`, use only
   `FreedomCats <noreply@freedomcats.invalid>` for both identities and today's
   midnight UTC for both commit dates, then verify the new metadata. Keep execution
   progress in ignored records after the candidate is frozen. If commits were added
   after the technical review, review only that delta before continuing.

   Commands below run from the repository root. Set these variables after the final
   documentation commit; run identifiers, absolute scratch paths, and raw outputs
   belong in the ignored journal, not published roadmap text:

   ```sh
   release_repo=FreedomCats/csqtt-cli
   release_tag=v0.8.0
   release_url=https://github.com/FreedomCats/csqtt-cli.git
   release_sha="$(git rev-parse HEAD)"
   release_short="$(git rev-parse --short "$release_sha")"
   git branch --show-current
   git status --porcelain
   git log -1 --format='%H %an <%ae> %aI | %cn <%ce> %cI'
   ```

   Require a clean candidate on `main`, correct metadata on all newly added commits,
   and the reviewed documentation in that SHA. `lead` performs the necessary push:

   ```sh
   git -c credential.helper= -c 'credential.helper=!gh auth git-credential' \
     push "$release_url" main:main
   ```

   Resume technical verification with that exact SHA. Preserve these values and the
   later run/scratch variables in the local handoff; a new session must restore the
   pinned candidate rather than silently reset it to a later HEAD. An older green
   implementation commit does not certify the final documentation commit.

3. **Verify exact-SHA main CI.** Locate the normal push runs, then set `linux_run`
   and `windows_run` to their identifiers in the local handoff:

   ```sh
   gh run list --repo "$release_repo" --workflow linux.yml --branch main \
     --commit "$release_sha" --event push \
     --json databaseId,headSha,status,conclusion,event,workflowName
   gh run list --repo "$release_repo" --workflow windows.yml --branch main \
     --commit "$release_sha" --event push \
     --json databaseId,headSha,status,conclusion,event,workflowName
   gh run view "$linux_run" --repo "$release_repo" \
     --json headSha,status,conclusion,event,attempt,jobs
   gh run view "$windows_run" --repo "$release_repo" \
     --json headSha,status,conclusion,event,attempt,jobs
   ```

   Both must be completed successfully on `release_sha`, with the full expected
   jobs: Linux build/vet/race, netns, vendor/core tests and downstream checks;
   Windows native build/vet/tests and vendor/core/real-core checks. Inspect targeted
   logs if needed to distinguish a skipped check from completed coverage. Pending
   runs mean verification is pending, not failed or passed. For a failure, use
   `gh run view <run-id> --repo "$release_repo" --log-failed`, preserve the first
   result, and return the diagnosis and any needed workflow action to `lead`.
   Record known authenticated-flow diagnostic repeats separately; a green retry
   does not establish a new fix. Do not suppress tests or change the upstream pin
   if its source fetch fails.

4. **Handoff to `lead`: tag the green candidate.** Before tagging, recheck the clean
   main revision, both green run SHAs, and remote refs:

   ```sh
   git branch --show-current
   git status --porcelain
   git rev-parse HEAD main
   git tag --list "$release_tag"
   git ls-remote "$release_url" refs/heads/main \
     "refs/tags/$release_tag" "refs/tags/$release_tag^{}"
   ```

   Remote main must equal `release_sha`. If the tag is absent, `lead` creates and
   pushes the lightweight tag, without `-a`, `-s`, `-m`, or force:

   ```sh
   git tag "$release_tag" "$release_sha"
   git -c credential.helper= -c 'credential.helper=!gh auth git-credential' \
     push "$release_url" "refs/tags/$release_tag:refs/tags/$release_tag"
   ```

   Verify `git cat-file -t "refs/tags/$release_tag"` returns `commit`, and local and
   remote tag values equal `release_sha`. If a legitimate tag/run already exists
   at this same SHA, verify and resume it; report a conflicting tag or unexpected
   published release to `lead` instead of replacing anything. Further pre-release
   tracked changes require a newly reviewed green candidate before tagging.

5. **Verify the tag pipeline and draft.** Identify `release_run` from the push run
   for this SHA and tag, not a manual dispatch or a `release/**` branch build:

   ```sh
   gh run list --repo "$release_repo" --workflow release.yml \
     --commit "$release_sha" --event push \
     --json databaseId,headBranch,headSha,status,conclusion,event
   gh run view "$release_run" --repo "$release_repo" \
     --json headBranch,headSha,status,conclusion,event,attempt,jobs
   gh release view "$release_tag" --repo "$release_repo" \
     --json tagName,targetCommitish,isDraft,isPrerelease,body,assets,url
   ```

   Require successful reusable Linux/Windows workflows, `core-windows`, both
   `core-linux` matrix jobs, `build`, and `release`. These cover the core probes,
   PE/static-CRT and Wintun checks, Linux glibc ceiling, packaging, distribution and
   license checks, checksum generation, and draft upload. The draft must identify
   v0.8.0 and remain unpublished. Its `targetCommitish` label alone does not prove
   tag identity; use the git refs and run SHA. Handle failures as in step 3; do not
   create a substitute release manually when a dispatch produced only artifacts.

6. **Download and inspect the actual draft assets.** Use a fresh ignored absolute
   scratch directory for this run. Download by explicit tag/run identifiers:

   ```sh
   verify_dir="$(git rev-parse --show-toplevel)/build/release-verify/$release_tag-$release_run"
   gh release download "$release_tag" --repo "$release_repo" --dir "$verify_dir/draft"
   gh run download "$release_run" --repo "$release_repo" \
     --name dist --dir "$verify_dir/workflow-dist"
   gh run download "$release_run" --repo "$release_repo" \
     --name core-windows-amd64 --name core-linux-amd64 --name core-linux-arm64 \
     --dir "$verify_dir/cores"
   ```

   The single `dist` artifact extracts directly into its destination; multiple core
   artifacts extract into their named subdirectories. Require exactly the three
   v0.8.0 platform archives and `SHA256SUMS.txt` as uploaded release assets. Check
   that the manifest names exactly those three archives, compare it with the same
   run's `dist` manifest, and verify every downloaded archive without regenerating
   the manifest or ignoring missing files:

   ```sh
   cmp "$verify_dir/draft/SHA256SUMS.txt" "$verify_dir/workflow-dist/SHA256SUMS.txt"
   (cd "$verify_dir/draft" && sha256sum -c SHA256SUMS.txt)
   go run ./scripts/distcheck -dir "$verify_dir/draft"
   ```

   Inspect archive listings against `scripts/internal/dist/dist.go`, including
   licenses/notices, README, example config, Wintun files, and Linux executable
   modes; investigate unexpected entries rather than relying only on required-file
   presence. Extract the verified archives into a separate scratch subdirectory:

   ```sh
   unpack_dir="$verify_dir/unpacked"
   mkdir -p "$unpack_dir"
   tar -xzf "$verify_dir/draft/csqtt-cli_v0.8.0_linux_amd64.tar.gz" -C "$unpack_dir"
   tar -xzf "$verify_dir/draft/csqtt-cli_v0.8.0_linux_arm64.tar.gz" -C "$unpack_dir"
   unzip -q "$verify_dir/draft/csqtt-cli_v0.8.0_windows_amd64.zip" -d "$unpack_dir"
   amd64_dir="$unpack_dir/csqtt-cli_v0.8.0_linux_amd64"
   arm64_dir="$unpack_dir/csqtt-cli_v0.8.0_linux_arm64"
   windows_dir="$unpack_dir/csqtt-cli_v0.8.0_windows_amd64"
   go version -m "$amd64_dir/csqtt-cli" "$arm64_dir/csqtt-cli" "$windows_dir/csqtt-cli.exe"
   cmp "$amd64_dir/csqtt-core" "$verify_dir/cores/core-linux-amd64/csqtt-core"
   cmp "$arm64_dir/csqtt-core" "$verify_dir/cores/core-linux-arm64/csqtt-core"
   cmp "$windows_dir/csqtt-core.exe" "$verify_dir/cores/core-windows-amd64/csqtt-core.exe"
   ```

   Go build information must show the intended GOOS/GOARCH, `CGO_ENABLED=0`,
   `-trimpath=true`, VCS revision `release_sha`, and `vcs.modified=false`. Go omits
   the recorded `-ldflags` setting with `-trimpath`; its absence is expected. Verify
   version injection from the tagged workflow/build inputs and the executable
   Linux version checks below, without claiming a Windows version-command run.
   Matching core bytes bind archive contents to the exact native/emulated probes
   in this release run. Do not rebuild replacement assets.

7. **Run offline archive smoke checks and state their limits.** Use the scope and
   timeout below for smoke execution as well as for any diagnostic tests:

   ```sh
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     "$amd64_dir/csqtt-cli" version
   probe_status=0
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     "$amd64_dir/csqtt-core" --csqtt-cli-release-probe \
     >"$verify_dir/amd64-probe.log" 2>&1 || probe_status=$?
   test "$probe_status" -eq 2
   grep -F 'Usage:' "$verify_dir/amd64-probe.log"
   readelf -l "$arm64_dir/csqtt-cli"
   readelf -l "$arm64_dir/csqtt-core"
   readelf -d "$arm64_dir/csqtt-core"
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     qemu-aarch64 "$arm64_dir/csqtt-cli" version
   ```

   Both CLI version outputs must identify v0.8.0, `release_short` without `-dirty`,
   core 2.5.0, and the expected Linux architecture. Confirm the ARM64 CLI is static
   as its CGO-disabled build promises; it should not require a glibc sysroot.

   For the ARM64 core, identify its ELF interpreter and needed libraries, then
   verify a matching loader/library set before setting `arm64_sysroot`. Existing
   ignored runtime files or a read-only download of a compatible runtime into
   scratch space may be used, with provenance/integrity checked and recorded
   locally. Do not install system packages or copy an unverified collection of
   libraries just to satisfy this step. When a usable runtime is available:

   ```sh
   probe_status=0
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 \
     qemu-aarch64 -L "$arm64_sysroot" "$arm64_dir/csqtt-core" --csqtt-cli-release-probe \
     >"$verify_dir/arm64-probe.log" 2>&1 || probe_status=$?
   test "$probe_status" -eq 2
   grep -F 'Usage:' "$verify_dir/arm64-probe.log"
   ```

   If no compatible runtime is available, explicitly report the local ARM64 core
   smoke as not run, with the missing prerequisite. Acceptance then uses the
   successful hosted ARM64 QEMU/glibc checks and byte-for-byte match of the archived
   core to `core-linux-arm64`, together with the local archived CLI check. This
   does not count as a passing local core run. A real binary/probe failure with
   satisfied prerequisites is a blocker to investigate, not this fallback.

   The core probe expects exit 2 and `Usage:`; a missing loader, timeout, or merely
   nonzero exit does not pass. It proves startup, not a core-version query or a
   connection. Core 2.5.0 identity also comes from the tagged pin and build
   provenance. For Windows, use the build-information and byte-identity checks
   from step 6 plus hosted native tests/probes. Report that the final ZIP was not
   executed locally; do not require a native Windows session or introduce Wine,
   credentials, VK interaction, or a live tunnel.

8. **Review the asset report; handoff to `lead` for publication.** Have `reviewer`
   check the release-range conclusion, exact SHA/run/tag linkage, archive and
   checksum results, core artifact identity, smoke results, and explicit limits.
   Return a bounded acceptance report with any initial failures or diagnostic
   repeats preserved. If anything remains a genuine release blocker, return it to
   `lead` before publication. Otherwise return the verified tag/assets and actual
   check results so `lead` can finish the English notes in
   `doc/local/release-v0.8.0.md`, including any unperformed local ARM64 core smoke.
   Keep the accepted profile-save and platform limitations; do not claim the
   authenticated-flow flake was repaired. `lead` reviews the sanitized final body
   and publishes the existing draft:

   ```sh
   notes_file="$(git rev-parse --show-toplevel)/doc/local/release-v0.8.0.md"
   gh release edit "$release_tag" --repo "$release_repo" --verify-tag \
     --title "$release_tag" --notes-file "$notes_file" --draft=false --prerelease=false
   ```

   The manual body replaces the generated description without changing the
   generator. Verify the three archive links, source/checksum links, README link,
   and comparison with v0.7.0; notes and local reports are not release assets.

9. **Verify publication and return the result.** Read back the public release and
   download its assets into a new directory:

   ```sh
   gh release view "$release_tag" --repo "$release_repo" \
     --json tagName,isDraft,isPrerelease,body,assets,url
   gh release download "$release_tag" --repo "$release_repo" --dir "$verify_dir/public"
   cmp "$verify_dir/draft/SHA256SUMS.txt" "$verify_dir/public/SHA256SUMS.txt"
   (cd "$verify_dir/public" && sha256sum -c SHA256SUMS.txt)
   ```

   Confirm the ordinary published state, intended notes, unchanged four-asset
   inventory, and public bytes matching the accepted manifest. Identical bytes do
   not need another smoke run. Return the final technical report and sanitized
   outcome facts to `lead`, who updates the roadmap and commits documentation.
   Outcome-only commits after publication do not move the release tag. Stage 12
   remains a separate subsequent assessment.

## Outcome

Published public release [v0.8.0](https://github.com/FreedomCats/csqtt-cli/releases/tag/v0.8.0)
on the existing patched CSQTT 2.5.0 core. The release tag points to the reviewed
commit; the existing tag-push workflow built and uploaded the Windows amd64, Linux
amd64, and Linux arm64 archives plus `SHA256SUMS.txt`. The published English notes
describe the accepted user-visible changes, server/core compatibility, licensing,
known profile-save limits, and actual verification boundaries.

The exact-SHA Linux and Windows workflows and all release-workflow jobs passed on
attempt 1. Independent review accepted the release-range preflight and the draft
assets. Downloaded assets matched the original checksum manifest and same-run
distribution output; `distcheck` passed, and all packaged cores matched the
corresponding core artifacts. Linux amd64 CLI/core smoke checks passed locally;
the static Linux arm64 CLI passed under QEMU. The arm64 core was not run locally
because a matching runtime was unavailable; its hosted QEMU/glibc checks passed.
The Windows ZIP was inspected but not executed locally; its core identity and
hosted native checks passed. No live connection or credentials were used.

The public release was read back after publication, and a fresh download of all
four public assets passed `sha256sum -c`; the public checksum manifest was
byte-identical to the accepted draft. No failed product check, retry, or rerun
occurred. An initial local smoke wrapper had a zsh reserved-variable naming error;
the corrected wrapper passed and the shell issue is recorded in the ignored journal.

The release includes scoped rollback for profile save failures; it does not solve
cross-process write/rollback races, the Secret Service snapshot/`Put` race, or the
pre-existing possibility that a keyring `Put` changes a value before metadata
persistence fails. These limitations are described in the release notes. The
separately known authenticated-flow flake was not observed in this run and is not
claimed to be fixed. No implementation issue was left unresolved due to cost.

All publication and asset checks are complete. The tag remains on the reviewed
release commit; subsequent roadmap outcome commits do not move it. Stage 12's
separate csqttr 2.5.3 assessment is next; no core migration is authorized by this
release.

## Done when

- The clean final candidate includes all pre-release documentation. The necessary
  main push is complete; the full Linux and Windows workflows succeeded on that
  exact SHA. Older green runs or a documentation-only difference do not substitute
  for these results. Published commits satisfy the anonymous author/committer and
  midnight UTC rules from `AGENTS.md`.
- Main and the local/remote lightweight v0.8.0 tag identify that same green SHA at
  tagging. The existing tag-push release workflow succeeds, including its reusable
  Linux/Windows tests, all three core builds/probes, packaging, license/distribution
  checks, checksum generation, and draft upload. No existing tag is force-moved and
  no manual-dispatch build is mistaken for draft creation.
- The downloaded draft contains exactly
  `csqtt-cli_v0.8.0_windows_amd64.zip`,
  `csqtt-cli_v0.8.0_linux_amd64.tar.gz`,
  `csqtt-cli_v0.8.0_linux_arm64.tar.gz`, and `SHA256SUMS.txt` as uploaded assets.
  All three archives match the original manifest using `sha256sum -c SHA256SUMS.txt`;
  it is not regenerated to validate the downloads. The existing
  `go run ./scripts/distcheck -dir <download-dir>` accepts all three archives, with
  the expected binaries, Wintun files, documentation, licenses, and executable modes.
- Extracted Linux amd64 CLI/core pass the offline native version and startup checks;
  the archived Linux arm64 CLI passes under QEMU. CLI output identifies v0.8.0, the
  tagged commit without `-dirty`, and core 2.5.0. The ARM64 core passes locally with
  a verified runtime, or the missing local runtime is reported explicitly and its
  identical bytes are tied to the successful hosted ARM64 probe/glibc checks as
  specified in step 7. Core probes return exit 2 and `Usage:`. All three packaged
  cores match the corresponding release-run artifacts; Windows build information
  is verified. The report distinguishes local, hosted, emulated, and unperformed
  checks, including final Windows ZIP and live/native ARM64 execution.
- The verification report preserves any initial failures and diagnostic repeats,
  including the known Rust authenticated-flow flake if observed. No failed check
  is represented as a newly repaired defect merely because a retry passed. A real
  unresolved blocker prevents publication and is returned to `lead`.
- The draft is published as the ordinary public v0.8.0 release only after asset
  acceptance. Its English notes cover the accepted changes, unchanged server/core
  2.5.0 and licensing, profile-save limitations, unsigned binaries, and actual
  verification limits. The three download links, source links, comparison with
  v0.7.0, and checksum link identify the intended release.
- A read-back of the public release confirms its tag, published state, notes, and
  asset inventory; downloaded public bytes still match the accepted manifest. The
  roadmap outcome records the result and limits without private data or raw logs.
  Later outcome-only commits do not move the release tag; the dependent stage 12
  remains a separate assessment after this release.

## Test execution constraints

The hosted workflows provide the full build/test gates. No mandatory local live
connection or repeated full local suite is added. Every locally launched `go test`,
Cargo test, or executable test binary, including a diagnostic repeat, must use
`systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0` and `timeout 600`.
Browser/window tests and the full Go suite additionally require Xvfb with `-noreset`:

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test <packages>
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 \
  xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test -count=1 ./...
```

## Risks and limits

- A later documentation commit invalidates an earlier candidate's CI evidence for
  tagging. Freeze all intended pre-release tracked changes before the exact-SHA
  runs; keep progress and downloaded evidence ignored.
- Core artifacts expire and do not contain a runtime/sysroot. Download the exact
  run's artifacts while available; report missing evidence rather than substituting
  an older artifact or treating an unavailable local ARM64 runtime as a passing run.
- `distcheck` on Linux verifies Windows archive contents without executing the ZIP.
  Hosted native core checks, archive inspection, and Go build information preserve
  that boundary; they do not establish new UAC, window, or live-platform behavior.
- Readiness and the existing authorization cover this release operation. Repairs,
  dependency/license/platform changes, a fork migration, and unrelated external
  writes require a separate scope. Deferred Windows work, W6, and live credentials
  are not prerequisites and are not reopened.
- Public notes/outcomes contain sanitized conclusions only. Keep run identifiers,
  logs, runtime provenance details, machine paths, work timestamps, and private
  values in ignored records; `lead` owns final documentation and external actions.

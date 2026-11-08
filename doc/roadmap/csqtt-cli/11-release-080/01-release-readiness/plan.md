---
kind: task
status: done
detailed: true
---

# Release readiness and notes

## Task

Prepare an accepted v0.8.0 release brief: the changes since v0.7.0, the unchanged
core/server and licensing requirements, publication-safety review, and an English
Markdown release-note draft. This gives
[build, verification, and publication](../02-publish-release/plan.md) a bounded scope
and concrete acceptance checklist.

Use the [parent stage](../plan.md), architecture decisions Р2 and §§09б/11 in
[`doc/architecture.md`](../../../../architecture.md), and the historical I3b/I4 and
v0.7.0 outcomes in [`doc/plan.md`](../../../../plan.md). The historical document is
frozen. `lead` prepares the notes and commits any release documentation; this task
does not require a product-code, test, script, configuration, or version-constant
change.

## Steps

1. Establish the reviewed revision and compare `v0.7.0..HEAD`, including pending
   release-documentation changes. Read `git status`, the commit summary, and the
   relevant accepted outcomes rather than repeating their implementation reviews.
   Retain the existing supported archives: Windows amd64, Linux amd64, and Linux
   arm64. Record the reviewed SHA and any pre-release documentation still to be
   committed in the ignored journal; the final tagged SHA is selected after those
   commits in the next task.
2. Reconcile the release inventory with the completed small-defect nodes and their
   affected modules:
   - [Physical interface selection](../../09-backlog/01-small-defects/04-physical-interface-selection/plan.md):
     Teredo/ISATAP exclusion in `internal/winsys/defaultroute_windows.go`, including
     accepted native hosted Windows coverage.
   - [Password input rules](../../09-backlog/01-small-defects/01-password-inputs/plan.md):
     `|` rejection and preserved `%` input/once-only share-link decoding in
     `internal/config/sharelink.go` and the CLI input paths.
   - [Profile save consistency](../../09-backlog/01-small-defects/02-profile-save-consistency/plan.md):
     restoration on profile-file save failure at `internal/cli/profile.go` and
     `internal/config/profile_secret.go`. Read that outcome's limitations and the
     snapshot/write/restore boundaries; public wording must retain cross-process
     write/rollback races, the Secret Service snapshot-to-`Put` race, and the
     pre-existing possibility of a partial `Put` changing the keyring value before
     metadata persistence fails. Do not promise fully atomic or concurrent saves.
   - [Core CI stability](../../09-backlog/01-small-defects/08-core-ci-stability/plan.md):
     writer-held TURN packet accounting in `scripts/vendorcore/rules.go`,
     `scripts/vendorcore/files/rust-client/turn_outbound_tests.rs`, and generated
     `core/rust-client/turn.rs`. Describe the bounded shutdown-flush correction;
     it does not guarantee remote packet delivery or fix the separately recorded
     authenticated-flow flake.
   - [CLI guidance](../../09-backlog/01-small-defects/03-cli-guidance/plan.md):
     the server-silence hint, conditional wizard privilege note, and PowerShell
     file-pipeline help in `internal/cli/connect.go`, `internal/cli/wizard.go`, and
     `internal/cli/profile.go`.
   Keep roadmap/workflow documentation changes separate from user-facing fixes in
   the release description, and do not include unfinished backlog as delivered.
3. Confirm release invariants from the diff and build sources: `core/CORE_VERSION`
   still pins CSQTT 2.5.0 to the existing upstream commit and crate, with all current
   local patches; `go.mod`, `go.sum`, core manifests/lockfile, supported targets, and
   licensing are unchanged. Verify the tag-to-`-version` path in
   `.github/workflows/release.yml`, the `-ldflags` injection in
   `scripts/release/main.go` and `cmd/csqtt-cli/main.go`, and the embedded core identity
   in `coreversion.go`. A plain development build reporting `dev` is not a reason to
   edit constants. Check the MIT shell/PolyForm NC core boundary in `LICENSE`,
   `core/LICENSE`, `README.md`, and the archive list in `scripts/internal/dist/dist.go`.
4. Review the new tracked content and every commit to be published since v0.7.0
   against `AGENTS.md`, including messages and both author and committer metadata.
   Both identities must be `FreedomCats <noreply@freedomcats.invalid>` with midnight
   UTC metadata; no Jira/session identifiers or private values may enter the release.
   Include the pending release/map documentation in this review. Keep raw checks and
   any sensitive findings in the ignored journal. Report a genuine privacy or
   licensing blocker to `lead`; do not rewrite history or change licensing here.
5. Prepare the verification handoff from the three workflow files and the existing
   `scripts/release`, `scripts/distcheck`, `scripts/sha256sums`, and
   `scripts/releasenotes` commands. Identify what hosted checks already prove and
   what the downloaded-archive checks in the parent add. Preserve accepted build,
   vet, Go/Rust regression, and integration evidence for unchanged implementations;
   final exact-SHA Linux/Windows CI and the tag pipeline belong to the next task.
   There is no mandatory local full-suite repeat or live-server pass for this review.
   If a discrepancy needs a local diagnostic test, every `go test`, Cargo test, and
   direct test binary must run in the 4 GiB/no-swap systemd scope with `timeout 600`.
   Browser/window and full-suite runs additionally require Xvfb with `-noreset`:

   ```sh
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test <packages>
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 \
     xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test -count=1 ./...
   ```

   Preserve failures, skips, and diagnostic repeats separately. In particular, a
   retry of the known authenticated-flow failure must not be described as a repair.
6. Have `lead` prepare `doc/local/release-v0.8.0.md` as the sanitized English Markdown
   draft to apply to the generated release. Cover the verified fixes above, CSQTT
   server 2.5.0 compatibility, the existing local core patches, scoped profile-save
   guarantees, MIT/PolyForm NC licensing, unsigned binaries, and remaining platform
   verification limits. Retain the three archive links, source links, checksum link,
   and comparison with v0.7.0; the generated download block can supply their targets.
   The current generator emits Russian download headings, so `lead` can compose the
   English final body manually without changing the generator. Leave future asset
   check results unclaimed until the next task. Keep the draft outside `dist` and
   release assets; put detailed execution evidence in the ignored journal. Return
   the release brief and checklist for acceptance before any publication operation.

## Done when

- The release inventory accounts for all accepted product changes since v0.7.0 and
  maps them to the existing regression/acceptance evidence. No unresolved
  release-blocking regression, privacy, licensing, or packaging issue was found;
  any actual blocker is reported to `lead` instead of being silently deferred.
- The reviewed source retains CSQTT 2.5.0, the existing upstream/crate pins, all local
  patches, dependencies, supported platforms, and licensing. The existing version
  injection path is sufficient for v0.8.0; no version bump in code is proposed.
- All commits and pending documents to be published pass the privacy/metadata
  review, including both anonymous identities and midnight UTC metadata.
- The English Markdown note draft and verification checklist are ready for `lead`.
  They state the remaining profile-save concurrency and partial-write limitations,
  server compatibility, licensing, unsigned-binary warning, and actual platform
  verification limits without claiming future test or asset results.
- The handoff distinguishes accepted earlier build/test evidence from the final
  exact-commit CI, tagged build, checksum/distcheck, and archive smoke acceptance
  still required by the next task. Diagnostic tests, if needed, obey the 4 GiB,
  no-swap, 600-second and Xvfb/`-noreset` restrictions above.

## Risks and limits

- The accepted profile-save fix is narrower than a common lock or transactional
  storage protocol across every writer. Its known limitations are release-note
  constraints, not additional implementation work for v0.8.0.
- Earlier native Windows CI does not establish a new final-archive runtime check,
  UAC behavior, or other deferred Windows-only/live verification. Keep those limits
  visible without turning that backlog or W6 into a release gate.
- Raw CI logs, run identifiers, machine paths, work timestamps, secrets, and private
  profile/server details belong in ignored local records. Only sanitized substance
  enters public notes and the roadmap outcome.

## Outcome

The release inventory matches the accepted Windows-interface, password-validation,
profile-save, TURN-shutdown, and CLI-guidance outcomes since v0.7.0. Existing
regression and integrated acceptance evidence is retained; no new product change
is required to prepare v0.8.0. The upstream core commit and crate remain unchanged
at CSQTT 2.5.0; only generated vendor counts changed for the accepted local patch.
Dependency files, supported archives, licensing, and release tooling are unchanged.
The tag-to-version injection already supports v0.8.0 without editing constants.

An English release-note draft is prepared outside git. It covers the user-visible
fixes, CSQTT 2.5.0 server compatibility, unsigned binaries, MIT/PolyForm NC boundary,
and the existing live-verification limits. Profile rollback is explicitly scoped:
cross-process writer/rollback races, the Secret Service snapshot/write race, and
the pre-existing partial keyring update on metadata-write failure remain possible.
The separate intermittent authenticated-flow test failure is not described as fixed.
Future asset and CI results are not claimed in the draft.

Commit author/committer metadata and messages since the prior release were checked
against the anonymous identity and midnight UTC rules. Accepted historical documents
retain their language; new release documentation is English. Published changes and
pending roadmap documents were reviewed for personal data and publication scope;
no release blocker was identified. No build or test was repeated for this
documentation task. The next task owns exact-commit hosted CI, the tagged build,
downloaded-asset verification, and publication.

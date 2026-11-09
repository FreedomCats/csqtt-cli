---
kind: task
status: in-progress
detailed: true
base: 366d87ae618b81cb7733e4ee6a5747b6c09f4e53
---

# Inventory dependency and licence delta

## Task

Produce one reviewed dependency/licence metadata inventory for the
[parent stage](../plan.md). Compare unpatched baseline B,
`d02da9bd9d8926969b766e197c434d830a83dbd8`, with candidate C,
`84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`; distinguish both from the locally
patched 2.5.0 product. This inventory supplies the subsequent platform and security
assessments and `lead`'s recommendation. It does not select new dependencies or
licences, establish legal compatibility, resolve Cargo features or prove binary use.

Use architecture decision R2 and §11 in
[`doc/architecture.md`](../../../../../architecture.md), the accepted
[provenance Outcome](../../01-upstream-provenance/plan.md), and
[vendor reconciliation Outcome](../../03-local-patches/05-vendor-rules-and-crate/plan.md).
Reuse `doc/local/core-253-vendor-review.md` and the integrated evidence in
`doc/local/core-253-patch-acceptance.md`, including their source-access references.
Preserve those records. A coordinator and independent non-coding reviewer perform
this task using manifests, lockfiles, licence/NOTICE files and provenance metadata;
external implementation source is unnecessary and must not be opened for it.

## Steps

1. Establish scope from `core/CORE_VERSION`, the accepted copied-input inventory and
   both exact source trees. Cover `rust-client/Cargo.toml`, its root `Cargo.lock`,
   relevant nested vendor manifests/licence metadata and copied `shared/` inputs.
   Separate everything copied by `vendorcore` from dependencies declared for the
   client, build/dev/target-conditional entries and what might be distributed or
   linked. Source presence, lock membership and an opt-in runtime feature establish
   different facts. Server, Android and iOS-only packages are outside the shipped
   desktop client scope; use their manifest/lock delta only to document that boundary,
   not to audit those applications. Record the local connector path-patch/direct
   dependency/lock rewrite as existing local treatment, using the accepted evidence.
2. Compare the original B/C client manifests and root locks by package identity
   (name, version and source/path), not by line diff alone. Inventory additions,
   removals, version/source/checksum changes and changed dependency edges separately.
   Metadata-only planning inspection found one added direct path dependency,
   `csqtt-gooby`, 26 added lock records, no removed records and three edited existing
   records (`csqtt-client`, `indexmap`, `tracing`); independently verify these leads.
   Distinguish disambiguated lock dependency strings from actual package upgrades and
   shared pre-existing dependencies from new ones. Check unchanged manifest sections
   and relevant vendor metadata rather than treating every lock entry as a change.
3. Trace each changed package to its introducing manifest/lock edge and record declared
   features, source type and integrity/provenance evidence. The new path package's
   manifest is `rust-client/vendor/gooby/Cargo.toml`; it declares `mlua` with
   `luajit`, `vendored`, `send`, and `rusqlite` with `bundled`, alongside other
   dependencies. Cover their added transitive/native-source and build-helper entries,
   distinguishing selected features from lockfile alternatives and uncertain target
   reachability. Use exact-version package manifests/registry metadata where needed;
   no resolver is run. Retain the accepted unchanged `primp-hyper-util` 0.1.22 archive
   contract rather than repeating its archive/patch audit. Flag new native inputs for
   child 02 and additional possible runtime surfaces for child 03 without auditing
   implementation, toolchains or security here.
4. For every changed package/input, record the exact-version licence expression or
   `license-file`, evidence for retained licence/NOTICE/attribution files, source
   origin and any missing/conflicting declaration. For Git-copied Gooby metadata,
   inspect `LICENSE`, `NOTICE` and `UPSTREAM.txt` at C, separating declared provenance
   from independently verified lineage. For registry packages use already available
   checksum-bound metadata or read-only version-specific primary metadata/text;
   do not download crate archives or dependency source. Include licence metadata for
   bundled native components where available, not just the Rust wrapper identifier.
   Mark unavailable exact-version texts, bundled-component provenance or applicability
   unknown; do not borrow terms from a different version or infer permission from a
   manifest identifier or checksum. Preserve the accepted pre-existing root PolyForm
   NC versus Cargo `Unlicense` conflict and existing root-LICENSE treatment. Escalate
   materially new boundary/read-restriction evidence to `lead` before any affected
   implementation review; a metadata gap alone does not require an authorization
   request when no change is proposed.
5. Map only the resulting notice/provenance implications to the current local
   distribution contract: architecture §11 and the Rust notice collection in
   `scripts/notices/main.go:20`, `:304`, `:531` and `:561`, plus relevant retained
   metadata in `scripts/notices/licenses/`. The collector uses target-filtered Cargo
   metadata and collects licence/NOTICE texts; do not invoke it or `scripts/release`.
   State potential missing attribution/text or source-kind handling that a later
   migration would need to verify; do not claim notices pass, prescribe a licence
   choice, modify scripts or regenerate output. Keep shipped-component certainty
   separate from the collector's conservative graph and from copied-source coverage.
6. Record the compact inventory in ignored `doc/local/core-253-dependency-review.md`.
   Each delta row identifies B/C package/source/checksum or edge, introducing path,
   declared feature/role and scope certainty, exact-version licence/provenance
   evidence, notice implication, exact-ref/primary URLs or local `file:line`, and
   confidence/gaps. Include a coverage summary for copied inputs versus client graph
   versus excluded applications and explicit handoffs to children 02/03. Have an
   independent non-coding reviewer reconstruct the manifest/lock delta and verify
   every new metadata/licence claim against the primary records, rather than merely
   endorsing the table. Record corrections there, preserve accepted evidence and
   finish with a concise sanitized Outcome in this task node for `lead`.

## Done when

- The reviewed inventory covers every B/C client package and dependency-edge delta,
  with additions, removals, version/source/checksum changes and lock disambiguation
  distinguished; all counts reconcile with the exact manifests/locks.
- Copied source/vendor metadata, declared client dependencies, build/dev/conditional
  roles, possible shipped use and excluded server/mobile scope are explicit. Existing
  local connector changes are not misreported as a new upstream dependency delta.
- Every changed package/input has exact-version licence/provenance evidence and
  notice implications or a precise unresolved gap, including available bundled-native
  metadata and the new path package's LICENSE/NOTICE/UPSTREAM records. The established
  root licence conflict is preserved without a legal conclusion or inferred permission.
- Independent metadata review is complete; the ignored inventory and public Outcome
  provide source references, confidence and bounded platform/security handoffs.
- Only permitted read-only metadata/document consistency and privacy/whitespace checks
  are performed. No external implementation review, code/config/pin edit, archive/
  dependency or binary acquisition, Cargo command/resolution, notice/vendor generation,
  build, test/test binary, live check or external write occurs. Candidate build,
  runtime, resolved features and final notice completeness remain unverified.

## Risks and limits

- A lockfile has no complete licence or dependency-kind/feature information. Its
  package set is neither proof that every package is used on every target nor proof
  that disabled runtime functionality removes build dependencies. Preserve unknowns
  instead of using Cargo to settle them during this phase.
- Native sources bundled inside a crate can require separate metadata and attribution;
  a wrapper's licence identifier does not establish their terms or availability.
- This is one delta inventory, not a new audit of the entire unchanged dependency
  graph, the proxy implementation, security advisories or supported platforms.
- No live access or user choice is needed to record facts and gaps. The parent's
  future-only Go constraints (4 GiB, no swap, `timeout 600`; browser/window tests under
  Xvfb with `-noreset`) must be relayed to the reviewer and any later task. They give
  no permission to run tests here.

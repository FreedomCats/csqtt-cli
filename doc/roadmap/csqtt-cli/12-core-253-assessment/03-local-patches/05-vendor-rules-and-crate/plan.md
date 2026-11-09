---
kind: task
status: done
detailed: true
base: 96158ac3ddeb1fae9c1ef35c7c45280c1e89ddd0
depends: [01-process-and-loopback-guards, 02-linux-tun-safeguards, 03-socket-binding-p3, 04-captcha-patches]
---

# Reconcile vendor rules and crate inputs

## Task

Produce one source/vendor reconciliation assessment for the [parent stage](../plan.md):
account for every current rule and generated input, identify static carry blockers and
dependencies, and state what remains unverified. Compare pinned 2.5.0 commit
`d02da9bd9d8926969b766e197c434d830a83dbd8` (B) with accepted csqttr source commit
`84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806` (C). The current patched `core/` defines
the local contract; it is not either unpatched upstream tree.

Use architecture decision R2 and §§06, 07, and 11, `doc/upstream-proposals.md`, and
children 01–04's accepted Outcomes and ignored matrices. Their process, socket,
Linux TUN, captcha and TURN behavior findings are inputs, not new research here.
Deliver the reconciled evidence in ignored `doc/local/core-253-vendor-review.md`,
preserving the earlier matrices, followed by a sanitized Outcome in this node after
independent review.

Only the coordinator or an independent non-coding reviewer may inspect incompatible
external implementation source; do not assign that work to a planner or programmer.
Report behavioral facts in original wording, local rule identities, counts and exact
source references without external source, snippets or retold implementation
structure. This assessment does not authorize migration or change the existing
root-LICENSE treatment.

## Steps

1. Fix the evidence boundary using `core/CORE_VERSION`, the documents above, and
   `doc/local/core-253-protocol-review.md`, `core-253-patch-review.md`,
   `core-253-tun-review.md`, and `core-253-runtime-review.md`. Reuse the accepted
   direct-substitution blockers and patch classifications; inspect external source
   only for missing vendor-input facts or a specific reconciliation discrepancy.
   Keep baseline-equivalent behavior separate from candidate changes. Preserve P6
   as already upstream at B, with no current carried rule. Do not repeat the wire,
   process, eight-site P3, TUN handoff or runtime behavior assessments.
2. Build a canonical inventory from `scripts/vendorcore/rules.go`, keyed by each
   entry's opening `file:line` and target, with links to its prior evidence rows.
   Record domain/local purpose, behavioral anchor identity, required count, input
   origin (exact Git tree or locked crate), B/C raw counts and references, earlier-rule
   dependencies, static carry expectation, effective-application evidence/status,
   and the separately accepted behavior classification. Use these coverage checks:

   | Disjoint group | Entries | Prior evidence |
   |---|---:|---|
   | P1 | 5 | Patch matrix |
   | P2 | 12 | Patch matrix |
   | P3 excluding the TURN correction/helpers | 22 | Broad P3 inventory |
   | P4 | 4 | TUN matrix |
   | P5 | 4 | Runtime matrix |
   | P7 | 1 | Runtime matrix |
   | TURN correction and helpers | 11 | Runtime matrix and overlapping P3 rows |

   The 33-entry/35-match broad P3 range and 16-entry/18-match runtime inventory
   overlap in eleven TURN entries requiring thirteen matches. Their union with
   P1/P2/P4 covers all 59 entries. The sum of required counts is 61; it is not an
   observed raw or effective total. Reconcile by identity, not by adding domain
   totals. Classify any unmatched local entry explicitly rather than omitting it.
3. Assess ordered applicability from `scripts/vendorcore/apply.go:8` and
   `scripts/vendorcore/main.go:255`. Counts are checked against the text produced by
   earlier rules for the same file, after LF
   normalization; file iteration itself has no fixed cross-file order. Inspect
   earlier insertions/replacements for created, removed or duplicated later anchors
   without executing substitutions. In particular, P3 entries at `rules.go:356` and
   `rules.go:362` have raw-zero anchors supplied by P1's field and flag-list rules;
   preserve that dependency and label the expected later count as a static inference.
   Retain C's `rules.go:492` TURN TCP mismatch as an intact-carry blocker despite the
   ordinary socket path still existing. Reuse child 04's passing 16 raw counts and
   runtime carry findings without treating them as successful rule application.
   Distinguish raw match, earlier-rule dependency, mismatch, blocked prerequisite
   and unknown effective status. Whole-pipeline application remains unexecuted;
   describe later-stage consequences conditionally on earlier blockers being fixed.
4. Audit the input/output contract in `main.go` and `rules.go`, including both exact
   source trees. Account for `rust-client/` and `shared/`, root `LICENSE` and
   `domains.txt`, ignored `rust-client/target`, and the two excluded Android inputs
   `rust-client/build_android_abis.ps1` and `rust-client/build_so.bat`. Check exact-path
   presence: a missing excluded input is an error, not permission to copy a renamed
   replacement. Identify relevant added/removed input paths and non-regular-file or
   rule-target problems without broadening the behavioral review.

   Inventory all nine files under `scripts/vendorcore/files/`: the `rust-client/`
   files `secrets_stdin.rs`, `listen_peer.rs`, `tun_peer.rs`, `bind_if.rs`,
   `bind_if_tests.rs`, `direct_relay_bind_if_tests.rs`, `captcha_window_tests.rs`,
   `turn_outbound_tests.rs`, and
   `rust-client/vendor/primp-hyper-util/src/csqtt_bind.rs`. For each, record purpose,
   production/test role, module or rule wiring, and B/C target-collision status;
   check the crate namespace as well as Git trees. Reuse established absence findings.
   Added files enter after rules and are not processed by them; include test wiring
   from added `bind_if.rs` as well as wiring inserted into upstream files.

   Explain the pipeline order: copy/normalize inputs, add the verified crate, validate
   targets and apply rules, add local files, run the socket guard, construct metadata,
   then compare or write/remove output. `CORE_VERSION` currently records `files = 125`,
   `rules = 59`, `added = 9`: `files` includes crate and added files but excludes
   `CORE_VERSION` itself; `rules` counts entries, not matches. Explain LF comparison,
   preserved binary content, extra-file handling and the ignored build output.
   Account for the hard-coded upstream URL, Git HEAD/dirty metadata and manifest-derived
   version; identify hypothetical provenance adaptation without editing metadata.
   `-check` constructs the same proposed output and compares it with current `core/`;
   that byte comparison does not establish behavioral compatibility with patched B.
   Do not predict a generated C file total as if the pipeline had succeeded.
5. Reconcile the crate contract in `crates.go`, `rules.go:553`, and
   `scripts/vendorcore/crates/primp-hyper-util-0.1.22.crate`. Reuse P3's accepted
   evidence that B/C lockfiles select 0.1.22 with the recorded archive checksum;
   verify missing dependency facts from exact-ref manifests/lockfiles and read-only
   archive inspection. Map the path through vendored `primp` and `primp-reqwest` to
   the connector, plus the local direct dependency, exact version, `client-legacy`
   feature, `[patch.crates-io]` path and client lock dependency. Determine whether the
   proposed paths refer to one connector package; static evidence is not Cargo
   resolution or build success.

   Separate the original registry version/source/checksum used before rules from
   the local lock rewrite that removes registry source/checksum for the path package.
   Cover missing/multiple registry records, changed source/lock format, absent or
   wrong archive, SHA-256 verification, archive prefix/path/type/duplicate constraints,
   destination collisions and the `.gitignore` skip. Distinguish skipped archive
   inputs from copied crate tests/examples that are only excluded from socket scanning.
   Map the two crate-target rules and added `csqtt_bind.rs` to the accepted connector
   requirement; Git-path absence is not a missing rule target when the archive supplies
   it. Identify any required archive/rule/manifest/lock adaptation and its dependency
   order, or record that none was identified. No archive acquisition or dependency
   change is authorized.
6. Assess socket-guard consequences from `sockets.go` against its intended post-rule,
   post-added-file input. Reuse P3's eight-site behavior matrix; inspect only new or
   changed guard-relevant source sites and existing path/count/`Then` expectations.
   Explain the lexical scan's scope, comment/test-module filtering, skipped
   `*_tests.rs` and crate tests/examples, missing-file/count/new-call failures,
   required binding/resolver/proxy markers and unterminated-test-module failure.
   Compare C's relevant call identities with `socketCall`/`socketSites` without
   invoking `checkSockets`. Include the ordinary TURN TCP anchor consequence and
   whether C's accepted Gooby loopback connection introduces a detected, unlisted
   production site. Record predicted blockers or scan limitations with exact evidence;
   do not claim that a guard stage blocked by earlier rules ran or passed.

   Resolve the assignment label "Gooby" only against architecture §07,
   `doc/upstream-proposals.md` and `scripts/vendorcore`. If no local artifact/patch is
   identified, record the searched scope and unresolved label for `lead`. Keep this
   separate from P3's accepted opt-in candidate TURN TCP/TLS SOCKS5 path and its
   unknown external route. Do not invent a local patch identity, approve a guard
   exemption, investigate the external proxy implementation, or add proxy support.
   Carry P3's stale system-DNS description into synthesis as an existing evidence
   caveat; do not edit architecture or repeat routing analysis here.
7. Inspect, without running, the relevant checks in `main_test.go`, `crates_test.go`
   and `sockets_test.go`: ordered rule/count failures; write/check, added-file,
   exclusion and extra-file fixtures; crate errors and
   `TestCrateArchiveMatchesCoreVersion`; `TestSocketGuardOnCore`,
   `TestSocketGuardCatches` and `TestSocketGuardSkipsTests`. Reuse child 04's TURN
   rule/test evidence. State which checks use fixtures or current generated `core/`
   and which candidate assertions they cannot establish. The architecture's later
   `cargo --locked`, compilation and socket-runtime protections remain unexecuted;
   add no tests and require no build/test success for this source-only result.
8. Complete the ignored reconciliation matrix with exact-ref URLs/lines, local
   `file:line` references, confidence and explicit unknowns. Include the 59-row
   inventory, nine-file/input reconciliation, generator/metadata/crate/guard facts,
   unresolved Gooby label and an ordered list of hypothetical migration work with
   concrete affected artifacts. Keep behavioral necessity, mechanical carry and
   unperformed generation/build/runtime validation separate. Have an independent
   non-coding reviewer reconstruct the identity/count reconciliation from local rules
   and accepted evidence, verify uniform row meanings and all new exact-ref input
   assertions, and check dependencies, collisions, crate/guard consequences and
   uncertainty labels. Review corrections belong in this new evidence record; preserve
   accepted siblings and earlier matrices. After review, the coordinator records a
   concise sanitized Outcome here and hands the assessment to `lead` for stage
   acceptance and later risk/recommendation synthesis.

## Done when

- The ignored matrix covers all 59 current rule identities exactly once, with 61
  required matches distinguished from observed/effective counts. B/C raw status,
  origin, dependencies, static carry expectations, behavior classification and evidence
  limits use consistent meanings. The eleven overlapping TURN entries are deduplicated;
  P6 remains a separate already-upstream finding with no carried rule.
- Ordered-anchor consequences explicitly include both P1-created P3 anchors and C's
  TURN TCP mismatch. Passing runtime raw counts do not imply effective application,
  generated-output correctness, compilation or runtime success.
- All nine added files have purpose, wiring and B/C collision status, including the
  crate namespace. Copied root inputs, both excluded Android inputs, ignored output,
  crate skips, relevant source-tree changes and generator failure/order semantics are
  accounted for. Current `CORE_VERSION` counts and provenance fields are explained
  without inventing a successful candidate output or changing the pin.
- The 0.1.22 archive, original lock source/version/checksum, archive validation/skip
  rules, path-patch/direct/transitive dependency contract and local lock rewrite are
  reconciled at both exact refs. Connector behavior uses accepted P3 evidence; any
  new adaptation or unresolved Cargo fact is identified explicitly.
- Socket-guard coverage and limitations, candidate guard-relevant changes and the
  distinction between the unresolved local "Gooby" label and accepted candidate
  proxy path are recorded. No unknown proxy route or newly allowed socket is assumed
  safe; no external proxy implementation is investigated.
- An independent non-coding reviewer verifies matrix completeness and consistency,
  new source/input assertions and carry conclusions. Inspected tests are identified
  with their limits and no claimed run result. The sanitized public Outcome and
  ordered migration-work handoff preserve prior accepted findings and unknowns.
- No code, tests, scripts, configuration, pin or generated core is changed. No rules
  are executed, no generator or `-check`, build, test run, live check, captcha window,
  external write or legal conclusion is produced. Existing licence treatment, captcha
  policy, supported platforms and the user's migration decision remain unchanged.

## Risks and limits

- Identical counts can hide an order dependency, a semantically changed insertion
  point, a collision or a later guard failure. Static expectations and accepted raw
  counts are not evidence that the whole rule sequence or output succeeds; leave
  effective status unknown where it cannot be established without execution.
- `socketCall` and `Then` are textual guards, not a complete Rust parser, Cargo graph
  proof or observation of an effective host route. A loopback proxy's external route
  remains unknown, and updating a guard table alone would not establish binding.
- Preserve accepted runtime limits: normal `wv` already bypasses the solver, P7 is
  defensive, candidate diagnostics do not fix writer-owned UDP draining, and separate
  captcha timers without request IDs leave an existing deadline-edge limit. None
  authorizes a new protocol, policy or runtime investigation here.
- No live access or new user choice is needed to finish this bounded assessment;
  unresolved labels and evidence gaps are valid findings. Any later test authorization
  must retain `doc/local/resume.md`'s memory/no-swap limits and timeout, with Xvfb and
  `-noreset` for window tests; those requirements do not authorize test execution here.

## Outcome

The ignored reconciliation record `doc/local/core-253-vendor-review.md` covers all
59 rule identities exactly once, with 61 required matches distinguished from raw
counts and unexecuted effective application. Eleven TURN entries are deduplicated
between the P3 and runtime inventories. The two raw-zero P3 anchors depend on
earlier P1 insertions; their expected later counts are static inferences. The
candidate's ordinary TURN TCP anchor still mismatches, blocking intact carry and
the following import rule. Passing runtime raw counts does not establish successful
generation. P1-P5, defensive P7 and the UDP completion correction retain their
accepted necessity; P6 remains already upstream at the baseline, without a rule.

All nine local additions have their purpose, module/test wiring and absence of
B/C Git or archive collisions recorded. Both exact Android exclusions remain
present. New candidate files, including examples, a WSL script and the Gooby
package, would enter through existing copy roots. Input/output ownership, LF and
binary handling, extra files, ignored build output, pipeline order and current
metadata counts are reconciled. The upstream URL is hard-coded, while the candidate
client manifest still declares 2.5.0; a source migration needs explicit provenance
adaptation rather than inferring client version from the release label.

Both original lockfiles select the same checksum-verified connector archive,
`primp-hyper-util` 0.1.22. Its retained inputs, validation constraints, module and
connector rules, direct exact dependency, feature, transitive package path, path
patch and local lock rewrite are consistent at both refs. No connector archive,
manifest, lock or rule adaptation was identified. This is static evidence for the
intended single connector package, not Cargo resolution or build success.

After earlier blockers are repaired, the unchanged socket guard would encounter
seven new unlisted lexical detections across five candidate files, including the
production loopback proxy connection, copied examples and Git-vendored Gooby
sources. Its example exclusions cover the configured connector crate, not every
copied example. Neither a new table entry nor a loopback address establishes the
proxy's external route or authorizes an exemption. The assignment's local Gooby
patch label remains unresolved in the prescribed search scope, separately from
the accepted candidate proxy path and its unknown external route.

An independent non-coding reviewer reconstructed identities and counts and checked
the new input, crate, dependency and guard assertions. Three small documentation
corrections clarified entry/match units, fixture coverage and P2 evidence links.
Existing tests were inspected, not run. No code, test, script, configuration, pin
or generated core changed; no rules, generator, dependency resolution, build,
browser/live check, external write or legal conclusion was produced. The ordered
hypothetical migration work is ready for parent-stage acceptance and task 04
synthesis. Preserve the existing system-DNS wording caveat and captcha contract
limits. No real current-product correction was deferred because of cost; the
patched 2.5.0 pin and separate migration decision remain unchanged.

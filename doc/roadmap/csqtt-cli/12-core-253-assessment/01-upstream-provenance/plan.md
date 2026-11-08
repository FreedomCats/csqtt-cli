---
kind: task
status: in-progress
detailed: true
base: 0d7cd9b6a119a1e1cc0a5461f0d8fe7e16045039
---

# Verify upstream status and the 2.5.3 source

## Task

Verify the reported maintenance transition from `amurcanov/csqtt` to
`Afteroid/csqttr`, and establish the exact commit-addressed source for csqttr 2.5.3
before any technical comparison. The GitHub API currently reports the original
repository as archived, but reports `Afteroid/csqttr` as `fork: false` and not
archived. Its `v2.5.3` tag and Release point to commit
`84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`. That Release names server core 2.5.3
and `CSQTT-WIRE-3`; at its commit,
`rust-server/Cargo.toml` says 2.5.3 but `rust-client/Cargo.toml` still says 2.5.0.
The `v2.5.3` Release is not marked immutable and its commit is reported unsigned.
Treat these as separate facts: verify lineage, tag target, client/server source
versions, and maintenance evidence. Do not call the repository a GitHub fork or
claim the published server artifact is reproducibly built from the tagged source
without evidence. Check licensing metadata before reading implementation
code. The local baseline remains the CSQTT 2.5.0 pin in `core/CORE_VERSION`; this
assessment must not change it.

Use the [parent stage](../plan.md), the architecture's vendor and licence decisions
Р2 and §§07/11 in [`doc/architecture.md`](../../../../architecture.md), and the
recorded upstream proposals in [`doc/upstream-proposals.md`](../../../../upstream-proposals.md).
Use primary GitHub repository, release, tag, commit, and source-file records; a fork
description or release announcement alone is not evidence for the underlying ref.
Relevant starting points are the repository APIs at
`https://api.github.com/repos/amurcanov/csqtt` and
`https://api.github.com/repos/Afteroid/csqttr`, the `v2.5.3` Release and Git ref,
and the raw `LICENSE`, `rust-client/Cargo.toml`, `rust-server/Cargo.toml`, and
`app/src/main/assets/csqtt.server-provenance.json` at the resolved commit.

## Steps

1. Read `core/CORE_VERSION` and record the current upstream URL, immutable commit,
   version, and crate pin as the comparison baseline. Do not edit the file or fetch a
   replacement into `core/`.
2. Read the canonical repository metadata and pages for `amurcanov/csqtt` and
   `Afteroid/csqttr`. Verify their archived flags, the `fork` metadata, shared
   ancestry/source, and the fork repository's current maintenance evidence using
   GitHub's read-only API and repository records. Distinguish a shared commit history
   from GitHub's formal fork relationship. Treat release and commit activity as
   evidence with limits; do not equate a non-archived flag or a single recent commit
   with active maintenance.
3. Verify the `v2.5.3` tag ref and commit through the Git refs/tag API, confirm the
   Release object's target and mutability, and record canonical URLs and the full
   commit ID, not a moving branch. At that exact ref, compare the client and server
   Cargo versions with the Release description and server provenance manifest,
   including the declared wire-protocol revision and build-input hashes. Report
   disagreements explicitly; source metadata or a checksum manifest is not by itself
   proof that a released binary matches the source.
4. Before examining implementation changes, inspect the fork's root licence and
   relevant source/dependency licence metadata at the exact ref. Compare the licence
   text and declarations, including any conflict between a manifest and a `LICENSE`
   file. The root `LICENSE` is PolyForm Noncommercial 1.0.0, while both Rust Cargo
   manifests declare `Unlicense`; report this conflict without selecting a licence
   or making a legal conclusion. If the root terms or any file-specific terms
   materially change the existing core boundary, stop implementation review and
   report the issue to `lead`.
5. Return a short evidence record to `lead`: verified facts, primary-source URLs and
   immutable refs, what each source establishes, and unresolved points. Keep raw API
   responses and investigation logs out of tracked files. No live server check,
   build, test, push, issue, or other write action is part of this task.

## Done when

- The archived state of the original repository, formal fork metadata, shared source
  lineage, and the fork repository's state are supported by primary-source evidence,
  with limits on what repository activity establishes stated explicitly.
- The exact 2.5.3 tag and commit are verified, and the GitHub Release object,
  mutability, client/server source versions, and provenance metadata are checked
  separately. Any disagreement is reported rather than guessed away; the ref is
  identified by its SHA even if the tag itself is mutable.
- Licence text and relevant declarations at the fork ref are checked against each
  other and the project's existing MIT-shell/PolyForm-NC-core boundary. Any review
  restriction or ambiguity is reported before implementation review proceeds.
- The current 2.5.0 commit and crate pin are recorded as unchanged; the evidence
  handoff distinguishes verified facts from inference and names any open evidence
  gaps.

## Risks and limits

- GitHub repository metadata can establish an archived flag and whether GitHub marks
  a formal fork, but not maintenance intent or future support. A mutable tag can be
  moved; an unsigned commit is not authenticated by its author field. Release/tag/
  commit records establish provenance, not source-to-binary reproducibility.
- A manifest licence identifier can conflict with the repository's licence text.
  Do not resolve such a conflict by selecting the more permissive declaration.
- No user input is needed for this read-only source check. Any later change to the
  project's licence, supported platforms, protocol, or core pin remains outside this
  task and requires a separate user decision.

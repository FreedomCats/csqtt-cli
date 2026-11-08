---
kind: task
status: todo
depends: [01-upstream-provenance, 02-protocol-compatibility, 03-local-patches]
---

# Assess release risks and recommend a path

## Goal

Complete the assessment of csqttr 2.5.3 by comparing dependency and licence
metadata, supported build targets, and security/privacy-relevant behavior with the
current pinned core and shell. Synthesize the evidence and migration estimate from
the preceding tasks into a recommendation to migrate, defer, or stay on 2.5.0.
The recommendation is for `lead` to present to the user; it is not approval to
migrate or publish another release. Use architecture §11 and the project's existing
platform/build and privacy constraints. `lead` owns the public assessment text.

## Done when

- Added, removed, or changed Rust dependencies and their licence/provenance
  implications are inventoried from the exact source manifests and lockfiles;
  unresolved licence conflicts are explicit and not treated as permission.
- Supported operating systems and architectures, required toolchains, and build or
  runtime constraints are compared with the existing Windows/Linux targets. Source
  metadata is not presented as proof of a successful build or platform support.
- Security and privacy findings address relevant changes to secret handling,
  logging, network behavior, permissions, and exposed process interfaces, separating
  demonstrated changes from unverified risk.
- A concise recommendation selects migrate, defer, or stay, cites concrete benefits,
  compatibility blockers, patch/shell work, dependencies, platform and licence
  constraints, and evidence gaps, and names the user decisions still required before
  any migration. The handoff contains sanitized public evidence; no migration,
  code/config change, build/test, or release action is performed.

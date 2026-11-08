---
kind: stage
status: in-progress
depends: [11-release-080]
base: 00004c3d78df4efb196508288775dff1c75362e1
---

# Assess csqttr 2.5.3

## Goal

After v0.8.0 is published on the current core, assess whether to migrate from the
current CSQTT 2.5.0 pin and local patches to `Afteroid/csqttr` 2.5.3. Verify the
reported archival of the original repository and maintenance continuation in the
fork using primary sources.

This is research and a recommendation, not approval to replace the vendored core,
change protocols or licensing, enable new features, or publish another release.
Preserve the current core while gathering evidence.

## Children

1. [Verify upstream status and the 2.5.3 source](01-upstream-provenance/plan.md) —
   establish the original repository's archived state, the continuation's lineage
   and maintenance evidence, exact source ref, and licence metadata before code
   review. This is the first detailed task.
2. [Assess technical delta and shell compatibility](02-protocol-compatibility/plan.md) —
   compare wire/server protocol and the core process contract with the current
   `coreproc` integration.
3. [Map local patches and migration impact](03-local-patches/plan.md) — classify the
   existing core patches and identify their vendor and shell consequences.
4. [Assess risks and recommend a path](04-risk-and-recommendation/plan.md) — compare
   dependencies, licensing, platforms, security and privacy, then hand the evidence
   and recommendation to `lead` for the user.

## Done when

- The original repository's archival status, the fork's maintenance status, and the
  exact 2.5.3 release/tag/commit are verified from primary sources.
- Changes from the current upstream pin are assessed for protocol and server
  compatibility, CLI/core events and arguments, configuration and profile effects,
  dependencies, supported platforms, licensing, security, and privacy.
- Existing local core patches and shell integrations are mapped to their status in
  the fork: still needed, upstream equivalent, conflicting, or requiring investigation.
- A concise public assessment describes concrete benefits, migration work and risks,
  evidence limits, and a recommendation to migrate, defer, or stay on the current
  core. Private working notes remain outside git.
- The user receives the recommendation and the decisions needed before any migration.

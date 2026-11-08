---
kind: stage
status: todo
depends: [11-release-080]
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

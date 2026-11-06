---
kind: stage
status: todo
---

# CLI guidance

## Goal

Correct three independent user-facing hints without changing profile, connection, or
PowerShell input semantics: explain the ambiguity of server silence, avoid a misleading
administrator-rights note in the wizard, and document a pipe form for reading `-link` in
PowerShell.

## Candidates

- Mention that a wrong password can look like a silent server, rather than naming only
  a core-version mismatch.
- Do not say administrator rights are needed in the wizard when the process already has
  them.
- Show a PowerShell-compatible pipe form for supplying a share link to `profile add
  -link` and `profile set -link`.

## Done when

- Each candidate is resolved, explicitly deferred, or dropped with a reason in its
  outcome; implemented hints have focused regression tests for user-visible text or
  conditions.
- The documented PowerShell pipe form works with the existing hidden-input behavior and
  tests cover the help text.
- Relevant CLI tests pass.

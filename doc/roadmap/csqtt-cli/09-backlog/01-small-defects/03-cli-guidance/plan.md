---
kind: stage
status: in-progress
base: cbeaad4
---

# CLI guidance

## Goal

Correct three independent user-facing hints without changing profile, connection, or
PowerShell input semantics: explain the ambiguity of server silence, avoid a misleading
administrator-rights note in the wizard, and document a pipe form for reading `-link` in
PowerShell.

## Children

1. [Server-silence hint](01-server-silence-hint/plan.md) — mention that a wrong password
   can look like a silent server, rather than naming only a core-version mismatch.
2. [Wizard privilege note](02-wizard-privilege-note/plan.md) — do not say administrator
   rights are needed in the wizard when the process already has them.
3. [PowerShell link help](03-powershell-link-help/plan.md) — show a PowerShell-compatible
   pipe form for supplying a share link to `profile add -link` and `profile set -link`.

These are independent changes with separate acceptance checks. Their numeric order is
the execution order; none requires another child's implementation.

## Done when

- Each candidate is resolved, explicitly deferred, or dropped with a reason in its
  outcome; implemented hints have focused regression tests for user-visible text or
  conditions.
- The documented PowerShell pipe form works with the existing hidden-input behavior and
  tests cover the help text.
- Relevant CLI tests pass.

---
kind: stage
status: done
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

## Outcome

All three candidates are resolved and independently reviewed:

- `ea1bc54`: the server-silence hint names an incorrect password as one possible
  cause and shows the password-update command for the selected profile. Existing
  version, address/port, and availability guidance and connection termination remain.
- `e6f1424`: the wizard shows the existing privilege note only when the selected
  mode requires rights that the process lacks. Serial regressions cover both
  privilege states, mode choices and defaults, unavailable modes, and missing guards.
- `f803af3`: both profile link-import descriptions include a PowerShell file pipeline.
  Hidden input, POSIX examples, parsing, persistence, and help routing are preserved.

The integrated review of `cbeaad4..f803af3` found no correctness, regression,
requirements, or privacy issues. The combined focused CLI regressions, fresh full Go
suite with the prescribed memory limits and Xvfb setup, build, vet, formatting, and
diff checks passed. No code correction was needed for stage acceptance, and no real
problem was left unfixed because of implementation cost.

The user chose existing `-h`, `-help`, and `--help` entry points rather than extending
subcommand `help` routing. The authorized temporary official PowerShell 7.6.9 runtime
successfully exercised both pipelines with isolated synthetic profiles and secret-free
output. Its archive checksum was verified and all temporary artifacts were removed.
That accepted runtime evidence was reviewed rather than repeated during integration.

Native Windows token detection, UAC, console/window lifecycle, and Windows PowerShell
5.1 remain unverified. Deterministic privilege tests and PowerShell on Linux establish
only their stated behavior. Core/Rust, real-core, network-namespace, and hosted CI
checks were not repeated for this CLI-only stage. Profile, connection, and input
semantics remain unchanged.

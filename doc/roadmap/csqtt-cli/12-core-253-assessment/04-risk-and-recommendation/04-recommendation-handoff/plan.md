---
kind: task
status: todo
depends: [01-dependency-and-licence, 02-platform-constraints, 03-security-and-privacy]
---

# Synthesize recommendation and decision handoff

## Task

This is a `lead`-owned documentation-only task after acceptance of the three research
children of the [parent stage](../plan.md). Combine their results with the accepted
provenance, protocol and integrated patch assessments into one concise sanitized
public assessment and user-decision handoff. `lead` writes the public document;
no research coordinator or programmer is assigned merely to compose it, and no new
source audit is required.

Explain concrete client benefits versus the current patched 2.5.0/v0.8.0 product,
not just upstream release claims or server-only improvements. Select and justify a
recommendation to migrate, defer or stay, without executing that choice. Include a
bounded migration estimate grouped by already identified work/dependencies and
later verification, explicit uncertainty and the user choices needed before any
change. The assessment's existing scope, licence treatment and supported platforms
remain fixed.

## Done when

- The public assessment and concise handoff select migrate, defer or stay and trace
  benefits, blockers, dependencies/licences, platforms, security/privacy and evidence
  gaps to accepted research. Source compatibility, binary provenance and deployed
  equivalence remain separate; source metadata is not called build/runtime proof.
- The migration estimate names concrete vendor/provenance, patch/shell/guard and
  notice/platform work in dependency order, distinguishes confirmed work from
  contingencies and unperformed validation, avoids double-counting accepted patch
  entries and states confidence rather than an unsupported precise duration.
- Existing route/proxy, Gooby-label, DNS-wording, captcha-timer and local UDP
  completion limits remain visible without being relabelled as candidate regressions
  or authorization for unrelated repairs.
- Required user decisions about migration and any proposed long-term dependency,
  licence, protocol, feature or platform choice are explicit. Unknown facts remain
  gaps rather than requests to authorize investigation when no change is proposed.
  `lead` can present the recommendation without requiring a new research summary.
- Public text is English and sanitized under `AGENTS.md`; private evidence stays
  ignored. All parent acceptance criteria are covered, and no migration, code/config/
  pin change, build, test, binary/dependency acquisition/execution, live action,
  release or external write is performed.

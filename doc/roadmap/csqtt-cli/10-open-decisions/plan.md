---
kind: stage
status: blocked
---

# Open decisions

Things that wait for the user or for an external event. They are not work: each one
starts moving only after the user answers or the outside condition changes.

## Done when

- Each item is decided by the user, and the decision is either a child node that
  starts work, or a `dropped` item with the reason written down.

## Items waiting

**Core update assessment.** The former wait for core 2.5.2 is superseded by
[stage 12](../12-core-253-assessment/plan.md): assess `Afteroid/csqttr` 2.5.3 after
publishing v0.8.0 on the current core. The assessment must verify compatibility and
the maintenance transition before recommending an update. No migration is approved
by the assessment request; the current pin remains unchanged. Background: journal
`doc/local/plan-core.md`.

**Sending `doc/upstream-proposals.md` to the upstream author.** The document is
written: our core patches worth offering and defects of core 2.5.0 we did not patch.
Sending is the user's decision, and it goes out under an account, which is also the
user's call. Nothing to do until answered.

**Reporting the LaLune and FOCSQ vulnerabilities.** They are recorded in
`doc/local/alternatives.md` and are deliberately not published. Whether to tell the
authors is the user's decision.

**Blocked-on-external, no action needed now:** the fork patch of `go-webview2` (export
of `Chromium` instead of reflection) needs a push by the user; it is listed in
[stage 9](../09-backlog/plan.md) as a candidate.

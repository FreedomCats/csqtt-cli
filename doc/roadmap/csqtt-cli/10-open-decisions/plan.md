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

**Core update assessment (completed).** [Stage 12](../12-core-253-assessment/plan.md)
assessed `Afteroid/csqttr` 2.5.3 and recommends deferring migration while retaining
the patched 2.5.0 core. No migration is authorized; any future migration requires a
separate user decision. The assessment item is complete and is no longer waiting.

**Sending `doc/upstream-proposals.md` to the upstream author.** The document is
written: our core patches worth offering and defects of core 2.5.0 we did not patch.
Sending is the user's decision, and it goes out under an account, which is also the
user's call. Nothing to do until answered.

**Reporting the LaLune and FOCSQ vulnerabilities.** They are recorded in
`doc/local/alternatives.md` and are deliberately not published. Whether to tell the
authors is the user's decision.

**Backlog work order.** Stage 12 recommends retaining core 2.5.0 and deferring
migration. The next eligible backlog groups are live verification
([stage 09/02](../09-backlog/02-live-verification/plan.md)) and tunnel-bypass routing
follow-ups ([stage 09/03](../09-backlog/03-routing-follow-ups/plan.md)). The user must
choose which group starts first; if live verification is chosen, they also select the
first check(s) and are present for them; if routing is chosen, they select the
candidate order. The Windows-only small-defect tasks remain skipped under the current
instruction. No work starts in either group until the choice is recorded.

**Blocked-on-external, no action needed now:** the fork patch of `go-webview2` (export
of `Chromium` instead of reflection) needs a push by the user; it is listed in
[stage 9](../09-backlog/plan.md) as a candidate.

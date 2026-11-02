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

**Core 2.5.2.** The user decided to wait for the release in `Afteroid/csqttr` and to
stay on upstream `d02da9b` (2.5.0) until then. When it is out: pin the commit, run
`vendorcore`, `-check`, `cargo test` and a short live run; if the protocol changed,
`README.md` names the new compatible server version. Background: journal
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

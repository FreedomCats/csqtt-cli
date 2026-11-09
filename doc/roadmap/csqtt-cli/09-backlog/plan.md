---
kind: stage
status: in-progress
base: cc183a4
---

# Backlog

Everything that is known to be undone but is not a stage of its own: small defects
found while working, features of core 2.5.0 never verified live, and follow-ups of
tunnel bypass routing. The candidates are preserved in the child nodes below, using
the categories in which they were collected. Their numbering is structural, not a
priority order. The user selected small defects first and authorized continuing
through that group in map order, skipping Windows-only work until its verification is
available. The core CI failure was fixed in `08-core-ci-stability`; the remaining
small-defect nodes are Windows-only. The user then selected the routing follow-ups with
work order 16 → 14 → 15. The acceptance of that group found one pre-existing upstream
defect: `sync_domain_routing` clears established direct TCP flows and UDP sessions on
`UPDATE_DOMAINS` when only IP routing is enabled. The user chose to add it to the
backlog and fix it; it is the fourth child.

## Done when

- Every candidate in the child nodes is either represented by a child node with an
  outcome, or was moved to
  [stage 10](../10-open-decisions/plan.md) as waiting for the user, or was dropped
  with a reason.
- Nothing in the list is left as a sentence in this node.

## Children

1. [Small defects](01-small-defects/plan.md) — investigate and resolve the collected
   defects, splitting them into independently reviewable tasks after the user chooses
   to start this stage. One candidate requires the user to push a change to a fork.
   Done when every candidate has an outcome and the relevant automated checks pass.
2. [Live verification](02-live-verification/plan.md) — verify the listed 2.5.0
   behaviours with the required user, account, or hardware present. Done when every
   candidate has a recorded outcome; checks that cannot be performed remain explicitly
   blocked rather than being treated as passed.
3. [Routing follow-ups](03-routing-follow-ups/plan.md) — assess and deliver the three
   collected tunnel-bypass follow-ups. The user chose this group with work order 16 →
   14 → 15; it is `done` (see its Outcome). Done when each candidate has an outcome and
   any implemented behaviour passes its build and tests.
4. [Routing update sessions](04-routing-update-sessions/plan.md) — fix the pre-existing
   `sync_domain_routing` behaviour: with domain routing off and IP routing on, an
   `UPDATE_DOMAINS` must not clear established direct TCP flows or UDP sessions; the
   learned live state clears only when the whole routing configuration is off. It is
   `done` in `efbe28a` (П11). Done when the deterministic Rust and Go regressions pass
   and the regenerated core is idempotent.

The live-verification group remains blocked until the user chooses to start it and is
present for the selected checks. The routing follow-ups are `done`; the defect found
by their acceptance is the fourth child and is `done` in `efbe28a`.

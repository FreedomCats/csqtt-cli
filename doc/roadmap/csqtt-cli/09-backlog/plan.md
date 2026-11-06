---
kind: stage
status: in-progress
base: cc183a4
---

# Backlog

Everything that is known to be undone but is not a stage of its own: small defects
found while working, features of core 2.5.0 never verified live, and follow-ups of
tunnel bypass routing. The candidates are preserved in the three child stages below,
using the categories in which they were collected. Their numbering is structural, not
a priority order. The user must choose which child stage to start, and the order of
work within it, before work begins.

## Done when

- Every candidate in the child stages is either represented by a child node with an
  outcome, or was moved to
  [stage 10](../10-open-decisions/plan.md) as waiting for the user, or was dropped
  with a reason.
- Nothing in the list is left as a sentence in this node.

## Child stages

1. [Small defects](01-small-defects/plan.md) — investigate and resolve the collected
   defects, splitting them into independently reviewable tasks after the user chooses
   to start this stage. One candidate requires the user to push a change to a fork.
   Done when every candidate has an outcome and the relevant automated checks pass.
2. [Live verification](02-live-verification/plan.md) — verify the listed 2.5.0
   behaviours with the required user, account, or hardware present. Done when every
   candidate has a recorded outcome; checks that cannot be performed remain explicitly
   blocked rather than being treated as passed.
3. [Routing follow-ups](03-routing-follow-ups/plan.md) — assess and deliver the three
   collected tunnel-bypass follow-ups. Done when each candidate has an outcome and any
   implemented behaviour passes its build and tests.

These groups retain the original candidate categories, not a selected execution
priority. The user must choose the stage and work order before any of these candidates
is started.

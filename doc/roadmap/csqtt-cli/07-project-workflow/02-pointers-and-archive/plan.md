---
kind: task
status: done
detailed: true
---

# Pointers from `README.md` and the architecture; `doc/plan.md` as an archive

Three documents still present `doc/plan.md` as the plan of the work ahead. After the
move the plan of the work ahead is the roadmap, and `doc/plan.md` is history. Readers
of the repository should not be sent to a frozen document for current tasks.

Target files: `README.md`, `doc/architecture.md`, `doc/plan.md`.

## Steps

1. `README.md`, the paragraph near line 1003 that says the architecture is in
   `doc/architecture.md` and the plan in `doc/plan.md` — point at
   `doc/roadmap/csqtt-cli/plan.md` for current work and keep `doc/architecture.md`.
   Mention `doc/plan.md` only as the record of completed work, if at all.
2. `doc/architecture.md`, § header block (line ~20 inside the Russian text) — already
   done: it now points at the roadmap root and calls `doc/plan.md` the record of the
   path before the move. Review the wording; do not translate surrounding sections.
3. `doc/architecture.md` §12 (the Russian heading for the stage overview) — already
   done: one sentence now says the live tree, order and status live in the roadmap.
   Review it. Do not duplicate the tree here.
4. `doc/plan.md` — a short note under the title, in English: this is the historical
   record of the step-by-step plan up to the move to `doc/roadmap/`; it is not updated
   any more; current work is in the roadmap root. No other content changes.
5. Check that no other document or source comment sends a reader to `doc/plan.md` for
   current work: `grep -rn 'plan\.md' --include='*.md' --include='*.go' .` outside
   `core/` and `build/`, and fix what turns up.

## Done when

- `README.md` and `doc/architecture.md` refer to `doc/roadmap/csqtt-cli/plan.md` for
  the work ahead.
- `doc/plan.md` carries the archive note at its top and is otherwise unchanged
  (`git diff --stat` shows a handful of added lines).
- No live document points at `doc/plan.md` as the current plan.

## Outcome

`README.md` and `doc/architecture.md` point to the roadmap for current work;
`doc/plan.md` is marked as a frozen historical record. The project-wide search found
no live document presenting `doc/plan.md` as the current plan.

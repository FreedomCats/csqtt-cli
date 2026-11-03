---
kind: stage
status: done
base: 5c7585f
---

# Project workflow: roadmap, rules and language

The project moves from one flat plan (`doc/plan.md`) to the roadmap scheme of
`doc/roadmap/`, and sets the language of its documents: English for everything new,
Russian left untouched in what is already written.

Goal: from now on the work is chosen, detailed and executed through the roadmap, and
the project rules say so plainly — a fresh session finds the map, knows where plans,
journals and continuation notes live, and knows that new documents and commits are in
English.

## Done when

- `AGENTS.md` names `doc/roadmap/` as the place for plans, records the language rule
  (documents, new code comments, project memory, commit subject and body — in
  English; existing Russian documents are not retranslated), and keeps
  `doc/roadmap/*/journal*.md` out of the repository.
- `.gitignore` ignores roadmap journals; `git status` stays clean after `lead`
  writes to one.
- `CLAUDE.md` points at `doc/roadmap/README.md`, and `README.md` and
  `doc/architecture.md` §12 refer to the map instead of `doc/plan.md` for the work
  ahead.
- `doc/local/resume.md` is reduced to rules, environment and pointers — no
  session-by-session history, which now lives in the map and its journal.
- `doc/plan.md` stays as the frozen historical record, marked as such at its top.

## Stages

1. Rules and language in `AGENTS.md`, `.gitignore`, `CLAUDE.md`.
2. Pointers from `README.md` and `doc/architecture.md`; the archive note in
   `doc/plan.md`.
3. A compact `doc/local/resume.md`.

## Outcome

Moved project planning to the roadmap and aligned the repository rules, entry-point
documentation and continuation note with that workflow. Existing Russian documents
remain in Russian, including edits within them; the old `doc/plan.md` remains a frozen
record. Roadmap journal ignore rules were checked for both journal filename forms.
The Claude import was not executed because invoking the external client could send
project content to an external service. Review found and resolved conflicting language
and outcome-writing instructions in the roadmap and continuation note.

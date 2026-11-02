---
kind: task
status: todo
detailed: true
---

# Rules and language in `AGENTS.md`, `.gitignore`, `CLAUDE.md`

The project rules still describe the old scheme: plans and journals belong in
`doc/local/` only, and nothing says where the roadmap lives or which language new
documents use. Without this, a fresh `lead` session will not find the map, and the
journals it writes will show up as untracked files.

Target files: `AGENTS.md`, `.gitignore`, `CLAUDE.md`.

## Steps

1. `AGENTS.md`, section "Documents in `doc/`: what is published and what is not" —
   extend it rather than adding a parallel
   section: state that the development roadmap lives in `doc/roadmap/`
   (`doc/roadmap/README.md` lists the maps, `doc/roadmap/<slug>/plan.md` is a node of
   the tree), and that `doc/local/` keeps the working notes it keeps now — stage
   journals `plan-*.md`, continuation notes, copies of documents before cleaning.
2. `AGENTS.md` — a subsection about roadmap journals: `doc/roadmap/<slug>/journal.md`
   holds "where we left off" and the decision log, names machines, dates and
   environment, and therefore stays out of git, like `doc/local/`. Rule of thumb for a
   committer: anything written by `lead` under `doc/roadmap/` except `journal.md` may
   go into the repository; `journal.md` and `journal-archive-*.md` never.
3. `AGENTS.md` — the language rule, as a new section of its own. New documents, new
   code comments, project memory and commit messages (subject and body) are written
   in English. Documents already written in Russian (`doc/architecture.md`,
   `doc/plan.md`, `README.md`, `doc/upstream-proposals.md`) are not retranslated and
   stay Russian; an edit inside one of them is written in its language, so that no
   document becomes bilingual. The roadmap map and its `journal.md` follow the English
   rule by the `lang: en` field of the root node. `AGENTS.md` itself is the exception:
   the user asked for it to be translated whole, so it is English and from now on is
   edited in English.
4. `AGENTS.md`, section "Commits" — change the body requirement from Russian to
   English, and say it explicitly overrides the global rule in the user's
   `~/.claude/CLAUDE.md` that asks for a Russian body. Keep everything else in that
   section as is: author and committer `FreedomCats`, midnight UTC date, the check of
   `git diff --cached` before committing.
5. `.gitignore` — under the comment about stage journals add
   `/doc/roadmap/*/journal*.md`, with a one-line comment that map journals are working
   notes too. Keep `/doc/local/` and `/doc/runs/` as they are.
6. `CLAUDE.md` — add `@doc/roadmap/README.md` next to the two existing imports, so a
   new session loads the list of maps without being told.
7. Check: `git status` after touching a file under `doc/roadmap/` named `journal.md`
   shows nothing; `claude` started in the repository reads `doc/roadmap/README.md`
   (verify by the absence of an import error, or by `claude --print '/memory'` —
   whichever is available; if neither works, state plainly in the outcome that the
   import was not verified).

## Done when

- `AGENTS.md` names `doc/roadmap/` as the place of the roadmap, keeps `doc/local/`
  for working notes, excludes roadmap journals from the repository, sets English for
  new documents, code comments, project memory and commits, and says that Russian
  documents are not retranslated.
- `.gitignore` ignores `/doc/roadmap/*/journal*.md`; `git status` stays clean with
  such a file present.
- `CLAUDE.md` imports `doc/roadmap/README.md`.
- `git diff` touches only `AGENTS.md`, `.gitignore`, `CLAUDE.md`.

## Outcome

(filled in by the coordinator)

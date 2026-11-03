---
kind: task
status: done
detailed: true
---

# A compact `doc/local/resume.md`

`doc/local/resume.md` is 658 lines and is imported into every session through
`CLAUDE.md`. Most of it is a session-by-session history of what was done, which now
lives in the roadmap, its journal and `doc/plan.md`. The knowledge worth keeping is
the operating rules and the environment: how tests are run, what the two machines
are, how the core is built, what not to read, what a live check costs.

Target file: `doc/local/resume.md`.

## Steps

1. Keep, rewritten in English and shortened:
   - what the project is, in three or four sentences, and where the main documents
     are (`doc/architecture.md`, the roadmap root, that `doc/plan.md` is history);
   - the user's decisions that still hold: purpose (whitelist-only networks),
     name and module path, wrapper plus vendored core, licence split (shell MIT, core
     PolyForm NC, the whole build non-commercial), the update rule for the core;
   - the working order: stage journals in `doc/local/`, outcomes of roadmap tasks in
     their corresponding roadmap nodes; `doc/plan.md` is a frozen historical document
     and is not updated; update `doc/architecture.md` only when recording architectural
     decisions or facts that belong there; commits strictly per `AGENTS.md`, roadmap
     work through `lead`;
   - how to build and run by hand: `go run ./scripts/buildcore`, `go build -o
     build/out/csqtt-cli ./cmd/csqtt-cli`, the core is looked up next to the binary;
   - the test rule that must never be lost: every `go test` and test binary runs under
     `systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0`, and window
     tests additionally under `xvfb-run -a -s "-screen 0 1280x1024x24 -noreset"`
     (two OOM incidents and focus stealing are the reasons);
   - environment: two machines (Windows and Linux) and what differs, where the
     neighbouring repositories are, the WSL notes, the CRLF pitfall of `../csqtt`;
   - what not to read: qWDTT-derived code, Android clients, the C# shell, per
     `AGENTS.md`; and the one-line reminder of how to pass behaviour instead;
   - how to reach the test server (ask the user; nothing in files).
2. Drop everything that is a record of a past session: the blocks starting with
   "Было:", the per-commit lists, the release recaps, the list of "next session,
   ask…" candidates — those are covered by the roadmap and `doc/plan.md`. If a fact
   is load-bearing for future work (a known flaky test, a pitfall), keep the fact in
   one line, not the story around it.
3. Write the result in English. Target length: as short as it can be while still
   letting a new session start without asking the user about any of the above.
4. Check that nothing in the file contradicts `AGENTS.md` or the roadmap root; where
   the rule already exists in `AGENTS.md`, refer to it instead of repeating it.

## Done when

- `doc/local/resume.md` covers every bullet of step 1 and reads in English.
- No history of past sessions remains: no "Было:", no commit lists, no release
  recaps.
- The test rules (`systemd-run` with memory limits, `xvfb-run` for window tests) and
  the ban on reading foreign-licensed code are present verbatim in substance.
- Length is a small fraction of the current 658 lines.

## Outcome

`doc/local/resume.md` is a compact English continuation note retaining the operating
rules, build and test procedures, environment guidance and licensing boundary, without
the former session-by-session history. Its task outcomes now point to the corresponding
roadmap nodes; the frozen `doc/plan.md` is not updated. The note remains ignored by git.

# Project rules for agents

## Personal data — never into the repository

**This is about the developer's safety, not about tidiness.** If the developer is
identified, it threatens his life, health and freedom. So nothing that can identify him
may reach the repository: real name, e-mail, IP addresses, profile names, employer,
call hashes, details of the working environment and OS, user name — in short, any
detail by which he is easy to find. When in doubt — do not record it.

The repository is public. Into documentation (`README.md`, examples), code, tests,
comments and commit messages one **must not** write:

- the user's real name, his e-mails (personal and work), the name of his personal GitHub
  account and other accounts, his employer;
- the developer's environment: the distribution and the machine the work is done on, the
  user name, groups, home directories (`/home/…`, `C:\Users\…`), working networks;
- dates and times of work: when something was done, checked, verified;
- the user's external IP addresses: home and VPS;
- the name and address of the CSQTT server (domain, IP, port);
- home network addresses (ULAs, local machine addresses) and corporate network ones
  (corporate VPN server, corporate DNS, search domains);
- VK call hashes and call links — neither whole nor shortened as the log prints them
  (`abcdEF-1…`); VK tokens, `csqtt://` links and their parts;
- the user's profile names. The exception is `home`: it is also the example name in
  code, tests and `README.md`, and may stay;
- passwords, keys and any values from the user's secret and profile files.

In their place — words or placeholders: "the VPS address", "the home address", "the
CSQTT server", `<profile>`, `<hash>`, `10.x`, `192.168.1.x`, `fd…`, user `user`. In
tests — documentation addresses (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`,
`2001:db8::/32`) and made-up values.

Showing these values to the user in chat is fine. Into repository files — no.

## Documents in `doc/`: what is published and what is not

The repository holds everything that lies directly in `doc/`. All the rules above apply
to those documents: no dates of work, no time of day ("at night", "late"), no
environment or machine, no names and paths — only the substance. Outcomes of live checks
may be written — without environment, channel speed or addresses; the work log by day
goes into journals only.

Out of git (`.gitignore`) — two subdirectories:

- `doc/local/` — stage journals `plan-*.md` (they inevitably hold environment and
  dates), working notes (the continuation point for new sessions) and copies of
  documents before cleaning;
- `doc/runs/` — scripts and logs of live runs.

Check a new document against this file before `git add`; a draft with environment goes
into `doc/local/`.

## Roadmap

The plan of work is a tree of stages and tasks in `doc/roadmap/<map>/`: `plan.md` is a
node, nested directories with `plan.md` are its children, the order is set by `NN-`
prefixes. The list of maps is `doc/roadmap/README.md`; they are driven by the agent
`lead`, nodes are detailed by `planner`, tasks are executed by `coordinator`.

- A map covers a project or a large feature. There is one now: `doc/roadmap/csqtt-cli/`
  — the main roadmap of the project.
- `doc/local/` stays for stage journals (`plan-*.md`), working notes and drafts: the map
  holds only the goal, the steps and the outcome, not the work log.

**A map's `journal.md` is not in git.** It holds "where we left off" and the decision
log: machines, dates, working-tree state — things that must not be in the repository.
Like `doc/local/`, it is in `.gitignore` (`/doc/roadmap/*/journal*.md`, and
`journal-archive-*.md` as well). Everything else under `doc/roadmap/` — node `plan.md`
files and `README.md` — is published.

## Language

The language of the project is English. English is used for:

- new documents, including the map and its `journal.md` (`lang: en` in the root);
- new comments and messages in code, output lines and help texts;
- project memory (`~/.claude/projects/…/memory/`) and working notes;
- commit messages — both the subject and the body.

What was written in Russian does not get retranslated: `doc/architecture.md`,
`doc/plan.md`, `doc/upstream-proposals.md`, `README.md`, the older stage journals in
`doc/local/` — that is the history of the project as it was written. They stay Russian,
and an edit inside one of them is written in its language — otherwise the document
becomes bilingual and hard to read. Everything new is English.

The rule about commit messages overrides the general rule in `~/.claude/CLAUDE.md`,
which asks for a Russian body.

## Foreign code under an incompatible licence

Code under a licence incompatible with the licence of the shell (§11
`doc/architecture.md`) is not opened by the executor — the one who writes code. Today
that is:

- fragments of qwdtt-cli carried over from the qWDTT Android client (GPL-3):
  `internal/core`, `supervisor/restart.go`, `captcha/token.go`, `captcha/solver.go` and
  their tests;
- the qWDTT and CSQTT Android clients in Kotlin (PolyForm NC), and focsq;
- the shell of the third-party client CSQTT-Windows in C# (`windows-client`, PolyForm
  NC).

The core code in `core/` is not covered by this: it is a vendor, reading it is allowed.

If you need to know what such code does, another participant reads it — the coordinator
or a separate agent, who then does not write code himself. He passes the behaviour to
the executor in his own words: which requests and fields, the order of steps, timings,
conditions, observable results. The description does not repeat the structure of the
code, function and variable names, the order of lines; code fragments, even retold, are
not passed on. Protocols, formats and behaviour are facts; they are implemented anew
(§11).

Best of all — facts from the primary source: a live recording of how a page or a service
behaves, an analysis of responses. Where the description came from (a file of the foreign
project, without paths from the machine) — say in the stage journal (`doc/local/`).

## Commits

Author and committer are only `FreedomCats <noreply@freedomcats.invalid>` (set in the
repository's `.git/config`). The date is today's, but without the time: midnight UTC.
Time of day and time zone never reach the history:

    d=$(date +%F)T00:00:00Z
    GIT_AUTHOR_DATE=$d GIT_COMMITTER_DATE=$d git commit …

The `.invalid` domain is reserved (RFC 2606): nobody owns such an address, and GitHub
will not attribute the commits to someone else's account.

The same applies to `--amend`, `rebase` and `cherry-pick`: git puts the current time as
the committer time.

Release tags (`v*`) are light only: `git tag v0.1.0`, without `-a`, `-s` and `-m`. An
annotated tag stores the name, e-mail and date of whoever created it.

**Before every commit** check `git diff --cached` against everything listed above:
addresses obtained in live runs, values from the csqtt-cli settings directory, the
server name, hashes, tokens, paths and names from the developer's machine, dates. After
the commit — `git log -1 --format='%an <%ae> %ad | %cn <%ce> %cd' --date=iso-strict`.
The forbidden values themselves are never written into this file or into check scripts.

If something like that still reached the history — tell the user at once. Cleaning the
history (`git filter-repo`) rewrites every commit hash and is done only with the user's
consent.

The commit subject and body are in English (see «Language»). Say what changed and why,
so that a colleague understands the change without opening the diff; do not retell the
diff line by line.

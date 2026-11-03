---
kind: root
status: todo
lang: en
source: doc/architecture.md
branch: main
base: 5c7585f
---

# csqtt-cli roadmap

The main roadmap of the project. Architecture and its decisions (Р1–Р8, core patches
П1–П7) live in [`doc/architecture.md`](../../architecture.md); this map carries the
work: what is done, what is left, and in which order.

Scope: a console client to CSQTT for Windows and Linux — a Go shell plus the vendored
Rust core running as a separate process. Releases up to `v0.7.0` are published.

The map was started when the project moved to the roadmap scheme. Stages 01–06 cover
work that was already finished and are kept as one node each, with a short outcome and
a pointer to the detailed record. Everything still ahead starts at stage 07.

**Conventions of this map.**

- `lang: en` — the map is written in English, as are new documents, new comments in
  code, project memory and commit messages (both subject and body). Existing Russian
  documents (`doc/architecture.md`, `doc/plan.md`, `README.md`) are not retranslated;
  edits inside them are written in Russian.
- [`doc/plan.md`](../../plan.md) is the historical record of the step-by-step plan the
  stages 01–06 came from: per-task "done when" and outcomes. It is frozen, not
  updated. `doc/local/plan-*.md` journals hold the working notes (environment,
  runs, decisions) and stay out of git.
- No Jira: the project is personal and anonymous, so commits carry no task number.
- Commits follow `AGENTS.md`: author `FreedomCats`, midnight UTC date.

## Done when

- Every node of this map is `done` or `dropped`, and the root acceptance pass over
  the whole project has been made against these criteria.
- The project repository documents work as a roadmap: `doc/roadmap/README.md` lists
  the maps, `AGENTS.md` names the roadmap as the place for plans, and `CLAUDE.md`
  points at the map, so a fresh session finds it.

## Stages

### 1. [Foundation](01-foundation/plan.md) — done

Go module, vendoring and building of the core, transferable packages, CI.

### 2. [Connection without privileges — `socks`](02-socks/plan.md) — done

`coreproc`, core patches П1, П2, П5, loopback link, `socks` datapath, profiles,
captcha window, commands.

### 3. [System VPN on Windows — `tun`](03-windows-tun/plan.md) — done

Wintun, patch П3, routes, DNS, kill-switch, network watching.

### 4. [System VPN on Linux — `tun`](04-linux-tun/plan.md) — done

Patches П4 and П6, TUN file descriptor, `SO_MARK`, nftables, `sudo`.

### 5. [Interactive mode and first release](05-interactive-release/plan.md) — done

Menu, profile wizard, `csqtt://` links, release archives, licence, publication.

### 6. [`auto_js` mode](06-auto-js/plan.md) — done

VK login in a window, token in secret storage, the core creating the VK call.

### 7. [Project workflow: roadmap, rules and language](07-project-workflow/plan.md) — done

Move the project onto this scheme: roadmap in `doc/`, rules in `AGENTS.md`, a
compact continuation note, links from `README.md` and the architecture.

### 8. [Network switch — measurement and decision](08-network-switch/plan.md) — todo

W6: a live measurement of Wi-Fi ↔ Ethernet switching, then the choice between
`BIND_IF` and a restart of the core, recorded in §06 of the architecture.

### 9. [Backlog](09-backlog/plan.md) — todo

Small defects found while working, the 2.5.0 features never verified live, and
follow-ups of tunnel bypass routing. To be detailed by `planner` when reached.

### 10. [Open decisions](10-open-decisions/plan.md) — blocked

Items waiting for the user or for an external event: core 2.5.2, sending
`doc/upstream-proposals.md` to the upstream author, whether to report the LaLune and
FOCSQ vulnerabilities.

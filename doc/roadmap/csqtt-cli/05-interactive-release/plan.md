---
kind: stage
status: done
base: f758980
depends: [03-windows-tun, 04-linux-tun]
---

# Interactive mode and first release

The project becomes usable by someone who did not build it: a menu when started
without arguments, a profile wizard, `csqtt://` share links, release archives, a
licence, and the first public publication.

## Done when

- Running `csqtt-cli` with no arguments opens a menu; the wizard creates a profile
  from a `csqtt://` link; the same works on Linux and by a double click on Windows.
- `release.yml` builds archives for Linux `amd64`/`arm64` and Windows `amd64` with
  `SHA256SUMS.txt`; `distcheck` verifies composition and sums.
- Licence and third-party notices are in place, the repository is public, and the
  first release is published.

## Outcome

Tasks I1–I5 of `doc/plan.md` §Э4, split into I1a/I1b and I3a/I3b along the way. Menu,
wizard and profile editing, privilege elevation, `README.md` and an example config,
release scripts and workflow, MIT licence for the shell with the core's PolyForm
Noncommercial terms kept apart, publication (tag `v0.1.0`). Detailed record:
`doc/plan.md` §Э4, journal `doc/local/plan-e4.md`.

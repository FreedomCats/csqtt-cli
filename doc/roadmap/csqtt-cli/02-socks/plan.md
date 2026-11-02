---
kind: stage
status: done
base: 3e91f68
depends: [01-foundation]
---

# Connection without privileges — `socks`

Core process control, secret transfer, loopback link, a `socks` datapath, profiles
and secrets, the captcha window, and commands — enough to browse through the server
without administrative rights, on both Windows and Linux.

## Done when

- `csqtt-cli connect` with a `socks` datapath brings up a tunnel in one to two
  seconds, and a browser configured for the local SOCKS5 reaches the network with the
  server's address.
- The core receives the password and VK hashes not through command-line arguments.
- Captcha is solved in a window, and a tunnel comes up after it.
- Live acceptance passed on a real server on Linux and on Windows.

## Outcome

Tasks C0–C11 of `doc/plan.md` §Э1. `internal/coreproc` (start, events, commands,
secret transfer via stdin — patch П1), loopback link with a pinned sender (П2), a
manual captcha window of 180 seconds (П5), `datapath.Socks`, `config` profiles with
secrets in the OS store, `internal/supervisor` with restarts, `internal/cli`.
Detailed record: `doc/plan.md` §Э1, journal `doc/local/plan-e1.md`.

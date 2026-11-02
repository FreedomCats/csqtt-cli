---
kind: stage
status: done
base: ec59340
depends: [02-socks]
---

# `auto_js` mode

A mode where the core creates the VK call itself: the user logs in to VK once in a
window, the token is stored in the OS secret store, and the core receives it through
`VK_JS_BOOTSTRAP:` instead of taking call links from the profile.

## Done when

- `csqtt-cli vk login | logout | status` works, a warning about the risk for the VK
  account precedes the login, and the token never reaches files or logs.
- `connect` on a profile with `vk_hash_mode = auto_js` raises a tunnel in `socks`
  and in `tun`, masks secrets of the guest path, cleans up the VK call on exit, and
  retries temporary VK API errors.
- Live acceptance with the user's own VK account passed.

## Outcome

Tasks A1–A3 of `doc/plan.md` §Э5, split into A3a/A3b. Behaviour is described in
`doc/architecture.md` §08а. Login through a WebView2 or a browser under control,
token in secret storage, bootstrap into the core, call cleanup confirmed by the
`N/N` outcome, retries of VK API codes 1/6/10 before `PROCESS`, a domain filter for
the login window (`vk login -net-allow`). Detailed record: `doc/plan.md` §Э5,
journal `doc/local/plan-e5.md`.

Work after that up to `v0.7.0` went outside the stages (core 2.5.0, tunnel bypass
routing, captcha auto-solver) and is recorded in `doc/plan.md` §Вне этапов and in the
journals `doc/local/plan-core.md`, `plan-routing.md`, `plan-captcha.md`.

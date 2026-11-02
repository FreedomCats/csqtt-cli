---
kind: stage
status: done
base: 5f4906e
depends: [02-socks]
---

# System VPN on Windows — `tun`

A system-wide VPN on Wintun: packets go through the shell to the core over loopback,
direct sockets of the core are bound to the physical interface (patch П3), routes,
DNS with changes on the fly, DNS protection and a kill-switch.

## Done when

- `connect -datapath tun` raises a Windows tunnel without `-allow-leak`, and the
  external address is the server's.
- System resolvers of the router and third-party resolvers on :53 are unreachable
  while the tunnel works; the kill-switch closes traffic between attempts, and a
  captcha in a window can still be solved.
- Network changes are watched; a lost adapter ends the session with an error instead
  of a silently "working" tunnel.
- Live acceptance passed on Windows.

## Outcome

Tasks W1–W8 of `doc/plan.md` §Э2. `datapath.Tun` on Wintun with a constant GUID and a
bridge to loopback, patch П3 in 18 rules plus two own modules of the core, DNS
protection and kill-switch with two WFP app IDs, `Tunnel.Rebind` with the `BIND_IF`
command, an adapter-loss path, Wintun inside a Windows Sandbox. One item stayed open
and moved to [stage 8](../08-network-switch/plan.md): the live measurement of network
switching (W6) that decides whether `BIND_IF` or a restart becomes the default.
Detailed record: `doc/plan.md` §Э2, journal `doc/local/plan-e2.md`.

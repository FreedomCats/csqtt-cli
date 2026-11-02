---
kind: stage
status: done
base: fb3e045
depends: [02-socks]
---

# System VPN on Linux — `tun`

A system-wide VPN on Linux: the TUN file descriptor is handed to the core, socket
marking with `SO_MARK`, nftables, system DNS, and `sudo` for the privileged parts.

## Done when

- `sudo csqtt-cli connect -datapath tun` raises a system VPN on Linux, and the
  external address is the server's.
- Kill-switch closes leaks on the physical interface; IPv6 is handled; DNS of the
  system points at the tunnel while it works and is restored afterwards.
- Live acceptance passed in a container with a real server, on a live machine, and
  with a captcha window under `sudo`.

## Outcome

Tasks L1–L4 of `doc/plan.md` §Э3. Patches П4 (`SO_PEERCRED`) and П6 (dropped by
upstream 2.5.0), `datapath.Tun` on Linux with the descriptor passed through
`-tun-uds` on every attempt, `Tunnel.Rebind`, socket marking, nftables kill-switch
and DNS handling. Live acceptance in a container with a real server and on a live
machine. Captcha under `sudo` together with a tunnel was not seen live — the task
moved to [stage 9](../09-backlog/plan.md). Detailed record: `doc/plan.md` §Э3,
journal `doc/local/plan-e3.md`.

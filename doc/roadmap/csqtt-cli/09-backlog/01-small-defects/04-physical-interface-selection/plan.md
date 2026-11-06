---
kind: task
status: in-progress
detailed: true
---

# Physical interface selection

## Task

Prevent Teredo and ISATAP tunnel interfaces from appearing as the physical interface
selected for `netguard`. The Windows route-based selection is in
`internal/winsys/defaultroute_windows.go`; preserve normal physical-interface selection
and the existing IPv4/IPv6 behavior.

## Steps

1. In `internal/winsys/defaultroute_windows.go`, exclude route candidates whose interface
   row has `TunnelTypeTeredo` or `TunnelTypeIsatap` before they can win the metric
   comparison. Keep the existing default-prefix, excluded-LUID, operational-status, and
   metric checks unchanged; do not exclude other tunnel types or ordinary interfaces.
2. In `internal/winsys/defaultroute_windows_test.go`, add deterministic regression coverage
   for both excluded tunnel types and an eligible ordinary interface, using synthetic
   interface metadata or a pure helper used by the selector. Do not derive these assertions
   from the host's adapter or route list. Update the live `GetBestInterfaceEx` comparison if
   needed so it does not expect an intentionally excluded Teredo or ISATAP interface to be
   selected; retain its IPv4 and IPv6 coverage and the existing excluded-LUID coverage.
3. Review `internal/netguard/netguard_windows.go` and
   `internal/netguard/netguard_windows_test.go` to confirm both address families still use
   `winsys.DefaultInterface` and that no `netguard` behavior needs changing. Run the
   relevant Windows `winsys` and `netguard` tests and build the Windows target.

## Done when

- `winsys.DefaultInterface` skips candidates with `TunnelTypeTeredo` or
  `TunnelTypeIsatap` for either address family, while preserving the existing route,
  status, excluded-LUID, and metric selection rules for other interfaces.
- Deterministic tests prove both excluded types are rejected and an ordinary physical
  interface remains eligible; these assertions do not query the live adapter list.
- The existing Windows route-selection tests retain IPv4 and IPv6 coverage and account for
  the intentional Teredo/ISATAP exclusion.
- The Windows target builds, and the relevant Windows `winsys` and `netguard` tests pass.

---
kind: task
status: done
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

## Outcome

- `winsys.DefaultInterface` rejects only Teredo and ISATAP interface rows before metric
  comparison, for both address families. The default-prefix, excluded-LUID,
  operational-status, and metric checks are unchanged.
- Deterministic tunnel-type tests cover both excluded types, an ordinary interface, all
  other defined tunnel types, and an unknown type. The live best-route comparison skips
  intentionally excluded tunnel types and retains IPv4, IPv6, and excluded-LUID coverage.
- Both address families in `netguard` already use `winsys.DefaultInterface`; no
  `netguard` behavior changes were needed.
- Verification passed: Windows CLI cross-build, Windows `winsys` and `netguard` test
  cross-compilation, Windows-targeted `go vet` for both packages, common deterministic
  and full Linux `netguard` test suites, and formatting checks. Windows test binaries
  were not executed; native route-selection and socket-binding checks remain unverified.

---
kind: stage
status: todo
---

# Backlog

Everything that is known to be undone but is not a stage of its own: small defects
found while working, features of core 2.5.0 never verified live, and follow-ups of
tunnel bypass routing. The list below is the raw material collected when the roadmap
was started; `planner` splits it into child tasks when this stage is reached, and the
user picks the order.

## Done when

- Every item below is either a child node with an outcome, or was moved to
  [stage 10](../10-open-decisions/plan.md) as waiting for the user, or was dropped
  with a reason.
- Nothing in the list is left as a sentence in this node.

## Candidates

Small defects, best done as one batch (each is a few lines, but they touch different
places):

- A password containing `|` or `%`: the parser of `csqtt://` links in
  `internal/config/sharelink.go` mis-handles it, and the server splits `GETCONF` by
  `|`.
- `storeProfile` writes secrets before `Save`: a failure in between leaves a new
  password with an old `peer` (affects both the wizard and `profile set`).
- The hint "server is silent" names only the core version as a reason — a wrong
  password looks the same.
- The wizard says "administrator rights are needed" even when it has them.
- `-link < file` does not work in PowerShell; the help text should show the pipe form.
- Teredo/ISATAP appear in the `netguard` interface choice.
- Long lines are broken mid-word in the Windows console.
- `GOARCH=386 go vet` fails in `monitorDPIAt`.
- Access to `Chromium` in the `go-webview2` fork is done by reflection; a proper
  export means patching the fork, which the user pushes.
- `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` is shared between windows.
- `req.Deadline` is not refreshed when `redirect_uri` is retried.

Never verified live (needs the user, a VK account or particular hardware):

- Captcha under `sudo` together with a tunnel on Linux.
- Wayland, GNOME and KDE for the captcha window; a captcha slider.
- Several monitors with different scaling.
- The UAC elevation prompt.
- Import of a share link from the real panel.
- VK error codes 1/6/10, a revoked token, the "server limited the number of workers"
  line.
- The core's `WORKERS` line with a real per-password limit.

Follow-ups of tunnel bypass routing (candidates from `doc/upstream-proposals.md`):

- Direct traffic in Linux `tun` without workers — the core drops it.
- Fall back into the tunnel when a direct connection fails.
- A separate counter of direct traffic.

## Notes

- Priority and grouping are the user's choice when this stage is reached; the list
  above is not ordered.
- Items of the "never verified live" group need the user present and are not code
  changes; they may become one task with a checklist, or stay separate — decide when
  splitting.

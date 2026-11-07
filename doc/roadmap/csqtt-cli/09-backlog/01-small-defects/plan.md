---
kind: stage
status: in-progress
base: cc183a4
---

# Small defects

## Goal

Resolve the small defects collected while working on the project. The user selected
this group and authorized continuing in map order, skipping Windows-only work until
its verification is available. The newly reported core CI failure takes precedence
over the remaining candidates. The Chromium export candidate requires a patch to the
`go-webview2` fork that the user pushes.

## Children

1. [Password input rules](01-password-inputs/plan.md) — reject `|` in every
   client-side password input, keep `%` allowed, and cover `%25` share-link decoding.
2. [Profile save consistency](02-profile-save-consistency/plan.md) — prevent a failed
   profile save from pairing an old peer with a newly written password; this affects
   both the wizard and `profile set`.
3. [CLI guidance](03-cli-guidance/plan.md) — correct the server-silence hint, the
   wizard's administrator-rights note, and PowerShell help for `-link`.
4. [Physical interface selection](04-physical-interface-selection/plan.md) — exclude
   Teredo and ISATAP from the `netguard` interface choice.
5. [Windows console wrapping](05-windows-console-wrapping/plan.md) — stop long lines
   from breaking in the middle of words in the Windows console.
6. [32-bit vet](06-goarch-386-vet/plan.md) — fix the `GOARCH=386 go vet` failure in
   `monitorDPIAt`.
7. [WebView2 defects](07-webview2-defects/plan.md) — remove reflective access to
   `Chromium` after the user pushes the fork patch, isolate
   `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` between windows, and refresh `req.Deadline`
   when the same `redirect_uri` is retried.
8. [Core CI stability](08-core-ci-stability/plan.md) — diagnose and fix the reported
   TURN channel-order integration-test failure before resuming the remaining work.

## Done when

- Every candidate is resolved, explicitly deferred, or dropped with a reason recorded
  in the outcome of its representing task or child stage.
- Relevant Go builds, tests, and vet checks pass for the tasks that are implemented.
- The Chromium export change, if selected, is integrated only after the user pushes the
  fork patch.

## Password protocol decision

The user chose to reject `|` in client-side password inputs and keep `%` allowed. This
avoids a server protocol change. Research of FOCSQ 1.0.0 and CSQTT for Windows 3.13.1
found that both decode percent-encoded passwords in open links (`%25` represents `%`,
`%7C` represents `|`), but literal characters were not tested. No compatible escaping
for `|` at the protocol boundary was found. `%` appears compatible as a literal, but
this was not verified with a live connection. Keep these limits in mind when detailing
the implementation and tests.

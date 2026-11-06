---
kind: stage
status: todo
---

# WebView2 defects

## Goal

Resolve the three independent WebView2 defects in the Windows captcha and VK login
windows: remove reflective access to the fork's `Chromium` object, prevent the
process-wide browser arguments environment variable from leaking between windows, and
refresh a reused captcha request's deadline. The `Chromium` export cannot be integrated
until the user pushes the required patch to the `go-webview2` fork.

## Candidates

- Replace reflective access to the fork's `Chromium` object with its exported API. This
  is externally blocked until the user pushes the required patch to `go-webview2`.
- Prevent `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` for one window from affecting another.
- Refresh `req.Deadline` when the same `redirect_uri` is retried and its window is reused.

## Done when

- Every candidate has an outcome; the Chromium export remains explicitly blocked until
  the user pushes the fork patch and is integrated only against that pushed change.
- Implemented changes have regression tests, and the relevant Windows captcha tests and
  builds pass.

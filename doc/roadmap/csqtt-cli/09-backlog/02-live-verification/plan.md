---
kind: stage
status: blocked
---

# Live verification

## Waiting for the user

The user must choose whether to start this group and which live candidates to perform
first. Until then, do not request credentials, use an account, or run a live check.
Unselected and unavailable checks remain unverified.

## Goal

Verify the listed behaviours of core 2.5.0 and the client in live conditions that need
the user, a VK account, or particular hardware. No candidate is considered verified
without the required live check. The user must choose the order and be present for the
selected checks; unavailable prerequisites leave the relevant work blocked.

## Candidates

- Captcha under `sudo` together with a tunnel on Linux.
- Wayland, GNOME and KDE for the captcha window; a captcha slider.
- Several monitors with different scaling.
- The UAC elevation prompt.
- Import of a share link from the real panel.
- VK error codes 1/6/10, a revoked token, the "server limited the number of workers"
  line.
- The core's `WORKERS` line with a real per-password limit.

## Done when

- Every candidate has a recorded outcome from its required live check, or remains
  explicitly blocked with the missing prerequisite identified.
- No blocked or unperformed check is reported as passed.

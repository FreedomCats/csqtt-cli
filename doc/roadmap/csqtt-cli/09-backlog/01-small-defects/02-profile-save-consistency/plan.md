---
kind: task
status: todo
---

# Profile save consistency

## Task

Make profile persistence leave the existing peer and password consistent when saving a
profile fails. The current `storeProfile` flow writes secrets before `config.Save`, so a
failure can leave the old profile pointing at a newly written password. Cover both the
menu wizard and `profile set`; see `internal/cli/profile.go` and the profile persistence
contract in §10 of `doc/architecture.md`.

## Done when

- A regression test forces the profile-file save to fail after a password change and
  verifies that the previously saved peer and password remain usable together.
- The failure path is covered for both wizard-based profile replacement and `profile
  set`, and successful profile saves continue to store the requested peer and password.
- Relevant CLI and config tests pass, with no secret value included in failure output.

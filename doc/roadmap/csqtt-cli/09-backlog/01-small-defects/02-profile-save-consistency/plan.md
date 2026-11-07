---
kind: task
status: done
detailed: true
---

# Profile save consistency

## Task

Make profile persistence leave the existing peer and password consistent when saving a
profile fails. The current `storeProfile` flow writes secrets before `config.Save`, so a
failure can leave the old profile pointing at a newly written password. Cover both the
menu wizard and `profile set`; see `internal/cli/profile.go` and the profile persistence
contract in §10 of `doc/architecture.md`.

## Steps

1. Make the `storeProfile` operation transactional at its existing CLI boundary in
   `internal/cli/profile.go`. Preserve the pre-update profile references and the old
   values for secret records that this call may overwrite before calling `Put`; keep the
   existing stable per-profile secret names and reference format. If the profile-file
   save fails, restore the previous secret records and the in-memory profile/file state
   before returning the save error. Rollback failures may add operation context, but must
   not include any secret value. Keep the existing deferred deletion of a superseded
   server VK token after a successful file save, and do not alter other persistence
   transactions or introduce a new storage format.
2. Add a narrow save seam to the command environment in `internal/cli/root.go` and the
   test environment in `internal/cli/main_test.go`: production must default to
   `config.Save`, while tests can make the profile-file save fail deterministically after
   the secret `Put`. Use this seam only from `storeProfile`; leave unrelated
   `config.Save` callers unchanged. Extend the in-memory fake store only as needed to
   observe restoration, without printing stored values in failures.
3. Add failure-path regressions in `internal/cli/profile_test.go` for `profile set`: start
   with an existing profile, request a new peer and password, force the injected save
   failure, and verify that the on-disk profile still has the old peer and that its saved
   password reference returns the old password. Assert that the command output and test
   diagnostics do not expose either password. Add a success case for the same peer and
   password update, verifying the requested values are persisted and the existing success
   messages remain secret-free.
4. Add the corresponding wizard replacement regression in `internal/cli/menu_test.go`:
   replace an existing profile through the menu wizard with a new link/password and peer,
   force the same post-`Put` save failure, and verify that the old peer and password remain
   usable together after the menu continues. Cover a successful wizard replacement as
   well, including preservation of the established profile behavior such as the existing
   device id where applicable. Keep synthetic secret inputs out of `t.Errorf` arguments
   and assert that stdout, stderr, and prompts contain no secret values.
5. Format the changed Go files with `gofmt`, then run the focused CLI and configuration
   tests and the full Go suite under the mandatory memory limit. Since the full suite
   includes browser-window tests, run it under Xvfb with `-noreset` as prescribed by
   `doc/local/resume.md`; also run `go build ./...` and `go vet ./...`.

## Done when

- `storeProfile` cannot leave the persisted old profile paired with a newly written
  password when its profile-file save fails: the previous secret and profile state are
  restored, and rollback errors contain no secret values.
- Regression tests cover a forced post-`Put` save failure for both wizard-based profile
  replacement and `profile set`, and verify that the old peer and password remain usable
  together on disk.
- Successful peer/password updates through both routes still persist the requested peer
  and password, preserve existing profile behavior and user-facing success semantics, and
  do not change the secret-storage format or unrelated transactions.
- The new tests and all relevant CLI/configuration tests pass; stdout, stderr, prompts,
  and test failure diagnostics contain no supplied secret value.
- `go build ./...` and `go vet ./...` pass. Go tests are run with
  `systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0`; the full suite is
  additionally run through `xvfb-run -a -s "-screen 0 1280x1024x24 -noreset"` with the
  prescribed timeout.

## Outcome

Implemented transactional profile persistence at the existing `storeProfile` boundary.
The operation now preserves the old profile state and every affected password, VK hash,
and server-token destination before writing. Rollback restores only the destinations
owned by the operation, preserves stable references and backend identity, detects
observable destination conflicts, keeps independent secret records, and aggregates
secret-free rollback context. Superseded server VK tokens are still deleted only after
the profile file has been saved successfully.

Added the narrow profile-save test seam, production fallback to `config.Save`, and
regressions for `profile set` and wizard replacement success and failure paths. The
tests also cover missing records, mismatched profile and index references, backend
migration, rollback failures, shared destinations, device-id preservation, and
secret-free output and diagnostics. Secret-store reads now distinguish confirmed
missing records from unavailable or damaged storage, so stale references can be
replaced without treating storage failures as absence.

The focused CLI/configuration tests, the required memory-limited full Go suite with
the prescribed window-test display setup, `go build ./...`, and `go vet ./...` pass.

Scoped rollback does not serialize independent writers across processes between its
final conflict check and the merge write. A complete cross-process guarantee would
require a common lock or transaction protocol in every secret-store and keyring
writer, which would change unrelated persistence transactions and is outside this
task. The pre-existing failure mode where a keyring value can change before its
metadata write fails is likewise unchanged.

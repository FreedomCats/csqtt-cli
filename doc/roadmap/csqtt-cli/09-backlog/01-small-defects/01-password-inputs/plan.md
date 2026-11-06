---
kind: task
status: todo
detailed: true
---

# Password input rules

## Task

Reject `|` in every client-side password input. The user explicitly chose to keep `%`
allowed; do not revisit that decision or coordinate a server protocol change. Share-link
parsing must preserve a literal percent sign encoded as `%25` and continue decoding
percent-encoded values correctly. See the password protocol decision in the parent
[`Small defects` stage](../plan.md), and the share-link and profile contracts in §10 of
`doc/architecture.md`.

## Steps

1. Add or reuse one password validation rule in `internal/config/sharelink.go`, exposing
   it to `internal/cli` as needed, and apply it to both imported links and manually
   entered passwords. Cover the manual input paths used by `profile add`, `profile set
   -password`, the menu wizard, and the menu password replacement (`internal/cli/profile.go`,
   `internal/cli/prompt.go`, `internal/cli/wizard.go`, and `internal/cli/menu_edit.go`).
   Reject decoded `|` values from both current and legacy share links; do not change the
   server protocol.
2. Add parser regression cases in `internal/config/sharelink_test.go` and
   `internal/config/sealedlink_test.go`: `%25` in a password must produce a literal `%`,
   while `|` is rejected after decoding and malformed percent escapes remain errors.
   Keep `%` accepted by the shared manual-input rule as well; decode the encoded password
   once, not twice.
3. Add CLI tests for every manual password entry route in `internal/cli/profile_test.go`,
   `internal/cli/profile_setlink_test.go`, `internal/cli/menu_test.go`, and
   `internal/cli/menu_edit_test.go`. Verify that rejection is actionable and does not echo
   the password or include it in an error or other output.
4. Format the changed Go files with `gofmt -w internal/config/sharelink.go
   internal/config/sharelink_test.go internal/config/sealedlink_test.go
   internal/cli/profile.go internal/cli/profile_test.go
   internal/cli/profile_setlink_test.go internal/cli/prompt.go
   internal/cli/wizard.go internal/cli/menu_test.go internal/cli/menu_edit.go
   internal/cli/menu_edit_test.go`, then run focused config and CLI tests, `go build
   ./...`, and `go vet ./...`. Run Go tests under the project memory limit. For the full
   suite, use Xvfb because captcha tests open browser windows:

   ```sh
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test ./internal/config ./internal/cli
   systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 \
     xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test ./...
   ```

## Done when

- `|` is rejected for manually entered passwords and for decoded passwords in every
  supported share-link form; rejection does not expose the supplied password.
- `%` remains accepted as a manually entered password character, and `%25` in a share
  link is parsed as the literal `%` without double-decoding.
- Malformed percent escapes continue to be rejected, and no server-side protocol change
  is introduced.
- Focused and full Go tests, `go build ./...`, and `go vet ./...` pass.

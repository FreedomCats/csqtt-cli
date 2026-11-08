---
kind: task
status: dropped
---

# Network switch: live measurement and decision (W6)

The code for switching networks without restarting the core exists (`Tunnel.Rebind`,
the `BIND_IF` command, the hidden `connect -net-switch restart|bindif` flag, default
`restart`), but it was never measured. The decision — whether `BIND_IF` or a restart
of the core becomes the default — belongs in §06 of `doc/architecture.md`, and until
it is made the feature stays a hidden diagnostic flag.

Why it matters: a network change (Wi-Fi ↔ cable, a VPN coming up) is common on a
laptop; a restart tears the tunnel down for seconds and drops every connection,
`BIND_IF` should keep them. The code is ready; only the measurement is missing.

The measurement breaks the network of the machine during the run — it needs the
user's consent and a machine with two networks.

## Steps

1. Read the W6 sections before starting: `doc/plan.md` §Э2 W6 (code, review fixes,
   what stayed open) and journal `doc/local/plan-e2.md` W6.
2. Prepare the measurement script under `doc/runs/` (it stays out of git): bring the
   tunnel up in `socks` first — it does not need root and does not touch routes; then
   in `tun` if the first pass gives a clear answer. For each mode, alternate the
   active interface and record the time until traffic works again, measured by a
   short repeated request through the tunnel.
3. Run the measurement on a machine with Wi-Fi and cable, with the user present and
   having agreed to the network being cut.
4. Compare: time to restore traffic, share of successful restorations, what the user
   sees in the output while it happens. Record the numbers in the journal.
5. Write the decision into §06 of `doc/architecture.md` (in English): which mode is
   the default and why; whether the hidden `-net-switch` flag stays as a diagnostic or
   is removed; if `bindif` becomes the default, the pending core change — patch П3
   should emit a separate event for acceptance and refusal of `BIND_IF`, so a refusal
   is not recognised by the text of the core's log.
6. Update the user-facing output if the default changes: help text of `connect`, the
   description in `README.md` if it mentions network switching, and the profile
   schema if a per-profile setting is wanted (ask the user first — a user-visible
   choice).
7. If the measurement shows neither mode restores traffic reliably, say so and stop:
   that changes the goal of the task and is a question for the user.

## Done when

- A live measurement on a machine with two networks is recorded in the journal, with
  numbers for both modes.
- §06 of `doc/architecture.md` states the chosen default and the reason.
- Tests still pass; if the default changed, the help text and tests reflect it.
- Nothing in the output or the repository names the machine, addresses or dates.

## Outcome

The W6 live measurement is dropped because the required two-network environment is
unavailable and will not be provided. No network-switching behavior was changed:
`restart` remains the default and `bindif` remains a hidden diagnostic flag. The
architecture decision in §06 remains open and must not be inferred from this task.
Do not reopen or ask about this measurement unless the user explicitly changes the
constraint.

---
kind: task
status: done
detailed: true
depends: [01-upstream-provenance]
base: c0ded0414f9dc891667122f0ffc2e68b77a89ebb
---

# Assess technical delta and shell impact

## Goal

Compare the exact csqttr source commit `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`
accepted by the provenance task with the current CSQTT 2.5.0 client pin
`d02da9bd9d8926969b766e197c434d830a83dbd8`. Establish server compatibility from
protocol behavior, not from release labels: the assessed release claims server 2.5.3
and `CSQTT-WIRE-3`, while the client manifest at that exact source still says 2.5.0.
Identify any changed process contract that affects the Go shell. Use primary source
evidence and report unresolved compatibility as unknown; neither a matching version
label nor compilation establishes wire compatibility. Reference architecture §§01,
04, 06, 07, and 10 and the relevant Go integration contracts. This is a focused,
read-only assessment, not a live server check, migration, or implementation task.

Task 01 is accepted and permits this role-appropriate read-only review. The exact
assessed source is the immutable SHA above; the local baseline is `core/CORE_VERSION`.
For incompatible external core source, only the coordinator or a separate
non-coding reviewer may inspect it. Do not give external source, snippets, or a
retelling of its structure to a programmer/executor; pass on behavioral facts in
your own words. Do not open the qWDTT/CSQTT Android clients, FOCSQ, or the
CSQTT-Windows shell. If a behavioral fact from those projects is necessary, leave it
outside this task and ask a coordinator/reviewer to investigate it separately.

## Steps

1. Establish the comparison boundary from `core/CORE_VERSION` and the accepted
   Outcome in [task 01](../01-upstream-provenance/plan.md): local pin
   `d02da9bd9d8926969b766e197c434d830a83dbd8`, assessed source
   `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`, and the recorded client/server
   manifest version difference. Obtain the changed-file inventory and compare only
   the immutable source range between those commits; do not use a moving branch or
   tag, update `core/`, or change the pin. Limit external implementation review to
   changed protocol/client behavior and directly necessary server protocol evidence.
2. Resolve the protocol compatibility question first. From the assessed client and
   shared protocol behavior, and the corresponding server-side protocol evidence at
   the same immutable source when available, trace version negotiation, handshake
   request fields and response/error forms, wire markers or packet/configuration
   formats, and worker negotiation. Compare each changed behavior with the local
   baseline in `core/rust-client/protocol.rs`, `core/shared/wire_protocol.rs`, and
   the directly affected local files. State whether the server claimed as 2.5.3 /
   `CSQTT-WIRE-3` is compatible, incompatible, or unknown for the assessed client;
   explain the evidence and confidence. Do not equate the server and client Cargo
   versions, or infer compatibility from their labels, release notes, hashes, or a
   successful build.
3. For changed client behavior outside the wire handshake, map only the changed
   contract to the shell: startup arguments and environment, initial and subsequent
   stdin records/commands, stdout events and JSON fields, stderr/fatal diagnostics,
   exit codes, ordering, timing, and shutdown. Compare with local
   `internal/coreproc/config.go`, `process.go`, `message.go`, `autojs.go`,
   `hashcheck.go`, and `routing.go`; follow affected messages into
   `internal/supervisor/lifecycle.go`, `internal/cli/connect.go`,
   `internal/cli/output.go`, and `internal/captcha/bridge.go` only where the changed
   contract reaches them. Include profile/config, routing, captcha, and hash-check
   behavior only when the exact source delta affects those paths.
4. Check each identified change against the focused existing contract tests, not by
   running them: `internal/coreproc/config_test.go`, `message_test.go`,
   `process_test.go`, `autojs_test.go`, `routing_test.go`, `hashcheck_test.go`, and
   the directly affected supervisor/CLI integration tests. Record which observable
   behavior each test protects and any untested compatibility risk; do not propose
   tests or implementation changes as though migration had been approved.
5. Give `lead` an evidence matrix with, for each changed area, the immutable source
   evidence, local baseline and Go consumer files, verified behavior or difference,
   shell impact, compatibility classification where applicable, and confidence.
   Separate source facts from inference, cite the exact source SHA and local paths,
   and explicitly retain unresolved items as unknown. Do not make a legal conclusion
   or revisit the existing root-`LICENSE` treatment recorded in Р2. No source from
   other client projects, secrets, environment details, or live-run data belongs in
   the published plan or handoff.

## Done when

- The evidence matrix covers changed client/shared wire behavior and the directly
  necessary server behavior: negotiation, handshake fields and responses, wire
  markers, packet/configuration formats, and worker negotiation. It classifies the
  assessed client against the server claimed as 2.5.3 / `CSQTT-WIRE-3` as compatible,
  incompatible, or unknown, with primary-source evidence and confidence; it does not
  substitute a release claim or client manifest version for observed source behavior.
- Every changed process-contract area is mapped to its local consumer and relevant
  existing tests: arguments/environment, stdin, stdout event names and fields,
  diagnostics and exit behavior, ordering/timing/shutdown, and only those
  configuration, profile, routing, captcha, or hash-check paths affected by the
  exact delta.
- The matrix identifies the exact source SHA and local files, separates facts from
  inference, states user-visible or operational impact, and records unresolved
  evidence as unknown for `lead`'s assessment. It neither changes the existing
  root-`LICENSE` approach nor makes a legal determination.
- No code, test, build, migration, configuration, pin, live server, external client,
  or external service is changed or run; no external client project is opened and
  no source/snippet/structure from incompatible external core is passed to a
  programmer/executor.

## Risks and limits

- The release server version and wire label do not establish the client source's
  protocol behavior. If the relevant server-side source evidence is unavailable or
  does not settle negotiation, report compatibility as unknown and leave migration
  gated; do not fill the gap with a version-label assumption.
- A changed event may be parsed permissively by `coreproc` but still alter supervisor
  state, shell output, timing, or security-sensitive handling. Follow each changed
  field to the actual consumer, especially secrets, captcha tokens, routing data,
  and `auto_js` output.
- The task is source analysis only: it cannot establish compatibility with a
  deployed server or prove a published binary was built from the assessed source.
  No additional user input or live access is needed for this task.

## Outcome

The read-only evidence matrix is preserved in the ignored local assessment record.
At immutable source commit `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`, the checked
client/server handshake and control-wire formats are compatible with the 2.5.0
source baseline: the client protocol and shared wire definitions match, and the
server accepts the current request fields and response forms. This is source-level
compatibility only; whether a published or deployed server binary corresponds to
that source remains unknown.

The assessed client is not a drop-in replacement for the existing Go shell. Its
startup contract lacks the shell's secret-stdin and peer-listener arguments, reads
the password from an environment variable rather than the current `SECRETS:` stdin
record, and expects link hashes by command-line argument. It also lacks the local
`LISTEN` event that supplies the ephemeral UDP port to the Link. Direct substitution
therefore fails before connection setup; removing flags alone would neither preserve
the secret-input contract nor start loopback forwarding.

The exact source also lacks the current P2 peer restriction. Without that filter, a
local process able to reach and probe the loopback UDP socket can retarget subsequent
downlink delivery; this is a local-process risk for Link/UDP datapaths, not evidence
of remote exploitation or a TUN-FD issue. The source does not expose the shell's
bind-interface/mark arguments; socket-by-socket P3 consequences are left to task 03.
Stats fields remain compatible, but unchanged snapshots are emitted less often, so
idle display values can remain stale longer. Diagnostics add endpoint/reason detail
to verbose logs; no new password/token output was found in the reviewed paths.

An independent fresh reviewer confirmed the wire, startup/secret, P2, stats, and
diagnostic findings. A prior reviewer reached the context threshold before full
review and was not resumed. Existing Go contract tests were inspected, not run;
no build, live server, migration, or external client project was used. These are
candidate-migration blockers, not defects in the currently pinned 2.5.0 product.
No code issue was left unresolved due to cost. Task 03 must complete the local patch
and socket-level P3 mapping before the assessment can recommend a migration path.

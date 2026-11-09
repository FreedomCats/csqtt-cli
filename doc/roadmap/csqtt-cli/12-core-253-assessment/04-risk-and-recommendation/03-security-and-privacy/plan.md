---
kind: task
detailed: true
status: done
base: 7bda03700cd307f8b4316efa45b5c67a6bb18a5b
depends: [01-dependency-and-licence]
---

# Assess security and privacy delta

## Task

Produce one bounded differential exposure assessment of the exact B/C client delta
for the [parent stage](../plan.md), using the accepted
[dependency inventory](../01-dependency-and-licence/plan.md), protocol and integrated
patch assessments. Reuse their secret-stdin, peer/FD, interface-binding, captcha and
TURN findings and source limits. Research only remaining changed client integration
and diagnostic behavior relevant to secrets, log content, network destinations and
activation conditions, permissions, persistence and exposed process interfaces.

The starting scope is changed client diagnostic/integration paths in `rust-client/`
(`main.rs`, `gooby.rs`, `stream_diagnostics.rs`, `traffic_diagnostics.rs` and directly
changed callers), mapped to existing `internal/coreproc`, supervisor and CLI
consumers where necessary. A coordinator and independent non-coding source reviewer
bound inspection by the changed-file inventory and accepted facts. Assess the new
dependency/proxy exposure at the client integration boundary; preserve unknown
external routes and opaque internals rather than undertaking a standalone proxy,
native-runtime, archive/Lua sandbox or whole-project vulnerability audit. No feature,
logging policy, permission or security contract is selected or changed.

## Steps

1. Fix the comparison at exact baseline B `d02da9bd9d8926969b766e197c434d830a83dbd8`
   and candidate C `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`. Read the accepted
   protocol, integrated patch, dependency and platform Outcomes plus their ignored
   evidence records. Reuse the established source/binary and licence boundaries,
   process-contract incompatibilities, P1/P2/P3/P4/P5/P7/TURN classifications, lock/
   feature graph and platform gaps. Do not repeat those audits or infer runtime
   behavior from manifest presence.
2. Establish the exact changed-file set for the assessed client source against B.
   Scope source review to changed integration/diagnostic paths in `rust-client/main.rs`,
   `gooby.rs`, `stream_diagnostics.rs`, `traffic_diagnostics.rs` and directly changed
   callers only where needed to establish conditions or observable output. Inspect
   metadata and already accepted facts before opening implementation. The coordinator
   and independent non-coding reviewer alone may read incompatible external source;
   provide only original-worded behavior, never source/snippets/retold structure to
   an executor. Stop and notify `lead` before affected source review if new licence
   evidence changes the read boundary.
3. For each changed integration path, record activation/default/permission conditions,
   new network destination or process interface, persistence, and the relevant local
   shell consumer. Separate endpoint names or log fields explicitly exposed by source
   from what a host route, remote service, feature selection, actual binary or runtime
   would do. For Gooby, assess only the changed client boundary and opt-in conditions;
   preserve the external route and opaque dependency internals as unknown. Do not audit
   Lua sandboxing, the whole proxy, native runtime, advisories or unrelated clients.
4. Reuse accepted process evidence for secret-stdin, peer filtering, FD checks and
   loopback contract, plus the prior bounded verbose-diagnostic finding. Inspect exact
   changed logging/output paths only to determine whether new endpoint/reason detail
   or secret-like material is emitted, under what verbosity, and how it reaches local
   consumers. Do not repeat P1/P2 implementation review. Explicitly distinguish
   demonstrated delta, retained safeguards, existing caveats and plausible unverified
   exposure; absence in inspected paths is not a product-wide safety proof.
5. Preserve the accepted boundaries: protocol source/binary compatibility distinction,
   candidate route/Gooby-label unknowns, stale system-DNS description in architecture
   and socket-guard reason, captcha's independent timers/no request ID/deadline-edge
   success precedence, and bounded local TURN completion without remote receipt. Do
   not call these candidate regressions, change logging or security policy, or propose
   unrelated repairs as though authorized. Name concrete migration gates for the
   later recommendation, not changes to implement here.
6. Record a compact English differential matrix in ignored
   `doc/local/core-253-security-review.md`. Each row identifies the changed path or
   accepted prior evidence, B/C status, trigger/threat condition, secret/log/network/
   permission/process surface, local consumer, evidence versus inference, confidence
   and limit. Include explicit rows for unchanged required safeguards and unknowns
   where necessary; retain the evidence records already accepted. An independent
   non-coding reviewer verifies every new source claim at both exact refs, its
   activation/threat conditions, output/consumer mapping and boundary against primary
   source; record and recheck any correction. Finish with a sanitized Outcome here
   for `lead`, including benefit/exposure balance and recommendation gates.

No programmer is needed. This is a source-only delta assessment, not implementation
or penetration testing. Do not run builds, tests/test binaries, Cargo or vendor/notice
commands, acquire/execute artifacts, access live systems, or write externally. Relay
the parent's future-only Go test limits (4 GiB, no swap, 600 seconds; Xvfb
`-noreset` for window tests) as constraints, not permission.

## Done when

- A differential exposure matrix covers secret handling, diagnostics/logging,
  network behavior, permissions/persistence and process interfaces, either through
  accepted evidence, narrowly verified B/C changes or an explicit evidence gap.
  Activation/default conditions and affected local consumers are stated in the ignored
  matrix with exact refs and local `file:line` sources.
- Demonstrated source changes, unchanged safeguards required by the shell, existing
  limitations and plausible unverified risks have distinct labels. Prior bounded
  logging findings are retained; deeper diagnostic claims require new exact-ref
  evidence. Neither no discovered issue nor a disabled runtime option proves safety.
- Host/proxy-route unknowns, unresolved local Gooby label, stale DNS wording in both
  architecture and guard reason, independent captcha timers/no request ID and the
  bounded local UDP completion contract remain accurately represented. Prior patch
  and socket audits are referenced without repeating them or authorizing repairs.
- An independent non-coding reviewer verifies new source claims and their threat
  conditions at both refs; corrections are recorded and rechecked. An ignored record
  at `doc/local/core-253-security-review.md` and a sanitized Outcome give `lead`
  concrete exposure/benefit findings, confidence, migration gates and limits without
  source snippets or retold implementation.
- The parent's read-only boundary and future-only test constraints are preserved;
  no programmer, implementation change, full external proxy audit, dependency
  download, executable/build/test/resolver/vendor command, live check, external
  write or new security-policy decision is part of the task.

## Outcome

The ignored evidence matrix is recorded in
`doc/local/core-253-security-review.md`. It fixes the exact B/C client inventory
at 37 changed paths and limits runtime review to the assigned integration and
diagnostic boundary. The patched 2.5.0/v0.8.0 product remains distinct from both
unpatched source refs.

Candidate C adds an opt-in Gooby integration and two diagnostic layers. Gooby is
off by default, is not activated by the current Go shell argument builder, and is
connected only to the selected TURN TCP/TLS path at the inspected boundary. The
candidate source validates an explicitly configured proxy as loopback with a
nonzero port and uses a no-auth SOCKS5 exchange. An independent reviewer confirmed
the resulting conditional local-listener trust risk: a process controlling that
port can observe, alter, or interrupt the proxied stream. This is a migration gate,
not a current-product regression; its full treatment would require an authorized
proxy/security contract decision. The external route and opaque Gooby behavior
remain unknown.

Stream diagnostics add endpoint and formatted reason detail to the candidate log
stream when the shell's existing `CSQTT_EVENTS=1` setting enables the event path.
The local shell receives these records as stderr-derived `Log` messages, masks the
known secret classes, and normally displays them only with `-v`. Gooby setup
messages can precede `PROCESS`; session diagnostics are emitted after that event,
but stdout and stderr are consumed on separate goroutines, so their startup-error
capture cannot be ruled out from emission order alone. No explicit password, VK token, or
captcha token field was found in the assigned new payloads, but generic reason
text is not a universal redaction guarantee. Traffic diagnostics require the
separate exact environment value `CSQTT_TRAFFIC_DIAGNOSTICS=1`, which the current
shell does not set; they add passive counters and byte/active summaries without a
new network destination or process interface. C's stats cadence change is a low
impact operational delta and does not establish a security property.

The assessment preserves the accepted P1-P5/P7 and TURN migration requirements,
P6 baseline status, loopback/peer and TUN-FD safeguards, route/proxy unknowns,
stale DNS wording, captcha timer/request-ID/deadline limits, bounded local UDP
completion, dependency/native gaps, platform gaps, and source/binary identity
distinction. Benefit is bounded: the candidate offers an optional integration and
diagnostic visibility that may help a later migration investigation. Exposure and
uncertainty outweigh adoption evidence until the later recommendation has, at
minimum, preserved the accepted shell safeguards, decided the explicit local proxy
trust contract, verified endpoint/reason log handling, resolved the Gooby/native
and route boundaries, and separately validated the candidate binary and targets.
No migration, logging policy, permission decision, route decision, or security
contract was selected here.

The independent non-coding review completed without source corrections after the
conditional no-auth proxy finding was accepted. No user question was needed.
Only read-only source/document/status/privacy/whitespace checks were performed;
no build, test, Cargo, vendor, notice, acquisition, live, browser, proxy, or
external check was run. Future Go tests remain subject to the parent's 4 GiB,
no-swap, 600-second limit, with Xvfb `-noreset` for window tests.

### Real problems not fully corrected because of cost

None. The no-auth local SOCKS5 trust finding above is a genuine conditional
candidate risk, but it is left open because this assessment does not authorize a
migration or a local-proxy trust/security contract decision, not because a repair
was rejected on cost grounds. It remains a recommendation gate. A later authorized
treatment would first decide the trust model and could affect `main.rs`, `gooby.rs`,
`turn_stream.rs`, shell argument/configuration mapping, and focused verification.

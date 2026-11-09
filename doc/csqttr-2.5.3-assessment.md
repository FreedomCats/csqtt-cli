# CSQTT 2.5.3 Migration Assessment

## Recommendation

**Defer migration and keep the patched CSQTT 2.5.0 core used by csqtt-cli v0.8.0.**
The assessment found no demonstrated client benefit that currently outweighs the
integration work and unresolved compatibility, trust, native-build, notice, and
binary-provenance questions. This recommendation is not authorization to migrate or
change the existing release.

The candidate offers an optional Gooby proxy integration and additional diagnostic
detail. The current Go shell does not enable Gooby. Diagnostics may help a later
investigation, but do not by themselves improve connection behavior. No candidate
build or deployed-binary result was established. Keeping 2.5.0 is therefore the
lower-risk choice for the current product; 2.5.3 can be reconsidered if its benefits
become necessary and the gates below are resolved.

## Evidence Scope

This source assessment compares the pinned CSQTT 2.5.0 baseline
`d02da9bd9d8926969b766e197c434d830a83dbd8` with the immutable csqttr 2.5.3 source
commit `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`. The current product is the
locally patched 2.5.0 core in the published v0.8.0 release; neither unpatched source
tree is that product.

The original repository is archived. The maintained repository shares history but
is not marked as a formal GitHub fork. Its 2.5.3 release is mutable and points to an
unsigned commit. The server manifest says 2.5.3, while the client manifest at the
assessed commit still says 2.5.0. These facts establish the assessed source ref, not
that a published or deployed binary was built from it.

Protocol review found source-level compatibility for the checked handshake and
control-wire formats. This does not make the candidate a drop-in replacement for
the Go shell or establish compatibility with a deployed server binary.

## Compatibility and Migration Work

Directly replacing the core is incompatible with the existing process contract. The
candidate does not accept the shell's secret-stdin and peer-listener arguments, uses
a different password input, expects link hashes through command-line arguments, and
does not emit the `LISTEN` event used to configure loopback forwarding. It also lacks
the local P2 peer restriction. Removing arguments alone would not preserve secret
handling, peer isolation, or the Link data path.

The patch review classified P1-P5, defensive P7, and the local TURN UDP completion
correction as still needed for a hypothetical migration. P6 behavior was already
present in the 2.5.0 baseline and has no carried rule. Socket binding, Linux TUN-FD
identity checks, captcha handoff, and TURN shutdown each have distinct shell and
vendor contracts; they cannot be treated as a generic core upgrade.

The vendor inventory has 59 unique rule entries with 61 required matches and nine
added files. Eleven TURN entries are shared with the P3 inventory and counted once.
The candidate's ordinary TURN TCP anchor does not match the existing rule, blocking
that rule and a following import rule. Two P3 anchors depend on earlier P1
insertions. After earlier blockers are fixed, the unchanged socket guard is expected
to encounter seven additional lexical detections across five candidate files. These
are source-level carry findings, not evidence that rules were applied or generated
output was validated. Source URL and client-version provenance also need explicit
migration treatment.

The existing `primp-hyper-util` 0.1.22 archive and its local connector treatment
remain consistent; no connector archive or runtime-helper adaptation was identified.
However, a static dependency graph is not Cargo resolution or build evidence.

## Dependencies, Licensing, and Platforms

The candidate client lockfile adds 26 package identities (25 registry packages and
the `csqtt-gooby` path package) and 73 dependency edges. Existing package versions,
sources, and checksums did not change. Gooby is an unconditional client build
dependency even though its runtime integration is opt-in. Its dependency metadata
introduces vendored LuaJIT and bundled SQLite inputs.

The project retains its existing treatment of the root PolyForm Noncommercial
licence; the Cargo `Unlicense` declarations are a pre-existing metadata conflict.
This assessment makes no legal conclusion and does not infer permission from
manifests. Gooby's licence and notice match its declared upstream revision, but
independent-source claims and full lineage were not verified. LuaJIT carries native
notices distinct from its Rust wrapper. The exact SQLite amalgamation, native
licence applicability, actual shipped native components, and completeness of
collected notices remain unknown. No dependency or licence choice is made here.

The three current release targets are Windows x86-64 MSVC, Linux x86-64 GNU, and
Linux ARM64 GNU. Permitted metadata demonstrated no candidate-specific blocker, but
does not establish successful candidate support. Native compilation requirements,
ARM64 cross-build compatibility, CRT and runtime linkage, glibc constraints for the
candidate, and the final packaged native composition remain unverified. Existing
2.5.0 CI and release results are not candidate validation.

## Security and Privacy

Gooby is off by default and is not enabled by the current shell argument builder. At
the reviewed candidate boundary it is limited to the selected TURN TCP/TLS path and
uses a no-auth SOCKS5 exchange on an explicitly configured loopback port. If this
integration is enabled, a local process controlling that port could observe, alter,
or interrupt the proxied stream. This is a conditional candidate risk, not a finding
about the current product. The proxy's external route and opaque dependency behavior
remain unknown. Enabling or carrying this feature requires an explicit local-listener
trust decision.

Candidate stream diagnostics can add endpoint and reason detail to logs when the
existing event setting is enabled. The shell masks known secret classes and normally
shows such messages only with verbose output, but generic reason text is not a
universal redaction guarantee; separate stdout/stderr consumption also prevents a
strong conclusion about every startup-error ordering. The additional traffic
diagnostics require a separate environment setting that the current shell does not
set. No explicit password, VK token, or captcha token field was found in the reviewed
new diagnostic payloads. These bounded source findings do not prove that every
runtime log is secret-free.

## Migration Sequence and User Decisions

Any future migration first requires the user's explicit authorization. Before then,
the core pin and release stay unchanged. If migration is considered:

1. Decide whether the optional Gooby integration is in scope. If it is retained or
   enabled, decide whether a no-auth SOCKS5 listener on a user-selected loopback
   port is an acceptable trust boundary.
2. Reconcile immutable source provenance, the server/client version discrepancy,
   dependency and native-component metadata, and the licence/notice gaps without
   changing the existing licence boundary by inference.
3. Preserve the shell's secret-input, peer, listener, interface-binding, Linux
   TUN-FD, captcha, and TURN contracts. Adapt dependent vendor anchors and socket-
   guard coverage without assuming an unknown route is safe.
4. Only after those changes, inspect generated output and metadata, resolve locked
   dependencies, and validate the three existing target builds, native packaging,
   process contracts, and appropriate socket and route behavior.

This is a multi-area port rather than a pin-only update. A reliable calendar estimate
would be premature until the Gooby trust decision, provenance and notice gaps, and
native build requirements are settled. No migration, rule execution, Cargo command,
build, test, binary inspection, live check, or external write was part of this
assessment.

The following limitations remain explicit and are not candidate regressions: the
external proxy and host routes are unverified; the local roadmap's "Gooby" patch
label remains unresolved; the system-DNS fallback wording in the architecture and
socket-guard reason is stale; independent captcha timers without request IDs do not
provide a universal late-reply guarantee; and bounded local UDP completion does not
guarantee remote receipt. No current-product correction was deferred because of
cost.

Detailed reviewed evidence is retained in the roadmap Outcomes and ignored local
records under `doc/local/`.

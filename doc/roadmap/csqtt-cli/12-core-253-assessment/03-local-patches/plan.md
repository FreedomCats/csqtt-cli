---
kind: stage
status: done
depends: [01-upstream-provenance, 02-protocol-compatibility]
base: c0ded0414f9dc891667122f0ffc2e68b77a89ebb
---

# Map local patches and migration impact

## Goal

Assess how the exact 2.5.3 source ref affects the project's local core patches and
their Go shell integrations. Use the patch inventory in architecture §07 and
`doc/upstream-proposals.md`, plus `scripts/vendorcore`, `core/CORE_VERSION`, and the
relevant `internal/coreproc`, `internal/datapath`, and `internal/captcha` contracts.
Classify every patch as still needed, equivalent upstream, conflicting, or requiring
investigation. Estimate migration work without applying patches, regenerating
`core/`, or changing the 2.5.0 pin.

## Assessment boundary

- Compare only the pinned 2.5.0 source commit
  `d02da9bd9d8926969b766e197c434d830a83dbd8` with the accepted immutable 2.5.3
  source commit `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`.
- Task 02's accepted Outcome establishes source-level wire compatibility and the
  direct-shell incompatibilities in arguments, secret input, `LISTEN`, and P2. Use
  those findings as evidence; do not repeat that review or reopen the migration
  decision. Complete the still-required socket-by-socket P3 and patch/vendor mapping.
- Only a coordinator or separate non-coding reviewer may read implementation source
  covered by the incompatible-licence rule. Do not give an executor source, snippets,
  or a retelling of implementation structure; report behavioral facts in original
  wording.
- This is read-only assessment. Do not edit code, tests, scripts, configuration, or
  `core/`; do not run vendor generation/checks, builds, tests, or live checks. Keep the
  current pin unchanged and make no legal conclusion from the existing licence
  metadata conflict.

## Children

1. [Assess process and loopback safeguards](01-process-and-loopback-guards/plan.md) —
   map P1 and P2 to exact-source status and Go consumers; reconcile, but do not redo
   task 02's accepted findings.
2. [Assess Linux TUN safeguards](02-linux-tun-safeguards/plan.md) — classify P4 and
   the already-upstream P6 behavior on the Linux TUN-FD path.
3. [Assess socket binding (P3)](03-socket-binding-p3/plan.md) — map all eight P3
   socket sites, direct-routing conditions, platform behavior, and shell safeguards
   socket by socket.
4. [Assess captcha and TURN runtime patches](04-captcha-patches/plan.md) — classify
   the P5 timeout adjustment, P7 solver switch, and local TURN UDP shutdown correction
   as one runtime patch assessment.
5. [Reconcile vendor rules and crate inputs](05-vendor-rules-and-crate/plan.md) —
   reconcile the domain-specific rule-anchor inventories with the 59-rule baseline,
   generated inputs, added files, crate archive, socket guard, and the assignment's
   unresolved "Gooby" label.

## Done when

- A patch-by-patch matrix covers the complete local patch inventory in architecture
  §07 and `scripts/vendorcore`, including П1–П7, added vendor files, the vendored
  `primp-hyper-util` change, and the local TURN UDP shutdown fix. Each classification
  has evidence at the exact source ref and distinguishes behavior already equivalent
  in the current 2.5.0 baseline from changes introduced by 2.5.3.
- Changes to patch anchors, generated files, crate versions, and vendor checks are
  identified, including any case where a patch can no longer be carried safely or
  where source evidence is insufficient.
- Shell and profile impact is mapped to concrete modules: required argument, event,
  stdin, config, routing, captcha, process-lifecycle, or datapath changes and any
  platform-specific work are named rather than described as a generic port.
- Migration work and unresolved items are grouped by dependency/order and handed to
  `lead` as assessment findings. No migration, vendor generation, code/test/config
  edit, build, or external write is performed.

## Outcome

All five children and the independent integration review cover the complete local
patch and vendor inventory at the two fixed source refs. P1-P5, defensive P7 and
the TURN UDP completion correction remain needed; P6 was already upstream at the
pinned baseline and has no carried rule. Concrete consumers are mapped through
`internal/coreproc`, the supervisor, Link and Linux TUN-FD datapaths, routing and
platform guards, and the captcha bridge.

The reconciled inventory has 59 unique rule entries requiring 61 matches and nine
added files without upstream/archive collisions. Eleven TURN entries are counted
once across the P3 and runtime evidence. P1-created P3 anchors are static order
dependencies. The candidate ordinary TURN TCP anchor and following import require
adaptation. Once earlier blockers are repaired, the unchanged socket guard would
encounter seven new lexical detections across five copied candidate files. The
verified connector archive remains `primp-hyper-util` 0.1.22; no archive or runtime
helper adaptation was identified. Source URL and client-version provenance also
need explicit migration treatment.

The ordered hypothetical work preserves the process/secret, peer, platform binding,
captcha and UDP completion contracts before considering generated-output and
platform validation. The local Gooby patch label remains unresolved separately
from the candidate proxy path; its external route is unknown. Raw anchors and a
static dependency graph are not successful rule application, Cargo resolution,
generation, build or runtime evidence. Source-wire compatibility does not establish
the identity or behavior of published/deployed binaries.

Independent integrated review accepted the complete range
`c0ded0414f9dc891667122f0ffc2e68b77a89ebb..366d87ae618b81cb7733e4ee6a5747b6c09f4e53`
against all four original criteria without correction-required findings. Detailed
evidence remains in the ignored local assessment records, including
`doc/local/core-253-patch-acceptance.md`. All public child Outcomes retain the
findings and limits. The stale system-DNS wording, independent captcha timers and
missing request IDs, and bounded local UDP completion without remote-delivery
guarantee remain explicit synthesis caveats.

No code, test, configuration, generated core or pin changed. No rule execution,
vendor command, build, test run, live check, migration or external write occurred.
No current-product correction was narrowed or deferred because of cost. The patched
2.5.0 core remains in place; final cross-cutting risk assessment and the user's
separate migration decision are still required.

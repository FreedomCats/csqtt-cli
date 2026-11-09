---
kind: stage
status: in-progress
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

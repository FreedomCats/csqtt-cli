---
kind: task
status: todo
depends: [01-upstream-provenance, 02-protocol-compatibility]
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

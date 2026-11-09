---
kind: stage
status: done
base: 366d87ae618b81cb7733e4ee6a5747b6c09f4e53
depends: [01-upstream-provenance, 02-protocol-compatibility, 03-local-patches]
---

# Assess release risks and recommend a path

## Goal

Complete the assessment of csqttr 2.5.3 by comparing dependency and licence
metadata, supported build targets, and security/privacy-relevant behavior with the
current pinned core and shell. Synthesize the evidence and migration estimate from
the preceding tasks into a recommendation to migrate, defer, or stay on 2.5.0.
The recommendation is for `lead` to present to the user; it is not approval to
migrate or publish another release. Use architecture §11 and the project's existing
platform/build and privacy constraints. `lead` owns the public assessment text.

## Assessment boundary

- Compare baseline B, `d02da9bd9d8926969b766e197c434d830a83dbd8`, with candidate C,
  `84a2ce84dd4ac0716ceabd1b9a9ae455e6e86806`. The patched 2.5.0 `core/` and
  published v0.8.0 define the current product, not either unpatched source tree.
- Reuse the accepted [provenance](../01-upstream-provenance/plan.md),
  [protocol](../02-protocol-compatibility/plan.md), and
  [integrated patch assessment](../03-local-patches/plan.md) Outcomes and their
  ignored evidence. Do not repeat those audits. Preserve the source/binary
  distinction, direct-substitution blockers, patch classifications, ordered vendor
  blockers, route unknowns and existing DNS/captcha/UDP contract caveats.
- Research is performed by a coordinator with an independent non-coding reviewer;
  no programmer is needed. Only those roles may inspect incompatible external
  implementation, and only for the assigned delta. Do not pass implementation,
  snippets or retold structure to a planner or executor. The first child needs
  metadata only. Other client projects and a standalone external proxy audit remain
  outside this stage.
- Keep the existing root-LICENSE treatment and MIT-shell/PolyForm-NC-core boundary.
  Report missing or conflicting metadata without inferring permission. Materially
  new licence-boundary evidence that changes source-read restrictions must be
  reported to `lead` before further implementation review.
- No migration, pin, code, test, script, build-file, configuration or generated-core
  changes; no rule execution, vendor generation/check, Cargo resolution (including
  `cargo metadata`), builds, tests/test binaries, dependency downloads, binary
  acquisition/execution, live checks or external writes. Existing source objects,
  metadata and read-only primary-source metadata requests are sufficient; unavailable
  facts remain explicit gaps. Public plans and handoffs follow `AGENTS.md`.

## Children

1. [Inventory dependency and licence delta](01-dependency-and-licence/plan.md) — one
   exact-ref metadata inventory of package, source and licence changes, separating
   copied inputs, possible build/runtime use and excluded server/mobile scope.
2. [Compare platform and build constraints](02-platform-constraints/plan.md) — one
   source-based feasibility matrix for the three existing release targets, using
   the dependency inventory without claiming a successful candidate build.
3. [Assess security and privacy delta](03-security-and-privacy/plan.md) — one bounded
   differential exposure assessment of changed client integration and diagnostics;
   reuse accepted safeguards and leave unverified proxy/host behavior explicit.
4. [Synthesize recommendation and decision handoff](04-recommendation-handoff/plan.md)
   — a `lead`-owned documentation task combining accepted research into a concise
   public assessment, bounded migration estimate and required user decisions.

Children 02 and 03 consume child 01's inventory and have separate acceptance;
child 04 consumes all three accepted results. Preserve earlier
ignored evidence files. Each research child supplies a new compact English evidence
record with exact-ref/local `file:line` references, findings versus inference,
confidence, unknowns and independent review, then a sanitized public Outcome in
its own node. `lead` performs the final synthesis; a research coordinator is not
assigned merely to write the public report.

## Future verification constraints

These are requirements to relay to any later authorized verification task, **not
permission to run anything in this assessment**. Every future Go test or test binary
requires a 4 GiB memory limit, no swap and a 600-second timeout; browser/window tests
also require Xvfb with `-noreset` (`doc/local/resume.md`):

```sh
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 timeout 600 go test <packages>
systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 \
  xvfb-run -a -s "-screen 0 1280x1024x24 -noreset" timeout 600 go test <packages>
```

## Done when

- Added, removed, or changed Rust dependencies and their licence/provenance
  implications are inventoried from the exact source manifests and lockfiles;
  unresolved licence conflicts are explicit and not treated as permission.
- Supported operating systems and architectures, required toolchains, and build or
  runtime constraints are compared with the existing Windows/Linux targets. Source
  metadata is not presented as proof of a successful build or platform support.
- Security and privacy findings address relevant changes to secret handling,
  logging, network behavior, permissions, and exposed process interfaces, separating
  demonstrated changes from unverified risk.
- A concise recommendation selects migrate, defer, or stay, cites concrete benefits,
  compatibility blockers, patch/shell work, dependencies, platform and licence
  constraints, and evidence gaps, and names the user decisions still required before
  any migration. The handoff contains sanitized public evidence; no migration,
  code/config change, build/test, or release action is performed.

## Outcome

The completed source assessment recommends deferring migration and retaining the
patched CSQTT 2.5.0 core in v0.8.0. The candidate's optional Gooby integration and
additional diagnostics offer limited investigation value but do not outweigh the
direct shell-contract incompatibilities and integration work. Source-level wire
compatibility is not binary provenance, deployed compatibility or build support.

The dependency inventory records 26 new lock identities and 73 additive edges,
including an unconditional Gooby build dependency; native LuaJIT/SQLite attribution,
SQLite source, shipped composition and notice completeness remain unknown. No
candidate blocker was demonstrated from permitted platform metadata, but builds,
cross-compilation, runtime linkage and packaged native contents remain unverified.
The existing root-LICENSE treatment is retained without legal conclusion.

The conditional no-auth SOCKS5 loopback-listener risk applies only if Gooby is
explicitly enabled; the current shell does not enable it. The public assessment
orders hypothetical provenance/dependency, shell and patch, vendor/guard, and later
target-validation work. Any migration first requires separate user authorization
and a decision on Gooby scope and its local-listener trust boundary. It does not
select new dependency, licence, protocol, feature or platform policy.

Independent integration review accepted all four criteria over
`366d87ae618b81cb7733e4ee6a5747b6c09f4e53..4752be1c4ffaa638e4aab3897c473853f0320bd3`
without correction-required findings. The acceptance record is ignored at
`doc/local/core-253-risk-acceptance.md`; the sanitized public report is
[`doc/csqttr-2.5.3-assessment.md`](../../../csqttr-2.5.3-assessment.md). No code,
tests, Cargo/vendor commands, builds, binaries, live checks, migration or external
actions were performed. The pin, release, supported targets and licence treatment
remain unchanged. No current-product correction was deferred because of cost.

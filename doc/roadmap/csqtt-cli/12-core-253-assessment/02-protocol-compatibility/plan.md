---
kind: task
status: todo
depends: [01-upstream-provenance]
---

# Assess technical delta and shell impact

## Goal

Compare the exact csqttr 2.5.3 source ref accepted by the provenance task with the
current CSQTT 2.5.0 client pin, while respecting any client/server version mismatch
found there. Establish protocol/server compatibility and identify changes to the
process contract consumed by the Go shell. Use primary source and protocol evidence;
do not infer compatibility from matching version labels or successful compilation.
Reference architecture §§01/04 and the relevant `internal/coreproc` contracts. This
is a read-only assessment, not a live server check or migration.

Read implementation only after the provenance task confirms that review may proceed.
The locally vendored core is readable under `AGENTS.md`. For incompatible external
core source, only the coordinator or a separate non-coding reviewer may inspect it;
do not assign that review to a programmer/executor. Pass findings on as behavioral
facts in your own words, not code snippets or retellings of code structure. Do not
open the qWDTT/CSQTT Android clients, FOCSQ, or the CSQTT-Windows shell. If a
behavioral fact from those projects is necessary, hand it to a coordinator/reviewer
to inspect and summarize under the same rules.

## Done when

- A focused evidence matrix compares protocol negotiation, handshake and packet or
  configuration formats relevant to the client, including the fork's client and
  server components. It states the server version requirement as compatible,
  incompatible, or unknown with supporting primary sources, and does not treat the
  release's server version as the client core's version without source evidence.
- Changes to command-line arguments, environment variables, stdin commands, stdout
  events, error/exit behavior, and core configuration are mapped to the shell
  consumers and tests in `internal/coreproc` and related integration modules.
- Findings cite immutable source refs and local files, distinguish source facts from
  inference, and are handed to `lead` for the public assessment. No patch is applied;
  no pin, configuration, test, build, or external service is changed or run.

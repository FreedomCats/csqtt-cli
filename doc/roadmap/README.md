# Roadmaps

Development roadmaps of this project. Each map is a tree of stages and tasks in
`doc/roadmap/<slug>/`; the agent `lead` works through it, `planner` details nodes,
`coordinator` executes tasks.

| Slug | Status | What it is |
|---|---|---|
| [csqtt-cli](csqtt-cli/plan.md) | in-progress | The main roadmap of the project; v0.8.0 is published on core 2.5.0, and the next stage assesses csqttr 2.5.3. |

Layout: `plan.md` is the node itself; child nodes are nested directories with their
own `plan.md`, ordered by numeric prefixes. `journal.md` holds the working log and
stays out of git (`/doc/roadmap/*/journal*.md` in `.gitignore`) — it names machines,
dates and environment, which `AGENTS.md` keeps out of the repository.

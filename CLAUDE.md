# CLAUDE

same as [./AGENTS.md](./AGENTS.md)

## Parallel delivery with Codex CLI (mandatory)

When the user asks for parallel/multi-agent delivery, Claude is the coordinator only: split work, schedule, accept. Claude must not write or edit product code itself, not even small fixes (gofmt, test keys, Makefile entries); route every code change to a Codex worker.

- Keep at most 3 subagents running at once. Each subagent is a driver that runs the Codex CLI and never edits product code itself:
  `codex exec --dangerously-bypass-approvals-and-sandbox -C "<repo root>" -o .temp/lanes/<lane>-last-N.md "<prompt naming .temp/lanes/common.md and .temp/lanes/<lane>.md>" > .temp/lanes/<lane>-codex-N.log 2>&1 < /dev/null`
- Each lane gets a written brief under `.temp/lanes/` with owned paths, ordered tasks and a report file. Shared rules live in `.temp/lanes/common.md`.
- Drivers verify each round (build, tests of touched packages, `./internal/app/`, frontend typecheck), then relaunch Codex with the exact remaining items. They kill only their own Codex process, never by image name.
- Small fixes that Claude finds during acceptance go to a running driver (SendMessage) or a new Codex round, not to direct edits.
- The coordinator runs the final full gates on a stable tree, then updates MEMORY.md and ADR.md.

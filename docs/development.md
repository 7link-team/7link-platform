# Repository development conventions

Project-specific workspace layout, commands and tooling belong here rather than in the reusable agent execution contract.

## Workspace and tooling

**Workspace layout.** `apps/backend-api` is Go. `apps/frontend-web` is a bun + Turborepo workspace (`apps/*` for deployables, `packages/*` for shared code). Run frontend checks from `apps/frontend-web` — `bun run typecheck`, `bun run lint`, `bun run test` fan out to every package. Invoking `tsc` directly from the wrong directory emits `.js` beside the sources and pollutes the tree; if that happens, delete the output before staging.

**New app scaffolds.** `apps/frontend-app` is a Tauri 2 packaging shell reusing the Web application and its build output; run `bun run tauri dev` from that directory. `apps/runner` is an independent Go module with entry point `cmd/runner`; run its Go commands from `apps/runner`. These are initialization scaffolds only; runner task execution and daemon behavior are not implemented.

**Agent skills.** Skills live once, in `.agents/skills`, pinned by `skills-lock.json`. `.claude/skills` is a per-clone link to it and is never committed: run `make skills-link` once after cloning (a junction on Windows, a symlink elsewhere). Add or change a skill under `.agents/skills`, never through the link.

**Windows execution.** Avoid visible command windows that disrupt other desktop applications. Agent `exec_command` calls on Windows use `tty: true` and `login: false` so commands run through ConPTY without profile startup. Node child-process launches use `windowsHide: true`; do not detach Windows test processes into a new console. Background `Start-Process` launches use `-WindowStyle Hidden`. If the user reports flashing windows, stop new launches and fix the execution path before resuming.

**Line endings.** Every area is LF and `core.autocrlf` is `false`. `apps/backend-api` lists its file types; `apps/frontend-web` declares `* text=auto eol=lf`, which also lets Git keep binary assets out of conversion. Confirm with `git ls-files --eol` rather than by eye — a byte dump of a file's first line is easy to misread, and writing CRLF into an LF tree turns a three-line change into a whole-file diff.

**Generated files.** Anything marked AUTO-GENERATED is derived output. Change the generator or its input and regenerate; never hand-edit the result. A generator must produce the same bytes on every machine — normalize platform-dependent input rather than copying it through.

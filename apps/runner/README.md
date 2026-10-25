# Runner

Standalone Go runner. Requires Go 1.26.2 or newer. It uses the existing IAM,
fleet and execution HTTP APIs; no extra Go dependencies are required.

```sh
go run ./cmd/runner inspect
go run ./cmd/runner login --profile /private/path/runner-profile --server https://api.example --tenant 7 --client 8 --name workstation
go run ./cmd/runner daemon --profile /private/path/runner-profile
go run ./cmd/runner status --profile /private/path/runner-profile
go test ./...
go build ./...
```

`--server` is the API base URL, including a deployment's API prefix if needed.
`--client` is an existing IAM public client that supports device authorization;
it is not the machine credential ID. Login displays the verification URL and
user code, polls the enrollment until approved, persists the one-time machine
credential, and exchanges that credential through `/oauth/token`. Human device
tokens are never used as runner identity. HTTPS is required; HTTP is permitted
only on loopback for local development. Redirects are refused.

Profiles must use a dedicated private directory. Only one process can hold a
profile, enforced by an OS lock that releases on process death. Windows protects
profile contents with current-user DPAPI. Other supported operating systems use
the private-file fallback (directory 0700, files 0600); an OS credential-vault
adapter is not implemented there. Profile and journal replacement is atomic and
synced. Neither status nor error messages print credentials, access tokens,
device proofs, or server response bodies. Access tokens remain in memory.

The daemon runs in the foreground. Interrupt it with Ctrl+C. It opens a fenced
session, uploads successful local version observations, sends five-second
heartbeats, pulls one execution at a time, and sends sequenced receipts. Failed
inspection probes refuse inventory replacement instead of erasing prior facts.
Version probes do not infer runtime authentication, supported models, Skills,
or MCP. Remote access is explicitly unavailable.

For ACTIVE Agents bound to this machine, the daemon starts a real resident
`agent-worker` subprocess per runtime/model profile and reports its actual PID
through session-fenced presence snapshots. This process owns the serial model
execution protocol: Agent plans run inside their matching worker, and a missing
worker never falls back to direct execution. Human plans retain direct runtime
execution. Worker RUNNING means the resident host process exists; it does not
claim model authentication or that a model request is currently running.
Cancellation kills the worker and its runtime/tool descendants; that worker is
discarded before later work can run. No interrupted execution is replayed.

While idle, the daemon polls inspection requests. RUNTIMES requests run a full
version/path scan in a background worker so heartbeats continue. Any failed
probe reports INSPECTION_FAILED without a partial inventory. MODELS and
CAPABILITIES read local catalogs and configuration as described below; missing
model lists produce per-runtime UNSUPPORTED facts. Reports carry
the current session fence; the backend retains previous model facts during a
runtime-only refresh.

Each execution attempt is recorded before a runtime can launch. Receipts are
saved before transmission and removed only after a matching acknowledgement.
Restart replays pending receipts, reports an unfinished attempt as LOST, and
never launches that attempt again. The journal retains an epoch fence for up to
10,000 execution IDs and an outbox of up to 10,000 receipts; reaching a limit
stops dispatch. A fenced/rejected replay remains saved for diagnosis and manual
reconciliation. Do not delete the journal to retry unknown work.

READ_ONLY and WORKSPACE_WRITE Codex and Claude Code plans can launch. ON_REQUEST
uses the frozen native approval protocol: CODEX_APP_SERVER_V1 for Codex or
CLAUDE_CONTROL_V1 for Claude Code. The daemon sends the actual runtime permission
request to the execution's approval requests and polls its exact recorded Human
decision while waiting. An authorized Human decides through the execution detail
or governance inbox. ALLOW resumes only that requested item; DENY, expiry,
transport failure or an unsupported request never grants a session-wide permit.
Heartbeat and cancellation remain active while a decision is pending.
FULL_AUTO remains unavailable without explicit project policy and recorded Human
step-up approval; the runner does not substitute a global runtime grant.
Runtime commands use fixed arguments without a shell, check the repository base
commit, isolate the parent environment from machine/API secrets, and require an
explicit runtime completion event. Raw model text is not uploaded because it can
contain secrets; streaming receipts report actual runtime observation metadata.

WORKSPACE_WRITE requires the registered workdir to be the Git repository root.
It creates a unique `runner/execution-*` branch and Git worktree beneath
`<workdir>/.runner-executions/`, starting at the plan's exact base commit. A
symlink/junction escaping this location is rejected. Codex uses `--sandbox
workspace-write`. Claude uses `--permission-mode acceptEdits` with only
`Read,Edit,Write,Glob,Grep` tools; Bash, network and MCP tools are not enabled.
Claude permission mode is runtime enforcement, not an OS sandbox; run the daemon
as a dedicated unprivileged user when stronger host isolation is required.
The source checkout and its HEAD are not changed. Git hooks and commit signing
are disabled for runner-owned Git operations. The runner stages actual workspace
changes and records a neutral Git commit, then uploads the complete binary Git
diff as `changes.patch` through the existing FILE evidence API and sends COMMIT
evidence and COMPLETED receipts. Plans may also declare `artifacts` before launch:

```json
{
  "artifacts": [
    {
      "criterion_id": "<acceptance-criterion-id>",
      "path": "hello.txt",
      "note": "Actual output for the exact hello.txt criterion"
    }
  ]
}
```

Place this field inside the execution request's `plan`. Each criterion must
belong to that work item and its frozen verification plan. The declaration is
included in the immutable execution plan hash. The runner reads the actual
regular-file Git blob at the final commit, uploads its bounded bytes and records
the declared criterion association. Missing, empty, oversized, symlink or
submodule artifacts fail explicitly. A criterion association records the planned
purpose of the evidence; it does not assert that verification passed.
The API requires every declared association before completing the execution,
hashes and binds the bytes to the execution, and submits a delivery Claim. Independent verification and Human
acceptance remain separate steps. No test report or criterion coverage is
invented. Diffs larger than the API's 64 KiB artifact limit fail explicitly rather
than truncate or assert success. READ_ONLY completion remains LOST/UNKNOWN and
does not submit a software delivery Claim.

Worktrees and branches remain available after completion, cancellation or failure
for inspection and exact-commit verification; there is no automatic cleanup.
After verification and evidence retention are complete, remove one explicitly
with `git worktree remove <absolute-worktree-path>` and then `git branch -d
<branch>`. Never delete the parent directory wholesale. A cancellation received
before launch can be acknowledged. After launch, the daemon acknowledges
CANCELLED only after its native process-tree kill succeeds and the runtime or
resident worker exit is joined. A failed or uncertain tree kill remains UNKNOWN;
there is no completion Claim for a cancelled attempt.

Transport failure immediately stops dispatch and cancels the current runtime.
An authenticated 401 refreshes the token from the protected current credential
and retries that rejected request once. Transport errors and 503 responses are
never replayed automatically.
The foreground supervisor reconnects after transport/transient failures with a 1s to 30s capped exponential backoff. It stops and joins the old runtime before reconnecting; interrupted attempts remain LOST/UNKNOWN and are never launched again. Stable sessions reset the backoff; permanent credential/config/protocol errors stop the supervisor. Windows hides subprocess windows and uses
`taskkill /T /F` on cancellation; Unix starts runtimes in a process group and kills
the group. The CLI waits for executor cleanup before exiting. The no-argument
CLI output is preserved for compatibility.

Credential rotation deliveries are polled alongside heartbeats. The runner
protects the previous and new credentials locally, atomically saves the new
identity before acknowledging its credential and session fences, and replays
an uncertain acknowledgement after restart. A timed-out revoked delivery can
fall back to the retained previous credential and receive a fresh attempt.

Not implemented: administrative revocation management, profile migration,
daemon stop/status IPC, WebSocket control channels or
remote file channels, multi-slot
parallel execution, runtime continuation, gateway proxy,
and automatic workspace retention cleanup. A recent live session can prevent a new
incarnation from connecting until its server lease expires. A one-time credential
response lost before local persistence requires restarting enrollment.

Remote interactive terminals and execution-owned service previews use separate
authenticated outbound NDJSON streams with framed input POSTs. Frames remain in
bounded memory and are not replayed after reconnect. Windows terminals use hidden
ConPTY and a kill-on-close Job Object, start in the actual pinned execution
workspace, and receive an allowlisted environment. They do not provide filesystem
isolation (`sandboxed=false`). Native terminal and owned-port adapters on other
operating systems explicitly return unsupported.

Terminal access requires current Human project authority, browser step-up and
the machine terminal switch. Input/output recording uses protected files;
15 minutes without input or two hours total closes the session. Service preview
ports must belong to the execution's Windows Job and listen only on loopback.
The independent TLS preview gateway requires `fleet.preview.domain`, a different
registrable domain from `fleet.preview.console_origin`, and certificate/key
configuration. SUBDOMAIN and PATH routing are supported; nonempty
`trusted_proxies` is explicitly unsupported. The transport is the documented
HTTP-streaming alternative, not the specification's binary WebSocket wire.

Default tests use local HTTP servers and deterministic subprocess fixtures for
both runtime protocols, including real descendant cancellation. Explicit opt-in
live tests can invoke an installed authenticated CLI and incur model usage;
RUNNER_APPROVAL_LIVE_RUNTIME and RUNNER_APPROVAL_LIVE_MODEL enable that check.
Its callback approves only an exact scratch write and is not a Human governance
decision. RUNNER_SERVICE_LIVE_EXE enables a real Windows service lifecycle test.
Build/test artifacts are not part of the source deliverable.

## Visual evidence

A VISUAL verification step uses `argv: ["index.html"]` for an HTML artifact in
the exact verification snapshot, or an HTTP(S) preview URL. Optional width and
height arguments select one viewport, for example `["index.html", "390", "844"]`.
Defaults capture both 1440x900 and 390x844. The runner detects installed Chrome
or Edge and captures the real page using a hidden headless process; a missing
browser or inaccessible target reports unavailable evidence explicitly.

An optional `report_path` names a snapshot-relative reference PNG, or a reference
directory for multiple viewports. The runner uploads canonical capture metadata,
SHA-256 hashes and immutable screenshot files, recording reference comparisons
when available. Capturing or comparing pixels does not establish visual quality:
without a calibrated reviewer, the result remains INCONCLUSIVE with
VISUAL_REVIEW_REQUIRED and needs an independent Human/Agent review.

## Local discovery

MODELS reads Codex models_cache.json under CODEX_HOME (or the default user directory) and Claude settings.json availableModels when that local list exists. It never invents a catalog from a configured default model. A runtime exposing no local list or CLI list command reports UNSUPPORTED with an explicit reason. No model request is made.

CAPABILITIES reads user and registered-project skill directories and local MCP configuration. Reports contain names and skill SHA-256 hashes only, never configuration values, command arguments, URLs, credentials or skill contents. MCP names do not establish connection or authentication health. Refresh after changing local config. Unsupported config syntax reports a failure rather than a successful empty list.

The daemon sends discovery at session opening, refreshes models every five minutes and processes fenced manual refreshes. The backend preserves the last successful list as CACHED after a per-runtime error; checked and last-success timestamps are separate. Workdir capability scans serialize through the existing inspection queue.

## Install and login

From this module, on Windows, macOS or Linux:

```sh
go install ./cmd/runner
runner --help
codex login
claude auth login
runner inspect
```

Install and log in only to the runtime you will use. Use the same OS account for
runtime login and the daemon, and put Go's binary directory and the runtime
executables on its PATH. Then run the `runner login` command above with your
deployment's API prefix, tenant and public device-client ID; approve the shown
device code in the browser. Version detection does not prove authentication.
Keep the private runner profile outside every registered repository.

An administrator registers an Agent through authenticated `POST /agents` with
an `Idempotency-Key`. First create an active tenant service account using IAM and
grant its IAM `agent_executor` role; registration maps `EXECUTOR` to that actual
grant and checks the existing account rather than creating a second identity.
Example body (replace IDs/name):

```json
{
  "principal_id": "<service-account-principal-id>",
  "machine_id": "<enrolled-machine-id>",
  "name": "Repository executor",
  "handle": "repository-executor",
  "runtime": "CLAUDE_CODE",
  "model": "sonnet",
  "roles": ["EXECUTOR"],
  "authorization_mode": "WORKSPACE_WRITE",
  "system_prompt": "Implement only the assigned repository task and report observed results."
}
```

For independent QA, an authorized Human first calls
`POST /tenants/{tenant_id}/agent-reviewer-role-provisions` with an
`Idempotency-Key`. This provisions the `agent_tester` and `agent_acceptor` role
definitions and returns their actual IDs; it assigns no members. Grant each
role to a separate real IAM service account through the ordinary role-member
API. These marker roles grant no command permissions; separately grant the
existing `agent_reviewer` role with the required project visibility. Register
the two accounts as separate `TESTER` and `ACCEPTOR` Agents. Choose an
installed runtime/model family different from the executor, and wait for their
actual resident workers. These eligibility roles do not grant Human decision
authority. Human CHECK and acceptance use an authorized independent Human
account, distinct from the result initiator. To inspect/download result artifacts,
that Human also needs current `file_view`, `file_traverse` and `file_download` permissions
scoped to the scratch project. Use the ordinary IAM role/binding APIs; a business
review role alone does not bypass file access controls.

Registration returns ACTIVE with process status NONE. Wait for the daemon's
actual resident worker PID and RUNNING presence before assigning work. A mode
omitted at registration defaults to READ_ONLY; a plan cannot exceed that cap.
The server freezes the registered persona into the execution plan; the runner
includes that immutable snapshot separately from the objective and feedback.
Complete the project's staffing/proposal approval and current assignment flow
before creating an execution. Runtime model availability must be an actual
observed fact; a version probe alone does not establish a model catalog or login.

## Background operation

Use one daemon per enrolled profile. Install the compiled runner at a stable
absolute path, then run these commands under the same OS account used for login:

```sh
runner service install --profile /absolute/private/runner-profile
runner service status --profile /absolute/private/runner-profile
runner service uninstall --profile /absolute/private/runner-profile
```

Installation starts the daemon immediately and configures startup for that user.
Windows installs a current-user logon Scheduled Task with a private Windows
Script Host launcher that hides the daemon window. Manager PowerShell processes
also run without a window. It requires Windows Script Host, an interactive user
logon, and permission to run local PowerShell scripts; it never uses SYSTEM or
stores an account password. Linux installs and enables a user systemd
unit (a working user systemd session is required). macOS installs a launchd user
agent in the logged-in GUI session. Linux/macOS restart after failure; Windows
starts again at the next logon. Startup without a user session is not configured.

Service names derive from the absolute profile path, allowing distinct profiles.
The executable path and current PATH are captured at installation; reinstall after
moving the executable or changing the runtime PATH. On Linux/macOS uninstall
before reinstalling. Status reports actual manager observations as JSON
`installed` and `running`; it does not assert backend connectivity or runtime
login. Manager failures return an error. A failed installation can leave its unit
file for diagnosis; remove it with uninstall after resolving the manager failure.

Uninstall stops and removes only the selected service, retaining the enrolled
profile and execution journal. Service status/uninstall work while the daemon
holds its profile lock. Stop/uninstall before upgrading or logging in again.
Interrupted attempts remain unknown after restart. Service output is discarded;
use foreground `runner daemon` for diagnostic output. Keep the account's HOME
and runtime credentials available; the installer copies no credentials into the
service definition. No daemon stop IPC is provided.

## Live scratch acceptance

Use throwaway API/frontend instances on unused loopback ports, a dedicated
profile, and a new Git directory outside this source tree:

```sh
git init scratch-runner
cd scratch-runner
git -c user.name=Tester -c user.email=tester@localhost commit --allow-empty -m baseline
git rev-parse HEAD
```

All paths below are relative to your deployment's API prefix. Use authenticated
Human requests for preparation and review, and the API's returned IDs rather
than guessing them. Mutation endpoints require an `Idempotency-Key`; where
`If-Match` is required, preserve the quoted concurrency value from the latest
ETag (for example, `If-Match: "3"`, not `If-Match: 3`). Refresh the work ETag after
each successful mutation.

1. Authenticate the real installed runtime, enroll with `runner login`, approve
   the device request as a Human, then start `runner daemon`. Confirm the machine
   is online and inventory lists the actual runtime.
2. Register the scratch repository and its absolute root with
   `POST /machines/{machine_id}/workdirs` using `project_id`, `repository_id` and
   `path`. Register an ACTIVE Agent as above, confirm its real worker PID/RUNNING
   state, and approve the project's staffing proposal.
3. Create the work through `POST /entities/{entity_id}/work-items`, with criterion
   `hello.txt contains exactly hello followed by a newline`. Before readiness,
   configure a COMMAND verification step for that criterion using an actual
   installed Python executable, argument `-c`, and this argument as the code:

   ```python
   from pathlib import Path; assert Path('hello.txt').read_bytes() == bytes([104,101,108,108,111,10])
   ```

   Prepare the work using `PATCH /entities/{entity_id}/work-items/{work_item_id}`
   with quoted work `If-Match`, then confirm readiness through
   `POST /entities/{entity_id}/work-items/{work_item_id}/readiness-checks`.
   Create an assignment proposal with
   `POST /entities/{entity_id}/dispatch-proposals`, body `work_item_id`, current
   `expected_work_version`, and `trigger: "MANUAL_REQUEST"`. Decide through
   `POST /entities/{entity_id}/dispatch-proposals/{proposal_id}/decisions` with
   quoted proposal `If-Match`, `decision: "CONFIRM"` for the recommendation, or
   `decision: "OVERRIDE"` with `assignee_principal_id` for the chosen Agent, and
   a `reason`. Confirm the applied assignment before execution.
   Where internal QA is required, provision eligible TESTER and
   ACCEPTOR Agents with the required independent/different-model assignments
   before expecting acceptance. Missing reviewers can require Human review;
   do not disable the policy to make the demonstration pass.
4. Dispatch through `POST /tenants/{tenant_id}/executions` using WORKSPACE_WRITE, the baseline hash
   and objective `Create hello.txt containing exactly hello followed by a newline`.
   In the request's `plan.artifacts`, declare the returned criterion ID,
   repository-relative `path: "hello.txt"`, and a descriptive `note` as shown
   above. This must happen before launch; do not relabel a generic patch after
   execution to claim a criterion association. Read `GET /executions/{execution_id}` to check the pinned Agent, machine,
   workdir and runtime, and observe sequenced runtime observation logs.
5. Wait for `changes.patch` FILE evidence, COMMIT evidence and Claim submission.
   Confirm original HEAD is still the baseline and `hello.txt` is absent in the
   original checkout. From that repository inspect
   `git show <evidence-head>:hello.txt` as bytes; inspect the separate
   `.runner-executions` worktree. List the result using
   `GET /entities/{entity_id}/work-items/{work_item_id}/results`. Download its
   pinned patch through
   `GET /entities/{entity_id}/work-items/{work_item_id}/results/{result_id}/evidence/{evidence_id}/content`
   and compare it byte for byte with
   `git diff --binary --no-ext-diff --no-textconv <base> <head> --` saved as bytes.
6. Observe the independent COMMAND verification against that exact head. A
   successful execution and COMMAND result do not mean Human acceptance. The
   generic diff evidence has no criterion association and truthfully declares
   NOT_APPLICABLE; the separately declared `hello.txt` artifact supplies the
   criterion association and must match the real final-commit bytes. Acceptance
   also requires a passed independent Human check, current baseline and all
   applicable QA gates. For each criterion, request AGENT_JUDGE verification at
   the result's `/verifications` endpoint with `stage: "AGENT_TEST"` and the
   eligible TESTER's `reviewer_principal_id`; wait for its actual exact-head
   PASSED receipt. Then request `stage: "AGENT_ACCEPTANCE"` with the independent
   ACCEPTOR's principal and wait for its actual PASSED receipt. Pin reviewers
   from a different model family than the executor. An HTTP success response
   creating a task is not a verdict. Collect observations through the result's
   `/verification-receipts` endpoint when required by the API. Record the authorized independent check
   at `POST /entities/{entity_id}/work-items/{work_item_id}/results/{result_id}/checks`,
   using `{ "verdict": "PASSED", "reason": "<observations actually checked>" }`,
   then an authorized decision maker requests acceptance at the same result's
   `/acceptance` endpoint with `{ "reason": "<actual acceptance basis>" }`.
   Both actors must be independent of the result initiator. Inspect the final
   result and work item: their persisted accepted decision and ACCEPTED state
   are the acceptance evidence, rather than the runtime exit code. Both require `Idempotency-Key` and quoted work
   `If-Match`. Confirm the recorded decision; report unmet gates or authorization
   failures explicitly instead of claiming acceptance.
7. Dispatch another scratch task and cancel while running using
   `PUT /executions/{execution_id}/cancellation` with its required headers.
   Capture the actual runtime/tool descendant PIDs before cancellation. Confirm
   those processes are gone, the execution becomes CANCELLED after confirmed
   termination, and no completion Claim is submitted. If termination cannot be
   confirmed, expect UNKNOWN rather than claiming a successful cancellation. Restart the daemon with the same
   profile and confirm that attempt is not launched twice. For ON_REQUEST, use
   the matching frozen native protocol, observe the actual request in the
   execution detail/governance inbox, and record an authorized Human ALLOW or
   DENY for that request. Verify that only the allowed item resumes; expired,
   denied or unsupported requests must not execute. FULL_AUTO without project
   policy and recorded Human step-up approval must be refused without launch.
8. Stop the daemon and throwaway servers. Retain worktrees through verification
   and evidence review, then explicitly remove scratch worktrees if appropriate.

Local checks are `go test ./...` and `go build ./...`. They use real Git
worktrees/commits and deterministic runtime subprocesses. They do not prove
installed CLI authentication, browser dispatch, OS service startup, live server
verification or Human acceptance; record those live outcomes separately.


### Private hidden acceptance verifier

Hidden cases are copied into a server-owned byte archive when the requirement
suite freezes. Later edits cannot change the bytes addressed by that suite's
case hashes. Ordinary execution plans, task pulls, public snapshots and context
assembly receive only redacted case references. Hidden verification uses a
separate IAM service account and the current project `acceptance_suite_verify`
permission; the executor identity cannot substitute for that service account.
Provision the account through the existing tenant `/service-accounts` API and
assign the project permission through normal IAM role grants.

Build the runner and deploy its trusted executable outside executor repositories.
The backend configuration `hidden_verifier` accepts `principal_id`, `executable`,
`args`, `model`, `family`, `timeout_sec`, `os_user`, `os_domain`, and
`password_env`, and `runtime_auth_env`. The Windows account must authenticate as a different, non-admin
SID from the backend/executor account. `password_env` names the backend environment
variable containing its password; the value never enters jobs or receipts. The
backend needs privileges to assign protected workspace ownership and to launch
the authenticated token. Missing identity, ACL, ownership or launch capabilities
produce UNAVAILABLE before case bytes reach a command. Use a system-wide runtime
installation that the dedicated account can execute; a CLI installed only in the
backend user profile is not a supported shared executable. Other operating systems
currently report OS isolation unavailable. Set `args` to
`["hidden-verifier", "--runtime", "CLAUDE_CODE"]` with family `anthropic`, or use
runtime `CODEX` with family `openai`. Set `model` to the model actually available
to that installed runtime. There is no model-family fallback: a matching source
family, missing command, inaccessible source repository or unavailable runtime
leaves acceptance unavailable. The backend host must have read access to the
source Git repository; a remote runner-only path is explicitly unavailable.

On automatic verification, the backend clones committed bytes with no local
hardlinks into a private temporary directory, checks out the result's exact head,
and sends the frozen cases only on the verifier command's stdin. The command
reuses the installed native runtime in READ_ONLY mode. Runtime logs and case
contents never become public verification evidence: only the bound verdict and
SHA256 hashes are recorded with the result, suite hash and verifier identity.
The private clone is deleted under the verifier token after all job descendants
terminate. If termination or protected cleanup cannot be confirmed, the directory
remains protected for operator cleanup and the verdict is UNAVAILABLE with a
fixed cleanup/termination reason; it is never restored to executor ACLs. Human and internal QA acceptance
both require the exact passed hidden receipt. The Windows workspace owner and
protected ACL grant access only to the authenticated verifier SID and SYSTEM;
the runtime receives a fresh private HOME/APPDATA/TEMP, with Codex ephemeral or
Claude no-session-persistence enabled. Shared executor profiles are never reused.
Set `runtime_auth_env` to the backend variable holding a dedicated model API
credential; the family maps it only to OPENAI_API_KEY or ANTHROPIC_API_KEY for
the private command. The private runner forwards only that family credential
to the native runtime. This explicit credential is never stored in a payload,
receipt or log. The backend does not copy executor login credentials. The local executor is
assumed to use the backend account; deployments with other executor OS accounts
need an attested identity boundary before enabling this local adapter. Privileged
administrators are outside this OS-account boundary. A successful native
separate-account end-to-end run requires configured deployment credentials and
privileges; unit command fixtures do not prove that deployment capability.

### Recorded full-auto approval

A FULL_AUTO execution is prepared as `FULL_AUTO_APPROVAL_REQUIRED` and is never leased until a Human approves its exact immutable plan and dispatch epoch. Use the execution's `project-execution-policy` GET/PUT API to inspect or change `allow_full_auto` (Human `project_manage`, optimistic `expected_version`, reason; default true). Approval uses PUT `/executions/{execution_id}/full-auto-grant` with a reason and a canonical browser-session reauthentication proof for purpose `execution_full_auto`, target `execution:<id>` (freshness five minutes). Proofs never enter the execution plan or runner payload.

The backend records the Human grant as immutable evidence with audit/outbox. Dispatch rechecks current project policy and Human authority. Recovery changes the epoch and requires a fresh approval. Only the backend lease may supply the grant marker; posting one inside a plan cannot enable full access. The runner uses Codex `danger-full-access` or Claude `bypassPermissions` only for a granted FULL_AUTO lease.

## Local release package

`node release.mjs` builds the six supported Windows/macOS/Linux x64/arm64 binaries
serially. `node release.mjs win32-x64` builds one target for a local installation
check. Then `npm pack` creates a local installable tarball; a single-target tarball
is only for that target, not a complete release. `npm install --prefix <directory>
<tarball>` installs the `machine-runner` command. No registry publication is done
by these commands. The launcher passes arguments directly to the native binary
without a shell and hides child windows on Windows.

Console installation commands come from `fleet.runner`: `package`, `version`,
`command`, `server` (external API base URL), and `client_ids` (tenant ID to existing
public device-client ID). Missing/invalid configuration shows unavailable; it does
not imply that a package has been published. Use the actual published package and
version for the deployment. The daemon command runs in the foreground. Enrollment
status inspection is restricted to current Human machine managers.

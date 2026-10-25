# Knowledge library and remote source configuration

Remote sources are disabled by default. Configure the module through deployment
configuration, then restart the API so it constructs and validates the connector:

```yaml
knowledge:
  remote:
    enabled: true
    repository_host: git.example.net
    repository_api_base_url: https://api.example.net
    # Optional for public repositories; supply a deployment secret for private access.
    repository_token: ""
```

`enabled` permits public HTTPS URL retrieval. Repository support additionally
requires `repository_host` and `repository_api_base_url`, using a GitHub-compatible
`GET /repos/{owner}/{repository}/tarball/{ref?}` endpoint. An empty token does not
prove private repository access; upstream access denial is reported explicitly.
The token is a secret configuration field. It is not a tenant source field,
never appears in settings/source responses, and is not sent to URL sources or
forwarded to a different origin during archive redirects.

Bind a URL source with an HTTPS document locator. Plain UTF-8 text, Markdown,
code and HTML are supported; HTML scripts/styles are excluded from extracted
text. PDF extraction is unavailable. Bind a repository source with a locator
such as `https://git.example.net/owner/repository?ref=release%2Fstable`. Omit `ref`
to use the repository's default branch. By default only README and `docs/` files
are indexed. Add `path=docs/subdirectory` or an exact file path to narrow that
selection. The archive is read in memory and never extracted or executed.
Supported text files are ordered by path before indexing.

Sync requires current source write authority; bindings do not fetch until a sync
or queued ingestion runs. The canonical source transaction saves downloaded
content, hashes and fragments together. Unchanged content retains fragment IDs.
Queued ingestion rechecks authority, retries transport failures, and records a
failed source after retry exhaustion. Disabling a connector prevents subsequent
fetches. Stored references do not bypass outbound policy.

All outbound sockets and redirect hops use the existing strict SSRF policy:
HTTPS only, no credentials in locators, no environment proxy, and private,
loopback, link-local and reserved destinations are blocked at connection time.
Requests time out after 15 seconds. Downloads are limited to 10 MiB. Repository
archives additionally allow at most 64 MiB expanded data, 200 selected files,
1 MiB per selected file and 10 MiB selected text; archive paths and UTF-8 are
validated before indexing. Exceeding a bound rejects the sync rather than
silently indexing a truncated repository.

Settings expose `url_sync_available` and `repository_sync_available` separately;
these describe configured transports, not a successful network probe or proof of
credentials. `remote_sync_available` means at least one transport is configured.
No live private repository credential is included in this repository.

# Versioned SKILL.md contracts

Skills have immutable DRAFT content versions. A human with `knowledge_write`
may approve or reject a draft, including their own. Approval moves the current
version pointer; rejected versions cannot be mounted. An Agent mount can follow
current or pin an approved version belonging to that same skill. Each Agent has
at most thirty active mounts. Archive retains mount history but excludes the
skill from future resolutions; restore makes it eligible again.

Agent HTTP paths and `SkillCatalog`/`SkillMounter` inputs use Agent principal IDs,
matching existing platform APIs. Mount rows store the fleet identity's Agent ID.
`SkillCatalog.ForAgentOn` resolves within the caller's transaction and holds the
Agent and skill locks until the caller persists its execution snapshot and
commits. The returned version ID, version number, hash, content and tags are
immutable values. Existing snapshots do not change after approval or archive.
`ForAgent` provides a standalone transaction for callers that do not freeze an
execution. Neither method asserts runtime distribution or replaces tool grants.


# Automatic execution context and source events

`RuntimeContext.BuildInjectionOn` selects current TEAM, PROJECT and the assigned
Agent's own memory inside the execution transaction. Its current mode is
`LEXICAL_ENTRIES`: bounded canonical-entry lexical ranking, not native SQL FTS or
vector search. Each scope is bounded to 1,000 active entries; exceeding that
bound fails unavailable. Reference defaults are eight items and 4,000 tokens;
approved TEAM/own-Agent RULEs are selected independently of query terms, up to
20 rules and a separate 1,500-token reserve. Budgeted reference bodies carry a
snapshot hash while retaining the canonical original content hash.

An immutable selection log precedes runtime delivery. Only source-verified
runtime inclusion and model output establish delivered/cited facts. The execution
and review views recheck current source permissions, retain frozen bytes after
source updates or retention, and show actual decision outcomes. Human execution
responses apply the same current source read fence as the knowledge panel.
Structured quality feedback uses its source decision snapshot and `[Fn]` labels.
Quality continuations recompute retrieval after their new feedback is frozen.

Canonical requirement versions, feedback, decisions and accepted deliveries are
rendered from owning-module records through transactional event consumers.
Superseded baselines invalidate prior live chunks. Agent retirement invalidates
its sources inside the original retirement transaction; source reads also fence
retired Agents before asynchronous delivery. Client acceptance affects ranking
only when backed by the immutable signed package receipt hash. Distillation,
native full-text indexes and vector backends retain their separate capability
gaps.

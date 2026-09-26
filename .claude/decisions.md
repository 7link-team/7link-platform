# Decisions

## D72 - The coverage gate measures the union across test binaries

**Date** 2026-09-22

**Decision / Reason**
`make test-coverage` now passes `-coverpkg` over the gated package set, so a
package exercised by a sibling integration suite is credited instead of reading
as untested. This closes the measurement gap D69 recorded: `internal/app`
measured 86.8% from its own tests and 88.3% once `internal/integration/iam` was
included, and its whole public lifecycle API read 0% despite being exercised
there.

It cannot flatter the result. The same packages are already in the profile and
therefore already in the denominator - Go includes packages with no test files -
so `-coverpkg` only adds covered blocks. Total went 91.0% -> 91.6%, and the run
got faster, 13 minutes to under 6.

**Alternatives Considered / Tradeoffs**
Duplicating the integration lifecycle tests inside `internal/app` would have
moved the number without adding safety, which is the coverage-chasing the rest
of this work avoided.

The per-module check had to be fixed in the same change. With `-coverpkg` every
test binary reports every gated package, so each block appears once per binary
and almost always with a zero count. The total is computed by
`go tool cover -func`, which merges blocks, but the per-module figure was a
line-by-line sum that counted one block as dozens of statements and collapsed
every module to about 2%. It now keys by block and takes the union, which is
what the total was already doing.

**Scope / Status**
Done. `make ci` passes: total 91.6%, every gated module above 90%.

---

## D73 - Guard-layer messages are respx keys, not bare codes

**Date** 2026-09-22

**Decision / Reason**
`authz` reported outcomes as bare strings - `not_found`, `forbidden`,
`unauthorized`, `invalid_id` and four more. Localization resolves
`respx.not_found`, never `not_found`, so every one passed through raw. Because
D66's owner resolution answers an absent globally addressed resource before the
handler runs, this was what a client saw on a 404 across the entire owned
resource surface, which is the regression D69 recorded.

respx already owned four of these keys with text in all three locales; they were
simply unreachable. It now exports them as constants, gains the four it was
missing, and authz reports those constants at its 25 call sites. No new i18n
namespace and no new registration point were introduced.

**Alternatives Considered / Tradeoffs**
Giving authz its own bundle was the first plan and was dropped once respx turned
out to own the vocabulary already: a second namespace for the same four
concepts would have split ownership of one client-visible message set. Having
the resolver defer to the owning module for the message was rejected as a
redesign of the authorization path for a wording problem.

The SAML test that pinned the raw `not_found` was not retargeted to the new key,
which would only have re-encoded the bug. Its harness now registers respx's
bundle and it asserts the localized sentence, plus that the key does not survive
into the body.

**Scope / Status**
Done. This changes the `message` field for guard-layer refusals on every
authorized route.

The one consumer in this repository was checked rather than assumed: no
production code in `apps/frontend-web-admin` compares `.message` at all, and the
places that look like they might are the frontend's own status enum or its own
i18n keys. `sessionFailureStatus` branches on `cause.status === 401`, and the
files page on `cause.status === 403`. Frontend typecheck and its 126 tests pass
unchanged. An external caller matching the bare strings would still break, but
that was never a supported contract: `message` is documented as a
human-readable summary and `Code` is the machine-readable field.

---

## D70 - Cross-module principal anonymization lives outside the IAM namespace

**Date** 2026-09-22

**Decision / Reason**
`internal/iam/lifecycle/anonymizer.go` moves to `internal/principallifecycle`.
It held one file with no `module.go`, no `Module` type, no i18n and no
migrations, so it was never a module, yet it sat directly under the IAM
namespace where `TestIAMModuleArchitecture` requires every directory to be one.
It imports eight IAM modules because it erases principal data across all of
them, which is cross-module composition rather than anything a single module
can own.

Adding "lifecycle" to the `iamModules` allowlist was rejected outright: that
relaxes an architecture rule to accommodate a misplacement, the same move as
excusing a coverage gate. The test was right and the file was in the wrong
place. The name follows the vocabulary the integration suite already uses in
`principallifecycle_test.go`.

**Alternatives Considered / Tradeoffs**
Folding it into an owning IAM module (identity or core) was rejected because it
would make that module depend on seven siblings, which is precisely the coupling
the `module-dependency` rule polices. `internal/maintenance` looked right - it
already holds cross-module operational procedures and imports seven IAM modules
- but it imports `internal/app`, and `app` wires the anonymizer, so that
placement is an import cycle. That was found by building, not by reasoning.

**Scope / Status**
Done. `internal/iam/` now contains exactly the nine modules the allowlist names.

---

## D71 - Table names are bound by their owner, not rebuilt at the call site

**Date** 2026-09-22

**Decision / Reason**
The two `concatenated-table-name` violations are fixed rather than budgeted.
`internal/todo/service.go` now takes its table from the module's own
`query.BindNames`. `principallifecycle` no longer prepends the deployment
prefix itself; `goose.Source.VersionTable()` is exported and does it, which is
already a sanctioned concatenation point. The stale ledger entry
`concatenated-table-name:c7121096622dde53` is deleted and the budget lowered
from 13 to 12, matching the gate's own instruction that the ledger may only
shrink.

**Alternatives Considered / Tradeoffs**
Reading each version table from the owning module's `MigrationSource()` would
have removed the literal names too, and the seven names were verified identical
first. It was reverted: the architecture gate rejected it as
`cross-module-concrete-type`, because reaching for a sibling's concrete `Module`
type is a worse coupling than a literal string. Raising the budget was never
considered - the gate exists to make that a deliberate, documented act.

**Scope / Status**
Done. `make ci` passes end to end for the first time on this branch: fmt, lint,
coverage at 91.0% with every module above 90%, all six integration suites
including `tests/architecture`, build, and the generated OpenAPI.

---

## D67 - The coverage gate is not relaxed to accommodate internal/files

**Date** 2026-09-21

**Decision / Reason**
The total coverage gate now passes at 90.023%. `internal/files` remains at
82.7% against the 90% per-module minimum, so `make test-coverage` still fails on
exactly that one module. The gate is left exactly as it is. No per-module
exception, no lowered threshold, no removal of `internal/files` from
MODULE_COVERAGE_PACKAGES.

A gate that is edited until it passes stops being evidence. `internal/files` is
the newest area of the codebase and the only module that touches user file
content directly, so it is the last place where the signal should be hidden. A
red gate naming one module is more useful than a green gate that quietly
excuses it.

**Alternatives Considered / Tradeoffs**
Granting a dated per-module exception was rejected: it makes CI green while the
gap stays invisible in day-to-day work, and dated exceptions outlive their
dates. Lowering the global minimum was rejected for the same reason plus it
relaxes every other module to buy one. Covering the remaining ~330 statements
now was rejected as disproportionate: they sit in 766 blocks averaging one
statement each, almost entirely `if err != nil` guards on individual database
and storage calls, so closing them means injecting a distinct fault at roughly
330 call sites for assertions that mostly restate that an error propagates.

The cost is that `make ci` stays red until this is resolved, and anyone running
it must know why.

**Scope / Status**
Accepted for now, explicitly unfinished. The efficient way to close it is a
shared SQL fault-injection helper - the mechanism already exists ad hoc in this
package as gorm callbacks - plus table-driven sweeps, rather than one test per
guard. Revisit before treating `make ci` as a release gate.

---

## D68 - Helpers orphaned by the D66 route migration are deleted, not kept

**Date** 2026-09-21

**Decision / Reason**
`tenantResourceIDs` in `internal/iam/governance/http.go` and
`parseOAuthClientIDs` in `internal/iam/oauth/http.go` are removed. Both parse a
tenant plus a resource identifier from the path, which is the shape D66
abolished when existing resources became globally addressed. Both are
package-private with no caller anywhere, including tests, and `golangci-lint`'s
`unused` rejected them. They were the only two lint findings in the repository,
so `make lint` was already failing at HEAD before any of this session's work.

**Alternatives Considered / Tradeoffs**
Keeping them behind a lint suppression was rejected: silencing the linter to
preserve code nothing calls trades a real signal for dead weight, and the next
reader would reasonably assume they are part of the routing contract. Leaving
them and the lint failure alone was rejected because it blocks the whole gate
for no benefit. The risk of deletion is that a future globally addressed route
wants the same parsing; that is a handful of lines to rewrite from the
surviving `parseGovernanceID` and `parseOAuthID`, which both remain in use.

**Scope / Status**
Done. `make lint` and `make fmt-check` are clean; the generated OpenAPI is
byte-identical, confirming neither function participated in route registration.

---

## D69 - Coverage measurement and the localized not-found regression are deferred

**Date** 2026-09-21

**Decision / Reason**
Two known problems are recorded rather than fixed in this pass.

The coverage gate runs each package's own tests with no `-coverpkg`, so code
exercised by a sibling integration package counts as uncovered. `internal/app`
measures 84.9% from its own suite but 86.5% once `internal/integration/iam` is
included, and its entire public lifecycle API reads 0% despite being exercised
there. The fix belongs in the Makefile, and changing measurement semantics
shifts every number in the gate at once, which is not something to do in the
same change that brings the gate to the line.

Separately, D66's owner resolution answers an absent globally addressed resource
with the registrar's raw `not_found` before the handler runs, so module-owned
localized not-found messages are no longer reachable on any owned route, and
authz's own error codes are not localized at all. That changes what every API
consumer sees on a 404 across the whole owned-resource surface, which is a
product decision.

**Alternatives Considered / Tradeoffs**
Duplicating the integration lifecycle tests inside `internal/app` would move the
number without adding safety, which is the coverage-chasing this work otherwise
avoided. Restoring localized messages by having the resolver defer to the module
was not attempted because it would change response bodies platform-wide.

**Scope / Status**
Open. Both are recorded so neither is rediscovered as a surprise.

---

## D66 - Globally unique resources use short canonical item paths

**Date** 2026-09-21

**Decision / Reason**
Every first-class internal resource uses a globally unique snowflake ID. Create
and list operations are nested under the direct owner, while read, update,
delete, direct subresources, and relationships address an existing resource by
its global ID. Thus entities are created and listed at
`/tenants/{tenant_id}/entities`, then addressed at `/entities/{entity_id}`;
departments are created and listed at `/entities/{entity_id}/departments`, then
addressed at `/departments/{department_id}`. The server resolves and verifies
tenant ownership. Scope headers and query parameters are not authorization
selectors.

Menus and files use the same account, tenant, entity, and department contexts.
Current-principal menu projections are `/me/menus`,
`/tenants/{tenant_id}/me/menus`, `/entities/{entity_id}/me/menus`, and
`/departments/{department_id}/me/menus`. Menu visibility reuses the existing
scoped permission evaluator and never replaces endpoint authorization.

**Alternatives Considered / Tradeoffs**
Always retaining the full ancestor chain was rejected because globally unique
IDs make it redundant for existing-resource operations and require callers to
repeat derivable state. Flat collections with scope headers were rejected
because the same URI could identify different resources and headers obscured
the authorization boundary. Optional entity/department query selectors were
rejected for the same reason. Short item paths require middleware to resolve the
resource owner before authorization and to fail cross-tenant references as not
found.

**Scope / Status**
Supersedes D65 where D65 requires full tenant/entity ancestry for every API item
path. Browser workspace URLs may remain fully nested for navigational context;
this decision governs the public HTTP API. Approved; breaking migration in
progress with no compatibility aliases.

---

## D65 - Browser and API resource scope is explicit in canonical paths

**Date** 2026-09-20

**Decision / Reason**
The browser uses `/me/*`, `/tenants/{tenant_id}/*`,
`/tenants/{tenant_id}/entities/{entity_id}/*`, and `/platform/*`. URL parameters
are the active workspace authority; local storage is only a remembered default.
Tenant/entity API authorization derives the same scope from path parameters and
validates entity ownership instead of trusting independently supplied context
headers. Public API routes have no `/iam` or `/authn` namespace prefix.

This makes links reproducible, prevents a selected tenant from displaying a
global account file root, and gives caches, logs, authorization and OpenAPI one
unambiguous resource identity.

**Alternatives Considered / Tradeoffs**
Keeping flat browser routes with local-storage/header scope was rejected because
the same URL could identify different data and incomplete entity context already
caused real isolation confusion. `/profile/{user_id}` was rejected for the
signed-in user's private resources because it implies arbitrary-user access;
`/me` expresses self-service. Compatibility aliases were rejected because the
user explicitly requested a complete breaking cleanup. The migration touches
route callers, menus, scripts, tests and generated clients in one release.

**Scope / Status**
Applies to the IAM admin Web, public HTTP route registration, documentation,
scripts, generated OpenAPI/client types and file-persona acceptance. Internal Go
package paths named `internal/iam` are architectural module names and are not
public URL prefixes. Approved; implementation in progress.

---

## D56 - File browser authorization is page-batched and SQL-bounded

**Decision**
Browser root/child pages load all target ancestor paths in one closure query,
all involved ACL rows in one bounded query, and file Version/Object metadata in
set queries. One authorizer caches IAM constraints by action and validates each
distinct root reference once. The ACL query is capped at
`unique ancestor nodes * maxNodeACLRules + 1`; reaching the cap fails closed.

**Reason**
Per-entry path, ACL, version, object and organization-reference reads made list
latency and database load grow linearly with page size. Batching must preserve
the same per-target root boundary, inheritance interruption, deny precedence and
IAM ceiling instead of widening authorization for performance.

**Alternatives Considered**
Increasing the page limit or caching permissions across requests was rejected:
neither removes N+1 work, and cross-request caching complicates revocation.
Joining every relation into one wide result was rejected because path, ACL and
object cardinalities multiply rows and make authorization budgets harder to
enforce consistently across dialects.

**Tradeoffs**
Each page still performs a fixed set of queries and may do one extra child-page
probe at exactly 100 visible entries. A corrupted over-budget ACL set denies the
whole request. The implementation retains tenant-space revision fencing for
writes; root-level revisions are a separate remaining scale change.

**Scope**
Files root and child browsing, node authorization and metadata projection only.
Search, trash, history, sharing and account-context browsing must independently
prove the same bounded authorization behavior before production acceptance.

**Status**
Implemented and tested: 2 visible files use 18 SELECTs and 100 use 19; Files full
and race suites pass. Real MySQL/PostgreSQL execution plans remain pending.

---

## D55 - File permission reads never acquire mutation locks

**Decision**
Loading a node's workspace path and ACL is a read-only operation and must read
`file_spaces` without a no-op UPDATE or `FOR UPDATE`. Mutation methods acquire
their own space/root lock explicitly before reading the state they fence.

**Reason**
The previous shared helper serialized all reads in one tenant through the single
`file_spaces` row. Keeping mutation locking at write entry points preserves
optimistic conflict behavior without turning browse/download authorization into
a global tenant write hotspot.

**Alternatives Considered**
Keeping one locking loader was rejected because it makes every GET a writer.
Removing locking from both reads and mutations was rejected because concurrent
ACL replacement could lose updates or surface dialect-specific lock errors.

**Tradeoffs**
Read and mutation paths now call different space-loading behavior, so new write
entry points must explicitly acquire their fence. Regression tests cover both
zero-write reads and concurrent ACL replacement.

**Scope**
Files permission persistence and its callers. Browser N+1 batching and moving
the global revision fence to file roots remain separate implementation work.

**Status**
Implemented and race-tested.

---

## D54 - Hierarchical file roots belong to organizations, not their creators

**Decision**
Tenant, Entity, Department, account-private, and tenant-personal roots are
ordinary directory files with an explicit `(root_scope, root_owner_id)` binding;
there is no Workspace resource or table. Tenant/Entity/Department roots belong
to the corresponding organization, never to the human who created it. The
creator is audit metadata only. Organization-wide file actions come from IAM
role permissions constrained to the matching Tenant/Entity/Department scope.
Directory and file ACLs refine that ceiling for principals, roles, groups and
departments; they cannot grant an action IAM denied. Only account-private and
tenant-personal roots follow a principal.

Tenant/Entity/Department roots are provisioned synchronously in the owning
resource creation transaction. Account-private and tenant-personal roots are
created lazily and idempotently on first real use. Every new Department belongs
to exactly one Entity; a child Department must use the same Entity as its parent.
A compatibility migration may leave legacy Department `entity_id` NULL, but
such rows fail closed until explicitly repaired.

**Reason**
Organization ownership must survive creator departure, suspension and transfer.
IAM role scope is the one existing system for whole-resource actions; duplicating
it as creator ACLs would create a second, drifting authorization model. Explicit
root ownership prevents accidental cross-scope writes and lets authorization
validate one root with bounded queries. Lazy personal roots avoid millions of
empty directory and ACL rows.

**Alternatives Considered**
Creator-owned Entity/Department roots were rejected because organizational data
must not follow a person. Tenant Administrator ACL on every descendant root was
rejected because it grants write/manage capability beyond explicitly scoped IAM
permissions. Eager personal root creation was rejected for storage and write-
amplification cost. A separate workspace/root/conflict table and a synthetic
`workspace_key` were rejected; existing file nodes remain the resource, and a
database unique constraint on `(space_id, root_scope, root_owner_id)` serializes
root identity.

**Tradeoffs**
Legacy unassigned Departments require an explicit repair/backfill before they can
own a file root. Required organization roots make resource creation dependent on
file-root provisioning availability. Cross-root writes require deterministic
root locking, and read paths must batch authorization rather than reuse mutation
locks.

**Scope**
IAM Department ownership, Tenant/Entity/Department lifecycle integration, Files
root metadata and ACL enforcement, read authorization performance, public root
metadata, and the corresponding Web file browser. Global route redesign and full
sharing implementation are not included in this phase.

**Status**
Approved by the user; implementation in progress.

---

## D1 – 审计脱敏默认表只覆盖凭据与高危标识，不覆盖联系类 PII

**Decision**
`audit.defaultSensitiveKeys` 保持现有范围（口令、令牌、密钥、授权头、卡号、CVV、
身份证、SSN 等）。邮箱、手机号、姓名、地址等联系类 PII **不**进入默认表，由部署方
按合规要求通过 `audit.sensitive_fields` 配置自行加入。

**Reason**
两类数据的审计价值截然不同。凭据被记录下来毫无用处而泄漏代价无限，全脱敏是纯收益。
联系类 PII 则常常**就是被审计变更的对象本身**（注册、改绑、客服修改联系方式、按邮箱
排查欺诈），默认抹除会削弱审计本身的取证价值。日志中的抹除不可逆：未记录的值无法
事后恢复，因此默认值偏向"记录"，把收紧留给显式配置。

注：审计事件的 `actor_id` / `client_ip` / `operation` 独立于 body，脱敏 body 不影响
"谁在何时做了什么"的追溯，只影响"改成了什么值"。

**Alternatives Considered**
1. 加入默认表 —— 简单，但如上所述削弱审计价值，且是不可逆的默认。
2. 新增掩码能力（`a***@b.com` / `138****5678`）—— 技术上最优，兼顾关联性与暴露面控制。
   **未实施**：属新功能，需先设计 `MaskedFields` 配置与各类型掩码规则，超出当前范围。
   若合规要求出现，这是推荐方向，而非改默认表。

**Tradeoffs**
审计日志会包含联系类 PII，因此审计存储（DB 表 / slog sink）必须与主数据同级的访问
控制和保留期限管理。这是 GDPR/PIPL 对审计日志的通行要求，而非"不准记录"。

**Scope**
`internal/infra/audit`。边界说明已写入 `redact.go` 的 `defaultSensitiveKeys` 文档注释，
避免后续被当作疏漏"修复"。

**Status**
已确认（用户授权代为决定）。若未来实施掩码能力，本决策需一并复审。

---

## D2 — golangci-lint 钉入 tools/go.mod

**Decision**
将 `github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8` 加入 `tools/go.mod`
的 `tool` 块，`make lint` 改用 `go tool -modfile=tools/go.mod golangci-lint run ./...`。

**Reason**
与该模块既有规范一致（buf、protoc-gen-go、protoc-gen-go-grpc 已同样钉版本）。在没有
CI 强制的前提下，版本漂移是唯一的实际风险：不同开发者本地安装不同版本会得到不同发现，
使 lint 结论不可复现。钉版本后无需本地安装即可运行。

**Alternatives Considered**
1. 不钉，依赖开发者本地安装 —— 零成本但不可复现，且与仓库既有工具管理方式不一致。
2. 钉在 CI 而非 tools/go.mod —— 当前无 CI（用户明确不做），不适用。

**Tradeoffs**
`tools/go.mod` 由 104 行增至 272 行（全部为 indirect 依赖）。该模块本就为隔离工具依赖
而独立存在，不进入运行时依赖图，因此代价可接受。已实测：钉定后运行结果与本地 v1.64.8
完全一致（0 findings）。

**Scope**
`apps/backend-api/tools/go.mod`、`Makefile`、`tools/README.md`。

**Status**
已确认（用户授权代为决定）。

---

## D3 — IAM schema 使用单一迁移流，目标为空库

**Decision**
IAM 域 9 个模块的 63 张表统一由 `internal/iamschema` 迁移流拥有；不按 Go 模块拆分
schema 流。目标数据库为全新空库，不实施存量数据 ETL。

**Reason**
IAM 与 organization 之间存在双向外键，跨流无法形成可执行 DAG；单一迁移流保留
全部数据库约束且无需改变现有迁移执行器。用户已确认目标无存量迁移要求。

**Tradeoffs**
不能单独 down 某个 IAM 子模块；表归属改由 `OWNERSHIP.md` 和集合相等测试维护。

**Scope**
IAM 域全量迁移 Phase 1–9。

**Status**
已被 D10 取代。外键环只能证明需要更细的迁移执行里程碑，不能证明应剥离模块 SQL 所有权。

---

## D4 — IAM 文本比较按语义分类

**Decision**
业务名称唯一键统一存储 `strings.ToLower(strings.TrimSpace(value))` 生成的 `name_key`，
不折叠重音；枚举、协议标识符、哈希/令牌、WebAuthn handle 使用 binary collation。
菜单 `label` 不参与唯一约束，不由 collation 承担国际化。

**Reason**
源库已有四类 `name_key` 使用同一规范。MySQL 的 `utf8mb4_unicode_ci` 会把大小写不同
的协议/安全标识符视为相等，也会令 CHECK 接受大小写变体；PostgreSQL 与 SQLite 默认
行为不同。分类处理可使三方言一致，并修复 WebAuthn handle 的身份匹配风险。

**Tradeoffs**
角色名等业务名称在 PostgreSQL 上从大小写敏感改为不敏感；这是用户确认的预期语义。
动态菜单多语言仍需独立产品决策。

**Scope**
IAM schema R16 及对应写入路径。

**Status**
已确认。

---

## D5 — IAM entity type 在建立关系后不可变

**Decision**
关系表使用包含 `resource_type` 的复合外键，保持源库 `ON DELETE RESTRICT`；实体一旦
被关系引用便不可修改 type。

**Reason**
源库 `iam/entity.go:273-281` 已明确执行该业务规则。数据库复合外键不是新增限制，
而是关闭应用层“先查关系数、再更新”之间的 TOCTOU 窗口。

**Tradeoffs**
未来若产品需要修改已引用实体的 type，必须显式设计关系迁移，不能依赖隐式级联。

**Scope**
IAM schema R9'' 与实体更新服务。

**Status**
已确认。

---

## D6 — IAM 迁移只支持已确认的 Mac/Windows 两机拓扑

**Decision**
迁移环境由 `make iam-preflight` 自动解析。Mac 使用
`/Volumes/Macintosh/Projects/chaosplus/chaosplus/apps/server` 与本机 Docker；Windows
使用 `D:\Projects\Chaosplus\chaosplus-api\apps\server`，无本机 Docker 时通过专用 SSH key
驱动 `root@10.0.0.100` 的 Docker。源基线固定为 `apps/server` tree
`201052baddd745d1c2727cf29bbc3c68253a1af0`；MySQL 固定为
`mysql:8.4@sha256:b3b90af2a6552ae30c266fdb7d5dd55f3afb72404bb78d37fe8a23eb857fd3fb`。

**Reason**
用户明确只在原 Mac 与当前 Windows 两台设备间轮换。单机路径、Docker Desktop 路径和
`localhost` 容器假设在换机后均失效；自动预检可在继续实现前发现源漂移、CRLF、Docker
transport 与三方言表集合问题。

**Alternatives Considered**
1. 要求两台机器手工维护同一路径与 Docker Desktop —— 容易再次产生陈旧状态。
2. 把 SSH 密码写入脚本 —— 不可接受；专用 key 只保存在机器 `~/.ssh`。
3. 支持任意第三台机器 —— 当前无需求，使用 `IAM_SOURCE_ROOT` 等覆盖项已足够应急。

**Tradeoffs**
Windows 依赖局域网主机 `10.0.0.100` 可达；Mac 的预检与完整门禁仍需回到该设备后实跑一次。
源 `apps/server` 发生任何变更时必须显式复审并更新 tree，不能静默迁入新版本。

**Scope**
IAM 迁移的本机预检、MySQL 8 门禁与换机恢复，不改变生产部署拓扑。

**Status**
已确认（用户明确限定两台设备并要求自动检测处理）。

---

## D7 — IAM 迁移不新增动态菜单多语言表

**Decision**
本次保持固定 source tree 的 63 张表，不新增 `iam_menu_translations`，`iam_menus.label`
继续保持源系统的单值语义。编译期系统消息仍使用现有 `pkg/i18n`，不把它扩展为租户动态
内容翻译系统。

**Reason**
用户要求全量迁移原有功能。源库只有 `iam_menus.label`，目标也不存在动态菜单翻译模型、
回退规则或管理 API；新增第 64 张表会引入新产品行为，不属于兼容迁移。

**Tradeoffs**
迁移后动态菜单仍只能保存一个 label。未来若产品明确需要多语言菜单，必须单独设计 locale
唯一键、默认语言、回退规则、CRUD/API 兼容与存量填充，不能混入本次迁移。

**Scope**
IAM schema 表集合、菜单 model/repository/API 与 Phase 1 baseline。

**Status**
已确认（按“迁移现有功能、不静默新增产品功能”的范围约束关闭）。

---

## D8 — IAM 目标采用 bounded context + 私有 capability 两级结构（已废弃）

**Decision**
9 个 IAM 一级模块继续作为 `pkg/module` bounded context；每个模块内部按业务能力分入
`internal/<capability>`，并把 gorm adapter、Huma adapter、跨能力 workflow 分别放入
模块私有的 `internal/store`、`internal/httpapi`、可选 `internal/workflow`。模块根只
负责生命周期、依赖注入和协议注册。跨模块协作只允许使用按提供方领域分组的
`pkg/port/{id,authz,audit,iam,authn,identity,organization}`，不直接导入对方实现。

**Reason**
源库的一级分模块清楚，但 IAM 等模块内部仍把领域、bun 仓储、REST 和装配放在同一个 Go
package。原样平移只会得到更整齐的大包，不能满足“迁入后结构更清晰”。两级结构保留业务
边界，同时通过 Go nested `internal` 和自动架构测试形成可执行约束。

**Constraints**
- capability 不依赖 gorm、Huma、`pkg/module` 或 adapter。
- repository port 由消费 capability 声明，store 负责 domain/model 转换。
- httpapi 不直接访问 gorm/query/model。
- sibling capability 不直接互相导入；必要的原子流程由 module-local workflow 和消费侧
  声明的 transaction view 协调，store 提供同一事务绑定的 repository 实现，不暴露 gorm DB。
- port DTO 不引用业务模块内部的 domain/model 类型，避免从中立端口反向泄漏实现。
- 不把 capability 注册成独立模块，不拆 schema flow，不为目录整齐制造单函数包。

**Tradeoffs**
相对源库会增加 package 数量和 domain/model 映射代码；代价换取编译期依赖方向、可单测领域
逻辑和更小的变更半径。小模块只拆有独立不变量的能力，避免过度分层。

**Scope**
Phase 2–8 的 9 个业务模块、覆盖率登记与 `tests/architecture`；不改变 63 表单一迁移流、
REST 基线或业务行为。

**Status**
已被 D9 取代。该方案只改 IAM 域为 Ports-and-Adapters，会让仓库同时存在两套模块架构；
用户明确要求若采用该架构必须全项目统一改造，而不是局部引入。

---

## D9 — IAM 迁移沿用项目原生模块，直接按 feature 子包组织

**Decision**
不在本次 IAM 迁移中引入 Ports-and-Adapters、中央 `pkg/port`/`internal/port` 或
`internal/<capability>` 嵌套层。9 个业务模块继续使用现有 `pkg/module` 生命周期、
`model/query/dbgen/i18n` 和 `ProvideServices/InjectServices` 模式；大模块直接按 feature
分包，例如 `internal/iam/{access,authorization,resourcegraph,administration}`。

每个 feature 包按需要使用 `domain.go`、`service.go`、`repository.go`、`http.go`。
跨模块契约沿用 `guid.Source`、`audit.Recorder` 模式，由 provider root 稳定导出，消费者
只导入 provider root，不导入 feature、model、query 或 concrete service。

**Reason**
当前项目已经有一致且经过测试的模块结构。只给 IAM 域增加 ports/adapters 会形成第二套
架构，增加理解和维护成本。另一方面，IAM 源模块规模远大于现有 todo，全部放根包也不够
清晰；直接 feature 子包在不改变项目架构哲学的前提下缩小 package 责任。

**Constraints**
- `internal/app` 是唯一总装配位置。
- 所有真实模块依赖必须同时写入 `ModuleDependencies()`。
- 跨模块只能导入 provider root contract。
- repository 文件负责 gorm/model/query；HTTP 文件负责 Huma；模块根只负责生命周期和汇总。
- feature 不是独立 `pkg/module`，不拥有 migration flow。
- 若未来采用 Ports-and-Adapters，必须另立全项目 ADR、迁移顺序与统一门禁。

**Tradeoffs**
IAM 域模块会比现有小模块多一层 feature package，但不会引入 adapter/port 抽象体系。
feature 间原子流程仍需模块内 workflow 明确协调，不能靠任意 sibling import。

**Scope**
Phase 0 的跨模块契约位置、Phase 2–8 目录结构、依赖门禁与覆盖率登记；不改变 63 表、
REST 基线和业务行为。

**Status**
已被 D10 取代。用户否决按 feature 再拆包，要求 9 个原模块统一归入 IAM 域目录并保持模块自治。

---

## D10 — IAM 域目录下 9 个自治模块，各自拥有 SQL

**Decision**
`internal/iam/` 只作领域命名空间，不含 Go 文件、生命周期或集中迁移。其下固定为
`core`、`authn`、`identity`、`oauth`、`organization`、`audit`、`governance`、
`provisioning`、`federation` 9 个自治 `pkg/module`；`core.ModuleName()` 仍为 `iam`。
每个模块按 `internal/todo` 使用扁平根包与 `model/query/dbgen/i18n`，拥有自己读写表的
`migrate.go`、三方言 SQL 和版本表。跨模块契约直接由 provider 根包声明，不设中央 port。

源 `modules/iam/sql` 混放的 36 表按实际职责拆为 core 13、authn 13、identity 3、oauth 4、
audit 3；organization 14、governance 4、provisioning 5、federation 4 保持原职责，总计 63。
跨模块 FK 仍由拥有子表的模块维护；基础表和 constraints 作为迁移里程碑构成 DAG，解决
iam↔organization 环，不能以环为理由集中 SQL。

**Reason**
`internal/todo` 已确立模块同时拥有生命周期、模型、查询与 migration source。集中 schema、
局部 Ports-and-Adapters 或 direct feature 分包都会制造第二套结构，削弱模块自治并偏离用户
确认的 IAM 域边界。

**Tradeoffs**
需要为现有迁移器增加可选 milestone 编排，并把源 iam SQL 按真实写入模块拆分；换取目录、
Go 包、数据模型、SQL 与版本状态的一致所有权。

**Scope**
IAM 全量迁移的目录、接口、表归属、Phase 1 迁移编排及 Phase 2–9 实现。

**Status**
已确认（用户明确要求 `internal/iam/{...}`、模块 SQL 自治并取消公共 port 包）。

---

## D11 — IAM 第三方依赖按源行为选型并即时引入

**Decision**
按固定源树的 `go.mod` 与生产 import 图保留以下完整模块路径和职责；除已落地的 WebAuthn 外，
只在对应实现任务开始时加入目标 `go.mod`：

| 类别 | 选定模块与源版本 | 引入时机 | 结论 |
|---|---|---|---|
| WebAuthn | `github.com/go-webauthn/webauthn v0.17.4` | Task 0.4（已引入） | `webauthnx` 与 passkey 的协议/凭据实现；迁移期间不升到 v0.18.0 |
| TOTP | `github.com/pquerna/otp v1.5.0` | Task 3 authn | TOTP/HOTP 唯一实现 |
| SAML | `github.com/beevik/etree v1.6.0`；`github.com/crewjam/saml v0.5.1`；`github.com/mattermost/xml-roundtrip-validator v0.1.0`；`github.com/russellhaering/goxmldsig v1.6.0` | Task 7 federation | 分别承担 XML、SAML 协议、XML 圆返攻击校验与 XML 签名，不是重复实现 |
| SCIM filter | `github.com/scim2/filter-parser/v2 v2.3.1` | Task 7 provisioning | SCIM filter/patch 语法解析唯一实现 |
| 文本验证码 | `github.com/mojocn/base64Captcha v1.3.8` | Task 3 authn | digit/string/math captcha 与嵌入字体 |
| 滑块/点选验证码 | `github.com/wenlng/go-captcha/v2 v2.0.5` | Task 3 authn | slide/click captcha；生产代码复用 `base64Captcha` 字体，不能删除前者或后者而保持源功能 |
| 审计对象存储 | `github.com/minio/minio-go/v7 v7.2.1` | Task 6 audit | WORM anchor 对象存储客户端 |
| Redis client | `github.com/redis/go-redis/v9 v9.21.0` | Task 8/9 composition 评审 | 仅由源 app 组合根创建，IAM 模块不直接依赖 |
| Redis rate limit | `github.com/go-redis/redis_rate/v10 v10.0.1` | Task 8/9 composition 评审 | 源 `ratex` 的全局 HTTP middleware；与 Redis client 是上下层关系而非替代关系 |

截至 2026-09-02 的 `go list -m ...@latest` 显示：TOTP、crewjam/saml、XML 圆返校验、SCIM、
两套验证码和 redis_rate 已是源版本；WebAuthn、etree、goxmldsig、MinIO 与 go-redis 有更新版本。
为先证明行为等价，迁移任务固定源版本；任何升级另做兼容性与安全评审，不混入模块移植。

**Reason**
Phase 0 的目标是冻结选择和引入时机，不是预装未来依赖。完整 import 图证明 SAML 四库与两套
验证码库分别覆盖不同职责；贸然删库会缩减用户要求的全量功能。Redis 只出现在源 app/ratex，
提前塞进任一 IAM 自治模块会制造错误所有权。即时引入还能避免未使用的传递依赖污染主模块。

**Alternatives Considered**
1. Phase 0 一次性加入全部依赖 —— 在实现到达前没有调用图或测试证明，增加无用依赖。
2. 只留一套验证码库 —— 会丢失文本或滑块/点选能力，并破坏现有字体复用。
3. 迁移时直接采用所有 latest —— 把依赖升级与 bun→gorm/模块迁移混在一起，无法隔离兼容回归。
4. 把 Redis 注入 authn/audit —— 源 IAM 模块没有该依赖，所有权属于组合根与全局 middleware。

**Tradeoffs**
源版本可能不是最新版本；先保持行为等价，后续升级需要单独门禁。Redis 是否进入最终目标仍须
在 Task 8/9 根据全局 rate-limit 配置兼容范围决定，但不得据此缩减 IAM 模块功能。

**Scope**
Task 0.5 的依赖冻结，以及 Task 3/6/7/8/9 的依赖引入顺序。

**Status**
已确认；依赖路径、源版本、直接 import 与引入任务均已复核。

---

## D12 — HTTP 文件命名与 RESTful 目标契约

**Decision**
IAM 各模块的 HTTP 传输文件和注册入口统一使用 `http.go` / `RegisterHTTP`；不使用
`rest.go` / `RegisterREST`。RESTful 作为接口设计标准：优先复数资源、名词化路径、HTTP method
表达 CRUD、统一路径参数和状态码。源服务 208 条 operation 只作为功能完整性基线，每个模块通过
显式 source→target 映射冻结目标 OpenAPI 契约，不要求复制源 RPC 风格 URL。

源 IAM 没有 gRPC proto/service 契约，本次迁移不创建占位 `grpc.go` 或虚构 gRPC API。未来只有在
出现真实内部调用契约时，才以 `grpc.go` / `RegisterGRPC` 与 HTTP 并列实现。

**Reason**
`http` 准确描述传输协议，也能与 `grpc` 对称；RESTful 则属于 HTTP API 的资源建模规范，二者不是
互斥命名。逐条映射同时保留全量迁移的可追溯性，并允许目标接口纠正源服务的动作式路径。

**Tradeoffs**
目标 URL、method 和 OperationID 可能与未发布的源接口不同，因此不能再用源三元组直接做集合相等；
必须维护 source→target 映射及目标契约测试。当前新项目 authn 尚未发布，不保留旧路径别名。

**Scope**
IAM Phase 2–9 的 HTTP 文件、OpenAPI 契约和验收门禁；不新增 IAM gRPC 功能。

**Status**
已确认（用户明确要求 HTTP 命名并尽可能按 RESTful 标准设计）。

---

## D13 — IAM 性能基线分离可移植计划门禁与同机时延门禁

**Decision**
四条 IAM 热路径使用生产组合和代表性多租户基数捕获实际 GORM SQL，对 PostgreSQL 16 与真 MySQL 8.4
保存 JSON `EXPLAIN`、规范化 SQL fingerprint 和 5 份 Go benchmark 样本。默认门禁始终验证 SQL 集合、
fingerprint 与计划结构，拒绝缺少 tenant/directory/全局唯一键约束的全表扫描，并对授权、session 与 SCIM
关键已填充表强制索引访问。绝对时延阈值取同机 5 份样本最大值的 3 倍并向上取整，只在
`IAM_PERFORMANCE_ENFORCE_THRESHOLDS=1` 时执行；基线更新必须显式设置 `IAM_PERFORMANCE_UPDATE=1`。

**Reason**
SQL 与访问计划回归可跨开发机稳定判断；绝对时延会受 CPU、虚拟化和远程 Docker 网络影响，尤其当前
MySQL 8.4 经 SSH Docker transport 暴露 TCP。把结构正确性设为默认硬门禁、把时延限定为同机显式门禁，
既持续捕获 tenant 条件或关键索引丢失，又避免把机器差异误报为代码回归。

**Tradeoffs**
跨机器默认运行不会因纯时延退化失败，发布机需要显式启用时延门禁。原始 plan 会随数据库版本变化，
引擎升级时必须复审并重新生成对应基线，不能仅接受 artifact diff。

**Scope**
Phase 9 的 authorization、browser session、SCIM bulk 25 和 audit append 性能验收；不改变生产查询、
索引或运行时配置。

**Status**
已确认并由 PostgreSQL 16.9/MySQL 8.4.11 双引擎基线验证。

**Amendment 2026-09-15 —— 时延门禁真正接上了发布路径**
本决策写明「发布机需要显式启用时延门禁」,但 `scripts/test-mysql8.sh` 从未设置过那个变量,
于是 `make test-mysql8-run`(= `release-check` 的一环)长期只校验 SQL 集合与计划指纹,
**时延一直是量而不判**。现已在该脚本的性能步骤加上 `IAM_PERFORMANCE_ENFORCE_THRESHOLDS=1`。
作用域仅此一条命令(前缀赋值不 export),`make ci` 里 `tests/performance` 的 PostgreSQL 那条
继承 `os.Environ()`,拿不到它 —— 与本决策「跨机器默认运行不因纯时延失败」一致,刻意不动。

**验证**:开启后真实 MySQL 8.4 跑 PASS;另做变异验证 —— 把 `scim_bulk_25` 阈值降到 60ms 后
门禁如期 FAIL(`exceeded the same-machine frozen threshold`,5 个样本逐条报),基线随即还原。
**门禁确认是活的,不是空开关。**

**随之暴露的约束(未处置,后续动基线时必须知道)**:本决策设计的「最大值 3 倍」余量,
现已被真实变化吃掉一部分 —— 当前余量 authorization 3.53x / audit_append 3.47x /
session_lookup 2.83x / **scim_bulk_25 仅 2.04x**。且冻结样本本身取自 2026-09-03 的机器状态:
把基线提交 `3277e5e` 的**同一份旧代码**在今天同机同镜像重跑,`scim_bulk_25` 是 58.56ms 而非
冻结的 50.77ms。即这份余量同时在吸收「机器差异」与「累积的真实变化」两件事,
scim 那条已接近设计意图的下沿。重测基线等于抬高门槛,属独立决策,未做。

---

## D14 — SQLite 默认事务使用 `BEGIN IMMEDIATE`

**Decision**
`normalizeSQLiteDSN` 在调用者未显式设置 `_txlock` 时注入 `_txlock=immediate`。调用者的显式配置保持
不变；busy timeout 继续使用 `mattn/go-sqlite3` 的 5 秒默认值或调用者提供的值。PostgreSQL 与 MySQL
配置不受影响。

**Reason**
Mac 最终 CI 在 SCIM ETag 更新和认证 session rotation 两条并发 CAS 路径上稳定暴露
`SQLITE_BUSY_SNAPSHOT`。deferred transaction 先读后升级为写事务时，即使配置 busy timeout，也不能在
另一个 writer 已提交后继续使用旧 snapshot。`BEGIN IMMEDIATE` 在事务开始时取得写入资格，使 SQLite
的单 writer 约束在业务读取前确定，失败方随后能观察新版本并走既有 CAS 冲突语义。

**Alternatives Considered**
1. 只给 provisioning 增加事务重试 —— authn 随后出现同一问题，且会在各模块复制驱动错误处理。
2. 只修改并发测试 DSN —— 会掩盖默认生产配置的相同行为。
3. 强制覆盖调用者显式 `_txlock` —— 不必要地取消 SQLite 驱动已有的可配置能力。

**Tradeoffs**
SQLite 写事务会更早串行化，长事务可能让后续 writer 更早等待；SQLite 本身同一时刻只允许一个 writer，
因此这改变的是锁获取时机而不是写并发上限。显式选择其他事务模式的部署需自行处理相应 contention。

**Scope**
共享 GORM SQLite datasource 配置，以及依赖其事务语义的 IAM 并发 CAS 路径。

**Status**
已确认；datasource 配置测试、SCIM ETag 与 session rotation 各 20 轮并发回归、相关全包 `-race`、
完整 `make ci`、真实 MySQL 8.4 与 live health 均通过。

## D15 — 前端 admin 采用 bun + turborepo workspace，复用 chaosplus `packages/ui`

**Decision**
`apps/frontend-web-admin` 建为 bun 1.3 + turborepo 2 的 workspace（`apps/*` + `packages/*`），而不是
单应用。首个 package 是 `@workspace/ui`，直接取自
`chaosplus-api/apps/admin/packages/ui`，保持包名 `@workspace/ui` 与其 `exports` 映射不变。
`packages/ui` 是**唯一**共享组件库，后续 IAM 应用与商城应用共用，不允许各自 fork 一份。
构建/校验命令统一为 turbo 的 `build` / `lint` / `format` / `typecheck` / `test` 五条。

**Reason**
`chaosplus-api/apps/admin/packages/ui` 与 `aspeed-framework/apps/7link-mall-web/packages/ui` 实测为
同一套组件库：各 145 个源文件、42 个组件，文件名 100% 重合；内容差异在归一化引号、分号和
`prettier-plugin-tailwindcss` 的 class 排序后消失，即只有格式差异。两侧业务前端已按
`apps/*` + `packages/*` 组织（旧 `apps/iam` 的 `vite.config.ts` 注释显示它本身就是从商城前端
`apps/web` 抽离出来的）。沿用同一形状可让 IAM 24 个页面与商城 124 个页面迁入时零结构改造，
并把组件库收敛为一份。

**Alternatives Considered**
1. 单应用（非 workspace）—— 商城 124 个页面迟早要接入，届时必须重做结构。
2. 取商城那份 `packages/ui` 作基线 —— 与迁移源 `apps/iam` 的格式风格不一致，会给 24 个页面的
   迁移额外制造 diff 噪声。两份实质相同，取迁移源那份摩擦更小。
3. 重命名 `@workspace/ui` 为 `@7link/ui` —— 需改动 145 个文件的 import，收益仅为命名偏好。
4. pnpm workspace —— 迁移源与商城均已用 bun，且本机 bun 1.3.12 与源 `packageManager` 声明一致。

**Tradeoffs**
沿用 `@workspace/ui` 这一通用包名，在未来若引入第二个组件库时语义偏泛；但改名的代价是全量
import 改写，当前无第二个组件库的需求。turborepo 增加一层任务编排，对单 package 是额外开销，
在第二个 app 迁入后才体现价值 —— 这是有意的前置投入，因为 workspace 结构本身已按 D15 确定。

**Scope**
`apps/frontend-web-admin/**`。不触及 `apps/backend-api`，不修改 `chaosplus-api`（只读迁移源）。

**Status**
已确认（用户 2026-09-04 授权"按推荐的最优方案"）。Phase A 已实测：`bun install` 1221 包，
`bun run typecheck`、`bun run lint` 通过，`bun run test` 85 pass / 0 fail。
`bun run build` 当前无 task（`@workspace/ui` 是源码包，无 build 脚本），待首个 app 迁入后生效。

## D16 — 前端 API 客户端按新 RESTful 契约重建，不平移旧 `iam-api.ts`

**Decision**
`apps/backend-api` 的运行中 OpenAPI 是前端 API 契约的唯一事实来源。迁移旧
`apps/admin/apps/iam/src/lib/iam-api.ts`（1711 行）时，必须先冻结"旧调用点 → 新 operation"
的逐条映射表，再实现；`/authn/*` 全部重新绑定。类型层由 OpenAPI 生成，业务调用走手写薄封装。

**Reason**
D12 已把 HTTP 目标契约改为 RESTful，源 RPC 风格 path 不再保留。实测比对旧前端调用的路由与
新后端非测试代码注册的路由：`/iam/*`、`/oauth/*` 全部命中，`/authn/*` **18 条全部落空**
（`/authn/login` → `POST /authn/sessions`、`/authn/session` → `GET /authn/sessions/current`、
passkey/邮箱验证/密码找回全部改名）。新后端另有旧前端没有的 `/authn/captchas`、
`/authn/step-up-challenges`、`/authn/step-up-verifications`。平移旧客户端必然全面 404。

**Alternatives Considered**
1. 全手写客户端 —— 与 D12 的契约漂移同源风险再次出现，且 208 条 operation 的类型需人工维护。
2. 全自动 codegen 直接给页面调用 —— 生成的调用风格与旧 24 个页面的既有用法差距大，
   迁移期 diff 噪声过高。
3. 在后端加旧路径兼容层 —— 与 D12 的 RESTful 目标契约直接冲突，且为已废弃的客户端形态续命。

**Tradeoffs**
codegen 引入一个生成步骤和生成物指纹门禁的维护成本，换取契约漂移在 CI 阶段即被发现。
薄封装层需要人工保持与生成类型同步。

**Scope**
`apps/frontend-web-admin/apps/iam` 的 API 客户端层（Phase B）。不改后端契约。

**Status**
已确认方向（用户 2026-09-04 授权）。

**2026-09-04 更新（Phase B 完成，映射表已冻结）**
后端已把「旧 operation → 新 operation」冻结在 `apps/backend-api/internal/app/testdata/iam_http_mappings.txt`
（211 条），并由 `bootstrap_iam_openapi_test.go` 的 `iamHTTPMappingCount = 211` 守住。
据此逐条 join 后：旧 `iam-api.ts` 共 144 个调用点，**只有 20 条契约有实质变化**
（`/authn/*` 18 条 + `/iam/principals/{id}/disable|restore` 2 条），其余 124 条原样可用。
完整表见 `apps/frontend-web-admin/docs/frontend-migration-plan.md` 第八节。

本条原文中「`/authn/session` → `GET /authn/me`」是当时的推断，**是错的**，已就地改正为
`GET /authn/sessions/current`；`authn-me` 是另一个独立 operation，两者并存。
**2026-09-04 再更正（@Claude 指出，已用 git 复核）**
上一段里「211 是 target 侧、208 是 source 侧」是错的。208 和 211 是**同一张表的两个版本**：
`git show HEAD:...iam_http_mappings.txt` 为 208 条、`iamHTTPMappingCount = 208`；
工作区多出 3 条 `oauth-consent-page|request|decision`（@Claude 当日新增的 OAuth 同意页），
故为 211。**该表处于活跃修改中，引用时必须注明版本。**

这 3 条不影响 Phase B 结论：它们是新增接口，旧前端不可能调用过，落在「旧前端未使用」那一桶里。
因此该桶按 HEAD 计为 **67** 条、按当前工作区计为 70 条；144 个调用点的映射一条都不受影响。

---

## D17 — Phase 10 的五个产品缺口按主流 IAM 通行做法定，不再逐条等产品确认

**Date** 2026-09-05

**Decision**
10.3 / 10.4 / 10.5 / 10.8 / 10.10 原先在计划里标记为「需产品确认」，长期空转。用户明确
指示按 IAM 领域的标准、通用、共识做法自行决定（"按标准的 通用的 大家共识的做法"），
下述五条即为所选方案，实施时逐条对照规范与参考实现核对，不凭印象写。

- **10.3 principal 管理权限**：按 Keycloak 的 realm 模型，改人属于 realm 管理员的
  租户内能力。**读代码后更正了本条最初的写法**：涉及的是三条而不是六条 operation
  （`identity-update-principal`、suspend、resume），而且方向相反——它们本就是租户内
  操作（路由带 tenantID，service 方法也带 tenantID），却错挂了 `platform_administer`。
  挪去 platform 平面等于删掉「租户管理员管自己租户的人」，正是要保住的能力。
  实际修法：三条改用权限表里现成的 `user/update`（scope=tenant），与同文件的
  list/create/get 用 `user/view`、`user/create` 一致。结构性的一半照做且更必要：
  让 `authz.Register` 拒绝把 platform scope 的 action 注册到 tenant registrar，
  与 `RegisterPlatform` 早已有的反向检查配对——缺的正是这一半，这个 bug 才漏出去。
- **10.4 数据权限**：范围过滤下沉到 repository 查询层（放在 controller 等于可绕过）。
  以 `internal/todo` 为参照实现接 `DataConstraint` + `RegisterEntity`（用户 2026-09-05
  确认用 todo 模块当用例），覆盖 tenant A/B、owner/部门/上级/显式 deny、分页 count、导出。
- **10.5 字段权限**：对标 Keycloak 声明式 User Profile 的每字段 `view`/`edit` 角色授权，
  与 SCIM 2.0 的 `mutability`/`returned` 属性特征。声明式字段策略、读写分离、deny 优先；
  读端在序列化投影强制，写端在校验阶段拒绝而非静默丢弃。前端只做展示，不做判定。
- **10.8 principal 生命周期**：SCIM + GDPR 的通行组合——停用（`active=false`）→ 匿名化
  PII → 保留主体行作墓碑。审计永不硬删，外键与审计链因此始终成立。
- **10.10 IAM gRPC**：**不做，关闭该项。** 主流 IAM 产品面是 REST + OIDC/OAuth2/SCIM，
  项目已具备。

**Reason**
这五项被标为「待产品确认」已阻塞多轮，而它们在 IAM 领域都有成熟且收敛的答案，不属于
本项目特有的产品分歧。继续等待是把行业已解决的问题当成本项目的开放问题。

**Alternatives Considered**
1. 继续逐条等确认 —— 已实测无效，且把工期消耗在没有实质分歧的地方。
2. 各自另设一套自创模型 —— 违反用户「要标准、要通用」的明确要求，且增加长期维护成本。
3. 10.10 建占位 gRPC service —— 无消费方、surface 未冻结，等于维护两份 API。

**Tradeoffs**
自行决定意味着若与用户预期不符，返工成本落在实现之后而非之前。为此每条都在频道公示并
说明所依据的标准，用户可随时否决其中任意一条；未否决即按此执行。

**Scope**
`apps/backend-api` Phase 10。10.10 关闭后，10.11 最终门禁的范围相应缩小。

**Status**
已决定并在 #sevenlink-platform-win 公示（2026-09-05）。10.4 已获用户明确确认。

---

## D17 implementation refinement - profile claims and SCIM PATCH

**Date** 2026-09-05

**Decision / Reason**
Tenant-controlled profile fields are projected only after a response tenant is
resolved. Global, unscoped sessions and principal tokens omit these attributes;
they do not guess a tenant or bypass tenant policy. Raw internal authentication
and identity provisioning retain the actual values. Email verification follows
email visibility, so hidden or masked email cannot expose a verification flag.
OIDC profile/email scopes further restrict token and userinfo output; openid
alone authorizes the subject identifier, not these optional profile attributes.
ID token issuance receives the consented scope explicitly. Bearer profile views
re-read the current principal rather than treating an old token's masked values
as current source data. Existing signed JWT payloads remain immutable.

SCIM PATCH must use a transactional raw snapshot and retain its version even
without a client If-Match. The public projected GetUser result is never an
input for persistence. PUT and PATCH share the same versioned write operation.

**Alternatives / Tradeoffs / Status**
Returning all global profile data would bypass tenant policies; filtering only
at token issuance would miss later policy revocations in userinfo. Making every
response field required would misrepresent omission. Optional public DTOs require
OpenAPI and frontend updates. Profile catalog registration, SCIM enforcement and
the generated contracts are now integrated; real composed tests cover bound
actors, role policy changes, raw PATCH preservation and outbound projection.
Profile browser acceptance now covers ordinary tenant administrators on desktop
and mobile, masking/omission, partial writes and tenant reset. Full legacy browser
regressions and final production acceptance remain open; this refinement does
not mark 10.5 or production delivery complete.

## D19 - SCIM automation uses explicit tenant-owned service accounts

**Date** 2026-09-05

**Decision**
As a prerequisite for D17 field policies, SCIM directories and outbound targets
bind an existing service account using optional `service_account_id`. Identity
owns the validator capability; provisioning passes its active transaction. The
account must belong to that tenant and have an active principal, membership and
owner tenant, no pending activation, no deletion and no expired lifetime.
No borrowed administrator identity, synthetic principal or automatic role grants.

**Reason**
Core field-policy evaluation requires a real principal with effective tenant
roles. Directory credentials and target configuration previously provided only
tenant/resource scope, not an automation principal. Reusing service accounts
keeps identity ownership and revocation semantics in their existing module.

**Alternatives Considered**
Borrow the invoking administrator (incorrect for background synchronization),
invent actor identifiers (no membership/role lifecycle), or automatically create
and authorize accounts (unrequested privilege grants).

**Tradeoffs / Scope / Status**
Additive provisioning v4 references identity.base. Binding changes use existing
optimistic versions and audit transactions; PUT null/omission clears the binding.
An invalid old binding does not prevent disabling or repairing a configuration.
Inbound audit records the bound actor; outbound records the initiating human,
when present, plus the actor ID, and background work records the bound actor.
NULL is allowed for configuration repair, but no longer permits automation:
inbound SCIM rejects it with 401 and outbound operations with 409 before remote
calls. Deployments must explicitly bind service accounts and configure their
roles before upgrading. Profile enforcement uses the bound actor and the default
catalog now includes those fields. This does not complete product acceptance.
Structural down drops bindings and
is not an operational rollback for an actor-dependent production deployment.

## D21 - Principal lifecycle implementation boundary

**Date** 2026-09-06

**Decision / Reason**
D17's irreversible erasure applies to a global principal, not one membership.
Its management surface therefore requires platform administration; existing
tenant profile and suspension permissions are unchanged. Require prior suspension,
an expired retention deadline and no legal hold. Do not invent a universal legal
retention period: an explicit platform retention policy records the deadline.
Use optimistic lifecycle versions and a principal row lock for lifecycle writes.
Identity owns the lifecycle record and permanent tombstone protection; operational
PII owners must clean their records in the same caller-owned transaction. Missing
cleanup capability fails closed. Historical audit records are not deleted or edited.

**Alternatives / Tradeoffs / Scope / Status**
Tenant-local global erasure would cross tenant authority. Clearing only the main
profile would leave copied PII and credentials. Automatic timed erasure without
an explicit operator action is not required here. This is implementation in
progress, not acceptance: cross-owner cleanup, concurrent-write prevention, UI,
multi-engine verification and final release gates must precede completion.

## D20 - Durable audit archive execution

**Date** 2026-09-06

**Decision**
The audit module owns archive jobs, SQL/model/query code and Start/Stop worker.
There is no reusable application job scheduler in the current repository; use
the established module lifecycle rather than add a generic scheduling framework.
Reuse the existing NDJSON export protocol and S3 object-lock client. Archives use
the configured anchor bucket under a separate prefix; enabling archives does not
implicitly enable anchoring. Each job freezes a contiguous sequence segment,
verified chain head, generation timestamp and minimum-retention deadline.

Jobs persist queued/running/failed/complete state. Shared database time governs
leases and retry eligibility; a unique lease token fences completion. Source
events are never deleted, consistent with D17. Only a full readback of the exact
S3 version, matching whole-file SHA-256/size and COMPLIANCE retention, permits
success. A retry reuses an existing verified artifact; a conflicting artifact
fails closed. No plaintext export temporary files are created.

**Reason / Tradeoffs**
This gives restart and multi-replica recovery without another service dependency.
Two streamed export passes determine the expected digest before upload, trading
database reads for bounded memory and avoiding audit PII on temporary disks.
The audit chain and job records remain in database backups; object versions must
be retained in bucket backups. Structural v3 rollback loses job references and
cursors and is not an operational rollback for an active deployment.

**Scope / Status**
10.7 execution and management API/UI implemented. Tenant management reads use
audit_event/view, requests/retries/downloads use audit_event/export; existing
retention uses audit_event/manage. Mirrored platform routes and platform retention
use RegisterPlatform with platform/administer, reject nonempty tenant headers,
and always scope to zero. The worker now includes platform events. Completed job
verification binds the object key to its tenant/job and enforces the frozen
retention minimum; downloads provide the verified version and content length.
Multi-pool PostgreSQL/SQLite and real MySQL queue/lease recovery now pass. A real
multipart race exposed minio-go v7.2.1 dropping If-None-Match at completion;
archive uploads now use SDK Core multipart APIs with explicit conditional
completion, streamed parts and producer validation before publication. Failure
cleanup uses a bounded uncanceled context. Real tests cover one-version replica
convergence, cancellation after a committed part, failed/changed source, lost
completion responses and 1,003 events across three chained objects. Operators
must configure incomplete-multipart lifecycle cleanup for abrupt process loss;
completed archive versions must not expire. Full real browser acceptance now
passes: an exclusive temporary SQLite file is seeded through the actual audit
service before app bootstrap; the composed app, worker and MinIO drive browser
retry, manual queueing, exact-version verification/download and access negatives.
Desktop/mobile screenshots were inspected.10.7 is closed; final combined release
gates and user acceptance remain after10.8. Do not interpret age-based archive_ready_events as
unarchived events, or this decision as completed 10.7/production acceptance.

---

## D18 — 控制台的租户来自会话，不存在编译期兜底租户

**Backend refinement (2026-09-05)**
The original frontend change assumed a tenant-bearing cookie session, but authn
sessions are global principal sessions. Both `/authn/me` and
`/authn/sessions/current` now resolve a response tenant through core's
`MemberTenantResolver`, which reads active memberships and active tenants from
the writer. An explicit `X-Tenant-Id` must be a current membership (403 otherwise).
Without a choice, exactly one active membership supplies `organization_id`;
zero or multiple memberships leave it absent. Never select the first tenant.
This is a request-scoped projection, not a persisted login-session mutation;
another browser tab is unaffected. A bearer token retains its signed tenant and
rejects a conflicting header. Bad IDs return 422 and an unavailable resolver/store
returns 503, not a guessed tenant. No tenant or role is automatically granted.
The console intentionally fetches its session without a tenant header, adopts
only into an empty selection, and keeps explicit user/deployment choices.
New ordinary-member browser acceptance starts with empty browser storage and
no injected default tenant; it covers desktop/mobile adoption, ambiguous
membership, preserved selection and immediate revocation. This closes D18's
original browser gap, not the still-open 10.5 profile projection requirement.

**Decision**
`getTenant()` 删除硬编的 `"platform"` 兜底，改为三段来源：操作员在控制台里选的（存
localStorage）→ 部署配置的 `VITE_DEFAULT_TENANT` → 空。登录成功后
`adoptSessionTenant(session)` 从 `session.organization_id` 采纳一次，**只填空值**，
操作员已经选过的不覆盖。租户为空时 `X-Tenant-Id` **整个头不发**，而不是发空串。

**Reason**
一次构建产物会部署到任意多个部署实例，构建期不可能知道某个部署有哪些租户——任何编译
进去的租户 slug 都是猜测，且是会静默生效的猜测：请求带着一个不存在或不该访问的租户
发出去，报错发生在服务端，读起来像权限问题而不是配置问题。会话本身携带了权威答案
（`organization_id`），从会话取是 Keycloak/Okta 一类控制台的通行做法。

发空串与不发头是两件不同的事：`X-Tenant-Id: ` 在服务端读起来是「租户名叫空字符串」，
是一个真实的选择；不发头才是「本次请求没有租户维度」。前者会把配置缺失伪装成数据不存在。

**Alternatives Considered**
1. 保留 `"platform"` 兜底 —— 违反 CLAUDE.md §4「不硬编品牌/部署相关字符串」，且如上
   会把配置缺失变成难以归因的运行期错误。
2. 未解析出租户时阻塞所有请求 —— 过严：登录、注册、会话建立这些请求本来就没有租户维度。
3. 每次登录都用会话租户覆盖本地选择 —— 会把操作员刚切过去的租户在下次刷新时弹回来。

**Tradeoffs**
`X-Tenant-Id` 里带的是 **id** 不是 slug（`tenants/page.tsx` 存的是 `tenant.id`），所以
能对上的是 `session.organization_id`，老的 `"platform"` slug 本来就对不上——这条同时修掉
了一个一直没被触发的错值。采纳路径有类型与单测覆盖（含「未解析出租户时不发头」一例），
但没有浏览器审计覆盖：13 个审计都显式传租户。留作已知缺口。

**Scope**
`apps/frontend-web-admin/apps/iam`：`src/lib/iam-api.ts`、`src/components/auth-provider.tsx`、
`src/lib/iam-api.test.ts`。提交 `022e5b7`。

**Status**
已决定并执行（2026-09-05）。依据用户当日指示「按标准的、通用的、大家共识的做法」自行
判定，未逐条等确认；已在 #sevenlink-platform-win 说明，用户可否决。

## D22 — 本轮交付范围锁定为 P0 + 门禁转绿，分四批推进

**Decision**
以 `apps/backend-api/docs/iam-production-readiness-audit-2026-09-07.md` 为权威清单，本轮
只关 P0-1..P0-5 并让发布门禁转绿，**不含 P1/P2**。执行分四批，批内并行、批间由主 agent
集成并跑门禁：批 0 修红线基线（gofmt、tests 子模块 lint、integration migration）；
批 1 关 P0 安全与生命周期（SSRF policy、SCIM 删除责任链、lifecycle API→OpenAPI→前端类型
→UI、七 owner 填充数据与并发验收）；批 2 补覆盖率行为测试；批 3 串行跑全量门禁、冻结
195 个迁移的 schema.lock、真实 MySQL/race/generate、UAT 环境与恢复演练。

**Reason**
当前门禁基线为红（gofmt、覆盖率 89.9%、integration 六种引擎组合全失败并 10 分钟超时）。
在红基线上累积 12-20 工程日的改动再统一验证，失败原因会互相纠缠，定位成本超过并行收益。
且存在硬顺序依赖：lifecycle API → OpenAPI 重新生成 → 前端类型 → UI → 浏览器验收；
所有迁移定稿 → 三引擎 upgrade/down/up 与真实 MySQL 通过 → 才能冻结 schema.lock，
冻早需重冻。`docs/openapi.json`、前端生成类型、`schema.lock`、`internal/iam/*/sql/`、
`tests/migration/*` 是多任务共同写入点，无序并行的合并成本高于串行。

**Alternatives Considered**
1. 一次性全部并行、最后统一测试（用户初始设想）—— 因上述共同写入点冲突与红基线纠缠而否决，
   已向用户说明并获认可。
2. 全部主 agent 串行 —— 最稳但最慢，未采用。
3. 范围扩到 P0+P1（20-30 工程日）或 P0+P1+P2 —— 用户明确选择 P0-only。

**Tradeoffs**
P1 的分布式登录限流、captcha 高可用、OIDC `prompt=none`/`azp` 协议合规、最小权限 UX、
移动端与 a11y **本轮不修**，属已知缺口。其中 OIDC 协议合规若对外声称标准 OIDC Provider
则须在 conformance 前补上；登录限流缺失意味着上线前必须由 ingress/网关承担，否则存在
用户名喷洒与恶意锁定风险。这两条须在交付说明中显式列为遗留项，不得默认已解决。

**Scope**
全 IAM 分支。审计报告为权威清单，`task-state.md` 记录批次进度。

**Status**
已决定（2026-09-07，用户确认）。批 0 进行中。

## D23 — SCIM target 删除默认 fail closed，另提供显式 deprovision 操作

**Decision**
target 存在 live remote mapping 时**禁止删除**（fail closed）；同时提供一个显式的
「删除并 deprovision」操作：持久化 deprovision 任务，在远端账号删除成功、或形成受治理的
例外记录之前，不销毁 bearer 凭据、mapping 与审计证据。两条路径都不得让 principal 擦除的
远端映射前置检查被静默绕过。

**Reason**
现状是硬删 target、数据库级联删除 mapping，而 `internal/iam/provisioning/lifecycle.go:26`
的擦除前置检查只看仍存在的 live mapping。删掉 target 后 bearer token 与 remote ID 一起消失，
远端账号和 PII 仍然存在，本地匿名化却会放行——这是一条可绕过的擦除责任链，属 P0。
只做禁止删除能立刻堵住绕过但把清理负担全部推给操作员；只做 deprovision job 则在任务失败、
远端不可达时仍需要一个 fail-closed 兜底。两者互补，缺一都留口子。

**Alternatives Considered**
1. 仅「有 live mapping 即禁止删除」—— 改动最小（1 天内），但大目录逐个手工 deprovision
   不可运营。
2. 仅「持久化 deprovision job」（3-5 天）—— 任务失败/远端不可达时若无 fail-closed 兜底，
   绕过口子仍在。
3. 保持现状并仅在 UI 提示「远端资源不会被删除」—— 提示不构成技术强制，已否决。

**Tradeoffs**
成本约等于方案 2 加半天。deprovision job 需要任务持久化、重试与例外治理面，是本轮新增的
运维语义；例外记录必须可审计，否则等于把绕过合法化。

**Scope**
`internal/iam/provisioning/`（target.go、service.go、repository.go、lifecycle.go、
sql/ 迁移）、`apps/frontend-web-admin/apps/iam/src/app/scim-directories/targets.tsx`。
需补 live mapping + 擦除绕过的负向测试。

**Status**
已决定（2026-09-07，用户确认）。属批 1，未开始。

## D24 — 允许引入 Redis 承载共享限流与 captcha，但属 P1，本轮不实施

**Decision**
用户批准为登录限流与 captcha 高可用引入 Redis。但这两项属 P1，不在 D22 锁定的本轮范围内，
因此**本轮不实施**，决策先行记录，待 P1 迭代直接采用。

**Reason**
仓库现无 Redis、无缓存层、无限流设施。captcha 当前是进程内状态
（`internal/iam/authn/captcha.go:19` 的 `DefaultMemStore`、`gocaptcha.go:81` 的包级 map），
多副本下 issue 与 verify 落到不同副本会随机失败；登录失败只更新账号锁定状态，无 IP/设备/
全局限流。共享存储是正解，用户已批准该依赖，无需再次确认。

**Alternatives Considered**
1. 照 `internal/infra/dlock` 的「数据库行 + owner token + TTL 租约」模式做 DB 支撑的共享
   限流与 captcha one-shot store —— 零新依赖、三引擎通用，但登录路径上的高频写会打到
   主库。用户选择了 Redis，本条作为备选保留。
2. 记为部署前置约束（网关限流 + sticky session）—— 把正确性押在部署配置上，未采用。

**Tradeoffs**
Redis 让生产部署多一个必须高可用的有状态组件，运维成本上升；须同时定义 Redis 不可用时的
降级语义（限流 fail open 还是 fail closed、captcha 是否阻断注册），否则会引入新的可用性
单点。这个降级语义在实施 P1 前仍需确认。

**Scope**
`internal/iam/authn/`（captcha.go、gocaptcha.go、web.go、http.go），新增共享存储设施。
本轮零改动。

**Status**
已实施（2026-09-10）。共享限流与 captcha 存储已交付：见 `apps/backend-api/docs/iam-redis-security.md`
与 `make test-redis-security`；`/authn/*` 与 `/oauth/{token,revoke,introspect}` 分别计自己的预算（D36 之后）。

## D25 — 模块的独立回滚只在自身 milestone 依赖闭包内有定义

**Decision**
一个模块的 down 迁移**允许**引用它已声明依赖的模块所拥有的表。因此「独立回滚」的契约是：
把模块回滚到某个 milestone 以下，要求该 milestone 声明的依赖当时在场。core 回滚到
`principal-erasure`(v13) 以下需要 identity 的 `lifecycle`(v4) 在场，这正是
`internal/iam/core/migrate.go:25` 已经声明的内容。生产回滚走
`app.ExecuteModuleMigrations` 的协调 reverse-DAG 路径，本就遵守该图。
`tests/migration` 中直接调用 `goosex.MigrateDownTo` 的 fixture 必须按依赖闭包组合模块，
不得只迁移单个模块的 source 就跨过带依赖的 milestone 回滚。

**Reason**
`core/sql/*/00013_principal_erasure_guards.sql` 的 down 段首句
`UPDATE iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL`
不是笔误，是**探针**：identity 的 `trg_iam_lifecycle_tombstone_update` 在
`OLD.anonymized_at IS NOT NULL` 时 RAISE(ABORT)，于是只要存在任何已提交的不可逆匿名化，
这条 no-op 更新就会中止回滚；没有则匹配零行放行。它实现的是「擦除已发生后不允许再摘掉
保护」这一安全属性，属 P0 关注范围。identity 自己的 `00004_tombstone_guards.sql:41` down
段用的是完全相同的写法，是项目既有约定而非孤例。

`TestIAMCoreMigrationsAcrossEngines` 在六种 engine×prefix 组合下全部失败，根因是
`tests/migration/iam_core_test.go:43` 只取 `(&iamcore.Module{}).MigrationSource()` 单模块
迁移，随后 `verifyCoreFieldPolicyContracts` 调 `MigrateDownTo(..., 11)` 跨过 v13，
此时 `iam_principal_lifecycles` 从未被建出。这是 fixture 与已声明依赖不一致，不是实现缺陷。

**Alternatives Considered**
1. 让 core 的 down 守卫容忍 lifecycles 表缺失（条件化/忽略错误）—— 会把一个 P0 安全守卫
   改成在表不存在时静默放行，等于用削弱实现去迁就测试，CLAUDE.md §10 明确禁止。
2. 禁止任何跨模块 down 引用，把守卫改到 identity 单侧 —— identity 无法知道 core 何时摘掉
   自己的触发器，保护会出现时间窗；且要推翻 `migrate.go` 里已声明并被协调路径使用的依赖图。
3. 保持 fixture 单模块、仅回滚到 v13 而不到 v11 —— 会丢掉 field policy 表 drop/recreate
   的既有断言覆盖。

**Tradeoffs**
测试 fixture 变重：涉及 v13 以下回滚的用例必须先迁 identity。代价是每个组合多迁一个模块的
迁移，换取安全守卫不被削弱。另需明确：`goosex.MigrateDownTo` 是低层原语，不做依赖检查；
依赖检查在 `app.ExecuteModuleMigrations`。这个分层差异须在 fixture 注释中写清，否则下一个
人会重犯同样的错。

**Scope**
`tests/migration/iam_core_test.go`。`internal/iam/core/sql/` 与
`internal/iam/identity/sql/` **不改**。

**Status**
已决定（2026-09-07）。属批 0，待子 agent 的超时排查返回后与其它 fixture 修复一并落地。

## D26 — 成熟标准方案直接实施，不逐条确认

**Decision**
凡属业界已有通用、标准、成熟解法的技术问题，直接按标准做法实施，不再向用户逐条确认。
仅当某个选择**属于产品或业务定制**、标准给不出答案（例如保留期默认天数、Redis 不可用时
限流 fail open 还是 fail closed）时才提问。

**Reason**
用户 2026-09-07 明确指示：「这些东西又不是什么新鲜的，也不是我强定制的，有通用的标准的
成熟的方案就直接做了」。这与 D17（Phase 10 缺口按主流 IAM 通行做法定）、D18（按标准通用
共识做法自行判定）是同一条授权的重申与扩展。用户不是该领域专家，把标准做法拿去请他裁决
既无信息增益，也拖慢交付。

**Alternatives Considered**
1. 每项设计仍出方案等确认 —— 已被用户明确否决两次。
2. 全部自行决定不再报告 —— 过头：产品级选择仍须用户拍板，且交付时必须说明做了什么。

**Tradeoffs**
风险在于「什么算标准」由我判断。缓解：实施时在代码注释或决策记录里写明依据的是哪条通行
规范（如 OWASP SSRF 防护、RFC 7644 SCIM、GDPR 第 17 条），使判断可被复核而非诉诸权威。
交付说明中仍需列出所有实质决定，用户保留否决权。

**Scope**
本次 IAM 交付全程，及后续同类工作。

**Status**
已决定（2026-09-07，用户明确指示）。

## D27 — SCIM 退役采用同步可续跑，不引入后台重试队列

**Decision**
D23 的第二半「显式删除并注销」实现为 `RetireTarget`：逐个注销 live mapping，全部成功后才
删除 target；任一失败即中止，保留 target、凭据与全部剩余 mapping 并返回已完成数量。
**未实现**后台重试队列与死信治理面。

**Reason**
已注销的 mapping 会被墓碑化，而墓碑化的 mapping 不计为 live，因此重跑天然从中断处继续——
幂等且可续，这已覆盖后台任务的主要价值。而持久化任务需要新增 target 状态，
`iam_scim_targets.status` 上有 `CHECK (status IN ('active','disabled'))`，改它在 SQLite 需
重建表，是一次三方言的重型迁移；在迁移即将冻结（批 3）的窗口内，这笔成本与它带来的增量
收益不成比例。

**Alternatives Considered**
1. 新增 `retiring` 状态 + 复用现有 `reconcileAll` sync 循环作为 worker —— 设计上更好，
   大目录不会阻塞调用方；代价是上述三方言 CHECK 迁移。若用户要求可后续补，属增量改动。
2. 保持仅 fail closed —— 审计允许（close condition 是「或」），但大目录逐个手工注销不可运营。

**Tradeoffs**
同步实现对超大目录会长时间占用一个 HTTP 请求，可能触发网关超时；此时操作员重新调用即可
续跑，不会丢失或重复注销。这一限制**须在交付说明中明示**，不得当作已解决。

**Scope**
`internal/iam/provisioning/target.go`（`RetireTarget`、`ErrTargetHasLiveMappings`、
`DeleteTarget` 守卫）、`repository.go`（`countLiveTargetResources`）、`http.go` 与 i18n、
前端 `targets.tsx` 与 `iam-api.ts`。

**Status**
已决定并执行（2026-09-07）。后台队列变体未实施，需用户决定是否排期。

## D28 - Explicit Integration Suite Time Budget

**Decision**
Set the configurable `INTEGRATION_TEST_TIMEOUT` to 20m in the backend Makefile.
This controls Go's cumulative per-package budget, not individual operation deadlines.

**Reason**
On 2026-09-08 the full CI migration package timed out at 600s while still progressing
through the last status matrix. The same complete package then passed independently
in 582.428s with no assertion failures. Eighteen seconds of headroom is insufficient
for the existing multi-engine, prefix, upgrade/rollback and subprocess matrix.

**Alternatives Considered / Tradeoffs**
Reducing concurrent packages cannot provide useful headroom over an isolated 582s run.
Keep all tests and assertions, expose the package budget through the existing Makefile
variable convention, and retain the new concurrency tests' 5s lock-wait and 30s operation
deadlines. A pre-existing unbounded test may now take longer to report a hang; this
budget is not evidence that any individual operation should take twenty minutes.

**Scope / Status**
Backend integration runner only; no production timeout or migration changes.
Implemented under D26's standard technical-remediation authorization. Normal `make ci`
subsequently passed; migration package took 640.915s, with all assertions intact.

## D28b — 擦除事务对锁冲突有界重试，并承认它可能被围栏反超

（编号说明：本条曾与上面的 `D28 - Explicit Integration Suite Time Budget` 重号，导致"按 D28 引用"无法消歧。
保留两条的历史位置不动，把这一条标为 D28b；全仓没有别处按编号引用 D28。）

**Decision**
`AnonymizePrincipal` 在数据库因锁循环回滚它时重试，最多 3 次；判定收敛在
`gormx.IsTransactionConflict`（pgx SQLState 40001/40P01、MySQL 1213）。**不重试**锁等待
超时（MySQL 1205）。相应地，`TestPrincipalErasureConcurrencyExternalDatabase` 的
`erasure-first` 分支承认「擦除被围栏反超」是合法结局：仅 `delivery-in-flight` 与
`live-remote-mapping` 两个围栏 owner 允许，且必须断言什么都没被擦掉。

**Reason**
擦除先锁 principal 再遍历各 owner 的表；普通请求先拿子表行锁，再经外键触及 principal。
两个方向天然相反，PostgreSQL 会发现环并回滚其中一侧，**挑谁由它决定**。原先擦除若被选中，
运维看到的是裸 `deadlock detected (SQLSTATE 40P01)`。

无法靠加锁顺序消除：写入方是任意并发请求，不受控；也不能把 principal 锁降级为
`FOR NO KEY UPDATE`——`login_name` 有唯一约束（`uq_iam_principals_login`）而擦除要改写它，
它是 key 列。因此重试是唯一正解，也是 PostgreSQL 对 40001/40P01 的官方建议。

重试带来的语义后果是**正确的而非缺陷**：回滚释放了 principal 锁，竞争方得以提交，重试时
重新评估围栏——此刻通知确实在投递中、远端账号确实存活，拒绝才是对的。原测试要求
`erasure-first` 必须成功，等于要求擦除永不输掉锁循环，而不输的唯一办法是不重试。

不重试锁等待超时：那说明对方仍持有行，立即重试只会排进同一个等待，会把容量或长事务问题
埋成静默循环。

**Alternatives Considered**
1. 让死锁上浮为可重试错误交给调用方 —— 运维仍要手工重试，且 HTTP 层要暴露一个纯数据库概念。
2. 统一加锁顺序 —— 不可行，写入方不受控。
3. 降级 principal 锁强度 —— 因 `login_name` 唯一约束不成立。

**Tradeoffs**
重试使「先拿到锁者必胜」不再成立，这一点已写进测试注释。

**验证强度（2026-09-08 更新）**：真实 MySQL 8.4.11 门禁（`make test-mysql8`）单次运行中
触发 **8 次真实死锁**，`TestPrincipalErasureConcurrencyExternalDatabase` 仍 PASS，门禁整体
EXIT=0。死锁语句正是竞争写入方对 `iam_tenant_members` / `iam_oauth_consents` /
`iam_identity_links` 的 UPDATE，与设计分析一致。至此重试吸收真实死锁已被直接观察到，
不再只有注入 40P01 的单元证据。PostgreSQL 侧：修复前失败可复现，修复后约 28 次运行
（含 2 次完整 `make ci`）不再复现。

**Scope**
`pkg/extension/gorm/retry.go`（新增）、`internal/iam/identity/lifecycle.go`、
`internal/app/principallifecycle_test.go`（erasure-first 分支）。

**Status**
已决定并执行（2026-09-08）。

## D29 — 门禁脚本必须对「docker 命令带远端 shell」成立

**Decision**
`scripts/test-mysql8.sh` 中授权 `performance_schema` 的语句改为经 **stdin** 传给
`docker exec --interactive`，不再放在 `--execute=` 参数里。

**Reason**
`DOCKER_CMD` 不保证是本地 `docker`：项目自己的 `make iam-preflight` 会为本机生成
`ssh -i ~/.ssh/iam-docker-<host> root@<host> docker`，中间因此多一层远端 shell。
`--execute="GRANT SELECT ON performance_schema.* TO 'user'@'%';"` 含空格与引号，会被那层
shell 重新分词，`mysql` 收到 `--execute=GRANT` 加散落参数，打印用法后以非零退出，整道门禁失败。

该语句由 `f371e7e` 引入，说明它只在本地 docker 传输下验证过；在本仓既定的远端传输下
**从未通过**。stdin 不会被任何一层 shell 重新分词，因此本地与远端两种传输都成立。

**Alternatives Considered**
1. 在调用方包一层用 `printf %q` 转义的 wrapper —— 只修一台机器，仓库里的缺陷仍在，
   下一个用远端传输的人照样踩。
2. 要求所有人改用本地 docker —— 与 `iam-preflight` 自动生成远端传输的既定设计冲突。

**Tradeoffs**
脚本里其余参数（十六进制口令、`--sql-mode=` 逗号串、`{{.State.Running}}`）均不含空格，
经远端 shell 后 argv 不变，故只此一处需要改。若日后再加入含空格的参数，应同样走 stdin
或显式转义。

**Scope**
`apps/backend-api/scripts/test-mysql8.sh`。

**Status**
已决定并执行（2026-09-08）。修复后 `make test-mysql8` 六个测试组全绿，EXIT=0。

## D30 — 永久擦除先建立数据库外的不可变义务

**Decision**
永久擦除在所有数据库内前置条件和 owner 围栏通过后、事务提交前，必须先把一条不含 PII 的
擦除义务写入外部 S3 Object Lock COMPLIANCE 对象。对象以主体 ID 和目标生命周期版本作幂等键；
未启用、对象锁不可用、写入或回读验证失败时，擦除返回服务不可用并回滚数据库。外部对象一旦
验证成功，即表示平台必须最终完成该次擦除；即使随后的数据库提交失败，也不能删除这条义务，
而应由重试或恢复对账完成。恢复工具必须从外部账本结构化列举义务，不能依赖已恢复数据库中的
墓碑或审计事件。

**Reason**
数据库墓碑、被擦行和 `principal_anonymized` 事件处于同一备份范围，恢复旧备份会同时复活 PII
并抹掉擦除痕迹。现有审计归档按 `archive_after_days` 延迟，默认 730 天且最短一天，不保证最近
擦除已外部化。数据库 outbox 同样会随备份回退；数据库与对象存储也没有原子提交。先建立外部
义务并 fail closed 可以保证不存在“数据库已擦除但外部毫无记录”的结局。数据库提交失败后留下
待完成义务是显式、可对账的状态，比静默丢失擦除事实安全。

**Alternatives Considered**
1. 把 `principal_anonymized` 事件立即走现有归档 —— 归档任务和游标仍在同库，提交与上传之间有
   丢失窗口，且会混淆历史归档与即时合规凭据。
2. 仅依赖 MySQL binlog/PITR —— 可以作为灾备补充，但依赖部署侧连续保留和时间点解析，不能给
   所有支持的数据库引擎提供同一擦除恢复契约。
3. 对象存储写在数据库提交之后 —— 进程在两步之间崩溃就会产生本次要消除的无痕擦除。

**Tradeoffs**
对象存储短暂不可用会阻断新的永久擦除；这是有意的 fail-closed。对象成功而数据库提交失败时会
存在尚未完成的义务，对账必须重新检查当前法律保留和主体状态后完成，不能把它误报为已经擦除。
账本复用 audit 的 S3 凭据、桶与保留期，但使用独立 `audit/erasures/` 前缀，不新增数据库迁移。

**Scope / Status**
`internal/iam/audit`、`internal/iam/identity`、应用配置、恢复 CLI、对应测试与恢复 runbook。
已实现并完成真实恢复演练（2026-09-09，依据 D26 的成熟安全方案授权）。MySQL 8.4.11 的
旧备份恢复确实复活 PII 并移除数据库墓碑/审计事件，外部义务仍以原 VersionID 和 SHA-256 可列举；
重新记录保留决策、停用并擦除后，数据库证据恢复且外部对象未被覆盖。恢复还必须重建 dump 所载
触发器的 definer 身份与权限；仅成功导入触发器不代表其可执行。删除标记后的重试还必须先搜索并
验证全部对象版本，不能因当前版本不可见而发布第二份义务。证据见恢复 runbook。

## D31 — UI-03 展示元数据使用生成查询，不扩展授权原始 SQL

**Decision**
人员授权来源仍复用 `Authorizer` 的既有授权计算结果。UI-03 新增的临时角色和业务主体绑定
有效期由 core 通过 `core/query.Bind` 读取；静态组、岗位和角色部门范围由 organization 模块通过
`organization/query.Bind` 读取，再经 core 提供的窄连接契约装配。时间窗用同一个 UTC 毫秒
`EvaluatedAt` 在 Go 中判断。不得为展示字段扩展 `effectiveRolesCTE` 或递归主体授权 SQL，也不得
复制授权优先级或让 core 直接导入 organization 的 model/query 包。

**Reason**
授权器中的 CTE 是历史核心计算路径；把仅供界面展示的 `expires_at` 加入不同分支的 `UNION` 和
递归列会扩大原始 SQL 契约，并把数据库日期类型推导差异带入 MySQL、PostgreSQL、SQLite。
生成查询对表、列和结果模型提供编译期约束，同时保持来源是否生效仍由唯一授权器决定。

**Tradeoffs**
每次授权来源快照固定增加四次有界查询，但不产生按来源逐行查询。修订号前后检查覆盖这些读取，
并发变更仍会使快照重试。历史授权器原始 SQL 不在 UI-03 范围内重写。

**Scope / Status**
`internal/iam/core/member_authorization.go`、organization 授权来源读取适配器及三方言针对性测试。
SQLite、PostgreSQL 16 无前缀/带前缀及真实 MySQL 8.4 浏览器验收均已通过；最终 `make ci`
通过，总覆盖率 90.1%，core 90.2%，organization 90.1%。

## D32 — 出站 SCIM 未知结果使用不自动过期的操作占用

**Decision / Reason**
以target持久operation token覆盖push、deprovision、配置替换及删除；首次远端PUT前提交live mapping intent。
自动同步取得claim后重新检查mapping状态，显式人工重新provisioning仍允许。客户端超时、HTTP202、
进程死亡与本地提交失败均不证明远端请求停止，因此不能依靠会过期的租约允许后续DELETE越过旧PUT。

**Tradeoffs / Scope**
未知结果会阻塞整个target，必须取得provider完成/取消证据后按精确token修复，不能自动清锁。
provisioning v6升级和回退均要求停止全部writer/worker并排空远端请求；COUNT guard不是在线屏障。
Redis失效时限流open/closed仍保留D24/D26的业务选择，不受此决策替代。

**Status**
本轮评审后修复授权范围内已实施；SQLite、真实PostgreSQL和MySQL操作并发及有数据回退保护通过。
gateway504先响应、provider迟到PUT/DELETE的双HTTP回归及变异通过；408/所有5xx/未知响应均保持claim。
真实MySQL完整门禁EXIT0，v6已纳入207文件迁移锁；生产停writer/排空请求与provider恢复取证仍须部署演练。

## D33 — OAuth撤销必须覆盖已签发的应用访问令牌

**Decision / Evidence**
真实MySQL/浏览器联测证实：个人consent撤销成功并从列表消失后，access token仍可introspect为active。
原实现只撤refresh/code，实际principal bearer校验未查询应用授权；现有“撤销结束已授访问”契约未兑现。
修复为应用授权独立grant incarnation，经consent→code/refresh→JWT传递，并在真实Bearer认证链校验。

**Alternatives / Tradeoffs**
仅查询consent是否存在会在重新同意时复活旧JWT；仅改introspection遗漏直接资源访问；全局提升
principal credential_version会误撤其他应用。采用独立随机grant代号，不依赖时间精度或同毫秒先后。
旧无grant OAuth令牌须重新授权，普通浏览器session及其他应用隔离语义保留。升级禁止新旧writer混跑；
仅删数据库grant并不能保证旧binary拒绝已签JWT，不能据此宣称安全降级。

**Scope / Status**
oauth/authn/pkg claims契约、模块内查询/三方言迁移和真实前后端验收；属于用户已授权的评审后必要修复，
不是代用户选择新的账号注销或组织归属模型。方案已二次复审并实施；三方言旧数据升级/回退保留状态、
签名后撤销交错、同毫秒regrant与其他app隔离通过，真实MySQL UAT浏览器10/10通过。
Introspect仅明确无效凭证返回inactive，基础设施/取消/未知故障保留500；HTTP回归与变异通过。
外部离线JWT消费方的即时撤销、生产升级/降级流程仍需部署验收；本地通过不代表生产已就绪。

## D34 — 应用就绪范围与Redis拓扑支持

**Decision / Reason**
用户明确本轮一次完成应用功能与代码验收，生产部署、灾备与运维演练暂不纳入；Redis集群所需
配置/客户端/错误处理属于应用代码，必须纳入。沿D26成熟方案授权补standalone/cluster/sentinel、
TLS/mTLS及ACL，保留单机URL配置，严格拒绝混用和半配置。未知网络结果不重放，保留明确重定向。

**Tradeoffs / Status**
全局Lua限流要求同slot，支持Cluster拓扑不表示认证负载跨槽分片；异步复制可能丢失消费/计数，
不附加数据库消费账本，也不宣称切主后严格exactly-once。Redis故障open/closed由启用者显式配置，
不替用户制定生产策略。真实plain/全mTLS拓扑、重定向、切主和三项变异已验证；最终门禁证据见发布检查点第2.2节。

## D35 — 个人令牌范围与本人账户操作

**Decision / Reason**
个人PAT限定一个当前有效租户、本人已有非platform接口权限及显式到期时间。现有权限/数据/字段
检查始终保留，PAT是其额外上限。opaque令牌仅存摘要，创建成功只返回一次；创建参数规范化后
绑定共享reauth目的/目标/会话/主体/credential version。消费proof与写入、审计同事务。
普通authentication-only、OAuth换发和平台入口不接受PAT，真实权限路由通过可信上下文提供
实际tenant/permission（entity adapter使用其query selector），防止scope在后续换发中丢失。
个人管理接口采用统一session-only注册，规范和运行时都拒绝Bearer。

**Alternatives / Tradeoffs**
不复用服务账号密钥，不默认无限期或制定业务最长有效期；数据库使用支持9999年的时间列，
用户显式填写期限。丢失创建响应不能再次披露secret，可从列表撤销后重新创建。
个人权限选择复用当前注册表及CheckBulk，不要求role_view管理权限，不另建权限目录。

**Scope / Status**
沿现有租户归属展示UI04；分别提供退出当前租户、申请关闭全局账号，不新建独立主体组织模型。
退出保留全局会话和其他租户，撤销所属租户OAuth/PAT并保留last-admin保护。关闭仅申请、可取消、
平台可追踪/驳回，申请本身不自动停用或选择保留期限。平台显式pending→processing处理入口
按version并发控制并原子停用/撤销凭据，覆盖零租户账号，保留全部租户last-admin及平台权限保护；
后续处理沿既有生命周期/保留/法律冻结/外部义务，完成态由墓碑派生。OAuth新code及待同意请求
与退租/关闭共用principal锁，并重验成员和credential version，防迟到请求恢复旧授权。
个人闭环服务/HTTP及失败回滚通过，真实PostgreSQL/MySQL组合与并发通过；最新后端真实浏览器
个人安全12/12、生命周期6/6、全业务矩阵792/792通过。最终独立门禁与交付状态见发布检查点和task-state。

## D36 — 查询门禁：禁手写 SQL 与禁拼表名，用只减不增的冻结基线

**Decision / Reason**
用户要求"严禁 raw SQL、全部走 gorm/gen 类型化写法"。实测：生产代码 18 个 `.Raw(`/`.Exec(` 调用点、23 条语句，
但**真正的战场是 72 条 `.Joins(` 生产语句与约 170 处绕过 `Bind` 的拼表名**。
新增 `tests/architecture/query_gate_test.go`（5 条 AST 规则）+ `testdata/query_gate_ledger.json` 冻结基线：
`handwritten-sql` 18、`concatenated-table-name` 146、`naked-table-literal` 715，预算 **879**（347 条目）。
门禁放在 `tests/architecture` 而非 `tools/*`，因为它随 `make test-integration` 在 `make ci` 内，每个提交必跑。

**Alternatives / Tradeoffs**
不用 `forbidigo`：它只能按标识符名/正则匹配，表达不了"ADD 出现在某方法实参里""字面量在某上下文里"，
而 `prefix` 这个名字在 URL、S3 key、env 变量三处合法存在，会大量误伤。
`naked-table-literal` 只匹配 `^(iam_|goose_)`，这三类前缀永远不会命中，实测零误报。
`gorm-expr-dynamic` 初版曾把 `gorm.Expr(clock+" + ?")`（列名 + 运算符，正是该 API 的用途）误判为违规，
已收窄为"动态**且**文本含 SQL 关键字或表名"，真实仓库归零。
预算只能降不能升：`QUERY_GATE_UPDATE=1` 在结果超过既有预算时直接失败；**确需升预算**（例如规则补漏后
合法浮出更多既有违规）必须设 `QUERY_GATE_REBASELINE=<原因>`，原因会写进账本与 diff，是一次留痕的、
可评审的动作，而不是靠删账本绕过。三条执行路径都做了变异验证
（漏一条→报 new violation；多一条→报 not found；预算调低→报 above its budget）。

**变异测试在冻结前抓到并修掉了两个真实漏洞**（这正是"规则要能被证伪"的价值）：
① 裸表名规则原本只看实参/RHS/复合字面量/常量声明，**不看 `return`**——
   `func helper(...) string { return prefix + "iam_roles" }` 只要不叫 `table` 就能绕过。
   补上 `return` 上下文后预算从 836 升到 879（多浮出 55 处既有违规，其中大数是此前完全没被统计的）。
② 补 `return` 时一开始把 `audit/anchor.go:151` 的 S3 对象键与 `authn/returnurl.go:129` 的 URL 前缀
   误报为拼表名（它们同样由名为 `prefix` 的变量拼成）。已收紧为"必须同时出现表名字面量或
   `.TableName()`"，两处误报消除；夹具里也各留了一个正例与一个反例把这条边界钉住。

**Scope / Status**
P0 已完成并实证（含夹具证明 5 条规则均非空断言、白名单可断言）。后续按阶段推进，**基线集合只减不增**；
清理时必须同步下调 `budget`。**不得为了让门禁变绿而抬高预算或删除条目。**

## D37 — 跨模块取表名走窄接口注入，不放宽架构测试、不放宽依赖全序

**Decision / Reason**
128 处 `table("字面量")` 中只有 34 处是本模块自有表，94 处是别人的表。
用户初选"允许跨模块 import query"，但实测 `pkg/module/registry.go:126` 的 `Activate` 要求
"模块必须注册在其全部依赖之后"——**依赖图必须是严格全序，环即启动失败**；
而 organization/identity/authn/oauth/provisioning 全部已依赖 core，
**core 反向 import 它们的 query 在启动期就是硬失败**，与风格无关。
最终确认走窄接口/注入（照抄既有 `DirectoryAuthorizationSourceConnector`、`RoleGrantStore` 两个先例），
未环方向的跨模块引用也不放开，保持 `cross-module-storage-import` 门禁与 `Activate` 一字不改。

**Alternatives / Tradeoffs**
改 `Activate` 允许成环会破坏关停严格逆序的根基，影响全仓 19 个模块的生命周期与迁移顺序，是降低架构强度而非提升，
且选窄接口后它无事可做。代价是需新建 6–9 个契约与连接器，工作量更大。

**Scope / Status**
P2/P4b/P5 落地；**具体形状由 D40 定稿**（跨界结构体声明在被对方 import 的那一侧）。
授权引擎的语句保持单条（`administrator.go` 的 last-admin 守卫由 `Protect` 在调用方事务里
做前后两次测量，拆分会让两次测量落在不同快照上）；`relationshipTraversalExpressions` 的三方言 switch
原样搬迁，性能基线 28 个指纹必须零变化以证明"只搬家不改语义"。

## D38 — authn 的 EnsureBootstrapPrincipal 与两条越界写删除，平台管理员初始化只保留 create-only 的那份

**Decision / Reason**
`authn/web.go` 的 `EnsureBootstrapPrincipal`（及 `BootstrapPrincipal`）与它专用的
`Repository.createPrincipal` / `updateBootstrapPrincipal`，连同无调用方的 `getPrincipal`，一并删除。
生产路径已由 `internal/app/platformadmin.go` 承担：`identity.BootstrapPrincipalStore` 建 principal、
`authn.BootstrapCredentialStore` 建 credential、`core.PlatformAdministratorStore` 写管理员行，
三个能力在同一事务里由组合根编排。authn 那份是重复实现，并且是 authn 写 identity 拥有的
`iam_principals` 的三条越界写中的全部来源。

**证据（实测，不是判断）**
① `grep -rn "EnsureBootstrapPrincipal" --include=*.go` 的全部命中在 `_test.go` 与定义处；
② **panic 探针**：在函数首行插 `panic(...)` 后，`go test` 全仓（排除 `internal/iam/authn` 自身）
   与 `tests/` 六个集成套件（architecture / goose / testdb / lifecycle / migration / performance）
   **全部通过、无 panic**；对 `getPrincipal` 同法同结果；
③ `internal/app/platformadmin.go:36-44` 自己写明 bootstrap 是 create-only，
   "password rotation belongs to a separate audited reset flow"。

**语义差异（必须记下来，否则以后会被当成回归）**
被删的那份会**重置密码并吊销会话**（reconcile 语义）；生产路径是 create-only，
重复执行返回 `PlatformAdminAlreadyBootstrapped`。密码轮换由 `internal/app/platformadminreset.go`
经 `authn.ResetBootstrapCredentialOn` 承担——**删它不丢任何能力**，只是去掉了第二条语义不同的路径。
`resetBootstrapCredential` 本身保留（重置流程在用）。

**Alternatives / Tradeoffs**
保留并把三条越界写改成调用 identity 的能力：代价是仓里长期留着两份做同一件事、语义还不同的实现。
本次不做、只记为技术债：同样留着那份重复实现。均已否决。

**覆盖率实测与预估相反**
事前预估"删掉一段覆盖率高于包均值的代码会把 authn 压低"，实测 **authn 90.6% → 90.7%**（反而升）——
被删的那段含若干未覆盖的错误分支。预估错了，以实测为准。

**Scope / Status**
S1 已完成：净 **-196 行**（8 文件，+74/-270）。`go build`、全量单测、`make lint`、
`tests/architecture`（含查询门禁账本）全绿；总覆盖率 90.1%。
测试侧改动：`bootstrapLocalPrincipal` 与 4 个 harness 改用新的包内 `seedLocalPrincipal`
（直接写 principal + credential 两行，并按 `normalizeLogin` 归一化登录名——否则 fixture 用自己的
登录名都查不到自己）；读 principal 的 6 处测试改用 harness 的 `principal()` 直读，
不再让生产代码为测试保留一个读方法。

## D39 — authn 与 identity 的双向越界改为互相委派；不可委派的只剩一个 JOIN 形状

**Decision / Reason**
authn 与 identity 互相往对方拥有的表里读写（18 条语句，双向）。修法不是「发布表名」，而是**委派**：
拥有者在调用方的事务里执行自己的语句。样板是仓内已有的 `internal/app/principallifecycle.go`
与 `identity.PrincipalStateStore`。

- **authn → identity 13 条**：3 条死码删除（D38）；7 条委派给 `identity.PrincipalStateStore`
  新增的 6 个 reader；3 条**不可委派**（每条都同时读 authn 自己的表），保留在 authn，表名改走
  identity 发布的集合。
- **identity → authn 5 条**：全部委派给新增的 `identity.PrincipalCredentialStore`（authn 提供）。

结果：脚本普查「模块用自己的前缀拼别人家表名」**65 处 → 0 处**。

**接线形状：两处方向反转，这是被迫的**
`authn` 激活在 `identity` **之前**，于是两种常规形状都走不通：
authn 去 `Resolve` identity 的类型 → 架构门禁 `module-dependency` 要求声明 identity →
`Activate` 的注册序校验失败；identity 去 `Resolve` authn 的类型 → 必须 import authn →
与已有的 authn→identity 成 **Go 包环**。
合法形状只有：**类型声明在 identity，消费方提供钩子，identity 解析自己包的类型后推过去**
（`inspectCall` 对「实参所属包 == 自己」不记依赖）。据此新增
`identity.PrincipalTableSetReceiver`（推表名）与 `identity.PrincipalStateReceiver`（推能力）。
命名刻意用 `Receiver` 而非 `Connector`，以区别于方向相反的 `core.PrincipalTableSetConnector`。

> **D40 起类型已改名，形状不变**：`identity.PrincipalTableSetReceiver` →
> `identity.PrincipalTableNamesReceiver`（载荷改为 identity 自己的 `PrincipalTableNames`）；
> `core.PrincipalTableSetConnector` → `core.PrincipalTableNamesConnector`。
> 本条的**结论与理由全部仍然成立**，只是不具名集合 `gormx.TableSet` 已整体删除。

**`LockActiveRow` 不是 `LockActive` 加个返回值——这是安全差异**
`lockPrincipal` 的 7 个调用点里 6 个只取 error，依赖「principal 不存在或已停用 → 报错 → 事务回滚」。
`LockActive` 是一条 UPDATE，**不报告是否匹配到行**。照「复用 LockActive」的最初设想改，
会让**停用账号走通找回密码 / MFA 注册 / 邮箱验证**。故新增会报告 `found` 的 `LockActiveRow`，
并有专门断言钉住（`TestSecurityStateWritesRefuseADisabledPrincipal` 5 个子用例）。

**SQL 逐字节未变，是实测而非声称**
先在 authn 侧冻结 9 条语句的完整文本（DryRun + 真实 PostgreSQL 方言，含 gorm 单独发出的
NOT EXISTS 子查询），再在 identity 侧**独立写死同一批字面量**断言新 reader 产出相同文本，
搬完再跑一次。三次都相等。这层断言的必要性在于：性能基线的查询计划指纹需要外部数据库，
**`make ci` 里没有**，没有它等于在 CI 看不见的地方改冻结路径。

**关户路径的 sessions 被写两次，刻意保留**
identity 请 authn 用 **identity 的数据库时钟**吊销；authn 自己的凭据吊销又用**服务时钟**跑一次。
两条都带 `revoked_at IS NULL`，identity 在前，**落库的时间戳是 identity 的**，第二条是真 no-op。
删掉任一条都会静默改变审计时间戳来源，而任何「终态」断言都看不见。
故保留，并在 `internal/app` 的组合测试里钉住「恰好 2 条 sessions UPDATE」；
该断言做过变异验证（改成 3 即失败，实测值 2）。要改时间戳来源需单独 ADR。

**Alternatives / Tradeoffs**
- 放宽 `isStableWiringCapability` 白名单或调换注册序：前者是用户明令禁止改 `tests/architecture`，
  后者影响全仓 19 个模块的生命周期与关停逆序，风险远大于收益。均否决。
- 加宽 `identity.ServiceAccountValidator` 以接走 authn 的四表 JOIN：它的 SQL 是冻结的
  （性能基线 `scim_bulk` 路径，`count(*)`），改返回字段即破指纹。否决，该 JOIN 留在 authn。
- 把两条 `iam_principals ⋈ iam_credentials` 的 JOIN 拆成两次调用：会改 `session_lookup` 指纹，
  且多一次往返。否决，保留 JOIN、表名走发布集。

**测试侧的一处不对称（如实记录）**
identity 的测试**不能**使用 authn 的真实能力——authn import identity，所以 identity 的测试也不能
import authn，这正是当初需要这个能力的同一个方向约束。identity 侧用替身（写同样的行），
真实闭包的行为在 `internal/iam/authn/credentialstore_test.go` 断言。替身文件顶部写明了这一点。

**Scope / Status**
阶段一（S0–S8）完成，7 个提交 `9e89c5e..61f2b35`。门禁账本 **385 → 372**，四次均手动下调，
全程未用 `QUERY_GATE_REBASELINE`。

## D40 — 跨模块表名一律走类型化结构体；声明在被对方 import 的那一侧；不具名集合整体删除

**Decision / Reason**
阶段二把「拥有者发布一袋按字符串查的表名」（`gormx.TableSet` + `XTables{Published}`）
全部换成类型化结构体，全仓一种形态。用户要求：强类型约束写法、全仓统一、0 遗憾。
消费方拿到的一律是结构体字段，**没有任何一处再用字符串查表**；问一张拥有者没有的表
从「运行期 error」变成「编译不过」。

**唯一的一条规则（由 Go 的 import 方向决定，不是偏好）**
> 跨界的类型化结构体，声明在**被对方 import 的那一侧**。

- **消费方 import 拥有者**（拥有者激活在前）→ 拥有者导出 `PublishedXTableNames(prefix)` 与
  `XTableNames`，`module.Provide[XTableNames]`，消费方 `Resolve` 后直接持有。
  适用 65 处：authn/identity/organization/oauth/provisioning/governance/federation ← core 等。
- **拥有者 import 消费方**（拥有者激活在后）→ **消费方声明它需要的字段**，拥有者在连接器里填满。
  适用 6 对：core←organization(12 字段)、core←identity(1)、authn←organization(1)、
  authn←oauth(2)、identity←organization(1)、identity←oauth(1)。
- **两条都走不通的一对**（authn↔identity，见 D39）→ identity 推自己的 `PrincipalTableNames`
  给 authn 提供的 `PrincipalTableNamesReceiver`。

连接器随载荷改名：`XTableSetConnector` → `XTableNamesConnector`。
`TableSet` 这个词在仓里已不存在，名字里留着它就是假话。

**「消费方加字段 → 拥有者编译不过」并不成立，别这么写文档**
计划里曾这么描述 B 类的强度。实际上 Go 的**带键结构体字面量**在结构体新增字段时照样编译，
新字段静默为零值。真正的强制手段是运行期 + 启动期：`core.joinedTables.bindDirectoryTables`
逐字段检查 `IsZero()` 并**报出字段名**，authn/identity 的 bind 同理；每个模块的 `Initialize`
再查一次 `foreign.bound()`，任一槽位未到即**启动失败**。这一条已对 7 个模块做过变异验证
（删掉检查 → 对应用例立刻失败）。

**「是否已绑定」从每处一条收敛到每模块一条**
原先每个调用点都有 `x, err := r.foreign.accessTable("iam_x"); if err != nil {...}` 三行。
现在是一行字段读取，检查搬到 `Initialize`（Registry 相位保证那时 Provide/Bind 全部完成）。
**代价，如实记录**：在模块外手工构造 repository 而忘记 bind 的调用方（只有测试夹具），
过去得到一个 error，现在得到空表名。模块自身的路径由启动检查覆盖。

**顺带删除的东西（全部是本工作流自己引入、现已无调用者）**
`gormx.TableSet`/`NewTableSet`/`Lookup`/`Names`/`Len`；13 个模块的 `TableSet(prefix)`；
4 个 `XTables{Published}` 能力；`query.BindDirectoryTables`/`BindPrincipalTable`；
authn/identity `Repository` 上的 `tenantTable()`/`memberTable()`/`principalTable()`/
`serviceAccountTable()`/`oauthTable()` 中间取名方法；8 个无人消费的 `tables.go` + 其 `tables_test.go`；
`internal/app/bootstrap_iam_tables_test.go`（它枚举的 13 个 `TableSet` 已不存在）。

**`query.TableNames.Map()` 也删了——先前记为「有意保留」，后来推翻**
最初的保留理由是「13 条前缀断言靠它枚举」。这个理由在看到 `backend-module` SKILL.md 的模板后不成立：
文档会教**每个新模块**都写一个无人调用的 `Map()`。13 条断言改成反射枚举 `BindNames(prefix)` 的字段后
**比原来更强**——`Map()` 同样是手写的，它漏掉的字段原测试根本看不见，而反射走的是结构体每一个字段。
已变异验证（去掉某个名字的前缀 → 用例立刻红）。federation 那条 `BindNames vs Bind` 漂移断言
也一并改成反射，语义不变。

**Alternatives / Tradeoffs**
- **让 organization 直接填 `core/query.DirectoryTables`**（省掉 core 根包再声明一份 12 字段）：
  被架构门禁 `cross-module-storage-import` 禁止（跨模块 import 他人 `query`/`model`/`dbgen`）。
  用 root 包里 `type X = query.Y` 的**类型别名**能绕过（源码里不出现该 import），
  但那是绕规则不是守规则。故 core 根包声明一份，`joined_tables.go` 里逐字段转换——
  多 12 行，换来的是转换本身受编译器检查。
- **不删 8 个无人消费的 `TableSet`**：它们是 P1 为这套机制铺的，机制没了就是死代码。
  代价是丢掉 `TestEveryModulePublishesPrefixedTableNames` 这条「13 个模块都得发布」的跨模块门禁；
  其「前缀正确」的保证在每个模块自己的 `query/table_test.go` 里仍在
  （`TestBindAppliesPrefixToEveryXTable`），丢掉的只是「新模块也必须发布一份」这层提醒。

**Scope / Status**
阶段二。SQL 文本逐字节未变——施工上靠「保留局部变量、只删错误分支、语句行不进 diff」，
验证上靠既有的 9+8 条冻结断言与 MySQL 查询计划指纹。

---

## D41 — Logical Table ≠ Physical Table（表名唯一权威 = Resolver）

**Date** 2026-09-13

**Decision**
数据库访问层严格区分三件事并保持解耦：**Logical Table Name**（业务 / Repository / Query Builder
可见的唯一表名）、**Physical Table Name**（运行时由部署 namespace 解析出的真实表名，如
`sl_iam_roles` / `uat_iam_roles`）、**Table Resolver**（唯一把 Logical 解析为 Physical 的
基础设施边界）。业务代码不得自行推导、拼接、缓存或传播物理表名。

**判定指标不是「0 个 `runtime.TablePrefix()`」，而是「0 个未经授权的 physical-table-name
决策」**：resolver 内部的 TablePrefix() 合法；硬编码 `"uat_iam_users"` 即使不用 TablePrefix
也违规。

**Invariants**
1. **逻辑名是业务可见的唯一表标识**：业务层 / Repository / Service / Query 只能见逻辑名、
   发布的 typed 引用或 resolver 生成的名称；不知道部署 namespace。
2. **`runtime.TablePrefix()` 是基础设施状态**：只允许出现在 resolver（`query/table.go` 的
   BindNames/Bind）、module 表名发布/绑定、DB 基础设施注入、明确登记的 B3 例外；禁止出现在
   业务查询、Repository 实现、Service/Domain 代码。
3. **前缀拼接只能发生在 resolver 边界内**：`prefix + table`、`runtime.TablePrefix() + table`、
   `fmt.Sprintf("... FROM %s ...", prefix+table)` 及其等价物在数据库访问代码中禁止。
4. **Raw SQL 不能绕过**：Raw / CTE / JOIN / recursive 中使用的表名必须来自 resolver /
   published names，不得自行拼 runtime prefix。
5. **跨模块访问保持所有权**：消费方只经 owner 发布的 typed 名/引用读表，不复制 owner 的
   物理命名逻辑。
6. **方言与 namespace 独立**：MySQL/PG/SQLite 的 dialect 差异不得改变 Logical→Physical 的
   规则；dialect 只负责 SQL 行为，resolver 只负责物理表名。

**Allowed**：`BindNames(...)`、`Bind(db, prefix)`、`PublishedXXXTableNames(...)`、typed 表引用、
resolver 生成名、明确的基础设施注入、登记的 B3 例外。
**Forbidden**：Repository/业务代码里出现 `runtime.TablePrefix()`；`prefix()` 访问器在
Repository 实现中传播；`prefix + table`；`fmt.Sprintf(... FROM %s ...)` 拼物理表名；模块自行
构造他模块物理表名；Raw SQL 独立重建物理表名。

**Non-goals**：不要求消灭 Raw SQL / 动态 SQL / gen 表达式；不强制三库同一 namespace 实现；
不把模块 schema 所有权与部署 namespace 混同。

**Enforcement 顺序（先清理、后机械化——避免「测试把现状定义成架构」）**：D41 入册 → SKILL
执行规则 → 明确 allowlist/边界 → 清理现存违规（9 `prefix()` + 4 `table()` + `NewRevisionStore(prefix)`）
→ 确认剩余合法点 → 账本 19→15→14 → **最后**才把规则机械化进 architecture tests / CI。
**清理完成前不改 `tests/architecture`。**

**Reason**
HEAD 0e33cd7 实测 40 处 `runtime.TablePrefix()`：约 24 处是合法 resolver/发布/绑定
（`PublishedXXXTableNames(prefix)`、`query.Bind`、infra 注入、dlock/wuid `WithTablePrefix`）；
剩余违规面集中为 **9 个 `prefix()` 访问器 + 4 个 `table(name) { return r.prefix() + name }`
帮手 + `core.NewRevisionStore(prefix)`（4 模块把部署前缀当业务参数传入 core 构造函数）**——
它们是同一缺口的不同表现：物理表名正从 Resolver 向上游泄漏。goose 版本表
（`principallifecycle.go:47`）是 B3 特殊项（框架拥有表、goosex 无访问器)；`audit/anchor.go`、
`authn/returnurl.go` 的 `prefix + "..."` 是 S3 对象键/URL 拼接，非 DB 表名，不误伤。

注：`NewRevisionStore(prefix)` 的严重度是 resolver 边界泄漏（前缀作为业务参数运输），不是
跨 namespace 混淆——全部模块共享同一个部署 `table_prefix`，不存在「上层模块知道 core 私有的
另一套命名」；修法仍是归位：core 的 revision 存储自己经 resolver/bound 名取表，调用方不再传前缀。

**Alternatives Considered**
1. `schema.NamingStrategy{TablePrefix}`（连接级）——gorm 只对 model 派生路径生效，
   `Table()`/Raw/JOIN 不经过 naming（已核 v1.31.2 源码），会造出两套前缀系统；且前缀是运行期
   部署配置，不该埋进数据源层。
2. 按库 namespace（PG schema / MySQL database 分部署）——SQLite 无 schema namespace，三库统一失败。
3. 新建中央 `Table(module, logicalName)` resolver——等价物已存在（每模块 `query/table.go` 的
   BindNames + 发布名），再建一层是过度设计；缺的是 enforcement 不是 abstraction。

**Tradeoffs**
每模块 resolver 各自持有（而非中央函数）符合「表名有主人」；代价是「不该碰表名的地方」要靠
规则钉死。D41 对 sqlc 的隐含裁决：多方言 + 运行期前缀 + 跨模块 schema + 动态查询四个补丁会
吃光 sqlc 的简单性，故 sqlc 不作目标（Bun 同理：迁移成本换不回编译期列校验）。

**Scope / Status**
全仓。Accepted / Architecture Invariant。enforcement 未机械化；现存合法点保持明确 allowlist。

---

## D42 — 列表端点默认有界,客户端需要全集时自己翻页

**Date** 2026-09-16

**Decision**
所有随租户数据增长的列表端点,分页默认**有界**:`minimum:"1" maximum:"200" default:"50"`
(沿用 `core/http.go` 里 `listTenantMembersInput` 的既有先例)。仓库侧新增 `XxxPage` 变体,
**与既有非分页方法并存**,内部调用方不受影响;handler 由 `respx.OK` 改为 `respx.List`。
前端由 `iam-api.ts` 的 `requestAllPages` 统一翻页,**需要完整集合的消费点行为完全不变**。

**Reason**
此前分页是 opt-in:`core/pagination.go` 的 `normalizePage` 把 `limit=0` 当"不限",而多数 input 的
`Limit` 是 `default:"0"`,于是**不传 limit 的调用方拿到全表**。契约审计实测:53 个返回数组的 GET
无分页参数,其中 31 个随租户数据增长,`/iam/tenants` 更是平台级。已追到实现层:
`core.ListMembers` 以 `.Find()` 结束,无 `.Limit()`,而全仓另有 317 处 `.Limit()` ——
**是既有模式没被用上,不是缺少模式。**

**Alternatives / Tradeoffs**
- *只加 opt-in 参数*:零行为变化,但不解决无界,否决。
- *服务端硬上限(恒不超过 200)*:前端零改动,但超限静默截断;虽可由 `meta.page.total` 检测,
  却把"发现被截断"的责任推给每个客户端。作为备选保留,未采用。
- 采用方案的代价是前端必须配合。**代价不在"加翻页控件",而在判断每个消费点要"一页"还是"全集"** ——
  角色编辑器用成员列表给复选框打勾,拿到部分集合会把**已分配成员渲染成未分配**,
  这是错误的界面而不是短的界面。这个判断无法机械化,是本次唯一不可并行的部分。

**Scope / Status**
样板 `aabee75`(`/iam/roles/{role_id}/members`)确证:`respx.OK → respx.List` **不改变响应 schema**
(两者返回同一个 `Envelope[T]`,`List` 只填 `meta.page`),故契约变更是纯增量的两个 query 参数,
前端类型无需重新生成。其余模块按同一模式铺开。

## D43 - File operation preparation shares the execution walker

**Date** 2026-09-18

**Decision / Reason**
Use the existing policy walker with a bounded, read-only prospective tree overlay
for operation preparation. Persist cumulative node/closure costs and expected
result counts in the files-owned migration8 plan_json column. Recheck the actual
cost at commit and roll back mismatches. Transient negative identities remain
strictly inside the overlay; they are never database keys, IAM inputs or API IDs.
New-node IAM checks remain mandatory on actual persisted identities at execution.

**Alternatives / Tradeoffs**
A second conflict algorithm would diverge on recursive batch collisions. Running
real tree writes and rolling them back during preparation would violate the
read-only preparation boundary. The lazy overlay instead bounds distinct cached
nodes by twice the configured operation node budget plus the maximum ancestor
path. It may reject excessive inspection rather than load an unlimited space.
This is NOT yet per-conflict identity/decision persistence or a timing guarantee.

**Scope / Status**
Implemented and fresh SQLite unit/race/fault tested. Migration roundtrip verified
in SQLite, embedded PostgreSQL16 and in-process MySQL, with/without prefixes.
Real MySQL8 operation runtime and full product acceptance remain pending.

## D44 - Local storage platform durability is explicit

**Date** 2026-09-18

**Decision / Reason**
Retain file data sync, root confinement and immutable hard-link publication.
Unix directory sync errors remain fatal. On Windows close the directory handle
without attempting unsupported directory FlushFileBuffers; do not advertise
Unix-equivalent metadata power-loss durability. Windows native functionality is
verified, but this platform durability criterion is NOT production accepted.
Use an actual junction to test Windows root escape without symlink privilege.

**Alternatives / Tradeoffs**
Ignoring arbitrary Access denied errors would hide real file/storage failures.
Windows has no equivalent supported per-directory fsync here; replacing
publication with absolute-path operations would weaken root confinement.
Preserve the current storage boundary and document the precise limitation.

**Scope / Status**
Local driver compatibility only, implemented and tested. Production durability
still requires target-platform crash/resource acceptance under the approved plan.

## D45 - File browser presentation does not invent a live API

**Date** 2026-09-18

**Decision / Reason**
Implement the user-requested Web page in the existing authenticated shell with
shared UI/theme/i18n. Presentation snapshots contain only authorized fields; they
are not a hand-written public API contract. The /files page remains explicitly
unavailable until files-owned IAM/audit HTTP and generated OpenAPI are ready.
Effective menus own discoverability; no unconditional tenant/platform link or
guessed permission code. Fixtures live outside the production route/module graph.

**Alternatives Considered / Tradeoffs**
Fake tenant files/localStorage mutations would obscure missing persistence and
authorization. Calling provisional endpoints would freeze an unapproved contract.
The component can be browser-tested now, but real file operations and UI/backend
acceptance remain pending. Bound pages to100, label search/selection page scope,
and clear stale selections/metadata when location/page/access state changes.

**Scope / Status**
Web browsing presentation checkpoint; no claims of working upload/download,
mutation, lazy whole-space tree, conflict decisions or real file-service E2E.

## D46 - Copy and move are real-time, conflicts are transient

**Date** 2026-09-18

**Decision / Reason**
User explicitly corrects the product contract: ordinary copy/move are synchronous
requests, not persisted awaiting-decision workflows. Detect and return conflicts
without conflict/decision tables or pending user tasks. Confirmation resubmits
explicit strategies and observed versions; reauthorize and revalidate before one
atomic publication. No open transaction waits for a user, no new conflict cleanup,
and no implicit background queue. Transfer/upload lifecycle remains separate.

**Alternatives Considered / Tradeoffs**
The prior persisted conflict frontier and submission records add inappropriate
lifecycle/schema/cleanup complexity. Removed the newly authored model/migration9/
query drafts before implementation or original DB migration. Real-time semantics
do not remove transaction rollback, source/target revision, IAM, bounded traversal,
audit or idempotency requirements. Existing operation engine/schema were earlier
work and need scoped runner review; prior migrations/data are not auto-deleted.

**Scope / Status**
Confirmed explicit user instruction, superseding asynchronous conflict/task
sections and historical next actions. Design updated; draft removal regenerated.
Files race regression suite, full backend build and diff check PASS after removal.
Synchronous runner/HTTP/real Web mutation and production acceptance remain pending.

## D49 - Workspace affiliations are directory anchors, ACL edits share file fences

**Date** 2026-09-18

**Decision / Reason**
Implement approved hierarchy within existing account/tenant namespaces through
directory workspace kind/entity/department/principal markers. Children resolve
their nearest workspace via verified ancestor path, not copied IDs or a second
organization mirror. Workspace roots are independent ACL inheritance boundaries;
private binding initializes unbound directories only, never silently reassigns.
Durable NodeACL stores one subject/action/effect/applicability row; node and space
revisions fence the complete direct rule set and synchronous mutations together.

**Alternatives Considered / Tradeoffs**
No workspace table or per-entity physical driver is needed. Per-child ownership
copies introduce subtree rewrites and drift on moves. Private persistence loaders
must not be treated as IAM authorization: actual organization/subject validation,
delegation and management-read still need owner capabilities and services.
Bounded256-rule node budgets prevent partial/truncated authorization; exceeding
the limit fails closed. Old nodes stay unbound, without inferred tenant-wide access.

**Scope / Status**
Migration9/query/private storage and isolated tests implemented. Six engine/prefix
upgrade/down/up combinations PASS (MySQL dialect engine, not real MySQL8). Files
race PASS at86.3% below90% gate; lint/vet/build/architecture PASS. No original DB,
existing service, conflict/task tables, commit/push or public-route changes.
Next: transaction-bound live IAM subjects/affiliations and service+actual audit,
then generated HTTP and real Web ACL/sharing. Full product not accepted.

## D48 - Hierarchical file ACL and sharing require design confirmation

**Date** 2026-09-18

**Decision / Reason**
User explicitly requires design before implementation. Scope now includes platform,
tenant, entity and department management visibility; fine-grained directory/file
access for different accounts; temporary/permanent password/token sharing.
Existing design section21 records the proposal. After delivery user says continue,
authorizing execution of the three recommended choices: management-read bypass of
ordinary child ACL without write/share authority, distinct account-private versus
tenant-personal ownership, anonymous read/download-only sharing.
Previous no-public-sharing exclusion is superseded by the sharing requirement;
the delivered anonymous read/download recommendation is now the implementation target.

**Alternatives Considered / Tradeoffs**
Do not infer full write/share authority from upper-level visibility or org position.
Propose separate management-read capability and ordinary IAM+ACL evaluation,
reusing existing role/group identities and policy fences rather than parallel IAM.
Propose distinct account-private and tenant-personal workspaces to avoid ambiguous
multi-tenant ownership. Durable ACL/share configuration is required across requests
and does not authorize conflict tables, pending copy/move tasks or plan lifecycle.

**Scope / Status**
Confirmed design-first, requested features and subsequent continue authorization.
No original DB migrations or existing service changes are authorized by continuation.
Next: isolated transaction IAM/actual audit tests,
workspace ownership+ACL, generated HTTP/real Web and sharing, with existing gates.

## D47 - Synchronous engine records only final success

**Date** 2026-09-18

**Decision / Reason**
Implement D46 through one caller-owned transaction and the shared bounded policy
walker. Conflicts and decisions live only in request memory; the first unresolved
frontier is returned without persistent writes. Confirmation checks the observed
space revision and exact snapshot keys. A merge can reveal a deeper frontier; no
intermediate tree is committed. Only final success uses the existing Operation
journal for actor/space/key-scoped replay. No OperationItem pins, leases, pending
states, new schema or conflict cleanup are created by this entrypoint.

**Alternatives Considered / Tradeoffs**
Reimplementing a second conflict walker would diverge on prospective batch
collisions. Simulation and actual execution instead share policies and verify
cost/revision parity. Copied identities use created-from aliases and immutable
effective content origins; generated or negative temporary IDs never enter
conflict responses/keys. Changing an earlier overwrite invalidates later choices
whose observed content/revision changed. Whole moves reauthorize old trash too.

**Scope / Status**
Internal engine implemented; 11 focused top-level SQLite tests, files race,
vet, scoped pinned lint, full build and architecture tests PASS. Files coverage
85.3% does not meet90% gate. Real transaction-bound IAM/audit, HTTP/OpenAPI,
live Web operations, other database runtime and production acceptance pending.

## D50 - File ACL subjects come from the caller transaction

**Date** 2026-09-18

**Decision / Reason**
Resolve a file request's principal, current active department, active static and
dynamic groups, and effective IAM roles through the same transaction that reads
and mutates the file tree. Lock the tenant policy revision before those reads and
reject a changed/stale snapshot. The resulting identities are ACL match inputs,
not action grants; an action-specific IAM constraint remains mandatory.

**Alternatives Considered / Tradeoffs**
Request claims do not carry current groups, roles, or department state and would
remain valid after revocation. Resolving each subject outside the file transaction
would permit IAM/file time-of-check races. Files must not query IAM-owned tables
or implement role expansion itself. The narrow core function adds one capability
but reuses the existing authorizer, foreign-table wiring, revision fence and role
resolution rather than creating a second policy engine.

**Scope / Status**
Implemented and focused core tests pass, including rollback-visible revocation,
expired group membership, inactive inputs and module wiring. Files integration,
workspace-reference validation, management-read, HTTP and Web remain pending.

## D51 - IAM action ceilings and file ACL are separate gates

**Date** 2026-09-18

**Decision / Reason**
Every file action first needs its transaction-bound `file_<action>` IAM data
constraint and then a matching effective node ACL. Copy uses source `read` plus
destination `create`; move retains its independent action. The separate
`file_read_all` permission may bypass ordinary ACL only for list, traverse, read
and download inside its verified management scope. Workspace entity, department
and personal-owner references are revalidated before either route can succeed.

**Alternatives Considered / Tradeoffs**
Treating an IAM entity grant as a node grant would expose every file in scope and
erase per-directory differences. Letting ACL grant an action absent from IAM would
let a file owner manufacture organization authority. Using one broad file_view
check for every mutation would make create/delete/share indistinguishable. The
two gates cost additional bounded reads but preserve explicit revocation and the
approved distinction between visibility scope and access mode.

**Scope / Status**
Implemented for the synchronous operation service and tested under race. The
permissions are registered in the shared catalog; no public file HTTP endpoint
is exposed yet. Platform/account entry, ACL editing/delegation, sharing and Web
live data remain pending.

## D52 - Public file routes expose files and directories, never nodes

**Date** 2026-09-18

**Decision / Reason**
`Node` remains the private persistence abstraction shared by files and directories.
The public HTTP contract uses `/files/{file_id}` and
`/directories/{directory_id}` so the resource type and accepted identifier are
unambiguous. Original bytes are the explicit `raw` representation at
`GET /files/{file_id}/raw`, following the useful convention in the archived
Spring Boot file API. Roots are independent resources at `/file-roots` and the
current principal's idempotent singleton is `/personal-file-root`.

**Alternatives Considered / Tradeoffs**
Keeping a public node collection leaks the storage model and lets one route claim
two resource types. Using `GET /files/{file_id}` directly for bytes leaves no
unambiguous metadata representation, so `/raw` is retained. Separate file and
directory mutation routes add small handler duplication but allow atomic kind
validation and precise OpenAPI parameters.

**Scope / Status**
Implemented in backend HTTP, generated OpenAPI/client, Web calls and contract
tests. Internal database columns, ACL evaluation and tree algorithms keep node
terminology. Global removal of `/iam` and `/authn` remains a separate proposed
routing migration.

## D53 - Files and directories share one public file resource

**Date** 2026-09-18

**Decision / Reason**
The user rejected D52's separate `/directories`, `/file-roots`, and
`/personal-file-root` resources because a directory is also a file entry. The
public contract is now one `/files` collection: `kind` distinguishes file from
directory and `scope` identifies a root's ownership boundary. Metadata, update,
delete, child listing and raw content are representations or operations on that
same file identity. A GitHub-style root-relative lookup is exposed as
`/files/{root_file_id}/-/{path}`; the explicit root is required because one caller
can see several organizational and personal roots.

**Alternatives Considered / Tradeoffs**
Separate directory URLs duplicate one underlying identity and force the Web
client to dispatch by kind. Bare `/files/-/{path}` cannot identify which visible
root anchors the path. The router uses a catch-all internally for multi-segment
matching while OpenAPI documents a normal `path` parameter; neither router syntax
nor the persistence `Node` name is public.

**Scope / Status**
D53 supersedes D52's route split. Implemented in backend HTTP and authorization,
OpenAPI, generated TypeScript, Web calls, route audits and focused tests. Internal
tree persistence remains unchanged. Global `/iam` and `/authn` migration is still
design-only.

## D57 - Account and organization files share URIs but never authorization context

**Date** 2026-09-19

**Decision / Reason**
Use the same `/files` resources for account-private and organization files. A
request with both `X-Tenant-Id` and `X-Entity-Id` enters the organization path;
a request with neither enters the authenticated Principal's account space. One
header without the other is invalid. The server derives ownership from verified
context, never from a body/query owner selector. GET remains read-only; the account
root is provisioned idempotently by `POST /files` with `scope=account`.

**Alternatives Considered / Tradeoffs**
A second account API would duplicate file semantics and drift. Reusing the current
selected tenant for account files would let organization state influence private
ownership. Account authorization therefore uses a strict owner ACL path without
tenant IAM, while global file IDs are still checked against the current space.
Account audit events use the existing tenant-zero chain and retain Principal ID;
this avoids inventing a second audit subsystem but leaves account events in one
platform chain rather than per-tenant chains.

**Scope / Status**
Implemented in middleware, service, HTTP/OpenAPI, generated Web client and
`/account/files`. Focused lifecycle, cross-space, incomplete-header, race and
desktop/mobile browser route tests pass. ACL management, sharing, organization
root recovery, root-level concurrency fences and real MySQL/PostgreSQL acceptance
remain out of scope for this decision and prevent production-readiness claims.
# Recycle Bin Restore Location Disclosure And Rename - 2026-09-19

- Decision: return the original restore location as optional structured path
  segments only when the caller can traverse the full active path from its file
  root to the original parent. Omit it on any missing, inactive, malformed, or
  unauthorized segment.
- Reason: trash read access must not reveal denied organization directory names;
  structured segments also preserve names containing separators and let Web
  localize known root labels.
- Decision: distinguish destination-name collision from stale revision/tree
  conflicts. A collision is retryable by supplying a new name on the existing
  file update resource; no restore-command route or persistent conflict record.
- Scope: recycle-bin list and restore only. Existing delete semantics, ACL rules,
  historical trash placement retention, and copy/move behavior are unchanged.
- Status: implemented and verified.

## D58 - File preview and editing remain separate versioned capabilities

**Date** 2026-09-19

**Decision / Reason**
Propose one MIME-first file presentation classifier for icons and preview/editor
eligibility. Isolate pre-1.0 `open-file-viewer` behind a dynamically loaded local
adapter and pass it only an already-authorized Blob. Preview uses the existing
download permission because receiving preview bytes is not meaningfully weaker
than downloading them. Text/code editing is a separate lazy Monaco mode. Each
save stages a new immutable Object, appends the existing Version model, and CAS
advances the file head; no history or editor-session table is introduced.

**Alternatives Considered / Tradeoffs**
Passing the raw URL cannot include required tenant/entity headers and invites PDF
iframe fallback into an authenticated URL. Loading every viewer plugin exposes
immature parsers: 0.1.47 has an open DOCX injection report and archive expansion
has no resource budgets. Phase 1 therefore enables only reviewed common formats.
CodeMirror remains appropriate for compact form fields; Monaco is proposed only
for the distinct IDE-like full-file editor and is loaded on demand. Blob preview
is simple and authorized but requires a size limit until range delivery exists.

**Scope / Status**
Design proposed in `apps/frontend-web-admin/docs/file-preview-and-editing-design.md`.
Awaiting user confirmation. No application dependency, runtime route, schema, or
source behavior has changed.

## D59 - Image rows use bounded authorized thumbnails

**Date** 2026-09-19

**Decision / Reason**
Browser-renderable images in active file listings load as thumbnails through the
existing authorization-aware raw client. Loading is near-viewport, cancellable,
limited to 8 MiB before buffering, and falls back to the category icon on any
load, limit, or decode failure. Account and organization headers remain derived
from request context.

**Alternatives Considered / Tradeoffs**
Direct raw URLs cannot carry mandatory tenant/entity headers. Eagerly buffering
every image in a 100-item page is wasteful. A generated thumbnail API would give
stronger decoded-pixel and bandwidth bounds, but adds backend conversion, cache,
storage, invalidation, and format policy outside this requested slice.

**Scope / Status**
Approved by the user and in implementation. Server-generated thumbnails remain a
future scale hardening item.

## D60 - Office editor candidates require format and licensing gates

**Date** 2026-09-19

**Decision / Reason**
Do not directly embed PPTist: it is an AGPL-3.0 complete Vue application rather
than a packaged renderer, and advertises roughly 85%+ PPTX import fidelity.
Univer's OSS core is Apache-2.0 and embeddable, but its current capability matrix
places Office import/export and mature slide support in commercial Pro. Either
candidate therefore requires an explicit licensing decision and real document
round-trip acceptance before it can save versions in this product.

**Alternatives Considered / Tradeoffs**
PPTist may still be viable with commercial permission or a separately operated
AGPL-compliant service. Univer Pro is architecturally closer to this React app
and spans sheets/docs/slides, but introduces commercial licensing and a large
office runtime. Neither changes the safe fallback in Phase 1.

**Scope / Status**
Univer remains excluded until Pro licensing is approved. FortuneSheet is approved
for implementation only after bounded ZIP preflight and termination controls.
This decision was superseded by D61 after the user required one solution to cover
all common Office families.

## D61 - External Office viewers replace partial open-source Office editors

**Date** 2026-09-19

**Decision / Reason**
Do not integrate PPTist, Univer OSS, or FortuneSheet. None reliably covers all of
PPT/PPTX, DOC/DOCX, and XLS/XLSX. Use Microsoft Office Viewer as the default
Office handler and Google Viewer as the alternate `Open with` choice.

**Alternatives Considered / Tradeoffs**
PPTist is presentation-only and AGPL-3.0; Univer's needed Office capabilities are
commercial; FortuneSheet is spreadsheet-only. External viewers provide broader
compatibility but disclose file bytes to a third party and require public HTTPS.
They may receive only an explicit short-lived, read-only, version-bound,
revocable and audited URL from the shared file gateway. Tenant policy may disable
the transfer; authenticated raw URLs and localhost URLs are never forwarded.

**Scope / Status**
Approved. Design updated; public token gateway and handler UI remain to be
implemented after the active route-contract migration.

## D62 - Public routes and Swagger groups follow resource prefixes

**Date** 2026-09-19

**Decision / Reason**
Remove `/iam`, `/authn`, and other implementation-domain prefixes from the
pre-production public API without compatibility aliases. Public paths use
resource nouns. Every application OpenAPI operation has exactly one lowercase
kebab-case tag equal to its first path segment, so the same resource prefix is
always grouped together.

**Alternatives Considered / Tradeoffs**
An IAM prefix can be a legitimate gateway namespace for an independently hosted
service, but this application exposes one API and IAM is not a resource. Deeply
nesting globally identified Tenant/Entity/Department resources would duplicate
ownership and enlarge authorization paths, so they remain top-level resources;
mandatory headers keep the request authorization boundary explicit.

**Scope / Status**
Implemented. Includes route declarations, action subresources, avatar resources,
menus, Web clients, test manifests, generated OpenAPI and a repository-wide
composed-contract gate. Core migration 16 upgrades existing built-in menu routes
from the retired `/iam/...` locations without rewriting custom labels or
translations.

## D63 - All durable uploaded content uses governed physical and logical file layers

**Date** 2026-09-19

**Decision / Reason**
All durable runtime uploads, including file-manager content, avatars, commerce
images and business attachments, use the shared physical Object and logical
Node/Version layers, validation, authorization, audit, retention and reference
safety. Source assets, deployment configuration, keys, backups and logs remain
in their proper infrastructure. Physical objects carry no business ownership;
logical roots carry the lowest verified ownership scope.

Scopes are system, tenant, entity, department, tenant-personal and account. The
server derives the narrowest scope from the resource; callers cannot choose a
broader owner for convenience. User-managed files may exist without a domain
binding. Resource-managed files require an atomic registered-purpose binding and
are hidden from ordinary directory listings by default while remaining visible
in their authorized domain UI and lifecycle reconciliation.

Platform-wide logical/physical file viewing is deferred. The current phase adds no
global file API, Web page, or placeholder platform permission. A platform
administrator's ordinary account `/files` request never changes into a global
query. If global viewing is requested later, metadata inventory and actual content
reading require a new design and separate permissions and auditing.

**Alternatives Considered / Tradeoffs**
Putting every business asset into visible directories makes the file manager noisy
and permits moves/deletes to break business resources. Separate avatar, commerce
and attachment stores duplicate security and cleanup. Object ownership is also
rejected because one immutable object can back multiple logical versions/scopes.
A registered binding ledger adds schema and reconciliation cost, but is necessary
to prove resource-managed lifecycle and prevent long-lived unowned files.

**Scope / Status**
Confirmed product direction; detailed proposal is section 23 of
`apps/backend-api/docs/file-management-design.md`. The binding and system-scope
foundation is implemented locally. Existing and new avatar uploads now use
logical resource-managed files; startup migration adopts legacy bytes without a
copy and is idempotent. Global file management is explicitly outside the active
implementation scope.

## D64 - Avatars are account-owned resource files

**Date** 2026-09-19

**Decision / Reason**
Store current and candidate avatars as account-root File Nodes and Versions with
registered `principal.avatar` or `principal.avatar-candidate` bindings. Reads
authorize the active binding and verified account owner rather than trusting a
storage key. Candidate promotion rebinds the same logical file; account erasure
revokes every active binding before the existing object collector reclaims bytes.

**Alternatives Considered / Tradeoffs**
Keeping direct avatar storage writes would leave durable bytes outside the shared
file lifecycle. Copying legacy bytes would duplicate storage and complicate
rollback. Legacy objects are therefore checksummed and adopted in place. New
accounts have no stored default avatar until one is explicitly set; presentation
uses the existing empty-avatar fallback instead of creating ungoverned bytes.

**Scope / Status**
Implemented and verified across the Files and Identity transaction tests, race
tests, three-dialect migration matrix and full backend regression. Commerce media,
general attachments and a platform-wide inventory remain future consumers of the
same registered-purpose contract, not part of this change.

## D74 - Current identity is discovery, not a parallel resource tree

**Date** 2026-09-23

**Decision / Reason**
`GET /me` is the only current-principal alias. It lets a newly authenticated
client discover its principal ID when login has not supplied one. Once known,
self-service and authorized administrators use the same
`/principals/{principal_id}` profile and child-resource contracts. Tenant,
entity, and department contexts never own `me`; target identity is selected by
the principal ID and access is decided by authorization, field filtering,
fresh-authentication requirements, and audit.

Credential-bound authentication transactions that do not yet have a principal
ID remain top-level transaction resources. This exception does not authorize a
second `/me/*` account tree or `/user/current/*` aliases.

**Alternatives Considered / Tradeoffs**
A parallel `/me/*` tree is convenient for self-service clients but duplicates
handlers, schemas, tests, and security behavior and prevents one uniform admin
contract. Putting `me` below tenant/entity paths falsely models ownership and
uses URL shape as a security mechanism. Requiring a principal ID adds one
post-login discovery read for clients that do not already receive the ID, but
keeps every later resource canonical.

**Scope / Status**
Confirmed design rule. This supersedes D65's `/me/*` hierarchy and D66's nested
`/.../me/...` projections. The complete OpenAPI migration remains proposed and
no runtime route is changed by this decision record.

## D75 - Public routes require a resource semantic contract first

**Date** 2026-09-23

**Decision / Reason**
No application URI is approved from an existing handler, table name, foreign
key, screen, or current path alone. The resource must first define its meaning,
direct owner, identity, lifecycle, canonical representation, and authorization
boundary. Route conclusions are labeled `MODEL_CONFIRMED`,
`CONTRACT_CONFIRMED`, or `PRODUCT_DECISION_REQUIRED`; an unresolved field blocks
the canonical route recommendation.

Path parameters identify the resource named by their collection. Database
composite keys may support a relationship identity but do not automatically
define the public API. Atomic workflows may affect several resources but must
name and return those resources honestly. Optional context does not establish
ownership.

A globally identified resource may deliberately expose both its short item URI
and a direct-owner scoped item URI. Both addresses are one resource contract:
identical schema, version, authorization, mutation, audit effect, and service
implementation. The scoped form additionally verifies the parent relationship.
This is not permission for compatibility aliases, transitive ancestor chains,
or owner-specific behavior behind the same noun.

**Alternatives Considered / Tradeoffs**
Designing from existing URLs is faster but preserves semantic collisions such
as tenant principal routes that list through membership and mutate global
identity. Designing directly from tables leaks storage choices. The semantic
gate adds review work, but makes incorrect assumptions visible before they
become public compatibility obligations.

**Scope / Status**
Confirmed review rule. This supersedes D66's unverified department-owner
example because the current model requires `tenant_id` and makes `entity_id`
optional, and supersedes D62's first-segment Swagger grouping rule. Documented in
`apps/backend-api/docs/http-resource-routing-design.md`. Runtime route changes
remain pending explicit approval of the revised OpenAPI proposal.

## D76 - Files and menus are resources with mount points, not a hierarchy to merge

**Date** 2026-09-24

**Decision / Reason**
The organization has levels -- tenant, entity, department -- and a person
beside them. Files and menus are not properties of those levels; they are
resources that can be mounted at any of them. A mount point is addressed by its
own collection, and a view reads exactly one: the tenant view lists the
tenant's files, the entity view the entity's, the department view the
department's, and a person's own view lists files that belong to no tenant at
all.

Seniority does not mean one collection answers with the levels beneath it. It
means an administrator may open the entity and department views as well, which
authorization already decides. Merging the levels into one answer puts a
navigation decision inside a resource and makes the collection mean something
its URI does not say.

A person is not a level of the organization. The tenant-independent personal
drive (`account` scope) therefore belongs to no tenant view.

**Alternatives Considered / Tradeoffs**
Containment -- a tenant collection answering with its entities' and
departments' roots -- was built and reverted. It is convenient for one screen
that wants every reachable root in a single request, and it reads plausibly
because a department's files really are stored inside the tenant's space. What
it costs is that every collection then means "everything under here that you
may see", which is a query result rather than a resource, and the caller can no
longer ask for one mount point without a filter. The earlier `context` scope
from `d35da67` was that same merge, and it also swept members' per-tenant
personal directories into an answer any tenant member could request.

The cost of mount points is that a screen showing several levels issues one
request per level. That is a client concern and is visible, rather than a
server-side merge whose membership rule nobody can see from the URI.

**Scope / Status**
Confirmed design rule for files, and for menus when their contract is revised.
`/entities/{entity_id}/files` listing only that entity's roots (`346a809`)
follows from it. The remaining work is on the client: each view reads its own
mount point, which needs a department view the router does not have yet.

## D77 - One canonical URI, and a natural key unless it is ambiguous

**Date** 2026-09-24

**Decision / Reason**
A resource has one address. Aliases cost a second operation id, a second set
of tests and a second authorization assertion, and they make every reader ask
which one to use. The only aliases are discovery entries for a client that
cannot know an id yet: `GET /me` and `GET /sessions/current`. That is the
rule, rather than a list of exceptions -- a browser holding only a session
cookie genuinely cannot name its session.

An item is addressed by its natural key under its collection. The test is
whether a client can build the address from what it already holds and whether
the result names exactly one resource. Segment count is not the test:
`(resource, field)` is two segments and is still a natural key. An id is
minted only when no natural key passes that test.

Tenant membership keeps `(tenant_id, principal_id)`, so its item is
`/tenants/{tenant_id}/memberships/{principal_id}`. Authorization relationship
gets an id, because `subject_relation` and `entity_id` may be empty and an
empty value is a legitimate part of the identity, so no address a client
constructs is unambiguous -- which is why today's eight-selector DELETE
silently reports no change when one is omitted.

**Alternatives Considered / Tradeoffs**
Minting an id everywhere is uniform and removes the judgement, at the cost of
a primary-key rebuild on a populated table and an indirection over an
identity that already exists; the manifest already rejected that for field
policy. Keeping tuples everywhere avoids migrations but leaves relationships
addressed by query selectors, which is a filtered delete wearing an item's
clothes. Dual addressing was offered as navigation convenience; it is
declined because the generated document has only the short form today, so
this is a decision not to build rather than a removal.

**Scope / Status**
Confirmed design rule. It supersedes D75's paragraph permitting a resource to
expose both a short and an owner-scoped item URI, and it replaces D74's
wording that `GET /me` is the only current-principal alias. Recorded in
`apps/backend-api/docs/proposed-api-manifest.md`. The relationship id needs a
migration plan for two tables before it is implemented; no runtime route
changes with this record.

## D78 - The web client must take its URLs from the contract before routes move

**Date** 2026-09-24

**Decision / Reason**
`apps/frontend-web-admin` builds request paths as strings and checks them
against nothing. The generated types are imported for `components` only; the
`paths` type, which is where a moved route shows up, is imported by no file.
Regenerating the types after a route migration that had already shipped moved
14,826 lines and broke no build, which is the evidence: typecheck catches a
renamed field and says nothing about a renamed route.

The proposed contract moves roughly 114 operations. Slicing that work by
domain and calling each slice verified because `bun run typecheck` passes
would be claiming a check that does not exist. The client must therefore take
its URLs from `paths` — request helpers that accept a path template and its
parameters, the shape `openapi-fetch` uses — before any route moves.

**Alternatives Considered / Tradeoffs**
A static inventory of the client's path strings was measured and rejected:
paths are composed at runtime (`/tenants/${id}` plus a suffix the caller
passes), so no regex recovers a whole path, and a checker that silently
skipped what it could not parse would be worse than none.

A runtime assertion in the shared request layer would catch only the calls a
test happens to exercise. Partial coverage presented as a gate is the failure
this record exists to avoid.

Converting is the real cost: 442 path strings across a 2,749-line client, and
every call site changes shape because a template plus parameters replaces an
interpolated string. It is mechanical and large, and it is cheaper before the
routes move than after, because afterwards every site is wrong in two ways at
once.

**Scope / Status**
Confirmed prerequisite for the route migration, not yet implemented.
`bun run check:api-types` (`755f533`) keeps the generated types honest about
the document meanwhile; it does not make the client safe.

**Implementation - 2026-09-25**
Implemented with `apiRoute`/`pagedRoute` importing generated `paths`. Every IAM
transport accepts only a branded address carrying its supported method;
template, required path/query parameters, parameter types and query enums are
checked at construction. Mounts are explicit, including files and audit scopes;
offset pagination admits only GET operations declaring offset/limit. Existing
fetch, error/envelope handling, uploads and download verification are reused.
Audit filters now use generated query objects. URLSearchParams standardizes
query serialization (spaces become +); identifiers still use encodeURIComponent,
and root-relative file paths preserve their slash separators.

Compiler diagnostics exposed existing archive aliases and POST anonymization;
the client now uses published attempts/verifications/content and PUT. The file
collection contract omitted inherited query fields because its embedded Go input
was unexported. Exporting FilePageInput repairs Huma discovery; regenerated
documents include after/limit/state for all collections. This is a necessary
contract repair, not a new route migration. Twelve negative compile assertions
replace the old permissive literal scan. All runtime tests remain covered.

Status: D78 implemented and verified. Public API migration has not started.

## D79 - Execute evidence-backed API optimization, not the whole URI inventory

**Date** 2026-09-25

**Decision / Reason**
User authorized an optimized implementation plan, second review, implementation
and real E2E. The full manifest failed its implementation review. Execute the
bounded specification in `apps/backend-api/docs/api-optimization-implementation-2026-09-25.md`:
identity reads use unchanged GET /me; real BrowserSession discovery uses
Cookie-only GET /sessions/current; global /sessions/{session_id} reads/revokes
only the authenticated owner's active item. Current logout and existing step-up
move under /sessions/current without aliases. Reuse ListSessions/RevokeSession,
logout transactions, profile projection, CSRF and existing security services.
Correct the already-invalid personal multipart scope and remove its unused
client entry. Preserve account drive and historical data.

**Alternatives Considered / Tradeoffs**
Whole principal unification, two-request onboarding, permission catalog removal,
generic authentication transactions, response/If-Match rewriting, destination
file-operation migration and relationship ID backfill are rejected for this
optimization: no required capability or demonstrated defect justifies their
behavior/data costs. Existing self-only /me/sessions collection is retained;
adding a principal selector provides no needed capability. These are decisions
against implementation here, not an automatic next-task list. D74/D76/D77 design
principles remain recorded; D77's relationship-ambiguity rationale is corrected
by the review, while its ID design direction is preserved.

**Scope / Status**
Implemented and accepted. Real SQLite/Chromium E2E 14/14, browser fixture
regression 74/74, archive desktop/mobile PASS, backend authn/files suites PASS,
frontend gates and generated drift checks PASS. 206 paths / 296 operations;
the added operation is session item GET. No database migration. Evidence and
limits are in the execution plan. Historical documents explicitly defer to it.

## D80 - Swagger groups belong to the final path prefix

**Date** 2026-09-26

**Decision / Reason**
The user explicitly requires first-path-segment groups, regardless of the
suffix or workflow. Implement through Huma OnAddOperation in the shared builder
after mount expansion. Tenant files belong to tenants, principal files to
principals, and globally addressed file items to files. Exactly one tag.

**Alternatives Considered / Tradeoffs**
Workflow grouping and suffix grouping violate the latest instruction. A central
framework hook covers typed routes, expanded mounts and adapter registrations.
Small prefix groups and a large tenants group are accepted consequences of the
specified ownership rule; no artificial grouping prefixes are introduced.

**Scope / Status**
Supersedes D75's exception to D62's prefix-grouping rule. Prefix grouping is
implemented; full export and live browser assertions pass. Complete explicit
principal migration was subsequently authorized and implemented under D81.

## D81 - Complete explicit principal addressing

**Date** 2026-09-26

**Decision / Reason**
User authorized "继续，搞到位": migrate all 41 global `/me/*` operations and four
nested tenant/entity projections to `/principals/{principal_id}/...`. Keep only
`GET /me` identity discovery and the existing `/sessions/current` resource.
Explicit self registration validates the verified actor before any handler:
invalid canonical ID 422, foreign ID 404. No cross-account privilege is added.

**Alternatives Considered / Tradeoffs**
Keeping aliases or migrating files alone leaves the requested unified resource
tree incomplete. Applying a self-only guard to every principal URL would break
the existing authorized cross-account avatar reader. Therefore scope is declared
explicitly, and avatar bytes move to `/avatar/content` to avoid colliding with
settings GET. Historical stored references are normalized when returned.
Restricted MFA enrollment keeps its original verifier and error behavior; the
verified password login result provides `subject` for its explicit address.

**Scope / Status**
Implemented. 296 operations retained; exactly 46 paths change (45 principal
migrations plus avatar bytes). Methods, request bodies and credential schemes
preserved. No database/file migration. Web page `/me/*` navigation is unchanged.
Evidence: docs/api-routing-inspection-2026-09-26.md; real SQLite E2E15/15,
browser regression74/74, live demo5/5 and generated inventory/drift checks PASS.
This supersedes D79's choice to retain `/me/*`; no optional API redesign follows
automatically from this decision.

## D82 - Qualified Public Resource Names And Unified Workflow Roots

**Decision / Reason**
User required a complete inspection after identifying ambiguous groups/positions
and split email/invitation Swagger groups. Names follow actual domain meaning:
principal-groups, job-positions, membership-invitations, role-access-requests,
role-access-reviews, authorization-relationships and authorization-scope-model.
Invitation acceptance is /membership-invitations/acceptances; email issuance is
/email-verifications/requests and consumption remains /email-verifications.
Tenant collections and principal projections use the same canonical names.

**Scope / Tradeoffs**
The change intentionally replaces public paths; retired URLs answer404. It does
not change database/domain names, SCIM protocol Groups, permission discriminators,
operation IDs, payloads, security or UI navigation. Polymorphic company/merchant/
store entities keep their established domain name. Final URI first-prefix tags
remain mandatory, so tenant-contained collections belong to tenants.

**Evidence / Status**
Implemented; docs/api-resource-naming-audit-2026-09-26.md records the exhaustive
resource dispositions. 296 operations preserved, exactly37 addresses changed;
all component schemas and every operation field except tags compare equal.
Real SQLite/Chrome E2E18/18, fixture UI74/74, live selectable demo5/5 and actual
Swagger309 prefix sections PASS. Final independent integration suite and generated
contract drift gates PASS; self-review and requirement acceptance PASS.

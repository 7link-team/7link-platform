# Decisions

## D1 — 审计脱敏默认表只覆盖凭据与高危标识，不覆盖联系类 PII

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

- **10.3 principal 管理权限**：按 Keycloak 的 realm 模型分两个平面，不是二选一。
  六个全局账号 operation 移到真正的 platform 路由挂 `platform_administer`；租户管理员
  另有租户内的 principal 更新权限，管自己租户的人。同时让 `authz.Register` 拒绝把
  platform scope 的 action 注册到 tenant registrar —— 当前这个 bug 正是这样产生的。
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

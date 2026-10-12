# 04 · 机器与运行时（`fleet` 模块 · 机器部分）

> 依据：`PRD.md` §6.8、§10.1、§10.5、§12、§13；`00-conventions.md`（约束性契约）；底座 `internal/iam/oauth`（设备码授权 RFC 8628：`device.go`、`http_device.go`、前端 `apps/iam/src/app/device/page.tsx`）、`internal/iam/identity`（服务账号与凭证：`service_account.go`）、`pkg/extension/authn`、`pkg/extension/authz`、`internal/infra/dlock`；旧项目 Preview 01（`internal/platform/{server,daemon,computer,models,inspection,gateway,gateway_daemon}.go`、`src/bridge.ts`、`docs/{preview,runtime-modes,api-gateway,api-gateway-p0,gateway-desktop}.md`、旧 PRD §5.3.1 / 附录 D.2–D.3 / F.1 / F.7）。
> 同级对齐：`03-execution-evidence.md`（执行级消息语义、`dispatch_epoch` 栅栏、执行租约 30 s、续约 ≤10 s、机器 20 s 自停、派发内 `seq` 缺口 409 `expected_seq`、outbox ≤10,000、`execution_commands`、`execution_workdir_locks`）；`02`/`03`/`07` 采用的 `ETag`/`If-Match` 乐观锁与时间列类型 `TIMESTAMPTZ(3)`/`DATETIME(3)`/`DATETIME`；`07` 的 `delivery_project_repositories`。
> 标注：「**本规格提议**」= 来源中没有、由本规格补齐的设计，附理由；「沿用 Preview」= 旧实现已验证的数值或语义。

---

## 1. 范围与目标

**解决**：让一台装有 Claude Code / Codex 的电脑以**机器服务账号**身份安全接入团队，并成为可调度、可观察、可吊销的执行面。

| 本规格负责（`fleet` 模块，机器部分） | 不负责（归属） |
|---|---|
| 机器端程序（下称「机器程序」，CLI 名 `machine-runner`，见 BR-04-01）的分发、安装命令、本地 profile 与凭据存储 | Agent 成员、生命周期、派单 → **05** |
| 基于底座 OAuth 设备码的接入：发码、附加机器信息、控制台确认/拒绝、过期、机器端撤回、签发机器服务账号凭证 | 执行生命周期、执行租约、`dispatch_epoch`、执行级命令/事件载荷、工作目录写锁 → **03** |
| 机器凭证轮换（24 h 宽限）与吊销（立即断开） | 项目仓库定义（`delivery_project_repositories`）→ **07** |
| 机器通道传输：会话、连接代次 `conn_epoch`、长轮询/短轮询、消息信封、认证头、心跳；WebSocket 通道（原 v1.1，D119 起 MVP） | 后台任务队列、outbox、SSE、通知渠道 → **09** |
| `MachineState` 状态机、心跳阈值、离线判定、排空（DRAINING） | 权限目录与矩阵、step-up 策略、审计视图 → **08** / 底座 |
| 运行时检测（版本/路径）、能力检测（Skills / MCP，不调用模型）、模型目录（Claude `supportedModels`、Codex 末次成功缓存、手填模型 ID）与模型单价 | 执行页的换机器交互、carryover 文本拼接 → **03**（本规格只给判定输入） |
| 执行槽（每机槽数、预留/释放、排队公平性）、放置规则、工作目录登记（机器 × 项目仓库 → 本地路径）、离线迁移判定输入 | 甲方门户、交付 → 01 / 07 |
| 桌面本地服务（回环静态页 + 代理）与 Desktop 壳（可选客户端） | |
| API 网关（实验）：配置、密钥显示/轮换、`/v1/models` 与 `/v1/responses` 兼容子集、请求记录 | |
| **远程访问隧道**（D119）：在机器出站 WebSocket 通道上多路复用预览 / 文件 / 终端流；机器端进程树端口归属校验；工作区路径限定的原始读写流；带宽与并发配额；预览网关（独立预览域名、通配证书、路径模式回退）的部署配置与反向代理 | 预览会话、产物查看器、文件浏览与编辑、终端会话、甲方分享链接的生命周期与权限判定 → **03** |
| **机器类型**（D119）：WORKSTATION（设备码接入）/ SERVER（无头守护进程，预签发服务账号凭证）/ K8S_POOL（每个执行一个临时 Pod，池伸缩）；每机远程访问开关 允许远程预览 / 允许远程终端 / 允许人工接管后编辑文件 | 权限码、step-up 用途、审计目录与收件箱 → **08** |
| 页面：机器列表、接入向导、机器详情、API 网关 | 成员与 Agent 页、执行页 → 05 / 03 |

**目标数值**（MVP）：单租户 ≥5 台在线机器、每台 ≤8 执行槽（沿用旧 PRD 容量验收目标；默认 1 槽）；接入从机器端运行命令到控制台「已上线」≤20 s（不含人工输入）；离线判定 ≤35 s；吊销后机器下一次请求即被拒（≤1 s）。

---

## 2. 用户故事

| ID | 故事 | 分期 |
|---|---|---|
| US-04-01 | 作为团队所有者，我要复制一条针对我操作系统的安装与登录命令，以便在新电脑上一次装好机器程序 | MVP |
| US-04-02 | 作为团队所有者，我要在控制台输入（或扫码打开）机器显示的 8 位接入码，看到主机名、系统和检测到的运行时后改名并确认，以便只有我认可的机器能接入 | MVP |
| US-04-03 | 作为团队所有者，我要在接入码 300 秒倒计时内完成确认，过期、拒绝或机器端取消时得到明确提示，以便不会误接入 | MVP |
| US-04-04 | 作为交付负责人，我要在机器列表按状态、运行时、空闲槽筛选机器，以便选择执行位置 | MVP |
| US-04-05 | 作为交付负责人，我要在机器详情看到运行时版本、模型目录、Skills/MCP、托管的 Agent 和执行记录，以便判断这台机器能做什么 | MVP |
| US-04-06 | 作为交付负责人，我要排空一台机器（不再接新执行、现有执行跑完），以便安全维护 | MVP |
| US-04-07 | 作为团队所有者，我要吊销一台机器（红色确认 + 重新认证），以便凭证泄露或设备丢失时立即切断，正在运行的执行进入「已中断」 | MVP |
| US-04-08 | 作为团队所有者，我要轮换机器凭证并给 24 小时宽限，以便定期更换而不中断工作 | MVP |
| US-04-09 | 作为工程师，我要手动刷新某运行时的模型目录或手填目录中没有的模型 ID，以便使用第三方代理的模型 | MVP |
| US-04-10 | 作为工程师，我要为项目仓库登记它在每台机器上的本地路径，以便执行在正确目录运行，且同一目录同时只有一个写执行 | MVP |
| US-04-11 | 作为执行模块（03），我要在机器离线时知道哪些机器可承接同一运行时以及能否原生续接，以便迁移执行 | MVP |
| US-04-12 | 作为团队所有者，当机器离线超过 1 分钟时我要收到通知，以便及时处理 | MVP |
| US-04-13 | 作为团队所有者，我要把一台机器上已登录的 Codex 暴露为 OpenAI 兼容 Responses API（实验），查看/轮换密钥并看最近请求 | MVP（实验） |
| US-04-14 | 作为工程师，我要在本机通过回环页面查看机器程序状态并用桌面托盘管理它 | v1.1 |
| US-04-15 | 作为运维，我要机器通道在支持的网络上升级为 WebSocket，降低轮询开销 | v1.1 → MVP（D119） |
| US-04-16 | 作为工程师，我要在手机或浏览器里打开 Agent 正在开发的网页 / API，而机器不开放任何入站端口，以便随时看进展（D119） | MVP |
| US-04-17 | 作为运维，我要把一台无界面的服务器以服务账号接入为执行机器，不需要有人坐在它前面输入接入码（D119） | MVP |
| US-04-18 | 作为运维，我要接入一个 Kubernetes 执行池，每个执行在一个用完即删的 Pod 里运行，并按排队量自动伸缩（D119） | v1.1 |
| US-04-19 | 作为团队所有者，我要按机器决定是否允许远程预览、远程终端、人工接管后编辑文件，个人电脑默认不开放终端（D119） | MVP（预览开关）/ v1.1（终端与编辑开关） |
| US-04-20 | 作为团队所有者，我要限制每台机器经隧道的带宽与并发，以免预览拖垮执行（D119） | MVP |
| US-04-21 | 作为部署管理员，我要用配置给出与控制台不同的预览域名与通配证书；没有通配 DNS 时退回路径模式（D119） | MVP |

> US-04-15 分期变更（D119）：WebSocket 通道作为远程预览 / 文件 / 终端流的承载，随 D119 的 MVP 能力提前到 MVP（BR-04-90）；长轮询仍是控制消息的回退。

---

## 3. 领域模型与数据表

### 3.1 关系

```
Tenant ─┬─ fleet_machine_enrollments（每个设备码一行；接入前的临时记录，不出现在机器列表）
        └─ fleet_machines（1 ↔ 1 底座服务账号 principal）
              ├─ fleet_machine_credentials（↔ 底座 service account credential id）
              ├─ fleet_runner_sessions（每个 conn_epoch 一行）
              ├─ fleet_machine_runtimes（每 RuntimeKind 一行）
              │     └─ fleet_model_catalogs（每 runtime 一行；含手填模型）
              ├─ fleet_machine_tools（每工具一行：无头浏览器、ffmpeg、ffprobe、whisperx、pyscenedetect、node、python）
              ├─ fleet_capability_inspections（每 runtime × 工作目录一行，缓存最新）
              ├─ fleet_workdirs（机器 × 项目仓库 → 本地路径）
              └─ fleet_slot_reservations（执行槽占用）
Tenant ─┬─ fleet_model_prices（运行时 × 模型单价，供 03 计费）
        └─ fleet_gateway_configs（1 行）── fleet_gateway_requests（最近 100 条元数据）
（幂等键统一存平台表 `platform_idempotency_keys`，09 C7；本模块不建幂等表）
外部：底座 iam_oauth_device_authorizations（设备码，归 oauth）；03 execution_commands / execution_workdir_locks（归 03）
```

### 3.2 类型映射（三方言，全表适用）

| 逻辑类型 | PostgreSQL | MySQL 8.0.16+ | SQLite |
|---|---|---|---|
| ID（雪花） | `BIGINT` | `BIGINT` | `INTEGER` |
| TS（UTC） | `TIMESTAMPTZ(3)` | `DATETIME(3)`（**禁用 `TIMESTAMP`**，2038 上限） | `DATETIME` |
| STR(n) | `VARCHAR(n)` | `VARCHAR(n)` `utf8mb4` | `TEXT` + `CHECK(length(x)<=n)` |
| HASH | `CHAR(64)`（SHA-256 hex） | `CHAR(64)` `ascii_bin` | `TEXT` + `CHECK(length(x)=64)` |
| ENUM | `VARCHAR(32)` + `CHECK(IN …)` | 同左（8.0.16+ 生效） | `TEXT` + `CHECK(IN …)` |
| BOOL | `BOOLEAN` | `TINYINT(1)` | `INTEGER CHECK(x IN (0,1))` |
| INT / BIGINT | `INTEGER` / `BIGINT` | `INT` / `BIGINT` | `INTEGER` |
| JSON | `JSONB` | `JSON` | `TEXT` + `CHECK(json_valid(x))` |
| TEXT | `TEXT` | `MEDIUMTEXT` | `TEXT` |

**部分唯一约束的统一写法**（MySQL 不支持部分索引）：用可空「锁键」列，满足条件时写值、否则写 NULL，再对该列建普通 UNIQUE（三方言 NULL 互不冲突）。下文以「锁键」标注。所有对象名带 `{{.Prefix}}`；迁移目录 `sql/{postgres,mysql,sqlite}/`，版本表 `goose_fleet`。每表均含 `tenant_id ID NOT NULL`、`created_at TS NOT NULL`、`updated_at TS NOT NULL`、`created_by ID NULL`、`updated_by ID NULL`（下表不再重复，除非有特殊约束）。

### 3.3 `fleet_machines`

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| id | ID | N | | PK |
| principal_id | ID | N | | 机器服务账号 principal；UNIQUE |
| enrollment_id | ID | N | | 创建它的接入记录 |
| name | STR(100) | N | | 1–100 字符，去首尾空白，无控制字符（沿用 Preview ≤100） |
| name_key | STR(100) | Y | | 锁键：state≠REVOKED 时 = `lower(name)`；UNIQUE(`tenant_id`,`name_key`) |
| state | ENUM `MachineState` | N | `OFFLINE` | ONLINE / OFFLINE / DRAINING / REVOKED（见 CR-04-01） |
| drain_requested | BOOL | N | false | 排空意图；离线期间保留，重连即进入 DRAINING |
| drain_mode | ENUM | Y | | `STAY`（排空后保持连接）/ `DISCONNECT`（排空后通知机器退出） |
| drain_requested_at / drained_at | TS | Y | | `drained_at`：排空中且占用槽归零的时刻 |
| hostname | STR(255) | N | | 机器上报，仅展示 |
| host_fingerprint | HASH | N | | SHA-256(`host_id` ‖ `profile_name`)；`host_id` 为机器程序首次运行生成的 128 bit 随机值 |
| profile_name | STR(64) | N | `default` | `[a-z0-9-]{1,64}` |
| os | ENUM | N | | WINDOWS / MACOS / LINUX |
| os_version | STR(64) | N | | |
| arch | ENUM | N | | X64 / ARM64 |
| runner_version | STR(32) | N | | SemVer |
| protocol_version | INT | N | 1 | 通道协议版本 |
| slot_count | INT | N | 1 | CHECK 1–8 |
| labels | JSON | N | `[]` | ≤20 项，每项 `^[a-z0-9][a-z0-9-]{0,31}$` |
| env_passthrough_names | JSON | N | `[]` | 允许透传给运行时进程的环境变量**名**（03 `env_extra_names` 来源）；≤50 项，`^[A-Z_][A-Z0-9_]{0,63}$`；禁止匹配 `*TOKEN*`/`*SECRET*` 之外的规则由 BR-04-46 校验 |
| conn_epoch | BIGINT | N | 0 | 当前连接代次；每开一次会话 +1 |
| current_session_id | ID | Y | | |
| last_seen_at | TS | Y | | 最近一次通道请求到达（服务端时间） |
| last_address | STR(64) | Y | | 最近请求来源 IP（经可信代理头解析） |
| online_since / offline_since | TS | Y | | |
| offline_notified_at | TS | Y | | 本次离线已通知时刻（去重） |
| revoked_at / revoked_by | TS / ID | Y | | |
| revoke_reason | STR(2000) | Y | | |
| version | INT | N | 1 | 仅用户可编辑字段（name、slot_count、labels、env_passthrough_names、drain_*）与吊销变更时 +1；系统心跳与在线/离线迁移**不**递增（**本规格提议**：避免心跳使用户编辑频繁 412/409） |
| archived_at | TS | Y | | 预留（吊销不归档；列表默认隐藏 REVOKED） |

索引：UNIQUE(`principal_id`)；UNIQUE(`tenant_id`,`name_key`)；(`tenant_id`,`state`)；(`tenant_id`,`host_fingerprint`)；(`state`,`last_seen_at`)（看门狗）；(`state`,`offline_since`,`offline_notified_at`)（离线通知）。
方言差异：仅 §3.2 映射。

### 3.4 `fleet_machine_enrollments`

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| id | ID | N | | PK |
| oauth_client_id | ID | N | | 本租户「机器接入」OAuth 公共客户端 |
| device_code_hash | HASH | N | | = 底座 `iam_oauth_device_authorizations.device_code_hash`；UNIQUE；跨模块只存值不建外键 |
| status | ENUM `EnrollmentStatus` | N | `PENDING_CONFIRM` | PENDING_CONFIRM / CONFIRMED / DENIED / EXPIRED / CANCELLED（CR-04-01） |
| expires_at | TS | N | | = 设备码 `expires_at`（创建 + 300 s） |
| hostname, os, os_version, arch, runner_version, profile_name, host_fingerprint | 同 §3.3 | N | | 机器上报快照 |
| runtimes | JSON | N | `[]` | `[{kind, version, path}]`，见 §3.13 Schema S1 |
| suggested_name | STR(100) | N | | 默认 = hostname 截断 |
| source_address | STR(64) | Y | | 发起附加请求的 IP |
| decided_by | ID | Y | | 确认/拒绝者 principal |
| decided_at | TS | Y | | |
| machine_id | ID | Y | | CONFIRMED 后填 |
| replaces_machine_id | ID | Y | | 确认时选择「替换已有机器」 |
| credential_collected_at | TS | Y | | 机器领取凭证时刻 |
| collect_last_polled_at | TS | Y | | 领取轮询节流（与设备码 `interval` 一致） |

索引：UNIQUE(`device_code_hash`)；(`tenant_id`,`status`,`expires_at`)；(`status`,`expires_at`)（过期任务）。

### 3.5 `fleet_machine_credentials`

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| id | ID | N | | PK（同时作为 `rotation_id`） |
| machine_id | ID | N | | |
| identity_credential_id | ID | Y | | 底座服务账号凭证 id；签发前为空 |
| state | ENUM `MachineCredentialState` | N | | PENDING_DELIVERY / ACTIVE / RETIRING / REVOKED |
| current_key | ID | Y | | 锁键：state∈{PENDING_DELIVERY, ACTIVE} 时 = machine_id；UNIQUE（每机至多一把「当前」凭证） |
| origin | ENUM | N | | ENROLLMENT / ROTATION / REENROLLMENT |
| issued_at | TS | Y | | 明文首次下发时刻 |
| delivery_attempts | INT | N | 0 | ≤5 |
| delivered_at | TS | Y | | 机器回执时刻 |
| retire_at | TS | Y | | RETIRING：= 轮换请求时刻 + 24 h |
| revoked_at | TS | Y | | |
| revoke_reason | ENUM | Y | | ROTATED / MACHINE_REVOKED / DELIVERY_FAILED / REENROLLED |
| requested_by | ID | Y | | 轮换发起人 |

索引：UNIQUE(`current_key`)；(`machine_id`,`state`)；(`state`,`retire_at`)；(`state`,`issued_at`)。

### 3.6 `fleet_runner_sessions`

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| id | ID | N | | PK = `session_id` |
| machine_id | ID | N | | |
| conn_epoch | BIGINT | N | | UNIQUE(`machine_id`,`conn_epoch`) |
| incarnation_id | STR(64) | N | | 机器程序每次进程启动生成（128 bit hex） |
| transport | ENUM | N | `LONG_POLL` | LONG_POLL / SHORT_POLL / WEBSOCKET |
| runner_version / protocol_version | STR(32) / INT | N | | |
| opened_at | TS | N | | |
| last_seen_at | TS | N | | |
| closed_at | TS | Y | | |
| close_reason | ENUM | Y | | DISCONNECTED / LEASE_EXPIRED / SUPERSEDED / REVOKED / DRAIN_STOP / CREDENTIAL_INVALID |
| remote_address | STR(64) | Y | | |

索引：UNIQUE(`machine_id`,`conn_epoch`)；(`machine_id`,`closed_at`)。

### 3.7 `fleet_machine_runtimes`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | PK |
| machine_id | ID | N | | UNIQUE(`machine_id`,`kind`) |
| kind | ENUM `RuntimeKind` | N | | CLAUDE_CODE / CODEX |
| version | STR(64) | N | | CLI 报告的版本字符串 |
| path | STR(1024) | N | | 可执行文件绝对路径 |
| status | ENUM | N | `AVAILABLE` | AVAILABLE / MISSING |
| detected_at | TS | N | | |
| missing_since | TS | Y | | |
| detection_error | STR(500) | Y | | |

### 3.8 `fleet_model_catalogs`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | PK |
| machine_id | ID | N | | UNIQUE(`machine_id`,`runtime_kind`) |
| runtime_kind | ENUM `RuntimeKind` | N | | |
| entries | JSON | N | `[]` | 最近一次**成功**检测的模型列表（Schema S2），按版本号降序（沿用 Preview `sortedModelEntries`） |
| source | ENUM `CatalogSource` | N | `DETECTED` | DETECTED（本次检测成功）/ CACHED（本次失败，展示上次成功结果） |
| errors | JSON | N | `[]` | 最近一次检测错误，≤10 条 × ≤500 字符 |
| manual_model_ids | JSON | N | `[]` | 手填模型 ID，≤50 项，每项 ≤160 字符、无空白与换行 |
| manual_model_families | JSON | N | `{}` | 手填 / 覆盖的模型家族 `{model_id: ModelFamily}`（BR-04-49），键须 ∈ entries ∪ manual_model_ids |
| checked_at | TS | Y | | 最近一次检测（成功或失败） |
| last_success_at | TS | Y | | |
| refresh_requested_at | TS | Y | | 手动刷新挂起中（非空即排队） |
| version | INT | N | 1 | 仅手填列表变更时 +1 |

### 3.9 `fleet_capability_inspections`

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | PK = `inspection_id` |
| machine_id | ID | N | | |
| runtime_kind | ENUM | N | | |
| workdir_id | ID | Y | | 空 = 机器用户目录范围（`~`） |
| scope_key | STR(40) | N | | `home` 或 workdir_id 十进制；UNIQUE(`machine_id`,`runtime_kind`,`scope_key`)（缓存仅保留最新） |
| state | ENUM `InspectionState` | N | `QUEUED` | QUEUED / RUNNING / READY / ERROR |
| request_id | STR(32) | N | | 每次请求新生成；回传须匹配 |
| conn_epoch | BIGINT | Y | | 下发时的连接代次 |
| requested_at / started_at / finished_at | TS | Y | | |
| requested_by | ID | Y | | 空 = 系统 |
| skills / mcp | JSON | Y | | Schema S3；READY 时非空 |
| error | STR(2000) | Y | | |
| duration_ms | BIGINT | Y | | |

### 3.10 `fleet_workdirs`（工作目录登记：机器 × 项目仓库 → 本地路径）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | PK |
| project_id | ID | N | | 项目实体 id（`RegisterEntity` 行过滤） |
| repository_id | ID | N | | 07 `delivery_project_repositories.id` |
| machine_id | ID | N | | |
| path | STR(1024) | N | | 绝对路径，原样保存 |
| path_key | HASH | N | | SHA-256(规范化路径)：分隔符统一 `/`、去尾 `/`、WINDOWS/MACOS 机器转小写 |
| path_lock | HASH | Y | | 锁键：archived_at 为空时 = path_key‖machine_id 的哈希；UNIQUE（同机同路径只属一个仓库） |
| repo_lock | STR(60) | Y | | 锁键：archived_at 为空时 = `machine_id:repository_id`；UNIQUE（每机每仓库一个路径） |
| status | ENUM `WorkdirStatus` | N | `UNVERIFIED` | UNVERIFIED / VERIFIED / INVALID |
| probe_detail | JSON | Y | | `{exists, is_dir, writable, git:{is_repo, remote_url?, head?, branch?}, error?}` |
| verified_at | TS | Y | | |
| version | INT | N | 1 | |
| archived_at | TS | Y | | |

索引：UNIQUE(`path_lock`)；UNIQUE(`repo_lock`)；(`tenant_id`,`project_id`)；(`machine_id`,`archived_at`)。

### 3.11 `fleet_slot_reservations`（执行槽占用）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | PK |
| machine_id | ID | N | | |
| holder_kind | ENUM | N | | EXECUTION / VERIFICATION / COLLECTION / MERGE（与 03 命令对应） |
| holder_id | ID | N | | 03 资源 id |
| dispatch_epoch | INT | Y | | EXECUTION 时填 |
| project_id | ID | Y | | 公平性统计 |
| slot_no | INT | N | | 1..slot_count |
| slot_lock | STR(40) | Y | | 锁键：released_at 为空时 = `machine_id:slot_no`；UNIQUE |
| holder_lock | STR(60) | Y | | 锁键：released_at 为空时 = `holder_kind:holder_id`；UNIQUE（同一持有者不重复占槽） |
| reserved_at | TS | N | | |
| released_at | TS | Y | | |
| release_reason | ENUM | Y | | TERMINAL / CANCELLED / REJECTED / SESSION_LOST / REVOKED / DISPATCH_TIMEOUT / SLOT_SHRINK |

索引：UNIQUE(`slot_lock`)；UNIQUE(`holder_lock`)；(`machine_id`,`released_at`)；(`tenant_id`,`project_id`,`released_at`)。
> 「排队」不建独立表：03 的 `execution_commands` 保持 PENDING 即排队（BR-03-20 ④）；本模块只负责槽位与选择顺序（BR-04-60）。

### 3.12 `fleet_model_prices`、`fleet_gateway_configs`、`fleet_gateway_requests`

**`fleet_model_prices`**（03 计费单价来源；运行时自报 `cost_usd` 时以自报为准）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK |
| runtime_kind | ENUM | N | UNIQUE(`tenant_id`,`runtime_kind`,`model_id`) |
| model_id | STR(160) | N | |
| input_micros_per_mtok / output_micros_per_mtok / cache_read_micros_per_mtok / cache_write_micros_per_mtok | BIGINT | N | 每百万 token 的 `cost_micros` |
| version | INT | N | |

**`fleet_gateway_configs`**（每租户 1 行）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | PK；UNIQUE(`tenant_id`) |
| enabled | BOOL | N | false | |
| machine_id | ID | Y | | 路由机器（须有 CODEX） |
| default_model | STR(160) | N | `''` | 启用时必填 |
| key_hash | HASH | N | | SHA-256(密钥)；UNIQUE（请求按哈希反查租户） |
| key_sealed | TEXT | N | | 密钥密文（平台密钥封装，CR-04-07），供「显示」 |
| key_last4 | STR(4) | N | | 遮罩显示 |
| revision | BIGINT | N | 1 | 启停、换机器、轮换密钥时 +1；机器端据此复位工作进程 |
| rotated_at / rotated_by | TS / ID | Y | | |
| version | INT | N | 1 | |

**`fleet_gateway_requests`**（「最近请求」，仅元数据；每租户保留最新 100 行，见 J8）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | ID | N | PK；对外 `request_id` = `gwr_` + id |
| revision | BIGINT | N | 创建时配置 revision；不符即取消 |
| machine_id / conn_epoch | ID / BIGINT | Y | 下发目标与代次 |
| model | STR(160) | N | |
| stream | BOOL | N | |
| state | ENUM `GatewayRequestState` | N | QUEUED / RUNNING / COMPLETED / FAILED / CANCELLED |
| body | JSON | Y | 请求体 ≤1 MiB，**仅在途期间保存，终态立即置 NULL**（不保留提示词与工具内容） |
| result | JSON | Y | 机器输出 ≤900 KB，HTTP 处理方读取后立即置 NULL |
| error_code | INT | Y | |
| error | STR(500) | Y | |
| created_at / started_at / finished_at | TS | | |
| duration_ms | BIGINT | Y | |

索引：(`tenant_id`,`created_at` DESC)；(`state`,`created_at`)；(`machine_id`,`state`)。

**幂等**：`Idempotency-Key` 由平台统一幂等辅助处理（09 C7，表 `platform_idempotency_keys`，保留 24 h）：同键同请求返回首次结果；同键不同请求 → 422 **176425**；首次请求仍在处理 → 409 **176409**（00 §9）。本模块不建幂等表、不定义幂等错误码。

### 3.13 JSON Schema

- **S1 runtimes**：`array ≤4 of {kind: RuntimeKind, version: string ≤64, path: string ≤1024}`，`kind` 唯一。
- **S2 model entries**：`array ≤1000 of {id: string ≤160 /^[^\s]+$/, label?: ≤200, resolved_model?: ≤160, efforts?: string[] ≤12（每项 ≤32）, default_effort?: ≤32, is_default?: bool, description?: ≤500, model_family?: ModelFamily}`（`model_family` 机器可不报，服务端按 BR-04-49 补全后存储）；整体 ≤512 KB（沿用 Preview）。
- **S3 capability result**：`{runtime: RuntimeKind, kind: "skills"|"mcp", entries: array ≤2000 of {name ≤200, description? ≤2000, status? ("enabled"|"disabled"|"error"|"unknown"), auth? ≤64, tools? string[] ≤500, path? ≤1024, scope? ("user"|"project"|"plugin"|"builtin"), label? ≤200}, errors: string[] ≤20}`；skills + mcp 合计 ≤900 KB（沿用 Preview）。
- **S4 tools**：`array ≤16 of {tool_key: "browser"|"ffmpeg"|"ffprobe"|"whisperx"|"pyscenedetect"|"node"|"python", status: "AVAILABLE"|"MISSING"|"ERROR", variant?: ≤32（browser：`edge`/`chrome`/`chromium`）, version?: ≤64, path?: ≤1024, probe?: {can_screenshot: bool, duration_ms, error? ≤500}（仅 browser）, error?: ≤500}`，`tool_key` 唯一；未知 `tool_key` 忽略（向前兼容）。
- **ModelFamily**：`anthropic` / `openai` / `google` / `other` / `local:<name>`（`<name>` ≤24，`^[a-z0-9-]+$`）；模块内值域，经 `MachineDirectory.ModelFamily` 提供给 03 / 05。
- **labels / env_passthrough_names / manual_model_ids**：见对应列约束。

### 3.14 `fleet_machine_tools`（D112 / D113：验证器与内部质量门所需工具）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | | PK |
| machine_id | ID | N | | UNIQUE(`machine_id`,`tool_key`) |
| tool_key | STR(32) | N | | S4 `tool_key` |
| variant | STR(32) | Y | | browser：`edge` / `chrome` / `chromium` |
| version / path | STR(64) / STR(1024) | Y | | 检测到的版本与绝对路径 |
| status | ENUM | N | `MISSING` | AVAILABLE / MISSING / ERROR |
| can_screenshot | BOOL | Y | | 仅 browser：最近一次截图探测结果；NULL = 未探测 |
| probe_error | STR(500) | Y | | |
| detected_at / probed_at | TS | Y | | |

派生能力标记（不落库，读时计算，BR-04-48）：`browser_capture`、`media_probe`、`media_transcode`、`speech_transcribe`、`scene_detect`。

### 3.15 `fleet_machines` 增补列（D119）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| machine_type | ENUM `MachineType` | N | `WORKSTATION` | WORKSTATION / SERVER / K8S_POOL（00 §5）；创建后不可改（422 **140482**） |
| allow_remote_preview | BOOL | N | true | 允许远程预览（服务预览与产物预览）；关闭时不得为该机器的任何执行开流（BR-04-95） |
| allow_remote_terminal | BOOL | N | WORKSTATION=false；SERVER / K8S_POOL=true | 允许远程终端（交互式 shell，v1.1）；只读观看不受此开关约束（03 BR-03-124）。**本规格提议**：SERVER / K8S_POOL 默认开启（无个人数据，且有沙箱，BR-04-96） |
| allow_file_edit | BOOL | N | WORKSTATION=false；SERVER / K8S_POOL=true | 允许人工接管后编辑文件（v1.1）；默认值为**本规格提议**，理由同上 |
| tunnel_bandwidth_kbps | INT | N | 20000 | 该机经隧道的出站带宽上限（KiB/s 取整为 kbps），范围 1000–1000000（BR-04-97） |
| tunnel_max_streams | INT | N | 256 | 该机并发流上限，范围 16–4096 |
| access_version | INT | N | 1 | 远程访问字段变更计数，随 `machine.access_changed` 发布；不参与 `version` 乐观锁以外的语义 |
| credential_origin | ENUM | N | `DEVICE_CODE` | DEVICE_CODE（WORKSTATION 接入）/ PRE_ISSUED（SERVER、K8S_POOL 预签发，BR-04-99） |

`fleet_machines.enrollment_id` 对 PRE_ISSUED 机器为 NULL（列改为可空；WORKSTATION 仍必填，CHECK `credential_origin='DEVICE_CODE' ⇔ enrollment_id IS NOT NULL`）。`slot_count` 的 CHECK 改为：WORKSTATION / SERVER 1–8；K8S_POOL 1–200（= 池最大 Pod 数，BR-04-100）。

### 3.16 `fleet_pool_configs`（K8S_POOL，每机 1 行，v1.1）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| machine_id | ID | N | | PK；机器 `machine_type=K8S_POOL` |
| namespace | STR(63) | N | | DNS-1123 标签 |
| pod_template | JSON | N | | 受限 Pod 模板：`{image, image_pull_secret?, cpu_request, cpu_limit, memory_request, memory_limit, ephemeral_storage, node_selector{}, tolerations[], service_account?, env_from_secrets[], git_credentials_secret?}`；**不允许** `hostPath`、`privileged`、`hostNetwork`、`hostPID`（422 **140488**） |
| min_warm | INT | N | 0 | 预热空闲 Pod 数 0–20 |
| max_pods | INT | N | 4 | = `fleet_machines.slot_count`，1–200 |
| scale_down_idle_s | INT | N | 600 | 超出 `min_warm` 的空闲 Pod 存活秒数 60–3600 |
| pod_start_timeout_s | INT | N | 300 | Pod 就绪超时 60–1800 |
| version | INT | N | 1 | 乐观锁 |

### 3.17 `fleet_pool_pods`（K8S_POOL 的临时 Pod，v1.1）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| id | ID | N | | PK |
| machine_id | ID | N | | 所属池 |
| pod_name | STR(253) | N | | 池控制器生成；UNIQUE(`machine_id`,`pod_name`) |
| state | ENUM `PoolPodState` | N | `REQUESTED` | §4.6 |
| slot_no | INT | Y | | 绑定的执行槽（BOUND 时） |
| holder_kind / holder_id | ENUM `SlotHolderKind` / ID | Y | | 绑定的持有者（执行、验证、评审预览等） |
| requested_at / ready_at / bound_at / terminated_at | TS | Y | | |
| failure | STR(500) | Y | | 调度失败、镜像拉取失败等摘要（不含密钥） |

索引：(`machine_id`,`state`)；(`state`,`requested_at`)（J9）。终态行保留 7 天后物理删除（Pod 是基础设施记录，不是业务记录，**本规格提议**）。

---

## 4. 状态机

### 4.1 `EnrollmentStatus`（接入记录；对应 PRD §6.8 的 PENDING_CONFIRM / DENIED / EXPIRED）

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| N1 | — | 机器 `POST /runner-enrollments`（附加机器信息） | 设备码有效、属本租户机器客户端、未附加过 | PENDING_CONFIRM | 埋点 `machine_register_begun` |
| N2 | PENDING_CONFIRM | 控制台确认 | `now < expires_at`；调用者 HUMAN 且有 `machine_manage`；名称合法唯一；底座设备码仍 pending | CONFIRMED | 同一事务：底座设备码 → approved（记录批准者与认证强度）；建机器服务账号；建 `fleet_machines`（OFFLINE）；建凭证行 PENDING_DELIVERY；替换时吊销旧机器凭证；审计 `fleet.machine_enrollment_confirmed`；SSE |
| N3 | PENDING_CONFIRM | 控制台拒绝 | 同上（无名称校验） | DENIED | 底座设备码 → denied；审计 `fleet.machine_enrollment_denied`；埋点 |
| N4 | PENDING_CONFIRM | 机器端撤回（Ctrl+C / `DELETE /runner-enrollments/{id}`） | 持有设备码 | CANCELLED | 底座设备码 → denied（经 `DeviceGrants.Withdraw`）；SSE |
| N5 | PENDING_CONFIRM | J2 过期任务 / 任何读取时惰性判定 `expires_at ≤ now` | — | EXPIRED | SSE；埋点 `machine_register_expired` |
| N6 | CONFIRMED | 机器领取凭证 | 设备码 approved 未消费；节流通过 | CONFIRMED（`credential_collected_at`） | 底座设备码 consumed；签发凭证明文（仅此一次） |

非法迁移：对非 PENDING_CONFIRM 的记录确认/拒绝 → 409 **140411**（`data.status` 返回当前值）；确认时恰好过期 → 409 **140412**；对 CONFIRMED 以外领取 → 按底座语义返回 `authorization_pending` / `access_denied` / `expired_token`（§6.3 P3）；重复领取（已消费）→ 400 **140410**（与「不存在」不可区分）。

### 4.2 `MachineState`

| # | 当前 | 触发 | 守卫 | 目标 | 副作用（事件 / 审计 / 通知） |
|---|---|---|---|---|---|
| M1 | — | N2 确认 | — | OFFLINE | `machine.state_changed{to:OFFLINE, reason:ENROLLED}` |
| M2 | OFFLINE | 机器开会话（§6.4 C1） | 凭证有效；`drain_requested=false` | ONLINE | `conn_epoch+1`；`online_since`；`offline_since=NULL`；若曾通知离线 → 恢复通知 N-04-02 |
| M3 | OFFLINE | 开会话 | `drain_requested=true` | DRAINING | 同 M2 |
| M4 | ONLINE | J1：`now − last_seen_at > 30 s` | — | OFFLINE（LEASE_EXPIRED） | 会话关闭；`offline_since=last_seen_at`；释放 SESSION_LOST 以外的槽由 03 终态驱动；事件（03 据此中断执行） |
| M5 | ONLINE | 机器 `DELETE /runner/sessions/current` | epoch 一致 | OFFLINE（DISCONNECTED） | 同 M4 |
| M6 | ONLINE | `PUT /machines/{id}/draining` | `machine_manage`；If-Match | DRAINING | `drain_requested=true`；审计 `fleet.machine_drain_started`；不再分配新槽 |
| M7 | DRAINING | `DELETE /machines/{id}/draining` | If-Match | ONLINE | 审计 `fleet.machine_drain_cancelled` |
| M8 | DRAINING | J1 租约过期 / 机器断开 | — | OFFLINE | `drain_requested` 保留 |
| M9 | DRAINING | 占用槽归零 | `drain_mode=DISCONNECT` | DRAINING → 下发 `stop{reason:DRAINED}` → 机器断开 → OFFLINE（DRAIN_STOP） | `drained_at`；通知发起人 N-04-05 |
| M10 | OFFLINE | `PUT /machines/{id}/draining` | — | OFFLINE（仅置 `drain_requested`） | 审计 |
| M11 | ONLINE / OFFLINE / DRAINING | `POST /machines/{id}/revocation` | HUMAN；`machine_revoke`（不可委托）；step-up；名称确认 | REVOKED | 同一事务：停用服务账号与全部凭证（底座递增 token_version → 访问令牌立即失效）；会话关闭（REVOKED）；释放全部槽；审计 `fleet.machine_revoked`；事件（03 → INTERRUPTED，05 → Agent STOPPED）；通知 N-04-04 |
| M12 | ONLINE / DRAINING | 新会话以新 `incarnation_id` 打开且旧会话已过期 | 旧会话 `last_seen_at` 超 30 s | 不变（epoch+1，旧会话 SUPERSEDED） | 旧 epoch 的一切写入 409 140431 |

非法迁移与错误码：

| 当前 | 尝试 | 结果 |
|---|---|---|
| REVOKED | 任何状态变更、编辑、排空、轮换、检测、开会话 | 409 **140421**（开会话为 401 140401：凭证已停用） |
| DRAINING | 再次排空 | 200 幂等（返回当前资源） |
| ONLINE / OFFLINE | 取消排空且 `drain_requested=false` | 200 幂等 |
| OFFLINE | 能力检测 / 模型刷新 / 运行时重检 / 工作目录探测 | 409 **140423** |
| DRAINING | 预留执行槽（`ReserveSlot`） | 返回 `MACHINE_DRAINING`（03 映射 130413） |
| 任意 | 版本不符 | 409 **140409**；缺 If-Match → 428 **140428** |

### 4.3 `MachineCredentialState`

| 当前 | 触发 | 目标 | 说明 |
|---|---|---|---|
| — | N6 领取 / 轮换后首次下发 | PENDING_DELIVERY（明文在本次响应中下发，`issued_at`） | 不落明文 |
| PENDING_DELIVERY | 机器回执 `credential.received` | ACTIVE | `delivered_at` |
| PENDING_DELIVERY | 60 s 内无回执（J5） | REVOKED（DELIVERY_FAILED） + 新建 PENDING_DELIVERY 重发 | `delivery_attempts ≤5`，超过 → 通知 N-04-06，轮换失败但旧凭证仍在宽限期内 |
| ACTIVE | 发起轮换 | RETIRING（`retire_at=now+24h`） | 同时新建 PENDING_DELIVERY |
| RETIRING | `retire_at ≤ now`（J5） | REVOKED（ROTATED） | 底座吊销 → token_version+1：机器下次请求 401 后用新凭证重取访问令牌（BR-04-24） |
| 任意非 REVOKED | 机器吊销 / 重新接入替换 | REVOKED（MACHINE_REVOKED / REENROLLED） | |

非法：已有 PENDING_DELIVERY 时再轮换 → 409 **140460**；REVOKED 机器轮换 → 409 140421。

### 4.4 `InspectionState`（能力检测；模型目录与运行时重检同构但无独立状态行）

| 当前 | 触发 | 目标 | 说明 |
|---|---|---|---|
| — / READY / ERROR | `POST …/capability-inspections` | QUEUED（新 `request_id`） | 机器离线 → 140423 |
| QUEUED / RUNNING | 再次请求 | 不变（合并，返回现行） | 沿用 Preview「重复请求合并」 |
| QUEUED | 轮询响应携带该检测 | RUNNING（`conn_epoch` 记录） | 每机同一时刻至多 1 个 RUNNING（沿用 Preview），其余排队 |
| RUNNING | 机器回传（`request_id` 与 epoch 匹配） | READY / ERROR | skills 或 mcp 缺失 → ERROR「运行时返回的能力清单不完整」 |
| QUEUED / RUNNING | J3：请求超 65 s，或机器离线，或 epoch 变化 | ERROR「检测中断或超时，请刷新重试」 | 沿用 Preview 65 s |
| 任意终态 | 迟到回传（request_id 不匹配） | 不变 | 409 **140450** `inspection_stale`，机器丢弃 |

### 4.5 `GatewayRequestState`

QUEUED →（随轮询下发）RUNNING →（机器结果）COMPLETED / FAILED；QUEUED / RUNNING →（客户端断开、操作员取消、轮换密钥、关闭网关、换机器、180 s 到期）CANCELLED / FAILED；机器上报 active 不含某 RUNNING 请求 → FAILED「节点丢失执行，请求未重放」；机器 epoch 变化 → FAILED「节点已重启，请求未重放」（沿用 Preview `gatewayRunner`）。终态不可再迁移（迟到结果忽略）。

---

### 4.6 `PoolPodState`（K8S_POOL，v1.1）

| # | 当前 | 触发 | 目标 | 副作用 |
|---|---|---|---|---|
| K1 | — | `ReserveSlot` 成功且无可用 WARM Pod，或 J9 补足 `min_warm` | REQUESTED | 池控制器下发 `pool.pod_create{pod_id}`（§6.4b） |
| K2 | REQUESTED | 控制器回报 `pool.pod_state{SCHEDULING}` | SCHEDULING | — |
| K3 | REQUESTED / SCHEDULING | 回报 READY（Pod 内机器程序已连上控制器） | WARM（无持有者）或 BOUND（为持有者创建） | `ready_at` |
| K4 | WARM | `ReserveSlot` 选中 | BOUND | `bound_at`、`slot_no`、持有者 |
| K5 | BOUND | 持有者 `ReleaseSlot`（执行终态 / INTERRUPTED，或评审预览停止） | TERMINATING | 下发 `pool.pod_delete`；Pod 不回到 WARM（一次执行一个 Pod，工作区不复用） |
| K6 | WARM | 超出 `min_warm` 且空闲 > `scale_down_idle_s`（J9） | TERMINATING | 同上 |
| K7 | TERMINATING | 回报 GONE 或 120 s 无回报（J9 再下发一次后强制） | GONE | `terminated_at` |
| K8 | REQUESTED / SCHEDULING | 超过 `pod_start_timeout_s` 或回报 FAILED | FAILED | 有持有者 → 释放槽并以 `SPAWN_FAILED` 回调 03；N-04-10 |
| K9 | 任意非终态 | 池机器 OFFLINE / REVOKED / 排空完成 | TERMINATING | 控制器重连后按服务端清单对账：服务端无记录的 Pod 一律删除 |

终态：GONE、FAILED。PAUSED 的执行**保留**其 BOUND Pod（工作区仍在，便于人工接管，03 BR-03-126）；保留上限 24 h，超时由 03 转 INTERRUPTED（`MACHINE_OFFLINE` 同语义，原因 `POD_RETENTION_EXPIRED`）后释放。

### 4.7 隧道流（`TunnelStreamState`，D119；只存内存与指标，不落库）

`OPENING`（服务端已发 `tunnel.open`）→ `OPEN`（机器 `tunnel.opened`）→ `HALF_CLOSED`（一端 FIN）→ `CLOSED`（双端 FIN）；任一状态 → `RESET`（任一端 RST、机器 `tunnel.rejected`、连接代次变化、通道断开、开关关闭、配额超限、所属执行或会话结束）。OPENING 10 s 无回应 → RESET（`open_timeout`）。机器端与服务端都以 `stream_id` 识别；服务端分配偶数、机器端不得发起流（机器发起的 `tunnel.open` → 连接级协议错误，关闭 WebSocket 并记 `fleet_tunnel_protocol_errors_total`）。

## 5. 业务规则与边界情况

### 5.1 分发与安装

- **BR-04-01 品牌中立命名**：机器程序的命令名、包名、配置目录、环境变量前缀按「做什么」命名：默认包名与命令 `machine-runner`、配置目录 `<用户配置目录>/machine-runner/`、环境变量前缀 `MACHINE_RUNNER_`。部署可经配置 `fleet.runner.package`、`fleet.runner.command` 覆盖。产品显示名运行时取自服务端 `GET /deployment`，缺失时用中性文案「机器程序」。PRD §6.8 的 `myrmidon-runner` 字样按此修正（CR-04-09）。**本规格提议**。
- **BR-04-02 分发形态**（MVP）：npm 包，内含 Go 主程序各平台二进制（windows-x64/arm64、darwin-x64/arm64、linux-x64/arm64）与运行时执行桥 JS（移植 Preview `src/bridge.ts`：`--detect` / `--models` / `--inspect` / 执行模式）。前置要求：Node.js ≥24（沿用 Preview）、Git ≥2.30、已安装并登录的 Claude Code 和/或 Codex CLI。理由：执行桥依赖 Claude Agent SDK 与 Codex SDK，Node 已是必需，npm 免去自建下载站与签名（签名二进制下载站列入 v1.1）。
- **BR-04-03 安装命令**（由 `GET /machine-enrollment-settings` 按配置模板生成，`{pkg}`=包名、`{ver}`=`fleet.runner.version`、`{server}`=部署外部 URL、`{tenant}`、`{client}`=本租户机器接入客户端 id）：

| OS | 安装 | 登录（接入） | 启动 |
|---|---|---|---|
| Windows（PowerShell） | `npm install -g {pkg}@{ver}` | `machine-runner login --server {server} --tenant {tenant} --client {client}` | `machine-runner start --detach` |
| macOS（zsh） | `npm install -g {pkg}@{ver}` | 同上 | 同上 |
| Linux（bash） | `npm install -g {pkg}@{ver}`（或 `npx -y {pkg}@{ver} login …` 免全局安装） | 同上 | 同上 |

  可选参数：`--profile <name>`（同机多身份，沿用 Preview profile）、`--name <显示名>`（预填控制台名称）、`--no-qr`。`start --detach` 在 Windows 以隐藏窗口启动（`windowsHide`，不新建控制台），在 macOS/Linux 以后台进程启动；系统服务安装（`service install`：systemd user unit / launchd agent / Windows 计划任务）为 v1.1。
- **BR-04-04 最低版本**：配置 `fleet.runner.min_version`；开会话时 `runner_version` 低于它 → 426 **140436**，响应含升级命令；`protocol_version` 不受支持同码。
- **BR-04-05 本地 profile**：`profiles/<profile>/` 下 `identity.json`（`host_id`、`machine_id`、`tenant_id`、`server`）、`outbox/`（03 事件，≤10,000 条）、`runner.log`（10 MB × 5 轮转）、进程锁文件（同 profile 只允许一个进程，OS 文件锁；冲突退出码 8）。凭证明文存 OS 凭据库（Windows 凭据管理器 / macOS 钥匙串 / libsecret）；不可用时写 `credential.json`（权限 0600 / 仅当前用户 ACL）并在启动日志与 `status` 输出警告。复制 profile 到另一台机器属于新接入，不能冒用原机器身份（BR-04-31 指纹提示）。
- **BR-04-06 退出码**：0 正常；2 参数错误；3 接入码过期；4 接入被拒绝；5 凭证无效或机器已吊销；6 用户在机器端取消；7 无法连接服务器（>60 s 重试后）；8 profile 已被占用；9 版本过低。

### 5.2 接入（设备码）

- **BR-04-10 机器接入客户端**：每租户一个 OAuth 公共客户端（`public_client=true`、`grant_types=device_code`、`scopes=fleet.machine`、名称「机器接入」），由首次 `GET /machine-enrollment-settings` 惰性创建（经底座 oauth `CreateClient`）。该客户端带底座新增属性 `device_subject=machine` 与 `device_code_ttl=300s`（CR-04-06）。客户端被禁用 → 设置接口返回 409 **140415**「机器接入已在 OAuth 客户端设置中停用」。
- **BR-04-11 时序**（与底座的映射见 §6.3 与 CR-04-06）：
  1. 机器 `login`：读取/生成 `host_id`；运行时检测（40 s 超时，沿用 Preview）；`POST /oauth/device_authorization`（`client_id`、`scope=fleet.machine`）→ `device_code`、`user_code`（`XXXX-XXXX`，底座字母表 25 字符，约 37 bit）、`expires_in=300`、`interval=5`。
  2. 机器 `POST /runner-enrollments`（公开端点，`device_code` 在请求体，不入 URL/日志）附加主机名、OS、架构、运行时、版本、profile → 建接入记录 PENDING_CONFIRM，返回 `enrollment_id` 与控制台核验地址 `{console}/t/{tenant}/machines/connect?user_code=XXXX-XXXX`（及不含码的地址）。
  3. 机器终端显示：大号接入码、核验地址、该地址的终端二维码（`--no-qr` 关闭）、倒计时「剩余 4:59」（每秒刷新；非 TTY 只打印一次）。
  4. 机器每 `interval` 秒 `POST /runner-enrollments/{id}/credential-collections`；底座语义：未决 → `authorization_pending`；过快 → `slow_down`（机器 interval +5 s，RFC 8628）；拒绝/撤回 → `access_denied`（退出码 4）；过期 → `expired_token`（退出码 3，提示「接入码已过期，请重新运行 login」）。
  5. 控制台输入或扫码打开 → `GET /machine-enrollments?user_code=` → 显示待确认信息 → 确认（N2）。
  6. 机器下一次领取（≤5 s）→ 获得 `{machine_id, principal_id, client_id(凭证 id), client_secret, token_endpoint}`，写入凭据库，发送回执（凭证 → ACTIVE），`client_credentials` 换访问令牌，开会话 → ONLINE。
- **BR-04-12 控制台标签页关闭**：服务端无任何依赖于页面的状态；关闭页面不取消、不确认。接入记录保持 PENDING_CONFIRM 直到 300 s 到期 → EXPIRED；期间任何有 `machine_manage` 的人（含同一人重新打开向导）可重新输入同一接入码继续。机器端在到期后收到 `expired_token` 退出。断网、刷新等未走确认/取消的路径一律走超时（沿用旧 PRD D.2「异常一律按超时取消」）。
- **BR-04-13 确认者**：必须是 HUMAN 主体（服务账号/Agent → 403 **140403**，`reason_code=HUMAN_ONLY`），持 `machine_manage`。批准者的认证时间、ACR、AMR 写入底座设备码行（复用 `DecideDeviceRequest` 语义）。确认不要求 step-up（**本规格提议**：接入受 300 s 时窗与人工比对主机名约束；吊销才是高危动作）。
- **BR-04-14 名称**：确认时可改名；默认 `--name` 或主机名（截断至 100）；与租户内非 REVOKED 机器重名（大小写不敏感）→ 422 **140416**。
- **BR-04-15 防猜码/重放**：
  - 查找接口对「不存在 / 已过期 / 已决定 / 属他租户 / 非机器客户端」一律 404 **140410**（不可区分，沿用底座 `LookupDeviceRequest` 原则）。
  - 失败查找限流：每主体 10 次 / 10 分钟、每租户 50 次 / 10 分钟，超出 429 **140413**（`Retry-After` 秒数），租户级触发时审计 `fleet.enrollment_lookup_throttled`。8 位码 25⁸ ≈ 1.5×10¹¹，在限流下 300 s 内猜中概率 <10⁻⁸。
  - `device_code` 32 字节随机，仅存哈希；领取在底座条件消费（`consumed_at IS NULL`）下进行，二次领取 → 400 140410；领取响应丢失 → 机器必须重新 `login`；领取后 **60 s 内**未以该凭证开会话（C1）的孤儿凭证由 J5 吊销（不重建；接入凭证与轮换凭证同一节奏，见 J5），重新接入经 BR-04-16 替换。
  - 附加机器信息只允许一次（`device_code_hash` UNIQUE）；重复附加 → 409 **140417**。
- **BR-04-16 同机重复接入**：确认页若发现同租户存在 `host_fingerprint` 相同且非 REVOKED 的机器 M，显示「检测到这台电脑之前接入过：M」并要求选择：〔替换 M〕（沿用 M 的 `machine_id`、名称、工作目录、Agent 绑定；M 的旧凭证 REENROLLED 吊销；M 现有会话被 SUPERSEDED）或〔作为新机器〕。未选择 → 422 **140414**。**本规格提议**：解决「凭证丢失需重接」而不产生重复机器（旧设计「重连复用原 runner_id，不能不断创建重复 machine」）。
- **BR-04-17 底座通用设备页**：`/admin/:tenantID/device` 查找到 `device_subject=machine` 的请求时，不显示批准按钮，改为提示「这是机器接入请求，请在『机器 → 接入机器』中确认」并链接过去；底座 `POST /oauth/device-requests/decisions` 对该类请求返回 404（CR-04-06）。防止绕过机器元数据核对与服务账号签发。
- **BR-04-18 令牌端点隔离**：`/oauth/token` 对 `device_subject=machine` 客户端的 `device_code` 授权一律 `invalid_grant`，绝不向机器签发批准者本人的访问令牌（底座现行 `deviceToken` 以批准者身份签发，CR-04-06）。

### 5.3 凭证、轮换与吊销

- **BR-04-20 机器身份**：一台机器 = 一个底座服务账号（`login_name = machine-<machine_id>`，显示名 = 机器名），无角色绑定 → 无法调用任何控制台受控接口（沿用 Preview「runner token 不能读取客户端 API」）；仅可调用 §6.4 通道接口（`RegisterTenantMember` + 本模块机器守卫）。凭证 = 服务账号凭证（`client_credentials`），不过期（`expires_at=NULL`），以轮换/吊销控制生命周期。
- **BR-04-21 访问令牌**：机器以 `client_id=凭证 id`、`client_secret` 调 `/oauth/token`（`grant_type=client_credentials`）取访问令牌（底座默认 15 min），在剩余 20% 时刷新；每个通道请求 `Authorization: Bearer`。底座对服务账号令牌逐请求校验账户状态与 `token_version`，因此停用/吊销在下一请求生效。
- **BR-04-22 执行环境隔离**：机器凭证、访问令牌、网关密钥绝不注入运行时进程环境（PRD §10.2、§16）；执行桥环境 = 最小基础集（PATH、HOME/USERPROFILE、LANG、TEMP 等）+ 03 下发的 `env_extra_names` ∩ 本机 `env_passthrough_names`。
- **BR-04-23 轮换**：`POST /machines/{id}/credential-rotations` → 旧 ACTIVE → RETIRING（24 h），新凭证 PENDING_DELIVERY；**明文在下发时才生成**：下一次轮询响应携带 `credential_rotation{rotation_id, client_id, client_secret, retire_old_at}`（同一事务调用底座签发，不落明文）；机器写入凭据库后在下一次请求发送回执 → ACTIVE。机器离线超过 24 h → 旧凭证被吊销，机器无法重连，须重新接入（BR-04-16 替换）。
- **BR-04-24 宽限期结束的副作用**：底座吊销任一凭证会递增服务账号 `token_version`，使**所有**现存访问令牌失效（含用新凭证换的）；机器收到 401 时先用当前凭证重换一次令牌，成功即继续，失败才判定被吊销（退出码 5）。
- **BR-04-25 吊销**：`POST /machines/{id}/revocation`，要求：HUMAN、`machine_revoke`（00 §6.4 不可委托，经 08 `AuthorityChecker` 判定，委托来源 → 403 `reason_code=NON_DELEGABLE`）、请求体 `confirm_name` 与当前名称完全一致（否则 422 **140425**）、`reason` 1–2000 字、`reauth_proof`（Cookie + Origin + 底座 `ReauthenticationConsumer`，purpose `machine_revoke`，target = machine_id；缺失或无效 → 403 **140403** `reason_code=REAUTH_REQUIRED`）。一个事务内完成 M11 全部副作用；服务账号与凭证经底座在同一事务停用（CR-04-08）。不可撤销：再次接入同一电脑产生新机器（REVOKED 机器不参与 BR-04-16 替换）。
- **BR-04-26 吊销后的机器端**：持有中的长轮询在 ≤1 s 内被唤醒并以 401 结束（同实例进程内信号；跨实例经 09 服务端内部订阅主题 `tenant/{t}/machine/{id}/channel`（仅服务端订阅，不对浏览器开放），兜底每 1 s 复查机器行）；机器收到 401 且重换令牌失败 → 向执行桥发 cancel、4 s 后杀进程树（与 03 BR-03-15 一致）、删除本地凭证、保留 outbox 以便诊断、退出码 5。吊销后的任何重连尝试 → 401 140401，审计 `fleet.revoked_machine_access_attempted`（同机器 10 分钟内合并一条）。

### 5.4 通道、心跳与离线

- **BR-04-30 时序参数**（与 03 对齐；沿用 Preview）：

| 参数 | 值 | 说明 |
|---|---|---|
| 长轮询保持 `wait_ms` | 默认 10,000，上限 10,000 | 有命令即刻返回；机器收到响应后立即发下一次 |
| 短轮询间隔 | 350 ms | 回退模式（代理不支持长连接时；沿用 Preview 350 ms） |
| 机器心跳 | 每次轮询即心跳；两次轮询到达间隔 ≤10 s（+网络时延） | |
| 执行租约续约 | ≤10 s（03 `leases.renew`，可与轮询同批） | 03 BR-03-10 |
| 机器自停 | 连续 20 s 无成功响应 → cancel 执行桥，4 s 后杀进程树，保留 outbox | 03 BR-03-11 |
| 会话租约 / 离线判定 | `now − last_seen_at > 30 s` → OFFLINE | 看门狗每 5 s，最坏 35 s |
| 离线通知 | OFFLINE 持续 >60 s | N-04-01 |
| 重连退避 | 1 s、2 s、4 s … 上限 30 s，±20% 抖动 | |

  机器在 20 s 先自停、服务端 30 s 才判离线，保证「判离线时进程已停」，再迁移不会双写（沿用 Preview 与 runtime-modes §7.4）。
- **BR-04-31 会话与连接代次**：开会话（C1）在机器行上 `conn_epoch+1` 并返回；此后每个通道请求必须带 `X-Runner-Session` 与 `X-Runner-Epoch`，与机器当前值不一致 → 409 **140431** `runner_epoch_fenced`，机器须重新开会话（Preview `X-Runner-Epoch` 语义）。已有会话在 30 s 内仍活跃且 `incarnation_id` 不同 → 409 **140430** `runner_session_active`（`data.retry_after_ms`=剩余租约），防止复制的 profile 互抢；同 `incarnation_id` 重开（网络抖动后）→ 允许，epoch+1。
- **BR-04-32 栅栏与 03 的关系**：`conn_epoch` 是连接级栅栏；`dispatch_epoch` 是执行级栅栏（03）。机器旧连接上的任何写入先被 140431 拒绝；新连接上补交旧派发的事件由 03 依 `dispatch_epoch` 与执行状态裁决（130427）。事件去重键为 `(execution_id, dispatch_epoch, seq)`（03），不使用 `conn_epoch`。
- **BR-04-33 离线后**：机器 OFFLINE 事件 → 03 将该机 RUNNING 执行按租约规则中断（03 E16/E19；本模块不直接改执行）；执行槽由 03 在把执行迁到**终态、INTERRUPTED 或 PAUSED** 的同一事务内调用 `ReleaseSlot` 释放（与 §7.3 兜底一致），本模块不在离线时自行批量释放（避免先释放后复用同一目录）。OFFLINE 期间 03 新命令 → `ReserveSlot` 返回 `MACHINE_OFFLINE`。
- **BR-04-34 服务端重启**：机器会话行不因进程重启而失效；只要机器在 30 s 内重新轮询（带旧 epoch）即继续。执行是否中断由 03 BR-03-12 决定。
- **BR-04-35 负载上限**：单个通道请求体 ≤1 MiB（blob 除外，≤100 MiB，03）；超出 413 **140435**。消息批次 ≤200 条（与 03 `execution.events` 一致）。每机通道请求速率 ≤20 次/秒（短轮询 350 ms 约 3 次/秒，留余量），超出 429。
- **BR-04-36 槽状态核对**：心跳携带 `slots[]`；服务端认为占用、机器连续 2 次心跳报告空闲的槽 → 回调 03 `ReportLost(…, HEARTBEAT_LOST)`；机器报告运行中、服务端无记录的持有者 → 响应 `cancels` 中要求取消（沿用 Preview gateway「未知活跃任务即取消」）。

### 5.5 运行时、能力、模型目录

- **BR-04-40 运行时检测**：机器在开会话前与每 10 分钟、以及收到 `runtime_detection` 请求时运行 `--detect`（40 s 超时），上报 `{kind, version, path}`；缺失的运行时标 MISSING（不删除行，`missing_since`），恢复后 AVAILABLE。MISSING 时：该运行时的放置失败（03 130414）、绑定该运行时的 Agent 由 05 处理（事件 `machine.runtimes_changed`，CR-04-03）。只识别 `RuntimeKind` 已定义值；未知运行时忽略。
- **BR-04-41 能力检测**（Skills / MCP）：不调用模型、不修改任何历史（沿用 Preview）；按 (机器, 运行时, 工作目录) 缓存；手动刷新或机器首次上线后对已登记工作目录各排队一次；机器端超时 55 s，服务端 65 s（沿用 Preview）；每机串行 1 个；结果 ≤900 KB；失败独立显示，不影响执行，也不自动替换运行时。原生配置在本机改动后需手动刷新（UI 文案提示）。MCP 列表不代表实时连接状态（文案提示）。
- **BR-04-42 模型目录**：机器每 5 分钟、开会话时、收到刷新请求时按运行时读取（每运行时 30 s 超时，≤1000 项，≤512 KB）：CLAUDE_CODE 用 Agent SDK `supportedModels()`；CODEX 的 `exec` 不提供目录命令 → 机器端用可得方式读取，失败时服务端**保留上次成功列表**、`source=CACHED` 并显示错误「列表可能过期」（沿用 Preview `modelResult`）。服务端校验每项 `id` 合法后按版本号降序存储。
- **BR-04-43 手填模型**：每机每运行时 ≤50 个手填 ID（第三方代理未列出的模型）；放置与 03 设置校验时，模型 ∈ 目录 ∪ 手填 ∪ 执行已保存的自定义模型 即可；不在其中的仍允许由 03 显式提交（Preview：「未在目录中列出的模型仍可手动填写」），本模块只在 UI 标「未在目录中」。目录刷新绝不替换已保存的自定义模型。
- **BR-04-44 effort**：目录项有 `efforts` 时按项提供；否则用 03 BR-03-33 列出的运行时通用集。
- **BR-04-45 单价**：`fleet_model_prices` 由 `machine_manage` 维护；03 计费时：运行时自报 `cost_usd` 优先，否则按单价 × token；均缺失 → 成本记 0 并标 `source=UNKNOWN`（03 定义）。
- **BR-04-47 工具检测**（D112 / D113，供 03 VISUAL 验证器与验收 Agent 截图、11 确定性验证器）：机器在运行时检测（BR-04-40）之后检测工具，同一触发时机，独立 60 s 总预算、单工具 10 s：
  - **browser**：依次取环境变量 `MACHINE_RUNNER_BROWSER` → Windows 常见安装路径（Edge `%ProgramFiles(x86)%\Microsoft\Edge\Application\msedge.exe`、`%ProgramFiles%` 同路径，Chrome `%ProgramFiles%\Google\Chrome\Application\chrome.exe`、`%LocalAppData%` 同路径）→ macOS `/Applications/{Microsoft Edge,Google Chrome,Chromium}.app` → PATH 中 `chromium`、`chromium-browser`、`google-chrome`、`microsoft-edge`；版本取 `--version`（Windows 读可执行文件版本资源，不启动窗口）。
  - **can_screenshot 探测**：以 `--headless=new --disable-gpu --hide-scrollbars --window-size=320,240 --screenshot=<临时目录>/probe.png` 渲染内置的单色 `data:` 页面，15 s 超时；PNG 签名有效且尺寸 = 320×240 → true。只在开会话时、手动检测（A14）时、浏览器路径或版本变化时执行（不随 10 分钟周期重复启动浏览器）；子进程不经 shell、Windows `windowsHide`；临时目录随后删除。
  - **ffmpeg / ffprobe**：`-version` 首行；**whisperx**（可选）、**pyscenedetect**：`<cmd> --version`，缺失记 MISSING，不报错；**node / python**：`--version`（python 依次试 `python3`、`python`）。
  - 只上报 S4 字段，不上报环境变量值、不上报用户目录以外的目录列表；`path` 在页面中对无 `machine_manage` 者隐藏（与运行时路径一致）。
- **BR-04-48 能力标记**：`browser_capture` = browser AVAILABLE ∧ `can_screenshot=true`；`media_probe` = ffprobe AVAILABLE；`media_transcode` = ffmpeg AVAILABLE；`speech_transcribe` = whisperx AVAILABLE；`scene_detect` = pyscenedetect AVAILABLE。消费：03（VISUAL / INTERACTION 验证器与 AGENT_ACCEPTANCE 截图只放置到具备 `browser_capture` 的机器；无则该验证结论 `INCONCLUSIVE` 并写入证据包 `unverified[]`）；05（ACCEPTOR 候选资格 BR-05-108）；11（BR-11-09 / BR-11-30：所需工具在全部在线机器都不可用 → 工作项 `TOOL_UNAVAILABLE`）。机器离线时其工具不计入 `ToolAvailability`。
- **BR-04-49 模型家族**：模型目录每项带 `model_family`：机器上报值优先；否则按前缀推断（`claude-` → `anthropic`；`gpt-`、`o1`、`o3`、`o4`、`codex-` → `openai`；`gemini-` → `google`）；仍无法判断 → 按运行时默认（CLAUDE_CODE → `anthropic`，CODEX → `openai`）。经第三方代理的模型可由 `machine_manage` 在 A17 以 `families` 覆盖（存 `manual_model_families`），覆盖优先于推断。05 `ModelFamilyOf` 与 03 执行快照 `model_family` 只经 `MachineDirectory.ModelFamily` 取值，不各自推断。
- **BR-04-46 透传环境变量名校验**：名称不得等于 `MACHINE_RUNNER_*` 或匹配本机凭证存储变量；名称列表变更审计 `fleet.machine_updated`（含 diff 名单，不含值——值从不经过服务端）。

### 5.6 工作目录、执行槽与调度

- **BR-04-50 工作目录登记**：`POST /machines/{id}/workdirs {repository_id, path}`：仓库须属项目且未归档（经 07 读取，422 **140442**）；路径须绝对路径（Windows `X:\…` 或 `\\server\share\…`；POSIX 以 `/` 开头），≤1024；同机同路径已属其他仓库 → 409 **140444**；同机同仓库已有路径 → 409 **140445**（先归档旧的）。创建后立即排队一次探测（机器在线时），结果写 `status`/`probe_detail`：不存在或非目录 → INVALID；存在但非 Git 仓库 → VERIFIED 并提示；`remote_url` 与仓库登记 URL 不同 → VERIFIED + 警告。
- **BR-04-51 `ResolveWorkspace(machine, project, repository?)`**（供 03）：返回 `{workdir_id, path, path_key, status}`；未登记 → `WORKDIR_NOT_REGISTERED`（03 映射 WORKDIR_UNAVAILABLE）；INVALID → 同上并附 `probe_detail.error`。
- **BR-04-52 一个目录一个写者**：写锁归 03 `execution_workdir_locks`（键 `(machine_id, workdir_key)`，本模块提供 `path_key` 作为 `workdir_key`）。本模块的放置排序会跳过写锁被占用的候选（只读查询），最终以 03 插锁唯一约束为准。
- **BR-04-53 `ReserveSlot(tx, machine_id, holder_kind, holder_id, dispatch_epoch?, project_id?)`**：在调用方（03）事务内：机器 ONLINE 且未排空（否则 `MACHINE_OFFLINE`/`MACHINE_DRAINING`）→ 找最小空闲 `slot_no` 插入（`slot_lock` 唯一冲突重试 ≤3 次）→ 槽满返回 `NO_FREE_SLOT`（03 命令保持 PENDING 排队，≤60 min 后 03 E04 DISPATCH_TIMEOUT）。同一持有者重复调用返回已有槽（`holder_lock`，幂等）。`ReleaseSlot(tx, holder_kind, holder_id, reason)` 幂等。
- **BR-04-54 放置候选**（`ListEligibleMachines`，供 03 / 05 选机）：过滤 ① 未吊销、ONLINE、非 DRAINING；② 有该 `RuntimeKind` 且 AVAILABLE；③ 若指定模型：模型 ∈ 目录 ∪ 手填（否则候选仍返回但 `model_listed=false`）；④ 项目仓库在该机已登记且 VERIFIED（`require_workdir=true` 时）；⑤ Agent 执行：只返回 Agent 绑定的机器（05）。排序：空闲槽数降序 → 最近成功心跳新者优先 → 名称。
- **BR-04-55 公平性**（**本规格提议**）：当某机存在多个项目的待取命令时，`PendingCommands` 的出队顺序由本模块给出：先按「该项目在此机当前占用槽数」升序，再按命令创建时间升序；且单个项目在有其他项目排队时至多占用 `max(1, slot_count−1)` 个槽。理由：防止一个项目的批量 Agent 执行长期占满共享机器。
- **BR-04-56 槽数修改**：`PATCH /machines/{id}` 调小 `slot_count` 时不中断运行中的执行，超出部分在释放后不再分配；调大立即生效。范围 1–8，越界 422 **140480**。
- **BR-04-57 离线迁移判定**（`PlanMigration(execution)`，供 03 BR-03-36；本模块不自动迁移，有副作用的工作不自动交给另一台机器——沿用 runtime-modes §6.3/§7.4）：返回候选机器列表，每项 `{machine_id, eligible, reason?, resume_mode}`：
  - eligible 需：BR-04-54 ①②④，且运行时种类**与原执行相同**（不跨 provider、不自动换模型；目标缺该运行时 → `RUNTIME_UNAVAILABLE`）。
  - `resume_mode = NATIVE` 当且仅当：目标为原机器（重连后），或目标工作目录 `path_key` 与原目录相同（任一端 Windows/macOS 时大小写不敏感，与 03 一致）；否则 `CARRYOVER`（03 负责拼接上下文）。目标机能否读取原生会话文件无法由服务端保证 —— NATIVE 续接失败时 03 按运行时错误降级为 CARRYOVER 新原生会话（沿用 Preview）。

### 5.7 并发、幂等、权限、上限（汇总）

- **BR-04-60 乐观锁**：可变资源（机器、工作目录、模型目录手填列表、单价、网关配置）响应头 `ETag: "<version>"`，写请求 `If-Match: "<version>"`；缺失 428 **140428**，不符 409 **140409**（与 02/03/07 一致，CR-04-02）。
- **BR-04-61 幂等**：确认、拒绝、轮换、吊销、工作目录创建、检测请求、网关轮换接受 `Idempotency-Key`（09 C7 平台幂等辅助；同键不同请求 422 176425，进行中 409 176409）；确认/拒绝另以接入记录状态 CAS 保证只生效一次（并发两人确认：一人成功，另一人 409 140411）。通道接口不使用 `Idempotency-Key`，以自然键去重（`command_id`、`(execution_id, dispatch_epoch, seq)`、`rotation_id`、`request_id`）。
- **BR-04-62 权限不足**：缺权限 403 **140403**（`data.missing_permission`）；他租户资源一律 404（不泄露存在性）。
- **BR-04-63 数据不存在**：机器 404 **140404**；工作目录 404 **140446**；接入记录（控制台按 id）404 **140410**。
- **BR-04-64 上限**：每租户非吊销机器 ≤200（422 **140481**，配置 `fleet.max_machines`）；每机工作目录 ≤200；每服务账号凭证 ≤20（底座上限；轮换不会超过 2 把非吊销凭证）。
- **BR-04-65 审计**：接入确认/拒绝、吊销、轮换、排空开始/取消、机器改名/槽数/透传名单变更、工作目录登记/归档、单价变更、网关配置/密钥轮换/显示 —— 均经 `audit.Appender` 在业务事务内追加，`detail` 含 `reason`（可空者写 `""`）、`authority_source`、`evidence_refs:[]`。

### 5.8 API 网关（实验）

> 语义移植 Preview `gateway.go` / `docs/gateway-desktop.md`；页面与接口常驻「实验性」标识。

- **BR-04-70 定位**：把一台在线机器上已登录的 Codex 暴露为 OpenAI 兼容 Responses API。每租户一套配置、一把独立密钥、一个可信访问域；不提供多用户身份隔离或细粒度工具授权（文案明示「仅供你信任的客户端使用，不是加固的公共服务」）。
- **BR-04-71 配置**：`PUT /gateway-config {enabled, machine_id, default_model}`（`gateway_manage`，If-Match）：启用时机器须 ONLINE 且 CODEX AVAILABLE（409 **140471** / 422 **140476**），`default_model` 1–160 字符必填（422 140400）。启停或换机器 → `revision+1`，取消全部在途请求（「网关配置已变更」）；只改默认模型不取消。
- **BR-04-72 密钥**：首次读取配置时生成；格式 `gk_` + 43 字符 base64url（256 bit）；只存 SHA-256 哈希（鉴权）与封装密文（显示）。**显示**（`POST /gateway-config/key-reveals`）要求 HUMAN + `gateway_manage` + step-up（purpose `gateway_key_reveal`），每次审计 `fleet.gateway_key_revealed`（**本规格提议**：Preview 可随时显示；新产品多成员共用控制台，显示即授予外部访问，按危险操作处理）。**轮换**（`POST /gateway-config/key-rotations`）红色确认：旧密钥立即失效、所有 QUEUED/RUNNING 请求 CANCELLED（「网关密钥已轮换」）、机器端等待工具结果的上下文被回收（`revision+1`）、响应一次性返回新密钥明文。
- **BR-04-73 外部端点**（协议端点，不走 respx 信封，错误为 OpenAI 错误对象 `{"error":{"message","type":"gateway_error","code"}}`）：`GET /v1/models`、`POST /v1/responses`；其他路径 404「仅支持 /v1/responses 与 /v1/models」。鉴权 `Authorization: Bearer gk_…` 按哈希反查租户（不符 401 **140472**）；有 `Origin` 头且与 Host 不同 → 403（沿用 Preview）；`Cache-Control: no-store`。
- **BR-04-74 兼容子集**（沿用 Preview 已验证范围）：仅文本输入输出；function / custom 工具（工具发现含 `input.additional_tools`，函数参数按 JSON Schema 校验）；`tool_choice` 仅 `auto`/`none`；流式按 Responses SSE 事件编码，等待期间每 5 s 心跳，**不提供逐 token 增量**（以完成的消息为单位）；非流式返回完整 Response 对象。不支持（执行前 400 **140474**，`error.param` 指明字段位置）：图像/文件输入、结构化输出 `text.format`、强制工具、`store=true`、`previous_response_id`、`background`、`temperature`、`max_output_tokens`、Responses WebSocket。工具结果续接期间校验完整历史，不得替换已提交的消息、工具或设置（409）。usage：工具等待中的响应为 `null`，最终响应为机器报告的本轮累计值，不逐响应相加。
- **BR-04-75 模型列表**：`/v1/models` = 路由机器 CODEX 目录（含 CACHED）∪ 手填 ∪ `default_model`，去重，`owned_by:"machine-codex"`；`/v1/responses` 的 `model` 不在其中 → 400 **140475**。
- **BR-04-76 限额与期限**（沿用 Preview）：活跃 HTTP 请求 ≤8（第 9 个 429 **140473**）；机器端保留上下文 ≤8；生成期限 180 s、工具等待上下文 TTL 180 s；请求体 ≤1 MiB（400）；单次机器输出 ≤900 KB；流开始后写超时 15 s；最近请求记录每租户 100 条（只含 id、时间、模型、状态、错误、耗时，**不含提示词与工具内容**）。网关关闭 → 503 **140470**；路由机器离线 → 503 **140471**。
- **BR-04-77 机器端隔离**：网关工作进程使用独立于执行槽的「网关槽」（不占用执行槽）与独立临时目录、只读沙箱，并禁用本地 shell、文件图像、插件、hooks、skills、多代理能力（沿用 Preview；文案注明「尚不构成对恶意模型输出的完整主机隔离证明」）；工作进程环境不含机器凭证与网关密钥；机器程序启动时检查 Codex 上游地址不得指回本网关（递归），检测到则拒绝承接并在网关页显示错误。
- **BR-04-78 不重放**：断线、机器重启、服务重启后不重放不确定的请求；等待工具的旧上下文不可续接（客户端需新会话）。
- **BR-04-79 多副本**：请求排队与结果经 `fleet_gateway_requests` 行传递（HTTP 处理方由实时广播唤醒，兜底每 250 ms 读取），故任一副本接收 HTTP、任一副本接收机器轮询均可工作（**本规格提议**：Preview 为单进程内存队列；新底座可多副本部署）。请求体与结果在终态即置空，满足「不持久化提示词」。

### 5.9 桌面本地服务与 Desktop 壳（可选客户端，v1.1）

- **BR-04-80 本地服务**：`machine-runner serve [--port 4318]`（端口被占用自动选空闲端口并打印）：只监听 `127.0.0.1`/`::1`；拒绝 Host 不是 `127.0.0.1`/`localhost`/`::1` 的请求（403）；有 `Origin` 头时须与 Host 一致（403）；响应头 `Referrer-Policy: no-referrer`、`X-Content-Type-Options: nosniff`、静态页 CSP `frame-ancestors 'none'; object-src 'none'; base-uri 'self'`（沿用 Preview `ComputerHandler`）。提供：`/health`；本机状态静态页（profile、连接状态、`conn_epoch`、槽位、最近 50 行日志摘要、重新登录指引）；`/v1/*` 透明转发到服务端网关（转发前删除 `Cookie` 与 `Origin`，`ResponseHeaderTimeout` 190 s、`/v1` 写超时 205 s、不缓冲 SSE）。不代理控制台 API（控制台在浏览器/Desktop 中直连服务端，使用底座会话 Cookie）。本地停止端点 `POST /local/stop` 须携带有效控制台访问令牌，先向服务端核验（5 s 超时）再停止（沿用 Preview）。
- **BR-04-81 Desktop 壳**：Electron 单实例（按 profile），窗口加载服务端控制台 URL（非本地副本）；`nodeIntegration=false`、`contextIsolation=true`、`sandbox=true`；仅允许本源的 `clipboard-sanitized-write` 权限；外链交给系统浏览器；关闭窗口收至托盘。托盘菜单（三语，产品名取自 `GET /deployment`）：〔打开控制台〕〔在浏览器中打开〕〔本机状态：在线/离线 · 槽 n/m〕（只读）〔暂停接单〕（= 请求排空，需当前登录用户有 `machine_manage`，否则置灰并提示缺少的权限）〔仅退出桌面（机器程序继续运行）〕〔停止机器程序并退出〕（有运行中执行时确认「n 个执行将被中断」）。`--profile` 选择 profile；双击托盘图标显示窗口。

### 5.10 远程访问隧道（D119）

- **BR-04-90 承载通道**：预览、文件读写与终端流只经机器已建立的**出站** WebSocket 通道（§6.4 WebSocket 小节）多路复用传输；机器不监听任何入站端口（本地服务 BR-04-80 仍只绑回环，与隧道无关）。机器不支持 WebSocket（代理拦截、10 分钟内升级失败 3 次而回退 LONG_POLL）时：控制消息照常走长轮询，**远程访问能力不可用**，控制台显示「这台机器的网络不支持实时通道，无法远程预览、浏览文件或观看终端」（错误 409 **140484**）。分期：WebSocket 通道由 v1.1 提前到 MVP（D119 的 MVP 能力依赖它；US-04-15）。
- **BR-04-91 端口归属（机器端判定，服务端不信任 Agent 的自述）**：`HTTP_PROXY` 流只能连接机器回环地址（`127.0.0.1` / `::1`）上的端口，且该端口的**监听进程**必须属于该流所属持有者（执行或评审预览）的进程树：机器程序在启动持有者时记录根进程（Windows 为 Job Object，POSIX 为进程组 + 子孙 PID 集），开流时按操作系统接口（Linux `/proc/net/tcp*` + `/proc/<pid>/fd`；macOS `libproc`；Windows `GetExtendedTcpTable`）取监听者 PID 并判定是否在树内；不在树内、或监听在非回环地址以外的接口但仅回环可达以外的情况一律拒绝（`tunnel.rejected{code:140485}`）。流打开后每 10 s 复查一次，归属变化（进程退出、端口被他人占用）→ RESET。目标主机永远是回环地址，不接受主机名、局域网地址或 Unix 套接字（D119：从不转发任意机器端口）。
- **BR-04-92 路径限定**：`FILE_*` 与 `FS_*` 流的路径为相对工作区根的路径；机器端先规范化（拒绝 `..` 段、绝对路径、Windows 盘符与 UNC、NUL），再解析符号链接与 junction 后检查真实路径仍在工作区根之内，否则拒绝（`140491`）；工作区根 = 该持有者的工作目录（执行的 worktree / 共享目录，评审预览的隔离副本）。机器程序 profile 目录、凭据文件永远不可读，即使位于工作区内。遮罩规则（哪些文件内容不得读出）由 03 在 `tunnel.open.target.mask` 中下发，机器端照做（BR-03-123）。
- **BR-04-93 流的语义来源**：隧道只做帧、流控、配额、归属与路径校验；流的种类、目标与生命周期（谁能开、何时关）由 03 经 `TunnelBroker`（§6.7）决定。04 不接受来自浏览器或机器的直接开流请求。
- **BR-04-94 预览网关**（服务端组件，本模块部署与运行；访问判定交 03 `PreviewAccessResolver`）：
  - 配置（`fleet.preview.*`，部署配置，不写入代码或数据库）：`domain`（**必须**是与控制台不同的可注册域名，如控制台 `console.example.com`、预览 `example-preview.net`；与控制台同一可注册域 → 启动失败，`fleet.preview` 视为未配置）、`mode`（`SUBDOMAIN` 默认 / `PATH`）、`listen`、`tls.cert_file` / `tls.key_file`（`*.<domain>` 通配证书，由部署方经 DNS-01 等方式签发与轮换，进程收到 SIGHUP 或文件变化时热加载）、`max_session_minutes`、`trusted_proxies`。未配置 `domain` → 远程预览整体不可用（A39 返回 `enabled:false`，03 的预览接口 503 **140492**），绝不退回控制台源（D119 已否决）。
  - **子域模式**：每个预览会话一个不可猜测的标签（03 生成，26 位 base32，≥128 bit）→ `https://<label>.<domain>/`；不同会话不同源，Agent 写的页面无法读取其他预览或控制台的 Cookie 与存储。
  - **路径模式回退**（无通配 DNS 时）：`https://<domain>/p/<label>/...`，Cookie 以 `Path=/p/<label>/` 限定；网关不改写响应体，绝对路径资源与 Service Worker 会失效，控制台在预览框上常驻琥珀标签「路径模式：部分应用可能无法正常工作」。
  - 代理能力：HTTP/1.1（含分块与 `Range`）、WebSocket 升级（HMR）、SSE（不缓冲、15 s 写超时、每 30 s 注释心跳）；请求体 ≤50 MiB；去掉入站 `Cookie` 中网关自己的会话 Cookie 后再转发，绝不转发控制台 Cookie（不同源，浏览器本不会发送）；响应补 `X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`，不改写应用自己的 CSP。
  - 访问：网关对每个请求取会话 Cookie（`__Host-preview`，HttpOnly、Secure、SameSite=Lax、15 min 滑动）→ 03 判定（BR-03-114）；无 Cookie 或失效 → 302 到控制台换取页（03 §8.6，接口 A60），不在预览域上显示登录表单。
- **BR-04-95 开关判定**：`TunnelBroker.Open` 在开流前读机器开关：`HTTP_PROXY`、`FILE_READ`、`FS_LIST`、`FS_WATCH`、`MEDIA_PROXY` 需 `allow_remote_preview`；`TERMINAL_SHELL` 需 `allow_remote_terminal`；`FILE_WRITE` 需 `allow_file_edit`；`TERMINAL_WATCH` 不受开关约束（与执行日志同属执行读取）。开关关闭 → `ErrAccessDisabled`（03 映射为 403 130403 `reason_code=MACHINE_ACCESS_DISABLED`）。开关改为关闭时，本模块在同一事务发布 `machine.access_changed`，并在 ≤1 s 内对该机该类全部 OPEN 流发 RST（03 同时把会话置 STOPPED）。
- **BR-04-96 终端沙箱**（v1.1）：`TERMINAL_SHELL` 在机器端以最小环境（03 BR-03-70 白名单，不含机器凭证与访问令牌）启动，初始 cwd = 工作区根；Linux / K8S_POOL 用文件系统沙箱（landlock，不可用时 bubblewrap）把写限制在工作区与临时目录、禁止读取机器程序 profile；macOS / Windows 工作站无可用沙箱时，开流响应带 `sandbox:false`，03 在终端顶部常驻红色说明「这台机器无法限制目录，终端可访问该系统账户能访问的全部文件」。这是 WORKSTATION 默认关闭终端开关的原因。
- **BR-04-97 配额**：每机并发流 ≤ `tunnel_max_streams`（默认 256）；每持有者 `HTTP_PROXY` 流 ≤64、`TERMINAL_WATCH` ≤8、`TERMINAL_SHELL` ≤1、`FILE_*` ≤16；超出 → 429 **140486**（`data.limit`）。带宽：每机出站（机器→服务端）令牌桶 `tunnel_bandwidth_kbps`，单流公平分配；执行控制消息与证据上传（C3 / C4）不计入且优先（流量整形在服务端读取侧做背压，机器端按 `tunnel.window` 信用发送）。每流窗口 256 KiB；连接级窗口 4 MiB。超过带宽不报错，只限速；隧道累计流量计入指标 `fleet_tunnel_bytes_total{machine_type,kind}`。

### 5.11 机器类型（D119）

- **BR-04-98 WORKSTATION**：现有设备码接入（§5.2）不变；`credential_origin=DEVICE_CODE`；开关默认值见 §3.15。
- **BR-04-99 SERVER 无头接入**（MVP）：控制台 A32 `POST /machines {machine_type:"SERVER", name, slot_count?, labels?}`（HUMAN、`machine_manage`、step-up purpose `machine_credential_issue`）在一个事务内：建机器服务账号与 `fleet_machines`（OFFLINE，`credential_origin=PRE_ISSUED`）、签发凭证并**仅在本响应中返回一次**明文 `{client_id, client_secret}`，同时返回无头安装命令（`machine-runner login --server {server} --tenant {tenant} --credential-file <path>` 或环境变量 `MACHINE_RUNNER_CLIENT_ID` / `MACHINE_RUNNER_CLIENT_SECRET`；`machine-runner service install --headless` 安装为 systemd 系统单元 / Windows 服务，以专用低权限系统账户运行）。机器首次开会话时上报的主机信息写入行；`host_fingerprint` 首次写入后，同一凭证从不同指纹开会话 → 409 140430 并审计 `fleet.machine_fingerprint_mismatch`（凭证被复制到另一台主机）。凭证丢失 → 轮换（A13，SERVER 机器的轮换明文同样只在 A13 响应中返回一次，而非经轮询下发，因为丢失凭证的机器无法轮询）。审计 `fleet.machine_created`、`fleet.machine_credential_issued`。
- **BR-04-100 K8S_POOL**（v1.1）：一台 K8S_POOL「机器」= 一个集群内的**池控制器**（同一 `machine-runner`，`--mode pool-controller`，以 A32 预签发的凭证认证，在集群内以 Deployment 运行，经 A33 生成的清单安装）。控制器维持唯一的出站 WebSocket 通道；每个执行（及评审预览）在一个临时 Pod 中运行，Pod 内机器程序以 `--mode pod` 启动，经集群内服务主动连接控制器（不对集群外暴露），控制器把 Pod 的消息与流在同一通道上多路复用（Pod 不持有平台凭证）。槽 = Pod：`ReserveSlot` 时绑定 WARM Pod，没有则创建（§4.6），`NO_FREE_SLOT` 语义不变（BOUND + REQUESTED ≤ `max_pods`）。工作区：Pod 启动后按 07 仓库地址克隆（凭证来自 `git_credentials_secret`），不使用 `fleet_workdirs`（`ResolveWorkspace` 对 K8S_POOL 返回 `{mode:"EPHEMERAL_CLONE"}`）；续接只能 CARRYOVER（`PlanMigration` 对 K8S_POOL 恒给 `CARRYOVER`）。运行时登录凭据（API Key）经 `env_from_secrets` 注入 Pod，名称须在 `env_passthrough_names` 中（BR-04-22 不变）。池伸缩见 J9；工具检测在 Pod 镜像首次就绪时执行一次并按镜像摘要缓存。
- **BR-04-101 类型不可变**：`machine_type` 创建后不可改；WORKSTATION 不能经 A32 预签发；K8S_POOL 的 `slot_count` 只能经 A35 池配置修改（A08 改 → 422 140482）。

### 5.12 Agent 唤醒提示、活动状态与批注辅助脚本的传输（D120）

- **BR-04-102 唤醒提示的传输**：05 的唤醒提示（05 BR-05-122）载荷 `{agent_id, inbox_seq}` 由本模块只做传输：WebSocket 通道上以文本帧 `type:"agent.wake"` 即时推送；长轮询时到达的提示使挂起的 C2 立即返回，在 `ServerBatch.agent_wakes[]` 中携带（同一 Agent 只保留最大 `inbox_seq`）。提示**不进入** `agent_commands[]`：不分配 `command_id`、不要求 ACK、不重发、可合并、可丢失。作为提示流的心跳，**每个** `ServerBatch`（含空批次，≤10 s 一次）与 WebSocket 心跳帧都携带 `agent_inbox_heads[{agent_id, inbox_seq}]`——本机绑定的全部非 RETIRED Agent 的当前收件箱头（数据由 05 `AgentInboxHeads(machine_id)` 提供，每批 ≤64 项，超出按 Agent 轮转）。提示与心跳只发给 Agent 绑定的机器；Agent 换绑后旧机不再收到。
- **BR-04-103 活动状态与会话代次的转交**：`AgentProcState` 增加 `activity?`、`session_epoch?`（语义以 05 §6.6 / §6.z 为唯一定义）；`agent.status` 的 `activity` 变化由机器程序在同一 Agent 2 s 内合并后经 C3 上报，本模块校验会话、`conn_epoch` 与 Agent 绑定本机后原样转交 05；旧 `conn_epoch` 的上报丢弃（140431）。`agent.reset` 作为 `agent_commands[]` 中的普通命令传输（需 ACK、按 §6.4 重发）。
- **BR-04-104 预览网关的批注辅助脚本**（03 BR-03-136）：预览网关对某预览会话仅在 03 标记「批注模式」（`PreviewAccessResolver.Authorize` 的 `Grant.annotate=true`，由控制台查看器经 A60 换取时请求）的访问会话中，向 `Content-Type: text/html` 的 200 响应在 `</head>` 前注入 `<script src="/__annotate.js" defer></script>`；`/__annotate.js` 由网关在预览域同源提供（固定版本、带 `sha256` 完整性校验），不访问 Cookie、不发网络请求，只通过 `postMessage` 向父窗口发送选中元素的选择器、包围盒与可见文本。不修改其他响应、不改写应用的 CSP（应用 CSP 拦截该脚本时由 03 退回 RECT_ONLY）；ARTIFACT 查看器（DOCUMENT / REPORT / PDF）同样适用；分享链接（BR-03-130）访问一律不注入。注入只在预览域发生，控制台源仍不渲染任何工作区内容（00 §10）。
- **BR-04-105 团队工具集的落地**（05 BR-05-144…146、03 BR-03-147）：`execution.start` 携带 `toolset`（载荷语义以 03 §6.3 为唯一定义）时，机器程序在启动运行时之前完成：① **技能**——按 `sha256` 校验每个技能内容后写入**该执行的运行时技能目录**（由运行时适配器决定的、位于运行目录 `{machine_data_dir}/runs/{execution_id}/` 内且只对本执行可见的位置；不写入仓库工作区、不修改用户全局技能目录），目录内放标记文件 `.catalog.json{entry_key, version_no, sha256}`；② **目录 MCP**——生成该执行的运行时 MCP 配置文件（同在运行目录内）：STREAMABLE_HTTP 条目指向机器本地代理 `http://127.0.0.1:<port>/r/<execution 随机段>/<entry_key>`，STDIO_PACKAGE 条目按 `package.integrity` 从包缓存（按完整性哈希内容寻址）安装后以配置中的 `command` / `args` 启动；白名单内的本机自带 MCP 照旧追加；③ 任一技能校验失败或包安装失败 → `execution.rejected{reason:"TOOLSET_UNAVAILABLE", detail}`（不带部分工具集启动）。执行结束（任一终态）后随运行目录清理一并删除技能与配置。
- **BR-04-106 本地代理与工具令牌**：机器程序在回环地址上运行一个 MCP 本地代理，按执行的随机路径段把请求转发到 05 中继 T13，并在上游请求上附加该执行该条目的工具令牌（`execution.start.toolset.tool_tokens[]`，由 `leases.renew` 响应续签，03 §6.3）；令牌只保存在机器程序内存中。代理拒绝：非回环来源、未知或已结束执行的路径段、超过 05 BR-05-147 的请求体上限。执行进入终态、被栅栏或机器会话的 `conn_epoch` 变化后立即作废该执行的路由与令牌。能力检测（BR-04-41）结果 S3 的技能项增加 `sha256?`（SKILL.md 内容的 SHA-256），供 05 判定「团队目录 v{n}」/「与团队目录不一致」/「本机自带」（05 BR-05-148）；运行目录中的目录技能不在检测范围内。
- **BR-04-107 凭据不落机器**（PRD §10.2、BR-04-22 的延伸）：团队工具目录的 MCP 凭据从不下发机器（05 BR-05-146）；工具令牌、机器凭证与 Agent 凭证**不得**出现在运行时进程的环境变量、命令行参数、MCP 配置文件、技能文件、执行事件与日志中——运行时只看到本地代理地址；机器程序日志中令牌一律以 `tok_…<后 4 位>` 形式脱敏。违反此条的机器程序版本不得发布（发布检查：在测试夹具中扫描运行目录与子进程环境，出现令牌前缀即失败）。

---

## 6. 接口契约

### 6.1 约定

- 响应信封 `respx`；ID 十进制字符串；时间 RFC 3339 UTC。
- 守卫记号：**T** = `authz.Register`（租户级，路径自动加 `/tenants/{tenant_id}`）；**O** = `authz.RegisterOwned`（全局短地址，按资源反查租户；与 `/tenants/{tenant_id}/…` 作用域地址为同一操作，归属不符 404）；**E** = `authz.RegisterEntity`（项目实体）；**P** = `authz.RegisterPublic` + `authn.RateLimitMetadata(RateLimitOAuth)`（共用 OAuth 限流额度）；**R** = `authz.RegisterTenantMember` + 本模块机器守卫 `requireMachine`（主体须为 `service_account` 且等于某非 REVOKED 机器的 `principal_id`，否则 401 140401；再校验会话/代次头）。
- step-up：请求带 `Cookie` + `Origin`（CSRF）+ body `reauth_proof`，底座 `ReauthenticationConsumer` 在业务事务内消费；因此 PAT/服务账号在结构上无法吊销机器或显示网关密钥。
- OpenAPI tag = **最终发布路径（含挂载前缀）首段**，由底座设置（00 §9）。表中 tag 列即该值：T 守卫挂载在 `/tenants/{tenant_id}` 下故为 `tenants`；O 守卫为短地址首段。
- **v** = 需 `If-Match`（GET 以 `ETag` 返回）；**✔** = 接受 `Idempotency-Key`。
- 列表：`pkg/query`（`field=op:value`、offset 分页、`capabilities`），字段见 §6.6。

### 6.2 控制台接口

| # | 方法 · 路径 | tag | 权限 · 守卫 | 请求 | 响应 `data` | 错误 | 幂等/锁 |
|---|---|---|---|---|---|---|---|
| A01 | GET `/machine-enrollment-settings` | tenants | `machine_manage` · T | — | `{server_url, tenant_id, client_id, code_ttl_seconds:300, poll_interval_seconds:5, runner:{package, version, min_version, command}, install:{windows:{install, login, start}, macos:{…}, linux:{…, npx}}, prerequisites[]}` | 409 140415 | 惰性建客户端（幂等） |
| A02 | GET `/machine-enrollments?user_code=` | tenants | `machine_manage` · T；HUMAN | `user_code` 1–64（容错同底座 `normalizeUserCode`） | `Enrollment` | 404 140410；429 140413；403 140403 | — |
| A03 | GET `/machine-enrollments/{enrollment_id}` | machine-enrollments | `machine_manage` · O | — | `Enrollment` | 404 140410 | — |
| A04 | POST `/machine-enrollments/{enrollment_id}/confirmation` | machine-enrollments | `machine_manage` · O；HUMAN | `{name 1–100, replace_machine_id? \| as_new_machine?:true}` | `{enrollment, machine}`（201） | 409 140411/140412；422 140414/140416/140481；403 140403 | ✔ |
| A05 | POST `/machine-enrollments/{enrollment_id}/denial` | machine-enrollments | `machine_manage` · O；HUMAN | `{reason? ≤2000}` | `Enrollment` | 409 140411 | ✔ |
| A06 | GET `/machines` | tenants | `machine_read` · T | §6.6 | `Machine[]` + `meta.page` | — | — |
| A07 | GET `/machines/{machine_id}` | machines | `machine_read` · O | — | `Machine`；`ETag` | 404 140404 | — |
| A08 | PATCH `/machines/{machine_id}` | machines | `machine_manage` · O | `{name?, slot_count?, labels?, env_passthrough_names?}` | `Machine` | 409 140421；422 140416/140480/140400 | v |
| A09 | PUT `/machines/{machine_id}/draining` | machines | `machine_manage` · O | `{mode:"STAY"\|"DISCONNECT", reason? ≤2000}` | `Machine` | 409 140421 | v；重复幂等 |
| A10 | DELETE `/machines/{machine_id}/draining` | machines | `machine_manage` · O | — | `Machine` | 409 140421 | v；幂等 |
| A11 | POST `/machines/{machine_id}/revocation` | machines | `machine_revoke`（不可委托）· O；HUMAN；step-up `machine_revoke` | `{confirm_name, reason 1–2000, reauth_proof}` | `{machine, interrupted_execution_ids[], stopped_agent_ids[]}` | 403 140403；422 140425；409 140421 | ✔（重放返回首次结果） |
| A12 | GET `/machines/{machine_id}/credentials` | machines | `machine_manage` · O | — | `MachineCredential[]`（无秘密） | 404 | — |
| A13 | POST `/machines/{machine_id}/credential-rotations` | machines | `machine_manage` · O；HUMAN | `{reason? ≤2000}` | `{credential(PENDING_DELIVERY), retiring:{id, retire_at}}`（201） | 409 140421/140460 | ✔ |
| A14 | POST `/machines/{machine_id}/runtime-detections` | machines | `machine_manage` · O | — | `{requested_at}`（202） | 409 140423/140421 | ✔；30 s 内合并 |
| A15 | GET `/machines/{machine_id}/model-catalogs` | machines | `machine_read` · O | — | `ModelCatalog[]` | 404 | — |
| A16 | POST `/machines/{machine_id}/model-catalog-refreshes` | machines | `machine_read` · O | `{runtime_kind}` | `{requested_at}`（202） | 409 140423；422 140441 | ✔；合并 |
| A17 | PUT `/machines/{machine_id}/model-catalogs/{runtime_kind}/manual-models` | machines | `machine_manage` · O | `{model_ids: string[] ≤50, families?: {model_id: ModelFamily}}`（BR-04-49） | `ModelCatalog` | 422 140400/140441/140448 | v（目录 `version`） |
| A18 | GET `/machines/{machine_id}/capability-inspections` | machines | `machine_read` · O | `?runtime_kind=&workdir_id=` | `CapabilityInspection`（无则 `state:null`） | 404 | — |
| A19 | POST `/machines/{machine_id}/capability-inspections` | machines | `machine_read` · O | `{runtime_kind, workdir_id?}` | `CapabilityInspection`（202，QUEUED 或合并后的现行） | 409 140423；422 140441；404 140446 | ✔ |
| A20 | GET `/machines/{machine_id}/workdirs` | machines | `machine_read` · O | `?project_id=&status=` | `Workdir[]`（按调用者可读项目过滤） | — | — |
| A21 | POST `/machines/{machine_id}/workdirs` | machines | `machine_manage` · O；另须对该项目有 `project_manage` | `{project_id, repository_id, path}` | `Workdir`（201） | 422 140442/140400；409 140444/140445/140421 | ✔ |
| A22 | PATCH `/workdirs/{workdir_id}` | workdirs | `machine_manage` · O | `{path?, archived?:bool}` | `Workdir` | 409 140444/140443 | v |
| A23 | POST `/workdirs/{workdir_id}/probes` | workdirs | `machine_read` · O | — | `Workdir`（202） | 409 140423 | 合并 |
| A24 | GET `/entities/{entity_id}/workdirs` | tenants | `project_read` · E | `?repository_id=&machine_id=` | `Workdir[]` | — | — |
| A25 | GET `/model-prices`；PUT / DELETE `/model-prices/{runtime_kind}/{model_id}` | tenants | 读 `machine_read`、写 `machine_manage` · T | PUT `{input_micros_per_mtok, output_…, cache_read_…, cache_write_…}`（≥0） | `ModelPrice[]` / `ModelPrice` / 204 | 422 140400 | v（更新）；DELETE 幂等 |
| A26 | GET `/gateway-config` | tenants | `gateway_manage` · T | — | `GatewayConfig{enabled, machine?:{id,name,online}, default_model, base_url, key_masked, revision, online, limits:{concurrency:8, contexts:8, timeout_s:180, request_bytes:1048576, output_bytes:921600, log:100}, requests: GatewayRequest[≤100], experimental:true, version}` | — | ETag |
| A27 | PUT `/gateway-config` | tenants | `gateway_manage` · T | `{enabled, machine_id?, default_model}` | `GatewayConfig` | 409 140471/140409；422 140476/140400 | v |
| A28 | POST `/gateway-config/key-reveals` | tenants | `gateway_manage` · T；HUMAN；step-up `gateway_key_reveal` | `{reauth_proof}` | `{key}`（`no-store`） | 403 140403（REAUTH_REQUIRED） | — |
| A30 | GET `/machines/{machine_id}/tools` | machines | `machine_read` · O | — | `{tools:S4（无 `machine_manage` 时省略 `path`）, capabilities: string[], detected_at?}` | 404 140404 | — |
| A31 | GET `/tool-availability` | tenants | `machine_read` 或 `project_read` · T | `?tool_key=`（可多值）`&capability=` | `[{tool_key \| capability, online_machines, versions[], can_screenshot_machines?}]`（只计 ONLINE / DRAINING 机器） | 422 140447 | — |
| A29 | POST `/gateway-config/key-rotations` | tenants | `gateway_manage` · T；HUMAN | `{}` | `{key, revision, cancelled_request_count}`（201） | — | ✔ |
| A30 | POST `/gateway-config/test-responses` | tenants | `gateway_manage` · T | `{prompt 1–64000}` | SSE 透传 `/v1/responses` 流（服务端以当前密钥代发，浏览器不接触密钥） | 503 140470/140471 | — |
| A31 | DELETE `/gateway-requests/{request_id}` | gateway-requests | `gateway_manage` · O | — | `GatewayRequest`（CANCELLED「操作员取消」；已终态返回现状） | 404 | 幂等 |
| A32 | POST `/machines` | tenants | `machine_manage` · T；HUMAN；step-up `machine_credential_issue` | `{machine_type:"SERVER"\|"K8S_POOL", name 1–100, slot_count?, labels?, reauth_proof}`（D119；K8S_POOL 为 v1.1） | `{machine, credential:{client_id, client_secret}, install:{linux:{login, service}, windows:{login, service}, kubernetes?:{manifest_url}}}`（201，`no-store`；明文只此一次） | 403 140403（REAUTH_REQUIRED / HUMAN_ONLY）；422 140416/140481/140482 | ✔ |
| A33 | GET `/machines/{machine_id}/pool-manifest` | machines | `machine_manage` · O | — | `text/yaml`：Namespace、ServiceAccount、Role（只允许在 `namespace` 内增删 Pod）、控制器 Deployment；凭证以 Secret 引用占位，不含明文 | 404；422 140482（非 K8S_POOL） | — |
| A34 | GET `/machines/{machine_id}/remote-access` | machines | `machine_read` · O | — | `MachineRemoteAccess`；`ETag`（机器 `version`） | 404 | — |
| A35 | PUT `/machines/{machine_id}/remote-access` | machines | `machine_access_manage`（不可委托）· O；HUMAN；把 `allow_remote_terminal` 由 false 改为 true 时另需 step-up `machine_terminal_enable` | `{allow_remote_preview, allow_remote_terminal, allow_file_edit, tunnel_bandwidth_kbps, tunnel_max_streams, reason ≤2000, reauth_proof?}` | `MachineRemoteAccess` | 403 140403（NON_DELEGABLE / REAUTH_REQUIRED）；409 140421；422 140400 | v |
| A36 | GET / PUT `/machines/{machine_id}/pool-config` | machines | 读 `machine_read`、写 `machine_manage` · O | PUT `{namespace, pod_template, min_warm, max_pods, scale_down_idle_s, pod_start_timeout_s}`（v1.1） | `PoolConfig` | 422 140482/140488 | v（PUT） |
| A37 | GET `/machines/{machine_id}/pool-pods` | machines | `machine_read` · O | `?state=`（v1.1） | `PoolPod[]` | 404 | — |
| A38 | GET `/machines/{machine_id}/tunnel-streams` | machines | `machine_manage` · O | — | `{active:[{stream_id, kind, holder_kind, holder_id, opened_at, bytes_in, bytes_out}], totals:{streams, kbps_1m}}`（运维视图；不含路径与内容） | 404 | — |
| A39 | GET `/preview-gateway` | tenants | `machine_read` 或 `execution_read` · T | — | `{enabled, mode:"SUBDOMAIN"\|"PATH", domain, tls:{wildcard, expires_at?}, path_mode_warning:bool}`（只读；配置来自部署） | — | — |


### 6.3 机器端公开接口（接入前，P）

| # | 方法 · 路径 | 请求 | 响应 | 错误 |
|---|---|---|---|---|
| P1 | POST `/oauth/device_authorization`（底座，form） | `client_id`、`scope=fleet.machine` | `device_code, user_code, verification_uri, verification_uri_complete, expires_in:300, interval:5` | 底座 `invalid_client` / `invalid_scope` |
| P2 | POST `/runner-enrollments` | `{client_id, device_code, host:{hostname ≤255, os, os_version, arch, host_fingerprint, profile_name}, runner_version, protocol_version:1, runtimes:S1, suggested_name?}` | `{enrollment_id, tenant_id, expires_at, server_time, console_verification_uri, console_verification_uri_complete}`（201） | 400 140410（无效/过期/非机器客户端，不可区分）；409 140417；426 140436；429 |
| P3 | POST `/runner-enrollments/{enrollment_id}/credential-collections` | `{device_code}` | 200 `{machine_id, principal_id, tenant_id, client_id, client_secret, token_endpoint, channel_base:"/tenants/{t}/runner"}`；或 400 `{error:"authorization_pending"\|"slow_down"\|"access_denied"\|"expired_token"}`（RFC 8628 形态） | 400 140410（已消费/不匹配） |
| P4 | DELETE `/runner-enrollments/{enrollment_id}` | `{device_code}` | 204 | 400 140410；409 140411 |

### 6.4 机器通道（R，前缀 `/tenants/{tenant_id}/runner`，OpenAPI tag `tenants`——tag 取最终发布路径首段，00 §9）

**请求头**：`Authorization: Bearer <访问令牌>`；`X-Runner-Protocol: 1`；`X-Runner-Incarnation`；C1 之外必带 `X-Runner-Session`、`X-Runner-Epoch`。**通用错误**：401 140401；409 140431（代次过期 → 重开会话）；409 140421；413 140435；426 140436；429（`Retry-After`）。

**消息信封**（03 §6.3 所指「04 定义」）：`{v:1, message_id(机器生成 128 bit 随机 base32), machine_id, conn_epoch, type, sent_at, payload}`；服务端按 `(machine_id, message_id)` 在 10 分钟窗口内去重（重放返回首次结果）。执行级 `type`/`payload` 由 03 §6.3 定义；本模块做信封校验、认证、连接栅栏并转交 `ExecutionDispatchHandler`。

| # | 方法 · 路径 | 请求 | 响应 | 说明 |
|---|---|---|---|---|
| C1 | POST `/sessions` | `{incarnation_id, runner_version, protocol_version, host:{hostname, os_version, arch}, runtimes:S1, slots:[SlotState], transports:["LONG_POLL","SHORT_POLL"(,"WEBSOCKET")]}` | `{session_id, conn_epoch, machine:{id, name, state, slot_count, drain_requested, env_passthrough_names}, lease_ms:30000, poll:{wait_ms:10000, short_interval_ms:350}, self_stop_ms:20000, lease_renew_ms:10000, server_time, transport}`（201） | M2/M3/M12；409 140430；运行时快照即时入库 |
| C2 | POST `/polls` | `{wait_ms 0–10000, slots:[SlotState], agents:[AgentProcState], outbox_depth, gateway_active:[request_id]}` | `ServerBatch` | 到达即更新 `last_seen_at`；有内容立即返回，否则保持至 `wait_ms`；吊销/代次变化 ≤1 s 唤醒 |
| C3 | POST `/messages` | `{messages:[Envelope] ≤200, ≤1 MiB}` | `{results:[{message_id, status, code?, data?}]}` | 03 的 M→S 消息与本模块消息（下表）**按序处理**；`execution.events` 缺口 → 该项 `status:409, code:130428, data:{expected_seq}`，同一派发后续项不处理（03 BR-03-21）；`leases.renew` 每 ≤10 s 一批 |
| C4 | PUT `/blobs/{upload_id}` | 流式体 + 头 `X-Blob-Meta`（03 `blob.upload` 元数据，base64url JSON） | `{file_id, sha256}` | ≤100 MiB；转交 03 `UploadBlob` |
| C5 | DELETE `/sessions/current` | — | 204 | M5；机器正常退出时调用（3 s 超时，沿用 Preview） |
| C6 | POST `/gateway-exchanges` | `{results:[{request_id, output?≤900 KB, error?, code?}], active:[request_id]}` | `{tasks:[{request_id, model, body, deadline_at}], cancels:[request_id], reset:bool, revision}` | 实验网关；`reset=true` → 机器回收全部网关上下文 |

**`ServerBatch`（C2 响应）**：

| 字段 | 内容 |
|---|---|
| `commands[]` | 03 `PendingCommands(machine, conn_epoch, max=8)` 的 S→M 信封（`execution.start/cancel`、`interaction.response`、`evidence.collect`、`verification.start/cancel`、`merge.start`），出队顺序经 BR-04-55；未 ACK 每 10 s 可重发，≤6 次（03 §3.8） |
| `agent_commands[]` | 05 `PendingAgentCommands(machine, conn_epoch, max=8)` 的 S→M 信封 `{type, payload}`。**命令集合与载荷以 05 §6.6 为唯一定义**（含 provision、start、stop、cleanup、lead_plan、review、triage、release_notes、workspace_list、workspace_read，D120 增 reset；`inbox` 自 D120 废止，由下方 `agent_wakes[]` 取代），本模块不复述、不校验载荷语义，只做信封、认证、连接栅栏与投递；未 ACK 每 10 s 重发，≤6 次，之后由 05 判定命令失败 |
| `agent_wakes[]`（D120） | 05 唤醒提示 `{agent_id, inbox_seq}`（BR-04-102；不带 `command_id`、不需 ACK、不重发，同一 Agent 只保留最大值） |
| `agent_inbox_heads[]`（D120） | 本机绑定 Agent 的收件箱头 `{agent_id, inbox_seq}`，每批都带（提示流心跳，BR-04-102） |
| `cancels[]` | BR-04-36 的未知持有者取消 `{holder_kind, holder_id, dispatch_epoch?}` |
| `inspections[]` | `{inspection_id, request_id, runtime_kind, cwd, timeout_ms:55000}`（≤1 项） |
| `model_catalog_requests[]` | `{runtime_kind}` |
| `runtime_detection` | bool |
| `workdir_probes[]` | `{workdir_id, path, timeout_ms:10000}` |
| `credential_rotation` | `{rotation_id, client_id, client_secret, retire_old_at}`（BR-04-23）或省略 |
| `machine` | `{state, drain_requested, slot_count, env_passthrough_names, config_revision}` |
| `stop` | `{reason:"DRAINED"\|"SERVER_SHUTDOWN"}` 或省略 |
| `gateway` | `{revision, enabled_for_this_machine}` |
| `next_poll_ms` | 0 或短轮询 350 |

**本模块的 M→S 消息**（经 C3）：

| type | payload | 处理 |
|---|---|---|
| `heartbeat` | `{slots:[SlotState]}`（WebSocket 模式每 10 s） | 更新 `last_seen_at`；BR-04-36 |
| `runtimes.report` | `{runtimes:S1, tools?:S4, detected_at, errors[]}` | BR-04-40 / 47；运行时或工具（含能力标记）变化发 `machine.runtimes_changed`；旧版机器程序不带 `tools` → 工具保持原值 |
| `model_catalog.report` | `{runtime_kind, entries:S2, errors[] ≤10, checked_at}` | BR-04-42；本机无此运行时 → 422 140441 |
| `inspection.result` | `{inspection_id, request_id, skills?:S3, mcp?:S3, error?}` | §4.4；不匹配 → 409 140450 |
| `workdir_probe.result` | `{workdir_id, exists, is_dir, writable, git?:{is_repo, remote_url?, head?, branch?}, error?}` | BR-04-50 |
| `credential.received` | `{rotation_id}` | §4.3 → ACTIVE |
| `agent.*`（05 §6.6 的全部 M→S 类型：`agent.status`、`agent.command_ack`、`agent.review_ack`、`agent.lead_plan_result`、`agent.workspace_result`） | **载荷以 05 §6.6 为唯一定义**，本模块不复述 | 校验会话、`conn_epoch` 与 Agent 绑定本机后原样转交 05 `AgentChannelHandler`；旧 `conn_epoch` → 409 140431 不转交；`command_ack` / `review_ack` / `workspace_result` 指向本机未知或已过期的 `command_id` / `request_id` → 该项 409 140452，同批其他项照常；`agent.status` 状态变化即发，另随每次轮询的 `agents[]` 快照上报 |

`SlotState` = `{slot_no, state:"IDLE"|"STARTING"|"RUNNING"|"STOPPING", holder_kind?, holder_id?, dispatch_epoch?, pid?, started_at?}`。

`AgentProcState` = 05 §6.6 `agent.status` 载荷去掉 `at`（`status` 取 05 定义的唯一枚举 `STARTING`/`RUNNING`/`STOPPING`/`EXITED`；D120 起含 `activity?`、`session_epoch?`，BR-04-103）。Agent 常驻进程**不占执行槽**（其触发的执行另按 03 占槽）。快照节奏 = 轮询节奏（两次轮询间隔 ≤10 s）；05 以「机器 ONLINE 且连续 3 次快照缺失，或报告 `EXITED` 且 `exit_code≠0`」判定崩溃（≈30 s），机器 OFFLINE 时不判崩溃（05 BR-05-11）。

**机器端行为**（移植 Preview `daemon.go`，数值见 BR-04-30）：长轮询循环；20 s 无成功响应 → 停止全部执行（cancel → 4 s → kill tree）、保留 outbox、继续重连；收到 140431 → 重开会话并按 03 规则补交未 ACK 事件；收到 401 → 重换令牌一次，仍 401 → BR-04-26；某执行 outbox 达 10,000 条 → `execution.lost{OUTBOX_FULL}`（03）。

**WebSocket 通道**（原 v1.1，D119 起 MVP；BR-04-90）：C1 响应可协商 `transport=WEBSOCKET`；机器 `GET /tenants/{t}/runner/channel`（`Upgrade: websocket`，子协议 `runner.v1`，同样的 Bearer 与会话/代次头）。帧 = 同一信封：S→M 推送 `ServerBatch` 各项，M→S 发送 C3 消息与 `heartbeat`（每 10 s）；服务端 ping 每 20 s，连续 2 次无 pong 关闭。代次、30 s 租约、20 s 自停、去重语义不变；10 分钟内升级失败或被中间设备中断 3 次 → 回退 LONG_POLL 并记录到会话 `transport`。blob 仍走 C4。

#### 6.4b 隧道流（D119，只在 WebSocket 通道上；MVP，K8S_POOL 帧为 v1.1）

控制帧是**文本帧**，内容为本节信封（`type` 如下）；数据帧是**二进制帧**：`stream_id`（u32，大端）· `flags`（u8：0x1 DATA、0x2 FIN、0x4 RST）· 保留 3 字节 · 负载（≤64 KiB）。同一流的数据帧有序；不同流之间不保证顺序。

| 方向 | type | payload | 处理 |
|---|---|---|---|
| S→M | `tunnel.open` | `{stream_id, kind, holder:{kind:"EXECUTION"\|"PREVIEW", id, dispatch_epoch?}, target, limits:{window_bytes:262144, idle_ms}}`；`kind` ∈ `HTTP_PROXY`（target `{port}`）、`FILE_READ`（`{path, offset?, length?, mask[]}`）、`FS_LIST`（`{path, depth≤2, mask[]}`）、`FS_WATCH`（`{paths[] ≤20, debounce_ms:500}`）、`MEDIA_PROXY`（`{path, profile:"480p"}`，v1.1）、`FILE_WRITE`（`{path, base_sha256?, mode:"REPLACE"\|"CREATE"}`，v1.1）、`TERMINAL_WATCH`（`{}`）、`TERMINAL_SHELL`（`{shell?, cols, rows}`，v1.1）；`target` 的业务语义由 03 定义 | 机器按 BR-04-91 / 92 / 95 / 96 校验后回 `tunnel.opened` 或 `tunnel.rejected` |
| M→S | `tunnel.opened` | `{stream_id, info?:{size?, sha256?, mime?, sandbox?:bool}}` | OPENING → OPEN |
| M→S | `tunnel.rejected` | `{stream_id, code:140485\|140491\|140484\|140486\|140423, detail ≤500}` | → RESET；`code` 原样交 03 映射 |
| 双向 | `tunnel.window` | `{stream_id, credit_bytes}` | 流控信用；累计未确认 > 窗口 → 发送方暂停 |
| 双向 | `tunnel.resize` | `{stream_id, cols, rows}` | 仅 `TERMINAL_SHELL` |
| M→S | `tunnel.event` | `{stream_id, event:"FS_CHANGED", paths[] ≤200, truncated:bool}` | 仅 `FS_WATCH`；03 据此推送刷新（BR-03-118） |
| M→S | `ports.report` | `{holder:{kind, id}, ports:[{port, pid, process_name}]}` | 持有者进程树内的回环监听端口快照（变化即发，最多每 5 s 一次）；03 用于 Agent 提供端口（BR-03-113）；不在树内的端口**从不上报** |
| S→M | `pool.pod_create` / `pool.pod_delete` | `{pod_id, pod_name, holder?}`（v1.1，仅 K8S_POOL） | §4.6 |
| M→S | `pool.pod_state` | `{pod_id, state:"SCHEDULING"\|"READY"\|"FAILED"\|"GONE", failure?}`（v1.1） | §4.6；未知 `pod_id` → 控制器须删除该 Pod |

机器端每持有者结束（执行终态 / INTERRUPTED、评审预览停止）时对其全部流发 RST；连接代次变化时双方丢弃全部流（不跨连接续传，客户端重开）。


### 6.5 响应模型

- `Enrollment`：`{id, status, expires_at, server_time, remaining_seconds, host:{hostname, os, os_version, arch, profile_name}, runner_version, runtimes:S1, suggested_name, duplicate_of?:{machine_id, name, state, last_seen_at}, decided_by?, decided_at?, machine_id?, credential_collected:bool, machine_state?}`。**从不返回** `device_code`、`user_code` 及其哈希。
- `Machine`：`{id, name, state, drain:{requested, mode?, requested_at?, drained_at?}, host:{hostname, os, os_version, arch, profile_name}, runner_version, upgrade_available, slot_count, slots:[{slot_no, state, holder_kind?, holder_id?, execution?:{id, title, project_id}（不可读项目显示「其他项目的执行」）, reserved_at?}], runtimes:[{kind, version, path, status, detected_at, missing_since?}], labels, env_passthrough_names, last_seen_at, last_address, online_since, offline_since, conn_epoch, current_session?:{id, transport, opened_at}, agent_count, credential_summary:{active_since, rotation?:{state, retire_at}}, enrolled:{at, by}, revoked?:{at, by, reason}, version}`。
- `MachineCredential`：`{id, state, origin, issued_at?, delivered_at?, retire_at?, revoked_at?, revoke_reason?, last_used_at?, delivery_attempts}`。
- `ModelCatalog` / `CapabilityInspection` / `Workdir` / `ModelPrice` / `GatewayRequest{id, created_at, model, state, error?, duration_ms?}`：字段同 §3 对应表，内部锁键与密文不返回。
- `MachineRemoteAccess`（D119）：`{machine_id, machine_type, allow_remote_preview, allow_remote_terminal, allow_file_edit, tunnel_bandwidth_kbps, tunnel_max_streams, websocket_available:bool, sandbox_available:bool, access_version, version}`。
- `PoolConfig` / `PoolPod`（v1.1）：字段同 §3.16 / §3.17；`pod_template.env_from_secrets` 只返回 Secret 名称。
- `Machine` 增加 `machine_type`、`credential_origin`、`remote_access:{preview, terminal, file_edit}`（D119）。

### 6.6 列表查询字段

| 列表 | 可过滤 | 可排序 | 默认 |
|---|---|---|---|
| A06 机器 | `state`(eq,in)、`runtime_kind`(eq,in)、`capability`(eq,in；BR-04-48，未知值 422 140447)、`labels`(contains)、`has_free_slot`(eq)、`drain_requested`(eq)、`os`(eq,in)、`name`/`hostname`(like)、`last_seen_at`(gte,lte)、`include_revoked`(eq) | `name`、`state`、`last_seen_at`、`created_at`、`free_slots` | `include_revoked=false`，`sort=name` |
| A20/A24 工作目录 | `project_id`、`repository_id`、`machine_id`(eq)、`status`(eq,in)、`archived`(eq) | `created_at`、`path` | `archived=false` |

### 6.7 跨模块接口（`internal/fleet` 根包冻结）

| 接口 | 方法 | 使用者 |
|---|---|---|
| `MachineDirectory`（00 §8） | `Get`；`ListEligibleMachines(tenant, {runtime, model?, project_id?, repository_id?, agent_machine_id?, require_workdir})`；`ResolveWorkspace(machine, project, repository?)`；`ReserveSlot(tx, …)` / `ReleaseSlot(tx, …)`；`OrderPending(machine, candidates)`；`ModelCatalog(machine, runtime)`；`ModelPrice(tenant, runtime, model)`；`EnvPassthroughNames(machine)`；`PlanMigration(execution_ref)`；`ConnEpoch(machine)`；`Tools(machine) → S4`；`HasCapability(machine, capability) bool`；`ToolAvailability(tenant, tool_key \| capability) → {online_machine_ids[], versions[]}`（11 BR-11-30）；`ModelFamily(tenant, machine, runtime, model) (ModelFamily, error)`（BR-04-49；05 `ModelFamilyOf`、03 执行快照）；`ListEligibleMachines` 另接受 `capabilities?[]`（03 VISUAL / 截图放置、05 ACCEPTOR 候选）；`Summary(tenant) → {total, online, offline, draining, slots_total, slots_busy, runtimes_missing}`（08 总览「机器状态」磁贴） | 03、05、08 |
| `MachineChannelNotifier` | `Wake(machine_id)`（新执行命令或 Agent 命令入队后唤醒长轮询；跨副本经 09 服务端内部订阅） | 03、05 |
| `TunnelBroker`（D119） | `Open(ctx, OpenRequest{tenant, machine_id, holder{kind, id, dispatch_epoch?}, kind, target, client_ip?}) (Stream, error)`：读开关与配额、选副本（流须在持有该机通道的副本上打开，跨副本经 09 内部订阅转发字节）、发 `tunnel.open` 并等待 ≤10 s；`Stream` 为 `io.ReadWriteCloser` + `Info()` + `Reset(code)`；错误 `ErrAccessDisabled` / `ErrNoWebSocket` / `ErrQuota` / `ErrRejected{code}` / `ErrMachineOffline`；`ResetHolder(ctx, holder, reason)` 关闭某持有者的全部流；`Ports(ctx, holder) []PortInfo`（最近一次 `ports.report`） | 03 |
| `MachineDirectory` 增补（D119） | `Type(machine) MachineType`；`RemoteAccess(machine) {preview, terminal, file_edit, websocket, sandbox}`；`ReserveSlot` 接受 `holder_kind=PREVIEW`（评审预览占槽，K8S_POOL 为其创建 Pod） | 03 |

> 执行槽经 `MachineDirectory.ReserveSlot/ReleaseSlot` 占用与释放（00 §8 不另设 `SlotReserver`）。

**本模块使用**：03 `ExecutionDispatchHandler`（CR-03-05）；03 `PreviewAccessResolver`（D119，预览网关的访问判定，03 §6.4）；05 `AgentChannelHandler` / `PendingAgentCommands`（机器通道上的 Agent 命令与状态，05 §6.6）；07 `RepositoryReader.Get(ctx, tenant, repository_id) → {project_id, url, archived}`（07 §6.3）；05 `AgentDirectory`；08 `AuthorityChecker`；底座 oauth `DeviceGrants`（新增，CR-04-06）、identity `ServiceAccountProvisioner`（新增事务内版本，CR-04-08）、authn `ReauthenticationConsumer`、`audit.Appender`、`dlock`、`guid`；09 `JobQueue`、`OutboxPublisher`、`RealtimeBroadcaster`、`Notifier`、`SecretSealer`（CR-04-07）。

### 6.8 错误码（04 子段 140000–141999，00 §9；i18n 命名空间 `fleet`，key 形如 `fleet.error.<名>`，三语齐全）

| 码 | HTTP | 名 | 含义 |
|---|---|---|---|
| 140400 | 422/400 | invalid_request | 字段校验失败（`data.fields`） |
| 140401 | 401 | runner_unauthenticated | 机器令牌无效、凭证停用或机器已吊销 |
| 140403 | 403 | forbidden | 一切权限拒绝：缺权限 / 需人类主体 / 不可委托 / step-up 缺失（`data.reason_code` ∈ MISSING_PERMISSION、HUMAN_ONLY、NON_DELEGABLE、REAUTH_REQUIRED；`data.missing_permission`） |
| 140404 | 404 | machine_not_found | |
| 140409 | 409 | version_conflict | `If-Match` 不符 |
| 140410 | 404/400 | enrollment_not_found | 接入码/设备码无效、过期、已用（不可区分） |
| 140411 | 409 | enrollment_not_pending | 已被决定（`data.status`） |
| 140412 | 409 | enrollment_expired | 确认时已过期 |
| 140413 | 429 | enrollment_lookup_throttled | 查找失败过多 |
| 140414 | 422 | duplicate_host_choice_required | 须选择替换或新建 |
| 140415 | 409 | enrollment_client_disabled | 机器接入客户端已停用 |
| 140416 | 422 | machine_name_taken | |
| 140417 | 409 | enrollment_already_attached | |
| 140421 | 409 | machine_revoked | |
| 140423 | 409 | machine_offline | |
| 140424 | — | （废弃，合并入 140403） | step-up 缺失或无效现返回 403 **140403**，`data.reason_code=REAUTH_REQUIRED`（00 §9：权限拒绝统一 `xx0403` + `reason_code`） |
| 140425 | 422 | name_confirmation_mismatch | |
| 140428 | 428 | precondition_required | 缺 `If-Match` |
| 140430 | 409 | runner_session_active | |
| 140431 | 409 | runner_epoch_fenced | |
| 140435 | 413 | payload_too_large | |
| 140436 | 426 | runner_upgrade_required | |
| 140441 | 422 | runtime_unavailable | |
| 140442 | 422 | repository_invalid | |
| 140443 | 409 | workdir_busy | 目录被写执行持有，暂不可改路径 |
| 140444 | 409 | workdir_path_conflict | |
| 140445 | 409 | workdir_repository_conflict | |
| 140446 | 404 | workdir_not_found | |
| 140447 | 422 | tool_key_unknown | `tool_key` / `capability` 不在 S4 / BR-04-48 值域 |
| 140448 | 422 | model_family_invalid | `families` 值不在 `ModelFamily` 值域，或键不在目录 ∪ 手填 |
| 140450 | 409 | inspection_stale | |
| 140452 | 409 | agent_command_unknown | `agent.command_ack` / `agent.review_ack` / `agent.workspace_result` 指向本机未知或已过期的命令 / 请求 |
| 140460 | 409 | credential_rotation_in_progress | |
| 140470 | 503 | gateway_disabled | |
| 140471 | 503/409 | gateway_machine_offline | |
| 140472 | 401 | gateway_key_invalid | |
| 140473 | 429 | gateway_concurrency_limit | |
| 140474 | 400 | gateway_unsupported_field | |
| 140475 | 400 | gateway_model_not_allowed | |
| 140476 | 422 | gateway_codex_unavailable | |
| 140480 | 422 | slot_count_invalid | |
| 140481 | 422 | machine_limit | |
| 140482 | 422 | machine_type_invalid | 类型不可改、WORKSTATION 不能预签发、操作不适用于该类型（D119） |
| 140484 | 409 | tunnel_unavailable | 机器未使用 WebSocket 通道，远程预览 / 文件 / 终端不可用（BR-04-90） |
| 140485 | 403 | port_not_owned | 端口不属于该持有者的进程树或不在回环地址（BR-04-91；03 映射为 130403 `PORT_NOT_OWNED`） |
| 140486 | 429 | tunnel_quota_exceeded | 并发流超出配额（`data.limit`，BR-04-97） |
| 140488 | 422 | pool_config_invalid | 池配置不合法或含禁止的 Pod 字段（v1.1） |
| 140491 | 403 | path_outside_workspace | 路径越出工作区或指向机器程序目录（BR-04-92；03 映射为 130403 `PATH_OUTSIDE_WORKSPACE`） |
| 140492 | 503 | preview_gateway_unconfigured | 部署未配置独立预览域名，远程预览不可用（BR-04-94） |

---

## 7. 后台任务与事件

### 7.1 任务（`Starter` 启动；跨副本互斥用 `dlock`，TTL 15 s + 自动续期；失败下个周期自然重试，`slog.Warn` 并计数）

| ID | 任务 | 频率 | 互斥 | 内容 |
|---|---|---|---|---|
| J1 | `fleet.heartbeat_watchdog` | 每 5 s | dlock `fleet:heartbeat-watchdog` | `state IN (ONLINE,DRAINING) AND last_seen_at < now−30s` → OFFLINE（M4/M8），会话 LEASE_EXPIRED，发事件；每批 ≤500 行，按 `state`+`conn_epoch` 条件更新 |
| J2 | `fleet.enrollment_expirer` | 每 10 s | dlock `fleet:enrollment-expirer` | PENDING_CONFIRM 且 `expires_at ≤ now` → EXPIRED（N5）；推 SSE |
| J3 | `fleet.inspection_timeout` | 每 5 s | dlock `fleet:inspection-timeout` | QUEUED/RUNNING 且（`requested_at < now−65s` 或机器非在线或 `conn_epoch` 变化）→ ERROR；模型刷新、运行时重检、目录探测挂起超 65 s → 清挂起标记并记错误 |
| J4 | `fleet.catalog_staleness` | 每 60 s | dlock `fleet:catalog-staleness` | ONLINE 机器某运行时 `checked_at < now−15min` → 置 `refresh_requested_at`（下次轮询下发）；机器自身每 5 min 刷新 |
| J5 | `fleet.credential_lifecycle` | 每 60 s | dlock `fleet:credential-lifecycle` | RETIRING 到期 → 吊销（ROTATED）；轮换的 PENDING_DELIVERY 下发后 60 s 无回执 → 吊销并重建（≤5 次，超过 → N-04-06）；接入领取的凭证 60 s 内未开会话 → 吊销（不重建，BR-04-15）；宽限剩 2 h 且新凭证未 ACTIVE → N-04-06 |
| J6 | `fleet.offline_notifier` | 每 15 s | dlock `fleet:offline-notifier` | OFFLINE 且 `offline_since < now−60s` 且未通知 → N-04-01；离线超 15 min → 邮件摘要；确认接入后 >10 min 仍从未在线（`online_since IS NULL`）且未通知 → N-04-09 |
| J7 | `fleet.drain_monitor` | 槽释放事件驱动 + 每 10 s | dlock `fleet:drain-monitor` | DRAINING 且占用槽 0 → `drained_at`；`drain_mode=DISCONNECT` → 下次轮询下发 `stop{DRAINED}`（M9） |
| J8 | `fleet.gateway_reaper` | 每 5 s | dlock `fleet:gateway-reaper` | 超 180 s → FAILED「生成超时」；路由机器离线 → FAILED；每租户只保留最新 100 行（请求元数据不是业务记录，允许物理删除，**本规格提议**） |
| J9 | `fleet.pool_autoscaler` | 每 10 s + 槽释放 / 预留事件驱动（v1.1） | dlock `fleet:pool-autoscaler` | 每个 ONLINE K8S_POOL：WARM 少于 `min_warm` 且总数 < `max_pods` → 创建；超出 `min_warm` 的 WARM 空闲 > `scale_down_idle_s` → 删除；REQUESTED / SCHEDULING 超 `pod_start_timeout_s` → FAILED（K8）；TERMINATING 超 120 s → 重发删除后 GONE（K7）；终态行 >7 天删除 |
| J10 | `fleet.tunnel_ownership_audit` | 每 60 s（D119） | 每副本本地 | 对本副本持有的 OPEN `HTTP_PROXY` 流核对持有者是否仍为非终态执行或 READY 会话（03 `PreviewAccessResolver.HolderActive`），否则 RST；只读核对，不改业务数据 |

### 7.2 发布的领域事件（09 outbox）

| 事件 | 时机 | `data` |
|---|---|---|
| `machine.state_changed`（00） | 每次 MachineState 迁移（含 M1） | `{machine_id, from?, to, reason:"ENROLLED"\|"SESSION_OPENED"\|"LEASE_EXPIRED"\|"DISCONNECTED"\|"DRAIN_REQUESTED"\|"DRAIN_CANCELLED"\|"DRAIN_STOP"\|"REVOKED", conn_epoch, offline_since?, revoked_by?}` |
| `machine.runtimes_changed`（**CR-04-03**） | 运行时或工具新增 / MISSING / 恢复 / 版本变化，或能力标记变化（BR-04-47/48） | `{machine_id, runtimes:[{kind, status, version, previous_version?}], tools_changed?:[{tool_key, status, version?, previous_version?}], capabilities:[string]}` |
| `machine.access_changed`（D119，CR-04-12） | A35 改变任一远程访问开关，或机器 OFFLINE 后通道类型变化（`websocket` 可用性） | `{machine_id, machine_type, allow_remote_preview, allow_remote_terminal, allow_file_edit, websocket_available, access_version, changed:[field]}` |

消费方：03（OFFLINE/REVOKED → 中断该机执行；runtimes_changed → 放置校验；access_changed → 停止被关闭类别的预览 / 终端会话与编辑，BR-03-119）、05（REVOKED → Agent STOPPED；runtimes_changed → 运行时缺失告警、ACCEPTOR 候选资格）、11（`tools_changed` → 重算 `TOOL_UNAVAILABLE`）、08（通知与审计视图）。

### 7.3 消费的事件

| 事件 | 处理 |
|---|---|
| `execution.state_changed`（03） | `to` 为终态 / INTERRUPTED / PAUSED → `ReleaseSlot`（幂等兜底；03 已在事务内同步调用）；触发 J7 |
| `project.stage_changed`（07，`to ∈ {CLOSED, CANCELLED}`） | 归档该项目在各机的工作目录（`archived=true`），不影响运行中执行；仓库单独归档时由 A21/A22 经 07 `RepositoryReader.archived` 校验拒绝新登记 |

### 7.4 审计事件（底座哈希链）

`fleet.machine_enrollment_confirmed`、`fleet.machine_enrollment_denied`、`fleet.enrollment_lookup_throttled`、`fleet.machine_revoked`、`fleet.machine_revoke_denied`（outcome=denied：权限、step-up、不可委托）、`fleet.revoked_machine_access_attempted`（同机 10 分钟合并）、`fleet.machine_credential_rotated`、`fleet.machine_credential_retired`、`fleet.machine_drain_started`、`fleet.machine_drain_cancelled`、`fleet.machine_updated`、`fleet.workdir_registered`、`fleet.workdir_archived`、`fleet.model_price_changed`、`fleet.gateway_config_changed`、`fleet.gateway_key_rotated`、`fleet.gateway_key_revealed`。D119 增补：`fleet.machine_created`（SERVER / K8S_POOL）、`fleet.machine_credential_issued`（预签发与 SERVER 轮换明文下发，detail 不含秘密）、`fleet.machine_fingerprint_mismatch`、`fleet.machine_access_changed`（detail `before{} / after{}`、`reauth_purpose?`）、`fleet.pool_config_changed`、`fleet.tunnel_stream_rejected`（`code ∈ {140485, 140491}` 的拒绝属安全事件，detail `{holder_kind, holder_id, kind, code}`，同持有者同码 10 分钟合并一条；不含路径原文，只含 SHA-256）。

### 7.5 实时（09 SSE）

主题（09 主题语法：单项为 `tenant/{t}/<subject_kind 单数蛇形>/{id}`，集合为复数）：`tenant/{t}/machine/{id}`（状态、心跳、槽、运行时、目录、检测进度；心跳时间每机 5 s 节流）；`tenant/{t}/machines`（列表行增删改，集合主题）；`tenant/{t}/machine_enrollment/{id}`（向导）；`tenant/{t}/gateway_config/{id}`（网关状态与请求记录）。本模块向 09 注册 `TopicAuthorizer`：`machine`、`machines` 需 `machine_read`；`machine_enrollment` 需 `machine_manage`；`gateway_config` 需 `gateway_manage`；`machine/{id}/channel` 仅服务端内部订阅。 D119：`machine/{id}` 帧增加 `remote_access` 与 `pool`（Pod 计数）；`machine/{id}/tunnel` 仅服务端内部订阅（跨副本转发流字节与 RST）。SSE 不可用时列表/详情每 10 s 轮询，向导每 2 s。

---

## 8. 页面规格

> 应用 `apps/frontend-web-admin/apps/delivery`，路由前缀 `/t/:tenantID`；只引用语义 Token；状态一律 `StateBadge`（图标 + 文字）：ONLINE `--success`「在线」、OFFLINE 虚线灰「离线」、DRAINING `--warning`「排空中」、REVOKED `--destructive`「已吊销」。所有页面具备加载骨架、空态（含下一步按钮）、错误态（可重试）、无权限态（写明缺少的权限码）、离线/重连态（顶部条「正在重连… 显示的可能不是最新状态」，保留已加载内容）。文案称「你」；用户可见文本不出现 Runner / Daemon / Computer，统一称「机器」「机器程序」（安装命令与技术详情面板除外）。

### 8.1 机器列表 `/t/:tenantID/machines`

- **目的**：一眼看清有哪些机器、能跑什么、空不空。入口：侧栏「团队 › 机器」；⌘K「接入机器」直达向导。
- **布局**：页头（标题、〔接入机器〕主按钮）→ 筛选条 → 卡片网格（≥1024 px 3 列、768 px 2 列）/ 表格切换（记忆在 localStorage）→ 分页（每页 24 卡 / 50 行）。
- **筛选**：状态多选（默认 在线·排空中·离线）、类型（个人电脑 / 服务器 / 集群池，D119）、运行时（Claude Code / Codex）、标签、「仅看有空闲槽」、操作系统、搜索（名称/主机名，300 ms 防抖）、「显示已吊销」开关。筛选写入 URL 查询串。
- **卡片字段**：机器图标 + 名称 + 类型徽章（个人电脑 / 服务器 / 集群池「v1.1」）；`StateBadge`；远程访问图标组（预览 / 终端 / 编辑，关闭的灰显，D119）；系统图标 + 主机名 · 架构；运行时芯片（`Claude Code 2.x` / `Codex 0.15x`，MISSING 划线 + 提示）；槽位条「已用 n / m」；最近心跳相对时间（`<time>` 带绝对时间 title）；托管 Agent 数；「有新版本」小徽标。表格列：名称、状态、运行时、槽、最近心跳、Agent 数、系统、操作（详情 / 排空，按权限）。
- **状态**：加载 = 6 张卡骨架；空（无机器）= 插画 +「还没有机器。把一台装有 Claude Code 或 Codex 的电脑接入团队」+〔接入第一台机器〕（无 `machine_manage` 时按钮替换为「请联系团队所有者接入机器（需要 machine_manage）」）；筛选无结果 =「没有符合条件的机器」+〔清除筛选〕；无 `machine_read` → 无权限态。
- **按钮**：〔接入机器〕需 `machine_manage`，为分裂按钮：个人电脑（设备码向导 §8.2）/ 服务器（§8.5）/ 集群池（§8.5，「v1.1」）；卡片菜单〔排空〕/〔取消排空〕需 `machine_manage` 且非 REVOKED。
- **实时**：订阅集合主题；状态变化 ≤1 s 更新卡片，ONLINE→OFFLINE 时卡片边框变虚线并以 `role="status"` 播报「机器 X 已离线」（同一分钟内合并播报）。
- **移动端（375）**：单列卡片；筛选收入底部 Sheet；〔接入机器〕成为右下浮动按钮（≥44 px）。

### 8.2 接入向导 `/t/:tenantID/machines/connect`（`?user_code=` 可预填，即机器显示的完整核验地址）

- **目的**：让人在 300 s 内核对并确认一台机器。入口：列表〔接入机器〕、⌘K、机器终端打印的链接/二维码（手机扫码打开同一地址，登录后直达）。
- **布局**（桌面两栏，左「在机器上运行」，右「输入接入码」；移动端上下堆叠，有预填码时右栏在前）：
  1. **安装与登录命令**（A01）：系统切换 Tab（Windows / macOS / Linux，默认按浏览器 UA 推断）；三条命令（安装、登录、启动）各带〔复制〕；前置条件列表；「机器会显示一个 8 位接入码，有效 5 分钟」。
  2. **接入码输入**：单个输入框，格式提示 `XXXX-XXXX`；自动大写、忽略空格与短横；8 个有效字符后自动查找（A02）；〔查找〕按钮。
  3. **待确认卡片**（PENDING_CONFIRM）：`CountdownRing`（300 s 环 + 「4:32」文字）；主机名、系统 + 版本 + 架构、机器程序版本、profile、检测到的运行时列表（种类、版本、路径；未检测到任何运行时 → 琥珀警告「这台机器没有检测到 Claude Code 或 Codex，接入后无法执行任务」但不阻止）；名称输入（预填建议名，1–100，实时重名校验）；同机提示（BR-04-16，单选〔替换 X〕/〔作为新机器〕，必选）；按钮〔拒绝〕〔确认接入〕。
  4. **确认后进度**：三步清单 ✓ 已确认 →（机器领取凭证）✓ 机器已获得凭证 →（会话打开）✓ 已上线；完成后〔查看机器〕〔再接入一台〕。
- **状态与按钮可用条件**：

| 状态 | 显示 | 〔查找〕 | 名称输入 | 〔确认接入〕 | 〔拒绝〕 |
|---|---|---|---|---|---|
| 未输入 | 命令 + 空输入框 | 输入 8 字符后可用 | — | — | — |
| 查找中 | 输入框 spinner | 禁用 | — | — | — |
| 码无效（140410） | 「接入码无效或已过期。请核对，或在机器上重新运行登录命令。」 | 可用 | — | — | — |
| 查找过多（140413） | 「尝试次数过多，请 n 分钟后再试」 | 禁用至 Retry-After | — | — | — |
| PENDING_CONFIRM，剩余 >0 | 待确认卡片 + 倒计时 | — | 可编辑 | 名称合法且（需要时）已选替换方式且非提交中 | 非提交中 |
| 提交中 | 按钮 spinner | — | 只读 | 禁用 | 禁用 |
| CONFIRMED，等待凭证/上线 | 进度清单 | — | 只读 | 隐藏 | 隐藏 |
| CONFIRMED 60 s 未上线 | 琥珀提示「机器还没有上线：确认机器上的登录命令仍在运行、网络可访问服务器」+ 排障链接 | — | — | — | — |
| EXPIRED（倒计时归零或 140412） | 「接入码已过期。请在机器上重新运行登录命令，然后输入新的接入码。」〔输入新的接入码〕 | 可用 | 禁用 | 禁用 | 隐藏 |
| DENIED | 「已拒绝，这台机器不会接入」〔接入其他机器〕 | — | — | — | — |
| CANCELLED（机器端取消） | 「机器端已取消接入」 | — | — | — | — |
| 140411（他人已处理） | 「这个接入请求已被其他成员处理（状态）」 | — | — | — | — |
| 无 `machine_manage` | 无权限态：「接入机器需要 machine_manage 权限」 | — | — | — | — |

- **`CountdownRing`**：剩余 = `expires_at − 服务器时间`（以响应 `server_time` 校正本地时钟偏差）；每秒刷新；剩余 ≤60 s 环转 `--warning`，≤10 s 转 `--destructive`；`aria-live`：60 s 时 polite 播报「接入码剩余 1 分钟」、10 s 时 assertive 播报「接入码 10 秒后过期」、归零时 assertive 播报「接入码已过期」；`prefers-reduced-motion` 时环不做动画只更新数字。到期时即使服务端尚未执行 J2，前端也立即禁用〔确认接入〕。
- **关闭标签页**：不发任何请求；再次打开向导输入同一码可继续（BR-04-12），页面提示「关闭此页不会取消接入；未确认的接入码 5 分钟后自动失效」。
- **实时**：订阅 `machine-enrollments/{id}`（不可用时 2 s 轮询 A03），机器端取消/他人处理/过期即时反映。
- **文案要点**：确认按钮旁说明「确认后，这台机器将以独立的机器身份加入团队，可被分配执行任务。你可以随时吊销。」

### 8.3 机器详情 `/t/:tenantID/machines/:machineID`

- **页头**：名称（可内联改名，`machine_manage`）、`StateBadge`、主机名 · 系统、最近心跳；操作区：〔排空〕/〔取消排空〕（`machine_manage`，非 REVOKED）；REVOKED 时整页只读并显示红色横幅「已于 {时间} 由 {人} 吊销：{原因}」。
- **标签页**（URL `?tab=`，移动端为可横滑 Tab 条）：

| Tab | 内容 | 交互与按钮条件 |
|---|---|---|
| 概览 | 身份（ID、principal、接入时间与确认人）；主机（主机名、系统、版本、架构、profile、最近地址）；机器程序版本（有新版本时给升级命令）；连接（会话、传输方式、连接代次、在线时长）；**执行槽网格**（每槽：空闲 / 运行中→执行链接与开始时间 / 启动中 / 停止中；不可读项目显示「其他项目的执行」）；槽数编辑（1–8）；标签；透传环境变量名；**工作目录**（项目、仓库、路径、状态徽章、探测详情、〔探测〕〔归档〕〔登记工作目录〕表单：项目选择 → 仓库选择 → 绝对路径） | 编辑类需 `machine_manage`；冲突（140409）弹「机器信息已被其他人修改」并刷新；离线时〔探测〕禁用并提示「机器离线」 |
| 运行时 | 表：种类、版本、路径、状态（可用 / 缺失自 …）、检测时间、错误；〔重新检测〕（同时重检工具与截图探测）。下方「工具」小节：无头浏览器（类型 Edge / Chrome / Chromium、版本、路径、「可截图 ✓ / ✗ + 探测错误」）、ffprobe、ffmpeg、WhisperX（可选，缺失显示「未安装（可选）」）、PySceneDetect、Node、Python 各一行；顶部能力芯片 `可截图`、`媒体探测`、`转码`、`语音转写`、`镜头检测`（缺失灰显并悬停说明影响：如「无法截图：视觉验证与产品验收截图不会派到这台机器」） | 〔重新检测〕需 `machine_manage` 且 ONLINE/DRAINING；路径列需 `machine_manage` |
| 模型目录 | 每运行时一节：来源徽章（已检测 / 缓存·可能过期）、检测时间、错误列表；模型表（ID、名称、推理强度、默认标记）支持搜索；手填模型列表（芯片 + 删除、输入框添加，校验 ≤160 无空白，≤50 个）；〔刷新〕 | 刷新需 `machine_read` 且在线；手填需 `machine_manage` |
| 能力 | 运行时选择 + 范围选择（用户目录 / 某工作目录）；状态（排队中 / 检测中（显示已用秒数，65 s 上限）/ 就绪 / 失败）；Skills 列表（名称、说明、启用状态、来源路径、作用域）、MCP 列表（名称、状态、认证、工具清单折叠、错误）；说明「检测只读取本机配置，不调用模型；MCP 列表不代表实时连接状态；本机配置改动后请刷新」；〔刷新〕 | `machine_read` 且在线；检测中按钮显示进度且禁用（重复请求会合并） |
| Agent | 05 提供的列表：头像（圆角方形 + 芯片角标）、名称、角色、运行时 × 模型、状态、当前工作项；搜索 | 无 `agent_read` → Tab 内无权限态 |
| 执行记录 | 03 执行列表 `machine_id=` 过滤（状态、所属工作项、运行时、开始/结束、成本），按调用者可读项目过滤 | 无 `execution_read` → Tab 内无权限态 |
| 凭证 | 当前凭证（ID、创建时间、最近使用）；轮换状态（新凭证待下发 / 已下发；旧凭证宽限倒计时至 `retire_at`）；历史凭证列表；〔轮换凭证〕（`machine_manage`，无 PENDING_DELIVERY 时可用；确认框「机器将在下一次连接时自动换用新凭证；旧凭证保留 24 小时」）；**危险区**〔吊销机器〕 | 吊销需 `machine_revoke`（无权限时按钮禁用并显示「需要 machine_revoke」） |

- **D119 增补 Tab**：

| Tab | 内容 | 交互与按钮条件 |
|---|---|---|
| 远程访问 | 三个开关（`role="switch"`）：允许远程预览（MVP）、允许远程终端（「v1.1」）、允许人工接管后编辑文件（「v1.1」），每个下方一行说明（终端：「只允许人类在重新认证后使用，全部输入输出录制进审计」；WORKSTATION 未开沙箱时红字「这台机器无法限制终端目录」）；带宽上限、并发流上限；通道状态（WebSocket 已连接 / 仅长轮询：远程访问不可用）；预览网关状态（A39：子域模式 / 路径模式「部分应用可能无法正常工作」/ 未配置）；当前活动流（A38，仅 `machine_manage`） | 修改需 `machine_access_manage`（默认仅团队所有者；无权限时开关只读并提示「需要 machine_access_manage」）；打开终端开关 → 底座 `ReauthenticationDialog`（`machine_terminal_enable`）→ A35；关闭任一开关确认框「正在使用的 n 个预览 / 终端会话会立即断开」；冲突 140409 刷新 |
| 池（仅 K8S_POOL，「v1.1」） | 池配置表单（命名空间、镜像、资源、预热数、最大 Pod 数、缩容空闲时间、启动超时）、Pod 列表（状态、持有者链接、就绪耗时、失败原因）、〔下载安装清单〕（A33） | 写需 `machine_manage`；含禁止字段 → 422 140488 内联提示字段 |

- **吊销对话框**（红色语义 `--destructive`）：后果清单（「n 个正在运行的执行将立即中断，需人工检查副作用后再继续」「m 个托管的 Agent 将停止」「这台机器的凭证立即失效，且无法恢复；再次接入会成为一台新机器」）；输入框「输入机器名称以确认」（完全一致才启用）；原因（必填，≤2000）；〔吊销〕→ 底座 `ReauthenticationDialog`（step-up）→ 提交 A11；失败 140424 时重新弹认证。成功后横幅与 toast「已吊销 X，n 个执行已中断」。
- **排空对话框**：选项「排空后保持连接」/「排空后让机器程序退出」；说明「不再接受新执行，正在运行的 n 个执行会继续直到结束」。
- **实时**：订阅 `machines/{id}`；槽与心跳 ≤1 s 更新（心跳 5 s 节流）。
- **移动端**：页头操作收入「更多」菜单；槽网格变为列表；表格转卡片。

### 8.5 接入服务器 / 集群池 `/t/:tenantID/machines/new?type=SERVER|K8S_POOL`（D119；SERVER 为 MVP，K8S_POOL 为 v1.1）

- **步骤**：① 名称、槽数（SERVER 1–8；K8S_POOL 为最大 Pod 数 1–200）、标签 → ② 〔生成凭证〕→ 底座 `ReauthenticationDialog`（`machine_credential_issue`）→ A32 → ③ 显示凭证（`client_id`、`client_secret` 遮罩 +〔显示〕〔复制〕，琥珀提示「只显示这一次，关闭后无法再次查看；丢失请轮换」）与安装命令（Linux systemd / Windows 服务两个 Tab；K8S_POOL 为〔下载安装清单〕+ `kubectl apply` 命令，凭证写入 Secret 的命令单列）→ ④ 等待上线（与 §8.2 进度清单同组件：✓ 已创建 → ✓ 机器已连接 → ✓ 已上线；10 分钟未上线同 N-04-09）。
- **状态**：未配置预签发（WORKSTATION 不可选）；step-up 取消 → 停留在步骤 ①；离开步骤 ③ 前确认「凭证只显示这一次」；无 `machine_manage` → 无权限态。
- **移动端**：单列，命令块横向滚动；复制按钮 ≥44 px。

### 8.4 API 网关 `/t/:tenantID/gateway`（侧栏「治理 › 连接 › API 网关（实验）」）

- **页头**：标题「API 网关」+ 常驻「实验性」徽章 + 副标题「Codex · Responses API」；说明条：「把一台机器上已登录的 Codex 提供给你信任的 OpenAI 兼容客户端。只支持文本、函数/自定义工具；不是加固的公共服务。」
- **状态卡**：开关（`role="switch"`，启用条件：已选在线 Codex 机器且已填模型）；状态文字「已开启 / 机器离线 / 已关闭」；机器选择（仅列出有 Codex 的机器，离线项标「离线」，开启状态下禁用切换——须先关闭）；模型选择器（目录 + 手填 + 自由输入，≤160）；模型变化时出现〔保存默认模型〕。
- **接入信息**：Base URL（`{server}/v1`，〔复制〕）；API Key（遮罩 `gk_••••1a2b`，〔显示〕→ step-up → 明文显示 60 s 后自动遮罩；〔复制〕同样需显示后才可复制；〔轮换〕→ 内联红色确认「旧密钥立即失效，正在进行的 n 个请求将被取消」〔取消〕〔轮换密钥〕→ 显示新密钥一次）；客户端配置示例（TOML，`base_url`、`env_key` 使用中性变量名 `GATEWAY_API_KEY`、`wire_api="responses"`、`supports_websockets=false`）。
- **连接测试**：提示词文本框（≤64000）、〔发送测试请求〕（网关开启且在线时可用）、测试中〔停止〕、结果 `<pre role="status">`。
- **最近请求**：最多 100 条（时间、请求 ID 前 17 位、模型、状态徽章 排队/生成中/完成/失败/已取消、错误、耗时）；排队/生成中行有〔取消〕；空态「暂无请求」。
- **限额面板**（静态）：并发 8、上下文 8、期限 180 s、请求 1 MiB、输出 900 KB、记录 100 条、不记录提示词与工具内容。
- **权限**：无 `gateway_manage` → 整页无权限态。**移动端**：单列，接入信息行内按钮换行，日志转卡片。

---

## 9. 通知（经 09 `Notifier`）

| ID | 事件 | 接收人 | 渠道 | 文案模板 | 去重与合并 |
|---|---|---|---|---|---|
| N-04-01 | 机器离线 >60 s | 持 `machine_manage` 的成员 + 该机正在/刚被中断执行的发起者与负责人 + 绑定 Agent 的管理者（05） | 站内；离线 >15 min 追加邮件 | 「机器 {name} 已离线 {n} 分钟，{k} 个执行已中断。」〔查看机器〕 | 每次离线周期一条（`offline_notified_at`）；5 分钟内多台机器离线合并为「{n} 台机器离线」；邮件每人每小时 ≤1 封 |
| N-04-02 | 已通知离线的机器恢复在线 | 同 N-04-01 已收到者 | 站内 | 「机器 {name} 已恢复在线（离线 {duration}）。」 | 与 N-04-01 同一会话线程（替换为已解决） |
| N-04-03 | 接入确认 | 持 `machine_manage` 的其他成员 | 站内 | 「{actor} 接入了机器 {name}（{os}，{runtimes}）。」 | 1 h 内同一确认人合并 |
| N-04-04 | 机器吊销 | 持 `machine_manage`/`machine_revoke` 的成员 + 受影响执行发起者 | 站内 + 邮件 | 「{actor} 吊销了机器 {name}：{reason}。{k} 个执行已中断，请检查副作用。」 | 不合并 |
| N-04-05 | 排空完成 | 发起排空者 | 站内 | 「机器 {name} 已排空，可以安全维护。」 | 每次排空一条 |
| N-04-06 | 凭证轮换未完成（宽限剩 2 h 或下发失败 5 次） | 轮换发起人 + 持 `machine_manage` | 站内 + 邮件 | 「机器 {name} 尚未换用新凭证，旧凭证将于 {retire_at} 失效，届时需重新接入。」 | 每次轮换至多 2 条 |
| N-04-07 | 运行时缺失（MISSING 且有绑定 Agent 或排队执行） | 持 `machine_manage` + 受影响 Agent 管理者 | 站内 | 「机器 {name} 上的 {runtime} 不可用，相关执行无法启动。」 | 每机每运行时每 24 h 一条 |
| N-04-09 | 确认接入后 10 min 仍未上线 | 接入确认人 + 持 `machine_manage` | 站内 | 「机器 {name} 已确认接入，但 10 分钟内没有上线。请在该电脑上运行 `{command} start` 并检查网络。」〔查看机器〕 | 每台机器一条 |
| N-04-08 | 网关密钥轮换 | 持 `gateway_manage` 的其他成员 | 站内 | 「{actor} 轮换了 API 网关密钥，旧密钥已失效。」 | 不合并 |
| N-04-10 | 集群池 Pod 启动失败（K8，v1.1） | 持 `machine_manage` | 站内 | 「集群池 {name} 无法启动 Pod：{failure}。{k} 个执行在排队。」〔查看池〕 | 每池每 30 分钟一条 |
| N-04-11 | 远程访问开关被修改（D119） | 持 `machine_access_manage` 的其他成员 | 站内 | 「{actor} {开启/关闭}了机器 {name} 的{远程预览/远程终端/文件编辑}。」 | 不合并 |

通知不含代码、提示词或消息正文；三语模板 key `fleet.notification.*`。

---

## 10. 验收标准

> 测试层：unit = 包内单元；integration = 本模块 + 底座 oauth/identity/authn + 夹具机器程序（fixture bridge，不调用真实模型）；e2e = Playwright（桌面 1440 与手机 390）；manual = 真实 Claude Code / Codex。时间相关用可控时钟。

**接入**

```
AC-04-01 [integration+e2e] US-04-01/02, BR-04-11
Given 租户 T 的所有者 O 打开接入向导，夹具机器运行 login 并显示接入码 C
When  O 输入 C，看到主机名/系统/两个运行时，改名为 "build-01" 并确认
Then  接入记录 CONFIRMED；底座设备码 approved（principal=O，带 O 的认证时间）；新建服务账号与 fleet_machines(OFFLINE)；
      机器 ≤5 s 内领取凭证（凭证 ACTIVE）、开会话 → ONLINE，conn_epoch=1；向导三步全部打勾；
      审计 fleet.machine_enrollment_confirmed 一条；埋点 machine_register_begun / confirmed 各一次；全流程（不含人工输入）≤20 s

AC-04-02 [integration+e2e] US-04-03, BR-04-12, §4.1 N5
Given 机器显示接入码 C，无人确认
When  时钟前进 300 s
Then  向导倒计时归零立即禁用〔确认接入〕并播报"接入码已过期"；J2 下一周期内记录 EXPIRED；
      机器下一次领取得到 expired_token，打印"接入码已过期，请重新运行 login"，退出码 3；随后用 C 查找 → 404 140410

AC-04-03 [e2e] CountdownRing
Given 待确认卡片剩余 61 s   When 时间流逝   Then 60 s 时 polite 播报一次"剩余 1 分钟"、10 s 时 assertive 播报一次、0 s 播报"已过期"；本地时钟快 90 s 时显示仍以 server_time 为准

AC-04-04 [integration] BR-04-11, §4.1 N3
Given 待确认接入 E   When O 点〔拒绝〕   Then E=DENIED，设备码 denied；机器下一次领取 access_denied，退出码 4；未创建服务账号

AC-04-05 [integration] §4.1 N4
Given 待确认接入 E   When 机器端 Ctrl+C（P4）   Then E=CANCELLED；向导 ≤2 s 显示"机器端已取消接入"；此后确认 → 409 140411

AC-04-06 [e2e] BR-04-12（关闭标签页）
Given O 查到接入码 C 后关闭标签页（剩余 200 s）
When  O 在 100 s 后重新打开向导并输入 C
Then  显示同一待确认记录，倒计时约 100 s，可确认成功；若改为不再打开，则 300 s 时 EXPIRED，无任何机器被创建

AC-04-07 [integration] BR-04-15（猜码 / 重放）
Given 成员 U 有 machine_manage
When  U 连续提交 10 个随机接入码   Then 均为 404 140410（"不存在"与"已过期"响应字节相同）；第 11 次 → 429 140413 带 Retry-After
When  已领取凭证的设备码再次领取（P3）   Then 400 140410，未签发新凭证
When  已确认的接入码再次查找   Then 404 140410
When  他租户成员输入属于 T 的有效接入码   Then 404 140410，T 的记录不变

AC-04-08 [integration] BR-04-17/18
Given 机器接入客户端的待确认请求 C
When  用底座通用设备页 POST /oauth/device-requests/decisions 批准 C   Then 404，设备码仍 pending
When  机器改用 /oauth/token 的 device_code 授权领取   Then invalid_grant，未签发任何访问令牌

AC-04-09 [integration] BR-04-13
Given 待确认接入 E
When  Agent 服务账号（即使被授予 machine_manage）调用 A04   Then 403 140403 reason_code=HUMAN_ONLY
When  无 machine_manage 的工程师调用 A02   Then 403 140403 missing_permission=machine_manage

AC-04-10 [integration] BR-04-61（并发确认）
Given 两位所有者同时对 E 调用 A04   Then 恰一个 201，另一个 409 140411；只建一台机器、一个服务账号

AC-04-11 [integration] BR-04-16
Given 机器 M（host_fingerprint=H）凭证丢失后重新 login
When  确认时不选替换方式   Then 422 140414
When  选择〔替换 M〕   Then 沿用 M 的 id/名称/工作目录；M 旧凭证 REVOKED(REENROLLED)；新会话 conn_epoch = 旧值+1
```

**凭证与吊销**

```
AC-04-12 [integration] BR-04-23/24, §4.3
Given M ONLINE   When O 轮换凭证
Then  旧凭证 RETIRING(retire_at=now+24h)；下一次轮询响应含 credential_rotation 明文，数据库无明文；
      机器回执后新凭证 ACTIVE；24 h 后 J5 吊销旧凭证，机器下一请求 401 → 用新凭证重换令牌成功，未中断执行
When  轮换未完成时再次轮换   Then 409 140460

AC-04-13 [integration] BR-04-23（离线超过宽限）
Given M 在轮换后离线 25 h   When M 重连   Then 旧凭证已吊销、新凭证从未下发 → 401 140401，退出码 5；N-04-06 已发出

AC-04-14 [integration] BR-04-25/26, M11
Given M ONLINE，执行 X1、X2 RUNNING，长轮询挂起中
When  O 提交 A11（名称一致、原因、有效 step-up）
Then  同一事务：M=REVOKED、服务账号停用、全部凭证吊销、槽释放、审计 fleet.machine_revoked；
      挂起的长轮询 ≤1 s 以 401 结束；machine.state_changed{REVOKED} 被 03 消费后 X1、X2=INTERRUPTED；
      夹具机器 cancel → 4 s 内进程树不存在，本地凭证删除，退出码 5；N-04-04 发出

AC-04-15 [integration] 吊销后重连
Given M 已吊销   When 机器以旧凭证 client_credentials 换令牌 / 以旧访问令牌调用 C1
Then  前者 invalid_client，后者 401 140401；审计 fleet.revoked_machine_access_attempted（10 分钟内重复尝试只一条）

AC-04-16 [integration] BR-04-25 权限拒绝
When  名称不一致 → 422 140425；缺 reauth_proof → 403 140403 reason_code=REAUTH_REQUIRED；仅经委托获得 machine_revoke 的主体 → 403 reason_code=NON_DELEGABLE；
      以 PAT（无 Cookie）调用 → 403 140403 reason_code=REAUTH_REQUIRED
Then  M 状态与 version 不变；各写 fleet.machine_revoke_denied（outcome=denied）

AC-04-17 [integration] 非法迁移
Given M=REVOKED   When 改名 / 排空 / 轮换 / 检测 / 工作目录登记   Then 各 409 140421（参数化 5 例），无副作用
```

**通道、栅栏、离线**

```
AC-04-18 [integration] BR-04-31/32
Given M 会话 conn_epoch=4
When  机器网络抖动后以同 incarnation 重开会话   Then conn_epoch=5
When  旧连接上迟到的 C3（X-Runner-Epoch=4）到达   Then 409 140431，消息未转交 03，执行状态不变
When  另一进程（不同 incarnation、复制的 profile）在旧会话 30 s 内开会话   Then 409 140430 retry_after_ms>0

AC-04-19 [integration] 03 事件序号（经 C3）
Given 某派发 last_seq=10   When C3 发送该派发 seq 14   Then 该项 status 409 code 130428 data.expected_seq=12，同批后续同派发项未处理；重发 12–14 → 200 ack_seq=14

AC-04-20 [integration] BR-04-30, J1, M4
Given M ONLINE，最后一次轮询在 T
When  机器停止轮询
Then  T+20 s 夹具机器已自停执行；T+30 s 至 T+35 s 之间 M=OFFLINE（reason LEASE_EXPIRED）且早于此刻不会 OFFLINE；
      T+60 s 至 T+75 s 间 N-04-01 发出一次；M 在 T+90 s 恢复 → ONLINE、conn_epoch+1、N-04-02

AC-04-21 [integration] BR-04-30（长轮询）
Given M 空闲长轮询中   When 03 为 M 生成 execution.start 并 Wake(M)   Then 机器 ≤1 s 收到命令（非 10 s 后）；短轮询模式下 ≤350 ms+RTT

AC-04-22 [integration] BR-04-36
Given 服务端记录 M 槽 1 由 X 占用   When 机器连续 2 次心跳报告槽 1 IDLE   Then 调用 03 ReportLost(X, HEARTBEAT_LOST)
Given 机器报告运行未知持有者 Y   Then 下一 ServerBatch.cancels 含 Y
```

**调度、工作目录、迁移**

```
AC-04-23 [integration] BR-04-53/55（槽争用）
Given M slot_count=2，项目 P1 已占 1 槽，P1 与 P2 各有 1 条 PENDING 命令（P1 的更早）
When  03 依次调用 ReserveSlot
Then  P2 的命令先获得剩余槽（公平性）；第三个请求得到 NO_FREE_SLOT，命令保持 PENDING；
      并发 20 个 ReserveSlot 争 1 槽时恰一个成功（slot_lock 唯一约束），其余 NO_FREE_SLOT，无重复 slot_no

AC-04-24 [integration] BR-04-50/52
When  在 M 上为仓库 R1 登记 /work/a，再为 R2 登记 /WORK/A/（M 为 Windows）   Then 第二次 409 140444
When  执行 X 持有 /work/a 写锁时改路径   Then 409 140443

AC-04-25 [unit+integration] BR-04-57
Given 执行 X 原在 M1（CODEX，/work/a）
Then  PlanMigration：M2（CODEX，/work/a）→ NATIVE；M3（CODEX，/work/b）→ CARRYOVER；M4（仅 CLAUDE_CODE）→ eligible=false RUNTIME_UNAVAILABLE；
      M5 DRAINING → eligible=false；M1 重连后 → NATIVE；任一端 Windows 时路径大小写不敏感
```

**运行时、能力、目录**

```
AC-04-26 [integration] BR-04-41, §4.4
Given M ONLINE   When 两人先后请求同一运行时×目录的能力检测   Then 第二次返回同一 inspection（合并）
When  夹具 55 s 不回传   Then 机器端放弃；服务端 65 s 时 ERROR"检测中断或超时，请刷新重试"；之后迟到结果 → 409 140450
When  机器离线时请求   Then 409 140423；检测过程中无任何模型调用（夹具断言）

AC-04-27 [integration] BR-04-42/43
Given CODEX 目录上次成功 6 项   When 本次上报空列表 + 错误   Then entries 仍 6 项，source=CACHED，errors 显示
When  手填 "proxy-model-x"   Then 目录合并显示且标"手填"；目录刷新不移除；含空白的 ID → 422 140400
```

**API 网关**

```
AC-04-28 [integration] BR-04-72（轮换取消在途）
Given 网关开启，3 个请求 RUNNING、1 个 QUEUED，机器保留 2 个等待工具的上下文
When  O 轮换密钥
Then  4 个请求 CANCELLED（"网关密钥已轮换"），客户端 SSE 收到失败终止事件；revision+1，机器下一次 C6 reset=true 回收上下文；
      旧密钥请求 → 401 140472；新密钥可用；审计 fleet.gateway_key_rotated

AC-04-29 [integration] BR-04-76/74
When  第 9 个并发请求   Then 429 140473
When  请求含 previous_response_id   Then 400 140474，error.param="previous_response_id"，未下发到机器
When  路由机器离线   Then 503 140471；请求体 1 MiB+1 字节 → 400；最近请求列表仅含元数据（数据库 body/result 在终态为 NULL）

AC-04-30 [integration] BR-04-72 显示密钥
When  无 step-up 调用 A28 → 403 140403 reason_code=REAUTH_REQUIRED；有 step-up → 返回密钥且审计 fleet.gateway_key_revealed
```

**通用**

```
AC-04-31 [integration] BR-04-60/61
When  PATCH 机器缺 If-Match → 428 140428；version 过期 → 409 140409（心跳更新不改变 version）
When  同 Idempotency-Key 重放 A13   Then 返回首次结果，未产生第二把凭证；同键不同体 → 422 176425（09 C7）

AC-04-32 [integration] BR-04-22
Given 夹具执行桥打印其环境变量   Then 不含机器凭证、访问令牌、网关密钥；只含基础集 ∩ 下发的 env_extra_names ∩ env_passthrough_names

AC-04-33 [e2e] 页面状态
机器列表 / 向导 / 详情 / 网关 各自的加载、空、错误（可重试）、无权限（写明权限码）、重连态截图通过；390 px 无横向滚动，触控目标 ≥44 px

AC-04-34 [manual] 真实运行时
在 Windows 与 macOS 各一台真实机器上完成接入、Claude Code 与 Codex 运行时检测、模型目录（Claude supportedModels 非空）、能力检测、工具检测（含 Edge 或 Chrome 截图探测）；记录版本与结果（不以夹具结果代替）
AC-04-40 [integration] BR-04-47/48 工具检测与能力标记
Given 夹具机器 M 上报 runtimes.report{tools:[browser{variant:edge, version, probe:{can_screenshot:true}}, ffprobe AVAILABLE, ffmpeg AVAILABLE, whisperx MISSING]}
Then  A30 返回 capabilities ⊇ [browser_capture, media_probe, media_transcode]，不含 speech_transcribe；无 machine_manage 的调用者看不到 path
When  下一次上报 browser probe.can_screenshot=false
Then  browser_capture 移除；发布 machine.runtimes_changed{tools_changed:[{tool_key:browser…}], capabilities} 一次；ListEligibleMachines(capabilities:[browser_capture]) 不再返回 M
When  旧版机器程序上报不带 tools 的 runtimes.report   Then 工具与能力保持原值，不发事件
AC-04-41 [integration] BR-04-47 截图探测节奏
Given M ONLINE 且浏览器版本未变   When 10 分钟周期检测   Then 夹具断言未启动浏览器进程；When A14 手动检测 Then 执行一次探测，子进程不经 shell 且隐藏窗口
AC-04-42 [integration] A31 工具可用性
Given 两台机器：M1 ONLINE 有 ffmpeg，M2 OFFLINE 有 ffmpeg   When GET /tool-availability?tool_key=ffmpeg   Then online_machines=1
When  M1 离线   Then ToolAvailability(ffmpeg) 为空（11 据此置 TOOL_UNAVAILABLE）；?tool_key=blender → 422 140447
AC-04-43 [integration] BR-04-49 模型家族
Given CLAUDE_CODE 目录项 claude-sonnet-x 未报家族；CODEX 目录项 gpt-x；手填 proxy-model-x
Then  ModelFamily 分别为 anthropic、openai、openai（CODEX 运行时默认）
When  A17 families{proxy-model-x: google}   Then ModelFamily(proxy-model-x)=google，05 ModelFamilyOf 同值
When  families{proxy-model-x: "Google AI"} 或键不在目录 ∪ 手填   Then 422 140448

AC-04-35 [integration] §6.4 Agent 命令（关联 BR-04-30、05 §6.6）
Given 机器 M ONLINE，05 为绑定 M 的 Agent A 入队 agent.start{command_id=c1}
When  M 的长轮询在途
Then  ≤1 s 内 C2 返回 ServerBatch.agent_commands 含 c1（经 MachineChannelNotifier.Wake）；
      M 未发送 agent.command_ack 时每 10 s 重发 c1，第 6 次后不再下发并回调 05 判定命令失败；
      M 发送 agent.command_ack{c1, ok:true} 与 agent.status{A, RUNNING, pid} → 原样转交 05（载荷按 05 §6.6）；
      agent.command_ack 指向未知 command_id → 该项 409 140452，同批其他项照常处理；
      旧 conn_epoch 上的 agent.status → 409 140431，不转交
AC-04-36 [integration] AgentProcState 快照
Given A 为 RUNNING
When  M 连续 3 次轮询的 agents[] 均缺少 A（≈30 s，机器保持 ONLINE）
Then  05 收到缺失信号判定 CRASHED（05 BR-05-11）；若期间 M 先变 OFFLINE（J1），则不产生崩溃判定
AC-04-37 [integration] 孤儿凭证与未上线通知（BR-04-15、J5、J6）
Given 接入确认后机器领取凭证但领取响应丢失
When  60 s 内未以该凭证开会话
Then  J5 吊销该凭证（不重建），审计 fleet.machine_credential_retired；机器重新 login 走 BR-04-16 替换，机器数不增加；
      确认后 10 min 机器仍从未在线 → N-04-09 恰发送一次给确认人
AC-04-38 [integration] 项目关闭归档工作目录
Given 项目 P 在机器 M 有工作目录 W 且有一个 RUNNING 执行
When  消费 project.stage_changed{to:CLOSED}
Then  W.archived=true，运行中的执行不受影响；之后 A21 为 P 登记新目录 → 422 140442
```

---

AC-04-39 [integration] Agent 消息只做传输（05 §6.6 唯一定义）
Given 05 为 M 上的 Agent A 入队 agent.release_notes{command_id=c2} 与 agent.workspace_list{request_id=r1}
When  M 轮询
Then  ServerBatch.agent_commands 原样携带两条信封，本模块不校验载荷字段；
      M 回 agent.workspace_result{r1} 与 agent.review_ack{command_id=c9（本机无此命令）}
Then  r1 原样转交 05；c9 项 409 140452，同批其他项照常处理
And   A 绑定的不是 M 时 M 上报的 agent.status{A} 不转交（409 140452 同码返回）

**远程访问隧道与机器类型（D119）**

```
AC-04-44 [integration] BR-04-91 端口归属（安全）
Given 夹具机器 M（WebSocket 通道）上执行 E 的进程树监听 127.0.0.1:5173；同机另一进程（不在树内）监听 127.0.0.1:8080；E 的子进程监听 0.0.0.0:9000
When  03 经 TunnelBroker.Open 为 E 开 HTTP_PROXY{port:5173}
Then  OPEN，代理 HTTP 与 WebSocket 升级均可用
When  开 HTTP_PROXY{port:8080}
Then  tunnel.rejected{code:140485}；审计 fleet.tunnel_stream_rejected 一条；ports.report 从未包含 8080
When  开 HTTP_PROXY{port:9000}   Then 允许，但连接目标为 127.0.0.1:9000（不连其他地址）
When  5173 的进程退出、另一非树内进程在 10 s 内占用 5173
Then  下一次复查对该流 RST；新开流被 140485 拒绝
AC-04-45 [integration] BR-04-92 路径限定（安全）
Given E 的工作区根 W；W/link → /etc（符号链接）；W/.env 存在
When  开 FILE_READ{path:"../x"}、{path:"/etc/passwd"}、{path:"C:\Windows\win.ini"}、{path:"link/hosts"}
Then  均 tunnel.rejected{code:140491}，审计合并为一条
When  开 FILE_READ{path:".env", mask:[".env"]}   Then 机器按 03 遮罩规则返回遮罩内容，不返回原值
When  路径指向机器程序 profile 目录（即使被链接进 W）   Then 140491
AC-04-46 [integration] BR-04-90 无 WebSocket
Given M 10 分钟内 3 次升级失败已回退 LONG_POLL
When  TunnelBroker.Open 任一类型
Then  ErrNoWebSocket（03 返回 409 140484）；执行命令照常经长轮询下发；A34 websocket_available=false
AC-04-47 [integration] BR-04-95 开关与断流
Given WORKSTATION 机器 M（allow_remote_terminal 默认 false），E 有 2 个 OPEN HTTP_PROXY 流
When  Open(TERMINAL_SHELL)   Then ErrAccessDisabled（03：403 130403 MACHINE_ACCESS_DISABLED）
When  Open(TERMINAL_WATCH)   Then 允许（只读观看不受开关约束）
When  所有者 A35 allow_remote_preview=false
Then  同一事务发布 machine.access_changed；≤1 s 内两个 HTTP_PROXY 流 RST；随后 Open(HTTP_PROXY) → ErrAccessDisabled
AC-04-48 [integration] A35 权限与 step-up
Given 交付负责人 L（无 machine_access_manage）与所有者 O
When  L PUT remote-access   Then 403 140403 MISSING_PERMISSION
When  Agent 或持委托者调用   Then 403 140403 NON_DELEGABLE / HUMAN_ONLY
When  O 把 allow_remote_terminal false→true 不带 reauth_proof   Then 403 140403 REAUTH_REQUIRED，状态不变
When  O 带有效 reauth_proof   Then 200；审计 fleet.machine_access_changed{before, after, reauth_purpose:machine_terminal_enable}；N-04-11 发给其他持码者
When  O 把 allow_remote_terminal true→false   Then 不需要 step-up
AC-04-49 [integration] BR-04-97 配额与限速
Given tunnel_max_streams=16，tunnel_bandwidth_kbps=1000
When  同一执行开第 17 个流   Then 429 140486（data.limit=16）
When  预览流持续下行   Then 机器→服务端吞吐 ≤1000 kbps ±10%；同期 C3 执行事件与 C4 证据上传不被延迟超过 1 s
AC-04-50 [integration] BR-04-99 SERVER 预签发
Given 所有者 O
When  A32{machine_type:SERVER} 带有效 step-up
Then  201 返回一次 client_secret（no-store）；再次 GET 机器或凭证列表不含明文；审计 fleet.machine_created 与 fleet.machine_credential_issued（不含秘密）
When  夹具无头机器程序用该凭证开会话   Then ONLINE，credential_origin=PRE_ISSUED，host_fingerprint 写入
When  同一凭证从另一指纹的主机开会话   Then 409 140430，审计 fleet.machine_fingerprint_mismatch
When  A32{machine_type:WORKSTATION}   Then 422 140482；无 step-up → 403 140403 REAUTH_REQUIRED
AC-04-51 [integration] BR-04-100 / §4.6 K8S_POOL（v1.1）
Given 池 P：min_warm=1，max_pods=2，夹具控制器
Then  J9 创建 1 个 WARM Pod
When  03 ReserveSlot 两次   Then 第一次绑定 WARM Pod（BOUND），第二次创建新 Pod；第三次 NO_FREE_SLOT
When  第一个执行 SUCCEEDED   Then 其 Pod → TERMINATING → GONE（不回到 WARM）；J9 补足 1 个 WARM
When  新 Pod 超过 pod_start_timeout_s 未就绪   Then FAILED，槽释放，03 收到 SPAWN_FAILED，N-04-10 一条
When  控制器重连后上报服务端无记录的 Pod   Then 控制器删除该 Pod
AC-04-52 [unit] BR-04-94 预览网关配置
Given fleet.preview.domain 与控制台同一可注册域   Then 启动时预览视为未配置，A39 enabled:false，03 预览接口 503 140492
Given mode=PATH   Then A39 path_mode_warning:true；会话 Cookie Path=/p/<label>/
AC-04-53 [e2e] 机器详情「远程访问」Tab 与接入服务器
桌面 1440 与手机 390：开关、只读态（无 machine_access_manage）、终端开关的重新认证、关闭开关的确认文案、WORKSTATION 无沙箱红字、路径模式琥珀提示；接入服务器向导的「只显示这一次」提示与离开确认；全部 v1.1 项带「v1.1」标签
AC-04-54 [integration] BR-04-102 唤醒提示传输（D120）
Given M1 长轮询（wait_ms=10000），绑定 Agent Ada、Bo
When  05 在 300 ms 内为 Ada 发出 inbox_seq=5、6 两个提示   Then 挂起的 C2 立即返回，agent_wakes=[{Ada, 6}]（只保留最大值），agent_commands 中无 wake，无 command_id
When  机器丢弃该批次并在 10 s 内再次 poll   Then 新批次 agent_wakes 为空，但 agent_inbox_heads 含 {Ada,6} 与 {Bo,当前值}；空批次同样携带 agent_inbox_heads
When  切换到 WebSocket   Then 提示以文本帧 type:"agent.wake" 即时到达，帧中只有 agent_id 与 inbox_seq；Ada 换绑到 M2 后 M1 不再收到 Ada 的提示与头
AC-04-55 [integration] BR-04-103 活动状态转交（D120）
Given Ada RUNNING 于 M1（conn_epoch=7）
When  C3 上报 agent.status{activity:THINKING}   Then 原样转交 05 AgentChannelHandler
When  以 conn_epoch=6 上报   Then 409 140431，不转交
When  agent.reset 命令未 ACK   Then 每 10 s 重发，≤6 次（与其他 agent_commands 相同）
AC-04-56 [integration] BR-04-104 批注辅助脚本注入（D120）
Given 预览会话 S 的 HTML 页面
When  以 annotate=true 的访问会话请求页面   Then 响应在 </head> 前含 /__annotate.js（带完整性哈希）；JSON / 图片 / 非 200 响应不变
When  以普通访问会话或分享链接访问   Then 不注入
When  请求 /__annotate.js   Then 由网关同源返回固定版本脚本，脚本不读取 document.cookie、不发起 fetch / XHR（静态检查）
AC-04-57 [integration] BR-04-105 工具集落地（D120）
Given execution.start 的 toolset 含技能 deploy v4（sha256=h4）、STREAMABLE_HTTP 条目 issue-tracker v2、STDIO_PACKAGE 条目 fmt（integrity=i1），白名单含本机 MCP local-db
When  机器启动执行   Then 运行目录中出现 deploy/SKILL.md 与 .catalog.json{deploy, 4, h4}，仓库工作区与用户全局技能目录无变化；MCP 配置含 issue-tracker → http://127.0.0.1:<port>/r/<随机段>/issue-tracker、fmt 的启动命令与 local-db
When  deploy 内容哈希与 h4 不符，或 fmt 安装失败   Then execution.rejected{reason:"TOOLSET_UNAVAILABLE"}，运行时未启动
When  执行 SUCCEEDED   Then 运行目录中的技能与 MCP 配置被删除，本地代理对该随机段返回 404
AC-04-58 [integration] BR-04-106 本地代理与令牌续签（D120）
Given 执行 E 运行中，issue-tracker 令牌剩余 2 分钟
When  运行时经本地代理调用 tools/call   Then 上游请求带 E 的工具令牌，运行时侧请求不含任何令牌
When  leases.renew 响应带新令牌   Then 之后的转发使用新令牌
When  来自非回环地址或未知随机段的请求   Then 拒绝，不转发
When  能力检测完成   Then S3 技能项带 sha256，运行目录中的目录技能不出现在检测结果中
AC-04-59 [integration] BR-04-107 凭据不落机器（D120）
Given 测试夹具扫描执行 E 的运行目录、子进程环境变量与命令行、执行事件与机器日志
When  E 使用目录 MCP 完成若干次调用，期间团队轮换了凭据   Then 扫描结果中不存在团队凭据（新旧均无）与完整工具令牌；日志中令牌只以 tok_…后 4 位出现
```

## 11. 度量与埋点（不含代码、提示词、路径原文与主机名）

| 事件 | 属性 |
|---|---|
| `machine_register_begun` / `machine_register_confirmed` / `machine_register_expired` / `machine_register_denied` / `machine_register_cancelled`（PRD §15） | `os`, `runtime_kinds[]`, `duration_ms`（从附加到决定）, `replaced:bool` |
| `machine_state_transition` | `from`, `to`, `reason`, `offline_duration_ms?` |
| `runner_session_opened` | `transport`, `runner_version`, `reconnect:bool` |
| `runner_epoch_fenced` | `message_kind` |
| `slot_reservation` | `result`（OK / NO_FREE_SLOT / OFFLINE / DRAINING）, `wait_ms`（排队到获得） |
| `slot_utilization`（每 5 min 采样） | `machines_online`, `slots_total`, `slots_used` |
| `capability_inspection_completed` | `runtime_kind`, `result`, `duration_ms`, `skill_count`, `mcp_count` |
| `model_catalog_reported` | `runtime_kind`, `entry_count`, `source`, `error_count`, `family_inferred_count` |
| `tool_detection_reported` | `tool_key`, `status`, `variant?`, `can_screenshot?`, `probe_ms?`（不含路径） |
| `machine_credential_rotated` / `machine_revoked` | `delivery_attempts` / `interrupted_executions` |
| `gateway_request` | `state`, `duration_ms`, `stream:bool`, `model_listed:bool`, `error_code?` |
| `tunnel_stream_closed`（D119） | `kind`, `machine_type`, `result`（CLOSED / RESET）, `reset_code?`, `duration_ms`, `bytes_bucket` |
| `pool_pod_ready`（v1.1） | `wait_ms`, `warm_hit:bool`, `result` |

服务端指标（Prometheus）：`fleet_machines{state,machine_type}`、`fleet_tunnel_streams{kind}`、`fleet_tunnel_bytes_total{machine_type,kind}`、`fleet_tunnel_protocol_errors_total`、`fleet_pool_pods{state}`（D119）、`fleet_channel_requests_total{endpoint,code}`、`fleet_longpoll_wake_latency_seconds`、`fleet_watchdog_lag_seconds`、`fleet_gateway_active_requests`。

---

## 12. 依赖、风险与待决

**依赖**

| 对象 | 需要 |
|---|---|
| 03 | `ExecutionDispatchHandler`（CR-03-05）接线到 C2/C3/C4；消费 `machine.state_changed` 中断执行；执行终态时在事务内 `ReleaseSlot`；`execution_workdir_locks` 以本模块 `path_key` 为 `workdir_key`；换机器调用 `PlanMigration` |
| 05 | `AgentDirectory`（机器详情 Agent Tab、`agent_count`、Agent 绑定机器）；消费 REVOKED / runtimes_changed；**Agent 机器通道消息（命令与上行）以 05 §6.6 为唯一定义**，本模块只提供 `agent_commands[]` 信封、`agents[]` 快照与 C3 上行转交（05 `PendingAgentCommands`、`AgentChannelHandler`） |
| 07 | `RepositoryReader`（07 §6.3）；`project.stage_changed` |
| 08 | `AuthorityChecker`（`machine_revoke` 不可委托）；通知偏好 |
| 09 | `JobQueue`、`OutboxPublisher`、`RealtimeBroadcaster`（含集合主题与跨副本长轮询唤醒）、`Notifier`、`SecretSealer` |
| 底座 oauth | `DeviceGrants` 窄接口 + 客户端属性（CR-04-06） |
| 底座 identity | 事务内服务账号/凭证操作（CR-04-08） |
| 底座 authn | step-up purpose `machine_revoke`、`gateway_key_reveal`；服务账号令牌逐请求状态校验（已有） |
| 03（D119） | `PreviewAccessResolver`（网关访问判定、`HolderActive`）；流的种类、目标与生命周期；消费 `machine.access_changed` |
| 08（D119） | 权限码 `machine_access_manage`（不可委托、仅人类）；step-up `machine_credential_issue`、`machine_terminal_enable`；审计目录 |
| 部署（D119） | 独立预览域名、通配 DNS 与通配证书（或路径模式）；K8S_POOL 需集群与命名空间权限（v1.1） |

**风险**

| 风险 | 缓解 |
|---|---|
| 长轮询被企业代理截断 | 自动回退 350 ms 短轮询（BR-04-30）；WebSocket 通道（D119 起 MVP） |
| 机器数增长导致轮询负载 | 长轮询 10 s；每机 ≤20 req/s；WebSocket 通道（D119 起 MVP） |
| 吊销凭证使同服务账号全部令牌失效（token_version） | 机器 401 后先重换令牌（BR-04-24） |
| Codex 无模型目录命令 | 保留末次成功 + 手填（BR-04-42/43） |
| 网关的机器端工具禁用未被完整证明 | 实验标识、可信访问域声明、只读沙箱（BR-04-77） |
| 领取响应丢失导致孤儿凭证 | 一次性消费 + 重接入替换 + J5 清理（BR-04-15） |
| 预览把机器上其他服务暴露出去 | 只连回环 + 进程树归属在机器端判定 + 周期复查（BR-04-91），拒绝即审计 |
| Agent 写的网页窃取控制台会话 | 独立可注册预览域 + 每会话独立子域，控制台 Cookie 永不到达预览源（BR-04-94） |
| 工作站上的交互终端无法限制目录 | 默认关闭、仅所有者可开且需 step-up；无沙箱时常驻红字（BR-04-96） |
| 预览流量挤占执行通道 | 每机令牌桶 + 控制消息优先 + 流窗口（BR-04-97） |
| WebSocket 被企业代理拦截 | 长轮询回退保证执行不受影响；远程访问明确提示不可用（BR-04-90） |

**待决（附建议）**

| # | 议题 | 建议 | 理由 |
|---|---|---|---|
| Q1 | 分发渠道：npm 还是签名二进制下载站 | MVP 用 npm（BR-04-02），v1.1 加签名二进制 | Node 已是执行桥前置条件；免建下载与签名流水线 |
| Q2 | 机器服务账号凭证的 scope 取值 | 由底座确认服务账号允许的最小 scope；机器只需 `client_credentials` 换取租户访问令牌 | 当前底座 `normalizeServiceAccountScopes` 的合法集未在本规格范围内核实 |
| Q3 | 接入确认是否需要 step-up | 不需要（BR-04-13）；租户策略可在 08 中开启 | 300 s 时窗 + 人工核对 + 批准者认证强度已记录 |
| Q4 | 默认槽数 | 1（沿用 Preview），由管理员调高 | 同目录单写之外，机器 CPU/内存与运行时配额差异大 |
| Q5 | `/v1` 是否作为根路径协议端点 | 保留 `/v1/*`（同 `/oauth/*` 属协议端点豁免资源命名规则） | 客户端只配置 base_url，需与 OpenAI 路径一致 |
| Q6 | 本地服务与 Desktop 壳是否进 MVP | v1.1（PRD §17 W3 只要求接入与检测） | 控制台可在浏览器直接使用；不阻塞闭环 |
| Q7 | 自动迁移 | 不做；由 03 / 人决定（BR-04-57） | 有副作用的工作不得自动交给另一台机器（Preview 原则） |
| Q8 | WebSocket 通道提前到 MVP（D119） | **已决（2026-10-12）：提前**：D119 的 MVP 预览 / 文件 / 只读终端都依赖多路复用流 | D119 未单列通道分期；长轮询无法承载 HMR WebSocket 与 SSE |
| Q9 | SERVER / K8S_POOL 默认开启远程终端与文件编辑 | **已决（2026-10-12）：开启**（§3.15）；租户可改 | 服务器与 Pod 不含个人数据且有沙箱；D119 只规定工作站默认关闭 |

---

## 13. 契约变更请求

| ID | 对象 | 请求 | 理由 |
|---|---|---|---|
| CR-04-01 | 00 §5 枚举 | `MachineState` 去掉 `PENDING_CONFIRM` → `ONLINE, OFFLINE, DRAINING, REVOKED`；新增 `EnrollmentStatus = PENDING_CONFIRM, CONFIRMED, DENIED, EXPIRED, CANCELLED`（所有者 04）；并登记 04 局部枚举：`MachineCredentialState`（PENDING_DELIVERY, ACTIVE, RETIRING, REVOKED）、`InspectionState`（QUEUED, RUNNING, READY, ERROR）、`CatalogSource`（DETECTED, CACHED）、`WorkdirStatus`（UNVERIFIED, VERIFIED, INVALID）、`GatewayRequestState`（QUEUED, RUNNING, COMPLETED, FAILED, CANCELLED）、`ResumeMode`（NATIVE, CARRYOVER）、`SlotHolderKind`（EXECUTION, VERIFICATION, COLLECTION, MERGE） | 未确认的接入不是机器（旧 PRD「临时 machine 不出现在列表」）；PRD §6.8 的 DENIED/EXPIRED 本就不是机器状态。确认后机器以 OFFLINE 起步、首次会话才 ONLINE（细化 PRD「确认 → ONLINE」：在线必须有活会话） |
| CR-04-02 | 00 §3 / §9 | 时间列 `TIMESTAMPTZ(3)` / `DATETIME(3)` / `DATETIME`（全仓禁用 MySQL `TIMESTAMP`）；乐观锁经 `ETag`/`If-Match`，缺失 428；OpenAPI tag = 最终路径首段 | 与 `backend-module` 规约、2026-09-26 路由规则及 02/03/07（CCR-07/08、CR-03-08）一致 |
| CR-04-03 | 00 §7.1 | 新增事件 `machine.runtimes_changed`（发布 fleet；消费 execution、fleet(agents)） | 05 需感知 Agent 绑定的运行时缺失，03 需放置校验 |
| CR-04-04 | 00 §8 | 新增 07 提供的 `RepositoryReader`（`Get(repository_id) {project_id, url, archived}`） | 工作目录登记校验仓库归属 |
| CR-04-05 | 00 §9 实时 / 09 | 允许集合主题 `tenant/{t}/machines` 与 `tenant/{t}/machine-enrollments/{id}`；`RealtimeBroadcaster` 支持服务端内部订阅（用于跨副本唤醒长轮询） | 列表实时与吊销 ≤1 s 生效 |
| CR-04-06 | 底座 `internal/iam/oauth` | ① 客户端新增属性 `device_subject`（`principal` 默认 / `machine`）与 `device_code_ttl`（覆盖全局 10 min，机器客户端 300 s）；② `/oauth/token` 对 `device_subject=machine` 的 device_code 授权返回 `invalid_grant`；③ 通用 `GET/POST /oauth/device-requests*` 对该类请求返回 404，前端设备页显示跳转提示；④ 提供 `module.Provide[DeviceGrants]`：`Pending(ctx, deviceCode) (tenant, client, expires_at)`、`Lookup(ctx, tenant, userCode)`、`DecideOn(ctx, tx, tenant, userCode, principal, approve, assurance)`、`ConsumeOn(ctx, tx, client, deviceCode) (DeviceGrantResult, error)`（复用 interval/slow_down/过期/拒绝/条件消费语义，不签发令牌）、`WithdrawOn(ctx, tx, deviceCode)` | 现行设备码以批准者本人身份签发令牌，且设备请求不携带设备信息；机器接入需要「人批准 → 签发机器服务账号凭证」 |
| CR-04-07 | 09 / 底座 | 平台通用 `SecretSealer`（AEAD，带 purpose 与关联数据，密钥由部署配置/轮换） | 网关密钥「显示」需可逆存储；不在业务模块自建加密 |
| CR-04-08 | 底座 `internal/iam/identity` | 提供 `ServiceAccountProvisioner` 事务内版本：`CreateOn(tx, …)`、`IssueCredentialOn(tx, …) (secret)`、`RevokeCredentialOn(tx, …)`、`DisableOn(tx, …)` | 确认接入、领取、轮换、吊销须与 fleet 行同事务（现行方法自开事务） |
| CR-04-09 | PRD §6.8 | 命令示例 `myrmidon-runner` 改为中性 `machine-runner`（包名/命令可配置） | 仓库规约：部署身份是配置，不得写入代码与产物名 |
| CR-04-10 | 00 §6.1 / §9 | 记录：机器通道（R）以服务账号 + `RegisterTenantMember` + 本模块机器守卫鉴权，不新增权限码；通道以自然键去重，不适用 `Idempotency-Key` | 机器身份无角色，仅能访问通道 |
| CR-04-11 | 00 §8 | `MachineDirectory` 方法集按 §6.7 冻结；新增 `MachineChannelNotifier`（fleet 提供，03 使用） | 与 03 CR-03-05 `ExecutionDispatchHandler` 对接 |
| CR-04-12 | 00 §5 / §6 / §7.1 / §8（D119） | ① 共享枚举 `MachineType`（WORKSTATION, SERVER, K8S_POOL；所有者 04，03 读取）；模块内枚举 `PoolPodState`（REQUESTED, SCHEDULING, WARM, BOUND, TERMINATING, GONE, FAILED）、`TunnelStreamKind`（§6.4b）、`TunnelStreamState`、`CredentialOrigin`（DEVICE_CODE, PRE_ISSUED）不进 §5；`SlotHolderKind` 增 `PREVIEW`；② 权限码 `machine_access_manage`（fleet 注册，非 DataScoped，不可委托、仅人类）；③ 事件 `machine.access_changed`（fleet → execution）；④ 接口 `TunnelBroker`（fleet → execution）、`MachineDirectory.Type/RemoteAccess`、03 `PreviewAccessResolver`（execution → fleet） | 远程预览、文件与终端经机器隧道（D119） |

**处理状态（00 v1.3 审校后）**：CR-04-01 已决——`MachineState` 去掉 `PENDING_CONFIRM`；其余为模块内枚举，按 00 §3 不进 §5。CR-04-02、CR-04-03、CR-04-04、CR-04-05、CR-04-07、CR-04-11 已并入 00（主题语法以 09 为准；`SlotReserver` 取消，经 `MachineDirectory`）。CR-04-06、CR-04-08 列入 00 §13 F2/F3。CR-04-09 已改 PRD。CR-04-10 已记录。幂等改用 09 C7 平台表；step-up purpose 完整清单以 08 危险操作登记表为准（本模块使用 `machine_revoke`、`gateway_key_reveal`）。

**第二轮复审（00 v1.5）**：Agent 机器通道消息改为引用 05 §6.6 唯一定义（#18），本模块只保留传输帧（`agent_commands[]`、`agents[]`、C3 转交）与 140452 判定；新增 AC-04-39。

**D112 / D113（00 v2.0，2026-10-12）**：新增工具检测与能力标记（§3.14、S4、BR-04-47/48、A30/A31、140447）与模型家族（S2 `model_family`、`manual_model_families`、BR-04-49、A17 `families`、140448）；`MachineDirectory` 增 `Tools`/`HasCapability`/`ToolAvailability`/`ModelFamily` 与 `ListEligibleMachines.capabilities`；`machine.runtimes_changed` 载荷增 `tools_changed`、`capabilities`；新增 AC-04-40..43。使用方 03（VISUAL 验证器、验收截图）、05（跨模型家族选择、ACCEPTOR 资格）、11（`TOOL_UNAVAILABLE`）。已并入 00 v2.0 §8（无新事件）。

**D119（2026-10-12）**：远程访问隧道（§5.10、§6.4b、§4.7、BR-04-90..97）、机器类型 SERVER / K8S_POOL（§3.15–3.17、§4.6、§5.11、BR-04-98..101）、每机远程访问开关与配额、预览网关部署配置；接口 A32–A39；错误码 140482/140484/140485/140486/140488/140491/140492；任务 J9/J10；事件 `machine.access_changed`；通知 N-04-10/11；AC-04-44..53；CR-04-12（已并入 00 v2.0 D119 补充）。分期：SERVER、隧道与 WebSocket 通道、预览开关、配额、预览网关为 MVP；K8S_POOL、终端与编辑开关的生效能力为 v1.1。

**D120（2026-10-12）**：Agent 唤醒提示与收件箱头的传输（`ServerBatch.agent_wakes[]` / `agent_inbox_heads[]`、WebSocket `agent.wake` 帧，BR-04-102）、`AgentProcState.activity` / `session_epoch` 与 `agent.reset` 的转交（BR-04-103）、预览网关的批注辅助脚本注入（BR-04-104，供 03 BR-03-136）；`agent_commands[]` 中的 `inbox` 废止；AC-04-54…56。载荷语义仍以 05 §6.6 / §6.z 与 03 为唯一定义；不新增错误码。D120 第 6 项（团队工具目录）：工具集落地、本地代理与工具令牌、能力检测技能哈希、凭据不落机器（BR-04-105…107）；AC-04-57…59。**CR-04-14**（对 00）：无新增共享枚举、权限码、事件或错误码段；`execution.rejected.reason` 增加取值 `TOOLSET_UNAVAILABLE`（03 定义）；依赖 05 `ToolsetResolver`（经 03 下发）与中继 T13。**CR-04-13**（对 00）：无新增共享枚举、权限码、事件或错误码段，只登记 05 新增的 `AgentInboxHeads(machine_id)` 读取（00 §8，与 `PendingAgentCommands` 同列）。

# 09 · 平台通用能力（后台任务 · 领域事件 outbox · 实时推送 · 对象存储 · 通知 · 菜单贡献 · 幂等 · 密钥封装 · 租户业务文件）

> **现行范围（2026-10-12，D125–D129）**：用户已批准需求审计 E1–E4；以 [00 共同契约](00-conventions.md) §16 与本文 §14 为准。原型全量展示，开发 MVP 仅单项目软件闭环；其余按现行分期延后。下文历史门户 / 跨项目菜单 / 独立拆解模板 / 旧 MVP 标记如冲突均已退休，不构成实现承诺。

> 上位：`PRD.md` §13.3、§16；契约：`00-conventions.md`（不在此重定义，变更见 §13）。
> 性质：**底座通用能力**，按「做什么」命名，不含任何产品/品牌字样；Myrmidon 是第一个使用者，其他产品同样复用。
> 已对齐的同级规格诉求：01（集合主题、邮件自定义头与投递回写、租户业务文件写入、幂等表列）、02（消费去重替代 `delivery_consumed_events`）、03（outbox、JobQueue、证据文件写入）、04（集合主题、跨副本长轮询唤醒、`SecretSealer`）、06/07（JobQueue、文件写入）。
> 约定：凡未在仓库或上位文档出现的设计均标「**本规格提议**」并附理由。时间列一律 `TIMESTAMPTZ(3)` / `DATETIME(3)` / `DATETIME`；仅「与数据库时钟比较的租约/调度时刻」用 `*_at_ms BIGINT`（理由见 BR-09-03）。

---

## 1. 范围与目标

### 1.1 解决什么

| # | 能力 | 一句话 | 放置 | 新模块名 / 表前缀 |
|---|---|---|---|---|
| C1 | 后台任务队列 | 与业务同事务入队、SKIP LOCKED 认领、租约心跳、退避重试、死信、延时/周期任务、幂等键、优先级、按租户公平、Quiesce 优雅停机 | `internal/infra/jobs` | `jobs` / `background_job*` |
| C2 | 领域事件 + 事务性 outbox | 00 §7.1 信封同事务落表；单实例定序器分配全局 `stream_seq`；按订阅者分区游标派发；至少一次 + 消费去重；按聚合保序；保留与重放 | `internal/infra/events` | `events` / `domain_event*` |
| C3 | 实时推送 SSE | 每标签页一条流、主题订阅（实体 + 集合 + 收件箱）、逐主题授权与周期复查、15 s 心跳、`Last-Event-ID` 续传、`resync_required`、跨副本扇出、服务端内部订阅（唤醒长轮询） | `internal/infra/realtime` + `pkg/extension/authz`（流式注册）+ `packages/ui` | `realtime` / 无表 |
| C4 | S3 兼容对象存储驱动 | 为 `internal/files` 现有 `Driver` 合同新增 `s3` 实现（范围读、multipart、服务端复制、预签名下载、SSE、校验、重试），本地→S3 迁移 | `internal/files/s3.go` | 沿用 `file_*` |
| C5 | 通知渠道 | `Notifier`：站内信、邮件（SMTP，支持自定义头与投递状态回写）、Web Push（v1.1）、i18n 模板、偏好钩子（语义归 08）、限流、去重合并 | `internal/notification` | `notification` / `notification*` |
| C6 | 模块菜单贡献 | 以模块能力接口替代硬编码 `core.DefaultMenus`，按权限门控，按顶层域（Domain，D118）、空间（Space：团队 / 项目，D118 修订）与分组归类，按租户播种/升级且不复活管理员删除项 | `internal/iam/core` | `iam_menu_contributions` |
| C7 | 幂等键助手 | 00 §9 `Idempotency-Key` 的共享实现：事务内模式 + 非事务中间件模式 | `internal/infra/idempotency` | `platform_idempotency_keys` |
| C8 | 密钥封装 `SecretSealer` | 业务模块可逆加密存储机密（网关密钥、外部凭据），带 purpose 与关联数据、可轮换 | `internal/infra/secrets`（包装 `pkg/extension/keyringx`） | 无表 |
| C9 | 租户级业务文件写入 | 服务端（非 HTTP 上传）向租户系统目录写文件并按 purpose 绑定业务资源，与 `AccountResourceFiles` 同形 | `internal/files/tenant_resource.go` | 沿用 `file_*` |

### 1.2 不解决什么（边界）

| 不做 | 归属 |
|---|---|
| 通知类型目录与路由（谁收、哪些渠道）、通知偏好的语义（08 提供 `PreferenceResolver`）、「我的裁决」聚合、通知中心页面 | 08（**站内信存储、投递与收件箱 HTTP 归本规格 C5**，08 不另建站内信表） |
| 机器长连接通道（HTTP 长轮询协议本身）、执行级消息序号 | 04 / 03（本规格只提供跨副本唤醒信号） |
| 业务事件的 `data` 字段定义、各事件消费逻辑 | 发布者所在规格 |
| 审计哈希链（`iam/audit`）与 webhook 投递 | 底座现状保留；本规格**不复制**审计链（BR-09-30） |
| 动态插件、WASM、消息中间件（Kafka 等）、Redis 作为必选依赖 | `plugin-architecture.md` 阶段三以后 |
| 整文件分片上传协议（tus）与 Web 文件管理器 | `file-management-design.md` |

### 1.3 与既有文档的关系

- **`plugin-architecture.md`**：本规格**落实**其「阶段一」中的领域事件 + Transactional Outbox、后台任务/超时/重试/死信、幂等请求、`NotificationProvider`（此处为 `Channel` SPI，首批两个真实实现：SMTP、站内信）。事件命名 `<资源>.<过去式>`、信封含事件 ID / 租户 / 时间 / 版本，与其一致；**不**建设插件市场、动态 Hook。
- **`todo-module-design.md` D5**（「仓库无后台任务基础设施，导入导出同步」）：前提被本规格消除；D5 本身不改写，异步导入是否采用由 todo 另行决定。
- **`module-lifecycle-redesign.md`**：遵循「模块自治 + 共享 `*appenv.Runtime` + 聚合即遍历」；贡献类能力（任务处理器、事件订阅者、主题授权器、菜单页）用模块能力接口声明，由 `internal/app` 在 `Wire` 之后遍历激活集交给 sink（与 `collectMetrics` 同法），不新增 DI。
- **旧项目 `runtime-modes.md` §7.3**：采纳「每个订阅 scope 序号固定」「快照带水位 + 游标被压缩返回 `resync_required`」「容量上限先背压，禁止静默丢弃」。

### 1.4 目标数值

| 指标 | 目标 |
|---|---|
| 实时：业务提交 → 浏览器呈现 | P95 ≤ 1 s（PostgreSQL 启用 NOTIFY 时 ≤ 300 ms；纯轮询 ≤ 700 ms） |
| 任务：入队 → 开始执行（空闲队列） | PostgreSQL ≤ 100 ms；MySQL/SQLite ≤ 1.2 s |
| 事件：提交 → 订阅者处理（无积压） | ≤ 600 ms |
| 任务吞吐（单副本，空处理器） | ≥ 300 jobs/s（PG），≥ 100 jobs/s（SQLite） |
| SSE 连接 | 每副本 ≤ 5,000；每主体 ≤ 10 |

---

## 2. 用户故事

| ID | 故事 | 期 |
|---|---|---|
| US-09-01 | 作为模块开发者，我要在业务事务内入队一个任务，以便业务回滚时任务不存在、提交后任务必执行 | MVP |
| US-09-02 | 作为模块开发者，我要声明处理器的超时、最大次数、退避与队列，以便不自己写 ticker 循环 | MVP |
| US-09-03 | 作为模块开发者，我要声明周期任务（间隔或 cron），以便多副本下每次只触发一次 | MVP |
| US-09-04 | 作为平台运维，我要查看死信任务并重试或丢弃，以便故障恢复后补偿 | MVP |
| US-09-05 | 作为模块开发者，我要在业务事务内发布领域事件，以便事件与状态变更同生共死 | MVP |
| US-09-06 | 作为模块开发者，我要注册事件订阅者并获得按聚合有序、去重的投递，以便消费逻辑简单 | MVP |
| US-09-07 | 作为平台运维，我要查看订阅者积压与搁置事件，重投或从某序号重放 | MVP |
| US-09-08 | 作为团队成员，我要页面上的状态变化 ≤1 s 自动出现，断网重连后不丢更新 | MVP |
| US-09-09 | 作为模块开发者，我要在服务端订阅主题以唤醒任意副本上挂起的长轮询（04） | MVP |
| US-09-10 | 作为运维，我要把文件存到 S3/MinIO 并把存量本地文件迁过去 | v1.1（驱动 MVP 可用，迁移工具 v1.1） |
| US-09-11 | 作为模块开发者，我要给主体发站内信/邮件，或给系统外地址发带线程头的邮件并得到投递结果 | MVP |
| US-09-12 | 作为团队成员，我要在浏览器关闭时也收到紧急事项推送 | v1.1 |
| US-09-13 | 作为模块开发者，我要声明本模块的租户菜单页，以便新老租户都看到入口而不改 IAM 代码 | MVP |
| US-09-14 | 作为 API 调用方，我要用 `Idempotency-Key` 重放命令时拿到首次结果 | MVP |
| US-09-15 | 作为模块开发者，我要可逆加密保存外部密钥并支持密钥轮换 | MVP |
| US-09-16 | 作为模块开发者，我要在服务端把生成的文件写入租户空间并绑定到业务资源 | MVP |

---

## 3. 领域模型与数据表

通用：所有对象名带 `{{.Prefix}}`；三方言迁移位于各模块 `sql/{postgres,mysql,sqlite}/`，goose 表 `goose_<module>`；文本列 PG `COLLATE "C"`、MySQL `utf8mb4_bin`、SQLite `COLLATE BINARY`（与 `iam_webhook_*` 一致）；JSON 列 `JSONB` / `JSON` / `TEXT`；布尔 `BOOLEAN` / `BOOLEAN`(=TINYINT(1)) / `INTEGER`(0/1)。`tenant_id = 0` 表示平台级（系统任务），**本规格提议**：基础设施表允许 0，见 CR-09-06。

### 3.1 `background_jobs`（C1）

| 列 | 类型（PG / MySQL / SQLite） | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| id | BIGINT | N | — | PK，雪花 |
| tenant_id | BIGINT | N | 0 | 0=平台级 |
| queue | VARCHAR(64) / VARCHAR(64) / TEXT | N | `'default'` | `^[a-z][a-z0-9_]{0,63}$` |
| kind | VARCHAR(128) / VARCHAR(128) / TEXT | N | — | `<module>.<verb_noun>`，必须已注册 |
| state | VARCHAR(16) | N | `'PENDING'` | CHECK ∈ PENDING, RUNNING, SUCCEEDED, DEAD, CANCELLED |
| priority | SMALLINT / SMALLINT / INTEGER | N | 5 | CHECK 1..9，数值小者先 |
| payload | JSONB / JSON / TEXT | N | — | JSON 对象，≤64 KiB，**不得含机密**（机密存业务表并用 C8 封装） |
| idempotency_key | VARCHAR(128) | Y | NULL | 见 UNIQUE |
| concurrency_key | VARCHAR(128) | Y | NULL | 非空时同 `(kind, concurrency_key)` 同时只运行 1 个 |
| fanout_kind | VARCHAR(128) / VARCHAR(128) / TEXT | Y | NULL | 实现细化：`jobs.fan_out` 冻结的目标任务类型；不可递归指向自身 |
| fanout_fire_ms | BIGINT | Y | NULL | 实现细化：对应计划的逻辑触发点，与 `fanout_kind` 同时为空或同时非空 |
| attempts | INTEGER | N | 0 | 认领时 +1；CHECK ≥0 |
| max_attempts | INTEGER | N | 8 | CHECK 1..50 |
| run_at_ms | BIGINT | N | — | 最早可执行时刻（数据库时钟 ms） |
| lease_owner | VARCHAR(64) | Y | NULL | `<replica>/<pid>/<rand>` |
| lease_token | BIGINT | N | 0 | 栅栏：每次认领 +1；完成/心跳 CAS 必须匹配 |
| lease_expires_at_ms | BIGINT | Y | NULL | RUNNING 时非空 |
| timeout_ms | BIGINT | N | 300000 | 单次执行上限，CHECK 1000..3600000 |
| last_error_code | VARCHAR(64) | N | `''` | 机器码：`HANDLER_ERROR`/`TIMEOUT`/`LEASE_EXPIRED`/`PANIC`/`INTERRUPTED`/`UNKNOWN_KIND`/`PERMANENT` |
| last_error | VARCHAR(1024) / VARCHAR(1024) / TEXT | N | `''` | 截断，不含载荷与机密 |
| error_history | JSONB / JSON / TEXT | N | — | 最近 5 次 `[{attempt, at, code, message≤256}]`；MySQL 不设 JSON 默认值，应用必写 `[]` |
| manual_retries | INTEGER | N | 0 | 人工重试次数 |
| correlation_id | VARCHAR(64) | N | `''` | 入队时请求 ID / trace id |
| created_by | BIGINT | Y | NULL | 入队主体；NULL=系统 |
| created_at / updated_at | TIMESTAMPTZ(3) / DATETIME(3) / DATETIME | N | — | UTC |
| started_at / finished_at | 同上 | Y | NULL | 最近一次开始 / 终态时间 |
| version | INTEGER | N | 1 | 管理操作乐观锁；系统迁移也 +1 |

索引与约束：`UNIQUE (tenant_id, kind, idempotency_key)`（三方言 NULL 均不冲突）；`idx_background_jobs_claim (queue, state, priority, run_at_ms, id)`；`idx_background_jobs_lease (state, lease_expires_at_ms)`；`idx_background_jobs_tenant (queue, state, tenant_id)`；`idx_background_jobs_concurrency (kind, concurrency_key, state)`；`idx_background_jobs_admin (state, kind, created_at, id)`；`idx_background_jobs_purge (state, finished_at, id)`。PG 另加部分索引 `... WHERE state='PENDING'`（**本规格提议**：认领热点只扫可执行行；MySQL 无部分索引，复合索引首列 state 已足够）。

**租户分发载荷快照（实现细化，2026-10-16）**：`jobs.fan_out` 的 `payload` 直接保存触发时的原始目标载荷，不再包一层 JSON，以保留完整 64 KiB 的有效输入边界；计划名复用 `concurrency_key`，目标类型与触发点保存在上述两个可空元数据列。元数据非空时约束 `kind='jobs.fan_out'`、`tenant_id=0`、计划名非空、触发点非负；普通任务两列均为空。不得在执行时读取可变计划定义来代替触发时快照。

### 3.2 `background_job_schedules`（C1）

运行态表，**定义在代码**（`Schedule` 声明），启动时由调度 leader 按 `name` upsert；**本规格提议**：避免运维改库造成代码与表分叉，覆盖值走配置键 `jobs.schedules.<name>.*`。

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | BIGINT | N | — | PK 雪花 |
| name | VARCHAR(128) | N | — | UNIQUE，`<module>.<name>` |
| kind / queue | 同 3.1 | N | — | 触发时入队的任务 |
| tenant_scope | VARCHAR(16) | N | `'PLATFORM'` | `PLATFORM`（入队一条，tenant_id=0）/ `PER_TENANT`（扇出：入队一条 `jobs.fan_out`，由其按活跃租户分页入队） |
| interval_ms | BIGINT | Y | NULL | 与 cron 二选一（CHECK 恰一非空），≥1000 |
| cron | VARCHAR(128) | Y | NULL | 5 字段标准 cron |
| timezone | VARCHAR(64) | N | `'UTC'` | IANA |
| payload | JSON | N | — | 固定载荷 |
| enabled | BOOLEAN | N | true | 运维可经 API 暂停 |
| misfire_policy | VARCHAR(16) | N | `'COALESCE'` | `COALESCE`（错过多次只补一次）/ `SKIP` |
| next_run_at_ms | BIGINT | N | — | 下次触发（DB 时钟） |
| last_fired_at_ms | BIGINT | Y | NULL | |
| last_job_id | BIGINT | Y | NULL | |
| definition_hash | CHAR(64) | N | — | 代码定义 SHA-256；变化时重算 `next_run_at_ms` |
| created_at / updated_at | 时间列 | N | — | |
| version | INTEGER | N | 1 | |

索引：`idx_background_job_schedules_due (enabled, next_run_at_ms)`。

### 3.3 `domain_events`（C2，outbox 本体）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | BIGINT | N | — | PK = `event_id`（雪花） |
| tenant_id | BIGINT | N | — | |
| project_id | BIGINT | Y | NULL | 00 §7.1 |
| event_type | VARCHAR(96) | N | — | `<资源>.<过去式>`，须已声明 |
| schema_version | SMALLINT / SMALLINT / INTEGER | N | 1 | `data` 结构版本（CR-09-01） |
| durable | BOOLEAN | N | true | false = 仅实时（`Broadcaster`），不派发给订阅者，保留 1 h |
| stream_seq | BIGINT | Y | NULL | 定序器分配的全局无缺口序号；UNIQUE |
| occurred_at | 时间列 | N | — | 业务发生时间（UTC） |
| actor_kind | VARCHAR(16) | N | — | CHECK ∈ HUMAN, AGENT, MACHINE, CLIENT, SYSTEM |
| actor_id | BIGINT | Y | NULL | |
| authority_source | VARCHAR(160) | N | `''` | 00 §6.3 |
| subject_kind | VARCHAR(64) | N | — | 聚合类型（snake_case 单数，如 `work_item`） |
| subject_id | BIGINT | N | — | 聚合 ID |
| subject_version | BIGINT | N | 0 | 变更后的聚合 `version`；无版本聚合填 0（BR-09-24） |
| topics | JSON | N | — | 实时主题数组，≤8（BR-09-41） |
| realtime_mode | VARCHAR(8) | N | `'SIGNAL'` | `NONE` / `SIGNAL`（不带 data）/ `FULL` |
| data | JSONB / JSON / TEXT | N | — | ≤16 KiB；超出存文件并放引用 |
| correlation_id | VARCHAR(64) | N | `''` | |
| causation_id | BIGINT | Y | NULL | 触发本事件的上游事件 id |
| sequenced_at | 时间列 | Y | NULL | |
| created_at | 时间列 | N | — | |

索引：`UNIQUE (stream_seq)`；未定序扫描：PG / SQLite 部分索引 `(id) WHERE stream_seq IS NULL`，MySQL `(stream_seq, id)`（NULL 位于 B-Tree 首端）；`idx_domain_events_subject (tenant_id, subject_kind, subject_id, stream_seq)`；`idx_domain_events_type (event_type, stream_seq)`；`idx_domain_events_purge (durable, occurred_at)`。

### 3.4 `domain_event_cursors`（C2）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | BIGINT | N | — | PK 雪花 |
| consumer | VARCHAR(128) | N | — | 订阅者名；定序器固定为 `$sequencer` |
| partition_no | SMALLINT / SMALLINT / INTEGER | N | 0 | 0..15 |
| last_seq | BIGINT | N | 0 | 已处理（定序器：已分配）的最大 `stream_seq` |
| fence_token | BIGINT | N | 0 | 每次换主 +1；推进游标 CAS `(last_seq, fence_token)` |
| paused | BOOLEAN | N | false | 运维暂停 |
| updated_at | 时间列 | N | — | |

`UNIQUE (consumer, partition_no)`。

### 3.5 `domain_event_receipts`（C2，消费去重）

| 列 | 类型 | 空 | 说明 |
|---|---|---|---|
| id | BIGINT | N | PK |
| consumer | VARCHAR(128) | N | |
| event_id | BIGINT | N | |
| tenant_id | BIGINT | N | |
| processed_at | 时间列 | N | |

`UNIQUE (consumer, event_id)`；`idx_domain_event_receipts_purge (processed_at)`。02 的 `delivery_consumed_events` 可删除（02 §12 已预留）。

### 3.6 `domain_event_parked`（C2，订阅者级死信与聚合阻塞）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | BIGINT | N | — | PK |
| consumer / partition_no | 同 3.4 | N | — | |
| event_id / stream_seq | BIGINT | N | — | |
| tenant_id | BIGINT | N | — | |
| subject_kind / subject_id | 同 3.3 | N | — | |
| reason | VARCHAR(16) | N | — | `FAILED`（处理失败）/ `BLOCKED`（同聚合前序事件搁置中） |
| state | VARCHAR(16) | N | `'PARKED'` | CHECK ∈ PARKED, REDELIVERING, DELIVERED, DISCARDED |
| attempts | INTEGER | N | 0 | |
| next_attempt_at_ms | BIGINT | Y | NULL | 自动重投时刻 |
| last_error_code / last_error | 同 3.1 | N | `''` | |
| resolved_by | BIGINT | Y | NULL | 人工处理者 |
| resolution_note | VARCHAR(2000) | N | `''` | |
| created_at / updated_at / resolved_at | 时间列 | N/N/Y | — | |
| version | INTEGER | N | 1 | |

`UNIQUE (consumer, event_id)`；`idx_domain_event_parked_subject (consumer, subject_kind, subject_id, state, stream_seq)`；`idx_domain_event_parked_due (state, next_attempt_at_ms)`。

### 3.7 `platform_idempotency_keys`（C7；00 §9 唯一幂等表，模块不得自建）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | BIGINT | N | — | PK |
| tenant_id | BIGINT | N | 0 | |
| subject_kind | VARCHAR(16) | N | — | `PRINCIPAL` / `CONTACT`（01 甲方门户联系人） |
| subject_id | BIGINT | N | — | principal id 或 intake_contact_id |
| operation | VARCHAR(128) | N | — | OpenAPI `operationId` |
| key | VARCHAR(64) | N | — | 请求头原值（≤64，`[\x21-\x7E]`） |
| request_hash | CHAR(64) | N | — | 规范化请求 SHA-256（BR-09-71） |
| state | VARCHAR(16) | N | — | `IN_PROGRESS` / `COMPLETED` |
| status_code | SMALLINT / SMALLINT / INTEGER | Y | NULL | 首次响应 HTTP 状态 |
| response_body | TEXT / MEDIUMTEXT / TEXT | Y | NULL | 首次响应 `respx` 信封 JSON，≤64 KiB；超出只存 `resource_kind/resource_id` 并在重放时重读 |
| resource_kind | VARCHAR(32) | N | `''` | 创建出的资源 |
| resource_id | BIGINT | Y | NULL | |
| created_at | 时间列 | N | — | |
| expires_at | 时间列 | N | — | 默认 created_at + 24 h |

`UNIQUE (tenant_id, subject_kind, subject_id, operation, key)`；`idx_platform_idempotency_keys_expiry (expires_at)`。列名与 00 §9 一致（`state`、`resource_kind`、`resource_id`、`expires_at` 为本规格附加列）。

### 3.8 `notifications`（C5，站内信本体）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | BIGINT | N | — | PK |
| tenant_id | BIGINT | N | — | |
| recipient_principal_id | BIGINT | N | — | |
| type | VARCHAR(96) | N | — | `<module>.<name>`，须已声明 |
| severity | VARCHAR(8) | N | `'INFO'` | `INFO` / `ACTION`（需你处理）/ `URGENT` |
| template_key | VARCHAR(160) | N | — | i18n 键前缀 |
| params | JSON | N | — | 模板参数，≤8 KiB，字符串值 |
| link_path | VARCHAR(512) | N | `''` | 应用内相对路径 |
| subject_kind / subject_id | VARCHAR(64) / BIGINT | Y | NULL | |
| dedupe_key | VARCHAR(160) | Y | NULL | |
| dedupe_slot | VARCHAR(160) | Y | NULL | 未读且在窗口内时 = dedupe_key，否则 NULL（BR-09-63） |
| merge_count | INTEGER | N | 1 | |
| read_at / archived_at | 时间列 | Y | NULL | |
| created_at / updated_at / last_merged_at | 时间列 | N | — | |

`UNIQUE (tenant_id, recipient_principal_id, dedupe_slot)`；`idx_notifications_inbox (recipient_principal_id, tenant_id, archived_at, read_at, id)`；`idx_notifications_purge (read_at)`。

### 3.9 `notification_deliveries`（C5，外部渠道投递）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | BIGINT | N | — | PK |
| tenant_id | BIGINT | N | — | |
| notification_id | BIGINT | Y | NULL | 站内信派生的投递；直发邮件为 NULL |
| channel | VARCHAR(16) | N | — | `EMAIL` / `WEB_PUSH` |
| recipient_principal_id | BIGINT | Y | NULL | |
| recipient_address_hash | CHAR(64) | N | — | 地址 HMAC（限流与检索，不存明文） |
| recipient_address_sealed | TEXT | N | — | C8 封装的地址（发送时解封；主体擦除时清空） |
| message_id | VARCHAR(998) | N | `''` | 邮件 Message-ID（调用方指定或生成） |
| headers | JSON | N | — | 允许的自定义头（BR-09-66） |
| source_ref | VARCHAR(160) | N | `''` | 调用方引用，如 `intake_message:<id>`，随回写事件返回 |
| state | VARCHAR(16) | N | `'PENDING'` | CHECK ∈ PENDING, SENDING, SENT, FAILED, SUPPRESSED |
| suppress_reason | VARCHAR(32) | N | `''` | PREFERENCE / RATE_LIMIT / NO_ADDRESS / CHANNEL_DISABLED / RECIPIENT_INACTIVE |
| job_id | BIGINT | Y | NULL | |
| attempts | INTEGER | N | 0 | |
| last_error | VARCHAR(1024) | N | `''` | |
| sent_at | 时间列 | Y | NULL | |
| created_at / updated_at | 时间列 | N | — | |

`UNIQUE (notification_id, channel)`（NULL 不冲突）；`idx_notification_deliveries_rate (recipient_address_hash, channel, created_at)`；`idx_notification_deliveries_state (state, created_at)`。

### 3.10 `notification_push_subscriptions`（C5，v1.1）

`id`、`principal_id`、`endpoint_hash CHAR(64) UNIQUE`、`endpoint_sealed TEXT`、`p256dh VARCHAR(128)`、`auth_sealed TEXT`（C8）、`user_agent VARCHAR(256)`、`failure_count INT DEFAULT 0`、`last_success_at`、`created_at`、`revoked_at`；`idx (principal_id, revoked_at)`。

### 3.11 `iam_menu_contributions`（C6，`internal/iam/core` 迁移）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | BIGINT | N | — | PK |
| tenant_id | BIGINT | N | — | 组织根（tenant / platform / system） |
| module | VARCHAR(64) | N | — | 贡献模块名 |
| page_key | VARCHAR(128) | N | — | 贡献内稳定键 |
| contribution_version | INTEGER | N | 1 | |
| menu_id | BIGINT | Y | NULL | 播种出的 `iam_menus.id`；NULL=管理员已删除（永不复活） |
| seeded_hash | CHAR(64) | N | — | 播种时字段快照哈希，用于判断是否被管理员改过 |
| seeded_at / updated_at | 时间列 | N | — | |

`UNIQUE (tenant_id, module, page_key)`。另：`iam_menus` 增加 `app VARCHAR(32) NOT NULL DEFAULT 'iam'`（**本规格提议**：菜单项需指向不同前端应用，如 `delivery`；现有行默认 `iam`，行为不变）。同法增加 `domain VARCHAR(16) NOT NULL DEFAULT ''`（D118：顶层域键，取值见 BR-09-80 `MenuDomain`；空串 = 不分域，IAM 应用现有行与系统区菜单保持空值，导航行为不变）与 `space VARCHAR(16) NOT NULL DEFAULT ''`（D118 修订：`TEAM` / `PROJECT`，取值见 BR-09-80 `MenuSpace`；空串 = 不区分，IAM 现有行保持空值）；三方言迁移同 `app` 列，无需回填（已有 delivery 行由 BR-09-82 同步补齐）。

### 3.12 C3 / C4 / C8 / C9

- C3 无表：读 `domain_events`（经 C2 `Tail` 能力，不跨模块读表）。
- C4 沿用 `file_objects.backend_id`（已存在，值 `local`/`s3`）与 `storage_key`；`Put` 返回的 location 为 `s3://<bucket>/<key>?versionId=<v>`。
- C8 无表；密文存使用方自己的列（如 04 `key_sealed TEXT`）。
- C9 沿用 `file_nodes` / `file_resource_bindings`；系统目录为租户根下 `/.system/<module>/<yyyy>/<mm>/`（由使用方 purpose 决定子目录，01 用 `/.intake/<yyyy>/<mm>/` 可配置为其 purpose 的根）。

---

## 4. 状态机

### 4.1 任务 `background_jobs.state`

| 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|
| — | `Enqueue` | kind 已注册；载荷 ≤64 KiB；同 `(tenant,kind,idempotency_key)` 不存在 | PENDING | PG 事务内 `pg_notify`；指标 `jobs_enqueued_total` |
| — | `Enqueue` 撞幂等键 | — | 不变（返回已有 id，`Created=false`） | — |
| PENDING | 认领 | `run_at_ms ≤ db_now`；租户运行数 < 上限；并发键空闲 | RUNNING | attempts+1、lease_token+1、租约 |
| RUNNING | 处理器返回 nil | lease_token 匹配 | SUCCEEDED | finished_at；audit.Do 结束 |
| RUNNING | 返回错误且 attempts < max | lease_token 匹配 | PENDING（run_at=退避） | error_history；`outcome=retry` |
| RUNNING | 返回错误且 attempts ≥ max，或 `Permanent(err)` | lease_token 匹配 | DEAD | 通知（§9 N-09-01） |
| RUNNING | 超时（ctx deadline） | — | 同「返回错误」，code=TIMEOUT | |
| RUNNING | 处理器 panic | — | 同「返回错误」，code=PANIC | 栈写日志不入库 |
| RUNNING | 租约过期（回收器） | `lease_expires_at_ms < db_now` | PENDING 或 DEAD（按 attempts） | code=LEASE_EXPIRED |
| RUNNING | 停机中断（Quiesce 超时取消） | lease_token 匹配 | PENDING（attempts−1，run_at=now） | code=INTERRUPTED，不消耗次数 |
| RUNNING | 并发键被占 | — | PENDING（attempts−1，run_at=now+5 s） | 不消耗次数 |
| DEAD | 人工重试 `POST /platform/jobs/{id}/attempts` | version 匹配 | PENDING（attempts=0，manual_retries+1，run_at=now） | 审计 |
| PENDING / DEAD | 人工丢弃 `PATCH {state:CANCELLED}` | version 匹配；reason 1–2000 | CANCELLED | 审计 |
| SUCCEEDED / DEAD / CANCELLED | 保留期满 | — | 物理删除 | `jobs.purge` |

非法迁移（409 **170420** `jobs.illegal_transition`）：重试 PENDING/RUNNING/SUCCEEDED/CANCELLED；丢弃 RUNNING/SUCCEEDED/CANCELLED。版本不符 409 **170409**；缺 If-Match 428 **170428**。

### 4.2 搁置事件 `domain_event_parked.state`

| 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|
| — | 处理器连续失败 3 次（进程内 1 s/2 s/4 s） | — | PARKED(reason=FAILED) | 分区游标照常推进；`domain_events_parked_total` |
| — | 同订阅者同聚合存在未解决搁置 | — | PARKED(reason=BLOCKED) | 不调用处理器 |
| PARKED(FAILED) | 自动重投到期 / 人工 `POST .../attempts` | attempts < 10（人工无此限） | REDELIVERING → DELIVERED / PARKED | 成功后按 stream_seq 依次释放同聚合 BLOCKED |
| PARKED(BLOCKED) | 前序同聚合事件 DELIVERED/DISCARDED | 自己是该聚合最早的未解决项 | REDELIVERING → … | |
| PARKED | 人工丢弃 `PATCH {state:DISCARDED, reason}` | version 匹配 | DISCARDED | 审计；释放后续 BLOCKED |
| DELIVERED / DISCARDED | — | — | 终态；随事件保留期清理 | |

非法：对 DELIVERED/DISCARDED 重投或丢弃 → 409 **171420**。

### 4.3 外部投递 `notification_deliveries.state`

`PENDING →(job 开始) SENDING →(SMTP 250) SENT`；`SENDING →(4xx 临时错 / 网络) PENDING`（任务退避）；`→(5xx 永久错 / 次数耗尽) FAILED`；`PENDING →(偏好/限流/无地址/渠道关) SUPPRESSED`（不入队）。每次进入 SENT / FAILED / SUPPRESSED 发布 `notification.delivery_state_changed`（含 `source_ref`），这是 01 「`delivery_state` 由 Notifier 回写」的唯一机制。终态不可迁移。

### 4.4 幂等键 `platform_idempotency_keys.state`

`—(事务内模式：与业务同事务插入) COMPLETED`；`—(中间件模式) IN_PROGRESS →(handler 完成且状态可缓存) COMPLETED`；`IN_PROGRESS →(handler 返回 5xx/429/409-version) 删除`；`IN_PROGRESS` 超过 `idempotency.in_progress_timeout`(60 s) 视为可接管（删除后重新执行，见 BR-09-73 风险说明）；`expires_at` 到期 → 物理删除。

### 4.5 周期计划

`enabled=true`：每次 `next_run_at_ms ≤ db_now` → 入队（幂等键 `schedule:<name>:<fire_ms>`）→ 计算下一次（COALESCE：从 `db_now` 起算下一个点）。`PATCH enabled=false` → 不再触发；已入队的任务不受影响。定义从代码移除 → 行保留但 `enabled=false` 且标记 `orphaned`（日志 Warn），不删除以保留历史。

---

## 5. 业务规则与边界情况

### 5.1 任务队列（C1）

- **BR-09-01 同事务入队**：`Queue.Enqueue(ctx, tx, …)` 使用调用方事务；`tx=nil` 时自开事务。业务回滚 → 任务不存在。数据源必须与业务同一 writer（底座单 writer 路由已保证）。
- **BR-09-02 认领（三方言）**：每队列每进程**一个**认领 goroutine，按空闲 worker 槽位数 n 认领：
  - PostgreSQL / MySQL 8.0+：事务内 ① 查已达租户上限的 tenant 集合（`state='RUNNING' GROUP BY tenant_id HAVING COUNT(*) ≥ cap`）；② `SELECT … WHERE queue=? AND state='PENDING' AND run_at_ms ≤ <db_now> AND tenant_id NOT IN (…) ORDER BY priority, run_at_ms, id LIMIT 2n FOR UPDATE SKIP LOCKED`（gorm `clause.Locking{Strength: UPDATE, Options: "SKIP LOCKED"}`，经生成的 query 层，不手写 SQL）；③ 在 Go 内按「每租户运行数 + 本批 < cap」选 ≤n 行；④ `UPDATE … SET state='RUNNING', attempts=attempts+1, lease_token=lease_token+1, … WHERE id IN (…) AND state='PENDING'`；提交。未选中行随提交解锁。
  - SQLite：底座 DSN 默认 `_txlock=immediate`（`pkg/extension/gorm/datasource.go`），事务一开始即持有库级写锁，故**不加** locking 子句（SQLite 不支持 `FOR UPDATE`，与 `internal/files/tree.go` 同法），步骤①–④相同，串行化由写锁保证。**本规格提议**：sqlite DSN 默认补 `_busy_timeout=5000`（未显式配置时），认领遇 `SQLITE_BUSY` 退避 50–200 ms 重试 ≤5 次；SQLite 部署视为单副本。
  - 正确性只依赖第④步的状态 CAS；`SKIP LOCKED` 只是减少争用。故 CI 中 go-mysql-server（忽略锁子句）不影响正确性，真实并发语义由 `make test-mysql8` 与嵌入式 PG 覆盖。
- **BR-09-03 时钟**：租约、可执行时刻一律用**数据库时钟**（`gormx.NowMillisExpr`，与 `dlock` 相同），列为 `*_at_ms BIGINT`。理由：多副本本地时钟不可信，而底座只有毫秒数值形式的跨方言 DB 时钟表达式；`dlock.expires_at`、`file_operations.expires_at`、`scim_targets.sync_after_ms` 已有先例。展示用时间列（created_at 等）仍为标准时间类型。
- **BR-09-04 租约与心跳**：默认租约 60 s，心跳每 20 s（租约/3）`UPDATE SET lease_expires_at_ms = db_now + lease WHERE id=? AND lease_token=? AND state='RUNNING'`；影响 0 行 → 立即取消处理器 ctx（租约已失），其结果丢弃。回收器每 30 s 扫 `state='RUNNING' AND lease_expires_at_ms < db_now`（≤100 行，逐行 CAS）。
- **BR-09-05 退避**：`d = min(backoff_max, backoff_base · 2^(attempt−1))`，实际延迟 = `d/2 + rand[0, d/2]`（equal jitter）。默认 base 5 s、max 1 h、max_attempts 8（累计约 21 min）。处理器可返回 `jobs.RetryAfter(dur, err)` 覆盖（≤24 h）或 `jobs.Permanent(err)` 直接 DEAD。
- **BR-09-06 至少一次**：任务可能重复执行（租约丢失、心跳失败、提交后崩溃）。处理器必须幂等；推荐在业务行上做状态 CAS，或在处理器事务内调用 C7 / C2 receipt。
- **BR-09-07 超时**：每次执行 `context.WithTimeout(timeout_ms)`；处理器忽略 ctx 时超时后仍占用 worker，超过 `timeout + 30 s` 未返回 → 记 TIMEOUT，worker 标记「泄漏」并计 `jobs_leaked_workers` 指标，槽位不回收直至返回（防止无限并发）。
- **BR-09-08 并发键**：`concurrency_key` 非空时处理器开始前 `dlock.TryLock("jobs:ck:<kind>:<key>")`；占用 → PENDING、run_at=now+5 s、attempts−1。用于「同一机器同时只跑一次检测」等。
- **BR-09-09 优先级与公平**：同队列内先 priority 后 run_at；每租户每队列同时 RUNNING ≤ `jobs.max_running_per_tenant`（默认 4）。平台级任务（tenant_id=0）不受此限。洪泛租户只占自己的 4 个槽，不阻塞他人。
- **BR-09-10 周期任务单实例**：调度 leader 由 `dlock("jobs:scheduler", TTL 30 s, auto-refresh, OnLost=停止触发)` 选出；即便两 leader 短暂重叠，幂等键 `schedule:<name>:<fire_ms>` 保证同一触发点只入队一次。执行期互斥用 `concurrency_key=<schedule name>`。
- **BR-09-11 未注册 kind**：入队 → `ErrUnknownKind`（编程错误，不进库）；库中存在未注册 kind（模块被禁用）→ 认领时跳过（查询条件 `kind IN (已注册)`），不 DEAD，保持 PENDING，管理页显示「无处理器」。
- **BR-09-12 停机**：`Quiesce` 停止认领与调度并等待在途任务 ≤ `jobs.drain_timeout`（默认 15 s，须 < `shutdown.timeout` 30 s − `drain_delay` 5 s）；到时取消剩余 ctx，按 4.1「停机中断」释放。处理器**只能依赖 Initialize 完成后的状态**（所有模块先完成 Initialize 再统一 Start，见 `module-lifecycle-redesign.md` §3.5），因 jobs 早于业务模块注册以满足 Depender 顺序。
- **BR-09-13 载荷与上限**：载荷 ≤64 KiB；单租户 PENDING 上限 `jobs.max_pending_per_tenant`（默认 100,000），超出入队返回 `ErrQueueFull`（调用方映射为 503 / 业务码）；批量入队一次 ≤500。
- **BR-09-14 保留**：SUCCEEDED 保留 7 d、DEAD/CANCELLED 30 d，`jobs.purge`（每 10 min，每批 1,000）物理删除。幂等窗口 = 保留期。**本规格提议**：任务是基础设施记录，不受 00 §3「不物理删除」约束（CR-09-06）。

### 5.2 领域事件（C2）

- **BR-09-20 同事务发布**：`Publisher.Publish(ctx, tx, e)` 只插入 `stream_seq=NULL` 的行；PG 同事务 `pg_notify('<prefix>domain_events')`（提交时才送达）。未声明的 event_type → `ErrUnknownEventType`。
- **BR-09-21 定序器**：单实例（`dlock("events:sequencer")`）循环：取 `stream_seq IS NULL ORDER BY id LIMIT 500`，按 BR-09-24 调整顺序，事务内 CAS 推进 `$sequencer` 游标（`WHERE last_seq=? AND fence_token=?`）并为每行写 `stream_seq = last+1…`。由于定序事务串行，**已提交的序号永远连续且按提交可见顺序递增**，读者按 `stream_seq > cursor` 尾随不会漏行。轮询间隔 200 ms，PG 收到 NOTIFY 立即执行；定序后再 `pg_notify('<prefix>domain_events_seq')`。
- **BR-09-22 换主栅栏**：dlock 丢失后旧 leader 的 CAS 因 `fence_token` 变化失败，不会写出重复序号。
- **BR-09-23 派发**：每订阅者 P 个分区（默认 1，≤16），分区 = `fnv32(subject_kind+":"+subject_id) % P`；每分区单实例（`dlock("events:sub:<name>:<p>")`），按 `stream_seq` 顺序读取本分区、`durable=true`、类型匹配的事件（每批 100）。
- **BR-09-24 按聚合保序**：同一聚合的写入因乐观锁/行锁串行提交，故可见顺序即因果顺序；同批内若同聚合多条，定序器按 `subject_version` 升序重排它们所占的位置（防副本时钟偏差使雪花 id 倒序）。同一订阅者同一聚合的事件永远在同一分区内按序处理；**跨聚合不保证顺序**。
- **BR-09-25 两种投递模式**：
  - `Transactional`：处理器在推进游标的同一事务内执行（`Handle(ctx, tx, e)`），DB 副作用与游标原子 → 对 DB 效果恰好一次；处理器须 ≤5 s，禁止外部 I/O。
  - `AtLeastOnce`：处理器在事务外执行，成功后同一事务写 receipt + 推进游标；调用前先查 receipt，已存在则跳过。外部副作用须自身幂等（建议入队任务并以 `event_id` 作幂等键）。
- **BR-09-25b 唯一消费去重**：领域事件的消费去重**只**用 `domain_event_receipts`（订阅模式 Transactional / AtLeastOnce）。业务模块不得再建 `*_consumed_events` / `*_event_receipts` 一类表；业务级唯一键（如「每个验收只建一条返工来件」）仍由业务表唯一约束保证，那不属于事件去重。
- **BR-09-26 失败不堵塞**：进程内重试 3 次（1/2/4 s）仍失败 → 写 PARKED(FAILED)、推进游标；同聚合后续事件写 BLOCKED，不调用处理器；`events.redeliver`（每 5 s）按 `next_attempt_at_ms` 重投（退避 5 s…1 h，自动 ≤10 次），成功后按 `stream_seq` 释放同聚合 BLOCKED。分区最长阻塞 ≈7 s。
- **BR-09-27 新订阅者起点**：`StartFrom: Latest`（默认，游标置为当前最大 seq）或 `Earliest`（从保留窗口最早处）。
- **BR-09-28 保留与清理**：`durable=true` 的事件在 `stream_seq < min(所有游标.last_seq)` 且 `occurred_at < now − 7 d` 且无未解决搁置引用时由 `events.purge`（每小时，每批 1,000）删除；`durable=false` 保留 1 h；receipts 与事件同期清理；未定序行永不清理（定序器停摆告警）。
- **BR-09-29 重放**：`POST /platform/domain-event-subscribers/{name}/replays {from_seq, to_seq?, ignore_receipts}` 或 CLI `backend-api events replay`：在新游标 `<name>#replay-<id>` 上从 from_seq 派发到 to_seq（默认当前最大）后自动删除该游标；`ignore_receipts=true` 时绕过 receipt 检查（处理器仍须幂等）。from_seq 早于保留窗口 → 400 **171401**。不改动主游标。
- **BR-09-30 与审计链 / webhook 的关系**：审计链（`iam/audit`，哈希链、每租户无缺口、`audit.Appender` 同事务）是**法律/取证记录**；领域事件是**集成信号**，不承诺不可篡改、会被清理。需要两者的业务在同一事务内各写一次（00 §7.2 已要求），事件 `data` 不复制审计 `detail`。webhook 继续尾随审计链，本规格不改；webhook 改为消费领域事件属 v2 议题。
- **BR-09-31 上限**：`data` ≤16 KiB、topics ≤8、单事务发布 ≤100 条；发布速率由下游定序吞吐约束（单 leader ≥2,000 事件/s，超出告警）。

### 5.3 实时推送（C3）

- **BR-09-40 端点与认证**：`GET /tenants/{tenant_id}/event-stream?topic=…&topic=…`，经新增 `authz.RegisterTenantMemberStream` 注册（与现有 `RegisterEntityAdapter` 同构：走同一认证、CSRF、账号视图与成员资格中间件，再把 `huma.Context` 交给原始处理器）。凭据：会话 Cookie 或 `Authorization: Bearer`（账号视图隔离标签页只用 Bearer，见前端 `account-view.ts`），故前端用 fetch 流而非 `EventSource`。OAuth 客户端令牌（含 `client_id`）按现有规则 403。
- **BR-09-41 主题语法（00 §9）**：`tenant/{tenant_id}/{kind}/{id}`（单项；`kind` = 事件 `subject.kind`，**单数蛇形**，如 `work_item`、`machine`、`intake`）、`tenant/{tenant_id}/{kinds}`（集合，**复数**，如 `tenant/{t}/intakes`、`tenant/{t}/machines`、`tenant/{t}/work_items`）、`tenant/{tenant_id}/project/{project_id}`（项目扇出）、`tenant/{tenant_id}/inbox/{principal_id}`、以及单项下的命名子流 `tenant/{t}/{kind}/{id}/{channel}`（如 04 `tenant/{t}/machine/{id}/channel`，仅服务端内部订阅，浏览器订阅一律拒绝）。不支持通配符。kind 为 `[a-z][a-z0-9_]{0,63}`（不含连字符）；`tenant_id` 必须等于路径租户。事件默认主题：`tenant/{t}/{subject_kind}/{subject_id}`；有 project_id 再加 `tenant/{t}/project/{p}`；发布者可追加集合主题与收件箱主题（合计 ≤8）。
- **BR-09-42 主题授权**：拥有该 kind 的模块经 `TopicAuthorizerProvider` 注册授权器，且必须在其规格 §7 列出所拥有的主题与所需权限码（`project` 主题由 delivery 注册，需 `project_read`）（实体：读权限；集合：集合读权限，结果按行级权限的「是否有任一可见」判定，行内容由客户端经 REST 重取过滤）。未注册 kind → 拒绝；`inbox` 由 C5 注册，仅 `principal_id == 调用者`。连接时逐主题判定，拒绝的主题在 `ready` 帧列出原因；全部拒绝 → 403 **172403**。
- **BR-09-43 周期复查**：每 60 s 用原始凭据头重新 `Authenticate`、复查成员资格与每个主题；凭据失效 → 发 `reauth_required` 并关闭；主题失权 → 发 `topic_revoked` 并移除。授权器结果可缓存 ≤60 s。
- **BR-09-44 帧内容**：默认 `SIGNAL`（不含 `data`，客户端经 REST 重取，从而复用字段级权限）；事件类型声明 `RealtimeFull` 且其 `data` 不含受字段策略保护字段时才带 `data`（发布者规格负责审定）。`Broadcaster` 发出的仅实时消息总是 FULL（内容即广播本身）。同一事件命中多个主题只发一帧，`topics` 列出命中项。
- **BR-09-45 续传**：帧 `id` = `stream_seq`。重连带 `Last-Event-ID`（或 `?last_event_id=`）：在本副本环形缓冲（最近 10,000 条或 15 min，启动时从库预热）内 → 先补发缺失帧再进入实时；早于缓冲尾 → 发 `resync_required{reason:"cursor_too_old"}` 后从当前继续，客户端重取快照；大于当前最大值 → `resync_required{reason:"cursor_unknown"}`。
- **BR-09-46 心跳与生命周期**：每 15 s 发注释帧 `: hb`；单条流最长 30 min，到期发 `reconnect` + `retry: 1000–3000`（随机）后关闭（强制定期重认证与副本再均衡）。Quiesce 时对所有流发 `reconnect` 并在 1 s 内关闭——SSE 永不空闲，若不先关闭，`http.Server.Shutdown` 会一直等到超时。
- **BR-09-47 限额与背压**：每副本 ≤5,000 条流、每主体每副本 ≤10 条（超出 429 **172429**，`Retry-After: 5`）；每流 ≤50 个主题、查询串 ≤4 KiB（超出 413 **172413**）；每连接发送队列 256 帧，满 → 关闭连接（计 `realtime_dropped_total{reason="slow_consumer"}`），客户端按 Last-Event-ID 恢复；单帧写超时 10 s（`http.ResponseController.SetWriteDeadline`）。
- **BR-09-48 跨副本扇出**：每副本一个 Hub 尾随 C2 `Tail(after_seq)`（轮询 250 ms；PG 上 LISTEN `domain_events_seq` 立即唤醒），写入环形缓冲并按「主题 → 连接」索引分发。**决策**：不引入 Redis（底座 Redis 只属 `authn.shared_security` 且非必选，`appenv.Runtime` 刻意不含 Redis）；数据库尾随天然提供持久续传，NOTIFY 只是唤醒优化。代价：MySQL/SQLite 多 250 ms 轮询延迟，仍满足 ≤1 s。
- **BR-09-49 服务端内部订阅（04）**：`realtime.Hub.Subscribe(ctx, topics) (<-chan Signal, func())` 供进程内代码等待主题信号（如机器长轮询处理器等待 `tenant/{t}/machine/{id}/channel`）；任一副本发布到该主题（`Broadcaster` 或事件），所有副本的订阅者在 ≤ 定序 + 尾随延迟（P95 ≤ 500 ms）内被唤醒。信号只表示「有变化请重读」，不保证每条都送达（channel 缓冲 1，合并）。调用方必须保留兜底轮询（04 已规定 1 s 复查）。
- **BR-09-50 代理**：响应头 `Content-Type: text/event-stream`、`Cache-Control: no-cache, no-transform`、`X-Accel-Buffering: no`；底座 `http.Server` 未设 `WriteTimeout`（已核对 `internal/app/http.go`），反向代理读超时须 > 15 s 心跳（建议 ≥ 60 s）；HTTP/1.1 每源 6 连接限制下多标签页会耗尽连接，部署须启用 HTTP/2（运维文档要求）。
- **BR-09-51 广播限额**：`Broadcaster` 每主题 ≤10 条/s（进程内 100 ms 合并，后者覆盖前者）、载荷 ≤4 KiB；高频日志流须由发布方按 ≥500 ms 批量。

### 5.4 对象存储（C4）

- **BR-09-55 能力**：`s3` 驱动实现 `Driver`、`RangeDriver`、`MultipartDriver`、`CopyDriver`、`IncompleteWriteReclaimer`，`Capabilities{RangeRead, Multipart, ServerCopy: true}`；新增可选能力 `PresignDriver`。不得整文件缓冲或落临时磁盘（`file-management-design.md` §2.1）。
- **BR-09-56 不可变写**：`Put` 使用条件写 `If-None-Match: *`；后端不支持条件写时（配置 `files.s3.conditional_put=false`）先 `StatObject` 再写，并记录竞态风险（key 由服务生成，仅程序缺陷可撞）；已存在 → `ErrExists`。
- **BR-09-57 校验**：`Put` 流式计算 SHA-256 并以 `x-amz-checksum-sha256`（后端支持时）或 `Content-MD5` 由服务端校验；长度不符 → `ErrLength`；multipart ETag 不当整文件摘要（设计文档 §5）。
- **BR-09-58 Multipart**：非末片 ≥5 MiB，默认片 16 MiB，≤10,000 片；`InspectParts` 用 `ListParts` 对账；`Abort` 幂等。租约接管时遵循设计文档 §8「冻结 attempt 先对账」。
- **BR-09-59 重试**：SDK 级重试 ≤3 次（指数 200 ms 起，仅幂等请求与 5xx/网络错误）；单请求超时 60 s；ctx 取消立即传播。
- **BR-09-60 预签名下载**：`files.s3.presign_downloads=true` 时下载端点在授权后返回 302 到 TTL 5 min 的预签名 URL（带 `response-content-disposition` 与 `response-content-type`）；对象清理延迟必须 ≥ 预签名 TTL（设计文档 §8.1 要求）；HTML/SVG 等危险类型仍强制 `attachment`。
- **BR-09-61 加密**：`files.s3.sse` ∈ `none`/`AES256`/`aws:kms`（+`sse_kms_key_id`）；凭据只来自配置/文件，绝不进 filemeta 或日志。
- **BR-09-62 迁移**：`files.read_backends` 列出仍可读的旧后端（如 `local`）；对象按 `file_objects.backend_id` 选驱动读取。迁移任务 `files.object_migrate`（v1.1）：逐对象复制 → 校验长度与 SHA-256 → 同事务 CAS 更新 `backend_id/storage_key/location` → 源对象 7 d 后删除；可中断、可重入（幂等键 = object id）。

### 5.5 通知（C5）

- **BR-09-63 去重合并**：同 `(tenant, recipient, dedupe_key)` 存在未读且 `created_at` 在 `notification.dedupe_window`（10 min）内 → `merge_count+1`、更新 params 与 `last_merged_at`，不新建、不重复外发；窗口过期或已读 → 旧行 `dedupe_slot=NULL`，插入新行。唯一约束 `(tenant, recipient, dedupe_slot)` 防并发重复，冲突时重读合并。
- **BR-09-64 渠道选择**：站内信**必达**；外部渠道 = 调用方请求渠道 ∩ 偏好钩子返回渠道（08 提供 `PreferenceResolver`；未提供时默认：URGENT/ACTION → 站内+邮件，INFO → 仅站内）。
- **BR-09-65 限流**：每地址每小时邮件 ≤20、推送 ≤60；超出 → SUPPRESSED(RATE_LIMIT)，站内信不受影响；`URGENT` 不豁免但单独计数上限 ×2。直发邮件（`SendEmail`）每租户每小时 ≤500（可配置）。
- **BR-09-66 邮件头（01）**：`SendEmail` 允许调用方设置 `Message-ID`（须 `<…@domain>` 且 domain = `notification.email.message_id_domain`）、`In-Reply-To`、`References`（≤20 个 id，≤998 字符/行折行）、`Reply-To`（≤1 个地址，用于入站开口回复地址）、`Auto-Submitted`（`auto-generated`/`auto-replied`）、`List-Unsubscribe`；其余头一律拒绝（防头注入：值中 CR/LF → `ErrInvalidHeader`）。`From` 显示名默认部署显示名（`GET /deployment` 同源配置），地址来自配置，调用方可覆盖显示名（如团队品牌，01 BR-01-40）。
- **BR-09-67 投递回写**：每个投递终态发布领域事件 `notification.delivery_state_changed {delivery_id, channel, state, source_ref, message_id, error_code?}`（durable，`subject_kind=notification_delivery`）；调用方以订阅者消费，按 `source_ref` 回写（01 `delivery_state`）。SMTP 250 = SENT（「已交给 MTA」，不等于已读/已送达收件箱）。
- **BR-09-68 模板与语言**：模板 = 发布模块 i18n 中 `<template_key>.title|body|email_subject|email_text|email_html`，三语齐全（缺键启动时校验失败）；用 `text/template`（HTML 用 `html/template` 转义）以命名参数渲染，`missingkey=error`。收件人语言：`RecipientDirectory.Contact().Locale` → 租户默认 → `notification.default_locale`（`en-US`）。
- **BR-09-69 收件人状态**：主体停用/擦除 → SUPPRESSED(RECIPIENT_INACTIVE)；擦除时 `principallifecycle` 调用 C5 清除其投递地址密文与站内信 params（与 `iam_notification_outbox` 擦除守卫同理）。
- **BR-09-70 与 `iam_notification_outbox` 关系**：authn 现有加密 outbox（安全通知 → 外部 webhook）保持不变；其轮询循环按 §7.3 迁移到 C1。是否改走 C5 SMTP 属后续决策（§12 Q-09-05）。

### 5.6 幂等（C7）

- **BR-09-71 范围与哈希**：键作用域 = `(tenant, subject_kind, subject_id, operation, key)`（`operation` = OpenAPI operationId）；`request_hash = SHA256(method + "\n" + 路由模板 + "\n" + 规范化路径参数 + "\n" + 规范化 JSON 体)`（规范化 = 解码后按 Go 结构体字段序重新编码）。
- **BR-09-72 结果**：同键同哈希且 COMPLETED → 重放首次 `status_code` 与 `response_body`，响应头 `Idempotent-Replayed: true`；同键不同哈希 → 422 **176425** `idempotency.key_reused`；IN_PROGRESS → 409 **176409** `idempotency.in_progress` + `Retry-After: 1`（与 00 §9 一致）；键不合法 → 400 **176400**。只缓存 2xx 与确定性 4xx（400/403/404/422 及业务 409 非版本冲突），不缓存 5xx/429/版本冲突 409。
- **BR-09-73 两种模式**：事务内 `idempotency.Do(ctx, tx, …)`：键行与业务同事务插入，并发重复请求在唯一索引上等待后读到已提交结果（PG/MySQL 行锁、SQLite 写锁）——强保证，命令型 POST 必用。中间件 `idempotency.Middleware`：IN_PROGRESS 行先独立提交，用于无单一 DB 事务的操作；进程在执行后、标记 COMPLETED 前崩溃时，60 s 后可被接管而重复执行——弱保证，须由业务 CAS 兜底，规格中必须注明使用该模式的操作。

### 5.7 密钥封装（C8）

- **BR-09-75**：`Sealer.Seal(purpose, associatedData, plain) (envelope string)`、`Open(...)`、`NeedsRewrap(envelope)`；基于 `keyringx.Keyring`（AEAD、每 purpose 派生子密钥、信封带密钥 ID）；purpose 形如 `<module>.<field>`，关联数据必须含行 ID 与租户 ID（防密文挪用）。轮换：配置新 active key + previous keys，`backend-api secrets rewrap --purpose …` 维护命令（复用 `keyringx.Rewrap`）。未配置密钥 → 模块 `Initialize` 失败（不静默明文）。明文不入日志/审计/事件。

### 5.8 租户业务文件（C9）

- **BR-09-77**：`TenantResourceFiles.StageOn(ctx, db, tenantID, purpose, reader, size, deadline)` 流式写入最终后端（不经 API 中转盘）→ `CreateOn(ctx, tx, TenantResourceInput{TenantID, Purpose, ResourceKind, ResourceID, Name, MIME, ExpiresAt?})` 在调用方事务内发布节点并绑定；`Rebind`（改绑到另一资源）、`Release`。purpose 由模块在 `Initialize` 注册（含目录、单文件上限 ≤100 MiB、允许 MIME）；目录对普通成员只读、仅系统写（`file_node_permissions` 系统 deny）。未绑定且超 deadline 的暂存对象由既有 files 清理任务回收。

### 5.9 菜单贡献（C6）

- **BR-09-80 声明**：`MenuPage{Key, Scope(TENANT|PLATFORM|SYSTEM), App, Domain, Space, Group, Route, Icon, SortOrder, PermissionCode, TranslationKey, Version}`；Domain 为顶层域键（D118 修订），取值集合 `MenuDomain` = `HOME`（工作台）、`CHAT`（会话）、`PROJECTS`（协作：当前项目）、`TEAM`（团队）、`GOVERNANCE`（治理），顺序即域标签顺序，标签 i18n key `menu.domain.<key 小写>`（本模块内枚举，按 00 §3 不进 00 §5；D118 初版的 `COLLAB` 已删除，从未发布）；Space 为菜单空间（D118 修订；字段不叫 Scope，因为 `Scope` 已表示 TENANT / PLATFORM / SYSTEM 组织层级），取值集合 `MenuSpace` = `TEAM`（团队级页面，Route 不含 `:projectID`）、`PROJECT`（只在项目空间内渲染，Route 必须以 `/t/:tenantID/p/:projectID` 开头，外壳以当前项目填入，08 §8.6），本模块内枚举；`App=delivery` 的贡献 Domain 与 Space 必填且须在集合内，`Space=PROJECT` 只允许 `Domain=PROJECTS`，Space 与 Route 是否含 `:projectID` 必须一致，其他应用 Domain 与 Space 必须为空；Group 为同模块声明的分组（无 Route）或核心既有分组标签，在同一 `(Domain, Space)` 内归类（不同 Domain 或 Space 可有同名 Group，如 `PROJECTS` 的项目空间与 `GOVERNANCE` 都可有「设置」类分组）；PermissionCode 必须已在 authz 目录注册且 scope 匹配；Route 在同 `(scope, app)` 内唯一；违者启动失败（**175400**，日志列出字段）。
- **BR-09-81 播种**：新租户：核心页 + 全部激活模块贡献，同事务写 `iam_menu_contributions`；`iam_menus.domain`、`iam_menus.space` 写入声明的 Domain 与 Space。
- **BR-09-82 升级**：任务 `iam_core.menu_contributions_sync`（启动后由 leader 入队 + 每日一次，按租户分页 100）：无台账 → 若同 route 不存在则插入并写台账，存在则只写台账（menu_id 指向已有行）；台账 version < 声明 version → 当前行哈希 == `seeded_hash`（管理员未改）才更新字段与哈希，否则跳过并记 `customized`；台账 `menu_id` 指向的行已被删除 → 置 NULL，**永不复活**（解决 `core/service.go` 注释所述「删除与从未创建无法区分」）；代码中已移除的贡献 → 对应菜单置 `disabled`（不删除）。Domain 与 Space 属于 `seeded_hash` 覆盖的字段：贡献改变 Domain / Space / Group / Route（如 D118 修订重新归位）时必须 Version+1，未被管理员改过的行随同步更新；被改过的行（`customized`）保留管理员值，若其 `domain` / `space` 为空或不在集合内（含 D118 初版写入的 `COLLAB`），`EffectiveMenus` 回退到贡献声明的 Domain / Space（外壳据此归入域标签与空间，保证升级前租户也能正确落位）。
- **BR-09-83 门控不可削弱**：`EffectiveMenus` 的路由→权限表由「核心默认 + 激活贡献」构成；管理员改动权限码不能弱化贡献页的守卫（与现有内置页规则一致）。模块被禁用时其权限未注册 → 菜单不可见。

### 5.10 D120 增补（过期上下文写入的幂等例外与新密钥用途）

- **BR-09-84 C7 对 `interrupted` 的例外**：业务处理返回 409 且响应 `data.state="interrupted"`（00 §9 过期上下文写入；02 120469、03 130474、05 145021、12 126061）时，幂等辅助**不保存**该结果：事务内模式下键行随业务事务一起回滚；中间件模式下删除本次的 IN_PROGRESS 行（不写 COMPLETED）。因此调用方可用**同一个** `Idempotency-Key` 携带新的 `seen_seq` 重试。`request_hash` 计算时排除请求体顶层字段 `seen_seq`（00 §9）：重试与原请求哈希相同，不会得到 176425；若首次已成功，以任意 `seen_seq` 重放都返回首次结果。其他 409 / 4xx / 5xx 仍按 BR-09-72 / 73 原规则处理（不改变既有语义）。
- **BR-09-85 C8 新 purpose**：fleet 在 `Initialize` 登记 `fleet.tool_credential`（05 团队工具目录中 MCP 服务器的凭据）与 `fleet.wake_webhook_secret`（05 Agent 唤醒 webhook 秘密，v1.1）；关联数据含行 ID 与租户 ID（BR-09-75）。`fleet.tool_credential` 的明文只在 05 MCP 中继处理单次请求时于进程内存中短暂存在，**绝不**下发机器、不入日志 / 审计 / 事件 / 错误详情；轮换由 05 以新信封整体替换，密钥轮换沿用 `backend-api secrets rewrap --purpose fleet.tool_credential`。本规格不新增 C8 能力或表。

---

## 6. 接口契约

### 6.1 放置与依赖方向

| 包 | 内容 | 依赖 |
|---|---|---|
| `internal/infra/jobs` | 模块、`Queue`、`Handler`、`Schedule`、`HandlerProvider`、`ScheduleProvider`、`Catalog`、`Singleton`；`model/ query/ dbgen/ sql/ i18n/` | dlock、guid、infra/audit、metrics（collector）、iam authz（管理 HTTP） |
| `internal/infra/events` | 模块、`Envelope`、`Publisher`、`Broadcaster`、`Subscriber`、`SubscriberProvider`、`TypeProvider`、`Tail`；表 | jobs、dlock、guid |
| `internal/infra/realtime` | 模块、Hub、SSE 处理器、`TopicAuthorizer`、`TopicAuthorizerProvider`、`Hub.Subscribe` | events、iam authz/core（成员资格） |
| `internal/infra/idempotency` | 模块、`Store`、`Do`、`Middleware`；表 | guid |
| `internal/infra/secrets` | 模块、`Sealer` | `pkg/extension/keyringx` |
| `internal/notification` | 模块、`Notifier`、`Channel`(SPI)、`PreferenceResolver`、`RecipientDirectory`；表；HTTP 收件箱 | jobs、events、secrets、iam identity |
| `internal/files` | `s3.go`（驱动）、`tenant_resource.go`（C9）、`PresignDriver` | — |
| `internal/iam/core` | `MenuPage`、`MenuContributor`、`MenuSink`；台账表 | — |
| `pkg/extension/gorm` | `Notify(tx, channel)`、`Listen(ctx, router, channel) (<-chan struct{}, error)`（PG 实现：专用 pgx 连接 `WaitForNotification`；其他方言 Notify no-op、Listen 返回永不触发的通道） | 查询门禁豁免的原语包，唯一允许出现 `LISTEN`/`pg_notify` 的地方 |
| `pkg/extension/authz` | `RegisterTenantMemberStream` | — |
| `packages/ui` | `lib/realtime.ts`、`hooks/use-realtime.ts`、`components/realtime-status.tsx`、`components/notification-bell.tsx` | — |

跨模块接口一律在**提供方根包**定义，使用方 `module.Resolve[T]` 并在 `ModuleDependencies()` 声明（`backend-module` 规约 §一、§三）。`pkg/` 不引用 `internal/`（`TestSharedPackagesDoNotImportInternal`）。

### 6.2 Go 公共接口

```go
// ---- internal/infra/jobs ----
type Queue interface { // 00 §8 JobQueue
    Enqueue(ctx context.Context, tx *gorm.DB, req EnqueueRequest) (EnqueueResult, error)
    EnqueueBatch(ctx context.Context, tx *gorm.DB, reqs []EnqueueRequest) ([]EnqueueResult, error) // ≤500
    Cancel(ctx context.Context, tx *gorm.DB, tenantID guid.ID, kind, idempotencyKey string) (bool, error) // 仅 PENDING
}
type EnqueueRequest struct {
    TenantID       guid.ID
    Kind           string        // 已注册
    Payload        any           // JSON 对象，≤64 KiB
    IdempotencyKey string        // 可空，≤128
    ConcurrencyKey string        // 可空
    RunAt          time.Time     // 零值=立即
    Delay          time.Duration // 与 RunAt 二选一
    Priority       int           // 0=处理器默认
    CreatedBy      guid.ID
}
type EnqueueResult struct { JobID guid.ID; Created bool }
type Job struct {
    ID, TenantID, CreatedBy    guid.ID
    Kind, Queue, CorrelationID string
    Payload                    json.RawMessage
    Attempt, MaxAttempts       int
}
type Handler struct {
    Kind                    string        // "<module>.<name>"，须以模块名为前缀
    Queue                   string        // 默认 "default"
    Priority                int           // 默认 5
    Timeout                 time.Duration // 默认 5m，≤1h
    MaxAttempts             int           // 默认 8，≤50
    BackoffBase, BackoffMax time.Duration // 默认 5s / 1h
    Audit                   bool          // 默认 true：每次执行经 audit.Run(audit.KindJob)
    Run                     func(ctx context.Context, job Job) error
}
func Permanent(err error) error
func RetryAfter(d time.Duration, err error) error
type Schedule struct {
    Name, Kind  string
    Interval    time.Duration // 与 Cron 二选一
    Cron        string        // 5 字段
    Timezone    string        // 默认 UTC
    TenantScope TenantScope   // PLATFORM | PER_TENANT
    Payload     any
    Misfire     Misfire       // COALESCE | SKIP
}
type HandlerProvider interface { module.Module; JobHandlers() []Handler }
type ScheduleProvider interface { module.Module; JobSchedules() []Schedule }
type Catalog interface { // internal/app 在 Wire 后遍历激活集调用；jobs.Initialize 封口
    AddHandlers(owner string, hs ...Handler) error
    AddSchedules(owner string, ss ...Schedule) error
}
// Singleton 运行跨副本单实例循环（dlock TTL 30s + auto-refresh；OnLost 取消 ctx），供调度器、定序器、派发分区复用。
type Singleton interface { Run(ctx context.Context, lockName string, loop func(ctx context.Context) error) }

// ---- internal/infra/events ----
type Actor struct { Kind string; ID guid.ID; AuthoritySource string } // 00 §4 三元组
type Subject struct { Kind string; ID guid.ID }
type Event struct {
    TenantID, ProjectID guid.ID
    Type                string
    Actor               Actor
    Subject             Subject
    SubjectVersion      int64
    Data                any
    ExtraTopics         []string // 集合/收件箱/子流主题；默认主题自动生成
    CorrelationID       string
    CausationID         guid.ID
}
type Envelope struct { // 00 §7.1 + CR-09-01
    EventID guid.ID; EventType string; SchemaVersion int; StreamSeq int64
    TenantID, ProjectID guid.ID; OccurredAt time.Time
    Actor Actor; Subject Subject; SubjectVersion int64
    Topics []string; Data json.RawMessage; CorrelationID string; CausationID guid.ID
}
type Publisher interface { // 00 §8 OutboxPublisher
    Publish(ctx context.Context, tx *gorm.DB, e Event) (guid.ID, error)
}
type Broadcaster interface { // 00 §8 RealtimeBroadcaster：仅实时，不派发订阅者，保留 1h
    Broadcast(ctx context.Context, tx *gorm.DB, m Message) error // tx 可空
}
type Message struct { TenantID guid.ID; Topic, Type string; Data any } // ≤4 KiB
type TypeSpec struct { Name string; SchemaVersion int; SubjectKind string; Realtime RealtimeMode }
type TypeProvider interface { module.Module; EventTypes() []TypeSpec }
type Subscriber struct {
    Name       string       // "<module>.<purpose>"
    EventTypes []string     // 精确名或 "<resource>.*"
    Mode       DeliveryMode // Transactional | AtLeastOnce
    Partitions int          // 1..16
    StartFrom  StartFrom    // Latest | Earliest
    Handle     func(ctx context.Context, tx *gorm.DB, e Envelope) error // AtLeastOnce 时 tx=nil
}
type SubscriberProvider interface { module.Module; EventSubscribers() []Subscriber }
type Tail interface { // 供 realtime 使用，避免跨模块读表
    After(ctx context.Context, afterSeq int64, limit int) ([]Envelope, error)
    MaxSeq(ctx context.Context) (int64, error)
    Wake() <-chan struct{} // PG NOTIFY；其他方言永不触发
}
func ActorFromContext(ctx context.Context, kind, authoritySource string) Actor

// ---- internal/infra/realtime ----
type TopicAuthorizer struct {
    Kind      string // 实体 kind 或集合名（"work_item"、"intakes"、"machines"）
    Authorize func(ctx context.Context, claims *authn.Claims, tenantID, id guid.ID /*集合为 0*/, channel string) (bool, error)
}
type TopicAuthorizerProvider interface { module.Module; TopicAuthorizers() []TopicAuthorizer }
type Signal struct { Topic string; StreamSeq int64; EventType string }
type Hub interface { // 服务端内部订阅（04 跨副本唤醒长轮询）
    Subscribe(ctx context.Context, topics []string) (<-chan Signal, func())
}

// ---- internal/infra/idempotency ----
type Key struct { TenantID guid.ID; SubjectKind string; SubjectID guid.ID; Operation, Value string; RequestHash [32]byte }
type Result struct { StatusCode int; Body json.RawMessage; ResourceKind string; ResourceID guid.ID }
type Store interface {
    // Do 在调用方 tx 内占用键；replayed=true 时返回首次结果且不调用 fn。
    Do(ctx context.Context, tx *gorm.DB, key Key, fn func(tx *gorm.DB) (Result, error)) (res Result, replayed bool, err error)
    Middleware(operation string) func(huma.Context, func(huma.Context)) // 弱保证模式（BR-09-73）
}
// 编码失败必须返回错误并停止执行，不能用零哈希继续查键。
func HashRequest(method, routeTemplate string, pathParams map[string]string, body any) ([32]byte, error)

// ---- internal/infra/secrets ----
type Sealer interface { // 04 CR-04-07 SecretSealer
    Seal(purpose string, associatedData, plain []byte) (string, error)
    Open(purpose string, associatedData []byte, envelope string) ([]byte, error)
    NeedsRewrap(envelope string) bool
}

// ---- internal/notification ----
type Notifier interface { // 00 §8 Notifier
    Notify(ctx context.Context, tx *gorm.DB, n Notification) ([]guid.ID, error)
    SendEmail(ctx context.Context, tx *gorm.DB, m EmailMessage) (deliveryID guid.ID, err error) // 系统外地址（01 甲方）
}
type Notification struct {
    TenantID    guid.ID
    Recipients  []guid.ID // 1..200
    Type        string    // 已声明 "<module>.<name>"
    Severity    Severity  // INFO | ACTION | URGENT
    TemplateKey string
    Params      map[string]string
    LinkPath    string
    Subject     *events.Subject
    DedupeKey   string
    Channels    []ChannelName // 请求的外部渠道，经偏好过滤
}
type EmailMessage struct {
    TenantID      guid.ID
    To            []mail.Address // 1..20
    FromName      string         // 空=部署显示名
    ReplyTo       *mail.Address
    MessageID     string         // 空=生成 <snowflake@message_id_domain>
    InReplyTo     string
    References    []string       // ≤20
    AutoSubmitted string         // "" | auto-generated | auto-replied
    TemplateKey   string; Locale string; Params map[string]string
    Attachments   []EmailAttachment // files 节点引用，合计 ≤10 MiB
    SourceRef     string            // ≤160，随回写事件返回
}
type Channel interface { // SPI：smtp（MVP）、webpush（v1.1）
    Name() ChannelName
    Deliver(ctx context.Context, d Delivery) error // jobs.Permanent = 不可重试
}
type PreferenceResolver interface { // 08 提供；缺省 BR-09-64
    Channels(ctx context.Context, tenantID, principalID guid.ID, notificationType string, sev Severity) ([]ChannelName, error)
}
type RecipientDirectory interface { // identity 提供（新增，§12）
    Contact(ctx context.Context, principalID guid.ID) (Contact, error) // {Email, Locale, DisplayName, Active}
}
type TypeProvider interface { module.Module; NotificationTypes() []TypeSpec } // 类型、默认严重度、模板键

// ---- internal/files ----
type PresignDriver interface {
    PresignGet(ctx context.Context, key string, ttl time.Duration, filename, contentType string) (string, error)
}
type TenantResourceFiles interface { // C9，与 AccountResourceFiles 同形
    RegisterPurpose(p TenantPurpose) error
    StageOn(ctx context.Context, db *gorm.DB, tenantID guid.ID, purpose string, r io.Reader, size int64, deadline time.Time) (*PreparedTenantObject, error)
    CreateOn(ctx context.Context, tx *gorm.DB, in TenantResourceInput, now time.Time) (*ResourceBindingView, error)
    Rebind(ctx context.Context, tx *gorm.DB, bindingID guid.ID, resourceKind string, resourceID guid.ID, expectedVersion int64, now time.Time) error
    Release(ctx context.Context, tx *gorm.DB, bindingID guid.ID, now time.Time) error
    Open(ctx context.Context, db *gorm.DB, tenantID, nodeID guid.ID) (io.ReadCloser, FileInfo, error)
}

// ---- internal/iam/core ----
type MenuPage struct {
    Key string; Scope MenuScope; App string; Domain MenuDomain; Space MenuSpace; Group string
    Route, Icon, PermissionCode, TranslationKey string; SortOrder, Version int
}
type MenuDomain string // D118 修订；IAM / 系统区菜单为空串
const (
    MenuDomainHome MenuDomain = "HOME"; MenuDomainChat MenuDomain = "CHAT"; MenuDomainProjects MenuDomain = "PROJECTS"
    MenuDomainTeam MenuDomain = "TEAM"; MenuDomainGovernance MenuDomain = "GOVERNANCE"
)
type MenuSpace string // D118 修订；PROJECT 项只在项目空间渲染，Route 含 :projectID；IAM / 系统区菜单为空串
const (
    MenuSpaceTeam MenuSpace = "TEAM"; MenuSpaceProject MenuSpace = "PROJECT"
)
type MenuContributor interface { module.Module; MenuPages() []MenuPage }
type MenuSink interface { ContributeMenus(owner string, pages ...MenuPage) error } // core 实现；files 现有 SystemMenus.Contribute 迁移至此
```

聚合（`internal/app/contributions.go`，**本规格提议**，与 `collectMetrics` 同法，在 `Wire` 后、`Initialize` 前）：遍历激活集，把 `HandlerProvider`/`ScheduleProvider` → `jobs.Catalog`，`events.TypeProvider`/`SubscriberProvider` → events，`TopicAuthorizerProvider` → realtime，`notification.TypeProvider` → notification，`MenuContributor` → `core.MenuSink`。重复名、前缀与模块名不符、引用未注册权限码 → 启动失败并点名模块。这样只有一种贡献机制（files 的 `SystemMenus.Contribute` 随 C6 改为 `MenuContributor`）。

### 6.3 HTTP 接口

全部经 `authz.Register*`；响应 `respx`；列表 `pkg/query`；乐观锁 `ETag`/`If-Match`（缺失 428，不符 409）；OpenAPI tag = 最终发布路径首段（00 §9），见 tag 列。平台路径在 `/platform/**`。

| # | 方法 路径 | tag | 权限码 / 守卫 | 请求 | 响应 data | 错误 | 幂等/锁 |
|---|---|---|---|---|---|---|---|
| H1 | GET `/platform/jobs` | platform | `system_job_view` / RegisterPlatform | `state,queue,kind,tenant_id=eq:`；`created_at,finished_at=gt/gte/lt/lte`；`priority=eq/lte`；排序 `created_at,run_at,priority,id` | `Job[]`（无 payload，含 `has_handler`） | 400 | — |
| H2 | GET `/platform/jobs/{job_id}` | platform | `system_job_view` | — | `Job` + `payload` + `error_history` | 404 170404 | ETag |
| H3 | POST `/platform/jobs/{job_id}/attempts` | platform | `system_job_manage` | `{reason ≤2000}` | `Job`（PENDING） | 409 170420/170409；428 170428 | If-Match |
| H4 | PATCH `/platform/jobs/{job_id}` | platform | `system_job_manage` | `{state:"CANCELLED", reason* 1–2000}` | `Job` | 409 170420/170409；428 | If-Match |
| H5 | POST `/platform/job-attempts` | platform | `system_job_manage` | `{job_ids[1..100], reason}` | `{retried[], skipped[{id, code}]}` | 400 170400 | 逐项 CAS |
| H6 | GET `/platform/job-schedules` | platform | `system_job_view` | — | `Schedule[]`（下次触发、上次任务、orphaned） | — | — |
| H7 | PATCH `/platform/job-schedules/{schedule_id}` | platform | `system_job_manage` | `{enabled}` | `Schedule` | 404/409 | If-Match |
| H8 | POST `/platform/job-schedules/{schedule_id}/runs` | platform | `system_job_manage` | — | `{job_id}` | 409 170421（已有运行中） | Idempotency-Key |
| H9 | GET `/platform/domain-event-subscribers` | platform | `system_event_view` | — | `[{name, mode, partitions, cursors[{p,last_seq,lag}], parked, blocked, paused}]` | — | — |
| H10 | GET `/platform/domain-events` | platform | `system_event_view` | `event_type,tenant_id,subject_kind,subject_id=eq:`；`stream_seq=gt/lt`；`occurred_at` 区间 | `Envelope[]`（列表不含 data） | 400 | — |
| H11 | GET `/platform/domain-events/{event_id}` | platform | `system_event_view`（`audit.Annotate` 强制记录读取） | — | `Envelope` | 404 171404 | — |
| H12 | GET `/platform/domain-event-subscribers/{subscriber}/parked-events` | platform | `system_event_view` | `state,reason,subject_kind=eq:` | `Parked[]` | 404 | — |
| H13 | POST `/platform/parked-events/{parked_id}/attempts` | platform | `system_event_manage` | `{reason}` | `Parked` | 409 171420/171409 | If-Match |
| H14 | PATCH `/platform/parked-events/{parked_id}` | platform | `system_event_manage` | `{state:"DISCARDED", reason*}` | `Parked` | 409 171420/171409 | If-Match |
| H15 | POST `/platform/domain-event-subscribers/{subscriber}/replays` | platform | `system_event_manage` + step-up（purpose `system_event_replay`，登记于 08 危险操作登记表） | `{from_seq*, to_seq?, ignore_receipts}` | `{replay_cursor, from_seq, to_seq}` | 400 171401；409 171422（重放进行中） | Idempotency-Key |
| H16 | PATCH `/platform/domain-event-subscribers/{subscriber}` | platform | `system_event_manage` | `{paused}` | 订阅者 | 404 | — |
| H17 | GET `/tenants/{tenant_id}/event-stream` | tenants | 租户成员 / `RegisterTenantMemberStream` + 逐主题授权 | `topic`(1..50)、`last_event_id?`；头 `Last-Event-ID` | `text/event-stream` | 400 172400；403 172403；413 172413；429 172429；503 172503 | — |
| H18 | GET `/principals/{principal_id}/notifications` | principals | 本人 / RegisterPrincipalAuthenticated | `tenant_id,read,severity=eq:`、`created_at` 区间；排序 `created_at,id` | `Notification[]`（按请求语言渲染） | 403 | — |
| H19 | GET `/principals/{principal_id}/notification-summary` | principals | 本人 | `tenant_id=eq:` | `{unread, action_required, urgent, by_tenant[]}` | — | — |
| H20 | PATCH `/notifications/{notification_id}` | notifications | 本人（属主解析器） | `{read?, archived?}` | `Notification` | 404 174404 | 幂等 |
| H21 | POST `/principals/{principal_id}/notification-reads` | principals | 本人 | `{tenant_id*, up_to_id*}` | `{marked}` | 400 174400 | 幂等 |
| H22 | POST `/principals/{principal_id}/push-subscriptions`（v1.1） | principals | 本人，会话认证 | `{endpoint, keys{p256dh, auth}}` | `{id}` | 400 174400 | 同 endpoint 幂等 |
| H23 | DELETE `/push-subscriptions/{subscription_id}`（v1.1） | push-subscriptions | 本人 | — | 204 | 404 | 幂等 |
| H24 | GET `/push-config`（v1.1） | push-config | 已认证 | — | `{vapid_public_key}` | 404（未启用） | — |

**本规格注册的权限码**（系统级，`authz` Action.Scope=`platform`，即系统控制台；不进租户角色，00 §13 F10）：

| 权限码 | 含义 | 注册模块 |
|---|---|---|
| `system_job_view` | 查看后台任务与计划任务 | jobs |
| `system_job_manage` | 重试、取消任务；启停计划、手动触发 | jobs |
| `system_event_view` | 查看领域事件、订阅者游标与搁置事件（读取事件详情强制审计） | events |
| `system_event_manage` | 重投、丢弃搁置事件；暂停订阅者；从序号重放（另需 step-up `system_event_replay`） | events |

H17 帧示例：

```
id: 81234
event: work_item.state_changed
data: {"v":1,"event_id":"…","stream_seq":81234,"topics":["tenant/7/work_item/9"],"subject":{"kind":"work_item","id":"9"},"subject_version":12,"occurred_at":"…","actor":{"kind":"AGENT","id":"…"}}

: hb
```

控制事件：`ready {cursor, heartbeat_ms:15000, accepted[], denied[{topic, reason}]}`、`resync_required {reason, cursor}`、`topic_revoked {topic, reason}`、`reauth_required {}`、`reconnect {retry_ms}`。`Broadcaster` 消息为 `event: <type>`，`data.payload` 携带内容。

### 6.4 配置键（`AppConfig` 新组；**secret** = `secret:"true"`）

| 键 | 默认 | 说明 |
|---|---|---|
| `jobs.workers_enabled` | true | false=本副本只入队不执行 |
| `jobs.queues` | `{default: 8, notifications: 4}` | `map[queue]concurrency`，每副本 |
| `jobs.poll_interval` / `jobs.claim_batch` | 1s / 10 | PG 另有 NOTIFY 唤醒 |
| `jobs.lease` | 60s | 心跳 = lease/3 |
| `jobs.max_running_per_tenant` / `jobs.max_pending_per_tenant` | 4 / 100000 | |
| `jobs.default_timeout` / `jobs.max_timeout` | 5m / 1h | |
| `jobs.default_max_attempts` / `jobs.backoff_base` / `jobs.backoff_max` | 8 / 5s / 1h | |
| `jobs.drain_timeout` | 15s | 必须 < `shutdown.timeout − shutdown.drain_delay` |
| `jobs.retention_succeeded` / `jobs.retention_dead` | 168h / 720h | |
| `jobs.scheduler_enabled` | true | |
| `jobs.schedules.<name>.enabled` / `.interval` | — | 覆盖代码定义 |
| `events.sequencer_interval` / `events.sequencer_batch` | 200ms / 500 | |
| `events.dispatch_interval` / `events.dispatch_batch` | 250ms / 100 | |
| `events.handler_timeout` | 30s | Transactional 强制 ≤5s |
| `events.redeliver_max_attempts` | 10 | |
| `events.retention` / `events.ephemeral_retention` | 168h / 1h | |
| `events.max_data_bytes` | 16384 | |
| `events.subscribers.<name>.partitions` | 1 | 1..16 |
| `events.notify` | true | 仅 PG 生效 |
| `realtime.enabled` | true | Enabler |
| `realtime.heartbeat` | 15s | |
| `realtime.max_streams` / `realtime.max_streams_per_principal` / `realtime.max_topics` | 5000 / 10 / 50 | 每副本 |
| `realtime.ring_size` / `realtime.ring_window` | 10000 / 15m | |
| `realtime.poll_interval` / `realtime.recheck_interval` / `realtime.max_stream_lifetime` | 250ms / 60s / 30m | |
| `realtime.send_queue` / `realtime.write_timeout` | 256 / 10s | |
| `idempotency.ttl` / `idempotency.in_progress_timeout` / `idempotency.max_response_bytes` | 24h / 60s / 65536 | |
| `secrets.active_key` / `secrets.active_key_file` | `""` **secret** / `""` | base64 32 字节，二选一必填 |
| `secrets.previous_keys` / `secrets.previous_key_files` | `[]` **secret** / `[]` | 轮换期旧钥 |
| `files.backend` / `files.read_backends` | `local` / `[]` | 已有 `files.Config.Backend` |
| `files.s3.endpoint` / `region` / `bucket` | `""` | bucket 必填 |
| `files.s3.access_key` / `access_key_file` | `""` | |
| `files.s3.secret_key` / `secret_key_file` / `session_token` | `""` **secret** | |
| `files.s3.use_path_style` / `tls` / `ca_file` | false / true / `""` | MinIO 设 path style |
| `files.s3.key_prefix` | `""` | `^[a-z0-9/_-]{0,64}$` |
| `files.s3.part_size` / `max_retries` / `request_timeout` | 16 MiB / 3 / 60s | part ≥5 MiB |
| `files.s3.conditional_put` | true | |
| `files.s3.sse` / `sse_kms_key_id` | `none` / `""` | `none`/`AES256`/`aws:kms` |
| `files.s3.presign_downloads` / `presign_ttl` | false / 5m | |
| `notification.default_locale` / `dedupe_window` / `retention_read` | `en-US` / 10m / 2160h | |
| `notification.email.enabled` | false | |
| `notification.email.smtp_host` / `smtp_port` / `tls_mode` | `""` / 587 / `starttls` | `starttls`/`implicit`/`none`（none 仅开发） |
| `notification.email.username` / `password` / `password_file` | `""` / `""` **secret** / `""` | |
| `notification.email.from_address` / `from_name` / `message_id_domain` | `""` | from_name 空=部署显示名 |
| `notification.email.timeout` | 10s | |
| `notification.email.per_address_hourly` / `per_tenant_hourly` | 20 / 500 | |
| `notification.webpush.enabled`（v1.1） | false | |
| `notification.webpush.vapid_public_key` / `vapid_private_key` / `vapid_subject` | `""` / `""` **secret** / `""` | |

### 6.5 生命周期接线（`internal/app/bootstrap.go`）

注册顺序（**本规格提议**）：`… audit, databasemonitor, deployment, secrets, jobs, events, idempotency, …(IAM 模块)…, notification, realtime, …(业务模块)…, todo, health`。理由：IAM 与业务模块 `Resolve` 并声明依赖 jobs/events/secrets（依赖必须先注册）；realtime 需要 IAM 的 authz Registrar 与成员资格服务；`Quiesce` 逆序执行时业务模块先停接新工作，随后 realtime 关流、jobs/events 等待在途处理，全部早于 HTTP 排空与任何 `Stop`（`internal/app/shutdown.go`：Quiesce → waitForDrain → HTTP Shutdown → Stop）。

| 模块 | 能力接口 | Start | Quiesce | Stop | Health |
|---|---|---|---|---|---|
| jobs | Depender、Provider/Injector、MigrationSourceProvider、Initializer（封口 Catalog）、Starter、Quiescer、Stopper、HealthChecker、PrometheusCollectorModule、HTTPRegistrar | 认领器（每队列 1）、worker 池、回收器、调度 Singleton | 停认领/调度，等待在途 ≤drain_timeout，取消并释放 | 释放 dlock、关闭 LISTEN | writer 可用 |
| events | 同上（无 Enabler） | 定序 Singleton、各分区派发 Singleton | 当前事件处理完即退出 | 释放锁 | writer 可用；定序滞后只告警不摘流量 |
| realtime | Enabler、Provider（Hub）、Injector、Starter、Quiescer、HTTPRegistrar、Collector | Hub 尾随、预热环形缓冲 | 全部流发 `reconnect` 并 ≤1 s 关闭 | 停尾随 | — |
| idempotency / secrets | Provider；idempotency 有 Migration | — | — | — | — |
| notification | Depender、Provider/Injector、Migration、Initializer（模板三语校验）、HTTPRegistrar、HandlerProvider、SubscriberProvider、TopicAuthorizerProvider（inbox）、Collector | — | — | — | SMTP 不入 health（抖动不摘流量，走告警） |

---

## 7. 后台任务与事件

### 7.1 本规格自带的任务与单实例循环

| 名称 | 类型 | 触发/频率 | 互斥 | 重试 |
|---|---|---|---|---|
| `jobs.scheduler` | Singleton | 每 1 s | dlock `jobs:scheduler` | 循环内继续 |
| `jobs.reaper` | 认领器内 | 每 30 s | 行 CAS | — |
| `jobs.purge` | Schedule | 每 10 min | concurrency_key | 默认 |
| `jobs.fan_out` | Handler | PER_TENANT 计划触发 | — | 默认；按活跃租户分页 500 入队，幂等键 `<schedule>:<fire_ms>:<tenant>` |
| `jobs.queue_stats` | Singleton | 每 15 s | dlock | 写 gauge |
| `events.sequencer` | Singleton | 200 ms / NOTIFY | dlock + fence_token | 循环内继续 |
| `events.dispatch:<sub>:<p>` | Singleton | 250 ms / 定序 NOTIFY | dlock + fence_token | BR-09-26 |
| `events.redeliver` | Schedule | 每 5 s | concurrency_key | 自身 3 次 |
| `events.purge` / `idempotency.purge` | Schedule | 每 1 h | concurrency_key | 默认 |
| `notification.deliver` | Handler（queue `notifications`） | 入队 | 幂等键 `delivery:<id>` | 8 次，base 30 s；SMTP 5xx → Permanent |
| `notification.purge` | Schedule | 每日 03:00 UTC | concurrency_key | 默认 |
| `iam_core.menu_contributions_sync` | Handler + Schedule | 启动后 + 每日 | concurrency_key | 默认 |
| `files.object_migrate`（v1.1） | Handler | 维护命令入队 | 幂等键 object id | 8 次 |

### 7.2 发布 / 消费的事件

| 事件 | 发布者 | 主题 | 消费者 |
|---|---|---|---|
| `notification.created`（**仅 SSE 实时消息**，经 `Broadcaster` 发出，非持久、不进 outbox，不是 00 §7 领域事件） | notification | `tenant/{t}/inbox/{principal}` | 前端收件箱 |
| `notification.delivery_state_changed` | notification（持久，CR-09-07） | `tenant/{t}/notification_delivery/{id}` | 01 intake 回写 `delivery_state`；其他按 `source_ref` 前缀过滤 |
| `job.dead_lettered` | jobs（持久，`realtime_mode=NONE`） | — | notification（§9 N-09-01） |

### 7.3 现有 ticker 工作者迁移计划

| # | 工作者（位置） | 现状问题 | 迁移目标 | 顺序 | 风险 | 估时 |
|---|---|---|---|---|---|---|
| M1 | files 清理（`internal/files/module.go` cleanupLoop，每副本每分钟 4 步） | 多副本重复执行 | 4 个 PLATFORM 计划 `files.operation_expiry`、`files.binding_expiry`、`files.orphan_purge`、`files.account_object_collect`（1 min，concurrency_key，游标进载荷） | 1（试点） | 低：各步已幂等分页 | 1.5 d |
| M2 | SCIM 同步（`internal/iam/provisioning` syncLoop） | **每副本都跑**，同目标可能并发推送 | 计划 `provisioning.scim_sync`（间隔=`sync_interval`，0=不注册）扇出 `provisioning.scim_sync_target`（concurrency_key=target） | 2 | 中：需验证外部端点限流 | 2 d |
| M3 | 审计归档（`internal/iam/audit/archive_worker.go`） | 每副本扫全租户，靠 `ErrArchiveBusy` 互斥 | 计划 `iam_audit.archive_sweep`（1 min）入队每租户 `iam_audit.archive_run`（concurrency_key=tenant，超时=`archive.timeout`） | 3 | 中高：哈希链完整性；保留原归档表与状态机，只换驱动循环 | 2 d |
| M4 | authn 通知 outbox（`internal/iam/authn/recovery.go`） | 自带轮询/认领/退避 | 写 outbox 行同事务入队 `authn.notification_deliver{outbox_id}`（幂等键=outbox id）；处理器复用 `deliverNotification`；表、加密、擦除守卫不变 | 4 | 中：安全邮件不可丢；过渡期两路径以行状态 CAS 防重发 | 2 d |
| M5 | webhook（`internal/iam/webhook` FanOut + DeliverDue） | 每副本轮询 | `webhook.fan_out` 改 Singleton（5 s）、`webhook.deliver_due` 改计划（5 s）；投递表不变 | 5（v1.1） | 低 | 1 d |
| — | databasemonitor、infra/audit recorder flush、dlock refresh、authn 开发用 miniredis 时钟 | 必须每进程运行或进程内批处理 | **不迁移** | — | — | — |

每项：加新路径与开关 `<module>.use_jobs`（默认 false）→ 双跑一个发布周期 → 默认 true → 删除旧 ticker 及其指标与测试。新增架构门禁 `TestBackgroundLoopsUseJobs`：`internal/` 下的 `time.NewTicker` 须登记于冻结台账（与查询门禁相同「只减不增」机制），上表「不迁移」项入台账。

---

## 8. 页面规格

平台运维页位于 IAM 控制台系统区（`apps/frontend-web/apps/web`，`/system/...`），菜单经 C6 以 `Scope=SYSTEM` 贡献到「基础管理」分组；组件用 `@workspace/ui` auto-table / crud 与语义 Token。

### P1 后台任务 `/system/jobs`
- **目的**：发现并处置失败任务。**入口**：系统菜单「后台任务」；N-09-01 通知链接带筛选。
- **布局**：4 个计数卡（运行中 / 待执行 / 24 h 死信 / 最老待执行时长）；筛选条（状态、队列、类型、租户 ID、创建时间）；表格（ID、类型、租户、状态、优先级、次数 n/max、下次/最近执行、最后错误码）；行点击开 P2 抽屉。
- **组件与校验**：状态徽标 PENDING=中性、RUNNING=`--info`、SUCCEEDED=`--success`、DEAD=`--destructive`、CANCELLED=虚线灰（图标 + 文字）；勾选 ≤100 行批量重试；丢弃理由 1–2000 必填。
- **状态**：加载骨架 10 行；空态「没有符合条件的任务」+〔清除筛选〕；错误态〔重试〕；无权限「需要权限 system_job_view」；离线顶部条「连接已断开，数据可能过期」。
- **交互**：〔重试〕仅 DEAD；〔丢弃〕仅 PENDING/DEAD；409 提示「任务已被他人处理」并刷新该行；`has_handler=false` 行显示「无处理器（模块未启用）」。
- **实时**：不订阅 SSE；计数卡每 10 s 刷新，可暂停。**移动端**：表格转卡片，隐藏批量。**文案**：错误码附释义（i18n `jobs.error_code.*`）。

### P2 任务详情（抽屉）
全部字段 + 载荷 JSON 只读（等宽，>16 KiB 折叠）+ 错误历史时间线（最近 5 次）+ 按 correlation_id 跳转操作审计；按钮同 P1。

### P3 计划任务 `/system/job-schedules`
表格：名称、类型、间隔/cron（人类可读「每 5 分钟」）、租户范围、启用开关（If-Match）、下次触发、上次任务（链 P2）、孤儿标记；〔立即运行〕（运行中禁用）。空态说明「计划由模块代码声明，此处只能启停」。

### P4 领域事件 `/system/domain-events`
- 选项卡「订阅者」：名称、模式、分区游标与滞后、搁置/阻塞数、暂停开关；〔从序号重放〕弹窗（from_seq 必填、to_seq 可选、「忽略去重记录」红色语义复选框，提交前底座 step-up）。
- 选项卡「搁置事件」：订阅者、原因（失败/阻塞）、聚合、次数、最后错误；〔重投〕〔丢弃(理由)〕；阻塞行显示「等待 #seq」。
- 选项卡「事件浏览」：按类型/租户/聚合/序号查询；详情显示信封与 data。
- 状态同 P1；无权限说明 `system_event_view`。

### P5 实时连接状态 `RealtimeStatus`（`packages/ui`）
顶栏右侧：`live` 不显示；`connecting` 脉冲点「正在连接」；`polling` 琥珀点「实时连接不可用，每 10 秒刷新」；`offline` 灰虚线点「离线」。`role="status"` 仅状态变化时播报一次；`prefers-reduced-motion` 关脉冲；触控目标 ≥44px。

### P6 通知铃铛 `NotificationBell`（`packages/ui`；通知中心页与分类语义归 08）
未读数（>99 显示 99+）；下拉最近 20 条（标题、相对时间、严重度图标、合并「×3」）；〔全部标为已读〕；点击条目 PATCH read 并跳转 `link_path`；订阅 `tenant/{t}/inbox/{me}`，收到 `notification.created` 重取摘要。空态「暂无通知」。

### 前端实时客户端

```ts
// packages/ui/src/lib/realtime.ts
export type RealtimeStatus = "connecting" | "live" | "polling" | "offline"
export interface RealtimeEvent { id: string; type: string; topics: string[]; subject?: { kind: string; id: string }; subjectVersion?: number; payload?: unknown; occurredAt: string }
export interface RealtimeClientOptions {
  url: (tenantID: string, topics: string[]) => string
  request: () => Pick<RequestInit, "headers" | "credentials"> // IAM 应用传 accountViewRequest("GET")
}
export function createRealtimeClient(opts: RealtimeClientOptions): RealtimeClient
// packages/ui/src/hooks/use-realtime.ts
export function RealtimeProvider(props: { client: RealtimeClient; tenantID: string; children: React.ReactNode }): React.JSX.Element
export function useRealtime(
  topic: string | string[] | null,
  options: { onEvent?: (e: RealtimeEvent) => void; eventTypes?: string[]; onResync?: () => void; poll?: () => unknown; pollIntervalMs?: number },
): { status: RealtimeStatus; lastEventAt?: number }
```

行为：每标签页一条 fetch 流（`ReadableStream` 解析 SSE；不用 `EventSource`，因其不能带 `Authorization`）；主题集合变化 300 ms 去抖后带 `Last-Event-ID` 重连；退避 1/2/4…30 s ±20%；60 s 内连续失败 3 次 → `polling`（调用 `poll`，默认 10 s）并每 30 s 尝试恢复；收到 `resync_required` 或从 polling 回到 live → 调 `onResync`；页面隐藏 >5 min 断开、可见时重连；401 → 应用既有会话过期流程（账号视图派发 `account-view-expired`）。

---

## 9. 通知

| ID | 事件 | 接收人 | 渠道 | 模板键 / 文案 | 去重与合并 |
|---|---|---|---|---|---|
| N-09-01 | `job.dead_lettered` | 系统根内持 `system_job_manage` 的主体 | 站内（ACTION）+ 邮件（按偏好） | `jobs.notify.dead_lettered`：「后台任务 {{.kind}} 已失败 {{.attempts}} 次并停止重试」 | `dedupe_key=jobs.dead:<kind>`，10 min 合并为「×n」 |
| N-09-02 | 搁置事件新增（FAILED） | 持 `system_event_manage` 的主体 | 站内（ACTION） | `events.notify.parked`：「订阅者 {{.subscriber}} 有 {{.count}} 个事件处理失败」 | `events.parked:<subscriber>`，10 min |
| N-09-03 | 定序滞后 >60 s、认领持续失败 5 min、SMTP 连续失败 10 次、S3 不可用 | 运维 | **Prometheus 告警**（`docs/iam-alerts.yml` 新增规则），不发站内信 | — | 告警系统抑制 |

文案只含错误码与 ≤128 字符摘要，不含载荷。业务通知由各发布规格定义，本规格只提供机制。

---

## 10. 验收标准

测试层：unit = 包内 SQLite 真库；integration = `tests/`（嵌入式 PostgreSQL、go-mysql-server、SQLite）；mysql8 = `make test-mysql8`（真实 MySQL 8.4 并发）；s3 = `make test-s3`（MinIO 容器，新增）；lifecycle = `tests/lifecycle`；e2e = Playwright。

### 10.1 任务队列

| AC | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-09-01 | unit | US-09-01, BR-09-01 | **G** 业务事务内 Enqueue **W** 事务回滚 **T** 表中无该任务；提交时则 PENDING 且 `Created=true` |
| AC-09-02 | integration | BR-09-01 | **G** 同 `(tenant, kind, key)` 已有任务 **W** 再次 Enqueue（含 10 并发） **T** 只有一行，全部返回同 `JobID`，仅首个 `Created=true` |
| AC-09-03 | integration + mysql8 | BR-09-02 | **G** 1,000 个 PENDING、4 副本各 8 worker **W** 全部执行 **T** 每任务 SUCCEEDED 恰一次（处理器计数 = 1,000，无重复），PG/MySQL 均成立 |
| AC-09-04 | unit | BR-09-02 | **G** SQLite、2 个认领 goroutine **W** 并发认领 200 个任务 **T** 无重复认领；遇 BUSY 自动重试，无错误外抛 |
| AC-09-05 | unit | BR-09-04 | **G** RUNNING 任务心跳停止 **W** 超过租约 **T** 回收器置 PENDING、code=LEASE_EXPIRED；原 worker 随后完成时 CAS 0 行，结果丢弃并计 `jobs_lease_lost_total` |
| AC-09-06 | unit | BR-09-05 | **G** base 5 s、max 1 h **W** 第 n 次失败 **T** run_at−now ∈ [d/2, d]，d=min(1h, 5s·2^(n−1))；第 8 次失败 → DEAD 并发布 `job.dead_lettered` |
| AC-09-07 | unit | BR-09-05 | **W** 处理器返回 `Permanent(err)` **T** 首次即 DEAD；返回 `RetryAfter(2m)` → run_at=now+2 m |
| AC-09-08 | unit | BR-09-07 | **G** timeout 1 s 的处理器阻塞 **W** 执行 **T** ctx 1 s 取消，记 TIMEOUT；忽略 ctx 的处理器 31 s 后计 `jobs_leaked_workers` 且该槽不被复用 |
| AC-09-09 | unit | BR-09-09 | **G** cap=4，租户 A 有 100 个、B 有 1 个待执行 **W** 单副本 8 worker 认领 **T** A 同时运行 ≤4，B 的任务在首轮被认领 |
| AC-09-10 | unit | BR-09-09 | **G** priority 1 与 9 同时待执行 **T** 先执行 1 |
| AC-09-11 | integration | BR-09-10 | **G** 3 副本、间隔 1 s 计划 **W** 运行 10 s **T** 入队 10±1 个任务，无同一 fire_ms 重复；杀掉 leader 后 ≤35 s 内另一副本接管 |
| AC-09-12 | unit | BR-09-08 | **G** 两个同 concurrency_key 任务 **T** 永不同时 RUNNING；被让行者 attempts 不增加 |
| AC-09-13 | lifecycle | BR-09-12 | **G** 处理器执行 5 s **W** 发停机信号 **T** Quiesce 等待其完成后 Stop；处理器执行 60 s 时 15 s 后被取消，任务回到 PENDING、attempts 不变、code=INTERRUPTED，进程 30 s 内退出 |
| AC-09-14 | unit | BR-09-11 | **G** 库中存在未注册 kind 的 PENDING **T** 不被认领、不 DEAD；管理列表 `has_handler=false` |
| AC-09-15 | unit | BR-09-13 | **W** 载荷 65 KiB / 未注册 kind / 租户待执行超限 **T** 分别 `ErrPayloadTooLarge` / `ErrUnknownKind` / `ErrQueueFull`，无行写入 |
| AC-09-16 | integration | US-09-04, §4.1 | **G** DEAD 任务 version=3 **W** 持 `system_job_manage` 者 POST attempts（If-Match 3）**T** PENDING、attempts=0、manual_retries=1；操作审计记录 |
| AC-09-17 | integration | §4.1 | **W** 对 SUCCEEDED 任务重试 / 对 RUNNING 丢弃 **T** 409 170420，状态不变 |
| AC-09-18 | integration | §4.1 | **W** 两人用同一 version 同时丢弃与重试 **T** 一个成功，另一个 409 170409；缺 If-Match → 428 170428 |
| AC-09-19 | integration | §6.3 | **G** 仅有 `system_job_view` **W** POST attempts **T** 403，任务不变；租户管理员访问 `/platform/jobs` → 403 |
| AC-09-20 | unit | BR-09-14 | **G** SUCCEEDED 8 d 前完成 **W** `jobs.purge` **T** 删除；DEAD 8 d 前的保留 |
| AC-09-21 | unit | audit | **W** `Audit=true` 的任务执行 **T** operation_logs 出现 entry=job、operation=kind、resource=background_job/<id>、metadata.attempt |

### 10.2 领域事件

| AC | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-09-22 | unit | US-09-05, BR-09-20 | **W** 业务事务发布后回滚 **T** 无事件；提交后 stream_seq 在 ≤400 ms 内被分配 |
| AC-09-23 | integration | BR-09-21 | **G** 20 个并发事务各发布 50 条（含延迟提交） **T** 已分配 stream_seq 连续无缺口、无重复；任一时刻读者按 `> cursor` 尾随不漏任何已提交事件 |
| AC-09-24 | integration | BR-09-22 | **G** 定序 leader 丢锁但继续运行 **W** 新 leader 接管 **T** 旧 leader CAS 失败，不写出重复或跳号 |
| AC-09-25 | unit | BR-09-24 | **G** 同聚合 v5、v6 两事件同批且 id 顺序颠倒 **T** stream_seq(v5) < stream_seq(v6)；订阅者按 v5→v6 处理 |
| AC-09-26 | unit | BR-09-25 | **G** Transactional 订阅者处理器写业务行后游标推进前进程崩溃（模拟回滚） **W** 重启 **T** 业务行与游标都未推进，重放后恰一次 |
| AC-09-27 | unit | BR-09-25 | **G** AtLeastOnce 订阅者处理成功、写 receipt 前崩溃 **W** 重投 **T** 处理器再被调用一次（至少一次）；receipt 已存在时不调用 |
| AC-09-28 | unit | BR-09-26 | **G** 聚合 X 的 e1 处理失败 3 次 **T** e1 PARKED(FAILED)、后续 X 的 e2/e3 BLOCKED 不调用处理器、聚合 Y 的事件照常处理；e1 重投成功后 e2、e3 按序处理 |
| AC-09-29 | integration | §4.2 | **W** 丢弃 PARKED e1（理由必填）**T** DISCARDED，e2 释放；再次丢弃 → 409 171420 |
| AC-09-30 | unit | BR-09-27 | **G** 已有 10,000 条事件 **W** 新订阅者 Latest 上线 **T** 只收到上线后的事件；Earliest 则从保留窗口最早处开始 |
| AC-09-31 | integration | BR-09-29 | **W** 从 seq 100 重放到 200、`ignore_receipts=true` **T** 101 次调用（按序），主游标不变，重放游标完成后删除；from_seq 早于保留 → 400 171401 |
| AC-09-32 | unit | BR-09-28 | **G** 所有游标已过 seq 500、事件 8 d 前 **W** purge **T** ≤500 且无搁置引用者删除；未定序行保留 |
| AC-09-33 | unit | BR-09-20/31 | **W** 发布未声明类型 / data 17 KiB / topics 9 个 **T** 分别报错且业务事务可感知回滚 |
| AC-09-34 | integration | 02 §3.19 | **G** delivery 订阅者 AtLeastOnce **W** 同一 event_id 投递两次 **T** 业务效果一次（receipt 去重），满足删除 `delivery_consumed_events` 的条件 |

### 10.3 实时推送

| AC | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-09-35 | integration | US-09-08, §1.4 | **G** 2 副本、浏览器连 A，变更写入 B **W** 提交事件 **T** 浏览器 ≤1 s 收到帧（PG+NOTIFY P95 ≤300 ms，MySQL P95 ≤700 ms，各 200 次测量） |
| AC-09-36 | integration | BR-09-42 | **G** 成员 M 无工作项 W 读权限 **W** 订阅 `tenant/t/work_item/W` 与可读主题 **T** `ready.denied` 含 W（reason=forbidden），只收到可读主题帧；全部主题被拒 → 403 172403 |
| AC-09-37 | integration | BR-09-41 | **W** 订阅他租户主题 / 非法格式 / 他人 inbox **T** 分别拒绝（tenant_mismatch / 400 172400 / forbidden） |
| AC-09-38 | integration | BR-09-43 | **G** 已连接 **W** 撤销其角色 / 注销会话 **T** ≤60 s 内收到 `topic_revoked` / `reauth_required` 且后者连接关闭 |
| AC-09-39 | integration | BR-09-45 | **G** 断线期间产生 30 条 **W** 带 Last-Event-ID 重连 **T** 先补发 30 条（按 seq），无重复无遗漏；Last-Event-ID 早于缓冲 → `resync_required{cursor_too_old}` |
| AC-09-40 | integration | BR-09-45 | **G** 副本重启 **W** 客户端带 5 min 前的 id 重连 **T** 由预热缓冲补发，不触发 resync |
| AC-09-41 | integration | BR-09-46 | **T** 空闲流每 15±1 s 收到 `: hb`；30 min 后收到 `reconnect` 并关闭 |
| AC-09-42 | lifecycle | BR-09-46 | **G** 100 条活动流 **W** 停机 **T** 1 s 内全部收到 `reconnect` 并关闭，HTTP Shutdown 不因流等待超时 |
| AC-09-43 | integration | BR-09-47 | **W** 同主体第 11 条流 / 51 个主题 **T** 429 172429（`Retry-After`）/ 413 172413 |
| AC-09-44 | unit | BR-09-47 | **G** 客户端不读取 **W** 发送队列满 256 **T** 连接关闭，计 `realtime_dropped_total{reason="slow_consumer"}`，其他连接不受影响 |
| AC-09-45 | integration | BR-09-49 | **G** 副本 B 上进程内 `Hub.Subscribe(tenant/t/machines/M/channel)` **W** 副本 A Broadcast 到该主题 **T** B 的通道 ≤500 ms（P95）收到 Signal |
| AC-09-46 | integration | BR-09-44 | **G** SIGNAL 类型事件 **T** 帧无 data；FULL 类型才含 data |
| AC-09-47 | integration | BR-09-40 | **G** 账号视图标签页（Bearer）**W** 连接 **T** 成功；OAuth 客户端令牌 → 403 |
| AC-09-48 | e2e | §8 前端 | **G** 页面使用 `useRealtime` **W** 断开网络 3 次 **T** 状态变为 polling 并每 10 s 调用 poll；恢复后回到 live 并调用 onResync |

### 10.4 对象存储

| AC | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-09-49 | s3 | BR-09-55 | **G** MinIO **W** 运行 files 驱动共享合同测试（长度、取消、超时、范围读、multipart 接管、清理）**T** 与 local 驱动同一套用例全部通过 |
| AC-09-50 | s3 | BR-09-56 | **W** 对已存在 key Put **T** `ErrExists`，原对象不变 |
| AC-09-51 | s3 | BR-09-57 | **W** 声明长度 10 MiB 实传 9 MiB **T** `ErrLength`，无对象残留（multipart 已 Abort） |
| AC-09-52 | s3 | — | **W** 上传 1 GiB **T** 进程 RSS 增量 <64 MiB，无临时磁盘文件 |
| AC-09-53 | s3 | BR-09-60 | **G** presign 开启 **W** 授权用户下载 **T** 302 到 TTL ≤5 min 的 URL，`Content-Disposition` 正确；无权用户 403 且不返回 URL |
| AC-09-54 | s3 | BR-09-62 | **G** `read_backends=[local]`、backend=s3 **T** 旧对象可读、新对象写 S3；迁移任务后旧对象 backend_id=s3 且 SHA-256 一致；中途杀进程再运行不重复复制已完成对象 |

### 10.5 通知

| AC | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-09-55 | unit | US-09-11, BR-09-64 | **G** 未提供偏好钩子 **W** Notify(URGENT) **T** 站内信 1 条 + EMAIL 投递 1 条（同事务入队 `notification.deliver`）；INFO 仅站内 |
| AC-09-56 | unit | BR-09-63 | **W** 同 dedupe_key 10 min 内 3 次（含并发）**T** 1 行 merge_count=3，外部渠道只投递 1 次；读后再发 → 新行 |
| AC-09-57 | integration | BR-09-66 | **W** SendEmail 带 Message-ID/In-Reply-To/References/Reply-To/Auto-Submitted **T** SMTP 测试服务器收到的报文头与之逐字一致；值含 CRLF → `ErrInvalidHeader`；自定义 `Bcc` 头 → 拒绝 |
| AC-09-58 | integration | BR-09-67 | **G** SMTP 先 451 后 250 **T** 第二次 SENT，发布 `notification.delivery_state_changed{state:SENT, source_ref}`；550 → FAILED 且只尝试一次 |
| AC-09-59 | unit | BR-09-65 | **W** 同地址 1 h 内第 21 封 **T** SUPPRESSED(RATE_LIMIT) 并发布回写事件；站内信照常 |
| AC-09-60 | unit | BR-09-68 | **G** 模板缺 ms-MY 键 **W** 启动 **T** Initialize 失败并点名键；收件人 locale=zh-CN → 中文渲染；参数缺失 → 渲染错误进 FAILED 而非发出半截邮件 |
| AC-09-61 | integration | BR-09-69 | **G** 主体被擦除 **T** 其投递地址密文清空、未发投递 SUPPRESSED(RECIPIENT_INACTIVE) |
| AC-09-62 | integration | H18/H20 | **W** 用户 A 读取/标记用户 B 的通知 **T** 403 / 404，无变更 |

### 10.6 菜单、幂等、密钥、租户文件

| AC | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-09-63 | unit | BR-09-81 | **G** 模块贡献 3 个租户页 **W** 新建租户 **T** 菜单含 3 页 + 台账 3 行 |
| AC-09-64 | integration | BR-09-82 | **G** 老租户无台账 **W** 同步任务 **T** 补齐 3 页；再次运行无变化（幂等） |
| AC-09-65 | integration | BR-09-82 | **G** 管理员删除贡献页 P、修改 Q 的标签 **W** 贡献 version+1 后同步 **T** P 不复活；Q 不被覆盖（customized）；未改动的 R 被更新 |
| AC-09-66 | unit | BR-09-80 | **W** 贡献引用未注册权限码 / 重复 route **T** 启动失败并点名模块与字段 |
| AC-09-67 | integration | BR-09-83 | **G** 管理员把贡献页权限码改为空 **T** EffectiveMenus 仍按贡献声明的权限码过滤 |
| AC-09-68 | integration | US-09-14, BR-09-72 | **W** 同键同体重放 POST **T** 返回首次状态与 body，头 `Idempotent-Replayed: true`，业务行仅 1；同键不同体 → 422 176425；首次仍在处理时并发重放 → 409 176409 |
| AC-09-69 | integration + mysql8 | BR-09-73 | **W** 同键 10 个并发请求（事务内模式）**T** 业务执行 1 次，其余 9 个返回同一结果 |
| AC-09-70 | unit | BR-09-72 | **G** 首次请求 500 **W** 重放 **T** 重新执行（未缓存）；首次 409 版本冲突亦不缓存 |
| AC-09-71 | unit | BR-09-71 | **G** 01 门户联系人（subject_kind=CONTACT）与主体使用相同键值 **T** 互不冲突 |
| AC-09-72 | unit | BR-09-75 | **W** 用行 A 的关联数据打开行 B 的密文 / 轮换后旧钥密文 **T** 失败 / 成功且 `NeedsRewrap=true`；未配置密钥 → 模块 Initialize 失败 |
| AC-09-73 | integration | BR-09-77 | **W** 服务端 StageOn + CreateOn 后业务事务回滚 **T** 不出现可见节点；暂存对象在 deadline 后被清理；提交则节点位于系统目录且普通成员只读 |
| AC-09-81 | integration | BR-09-80, BR-09-81, BR-09-82 | **G** delivery 贡献 `chat`（Domain=CHAT, Space=TEAM）、`defects`（Domain=PROJECTS, Space=TEAM, Group=跨项目, Route `/t/:tenantID/defects`）、`p-defects`（Domain=PROJECTS, Space=PROJECT, Group=测试, Route `/t/:tenantID/p/:projectID/defects`）**W** 新建租户 **T** `iam_menus` 三行 `domain` / `space` 分别为 CHAT/TEAM、PROJECTS/TEAM、PROJECTS/PROJECT，IAM 应用现有菜单 `domain=''`、`space=''` 且 IAM 导航不变；**W** 老租户（无 domain / space 值，或 D118 初版写入 `domain=COLLAB`）贡献 Version+1 后同步 **T** 未改动行补齐为声明值，管理员改过的行保持原值且 `EffectiveMenus` 返回声明的 Domain / Space；**W** 某 delivery 贡献缺 Domain、Domain=`COLLAB`、缺 Space、`Space=PROJECT` 而 Route 不含 `:projectID`、`Space=TEAM` 而 Route 含 `:projectID`，或 `Space=PROJECT` 且 Domain≠PROJECTS **T** 均启动失败 175400 并点名模块与字段 |
| AC-09-82 | integration | BR-09-84 | **G** 05 C09 以事务内模式使用 C7；Agent 请求带键 K、`seen_seq=100`，会话已有 seq=101 **W** 首次调用返回 409 145021（`data.state="interrupted"`）；以 K、`seen_seq=101` 重试；再以 K、`seen_seq=105` 重放 **T** 首次后 `platform_idempotency_keys` 无 K 行；重试 201 并写入 COMPLETED；重放返回同一 201 结果（`seen_seq` 不参与哈希，非 176425）；中间件模式下 interrupted 后 IN_PROGRESS 行被删除、重试不得到 176409 |
| AC-09-83 | unit | BR-09-85 | **G** 未配置密钥 **W** fleet `Initialize` 登记 `fleet.tool_credential` **T** 初始化失败；配置后 `Seal` 的信封挪到另一行（关联数据不符）→ `Open` 失败；日志与审计夹具中不出现明文 |

### 10.7 架构与门禁

| AC | 层 | Then |
|---|---|---|
| AC-09-74 | architecture | 新增 SQL 全部经生成 query 层；`TestQueryGate` 台账预算不增加；`LISTEN`/`pg_notify` 只出现在 `pkg/extension/gorm` |
| AC-09-75 | architecture | `TestSharedPackagesDoNotImportInternal`、`TestModulesDoNotImportAnotherModulesStorage` 通过（realtime 经 `events.Tail` 不读 events 表） |
| AC-09-76 | architecture | `TestBackgroundLoopsUseJobs`：新增未登记 `time.NewTicker` 失败；夹具用例证明门禁会红 |
| AC-09-77 | architecture | 任务 kind、事件类型、订阅者名、主题 kind、通知类型均以声明模块名为前缀（启动校验 + 门禁夹具） |
| AC-09-78 | ci | 新包加入 `MODULE_COVERAGE_PACKAGES`，各包覆盖率 ≥90%；`make generate` 后零差异；`make test-mysql8` schema lock 通过（无 MySQL `TIMESTAMP`） |
| AC-09-79 | integration | BR-09-41、BR-09-42 | **W** 浏览器订阅 `tenant/t/machine/M/channel`（服务端子流）/ `tenant/t/work-items/W`（连字符 kind）/ `tenant/t/work_item/*`（通配符） **T** 分别 `ready.denied`（reason=server_only）/ 400 172400 / 400 172400；订阅 `tenant/t/project/P` 无 `project_read` → denied forbidden，有则收到该项目内任一聚合事件帧 |
| AC-09-80 | architecture | BR-09-25b | 门禁夹具：业务模块迁移中出现名称匹配 `*_idempotency_keys`、`*_consumed_events`、`*_event_receipts` 的表 → 失败；仅 `platform_idempotency_keys` 与 `domain_event_receipts` 允许 |

---

## 11. 度量与埋点

Prometheus（模块 Collector；标签不得含 tenant_id、principal_id、载荷）：

| 指标 | 类型 | 标签 |
|---|---|---|
| `jobs_enqueued_total` | counter | queue, kind |
| `jobs_completed_total` | counter | queue, kind, outcome（succeeded/retry/dead/cancelled/lease_expired/interrupted） |
| `jobs_run_duration_seconds` | histogram（0.05…3600） | queue, kind |
| `jobs_claim_latency_seconds` | histogram | queue（run_at → 开始） |
| `jobs_depth` | gauge | queue, state |
| `jobs_oldest_pending_age_seconds` | gauge | queue |
| `jobs_lease_lost_total` / `jobs_leaked_workers` | counter / gauge | queue |
| `jobs_claim_errors_total` | counter | queue |
| `jobs_schedule_fires_total` / `jobs_schedule_lag_seconds` | counter / gauge | schedule |
| `domain_events_published_total` | counter | event_type |
| `domain_events_unsequenced` / `domain_events_sequencer_lag_seconds` | gauge | — |
| `domain_events_dispatch_lag` | gauge | subscriber, partition（seq 差） |
| `domain_events_handler_duration_seconds` | histogram | subscriber |
| `domain_events_handler_failures_total` / `domain_events_parked_total` | counter | subscriber |
| `realtime_streams` | gauge | — |
| `realtime_frames_sent_total` | counter | kind（event/control/heartbeat） |
| `realtime_delivery_latency_seconds` | histogram（0.01…5） | — （occurred_at → 写出） |
| `realtime_dropped_total` | counter | reason（slow_consumer/limit/lifetime/shutdown/reauth） |
| `realtime_resync_total` | counter | reason |
| `realtime_topic_denied_total` | counter | kind |
| `notifications_created_total` | counter | type, severity |
| `notification_deliveries_total` | counter | channel, outcome（sent/failed/suppressed） |
| `notification_suppressed_total` | counter | channel, reason |
| `files_s3_requests_total` / `files_s3_request_duration_seconds` | counter / histogram | operation, outcome |
| `idempotency_replays_total` / `idempotency_conflicts_total` | counter | operation |

产品埋点（前端，事件名与属性，不含正文）：`realtime_status_changed{from,to}`、`realtime_resync{reason}`、`notification_opened{type,severity}`、`notification_mark_all_read{count}`、`admin_job_retried{kind,bulk}`、`admin_job_discarded{kind}`、`admin_event_replayed{subscriber}`。

告警规则（加入 `docs/iam-alerts.yml` 与其测试文件）：`jobs_oldest_pending_age_seconds > 300` 10 min；`increase(jobs_completed_total{outcome="dead"}[1h]) > 10`；`domain_events_sequencer_lag_seconds > 60`；`domain_events_dispatch_lag > 10000` 15 min；`rate(realtime_dropped_total{reason="slow_consumer"}[5m]) > 1`；`increase(notification_deliveries_total{channel="EMAIL",outcome="failed"}[15m]) > 10`。

---

## 12. 依赖、风险与待决

### 12.1 依赖

| 依赖 | 需要什么 | 状态 |
|---|---|---|
| `internal/infra/dlock` | TryLock、auto-refresh、OnLost | 已有 |
| `internal/infra/audit` | `audit.Run` + `KindJob`/`KindSchedule` | 已有 |
| `pkg/extension/keyringx` | Seal/Open/Rewrap | 已有 |
| `minio-go/v7` | S3 驱动 | 已在 go.mod（审计锚点使用） |
| `pgx/v5` | LISTEN/NOTIFY 专用连接 | 已在 go.mod |
| cron 解析 | 5 字段 cron | **新依赖** `github.com/robfig/cron/v3`（仅 parser），需批准；未批准则 MVP 只支持 `Interval`（现有工作者全为间隔型） |
| Web Push | VAPID 签名与加密 | **新依赖** `github.com/SherClockHolmes/webpush-go`，v1.1，需批准 |
| `pkg/extension/authz` | `RegisterTenantMemberStream` | 新增（与 `RegisterEntityAdapter` 同构） |
| `pkg/extension/gorm` | `Notify`/`Listen`；sqlite DSN 默认 `_busy_timeout` | 新增 |
| `internal/iam/identity` | `RecipientDirectory`（邮箱、语言、显示名、是否活跃） | 新增窄接口 |
| `internal/iam/core` | `MenuContributor`/`MenuSink`、`iam_menus.app` 与 `iam_menus.domain`（D118）列、台账表；files 改用新接口 | 新增 |
| `internal/principallifecycle` | 擦除时调用 notification 清理 | 新增一步 |
| 08 | `PreferenceResolver`、`ListEligible`、通知中心页 | 08 提供；缺省时用 BR-09-64 默认 |
| 中间件 | metrics/telemetry/audit 的 ResponseWriter 包装须支持 `Unwrap`（`http.ResponseController` Flush/SetWriteDeadline） | 实施时验证；audit README 已声明支持流会话 |

### 12.2 风险

| 风险 | 缓解 |
|---|---|
| CI 的 go-mysql-server 不实现 `SKIP LOCKED`/行锁语义 | 正确性只依赖状态 CAS；真实并发用例放 `make test-mysql8`（release-check 必跑） |
| SQLite 无 `busy_timeout` 时 IMMEDIATE 事务立即 BUSY | DSN 默认补 5000 ms + 认领重试；SQLite 部署声明为单副本 |
| 单一定序器成为吞吐瓶颈 | 批量 500、单事务分配；实测 <2,000 事件/s 时告警；必要时按租户分片序列（v2，不影响信封） |
| `stream_seq` 作为 SSE id 透露平台总事件量 | 接受（仅数量级信息）；若需隐藏可改为 HMAC 包装的不透明游标，接口不变 |
| HTTP/1.1 下多标签页耗尽 6 连接 | 部署文档要求 HTTP/2；客户端失败 3 次降级轮询 |
| 处理器依赖 Start 阶段状态（jobs 先于业务模块 Start） | BR-09-12 明文规则 + 处理器单测在 Initialize 后即运行 |
| 中间件幂等模式在崩溃窗口可能重复执行 | 仅限无单事务的操作并要求业务 CAS；规格逐个标注 |
| 迁移 M3/M4 触及审计链与安全邮件 | 开关 + 双跑 + 状态 CAS；回滚 = 关开关 |
| 环形缓冲按副本独立，负载均衡切换副本后续传 | 缓冲在每副本均从库预热，覆盖同一窗口，任一副本都能续传 |

### 12.3 待决（附建议）

| # | 议题 | 建议 | 理由 |
|---|---|---|---|
| Q-09-01 | 是否批准 `robfig/cron/v3` | **批准**（只用 parser） | 自写 cron 解析易错；无新的传递依赖 |
| Q-09-02 | 主体首选语言来源 | identity 增加 `locale` 资料字段（RecipientDirectory 返回）；过渡期用租户默认 | 通知、邮件、门户都需要；目前底座无此字段 |
| Q-09-03 | 租户管理员是否可见本租户任务 | v1.1 增 `/tenants/{t}/jobs` 只读 + `job_view` 权限 | MVP 运维集中在平台；先控制范围 |
| Q-09-04 | 已读站内信 90 d 物理删除是否与 00「不物理删除」冲突 | 视为基础设施记录，按 CR-09-06 豁免 | 通知不是证据/裁决；08 如需留档应写审计 |
| Q-09-05 | authn 安全通知是否改走 C5 SMTP | **暂不**；先完成 M4 驱动迁移，另立决策 | 现有外部 webhook 契约有部署依赖 |
| Q-09-06 | 是否需要 Redis 扇出 | **不需要**（BR-09-48）；当单副本连接 >5,000 或延迟不达标时再评估 | 少一个必选基础设施 |
| Q-09-07 | 事件 `FULL` 模式审定责任 | 由发布者规格逐类型声明并在评审时检查字段策略 | 避免 SSE 绕过字段级权限 |

### 12.4 上线顺序与估时（人日，含三方言迁移、单测、集成测与文档）

| 阶段 | 内容 | 前置 | 估时 |
|---|---|---|---|
| A（W2） | C7 幂等（2.5）· C8 SecretSealer（1.5）· C6 菜单贡献 + files 改接口（4）· C1 核心：表、入队、认领、租约、退避、Catalog、Singleton、调度、Quiesce、指标（8） | — | 16 |
| B（W3） | C2 事件：表、发布、定序、分区派发、搁置/重投、receipts、purge、重放 CLI（9）· C9 租户业务文件（3）· C4 S3 驱动 + MinIO 合同测试 + `make test-s3`（6） | A（jobs） | 18 |
| C（W4） | C3 实时：authz 流式注册、Hub、环形缓冲、LISTEN、授权复查、限额、内部订阅（6）· 前端客户端 + hook + 状态组件（3）· C5 通知：表、Notifier、SMTP、模板、限流、去重、回写事件、收件箱 API、铃铛组件（8） | B（events） | 17 |
| D（W5–W6） | 管理 API H1–H16 + 页面 P1–P4（5）· 迁移 M1–M4（7.5）· 告警规则（0.5） | A–C | 13 |
| v1.1 | Web Push（3）· 本地→S3 迁移任务（3）· M5 webhook（1）· 租户任务只读页（2） | — | 9 |
| **合计** | MVP 64 人日；v1.1 9 人日 | | **73** |

关键路径：jobs → events → realtime（≈ 8 + 9 + 6 = 23 人日，单人约 5 周；双人并行 A/B 中 S3、菜单、幂等可与核心路径并行，约 3 周达到 PRD W4 需要的 JobQueue/outbox/SSE）。

---

## 13. 契约变更请求

| # | 位置 | 请求 | 理由 |
|---|---|---|---|
| CR-09-01 | 00 §7.1 信封 | 增加 `schema_version`、`stream_seq`（定序后填写）、`subject_version`、`topics[]`、`correlation_id`、`causation_id`；明确 `event_id` 即消费去重键 | `plugin-architecture.md` 要求版本；按聚合保序需要 subject_version；SSE 续传需要 stream_seq |
| CR-09-02 | 00 §8 | 写明 Go 标识：`JobQueue`=`jobs.Queue`、`OutboxPublisher`=`events.Publisher`、`RealtimeBroadcaster`=`events.Broadcaster` + `realtime.Hub`（服务端订阅）、`Notifier`=`notification.Notifier`；新增 `SecretSealer`=`secrets.Sealer`、`IdempotencyStore`=`idempotency.Store`、`TenantResourceFiles`（files）、`TopicAuthorizerProvider`、`MenuContributor`、`PreferenceResolver`（08 提供）、`RecipientDirectory`（identity 提供） | 统一 01/04 已引用的名称；避免 Go 包名重复（stutter） |
| CR-09-03 | 00 §9 幂等 | 以平台表 `idempotency_keys` 取代每模块 `<module>_idempotency_keys`；列：`subject_kind`(PRINCIPAL/CONTACT)、`subject_id`、`scope`、`idem_key`、`request_hash`、`state`、`status_code`、`response_body`、`resource_kind/id`、`expires_at`；冲突码 176409（同键不同请求）、176425（处理中）、400 176400 | 满足 01 CR-04、02 CCR-06、03 的列需求；同库同事务可用，免去 8 份相同迁移 |
| CR-09-04 | 00 §9 实时 | 主题语法扩为：实体 `tenant/{t}/{kind}/{id}`、集合 `tenant/{t}/{kinds}`、收件箱 `tenant/{t}/inbox/{p}`、项目 `tenant/{t}/project/{p}`、服务端子流 `tenant/{t}/{kind}/{id}/{channel}`；集合主题按集合读权限授权；SSE `id` = `stream_seq` | 01 CR-09、04 CR-04-05 |
| CR-09-05 | 00 §6 权限目录 | 新增平台级（system scope）权限 `system_job_view`、`system_job_manage`、`system_event_view`、`system_event_manage`（注册模块 jobs / events） | 管理页与 API 守卫 |
| CR-09-06 | 00 §3 数据约定 | 基础设施表允许 `tenant_id=0`（平台级）；与数据库时钟比较的租约/调度列使用 `*_at_ms BIGINT`；基础设施记录（任务、事件、receipts、幂等键、已读通知）按保留期物理删除，不适用「不物理删除」 | 与 `dlock`/`files`/`provisioning` 现状一致；避免无界增长 |
| CR-09-07 | 00 §7.1 事件表 | 新增 `notification.delivery_state_changed`（发布 notification；消费 intake 及任何调用 `SendEmail` 的模块）、`job.dead_lettered`（发布 jobs；消费 notification） | 01「由 Notifier 回写」的机制 |
| CR-09-08 | 00 §9 错误码段 | platform 170000–179999 细分：jobs 1700xx、events 1710xx、realtime 1720xx、storage 1730xx、notification 1740xx、menus 1750xx、idempotency 1760xx、secrets 1770xx | 避免能力间撞码 |
| CR-09-09 | 00 §5 枚举 | 登记 09 拥有的枚举：`JobState`(PENDING, RUNNING, SUCCEEDED, DEAD, CANCELLED)、`ParkedState`(PARKED, REDELIVERING, DELIVERED, DISCARDED)、`NotificationSeverity`(INFO, ACTION, URGENT)、`NotificationChannel`(IN_APP, EMAIL, WEB_PUSH)、`DeliveryState`(PENDING, SENDING, SENT, FAILED, SUPPRESSED)、`RealtimeMode`(NONE, SIGNAL, FULL) | 00 §5 为唯一定义处 |
| CR-09-10 | 00 §1 / §10 | 09 增加页面列：「后台任务、计划任务、领域事件（系统控制台）；共享组件 RealtimeStatus、NotificationBell」；§10 共享组件清单加入这两个组件与 `useRealtime` | 00 §1 现写「—」 |
| CR-09-11 | 底座 `iam_menus` | 新增列 `app VARCHAR(32) NOT NULL DEFAULT 'iam'`，菜单可指向不同前端应用（如 `delivery`） | 00 §10 规定业务应用为独立 `apps/delivery` |
| CR-09-12 | 底座 `iam_menus` | 新增列 `domain VARCHAR(16) NOT NULL DEFAULT ''`（D118 顶层域键；空 = 不分域，IAM 现有行不变）与 `space VARCHAR(16) NOT NULL DEFAULT ''`（D118 修订：TEAM / PROJECT；空 = 不区分，IAM 现有行不变） | 08 §8.6 控制台改为顶栏域标签 + 当前域侧栏，项目为主容器、项目内页面在项目空间渲染（D118 及其修订） |
| CR-09-13（D120） | 00 §9 幂等 | 写明 `data.state="interrupted"` 的 409 不入幂等表、`seen_seq` 不参与 `request_hash`（BR-09-84）；C8 purpose 由 fleet 登记（BR-09-85），无新平台能力 | 05 BR-05-128 / 00 §9 过期上下文写入需同键重试 |

**处理状态（00 v1.3 审校后）**：CR-09-01、CR-09-02、CR-09-04、CR-09-06、CR-09-07 已并入 00；CR-09-03 已并入，表名按 00 定为 `platform_idempotency_keys`、列 `operation`/`key`，错误码以 00 为准（176425 = 同键不同请求，176409 = 进行中），本规格已对齐；CR-09-05 记入 00 §13 F10，权限行见 §6.3；CR-09-09 中仅本模块使用的枚举按 00 §3 留在本规格，不进 §5；CR-09-08、CR-09-10、CR-09-11 待主规格下一轮合并。CR-09-12（D118，2026-10-12）同 CR-09-11，为底座表列变更，随 C6 实施，不涉及 00。

> **D120（2026-10-12）已并入本规格**：§5.10（BR-09-84 幂等对 `interrupted` 的例外、BR-09-85 C8 新 purpose `fleet.tool_credential` / `fleet.wake_webhook_secret`）、AC-09-82 / 83、CR-09-13。


## 14. D123 / D124 菜单、身份投影与富媒体分期

`MenuDomain` 内部值 `PROJECTS` 保持兼容，**显示标签改为「协作」**，只允许 `Space=PROJECT` 的当前项目页进入该域菜单；旧团队项目组合 / 跨项目需求、任务、缺陷、流水线、交付、来件、对接贡献全部退休。不得恢复旧 `COLLAB` 枚举。协作侧栏不得贡献项目频道 / 共享频道 / 甲方共享频道或任意跳会话区项。logo 右侧项目切换器只列账号可見项目；详情路由中的所属项目优先，不依赖当前选择。

菜单声明需支持按内部 / 外部 viewer 的读权限与投影过滤（复用 IAM，不另建认证）；外部只有待办 / 会话 / 协作与白名单项目页面。菜单隐藏不替代对象 / 文件 / SSE / 搜索权限；服务端请求先查 actor 的项目资格，再生成白名单 DTO 和资源绑定。未授权文件的上传扫描状态、缩略图、提取文本与下载 URL 也不得泄露。

D122 Office 转换、PDF / 视频复杂查看、可编辑语音转录、附件 OCR / 抽取与联网搜索开发 v1.1；原型完整展示加载 / 扫描 / 转换失败 / 不支持 / 下载原件。MVP 使用基本附件与软件报告查看。遥测预留 trace_id / span_id / parent_span_id 与领域对象关联，OTel 全导出 / AG-UI v1.1。MVP 关键事实 + outbox + audit 同事务并保证投影重建回归，不采用全量事件溯源。

验收修订：AC-09-81 不再播种团队级 defects 跨项目菜单，而是 CHAT/TEAM 与 PROJECTS/PROJECT 的当前项目菜单；贡献跨域频道侧栏或外部内部页即拒绝；旧贡献同步升级移除，管理员删除项不复活。共享频道通知 / SSE / 菜单外部计数只能来自授权对象。

### 14.P 本域易用性与大数据验收

本域适用 00 §16.8：服务端权限过滤 / 稳定游标分页，不全量拉取前端后筛；长列表虚拟化、聚合服务端取，精确总数不阻塞首屏；涉及看板按列分页、频道历史锚点与增量、流程图可见渲染 / 子流程惰性加载、文件范围读。切项目 / 筛选取消过期请求，保留阅读 / 草稿上下文，明示 loading / 空 / 超时 / 限流 / 断流 / 重试。协作二级菜单不跳会话域。规模与 API / UI 性能预算是开发后待实测门槛，原型不宣称已通过。

### 14.Q 集合读取 API 分页参数同步

本规格 §6 全部集合 GET operation（包括现有列表和本次补的读模型）采用 00 §16.10 的唯一产品分页合同：`cursor?` / `limit?`（默认50、最多100）/ 白名单 `sort?`，保留各 operation 的业务 filter / q / project_id；返回 `items,next_cursor?,has_more`，旧 offset / page / page_size 参数退休，不并存两套 API。消息历史 anchor_seq / 增量 after_seq 单独按语义使用；聚合 / 有限枚举不分页。服务端稳定排序与权限 / 筛选绑定 cursor，不依赖精确 COUNT。公共底座既有查询不改，本产品开发前 W1 检查 pkg/query 能力及最小复用 / 扩展 ADR，不能假称已具备游标。旧接口表分页字段由本条原地替代，最终 OpenAPI 只生成这一合同。

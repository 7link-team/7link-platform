# 01 · 需求开口、来件、分诊、澄清与甲方门户（模块 `intake`）

> 上位文档：`PRD.md` §2.2、§6.1、§9、§13；共同契约：`00-conventions.md`（本文不重定义其中任何枚举、权限码、事件名、错误码段；需要的变更见 §13）。
> 标记约定：「本规格提议」= PRD 未写明、由本规格补充的设计，后附一句理由。所有数值为默认值，标注「可配」者可在开口设置中调整，范围写在括号内。

---

## 1. 范围与目标

**解决**：甲方需求从任意开口进入乙方团队，到「转为需求 / 合并 / 拒绝」为止的全部过程，以及甲方在系统外唯一可见的界面（甲方门户）。

| 本规格负责 | 交给其他规格 |
|---|---|
| 开口（PORTAL、EMAIL、API；WEBHOOK v1.1、IM v2 仅保留枚举）的配置与启停 | 需求表、验收标准、需求版本、范围变更 → **02** |
| 来件记录：不可改原文、附件、来源元数据、状态机 | 项目创建、团队模式、交付包、验收数据模型、返工来件的触发 → **07** |
| 甲方联系人（`intake_contacts`，**不是** Principal）与门户访问链接 | 授权判定（角色/委托/策略）、我的裁决聚合、业务通知编排 → **08** |
| 分诊卡（Agent 协调员产出草案，人确认或经委托转化） | 协调员 Agent 何时被唤醒、在哪台机器执行 → **05** |
| 澄清轮次：问题、回答、经原开口发送、提醒、超时 | 后台任务队列、outbox、SSE、通知投递、租户级业务文件写入能力 → **09** |
| 转为需求（调用 02 的接口）、合并重复、拒绝（理由必填） | 文件存储与 ResourceBinding 本身 → 底座 `files` |
| 邮件收件（解析、线程、附件上限、垃圾与未知发件人隔离） | Webhook 订阅与签名投递 → 底座 `iam/webhook` |
| API 来件（PAT / OAuth 客户端）与回执回调（经底座 webhook） | PAT、服务账号、OAuth 客户端凭证 → 底座 `iam/authn`、`iam/identity`、`iam/oauth` |
| 甲方门户页面与 API 面：提交需求、我的需求（里程碑）、澄清作答、验收入口（调用 07）、申请变更与变更确认（kind=CHANGE，D117；版本与容差落在 02 / 07） | 门户之外的任何甲方组织建模（PRD §2.4 非目标） |
| 品牌（部署 / 租户配置）与「AI 参与展示」设置 | — |

**不解决**：自动报价、合同、甲方侧多角色与组织管理、IM 与 Webhook 开口的具体协议（仅定义扩展位）。

---

## 2. 用户故事

| ID | 故事 | 期 |
|---|---|---|
| US-01-01 | 作为甲方联系人 Lee，我要用团队发给我的链接在门户提交需求（标题、描述、附件、期望日期），以便不注册账号就能下单 | MVP |
| US-01-02 | 作为甲方联系人，我要给团队专属邮箱发邮件，以便用最熟悉的方式提需求，并在同一邮件线程里收到回执与澄清问题 | MVP |
| US-01-03 | 作为甲方的工单系统，我要用 API 提交来件，并通过 webhook 收到受理、澄清、转化、拒绝等回执 | MVP |
| US-01-04 | 作为 Agent 协调员，我要读取新来件并提交分诊卡（摘要、需求草案、建议项目、风险、待澄清问题、规模），以便负责人快速确认 | MVP |
| US-01-05 | 作为交付负责人 Morgan，我要在来件详情里对照原文审阅分诊卡、修改后一键转为需求（现有项目或新建项目） | MVP |
| US-01-06 | 作为交付负责人，我要把澄清问题经原开口发给甲方，并看到回答回流到同一线程 | MVP |
| US-01-07 | 作为甲方联系人，我要在门户逐条回答澄清问题，以便团队继续推进 | MVP |
| US-01-08 | 作为交付负责人，我要在甲方长期不回复时收到超时提醒，并决定按假设转化、再问一次或拒绝 | MVP |
| US-01-09 | 作为交付负责人，我要把重复来件合并到已有来件、或填写理由拒绝来件，以便来件队列干净且甲方得到明确答复 | MVP |
| US-01-10 | 作为团队所有者 Sam，我要配置开口（门户、邮箱、API）、品牌和「AI 参与展示」，以便甲方看到的是我们团队的形象 | MVP |
| US-01-11 | 作为交付负责人，我要为联系人签发、查看、吊销门户链接，以便控制谁能看到什么 | MVP |
| US-01-12 | 作为甲方联系人，我要在「我的需求」看到每条需求的里程碑（已接收→澄清中→进行中→待验收→已交付），但看不到内部工作项与 Agent 细节 | MVP |
| US-01-13 | 作为甲方联系人，我要在门户打开交付包并提交〔接受〕〔部分接受〕〔拒绝(原因)〕 | MVP |
| US-01-14 | 作为全自治项目的所有者，我要允许经委托的 Agent 把来件转为**已有**项目的需求，但新建项目必须由人完成 | MVP |
| US-01-15 | 作为交付负责人，我要处理被隔离的邮件（未知发件人、疑似垃圾），放行或丢弃 | MVP |
| US-01-16 | 作为甲方工单系统，我要通过 Webhook 开口推送工单 | v1.1 |
| US-01-18 | 作为交付负责人，我要把甲方拒收形成的返工来件按条目处置为「重开被拒工作项」或「追加到需求（范围变更）」，以便质量返工与范围变更分开处理 | MVP |
| US-01-19 | 作为执行中被需求疑点阻塞的工作项负责人，我要沿原来件线程向甲方追问，以便不另起沟通渠道 | MVP |
| US-01-20 | 作为甲方联系人，我要在已立项的需求上〔申请变更〕，并看到变更的处理状态（评估中 / 待你确认 / 已接受 / 未接受）以及对进度、日期和价格的影响，以便不用直接改需求也能提出新要求 | MVP |
| US-01-21 | 作为交付负责人，我要在变更申请上评估影响（验收标准差异、受影响工作项、进度与成本、容差），超出容差时请甲方确认、容差内直接转化并告知甲方结果，以便范围变化有据可查且不拖慢小改动 | MVP |
| US-01-22 | 作为甲方联系人，我要在门户对超出容差的变更〔接受〕或〔不接受(理由)〕，以便我对进度与价格的变化有最终发言权 | MVP |
| US-01-17 | 作为甲方联系人，我要在企业微信 / Slack / Telegram 会话里提需求并答复澄清 | v2 |
| US-01-23 | 作为甲方联系人，我要在验收页里直接打开交付给我的文件，在代码行、表格行、网页区域或文档段落上留下批注并回复，拒收某项时引用这些批注，以便团队返工时能回到我指出的原处（D120） | MVP（视频时间点与章节段落 v1.1） |

---

## 3. 领域模型与数据表

### 3.1 关系

```
Tenant ─┬─ intake_channels (1 PORTAL / n EMAIL / n API)
        ├─ intake_client_settings (每租户 1 行：品牌、AI 参与展示、门户默认值)
        ├─ intake_contacts ── intake_access_links ── intake_portal_sessions
        ├─ intake_email_messages (每封入站邮件 1 行，含隔离)
        └─ intake_records ─┬─ intake_triage_cards (revision 1..n，不可变)
                    ├─ intake_clarification_rounds ── intake_clarification_questions
                    ├─ intake_messages (双向线程日志，不可变)
                    └─ files 绑定：purpose=intake.attachment / intake.raw-mime
外部引用（只存 ID）：project_id(07)、requirement_id(02)、origin_delivery_id / origin_acceptance_id(07)、triage_agent_id(05)、principal_id(底座)
```

### 3.2 逻辑类型与三方言映射

| 逻辑类型 | PostgreSQL | MySQL 8.0.16+ | SQLite | 备注 |
|---|---|---|---|---|
| `id` | `BIGINT` | `BIGINT` | `BIGINT` | 雪花 ID；JSON 中为十进制字符串 |
| `ts` | `TIMESTAMPTZ(3)` | `DATETIME(3)` | `DATETIME` | UTC；遵循 00 §3 与 backend-module 规约（**不用 MySQL `TIMESTAMP`**） |
| `str(n)` | `VARCHAR(n)` | `VARCHAR(n)` | `TEXT COLLATE BINARY` + `CHECK(length(CAST(x AS BLOB))<=n*4)` | |
| `enum` | `VARCHAR(32)` + `CHECK IN` | `VARCHAR(32)` + `CHECK IN` | `TEXT COLLATE BINARY` + `CHECK IN` | 值为大写蛇形 |
| `text64k` | `TEXT` + `CHECK(octet_length(x)<=65536)` | `MEDIUMTEXT` + `CHECK(LENGTH(x)<=65536)` | `TEXT` + `CHECK(length(CAST(x AS BLOB))<=65536)` | MySQL `TEXT` 仅 65535 字节，utf8mb4 下须用 `MEDIUMTEXT` |
| `text2k` | `VARCHAR(2000)` | `VARCHAR(2000)` | `TEXT` + 长度 CHECK | 短理由 |
| `json` | `JSONB` | `JSON` | `TEXT` + `CHECK(json_valid(x))` | Schema 写在各表下 |
| `bool` | `BOOLEAN` | `TINYINT(1)` + `CHECK IN(0,1)` | `INTEGER` + `CHECK IN(0,1)` | |
| `int` | `INT` | `INT` | `INTEGER` | |
| `hash` | `CHAR(64)` | `CHAR(64)` | `TEXT COLLATE BINARY` + `CHECK(length=64)` | 小写十六进制 SHA-256 |
| `date` | `DATE` | `DATE` | `TEXT` + `CHECK(x GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]')` | |

**公共列**（每张表都有，下文不再重复）：`tenant_id id NOT NULL CHECK(>0)`、`created_at ts NOT NULL`、`updated_at ts NOT NULL`、`created_by id NULL`、`updated_by id NULL`（NULL = 系统）。所有对象名带 `{{.Prefix}}`；goose 版本表 `goose_intake`；所有唯一约束额外含 `UNIQUE(tenant_id, id)` 以便同租户复合引用。
**部分唯一索引**：PostgreSQL / SQLite 用 `WHERE` 谓词；MySQL 不支持，统一改写为「可空哨兵列 + 普通唯一索引」（MySQL 唯一索引允许多个 NULL），下文逐处注明。

### 3.3 `intake_channels` 开口

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| id | id | N | — | PK |
| kind | enum `IntakeChannelKind` | N | — | 创建 `WEBHOOK`/`IM` 在 MVP 返回 110431 |
| name | str(80) | N | — | 租户内唯一 |
| status | enum `IntakeChannelStatus` | N | `ENABLED` | `ENABLED`/`DISABLED`（§13 CR-03） |
| portal_singleton | int | Y | NULL | kind=PORTAL 时为 1，其他为 NULL；`UNIQUE(tenant_id, portal_singleton)` 保证每租户至多 1 个门户开口（三方言同写法，无需部分索引） |
| inbound_local_part | str(40) | Y | NULL | 仅 EMAIL；`^[a-z0-9][a-z0-9-]{2,39}$`；**全部署唯一** `UNIQUE(inbound_local_part)`（收件域由部署配置 `intake.email.inbound_domain` 统一提供，跨租户共享域名） |
| config | json | N | `{}` | 见下方 Schema，按 kind 校验 |
| triage_agent_id | id | Y | NULL | 05 的 Agent；须具备 `COORDINATOR` 角色，由 `AgentDirectory` 校验 |
| default_project_id | id | Y | NULL | 分诊卡的默认建议项目 |
| notify_principal_ids | json | N | `[]` | 新来件通知对象；空 = 持有 `delivery_lead` 角色者；≤20 个 |
| version | int | N | 1 | 乐观锁 |
| archived_at | ts | Y | NULL | 归档后不可再启用 |

`config` JSON Schema（公共字段 + kind 专属）：

```json
{ "send_receipt": "boolean=true", "agent_may_send_clarifications": "boolean=true",
  "reminder_interval_hours": "int 24..168 =48", "max_reminders": "int 0..5 =2", "clarification_timeout_hours": "int 48..720 =168",
  "PORTAL": { "require_access_code": "boolean=true", "default_link_ttl_days": "int 1..180 =30", "max_submissions_per_contact_per_day": "int 1..100 =20" },
  "EMAIL":  { "ingest_mode": "INBOUND_WEBHOOK|IMAP_POLL", "imap": {"host":"str","port":"int","username":"str","secret_ref":"str（密文存底座 secretx，不回显）","folder":"str=INBOX","processed_folder":"str=Processed"},
              "sender_policy": "OPEN|KNOWN_CONTACTS|ALLOWLIST =KNOWN_CONTACTS", "allowlist": ["域名或地址, ≤200"], "spam_score_threshold": "number 1..20 =5.0" },
  "API":    { "allowed_principal_ids": ["服务账号/人类 principal id, 1..20"], "callback_subscription_id": "id|null（iam/webhook 订阅）", "max_intakes_per_day": "int 1..10000 =1000" } }
```

索引：`UNIQUE(tenant_id, name)`、`UNIQUE(tenant_id, portal_singleton)`、`UNIQUE(inbound_local_part)`、`(tenant_id, kind, status)`。

### 3.4 `intake_client_settings` 甲方可见设置（每租户 1 行）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK；`UNIQUE(tenant_id)` |
| display_name | str(80) | Y | NULL | 门户 / 邮件显示的团队名；NULL 时按 BR-01-40 回退 |
| logo_file_id | id | Y | NULL | files 绑定 purpose=`intake.branding`；PNG/SVG/WebP ≤512KB |
| support_email | str(254) | Y | NULL | 门户页脚联系方式 |
| ai_disclosure | enum `AiDisclosureMode` | N | `BY_MODE` | `HIDDEN` / `COLLABORATIVE` / `BY_MODE`（§13 CR-03）。「本规格提议」默认 `BY_MODE`：PRD Q7 默认「AI 与工程师协作完成」，但全自治项目没有工程师参与，固定文案会失实 |
| show_platform_attribution | bool | N | false | 是否在页脚显示部署名；默认不显示（PRD §9.3） |
| default_locale | str(8) | N | `zh-CN` | `en-US`/`zh-CN`/`ms-MY`；联系人未设语言时使用 |
| version | int | N | 1 | |

### 3.5 `intake_contacts` 甲方联系人

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK；actor_kind=`CLIENT` 时的 actor_id |
| kind | enum `IntakeContactKind` | N | `PERSON` | `PERSON` / `SYSTEM`（API 调用方系统） |
| display_name | str(120) | N | — | |
| email | str(254) | Y | NULL | 存小写、去首尾空白；`UNIQUE(tenant_id, email)`（NULL 可重复，三方言一致） |
| organization_label | str(120) | Y | NULL | 自由文本，仅展示，不建模甲方组织 |
| principal_id | id | Y | NULL | kind=SYSTEM 时为 API 调用方 principal；`UNIQUE(tenant_id, principal_id)` |
| locale | str(8) | Y | NULL | |
| state | enum `IntakeContactState` | N | `ACTIVE` | `ACTIVE` / `BLOCKED`；BLOCKED 立即吊销全部链接与会话，邮件静默拒收 |
| note | text2k | Y | NULL | 团队内部备注，甲方不可见 |
| version | int | N | 1 | |
| archived_at | ts | Y | NULL | |

### 3.6 `intake_access_links` 门户访问链接

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK |
| contact_id | id | N | — | 同租户联系人 |
| channel_id | id | N | — | PORTAL 开口 |
| token_hash | hash | N | — | SHA-256(token)；`UNIQUE(token_hash)`（全局，查找入口）。明文 token 仅在签发响应中出现一次 |
| token_hint | str(4) | N | — | 明文末 4 位，便于团队辨认 |
| expires_at | ts | N | — | 签发时 + TTL（1..180 天） |
| revoked_at / revoked_by / revoke_reason | ts / id / text2k | Y | NULL | 吊销三元组 |
| last_used_at | ts | Y | NULL | |
| last_code_sent_at | ts | Y | NULL | 访问码重发冷却 |
| codes_sent_window_start / codes_sent_in_window | ts / int | Y / N | NULL / 0 | 每小时访问码发送计数 |

状态为派生值（不落枚举列）：`revoked_at` 非空 → REVOKED；`expires_at ≤ now` → EXPIRED；否则 ACTIVE。索引：`(tenant_id, contact_id, expires_at)`。

### 3.7 `intake_portal_sessions` 门户会话（含一次性访问码）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK |
| link_id / contact_id | id | N | — | |
| session_hash | hash | N | — | SHA-256(会话密钥)；`UNIQUE(session_hash)` |
| code_hash | hash | Y | NULL | SHA-256(6 位访问码 + session id)；不需验证码时为 NULL |
| code_expires_at | ts | Y | NULL | 发码后 10 分钟 |
| code_attempts | int | N | 0 | ≥5 即锁定该会话 |
| verified_at | ts | Y | NULL | 非空 = ACTIVE |
| expires_at | ts | N | — | 绝对过期：创建 + 12h |
| idle_expires_at | ts | N | — | 每次请求顺延至 now + 2h，不超过 expires_at |
| revoked_at | ts | Y | NULL | |
| client_ip_hash | hash | N | — | SHA-256(IP + 部署盐)，不存明文 IP |
| user_agent | str(256) | N | `''` | |

索引：`(tenant_id, link_id, revoked_at)`、`(expires_at)`（清理任务用）。「本规格提议」12h/2h：门户是低频外部入口，短会话降低共享设备风险，链接本身长期有效弥补体验。

### 3.8 `intake_records` 来件（表名按 00 §3 模块前缀规则；资源名与路由仍为 `intakes`）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK |
| ref_no | int | N | — | 租户内递增展示号，显示为 `IN-<ref_no>`；`UNIQUE(tenant_id, ref_no)`，分配时冲突重试 ≤3 次 |
| channel_id / channel_kind | id / enum | N | — | channel_kind 冗余，便于列表过滤 |
| contact_id | id | N | — | |
| kind | enum `IntakeKind` | N | `NEW` | `NEW` / `REWORK`（07 拒收触发的返工来件）/ `CHANGE`（甲方对已转化需求的变更申请，D117）（§13 CR-03、CR-12） |
| status | enum `IntakeStatus` | N | `RECEIVED` | 00 §5 |
| title | str(200) | N | — | 门户/API 为提交标题；邮件为 Subject（去掉 `Re:`/`Fwd:`，空则取正文首行前 80 字） |
| raw_content | text64k | Y | NULL | **不可变**原文（纯文本或 Markdown）；>64KB 时为 NULL 并以 `intake.attachment` 槽位 `raw-overflow` 存文件 |
| raw_content_format | enum | N | `PLAIN` | `PLAIN` / `MARKDOWN` / `HTML_TEXT`（HTML 邮件提取的纯文本） |
| raw_content_hash | hash | N | — | 规范化原文的 SHA-256，供审计与去重提示 |
| source_message_id | str(998) | Y | NULL | 邮件 Message-ID |
| external_ref | str(128) | Y | NULL | API 调用方自有单号；`UNIQUE(tenant_id, channel_id, external_ref)` |
| desired_by | date | Y | NULL | 甲方期望日期，≥ 提交日 |
| received_at | ts | N | — | |
| submitted_by_principal_id | id | Y | NULL | API 调用方或代录入的成员；门户/邮件为 NULL |
| reply_token | str(16) | N | — | 随机 base32，邮件 Reply-To 加号标签；`UNIQUE(reply_token)` |
| latest_card_revision | int | N | 0 | 0 = 尚无分诊卡 |
| open_round_id | id | Y | NULL | 当前未关闭的澄清轮（至多 1 个） |
| client_unresponsive | bool | N | false | 最近一轮澄清超时置 true，甲方再回复时清除 |
| project_id / requirement_id | id | Y | NULL | 转化结果；CONVERTED 时 requirement_id 必非空（CHECK） |
| conversion_mode | enum | Y | NULL | `NEW_REQUIREMENT` / `NEW_PROJECT` / `APPEND_TO_REQUIREMENT` |
| merged_into_intake_id | id | Y | NULL | MERGED 时必非空（CHECK），且 ≠ id |
| decline_reason_code | enum | Y | NULL | `OUT_OF_SCOPE` / `INSUFFICIENT_INFO` / `CAPACITY` / `SPAM` / `OTHER`；DECLINED 时必非空 |
| decline_reason | text2k | Y | NULL | 内部理由，DECLINED 时必非空 |
| client_message | text2k | Y | NULL | 合并 / 拒绝 / 转化时发给甲方的话，可空 |
| resolved_at | ts | Y | NULL | 进入终态时间 |
| resolved_actor_kind / resolved_actor_id / resolved_authority_source | enum / id / str(80) | Y | NULL | 终态动作主体三元组（00 §4） |
| origin_delivery_id / origin_acceptance_id | id | Y | NULL | REWORK 来源；`UNIQUE(tenant_id, origin_acceptance_id)` 保证一次拒收至多一条返工来件 |
| last_client_activity_at / last_team_activity_at | ts | Y | NULL | 列表排序与「待你处理」 |
| change_of_intake_id | id | Y | NULL | 仅 kind=CHANGE：甲方在哪条已转化来件上〔申请变更〕（同租户、≠ id；BR-01-76） |
| target_requirement_id | id | Y | NULL | 仅 kind=CHANGE：被变更的 02 需求，创建时取原来件的 `requirement_id` 并固定 |
| client_approval | json | Y | NULL | 仅 kind=CHANGE：当前（最近一轮）甲方确认请求，Schema 见下；历次请求见审计与线程消息 |
| result_version_no | int | Y | NULL | 仅 kind=CHANGE 且 CONVERTED：转化后需求的版本号 vN（门户「已接受（需求 vN）」与版本历史用；技术必需列，BR-01-84） |
| version | int | N | 1 | |

CHECK（三方言同写法）：`kind <> 'CHANGE' OR (change_of_intake_id IS NOT NULL AND target_requirement_id IS NOT NULL)`；`kind = 'CHANGE' OR (change_of_intake_id IS NULL AND target_requirement_id IS NULL AND client_approval IS NULL)`；`change_of_intake_id IS NULL OR change_of_intake_id <> id`。

`client_approval` JSON Schema（D117；服务端写入，客户端不可直接提交）：

```json
{ "state": "PENDING|APPROVED|REJECTED|WITHDRAWN", "round": "int ≥1（同一来件第几次请甲方确认）", "card_revision": "int（请求所依据的分诊卡 revision）",
  "requested_at": "ts", "requested_by": "principal id（HUMAN）", "request_message_id": "id（给甲方的说明，intake_messages OUTBOUND）",
  "decided_at": "ts|null", "decided_via": "PORTAL|RECORDED_BY_TEAM|null", "decided_actor_kind": "CLIENT|HUMAN|null", "decided_actor_id": "id|null",
  "authority_source": "CLIENT:<contact_id>（门户）| ROLE:*（团队代录）| null", "comment": "str 10..2000（REJECTED 必填）|null",
  "evidence_file_ids": ["id（RECORDED_BY_TEAM 时 1..5，intake.attachment 槽位 client-approval:<round>）"], "offline_note": "str 1..2000（RECORDED_BY_TEAM 必填）|null",
  "withdrawn_reason": "str 1..2000（WITHDRAWN 必填）|null",
  "client_reminders_sent": "int 0..3", "next_client_reminder_at": "ts|null", "next_team_reminder_at": "ts|null" }
```

索引：`(tenant_id, status, received_at, id)`、`(tenant_id, contact_id, received_at)`、`(tenant_id, channel_id, received_at)`、`(tenant_id, requirement_id)`、`(tenant_id, merged_into_intake_id)`、`(tenant_id, change_of_intake_id)`。
不可变性：`raw_content`、`raw_content_hash`、`title`、`channel_*`、`contact_id`、`received_at`、`source_message_id`、`external_ref`、`change_of_intake_id`、`target_requirement_id` 在仓储层没有任何更新路径（`Updates` 只允许白名单列），由单元测试固定。

### 3.9 `intake_triage_cards` 分诊卡（不可变，修改 = 新 revision）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK |
| intake_id | id | N | — | `UNIQUE(tenant_id, intake_id, revision)` |
| revision | int | N | — | 从 1 起，= intake_records.latest_card_revision + 1（同一事务内条件更新保证） |
| author_kind / author_id / authority_source | enum / id / str(80) | N / Y / N | — | AGENT（协调员）或 HUMAN（编辑） |
| based_on_revision | int | Y | NULL | 人工编辑基于哪个 revision |
| summary | text2k | N | — | 需求摘要 |
| draft_title | str(200) | N | — | |
| draft_description | text64k | N | — | |
| draft_acceptance_criteria | json | N | — | `[{ "text": "str 1..1000" }]`，1..30 条 |
| suggested_project_id | id | Y | NULL | 与 `suggest_new_project` 互斥（CHECK） |
| suggest_new_project | bool | N | false | |
| new_project_name | str(200) | Y | NULL | suggest_new_project=true 时必填 |
| risks | json | N | `[]` | `[{ "level": "LOW|MEDIUM|HIGH"（RiskLevel）, "text": "str 1..1000" }]`，≤20 |
| open_questions | json | N | `[]` | `["str 1..2000"]`，≤10；可一键转为澄清问题草稿 |
| size_estimate | enum `SizeEstimate` | N | — | `XS`/`S`/`M`/`L`/`XL`（§13 CR-03） |
| size_rationale | text2k | Y | NULL | |
| confidence | int | Y | NULL | 0..100，Agent 自评；人工卡为 NULL |
| knowledge_refs | json | N | `[]` | `[{ "chunk_id": "id", "source_kind": "KnowledgeSourceKind" }]`（06），≤20 |
| model_ref | str(120) | Y | NULL | runtime×模型标识（不含提示词与正文） |
| rejected_items | json | N | `[]` | 仅 kind=REWORK：`[{ "work_item_id": "id", "delivery_item_id": "id", "feedback_category": "FeedbackCategory", "comment": "str ≤2000", "suggested_disposition": "REOPEN_WORK_ITEMS|APPEND_TO_REQUIREMENT|NONE" }]`，≤50（BR-01-27）；D120：元素另含可选 `anchors[]`（BR-01-91） |
| change_impact | json | Y | NULL | 仅 kind=CHANGE（其他 kind 必须为 NULL，否则 110400）：变更影响评估，Schema 见下。kind=CHANGE 时〔请甲方确认〕（T15）与 `APPEND_TO_REQUIREMENT` 转化前必填（110452）；Agent 可起草（BR-01-78） |

`change_impact` JSON Schema（D117）：

```json
{ "base_version_no": "int ≥1（评估所依据的目标需求版本，须 = 02 当前版本，否则 110453）",
  "criteria_diff": [{ "op": "ADD|RETIRE|REPLACE", "criterion_ref": "id（02 验收标准 id；RETIRE/REPLACE 必填，ADD 为空）", "text": "str 1..1000（ADD/REPLACE 为新表述；RETIRE 为被移除条目的通俗表述，供甲方阅读）" }]（1..30）,
  "affected_work_items": [{ "work_item_id": "id|null（NEW 时为空）", "effect": "STALE|NEW|CANCELLED" }]（≤200；仅团队可见）,
  "schedule_delta_days": "int -365..365", "new_expected_date": "date|null",
  "internal_cost_delta_micros": "int64（仅团队可见）",
  "client_price_delta": { "amount": "十进制字符串，可为负", "currency": "ISO 4217 三字母", "note": "str ≤500" } | null,
  "tolerance_check": { "breached": ["TIME|COST|SCOPE"], "snapshot": "07 ToleranceReader.WouldBreach 返回的快照（仅团队可见）", "checked_at": "ts" },
  "requires_client_approval": "bool = breached ≠ ∅" }
```

服务端规则：`affected_work_items` 以 02 `ChangeImpactPreviewer.Preview` 结果为准（STALE / NEW 条目必须保留，作者只能把 STALE 改为 CANCELLED）；`tolerance_check` 与 `requires_client_approval` 一律由服务端在写卡事务内经 07 `ToleranceReader.WouldBreach` 计算，请求中的同名值被忽略；`client_price_delta` 只能由 HUMAN 作者填写（AGENT 卡带该字段 → 400 110400，BR-01-78）；`scope_delta_criteria` = ADD 条数 − RETIRE 条数（REPLACE 计 0）。

### 3.10 `intake_clarification_rounds` 澄清轮

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK |
| intake_id | id | N | — | |
| work_item_id | id | Y | NULL | 由 02 `ClarificationRequester` 发起（T14）时为阻塞的工作项；回答与超时事件回带该值 |
| round_no | int | N | — | 1..20；`UNIQUE(tenant_id, intake_id, round_no)` |
| state | enum `ClarificationRoundState` | N | `OPEN` | `OPEN` / `ANSWERED` / `EXPIRED` / `CANCELLED`（§13 CR-03） |
| sent_via | enum `IntakeChannelKind` | N | — | 发送时的原开口 |
| sent_at | ts | N | — | |
| due_at | ts | N | — | sent_at + clarification_timeout_hours |
| reminders_sent | int | N | 0 | |
| next_reminder_at | ts | Y | NULL | 关闭时置 NULL；索引 `(next_reminder_at)` 供提醒任务 |
| outbound_message_id | id | Y | NULL | 对应 intake_messages 的出站记录 |
| closed_at / close_reason | ts / enum | Y | NULL | `ALL_ANSWERED` / `TIMEOUT` / `CANCELLED_BY_TEAM` / `INTAKE_RESOLVED` |
| version | int | N | 1 | |

### 3.11 `intake_clarification_questions` 澄清问题

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK |
| intake_id | id | N | — | |
| round_id | id | Y | NULL | DRAFT 时为 NULL，发送时写入 |
| seq | int | N | — | 轮内序号 1..10，邮件中显示为 `Q<seq>` |
| state | enum `ClarificationQuestionState` | N | `DRAFT` | `DRAFT` / `SENT` / `ANSWERED` / `WITHDRAWN` / `EXPIRED`（§13 CR-03） |
| text | text2k | N | — | |
| author_kind / author_id / authority_source | — | — | — | 起草者（协调员 Agent 或人） |
| answer_text | str(10000)（MySQL `TEXT`） | Y | NULL | |
| answered_at | ts | Y | NULL | |
| answered_via | enum | Y | NULL | `PORTAL` / `EMAIL` / `API` / `RECORDED_BY_TEAM` |
| answer_actor_kind / answer_actor_id | enum / id | Y | NULL | CLIENT + contact_id，或 HUMAN（团队代录） |
| answer_message_id | id | Y | NULL | 回答来自哪条入站消息 |
| version | int | N | 1 | |

索引：`(tenant_id, intake_id, state)`、`(tenant_id, round_id, seq)` 唯一。

### 3.12 `intake_messages` 线程日志（不可变）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK |
| intake_id | id | N | — | |
| direction | enum | N | — | `INBOUND` / `OUTBOUND` |
| kind | enum | N | — | `SUBMISSION` / `RECEIPT` / `CLARIFICATION` / `REMINDER` / `ANSWER` / `CLIENT_NOTE` / `RESOLUTION_NOTICE` / `DELIVERY_NOTICE` / `ACCEPTANCE_RECEIPT` |
| channel_kind | enum | N | — | |
| actor_kind / actor_id | enum / id | N / Y | — | 甲方为 CLIENT |
| body | text64k | N | — | 出站为渲染后的纯文本；入站为去除引用后的正文（完整原文见 raw MIME） |
| body_hash | hash | N | — | |
| email_message_id | str(998) | Y | NULL | 出站自生成 `<{id}.intake@{inbound_domain}>`；入站取头部；`UNIQUE(tenant_id, email_message_id)` |
| in_reply_to | str(998) | Y | NULL | |
| question_ids | json | N | `[]` | 本消息涉及/回答的问题 |
| needs_mapping | bool | N | false | 入站邮件回答无法自动对应问题时置 true |
| delivery_state | enum | N | `NOT_APPLICABLE` | 出站：`PENDING`/`SENT`/`FAILED`/`SUPPRESSED`（由消费 09 `notification.delivery_state_changed` 回写，按 `source_ref` 匹配；`SUPPRESSED` = 因偏好 / 退订 / 联系人无邮箱而未发送） |

### 3.13 `intake_email_messages` 入站邮件登记

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK |
| channel_id | id | N | — | 由收件地址 local-part 解析 |
| message_id_header | str(998) | N | — | 缺失时用 SHA-256(raw) 生成 `<sha256@missing>`；`UNIQUE(tenant_id, message_id_header)` 去重 |
| from_address / to_address | str(254) | N | — | 小写 |
| subject | str(998) | N | `''` | |
| size_bytes | int | N | — | |
| auth_result | json | N | `{}` | `{ "spf": "pass|fail|none", "dkim": "...", "dmarc": "..." }`（来自 MTA 的 Authentication-Results） |
| spam_score | str(16) | Y | NULL | 头部原值 |
| disposition | enum `EmailDisposition` | N | `PENDING` | `PENDING` / `CREATED_INTAKE` / `APPENDED` / `QUARANTINED` / `RELEASED` / `DISCARDED` / `REJECTED` / `FAILED`（§13 CR-03） |
| disposition_reason | enum | Y | NULL | `UNKNOWN_SENDER` / `SPAM` / `DMARC_FAIL` / `AUTO_REPLY` / `LOOP` / `BLOCKED_CONTACT` / `TOO_LARGE` / `PARSE_ERROR` / `UNMATCHED_REPLY` / `CHANNEL_DISABLED` / `QUOTA` |
| intake_id / message_id | id | Y | NULL | 处理结果 |
| raw_file_id | id | N | — | files 绑定 purpose=`intake.raw-mime`，原始 MIME 永久保存 |
| attempts | int | N | 0 | 解析尝试次数 |
| version | int | N | 1 | |

### 3.13b `intake_attention_daily` 人工关注时长（按日汇总）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | id | N | — | PK |
| principal_id | id | N | — | 人类成员 |
| project_id | id | Y | NULL | 来件已关联项目（转化后或分诊卡建议项目）时填，否则 NULL（计入团队级） |
| day | date | N | — | UTC 日 |
| attention_ms | int | N | 0 | `UNIQUE(tenant_id, principal_id, project_id_key, day)`；MySQL/SQLite 用 `project_id_key = COALESCE(project_id, 0)` 生成列 |

采样规则与 02 评审台一致：来件详情页可见、聚焦且 60s 内有输入时每 15s 发一次心跳（`POST /intakes/{id}/attention-heartbeats {elapsed_ms ≤ 20000}`），服务端按日 upsert 累加；同一人多设备并行累加。供 07 指标「人工关注分钟」经 `IntakeReader.AttentionByDay` 汇总。

### 3.14 幂等（不建模块表）

所有创建 / 命令型 POST 的 `Idempotency-Key` 由平台统一幂等辅助处理（09 C7 单表，00 §9）：同 key 同请求返回首次结果；同 key 不同请求返回 `176425`；进行中返回 `176409`。门户请求的主体为 `subject_kind=INTAKE_CONTACT`、`subject_id=contact_id`。领域事件消费去重统一使用 09 `domain_event_receipts`；业务唯一键（如 `origin_acceptance_id`）照常保留。

### 3.15 文件绑定（底座 `files` ResourceBinding）

| purpose | resource_kind | slot | 版本 | 过期 | 说明 |
|---|---|---|---|---|---|
| `intake.attachment` | `intake` | `submission` / `message:<message_id>` / `raw-overflow` / `client-approval:<round>` | pinned | 否 | 来件与回答附件；`client-approval:<round>` 为团队登记甲方线下确认的证据（T19） |
| `intake.upload` | `intake_contact` | `pending` | pinned | 是（24h） | 门户 / API 先上传后提交；提交时同事务解绑并改绑到来件 |
| `intake.raw-mime` | `intake_email_message` | `raw` | pinned | 否 | 原始 .eml |
| `intake.branding` | `intake_client_settings` | `logo` | head | 否 | |

文件存放于租户根下由系统维护的目录 `/.intake/<yyyy>/<mm>/`，写入需要 `files` 提供租户级业务文件写入能力（与现有 `AccountResourceFiles` 同形），见 §12 依赖 D-09-5。

---

## 4. 状态机

### 4.1 来件 `IntakeStatus`

00 将 PRD §6.1 的 `ACCEPTED_AS_REQUIREMENT` 定名为 `CONVERTED`，本文沿用 00。`CLIENT_APPROVAL`（待甲方确认，D117）只出现在 kind=CHANGE 的来件上，经 T15 进入、T16–T19 离开，不是终态。

| # | 当前 | 触发命令 / 事件 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| T1 | — | 提交（门户 / 邮件 / API / 团队代录 / 07 拒收 / 变更申请 kind=CHANGE） | 开口 ENABLED；联系人 ACTIVE；配额与大小限制；kind=CHANGE 另见 BR-01-76/77 | RECEIVED | 事件 `intake.received`；审计 `intake.intake_received`；回执（send_receipt=true 时经原开口）；通知 §9 N1 |
| T2 | RECEIVED | 提交分诊卡（revision=1） | 卡片校验通过；作者有 `intake_triage` | TRIAGED | `intake.triaged`；通知 N2 |
| T3 | TRIAGED / CLARIFYING | 提交分诊卡（新 revision） | 同上 | 不变 | `intake.triaged`（含 revision） |
| T4 | RECEIVED / TRIAGED | 发送澄清轮 | ≥1 条 DRAFT 问题且 ≤10；无 OPEN 轮；轮数 <20 | CLARIFYING | 新建 OPEN 轮；问题 DRAFT→SENT；经原开口发送；`intake.clarification_requested`；审计 |
| T5 | CLARIFYING | 甲方回答（部分） | 问题属于 OPEN 轮且为 SENT | 不变 | 问题→ANSWERED；`intake.clarification_answered`；清除 client_unresponsive |
| T6 | CLARIFYING | 甲方回答使本轮全部问题非 SENT | — | TRIAGED | 轮→ANSWERED(ALL_ANSWERED)；`intake.clarification_answered{round_closed:true}`；自动发出 `intake.triage_requested`（协调员重新分诊）；通知 N4 |
| T7 | CLARIFYING | 团队取消本轮 | 有 `intake_triage` | TRIAGED | 轮→CANCELLED；未答问题→WITHDRAWN |
| T8 | CLARIFYING | 超时任务：now ≥ due_at | 轮 OPEN | TRIAGED | 轮→EXPIRED(TIMEOUT)；SENT 问题→EXPIRED；client_unresponsive=true；`intake.clarification_expired`；通知 N5。「本规格提议」超时**不自动拒绝**：拒绝属产品决定，交负责人判断 |
| T9 | TRIAGED | 转为需求 | 见 BR-01-20..28；kind=CHANGE 另见 BR-01-81 | CONVERTED | 同事务调用 02（及 07）创建 / 追加 / 重开；`intake.converted`；审计；通知甲方 N8 |
| T10 | CLARIFYING | 转为需求 + `open_questions_ack=true` | 同 T9；调用者为 HUMAN | CONVERTED | OPEN 轮→CANCELLED(INTAKE_RESOLVED)，未答问题→WITHDRAWN；其余同 T9 |
| T11 | RECEIVED / TRIAGED / CLARIFYING | 合并 | 目标同租户、≠自身、状态 ∉ {MERGED, DECLINED} | MERGED | OPEN 轮关闭；`intake.merged`；审计；通知甲方（可选） |
| T12 | RECEIVED / TRIAGED / CLARIFYING | 拒绝 | `decline_reason_code` + `decline_reason` 必填 | DECLINED | OPEN 轮关闭；`intake.declined`；审计；`notify_client`=true 时经原开口发 client_message |
| T13 | 任意 | 甲方新消息（邮件回复 / 门户留言） | 门户留言（〔补充说明〕）仅限 RECEIVED / TRIAGED / CLARIFYING（BR-01-85） | 不变 | 记 CLIENT_NOTE；终态来件时通知 N6（「已结束来件收到新消息」），不改变状态 |
| T14 | CONVERTED | 02 `ClarificationRequester.RequestFromWorkItem`（工作项因澄清阻塞） | 无 OPEN 轮；轮数 <20；问题 1..10 | 不变（仍 CONVERTED） | 新建 OPEN 轮（`work_item_id` 非空）并立即经原开口发送；`intake.clarification_requested{work_item_id}`；回答 / 超时事件同样携带 `work_item_id`，由 02 解除阻塞；有 OPEN 轮时问题追加为 DRAFT 并通知 N4（「本规格提议」：转化后的澄清不改变来件状态，只是线程延续） |
| T15 | TRIAGED | 请甲方确认（`PUT /intakes/{id}/client-approval`） | kind=CHANGE；无 OPEN 轮；最新卡片含 `change_impact` 且 `requires_client_approval=true`；`triage_card_revision` = 最新；`base_version_no` = 02 当前版本；actor_kind=HUMAN 且对目标需求所属项目持 `scope_change`（经 `AuthorityChecker`，不可委托）（BR-01-79） | CLIENT_APPROVAL | `client_approval` = {PENDING, round+1, card_revision}；写给甲方的说明（OUTBOUND）；`intake.client_approval_requested`；审计 `intake.client_approval_requested`；通知甲方 N14（经原开口） |
| T16 | CLIENT_APPROVAL | 甲方在门户〔接受〕（`decision=APPROVED`） | 门户会话联系人 = 来件联系人；`approval_round` = 当前 round；actor_kind=CLIENT | TRIAGED | `client_approval.state=APPROVED`、`decided_via=PORTAL`、`authority_source=CLIENT:<contact_id>`；`intake.client_approval_decided{decision:APPROVED}`；审计；通知团队 N16 |
| T17 | CLIENT_APPROVAL | 甲方在门户〔不接受〕（`decision=REJECTED`） | 同 T16；`comment` 10..2000 | TRIAGED | `client_approval.state=REJECTED` + comment；`intake.client_approval_decided{decision:REJECTED}`；审计；通知团队 N16。团队可改卡后再次 T15、按容差内方案转化（需新 revision 且 `requires_client_approval=false`）或拒绝（BR-01-83） |
| T18 | CLIENT_APPROVAL | 团队撤回确认请求 | actor_kind=HUMAN；`intake_triage`；`reason` 1..2000 | TRIAGED | `client_approval.state=WITHDRAWN`；`intake.client_approval_decided{decision:WITHDRAWN}`；审计 `intake.client_approval_withdrawn`；通知甲方 N8（「团队已撤回本次确认请求」） |
| T19 | CLIENT_APPROVAL | 团队登记甲方线下确认（电话、会议、邮件） | actor_kind=HUMAN；对目标项目持 `scope_change`（经 `AuthorityChecker`）；`evidence_file_ids` 1..5；`offline_note` 1..2000 | TRIAGED | `client_approval.state=APPROVED`、`decided_via=RECORDED_BY_TEAM`、`decided_actor_kind=HUMAN`；证据绑定 `client-approval:<round>`；`intake.client_approval_decided{decision:APPROVED, decided_via:RECORDED_BY_TEAM}`；审计 `intake.client_approval_recorded_offline`；门户显示「由团队记录」 |

**非法迁移**（全部不改变数据，审计记 `outcome=denied` 仅针对权限类）：

| 当前 | 命令 | 错误码 | HTTP |
|---|---|---|---|
| CONVERTED / MERGED / DECLINED | 任何改变状态的命令（分诊卡、澄清、转化、合并、拒绝）；例外：CONVERTED 上经 T14 的工作项澄清 | 110421 `intake.terminal` | 422 |
| RECEIVED | 转为需求（尚无分诊卡） | 110422 `intake.triage_required` | 422 |
| CLARIFYING | 转为需求且 `open_questions_ack` 缺失/false | 110436 `intake.open_questions_unacknowledged` | 422 |
| CLARIFYING | 转为需求、actor_kind=AGENT（即使带 ack） | 110403，`reason_code=NON_DELEGABLE` | 403 |
| RECEIVED / TRIAGED / CLARIFYING | `REOPEN_WORK_ITEMS` 用于 kind≠REWORK 的来件 | 110448 `intake.reopen_requires_rework_intake` | 422 |
| CLARIFYING | 再发送一轮（已有 OPEN 轮） | 110423 `intake.open_round_exists` | 422 |
| RECEIVED / TRIAGED | 发送澄清但无 DRAFT 问题 | 110424 `intake.no_draft_questions` | 422 |
| RECEIVED / TRIAGED | 取消澄清轮（无 OPEN 轮） | 110420 `intake.illegal_transition` | 422 |
| 任意非终态 | 合并到自身 / MERGED / DECLINED / 他租户来件 | 110426 `intake.merge_target_invalid`（他租户返回 110404） | 422 / 404 |
| 任意非终态 | 拒绝缺理由 | 110427 `intake.reason_required` | 422 |
| 任意 | 版本号不匹配 | 110409 `intake.version_conflict` | 409 |
| CLIENT_APPROVAL | 提交新分诊卡 revision、发送澄清轮、转化、合并、拒绝（须先 T18 撤回） | 110451 `intake.awaiting_client_approval` | 409 |
| TRIAGED（kind=CHANGE） | 转化，`requires_client_approval=true` 而 `client_approval` 非 APPROVED，或 APPROVED 的 `card_revision` ≠ 转化所依据的 revision | 110450 `intake.client_approval_required` | 422 |
| TRIAGED（kind=CHANGE） | T15 或 `APPEND_TO_REQUIREMENT` 转化时最新卡片无 `change_impact` | 110452 `intake.change_impact_required` | 422 |
| RECEIVED / TRIAGED / CLARIFYING（kind=CHANGE） | 写入含 `change_impact` 的分诊卡、T15 或转化时 `change_impact.base_version_no` ≠ 02 当前版本，或转化时 `WouldBreach` 复核结果与卡片不一致（`data.current_version_no`, `data.breached`） | 110453 `intake.change_impact_stale` | 409 |
| TRIAGED（kind=CHANGE） | T15 但 `requires_client_approval=false`（容差内无需甲方确认） | 110420 `intake.illegal_transition` | 422 |
| 非 CLIENT_APPROVAL | 甲方决定（T16 / T17）、撤回（T18）、登记线下确认（T19）；或甲方提交的 `approval_round` ≠ 当前 round | 110455 `intake.client_approval_not_pending`（`data.reason ∈ {not_pending, superseded}`） | 409 |
| CONVERTED（门户） | 〔补充说明〕（`POST /portal-intakes/{id}/notes`）——应改用〔申请变更〕 | 110454 `intake.change_request_required` | 422 |
| RECEIVED / TRIAGED / CLARIFYING（kind=CHANGE） | 拒绝但缺 `client_message`（变更申请的拒绝必须告诉甲方理由） | 110427 `intake.reason_required`（`data.field=client_message`） | 422 |
| 原来件 ≠ CONVERTED（或无 `requirement_id`） | 在其上创建 kind=CHANGE 来件 | 110449 `intake.change_original_not_converted` | 422 |
| TRIAGED / CLIENT_APPROVAL（kind=CHANGE） | T15 / T18 / T19 由 AGENT（或服务账号、PAT）发起；任何团队主体调用门户决定接口 | 110403，`reason_code` = `NON_DELEGABLE`（T15 / T19，`scope_change` 不可委托）/ `HUMAN_ONLY`（T18）；门户决定只接受该来件联系人的 CLIENT 会话（其他会话 110404） | 403 / 404 |

### 4.2 澄清轮 `ClarificationRoundState`

| 当前 | 事件 | 目标 | 非法时 |
|---|---|---|---|
| — | 发送 | OPEN | — |
| OPEN | 全部问题非 SENT | ANSWERED | — |
| OPEN | 团队取消 / 来件进入终态 | CANCELLED | — |
| OPEN | 超时 | EXPIRED | — |
| ANSWERED / CANCELLED / EXPIRED | 任何命令 | 不变 | 110425 `intake.question_not_open`（回答）/ 110420（取消） |

### 4.3 澄清问题 `ClarificationQuestionState`

`DRAFT →(发送) SENT →(回答) ANSWERED`；`DRAFT →(删除/撤回) WITHDRAWN`；`SENT →(轮取消) WITHDRAWN`；`SENT →(轮超时) EXPIRED`。**EXPIRED 的问题仍可被回答**：甲方迟到的回答写入 ANSWERED 并清除 client_unresponsive，但不重开已关闭的轮、不改变来件状态（「本规格提议」，丢弃甲方回答没有任何好处）。对 ANSWERED / WITHDRAWN 问题再次回答 → 110425；编辑非 DRAFT 问题 → 110420。

### 4.4 入站邮件 `EmailDisposition`

`PENDING →` `CREATED_INTAKE` | `APPENDED` | `QUARANTINED` | `DISCARDED` | `REJECTED` | `FAILED`（解析重试 5 次仍失败）；`QUARANTINED → RELEASED`（随即按放行结果写 intake_id/message_id）| `DISCARDED`；`FAILED → RELEASED | DISCARDED`。其余迁移 → 110420。

### 4.5 联系人与链接

联系人：`ACTIVE ⇄ BLOCKED`；归档（archived_at）仅当无非终态来件，否则 110420。链接：签发 → ACTIVE；吊销 → REVOKED（不可恢复，重新签发即可）；到期 → EXPIRED。吊销已吊销链接是幂等的，返回 200 与原吊销记录。

---

## 5. 业务规则与边界情况

**来件与原文**

- **BR-01-01** 原文不可改：来件创建后 §3.8 列出的不可变列没有更新路径；团队的理解与修改只体现在分诊卡 revision 与转化后的需求中。
- **BR-01-02** 长度：标题 1..200 字；门户描述 1..20,000 字（「本规格提议」：表单输入超过 2 万字说明应改用附件）；API / 邮件正文 ≤64KB 内联，超出存为 `raw-overflow` 文件，raw_content=NULL，详情页给出下载。
- **BR-01-03** 附件：单文件 ≤25MB；每条来件 ≤10 个、合计 ≤50MB；每条回答 ≤5 个、合计 ≤25MB；整封邮件 MIME ≤35MB。拒绝类型（扩展名或嗅探 MIME 命中即拒）：`exe dll msi bat cmd com scr ps1 vbs js jar apk app dmg sh`。超出 → 110413 / 110439；类型 → 110415。
- **BR-01-04** 配额：门户每联系人每日（租户时区 UTC 计）≤`max_submissions_per_contact_per_day`（默认 20）；API 每开口每日 ≤`max_intakes_per_day`（默认 1000）；超出 → 110433，HTTP 429 + `Retry-After`（距次日 0 点秒数）。邮件来件计入同一联系人配额，超出 → disposition=QUARANTINED、reason=`QUOTA`，由人放行（邮件无法同步拒绝，丢弃会丢失甲方需求）。
- **BR-01-05** 开口停用：DISABLED 只阻止**新来件**（110430）；已存在来件的澄清作答、验收、邮件线程回复照常处理（「本规格提议」：避免在途工作被卡死）。
- **BR-01-06** 去重提示：同一联系人 72h 内 `raw_content_hash` 相同的新来件仍然创建，但分诊卡与列表显示「可能重复：IN-x」，由人决定合并。不自动合并（合并会让甲方看到状态变化，属于人的判断）。
- **BR-01-07** `ref_no` 分配：`MAX(ref_no)+1` 在写事务中插入，唯一冲突重试 ≤3 次，仍失败返回 110503。

**分诊**

- **BR-01-10** 触发：来件进入 RECEIVED 时发布 `intake.received`，`data.triage_agent_id` 取开口的 `triage_agent_id`；05 消费该事件与 `intake.triage_requested` 并向该 Agent 下发 `agent.triage` 指令（05 §6.6），Agent 未运行或 30 分钟内未产出分诊卡时，由本模块 BR-01-11 看门狗触发 N7（05 不回调本模块）。Agent 以自身服务账号调用 `POST /intakes/{id}/triage-cards`（需 `intake_triage`）。未配置协调员时由人手写分诊卡。
- **BR-01-11** 分诊看门狗：RECEIVED 超过 30 分钟仍无分诊卡 → 通知 N3 一次（每条来件只发一次）。
- **BR-01-12** revision 并发：写入卡片时 `UPDATE intake_records SET latest_card_revision = r WHERE id=? AND latest_card_revision = r-1`；影响 0 行 → 110409，客户端重新读取后再提交。人工编辑卡片必须带 `based_on_revision`，若 ≠ 最新 → 110443 `intake.card_stale`（防止覆盖 Agent 刚提交的新稿）。
- **BR-01-13** 协调员权限边界：协调员可写分诊卡、起草澄清问题、发起 `triage-requests`；**不可**发送澄清轮以外的对外消息、不可转化 / 合并 / 拒绝（除非经委托，见 BR-01-24）。「本规格提议」协调员**可以发送**澄清轮：PRD §3.2 协调员职责含「催办」，且澄清不改变范围；团队可在开口 config 关闭（`agent_may_send_clarifications`，默认 true）。

**澄清**

- **BR-01-14** 每条来件同时至多 1 个 OPEN 轮；每轮 1..10 个问题；每条来件至多 20 轮（110433）。问题 1..2000 字，回答 1..10,000 字。
- **BR-01-15** 发送路径 = 来件原开口：PORTAL → 门户内展示，且联系人有邮箱时发邮件通知（含门户入口说明，**不含**访问链接明文，联系人用已有链接进入）；EMAIL → 同一邮件线程回复；API → 审计事件经 webhook 回调，调用方拉取问题；IM v2。
- **BR-01-16** 提醒：OPEN 轮在 sent_at + `reminder_interval_hours`×k（k=1..max_reminders）发送提醒（默认 48h、96h），到 due_at（默认 168h）超时。提醒与超时由任务 J3 执行，同一轮同一序号的提醒只发一次（`reminders_sent` 条件更新）。
- **BR-01-17** 邮件回答映射：轮内仅 1 个问题 → 整封回复即为回答；多个问题 → 解析去引用正文中的 `Q<n>`/`<n>.`/`<n>)` 段落，段数与问题数一致才自动映射；否则消息标 `needs_mapping=true`，问题保持 SENT，通知 N4b 由团队代录（`recorded-answer`）。
- **BR-01-18** 团队代录回答（电话、会议）：answered_via=`RECORDED_BY_TEAM`，actor 为录入人，甲方门户显示「由团队记录」。

**转化**

- **BR-01-20** 模式：`NEW_REQUIREMENT`（已有项目）、`NEW_PROJECT`（新建项目 + 需求）、`APPEND_TO_REQUIREMENT`（追加到已有需求 = 范围变更）、`REOPEN_WORK_ITEMS`（仅 kind=REWORK 返工来件：把被甲方拒收的工作项按质量返工重开，见 BR-01-28）。
- **BR-01-21** 请求必须携带 `triage_card_revision` = 当前最新 revision，否则 110443；APPEND 另须携带 `base_version_no`（需求当前版本，不符 → 110409）；NEW_PROJECT 与 APPEND 须携带 `reauth_proof`（底座 step-up，purpose `intake_convert_privileged`，target `intake:<id>`，有效 5 分钟），缺失或过期 → 401 110447；请求中的 title/description/acceptance_criteria 若与卡片不同，服务端先写一条 HUMAN revision 再转化（同一事务），保证「转化所依据的草案」永远可追溯。
- **BR-01-22** 权限：`intake_convert`（团队级）+ 目标项目上的 `requirement_write`（02，DataScoped，经 `AuthorityChecker`）；APPEND 另需 `scope_change`（**不可委托**）；NEW_PROJECT 另需 07 的项目创建权限，且 actor_kind 必须为 HUMAN、authority_source 必须为 `ROLE:*`。
- **BR-01-23** 目标项目阶段 ∈ {INTAKE, DISCOVERY, PLANNING, ACTIVE, BLOCKED, ACCEPTANCE_PENDING}；DELIVERED / CLOSED / CANCELLED → 110435。
- **BR-01-24** 全自治委托：actor 为 Agent 时，仅允许 `NEW_REQUIREMENT`，且 ① 目标项目 TeamMode=AUTONOMOUS；② `AuthorityChecker.Check(ctx, tx, {action: intake_convert, project_id, facts{intake_convert_mode: NEW_REQUIREMENT, triage_card_author}})` 返回 `DELEGATION:<id>`；③ 来件状态为 TRIAGED（不允许 ack 未答问题）；④ 转化者 ≠ 所依据分诊卡的作者（与 PRD §2.3「不得裁决自己的产出」同理）。委托的 `intake_convert` **同时覆盖**该次在目标项目内创建需求所需的 `requirement_write`（不另行检查，08 判定时据 `intake_convert_mode` 处理）。任一不满足 → 403 110403（`reason_code` 取 08 返回值，如 `NON_DELEGABLE` / `SELF_REVIEW` / `NO_DELEGATION`），审计 `intake.conversion_denied`。NEW_PROJECT、APPEND、REOPEN 与拒绝在 AUTONOMOUS 下仍进入 08「我的裁决」`INTAKE_TRIAGE` 由人处理。
- **BR-01-25** 原子性：转化在 intake 的写事务内调用（backend-module「委派」模式，由下游在调用方事务里执行自己的语句）：NEW_PROJECT → 07 `ProjectCreator.CreateForIntakeOn(ctx, tx, …)`（含默认频道与联系人设为 PRIMARY）后再 02 `RequirementCreator.CreateFromIntake`；NEW_REQUIREMENT → 02 `CreateFromIntake`；APPEND → 02 `AppendFromIntake`（产生 SCOPE 变更记录与需求新版本）；REOPEN → 02 `WorkItemReopener.ReopenFromAcceptance`（BR-01-28）。任一失败整体回滚，返回 110502（下游业务错误按其码映射为 110435 / 110403 / 110409）。
- **BR-01-26** 转化幂等：同一来件已 CONVERTED 且请求的 `mode + project_id + triage_card_revision` 与已记录一致 → 200 返回已有结果；不一致 → 110421。
- **BR-01-27** 返工来件（kind=REWORK，由 `acceptance.received` 创建）的分诊卡自动带入 `rejected_items[{work_item_id, delivery_item_id, feedback_category, comment}]`（来自 07 `AcceptanceReader.Get`），目标项目 = 原交付所属项目。处置四选一，默认按拒收条目的 `feedback_category`：`FUNCTIONAL_DEFECT` / `STYLE` / `PERFORMANCE` / `SECURITY` → `REOPEN_WORK_ITEMS`；`REQUIREMENT_MISMATCH` → `APPEND_TO_REQUIREMENT`（范围变更）；`OTHER` → 无默认，由人选择；另可 `NEW_REQUIREMENT` 或拒绝（理由必填，甲方收到 client_message）。混合类别时界面按条目分组，可拆成「重开」+「追加」两次转化（第二次在同一来件上以 `APPEND` 追加时来件已 CONVERTED → 允许，作为 T9 的同来件补充转化，幂等键不同）。
- **BR-01-28** `REOPEN_WORK_ITEMS`：仅 HUMAN、持 `decision_request_changes`（目标项目，经 `AuthorityChecker`）；请求列出 `work_item_ids[]`（必须 ⊆ 分诊卡 `rejected_items`，否则 110400）；同事务调用 02 `WorkItemReopener.ReopenFromAcceptance(ctx, tx, {tenant_id, work_item_ids[], source_acceptance_id: origin_acceptance_id, feedback[], reason, actor})`（`intake_id` 记入审计 detail；签名以 02 §6.9 为准），02 将每项 ACCEPTED → REWORK（QUALITY_REJECTION）并创建返工分支；来件 → CONVERTED，`requirement_id` 记原需求。项目阶段须 ∈ {ACTIVE, BLOCKED, ACCEPTANCE_PENDING}，否则 110435。

**合并与拒绝**

- **BR-01-30** 合并需要 `intake_convert`；目标可为 RECEIVED/TRIAGED/CLARIFYING/CONVERTED。源来件附件与线程不移动；源的后续甲方邮件回复追加到**目标**线程并标记「来自已合并 IN-x」。
- **BR-01-31** 合并后门户：源与目标同属一个联系人时显示「已合并到 IN-y」并可跳转；不同联系人时只显示「已与其他需求合并处理」，不泄露目标内容。
- **BR-01-32** 拒绝需要 `intake_decline`；**不可委托给 Agent**（00 §6.4；拒绝直接对甲方表态）。Agent 调用 → 403 110403 `reason_code=NON_DELEGABLE`。`decline_reason_code=SPAM` 时强制 notify_client=false。
- **BR-01-33** 拒绝的来件不可重开；甲方需要重提时提交新来件（门户详情页给出「重新提交」并预填原文）。

**邮件**

- **BR-01-40** 品牌回退链（门户、邮件 From 显示名、页面标题一致）：`intake_client_settings.display_name` → 租户显示名（底座 organization）→ 部署 `GET /deployment` 的 `name` → 中性文案 i18n `intake.portal.neutral_title`（zh-CN「需求门户」）。**任何位置不出现硬编码品牌**；`show_platform_attribution=true` 时页脚显示部署 `name`（为空则不显示）。
- **BR-01-41** 收件路由：`To/Cc/Delivered-To` 中 `<local>[+<tag>]@<inbound_domain>` 的 local 匹配 `inbound_local_part`；无匹配 → REJECTED（webhook 返回 202 以免泄露地址存在性，记指标）。
- **BR-01-42** 线程识别顺序：① `In-Reply-To`/`References` 命中本租户 `intake_messages.email_message_id`；② 加号标签 `+r<reply_token>` 命中；③ 均不命中 → 新来件。①② 命中时发件人必须等于该来件联系人邮箱，或与之同域且 sender_policy≠ALLOWLIST 时进入隔离（`UNMATCHED_REPLY`）；Subject 中的 `[IN-123]` 仅作展示，**不作为**匹配依据（易伪造）。
- **BR-01-43** 发件人策略：`OPEN` 任何人可建来件并自动建联系人；`KNOWN_CONTACTS`（默认）仅已存在的 ACTIVE 联系人，否则隔离 `UNKNOWN_SENDER`；`ALLOWLIST` 仅命中 allowlist 的地址/域。BLOCKED 联系人 → REJECTED，不回执。
- **BR-01-44** 垃圾与环路：DMARC=fail → 隔离 `DMARC_FAIL`；`X-Spam-Flag: YES` 或 `X-Spam-Score` ≥ 阈值 → 隔离 `SPAM`；`Auto-Submitted` ≠ `no`、`Precedence: bulk|list|junk`、`X-Autoreply` → DISCARDED `AUTO_REPLY`（不回执，防回复风暴）；From 为任一开口地址 → DISCARDED `LOOP`。
- **BR-01-45** 去重：`UNIQUE(tenant_id, message_id_header)`；重复投递直接返回已有登记，不重复建来件。
- **BR-01-46** 解析：优先 `text/plain`；仅 HTML 时提取纯文本（去 script/style，保留链接文字），format=`HTML_TEXT`；回复正文去除 `>` 引用与「On … wrote:」/「在 … 写道：」之后内容作为 `body`，完整内容以 raw MIME 保存；内联图片与附件按 BR-01-03 校验，单个超限附件被跳过并在来件详情标注「附件 x 超出上限未导入」，不拒整封邮件。
- **BR-01-47** 隔离放行：`intake_triage` 持有者可放行（按正常流程建来件或追加，`OPEN` 策略外的未知发件人放行时可勾选「同时创建联系人」）或丢弃；隔离不自动清理（保留期属数据保留策略，见 §12 Q3）。

**API 与回执**

- **BR-01-50** API 调用方必须是开口 `allowed_principal_ids` 之一（PAT 或 OAuth client credentials 服务账号），持有 `intake_submit`；否则 403 110403（`reason_code=CHANNEL_PRINCIPAL_NOT_ALLOWED`）。首次调用自动建 kind=SYSTEM 联系人；请求可带 `contact{display_name,email}` 指定真实联系人（按邮箱匹配/创建）。
- **BR-01-51** API 调用方只能读取自己提交的来件（`submitted_by_principal_id = 调用者`），他人来件返回 110404。
- **BR-01-52** 回执回调复用底座 `iam/webhook`：intake 把对外有意义的状态变化写入审计链（§7.3），开口 `callback_subscription_id` 指向的订阅按事件类型投递（HMAC-SHA256 签名、`Idempotency-Key` = 投递 id、最多 8 次指数退避重试，均为底座既有行为）。载荷只含 `event_type/target_id(intake id)/occurred_at`，调用方再用 `GET /intake-submissions/{id}` 拉取，正文不出现在回调中。

**门户安全**

- **BR-01-60** 访问 token：32 字节 CSPRNG，base64url（43 字符），仅存 SHA-256；同联系人同时 ACTIVE 链接 ≤5（110441）；TTL 1..180 天，默认取开口配置 30 天。
- **BR-01-61** 访问码：联系人有邮箱且 `require_access_code=true` 时，打开链接后向**联系人登记邮箱**发送 6 位数字码，10 分钟有效，单码最多尝试 5 次（超过锁定该会话 → 110412），重发冷却 60s，每链接每小时 ≤5 封。无邮箱联系人只能使用无码链接，且其 TTL 上限 7 天（「本规格提议」：没有第二因素时缩短暴露窗口）。
- **BR-01-62** 会话：HttpOnly、Secure、`SameSite=Strict` cookie `intake_portal_session`（名称不含品牌），Path 限门户 API；写操作须带请求头 `X-Portal-Request: 1`，否则 110444；绝对 12h、空闲 2h。
- **BR-01-63** 每次请求校验：会话未吊销未过期、链接未吊销未过期、联系人 ACTIVE、租户一致；任一失败立即失效（无缓存），返回 110401 / 110410。
- **BR-01-64** 限流（经底座 shared-security，新增作用域 `portal`，见 CR-06）：`POST /portal-sessions` 每 IP 10 次/10 分钟、全局 300 次/分钟；其余门户接口每 IP 120 次/分钟、每会话 120 次/分钟；上传每联系人 30 次/小时。超限 110429 + `Retry-After`。
- **BR-01-65** 未知 token 与已失效 token 区分：哈希未命中 → 110401（不说明原因）；命中但过期/吊销 → 110410，`data.reason ∈ {expired, revoked}` 并显示团队联系方式。命中即证明持有过真实 token，区分不增加猜测收益。
- **BR-01-66** 可见范围：只返回 `contact_id = 会话联系人` 的来件，以及其来件关联的交付包（经 07 校验）；所有 ID 参数先按租户 + 联系人过滤，不匹配一律 110404。门户响应**永不包含**：工作项、执行、Agent 名称与头像、内部备注、分诊卡、decline_reason（只给 client_message）、其他联系人信息、成本。
- **BR-01-67** 门户访问 token 不出现在任何 API URL 中（只在请求体）；前端路由收到 token 后立即兑换会话并 `history.replaceState` 去掉 token，页面设置 `Referrer-Policy: no-referrer`（见 CR-07）。
- **BR-01-68** AI 参与展示：`HIDDEN` 不显示任何 AI 标识；`COLLABORATIVE` 在需求卡片与交付页显示「由 AI 与工程师协作完成」；`BY_MODE` 按项目 TeamMode：HYBRID 同上，AUTONOMOUS 显示「由 AI Agent 完成，团队负责人监督」，尚未转化的来件不显示。澄清问题署名一律为团队显示名，不显示起草 Agent。

**通用**

- **BR-01-70** 幂等：所有创建 / 命令型 POST 与 PUT 支持 `Idempotency-Key`（≤64 字符），经 09 平台幂等辅助（§3.14）：同键同请求体 24h 内返回首次响应（含状态码）；同键不同请求体 → 422 `176425`；首次请求仍在处理 → 409 `176409`。门户以 `INTAKE_CONTACT:<contact_id>`、API 以 principal 作为主体键空间。
- **BR-01-71** 乐观锁：GET 单项返回 `ETag: "<version>"`；PATCH/PUT 命令须带 `If-Match`（与 `internal/todo` 一致），缺失 → 428 110446，不匹配 → 409 110409 + `data.current_version`。
- **BR-01-75** 权限拒绝统一 403 110403，`data.reason_code` 说明具体原因（`MISSING_PERMISSION`（附 `data.permission`）/ `NON_DELEGABLE` / `SELF_REVIEW` / `NO_DELEGATION` / `CHANNEL_PRINCIPAL_NOT_ALLOWED` / `HUMAN_ONLY`），00 §9。
- **BR-01-72** 跨租户：任何全局短地址（`/intakes/{id}` 等）由 owner resolver 解析租户后再授权，他租户资源一律 110404。
- **BR-01-73** 数据源不可用：路由照常注册并返回 110503，`slog.Warn` 说明原因（backend-module §五）。
- **BR-01-74** 离线：团队控制台断网时命令不排队，按钮禁用并显示重连态；门户提交表单本地保留草稿（sessionStorage，含失败重试），不自动重发命令。

**需求变更申请（kind=CHANGE，D117）**

- **BR-01-76** 创建：甲方不能原地修改已提交的需求（BR-01-01）；来件 CONVERTED 后门户在该来件上提供〔申请变更〕（`POST /portal-intakes/{id}/change-requests`），API 调用方以 `POST /tenants/{tenant_id}/intakes {kind: CHANGE, change_of_intake_id}` 提交，团队可代录（同一接口，需 `intake_submit`）；邮件开口 v1.1 支持（MVP 中对已转化来件的邮件回复仍按 T13 记 CLIENT_NOTE 并通知 N6，由团队代为创建变更申请）。原来件须 CONVERTED 且 `requirement_id` 非空，否则 110449；原来件本身可以是已转化的 CHANGE（链式变更，`target_requirement_id` 取其 `requirement_id`）。新来件 `contact_id` = 原来件联系人（团队代录时强制取原联系人）、`channel_id` = 提交所用开口、`target_requirement_id` = 原来件 `requirement_id`，走 T1，计入 BR-01-04 配额，标题 / 描述 / 附件限制同 BR-01-02/03。同一原来件可有多条进行中的变更申请（各自评估；并发见 BR-01-87）。
- **BR-01-77** 可见性：门户只能在**本联系人**可见的原来件上申请变更（BR-01-66，否则 110404）；API 调用方只能在自己提交的原来件上申请（BR-01-51，否则 110404）。原来件被合并或拒绝的不可申请（110449）。变更申请在门户中挂在原来件之下展示（§8.4），不进入「我的需求」顶层列表，原来件卡片显示「变更待你确认 n」徽标。
- **BR-01-78** 影响评估起草：协调员 Agent 与人都可提交含 `change_impact` 的分诊卡（`intake_triage`）。写卡事务内服务端依次：① 调用 02 `ChangeImpactPreviewer.Preview(ctx, tenant, target_requirement_id, base_version_no, criteria_diff)`（只读）——`current_version_no` ≠ `base_version_no` → 409 110453；`criterion_ref` 不属于目标需求或已退役 → 400 110400；以返回的 STALE / NEW 条目为 `affected_work_items` 基础，作者只能把 STALE 改为 CANCELLED；② 调用 07 `ToleranceReader.WouldBreach(ctx, tenant, target_requirement_id, {schedule_delta_days, cost_delta_micros: internal_cost_delta_micros, scope_delta_criteria})`，写入 `tolerance_check{breached, snapshot, checked_at}` 与 `requires_client_approval = breached ≠ ∅`（07 不可用 → 110503，不写卡；不得按「无越界」降级）。`client_price_delta` 是对甲方的价格表态，只能由 HUMAN 填写（AGENT 卡带该字段 → 400 110400）；系统不计算价格（无计费模型）。Agent 草拟的卡片照常需要人确认后才能请甲方确认或转化。
- **BR-01-79** 请甲方确认（T15）：仅 HUMAN，且经 `AuthorityChecker.Check(ctx, tx, {action: scope_change, project_id: 目标需求所属项目})` 允许（`scope_change` 不可委托，00 §6.4；Agent → 403 110403 `NON_DELEGABLE`）；请求带 `triage_card_revision`（须为最新，否则 110443）与可选 `message`（≤2000，甲方可见）；重新校验 `base_version_no`（110453）。确认期间不得产生新卡片 revision、澄清轮、合并、拒绝或转化（110451）；需要修改影响时先撤回（T18）再改卡、再次 T15（round+1，旧决定失效）。
- **BR-01-80** 甲方决定是甲方行为：只有该来件联系人的已验证门户会话可以〔接受〕/〔不接受〕（`authority_source=CLIENT:<contact_id>`）；任何团队主体、Agent、服务账号、PAT 都不能代为做出门户决定，Agent 也不能登记线下确认（T19 仅 HUMAN 且须 `scope_change`）。线下确认（电话、会议、邮件「同意」）由团队经 T19 登记，必须附 1..5 个证据文件（如邮件原件、会议纪要），门户显示「由团队记录」与登记时间；邮件中的「同意」字样**不会**被自动识别为确认。请求须带 `approval_round` 等于当前 round（防止对已撤回 / 已更新的请求作决定，否则 110455 `superseded`）；同一决定重放幂等返回 200，不同决定 → 110455。
- **BR-01-81** 转化 CHANGE（T9）：默认模式 `APPEND_TO_REQUIREMENT`，`requirement_id` 必须 = `target_requirement_id`（否则 400 110400）；允许 `NEW_REQUIREMENT`（把变更作为独立新需求，项目任选可写项目）；`NEW_PROJECT` 与 `REOPEN_WORK_ITEMS` 不适用（400 110400）。守卫在 BR-01-21/22/23 之上追加：① 最新卡片含 `change_impact`（APPEND 时必需，110452）；② `change_impact.base_version_no` = 当前版本，且服务端重新调用 `WouldBreach` 的 `breached` 与卡片一致（否则 110453，须出新 revision 重新评估）；③ `requires_client_approval=true` 时 `client_approval.state=APPROVED` 且 `client_approval.card_revision` = 本次 `triage_card_revision`（否则 110450）。APPEND 在同事务调用 02 `RequirementCreator.AppendFromIntake`，传入 `Change{CriteriaDiff, Impacts（STALE→STALE、CANCELLED→CANCEL；NEW 不传，由团队之后拆解）, SourceIntakeID, ApprovedAdjustment?}`；`ApprovedAdjustment = {schedule_delta_days, cost_delta_micros: internal_cost_delta_micros, scope_delta_criteria}` 仅在需要且已获甲方确认时传入，由 02 在同事务调用 07 `ToleranceAdjuster.ApplyApprovedChange` 调整容差基线（02 BR-02-63、07 BR-07-62）。下游错误映射：02 `120440` → 409 110453；02 `120439`（需求非 BASELINED）与 07 `127055`（目标需求或项目有未决例外，`data.reason=tolerance_exception_open`）→ 422 110435；其余按 BR-01-25。`NEW_REQUIREMENT` 不调整原需求与项目的容差基线（新需求按 07 常规建立自己的容差；如因此导致项目预测越界，走 07 例外流程）。成功后写 `result_version_no`、发布 `intake.converted`，通知甲方 N8（含新版本号与影响）。
- **BR-01-82** 容差内变更：`requires_client_approval=false` 时不需要甲方确认，团队按 BR-01-81 直接转化；甲方必须被告知结果与影响——N8 转化通知与门户变更详情给出「已接受（需求 vN）」、验收标准变化、进度变化天数、新的预计日期与价格变化（如有）。
- **BR-01-83** 未接受：甲方〔不接受〕（T17）后来件回到 TRIAGED，`client_approval.state=REJECTED` 保留甲方理由；团队可 ① 改卡（新 revision）后再次 T15；② 出容差内的方案（新 revision 且 `requires_client_approval=false`）直接转化；③ 拒绝。拒绝 CHANGE（T12）必须填写 `client_message`（110427），且 `notify_client` 强制为 true（`decline_reason_code=SPAM` 除外）；门户显示「未接受（{client_message}）」。原需求不受任何影响。
- **BR-01-84** 甲方可见投影（门户 `PortalChangeView`、API `change_view`，白名单序列化）：变更标题、IN 号、状态标签（`ASSESSING` 评估中 = RECEIVED / TRIAGED / CLARIFYING；`AWAITING_YOUR_APPROVAL` 待你确认 = CLIENT_APPROVAL；`ACCEPTED` 已接受（需求 v`result_version_no`）= CONVERTED；`NOT_ACCEPTED` 未接受（理由 = `client_message`）= DECLINED；MERGED 按 BR-01-31）、验收标准差异（新增 / 移除 / 改为，通俗文字取 `criteria_diff[].text`）、`schedule_delta_days`、`new_expected_date`、`client_price_delta`（仅在已填写时）、团队说明（T15 的 `message`）、确认记录（状态、时间、`decided_via`，团队登记时标「由团队记录」）、待答澄清问题。**永不包含**：`internal_cost_delta_micros`、`affected_work_items` 与任何工作项 id、`tolerance_check`（含越界维度与快照）、分诊卡其余字段、内部质量门与内部返工信息（07 BR-07-60）、Agent 信息，以及 BR-01-66 已列项。原来件详情另给出「版本历史」：v1 = 原始需求，之后每条已接受变更一行（vN、变更标题、接受日期）。
- **BR-01-85** 〔补充说明〕边界：门户留言只在来件 RECEIVED / TRIAGED / CLARIFYING 时可用；CONVERTED → 422 110454（引导〔申请变更〕）；CLIENT_APPROVAL → 409 110451（引导在确认页给出意见）；MERGED / DECLINED → 422 110421（DECLINED 提供〔重新提交〕）。邮件回复不受此限（无法拒收邮件），仍按 T13 记录。
- **BR-01-86** 提醒（任务 J7）：甲方——请求后每满 7 天经原开口发 N15，最多 3 次；团队——请求后每满 14 天发 N17 给请求人与 `HOLDERS(intake_convert)`。任何情况下**不自动决定、不自动撤回、不自动拒绝**（与 T8「超时不自动拒绝」同理：替甲方表态不属于系统）。提醒以 `client_approval` 中的 `next_*_reminder_at` 条件更新去重；撤回或决定后不再提醒。
- **BR-01-87** 并发：① 同一需求上的两条变更先后转化，后者的 `base_version_no` 已过期 → 110453，须出新 revision 重新评估（若仍需确认则重新请甲方确认，旧确认不沿用）；② 甲方决定与团队撤回并发：以来件 `version` 条件更新，先提交者生效，后者 409 110455；③ 甲方在 T15 后、决定前原需求被其他途径（02 R10 范围变更）升版：甲方的决定照常记录，但转化时 110453，团队须重新评估并在门户说明。

**甲方锚定批注（D120；数据、可见性与转换归 07 / 03，本模块只提供门户端点与页面）**

- **BR-01-88** 面向甲方的文件：`GET /portal-deliveries/{delivery_id}/files` 先按 BR-01-66 校验交付可见（07 `PortalView` 的 `source_intake_ids ∩ 本联系人来件 ≠ ∅`，否则 110404），再调 07 `DeliveryEvidenceReader.ListClientFacing`（CR-13）。只返回 07 BR-07-63 判定为面向甲方的证据：本交付 SENT、`include_mode=FILE`、未被构建剔除；SUMMARY / FULL_LOG 证据、执行、工作项、日志、内部质量门记录与 INTERNAL 线程**永不出现**（BR-01-66、07 BR-07-60）。响应按白名单序列化 `PortalFile{evidence_id, delivery_item_id, name, size, media_type, viewer_kind, version_ref（内容 sha256）, anchor_kinds[], content_url, content_url_expires_at, client_thread_count}`：`content_url` 为预览域上的短时签名地址（≤15 分钟，过期后重新拉取列表），HTML / 报告 / PDF 只在预览域渲染、不与门户同源；遮罩文件与无查看器的二进制文件只列出（`anchor_kinds=[]`）不可批注。`can_comment` = 交付尚未回执且本联系人对该项目 `can_accept=true`（07 BR-07-31）。
- **BR-01-89** 新建与回复：`POST /portal-deliveries/{delivery_id}/comment-threads` 校验交付可见后调 07 `AcceptanceRecorder.CreateClientComment({delivery_id, evidence_id, intake_contact_id: 会话联系人, anchor_kind, anchor, version_ref, body})`；`POST /portal-comment-threads/{thread_id}/comments` 先确认该线程是本联系人可见交付上的 CLIENT 线程（否则 110404，不区分不存在与不可见），再调 07 `AcceptanceRecorder.ReplyClientComment`（CR-13）。线程可见性固定为 `CommentVisibility.CLIENT`，门户不接受 `visibility` 参数；作者记 `CLIENT:<contact_id>`，不可提及。甲方可用的锚点种类（MVP）：CODE_LINES、TABLE_ROW、HTML_REGION（取点同 03 BR-03-136：门户只接受来自该文件预览源、通过 S9 校验的 `postMessage`，内容只作坐标与引文；不支持元素定位时按位置框选 `RECT_ONLY`）、文档中的 TEXT_PARAGRAPH；MEDIA_TIME 与章节段落为 v1.1；且必须属于该文件的 `anchor_kinds[]`。`version_ref` 须等于列表返回值，内容已变化 → 409 110458。正文 1..2000 字；每线程 ≤200 条（03 BR-03-138）；每联系人每交付每日新建线程 + 回复合计 ≤200，超出 → 429 110433；其余限流同 BR-01-64。交付已回执或不再待验收 → 110440（07 `128030`）；联系人不可签收 → 403 110460；跨交付（证据不属于路径中的交付，含属于同一联系人的另一交付）→ 422 110456。
- **BR-01-90** 读取：`GET /portal-deliveries/{delivery_id}/comment-threads?evidence_id=&view_version=` 经 07 `AcceptanceRecorder.PortalView` 返回本交付上 `visibility=CLIENT` 的线程（可来自本交付的任一可签收联系人；作者只显示联系人显示名），每线程含批注、`anchor_status` / `relocated_anchor`（相对 `view_version`，03 BR-03-137）与 `converted_to_rejection`；INTERNAL 线程、团队成员与 Agent 信息一律不返回。交付回执后线程只读仍可查看。
- **BR-01-91** 拒收项引用批注：`PUT /portal-deliveries/{delivery_id}/acceptance` 的 `items[]` 增加 `comment_thread_ids[]?`（≤20，去重），原样传给 07 `Record`（07 BR-07-64）：只允许在 `result=REJECTED` 的项上；须是本交付、本交付项上的 CLIENT 线程；已转为拒收项的线程不可再引用；任一不符 → 422 110459（`data.reason ∈ {NOT_REJECTED, NOT_THIS_ITEM, NOT_CLIENT_THREAD, ALREADY_CONVERTED}`），整份回执不写入。引用批注不替代拒收理由：`feedback_category` 与 10..2000 字 `comment` 仍必填（110427）；`manifest_sha256_ack` 与回执签名不因引用而变。回执生成的返工来件（BR-01-27、§7.4）在 `rejected_items[]` 元素中带入 07 `AcceptanceReader.Get` 返回的 `anchors[]{thread_id, anchor_kind, anchor_excerpt, version_ref, artifact_ref}`，分诊卡与来件详情按原版本展示锚点。

---

## 6. 接口契约

约定：huma v2 + `respx` 信封；ID 为十进制字符串；时间 RFC 3339 UTC。守卫缩写：**R** = `authz.Register`（团队级，路径含 `{tenant_id}`）；**O** = `authz.RegisterOwned`（全局短地址，owner resolver 由 intake 提供：按 ID 查行得 `ResourceOwner{TenantID, EntityID:0}`，不存在返回 found=false）；**P** = `authz.RegisterPublic` + 门户会话校验中间件（BR-01-62/63）；**PI** = `RegisterPublic` + 入站签名校验。每个全局短地址同时提供 `/tenants/{tenant_id}/…` 作用域地址（routing 规则 7/15，同一服务方法），下表只列短地址。OperationID = `<动词>-<资源>`，如 `list-intakes`。**OpenAPI tag = 最终发布路径（含挂载前缀）的第一段，由底座自动设置**（00 §9）：`/tenants/{tenant_id}/…` 全部为 `tenants`；短地址按首段为 `intakes`、`intake-channels`、`intake-contacts`、`intake-access-links`、`intake-clarification-questions`、`intake-clarification-rounds`、`intake-email-messages`、`intake-submissions`、`inbound-emails`；门户为 `portal-sessions`、`portal-profile`、`portal-uploads`、`portal-intakes`、`portal-questions`、`portal-deliveries`。下文小节标题只是分组，不是 tag。权限拒绝一律 403 110403 + `data.reason_code`（BR-01-75）；If-Match 缺失 428 110446；幂等冲突用平台码 176425 / 176409（BR-01-70）。

### 6.1 开口与甲方设置

| 方法 路径 | 权限 / 守卫 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|
| `GET /tenants/{tenant_id}/intake-channels` | `intake_read` / R | query：`kind`,`status`（eq,in）；排序 `created_at`,`name` | `IntakeChannel[]`（config 中密钥只回 `secret_set:bool`） | 110403 | — |
| `POST /tenants/{tenant_id}/intake-channels` | `intake_channel_manage` / R | `kind*`,`name*`,`config`,`inbound_local_part`(EMAIL*),`triage_agent_id`,`default_project_id`,`notify_principal_ids` | `IntakeChannel`；EMAIL 另含 `inbound_address` | 110400,110431,110438,110445 | Idempotency-Key；201 |
| `GET /intake-channels/{channel_id}` | `intake_read` / O | — | `IntakeChannel` | 110404 | — |
| `PATCH /intake-channels/{channel_id}` | `intake_channel_manage` / O | 任意可变字段 + `status` | `IntakeChannel` | 110400,110409,110438 | If-Match |
| `GET /tenants/{tenant_id}/intake-client-settings` | `intake_read` / R | — | `ClientSettings` + `resolved_display_name` | — | 无行时返回默认值（不建行） |
| `PUT /tenants/{tenant_id}/intake-client-settings` | `intake_channel_manage` / R | `display_name`,`logo_file_id`,`support_email`,`ai_disclosure`,`show_platform_attribution`,`default_locale` | `ClientSettings` | 110400,110409 | If-Match（首次写入 `If-Match: 0`） |

### 6.2 联系人与访问链接

| 方法 路径 | 权限 / 守卫 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|
| `GET /tenants/{tenant_id}/intake-contacts` | `intake_read` / R | query：`state`,`kind`,`email`(eq,like),`display_name`(like)；排序 `display_name`,`created_at` | `IntakeContact[]` + `open_intake_count`、`active_link_count` | — | — |
| `POST /tenants/{tenant_id}/intake-contacts` | `intake_triage` / R | `display_name*`,`email`,`organization_label`,`locale`,`note` | `IntakeContact` | 110400；邮箱已存在 → 409 110409，data 带已有 id | Idempotency-Key；201 |
| `GET /intake-contacts/{contact_id}` | `intake_read` / O | — | `IntakeContact` | 110404 | — |
| `PATCH /intake-contacts/{contact_id}` | `intake_triage` / O | 可变字段、`state`（ACTIVE/BLOCKED）、`archived:true` | `IntakeContact` | 110400,110409,110420 | If-Match |
| `GET /intake-contacts/{contact_id}/access-links` | `intake_read` / O | — | `AccessLink[]`（派生 `state`、`token_hint`，无 token） | 110404 | — |
| `POST /intake-contacts/{contact_id}/access-links` | `intake_channel_manage` / O | `ttl_days`(1..180),`send_email:bool` | `AccessLink` + **`access_url`（含 token，仅此一次）** | 110400,110432,110441 | Idempotency-Key（重放返回 `access_url:null, replayed:true`，明文不二次输出）；201 |
| `PUT /intake-access-links/{link_id}/revocation` | `intake_channel_manage` / O | `reason*`(≤2000) | `AccessLink` | 110404,110427 | 幂等（已吊销返回原记录） |

### 6.3 来件

| 方法 路径 | 权限 / 守卫 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|
| `GET /tenants/{tenant_id}/intakes` | `intake_read` / R | `pkg/query` 过滤：`status`,`channel_kind`,`channel_id`,`contact_id`,`kind`,`client_unresponsive`,`project_id`,`received_at`(gte,lte),`ref_no`(eq),`title`(like)；排序：`received_at`(默认 desc),`last_client_activity_at`,`ref_no`；offset 分页，limit ≤100 | `IntakeSummary[]`：id, ref_no, title, status, kind, channel_kind, contact{id,display_name}, received_at, latest_card{revision,size_estimate,suggested_project_id}, open_round{round_no,due_at,answered,total}, client_unresponsive, possible_duplicate_of | — | — |
| `POST /tenants/{tenant_id}/intakes` | `intake_submit` / R | `channel_id*`（API 开口，或团队代录时的任一开口）,`title*`,`description*`,`format`(PLAIN/MARKDOWN),`desired_by`,`external_ref`,`contact{display_name,email}`,`upload_ids[]`(≤10),`kind`(NEW 默认 / CHANGE),`change_of_intake_id`(CHANGE*；CHANGE 时忽略 `contact`，取原来件联系人) | `Intake` | 110400,110403,110404（原来件不可见，BR-01-77）,110413,110430,110433,110439,110449；`external_ref` 重复 → 200 返回已有来件 + `replayed:true` | Idempotency-Key；201 |
| `GET /intakes/{intake_id}` | `intake_read` / O | — | `Intake`：全部列 + attachments[{file_id,name,size,mime}] + latest_card + open_round + milestone | 110404 | ETag = version |
| `GET /intakes/{intake_id}/content` | `intake_read` / O | — | 原文 `text/plain; charset=utf-8`；overflow 时 302 到文件下载 | 110404 | — |
| `GET /intakes/{intake_id}/messages` | `intake_read` / O | 排序 `created_at` asc；limit ≤200 | `IntakeMessage[]` | 110404 | — |
| `POST /intakes/{intake_id}/triage-cards` | `intake_triage` / O | `based_on_revision`(人工必填),`summary*`,`draft_title*`,`draft_description*`,`draft_acceptance_criteria*`,`suggested_project_id` \| `suggest_new_project`+`new_project_name`,`risks`,`open_questions`,`size_estimate*`,`size_rationale`,`confidence`,`knowledge_refs`,`model_ref`,`change_impact`(kind=CHANGE；`tolerance_check`、`requires_client_approval` 由服务端计算，BR-01-78) | `TriageCard` + 来件新 status/version | 110400,110409,110421,110443,110451,110453,110503 | Idempotency-Key；201 |
| `GET /intakes/{intake_id}/triage-cards` | `intake_read` / O | — | `TriageCard[]`（全部 revision，倒序） | 110404 | — |
| `POST /intakes/{intake_id}/triage-requests` | `intake_triage` / O | `note`(≤2000) | `{requested_at}`；发布 `intake.triage_requested` | 110421；未配置协调员 → 110400 | Idempotency-Key；同来件 10 分钟内重复请求合并（202） |
| `PUT /intakes/{intake_id}/conversion` | `intake_convert` / O（处理器内再经 `AuthorityChecker`） | `mode*`,`triage_card_revision*`,`project_id`(NEW_REQUIREMENT/APPEND/REOPEN*),`requirement_id`(APPEND*),`base_version_no`(APPEND*),`criteria_retired[]`(APPEND，可空),`work_item_ids[]`(REOPEN*),`new_project{name*, code?}`(NEW_PROJECT*),`reauth_proof`(NEW_PROJECT/APPEND*),`title*`,`description*`(REOPEN 时可空),`acceptance_criteria*[]`(1..30；REOPEN 时可空),`priority`,`open_questions_ack`,`client_message` | `{intake, requirement{id,project_id,version}, project{id,created,channel_id?}, change_record_id?, reopened_work_item_ids?, authority_source}` | 110400,110403,110409,110421,110422,110435,110436,110443,110446,110447,110448,110450,110451,110452,110453,110502 | If-Match + Idempotency-Key；BR-01-26；kind=CHANGE 另见 BR-01-81（响应另含 `result_version_no`、`tolerance_adjusted`） |
| `PUT /intakes/{intake_id}/merger` | `intake_convert` / O | `target_intake_id*`,`reason*`,`client_message`,`notify_client`(默认 true) | `Intake` | 110404,110409,110421,110426,110427,110451 | If-Match |
| `PUT /intakes/{intake_id}/declination` | `intake_decline` / O（HUMAN 守卫） | `reason_code*`,`reason*`,`client_message`（kind=CHANGE 必填）,`notify_client`(默认 true；kind=CHANGE 强制 true，SPAM 除外) | `Intake` | 110403,110409,110421,110427,110446,110451 | If-Match |
| `PUT /intakes/{intake_id}/client-approval` | `intake_convert` / O（HUMAN；处理器内经 `AuthorityChecker` 校验目标项目 `scope_change`） | `triage_card_revision*`,`message`(≤2000，甲方可见) | `Intake` + `client_approval` | 110400,110403(NON_DELEGABLE / MISSING_PERMISSION),110409,110420,110421,110443,110451,110452,110453,110446 | If-Match + Idempotency-Key；T15 |
| `PUT /intakes/{intake_id}/client-approval/withdrawal` | `intake_triage` / O（HUMAN） | `reason*`(1..2000) | `Intake` + `client_approval` | 110403(HUMAN_ONLY),110409,110427,110446,110455 | If-Match；T18；已撤回的同 round 重放返回 200 |
| `PUT /intakes/{intake_id}/client-approval/recorded-decision` | `intake_convert` / O（HUMAN；`AuthorityChecker` 校验 `scope_change`） | `approval_round*`,`evidence_file_ids*[]`(1..5，调用者可读的 `files` 节点),`offline_note*`(1..2000),`decided_on*`(date，≤今天) | `Intake` + `client_approval` | 110400,110403(NON_DELEGABLE),110409,110446,110455 | If-Match + Idempotency-Key；T19 |
| `POST /intakes/{intake_id}/attention-heartbeats` | `intake_read` / O（仅 HUMAN） | `elapsed_ms*`(1..20000) | 204 | 110400,110404 | 无（按日累加；重复心跳以 `X-Heartbeat-Seq` 去重 60s） |

### 6.4 澄清

| 方法 路径 | 权限 / 守卫 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|
| `GET /intakes/{intake_id}/clarification-questions` | `intake_read` / O | query `state`,`round_id` | `Question[]` | 110404 | — |
| `POST /intakes/{intake_id}/clarification-questions` | `intake_triage` / O | `questions*[]`({text*})，1..10，DRAFT 总数 ≤10 | `Question[]` | 110400,110421,110433 | Idempotency-Key；201 |
| `PATCH /intake-clarification-questions/{question_id}` | `intake_triage` / O | `text` 或 `state:WITHDRAWN` | `Question` | 110409,110420 | If-Match |
| `POST /intakes/{intake_id}/clarification-rounds` | `intake_triage` / O | `question_ids*[]`（全部 DRAFT）,`intro`(≤2000) | `Round` + 出站消息 id + 来件 version | 110400,110409,110421,110423,110424,110433；Agent 且 `agent_may_send_clarifications=false` → 110403 | If-Match（来件）+ Idempotency-Key；201 |
| `PATCH /intake-clarification-rounds/{round_id}` | `intake_triage` / O | `state:CANCELLED`,`reason` | `Round` | 110409,110420 | If-Match |
| `PUT /intake-clarification-questions/{question_id}/recorded-answer` | `intake_triage` / O | `answer_text*`,`source_note`,`message_id`（对应 needs_mapping 消息时） | `Question` | 110409,110425 | If-Match |

### 6.5 邮件

| 方法 路径 | 权限 / 守卫 | 请求 | 响应 data | 错误 | 幂等 / 锁 |
|---|---|---|---|---|---|
| `POST /inbound-emails` | 无主体 / PI：`X-Inbound-Timestamp` + `X-Inbound-Signature` = hex HMAC-SHA256(部署密钥 `intake.email.inbound_secret`, `ts + "." + body`)，偏差 ≤300s | `Content-Type: message/rfc822`，原始 MIME ≤35MB | 202 `{accepted:true}`（不回报路由结果） | 401 110442,413 110413 | Message-ID 去重；先落 raw 文件与 PENDING 登记行再 202，解析异步（J1） |
| `GET /tenants/{tenant_id}/intake-email-messages` | `intake_triage` / R | 过滤 `disposition`,`disposition_reason`,`channel_id`,`from_address`(like),`created_at`；排序 `created_at` desc | `EmailMessage[]`（subject、from、原因，无正文） | — | — |
| `GET /intake-email-messages/{email_message_id}/content` | `intake_triage` / O | — | 去脚本纯文本预览 + 附件清单 | 110404 | — |
| `PUT /intake-email-messages/{email_message_id}/disposition` | `intake_triage` / O | `disposition*`(RELEASED/DISCARDED),`create_contact:bool`,`target_intake_id`（放行为追加时） | `EmailMessage` + 结果 intake_id | 110409,110420 | If-Match |

### 6.6 API 调用方视图

| 方法 路径 | 权限 / 守卫 | 请求 | 响应 data | 错误 |
|---|---|---|---|---|
| `GET /tenants/{tenant_id}/intake-submissions` | `intake_submit` / R（行过滤 submitted_by = 调用者） | 过滤 `status`,`external_ref`,`received_at` | `Submission[]`：id, ref_no, external_ref, status, milestone, open_questions_count, updated_at | — |
| `GET /intake-submissions/{intake_id}` | `intake_submit` / O（+ 行过滤） | — | `Submission` + `questions[]`（SENT/ANSWERED/EXPIRED） + `client_message` + `deliveries[]{id, state, manifest, download_url（72h 签名）}` + kind=CHANGE 时 `change_view`（同门户 `PortalChangeView`，BR-01-84；API 调用方不能作确认决定，确认经门户或团队登记） | 110404 |
| `PUT /intake-submissions/{intake_id}/answers/{question_id}` | `intake_submit` / O（+ 行过滤） | `answer_text*`,`upload_ids[]`(≤5) | `Question`（外部投影） | 110404,110425,110413 |

### 6.7 甲方门户（全部 **P**）

PRD §13.2 的 `/portal/{token}/*` 改为下列资源路径：token 不进 URL（BR-01-67），且 routing 规则 1–2 禁止非资源前缀。

| 方法 路径 | 请求 | 响应 data | 错误 | 幂等 |
|---|---|---|---|---|
| `POST /portal-sessions` | `access_token*` | `{session_state: PENDING_CODE\|ACTIVE, code_sent_to?: "l***@example.com", branding{display_name,logo_url,support_email,locale}}`；ACTIVE 时 Set-Cookie | 110401,110410,110429 | — |
| `PUT /portal-sessions/current/verification` | `code*`（6 位） | `{session_state: ACTIVE}` + Set-Cookie（轮换会话密钥） | 110410,110411,110412 | — |
| `POST /portal-sessions/current/code-deliveries` | — | `{resend_available_at}` | 110429 | 冷却 60s |
| `DELETE /portal-sessions/current` | — | 204，清 cookie | — | 幂等 |
| `GET /portal-profile` | — | `{contact{display_name,email_masked}, branding, channel_enabled}` | 110401 | — |
| `POST /portal-uploads` | multipart 单文件 | `{upload_id, name, size, expires_at}` | 110413,110415,110429 | Idempotency-Key |
| `GET /portal-intakes` | 过滤 `milestone`；排序 `received_at` desc；limit ≤50 | `PortalIntake[]`：id, ref_no, title, received_at, milestone, milestone_steps[5], open_questions_count, awaiting_acceptance, ai_label? | 110401 | — |
| `POST /portal-intakes` | `title*`,`description*`,`desired_by`,`upload_ids[]` | `PortalIntake` | 110400,110413,110430,110433,110439 | Idempotency-Key 必填（前端生成） |
| `GET /portal-intakes/{intake_id}` | — | `PortalIntake` + 原文 + 附件 + `questions[]` + `client_message` + `merged_into_ref?` + `deliveries[]{id,title,sent_at,state}` + `change_request_available`（CONVERTED 且有需求）+ `changes[]{id, ref_no, title, status_label, received_at, result_version_no?}` + `version_history[]{version_no, change_intake_ref?, title, accepted_at}`（BR-01-84）；kind=CHANGE 时另含 `change_of_ref` | 110404 | — |
| `POST /portal-intakes/{intake_id}/notes` | `body*`(≤10,000),`upload_ids[]`(≤5) | `{message_id}`（CLIENT_NOTE） | 110404；CONVERTED → 110454；CLIENT_APPROVAL → 110451；MERGED / DECLINED → 110421（BR-01-85）；每来件每日 >50 条 → 110433 | Idempotency-Key |
| `POST /portal-intakes/{intake_id}/change-requests` | `title*`(1..200),`description*`(1..20,000),`desired_by`,`upload_ids[]`(≤10) | `PortalIntake`（kind=CHANGE，`change_of_ref`） | 110400,110404,110413,110430,110433,110439,110449 | Idempotency-Key 必填（前端生成）；201 |
| `GET /portal-intakes/{intake_id}/change-impact` | — | `PortalChangeView`（BR-01-84 白名单）：`status_label`,`criteria_changes[]{op, text}`,`schedule_delta_days?`,`new_expected_date?`,`price_delta?{amount,currency,note}`,`team_message?`,`approval?{round, state, requested_at, decided_at?, decided_via?, comment?, recorded_by_team}`,`result_version_no?`,`client_message?`；评估未完成时只有 `status_label=ASSESSING` | 110404（非 CHANGE 或非本联系人） | — |
| `PUT /portal-intakes/{intake_id}/client-approval/decision` | `approval_round*`,`decision*`(APPROVED/REJECTED),`comment`(REJECTED 必填 10..2000) | `PortalChangeView` | 110400,110404,110427,110455 | Idempotency-Key；同 round 同决定重放 200；T16 / T17 |
| `PUT /portal-questions/{question_id}/answer` | `answer_text*`,`upload_ids[]`(≤5) | `PortalQuestion` | 110404,110425 | 同文重放 200 |
| `GET /portal-deliveries/{delivery_id}` | — | 07 定义的 `PortalDeliveryView`：说明、演示链接、交付物清单、证据摘要 | 110404 | — |
| `PUT /portal-deliveries/{delivery_id}/acceptance` | `manifest_sha256_ack*`（须等于门户展示的交付清单指纹）,`signer_name*`(1..100),`confirmed*`(必须 true),`items*[]`{`delivery_item_id*`,`result*`(ACCEPTED/REJECTED),`feedback_category`(REJECTED 必填，FeedbackCategory),`comment`(REJECTED 必填 10..2000),`comment_thread_ids[]`(仅 REJECTED，≤20，D120 BR-01-91)}（必须覆盖交付包全部条目）,`overall_comment`(≤2000) | 07 返回的回执投影（整体 `result` 由 07 按条目推导：全接受 ACCEPTED、全拒绝 REJECTED、否则 PARTIALLY_ACCEPTED） | 110400,110404,110427,110440,110459 | Idempotency-Key；已有回执 → 110440；指纹不符 → 110440（`data.reason=manifest_changed`） |
| `GET /portal-deliveries/{delivery_id}/files`（D120） | — | `PortalFile[]`（BR-01-88 白名单）+ `can_comment` | 110404 | — |
| `GET /portal-deliveries/{delivery_id}/comment-threads`（D120） | 过滤 `evidence_id`；`view_version`；排序 `created_at` asc；limit ≤100 | `PortalCommentThread[]`：id, evidence_id, delivery_item_id, anchor_kind, anchor, anchor_excerpt, version_ref, anchor_status, relocated_anchor?, state, author_display, converted_to_rejection, comments[]{id, seq, author_display, body, created_at}（BR-01-90） | 110404 | — |
| `POST /portal-deliveries/{delivery_id}/comment-threads`（D120） | `evidence_id*`,`version_ref*`,`anchor_kind*`(CODE_LINES/TABLE_ROW/HTML_REGION/TEXT_PARAGRAPH),`anchor*`(03 S9),`body*`(1..2000) | `PortalCommentThread`（201） | 110400,110404,110433,110440,110456,110457,110458,110460 | Idempotency-Key 必填（前端生成） |
| `POST /portal-comment-threads/{thread_id}/comments`（D120） | `body*`(1..2000) | `{id, seq, author_display, body, created_at}`（201） | 110400,110404,110433,110440,110460 | Idempotency-Key 必填（前端生成） |

门户 → 07：列表用 `AcceptanceRecorder.ListForIntakes(ctx, tenantID, intakeIDs[])`；详情用 `AcceptanceRecorder.PortalView(ctx, tenantID, deliveryID)`（按 07 portal_settings 裁剪，含 `manifest_sha256`、条目与 `source_intake_ids`）；门户先校验 `source_intake_ids ∩ 本联系人来件 ≠ ∅`（否则 110404），再调用 `AcceptanceRecorder.Record(ctx, {DeliveryID, IntakeContactID: contact_id, Confirmed: true（门户请求 `confirmed` 必须为 true，否则 422 110400）, ManifestSHA256Ack, SignerName, Items[], OverallComment, Source: PORTAL, Actor{kind:CLIENT, id:contact_id, authority_source:"CLIENT:<contact_id>"}})`（幂等由平台 Idempotency-Key 保证）。验收数据、状态与事件全部归 07；07 的「状态不可验收 / 指纹不符」错误映射为 110440，条目校验错误映射为 110400 / 110427。

门户批注（D120）→ 07 / 03 的错误映射（`data.cause_code` 保留原码）：07 `128030` → 110440；07 `127056` → 110456；07 `127057` → 110459（`data.reason` 原样）；03 `130476` / `130469` / `130480` → 110457（`data.reason` 分别为 `ANCHOR_INVALID`（附 `data.field`）/ `MASKED_FILE` / `ANCHOR_KIND_UNAVAILABLE`）；03 `130477` → 110458；03 `130481` → 110433；联系人非 `can_accept` → 110460。批注读写的数据、版本绑定、审计（`delivery.client_comment_created` 等）与事件全部归 07 / 03，批注本身不落本模块表（返工来件的 `rejected_items[].anchors[]` 只是快照副本，BR-01-91）。

### 6.8 跨模块接口

方法签名以**提供者规格**为准（00 §8）；下表「本模块提供」部分为权威定义。

**本模块提供**

| 接口 | 方法（签名 · 事务语义） | 使用者 |
|---|---|---|
| `IntakeReader` | `Get(ctx, tenantID, intakeID) (IntakeRef{ID, RefNo, Title, Kind, ContactID, ChannelID, ChannelKind, Status, ProjectID, RequirementID, OriginAcceptanceID}, error)`；`Contacts(ctx, tenantID, contactIDs[]) ([]ContactRef{ID, DisplayName, EmailMasked, Kind, State, HasEmail}, error)`；`ChannelAvailable(ctx, tenantID, channelID) (bool, reason, error)`（ENABLED 且可发送）；`OpenClarifications(ctx, tenantID, projectID) (count int, error)`（项目关联来件中 OPEN 澄清轮数，供 07 阶段守卫）；`AttentionByDay(ctx, tenantID, projectIDs[], from, to date) ([]AttentionDay{ProjectID, Day, PrincipalID, AttentionMs}, error)`（§3.13b）；`RawText(ctx, tenantID, intakeID) (text string, overflowFileID *id, error)`（06 EXTERNAL_TEXT 来源；只读） | 02、06、07 |
| `IntakeReplySender` | `SendDelivery(ctx, SendDeliveryInput{TenantID, DeliveryID, ContactID, ChannelID, IntakeIDs[], AttemptNo, ManifestSHA256, BundleFileID, ManifestFileID, Title, Summary, DemoURL, PortalView}) (SendResult{Status: SENT\|QUEUED\|FAILED, MessageRef, Reason}, error)` — 同步、按 `(delivery_id, contact_id, attempt_no)` 幂等（`AttemptNo` 由 07 发送任务给出，同一次发送的重试保持不变；`IntakeIDs[]` 由 07 传入，用于逐来件写 DELIVERY_NOTICE）；PORTAL → 门户可见 + 联系人有邮箱时邮件通知；EMAIL → 线程内回复（说明 + 清单 + 门户入口；manifest ≤10 MiB 才作附件，zip 不作附件）；API → webhook 元数据回调，调用方经 `GET /intake-submissions/{id}` 取 `deliveries[]{id, manifest, download_url(72h 签名)}`；对每个 `intake_id` 写 DELIVERY_NOTICE 线程消息。`SendAcceptanceReminder(ctx, {TenantID, DeliveryID, ContactID, ReminderNo}) (SendResult, error)` — 按 `(delivery_id, contact_id, reminder_no)` 幂等。两者均**不**在调用方事务内（外部 I/O），由 07 任务 `delivery.send` / 提醒任务调用并重试 | 07 |
| `ClarificationRequester` | `RequestFromWorkItem(ctx, tx, {TenantID, IntakeID, WorkItemID, Questions[]{text}, Actor}) (RoundID *id, QuestionIDs[], error)` — 在调用方事务内执行 T14：无 OPEN 轮则建轮并发送（发送为事务提交后的 outbox 动作），有 OPEN 轮则追加为 DRAFT（RoundID=nil）；来件不存在 / 非本项目 → 110404；轮数上限 → 110433 | 02 |

**本模块使用**

| 接口 | 提供者 | 调用（签名以提供者为准） | intake 用途 |
|---|---|---|---|
| `RequirementCreator` | 02 | `CreateFromIntake(ctx, tx, {ProjectID, IntakeID, TriageCardID, Title, Description, Criteria[], Priority, Actor}) → {RequirementID, Version}`；`AppendFromIntake(ctx, tx, {TenantID, RequirementID, IntakeID, BaseVersionNo, Summary, Diff{criteria_added[], criteria_retired[], impacts[]}, Change?{CriteriaDiff[]{op, criterion_id?, text}, Impacts[]{work_item_id, action STALE\|CANCEL}, SourceIntakeID, ApprovedAdjustment?{ScheduleDeltaDays, CostDeltaMicros, ScopeDeltaCriteria}}, Actor}) → ChangeRecordID`（`Diff` 与 `Change` 互斥；kind=CHANGE 传 `Change`，BR-01-81；**不**传 ReauthProof：`intake_convert_privileged` step-up 由本模块在 BR-01-21 校验，02 信任调用方已校验，见 08 §6.9 登记） | NEW_REQUIREMENT / NEW_PROJECT / APPEND |
| `ChangeImpactPreviewer` | 02 | `Preview(ctx, tenantID, requirementID, baseVersionNo, criteriaDiff[]{op, criterion_id?, text}) → {AffectedWorkItems[]{work_item_id?, effect STALE\|NEW}, CurrentVersionNo}`（只读，不写库） | CHANGE 影响评估（BR-01-78） |
| `ToleranceReader` | 07 | `WouldBreach(ctx, tenantID, requirementID, {ScheduleDeltaDays, CostDeltaMicros, ScopeDeltaCriteria}) → (breached[]{dimension TIME\|COST\|SCOPE, subject_kind}, snapshot, error)`（只读） | CHANGE 容差检查（BR-01-78、BR-01-81 复核）；`ToleranceAdjuster.ApplyApprovedChange` 由 02 在 `AppendFromIntake` 内调用，本模块不直接调用 |
| `WorkItemReopener` | 02 | `ReopenFromAcceptance(ctx, tx, {tenant_id, work_item_ids[], source_acceptance_id, feedback[]{work_item_id, feedback_category, comment}, reason, actor}) → []decisionID`（签名以 02 §6.9 为准；本模块另记 `intake_id` 于审计 detail） | REOPEN_WORK_ITEMS（BR-01-28） |
| `ProjectCreator` / `ProjectReader` | 07 | `CreateForIntakeOn(ctx, tx, {TenantID, Name, Code?, OriginIntakeID, PrimaryContactID, Actor}) → {ID, ChannelID}`（TeamMode=HYBRID）；`ProjectReader.Get → {Stage, TeamMode, Name}` | NEW_PROJECT；BR-01-23/24/28 守卫 |
| `IntakeProgressReader` | 07 | `Progress(ctx, tenantID, intakeIDs[]) → map[id]{RequirementID, ProjectStage, TeamMode, DeliveryStates[], LatestAcceptance}` | 里程碑、AI 标签 |
| `AcceptanceRecorder` | 07 | `ListForIntakes`、`PortalView`、`Record`；D120：`CreateClientComment`、`ReplyClientComment`（CR-13）、`Record` 的 `items[].comment_thread_ids[]`、`PortalView` 的 CLIENT 线程 | 门户验收（§6.7）；甲方批注（BR-01-89…91） |
| `DeliveryEvidenceReader`（D120） | 07 | `ListClientFacing(ctx, tenantID, deliveryID) → []ClientFacingFile`（CR-13；只读） | 门户文件列表与查看器数据（BR-01-88） |
| `AcceptanceReader` | 07 | `Get(ctx, tenantID, acceptanceID) → {DeliveryID, ProjectID, Result, Items[]{work_item_id, delivery_item_id, result, feedback_category, comment, anchors[]（D120）}, ContactID}` | 构造返工来件原文与 `rejected_items`（含锚点，BR-01-91） |
| `AuthorityChecker` | 08 | `Check(ctx, tx, AuthorityRequest{Principal, Action, ProjectID, Facts{IntakeConvertMode, TriageCardAuthor}}) → AuthorityDecision{Allowed, AuthoritySource, ReasonCode}` | 转化、拒绝、重开；请甲方确认与登记线下确认（`scope_change`，T15 / T19） |
| `AgentDirectory` | 05 | `Get(agentID)` 校验协调员角色 | 开口配置 |

里程碑推导（`PortalMilestone`，CR-03）：RECEIVED/TRIAGED → `RECEIVED`；CLARIFYING → `CLARIFYING`；CONVERTED 且无 SENT 交付 → `IN_PROGRESS`；存在 SENT 未回执交付 → `AWAITING_ACCEPTANCE`；最近交付 ACCEPTED → `DELIVERED`；MERGED / DECLINED 为旁支终态，进度条显示灰色终止节点。kind=CHANGE 的来件不使用里程碑，门户改用 BR-01-84 的变更状态标签（CLIENT_APPROVAL 只出现在变更申请上）。

### 6.9 错误码（i18n namespace `intake`，三语齐全）

| 码 | HTTP | key | 含义 |
|---|---|---|---|
| 110400 | 400 | `intake.invalid_request` | 字段校验失败（含试图修改不可变列） |
| 110401 | 401 | `intake.portal_unauthenticated` | 门户未登录 / token 未命中 |
| 110403 | 403 | `intake.forbidden` | 权限拒绝；`data.reason_code`（BR-01-75），缺权限码时附 `data.permission` |
| 110404 | 404 | `intake.not_found` | 不存在、跨租户或跨联系人 |
| 110409 | 409 | `intake.version_conflict` | 乐观锁冲突（data.current_version） |
| 110410 | 410 | `intake.portal_link_unusable` | 链接过期 / 吊销（data.reason） |
| 110411 | 401 | `intake.portal_code_invalid` | 访问码错误或过期（data.attempts_left） |
| 110412 | 429 | `intake.portal_code_locked` | 访问码尝试超限 |
| 110413 | 413 | `intake.payload_too_large` | 正文 / 文件 / 邮件超限 |
| 110415 | 415 | `intake.attachment_type_rejected` | 禁止的文件类型 |
| 110420 | 422 | `intake.illegal_transition` | 非法迁移（data.from, data.command） |
| 110421 | 422 | `intake.terminal` | 来件已终态 |
| 110422 | 422 | `intake.triage_required` | 无分诊卡不可转化 |
| 110423 | 422 | `intake.open_round_exists` | 已有进行中的澄清轮 |
| 110424 | 422 | `intake.no_draft_questions` | 无可发送问题 |
| 110425 | 422 | `intake.question_not_open` | 问题不可回答 |
| 110426 | 422 | `intake.merge_target_invalid` | 合并目标非法 |
| 110427 | 422 | `intake.reason_required` | 缺理由 |
| ~~110428~~ | — | — | 已废弃：并入 110403（`reason_code=NON_DELEGABLE`） |
| 110429 | 429 | `intake.rate_limited` | 限流（Retry-After） |
| 110430 | 422 | `intake.channel_disabled` | 开口已停用 |
| 110431 | 422 | `intake.channel_kind_unavailable` | 该开口类型本版本不可用 |
| 110432 | 403 | `intake.contact_blocked` | 联系人已屏蔽 |
| 110433 | 429 | `intake.quota_exceeded` | 配额 / 数量上限用尽 |
| ~~110434~~ | — | — | 已废弃：改用平台码 176425（00 §9） |
| 110435 | 422 | `intake.conversion_target_invalid` | 目标项目 / 需求不可写 |
| 110436 | 422 | `intake.open_questions_unacknowledged` | 有未答问题未确认 |
| ~~110437~~ | — | — | 已废弃：并入 110403（`reason_code=CHANNEL_PRINCIPAL_NOT_ALLOWED`） |
| 110438 | 409 | `intake.email_address_taken` | 收件地址已被占用 |
| 110439 | 422 | `intake.attachment_limit` | 附件数量 / 总量超限 |
| 110440 | 422 | `intake.delivery_not_acceptable` | 交付不可验收或已回执（07 映射） |
| 110441 | 422 | `intake.link_limit` | 有效链接数已达上限 |
| 110442 | 401 | `intake.inbound_signature_invalid` | 入站签名错误或过期 |
| 110443 | 409 | `intake.card_stale` | 分诊卡 revision 已过期 |
| 110444 | 403 | `intake.portal_csrf` | 缺少门户请求头 |
| 110445 | 409 | `intake.portal_channel_exists` | 每租户仅一个门户开口 |
| 110446 | 428 | `intake.precondition_required` | 缺 `If-Match` |
| 110447 | 401 | `intake.reauthentication_required` | NEW_PROJECT / APPEND 缺少或过期的 step-up 证明（purpose `intake_convert_privileged`） |
| 110448 | 422 | `intake.reopen_requires_rework_intake` | `REOPEN_WORK_ITEMS` 仅适用于返工来件，且工作项须在 `rejected_items` 中 |
| 110449 | 422 | `intake.change_original_not_converted` | 只能在已转化（有需求）的来件上申请变更（D117） |
| 110450 | 422 | `intake.client_approval_required` | 超出容差的变更须先获甲方对同一分诊卡 revision 的确认 |
| 110451 | 409 | `intake.awaiting_client_approval` | 待甲方确认期间不可改卡、澄清、转化、合并、拒绝或留言（先撤回） |
| 110452 | 422 | `intake.change_impact_required` | 变更申请缺少影响评估 |
| 110453 | 409 | `intake.change_impact_stale` | 影响评估所依据的需求版本或容差结果已过期（data.current_version_no、data.breached） |
| 110454 | 422 | `intake.change_request_required` | 已立项的需求请用〔申请变更〕，不能再补充说明 |
| 110455 | 409 | `intake.client_approval_not_pending` | 没有待确认的请求，或请求已被撤回 / 更新（data.reason） |
| 110456 | 422 | `intake.comment_target_not_client_facing` | 该文件未作为文件交付给你，或不属于此交付（07 `127056`，D120） |
| 110457 | 422 | `intake.comment_anchor_invalid` | 批注位置无效、文件不可批注或该类批注暂不可用（data.reason、data.field，D120） |
| 110458 | 409 | `intake.comment_content_changed` | 文件内容已变化，请刷新后再批注（03 `130477`，D120） |
| 110459 | 422 | `intake.rejection_comment_invalid` | 拒收项引用的批注无效（data.reason ∈ NOT_REJECTED / NOT_THIS_ITEM / NOT_CLIENT_THREAD / ALREADY_CONVERTED；07 `127057`，D120） |
| 110460 | 403 | `intake.portal_not_signatory` | 你不是本项目的签收人，不能批注（07 BR-07-31，D120） |
| 110502 | 502 | `intake.downstream_failed` | 下游（02/07）失败，已回滚 |
| 110503 | 503 | `intake.unavailable` | 数据源不可用 |

---

## 7. 后台任务与事件

### 7.1 任务（经 09 `JobQueue`；互斥用 `internal/infra/dlock`）

| 任务 | 触发 | 频率 | 互斥 | 批量 / 重试 |
|---|---|---|---|---|
| J1 `intake.email.ingest` | `/inbound-emails` 或 J2 落库后入队（每封一个任务） | 即时 | 行级条件更新：`attempts=attempts+1 WHERE id=? AND disposition='PENDING' AND version=?` | 退避 30s×2^n，5 次后 FAILED 并通知 N7 |
| J2 `intake.email.poll`（仅 IMAP_POLL 开口） | 定时 | 60s | dlock `intake:email-poll:<channel_id>`，TTL 120s，auto-refresh | 每次 ≤50 封；事务提交后才在 IMAP 上移到 processed_folder；连续 5 次连接失败通知 N7 |
| J3 `intake.clarification.reminder` | 定时 | 5 分钟 | dlock `intake:clarification-reminder`，TTL 4 分钟 | 取 `next_reminder_at ≤ now` 的 OPEN 轮 ≤100 条；先条件更新 `reminders_sent`/`next_reminder_at` 再入队通知；到 due_at 执行 T8 |
| J4 `intake.access.expiry` | 定时 | 15 分钟 | dlock `intake:access-expiry` | 吊销过期链接下的会话；删除过期超 12h 的会话行；链接 72h 内到期且联系人有非终态来件 → N9（每链接一次） |
| J5 `intake.triage.watchdog` | 定时 | 5 分钟 | dlock `intake:triage-watchdog` | RECEIVED 且 received_at ≤ now−30min 且未通知 → N3 |
| J6 `intake.housekeeping` | 定时 | 1 小时 | dlock `intake:housekeeping` | `intake.upload` 到期绑定由 files `Expire` 回收（幂等键由 09 平台清理） |
| J7 `intake.client_approval.reminder`（D117） | 定时 | 1 小时 | dlock `intake:client-approval-reminder`，TTL 50 分钟 | 取 status=CLIENT_APPROVAL 且 `next_client_reminder_at` 或 `next_team_reminder_at` ≤ now 的来件 ≤100 条；先以 `version` 条件更新 `client_reminders_sent` / `next_*_reminder_at` 再入队 N15 / N17（BR-01-86）；**不改变来件状态** |

重启语义：任务只依赖表状态，重启后从表继续；条件更新保证已完成的通知不重放。

### 7.2 发布的领域事件（经 09 outbox，与业务同事务）

公共载荷见 00 §7.1，`subject{kind:"intake", id}`；`data` 不含原文与回答正文。

| 事件 | data |
|---|---|
| `intake.received` | `{ref_no, channel_kind, channel_id, contact_id, kind（NEW / REWORK / CHANGE）, triage_agent_id?, origin_delivery_id?, origin_acceptance_id?, possible_duplicate_of?, change_of_intake_id?, target_requirement_id?}`（kind=CHANGE 时后两项必有） |
| `intake.triaged` | `{revision, author_kind, size_estimate, suggested_project_id?, suggest_new_project, risk_max, open_questions_count}` |
| `intake.triage_requested` | `{triage_agent_id, reason: ROUND_ANSWERED \| MANUAL}` |
| `intake.clarification_requested` | `{round_id, round_no, question_count, sent_via, due_at, work_item_id?}` |
| `intake.clarification_answered` | `{question_id, round_id, answered_via, round_closed, work_item_id?}` |
| `intake.clarification_expired` | `{round_id, unanswered_count, work_item_id?}` |
| `intake.converted` | `{mode, project_id, requirement_id, project_created, triage_card_revision, change_record_id?, reopened_work_item_ids?, authority_source, result_version_no?, tolerance_adjusted?}`（后两项仅 kind=CHANGE） |
| `intake.merged` | `{target_intake_id}` |
| `intake.declined` | `{reason_code, client_notified}` |
| `intake.client_approval_requested`（D117） | `{round, card_revision, target_requirement_id, breached[]（TIME / COST / SCOPE）}`；不含金额与工作项 |
| `intake.client_approval_decided`（D117） | `{round, card_revision, target_requirement_id, decision: APPROVED \| REJECTED \| WITHDRAWN, decided_via: PORTAL \| RECORDED_BY_TEAM \| TEAM_WITHDRAWN}`；不含甲方意见正文 |

### 7.3 审计（`audit.Appender` 同事务）与 webhook 回执

审计事件：`intake.intake_received`、`intake.clarification_requested`、`intake.clarification_answered`、`intake.intake_converted`、`intake.conversion_denied`（outcome=denied）、`intake.intake_merged`、`intake.intake_declined`、`intake.decline_denied`、`intake.access_link_issued`、`intake.access_link_revoked`、`intake.contact_blocked`、`intake.channel_changed`、`intake.client_settings_changed`、`intake.email_released`、`intake.email_discarded`、`intake.portal_acceptance_submitted`、`intake.client_approval_requested`、`intake.client_approval_decided`（门户决定，actor=CLIENT）、`intake.client_approval_withdrawn`、`intake.client_approval_recorded_offline`（detail 含 `evidence_refs` = 证据文件）、`intake.client_approval_denied`（outcome=denied）。`target_type="intake"`（链接、开口类为各自资源）；`detail` 含 `reason`、`authority_source`、`evidence_refs:[]`（00 §7.2）。API 开口回执即这些审计事件经 `callback_subscription_id` 订阅投递（BR-01-52）。

### 7.4 消费的事件

| 事件 | 来源 | 处理 |
|---|---|---|
| `delivery.sent` | 07 | 仅刷新门户里程碑投影；**不发通知、不写线程消息**——两者已由 07 同步调用的 `IntakeReplySender.SendDelivery` 完成（避免甲方重复收到） |
| `acceptance.received` | 07 | 按 `data.intake_ids[]` 写 ACCEPTANCE_RECEIPT 并回执（N11）；`result ∈ {REJECTED, PARTIALLY_ACCEPTED}` 时**由 intake 创建** kind=REWORK 来件：联系人 = 回执人，原文与分诊卡 `rejected_items` 由 07 `AcceptanceReader.Get` 返回的 `AcceptanceView{ProjectID, ContactID, Items[]{delivery_item_id, work_item_id, result, feedback_category, comment}}` 构造（目标项目 = `ProjectID`，联系人 = `ContactID`），`origin_acceptance_id` / `origin_delivery_id` 填入；`origin_acceptance_id` 业务唯一约束保证重复投递只建一条，消费去重用 09 `domain_event_receipts`；走 T1 |
| `notification.delivery_state_changed` | 09 | 按 `source_ref`（= `intake_messages.id`）更新该出站消息的 `delivery_state`（SENT / FAILED / SUPPRESSED）；FAILED 且为澄清或验收提醒时在来件详情显示「投递失败 · 重试」；未知 source_ref 忽略；消费去重用 09 `domain_event_receipts` |
| `agent.retired` / `agent.state_changed` | 05 | 开口 `triage_agent_id` 指向的 Agent 退役 → 置 NULL 并通知 N7 |

---

## 8. 页面规格

团队页面位于 `apps/delivery`，外壳复用 IAM 应用（侧栏「项目 › 来件」（项目外，团队级）；「治理 › 连接 › 需求开口」，08 §8.6）。组件：`@workspace/ui` 的 `crud/common-table`、`auto-form`、`tabs`、`sheet`、`dialog`、`badge`、`skeleton`、`pickers/file-input`、`pickers/date-picker`，以及 00 §10 共享组件 `StateBadge`、`ActorAvatar`、`CountdownRing`、`AuthorityNote`。**通用状态**：加载 = 骨架屏；空 = 说明 + 下一步按钮；错误 = 文案 + 错误码 + 〔重试〕；无权限 = 「需要权限 `<code>`，请联系团队所有者」；离线 = 顶部琥珀条「连接已断开，正在重连…」，命令按钮全部禁用，已加载数据保留。

### 8.1 来件列表 `/t/:tenantID/intakes`

- **目的**：一眼看到要接手的来件。**权限**：`intake_read`。
- **布局**：筛选条（状态分段：全部 / 待分诊 / 已分诊 / 澄清中 / 待甲方确认 / 已结束；变更申请显示蓝色「变更申请 · IN-x」标签；开口类型多选；联系人；时间范围；「甲方无响应」开关）+ 搜索（标题、IN 号）。表格列：开口图标、IN 号、标题（「可能重复」琥珀标签、REWORK 紫色「返工来件」标签）、联系人（`ActorAvatar` 甲方样式）、状态 `StateBadge`、分诊（规模 + 建议项目，或「待分诊 12 分钟」）、澄清进度（`2/3 · 剩 3 天` + `CountdownRing`）、收到时间。右上〔代录来件〕（需 `intake_submit`）、〔隔离邮件 n〕（需 `intake_triage`，n>0 才显示）。
- **空态**：无开口 →「还没有开口」〔去设置开口〕；有开口无来件 → 显示收件地址与〔签发门户链接〕。
- **实时**：订阅 SSE 集合主题 `tenant/{t}/intakes`（00 §9 / 09 C3；本模块注册 `TopicAuthorizer`：集合需 `intake_read`，单项需 `intake_read` 且同租户），新来件插入顶部高亮 3s，≤1s 呈现；不匹配当前筛选的更新只更新分段计数。
- **移动端**：表格变两行卡片（标题 + 状态 / 联系人 + 时间），筛选收进底部 Sheet。

### 8.2 来件详情 `/t/:tenantID/intakes/:intakeID`

- **布局（≥1024px 三栏）**：左「原文」：来源卡（开口、联系人、收到时间、Message-ID / external_ref、`raw_content_hash` 前 8 位可复制全值）、只读原文（Markdown 安全渲染；HTML_TEXT 等宽）、附件（超限未导入的灰显并说明）。中「分诊卡」：revision 切换（作者 `ActorAvatar` 区分人 / Agent、confidence、时间、与上一版 diff 开关）；摘要、需求草案（标题、描述、验收标准）、建议项目 / 新建项目名、风险（等级图标 + 文本）、待澄清问题（每条〔加入澄清草稿〕）、规模与理由；无卡片时显示「协调员分诊中」或〔手写分诊卡〕。右「澄清线程」：时间线（入站 / 出站消息、问题卡片含状态与回答、提醒记录、needs_mapping 消息带〔对应到问题〕）+ 底部问题草稿区（1..2000 字，最多 10 条）。
- **头部**：IN 号、标题、`StateBadge`、kind 标签、`AuthorityNote`（「你以 交付负责人 身份 · 来源 ROLE:delivery_lead」）、操作按钮：

| 按钮 | 可用条件 | 交互 |
|---|---|---|
| 〔转为需求〕 | `intake_convert`；状态 TRIAGED，或 CLARIFYING（对话框内须勾选「放弃未答问题」）；latest_card_revision ≥1 | 对话框：模式（已有项目 / 新建项目 / 追加到已有需求；返工来件另有「重开被拒工作项」并按 BR-01-27 预选默认）；项目选择器只列可写且阶段允许的项目；字段预填自最新卡片，可编辑（编辑即提示「将生成人工修订版」）；APPEND 显示需求当前版本与将新增 / 退役的验收标准 diff；REOPEN 显示拒收条目表（工作项、类别、甲方原话）可勾选；给甲方的话（可空）；NEW_PROJECT 与 APPEND 显示红色语义「不可委托」说明，提交前弹出底座 `ReauthenticationDialog`（purpose `intake_convert_privileged`）；110447 时重新弹出 |
| 〔请求澄清〕 | `intake_triage`；状态 RECEIVED/TRIAGED；DRAFT 问题 1..10；无 OPEN 轮 | 预览经「邮件 / 门户 / API」发出的内容，显示提醒与超时时间点（默认 48h、96h、168h） |
| 〔取消本轮〕 | 存在 OPEN 轮 | 确认对话框 |
| 〔重新分诊〕 | `intake_triage`；非终态；开口已配协调员 | 点击后 10 分钟内禁用 |
| 〔编辑分诊卡〕 | `intake_triage`；非终态 | 以当前 revision 为底稿的 `auto-form`，提交带 `based_on_revision`；110443 时提示「协调员刚提交了新版本」并显示对比 |
| 〔合并〕 | `intake_convert`；非终态 | 目标搜索（排除自身、MERGED、DECLINED），理由必填 |
| 〔拒绝〕 | `intake_decline`；非终态 | 原因码 + 内部理由（1..2000）+ 给甲方的话 + 「通知甲方」开关（SPAM 时强制关闭） |
| 〔请甲方确认〕 | kind=CHANGE；状态 TRIAGED；最新卡片 `requires_client_approval=true`；HUMAN 且对目标项目有 `scope_change` | 对话框：甲方将看到的内容预览（标准差异通俗文字、进度变化、新日期、价格变化；**不含**内部成本与工作项）+ 给甲方的说明（≤2000）；显示「超出容差：时间 / 成本 / 范围」；110453 时提示「需求已更新到 vN，请重新评估」 |
| 〔撤回确认请求〕 | 状态 CLIENT_APPROVAL；HUMAN | 理由必填（1..2000）；提示「甲方将收到撤回通知，之前的确认链接失效」 |
| 〔登记线下确认〕 | 状态 CLIENT_APPROVAL；HUMAN 且有 `scope_change` | 确认日期、说明（1..2000）、证据文件 1..5（邮件原件 / 会议纪要）；红色语义说明「将以团队名义记录甲方已同意，门户显示『由团队记录』」 |

- **变更申请（kind=CHANGE）**：左栏来源卡增加「变更自 IN-x」链接、目标需求（REQ-n · 当前 vN）；中栏分诊卡增加「变更影响」区块：标准差异表（新增 / 移除 / 改为）、受影响工作项（STALE / NEW / CANCELLED，STALE 可改为 CANCELLED）、进度变化天数与新预计日期、内部成本变化（仅团队）、给甲方的价格变化（仅人可填，Agent 草稿显示「待人填写」）、容差检查徽章（OK / 越界维度）与「需要甲方确认」标记；头部在 CLIENT_APPROVAL 时显示琥珀横幅「等待甲方确认（第 n 轮，已 d 天）」，`client_approval` 结果（接受 / 不接受 + 甲方理由 / 撤回 / 由团队记录 + 证据）以时间线卡片显示在右栏。〔转为需求〕对 CHANGE 默认「追加到已有需求」并锁定目标需求，可切换为「作为新需求」；需要确认而未获确认时按钮禁用并说明原因（110450）。

- **校验**：标题 1..200；验收标准 1..30 条、每条 1..1000；问题 1..2000；理由 1..2000；超长实时计数并禁用提交。
- **终态**：按钮区替换为结果条（CONVERTED → 需求与项目链接；MERGED → 目标来件链接；DECLINED → 原因码、内部理由、给甲方的话）。
- **实时**：SSE 单项主题 `tenant/{t}/intake/{id}`（主题种类 = `subject.kind` 单数）；version 变化时打开中的对话框顶部提示「此来件已更新」〔刷新〕；提交遇 110409 保留表单内容。
- **移动端**：三栏改为 Tabs（原文 / 分诊 / 澄清），操作固定在底部栏（2 个主按钮 + 「更多」）。

### 8.3 开口设置 `/t/:tenantID/settings/intake-channels`

- **权限**：查看 `intake_read`；编辑 `intake_channel_manage`（无编辑权限时表单只读并显示缺失权限码）。
- **区块**：① 开口卡片（类型图标、名称、启停开关、健康：最近收件时间 / IMAP 错误）；〔新建开口〕菜单：PORTAL（已存在时禁用并说明）/ EMAIL / API，WEBHOOK 与 IM 显示「即将推出」不可点。② 编辑 Sheet（`auto-form`）：公共项（名称、协调员 Agent、默认项目、通知对象、回执开关、提醒间隔 24–168h、提醒次数 0–5、超时 48–720h、协调员可发送澄清）；EMAIL（local-part 实时校验与占用检查、完整地址复制、接收方式、IMAP 参数与〔测试连接〕、发件人策略、白名单、垃圾阈值）；API（允许的服务账号、回调 webhook 订阅选择或跳转底座新建、每日上限）；PORTAL（需访问码、默认链接有效期、每联系人每日上限）。③ 甲方可见设置：显示名（占位符显示回退结果）、Logo（≤512KB）、支持邮箱、AI 参与展示三选一（带甲方视角预览）、平台署名开关、默认语言。④ 联系人表：名称、邮箱、类型、状态、未结来件数、有效链接数；行操作〔签发链接〕〔吊销〕〔屏蔽〕。签发成功对话框只显示一次完整链接，提供〔复制〕〔通过邮件发送〕，并明确提示「关闭后无法再次查看」。
- **危险操作**：停用开口、屏蔽联系人、吊销链接 → 红色确认 + 后果说明（如「Lee 的 2 个会话将立即失效」）。
- **移动端**：卡片单列，编辑 Sheet 全屏。

### 8.4 甲方门户（独立布局，`/portal`、`/portal/:accessToken`，见 CR-07）

通用：无控制台外壳；页眉团队 Logo + 显示名（BR-01-40），页脚支持邮箱与可选署名；语言跟随联系人 locale，可切换；移动优先（375px 单列，触控目标 ≥44px）；不出现 Agent、工作项、Runner 等词。错误：110401 →「链接无效，请向团队索取新链接」；110410 →「链接已过期 / 已停用」+ 支持邮箱；加载用骨架；离线 → 顶部提示，表单草稿保存在 sessionStorage，恢复后由用户手动重试。门户没有「无权限」态（只有已认证 / 未认证），未认证统一引导到「请使用团队发给你的链接」。

| 页面 | 内容与交互 |
|---|---|
| **验证** `/portal/:accessToken` | 自动 `POST /portal-sessions` 后 replaceState 到 `/portal`；需访问码时显示 6 格输入（支持粘贴、填满自动提交）、「已发送到 l***@example.com」、60s 倒计时后〔重新发送〕、剩余尝试次数；锁定后提示联系团队 |
| **我的需求** `/portal` | 卡片：标题、IN 号、提交时间、5 节里程碑进度条（当前节点高亮；MERGED/DECLINED 显示灰色终止节点及团队的话）、「有 n 个问题待你回答」琥珀徽标、「待你验收」徽标、「变更待你确认 n」琥珀徽标（变更申请不单独成卡，BR-01-77）、AI 标签（BR-01-68）；顶部〔提交新需求〕；空态「还没有需求」〔提交第一条需求〕；页面可见时 30s 轮询（「本规格提议」：门户主体非 Principal，不接入 SSE） |
| **提交需求** `/portal/new` | 标题（1..200）、描述（1..20,000，简易 Markdown 工具栏）、期望日期（≥今天）、附件（≤10 个、单个 ≤25MB、合计 ≤50MB，进度条与禁止类型即时提示）；〔提交〕在必填齐全且无上传中文件时可用；Idempotency-Key 由前端生成并在重试时复用；成功页显示 IN 号；开口停用时显示「暂不接收新需求」并隐藏表单 |
| **需求详情** `/portal/intakes/:intakeID` | 里程碑、原文与附件（只读）、澄清问题（问题文本、状态、回答框 1..10,000 字 + 附件 ≤5、〔提交回答〕；已答显示回答与时间及「由团队记录」标识）、〔补充说明〕（仅 RECEIVED / TRIAGED / CLARIFYING，BR-01-85）、团队给你的话、交付列表（待验收项带〔去验收〕）、DECLINED 时〔重新提交〕（预填原文）。**已立项（CONVERTED）时**：〔补充说明〕替换为〔申请变更〕；新增「版本历史」（v1 原始需求，其后每条已接受变更：vN、标题、日期）与「变更申请」列表（标题、IN 号、提交日期、状态：评估中 / 待你确认（琥珀，带〔去确认〕）/ 已接受（需求 vN）/ 未接受（理由））；文案不出现「容差」「工作项」「成本」等内部词 |
| **申请变更** `/portal/intakes/:intakeID/changes/new` | 顶部只读摘要「变更：《{原标题}》（当前 v{n}）」；表单同〔提交需求〕：变更标题（1..200）、想改什么（1..20,000，简易 Markdown）、期望日期（≥今天，可空）、附件（≤10）；说明「提交后团队会评估对进度和价格的影响，必要时请你确认」；成功页显示 IN 号并返回需求详情；原来件不可申请时（110449）显示「这条需求目前不能申请变更」 |
| **变更详情 / 确认** `/portal/intakes/:intakeID/changes/:changeID` | 状态标签（评估中 / 待你确认 / 已接受（需求 vN）/ 未接受（理由））；原文与附件；待答澄清问题（同需求详情）；评估完成后显示「变化」卡：验收标准变化（新增 / 移除 / 改为，逐条通俗文字）、进度（「预计延后 3 天」/「提前 2 天」/「无变化」）、新的预计日期、价格变化（仅在团队填写时，含说明）、团队说明。**待你确认**时底部：〔接受变更〕/〔不接受〕；不接受须填理由（10..2000，计数器）；提交前确认对话框重述进度与价格变化；`approval_round` 随页面数据提交，110455 `superseded` 时提示「团队已更新本次变更，请重新查看」并刷新。已决定时只读显示结果、时间；团队登记的显示「由团队记录（{日期}）」。移动端按钮固定底部，触控 ≥44px |
| **验收** `/portal/deliveries/:deliveryID` | 07 投影：交付说明、演示链接、交付物清单（逐条）、证据摘要、清单指纹（前 8 位）。每条交付物一行：〔接受〕/〔不接受〕切换；不接受时必选类别（功能缺陷 / 需求不符 / 样式 / 性能 / 安全 / 其他）并填写原因（10..2000 字）。顶部快捷〔全部接受〕。底部：签署人姓名（1..100，必填）、复选框「我已核对以上交付内容，提交后不可修改」（必勾）、整体备注（可空）；〔提交验收〕在每条都有选择、不接受条目信息完整、签名与勾选齐全时才可用；提交前汇总显示「将接受 n 项、不接受 m 项」。110440 `manifest_changed` 时提示「交付内容已更新，请重新核对」并刷新；已回执时只读显示逐条结果、签署人与时间。**D120 批注**：每条交付物下列出该项的可批注文件（`GET …/files`，带「n 条批注」计数），〔查看并批注〕在本页内展开面板（不跳转、不新开窗口；移动端为全屏抽屉，关闭后回到原条目且已填写的选择不丢失）：左侧按 `viewer_kind` 渲染文件（代码 / 表格 / 网页与报告 / 文档；网页、报告与 PDF 在预览域 iframe 中），右侧为本交付的批注线程（按锚点位置排序；「基于旧版本」分组同 03 BR-03-137）。〔批注〕模式下选中代码行、表格行、网页区域或文档段落后填写正文（1..2000，计数器）提交；线程内可〔回复〕。不可批注的文件只显示〔下载〕与说明「此文件不支持批注」；视频与章节显示「v1.1 支持」；`can_comment=false` 时面板只读、不出现〔批注〕〔回复〕。110458 时提示「文件内容已更新，请刷新后再批注」并重新拉取；110460 提示「请由本项目的签收人批注」。**不接受**的条目下出现〔引用批注〕：从该项已有的未转换批注中多选（≤20，显示锚点位置与原文摘要），提交后回执逐条显示「引用批注 n 条」并可点开定位；接受的条目不出现引用入口。页面与面板均不出现执行、工作项、Agent、日志或团队内部批注 |

---

## 9. 通知

经 09 `Notifier`，路由与偏好由 08 统一（本表与 08 §9 通知目录一致）；团队侧同时进入 08「我的裁决」（`INTAKE_TRIAGE`：新建项目 / 追加 / 返工来件待处置 / 变更申请待评估 / 甲方已对变更作出决定；澄清回复与甲方无响应为通知）。模板键 `intake.notify.<name>`，三语；甲方模板使用 BR-01-40 显示名与 BR-01-68 AI 文案，**不含**团队成员与 Agent 名称。

| # | 事件 | 接收人 | 渠道 | 文案要点（zh-CN） | 去重 / 合并 |
|---|---|---|---|---|---|
| N0 | 来件创建（send_receipt=true） | 甲方联系人 | 原开口（邮件回复 / 门户 / webhook） | 「我们已收到你的需求《{title}》（编号 {ref}）」 | 每来件一次；AUTO_REPLY 来源不发 |
| N1 | `intake.received` | 开口 notify 对象 | 站内 + 邮件摘要（08 §9） | 「新来件 {ref}：{title}（{contact}，{channel}）」；kind=CHANGE：「{contact} 对 {原 ref} 申请变更：{title}」 | 同开口 5 分钟内合并为「n 条新来件」 |
| N2 | `intake.triaged`（rev=1） | 同 N1 | 站内 | 「{ref} 分诊卡已就绪：规模 {size}，建议 {project}」 | 每来件一次 |
| N3 | 分诊超时 30 分钟 | 同 N1 | 站内 + 邮件 | 「{ref} 已等待分诊 30 分钟」 | 每来件一次 |
| N4 | 甲方回答 / 轮次答完 | 发送该轮的成员 + 最近处理人 | 站内 | 「{contact} 回答了 {ref} 的 {n} 个问题」 | 同来件 10 分钟内合并 |
| N4b | needs_mapping | 同 N4 | 站内 | 「{ref} 收到回复，需要你对应到问题」 | 每消息一次 |
| N5 | `intake.clarification_expired` | 同 N4 + 开口 notify 对象 | 站内 + 邮件 | 「{contact} 已 {days} 天未回复 {ref} 的澄清，请决定：按假设转化 / 再次询问 / 拒绝」 | 每轮一次 |
| N6 | 终态来件收到甲方新消息 | 终态动作执行人 | 站内 | 「已结束的 {ref} 收到甲方新消息」 | 同来件 1 小时内合并 |
| N7 | 解析失败 / IMAP 故障 / 协调员失效 | `intake_channel_manage` 持有者 | 站内 + 邮件 | 「开口 {name} 异常：{reason}」 | 同开口同原因 24h 一次 |
| N8 | 澄清 / 提醒 / 转化 / 合并 / 拒绝 | 甲方联系人 | 原开口 | 澄清：「为准确理解你的需求，请回答以下问题：Q1 …」+ 门户入口说明（不含链接明文）；提醒：「仍有 {n} 个问题待回答（{due} 前）」；转化：「你的需求已立项，进入进行中」；变更转化（BR-01-82）：「你的变更《{title}》已接受，需求更新为 v{n}：{标准变化摘要}；进度 {schedule_delta}，预计 {new_date}{；价格变化 {price}}」；撤回确认（T18）：「团队已撤回《{title}》的确认请求，正在重新评估」；合并 / 拒绝：client_message | 提醒按（轮, 序号）去重 |
| N9 | 链接 72h 内到期且有未结来件 | 链接签发人 | 站内 | 「{contact} 的门户链接将于 {date} 到期」 | 每链接一次 |
| N10 | `IntakeReplySender.SendDelivery`（07 发送任务） | 甲方联系人 | 原开口 | 「《{title}》的交付已就绪，请在门户中验收」 | 每（交付, 联系人, attempt）一次；`delivery.sent` 事件不再触发 |
| N10b | `IntakeReplySender.SendAcceptanceReminder` | 甲方联系人 | 原开口 | 「《{title}》仍待你验收（已 {days} 天）」 | 每（交付, 联系人, 序号）一次 |
| N11 | `acceptance.received` | 甲方联系人 | 原开口 | 「我们已收到你的验收结果：{result}」 | 每回执一次 |
| N12 | 访问码 | 联系人登记邮箱 | 邮件（即时，不合并） | 「你的访问码是 {code}，10 分钟内有效；如非本人操作请忽略」 | 冷却 60s，每链接每小时 ≤5 |
| N13 | 签发链接且 send_email=true | 联系人 | 邮件 | 「{team} 邀请你通过以下链接提交和跟进需求（{date} 前有效）」 | 每签发一次 |
| N14 | `intake.client_approval_requested`（T15） | 甲方联系人 | 原开口（门户可见 + 有邮箱时邮件；API → webhook 元数据） | 「你的变更《{title}》需要你确认：进度 {schedule_delta}，预计 {new_date}{，价格变化 {price}}。请在门户中查看并确认」+ 门户入口说明（不含链接明文；邮件回复不视为确认） | 每（来件, round）一次 |
| N15 | J7 甲方提醒（每 7 天，≤3 次） | 甲方联系人 | 原开口 | 「《{title}》的变更仍等待你确认（已 {days} 天）」 | 每（来件, round, 序号）一次 |
| N16 | `intake.client_approval_decided`（APPROVED / REJECTED） | T15 请求人 + 最近处理人 + `HOLDERS(intake_convert)` | 站内 + 邮件 | 接受：「{contact} 已接受 {ref} 的变更（{via}），可以转化」；不接受：「{contact} 未接受 {ref} 的变更：{comment 前 80 字}」 | 每（来件, round）一次 |
| N17 | J7 团队提醒（每 14 天） | T15 请求人 + `HOLDERS(intake_convert)` | 站内 + 邮件 | 「{ref} 的变更已等待甲方确认 {days} 天，可联系甲方、登记线下确认或撤回」 | 每（来件, round, 序号）一次 |

出站邮件：From = 开口地址（PORTAL / API 来件用部署配置 `intake.email.outbound_from`），显示名按 BR-01-40；Reply-To = `<local>+r<reply_token>@<inbound_domain>`；`In-Reply-To` / `References` 延续线程；Subject `Re: {原主题} [{ref}]`；回执与提醒带 `Auto-Submitted: auto-replied` 防自动回复环路。

---

## 10. 验收标准

格式同 00 §11，以表格紧凑列出（ID / 测试层 / 关联 / Given · When · Then）。

| ID | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-01-01 | e2e | US-01-01, BR-01-60/61 | **G** 联系人 Lee 有邮箱与有效链接，门户需访问码 **W** 打开链接、输入邮件中的 6 位码、提交标题 + 描述 + 2 个附件 **T** 返回 IN 号，来件 RECEIVED，附件以 `intake.attachment` 绑定，Lee 收到 N0，团队收到 N1，outbox 有 `intake.received` |
| AC-01-02 | integration | US-01-02, BR-01-41/42 | **G** EMAIL 开口 `req-acme`、联系人 lee@x.com **W** 入站 webhook 投递签名正确的新邮件 **T** 202；登记 CREATED_INTAKE；title = Subject；原始 MIME 可下载；回执邮件 In-Reply-To = 原 Message-ID |
| AC-01-03 | integration | BR-01-42 | **G** IN-7 已发澄清邮件 M1 **W** Lee 回复（References 含 M1）**T** 追加为 IN-7 的 ANSWER 消息，不建新来件；同一回复若 From 为 eve@other.com → QUARANTINED(UNMATCHED_REPLY) |
| AC-01-04 | integration | US-01-03, BR-01-50/52 | **G** API 开口白名单含服务账号 S，S 的 PAT 含 `intake_submit`，回调订阅已配置 **W** S 提交来件 **T** 201；webhook 收到 `intake.intake_received`（仅 target_id 等元数据）；`GET /intake-submissions/{id}` 返回状态 |
| AC-01-05 | integration | BR-01-50 | **G** S2 有 `intake_submit` 但不在白名单 **W** 提交 **T** 403 110403，`reason_code=CHANNEL_PRINCIPAL_NOT_ALLOWED`，无来件 |
| AC-01-06 | integration | BR-01-51 | **G** S、S3 均在白名单，IN-9 由 S3 提交 **W** S 读 `/intake-submissions/{IN-9}` **T** 404 110404 |
| AC-01-07 | integration | US-01-04, BR-01-10/12 | **G** 来件 RECEIVED，协调员 C 有 `intake_triage` **W** C 提交分诊卡 **T** revision=1、TRIAGED、发布 `intake.triaged`；C 与人同时基于 rev0 提交时一个 201、另一个 409 110409 |
| AC-01-08 | unit | BR-01-12 | **G** 最新 revision=2 **W** 人提交 `based_on_revision=1` **T** 409 110443，revision 不变 |
| AC-01-09 | e2e | US-01-05, BR-01-21/25 | **G** TRIAGED 来件，负责人 M 对项目 P 有 `requirement_write` **W** M 修改验收标准后 `PUT /conversion {NEW_REQUIREMENT, P, rev=2}` **T** 同事务生成人工 rev3 与 02 需求；来件 CONVERTED、requirement_id 非空；审计 `intake.intake_converted` 含 authority_source=ROLE:delivery_lead |
| AC-01-10 | integration | BR-01-25 | **G** 02 `CreateFromIntake` 返回错误 **W** 转化 **T** 502 110502；来件仍 TRIAGED，无新 revision，无 outbox 事件 |
| AC-01-11 | integration | BR-01-26/70 | **G** 已转化 **W** 同 Idempotency-Key 重放 **T** 与首次相同响应；换键同参数 → 200 同结果；参数不同 → 422 110421 |
| AC-01-12 | integration | BR-01-70 | **G** 键 K 已用于提交 A **W** 用 K 提交不同正文 B **T** 422 176425（平台码），无新来件；**W** 首次请求未完成时以 K 并发重放 **T** 409 176409 |
| AC-01-13 | integration | §4.1 非法 | **G** 来件 RECEIVED **W** 转化 **T** 422 110422 |
| AC-01-14 | integration | §4.1 非法, T10 | **G** CLARIFYING **W** 人转化不带 ack **T** 422 110436；带 ack → 200，OPEN 轮 CANCELLED，未答问题 WITHDRAWN |
| AC-01-15 | integration | §4.1 非法, BR-01-24 | **G** CLARIFYING，Agent R 持有效委托 **W** R 带 ack 转化 **T** 403 110403 `reason_code=NON_DELEGABLE`，审计 `intake.conversion_denied` outcome=denied |
| AC-01-16 | integration | §4.1 非法 | **G** 来件分别为 CONVERTED / MERGED / DECLINED **W** 提交分诊卡、发送澄清、转化（不同参数）、合并、拒绝 **T** 均 422 110421，数据不变 |
| AC-01-17 | integration | §4.1 非法 | **G** CLARIFYING 有 OPEN 轮 **W** 再发一轮 **T** 422 110423；**G** TRIAGED 无 DRAFT 问题 **W** 发送 **T** 422 110424；**G** TRIAGED 无 OPEN 轮 **W** 取消轮 **T** 422 110420 |
| AC-01-18 | integration | BR-01-30, §4.1 | **W** 合并到自身 / MERGED / DECLINED 来件 **T** 422 110426；合并到他租户来件 → 404 110404 |
| AC-01-19 | integration | BR-01-32 | **W** 拒绝缺 reason **T** 422 110427；**W** reason_code=SPAM 且 notify_client=true **T** 落库 notify=false，甲方无邮件 |
| AC-01-20 | integration | US-01-14, BR-01-24 | **G** 项目 P 为 AUTONOMOUS，Agent R 对 `intake_convert` 有委托，卡片作者为 Agent C **W** R 以 NEW_REQUIREMENT 转化 TRIAGED 来件 **T** 200，authority_source=DELEGATION:<id> |
| AC-01-21 | integration | BR-01-24 | 同上但分别 ① P 为 HYBRID ② 模式 NEW_PROJECT ③ 模式 APPEND ④ 卡片作者即 R（08 S3 依 `TriageCardAuthorID` 判 SELF_REVIEW） ⑤ 模式 REOPEN_WORK_ITEMS **T** 均 403 110403（`reason_code` 依次 NO_DELEGATION / NON_DELEGABLE / NON_DELEGABLE / SELF_REVIEW / HUMAN_ONLY），来件不变 |
| AC-01-22 | integration | BR-01-22, §6 | **G** 工程师 E 仅有 `intake_read` **W** 转化 / 合并 / 拒绝 / 发送澄清 / 改开口 **T** 均 403 110403，data.permission 分别为 intake_convert / intake_convert / intake_decline / intake_triage / intake_channel_manage，审计 denied |
| AC-01-23 | integration | BR-01-22 | **G** M 有 `intake_convert` 但对 P 无 `requirement_write` **W** 转化到 P **T** 403 110403；APPEND 而无 `scope_change` → 403 110403 |
| AC-01-24 | integration | BR-01-23 | **G** 项目阶段 CLOSED **W** 转化到该项目 **T** 422 110435 |
| AC-01-25 | integration | 并发 | **G** 两名负责人对同一 TRIAGED 来件（version=5）同时转化与拒绝 **T** 恰一个成功，另一个 409 110409，outbox 只有一个终态事件 |
| AC-01-26 | integration | US-01-06, BR-01-15/16 | **G** EMAIL 来件发出 2 题澄清（默认配置）**W** 时钟推进至 48h、96h **T** 各发一次同线程提醒；两个实例并发运行 J3 时不重复 |
| AC-01-27 | integration | US-01-08, T8 | **G** 澄清轮 168h 未答 **W** J3 运行 **T** 轮 EXPIRED、问题 EXPIRED、来件 TRIAGED + client_unresponsive=true，N5 发出；来件**未**被拒绝 |
| AC-01-28 | integration | §4.3 | **G** 问题 EXPIRED **W** 甲方在门户回答 **T** 问题 ANSWERED、client_unresponsive=false，轮仍 EXPIRED，来件状态不变 |
| AC-01-29 | integration | T6 | **G** OPEN 轮 2 题已答 1 **W** 门户回答第 2 题 **T** 轮 ANSWERED，来件 TRIAGED，发布 `intake.triage_requested{reason:ROUND_ANSWERED}` |
| AC-01-30 | integration | BR-01-17/18 | **G** 3 题轮，邮件回复含「1. … 2. …」两段 **T** needs_mapping=true、问题仍 SENT、N4b 发出；团队 `recorded-answer` 后问题 ANSWERED(RECORDED_BY_TEAM) |
| AC-01-31 | integration | §4.3 | **W** 回答 ANSWERED 或 WITHDRAWN 的问题 **T** 422 110425 |
| AC-01-32 | unit | BR-01-43/44 | 邮件分别为未知发件人（KNOWN_CONTACTS）、DMARC fail、`X-Spam-Flag: YES`、`Auto-Submitted: auto-replied`、来自开口自身地址、BLOCKED 联系人 **T** 依次为 QUARANTINED(UNKNOWN_SENDER)、QUARANTINED(DMARC_FAIL)、QUARANTINED(SPAM)、DISCARDED(AUTO_REPLY)、DISCARDED(LOOP)、REJECTED(BLOCKED_CONTACT)，均无回执 |
| AC-01-33 | integration | BR-01-45 | **W** 同一 Message-ID 投递两次（含并发）**T** 只有一条登记、一条来件 |
| AC-01-34 | integration | BR-01-47, §4.4 | **G** 隔离邮件 **W** 有 `intake_triage` 的成员放行并勾选建联系人 **T** 建联系人与来件，RELEASED；再次放行 → 422 110420 |
| AC-01-35 | integration | §6.5 | **W** `/inbound-emails` 签名错误或时间戳偏差 301s **T** 401 110442，无登记；MIME 36MB → 413 110413 |
| AC-01-36 | unit | BR-01-03/46 | 附件 26MB / 第 11 个 / 合计 51MB / `.exe` **T** 分别 110413 / 110439 / 110439 / 110415；邮件中的超限附件被跳过并标注，邮件仍建来件 |
| AC-01-37 | integration | BR-01-04 | **G** 联系人今日已提交 20 条 **W** 第 21 条 **T** 429 110433 + Retry-After；当日第 21 封邮件 → QUARANTINED(QUOTA) |
| AC-01-38 | integration | BR-01-05 | **G** 门户开口 DISABLED **W** 提交新需求 **T** 422 110430；**W** 回答已有问题、提交验收 **T** 成功 |
| AC-01-39 | integration | BR-01-65（猜测） | **W** 用随机 43 字符 token 建会话 **T** 401 110401（不区分原因）；**W** 用已过期 / 已吊销链接 **T** 410 110410，data.reason=expired / revoked |
| AC-01-40 | integration | BR-01-64 | **W** 同一 IP 10 分钟内第 11 次 `POST /portal-sessions` **T** 429 110429 + Retry-After，且不执行 token 查找 |
| AC-01-41 | integration | BR-01-61 | **W** 连续 5 次错误访问码 **T** 之后正确码也返回 429 110412，须重新打开链接；**W** 发码 11 分钟后输入正确码 **T** 401 110411 |
| AC-01-42 | integration | BR-01-63（吊销） | **G** Lee 有活跃会话 **W** 团队吊销其链接 / 屏蔽联系人 **T** Lee 下一次请求即 410 110410 / 401 110401，无缓存窗口 |
| AC-01-43 | integration | BR-01-62 | **W** 带有效 cookie 但缺 `X-Portal-Request` 调用 `POST /portal-intakes` **T** 403 110444 |
| AC-01-44 | integration | BR-01-66/72（跨租户） | **G** 租户 A 的联系人会话 **W** 访问租户 B 的来件 id、同租户其他联系人的来件 id、非本人来件关联的交付 **T** 均 404 110404，响应不含目标字段 |
| AC-01-45 | unit | BR-01-66 | 门户响应契约测试：字段白名单之外（work_item、agent、triage、decline_reason、note、cost 等）不出现 |
| AC-01-46 | e2e | US-01-13, §7.4 | **G** 交付 D（SENT，3 个条目）的 source_intake 属于 Lee **W** 对条目 2 选〔不接受〕但不填类别与原因 **T** 前端禁用提交；直调 API → 422 110427；**W** 补全类别与原因、签名并勾选确认后提交 **T** 200，07 推导整体结果 PARTIALLY_ACCEPTED，记 actor=CLIENT:<contact>、signer_name；intake 消费 `acceptance.received` 建 REWORK 来件一条，`origin_acceptance_id` 填入、分诊卡 `rejected_items` 含条目 2；事件重复投递不重复建 |
| AC-01-47 | integration | US-01-13 | **W** 对已回执交付再次提交验收 **T** 422 110440 |
| AC-01-48 | integration | US-01-11, BR-01-60 | **W** 签发链接 **T** 响应含一次性 access_url，库中仅 token_hash；同键重放返回 `access_url:null, replayed:true`；第 6 条有效链接 → 422 110441 |
| AC-01-49 | integration | BR-01-40 | **G** 租户未设 display_name、部署 name 为空 **T** 门户与回执邮件显示租户名；租户名也为空时显示「需求门户」；遍历全部模板与门户响应不含硬编码品牌字符串 |
| AC-01-50 | e2e | BR-01-68 | **G** ai_disclosure=BY_MODE；IN-1 属 AUTONOMOUS 项目、IN-2 属 HYBRID、IN-3 未转化 **T** 门户依次显示「由 AI Agent 完成，团队负责人监督」「由 AI 与工程师协作完成」、无标签；HIDDEN 时均无 |
| AC-01-51 | unit | BR-01-01 | **W** 任何 PATCH/PUT 携带 raw_content / title / contact_id **T** 400 110400；仓储更新白名单不含不可变列（单测固定） |
| AC-01-52 | integration | BR-01-73 | **G** 数据库不可用 **W** 调用任一 intake 接口 **T** 503 110503，进程不崩溃，日志有 Warn |
| AC-01-53 | e2e | §8 | 来件列表 / 详情 / 开口设置 / 门户各页在 375、768、1024、1440 下：加载骨架、空态、错误重试、无权限（显示缺失权限码，门户除外）、离线条均正确；离线时命令按钮禁用 |
| AC-01-54 | integration | §6.1 | **W** 创建 WEBHOOK 或 IM 开口 **T** 422 110431；第二个 PORTAL → 409 110445；EMAIL local-part 被他租户占用 → 409 110438 |
| AC-01-55 | integration | J2 | **G** IMAP 开口 **W** 两实例同时运行 J2 **T** dlock 保证只有一个拉取；事务提交前崩溃时邮件仍在 INBOX，下次按 Message-ID 去重 |
| AC-01-56 | integration | BR-01-71 | **W** PATCH 不带 If-Match **T** 428 110446；版本不符 → 409 110409 + data.current_version；GET 单项响应带 `ETag` |
| AC-01-57 | integration | §6.8 IntakeReplySender, §7.4 | **G** 交付 D 关联 IN-3（PORTAL，联系人有邮箱）**W** 07 调 `SendDelivery` 后发布 `delivery.sent` **T** 甲方恰收到 1 封 N10 邮件、IN-3 线程 1 条 DELIVERY_NOTICE；消费 `delivery.sent` 不再产生通知；同 `(delivery, contact, attempt)` 重调返回原 MessageRef |
| AC-01-58 | integration | §6.8, US-01-03 | **G** API 开口来件 IN-4 已转化 **W** `SendDelivery` **T** 回调订阅收到元数据；`GET /intake-submissions/{IN-4}` 返回 `deliveries[]` 含 manifest 与 72h 有效签名 `download_url`，73h 后链接失效 |
| AC-01-59 | integration | US-01-19, T14, §6.8 ClarificationRequester | **G** IN-5 CONVERTED，工作项 W 因澄清阻塞 **W** 02 在其事务内调 `RequestFromWorkItem(IN-5, W, 2 题)` **T** 新 OPEN 轮 `work_item_id=W` 并经原开口发送，来件仍 CONVERTED；甲方答完后 `intake.clarification_answered{round_closed:true, work_item_id:W}`；02 事务回滚时无轮、无出站消息 |
| AC-01-60 | integration | T14 | **G** IN-5 已有 OPEN 轮 **W** 再次 `RequestFromWorkItem` **T** 问题追加为 DRAFT、返回 RoundID=nil、通知 N4；轮数已 20 → 110433 |
| AC-01-61 | e2e | US-01-18, BR-01-27/28, T9 | **G** 返工来件 R1，`rejected_items` 含 W1（FUNCTIONAL_DEFECT）、W2（REQUIREMENT_MISMATCH）**W** 负责人以 REOPEN 处置 W1 **T** 同事务调 02 `ReopenFromAcceptance`，W1 → REWORK 并有新返工分支；R1 CONVERTED，`reopened_work_item_ids=[W1]`；**W** 再以 APPEND 处置 W2（带 reauth_proof、base_version_no）**T** 生成变更记录与需求新版本 |
| AC-01-62 | integration | BR-01-28, §4.1 | **W** 对 kind=NEW 来件用 REOPEN → 422 110448；`work_item_ids` 含不在 `rejected_items` 的项 → 400 110400；项目 CLOSED → 422 110435；工程师无 `decision_request_changes` → 403 110403 |
| AC-01-63 | integration | BR-01-21 | **W** NEW_PROJECT / APPEND 缺 `reauth_proof` 或 step-up 已过 5 分钟 **T** 401 110447，无写入；APPEND `base_version_no` ≠ 当前版本 → 409 110409 |
| AC-01-64 | integration | BR-01-25 | **G** TRIAGED 来件，联系人 Lee **W** NEW_PROJECT 转化 **T** 同事务调 07 `CreateForIntakeOn`（项目 HYBRID、默认频道、Lee 为 PRIMARY）再调 02 `CreateFromIntake`；任一失败整体回滚 110502，无项目、无需求 |
| AC-01-65 | integration | BR-01-24 | **G** AUTONOMOUS 项目 P，Agent R 仅有 `intake_convert` 委托（无 `requirement_write` 角色）**W** R 以 NEW_REQUIREMENT 转化 **T** 200，需求创建成功，authority_source=DELEGATION:<id> |
| AC-01-66 | integration | §6.7 | **W** 门户验收 `items[]` 少一个条目 / `confirmed=false` / `manifest_sha256_ack` 与当前清单不符 **T** 依次 400 110400 / 400 110400 / 422 110440(`manifest_changed`)，07 无回执 |
| AC-01-67 | integration | §3.13b | **G** 负责人在两个设备打开同一来件 **W** 各发 4 次 15s 心跳 **T** 当日 `attention_ms=120000`；Agent 主体发心跳 → 403 110403；`IntakeReader.AttentionByDay` 返回该值 |
| AC-01-68 | integration | §6.8 IntakeReader | **G** 项目 P 关联 2 个来件，其一有 OPEN 轮 **W** 07 调 `OpenClarifications(P)` **T** 返回 1；轮关闭后返回 0 |
| AC-01-69 | integration | §6.7, 复审 #1 | **G** 交付 D SENT，Lee 已验证门户会话 **W** 提交验收 `confirmed=true` **T** 调 07 `Record` 时携带 `IntakeContactID=Lee`、`Confirmed=true`，200；**W** `confirmed=false` 或缺失 **T** 422 110400，不调用 07 |
| AC-01-70 | integration | §6.8 IntakeReplySender, 复审 #12 | **G** 07 以 `AttemptNo=1`、`IntakeIDs=[IN-3, IN-4]` 调 `SendDelivery` **W** 同参数重调 **T** 返回原 MessageRef，IN-3、IN-4 各 1 条 DELIVERY_NOTICE；**W** `AttemptNo=2` **T** 视为新的一次发送 |
| AC-01-71 | integration | §7.4, 复审 #23 | **G** 澄清消息 M（delivery_state=PENDING）**W** 消费 `notification.delivery_state_changed{source_ref=M, state=SUPPRESSED}` **T** M.delivery_state=SUPPRESSED；重复投递不重复处理；未知 source_ref 无副作用 |
| AC-01-72 | integration | BR-01-21, 复审 #11 | **G** APPEND 转换且 step-up 已通过 **W** 调 02 `AppendFromIntake` **T** 入参含 `TenantID`、`Summary`，不含 ReauthProof；step-up 缺失时本模块先 401 110447，不调用 02 |
| AC-01-73 | e2e | US-01-20, BR-01-76/77, T1 | **G** IN-5 已 CONVERTED 为需求 R（v2），联系人 Lee 已验证门户会话 **W** Lee 在需求详情点〔申请变更〕提交标题、描述与 1 个附件 **T** 201，新来件 IN-9 kind=CHANGE、RECEIVED、`change_of_intake_id=IN-5`、`target_requirement_id=R`、`contact_id=Lee`；outbox 有 `intake.received{kind:CHANGE, change_of_intake_id, target_requirement_id}`；团队收到 N1「对 IN-5 申请变更」；门户 IN-5 详情变更列表显示 IN-9「评估中」，「我的需求」无 IN-9 单独卡片 |
| AC-01-74 | integration | BR-01-76/77, §4.1 非法 | **W** ① 在 TRIAGED 的 IN-6 上申请变更 ② 在 DECLINED / MERGED 来件上申请 ③ Lee 对其他联系人的 CONVERTED 来件申请 ④ API 调用方 S 以 `kind=CHANGE` 对 S3 提交的来件申请 ⑤ `kind=CHANGE` 缺 `change_of_intake_id` **T** 依次 422 110449 / 422 110449 / 404 110404 / 404 110404 / 400 110400；均无新来件；库层 CHECK 拒绝 kind=CHANGE 而 `target_requirement_id` 为 NULL 的直接插入 |
| AC-01-75 | integration | US-01-21, BR-01-78 | **G** IN-9（CHANGE，R 当前 v2），协调员 C 起草卡片：`criteria_diff=[ADD 1 条, RETIRE c3]`、`schedule_delta_days=4`、并在请求中写 `requires_client_approval=false` **W** C 提交分诊卡 **T** 服务端调用 02 `Preview(R, 2, diff)`，`affected_work_items` 含 c3 关联的 W7(STALE) 与 1 条 NEW；调用 07 `WouldBreach(R, {4, cost, scope_delta=0})` 返回 [TIME]，卡片落库 `tolerance_check.breached=[TIME]`、`requires_client_approval=true`（请求值被忽略）；C 的卡带 `client_price_delta` → 400 110400；`base_version_no=1` → 409 110453；07 不可用 → 503 110503 且无新 revision |
| AC-01-76 | integration | T15, BR-01-79 | **G** IN-9 TRIAGED，最新卡 rev3 `requires_client_approval=true` **W** 负责人 M（持 `scope_change`）`PUT /intakes/IN-9/client-approval {triage_card_revision:3, message}` **T** IN-9 → CLIENT_APPROVAL，`client_approval={PENDING, round:1, card_revision:3}`；`intake.client_approval_requested{round:1, breached:[TIME]}`；审计；Lee 收到 N14，邮件不含链接明文；**W** 协调员 Agent C 或持委托的 Agent 发起同一请求 **T** 403 110403 `NON_DELEGABLE`，审计 `intake.client_approval_denied`；**W** 工程师 E（无 `scope_change`）**T** 403 110403 `MISSING_PERMISSION` |
| AC-01-77 | integration | T15, §4.1 非法 | **W** ① 对 `requires_client_approval=false` 的卡 T15 ② 对 kind=NEW 来件 T15 ③ 卡片无 `change_impact` 时 T15 ④ `triage_card_revision=2`（最新为 3）**T** 依次 422 110420 / 422 110420 / 422 110452 / 409 110443；来件均不变 |
| AC-01-78 | e2e | US-01-22, T16, BR-01-80/84 | **G** IN-9 CLIENT_APPROVAL round 1 **W** Lee 打开门户变更详情 **T** 显示标准变化（新增 1 条、移除 1 条的通俗文字）、「预计延后 4 天」、新日期、团队说明；响应与 DOM 均不含 `internal_cost_delta_micros`、工作项 id、`tolerance_check`、「容差」字样；**W** Lee〔接受变更〕`{approval_round:1, decision:APPROVED}` **T** IN-9 → TRIAGED，`client_approval.state=APPROVED`、`decided_via=PORTAL`、`authority_source=CLIENT:<Lee>`；`intake.client_approval_decided{decision:APPROVED}`；M 收到 N16；同请求重放 200 |
| AC-01-79 | integration | T17, BR-01-83 | **G** IN-9 CLIENT_APPROVAL **W** Lee〔不接受〕不填理由 **T** 前端禁用；直调 → 422 110427；**W** 理由 15 字 **T** IN-9 → TRIAGED、`state=REJECTED` 含理由，N16 发出；**W** 团队随后以 APPEND 转化 rev3 **T** 422 110450；**W** 团队拒绝不带 `client_message` **T** 422 110427；带 `client_message` → DECLINED，门户显示「未接受（{client_message}）」，原需求 R 版本不变 |
| AC-01-80 | integration | T18, T19, BR-01-80 | **G** IN-9 CLIENT_APPROVAL round 1 **W** M 撤回（理由）**T** → TRIAGED、`state=WITHDRAWN`，Lee 收到撤回通知；Lee 随后以 `approval_round:1` 决定 → 409 110455（`not_pending`）；**W** M 改卡为 rev4 并再次 T15 **T** round=2；Lee 用旧页面提交 `approval_round:1` → 409 110455（`superseded`）；**W** M 以 T19 登记线下确认但不附证据 **T** 400 110400；附 1 个邮件原件 **T** `state=APPROVED`、`decided_via=RECORDED_BY_TEAM`，门户显示「由团队记录」；Agent 调用 T19 → 403 110403 `NON_DELEGABLE` |
| AC-01-81 | integration | BR-01-80 | **G** IN-9 CLIENT_APPROVAL **W** M 以团队会话调用门户决定接口、Agent 以服务账号调用、另一联系人 Ann 的门户会话调用 **T** 前两者无门户会话 401 110401，Ann 404 110404；`client_approval` 不变 |
| AC-01-82 | e2e | T9, BR-01-81, 02 BR-02-63, 07 BR-07-62 | **G** IN-9 TRIAGED，rev4 已获甲方 APPROVED（card_revision=4），R 当前 v2，R 的容差 appetite_end_at=D、baseline_no=1 **W** M 带 reauth_proof 以 `APPEND_TO_REQUIREMENT, requirement_id=R, base_version_no=2, triage_card_revision=4` 转化 **T** 同事务：02 `AppendFromIntake(Change{…, ApprovedAdjustment{4 天,…}})` 产生 v3、ChangeRecord（`source_intake_id=IN-9`）、W7 `baseline_stale`；07 R 的 appetite_end_at=D+4 天、baseline_no=2、审计 `delivery.tolerance_changed`；IN-9 CONVERTED、`result_version_no=3`；`intake.converted{tolerance_adjusted:true}`；Lee 收到 N8「需求更新为 v3」，门户 IN-5 版本历史含 v3 |
| AC-01-83 | integration | BR-01-82 | **G** IN-10（CHANGE）最新卡 `requires_client_approval=false` **W** M 直接 APPEND 转化 **T** 成功，不调用 `ApplyApprovedChange`（无 `ApprovedAdjustment`），容差 baseline_no 不变；Lee 收到含新版本、标准变化与进度影响的 N8；门户状态「已接受（需求 v3）」 |
| AC-01-84 | integration | BR-01-81/87 | **G** IN-9、IN-10 都基于 R v2 评估且都可转化 **W** 先转化 IN-10（R → v3），再转化 IN-9 **T** IN-9 409 110453（`data.current_version_no=3`），无部分写入；**G** 转化时 07 `WouldBreach` 返回与卡片不同的越界集合 **T** 409 110453；**G** R 的容差有未决例外 **W** 转化已获确认的 IN-9 **T** 07 返回 127055，整体回滚，422 110435 `data.reason=tolerance_exception_open` |
| AC-01-85 | integration | §4.1 非法, BR-01-79 | **G** IN-9 CLIENT_APPROVAL **W** 协调员提交新分诊卡、发送澄清轮、转化、合并、拒绝 **T** 均 409 110451；**W** Lee 在门户对 IN-9 留言 **T** 409 110451 |
| AC-01-86 | integration | BR-01-85 | **G** IN-5 CONVERTED、IN-6 TRIAGED、IN-7 DECLINED **W** Lee 分别 `POST /portal-intakes/{id}/notes` **T** IN-5 → 422 110454（门户引导〔申请变更〕）；IN-6 → 200 CLIENT_NOTE；IN-7 → 422 110421；Lee 对 IN-5 的邮件回复仍记 CLIENT_NOTE 并发 N6 |
| AC-01-87 | integration | BR-01-86, J7 | **G** IN-9 于 t0 进入 CLIENT_APPROVAL **W** 时钟推进到 t0+7d、14d、21d、28d、42d **T** Lee 在 7/14/21 天各收一次 N15、第 28 天不再收；团队在 14d、28d、42d 各收一次 N17；两个实例并发运行 J7 不重复；IN-9 始终 CLIENT_APPROVAL，未被自动决定或撤回 |
| AC-01-88 | integration | BR-01-81 | **G** IN-11（CHANGE）**W** 以 `NEW_REQUIREMENT` 转化到项目 P **T** 02 `CreateFromIntake` 建新需求，原需求 R 与其容差不变，`intake.converted{mode:NEW_REQUIREMENT}`；以 `NEW_PROJECT` 或 `REOPEN_WORK_ITEMS` 转化 → 400 110400；APPEND 的 `requirement_id ≠ target_requirement_id` → 400 110400 |
| AC-01-89 | e2e | US-01-23, BR-01-88/89/90 | **G** 交付 D（SENT，未回执）的 source_intake 属于 Lee（项目 `can_accept=true`），交付项 I1 选入证据 Ev（`report.csv`，`include_mode=FILE`）**W** Lee 在验收页点 I1 的〔查看并批注〕，选中第 42 行（TABLE_ROW）写批注并提交，再〔回复〕一次 **T** 面板在本页展开，`GET …/files` 返回 Ev 的 `PortalFile`（`viewer_kind`、`version_ref`、`anchor_kinds` 含 TABLE_ROW、≤15 分钟 `content_url`）；POST 201，07 `CreateClientComment` 收到 `intake_contact_id=Lee`，03 线程 `visibility=CLIENT`、作者 `CLIENT:<Lee>`；回复 201；`GET …/comment-threads` 返回该线程与 2 条批注；响应与 DOM 不含执行、工作项、Agent 或 INTERNAL 线程；以旧 `version_ref` 提交 → 409 110458；MEDIA_TIME → 422 110457（`ANCHOR_KIND_UNAVAILABLE`）；`can_accept=false` 的联系人提交 → 403 110460；D 回执后再批注 → 422 110440 |
| AC-01-90 | integration | BR-01-88/89, 07 BR-07-60/63 | **G** 交付 D 选入 Ev（FILE）、Ev2（SUMMARY）、Ev3（FILE 但被构建剔除）；D 所在执行上有 INTERNAL 线程 T4 **W** Lee `GET …/files` 并分别对 Ev2、Ev3 `POST …/comment-threads` **T** 列表只含 Ev；两次 POST 均 422 110456（`data.cause_code=127056`），03 无新线程；`GET …/comment-threads` 不含 T4 |
| AC-01-91 | integration | BR-01-89/91 | **G** Lee 可见交付 D1、D2（均 SENT 未回执）；Ev5 只属于 D2；D2 上有 CLIENT 线程 T5；另一联系人 Kim 的交付 D3 上有线程 T6 **W** Lee `POST /portal-deliveries/{D1}/comment-threads {evidence_id: Ev5}`；`POST /portal-comment-threads/{T6}/comments`；对 D1 回执 `items[I1]={REJECTED, …, comment_thread_ids:[T5]}` **T** 分别 422 110456；404 110404（不泄露 T6 是否存在）；422 110459（`NOT_THIS_ITEM`），D1 无回执写入 |
| AC-01-92 | e2e | US-01-23, BR-01-91, 07 BR-07-64 | **G** 交付 D 的 I1 上有 Lee 的 CLIENT 线程 T1（`report.csv` 第 42 行，`version_ref=v1`）**W** Lee 将 I1 选〔不接受〕（类别、原因 ≥10 字），〔引用批注〕选 T1，I2〔接受〕，签名并提交 **T** 200；07 `Record` 收到 I1 `comment_thread_ids=[T1]`，T1 转为拒收项且锚点快照保留 TABLE_ROW 与 `v1`；回执页 I1 显示「引用批注 1 条」并能定位原行；intake 消费 `acceptance.received` 建 REWORK 来件，分诊卡 `rejected_items[I1].anchors[]` 含 T1 的 `anchor_kind`、`anchor_excerpt`、`version_ref=v1`；即便后续交付使 `report.csv` 变为 v2，来件详情仍按 v1 显示原文；若 I2（ACCEPTED）也带 `[T1]` → 422 110459（`NOT_REJECTED`），无回执写入 |

---

## 11. 度量与埋点

不采集原文、问题 / 回答正文、邮件地址明文（联系人以 id 计）。

| 事件 / 指标 | 属性 |
|---|---|
| `intake_received` | channel_kind, kind, has_attachments, possible_duplicate |
| `intake_triaged` | author_kind, revision, latency_ms（距 received）, size_estimate, confidence_bucket |
| `intake_clarification_sent` | round_no, question_count, sent_via, author_kind |
| `intake_clarification_answered` | answered_via, latency_ms, late（EXPIRED 后作答） |
| `intake_clarification_expired` | unanswered_count |
| `intake_converted` | mode, actor_kind, authority_kind（ROLE / DELEGATION）, time_to_convert_ms, rounds, card_revisions, human_edited |
| `intake_merged` / `intake_declined` | reason_code（仅 declined） |
| `intake_email_disposition` | disposition, reason, ingest_mode |
| `portal_session` | outcome（created / code_sent / verified / unknown_token / expired / revoked / locked / rate_limited） |
| `portal_submission` | outcome, attachments_count |
| `portal_acceptance_submitted` | result |
| `intake_change_requested`（D117） | channel_kind, original_age_days（原来件转化至今天数） |
| `intake_change_assessed` | author_kind, criteria_ops（ADD / RETIRE / REPLACE 计数）, affected_count, breached（维度集合）, requires_client_approval, latency_ms（距 received） |
| `intake_client_approval` | outcome（requested / approved / rejected / withdrawn / recorded_offline）, round, hours_open, reminders_sent |
| `intake_change_converted` | mode, requires_client_approval, tolerance_adjusted, time_to_convert_ms |
| 告警 | `portal_session{outcome=unknown_token}` 5 分钟 >100（疑似 token 枚举）；J1 出现 FAILED；J3 积压（最早 next_reminder_at < now−15min） |

PRD §15 的 `intake_received{channel}`、`intake_converted` 即上表同名事件。

---

## 12. 依赖、风险与待决

**依赖**

| ID | 依赖 | 内容 |
|---|---|---|
| D-02-1 | 02 | `RequirementCreator.CreateFromIntake` / `AppendFromIntake`、`WorkItemReopener.ReopenFromAcceptance`（在调用方事务内执行）；需求记录 `source_intake_id`；调用本模块 `ClarificationRequester` |
| D-02-2 | 02 | `ChangeImpactPreviewer.Preview`（只读）；`AppendFromIntake` 接受 `Change` 载荷并在同事务调用 07 `ToleranceAdjuster.ApplyApprovedChange`（02 BR-02-63，D117） |
| D-07-2 | 07 | `ToleranceReader.WouldBreach`（只读）与 `ToleranceAdjuster.ApplyApprovedChange`（未决例外时拒绝 `127055`，07 BR-07-62，D117） |
| D-07-1 | 07 | `ProjectCreator.CreateForIntakeOn`、`ProjectReader`、`IntakeProgressReader`、`AcceptanceRecorder{ListForIntakes, PortalView, Record}`、`AcceptanceReader.Get`；`delivery.sent` / `acceptance.received` 的 data 含 `intake_ids[]`；07 调用本模块 `IntakeReplySender` 与 `IntakeReader`；**返工来件由 intake 消费事件创建**，07 不直接写来件 |
| D-07-3 | 07 / 03 | D120 甲方批注：07 `DeliveryEvidenceReader.ListClientFacing`、`AcceptanceRecorder.CreateClientComment` / `ReplyClientComment`、`Record` 的 `comment_thread_ids[]`、`AcceptanceReader.Get` 的 `anchors[]`（07 BR-07-63 / 64）；锚点校验、版本绑定与线程上限由 03 `ArtifactComments` 判定（03 BR-03-133…141）；`ListClientFacing` 与 `ReplyClientComment` 待 07 / 03 按 CR-13 补充 |
| D-08-1 | 08 | `AuthorityChecker.Check(ctx, tx, req)` 支持 `intake_convert` 委托判定（项目范围、仅 AUTONOMOUS、仅 NEW_REQUIREMENT，覆盖需求创建）；`intake_decline` 与新建项目不可委托；「我的裁决」`INTAKE_TRIAGE`；step-up purpose `intake_convert_privileged` 登记 |
| D-05-1 | 05 | 订阅 `intake.received` / `intake.triage_requested` 唤醒 `triage_agent_id`；`AgentDirectory`；`agent.retired` |
| D-09-1..4 | 09 | `JobQueue`（J1–J6）、`OutboxPublisher`、`RealtimeBroadcaster`（含集合主题，CR-09）、`Notifier`（邮件需支持自定义 Message-ID / In-Reply-To / References / Reply-To / Auto-Submitted 头，并回写投递状态） |
| D-09-5 | 09 / files | 租户级业务文件写入能力（与 `AccountResourceFiles` 同形：写入系统目录 + 按 purpose 绑定，支持带过期绑定与改绑） |
| D-IAM-1 | 底座 webhook | 订阅按 `target_type` 与开口过滤（MVP 下回调会收到全租户 intake 事件元数据，见 R2） |
| D-IAM-2 | 底座 authn | shared-security 新增 `portal` 限流作用域（CR-06） |
| D-DEP-1 | 部署配置 | `intake.email.inbound_domain`、`intake.email.inbound_secret`、`intake.email.outbound_from` |

**风险**

| # | 风险 | 缓解 |
|---|---|---|
| R1 | 邮件误判导致需求丢失或垃圾泛滥 | 默认隔离而非丢弃；隔离入口常驻列表；原始 MIME 永久保存 |
| R2 | 底座 webhook 无法按开口过滤，回调泄露他人来件的 id 与时间 | 载荷仅元数据；读取受行过滤；D-IAM-1 落地前在开口设置页明示该限制 |
| R3 | 门户链接被转发给第三方 | 访问码发往登记邮箱；短会话；一键吊销 |
| R4 | 协调员草案质量差，人工改写成本高 | revision diff 与 `human_edited` 指标驱动 05/06 改进 |

**待决**

| # | 议题 | 建议 |
|---|---|---|
| Q1 | PRD §9.1「团队专属链接 + 一次性访问码」是否还需要团队公开入口（任何人填邮箱即可提交） | MVP 只做按联系人签发的链接 + 访问码（更安全，同样免注册）；公开入口作为 v1.1 门户配置项，复用 sender_policy |
| Q2 | 拒绝来件是否可委托给 Agent | MVP 不可委托；试点显示垃圾来件占比高时，再开放 `SPAM` 原因码的委托 |
| Q3 | 隔离邮件与 DECLINED 来件原文的保留期 | 属产品 / 合规决定；建议隔离 30 天后人工批量丢弃、来件原文随项目保留，需负责人确认 |
| Q4 | API 调用方能否直接提交验收 | MVP 不支持，验收统一走门户（联系人身份明确）；v1.1 增加 `/intake-submissions/{id}/acceptances`，需 07 定义系统主体验收语义 |
| Q5 | 同一甲方的多位联系人互看需求 | 非目标（PRD §2.4），保持仅本人可见 |

---

## 13. 契约变更请求

| # | 对象 | 请求 | 理由 |
|---|---|---|---|
| CR-01（已合入 00 v1.1） | 00 §3 时间类型 | 改为 PostgreSQL `TIMESTAMPTZ(3)` / MySQL `DATETIME(3)` / SQLite `DATETIME` | 与 backend-module 规约及 `make test-mysql8` 门禁一致；MySQL `TIMESTAMP` 有 2038 上限且被全仓禁止 |
| CR-02（已合入 00 v1.1；本版按 §6.8 更新方法集） | 00 §8 | 新增 `IntakeReader`（提供者 intake）；新增 `RequirementCreator`（02）、`ProjectCreator` / `ProjectReader` / `PortalDeliveries`（07）、`IntakeProgressReader`（02/07） | 转化、门户验收与里程碑所需；均为「调用方事务内委派」 |
| CR-03（已处理：00 v1.1 §3 模块内枚举，不进 §5） | 00 §5 | 新增模块枚举：`IntakeChannelStatus`(ENABLED, DISABLED)、`IntakeKind`(NEW, REWORK)、`IntakeContactKind`(PERSON, SYSTEM)、`IntakeContactState`(ACTIVE, BLOCKED)、`ClarificationRoundState`(OPEN, ANSWERED, EXPIRED, CANCELLED)、`ClarificationQuestionState`(DRAFT, SENT, ANSWERED, WITHDRAWN, EXPIRED)、`EmailDisposition`(PENDING, CREATED_INTAKE, APPENDED, QUARANTINED, RELEASED, DISCARDED, REJECTED, FAILED)、`SizeEstimate`(XS, S, M, L, XL)、`AiDisclosureMode`(HIDDEN, COLLABORATIVE, BY_MODE)、`PortalMilestone`(RECEIVED, CLARIFYING, IN_PROGRESS, AWAITING_ACCEPTANCE, DELIVERED, MERGED, DECLINED)；或在 00 §3 声明「模块私有枚举由模块规格定义」 | 00 §3 要求枚举列取 §5 的值 |
| CR-04（已被 00 v1.2 取代：平台统一幂等表 09 C7） | 00 §9 幂等表 | `<module>_idempotency_keys` 增加 `request_hash`、`status_code`、`response_body`、`principal_or_contact_id` | 「返回首次结果」需要保存响应；识别「同键不同请求」需要请求摘要；门户主体不是 Principal |
| CR-05（已合入 00 v1.1） | 00 §7.1 | 新增事件 `intake.triage_requested`、`intake.clarification_expired`、`intake.merged` | 重新分诊、超时与合并需被 05 / 08 消费 |
| CR-06（已合入 00 v1.1 / F6） | 00 §6.1 / §6.2 / 底座 | 新增权限码 `intake_submit`（团队级，可授予服务账号；默认授予 team_owner、delivery_lead、engineer）与默认角色 `intake_api_client`（仅 `intake_submit`）；shared-security 新增限流作用域 `portal` | API 来件与代录需要不含「读全部来件」的提交权限；门户需要独立限流预算 |
| CR-07（已合入 00 v1.1） | 00 §10 门户路由 | 访问链接格式改为 `/portal#t=<token>`（片段不发往服务器、不进访问日志与 Referer）；保留 `/portal/:accessToken` 作兼容入口并立即 replaceState | 缩小 token 泄露面 |
| CR-08（撤回：tag = 发布路径首段，00 v1.1） | 00 §9 / routing 词汇 | 新增 OpenAPI tag：`intake-channels`、`intake-contacts`、`intakes`、`intake-clarifications`、`inbound-emails`、`intake-submissions`、`client-portal` | routing 规则 8 要求 tag 来自批准词汇 |
| CR-09（已合入 00 v1.1） | 00 §9 实时 | 增加集合主题 `tenant/{tenant_id}/<resources>`（如 `tenant/{t}/intakes`），订阅时按集合读权限过滤 | 列表页新来件需 ≤1s 呈现 |
| CR-10（已合入 00 v1.1） | 00 §6.4 不可委托 | 增加「新建项目」与 `intake_decline`；写明 `intake_convert` 可委托（仅 NEW_REQUIREMENT、仅 AUTONOMOUS 项目） | PRD §9.2 与 BR-01-24 / BR-01-32 |
| CR-11 | 00 §8 | 新增 02 `WorkItemReopener.ReopenFromAcceptance`（REOPEN_WORK_ITEMS）与 07 `AcceptanceReader.Get`；`IntakeReader` 方法集按 §6.8 扩充 | 返工来件按条目处置（审计 C27/C8） |
| CR-12（已合入 00 v2.0，D117） | 00 §5 / §6.5 / §7.1 / §8 | `IntakeStatus` 增 `CLIENT_APPROVAL`；模块内 `IntakeKind` 增 `CHANGE`；§6.5 仅限人类增「请甲方确认变更、登记甲方线下确认」（甲方本人的决定是甲方行为，`CLIENT:<contact_id>`）；§7.1 增事件 `intake.client_approval_requested`、`intake.client_approval_decided`；§8 增 02 `ChangeImpactPreviewer`、07 `ToleranceReader.WouldBreach` 与 `ToleranceAdjuster.ApplyApprovedChange` | 甲方经门户申请变更、超出容差的变更须甲方确认（D117） |
| CR-13（D120，**已并入** 07 / 03 / 00，2026-10-12） | 07 §6.3 / 03 §6.z / 00 §8 | 07 `DeliveryEvidenceReader` 增 `ListClientFacing(ctx, tenant, delivery_id) ([]ClientFacingFile{evidence_id, delivery_item_id, name, size, media_type, viewer_kind, version_ref, anchor_kinds[], content_url, content_url_expires_at, client_thread_count}, error)`（只读；与 `ClientFacing` 同一判定，`content_url` 为预览域短时签名地址 ≤15 分钟）；07 `AcceptanceRecorder` 增 `ReplyClientComment(ctx, {delivery_id, thread_id, intake_contact_id, body≤2000}) (comment_id, error)`（守卫同 `CreateClientComment` ①②，线程须为本交付的 CLIENT 线程），转调 03 新增 `ArtifactComments.ReplyClientOn(ctx, tx, …)`（只对 CLIENT 线程、作者 `CLIENT:<intake_contact_id>`；团队仍不能回复，03 BR-03-141 不变）；00 §8 登记两项方法。不新增共享枚举、权限码或事件 | 门户文件列表与甲方回复自己交付上的批注（BR-01-88 / 89）；07 现有 `ClientFacing` 只判定单个证据、03 只提供 `CreateClientOn` |

> **D120（2026-10-12，锚定批注）已并入本规格**：US-01-23、BR-01-88…91（面向甲方的文件与查看器数据、甲方批注新建 / 回复 / 读取、拒收项引用批注并在返工来件中保留锚点）、§6.7 四个门户端点与验收 `items[].comment_thread_ids[]`、§6.8 `DeliveryEvidenceReader` / `AcceptanceRecorder` 增补、错误码 110456…110460、§8.4 验收页〔查看并批注〕面板与〔引用批注〕、D-07-3、AC-01-89…92、CR-13。批注数据、可见性（只 `CLIENT`）、版本绑定、转换与审计归 07 / 03；门户永不返回内部记录与 INTERNAL 线程。

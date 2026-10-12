# 11 · 领域模板包（模块 `templates`）

> 决议 D112（研究方案 A–G 采纳，第 E 项：首批领域模板为软件、视频、小说、团队任务）与 D113（内部质量门、不穿透）。内核保持领域无关（PRD P7「内核不出现领域词汇」）；领域内容全部以**模板**形式提供：工作流（阶段 → 工作项树 + 必经人工关口）、产物 schema、验证器插件、甲方验收评分标准（rubric）、内部质量门配置、评测集、生成器适配器。
>
> 本规格遵守 `00-conventions.md`（v2.0，尤其 §15）；需要 00 登记的内容见 §13。
>
> **来源说明**：领域阈值与工具信息来自 `research/2026-10-collaboration-research.md` §1.3（视频 / 小说 / 团队调研全部为检索摘要），落地前必须对照官方文档复核（标「需复核」）；所有模型打分类阈值标「需校准」，上线前用真实素材标定，标定前只能产出「建议通过 / 建议拒绝」，不能单独决定内部验收结论。

---

## 1. 范围与目标

**解决**

1. 定义「模板」这一一等对象：可版本化、可被项目锁定版本、可升级；描述一个领域从需求到交付的完整工作流与质量标准。
2. 定义验证器插件协议（确定性 / 模型打分 / 人工三类）、参数与阈值、阈值被甲方 Brief 覆盖的规则、证据输出格式。
3. 定义甲方验收评分标准（硬门槛 + 加权维度 + 通过规则），在内部质量门（产品验收 Agent）与 L4 甲方验收两处共用同一份标准，保证「内部验收的尺子 = 甲方验收的尺子」。
4. 定义内部质量门在各领域的具体内容：测试 Agent 查什么、产品验收 Agent 查什么、`max_internal_rounds`、哪些关口必须人工（`MANDATORY_HUMAN_GATE`）。
5. 定义每个模板的评测集（golden set，20–50 条）、模型评委校准、穿透案例自动入集。
6. 定义生成器适配器（provider adapter）：外部生成服务（视频生成、TTS、图像）可替换，产物记录模型与版本；不硬绑定任何厂商（Sora 2 Videos API 已于 2026-09-24 下线，需复核）。
7. 交付四个一方模板：`software-dev`（SOFTWARE）、`video-production`（VIDEO）、`novel-writing`（NOVEL）、`team-tasks`（TEAM_TASKS）。

**不解决（边界）**

| 事项 | 归属 |
|---|---|
| 工作项状态机、DoR、计划审批、内部质量门路由、升级 | 02 |
| 验证执行（平台重跑、隔离环境、冻结 / 隐藏验收集、测试 diff、证据存储） | 03 |
| 测试 / 验收 Agent 的派单、跨模型家族约束 | 05 |
| 项目创建时选择模板、交付包、甲方验收记录 | 07 |
| 权限码判定、原因码、穿透指标路由 | 08 |
| 云效对齐的工作项字段、状态组、流转矩阵、用例 | 12（软件模板引用之） |
| 机器上的工具可用性（ffmpeg、WhisperX 等的安装与检测） | 04（运行时能力检测扩展为「工具能力」） |
| 模板市场（第三方模板分发与签名） | v2+（PRD §30 冻结） |

---

## 2. 用户故事

| ID | 故事 | 期 |
|---|---|---|
| US-11-01 | 作为团队所有者，我要从模板库选择领域模板创建项目，以便工作流、验证器与验收标准开箱即用 | MVP |
| US-11-02 | 作为交付负责人，我要查看模板的阶段、产物、验证器与评分标准，以便向甲方解释我们如何保证质量 | MVP |
| US-11-03 | 作为交付负责人，我要用甲方 Brief 覆盖模板阈值（如目标平台响度、视频时长、章节字数），以便按甲方要求验收 | MVP |
| US-11-04 | 作为产品验收 Agent，我要按与甲方相同的评分标准逐条打分并附证据，以便内部验收通过即接近甲方验收 | MVP |
| US-11-05 | 作为测试 Agent，我要知道本领域必须检查的项目（如 UI 截图、字幕对齐、连续性），以便内部发现问题 | MVP |
| US-11-06 | 作为团队所有者，我要把项目锁定在模板的某个版本，模板升级时看到差异并选择是否升级 | MVP |
| US-11-07 | 作为交付负责人，我要在视频工作台按镜头查看候选片段、自动 QC 结果与人工挑选 | MVP |
| US-11-08 | 作为交付负责人，我要在小说工作台查看故事圣经、章节、连续性报告与伏笔台账 | MVP |
| US-11-09 | 作为纯人工团队的成员，我要用团队任务模板管理清单、交付物链接与签收，由 AI 助理起草状态 | MVP |
| US-11-10 | 作为质量负责人，我要看到每次穿透被自动加入模板评测集，并定期校准模型评委 | MVP |
| US-11-11 | 作为团队所有者，我要替换视频生成服务商而不改工作流，以便服务下线时业务不中断 | MVP |
| US-11-12 | 作为团队所有者，我要复制一方模板做团队自定义模板（改阈值、加验证器、改人工关口） | v1.1 |
| US-11-13 | 作为团队所有者，我要从成功项目沉淀模板（工作流与评分标准） | v2 |

---

## 3. 领域模型与数据表

### 3.0 类型映射（三方言）

| 逻辑类型 | PostgreSQL | MySQL | SQLite |
|---|---|---|---|
| ID | `BIGINT` | `BIGINT` | `INTEGER` |
| STR(n) | `VARCHAR(n)` | `VARCHAR(n)` | `TEXT` |
| TEXT | `TEXT` | `MEDIUMTEXT` | `TEXT` |
| JSON | `JSONB` | `JSON` | `TEXT` |
| TIME | `TIMESTAMPTZ(3)` | `DATETIME(3)` | `DATETIME` |
| DEC | `NUMERIC(18,6)` | `DECIMAL(18,6)` | `REAL` |
| BOOL | `BOOLEAN` | `TINYINT(1)` | `INTEGER` |
| ENUM | `VARCHAR(32)` + CHECK | `VARCHAR(32)` + CHECK | `TEXT` + CHECK |

所有表：`id` 雪花 ID 主键；`tenant_id` 必填（一方模板的 `tenant_id = 0` 表示系统级，只读，按 00 §3 基础设施表例外登记，见 CR-11-06）；审计列 `created_at/updated_at/created_by/updated_by`；可变聚合根带 `version`（ETag / If-Match）。MySQL 不支持部分唯一索引处，用生成列 `active_key` + 普通唯一索引。

### 3.1 枚举（模块内；共享者见 §13）

| 枚举 | 值 |
|---|---|
| `TemplateDomain`（共享，00 §5） | `SOFTWARE`, `VIDEO`, `NOVEL`, `TEAM_TASKS` |
| `TemplateState` | `DRAFT`, `PUBLISHED`, `DEPRECATED`, `RETIRED` |
| `TemplateOrigin` | `FIRST_PARTY`, `TEAM`（团队自定义，v1.1） |
| `VerifierKind` | `DETERMINISTIC`（硬门槛，可单独判定）, `MODEL_SCORED`（需校准，只出建议）, `HUMAN`（人工签字） |
| `VerifierRunner` | `MACHINE_COMMAND`（在机器上运行命令，经 03 平台重跑）, `PLATFORM_BUILTIN`（平台内置，如字数、名录比对）, `AGENT_JUDGE`（由内部 Agent 按 rubric 判断，跨模型家族）, `HUMAN_FORM` |
| `VerdictLevel` | `PASS`, `WARN`, `FAIL`, `INCONCLUSIVE` |
| `CalibrationState` | `UNCALIBRATED`, `CALIBRATING`, `CALIBRATED`, `DRIFTED` |
| `RubricDimensionKind` | `HARD_GATE`（一票否决）, `WEIGHTED` |
| `EvalCaseOrigin` | `SEED`（模板自带）, `ESCAPE`（穿透自动入集）, `MANUAL`（人工添加） |
| `CalibrationMethod` | `AGREEMENT`（模型打分验证器：与人工标注的一致率）, `PROBE`（确定性采集 / 检测工具：已知样例探针，D116） |
| `EvalCaseKind` | `JUDGEMENT`（判定用例，默认）, `TOOL_PROBE`（工具探针：已知正确 / 错误的样例页及其必然结论） |
| `ProviderKind` | `VIDEO_GEN`, `IMAGE_GEN`, `TTS`, `MUSIC_GEN`, `ASR_ALIGN`, `VLM_JUDGE`, `LLM_JUDGE` |
| `UpgradePolicy` | `PINNED`（默认）, `AUTO_PATCH`（仅自动接受补丁版本） |

### 3.2 `templates_templates`（模板，聚合根）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| id | ID | N | — | PK |
| tenant_id | ID | N | — | 一方模板为 0 |
| key | STR(64) | N | — | 如 `software-dev`、`video-production`；`(tenant_id, key)` 唯一 |
| domain | ENUM TemplateDomain | N | — | |
| origin | ENUM TemplateOrigin | N | `FIRST_PARTY` | |
| name / description | STR(120) / TEXT | N / Y | — | 显示名走 i18n key（一方模板） |
| team_modes | JSON | N | — | 适用的 TeamMode 数组；`team-tasks` 含 `HUMAN_ONLY` |
| based_on_template_id | ID | Y | NULL | 团队模板的来源 |
| state | ENUM TemplateState | N | `DRAFT` | |
| latest_version | STR(16) | Y | NULL | 最新已发布版本号 |
| version | INT | N | 1 | 乐观锁 |

### 3.3 `templates_versions`（模板版本，发布后不可变）

| 列 | 类型 | 空 | 默认 | 约束 / 说明 |
|---|---|---|---|---|
| id | ID | N | — | PK |
| tenant_id | ID | N | — | |
| template_id | ID | N | — | |
| semver | STR(16) | N | — | `MAJOR.MINOR.PATCH`；`(template_id, semver)` 唯一 |
| definition | JSON | N | — | 完整模板定义（§3.10 JSON Schema）；发布后只读 |
| definition_sha256 | STR(64) | N | — | 定义内容哈希，项目锁定以它为准 |
| changelog | TEXT | Y | NULL | |
| breaking | BOOL | N | false | MAJOR 变更必须为 true |
| published_at / published_by | TIME / ID | Y | NULL | |
| state | ENUM TemplateState | N | `DRAFT` | `PUBLISHED` 后定义不可改 |

### 3.4 `templates_project_bindings`（项目锁定的模板版本）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | — | PK |
| tenant_id / project_id | ID | N | — | `(tenant_id, project_id)` 唯一 |
| template_version_id | ID | N | — | |
| definition_sha256 | STR(64) | N | — | 冗余，便于核对 |
| upgrade_policy | ENUM UpgradePolicy | N | `PINNED` | |
| brief_overrides | JSON | N | `{}` | 甲方 Brief 覆盖的参数（§5.3），按 `verifier_key.param` 键 |
| overrides_approved_by / at | ID / TIME | Y | NULL | 覆盖须交付负责人确认（人类） |
| version | INT | N | 1 | |

### 3.5 `templates_verifier_calibrations`（模型打分验证器校准）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | — | PK |
| tenant_id | ID | N | — | 团队级校准（同一一方模板在不同团队可不同） |
| template_key / verifier_key | STR(64) / STR(64) | N | — | |
| provider_ref | STR(120) | N | — | 评分模型或工具与版本，如 `arcface:buffalo_l@0.7.3`、`vlm:<model>@<ver>`、`browser:edge@141`（取自 04 S4 `variant` / `version` 主版本） |
| method | ENUM CalibrationMethod | N | `AGREEMENT` | `PROBE` 用于 `DETERMINISTIC` 采集 / 检测验证器（BR-11-33） |
| machine_id | ID | Y | NULL | 仅 `PROBE`：工具结果与具体机器的安装、字体、显示参数相关，按机器校准 |
| last_failures | JSON | Y | NULL | 仅 `PROBE`：最近一次未通过的探针 `[{case_id, expected, actual, evidence_refs}]` |
| state | ENUM CalibrationState | N | `UNCALIBRATED` | |
| threshold | JSON | Y | NULL | 标定后的阈值（覆盖模板默认） |
| agreement | DEC | Y | NULL | 与人工标注的一致率（Cohen κ 或准确率） |
| sample_size | INT | N | 0 | |
| calibrated_at / by | TIME / ID | Y | NULL | 人类确认 |
| drift_checked_at | TIME | Y | NULL | |
| version | INT | N | 1 | |

唯一：`(tenant_id, template_key, verifier_key, provider_ref, machine_id)`（`machine_id` 为空视为同一值）。

### 3.6 `templates_eval_cases`（评测集）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | — | PK |
| tenant_id | ID | N | — | |
| template_key | STR(64) | N | — | |
| origin | ENUM EvalCaseOrigin | N | — | |
| kind | ENUM EvalCaseKind | N | `JUDGEMENT` | `TOOL_PROBE` 用例只用于 `PROBE` 校准与内部 Agent 配置的误报检查（BR-11-34），不计入 BR-11-21 的 20–50 条判定用例 |
| title | STR(200) | N | — | |
| input_ref | JSON | N | — | 输入（需求片段、产物文件 ID、截图 ID 等，经 files ResourceBinding） |
| expected | JSON | N | — | 期望结论：`{verifier_key → VerdictLevel}` 与 / 或 `{rubric_dimension → score_range}` |
| source_escape_id | ID | Y | NULL | 来自穿透事件（`quality.escaped`）时必填 |
| active | BOOL | N | true | |
| version | INT | N | 1 | |

### 3.7 `templates_eval_runs`（评测运行）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | — | PK |
| tenant_id / template_key | ID / STR(64) | N | — | |
| template_version_id | ID | N | — | |
| target | JSON | N | — | 被测对象：验证器 + provider_ref，或内部 Agent 配置（角色 + 模型） |
| cases_total / passed / failed | INT | N | 0 | |
| agreement | DEC | Y | NULL | 与期望的一致率 |
| report_file_id | ID | Y | NULL | 明细报告 |
| started_at / finished_at | TIME | Y | NULL | |
| state | STR(16) | N | `RUNNING` | RUNNING / COMPLETED / FAILED |

### 3.8 `templates_providers`（生成器 / 评委适配器配置）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | — | PK |
| tenant_id | ID | N | — | |
| kind | ENUM ProviderKind | N | — | |
| adapter_key | STR(64) | N | — | 适配器实现，如 `veo`、`kling`、`runway`、`comfyui-wan`、`comfyui-hunyuan`、`whisperx`、`openai-compatible-vlm` |
| display_name | STR(120) | N | — | |
| config | JSON | N | `{}` | 端点、模型、默认参数；**密钥经 09 `SecretSealer` 封存**，此处只存密钥引用 |
| machine_id | ID | Y | NULL | 本地部署（ComfyUI 等）时运行所在机器 |
| state | STR(16) | N | `ENABLED` | ENABLED / DISABLED / UNAVAILABLE（健康检查失败） |
| health_checked_at | TIME | Y | NULL | |
| version | INT | N | 1 | |

### 3.9 `templates_rubric_scores`（评分记录，内部验收与甲方验收共用）

| 列 | 类型 | 空 | 默认 | 说明 |
|---|---|---|---|---|
| id | ID | N | — | PK |
| tenant_id / project_id | ID | N | — | |
| subject_kind | STR(16) | N | — | `WORK_ITEM` / `DELIVERY` |
| subject_id | ID | N | — | |
| stage | ENUM VerificationStage | N | — | `AGENT_ACCEPTANCE` / `HUMAN_REVIEW` / `CLIENT_ACCEPTANCE` |
| scorer_kind / scorer_id | STR(16) / ID | N | — | AGENT / HUMAN / CLIENT |
| template_version_id | ID | N | — | 评分所用版本 |
| scores | JSON | N | — | `[{dimension_key, score(1–5)|pass_fail, evidence_refs[], note}]` |
| total | DEC | Y | NULL | 加权总分（硬门槛失败时为 NULL） |
| passed | BOOL | N | — | 按 §5.5 通过规则 |
| created_at | TIME | N | — | 只追加 |

### 3.10 模板定义 `definition` JSON Schema（摘要）

```json
{
  "key": "video-production", "domain": "VIDEO", "semver": "1.0.0",
  "parameters": { "<param_key>": { "type": "number|string|bool|enum", "default": "...", "brief_overridable": true, "unit": "LUFS" } },
  "stages": [ { "key": "A1_SCRIPT", "name_i18n": "...", "order": 1,
      "work_items": [ { "key": "script", "type": "CONTENT", "title_i18n": "...", "role": "EXECUTOR",
          "depends_on": ["brief"], "produces": ["script"], "consumes": ["brief"],
          "acceptance_criteria_templates": [ { "id": "AC-SCRIPT-1", "ears": "当……时，脚本应……" } ],
          "verification_plan": { "AC-SCRIPT-1": "AGENT_JUDGE" },
          "autonomy_level": "MEDIUM", "plan_review": true } ],
      "human_gates": [ { "key": "client_script_signoff", "kind": "CLIENT", "after": "script", "mandatory": true } ] } ],
  "artifacts": { "<artifact_type>": { "schema": { "$ref": "..." }, "file_kinds": ["md","json","mp4"], "evidence_role": "PRIMARY|SUPPORT" } },
  "verifiers": [ { "key": "loudness", "kind": "DETERMINISTIC", "runner": "MACHINE_COMMAND",
      "applies_to": ["master"], "command": ["ffmpeg", "-i", "{input}", "-af", "ebur128=peak=true", "-f", "null", "-"],
      "parse": "ebur128", "params": { "target_lufs": "{param.target_lufs}", "tolerance_lu": 0.5, "true_peak_max_dbtp": -1 },
      "verdict": { "FAIL": "abs(I - target_lufs) > tolerance_lu || TP > true_peak_max_dbtp" },
      "calibration": null, "needs_verification_against_official": true } ],
  "rubric": { "dimensions": [ { "key": "brief_fit", "kind": "WEIGHTED", "weight": 0.25, "guide_i18n": "..." },
                              { "key": "tech_spec", "kind": "HARD_GATE", "verifiers": ["ffprobe_spec","loudness","subtitle_rules"] } ],
              "pass_rule": { "hard_gates": "all_pass", "weighted_min": 3.5 } },
  "internal_qa": { "max_internal_rounds": 5,
      "tester": { "checks": ["..."], "required_verifiers": ["..."] },
      "acceptor": { "uses_rubric": true, "inputs": ["master","qc_report","shot_list"], "must_differ_family_from": "EXECUTOR" } },
  "providers": { "VIDEO_GEN": { "required": true, "candidates": ["veo","kling","runway","comfyui-wan"] } },
  "eval_set": { "min_cases": 20, "max_cases": 50, "calibration_interval_days": 30 },
  "constitution_seed": "项目宪法初始内容（构建 / 测试命令、约束、禁区、风格）"
}
```

### 3.11 外部引用（只存 ID）

项目（07 Entity）、工作项与验收标准（02）、执行 / 证据 / 验证（03）、机器（04）、Agent（05）、文件（底座 files，经 ResourceBinding，purpose `template_eval` / `verifier_evidence`）、穿透事件（`quality.escaped`，发布者 02 / 07 / 12 Part A；本模块只经 02 `delivery.escape_learning` 调用 `EvalSetWriter.ProposeCase` 接收，D114）。

---

## 4. 状态机

### 4.1 模板版本 `TemplateState`

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| T1 | — | 创建草稿（团队模板）或系统初始化（一方） | `template_manage`（团队模板） | DRAFT | — |
| T2 | DRAFT | 发布 | 定义通过 JSON Schema 与静态检查（§5.1）；评测集 ≥ `min_cases` 或标记「评测不足」并由人确认 | PUBLISHED | `definition_sha256` 固化；`template.version_published`；审计 |
| T3 | PUBLISHED | 弃用 | 有更高版本 | DEPRECATED | 新项目不可选；已锁定项目收到升级提示 |
| T4 | DEPRECATED | 退役 | 无项目锁定该版本 | RETIRED | 只读归档 |

非法迁移：对 PUBLISHED 修改定义 → 409 **185410**；退役仍被锁定的版本 → 409 **185411**；跳过 DRAFT 直接退役 → 409 **185412**。

### 4.2 校准 `CalibrationState`

| # | 当前 | 触发 | 守卫 | 目标 | 副作用 |
|---|---|---|---|---|---|
| C1 | UNCALIBRATED | 开始校准 | 评测集该验证器相关用例 ≥ 20（人工标注） | CALIBRATING | 运行评测（§7 J2） |
| C2 | CALIBRATING | 评测完成且一致率 ≥ 目标（默认 κ ≥ 0.6 或准确率 ≥ 0.85）且人类确认 | HUMAN | CALIBRATED | 阈值写入 `threshold`；该验证器结论可参与内部验收判定 |
| C3 | CALIBRATING | 一致率不足 | — | UNCALIBRATED | 记录原因；仍只出建议 |
| C4 | CALIBRATED | 周期复核失败或新增穿透用例导致一致率跌破目标 | — | DRIFTED | 结论降级为建议；通知质量负责人 |
| C5 | DRIFTED | 重新校准通过 | HUMAN | CALIBRATED | — |

**`PROBE` 方法的迁移**（D116，BR-11-33）：C1 的守卫改为「该验证器的 `TOOL_PROBE` 用例 ≥ 模板要求」，触发为自动（BR-11-35）；C2 不需要人类确认——探针结果是确定性的，**全部探针结论与期望一致**即 CALIBRATED；任一不一致走 C3 / C4（CALIBRATED → DRIFTED），写 `last_failures` 并通知质量负责人；C5 同样自动。人类可在 C1–C5 任何时候手动发起重跑（C2 接口）。

**规则**：`MODEL_SCORED` 验证器在非 CALIBRATED 状态时，结论只能是「建议」，不能单独让工作项通过或拒绝内部验收；产品验收 Agent 汇总时把这类结论作为参考证据，最终结论须由确定性检查与 rubric 维度共同支持（§5.4）。

### 4.3 项目绑定升级

| # | 当前 | 触发 | 守卫 | 目标 |
|---|---|---|---|---|
| U1 | 锁定 vX | 发布 vY（同 MAJOR） | `upgrade_policy=AUTO_PATCH` 且仅 PATCH 变化 | 自动锁定 vY；通知 |
| U2 | 锁定 vX | 人工升级 | `project_manage`；HUMAN；展示定义 diff 与受影响的进行中工作项 | 锁定 vY；进行中工作项继续按 vX 验证，新工作项用 vY |
| U3 | 锁定 vX | 升级到不同 MAJOR | 同 U2，且须确认「验收标准可能变化」 | 锁定 vY；已冻结的验收集不变（03） |

---

## 5. 业务规则与边界情况

### 5.1 模板定义与静态检查

- **BR-11-01 定义校验**：发布前校验 JSON Schema；工作项依赖成环 → 422 **185420**；`produces` 产物类型未在 `artifacts` 定义 → 422 **185421**；验证器 `applies_to` 引用未知产物 → 422 **185422**；rubric 权重和 ≠ 1（±0.001）→ 422 **185423**；`HARD_GATE` 维度必须至少关联一个 `DETERMINISTIC` 或 `HUMAN` 验证器（不能只靠模型打分）→ 422 **185424**。
- **BR-11-02 内核中立**：模板只能引用 00 定义的共享枚举与 02 的通用字段；领域词汇只出现在模板定义与 i18n 文案中，不进内核表结构（PRD P7）。
- **BR-11-03 人工关口不可删除**：一方模板标记 `mandatory: true` 的人工关口，团队自定义模板不得删除，只能增加（v1.1 团队模板校验）。
- **BR-11-04 每条验收标准都有验证方式**：模板生成的工作项必须为每条验收标准提供 `verification_plan`（00 §15.3），否则无法通过就绪闸门。

### 5.2 参数与 Brief 覆盖

- **BR-11-05 覆盖范围**：只有 `brief_overridable: true` 的参数可被甲方 Brief 覆盖；覆盖值必须在参数允许范围内（如响度目标只能取模板列出的平台值或 −30…−10 LUFS）→ 否则 422 **185430**。
- **BR-11-06 覆盖须人确认**：覆盖由交付负责人（人类）确认后生效（`overrides_approved_by`）；未确认的覆盖不参与验证。
- **BR-11-07 覆盖不回溯**：已冻结的验收集与已完成的验证不受后续覆盖影响；覆盖变化写审计并在内部验收证据中注明参数版本。

### 5.3 验证器插件协议

- **BR-11-08 统一输出**：每个验证器输出 `{verifier_key, kind, runner, params_resolved, verdict: VerdictLevel, metrics{}, evidence_refs[], limitations, tool_version, duration_ms}`；`limitations` 必填（如「截图仅证明渲染，不证明交互」）。
- **BR-11-09 确定性验证器**：由 03 在隔离环境**平台重跑**（不采信执行者日志）；工具缺失（04 检测不到 ffmpeg 等）→ `INCONCLUSIVE`（`TOOL_UNAVAILABLE`），不得当作通过。
- **BR-11-10 模型打分验证器**：评分模型必须与执行者**不同模型家族**；未校准时只出建议（§4.2）；结论必须附逐条证据引用（原文片段、帧号、时间码）。
- **BR-11-11 人工验证器**：以表单收集（签字人、范围、结论、附件），只能由人类完成；Agent 不能代填。
- **BR-11-12 证据可追溯**：所有验证器读取的输入以文件 ID + 内容哈希引用，结果写 03 证据；同一输入哈希的重复验证可复用结果（同一 `tool_version` 与参数）。

### 5.4 内部质量门（D113）在模板中的配置

- **BR-11-13 测试 Agent**：执行 `internal_qa.tester.checks` 列出的检查（各模板 §5.7–§5.10），补充测试或检查脚本，但**不得修改冻结的验收集**（03）；发现问题输出结构化缺陷（类别、位置、期望、证据）。
- **BR-11-14 产品验收 Agent**：按模板 rubric 逐维打分（`templates_rubric_scores`，stage = AGENT_ACCEPTANCE），检查实际交付形态（运行程序、截图、成片、全书）；硬门槛只能由确定性或人工验证器的结论支撑；`passed=false` → 内部返工（02 `REWORK`），反馈 = 低分维度的说明与证据。
- **BR-11-15 内部通过条件**：确定性验证器全部 PASS（WARN 须测试 Agent 说明可接受理由）+ 测试 Agent 无未关闭的 HIGH 缺陷 + 产品验收 Agent `passed=true` → L2 内部验收通过。
- **BR-11-16 轮次**：`max_internal_rounds` 默认 5（各模板可设 3–8）；用尽 → 02 升级 `INTERNAL_ROUNDS_EXHAUSTED`。
- **BR-11-17 必经人工关口**：模板 `human_gates[].mandatory=true` 的关口（如视频脚本甲方确认、小说大纲甲方确认）不受「不穿透」限制，按模板顺序到达人或甲方；这是计划中的关口，不是问题升级。

### 5.5 评分标准（rubric）通过规则

- **BR-11-18 计分**：WEIGHTED 维度 1–5 分，总分 = Σ(权重 × 分数)；HARD_GATE 维度只有通过 / 不通过，任一不通过 → `passed=false`，`total=NULL`。
- **BR-11-19 同一把尺子**：产品验收 Agent、团队人工验收（L3）、甲方验收（L4）使用**同一模板版本的同一份 rubric**；三者评分并列展示，差异 ≥ 1.0 分的维度高亮为「内部与人工 / 甲方判断分歧」，记入校准数据。
- **BR-11-20 默认通过线**：`weighted_min = 3.5`（模板可调 3.0–4.5，甲方 Brief 可提高不可降低）。

### 5.6 评测集与穿透回流

- **BR-11-21 规模**：每个已发布模板评测集 20–50 条活跃用例（`SEED` 随模板发布）；不足 20 条时模板详情显示「评测不足」，模型打分验证器不能进入 CALIBRATED。
- **BR-11-22 穿透自动入集**：02 的 `delivery.escape_learning`（BR-02-58）在处理 `quality.escaped` 时调用 `EvalSetWriter.ProposeCase(source=ESCAPE)` → 创建 `ESCAPE` 用例（本模块不直接消费 `quality.escaped`，避免重复入集）（输入 = 当时的产物与上下文快照，期望 = 人工判定），并触发相关验证器与内部 Agent 配置的评测（J2）；超过 50 条时淘汰最旧的 SEED 用例（ESCAPE 用例保留）。
- **BR-11-23 校准周期**：每 30 天（模板参数）或新增 ≥ 5 条 ESCAPE 用例时重跑评测；一致率跌破目标 → DRIFTED。
- **BR-11-24 隐私**：评测用例只引用文件 ID；跨项目复用用例前须对甲方内容脱敏或取得授权（项目设置开关，默认仅本项目可见）。

### 5.6b 工具校准（D116）

确定性验证器同样依赖工具：浏览器、截图、DOM 检测、媒体探测。工具出错时，确定性检查会**稳定地给出错误结论**；内部质量门越可靠、越不经人，这类误报的代价越大（浪费内部轮次、错误升级给人）。2048 实测中，无头浏览器窗口窄于约 500px 时按更宽的最小宽度排版再裁切，导致「窄视口被裁切」误报两轮并错误升级（研究报告 §5.1）。

- **BR-11-33 适用范围**：模板中 `kind=DETERMINISTIC` 且属于采集或检测类的验证器（SOFTWARE：`ui_screenshot`、`ui_layout_rules`、`a11y_basic`；VIDEO：规格 / 黑场 / 冻帧 / 响度检测；NOVEL：字数与重复率统计）必须声明 `TOOL_PROBE` 用例并按 `method=PROBE`、按机器校准。纯命令类验证器（构建、单元测试、冻结验收集）不需要——其结论由命令退出码决定，已由平台重跑保证（03 BR-03-95）。
- **BR-11-34 探针内容**：每个探针是一个已知正确或已知错误的样例输入及其必然结论（`expected` 含 `{verifier_key → VerdictLevel}` 与可选的必有 / 必无发现）。每个须校准的验证器至少一个「已知正确」探针（防误报）和一个「已知错误」探针（防漏报）。同一组截图类探针还用于 J2 评测内部 Agent 配置（`target` = ACCEPTOR 角色 + 模型）：产品验收 Agent 对「已知正确」探针提出 BLOCKER / MAJOR 发现即记误报，对「已知错误」探针没有发现即记漏报，计入该配置的 EvalRun，在模板详情「评测集」与 Agent 详情展示（不自动改变 05 派单评分）。
- **BR-11-35 何时校准**：①机器首次上报具备相关能力（04 `browser_capture` 等）；②04 `machine.runtimes_changed` 中相关工具的 `variant` / 主版本变化（同时把该机器旧行置 DRIFTED）；③J3 周期复核（`calibration_interval_days`，默认 30 天）；④人工发起。校准作为 J2 任务在**该机器上**以平台身份执行，不占用工作项执行槽位，超时 10 分钟记失败。
- **BR-11-36 生效**：03 执行 `VISUAL` / `INTERACTION` 等须校准的验证器前，经 `VerifierCatalog.ToolCalibration` 查询目标机器状态；只有 CALIBRATED 的机器上的结论计入判定，规则见 03 BR-03-103。探针用例随一方模板发布（`origin=SEED`、`tenant_id=0`）；团队可追加 `MANUAL` 探针，不可删除 SEED 探针。

### 5.7 SOFTWARE 模板 `software-dev`（v1.0.0）

- **阶段**：S0 需求澄清与需求评审（12 评审单）→ S1 设计（架构 / 接口 / UI 结构契约）→ S2 计划与验收集冻结（含隐藏验收部分）→ S3 实现（并行，工作项类型按 12：需求 / 任务 / 缺陷）→ S4 内部质量门 → S5 团队验收 → S6 交付。
- **人工关口**：需求评审（人类，必经）、架构评审（中高风险必经）、交付（人类，不可委托）。
- **产物**：`requirement_doc`、`design_doc`、`ui_contract`（组件树、语义化选择器、状态与交互清单）、`source_change`（commit / MR）、`test_report`、`screenshot_set`、`build_artifact`、`release_notes`。
- **确定性验证器**：构建、单元测试、lint、类型检查（命令取自项目宪法）；**冻结验收集**（可见 + 隐藏）；测试 diff 检测（改动冻结测试 → `cheating.suspected`）；覆盖率阈值（参数，默认 70%）；依赖与密钥扫描。
- **视觉 / 交互验证器（UI 类工作项必选）**：
  - `ui_screenshot`（DETERMINISTIC 采集）：在无头浏览器打开交付形态（按项目宪法的启动方式；静态页面同时以 `file://` 与本地 HTTP 两种方式），按 `ui_contract` 的视口（375 / 768 / 1440）截图并记录控制台错误；控制台错误 → FAIL。
  - `ui_layout_rules`（DETERMINISTIC）：DOM 检查规则（如网格单元同尺寸、无横向滚动、关键元素可见、焦点可达）。
  - `ui_visual_judge`（MODEL_SCORED，需校准）：VLM 对照 `ui_contract` 与截图判断布局一致性、对齐、明显视觉缺陷，给出带坐标的证据。
  - `a11y_basic`（DETERMINISTIC）：对比度、可访问名称、键盘可达。
- **工具探针（`TOOL_PROBE`，BR-11-34）**：
  - `probe_ok_page`：布局正确、无控制台错误的页面 → 所有 UI 验证器 PASS、无发现。
  - `probe_viewport_fidelity`：页面把 `innerWidth × innerHeight` 写入可见元素并在四边放置定位标记 → 每个视口的截图像素尺寸与页面自报尺寸都等于请求的视口，四个标记都可见（窄于浏览器最小窗口宽度的视口也必须真实渲染，2048 误报即属此类）。
  - `probe_uneven_rows`：只定义列模板的网格，有内容的行更高 → `ui_layout_rules` FAIL（网格单元不同尺寸）。
  - `probe_horizontal_overflow`：375 宽下有固定 480px 元素 → FAIL（横向滚动）。
  - `probe_console_error`：加载时抛出异常 → `ui_screenshot` FAIL。
  - `probe_file_module`：用模块脚本加载同目录逻辑 → `file://` 方式 FAIL（未初始化），本地 HTTP 方式 PASS。
  - `probe_low_contrast` / `probe_unreachable_focus` → `a11y_basic` FAIL。
- **测试 Agent 检查**：补充边界测试；运行交付形态（不仅是模块测试）；探索式操作关键流程并截图；检查同一逻辑是否存在多份实现（重复实现 → 需一致性测试）。
- **产品验收 Agent 检查**：逐条验收标准对照证据；以甲方视角运行与截图；检查需求中的交付形态要求（如「直接打开即可运行」）是否满足。
- **rubric**：功能符合验收标准（HARD_GATE，验收集全部通过）、交付形态可用（HARD_GATE，启动与截图无错误）、UI / 交互质量（WEIGHTED 0.25）、代码质量与可维护性（0.20）、测试充分性（0.20）、文档与交付说明（0.15）、需求理解（0.20）；通过线 3.5。
- **默认 `max_internal_rounds`**：5。

> 2048 实测（研究报告 §5）中漏掉的「棋盘行高不一致」将被 `ui_layout_rules` 与 `ui_visual_judge` 覆盖；「逻辑两份副本」将被测试 Agent 的重复实现检查覆盖。

### 5.8 VIDEO 模板 `video-production`（v1.0.0，阈值需复核与校准）

- **阶段**：A0 Brief（目标、受众、时长、画幅、平台、品牌规范、禁用元素）→ A1 脚本（**甲方确认，必经**）→ A2 分镜 / 镜头表 → A3 设定资产（角色定妆，**人工挑选**）→ A4 镜头生成（每镜 N 个候选，参数 `takes_per_shot` 默认 3）→ A5 剪辑合成 → A6 声音与字幕 → A7 QC（内部质量门）→ A8 交付（多平台版本）。
- **产物**：`brief`（JSON）、`script`（md）、`shot_list`（每镜：id、时长、景别、运镜、角色 ID、提示词、参考图、对白）、`character_sheet`（参考图、描述、声纹）、`shot_take`（候选片段 + provider + 模型版本 + seed + 参数）、`edit_timeline`（OTIO / EDL）、`audio_mix`、`subtitle`（srt / vtt）、`master`（mp4）、`deliverable_variant`、`qc_report`、`provenance`（模型、提示词、C2PA 清单）。
- **确定性验证器**：
  - `ffprobe_spec`：分辨率、fps、编码、色彩空间、画幅、时长与 Brief 一致；时长容差 ±0.5 s 或 ±2%（取较大）。
  - `av_qc`：单次解码运行 `blackdetect=d=0.5:pic_th=0.98:pix_th=0.10`、`freezedetect=n=-60dB:d=2`、`silencedetect=n=-50dB:d=2`；片头片尾黑场可配置放行；中段命中 → FAIL（需复核）。
  - `loudness`：按 Brief 目标平台选阈值：广播 EBU R128 −23 LUFS ±0.5 LU、True Peak ≤ −1 dBTP；北美广播 −24 LKFS；流媒体约 −14 LUFS（Apple 约 −16）（需复核）。
  - `subtitle_rules`：每秒字符数（英文成人 ≤ 17、儿童 ≤ 15）、每行 ≤ 42 字符、≤ 2 行、单条时长与间隔（需复核 Netflix 官方模板）；中文按 Brief 参数。
  - `subtitle_alignment`：WhisperX / MFA 强制对齐，字幕起止与词级时间戳偏差 ≤ 200 ms；字幕文本与脚本 WER / CER 阈值（参数）。
  - `shot_cut_match`：PySceneDetect / `scdet` 切点数量与镜头表一致，单镜时长偏差 ≤ 0.3 s（参数）。
- **模型打分验证器（需校准）**：
  - `face_consistency`：ArcFace 与角色参考图余弦相似度；初值「均值 ≥ 0.5 且低于 0.4 的帧占比 < 10%」。
  - `vbench_subset`：subject / background consistency、motion smoothness、aesthetic / imaging quality；**dynamic_degree 下限守卫**（防止「几乎不动」获得高一致性）。
  - `shot_vs_storyboard`：每镜抽 3–5 帧 + 镜头表条目交给 VLM，判断角色、场景、动作、景别是否符合，必须给出帧号证据。
  - `brand_safety`：Logo、违禁元素、乱码文字、肢体畸变。
- **人工验证器（必须人工）**：创意方向与表演、角色「像不像」的最终判断、肖像权与音乐素材版权、敏感题材、AI 披露决定（平台规则需复核）。
- **测试 Agent 检查**：跑全部确定性验证器；逐镜对照镜头表；检查候选片段是否有替换历史与 provider 记录；音画同步抽检。
- **产品验收 Agent 检查**：按 rubric 观看成片（抽帧 + 字幕 + 音频摘要）逐维评分；核对 Brief 必须信息与 CTA。
- **rubric**：技术规格（HARD_GATE：`ffprobe_spec`、`av_qc`、`loudness`、`subtitle_rules`）、品牌合规（HARD_GATE：人工 + `brand_safety` 建议）、Brief 符合度（0.30）、叙事与节奏（0.20）、角色与场景一致性（0.20）、画面质量（0.20）、声音与字幕（0.10）；加权维度权重和为 1；通过线 3.5。
- **重试与预算**：单镜重新生成上限 `max_takes_per_shot`（默认 6），超出升级给人（改分镜或放宽要求）；生成成本计入执行成本。
- **生成器**：`VIDEO_GEN` 必需，候选适配器 `veo`、`kling`、`runway`、`comfyui-wan`、`comfyui-hunyuan`、`ltx`（许可条款需复核）；不得硬绑定；provider 不可用 → 工作项 `BLOCKED`（`PROVIDER_UNAVAILABLE`）并提示切换。

### 5.9 NOVEL 模板 `novel-writing`（v1.0.0，阈值需复核与校准）

- **阶段**：B0 Premise（一句话 → 一段话梗概）→ B1 世界观圣经 → B2 角色卡 → B3 大纲（Save the Cat 15 节拍或雪花法 → 事件级大纲，**甲方确认，必经**）→ B4 章节计划（事件、视角、伏笔埋设 / 回收、字数目标）→ B5 逐章起草（每章后更新故事状态）→ B6 全书连续性编辑 → B7 行文编辑（**人工必经**）→ B8 交付。
- **产物**：`premise`、`story_bible`（条目可链接，正文提及时自动注入上下文）、`character_sheet`、`beat_sheet`、`event_outline`、`chapter_plan`、`chapter_draft`、`story_state`（每章快照：已发生事实、时间线、人物位置与状态、伏笔表）、`foreshadow_ledger`、`consistency_report`、`editorial_notes`、`manuscript`。
- **确定性验证器**：
  - `word_count`：每章在目标 ±15%。
  - `name_registry`：命名实体识别结果对照圣经名录，未登记人名地名与拼写变体 → WARN（未处理则 FAIL）。
  - `repetition`：章内与跨章 8-gram 重复率 < 2%；句首与惯用语高频统计超阈值 → WARN。
  - `beat_position`：各节拍在全书位置（Catalyst ≈ 10%、Midpoint 50%、All is Lost 75% 等）±3%。
  - `foreshadow_closure`：全书结束时所有伏笔已回收或标「有意保留」。
- **模型打分验证器（需校准）**：
  - `continuity_check`：按 ConStory 式 5 大类 19 子类（时间线与情节逻辑、人物塑造、世界观与设定、事实与细节、叙事与文风）抽取 → 配对矛盾 → 证据链，每条问题附原文引用；计量每万词错误数 CED，初值 ≤ 0.5 且无未解释的严重矛盾；**全书检查，不抽样**。
  - `outline_adherence`：本章是否完成章节计划中的每个事件，附引用。
  - `style_consistency`：对照风格样章打分，或文体特征（句长分布、对白占比）距离。
  - `reader_rubric`：多评委或多次采样取中位数。
- **人工验证器（必须人工）**：主题与情感效果、人物是否立得住、结局、敏感内容、抄袭与版权最终判断（查重工具只提供证据）、署名与 AI 使用披露。
- **测试 Agent 检查**：跑确定性验证器；全书连续性检查并维护故事状态一致；伏笔台账核对。
- **产品验收 Agent 检查**：按 rubric 通读（分段汇总 + 关键章节全文）逐维评分；核对 premise 与大纲符合度。
- **rubric**：原创性（HARD_GATE：查重通过 + 人工判断）、格式与字数（HARD_GATE：`word_count`）、大纲与 premise 符合度（0.25）、连续性（0.20）、人物塑造与弧光（0.20）、情节张力与节奏（0.15）、文风一致与语言质量（0.20）；加权维度权重和为 1；通过线 3.5。
- **上下文管理**：章节起草时注入：章节计划、相关圣经条目（被提及即注入）、上一章故事状态、未回收伏笔；遵循 03 上下文预算与 06 检索。

### 5.10 TEAM_TASKS 模板 `team-tasks`（v1.0.0，适用 HUMAN_ONLY）

- **阶段**：需求收集 → 分诊 → 拆解 → 排期与指派 → 执行 → 状态同步 → 风险管理 → 交付与签收 → 复盘。
- **AI 助理（`ASSISTANT` 角色）只出草稿与建议**：分诊建议（团队、负责人、标签、疑似重复，附推理）、拆解草稿、会议纪要 → 行动项草稿（无负责人或截止日期的行动项不得转为任务，回推给主持人）、状态与报告草稿（**必须附来源链接**）、风险探测（逾期趋势、无人认领、依赖冲突）、报表核对提示。人负责范围、验收标准、优先级、对外口径、签收。
- **产物**：`request`、`task`（含验收清单）、`meeting_note`（转写链接 + 行动项）、`status_report`、`risk_register`、`deliverable_link`（URL + 版本 / 修改时间）、`checklist_completion`、`sign_off`（签收人、时间、范围）、`retro_note`。
- **确定性验证器**：验收清单 100% 勾选；交付物链接可访问且版本 / 修改时间晚于任务开始；需要签收的任务有指定角色的 `sign_off`；状态报告中引用的任务 ID 存在且状态与系统一致。
- **模型打分验证器（需校准，仅建议）**：行动项抽取召回（人工抽查 10% 会议）、重复判定、状态草稿与原始数据一致性、交付物与验收标准符合度预审（逐条证据）。
- **内部质量门**：HUMAN_ONLY 模式下没有测试 / 验收 Agent；内部质量门由确定性检查 + **同组另一名人类的互审**（参数，默认对风险 ≥ MEDIUM 的任务开启）组成；AI 助理可做预审提示，不能给出通过结论。
- **rubric**：签收证据齐全（HARD_GATE）、交付物完整（0.30）、质量（评审意见已关闭，0.25）、按期（0.20）、沟通透明（状态报告与实际一致，0.25）；加权维度权重和为 1；通过线 3.5。

### 5.11 生成器适配器

- **BR-11-25 适配器接口**：`Generate(ctx, request) → {job_id}`、`Poll(job_id) → {state, outputs[file_ref], cost, model, model_version, seed?}`、`Cancel(job_id)`、`Health()`；所有调用经 03 执行记录与成本统计。
- **BR-11-26 不硬绑定**：模板只声明需要的 `ProviderKind` 与候选列表；项目在设置中选择具体 provider；provider 健康检查失败 → `UNAVAILABLE`，相关工作项 `BLOCKED`（原因 `PROVIDER_UNAVAILABLE`），提示切换；切换 provider 不改变已生成产物的 provenance。
- **BR-11-27 密钥**：provider 密钥经 09 `SecretSealer` 封存，仅在执行时注入执行环境的 allowlist 变量，不写日志、不进证据。
- **BR-11-28 许可与地区**：provider 配置页显示许可与地区限制提示（如部分开源模型的地区或营收限制，需复核），由团队所有者确认后启用。

### 5.12 边界

- **BR-11-29 模板与项目不一致**：项目已锁定模板版本，但工作项由人手工创建、未来自模板 → 仍须满足 DoR；其验证计划可引用模板验证器。
- **BR-11-30 工具不可用**：确定性验证器所需工具在所有在线机器上都不可用 → 工作项不能进入内部质量门，`BLOCKED`（`TOOL_UNAVAILABLE`），通知团队所有者安装；不得跳过。
- **BR-11-31 超大产物**：视频母版 > 4 GB 或全书 > 100 万字时，验证器分段运行并合并结果；分段边界在报告中注明。
- **BR-11-32 Brief 冲突**：Brief 覆盖导致两个硬门槛互相矛盾（如时长与镜头表总时长不可兼得）→ 需求质量检查报 `REQUIREMENT_CONFLICT`（02 升级给交付负责人）。

---

## 6. 接口契约

约定：响应 `respx` 信封；GET 返回 `ETag`，更新带 `If-Match`（缺失 428 **185428**，冲突 409 **185409**）；创建 / 命令型 POST 支持 `Idempotency-Key`（平台统一幂等：同 key 不同请求 422 **176425**，进行中 409 **176409**）；OpenAPI tag = 最终发布路径（含挂载前缀）首段；权限拒绝 403 **185403** + `data.reason_code`（原因码由 08 拥有）；列表用 `pkg/query`（`field=op:value`）。守卫记号：T = 租户成员 + 权限码，O = 按对象所属租户 / 项目判定，P = 项目内（`RegisterEntity`），S = 系统级只读。

### 6.1 模板库

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 错误 / 幂等 |
|---|---|---|---|---|---|---|
| L1 | GET `/tenants/{tenant_id}/templates` | tenants | `project_read` · T | 过滤 `domain`(eq,in) `origin`(eq) `state`(eq,in) `team_mode`(eq)；排序 `name`,`updated_at` | Template[]（含一方模板，`tenant_id=0`） | — |
| L2 | GET `/templates/{template_id}` | templates | `project_read` · O | `?include=versions,eval_summary,calibrations` | Template + 最新已发布版本摘要 | 404 185404 |
| L3 | GET `/template-versions/{version_id}` | template-versions | `project_read` · O | — | Version（含完整 definition、definition_sha256） | 404 185404 |
| L4 | GET `/template-versions/{version_id}/diff` | template-versions | `project_read` · O | `?against=<version_id>` | `{added[], removed[], changed[]}`（阶段、验证器、参数、rubric） | 422 185431（不同模板） |
| L5 | POST `/tenants/{tenant_id}/templates` | tenants | `template_manage` · T；HUMAN | `{based_on_version_id*, key*, name*}` | Template（TEAM，DRAFT，复制定义） | 409 185413（key 重复）；v1.1 |
| L6 | PATCH `/template-versions/{version_id}` | template-versions | `template_manage` · O；HUMAN；If-Match | `{definition}`（仅 DRAFT） | Version | 409 185410；422 185420–185424 |
| L7 | PUT `/template-versions/{version_id}/publication` | template-versions | `template_manage` · O；HUMAN | `{changelog, confirm_eval_insufficient?}` | Version（PUBLISHED） | 422 185425（评测不足未确认）；v1.1（一方模板由发布流程管理） |
| L8 | PUT `/template-versions/{version_id}/deprecation` | template-versions | `template_manage` · O；HUMAN | — | Version（DEPRECATED） | 409 185414（无更高版本） |

### 6.2 项目绑定与参数覆盖

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 错误 |
|---|---|---|---|---|---|---|
| B1 | GET `/entities/{project_id}/template-binding` | entities | `project_read` · P | — | Binding + 解析后的参数（默认 / 覆盖 / 生效值） | 404 185404 |
| B2 | PUT `/entities/{project_id}/template-binding` | entities | `project_manage` · P；HUMAN；If-Match（已存在时） | `{template_version_id*, upgrade_policy?}` | Binding | 409 185415（项目已有进行中工作项且变更 MAJOR 未确认）；422 185416（模板不适用当前 TeamMode） |
| B3 | PATCH `/entities/{project_id}/template-binding/brief-overrides` | entities | `project_manage` · P；HUMAN；If-Match | `{overrides: {param_key: value}}` | Binding（`overrides_approved_by` = 调用者） | 422 185430 |
| B4 | PUT `/entities/{project_id}/template-binding/upgrade` | entities | `project_manage` · P；HUMAN | `{target_version_id*, acknowledge_breaking?}` | Binding + 影响报告 | 422 185417（MAJOR 未勾选确认） |

### 6.3 验证器、评分与校准

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 错误 |
|---|---|---|---|---|---|---|
| V1 | GET `/work-items/{work_item_id}/verifier-results` | work-items | `work_item_read` · O | `?stage=` | `[{verifier_key, kind, verdict, metrics, evidence_refs, limitations, tool_version, calibration_state}]`（数据来自 03 验证记录） | — |
| V2 | POST `/work-items/{work_item_id}/rubric-scores` | work-items | 内部：产品验收 Agent（经 05 派单的执行凭证）；人工：`decision_approve` · O | `{stage*, scores*[]}` | RubricScore（含 total、passed） | 422 185440（维度缺失）；403 `HUMAN_ONLY`（stage=HUMAN_REVIEW 由 Agent 提交）；Idempotency-Key |
| V3 | GET `/work-items/{work_item_id}/rubric-scores` | work-items | `work_item_read` · O | — | 各阶段评分并列 + 分歧维度 | — |
| V4 | POST `/deliveries/{delivery_id}/rubric-scores` | deliveries | 甲方经 01 门户（`CLIENT`）；团队 `delivery_compose` · O | 同 V2（stage=CLIENT_ACCEPTANCE / HUMAN_REVIEW） | RubricScore | 同 V2 |
| C1 | GET `/tenants/{tenant_id}/verifier-calibrations` | tenants | `template_manage` 或 `project_read` · T | 过滤 `template_key` `verifier_key` `state` | Calibration[] | — |
| C2 | POST `/tenants/{tenant_id}/verifier-calibrations` | tenants | `template_manage` · T；HUMAN | `{template_key*, verifier_key*, provider_ref*}` | Calibration（CALIBRATING，排队 J2） | 422 185441（相关人工标注用例 < 20） |
| C3 | PUT `/verifier-calibrations/{calibration_id}/confirmation` | verifier-calibrations | `template_manage` · O；HUMAN | `{accept: bool, note?}` | Calibration（CALIBRATED / UNCALIBRATED） | 409 185442（未完成评测）；422 185443（一致率低于目标仍要求接受） |

### 6.4 评测集

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 错误 |
|---|---|---|---|---|---|---|
| E1 | GET `/tenants/{tenant_id}/eval-cases` | tenants | `template_manage` 或 `project_read` · T | 过滤 `template_key` `origin` `active` | EvalCase[] | — |
| E2 | POST `/tenants/{tenant_id}/eval-cases` | tenants | `template_manage` · T；HUMAN | `{template_key*, title*, input_ref*, expected*}` | EvalCase（MANUAL） | 422 185444（输入文件不可访问） |
| E3 | PATCH `/eval-cases/{case_id}` | eval-cases | `template_manage` · O；HUMAN；If-Match | `{title?, expected?, active?}` | EvalCase | — |
| E4 | POST `/tenants/{tenant_id}/eval-runs` | tenants | `template_manage` · T | `{template_key*, template_version_id*, target*}` | EvalRun（RUNNING） | 409 185445（同目标已有运行中）；Idempotency-Key |
| E5 | GET `/eval-runs/{run_id}` | eval-runs | `template_manage` 或 `project_read` · O | — | EvalRun + 明细链接 | — |

### 6.5 生成器适配器

| # | 方法 路径 | tag | 权限 · 守卫 | 请求 | 响应 data | 错误 |
|---|---|---|---|---|---|---|
| G1 | GET `/tenants/{tenant_id}/template-providers` | tenants | `project_read` · T | 过滤 `kind` `state` | Provider[]（不返回密钥） | — |
| G2 | POST `/tenants/{tenant_id}/template-providers` | tenants | `integration_manage` · T；HUMAN | `{kind*, adapter_key*, display_name*, config*, secret?, machine_id?, accept_license_notice*}` | Provider（密钥经 SecretSealer） | 422 185450（未知适配器）；422 185451（未确认许可提示） |
| G3 | PATCH `/template-providers/{provider_id}` | template-providers | `integration_manage` · O；HUMAN；If-Match | 可选字段 | Provider | — |
| G4 | POST `/template-providers/{provider_id}/health-checks` | template-providers | `integration_manage` · O | — | `{state, latency_ms, error?}` | — |
| G5 | PUT `/entities/{project_id}/template-binding/providers` | entities | `project_manage` · P；HUMAN | `{kind → provider_id}` | Binding | 422 185452（provider 不可用或类型不符） |

### 6.6 领域工作台（读模型）

| # | 方法 路径 | tag | 权限 · 守卫 | 响应 data |
|---|---|---|---|---|
| W1 | GET `/entities/{project_id}/video-workbench` | entities | `work_item_read` · P | 镜头表 × 候选片段（provider、模型、seed、自动 QC 结论、人工挑选状态）、角色定妆、母版 QC 汇总 |
| W2 | PUT `/shot-takes/{take_id}/selection` | shot-takes | `work_item_execute` 或 `decision_approve` · O；HUMAN | `{selected: bool, note?}`；同镜头只能选一个；→ 选中候选进入剪辑输入 |
| W3 | GET `/entities/{project_id}/novel-workbench` | entities | `work_item_read` · P | 故事圣经条目、章节列表（字数、状态、验证结论）、故事状态时间线、伏笔台账、连续性报告（CED、问题清单与引用） |
| W4 | GET `/entities/{project_id}/team-task-board` | entities | `work_item_read` · P | 任务（清单完成度、交付物链接有效性、签收状态）、风险登记、AI 助理草稿（待采纳） |

### 6.7 错误码（本模块 185000–189999）

| 码 | HTTP | i18n key | 含义 |
|---|---|---|---|
| 185403 | 403 | `templates.error.forbidden` | 权限拒绝（`data.reason_code`） |
| 185404 | 404 | `templates.error.not_found` | 模板 / 版本 / 绑定 / 用例不存在 |
| 185409 | 409 | `templates.error.version_conflict` | If-Match 不符 |
| 185410 | 409 | `templates.error.version_immutable` | 已发布版本不可修改 |
| 185411 | 409 | `templates.error.version_in_use` | 版本仍被项目锁定 |
| 185412 | 409 | `templates.error.illegal_transition` | 非法状态迁移 |
| 185413 | 409 | `templates.error.key_taken` | 模板 key 重复 |
| 185414 | 409 | `templates.error.no_newer_version` | 弃用时无更高版本 |
| 185415 | 409 | `templates.error.binding_change_unconfirmed` | 有进行中工作项时变更 MAJOR 未确认 |
| 185416 | 422 | `templates.error.team_mode_mismatch` | 模板不适用当前团队模式 |
| 185417 | 422 | `templates.error.breaking_unacknowledged` | MAJOR 升级未确认 |
| 185420–185424 | 422 | `templates.error.definition_invalid` | 定义校验失败（`data.rule` 指明 BR-11-01 子项） |
| 185425 | 422 | `templates.error.eval_insufficient` | 评测不足且未确认 |
| 185428 | 428 | `templates.error.precondition_required` | 缺少 If-Match |
| 185430 | 422 | `templates.error.override_out_of_range` | 参数覆盖越界或不可覆盖 |
| 185431 | 422 | `templates.error.diff_mismatch` | 跨模板比较 |
| 185440 | 422 | `templates.error.rubric_incomplete` | 评分缺维度 |
| 185441 | 422 | `templates.error.calibration_samples` | 校准样本不足 |
| 185442 | 409 | `templates.error.calibration_pending` | 评测未完成 |
| 185443 | 422 | `templates.error.calibration_below_target` | 一致率未达标 |
| 185444 | 422 | `templates.error.eval_input_unavailable` | 评测输入不可访问 |
| 185445 | 409 | `templates.error.eval_run_busy` | 已有运行中评测 |
| 185450–185452 | 422 | `templates.error.provider_invalid` | 适配器未知 / 许可未确认 / 不可用 |

### 6.8 跨模块接口

**本模块提供**

| 接口 | 方法 | 消费者 |
|---|---|---|
| `TemplateResolver` | `ForProject(ctx, tenant, project) (ResolvedTemplate{version_id, sha256, params(resolved), stages, verifiers, rubric, internal_qa, human_gates}, error)`；`WorkItemBlueprint(ctx, tenant, project, stage_key) ([]WorkItemDraft, error)` | 02（拆解与 DoR 默认值）、03（验证计划）、05（内部质量门派单）、07（项目创建）、10（KR 指标映射） |
| `VerifierCatalog` | `Get(ctx, tenant, template_version_id, verifier_key) (VerifierDef, CalibrationState, error)`；`ToolCalibration(ctx, tenant, template_version_id, verifier_key, machine_id) (CalibrationState, error)`（D116：不需工具校准的验证器恒返回 CALIBRATED） | 03（执行验证器、选择截图机器） |
| `RubricScorer` | `Record(ctx, tx, RubricScoreInput) (score_id, passed, error)`；`Compare(ctx, subject) (Divergence[])` | 03 / 05（产品验收 Agent 结果入库）、07（甲方评分）、08（分歧指标） |
| `ProviderRegistry` | `Resolve(ctx, tenant, project, kind) (ProviderHandle, error)` | 03（生成类执行） |
| `EvalSetWriter` | `ProposeCase(ctx, tx, {template_key, template_version?, source: ESCAPE \| MANUAL, work_item_id?, description, evidence_refs[], expected}) (case_id, created bool, error)`：在调用方事务内写 `templates_eval_cases`（`origin` = source；ESCAPE 时 `source_escape_id` 取 02 穿透记录 ID）；幂等键 `(tenant_id, template_key, source, work_item_id, sha256(description))`，重复调用返回已有 `case_id` 且 `created=false`；提交后触发 J5 | 02（BR-02-58 穿透回流，**唯一的穿透入集调用方**）、模板维护者手工添加（MANUAL） |

**本模块使用**：07 `ProjectReader`；02 `WorkItemReader`；03 `EvidenceReader`、`ExecutionLauncher`（评测运行以 ADHOC 执行跑内部 Agent 配置）；04 `MachineDirectory`（工具与 provider 本地部署机器）；06 `MemoryWriter`（穿透学习条目写入测试 / 验收 Agent 经验）；08 `AuthorityChecker`；09 `JobQueue` / `OutboxPublisher` / `SecretSealer`。

---

## 7. 后台任务与事件

### 7.1 任务（09 JobQueue；周期任务经 dlock 单实例）

| 任务 | 触发 / 频率 | 互斥 | 内容 | 重试 |
|---|---|---|---|---|
| J1 `templates.seed_first_party` | 启动时（幂等） | dlock `templates:seed` | 写入 / 升级一方模板版本（按 `definition_sha256` 判断是否新版本）与 SEED 评测用例 | 指数退避 5 次 |
| J2 `templates.eval_run` | E4 / C2 / 穿透入集 / 周期 | 每 `(template_key, target)` 单实例 | 逐用例运行验证器或内部 Agent 配置，计算一致率，写 EvalRun 与校准结果 | 单用例失败记 `INCONCLUSIVE`，不中断 |
| J3 `templates.calibration_review` | 每日 03:00（团队时区） | dlock | 到期（30 天）或 ESCAPE 新增 ≥ 5 的校准重跑评测；一致率跌破 → DRIFTED；`PROBE` 行到期的按机器排队 J2（BR-11-35 ③） | — |
| J4 `templates.provider_health` | 每 5 min | 每 provider | 健康检查；连续 3 次失败 → UNAVAILABLE 并发事件；恢复后 ENABLED | — |
| J5 `templates.escape_ingest` | `EvalSetWriter.ProposeCase` 新建用例（`created=true`）后入队 | 每 `template_key` 单实例 | 触发 J2 对相关验证器与内部 Agent 配置重跑评测；累计 ESCAPE ≥ 5 时提前触发 J3 | 指数退避 5 次 |

### 7.2 发布事件

| 事件 | 时机 | data |
|---|---|---|
| `template.version_published` | T2 | `{template_key, version_id, semver, breaking}` |
| `template.version_deprecated` | T3 | `{template_key, version_id}` |
| `template.binding_changed` | B2 / B4 / U1 | `{project_id, from_version_id?, to_version_id}` |
| `verifier.calibration_changed` | C2–C5 | `{template_key, verifier_key, provider_ref, method, machine_id?, state, agreement?, failed_probes?}` |
| `eval.run_completed` | J2 | `{run_id, template_key, agreement, failed}` |
| `provider.state_changed` | J4 | `{provider_id, kind, state}` |
| `rubric.divergence_detected` | §5.5 分歧 ≥ 1.0 | `{subject_kind, subject_id, dimensions[]}` |

### 7.3 消费事件

（穿透用例经 02 同步调用 `EvalSetWriter.ProposeCase` 写入，本模块不消费 `quality.escaped`）；`machine.runtimes_changed`（04，`tools_changed` 非空）→ 重算 `MachineDirectory.ToolAvailability`；相关工具首次可用或 `variant` / 主版本变化 → 该机器的 `PROBE` 行置 DRIFTED（如有）并排队 J2 工具校准（BR-11-35 ①②）；工具恢复时经 02 `WorkItemTransitioner.SetSystemBlock(on=false)` 清除 `TOOL_UNAVAILABLE`；`project.created`（07）→ 若创建时带模板则建立绑定；`machine.runtimes_changed`（04）→ 重新评估工具可用性（BR-11-30）。

### 7.4 审计与 SSE

审计：`templates.version_published`、`templates.binding_changed`、`templates.overrides_approved`、`templates.calibration_confirmed`、`templates.provider_changed`、`templates.eval_case_changed`（detail 必含 `project_id`（如有）、`actor_kind`、`authority_source`）。SSE 主题：`tenant/{t}/template/{id}`、`tenant/{t}/project/{p}`（绑定、工作台更新）、`tenant/{t}/eval_run/{id}`；`TopicAuthorizer` 权限：`project_read` / `template_manage`。

---

## 8. 页面规格

### 8.1 模板库 `/t/:tenantID/templates`

- **目的**：选择与了解模板；新建项目向导的第一步引用此列表。
- **布局**：领域筛选 chip（全部 / 软件 / 视频 / 小说 / 团队任务）、团队模式筛选；模板卡片：领域图标、名称、最新版本、阶段数、验证器数（确定性 / 模型打分 / 人工 三色计数）、评测集规模与校准状态汇总、适用团队模式、「用此模板新建项目」「查看详情」。
- **状态**：加载骨架；空（团队无自定义模板时只显示一方模板，并提示「复制一方模板可创建团队模板（v1.1）」）；错误可重试；无权限（缺 `project_read`）。
- **移动端**：卡片单列。

### 8.2 模板详情 `/t/:tenantID/templates/:templateID`

- **标签**：概览（定位、适用模式、版本与变更日志、被多少项目锁定）/ 阶段（阶段 → 工作项树图，人工关口以菱形标记，必经关口标「必经」）/ 产物（产物类型、schema 摘要、文件类型）/ 验证器（表格：名称、类别、运行方式、适用产物、参数默认值、是否可被 Brief 覆盖、校准状态、「需复核」标记、局限说明）/ 评分标准（硬门槛与加权维度、权重、通过线、评分指引）/ 内部质量门（测试 Agent 检查清单、产品验收 Agent 检查清单、最大内部轮次）/ 评测集（用例数按来源分布、最近评测一致率、穿透入集记录）/ 生成器（所需 provider 类型与候选）。
- **交互**：版本切换与 diff（L4）；「用此模板新建项目」；团队模板可编辑（v1.1）。
- **状态**：评测不足时顶部琥珀提示；含 UNCALIBRATED 模型打分验证器时提示「这些验证器只出建议」。

### 8.3 项目设置 · 模板（嵌入 07 项目设置）

锁定版本、升级策略、升级按钮（显示 diff 与影响的进行中工作项）、参数表（默认 / Brief 覆盖 / 生效值，可编辑覆盖并需确认）、provider 选择（按类型下拉，显示健康状态与许可提示）。

### 8.4 视频工作台 `/t/:tenantID/p/:projectID/video`

- **布局**：左侧阶段进度（A0–A8，当前阶段高亮、必经关口状态）；主区镜头表：每行一个镜头（编号、时长、景别、角色、提示词摘要），横向排列候选片段缩略图（provider、模型版本、seed、自动 QC 徽章：规格 / 黑场冻帧 / 人脸一致性 / 对照分镜），人工「选用」单选；右侧所选镜头详情（候选播放、QC 明细与证据帧号、重新生成按钮（剩余次数））。底部母版 QC 汇总：时长、分辨率、响度（数值 + 目标）、字幕 CPS / 对齐偏差、切点匹配、品牌安全。
- **规则**：同镜头只能选一个候选；重新生成超过上限按钮禁用并提示「已达上限，请升级给负责人」；模型打分徽章在未校准时显示「建议」。
- **状态**：provider 不可用横幅（可切换）；工具不可用横幅（缺 ffmpeg 等）；空（尚无镜头表）。
- **移动端**：镜头表改卡片列表，候选横滑。

### 8.5 小说工作台 `/t/:tenantID/p/:projectID/novel`

- **布局**：标签：故事圣经（条目分类：世界观 / 角色 / 地点 / 术语，可搜索，显示被提及章节）/ 大纲（节拍表与全书位置条，偏离 ±3% 标黄）/ 章节（列表：字数与目标、验证结论、连续性问题数、状态；点击进入章节阅读视图，问题以高亮与侧注显示原文引用）/ 故事状态（时间线：每章快照，人物位置与状态变化）/ 伏笔台账（埋设章节、计划回收、实际回收、状态）/ 连续性报告（CED、按 5 大类分布、问题清单与证据链）。
- **规则**：连续性报告为全书范围；未校准时 CED 显示「建议」。

### 8.6 团队任务视图 `/t/:tenantID/p/:projectID/team-tasks`

- **布局**：任务列表（清单完成度进度条、交付物链接有效性图标、签收状态、负责人）、风险登记、AI 助理草稿收件区（分诊建议、行动项草稿、状态草稿；每条带来源链接与「采纳 / 修改 / 拒绝」）。
- **规则**：无负责人或截止日期的行动项不能「采纳为任务」，按钮禁用并提示；草稿未经人采纳不进入正式记录。

---

## 9. 通知

| # | 事件 | 接收人 | 渠道 | 模板 | 去重 |
|---|---|---|---|---|---|
| N-11-01 | `template.version_published`（锁定旧版本的项目） | 项目交付负责人 | 站内 | 「模板 {name} 发布 {semver}，你的项目仍锁定 {old}，可查看差异后升级」 | 每项目每版本一次 |
| N-11-02 | `verifier.calibration_changed` → DRIFTED | 团队所有者、质量负责人 | 站内 + 邮件摘要 | 「验证器 {verifier} 与人工判断一致率降至 {agreement}，结论已降级为建议，请重新校准」 | 每验证器每 7 天一次 |
| N-11-03 | `provider.state_changed` → UNAVAILABLE | 团队所有者、受影响项目负责人 | 站内 + 浏览器推送 | 「{provider} 不可用，{n} 个工作项已阻塞，可在项目设置切换」 | 状态变化时一次 |
| N-11-04 | `eval.run_completed` 且一致率低于目标 | 发起人 | 站内 | 「评测 {template} 一致率 {agreement}，{failed} 条不一致」 | — |
| N-11-05 | `rubric.divergence_detected` | 交付负责人 | 站内 | 「{subject} 内部验收与人工 / 甲方在 {dimensions} 判断分歧较大，已记入校准数据」 | 每对象一次 |
| N-11-06 | 工具不可用（BR-11-30） | 团队所有者 | 站内 | 「验证需要 {tool}，当前没有在线机器具备，请安装」 | 每工具每天一次 |

---

## 10. 验收标准

| ID | 层 | 关联 | Given / When / Then |
|---|---|---|---|
| AC-11-01 | integration | US-11-01, BR-11-01 | **G** 一方模板 `video-production` 1.0.0 已发布 **W** 新建项目选择该模板 **T** 项目绑定锁定 1.0.0 与 `definition_sha256`；按阶段 A0 生成工作项草稿；每条验收标准都有验证方式 |
| AC-11-02 | unit | BR-11-01 | **G** 定义中工作项依赖成环 **W** 发布 **T** 422 185420，状态仍为 DRAFT |
| AC-11-03 | unit | BR-11-01 | **G** HARD_GATE 维度只关联 MODEL_SCORED 验证器 **W** 发布 **T** 422 185424 |
| AC-11-04 | integration | BR-11-05/06 | **G** 视频项目，参数 `target_lufs` 可覆盖 **W** 交付负责人覆盖为 −14 **T** 绑定记录覆盖与确认人；`loudness` 验证按 −14 ±0.5 判定 |
| AC-11-05 | integration | BR-11-05 | **W** 覆盖 `target_lufs=-40` **T** 422 185430 |
| AC-11-06 | integration | BR-11-06 | **G** Agent 调 B3 **T** 403 185403（`HUMAN_ONLY`） |
| AC-11-07 | integration | BR-11-09 | **G** 所有在线机器都没有 ffmpeg **W** 母版进入内部质量门 **T** `av_qc` 结论 INCONCLUSIVE（`TOOL_UNAVAILABLE`），工作项 BLOCKED，N-11-06 发出；**不得**判定通过 |
| AC-11-08 | e2e | BR-11-09, 03 | **G** 执行者在证据里贴出「ffprobe 通过」日志，但实际母版为 23.976 fps 而 Brief 要求 25 fps **W** 内部质量门 **T** 平台重跑 `ffprobe_spec` FAIL，工作项回到 REWORK，反馈含实测值与期望值；人收件箱无事项 |
| AC-11-09 | integration | §4.2 | **G** `face_consistency` 状态 UNCALIBRATED，得分低于初值阈值 **W** 产品验收 Agent 汇总 **T** 该结论标「建议」，不单独导致内部验收失败；若 rubric 其他维度通过则内部验收可通过并在证据中注明 |
| AC-11-10 | integration | §4.2, BR-11-23 | **G** 校准 CALIBRATED，新增 5 条 ESCAPE 用例后一致率 κ 降到 0.5 **W** J3 **T** 状态 DRIFTED，N-11-02 发出，结论降级为建议 |
| AC-11-11 | e2e | BR-11-22, 00 §15.2 | **G** 软件项目工作项内部验收通过，人工验收（L3）时发现「网格行高不一致」并拒绝 **W** 02 发布 `quality.escaped`，其 `escape_learning` 调用 `EvalSetWriter.ProposeCase(source=ESCAPE)` **T** 创建 ESCAPE 用例（输入 = 当时截图与 ui_contract，期望 `ui_layout_rules`=FAIL），J5 触发 J2 评测；06 写入测试 / 验收 Agent 经验记忆；同一穿透重复调用返回同一 `case_id`、`created=false` |
| AC-11-12 | e2e | §5.7 | **G** 2048 类 UI 工作项，`#board` 未设置行模板 **W** 内部质量门 **T** `ui_layout_rules`（网格单元同尺寸）FAIL，内部返工，不到达人；修复后通过 |
| AC-11-13 | integration | §5.7 | **G** 静态页面同时以 `file://` 与 HTTP 打开，`file://` 下控制台报模块加载错误 **W** `ui_screenshot` **T** FAIL（交付形态硬门槛） |
| AC-11-14 | integration | §5.7 | **G** 测试 Agent 发现同一逻辑在 `index.html` 内嵌与 `src/` 各有一份 **W** 测试 Agent 输出 **T** 缺陷「重复实现无一致性验证」；产品验收 Agent 在「代码质量」维度扣分并给出证据 |
| AC-11-15 | integration | §5.8 | **G** 母版中段 3 秒黑场 **W** `av_qc` **T** FAIL，证据含起止时间码；片头 0.4 秒黑场 **T** PASS（放行配置） |
| AC-11-16 | integration | §5.8 | **G** 字幕某条 22 CPS（Brief 为英文成人） **W** `subtitle_rules` **T** FAIL，证据含字幕序号 |
| AC-11-17 | integration | §5.8 | **G** 字幕起始与对齐词级时间戳偏差 350 ms **W** `subtitle_alignment` **T** FAIL；偏差 150 ms **T** PASS |
| AC-11-18 | integration | §5.8, BR-11-26 | **G** 项目选择的视频生成 provider 健康检查连续 3 次失败 **W** J4 **T** provider UNAVAILABLE；A4 阶段工作项 BLOCKED（`PROVIDER_UNAVAILABLE`）；切换到另一 provider 后恢复，已生成片段 provenance 不变 |
| AC-11-19 | integration | §5.8 | **G** 某镜头已重新生成 6 次仍未通过 **W** 再次请求生成 **T** 拒绝并升级给负责人（02 `INTERNAL_ROUNDS_EXHAUSTED` 类升级），视频工作台按钮禁用 |
| AC-11-20 | integration | §5.8 | **G** VBench subject_consistency 0.98 但 dynamic_degree 低于下限 **W** `vbench_subset` **T** WARN「画面几乎不动」，不因高一致性判通过 |
| AC-11-21 | integration | §5.9 | **G** 第 7 章 5600 字，目标 4000 **W** `word_count` **T** FAIL（超 15%） |
| AC-11-22 | integration | §5.9 | **G** 第 12 章出现圣经未登记人名「李沐」（圣经为「李慕」） **W** `name_registry` **T** WARN，证据为原文位置；未处理进入验收时 FAIL |
| AC-11-23 | integration | §5.9 | **G** 全书 30 章 **W** `continuity_check` **T** 检查覆盖全部 30 章（日志可证），不得抽样；每条问题附两处原文引用 |
| AC-11-24 | integration | §5.9 | **G** 全书结束仍有 2 个伏笔未回收且未标「有意保留」 **W** `foreshadow_closure` **T** FAIL |
| AC-11-25 | integration | §5.10, BR-11-11 | **G** HUMAN_ONLY 团队任务需签收 **W** AI 助理尝试提交 `sign_off` **T** 403 185403（`HUMAN_ONLY`） |
| AC-11-26 | integration | §5.10 | **G** 会议纪要行动项无负责人 **W** 采纳为任务 **T** 按钮禁用；API 422（02 字段缺失），行动项回推主持人 |
| AC-11-27 | integration | §5.10 | **G** 交付物链接最后修改时间早于任务开始 **W** 确定性验证 **T** FAIL「交付物未在本任务期间更新」 |
| AC-11-28 | integration | BR-11-19 | **G** 产品验收 Agent 「叙事与节奏」4 分，甲方 2 分 **W** 甲方评分提交 **T** `rubric.divergence_detected`；三方评分并列显示 |
| AC-11-29 | integration | BR-11-10 | **G** 执行者与配置的评分模型同属一个模型家族 **W** 派发模型打分验证 **T** 拒绝并改用其他家族；无可用其他家族时结论为 INCONCLUSIVE 并升级 |
| AC-11-30 | integration | §4.3 | **G** 项目锁定 1.0.0，发布 2.0.0（breaking） **W** 升级未勾选确认 **T** 422 185417；勾选后锁定 2.0.0，进行中工作项仍按 1.0.0 验证 |
| AC-11-31 | integration | BR-11-27 | **G** provider 配置含密钥 **W** 读取 G1 与执行日志 **T** 均不出现密钥明文 |
| AC-11-32 | unit | BR-11-18 | **G** 加权维度分 [4,3,4,3,5] 权重 [0.25,0.15,0.15,0.15,0.30] 且硬门槛全过 **W** 计分 **T** total = 3.95，passed = true；任一硬门槛失败 **T** total = NULL，passed = false |
| AC-11-33 | integration | 并发 | **G** 两个负责人同时修改 Brief 覆盖 **W** 后提交者用旧 ETag **T** 409 185409 |
| AC-11-34 | integration | 幂等 | **G** 同一 `Idempotency-Key` 重放 E4 **T** 返回同一 EvalRun；请求体不同 **T** 422 176425 |
| AC-11-35 | integration | BR-11-35, §4.2 | **G** 机器 M 首次上报 `browser_capture`（`browser:edge@141`）**W** 消费 `machine.runtimes_changed` **T** 建 `PROBE` 行（`machine_id=M`）并排队 J2；SOFTWARE 探针全部与期望一致 → CALIBRATED，无需人工确认；发布 `verifier.calibration_changed{method:PROBE, machine_id:M}` |
| AC-11-36 | integration | BR-11-34, §5.7 | **G** M 的浏览器以 496px 最小宽度排版后裁切 420 视口截图 **W** 运行 `probe_viewport_fidelity` **T** 页面自报宽度 ≠ 420 → 探针失败；M 的 `ui_screenshot` 行为 UNCALIBRATED（或由 CALIBRATED 转 DRIFTED），`last_failures` 含该探针与截图；通知质量负责人 |
| AC-11-37 | integration | BR-11-35 ② | **G** M 的 `ui_screenshot` 为 CALIBRATED **W** 04 上报浏览器主版本 141 → 142 **T** 旧行 DRIFTED，新 `provider_ref` 排队校准；校准完成前 `ToolCalibration(M)` 返回非 CALIBRATED |
| AC-11-38 | integration | BR-11-34 | **G** ACCEPTOR 配置 X **W** J2 用截图类探针评测 X **T** X 对 `probe_ok_page` 提 MAJOR → 记误报 1；对 `probe_uneven_rows` 无发现 → 记漏报 1；EvalRun 展示在模板评测集与 X 的 Agent 详情 |

---

## 11. 度量与埋点

| 指标 | 定义 | 用途 |
|---|---|---|
| 内部一次通过率（按模板） | 首轮即通过内部质量门的工作项占比 | 模板与执行 Agent 质量 |
| 穿透率（按模板、按验证器） | L3 / L4 发现、内部已放行的问题数 ÷ 进入 L3 的工作项数 | 内部质量门有效性（00 §15.5） |
| 验证器命中率 | 各验证器 FAIL 次数及其后续被人确认为真问题的比例 | 冗余或噪声验证器识别 |
| 校准一致率 | 模型打分验证器与人工标注一致率 | 是否可参与判定 |
| 评分分歧 | 内部 / 人工 / 甲方三方评分差 | 产品验收 Agent 校准 |
| 生成成本 | 每镜头候选数与成本、每章节生成成本 | 预算与 provider 选择 |

埋点：`template_selected{domain}`、`template_upgraded{breaking}`、`brief_override_set{param}`、`verifier_run{key, kind, verdict, tool_version}`、`calibration_changed{state}`、`eval_run_completed{agreement}`、`escape_ingested{template, verifier?}`、`provider_switched{kind}`。不采集产物内容。

---

## 12. 依赖、风险与待决

### 12.1 依赖

| 规格 | 需要的能力 |
|---|---|
| 02 | DoR 与验证计划字段（00 §15.3）、`REWORK` / 升级路由（00 §15.2）、`quality.escaped` 事件、WorkItemType `CONTENT` |
| 03 | 平台重跑确定性验证器、冻结 / 隐藏验收集、视觉验证器执行（无头浏览器截图）、证据存储、生成类执行与成本 |
| 04 | 工具能力检测（ffmpeg、ffprobe、WhisperX、PySceneDetect、无头浏览器）进入机器能力清单 |
| 05 | TESTER / ACCEPTOR / ASSISTANT 角色、跨模型家族约束、内部质量门派单 |
| 06 | 穿透学习条目写入经验记忆；故事圣经条目被提及自动注入（与检索协作） |
| 07 | 项目创建选择模板、交付评分（甲方 rubric） |
| 08 | 原因码登记、穿透指标、`template_manage` / `integration_manage` 权限 |
| 09 | JobQueue、Outbox、SecretSealer、SSE |
| 12 | 软件模板的工作项类型、状态组、流转矩阵与评审单 |

### 12.2 风险

| 风险 | 缓解 |
|---|---|
| 领域阈值来自二手资料 | 全部标「需复核」；参数化并允许 Brief 覆盖；上线前逐项对照官方文档 |
| 模型打分不稳定 | 未校准只出建议；评测集与周期校准；跨模型家族 |
| 生成服务下线或涨价 | 适配器抽象；provenance 记录；provider 健康检查与切换 |
| 视觉 / 创意判断难以自动化 | 必经人工关口；rubric 三方并列；分歧入校准 |
| 工具链缺失导致验证无法运行 | 工具不可用即阻塞并通知，不跳过 |

### 12.3 待决（附建议）

| # | 问题 | 建议 |
|---|---|---|
| Q-11-1 | 团队自定义模板何时开放 | v1.1；MVP 只用一方模板 + Brief 覆盖 |
| Q-11-2 | 视频生成默认 provider | 不设默认；项目创建时必须选择并通过健康检查 |
| Q-11-3 | 小说模板的查重服务 | 作为 provider（`LLM_JUDGE` 之外新增 `PLAGIARISM_CHECK`，v1.1）；MVP 由人工判断 + 网络检索证据 |
| Q-11-4 | 评测用例是否可跨团队共享 | 否（隐私）；仅一方 SEED 用例全局可用 |

---

## 13. 契约变更请求

| # | 对 00 的位置 | 变更 |
|---|---|---|
| CR-11-01 | §1 | 规格 11 已登记（00 v2.0）；本规格使用模块 `templates`、表前缀 `templates_`、错误码段 **185000–189999**（请在 §9 错误码段登记） |
| CR-11-02 | §5 | `TemplateDomain` 已登记；其余枚举（TemplateState、VerifierKind、VerifierRunner、VerdictLevel、CalibrationState、RubricDimensionKind、EvalCaseOrigin、ProviderKind、UpgradePolicy）为模块内枚举 |
| CR-11-03 | §6.1 | `template_manage`、`integration_manage` 已登记（§15.5）；本规格另用 `project_read` / `project_manage` / `work_item_read` / `work_item_execute` / `decision_approve` / `delivery_compose`，无新增 |
| CR-11-04 | §7.1 | 新增事件：`template.version_published`、`template.version_deprecated`、`template.binding_changed`、`verifier.calibration_changed`、`eval.run_completed`、`provider.state_changed`、`rubric.divergence_detected`（发布者 templates；消费者 governance（通知）、delivery（拆解默认值刷新）） |
| CR-11-05 | §8 | 新增接口 `TemplateResolver`、`VerifierCatalog`、`RubricScorer`、`ProviderRegistry`（提供者 templates） |
| CR-11-06 | §3 | 一方模板及其 SEED 评测用例使用 `tenant_id = 0`（系统级只读），按基础设施表例外登记 |
| CR-11-07 | §5 | `WorkItemType` 的 `CONTENT` 已登记；建议 02 增加 `template_stage_key` 字段以便工作台按阶段聚合 |
| CR-11-08 | 08 | 原因码登记：`HUMAN_ONLY`（已有）、`TOOL_UNAVAILABLE`、`PROVIDER_UNAVAILABLE`（用于阻塞原因，非权限拒绝，建议放 02 `BlockReason`） |
| CR-11-09 | 04 | 机器能力检测扩展：工具清单（ffmpeg、ffprobe、WhisperX、PySceneDetect、无头浏览器）及版本，供 BR-11-09 / BR-11-30 使用 |
| CR-11-10 | §8 | `VerifierCatalog` 增加 `ToolCalibration`（D116 工具校准，03 调用）；已并入 00 §8 |

> **合并状态（2026-10-12）**：CR-11-01、04、05、06、07、08、09 已并入 00 v2.0（§9 错误码段、§7.1 事件、§8 接口、§3 基础设施表例外、02 `template_stage_key` 与 `BlockReason`、04 工具检测）；另新增 `EvalSetWriter`（本规格 §6.8，替代 02 原写法 `TemplateEvalSink`）。CR-11-02、03 无需变更。

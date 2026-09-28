# Backend API 文档索引

这一层只放**需要留档的"为什么"**：设计决策、被否决的替代方案、跨模块强约定、尚未落地的方案。

不放这里的东西：

| 想知道 | 去哪看 |
|---|---|
| 目录结构、怎么跑、怎么测、怎么生成代码 | `../README.md` |
| 端点、字段、状态码 | [openapi.json](openapi.json)，或运行时的 `/openapi`、`/docs` |
| 表结构 | 各模块 `sql/{postgres,mysql,sqlite}/*.sql` |
| 行为细节 | 测试 |
| 当前任务进行到哪、下一步做什么 | `../../../.claude/task-state.md` |
| 模块怎么写（规约） | `../../../.claude/skills/backend-module/SKILL.md` |

## 文档

| 文档 | 类型 | 状态 | 什么时候读 |
|---|---|---|---|
| [module-lifecycle-redesign.md](module-lifecycle-redesign.md) | 设计决策记录 | 已实现，等待最终人工验收 | 改 `pkg/module`、`pkg/appenv`、`internal/app` 的生命周期或装配之前 |
| [todo-module-design.md](todo-module-design.md) | 决策与约束记录 | 已实现（`internal/todo`、`pkg/query`） | 写新的业务模块、列表查询、导入导出之前——TODO 是打样模块，后续模块照抄它的约定 |
| [iam-domain-migration-plan.md](iam-domain-migration-plan.md) | 执行计划 v10（带 checkbox） | 固定源树迁移等价已完成；Phase 10 余 10.8/10.11 未勾选（10.8 的两条过期判据已于 2026-09-10 更正） | 动 `internal/iam/**` 之前；Phase 10 待办也在这里 |
| [iam-release-checkpoints.md](iam-release-checkpoints.md) | 审计报告 + 验收清单 | **当前**：应用/代码范围内已就绪；不含生产部署、灾备与签字 | 判断 IAM 当前能不能交付时，先读这份 |
| [iam-production-readiness-audit-2026-09-07.md](iam-production-readiness-audit-2026-09-07.md) | 历史审计报告 | 历史快照（NOT READY 是当时结论），**不要据此判断当前状态** | 追溯某个缺陷最初是怎么被发现与定级时 |
| [iam-local-uat.md](iam-local-uat.md) | 运行手册 | 单机 UAT 自举；**要求本地 Unix-socket Docker context**（与迁移门禁不同） | 要起一个可复现的本地验收环境、跑真实浏览器审计时 |
| [runbook-migration-recovery.md](runbook-migration-recovery.md) | 运行手册 | 迁移回滚/备份恢复/擦除对账演练；传输由 `make iam-preflight` 决定，**可用远端 Docker** | 演练恢复、或恢复之后要对账擦除义务时 |
| [iam-scim-operations.md](iam-scim-operations.md) | 运行手册 | 出站 SCIM 的未知结果处理；claim 永不过期，无自动解锁 | SCIM 远端调用结果不明、告警指向 unfinished operation 时 |
| [iam-redis-security.md](iam-redis-security.md) | 运行手册 | Redis 拓扑/认证/故障策略的应用侧配置与边界 | 配置共享限流与 captcha 存储、或评估异步复制下的消费语义时 |
| [plugin-architecture.md](plugin-architecture.md) | 提案 | **未决**——结论是当前阶段不建设动态插件平台，只按可插件化方式设计边界 | 讨论扩展点、Provider SPI、领域事件、脚本/WASM 执行引擎定位时 |

## 约定

- 文件名 kebab-case，不带版本号或 `-final` 之类的状态后缀；状态写在文档头部。
- 中文。单一版本，不做双语——双语必然漂移。
- 只写"为什么"和"否决了什么"。实现细节会漂移，留在代码、测试和 OpenAPI 里。
- `openapi.json` 是这一层唯一的例外，也是唯一由机器生成的文件：**不要手改**。用 `make openapi` 重新生成并提交；`make openapi-check` 会在它与代码不一致时让 `make ci` 失败。它落在仓库里，是因为客户端类型生成和兼容性检查都在服务启动之前运行，那时候 `/openapi` 还没人在供。

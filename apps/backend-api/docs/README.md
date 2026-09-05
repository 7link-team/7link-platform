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
| [iam-domain-migration-plan.md](iam-domain-migration-plan.md) | 执行计划 v10（带 checkbox） | 固定源树迁移等价已完成；Phase 10 产品化闭环**未完成** | 动 `internal/iam/**` 之前；Phase 10 待办（平台管理员 bootstrap、租户 owner bootstrap、字段权限）也在这里 |
| [plugin-architecture.md](plugin-architecture.md) | 提案 | **未决**——结论是当前阶段不建设动态插件平台，只按可插件化方式设计边界 | 讨论扩展点、Provider SPI、领域事件、`pkg/interpreter` 定位时 |

## 约定

- 文件名 kebab-case，不带版本号或 `-final` 之类的状态后缀；状态写在文档头部。
- 中文。单一版本，不做双语——双语必然漂移。
- 只写"为什么"和"否决了什么"。实现细节会漂移，留在代码、测试和 OpenAPI 里。
- `openapi.json` 是这一层唯一的例外，也是唯一由机器生成的文件：**不要手改**。用 `make openapi` 重新生成并提交；`make openapi-check` 会在它与代码不一致时让 `make ci` 失败。它落在仓库里，是因为客户端类型生成和兼容性检查都在服务启动之前运行，那时候 `/openapi` 还没人在供。

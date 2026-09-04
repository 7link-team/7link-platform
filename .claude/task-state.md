# Task State

## Task
IAM 产品完整性深度审计、初始化方案、测试命名治理与模块自治修正。

## Objective
在已完成固定源树迁移等价的基础上，区分迁移完成度与可部署 IAM 产品完整度；补充安全初始化方案，
审计 HTTP/gRPC、接口/数据/菜单/字段权限及 9 个 IAM 模块，并落实测试命名和 module activation 治理。

## Current Phase
ACCEPTANCE

## Current Step
IAM lifecycle 自治实现、最终 diff/staging 审阅、全量回归和非 skill 提交均已完成。

## Overall Progress
- [x] 恢复仓库事实、固定源树与既有三引擎/208 HTTP 验收证据
- [x] 深审平台/租户自举、HTTP/gRPC、接口/数据/菜单/字段权限和产品生命周期
- [x] 设计平台 bootstrap 与 tenant owner bootstrap 的三引擎验收方案
- [x] 重命名历史违规测试，并增加同目录 production stem 架构门禁
- [x] 删除 composition root 的 IAM/federation 条件 append，改为 9 模块自行 `Enabled()`
- [x] 完成相关回归和生成零 diff 验证
- [x] 完成 staged diff 审阅和本轮最终验收
- [x] Core 自治持有 Registry/Authorizer/MembershipChecker/Registrar，并通过 Services 发布稳定网关
- [x] authn/identity 通过窄能力连接注册流程，App 不再捕获模块实例
- [x] 九个 IAM 构造器统一为 `NewModule(*appenv.Runtime, Config)`
- [x] 删除六个 declaration-only 模块构造器、状态分支和 App 声明图
- [x] 增加生命周期自治 architecture 门禁并完成全量回归

Core 将拥有并提供 authorization registrar；authn/identity 及其余 IAM 模块通过 `module.Services` 连接。不得修改 `.claude/skills/**`。

## Completed Work
- 固定源功能迁移等价仍成立：9 模块、63 表、208 HTTP operation，SQLite/PostgreSQL/MySQL 迁移与既有 CI 已通过。
- 发现 Critical：生产无首个平台管理员入口；`EnsureBootstrapPrincipal` 仅测试使用且不写 platform administrator。
- 发现 Critical：创建 tenant 不创建首个 member/Administrator role/grant，tenant membership guard 导致后续 API 死锁。
- 权限结论：接口 middleware 执行链存在；principal 管理 guard 语义不可达；数据约束无业务消费者；菜单判定有实现但无默认行；字段权限完全缺失。
- HTTP 208 条迁移契约完整；IAM 无 gRPC，固定源也无，双协议属于新增产品需求。
- 迁移计划升至 v10，加入双层 bootstrap、安全输入、事务/并发/readiness 和三引擎测试矩阵，以及 Phase 10 产品缺口。
- 20 个历史测试文件按 `xx.go` / `xx_subxx_test.go` 规则重命名，`tests/architecture` 自动扫描全 backend。
- composition root 现在总是登记 9 个 IAM runtime module；每个条件模块持有专属 Config 并按 `module.Enabler` 自行激活，非法依赖组合由 registry fail fast。
- 移除 IAM 构造器中的可变 `activation ...module.Config` 和裸 `enabled bool`；按 infra 约定由模块 Config 嵌入 `module.Config`，federation 根据自身 OIDC/SAML 配置决定激活。
- App 的 IAM composition 现在只声明 9 个模块的 Runtime + Config BOM，不再构造或保留 authorization graph。
- Core 在 Provide 阶段发布未绑定 Registrar，在 Inject 阶段一次性绑定 authn TokenVerifier、领域 Authorizer 和 MembershipChecker；未绑定 Registrar 禁止注册受保护路由。
- Identity 发布稳定 RegistrationPrincipalCreator；authn 解析并映射领域错误，保留原注册 API 错误语义及 verifier 的 CSRF/cookie 可选能力。
- 所有 IAM runtime 构造器统一为 `NewModule(runtime, config)`；生产代码不存在 declaration-only module/service 分支。
- module lifecycle 文档补充稳定 provider-phase capability 与 lifecycle dependency 的区别，architecture 门禁限定允许的稳定能力类型。

## Remaining Work
本轮无剩余工作。Phase 10.1 平台管理员 bootstrap 与 10.2 tenant owner bootstrap 作为后续独立任务；不属于本轮 module lifecycle 实施范围。

## Important Decisions
- migration 只拥有 schema，不写固定账号、默认密码或环境相关秘密。
- 平台 bootstrap 必须显式、create-only、跨 authn/core/audit 原子化；密码仅 file/stdin。
- tenant 创建必须原子创建 owner member、Administrator role、`tenant_administer` grant、role member 和 15 个默认菜单。
- permission catalog 是代码声明，不做数据库 seed；其他租户业务对象不做全局 seed。
- 固定源无 IAM gRPC；除非产品明确要求双协议，不创建占位 proto/service。
- 字段权限和 principal 权限所有权属于新增产品语义，本轮只记录方案，不静默扩 schema 或改 API。
- composition root 只登记 inventory；activation 属于 module，依赖有效性属于 registry。
- Core 是 authorization catalog/evaluator/membership/registrar 的领域所有者；Registrar 在 Wire 阶段一次性绑定真实 TokenVerifier。
- 不新增无领域职责的 gateway module，不把 IAM service 塞入 `appenv.Runtime`，不使用运行时 service locator。

## Confirmed Constraints
- 不修改或提交 `.claude/skills/**`；当前该目录存在用户自己的未跟踪内容。
- 保留已提交的 `make dev` IAM 开启与开发密钥生成方案。
- 用户要求最终“除了 skill 全部提交”；提交前必须逐文件暂存，禁止 `git add .` / `git add -A`。

## Known Issues / Risks
- IAM 产品 readiness 未完成，空库即使 schema 正常也没有可运营管理员。
- principal update/suspend/resume 的 `platform_administer` tenant guard 目前普通 tenant admin 与非成员 platform admin 均不可达。
- `DataConstraint` 只可计算/解释，尚未在业务 repository 强制执行；字段权限完全未实现。
- OAuth 交互式 consent、audit archive worker、principal 删除/匿名化、渐进 key rotation 尚未闭环。
- Federation replay registry 为进程内状态，多 replica 不保证一次性消费。
- IAM graph 仍沿用兼容配置 `authn.enabled` 作为基础模块共同激活值；是否拆成独立 `iam.enabled` 是后续配置兼容决策。

## Verification
Build: PASS（`make ci` production build）
Tests: PASS（最终复跑 `go test ./...`、独立 `go -C tests test ./architecture`；此前 `make ci` 总覆盖率 90.0%，9 个 IAM root package 均 ≥90%）
Lint: PASS（最终复跑 `make fmt-check lint`、`go -C tools test ./iam-schema-lock`；此前完整 `make ci`）
Integration: PASS（本轮三引擎 migration 503.785s；真实 Docker MySQL 8.4 migration/performance/schema lock）
Other: PASS（`make generate` 零生成 diff；`git diff --check`；schema lock 147 files / 63 tables / 27 FKs）

后续按独立任务实施 Phase 10 bootstrap；当前仅保留用户的 `.claude/skills/backend-module/SKILL.md` 工作区修改。

## Last Checkpoint
2026-09-04 · IAM LIFECYCLE ALIGNMENT / ACCEPTANCE：九模块自治、稳定能力 wiring、单构造路径和 architecture 门禁完成；最终全仓测试、独立架构测试、schema-lock、fmt/lint 全部通过；非 skill 改动已提交。

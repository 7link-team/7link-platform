# Task State

## Task
IAM 产品完整性深度审计、初始化方案、测试命名治理与模块自治修正。

## Objective
在已完成固定源树迁移等价的基础上，区分迁移完成度与可部署 IAM 产品完整度；补充安全初始化方案，
审计 HTTP/gRPC、接口/数据/菜单/字段权限及 9 个 IAM 模块，并落实测试命名和 module activation 治理。

## Current Phase
ACCEPTANCE

## Current Step
本轮深度审计、初始化方案、测试命名治理与 IAM module 自治修正已完成并通过最终验收；Phase 10 产品能力等待实施。

## Overall Progress
- [x] 恢复仓库事实、固定源树与既有三引擎/208 HTTP 验收证据
- [x] 深审平台/租户自举、HTTP/gRPC、接口/数据/菜单/字段权限和产品生命周期
- [x] 设计平台 bootstrap 与 tenant owner bootstrap 的三引擎验收方案
- [x] 重命名历史违规测试，并增加同目录 production stem 架构门禁
- [x] 删除 composition root 的 IAM/federation 条件 append，改为 9 模块自行 `Enabled()`
- [x] 完成相关回归和生成零 diff 验证
- [x] 完成 staged diff 审阅和本轮最终验收

## Active Work
无进行中的代码修改。后续按 Phase 10 优先处理平台/租户 bootstrap；不得触碰或提交 `.claude/skills/**`。

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

## Remaining Work
1. 实现已明确方案的 Phase 10.1 平台管理员 bootstrap 与 10.2 tenant owner bootstrap。
2. 确认 principal 权限所有权和字段权限模型后实施 10.3–10.5。
3. 排期 consent/archive/lifecycle/key rotation；仅在产品要求双协议时设计 IAM gRPC。

## Important Decisions
- migration 只拥有 schema，不写固定账号、默认密码或环境相关秘密。
- 平台 bootstrap 必须显式、create-only、跨 authn/core/audit 原子化；密码仅 file/stdin。
- tenant 创建必须原子创建 owner member、Administrator role、`tenant_administer` grant、role member 和 15 个默认菜单。
- permission catalog 是代码声明，不做数据库 seed；其他租户业务对象不做全局 seed。
- 固定源无 IAM gRPC；除非产品明确要求双协议，不创建占位 proto/service。
- 字段权限和 principal 权限所有权属于新增产品语义，本轮只记录方案，不静默扩 schema 或改 API。
- composition root 只登记 inventory；activation 属于 module，依赖有效性属于 registry。

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
Tests: PASS（自治最终实现后的 `go test ./...`、9 个 IAM/app 定向包、独立 architecture；此前 `make ci` 总覆盖率 90.0%，9 个 IAM root package 均 ≥90%）
Lint: PASS（自治最终实现后的 `make fmt-check lint`；此前完整 `make ci`）
Integration: PASS（本轮三引擎 migration 503.785s；真实 Docker MySQL 8.4 migration/performance/schema lock）
Other: PASS（`make generate` 零生成 diff；`git diff --check`；schema lock 147 files / 63 tables / 27 FKs）

## Next Action
按 Phase 10.1/10.2 实施安全自举；在修改权限 schema/API 前先确认 Phase 10.3/10.5 的产品语义。

## Last Checkpoint
2026-09-04 · IAM PRODUCT AUDIT / ACCEPTED：专属 Config + Enabler 已替代 composition-root gate；全仓测试、architecture、fmt、lint 及既有完整 CI/三引擎验证均 PASS。

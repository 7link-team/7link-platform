# Task State

## Task
IAM 域全量迁移：chaosplus `apps/server` → 7link-platform `apps/backend-api`

## Objective
按 `apps/backend-api/docs/superpowers/plans/2026-09-02-iam-domain-migration.md` 将 9 个 IAM 域模块与
63 张表全功能迁入 gorm/gorm-gen/goosex/pkg-module 架构，修复源 SQL 缺陷，并通过
PostgreSQL、MySQL、SQLite 真实迁移与 `make ci` 门禁。

## Current Phase
IMPLEMENTATION

## Current Step
Phase 0 / Task 0.2：迁移 policyx 纯逻辑与 gorm revision store。

## Overall Progress
- [x] 深度分析与迁移方案 v3
- [x] 两轮独立评审问题回收并修订方案
- [x] Docker / collation / entity type 三项决策闭环
- [ ] Phase 0 前置修复、扩展包、端口图与依赖评审
- [ ] Phase 1 单一 schema 流与三引擎验证
- [ ] Phase 2–8 Go 模块迁移
- [ ] Phase 9 集成与验收

## Active Work
Task 0.0/0.1 已实现并通过完整门禁；下一步检查源 policyx 与目标可复用实现。

## Completed Work
- 已从任务记录与实际仓库恢复断点；工作树在本轮开始时干净。
- Docker Desktop 已实测可用：`~/.docker/bin/docker`, Server 29.7.2。
- R16 已确定：业务名称 `lower(trim)` 且不折叠重音；枚举、协议标识符、哈希/密钥、
  WebAuthn handle 使用 binary collation。
- R9'' 已由源代码确认：实体存在关系时本就不能修改 type；复合外键不新增产品限制。
- Task 0.0：一次迁移只捕获一个 writer；迁移状态预检查不再推进 writer 轮询。
- Task 0.1：SQLite DSN 默认注入 `_foreign_keys=on`；显式关闭、冲突或非法值在启动时拒绝。
- 回归测试覆盖双 writer 落点、迁移状态落点、三条池连接 PRAGMA、孤儿写入与 DSN 边界。

## Important Decisions
- 目标为全量、全功能迁移；SAML/SCIM 不允许长期 Disabled。
- 63 表由 `internal/iamschema` 单一迁移流拥有，目标是全新空库，无存量 ETL。
- 多写者是现有正式契约；Task 0.0 只修同一次迁移的 writer 句柄漂移，不禁用多写者。
- 动态菜单多语言在源库与目标库均不存在，不得未经确认静默扩展为第 64 张表。

## Confirmed Constraints
- 三方言 DDL 全部前缀化、真实时间类型、无 0 哨兵、FK/guid 使用 BIGINT。
- 每个新增受控模块覆盖率 ≥90%，阶段结束 `make ci` 必须通过。
- 不迁移存量数据；不缩减现有 IAM 域功能。
- 当前执行环境不自行启动子代理；按依赖顺序直接执行并分阶段 checkpoint。

## Known Issues / Risks
- 动态菜单多语言是否纳入本次迁移尚待用户确认；不阻塞 Phase 0，但阻塞 Phase 1
  的表集合冻结。
- 多写者 routing mode 与读副本接线仍是独立架构决策，不在 Task 0.0 中扩大范围。
- 本任务约 6 万行，必须按 Phase 分批实现、验证和留档。

## Verification
Build: PASS（`make ci` build）
Tests: PASS（相关完整包 + `make ci`）
Lint: PASS
Coverage: PASS（总体 90.8%，15/15 受控包 ≥90%）
Integration: PASS（`tests/goose`, 34.066s）
Other verification: `git diff --check` PASS；Docker 29.7.2 PASS。

## Next Action
检查源 `policyx` 的条件/成员规则/revision store 及目标 gorm 事务、锁与前缀模式；按
Task 0.2 先移植纯逻辑测试，再实现可并发验证的 revision store。

## Last Checkpoint
2026-09-02 18:29 +08 · Task 0.0/0.1 实现完成，`make ci` PASS，已提交

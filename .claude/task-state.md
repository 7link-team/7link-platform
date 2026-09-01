# Task State

## Task
backend-api 生产级评审后续修复（用户选定 1 / 2 / 4 + lint 发现清理）

## Objective
1. 修复 audit 脱敏漏 camelCase（安全）
2. 修复 wuid 分配锁挪用 worker 租约（可用性）
4. 引入 golangci-lint 配置并清理其报出的发现

## Current Phase
ACCEPTANCE

## Current Step
全部完成并通过验收验证。无待决事项。

## Overall Progress
- [x] 全仓评审（架构/质量/模式/框架/治理/成长）并交付结论
- [x] 修复 1：audit `normalizeKey` 分隔符归一化 + 2 个回归测试
- [x] 修复 2：wuid `allocLockTTL=15s` + `WithAutoRefresh` + 1 个回归测试
- [x] 修复 4a：新增 `.golangci.yml`（21 linter）+ Makefile `lint` 目标
- [x] 修复 4b：53 条 lint 发现全部清零
- [x] 决策 D1：邮箱/手机号不入默认脱敏表（用户授权代定，见 decisions.md）
- [x] 决策 D2：golangci-lint 钉入 tools/go.mod v1.64.8（用户授权代定）

## Active Work
无。实现与验证均已完成。

## Completed Work
已验证通过（实测，非推断）：
- `go build ./...` PASS
- `go vet ./...` PASS 零告警
- `go test ./...` PASS 30/30 包 exit 0
- `make test-coverage` PASS exit 0，总体 92.1%，13 个受控模块全部 ≥90%
  （audit 92.1% / wuid 90.6%）

## Important Decisions
- wuid 分配锁采用「短 TTL + 自动续租」而非单纯缩短 TTL：只缩 TTL 会让慢分配
  期间锁被他人抢占；自动续租使「崩溃后 15s 释放」与「慢分配不被打断」并存。
- `.golangci.yml` 显式禁用 testifylint 的 `require-error` 规则（原 181 条）：
  该项目一致采用 assert 做错误断言以便单次运行报出全部失败，改 181 个调用点
  属风格变更而非正确性修复。其余 testifylint 规则全部保留。
- 邮箱/手机号未入默认脱敏表：它们常是审计对象本身，全脱敏会废掉审计价值；
  正确方案是掩码而非抹除。等用户决策。

## Confirmed Constraints
- 用户选定范围 = 评审发现的 1、2、4 三项；第 3 项（接 CI）明确不做。
- 不得为了让 lint 通过而放宽测试断言（CLAUDE.md §33）。

## Known Issues / Risks
- float-compare(15) 改 InDelta 需逐个选取 delta，属判断题，不可机械替换。
- 无未闭合风险。lint 版本已钉入 tools/go.mod。
- 仍缺 CI（用户明确排除在本次范围外）：所有门禁仍靠本地自觉执行。

## Verification
Build: PASS
Vet: PASS（零告警）
Tests: PASS（30/30 包，exit 0）
Lint: PASS（`golangci-lint run ./...` exit 0，0 findings）
Coverage gate: PASS（exit 0，总体 92.1%，13/13 模块 ≥90%）
Integration: PASS（`make test-integration` exit 0，真实 embedded-postgres + go-mysql-server）

## Next Action
本任务已交付完毕。若继续推进生产化，下一步建议是接 CI 强制
`make lint` / `make test-coverage` / `make test-integration`（评审第 3 项，
用户本次明确不做），其后才是补 authn/authz。

## Last Checkpoint
2026-09-01 · 1/2/4 + D1/D2 全部完成；lint 清零并钉版本，全部门禁绿

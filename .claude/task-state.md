# Task State

## Task
关闭 IAM 功能性生产就绪残留项（原 2026-09-07 审计 P1-4/P1-5/P1-6）。运维类（部署物、灾备、RTO/RPO、
告警路由、签字）按用户指示排除，除非属于集群下的功能问题。

## Objective
1. (a) 部分权限用户不再整页失败：可选数据集按权限跳过 + allSettled 兜底降级。
2. (b) 写操作按钮按真实权限裁剪：新增 `GET /iam/me/permissions`，前端按 `resource_verb` 裁剪。
3. (c) 触控目标 44px；顺带关闭同一审计项的移动导航与 Sheet 无障碍缺口。

## Current Phase
ACCEPTANCE

## Current Step
应用/代码范围内三项全部实现并实证通过；待用户决定是否提交推送 origin/IAM

## Overall Progress
- [x] 后端：core `EffectivePermissions`（tenant `CheckBulk` + platform `CheckPlatform`）、
      `GET /iam/me/permissions`（`RegisterTenantMember`，与 `/iam/me/menus` 同形状）、
      `AuthorizationEvaluator` 嵌入既有 `authz.PermissionChecker`、3 个新测试、
      路由清单 59→60、app 映射/守卫 fixture 257→258
- [x] OpenAPI 272（+1，无删除、无既有 schema 变化）+ `api-schema.ts` 再生成
- [x] 前端 `lib/permissions.ts`（`can`/`useAllowed`；未解析→隐藏，读取失败→放行）+ layout 经 Outlet 下发
- [x] 16 个管理页写操作裁剪 + 6 处可选数据集降级（SCIM、平台关闭队列读写同码，裁剪为 no-op 故跳过）
- [x] 触控目标（globals.css `pointer: coarse` → 44px）、移动导航点链接后关闭、Sheet sr-only 标题
- [x] route-matrix 新增 admin/reader 写控件断言（exact + visible）；792/792 PASS
- [x] 个人安全 12/12、个人生命周期 6/6 PASS

## Important Decisions
- D-a：端点用 `RegisterTenantMember` + 必填租户 header。原打算 `RegisterAuthenticated` + 可选 header，
  但 `http_contract_test` 钉住契约（authenticated 类不得带租户 header 且必须 sessionCookie-only）。不弱化测试。
  平台管理员无成员身份时 403 → 前端 permissions 为 null → 放行显示，与改动前一致，无回归。
  响应含调用者持有的 platform code，故平台页按钮也能正确裁剪。
- D-b：`permissions` 三态。未解析（首次加载中）→ 隐藏写操作；解析失败 → 放行（服务端仍拒绝）；
  已解析 → 严格裁剪。原二态方案在浏览器矩阵里暴露为按钮闪现，已按此修正。
- D-c：触控目标用 CSS `@media (pointer: coarse)` 作用于 `[data-slot="button"]`，不改桌面 h-6..h-9 密度。
- D-d：切换开关（角色权限/成员/数据范围）用 disabled 而非隐藏，保留"当前是否已授予"的可读性；
  独立动作按钮才隐藏。

## Confirmed Constraints
- 不碰运维/部署/灾备条目；不改后端授权模型与既有 Guard；不新建授权框架。
- 生成物（openapi.json、api-schema.ts）必须经生成器产出。
- 浏览器脚本在本机须用 `node` 运行：bun 1.3.14 下 playwright-core 解析 Set-Cookie 抛
  `ERR_INVALID_URL`（环境问题，与本轮改动无关，未改脚本 URL 逻辑）。

## Known Issues / Risks
- `make test-alerts` 本机无 promtool，未执行（本轮未改告警规则）。
- `make test-mysql8-run`、`make test-redis-security` 未重跑：本轮无迁移与 Redis 改动。

## Verification
后端：fmt-check PASS；lint PASS（主模块 + tests 子模块各 0 findings）；test-coverage PASS（总 90.1%，
全部受管模块 ≥90%）；test-integration PASS（architecture 4.1s、goose 108.9s、testdb 2.4s、lifecycle 369.1s、
migration 869.0s、performance 104.5s）；test-race PASS 无 DATA RACE；build PASS；openapi-check PASS；
generate-check PASS。
前端：typecheck / lint（0 error 0 warning）/ test（62 IAM + 85 UI）/ build PASS；改动文件 prettier clean。
浏览器（真实 UAT，新 binary）：route-matrix 792/792 PASS（22 路由 × 3 语言 × 1440/390 × admin/reader/denied
× 2 租户，含新增写控件存在/缺失断言）；personal-security 12/12 PASS；personal-lifecycle 6/6 PASS。
过程中浏览器断言查出两个真实缺陷并已修：access-reviews 守卫误写成 JSX 字面文本；按钮在权限解析前闪现。

## 保留环境
UAT 已刷新：旧环境按 `reset --preserve-old` 保留（后缀 2026-09-10T11-12-14-602Z）。
新 UAT 运行中：console http://127.0.0.1:18091、API http://127.0.0.1:18080，revision e18e1aa(+本轮改动)。
浏览器报告在 `apps/backend-api/.temp/iam-uat/browser/{route-matrix,personal-security,personal-lifecycle}/report.json`。

## Next Action
等用户确认后提交推送 origin/IAM：精确暂存 36 个修改 + 3 个新增文件（无构建产物/日志/临时文件），
Conventional Commit，并核对远端 SHA。若要求更完整门禁，再跑 `make release-check`
（含真实 MySQL 与 Redis 两拓扑矩阵，需 Docker 与 promtool）。

## Last Checkpoint
2026-09-10 三项功能缺口实现 + 后端/前端/浏览器门禁全绿，进入交付确认

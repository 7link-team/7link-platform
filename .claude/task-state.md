# Task State

## Task
IAM Phase 10.1 收尾：平台超管走命令行 bootstrap + `reset-admin-password` 破窗改密（用户 2026-09-05 决策，
参考 Grafana 的 `grafana-cli admin reset-admin-password`）。

## Objective
让「唯一一个没有上级、也没有可信邮箱可走找回流程」的平台超管账号，可以由能在服务器上执行命令的人恢复访问；
同时保证它不是一个万能改密后门：只认持有 full platform administration 的那一个 principal，且只在其 active 时生效。

## Current Phase
VERIFICATION

## Current Step
四层实现与测试全部完成，`make ci` 运行中（本地 fmt / vet / 三包测试均已通过）。

## Overall Progress
- [x] `internal/iam/authn/repository_credentials.go`：`resetPassword`（不校验旧哈希、bump version、清锁定）
      与 `passwordHistoryDepth` 常量（recovery.go / web.go 的字面量 5 一并收编）
- [x] `internal/iam/authn/bootstrap.go`：`ResetBootstrapCredentialOn`，`BootstrapCredentialStore` 增加 `Reset`
- [x] `internal/iam/authn/module.go`：`Reset` 闭包接线（与 `Create` 同形，未接线时 `ErrUnavailable`）
- [x] `internal/app/platformadmin.go`：抽出 `startForPlatformAdministration`，bootstrap 与 reset 共用启动
- [x] `internal/app/platformadminreset.go`：`RunPlatformAdminPasswordReset` + `reset` + `resetPassword`
      + `requireFullAdministrator` + `holdsFullPlatformAccess`
- [x] `cmd/7link-api/cmd/iam.go`：`reset-admin-password` 子命令，新增 `exitNotBootstrapped = 7`，
      `bootstrapExitCode` 更名 `platformAdminExitCode`
- [x] 三层测试：`bootstrap_reset_test.go`、`platformadminreset_test.go`、`iam_reset_test.go`
- [ ] `make ci` 全绿
- [ ] 提交推送（10.1 单独一个提交）
- [ ] §2.5.2 要求的「存量零成员租户」修复命令（`IsUnclaimed` 已实现，尚无调用方）

## Completed Work
- Phase 10.2 已完成并推送（提交 `24f2483`）：新建租户在同一事务内完成 owner bootstrap。
- `internal/iam/federation/state.go` 的品牌串 AAD 已改为协议常量 `iam:federation:state`（提交 `47e4b07`）。
- 本轮 reset 语义：换密码 → 旧哈希入 history → bump `credential_version` → 清 `failed_attempts` / `locked_until`
  → 撤销全部 session 与 refresh token → 作废未消费的找回令牌 → 写审计
  `platform_administrator_password_reset`（Detail 含 `credential_existed` / `sessions_revoked`）。
  **不碰 MFA 注册**，这是命令 `Long` 文案里显式写明的边界。
- 覆盖率：`internal/iam/authn` 90.1%（逐包门禁包内，余量极薄但守住）；`internal/app` 89.7%，
  新文件 `platformadminreset.go` 自身 93%+；`cmd/7link-api/cmd` 82.1%（两者只计总覆盖率）。

## Remaining Work
- CI 绿后按显式路径提交（暂存区里有他人的文档改名，禁止 `git add .` / `-A`）。
- §2.5.2 的存量租户修复命令。
- 10.3 待用户拍板（见下）。

## Important Decisions
- 密码永不作为命令行参数：只接受 `--password-file`（非 Windows 上必须 0600）或标准输入。
- reset 只作用于 full platform administrator 且 principal 必须是 `active`；
  disabled / locked 的 principal 报 `ErrPlatformAdminConflict`，要求先恢复账号再改密。
- 「管理员存在但没有 credential」这一 bootstrap 拒绝触碰的状态，由 reset 负责修复（写第一条 credential），
  审计里用 `credential_existed=false` 区分「轮换」与「修复」。
- 退出码按运维下一步动作区分：3 已 bootstrap / 4 冲突 / 5 未迁移 / 6 暂时不可用 / 7 尚未 bootstrap。
- 跨模块只走窄事务能力（`func(ctx, *gorm.DB, ...)`，Provide 阶段发布、Inject 阶段解析）。
- 用户状态词汇是 `active` / `disabled` / `locked`（schema CHECK 约束），不存在 `suspended`。

## Confirmed Constraints
- 提交必须逐文件显式暂存；工作区有若干非本轮改动的文件等用户处置
  （`.gitignore`、`README.md`、`pkg/interpreter/interpreter.go`、4 个已暂存的文档改名、未跟踪的 `docs/README.md`）。
- migration 不写固定账号或默认密码。
- 不在代码里硬编品牌相关字符串（用户 2026-09-05 规则）。`cp_session` / `cp_federation_state` 这一族
  cookie 名留给 @ClaudeWEB 的品牌可配置改造，本轮不动，避免撞车。

## Known Issues / Risks
- 逐包 90% 覆盖率门禁余量极薄（authn 90.1%、organization 90.0%、core 90.1%），后续新增生产代码必须同批补测。
- 10.3 principal 归属未决：全局账号的 6 个接口（list/create/get/update/suspend/resume）挂在租户注册器上，
  其中 3 个却要 `platform:administer`，平台管理员与租户管理员**都不可达**。已两次向用户说明方案 A
  （整体挪到平台注册器），@ClaudeWEB 亦独立给出同一建议，等一句「同意」。
  证据：`organization-accept-invitation` 是 `RegisterPublic` 的公开端点、自己创建 principal，
  且 `POST /iam/members` + `PATCH /iam/members/{subject}` 已覆盖租户内增删禁用，
  所以挪走这 6 个接口不会拿掉租户管理员真正需要的能力。

## Verification
Build: PASS（`go build ./...`）
Tests: PASS（`go test ./cmd/... ./internal/app/... ./internal/iam/authn/...`）
Lint: 待 `make ci` 确认
Integration: 待 `make ci` 确认
Other: 本轮不改 HTTP operation 集合，冻结契约（`iam_http_mappings.txt` 211 条）预期不受影响。

## Last Checkpoint
2026-09-05 · PHASE 10.1 / VERIFICATION：`iam reset-admin-password` 四层实现 + 三层测试完成，
`make ci` 运行中；绿后单独提交推送，然后回到 §2.5.2 存量租户修复命令，10.3 等用户拍板。

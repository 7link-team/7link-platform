# Decisions

## D1 — 审计脱敏默认表只覆盖凭据与高危标识，不覆盖联系类 PII

**Decision**
`audit.defaultSensitiveKeys` 保持现有范围（口令、令牌、密钥、授权头、卡号、CVV、
身份证、SSN 等）。邮箱、手机号、姓名、地址等联系类 PII **不**进入默认表，由部署方
按合规要求通过 `audit.sensitive_fields` 配置自行加入。

**Reason**
两类数据的审计价值截然不同。凭据被记录下来毫无用处而泄漏代价无限，全脱敏是纯收益。
联系类 PII 则常常**就是被审计变更的对象本身**（注册、改绑、客服修改联系方式、按邮箱
排查欺诈），默认抹除会削弱审计本身的取证价值。日志中的抹除不可逆：未记录的值无法
事后恢复，因此默认值偏向"记录"，把收紧留给显式配置。

注：审计事件的 `actor_id` / `client_ip` / `operation` 独立于 body，脱敏 body 不影响
"谁在何时做了什么"的追溯，只影响"改成了什么值"。

**Alternatives Considered**
1. 加入默认表 —— 简单，但如上所述削弱审计价值，且是不可逆的默认。
2. 新增掩码能力（`a***@b.com` / `138****5678`）—— 技术上最优，兼顾关联性与暴露面控制。
   **未实施**：属新功能，需先设计 `MaskedFields` 配置与各类型掩码规则，超出当前范围。
   若合规要求出现，这是推荐方向，而非改默认表。

**Tradeoffs**
审计日志会包含联系类 PII，因此审计存储（DB 表 / slog sink）必须与主数据同级的访问
控制和保留期限管理。这是 GDPR/PIPL 对审计日志的通行要求，而非"不准记录"。

**Scope**
`internal/infra/audit`。边界说明已写入 `redact.go` 的 `defaultSensitiveKeys` 文档注释，
避免后续被当作疏漏"修复"。

**Status**
已确认（用户授权代为决定）。若未来实施掩码能力，本决策需一并复审。

---

## D2 — golangci-lint 钉入 tools/go.mod

**Decision**
将 `github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8` 加入 `tools/go.mod`
的 `tool` 块，`make lint` 改用 `go tool -modfile=tools/go.mod golangci-lint run ./...`。

**Reason**
与该模块既有规范一致（buf、protoc-gen-go、protoc-gen-go-grpc 已同样钉版本）。在没有
CI 强制的前提下，版本漂移是唯一的实际风险：不同开发者本地安装不同版本会得到不同发现，
使 lint 结论不可复现。钉版本后无需本地安装即可运行。

**Alternatives Considered**
1. 不钉，依赖开发者本地安装 —— 零成本但不可复现，且与仓库既有工具管理方式不一致。
2. 钉在 CI 而非 tools/go.mod —— 当前无 CI（用户明确不做），不适用。

**Tradeoffs**
`tools/go.mod` 由 104 行增至 272 行（全部为 indirect 依赖）。该模块本就为隔离工具依赖
而独立存在，不进入运行时依赖图，因此代价可接受。已实测：钉定后运行结果与本地 v1.64.8
完全一致（0 findings）。

**Scope**
`apps/backend-api/tools/go.mod`、`Makefile`、`tools/README.md`。

**Status**
已确认（用户授权代为决定）。

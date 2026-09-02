# Operation Audit (audit)

Transport-independent 操作日志：记录谁、何时、通过什么入口、对什么资源、做了什么、结果如何。
HTTP 与 gRPC 已自动接入；其它入口（定时任务、分布式任务、MQ 消费者、WS/SSE 会话）通过通用
包装器一行接入。双写落地：结构化日志（slog，常开）+ 数据库（`operation_logs`，可配置）。

## ID 约定（业务主键一律雪花）

`operation_logs` 遵循平台"所有业务表主键 / `xx_id` 一律雪花 ID"约定：

- `id`（事件主键）、`resource_id`、`actor_id`、`tenant_id` → **BIGINT 雪花 ID**
  （`guid.Source.Next()` / `guid.ID`）。`resource_id` 由业务代码经 `audit.Resource(kind, id)` 传入，
  必须是平台雪花 ID。若出现非雪花/非数字标识（如外部实体 UUID），数值列保持 0，
  原文会以 `metadata.raw_<field>` 保留（`raw_resource_id` / `raw_actor_id` / `raw_tenant_id`），
  避免静默丢数据。
- 传输/可观测标识符保持文本：`request_id`（X-Request-ID）、`session_id`、`trace_id`、`span_id`、
  `client_ip`、`user_agent`——它们不是业务实体 ID（"除非特定业务需要"）。

## 架构

```
       入口适配层                    核心（传输无关）                   落地
┌────────────────────┐      ┌──────────────────────────────┐    ┌─────────────┐
│ HTTP 中间件          │      │ Policy   记不记 / 读写分类     │    │ slog Sink   │
│ gRPC 拦截器          │─────▶│ Redact   脱敏 + 截断 + 类型闸  │───▶│ (常开)      │
│ audit.Do/Run        │      │ Recorder 异步队列/批量/drain   │   │ gorm Sink   │
│  ↳ job/schedule/mq  │      │  ↳ service registry (DI)     │    │ (可配置)     │
└────────────────────┘      └──────────────────────────────┘    └─────────────┘
```

## 已有入口

| 入口 | 位置 | 说明 |
|---|---|---|
| HTTP | `internal/app/http.go` | 写操作记录元数据；显式开启正文捕获后才记录脱敏 JSON；WS/SSE 按会话记录 |
| gRPC | `internal/app/grpc.go` | unary + stream 拦截器；方法名前缀启发式读写分类 |

跳过路由（不审计）：`/healthz /livez /readyz /metrics /debug /docs /openapi /`（可用
`audit.skip_routes` 追加）。读操作（GET / List / Query…）默认不记录，`audit.record_reads=true`
开启；敏感读可在业务代码里用 `Annotate` 强制记录。

## 配置

```yaml
audit:
  enabled: true            # 总开关，默认 true
  database:
    enabled: false         # 落库；需先运行 goose 迁移（make dev 带 --migrate 自动执行）
  record_reads: false      # 是否记录读操作
  record_streams: true     # 是否记录 WS/SSE 会话
  capture_bodies: false   # 是否捕获脱敏后的 JSON 正文；默认关闭
  max_body_bytes: 8192     # 请求/响应体单方向捕获上限（字节）
  sensitive_fields: []     # 额外需要脱敏的 JSON 字段名
  skip_routes: []          # 额外跳过的路径前缀
  queue_size: 4096
  batch_size: 200
  flush_interval: 1s
  flush_timeout: 5s
  overflow: drop           # drop | block
```

> `audit.enabled` 默认 `true`，但只记录操作元数据；`capture_bodies` 默认 `false`。开启正文捕获前，
> 应先确认数据分级与保留策略，并把内置集合以外的 PII/敏感字段加入 `sensitive_fields`。

显式配置 `audit.database.enabled=true` 时，启动阶段会验证 writer 和 `operation_logs` 表；不可用会
启动失败，不会静默降级成只有 slog。事件 ID 优先使用平台雪花 ID（`guid.Source.Next()`，Sonyflake）；
生成器暂时不可用时使用 UUID 兜底，保证 slog 事件不丢。BIGINT 数据库 sink 会明确拒绝 UUID，
并增加 sink error 计数，绝不会把非法 ID 写成主键 0。
Prometheus：`audit_events_flushed_total / dropped_total / failed_total`、
`audit_queue_depth`、`audit_sink_errors_total`。

## 如何在业务代码中补充审计上下文

HTTP/gRPC 的中间件已自动打开审计上下文，业务代码在任意深度补充信息：

```go
audit.Annotate(ctx,
    audit.Action("order.cancel"),
    audit.Resource("order", orderID.String()),
    audit.Tenant(tenantID),
    audit.Metadata("reason", reason),
)
```

`Annotate` 在上下文没有在途审计条目时是 no-op，所以可以无条件调用。
认证中间件（尚未实现）可用 `audit.WithActor(ctx, actor)` 提前放置主体，或
`audit.Annotate(ctx, audit.SetActor(actor))` 覆盖。

## 如何在未来入口接入（定时 / 分布式任务 / MQ / 消费者）

从服务注册表解析 `audit.Recorder`（`module.Resolve[audit.Recorder]`），然后：

### 有返回值的操作 —— `audit.Do`

```go
rec, _ := module.Resolve[audit.Recorder](services)

result, err := audit.Do(ctx, rec, audit.Spec{
    Entry:        audit.KindJob,          // job | schedule | message | internal | 自定义
    Operation:    "inventory.reconcile",
    ResourceType: "inventory",
}, func(ctx context.Context) (ReconcileResult, error) {
    // 内部可继续 audit.Annotate(ctx, ...)
    return reconcile(ctx)
})
```

### 无返回值的任务 —— `audit.Run`

```go
err := audit.Run(ctx, rec, audit.Spec{Entry: audit.KindSchedule, Operation: "cron.cleanup"}, func(ctx context.Context) error {
    return cleanup(ctx)
})
```

新的入口类型无需改动审计核心；`EntryKind` 是开放字符串，可自定义（如 `"mq.kafka"`）。

### MQ 消费者示例

```go
func (c *consumer) handle(ctx context.Context, msg Message) error {
    _, err := audit.Do(ctx, c.recorder, audit.Spec{
        Entry:        audit.KindMessage,
        Operation:    "order.created.handle",
        ResourceType: "order",
        ResourceID:   msg.OrderID,
    }, func(ctx context.Context) (struct{}, error) {
        return struct{}{}, c.process(ctx, msg)
    })
    return err
}
```

## 记录规则

- 请求体：`capture_bodies=true` 时，仅写操作 + `Content-Type` 为 `application/json` 或 `*+json` + 未超 `max_body_bytes`。
  超限 → `request.omitted=too_large`，仍记录 size；声明为 JSON 但格式非法 → `invalid_json`；
  非 JSON → `unsupported_content_type`。超限或非法 JSON 均不保留正文，避免未脱敏内容落地。
  决定不捕获时**完全不读**请求体（文件上传/大流零开销）；捕获后用 `io.MultiReader` 还原。
- 响应体：`capture_bodies=true` 时，成功 → 仅 envelope 的 `code/message` + 从 `data.id` 提取的资源 ID
  （`response.omitted=success_summary`）；失败（≥400 / gRPC error）→ 未超限的合法 JSON 错误体会脱敏记录，
  超限或非法 JSON 错误体仅记录 size 与 omission 原因。
- 脱敏：大小写不敏感的敏感键集合（password/token/secret/authorization/api_key/credit_card 等，
  可配置扩展）→ 值替换为 `[REDACTED]`。请求头不整体记录，只取 User-Agent / X-Request-ID；
  Authorization / Cookie 永不落地。
- 异步管道：有界队列 + 批量落库；`overflow=drop` 时队列满丢弃并计数 + 限流 WARN。
  任一 sink 失败都会增加 `audit_sink_errors_total`；至少一个 sink 成功时事件仍视为送达。
  关闭时（HTTP/gRPC 已停）在 shutdown 期限内 drain 并 flush。关闭 audit 时仍提供 no-op `Recorder`，
  依赖它的业务模块无需增加条件装配。

## 迁移

`sql/{sqlite,mysql,postgres}/00001_operation_logs.sql`，goose 版本表 `goose_audit`。

`00002_operation_logs_append_only.sql` 通过三方言触发器拒绝 `UPDATE` 和 `DELETE`。该保护不拦截
`TRUNCATE`，高权限数据库账号也可以禁用或删除触发器；完整的 append-only 保证仍要求生产
业务账号遵循最小权限，不授予 `TRUNCATE` 或触发器管理权限。
独立执行：`go -C cmd/7link-api run . migrate up --module audit`（或 `--migrate` 随服务启动自动执行）。

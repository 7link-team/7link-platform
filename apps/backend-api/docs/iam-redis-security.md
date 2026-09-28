# IAM 共享安全存储的 Redis 配置

`authn.shared_security` 支持 Redis 6.2+ 单机、Redis Cluster 与 Sentinel。
验证码及认证限流共享同一客户端；未提供任何 Redis 设置时保留原进程内验证码实现。
提供不完整或冲突的 Redis 设置会导致配置失败，不会静默回退。

## 配置

兼容现有 `redis_url_file`：文件内容为完整 `redis://` 或 `rediss://` URL。
此方式只连接单机，不能与下面的拓扑、认证或 TLS 字段混用。
凭据及 TLS 文件沿用平台 `secretx` 的普通文件检查和 64 KiB 大小限制，不能将密码放进地址列表。
部署时应限制密码和私钥文件的读取权限。

```yaml
authn:
  shared_security:
    redis_mode: cluster
    redis_addresses:
      - redis-a.internal:6379
      - redis-b.internal:6379
    redis_username: application
    redis_password_file: /run/secrets/redis-password
    redis_tls_enabled: true
    redis_tls_ca_file: /run/secrets/redis-ca.pem
    redis_tls_cert_file: /run/secrets/redis-client.pem
    redis_tls_key_file: /run/secrets/redis-client-key.pem
    namespace: iam-security
    failure_policy: closed
    request_timeout: 2s
    window: 1m
    per_ip: 60
    global: 3000
```

`redis_mode` 为 `standalone`、`cluster` 或 `sentinel`，省略时为单机。
单机必须且只能提供一个地址；Cluster 即使只有一个种子地址也必须显式选择 `cluster`。
Cluster 仅支持 `redis_db: 0`，其他模式可选择非负数据库编号。
地址必须为 `host:port`，IPv6 使用 `[address]:port`。

Sentinel 配置使用发现节点地址，并单独配置发现服务凭据：

```yaml
redis_mode: sentinel
redis_addresses:
  - sentinel-a.internal:26379
  - sentinel-b.internal:26379
  - sentinel-c.internal:26379
redis_master_name: primary
redis_username: application
redis_password_file: /run/secrets/redis-password
sentinel_username: discovery
sentinel_password_file: /run/secrets/sentinel-password
```

Sentinel 的发现凭据和 Redis 数据节点凭据互不替代。
应用必须能够访问发现服务返回的节点地址；Cluster 亦须能访问全部公告地址。
凭据文件在客户端构造时读取，轮换后需要重建应用客户端。

TLS 最低版本为 1.2，始终验证证书链及主机名。省略 CA 文件时使用系统信任库。
客户端证书和私钥必须成对提供；需要 mTLS 时同时配置二者。
`redis_tls_server_name` 可指定证书验证名称；省略时按实际连接的节点名称验证。
当前 go-redis 的 Sentinel 实现为发现服务与数据节点使用同一 TLS 配置，
因此只支持两者均明文或均 TLS 的拓扑，不支持明文 Sentinel 配 TLS 数据节点。
所有节点须满足相同的信任和客户端身份约束。

## 失败及一致性边界

启用 Redis 时必须明确选择 `failure_policy: open` 或 `closed`：
限流存储错误分别放行或返回 503；达到额度返回 429 和 `Retry-After`。
验证码存储失败始终拒绝签发或验证，不按 `open` 放行。
操作使用配置的请求期限、连接及读写期限；模块关闭时释放客户端和连接池。

验证码通过 `GETDEL` 在当前主节点上原子消费；限流通过 Lua 原子更新全局及来源计数。
键保留 `{namespace}` 哈希标签，使一次 Lua 调用的两个键落在同一 Cluster 槽。
这意味着全局限流本身不会随 Cluster 分片水平扩展；所有应用副本必须使用相同 namespace。
不同环境须使用不同 namespace，原始挑战标识及 IP 不直接出现在键中。

客户端关闭普通错误重试，并抑制 Cluster 对不确定网络错误的命令重放；
明确的 `MOVED`、`ASK` 路由及 `NOSCRIPT` 脚本装载仍可执行。
请求响应丢失时，写入可能已执行，调用方只能按存储错误处理，不能保证写入未发生。
Redis 异步复制可能在故障切换时丢失已确认的消费或计数写入，
因此不承诺跨故障切换严格一次性消费或无损额度。这里没有数据库消费账本。

## 本地兼容验证

```sh
bash scripts/test-redis-security.sh
IAM_TEST_REDIS_TLS=1 bash scripts/test-redis-security.sh
go test ./internal/iam/authn -run '^TestShared' -count=1
```

脚本创建唯一名称、无外部网络及宿主端口的临时容器，运行六节点 Cluster、
三个数据节点与三个 Sentinel、一个 mTLS 单机节点。第二条命令对全部拓扑启用 mTLS。
测试二客户端并发消费、Lua 两级额度、ASK/MOVED、真实执行后响应丢失不重放、
受控主节点切换后的新验证码、TLS 主机名/客户端证书/ACL 拒绝路径。
退出时删除本次容器及生成的证书、密码、配置和测试二进制，不接触现有环境。
这是应用客户端兼容检查，不是生产 HA、灾备、RTO/RPO 或运营签字验收。

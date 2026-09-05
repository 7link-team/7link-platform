# Backend API 插件化设计建议

## 结论

当前阶段应优先完成电商核心业务，同时按照未来可插件化的方式设计模块边界；暂不建设完整的动态插件平台。

推荐路线是：

```text
领域模块 + 领域事件 + Provider SPI + AI Tool Gateway
                         ↓
                  Source Plugin
                         ↓
              WASM Dynamic Plugin
```

现在需要设计的是稳定的“插座”，包括业务接口、事件、权限和运行上下文，而不是提前实现插件市场、运行时上传、热更新等完整控制面。等真实扩展需求和业务契约稳定后，再把成熟的扩展点开放给动态插件。

## 为什么不立即建设完整插件系统

电商系统早期的商品、库存、价格、订单、支付和履约模型通常会持续调整。此时提前固化插件 ABI、Manifest、生命周期和数据库协议，容易把尚未验证的业务抽象变成长期兼容负担。

完整动态插件平台还会引入以下持续成本：

- 插件与宿主的版本兼容；
- 安装、启用、禁用、升级、回滚和卸载状态管理；
- 租户、用户和资源级权限隔离；
- 数据库迁移、失败恢复和数据归属；
- 恶意代码、无限循环、内存和网络访问控制；
- 日志、指标、追踪、审计和故障定位；
- 第三方开发文档、SDK、测试和支持。

这些能力只有在多租户差异化、第三方开发或插件商业化成为真实需求后，才会产生足够回报。

## 插件化能带来的业务价值

### 第三方集成

插件或 Provider 可以隔离变化频繁、地区差异明显的能力，例如：

- 支付渠道；
- 物流、运费和轨迹查询；
- ERP、CRM、财务及电子发票；
- 短信、邮件、WhatsApp 等通知渠道；
- 搜索、推荐、风控和 AI 模型服务；
- 外部电商平台和社交销售渠道。

新增或替换集成时，可以避免修改订单、库存等核心领域。

### 商家差异化

不同商家可能需要不同的定价、促销、下单校验、库存分配、履约、售后审批、积分和打印规则。稳定的扩展点可以减少分支版本和客户定制代码。

### AI 应用

AI 更适合调用受控的业务工具，而不是直接生成代码并在 Backend API 进程内执行。建议逐步提供以下工具：

```text
catalog.search
product.get
product.describe
inventory.check
order.query
order.cancel
refund.create
customer.profile
promotion.recommend
shipment.track
```

所有工具调用都应经过租户、身份、权限、审批、幂等、限流和审计控制。工具注册中心可以同时服务内部 AI 应用、外部 Agent、HTTP/gRPC 接口以及未来的 MCP 适配层。

### 插件生态

当系统拥有足够多的客户、合作伙伴和稳定扩展点后，可以形成支付、物流、营销和 AI 插件市场。这属于平台成熟后的商业能力，不应作为当前核心业务开发的前置条件。

## 当前代码基础

`pkg/module` 已具备源码插件所需的大部分基础：

- `Module`：模块身份；
- `Depender`：模块依赖；
- `ServiceProvider` / `ServiceInjector`：服务提供与注入；
- `Initializer` / `Starter` / `Stopper`：生命周期；
- `HealthChecker`：健康检查；
- `HTTPRegistrar` / `GRPCRegistrar`：协议入口注册。

因此，当前业务模块应继续基于 `module.Module` 建设。后续可以增加轻量的插件描述信息，而不改变现有生命周期模型：

```go
type Descriptor struct {
	ID           string
	Version      string
	Dependencies []string
}

type Plugin interface {
	module.Module
	Descriptor() Descriptor
}
```

当前模块仍在 `internal/app/bootstrap.go` 中硬编码注册。近期可以将其调整为源码模块目录或配置驱动的 Source Plugin Catalog，但所有模块仍随主程序编译和部署。

## Interpreter 方案定位

`pkg/interpreter` 是执行引擎适配层，不应直接承担插件发现、授权、生命周期和版本管理。

| 引擎 | 推荐用途 | 是否用于第三方动态插件 |
| --- | --- | --- |
| Wazero | 隔离执行、运行时加载、商业插件 | 是，作为主要动态插件方案 |
| Goja | 促销、校验、字段转换、自动化规则 | 否，仅运行可信脚本 |
| Lua | 设备规则、POS/KDS 小脚本、轻量流程 | 否，仅运行可信脚本 |
| Yaegi | 内部调试、开发期扩展和原型 | 否，不进入生产动态插件体系 |

当前 Goja、Lua 和 Yaegi 只在执行前检查 `context`。脚本进入无限循环后不能可靠中断；绑定 Go 函数也缺少统一的能力授权。因此不能把它们视为不可信代码沙箱。

当前 Wazero 已有内存限制、context 终止和字节 ABI，可以作为未来动态插件的技术基础。不过仍需补齐：

- 插件 Manifest 和 ABI 版本；
- 宿主函数注册和能力授权；
- 编译缓存与实例生命周期；
- 每次请求独立实例或有上限的实例池；
- 输入、输出、执行时间和并发限制；
- Artifact 校验、签名、升级和回滚。

## 当前应建设的核心能力

### 领域模块

建议优先形成以下模块边界：

```text
catalog
inventory
pricing
promotion
cart
checkout
order
payment
fulfillment
customer
notification
```

模块之间依赖窄接口，不直接依赖其他模块的数据库模型或具体实现。

### Provider SPI

以下能力适合优先定义 Provider 接口：

```text
PaymentProvider
ShippingProvider
TaxProvider
NotificationProvider
SearchProvider
AIProvider
FraudProvider
```

先实现两个以上真实 Provider，再根据共同需求稳定接口。Source Plugin 可以提供 Provider 实现；未来 WASM 插件原则上只消费宿主能力，是否允许提供核心 Provider 需要单独评估可靠性和延迟。

### 领域事件与 Transactional Outbox

建议逐步定义：

```text
product.updated
inventory.changed
order.created
order.paid
order.cancelled
refund.requested
refund.completed
shipment.updated
```

领域事件应具备版本、事件 ID、租户 ID、发生时间和幂等键。需要可靠投递的事件通过 Transactional Outbox 发布。未来插件 Hook、Webhook、异步任务和 AI 自动化都可以消费同一事件模型。

### 平台基础能力

无论以后是否实现动态插件，以下能力对电商系统本身都有价值：

- 租户、用户和权限上下文；
- 审计日志；
- 幂等请求；
- 后台任务、超时、重试和死信；
- 配置与 Secret 管理；
- API 和事件版本管理；
- 可观测性；
- AI Tool Registry。

## 未来动态插件架构

当动态插件成为真实需求时，建议采用双模式：

```text
Source Plugin
  -> 随宿主编译
  -> 可实现 Go Provider 接口
  -> 适合核心和长期维护能力

WASM Dynamic Plugin
  -> 运行时安装和启停
  -> 通过稳定 ABI 执行
  -> 通过授权后的 Host Capabilities 访问系统
  -> 适合第三方、商业和高隔离扩展
```

动态 HTTP 路由应在服务启动时注册一个固定入口：

```text
/x/{pluginID}/*
```

请求进入后，由宿主完成路由匹配、认证、租户和权限检查，再将脱敏请求快照发送给 WASM。启用或禁用插件只切换有效版本快照，不动态修改 Chi 路由树。

gRPC 服务不适合运行时注册和卸载。动态插件需要 gRPC 时，可以提供统一的 `PluginService.Invoke`，而不是允许插件注册任意 protobuf 服务。

### Host Capabilities

动态插件不能直接获得 `*gorm.DB`、Router、文件系统或具体 Module，只能调用经过版本化和授权的宿主能力：

```text
runtime.log
runtime.now
runtime.uuid
guid.next
config.get
lock.acquire
lock.renew
lock.release
network.request
data.list
data.get
data.create
data.update
storage.get
storage.put
```

`network`、`data`、`storage` 和 `config` 必须支持域名、表、字段、路径或配置前缀级的资源授权。

## 分阶段实施计划

### 阶段一：核心业务与扩展边界

- 基于 `module.Module` 建设领域模块；
- 定义 Provider SPI；
- 建设领域事件和 Transactional Outbox；
- 建设后台任务、幂等、权限和审计；
- 建设 AI Tool Registry；
- Goja 仅用于可信业务规则。

### 阶段二：源码插件治理

- 增加插件 ID、版本和依赖描述；
- 将硬编码模块注册调整为 Source Plugin Catalog；
- 支持配置启用、禁用源码插件，但仍随宿主部署；
- 统一插件配置、健康检查、迁移和可观测性。

### 阶段三：WASM 动态插件 MVP

- 定义 `plugin.yaml` 和 ABI v1；
- 实现固定动态 HTTP 路由；
- 首批只开放 `log`、`now`、`uuid`、受限配置读取；
- 实现超时、内存、输入输出和并发限制；
- 实现 Artifact 校验、安装、启停和回滚。

### 阶段四：完整插件治理

- 增加数据库迁移、i18n、静态资源；
- 增加 Hook、Job 和 Webhook；
- 增加资源级授权和审批；
- 增加多租户及多节点状态同步；
- 最后建设插件管理 UI、SDK 和市场能力。

## 启动动态插件建设的触发条件

满足以下两到三项后，再正式投入动态插件平台：

- 已存在至少三个支付、物流或 AI Provider；
- 不同客户需要不同模块组合；
- 集成模块发布频率明显高于主系统；
- 客户要求运行时自行安装、启停或升级扩展；
- 出现第三方插件开发者；
- 需要通过二进制形式交付商业插件；
- 有明确的插件市场或商业授权计划。

## 设计原则

1. 核心领域保持明确所有权，插件不能绕过领域服务直接修改核心表。
2. 宿主负责认证、租户、权限、审计、幂等和资源授权。
3. 接口应从多个真实实现中提炼，不为尚未出现的需求提前抽象。
4. 源码插件用于核心能力，WASM 用于需要隔离和热加载的扩展。
5. AI 默认调用受控工具，不在 Backend API 主进程内执行模型生成的任意代码。
6. 动态插件必须可限制、可观测、可禁用、可回滚。

